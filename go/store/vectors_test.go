// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"
)

func TestVectorRoundTrip(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 10},
	})
	syms, _ := s.SymbolsInFile("a.go")
	if len(syms) != 1 {
		t.Fatal("missing symbol")
	}
	// Look up the inserted id by joining symbols.id; since we don't expose it
	// directly, run a count check via LoadEmbeddings round-trip.
	var symID int64
	row := s.db.QueryRow(`SELECT id FROM symbols WHERE name='F'`)
	if err := row.Scan(&symID); err != nil {
		t.Fatal(err)
	}

	vec := []float32{0.1, -0.2, 0.3, 0.4}
	if err := s.UpsertEmbedding(symID, vec, "hash1", "mock"); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadEmbeddings("mock")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(got))
	}
	for _, v := range got {
		if len(v) != len(vec) {
			t.Fatalf("dim mismatch: got %d want %d", len(v), len(vec))
		}
		for i := range v {
			if v[i] != vec[i] {
				t.Errorf("vec[%d]: got %v want %v", i, v[i], vec[i])
			}
		}
	}

	// Different model: nothing returned.
	other, _ := s.LoadEmbeddings("ollama:nomic-embed-text")
	if len(other) != 0 {
		t.Errorf("expected zero embeddings for unmatched model, got %d", len(other))
	}
}

func TestVectorCascadeOnSymbolDelete(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 10},
	})
	var symID int64
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='F'`).Scan(&symID)
	s.UpsertEmbedding(symID, []float32{1, 2, 3}, "h", "mock")

	// Re-index the file with a different set of symbols — old row gets
	// deleted, vector should cascade.
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "G", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 10},
	})
	got, _ := s.LoadEmbeddings("mock")
	if len(got) != 0 {
		t.Errorf("expected vector to cascade-delete on symbol delete, got %d", len(got))
	}
}

func TestVectorLookupFreshness(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 10},
	})
	var symID int64
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='F'`).Scan(&symID)
	s.UpsertEmbedding(symID, []float32{1, 2, 3}, "hashA", "mock")

	if got, _ := s.LookupEmbedding(symID, "mock"); got != "hashA" {
		t.Errorf("LookupEmbedding got %q, want hashA", got)
	}
	if got, _ := s.LookupEmbedding(symID, "other-model"); got != "" {
		t.Errorf("LookupEmbedding for other model: got %q, want empty", got)
	}
	if got, _ := s.LookupEmbedding(symID+999, "mock"); got != "" {
		t.Errorf("LookupEmbedding for missing symbol: got %q, want empty", got)
	}
}
