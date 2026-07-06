// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/marcelocantos/sawmill/forest"
)

// TestUndoWorksAfterPartialApplyRenameFailure is the regression for F7 (T47):
// when a pending apply has both content changes and a file rename, and the
// rename loop fails AFTER the content changes were written to disk, the session
// must still retain the backup manifest so `undo` can revert the content
// changes. The old code assigned h.lastBackups only after the rename loop, so a
// rename failure left the written files unrecoverable ("No backups available").
func TestUndoWorksAfterPartialApplyRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}

	h, dir := testHandlerWithDir(t, map[string]string{
		"a.txt": "orig-a\n",
		"b.txt": "orig-b\n",
	})

	apath := filepath.Join(dir, "a.txt")
	origA, err := os.ReadFile(apath)
	if err != nil {
		t.Fatal(err)
	}

	// A read-only destination directory so the rename fails mid-apply.
	roDir := filepath.Join(dir, "ro")
	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stage a content change to a.txt PLUS a rename that will fail.
	h.pending = &PendingChanges{
		Changes: []forest.FileChange{
			{Path: apath, Original: origA, NewSource: []byte("new-a\n")},
		},
		Renames: []FileRename{
			{From: filepath.Join(dir, "b.txt"), To: filepath.Join(roDir, "b.txt")},
		},
	}

	if err := os.Chmod(roDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	text, isErr, err := h.handleApply(map[string]any{"confirm": true})
	if err != nil {
		t.Fatalf("handleApply: %v", err)
	}
	if !isErr {
		t.Fatalf("expected the rename to fail the apply, got: %s", text)
	}

	// The content change was written to disk before the rename failed.
	if b, _ := os.ReadFile(apath); string(b) != "new-a\n" {
		t.Fatalf("content change not applied before rename failure: %q", b)
	}

	// undo must recover it.
	undoText, undoErr, err := h.handleUndo(nil)
	if err != nil {
		t.Fatalf("handleUndo: %v", err)
	}
	if undoErr {
		t.Fatalf("undo returned an error: %s", undoText)
	}
	if b, _ := os.ReadFile(apath); string(b) != "orig-a\n" {
		t.Fatalf("undo failed to restore a.txt after partial apply: %q", b)
	}
}
