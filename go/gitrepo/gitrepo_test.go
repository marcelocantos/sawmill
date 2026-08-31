// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package gitrepo_test

import (
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/marcelocantos/sawmill/gitrepo"
)

// fixture builds a small in-memory repo with a known two-commit history.
//
//	commit A: adds foo.go ("package foo\n") and bar.go ("package bar\n")
//	commit B: modifies foo.go ("package foo\n\nvar X = 1\n"), leaves bar.go
//
// bar.go is unchanged between commits, so its blob SHA is identical in A
// and B — this is the property blob-SHA dedup relies on.
func fixture(t *testing.T) (*gitrepo.Repo, string, string) {
	t.Helper()

	fs := memfs.New()
	storer := memory.NewStorage()
	r, err := git.Init(storer, fs)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	write := func(path, content string) {
		f, err := fs.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		f.Close()
	}

	sig := &object.Signature{
		Name:  "Test",
		Email: "test@example.com",
		When:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	// Commit A: two files.
	write("foo.go", "package foo\n")
	write("bar.go", "package bar\n")
	if _, err := wt.Add("foo.go"); err != nil {
		t.Fatalf("add foo.go: %v", err)
	}
	if _, err := wt.Add("bar.go"); err != nil {
		t.Fatalf("add bar.go: %v", err)
	}
	commitA, err := wt.Commit("add foo and bar", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatalf("commit A: %v", err)
	}

	// Commit B: modify foo.go only.
	write("foo.go", "package foo\n\nvar X = 1\n")
	if _, err := wt.Add("foo.go"); err != nil {
		t.Fatalf("add foo.go v2: %v", err)
	}
	sigB := *sig
	sigB.When = sig.When.Add(time.Hour)
	commitB, err := wt.Commit("bump foo", &git.CommitOptions{Author: &sigB})
	if err != nil {
		t.Fatalf("commit B: %v", err)
	}

	return gitrepo.NewFromRepository(r), commitA.String(), commitB.String()
}

func TestHeadAndResolve(t *testing.T) {
	repo, _, commitB := fixture(t)

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != commitB {
		t.Errorf("Head = %s, want %s", head, commitB)
	}

	// Resolve HEAD.
	resolved, err := repo.Resolve("HEAD")
	if err != nil {
		t.Fatalf("Resolve HEAD: %v", err)
	}
	if resolved != commitB {
		t.Errorf("Resolve(HEAD) = %s, want %s", resolved, commitB)
	}
}

func TestGetCommit(t *testing.T) {
	repo, commitA, commitB := fixture(t)

	a, err := repo.GetCommit(commitA)
	if err != nil {
		t.Fatalf("GetCommit A: %v", err)
	}
	if a.SHA != commitA {
		t.Errorf("A.SHA = %s, want %s", a.SHA, commitA)
	}
	if a.Author != "Test" || a.Email != "test@example.com" {
		t.Errorf("A author mismatch: %+v", a)
	}
	if len(a.ParentSHAs) != 0 {
		t.Errorf("A should have 0 parents, got %d", len(a.ParentSHAs))
	}
	if a.TreeSHA == "" {
		t.Error("A.TreeSHA empty")
	}

	b, err := repo.GetCommit(commitB)
	if err != nil {
		t.Fatalf("GetCommit B: %v", err)
	}
	if len(b.ParentSHAs) != 1 || b.ParentSHAs[0] != commitA {
		t.Errorf("B parents = %v, want [%s]", b.ParentSHAs, commitA)
	}
}

func TestWalkCommits(t *testing.T) {
	repo, commitA, commitB := fixture(t)

	var seen []string
	err := repo.WalkCommits(commitB, func(c *gitrepo.Commit) error {
		seen = append(seen, c.SHA)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCommits: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("walked %d commits, want 2: %v", len(seen), seen)
	}
	if seen[0] != commitB {
		t.Errorf("first walked = %s, want %s", seen[0], commitB)
	}
	if seen[1] != commitA {
		t.Errorf("second walked = %s, want %s", seen[1], commitA)
	}
}

func TestFilesAtCommit(t *testing.T) {
	repo, commitA, commitB := fixture(t)

	filesA, err := repo.FilesAtCommit(commitA)
	if err != nil {
		t.Fatalf("FilesAtCommit A: %v", err)
	}
	if len(filesA) != 2 {
		t.Fatalf("commit A has %d files, want 2: %+v", len(filesA), filesA)
	}

	filesB, err := repo.FilesAtCommit(commitB)
	if err != nil {
		t.Fatalf("FilesAtCommit B: %v", err)
	}
	if len(filesB) != 2 {
		t.Fatalf("commit B has %d files, want 2: %+v", len(filesB), filesB)
	}

	// Blob-SHA dedup: bar.go is unchanged between A and B, so its blob SHA
	// must be identical.
	barA := findFile(filesA, "bar.go")
	barB := findFile(filesB, "bar.go")
	if barA == nil || barB == nil {
		t.Fatalf("bar.go missing from one or both commits")
	}
	if barA.BlobSHA != barB.BlobSHA {
		t.Errorf("bar.go blob SHA changed across unchanged commits: %s vs %s", barA.BlobSHA, barB.BlobSHA)
	}

	// foo.go did change, so its blob SHA must differ.
	fooA := findFile(filesA, "foo.go")
	fooB := findFile(filesB, "foo.go")
	if fooA == nil || fooB == nil {
		t.Fatalf("foo.go missing from one or both commits")
	}
	if fooA.BlobSHA == fooB.BlobSHA {
		t.Errorf("foo.go blob SHA unchanged across modification: %s", fooA.BlobSHA)
	}
}

func TestReadBlob(t *testing.T) {
	repo, commitA, commitB := fixture(t)

	filesA, err := repo.FilesAtCommit(commitA)
	if err != nil {
		t.Fatalf("FilesAtCommit A: %v", err)
	}
	filesB, err := repo.FilesAtCommit(commitB)
	if err != nil {
		t.Fatalf("FilesAtCommit B: %v", err)
	}

	fooA := findFile(filesA, "foo.go")
	fooB := findFile(filesB, "foo.go")

	contentA, err := repo.ReadBlob(fooA.BlobSHA)
	if err != nil {
		t.Fatalf("ReadBlob foo A: %v", err)
	}
	if string(contentA) != "package foo\n" {
		t.Errorf("foo A content = %q, want %q", contentA, "package foo\n")
	}

	contentB, err := repo.ReadBlob(fooB.BlobSHA)
	if err != nil {
		t.Fatalf("ReadBlob foo B: %v", err)
	}
	if string(contentB) != "package foo\n\nvar X = 1\n" {
		t.Errorf("foo B content = %q, want %q", contentB, "package foo\n\nvar X = 1\n")
	}
}

func findFile(files []gitrepo.FileEntry, path string) *gitrepo.FileEntry {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}
