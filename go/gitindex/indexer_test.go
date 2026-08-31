// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package gitindex_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
)

// buildIndexRangeFixture creates an in-memory git repo with the given
// number of tiny commits (each writes a unique go file). Returns the
// indexer and the HEAD commit SHA.
func buildIndexRangeFixture(t *testing.T, commits int) (*gitindex.Indexer, string) {
	t.Helper()

	fs := memfs.New()
	storer := memory.NewStorage()
	r, err := git.Init(storer, fs)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	sig := &object.Signature{Name: "T", Email: "t@example.com", When: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	var head string
	for i := 0; i < commits; i++ {
		path := fmt.Sprintf("f%d.go", i)
		content := fmt.Sprintf("package x\n\nvar V%d = %d\n", i, i)
		f, err := fs.Create(path)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write: %v", err)
		}
		f.Close()
		if _, err := wt.Add(path); err != nil {
			t.Fatalf("add: %v", err)
		}
		sig.When = sig.When.Add(time.Hour)
		h, err := wt.Commit(fmt.Sprintf("commit %d", i), &git.CommitOptions{Author: sig})
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		head = h.String()
	}

	repo := gitrepo.NewFromRepository(r)
	store, err := gitindex.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return gitindex.NewIndexer(store, repo), head
}

// repoRoot returns the sawmill repo root for integration tests, or calls
// t.Skip if we are not inside a git repository.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "../..")
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skip("not in a git repo")
	}
	return root
}

// openIndexer creates an in-memory store and an Indexer backed by the repo at
// root. The store is closed via t.Cleanup.
func openIndexer(t *testing.T, root string) *gitindex.Indexer {
	t.Helper()
	s, err := gitindex.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatalf("gitrepo.Open: %v", err)
	}

	return gitindex.NewIndexer(s, repo)
}

// TestIndexHead indexes HEAD of the sawmill repo and verifies basic invariants.
func TestIndexHead(t *testing.T) {
	root := repoRoot(t)
	ix := openIndexer(t, root)

	if err := ix.IndexHead(); err != nil {
		t.Fatalf("IndexHead: %v", err)
	}

	// Resolve HEAD to get its SHA.
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatalf("gitrepo.Open: %v", err)
	}
	headSHA, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}

	// Commit should be marked as indexed.
	indexed, err := ix.Store().IsCommitIndexed(headSHA)
	if err != nil {
		t.Fatalf("IsCommitIndexed: %v", err)
	}
	if !indexed {
		t.Fatal("expected HEAD commit to be marked indexed")
	}

	// There should be commit_files registered.
	files, err := ix.Store().CommitFiles(headSHA)
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one commit file for HEAD")
	}
}

// TestIndexHeadIdempotent verifies that indexing HEAD twice is a no-op.
func TestIndexHeadIdempotent(t *testing.T) {
	root := repoRoot(t)
	ix := openIndexer(t, root)

	if err := ix.IndexHead(); err != nil {
		t.Fatalf("IndexHead (first): %v", err)
	}
	// Second call must not return an error.
	if err := ix.IndexHead(); err != nil {
		t.Fatalf("IndexHead (second): %v", err)
	}
}

// TestIndexRange verifies that IndexRange stops after the requested limit.
// Uses an in-memory 5-commit fixture so the test is bounded and fast — the
// earlier incarnation walked the real sawmill repo and took minutes under
// -race in CI.
func TestIndexRange(t *testing.T) {
	ix, head := buildIndexRangeFixture(t, 5)

	const limit = 3
	n, err := ix.IndexRange(head, limit)
	if err != nil {
		t.Fatalf("IndexRange: %v", err)
	}
	if n != limit {
		t.Fatalf("expected %d indexed commits, got %d", limit, n)
	}
}

// TestIndexRangeIdempotent verifies that running IndexRange twice with the
// same limit succeeds (idempotent).
func TestIndexRangeIdempotent(t *testing.T) {
	ix, head := buildIndexRangeFixture(t, 4)

	const limit = 2
	if _, err := ix.IndexRange(head, limit); err != nil {
		t.Fatalf("IndexRange (first): %v", err)
	}
	if _, err := ix.IndexRange(head, limit); err != nil {
		t.Fatalf("IndexRange (second): %v", err)
	}
}
