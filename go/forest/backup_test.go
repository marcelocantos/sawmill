// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestUndoRestoresOriginallyEmptyFile is the regression for the Fable-5 F1
// (T41) data-loss finding: undoing a change to a file that was originally
// EMPTY must restore the empty file, not delete it. The old backup format
// overloaded 0-byte backup content as the "file was newly created" sentinel,
// so an originally-empty file was indistinguishable from a new file and undo
// os.Remove'd it.
func TestUndoRestoresOriginallyEmptyFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "empty.txt")
	if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	changes := []FileChange{{Path: p, Original: []byte{}, NewSource: []byte("content")}}
	manifest, err := ApplyWithBackup(root, changes)
	if err != nil {
		t.Fatalf("ApplyWithBackup: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "content" {
		t.Fatalf("apply did not write new content, got %q", b)
	}

	if _, err := UndoFromBackups(manifest); err != nil {
		t.Fatalf("UndoFromBackups: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("undo deleted the originally-empty file (want restored-empty): %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("want 0-byte restored file, got size %d", info.Size())
	}
}

// TestApplyAndUndoPreserveMode is the regression for F3 (T43): a non-0644 file
// (e.g. a 0755 script) must keep its permission bits across both apply and
// undo. The old code hardcoded 0o644 when writing the staging temp and when
// restoring on undo, silently stripping the execute bit.
func TestApplyAndUndoPreserveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	root := t.TempDir()
	p := filepath.Join(root, "script.sh")
	orig := []byte("#!/bin/sh\necho hi\n")
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	// Chmod explicitly (not via WriteFile's perm, which umask masks) so the
	// baseline is definitely 0o755 regardless of the test runner's umask.
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}

	changes := []FileChange{{Path: p, Original: orig, NewSource: []byte("#!/bin/sh\necho bye\n")}}
	manifest, err := ApplyWithBackup(root, changes)
	if err != nil {
		t.Fatalf("ApplyWithBackup: %v", err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o755 {
		t.Fatalf("apply dropped mode: got %o, want 0755", info.Mode().Perm())
	}

	if _, err := UndoFromBackups(manifest); err != nil {
		t.Fatalf("UndoFromBackups: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("undo dropped mode: got %o, want 0755", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != string(orig) {
		t.Fatalf("undo restored wrong content: %q", b)
	}
}

// TestApplyAbortsOnStaleContent is the regression for F4 (T44): if a file
// changed on disk between preview and apply, ApplyWithBackup must abort rather
// than clobber the newer content (optimistic-concurrency / lost-update guard).
func TestApplyAbortsOnStaleContent(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "f.txt")
	if err := os.WriteFile(p, []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The change was previewed against "v0".
	changes := []FileChange{{Path: p, Original: []byte("v0"), NewSource: []byte("derived-from-v0")}}
	// A concurrent/external edit lands before apply.
	if err := os.WriteFile(p, []byte("v1-external"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyWithBackup(root, changes)
	if !errors.Is(err, ErrStaleContent) {
		t.Fatalf("want ErrStaleContent, got %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "v1-external" {
		t.Fatalf("stale apply clobbered the external edit: %q", b)
	}
}

// TestApplyRollsBackOnPartialRenameFailure is the regression for F5 (T45): a
// multi-file apply must be all-or-nothing. If a later file's rename fails, an
// earlier file that was already renamed must be rolled back to its original
// content, not left half-applied.
func TestApplyRollsBackOnPartialRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	subdir := filepath.Join(root, "sub")
	b := filepath.Join(subdir, "b.txt")
	c := filepath.Join(root, "c.txt")
	if err := os.WriteFile(a, []byte("a-orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b-orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("c-orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make sub/ read-only so renaming a staging temp onto b fails mid-loop.
	if err := os.Chmod(subdir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o755) })

	changes := []FileChange{
		{Path: a, Original: []byte("a-orig"), NewSource: []byte("a-new")},
		{Path: b, Original: []byte("b-orig"), NewSource: []byte("b-new")},
		{Path: c, Original: []byte("c-orig"), NewSource: []byte("c-new")},
	}
	if _, err := ApplyWithBackup(root, changes); err == nil {
		t.Fatal("expected a rename failure, got nil error")
	}

	// a.txt was renamed first; rollback must have restored it.
	if got, _ := os.ReadFile(a); string(got) != "a-orig" {
		t.Fatalf("A left half-applied (no rollback): got %q, want a-orig", got)
	}
	// c.txt was never reached.
	if got, _ := os.ReadFile(c); string(got) != "c-orig" {
		t.Fatalf("C unexpectedly modified: got %q, want c-orig", got)
	}
}
