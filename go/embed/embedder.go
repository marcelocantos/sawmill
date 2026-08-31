// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package embed defines the embedding interface used by Sawmill's semantic
// discovery tier and bundles concrete embedders backed by local model
// servers (currently Ollama). The package is intentionally network-only:
// Sawmill stays CGo-free, so model inference is the responsibility of an
// external server the user already runs.
package embed

import (
	"context"
	"fmt"
)

// Embedder converts a batch of texts into vectors. Implementations should
// return vectors in the same order as the inputs, and produce vectors of a
// consistent dimension across calls. Dimension is reported by Dim() so the
// store can sanity-check at insertion time.
type Embedder interface {
	// ModelID returns a stable identifier for the model behind this embedder
	// (e.g. "ollama:nomic-embed-text"). Used to detect when a project's
	// existing vectors were produced by a different model and need a refresh.
	ModelID() string

	// Dim returns the expected vector dimension. May trigger a one-shot probe
	// embed on first call; subsequent calls cache the value.
	Dim(ctx context.Context) (int, error)

	// Embed returns one vector per input text. Vectors are float32 for
	// storage compactness — most local embedding models are happy with f32,
	// and the cosine score doesn't benefit measurably from f64.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Cosine returns the cosine similarity of a and b. Returns 0 if either
// vector is zero-norm or the lengths don't match. Inputs are NOT modified.
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt32(na) * sqrt32(nb))
}

// sqrt32 is a small helper that avoids importing math in tight loops.
func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton iteration is plenty for our purposes — we only need a
	// monotonic-positive square root for ranking, not bit-exact float math.
	z := x
	for range 6 {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

// Hit is one ranked result returned by retrievers.
type Hit struct {
	SymbolID int64
	Score    float32
}

// ValidateDim checks that every vector in vecs has dimension d. Returns the
// first offending index or -1 if all OK.
func ValidateDim(vecs [][]float32, d int) int {
	for i, v := range vecs {
		if len(v) != d {
			return i
		}
	}
	return -1
}

// ErrUnavailable is returned by NewFromEnv when no embedder is configured.
type ErrUnavailable struct{ Reason string }

func (e *ErrUnavailable) Error() string {
	if e.Reason == "" {
		return "embed: no embedder configured"
	}
	return fmt.Sprintf("embed: no embedder configured: %s", e.Reason)
}
