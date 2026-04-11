// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package gitrepo provides programmatic access to git objects (commits,
// trees, blobs) via go-git. It is the lowest-level foundation for sawmill's
// semantic git indexing: everything else in 🎯T6 depends on being able to
// read git objects without shelling out to the git CLI.
//
// A Repo wraps a go-git repository and exposes a small, focused API tailored
// to sawmill's needs:
//
//   - Walk commits from any ref (branch, tag, SHA) via WalkCommits.
//   - Resolve a commit to its file tree via FilesAtCommit.
//   - Read blob content by SHA via ReadBlob.
//   - Resolve refs (branch/tag/HEAD/short SHA) via Resolve.
//
// The API is deliberately narrower than go-git's full surface to keep
// callers decoupled from go-git internals.
package gitrepo

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Repo wraps a go-git repository with a sawmill-specific API.
type Repo struct {
	r *git.Repository
}

// Open opens the git repository at path. The path must contain a .git
// directory (or be inside a working tree — go-git discovers upward).
func Open(path string) (*Repo, error) {
	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, fmt.Errorf("opening git repo at %s: %w", path, err)
	}
	return &Repo{r: r}, nil
}

// NewFromRepository wraps an existing go-git repository. Test-only — use
// Open in production code.
func NewFromRepository(r *git.Repository) *Repo {
	return &Repo{r: r}
}

// Commit describes a single git commit in sawmill-friendly form.
type Commit struct {
	SHA        string
	Author     string
	Email      string
	When       time.Time
	Message    string
	ParentSHAs []string
	TreeSHA    string
}

func commitFromObject(c *object.Commit) *Commit {
	parents := make([]string, len(c.ParentHashes))
	for i, p := range c.ParentHashes {
		parents[i] = p.String()
	}
	return &Commit{
		SHA:        c.Hash.String(),
		Author:     c.Author.Name,
		Email:      c.Author.Email,
		When:       c.Author.When,
		Message:    c.Message,
		ParentSHAs: parents,
		TreeSHA:    c.TreeHash.String(),
	}
}

// Resolve resolves a ref (branch name, tag name, HEAD, full or short SHA) to
// its commit SHA.
func (r *Repo) Resolve(ref string) (string, error) {
	hash, err := r.r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return "", fmt.Errorf("resolving ref %q: %w", ref, err)
	}
	return hash.String(), nil
}

// Head returns the commit SHA that HEAD currently points to.
func (r *Repo) Head() (string, error) {
	ref, err := r.r.Head()
	if err != nil {
		return "", fmt.Errorf("reading HEAD: %w", err)
	}
	return ref.Hash().String(), nil
}

// GetCommit retrieves a single commit by SHA.
func (r *Repo) GetCommit(sha string) (*Commit, error) {
	hash := plumbing.NewHash(sha)
	c, err := r.r.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("reading commit %s: %w", sha, err)
	}
	return commitFromObject(c), nil
}

// WalkCommits walks commits from start backwards along the first-parent
// chain, newest first. The callback is invoked for each commit; returning
// io.EOF stops the walk cleanly, any other error stops it and is returned.
//
// First-parent walking produces a linear view of the branch history, which
// is what most semantic-diff queries want. Merge commits are reported but
// their second-parent ancestors are not walked.
func (r *Repo) WalkCommits(startSHA string, fn func(*Commit) error) error {
	hash := plumbing.NewHash(startSHA)
	for {
		c, err := r.r.CommitObject(hash)
		if err != nil {
			return fmt.Errorf("reading commit %s: %w", hash, err)
		}
		if err := fn(commitFromObject(c)); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(c.ParentHashes) == 0 {
			return nil
		}
		hash = c.ParentHashes[0]
	}
}

// FileEntry describes one file in a commit's tree.
type FileEntry struct {
	Path    string
	BlobSHA string
	Mode    uint32
}

// FilesAtCommit walks the commit's tree and returns every regular file with
// its blob SHA. Directories and symlinks are skipped.
//
// Uses the low-level TreeWalker to read entries directly without
// hydrating blob objects — critical for performance when indexing thousands
// of commits, most of which reuse blobs via 🎯T10's dedup.
func (r *Repo) FilesAtCommit(commitSHA string) ([]FileEntry, error) {
	hash := plumbing.NewHash(commitSHA)
	c, err := r.r.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("reading commit %s: %w", commitSHA, err)
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("reading tree for commit %s: %w", commitSHA, err)
	}

	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()

	files := make([]FileEntry, 0, 256)
	for {
		name, entry, err := walker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("walking tree for commit %s: %w", commitSHA, err)
		}
		if entry.Mode != filemode.Regular && entry.Mode != filemode.Executable {
			continue
		}
		files = append(files, FileEntry{
			Path:    name,
			BlobSHA: entry.Hash.String(),
			Mode:    uint32(entry.Mode),
		})
	}
	return files, nil
}

// ReadBlob returns the raw bytes of the blob with the given SHA.
func (r *Repo) ReadBlob(sha string) ([]byte, error) {
	hash := plumbing.NewHash(sha)
	blob, err := r.r.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", sha, err)
	}
	rd, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("opening blob %s: %w", sha, err)
	}
	defer rd.Close()

	buf := bytes.NewBuffer(make([]byte, 0, blob.Size))
	if _, err := io.Copy(buf, rd); err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", sha, err)
	}
	return buf.Bytes(), nil
}
