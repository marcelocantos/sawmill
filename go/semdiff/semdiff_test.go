// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package semdiff_test

import (
	"testing"
	"time"

	billy "github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
	"github.com/marcelocantos/sawmill/semdiff"
)

type testRepo struct {
	repo  *gitrepo.Repo
	store *gitindex.Store
	ix    *gitindex.Indexer
	wt    *git.Worktree
	fs    billy.Filesystem
	sig   *object.Signature
}

func newTestRepo(t *testing.T) *testRepo {
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
	repo := gitrepo.NewFromRepository(r)
	store, err := gitindex.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ix := gitindex.NewIndexer(store, repo)

	return &testRepo{
		repo:  repo,
		store: store,
		ix:    ix,
		wt:    wt,
		fs:    fs,
		sig: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
}

func (tr *testRepo) write(t *testing.T, path, content string) {
	t.Helper()
	f, err := tr.fs.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	f.Close()
}

func (tr *testRepo) add(t *testing.T, path string) {
	t.Helper()
	if _, err := tr.wt.Add(path); err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
}

func (tr *testRepo) remove(t *testing.T, path string) {
	t.Helper()
	if _, err := tr.wt.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
}

func (tr *testRepo) commit(t *testing.T, msg string) string {
	t.Helper()
	tr.sig.When = tr.sig.When.Add(time.Hour)
	h, err := tr.wt.Commit(msg, &git.CommitOptions{Author: tr.sig})
	if err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	sha := h.String()
	if err := tr.ix.EnsureCommitIndexed(sha); err != nil {
		t.Fatalf("index commit %s: %v", sha, err)
	}
	return sha
}

func findChange(changes []semdiff.SymbolChange, name string, op semdiff.EditOp) *semdiff.SymbolChange {
	for i, c := range changes {
		if c.Name == name && c.Op == op {
			return &changes[i]
		}
	}
	return nil
}

func findFileDiff(files []semdiff.FileDiff, path string) *semdiff.FileDiff {
	for i, f := range files {
		if f.Path == path {
			return &files[i]
		}
	}
	return nil
}

// TestDiffAddRemoveModify tests basic symbol-level adds, removes, and modifications.
func TestDiffAddRemoveModify(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "main.go", `package main

func Foo() int { return 1 }

func Bar() string { return "hello" }
`)
	tr.add(t, "main.go")
	commitA := tr.commit(t, "add functions")

	tr.write(t, "main.go", `package main

func Foo() int { return 2 }

func Baz() bool { return true }
`)
	tr.add(t, "main.go")
	commitB := tr.commit(t, "modify Foo, remove Bar, add Baz")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	fd := findFileDiff(result.Files, "main.go")
	if fd == nil {
		t.Fatal("expected main.go in diff")
	}
	if fd.Status != "modified" {
		t.Errorf("status = %q, want %q", fd.Status, "modified")
	}

	if c := findChange(fd.Symbols, "Foo", semdiff.OpModify); c == nil {
		t.Error("expected Foo to be modified")
	}
	if c := findChange(fd.Symbols, "Bar", semdiff.OpRemove); c == nil {
		t.Error("expected Bar to be removed")
	}
	if c := findChange(fd.Symbols, "Baz", semdiff.OpAdd); c == nil {
		t.Error("expected Baz to be added")
	}
}

// TestDiffRename tests detection of a renamed symbol within the same file.
func TestDiffRename(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "lib.go", `package lib

func OldName(x int, y int) int {
	return x + y
}
`)
	tr.add(t, "lib.go")
	commitA := tr.commit(t, "add OldName")

	tr.write(t, "lib.go", `package lib

func NewName(x int, y int) int {
	return x + y
}
`)
	tr.add(t, "lib.go")
	commitB := tr.commit(t, "rename to NewName")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	fd := findFileDiff(result.Files, "lib.go")
	if fd == nil {
		t.Fatal("expected lib.go in diff")
	}

	if c := findChange(fd.Symbols, "OldName", semdiff.OpRename); c == nil {
		t.Error("expected OldName to be detected as renamed")
	} else if c.NewName != "NewName" {
		t.Errorf("rename new_name = %q, want %q", c.NewName, "NewName")
	}
}

// TestDiffCrossFileMove tests detection of a symbol moved to another file.
func TestDiffCrossFileMove(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "a.go", `package pkg

func Helper() int { return 42 }
`)
	tr.write(t, "b.go", `package pkg

func Other() bool { return true }
`)
	tr.add(t, "a.go")
	tr.add(t, "b.go")
	commitA := tr.commit(t, "initial")

	// Move Helper from a.go to b.go.
	tr.write(t, "a.go", `package pkg
`)
	tr.write(t, "b.go", `package pkg

func Other() bool { return true }

func Helper() int { return 42 }
`)
	tr.add(t, "a.go")
	tr.add(t, "b.go")
	commitB := tr.commit(t, "move Helper to b.go")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	// Look for a move operation on Helper.
	found := false
	for _, f := range result.Files {
		for _, s := range f.Symbols {
			if s.Name == "Helper" && s.Op == semdiff.OpMove {
				found = true
				if s.OldPath != "a.go" {
					t.Errorf("move old_path = %q, want %q", s.OldPath, "a.go")
				}
				if s.NewPath != "b.go" {
					t.Errorf("move new_path = %q, want %q", s.NewPath, "b.go")
				}
			}
		}
	}
	if !found {
		t.Error("expected Helper to be detected as moved from a.go to b.go")
	}
}

// TestDiffSignatureChange tests detection of parameter and return type changes.
func TestDiffSignatureChange(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "api.go", `package api

func Process(name string) error {
	return nil
}
`)
	tr.add(t, "api.go")
	commitA := tr.commit(t, "initial Process")

	tr.write(t, "api.go", `package api

func Process(name string, count int) (error, bool) {
	return nil, true
}
`)
	tr.add(t, "api.go")
	commitB := tr.commit(t, "add count param, change return")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	fd := findFileDiff(result.Files, "api.go")
	if fd == nil {
		t.Fatal("expected api.go in diff")
	}

	c := findChange(fd.Symbols, "Process", semdiff.OpModify)
	if c == nil {
		t.Fatal("expected Process to be modified")
	}
	if c.Signature == nil {
		t.Fatal("expected signature change details")
	}
	if !c.Signature.ReturnChanged {
		t.Error("expected return_changed to be true")
	}
}

// TestDiffFileMove tests detection of a file moved to a different path.
func TestDiffFileMove(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "old.go", `package pkg

func Greet() string { return "hi" }
`)
	tr.add(t, "old.go")
	commitA := tr.commit(t, "add old.go")

	// Move the file (same content, different path).
	tr.remove(t, "old.go")
	tr.write(t, "new.go", `package pkg

func Greet() string { return "hi" }
`)
	tr.add(t, "new.go")
	commitB := tr.commit(t, "move old.go to new.go")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	fd := findFileDiff(result.Files, "new.go")
	if fd == nil {
		t.Fatal("expected new.go in diff")
	}
	if fd.Status != "moved" {
		t.Errorf("status = %q, want %q", fd.Status, "moved")
	}
	if fd.OldPath != "old.go" {
		t.Errorf("old_path = %q, want %q", fd.OldPath, "old.go")
	}
}

// TestDiffJSONKeyLevel tests key-level diffing for JSON files.
func TestDiffJSONKeyLevel(t *testing.T) {
	tr := newTestRepo(t)

	tr.write(t, "config.json", `{"name": "sawmill", "version": "1.0", "debug": true}`)
	tr.add(t, "config.json")
	commitA := tr.commit(t, "add config")

	tr.write(t, "config.json", `{"name": "sawmill", "version": "2.0", "verbose": false}`)
	tr.add(t, "config.json")
	commitB := tr.commit(t, "update config")

	result, err := semdiff.Diff(tr.store, tr.repo, commitA, commitB)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	fd := findFileDiff(result.Files, "config.json")
	if fd == nil {
		t.Fatal("expected config.json in diff")
	}

	// "version" should be modified, "debug" removed, "verbose" added.
	if c := findChange(fd.Symbols, "version", semdiff.OpModify); c == nil {
		t.Error("expected 'version' key to be modified")
	}
	if c := findChange(fd.Symbols, "debug", semdiff.OpRemove); c == nil {
		t.Error("expected 'debug' key to be removed")
	}
	if c := findChange(fd.Symbols, "verbose", semdiff.OpAdd); c == nil {
		t.Error("expected 'verbose' key to be added")
	}
}

// TestChangelog verifies that the changelog formatter produces output.
func TestChangelog(t *testing.T) {
	result := &semdiff.DiffResult{
		Base: "abc1234567890",
		Head: "def7890123456",
		Files: []semdiff.FileDiff{
			{
				Path:   "api.go",
				Status: "modified",
				Symbols: []semdiff.SymbolChange{
					{Op: semdiff.OpAdd, Name: "NewFunc", Kind: "function"},
					{Op: semdiff.OpRemove, Name: "OldFunc", Kind: "function"},
					{Op: semdiff.OpModify, Name: "Changed", Kind: "function", Signature: &semdiff.SignatureChange{
						ParamsAdded:   []string{"ctx context.Context"},
						ReturnChanged: true,
					}},
					{Op: semdiff.OpRename, Name: "Before", NewName: "After", Kind: "type"},
				},
			},
		},
	}

	text := semdiff.Changelog(result)
	if text == "" {
		t.Fatal("expected non-empty changelog")
	}
	for _, want := range []string{"## Added", "## Removed", "## Modified", "## Renamed", "NewFunc", "OldFunc", "Changed", "Before", "After"} {
		if !containsString(text, want) {
			t.Errorf("changelog missing %q", want)
		}
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
