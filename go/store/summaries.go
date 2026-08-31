// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SummaryRecord is one row from symbol_summaries.
type SummaryRecord struct {
	SymbolID    int64
	Summary     string
	PromptID    string
	ModelID     string
	CostUSD     float64
	Tokens      int
	GeneratedAt time.Time
}

// KGEdgeRecord is one LLM-extracted knowledge-graph edge.
type KGEdgeRecord struct {
	SrcSymbolID int64
	DstName     string
	Kind        string
	Confidence  float64
	PromptID    string
}

// UpsertSummary writes one LLM summary and replaces any kg_edges that were
// produced for the same symbol by the same prompt. Both happen in one
// transaction so the kg_edges always match the summary they were generated
// alongside.
func (s *Store) UpsertSummary(rec SummaryRecord, edges []KGEdgeRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("summary tx begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(
		`INSERT INTO symbol_summaries
		   (symbol_id, summary, prompt_id, model_id, cost_usd, tokens, generated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(symbol_id) DO UPDATE SET
			summary = excluded.summary,
			prompt_id = excluded.prompt_id,
			model_id = excluded.model_id,
			cost_usd = excluded.cost_usd,
			tokens = excluded.tokens,
			generated_at = excluded.generated_at`,
		rec.SymbolID, rec.Summary, rec.PromptID, rec.ModelID,
		rec.CostUSD, rec.Tokens, rec.GeneratedAt.Unix(),
	); err != nil {
		return fmt.Errorf("upserting summary %d: %w", rec.SymbolID, err)
	}

	// Replace edges produced under the same prompt id.
	if _, err := tx.Exec(
		`DELETE FROM kg_edges WHERE src_symbol_id = ? AND prompt_id = ?`,
		rec.SymbolID, rec.PromptID,
	); err != nil {
		return fmt.Errorf("clearing kg_edges for %d: %w", rec.SymbolID, err)
	}
	if len(edges) > 0 {
		stmt, err := tx.Prepare(
			`INSERT INTO kg_edges (src_symbol_id, dst_name, kind, confidence, prompt_id)
			 VALUES (?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return fmt.Errorf("preparing kg_edges insert: %w", err)
		}
		defer stmt.Close()
		for _, e := range edges {
			if _, err := stmt.Exec(e.SrcSymbolID, e.DstName, e.Kind, e.Confidence, e.PromptID); err != nil {
				return fmt.Errorf("inserting kg_edge %s->%s: %w", e.Kind, e.DstName, err)
			}
		}
	}
	return tx.Commit()
}

// SummaryStatus is the snapshot reported by index_status. It reflects what's
// already on disk; the running queue's in-flight count lives in the model
// layer.
type SummaryStatus struct {
	PromptID         string
	SummariesCurrent int     // summaries whose prompt_id matches PromptID
	SummariesStale   int     // summaries whose prompt_id differs
	KGEdges          int     // kg_edges rows for the current prompt
	TotalCostUSD     float64 // sum of cost_usd over summaries with current prompt
	TotalTokens      int
	Failures         int // rows in summary_failures
}

// SummaryStatusForPrompt returns aggregate counters about the summariser
// state for the given prompt id.
func (s *Store) SummaryStatusForPrompt(promptID string) (SummaryStatus, error) {
	status := SummaryStatus{PromptID: promptID}

	row := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(cost_usd), 0), COALESCE(SUM(tokens), 0)
		   FROM symbol_summaries WHERE prompt_id = ?`,
		promptID,
	)
	if err := row.Scan(&status.SummariesCurrent, &status.TotalCostUSD, &status.TotalTokens); err != nil {
		return status, fmt.Errorf("summary status (current): %w", err)
	}

	row = s.db.QueryRow(`SELECT COUNT(*) FROM symbol_summaries WHERE prompt_id != ?`, promptID)
	if err := row.Scan(&status.SummariesStale); err != nil {
		return status, fmt.Errorf("summary status (stale): %w", err)
	}

	row = s.db.QueryRow(`SELECT COUNT(*) FROM kg_edges WHERE prompt_id = ?`, promptID)
	if err := row.Scan(&status.KGEdges); err != nil {
		return status, fmt.Errorf("summary status (kg_edges): %w", err)
	}

	row = s.db.QueryRow(`SELECT COUNT(*) FROM summary_failures`)
	if err := row.Scan(&status.Failures); err != nil {
		return status, fmt.Errorf("summary status (failures): %w", err)
	}

	return status, nil
}

// SymbolsNeedingSummary returns symbol ids that have no fresh summary under
// promptID. Limit caps how many ids are returned; pass <= 0 for "no cap".
func (s *Store) SymbolsNeedingSummary(promptID string, limit int) ([]EmbedCandidate, error) {
	q := `
		SELECT s.id, s.name, s.file_path,
		       COALESCE(fts.signature, ''), COALESCE(fts.doc, ''),
		       s.start_byte, s.end_byte, s.kind
		  FROM symbols s
		  JOIN symbols_fts fts ON fts.rowid = s.id
		  LEFT JOIN symbol_summaries sm
		    ON sm.symbol_id = s.id AND sm.prompt_id = ?
		 WHERE s.kind IN ('function', 'method', 'type')
		   AND sm.symbol_id IS NULL`
	args := []any{promptID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("SymbolsNeedingSummary: %w", err)
	}
	defer rows.Close()
	var out []EmbedCandidate
	for rows.Next() {
		var c EmbedCandidate
		var name, sig, doc, kind string
		var startByte, endByte int
		if err := rows.Scan(&c.SymbolID, &name, &c.FilePath, &sig, &doc, &startByte, &endByte, &kind); err != nil {
			return nil, fmt.Errorf("scanning candidate: %w", err)
		}
		_ = kind
		c.Text = name + "\n" + sig + "\n" + doc
		c.BodyHash = fmt.Sprintf("%d-%d-%s", startByte, endByte, name)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SummaryByID returns the stored summary for one symbol under promptID, or
// (zero, false) if absent.
func (s *Store) SummaryByID(symbolID int64, promptID string) (SummaryRecord, bool) {
	var (
		rec SummaryRecord
		at  int64
	)
	err := s.db.QueryRow(
		`SELECT symbol_id, summary, prompt_id, model_id, cost_usd, tokens, generated_at
		   FROM symbol_summaries WHERE symbol_id = ? AND prompt_id = ?`,
		symbolID, promptID,
	).Scan(&rec.SymbolID, &rec.Summary, &rec.PromptID, &rec.ModelID,
		&rec.CostUSD, &rec.Tokens, &at)
	if err != nil {
		return SummaryRecord{}, false
	}
	rec.GeneratedAt = time.Unix(at, 0)
	return rec, true
}

// RecordSummaryFailure appends one failure row. Pass retryCount=0 for the
// first attempt; subsequent attempts pass an increasing counter.
func (s *Store) RecordSummaryFailure(symbolID int64, promptID, reason string, retryCount int) error {
	_, err := s.db.Exec(
		`INSERT INTO summary_failures (symbol_id, prompt_id, reason, occurred_at, retry_count)
		 VALUES (?, ?, ?, ?, ?)`,
		symbolID, promptID, reason, time.Now().Unix(), retryCount,
	)
	if err != nil {
		return fmt.Errorf("recording summary failure: %w", err)
	}
	return nil
}

// ExpandKGForward returns LLM-extracted out-edges for the named source
// symbol, optionally filtered by edge kind.
func (s *Store) ExpandKGForward(srcName, edgeKind string) ([]GraphEdge, error) {
	q := `
		SELECT
		  COALESCE(src.name, ''), COALESCE(src.kind, ''), COALESCE(src.file_path, ''),
		  e.kind, e.dst_name,
		  COALESCE(dst.kind, ''), COALESCE(dst.file_path, ''),
		  0, 0
		FROM kg_edges e
		JOIN symbols src ON src.id = e.src_symbol_id
		LEFT JOIN symbols dst
		       ON dst.name = e.dst_name
		      AND dst.kind IN ('function','method','type')
		WHERE src.name = ?`
	args := []any{srcName}
	if edgeKind != "" {
		q += " AND e.kind = ?"
		args = append(args, edgeKind)
	}
	return scanGraphEdges(s.db, q, args)
}

// ExpandKGReverse returns LLM-extracted in-edges that point at dstName.
func (s *Store) ExpandKGReverse(dstName, edgeKind string) ([]GraphEdge, error) {
	q := `
		SELECT
		  COALESCE(src.name, ''), COALESCE(src.kind, ''), COALESCE(src.file_path, ''),
		  e.kind, e.dst_name,
		  COALESCE(dst.kind, ''), COALESCE(dst.file_path, ''),
		  0, 0
		FROM kg_edges e
		JOIN symbols src ON src.id = e.src_symbol_id
		LEFT JOIN symbols dst
		       ON dst.name = e.dst_name
		      AND dst.kind IN ('function','method','type')
		WHERE e.dst_name = ?`
	args := []any{dstName}
	if edgeKind != "" {
		q += " AND e.kind = ?"
		args = append(args, edgeKind)
	}
	return scanGraphEdges(s.db, q, args)
}

// Avoid an unused-import warning when other store callers don't pull in
// database/sql. (Defensive — most builds drop dead imports.)
var _ = sql.ErrNoRows
