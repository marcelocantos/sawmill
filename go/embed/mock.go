// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
)

// MockEmbedder produces deterministic vectors from the input text by hashing
// chunks of the input into a fixed-dimension vector. It's not a real
// semantic embedder — it only gives stable, distinct vectors for distinct
// inputs, suitable for unit tests of the retrieval and storage layers.
type MockEmbedder struct {
	D       int    // vector dimension (default 32 if zero)
	ModelTag string // appended to ModelID() so tests can simulate model swaps
}

// ModelID returns "mock[:tag]".
func (m *MockEmbedder) ModelID() string {
	if m.ModelTag == "" {
		return "mock"
	}
	return "mock:" + m.ModelTag
}

// Dim returns the configured dimension (default 32).
func (m *MockEmbedder) Dim(_ context.Context) (int, error) {
	d := m.D
	if d == 0 {
		d = 32
	}
	return d, nil
}

// Embed returns one vector per input, derived from SHA-256 of the text. The
// vector is fully determined by the text, so two identical inputs produce
// identical vectors; small text differences produce small vector
// differences only by coincidence (this is a mock, not a real embedder).
func (m *MockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	d, _ := m.Dim(context.Background())
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashVec(t, d)
	}
	return out, nil
}

// hashVec generates a deterministic d-dimensional vector from s. It folds
// SHA-256 outputs as needed to fill d float32 slots.
func hashVec(s string, d int) []float32 {
	v := make([]float32, d)
	seed := []byte(s)
	out := 0
	for out < d {
		h := sha256.Sum256(seed)
		for i := 0; i+4 <= len(h) && out < d; i += 4 {
			u := binary.LittleEndian.Uint32(h[i : i+4])
			// Map u (uint32) into [-1, 1) for a unit-vector-friendly range.
			v[out] = float32(int32(u))/float32(1<<31) - 0
			out++
		}
		seed = h[:]
	}
	return v
}
