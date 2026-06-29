// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"context"
	"fmt"
	"os"

	"github.com/marcelocantos/sawmill/embed"
	"github.com/marcelocantos/sawmill/store"
)

// embedBatchSize controls how many candidates we send per HTTP call to the
// embedder. Larger batches amortise request overhead but keep this small
// enough that one stalled call doesn't block all progress.
const embedBatchSize = 32

// EmbedderFromEnv returns an Embedder configured from environment variables,
// or nil if no embedding model is configured. Currently understands:
//
//	SAWMILL_EMBED_PROVIDER=ollama       (default if SAWMILL_EMBED_MODEL is set)
//	SAWMILL_EMBED_MODEL=nomic-embed-text
//	SAWMILL_EMBED_ENDPOINT=http://...   (provider-specific, defaults to provider's standard)
//
// A nil return value is the "no embedder configured" signal — the FTS and
// graph tiers still work without it.
func EmbedderFromEnv() embed.Embedder {
	model := os.Getenv("SAWMILL_EMBED_MODEL")
	if model == "" {
		return nil
	}
	provider := os.Getenv("SAWMILL_EMBED_PROVIDER")
	if provider == "" {
		provider = "ollama"
	}
	endpoint := os.Getenv("SAWMILL_EMBED_ENDPOINT")
	switch provider {
	case "ollama":
		return embed.NewOllama(endpoint, model)
	default:
		return nil
	}
}

// EmbedAll walks every embed candidate in the store and ensures each has a
// fresh vector under the current model id. Vectors that already exist with
// a matching body hash are skipped. Returns the number of new vectors
// written.
func EmbedAll(ctx context.Context, s *store.Store, e embed.Embedder) (int, error) {
	if e == nil {
		return 0, nil
	}
	return embedFiltered(ctx, s, e, "")
}

// embedFiltered is the per-file form of EmbedAll. filePath == "" embeds
// every candidate.
func embedFiltered(ctx context.Context, s *store.Store, e embed.Embedder, filePath string) (int, error) {
	cands, err := s.EmbedCandidates(filePath)
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}
	modelID := e.ModelID()

	// Filter to symbols whose stored hash doesn't match.
	var todo []store.EmbedCandidate
	for _, c := range cands {
		got, _ := s.LookupEmbedding(c.SymbolID, modelID)
		if got == c.BodyHash {
			continue
		}
		todo = append(todo, c)
	}
	if len(todo) == 0 {
		return 0, nil
	}

	dim, err := e.Dim(ctx)
	if err != nil {
		return 0, fmt.Errorf("probing embedder dim: %w", err)
	}

	written := 0
	for start := 0; start < len(todo); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(todo) {
			end = len(todo)
		}
		batch := todo[start:end]
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Text
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return written, fmt.Errorf("embedding batch %d-%d: %w", start, end, err)
		}
		if i := embed.ValidateDim(vecs, dim); i >= 0 {
			return written, fmt.Errorf("embedder returned dim %d for batch[%d], want %d", len(vecs[i]), i, dim)
		}
		for i, c := range batch {
			if err := s.UpsertEmbedding(c.SymbolID, vecs[i], c.BodyHash, modelID); err != nil {
				return written, err
			}
			written++
		}
	}
	return written, nil
}
