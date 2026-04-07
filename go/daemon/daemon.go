// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the sawmill daemon — a single global process that
// manages per-project CodebaseModels and serves MCP tool calls over a Unix
// domain socket. Each connection announces its project root in the handshake;
// the daemon lazily loads and shares a CodebaseModel per unique root.
package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
)

// ModelPool manages a shared pool of CodebaseModels keyed by project root.
// Multiple connections to the same root share one model (amortised parsing).
type ModelPool struct {
	mu     sync.Mutex
	models map[string]*model.CodebaseModel
}

// NewModelPool creates an empty model pool.
func NewModelPool() *ModelPool {
	return &ModelPool{models: make(map[string]*model.CodebaseModel)}
}

// Get returns the model for root, loading it lazily on first access.
func (p *ModelPool) Get(root string) (*model.CodebaseModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if m, ok := p.models[root]; ok {
		return m, nil
	}

	m, err := model.Load(root)
	if err != nil {
		return nil, err
	}
	p.models[root] = m
	return m, nil
}

// CloseAll closes all models in the pool.
func (p *ModelPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.models {
		m.Close()
	}
	p.models = nil
}

// Daemon manages a pool of CodebaseModels and an mcpbridge.Server.
type Daemon struct {
	pool   *ModelPool
	server *mcpbridge.Server
}

// Start starts the global daemon, listening on socketPath. Each connection
// announces its project root in the handshake; the daemon lazily loads a
// shared CodebaseModel per root. Blocks until SIGINT or SIGTERM.
func Start(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	pool := NewModelPool()

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		HandlerFactory: func(root string) mcpbridge.ToolHandler {
			if root == "" {
				// No root announced — return a handler with no pre-loaded
				// model. The client must call parse with an explicit path.
				return mcp.NewHandler()
			}
			m, err := pool.Get(root)
			if err != nil {
				log.Printf("warning: failed to load model for %q: %v", root, err)
				return mcp.NewHandler()
			}
			return mcp.NewHandlerWithModel(m)
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
