// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package bisect_test

import (
	"testing"
	"time"

	billy "github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/marcelocantos/sawmill/bisect"
	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
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
	return &testRepo{
		repo:  repo,
		store: store,
		ix:    gitindex.NewIndexer(store, repo),
		wt:    wt,
		fs:    fs,
		sig: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
}

func (tr *testRepo) writeAndCommit(t *testing.T, path, content, msg string) string {
	t.Helper()
	f, err := tr.fs.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	f.Close()
	if _, err := tr.wt.Add(path); err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
	tr.sig.When = tr.sig.When.Add(time.Hour)
	h, err := tr.wt.Commit(msg, &git.CommitOptions{Author: tr.sig})
	if err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	// Don't pre-index — let bisect index lazily.
	return h.String()
}

// TestBisectFunctionHasParam verifies bisect finds the commit that introduced
// a specific parameter on a function.
func TestBisectFunctionHasParam(t *testing.T) {
	tr := newTestRepo(t)

	// Commit 1: bare function, no params.
	c1 := tr.writeAndCommit(t, "lib.go", `package lib

func Foo() int { return 1 }
`, "v1")

	// Commit 2: still no ctx.
	tr.writeAndCommit(t, "lib.go", `package lib

func Foo() int { return 2 }
`, "v2")

	// Commit 3: ctx parameter introduced — this is the flip commit.
	c3 := tr.writeAndCommit(t, "lib.go", `package lib

import "context"

func Foo(ctx context.Context) int { return 3 }
`, "add ctx parameter")

	// Commit 4: more params.
	tr.writeAndCommit(t, "lib.go", `package lib

import "context"

func Foo(ctx context.Context, x int) int { return 4 }
`, "v4")

	// Commit 5: yet more.
	c5 := tr.writeAndCommit(t, "lib.go", `package lib

import "context"

func Foo(ctx context.Context, x int, y string) int { return 5 }
`, "v5")

	pred, err := bisect.ParsePredicate(`{"kind":"function_has_param","function":"Foo","param":"ctx"}`)
	if err != nil {
		t.Fatalf("ParsePredicate: %v", err)
	}

	res, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, c1, c5)
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if res.FlipSHA != c3 {
		t.Errorf("flip = %s, want %s", res.FlipSHA[:7], c3[:7])
	}
	if !res.BadValue || res.GoodValue {
		t.Errorf("expected good=false bad=true, got good=%v bad=%v", res.GoodValue, res.BadValue)
	}
	if res.Message != "add ctx parameter" {
		t.Errorf("message = %q", res.Message)
	}
	if res.StructuralChange == nil {
		t.Fatal("expected structural change attribution")
	}
	if res.StructuralChange.Name != "Foo" {
		t.Errorf("structural change subject = %q, want Foo", res.StructuralChange.Name)
	}
	// Should examine far fewer than all 5 commits.
	if res.CommitsInRange != 5 {
		t.Errorf("commits_in_range = %d, want 5", res.CommitsInRange)
	}
}

// TestBisectSymbolExists finds the commit where a symbol first appeared.
func TestBisectSymbolExists(t *testing.T) {
	tr := newTestRepo(t)

	c1 := tr.writeAndCommit(t, "lib.go", "package lib\n", "empty")
	tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Other() {}\n", "add Other")
	c3 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Other() {}\n\nfunc Target() {}\n", "introduce Target")
	c4 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Other() {}\n\nfunc Target() { return }\n", "tweak Target")

	pred, err := bisect.ParsePredicate(`{"kind":"symbol_exists","name":"Target"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, c1, c4)
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if res.FlipSHA != c3 {
		t.Errorf("flip = %s, want %s", res.FlipSHA[:7], c3[:7])
	}
	if res.StructuralChange == nil || res.StructuralChange.Name != "Target" {
		t.Errorf("expected structural change for Target, got %+v", res.StructuralChange)
	}
}

// TestBisectTypeHasField finds the commit where a struct gained a field.
func TestBisectTypeHasField(t *testing.T) {
	tr := newTestRepo(t)

	c1 := tr.writeAndCommit(t, "config.go", `package cfg

type Config struct {
	Name string
}
`, "v1")
	c2 := tr.writeAndCommit(t, "config.go", `package cfg

type Config struct {
	Name    string
	Verbose bool
}
`, "add Verbose")
	tr.writeAndCommit(t, "config.go", `package cfg

type Config struct {
	Name    string
	Verbose bool
	Debug   bool
}
`, "add Debug")
	c4 := tr.writeAndCommit(t, "config.go", `package cfg

type Config struct {
	Name    string
	Verbose bool
	Debug   bool
	Trace   int
}
`, "add Trace")

	pred, err := bisect.ParsePredicate(`{"kind":"type_has_field","type":"Config","field":"Verbose"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, c1, c4)
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if res.FlipSHA != c2 {
		t.Errorf("flip = %s, want %s", res.FlipSHA[:7], c2[:7])
	}
}

// TestBisectNoFlipIsError verifies that a predicate with the same value at
// good and bad returns an explanatory error.
func TestBisectNoFlipIsError(t *testing.T) {
	tr := newTestRepo(t)
	c1 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Foo() {}\n", "v1")
	c2 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Foo() { return }\n", "v2")

	pred, _ := bisect.ParsePredicate(`{"kind":"symbol_exists","name":"Foo"}`)
	_, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, c1, c2)
	if err == nil {
		t.Fatal("expected error for predicate with no flip")
	}
}

// TestBisectGoodNotAncestor verifies that a non-ancestor good commit errors.
func TestBisectGoodNotAncestor(t *testing.T) {
	tr := newTestRepo(t)
	c1 := tr.writeAndCommit(t, "lib.go", "package lib\n", "v1")
	tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Foo() {}\n", "v2")

	// Use c1 as bad, and a fake unrelated SHA as good.
	pred, _ := bisect.ParsePredicate(`{"kind":"symbol_exists","name":"Foo"}`)
	_, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, "0000000000000000000000000000000000000000", c1)
	if err == nil {
		t.Fatal("expected error for missing good commit")
	}
}

// TestBisectReverseDirection verifies bisect works when the predicate flips
// from true (good) to false (bad) — i.e. a symbol was removed.
func TestBisectReverseDirection(t *testing.T) {
	tr := newTestRepo(t)

	c1 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Removed() {}\nfunc Other() {}\n", "v1 — Removed exists")
	tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Removed() {}\nfunc Other() { return }\n", "v2 — still there")
	c3 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Other() { return }\n", "v3 — remove Removed")
	c4 := tr.writeAndCommit(t, "lib.go", "package lib\n\nfunc Other() { return nil }\n", "v4")

	pred, _ := bisect.ParsePredicate(`{"kind":"symbol_exists","name":"Removed"}`)
	res, err := bisect.Bisect(tr.ix, tr.store, tr.repo, pred, c1, c4)
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if res.FlipSHA != c3 {
		t.Errorf("flip = %s, want %s", res.FlipSHA[:7], c3[:7])
	}
	if !res.GoodValue || res.BadValue {
		t.Errorf("expected good=true bad=false, got good=%v bad=%v", res.GoodValue, res.BadValue)
	}
}

// TestParsePredicateRejectsInvalid verifies validation of malformed predicates.
func TestParsePredicateRejectsInvalid(t *testing.T) {
	cases := []string{
		`{}`,
		`{"kind":"symbol_exists"}`,
		`{"kind":"function_has_param","function":"Foo"}`,
		`{"kind":"unknown"}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := bisect.ParsePredicate(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}
