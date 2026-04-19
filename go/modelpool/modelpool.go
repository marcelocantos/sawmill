// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package modelpool manages a shared, reference-counted pool of
// CodebaseModels keyed by project root. Multiple MCP sessions targeting the
// same root share one model (amortised parsing). Models are evicted after an
// idle period when no sessions reference them.
package modelpool

import (
	"log"
	"sync"
	"time"

	"github.com/marcelocantos/sawmill/model"
)

const idleEvictionTimeout = 5 * time.Minute

type entry struct {
	model *model.CodebaseModel
	refs  int
	timer *time.Timer
}

// Pool ref-counts CodebaseModels by project root. Get/Release pair off; the
// model is closed after idleEvictionTimeout with no active borrowers.
type Pool struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// New creates an empty pool.
func New() *Pool {
	return &Pool{entries: make(map[string]*entry)}
}

// Get returns the model for root, loading it lazily on first access.
// Increments the reference count. Callers must call Release when done.
func (p *Pool) Get(root string) (*model.CodebaseModel, error) {
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
	p.entries[root] = &entry{model: m, refs: 1}
	return m, nil
}

// Release decrements the reference count for root. When it reaches zero, an
// idle timer starts; if no new borrower arrives before it fires, the model
// is closed and removed from the pool.
func (p *Pool) Release(root string) {
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

// CloseAll closes every model in the pool immediately.
func (p *Pool) CloseAll() {
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
