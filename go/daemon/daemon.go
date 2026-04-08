// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the sawmill daemon — a single global process that
// manages per-project CodebaseModels and serves MCP tool calls over a Unix
// domain socket. Each connection announces its project root in the handshake;
// the daemon lazily loads and shares a CodebaseModel per unique root. Models
// are evicted after an idle period when no connections reference them.
package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
)

const idleEvictionTimeout = 5 * time.Minute

// poolEntry tracks a model, its reference count, and an idle eviction timer.
type poolEntry struct {
	model *model.CodebaseModel
	refs  int
	timer *time.Timer
}

// ModelPool manages a shared pool of CodebaseModels keyed by project root.
// Multiple connections to the same root share one model (amortised parsing).
// Models are evicted after idleEvictionTimeout with no active connections.
type ModelPool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
}

// NewModelPool creates an empty model pool.
func NewModelPool() *ModelPool {
	return &ModelPool{entries: make(map[string]*poolEntry)}
}

// Get returns the model for root, loading it lazily on first access.
// Increments the reference count. Callers must call Release when done.
func (p *ModelPool) Get(root string) (*model.CodebaseModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e, ok := p.entries[root]; ok {
		e.refs++
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
		return e.model, nil
	}

	m, err := model.Load(root)
	if err != nil {
		return nil, err
	}
	p.entries[root] = &poolEntry{model: m, refs: 1}
	return m, nil
}

// Release decrements the reference count for root. When it reaches zero, an
// idle timer starts. If no new connection arrives before it fires, the model
// is closed and removed from the pool.
func (p *ModelPool) Release(root string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, ok := p.entries[root]
	if !ok {
		return
	}
	e.refs--
	if e.refs <= 0 {
		e.timer = time.AfterFunc(idleEvictionTimeout, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if e, ok := p.entries[root]; ok && e.refs <= 0 {
				log.Printf("evicting idle model for %s", root)
				e.model.Close()
				delete(p.entries, root)
			}
		})
	}
}

// CloseAll closes all models in the pool immediately.
func (p *ModelPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.timer != nil {
			e.timer.Stop()
		}
		e.model.Close()
	}
	p.entries = nil
}

// Daemon manages a pool of CodebaseModels and an mcpbridge.Server.
type Daemon struct {
	pool   *ModelPool
	server *mcpbridge.Server
}

// Start starts the global daemon, listening on socketPath. Each connection
// announces its project root in the handshake; the daemon lazily loads a
// shared CodebaseModel per root. Models are evicted when idle. Blocks until
// SIGINT or SIGTERM.
func Start(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	pool := NewModelPool()

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		HandlerFactory: func(root string) (mcpbridge.ToolHandler, func()) {
			if root == "" {
				return mcp.NewHandler(), nil
			}
			m, err := pool.Get(root)
			if err != nil {
				log.Printf("warning: failed to load model for %q: %v", root, err)
				return mcp.NewHandler(), nil
			}
			cleanup := func() { pool.Release(root) }
			return mcp.NewHandlerWithModel(m), cleanup
		},
	})
	if err != nil {
		pool.CloseAll()
		return fmt.Errorf("creating server: %w", err)
	}

	d := &Daemon{pool: pool, server: srv}

	log.Printf("sawmill daemon listening on %s", socketPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("shutting down")
		d.Shutdown()
	}()

	if err := srv.Serve(); err != nil {
		return err
	}
	return nil
}

// Shutdown closes the server and all models.
func (d *Daemon) Shutdown() {
	if d.server != nil {
		d.server.Close()
	}
	if d.pool != nil {
		d.pool.CloseAll()
	}
}
