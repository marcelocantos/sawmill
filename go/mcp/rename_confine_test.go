// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// parseHandlerRooted builds a Handler bound to an explicit root directory
// (so tests can place files OUTSIDE the root to exercise containment).
func parseHandlerRooted(t *testing.T, root string) *Handler {
	t.Helper()
	h := NewHandler()
	text, isErr, err := h.handleParse(map[string]any{"path": root})
	if err != nil {
		t.Fatalf("handleParse: %v", err)
	}
	if isErr {
		t.Fatalf("handleParse returned error: %s", text)
	}
	return h
}

// TestRenameFileRejectsTraversalDestination is the regression for F2 (T42):
// rename_file must confine its destination to the project root. A `to` with
// ".." escapes the root and — because renames run outside the backup machinery
// — overwrites an arbitrary external file with no way to undo it.
func TestRenameFileRejectsTraversalDestination(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A victim file OUTSIDE the root, reachable via "../".
	victim := filepath.Join(base, "VICTIM.txt")
	if err := os.WriteFile(victim, []byte("IMPORTANT ORIGINAL CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := parseHandlerRooted(t, root)
	text, isErr, err := h.handleRenameFile(map[string]any{"from": "a.go", "to": "../VICTIM.txt"})
	if err != nil {
		t.Fatalf("handleRenameFile: %v", err)
	}
	if !isErr {
		t.Fatalf("expected traversal destination to be rejected, got: %s", text)
	}
	// No rename should have been staged.
	if h.pending != nil && len(h.pending.Renames) != 0 {
		t.Fatalf("a rename was staged despite rejection: %+v", h.pending.Renames)
	}
	if b, _ := os.ReadFile(victim); string(b) != "IMPORTANT ORIGINAL CONTENT" {
		t.Fatalf("external victim file was clobbered: %q", b)
	}
}

// TestRenameFileRejectsExistingDestination covers the in-root half of F2: a
// `to` that names an existing file would silently clobber it (the rename is a
// bare os.Rename with no backup), so it must be refused.
func TestRenameFileRejectsExistingDestination(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := parseHandlerRooted(t, root)
	text, isErr, err := h.handleRenameFile(map[string]any{"from": "a.go", "to": "b.go"})
	if err != nil {
		t.Fatalf("handleRenameFile: %v", err)
	}
	if !isErr {
		t.Fatalf("expected existing destination to be rejected, got: %s", text)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "b.go")); string(b) != "package b\n" {
		t.Fatalf("existing destination was clobbered: %q", b)
	}
}
