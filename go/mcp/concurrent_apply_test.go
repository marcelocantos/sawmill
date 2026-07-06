// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/model"
)

// TestConcurrentSameFileApplyStaysConsistent is the regression for F9 (T49):
// two sessions sharing one pooled CodebaseModel apply a change to the SAME file
// concurrently. With deterministic per-(root,file) backup/staging paths and no
// cross-session lock, the two applies interleaved — corrupting each other's
// backups and leaving undo unable to recover the true original. With the
// per-apply unique directory plus the model-wide apply lock, the two applies
// are serialised: exactly one lands, the other is cleanly rejected because the
// file no longer matches what it previewed, and the winner's undo restores the
// original.
func TestConcurrentSameFileApplyStaysConsistent(t *testing.T) {
	dir := t.TempDir()
	xpath := filepath.Join(dir, "x.txt")
	const original = "v0-original\n"
	if err := os.WriteFile(xpath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// One shared model, two handlers — mirrors two MCP sessions on one root.
	m, err := model.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	hA := NewHandlerWithModel(m, nil)
	hB := NewHandlerWithModel(m, nil)

	hA.pending = &PendingChanges{Changes: []forest.FileChange{
		{Path: xpath, Original: []byte(original), NewSource: []byte("A-content\n")},
	}}
	hB.pending = &PendingChanges{Changes: []forest.FileChange{
		{Path: xpath, Original: []byte(original), NewSource: []byte("B-content\n")},
	}}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := map[string]bool{} // handler -> apply error?
	run := func(name string, h *Handler) {
		defer wg.Done()
		_, isErr, err := h.handleApply(map[string]any{"confirm": true})
		if err != nil {
			t.Errorf("%s handleApply transport error: %v", name, err)
		}
		mu.Lock()
		results[name] = isErr
		mu.Unlock()
	}
	wg.Add(2)
	go run("A", hA)
	go run("B", hB)
	wg.Wait()

	// Exactly one apply must succeed; the other must be rejected (its preview
	// no longer matches disk) rather than silently clobbering the winner.
	failures := 0
	for _, isErr := range results {
		if isErr {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("want exactly one rejected apply, got results=%v", results)
	}

	// The file must equal exactly one applied version — never a corrupted mix.
	got, _ := os.ReadFile(xpath)
	if s := string(got); s != "A-content\n" && s != "B-content\n" {
		t.Fatalf("file left in an inconsistent state: %q", s)
	}

	// The winner's undo must restore the true original.
	winner := hA
	if results["A"] {
		winner = hB // A was the one rejected
	}
	undoText, undoErr, err := winner.handleUndo(nil)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undoErr {
		t.Fatalf("winner undo errored: %s", undoText)
	}
	if b, _ := os.ReadFile(xpath); string(b) != original {
		t.Fatalf("undo did not restore the original: %q", b)
	}
}
