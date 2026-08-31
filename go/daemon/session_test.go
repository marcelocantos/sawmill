// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/modelpool"
)

// noopLoader satisfies mcp.ModelLoader without touching the filesystem. The
// registry never dereferences the model; it only owns the handler's lifetime.
func noopLoader(string) (*model.CodebaseModel, func(), error) {
	return nil, func() {}, nil
}

// TestReapIdleClosesQuietSessions is the regression guard for the leak that
// pinned models indefinitely: a bridge process outliving its agent never
// unregisters, so the unregister hook never fires and the pool's reference
// count never reaches zero.
func TestReapIdleClosesQuietSessions(t *testing.T) {
	r := newSessionRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }

	r.get("busy", noopLoader)
	r.get("quiet", noopLoader)
	if got := r.len(); got != 2 {
		t.Fatalf("len() = %d, want 2", got)
	}

	// Move past the timeout, then touch only one session.
	now = now.Add(SessionIdleTimeout + time.Minute)
	r.get("busy", noopLoader)

	if got := r.reapIdle(SessionIdleTimeout); got != 1 {
		t.Fatalf("reapIdle() reaped %d, want 1", got)
	}
	if got := r.len(); got != 1 {
		t.Fatalf("len() = %d after reap, want 1 (the active session)", got)
	}
	// The survivor must be the one that was still being used.
	if _, ok := r.entries["busy"]; !ok {
		t.Error("the active session was reaped instead of the idle one")
	}
}

// A session that keeps calling tools is never reaped, however long it lives.
func TestReapIdleKeepsActiveSessions(t *testing.T) {
	r := newSessionRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }

	r.get("s", noopLoader)
	for range 5 {
		now = now.Add(SessionIdleTimeout / 2)
		r.get("s", noopLoader)
		if got := r.reapIdle(SessionIdleTimeout); got != 0 {
			t.Fatalf("reapIdle() reaped %d, want 0 for a session in continuous use", got)
		}
	}
	if got := r.len(); got != 1 {
		t.Fatalf("len() = %d, want 1", got)
	}
}

// mcp.Handler is what the registry owns; this pins that a handler with no
// borrowed model closes cleanly, so reaping cannot panic on an idle session
// that never called parse.
func TestHandlerWithoutModelClosesCleanly(t *testing.T) {
	h := mcp.NewHandlerWithLoader(noopLoader)
	h.Close()
	h.Close() // idempotent: the reaper and Shutdown can race
}

// TestReapingReleasesTheBorrowedModel is the end-to-end that matters: reaping
// an idle session must actually hand its model back, not merely forget the
// handler. Without the release, the pool's reference count never reaches zero
// and its own idle eviction never fires — which is exactly how twenty
// abandoned bridges kept twenty models, and their watchers, alive.
func TestReapingReleasesTheBorrowedModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pool := modelpool.New()
	t.Cleanup(pool.CloseAll)

	var released atomic.Int32
	loader := func(r string) (*model.CodebaseModel, func(), error) {
		m, err := pool.Get(r)
		if err != nil {
			return nil, nil, err
		}
		return m, func() {
			released.Add(1)
			pool.Release(r)
		}, nil
	}

	reg := newSessionRegistry()
	now := time.Now()
	reg.now = func() time.Time { return now }

	h := reg.get("s", loader)
	if _, isErr, err := h.Call("parse", map[string]any{"path": root}); err != nil || isErr {
		t.Fatalf("parse: err=%v isErr=%v", err, isErr)
	}
	if got := released.Load(); got != 0 {
		t.Fatalf("released %d times before reaping, want 0", got)
	}

	now = now.Add(SessionIdleTimeout + time.Minute)
	if n := reg.reapIdle(SessionIdleTimeout); n != 1 {
		t.Fatalf("reapIdle() = %d, want 1", n)
	}
	if got := released.Load(); got != 1 {
		t.Errorf("model released %d times, want 1 — a reaped session still pins its model", got)
	}
}
