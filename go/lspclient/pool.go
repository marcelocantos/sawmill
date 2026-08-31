// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package lspclient

import (
	"os/exec"
	"sync"

	"github.com/marcelocantos/sawmill/adapters"
)

// Pool manages LSP clients per (language, project root) pair.
type Pool struct {
	mu      sync.Mutex
	clients map[poolKey]*Client
}

type poolKey struct {
	language string
	root     string
}

// NewPool creates a new empty Pool.
func NewPool() *Pool {
	return &Pool{
		clients: make(map[poolKey]*Client),
	}
}

// Get returns an existing LSP client for the adapter's language in the given
// root directory, or launches a new one. Returns nil if the adapter's
// LSPCommand() is nil or the server binary is not on PATH.
func (p *Pool) Get(adapter adapters.LanguageAdapter, root string) *Client {
	cmd := adapter.LSPCommand()
	if len(cmd) == 0 {
		return nil
	}

	// Check that the binary exists on PATH.
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return nil
	}

	langID := adapter.LSPLanguageID()
	key := poolKey{language: langID, root: root}

	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.clients[key]; ok {
		return c
	}

	c, err := NewClient(cmd, root, langID)
	if err != nil {
		// Failed to start — return nil for graceful degradation.
		return nil
	}

	p.clients[key] = c
	return c
}

// Close shuts down all managed LSP clients.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, c := range p.clients {
		_ = c.Close()
		delete(p.clients, key)
	}
}
