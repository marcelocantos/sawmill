// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package gitindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
)

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
func TestIndexRange(t *testing.T) {
	root := repoRoot(t)
	ix := openIndexer(t, root)

	const limit = 3
	n, err := ix.IndexRange("HEAD", limit)
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
	root := repoRoot(t)
	ix := openIndexer(t, root)

	const limit = 2
	if _, err := ix.IndexRange("HEAD", limit); err != nil {
		t.Fatalf("IndexRange (first): %v", err)
	}
	if _, err := ix.IndexRange("HEAD", limit); err != nil {
		t.Fatalf("IndexRange (second): %v", err)
	}
}
