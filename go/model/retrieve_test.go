// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/embed"
)

// TestSemanticSearchHybrid drives the fused retriever end-to-end with a
// MockEmbedder so we can verify both BM25 and vector signals contribute.
func TestSemanticSearchHybrid(t *testing.T) {
	dir := t.TempDir()
	// Two functions: one whose doc says "connection", one unrelated.
	main := `package main

// Parse a DSN-style connection string and return a Connection.
func parseConnectionString(s string) error { return nil }

// Compute the SHA-256 hash of input.
func hashInput(s string) string { return "" }

func main() {
	_ = parseConnectionString("")
	_ = hashInput("")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Inject a mock embedder and populate vectors.
	m.Embedder = &embed.MockEmbedder{D: 32}
	if _, err := EmbedAll(context.Background(), m.Store, m.Embedder); err != nil {
		t.Fatalf("EmbedAll: %v", err)
	}
	loaded, err := m.Store.LoadEmbeddings(m.Embedder.ModelID())
	if err != nil {
		t.Fatalf("LoadEmbeddings: %v", err)
	}
	m.Vecs = loaded
	if len(m.Vecs) == 0 {
		t.Fatal("expected stored vectors after EmbedAll")
	}

	hits, err := m.SemanticSearch(context.Background(), "parseConnectionString", "", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	top := hits[0]
	if top.Name != "parseConnectionString" {
		t.Errorf("top hit = %q, want parseConnectionString", top.Name)
	}
	// `why` should record bm25 and vec because the exact text matches both.
	whyJoined := strings.Join(top.Why, ",")
	if !strings.Contains(whyJoined, "bm25") {
		t.Errorf("expected bm25 in why=%v", top.Why)
	}
	if !strings.Contains(whyJoined, "vec") {
		t.Errorf("expected vec in why=%v (mock embeddings should rank exact name match high)", top.Why)
	}
}

func TestSemanticSearchGracefulWithoutEmbedder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// Embedder unset.
	hits, err := m.SemanticSearch(context.Background(), "Helper", "", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "Helper" {
		t.Errorf("expected Helper as top hit; got %v", hits)
	}
}
