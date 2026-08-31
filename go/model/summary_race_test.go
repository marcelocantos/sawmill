// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/marcelocantos/sawmill/embed"
)

// TestSummaryVecsConcurrentAccessNoRace is the regression for F8 (T48): the
// summary-vector cosine search must not range SummaryVecs while a concurrent
// summariser inserts into it in place. The old code copied only the map header
// under the RLock, then ranged the shared map unlocked — a data race that the
// Go runtime turns into an uncatchable "fatal error: concurrent map read and
// map write", killing the whole daemon.
//
// Run under `go test -race` to catch the read/write race directly.
func TestSummaryVecsConcurrentAccessNoRace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Foo() {}\n\nfunc Bar() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	const dim = 16
	m.Embedder = &embed.MockEmbedder{D: dim}
	// Seed a non-trivial summary-vector map so the read side has a real
	// iteration window that overlaps the writer.
	m.SummaryVecs = make(map[int64][]float32, 64)
	for i := int64(1); i <= 64; i++ {
		m.SummaryVecs[i] = make([]float32, dim)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: mirror the real summariser, inserting into SummaryVecs in place
	// under vecsMu.Lock (summary.go:OnResult). The key space is bounded so the
	// map stays small (cheap snapshots, bounded memory); the point is only to
	// keep a concurrent in-place writer hammering the same map the reader
	// ranges, so -race sees the read/write overlap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		const keySpace = 128
		id := int64(1000)
		for writes := 0; writes < 2_000_000; writes++ {
			select {
			case <-stop:
				return
			default:
			}
			m.vecsMu.Lock()
			m.SummaryVecs[id] = make([]float32, dim)
			m.vecsMu.Unlock()
			id++
			if id >= 1000+keySpace {
				id = 1000
			}
		}
	}()

	// Reader: repeatedly run the fused search, which ranges the summary index.
	for j := 0; j < 300; j++ {
		if _, err := m.SemanticSearch(context.Background(), "Foo", "", "", 5); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("SemanticSearch: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}
