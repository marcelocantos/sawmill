// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"context"
	"math"
	"os"
	"testing"
)

func TestCosineIdentityOne(t *testing.T) {
	v := []float32{1, 2, 3}
	if got := Cosine(v, v); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Errorf("Cosine(v,v) = %v, want ~1.0", got)
	}
}

func TestCosineOrthogonalZero(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	if got := Cosine(a, b); math.Abs(float64(got)) > 1e-5 {
		t.Errorf("Cosine(e1,e2) = %v, want ~0", got)
	}
}

func TestMockEmbedderDeterministic(t *testing.T) {
	m := &MockEmbedder{D: 16}
	v1, _ := m.Embed(context.Background(), []string{"alpha"})
	v2, _ := m.Embed(context.Background(), []string{"alpha"})
	if len(v1) != 1 || len(v1[0]) != 16 {
		t.Fatalf("unexpected shape %v", v1)
	}
	for i := range v1[0] {
		if v1[0][i] != v2[0][i] {
			t.Errorf("mock embedder not deterministic at index %d", i)
		}
	}
}

func TestMockEmbedderDistinct(t *testing.T) {
	m := &MockEmbedder{D: 16}
	v, _ := m.Embed(context.Background(), []string{"alpha", "beta"})
	if Cosine(v[0], v[1]) > 0.95 {
		t.Errorf("expected distinct vectors, got cosine=%v", Cosine(v[0], v[1]))
	}
}

// TestOllamaEmbedderIntegration only runs when SAWMILL_OLLAMA_TEST=1 is set —
// CI doesn't run it. Local smoke-test of the live HTTP path.
func TestOllamaEmbedderIntegration(t *testing.T) {
	if os.Getenv("SAWMILL_OLLAMA_TEST") != "1" {
		t.Skip("set SAWMILL_OLLAMA_TEST=1 to run")
	}
	o := NewOllama("", "nomic-embed-text")
	d, err := o.Dim(context.Background())
	if err != nil {
		t.Fatalf("Dim: %v", err)
	}
	if d <= 0 {
		t.Errorf("expected positive dim, got %d", d)
	}
	vs, err := o.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vs) != 2 {
		t.Errorf("expected 2 vectors, got %d", len(vs))
	}
}
