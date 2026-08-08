// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/model"
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// debounceSettle is comfortably longer than the watcher's debounce window, so
// that any event the apply provoked has been delivered and acted on before the
// assertions run.
//
// Outlasting the debounce is the whole point. Every existing test finished
// inside that window, which is precisely why an apply could delete its own
// files from the index for months without anything going red.
const debounceSettle = 750 * time.Millisecond

// TestAtomicReplaceKeepsFileIndexed pins the bug that ApplyWithBackup's
// temp-file-plus-rename provoked: the rename destroys the watched inode, the
// watcher reports a removal for a path that still exists, and taking that
// literally dropped the just-written file out of the store one debounce after
// every apply.
func TestAtomicReplaceKeepsFileIndexed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "widget.go")
	original := "package main\n\ntype Widget struct{ w int }\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := model.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()

	indexed := func(when string) bool {
		t.Helper()
		accessors, err := m.FileAccessors("")
		if err != nil {
			t.Fatalf("%s: FileAccessors: %v", when, err)
		}
		for _, acc := range accessors {
			if acc.Path() == path {
				return true
			}
		}
		return false
	}

	if !indexed("after load") {
		t.Fatal("file is not indexed after load; the test cannot detect a later removal")
	}

	updated := "package main\n\ntype Widget struct{ w, size int }\n"
	if _, err := forest.ApplyWithBackup(dir, []forest.FileChange{{
		Path:      path,
		Original:  []byte(original),
		NewSource: []byte(updated),
	}}); err != nil {
		t.Fatalf("ApplyWithBackup: %v", err)
	}
	m.ReindexNow([]string{path})

	if !indexed("immediately after apply") {
		t.Fatal("file left the index during apply")
	}

	time.Sleep(debounceSettle)

	if !indexed("after the debounce window") {
		t.Fatal("file was dropped from the index a debounce after apply: " +
			"the rename's removal event was believed even though the path still exists")
	}

	// The surviving row must also carry the applied content, not the old text
	// it was indexed with before the rename.
	accessors, err := m.FileAccessors("")
	if err != nil {
		t.Fatalf("FileAccessors: %v", err)
	}
	var got string
	for _, acc := range accessors {
		if acc.Path() != path {
			continue
		}
		if err := acc.WithTree(func(source []byte, _ *tree_sitter.Tree) error {
			got = string(source)
			return nil
		}); err != nil {
			t.Fatalf("WithTree: %v", err)
		}
	}
	if got != updated {
		t.Errorf("indexed source is stale after apply:\n got: %q\nwant: %q", got, updated)
	}
}

// TestRealRemovalStillDeindexes is the other half: the stat guard must not turn
// a genuine deletion into a file that lingers in the index forever.
func TestRealRemovalStillDeindexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.go")
	if err := os.WriteFile(path, []byte("package main\n\ntype Gone struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := model.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(debounceSettle)

	accessors, err := m.FileAccessors("")
	if err != nil {
		t.Fatalf("FileAccessors: %v", err)
	}
	for _, acc := range accessors {
		if acc.Path() == path {
			t.Fatal("deleted file is still indexed")
		}
	}
}
