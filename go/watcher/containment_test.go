// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package watcher_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/watcher"
)

// quietWindow is how long "nothing should happen" assertions wait. Comfortably
// longer than the 100ms debounce, short enough not to pad the suite.
const quietWindow = 1500 * time.Millisecond

// expectNoEvent fails if any event arrives within quietWindow.
func expectNoEvent(t *testing.T, ch <-chan watcher.FileEvent, why string) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			return // channel closed; nothing was delivered
		}
		t.Fatalf("%s: unexpected event for %s (%v)", why, ev.Path, ev.Kind)
	case <-time.After(quietWindow):
	}
}

// TestSymlinkPresentBeforeWatchDoesNotEscapeRoot covers the walk path: a
// directory symlink already in the tree when Watch starts must not pull its
// target into the watch set.
func TestSymlinkPresentBeforeWatchDoesNotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	w, events, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	// A change on the far side of the link must be invisible.
	if err := os.WriteFile(filepath.Join(outside, "sub", "escaped.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	expectNoEvent(t, events, "a directory symlink must not extend the watch set outside the root")
}

// TestSymlinkCreatedAfterWatchDoesNotEscapeRoot covers the event path. This is
// the one that bit in production: the Create handler used os.Stat, which
// follows symlinks, so a link created inside the tree looked like a new
// directory and its whole target was walked and watched.
func TestSymlinkCreatedAfterWatchDoesNotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	w, events, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "escaped.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	expectNoEvent(t, events, "a symlink created after Watch must not extend the watch set")
}

// TestAttributeChangeDoesNotReportModification pins the property that makes
// the daemon safe to run alongside ordinary tooling. On macOS the kqueue
// backend reports an attribute change — including the atime bump from a plain
// read — as Chmod. Treating that as a modification meant anything walking the
// tree scheduled a reparse of every file it touched.
func TestAttributeChangeDoesNotReportModification(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, events, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	// Touch attributes only — no content change.
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	expectNoEvent(t, events, "an attribute-only change must not be reported as a modification")

	// A real content change must still come through, or the guard has simply
	// broken change detection.
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := receiveEvent(t, events, func(ev watcher.FileEvent) bool {
		return filepath.Base(ev.Path) == "main.go" && ev.Kind == watcher.Modified
	})
	if ev.Kind != watcher.Modified {
		t.Fatalf("kind = %v, want Modified", ev.Kind)
	}
}

// TestLibraryDirectoriesAreNotWatched pins the descriptor-budget decision.
// Dependency trees dominate a repo's file count while almost never changing,
// and on macOS every watched file costs a descriptor, so they are indexed but
// not watched.
func TestLibraryDirectoriesAreNotWatched(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	w, events, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(nm, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	expectNoEvent(t, events, "library directories must not be watched")

	// Control: an owned file in the same tree still reports.
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	receiveEvent(t, events, func(ev watcher.FileEvent) bool {
		return filepath.Base(ev.Path) == "app.js"
	})
}
