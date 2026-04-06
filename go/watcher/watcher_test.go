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

const eventTimeout = 2 * time.Second

// receiveEvent waits up to eventTimeout for a FileEvent matching the predicate.
func receiveEvent(t *testing.T, ch <-chan watcher.FileEvent, match func(watcher.FileEvent) bool) watcher.FileEvent {
	t.Helper()
	deadline := time.After(eventTimeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("events channel closed before expected event arrived")
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for expected file event")
		}
	}
	panic("unreachable")
}

func TestWatchDetectsNewFile(t *testing.T) {
	dir := t.TempDir()
	w, events, err := watcher.Watch(dir)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	path := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := receiveEvent(t, events, func(e watcher.FileEvent) bool {
		return e.Path == path
	})
	if ev.Kind != watcher.Created {
		t.Errorf("expected Created, got %v", ev.Kind)
	}
}

func TestWatchDetectsModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile initial: %v", err)
	}

	w, events, err := watcher.Watch(dir)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	// Give the watcher time to settle before modifying.
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(path, []byte("package main\n// changed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile modify: %v", err)
	}

	ev := receiveEvent(t, events, func(e watcher.FileEvent) bool {
		return e.Path == path
	})
	if ev.Kind != watcher.Modified {
		t.Errorf("expected Modified, got %v", ev.Kind)
	}
}

func TestWatchDetectsRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, events, err := watcher.Watch(dir)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	// Give the watcher time to settle before removing.
	time.Sleep(50 * time.Millisecond)

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ev := receiveEvent(t, events, func(e watcher.FileEvent) bool {
		return e.Path == path
	})
	if ev.Kind != watcher.Removed {
		t.Errorf("expected Removed, got %v", ev.Kind)
	}
}

func TestDebouncing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile initial: %v", err)
	}

	w, events, err := watcher.Watch(dir)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	// Give the watcher time to settle.
	time.Sleep(50 * time.Millisecond)

	// Rapidly write to the same file many times.
	for i := range 10 {
		content := []byte("package main\n// v" + string(rune('0'+i)) + "\n")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("WriteFile rapid %d: %v", i, err)
		}
	}

	// Wait for at least one event.
	receiveEvent(t, events, func(e watcher.FileEvent) bool {
		return e.Path == path
	})

	// After the debounce window, we should see at most one more event
	// (the debouncer may split rapid writes across two windows on slow CI).
	// The key assertion: 10 rapid writes should NOT produce 10 events.
	time.Sleep(300 * time.Millisecond)
	extraCount := 0
drain:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break drain
			}
			if ev.Path == path {
				extraCount++
			}
		default:
			break drain
		}
	}
	if extraCount > 2 {
		t.Errorf("debouncing failed: 10 writes produced %d extra events (expected <= 2)", extraCount)
	}
}
