// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/marcelocantos/sawmill/store"
	"github.com/marcelocantos/sawmill/summary"
)

// SummaryConfig is the env-driven configuration for the background
// summariser. Disabled() is true iff no env vars enable the feature.
type SummaryConfig struct {
	Enabled    bool
	Model      string  // SAWMILL_SUMMARY_MODEL
	CostCapUSD float64 // SAWMILL_SUMMARY_COST_CAP_USD
	BatchLimit int     // SAWMILL_SUMMARY_BATCH (max symbols to summarise per Run pass)
}

// SummaryConfigFromEnv reads SAWMILL_SUMMARISE / SAWMILL_SUMMARY_* and
// returns a config. Disabled by default.
func SummaryConfigFromEnv() SummaryConfig {
	cfg := SummaryConfig{
		Enabled:    os.Getenv("SAWMILL_SUMMARISE") == "1",
		Model:      os.Getenv("SAWMILL_SUMMARY_MODEL"),
		CostCapUSD: 1.00, // safety default — 1 USD per run, override to lift
		BatchLimit: 0,    // 0 -> no per-run cap (cost cap is the real ceiling)
	}
	if v := os.Getenv("SAWMILL_SUMMARY_COST_CAP_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.CostCapUSD = f
		}
	}
	if v := os.Getenv("SAWMILL_SUMMARY_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchLimit = n
		}
	}
	return cfg
}

// runSummariserBackground starts the summariser as a background goroutine.
// It does NOT block Load — the FTS/graph/vector tiers come online
// immediately, and summaries trickle in over the lifetime of the session.
// On cost-cap breach the goroutine exits cleanly; the user can re-launch
// the server with a larger cap.
func (m *CodebaseModel) runSummariserBackground(cfg SummaryConfig) {
	go func() {
		ctx := context.Background()
		runner := summary.NewQueueRunner(summary.QueueConfig{
			WorkDir:    m.Root,
			Model:      cfg.Model,
			CostCapUSD: cfg.CostCapUSD,
		})
		sink := &storeSink{m: m}

		// next() drains the pending list on first call, then returns nil so
		// the worker exits. New candidates discovered later are picked up on
		// the next Load (or a future refresh hook).
		pending, err := m.Store.SymbolsNeedingSummary(summary.PromptID, cfg.BatchLimit)
		if err != nil {
			log.Printf("sawmill: enumerating summary candidates: %v", err)
			return
		}
		if len(pending) == 0 {
			return
		}
		log.Printf("sawmill: summariser starting on %d symbol(s) (cost cap $%.2f)", len(pending), cfg.CostCapUSD)
		idx := 0
		next := func() *summary.Item {
			if idx >= len(pending) {
				return nil
			}
			c := pending[idx]
			idx++
			return &summary.Item{
				SymbolID:  c.SymbolID,
				FilePath:  c.FilePath,
				Name:      extractName(c.Text),
				Kind:      "",
				Signature: extractSignature(c.Text),
				Body:      "", // signature + doc already in Text; saves prompt cost
			}
		}
		err = runner.Run(ctx, sink, next)
		log.Printf("sawmill: summariser done — spent $%.4f, %d processed, err=%v", runner.CostUSD(), idx, err)
	}()
}

// storeSink persists OnResult / OnFailure to the database and refreshes
// the in-memory vector cache so newly-summarised symbols become searchable
// without restart.
type storeSink struct{ m *CodebaseModel }

func (s *storeSink) OnResult(item summary.Item, res summary.Result) error {
	rec := store.SummaryRecord{
		SymbolID:    item.SymbolID,
		Summary:     res.Summary,
		PromptID:    res.PromptID,
		ModelID:     res.ModelID,
		CostUSD:     res.CostUSD,
		Tokens:      res.Tokens,
		GeneratedAt: time.Now(),
	}
	edges := make([]store.KGEdgeRecord, 0, len(res.Edges))
	for _, e := range res.Edges {
		edges = append(edges, store.KGEdgeRecord{
			SrcSymbolID: item.SymbolID,
			DstName:     e.Dst,
			Kind:        e.Kind,
			Confidence:  e.Confidence,
			PromptID:    res.PromptID,
		})
	}
	if err := s.m.Store.UpsertSummary(rec, edges); err != nil {
		return err
	}

	// If the embedding tier is on, embed the summary so semantic_search can
	// match against it with a distinct "summary" provenance.
	if s.m.Embedder != nil && res.Summary != "" {
		ctx := context.Background()
		vecs, err := s.m.Embedder.Embed(ctx, []string{res.Summary})
		if err == nil && len(vecs) == 1 {
			modelID := s.m.Embedder.ModelID()
			if err := s.m.Store.UpsertSummaryEmbedding(item.SymbolID, vecs[0], res.PromptID, modelID); err == nil {
				s.m.vecsMu.Lock()
				if s.m.SummaryVecs == nil {
					s.m.SummaryVecs = make(map[int64][]float32)
				}
				s.m.SummaryVecs[item.SymbolID] = vecs[0]
				s.m.vecsMu.Unlock()
			}
		}
	}
	return nil
}

func (s *storeSink) OnFailure(item summary.Item, reason string, retryCount int) error {
	return s.m.Store.RecordSummaryFailure(item.SymbolID, summary.PromptID, reason, retryCount)
}

// extractName / extractSignature pull the first two lines out of the
// embed-candidate Text format (name\nsignature\ndoc\n…). They're best
// effort: if the format changes we just pass whatever we got, which still
// gets summarised, just with a slightly worse prompt.
func extractName(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			return text[:i]
		}
	}
	return text
}

func extractSignature(text string) string {
	first := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			first = i + 1
			break
		}
	}
	rest := text[first:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\n' {
			return rest[:i]
		}
	}
	return rest
}
