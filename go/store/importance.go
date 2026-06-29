// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"fmt"
	"math"
)

// PageRank parameters. Defaults are tuned to converge quickly on the kinds
// of small/medium codebases Sawmill indexes; raising iterations beyond ~50
// adds cost without meaningfully changing ranks.
const (
	pageRankDamping    = 0.85
	pageRankIterations = 50
	pageRankTolerance  = 1e-6
)

// edgeKindWeights weights different reference kinds when computing
// importance. Calls weighted highest — they capture runtime dependency, the
// strongest "X is important to Y" signal. Type-uses are close behind. Import
// edges are intentionally damped because every file in the module typically
// imports the same handful of utility modules, which would otherwise drown
// out real centrality.
var edgeKindWeights = map[string]float64{
	"call":       1.0,
	"type_use":   0.7,
	"import_use": 0.2,
}

// RecomputeImportance runs a weighted PageRank pass over symbol_refs and
// writes the result back into symbols.importance. Safe to call on an empty
// store; it just leaves every importance at zero.
//
// Complexity: O(iterations × |resolved edges| + |symbols|). On the sawmill
// repo (~5k symbols, ~20k edges) it completes in well under a second.
func (s *Store) RecomputeImportance() error {
	// Step 1: load all resolved edges (both src and dst resolved to symbol ids).
	// Unresolved edges are excluded — they have no meaningful PageRank
	// contribution because the destination doesn't exist as a known symbol.
	rows, err := s.db.Query(`
		SELECT r.src_symbol_id, dst.id, r.kind
		FROM symbol_refs r
		JOIN symbols dst
		  ON dst.name = r.dst_name
		 AND dst.kind IN ('function','method','type')
		WHERE r.src_symbol_id IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("loading resolved edges: %w", err)
	}
	type weightedEdge struct {
		dst    int64
		weight float64
	}
	outAdj := make(map[int64][]weightedEdge)
	totalWeight := make(map[int64]float64) // per-source weight sum
	nodes := make(map[int64]bool)
	for rows.Next() {
		var src, dst int64
		var kind string
		if err := rows.Scan(&src, &dst, &kind); err != nil {
			rows.Close()
			return fmt.Errorf("scanning edge: %w", err)
		}
		w := edgeKindWeights[kind]
		if w == 0 {
			w = 1.0 // unknown kind: default weight
		}
		outAdj[src] = append(outAdj[src], weightedEdge{dst: dst, weight: w})
		totalWeight[src] += w
		nodes[src] = true
		nodes[dst] = true
	}
	rows.Close()

	// Step 2: include all symbols as nodes (even ones with no edges) so the
	// distribution sums to 1 and dangling nodes are handled correctly.
	allRows, err := s.db.Query("SELECT id FROM symbols")
	if err != nil {
		return fmt.Errorf("loading symbol ids: %w", err)
	}
	for allRows.Next() {
		var id int64
		if err := allRows.Scan(&id); err != nil {
			allRows.Close()
			return fmt.Errorf("scanning symbol id: %w", err)
		}
		nodes[id] = true
	}
	allRows.Close()

	if len(nodes) == 0 {
		return nil
	}

	// Step 3: run PageRank.
	n := float64(len(nodes))
	base := (1 - pageRankDamping) / n
	rank := make(map[int64]float64, len(nodes))
	for v := range nodes {
		rank[v] = 1.0 / n
	}
	next := make(map[int64]float64, len(nodes))

	for iter := 0; iter < pageRankIterations; iter++ {
		var dangling float64
		for v := range nodes {
			next[v] = base
			if _, hasOut := outAdj[v]; !hasOut {
				dangling += rank[v]
			}
		}
		// Distribute dangling rank uniformly across all nodes.
		if dangling > 0 {
			share := pageRankDamping * dangling / n
			for v := range nodes {
				next[v] += share
			}
		}
		// Distribute weighted out-edges.
		for u, es := range outAdj {
			tw := totalWeight[u]
			if tw == 0 {
				continue
			}
			share := pageRankDamping * rank[u] / tw
			for _, e := range es {
				next[e.dst] += share * e.weight
			}
		}
		// Convergence check.
		var diff float64
		for v, r := range rank {
			diff += math.Abs(next[v] - r)
		}
		rank, next = next, rank
		if diff < pageRankTolerance {
			break
		}
	}

	// Step 4: write back. One UPDATE per node would be N queries; batch via
	// CASE WHEN won't fit cleanly for thousands of rows, so just do a
	// transaction with prepared statement.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning importance update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Zero out everything first — symbols absent from `rank` (none here, but
	// defensive against schema changes) should reset to 0.
	if _, err := tx.Exec("UPDATE symbols SET importance = 0"); err != nil {
		return fmt.Errorf("zeroing importance: %w", err)
	}

	stmt, err := tx.Prepare("UPDATE symbols SET importance = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("preparing importance update: %w", err)
	}
	defer stmt.Close()
	for v, r := range rank {
		if _, err := stmt.Exec(r, v); err != nil {
			return fmt.Errorf("updating importance for %d: %w", v, err)
		}
	}
	return tx.Commit()
}

// CentralSymbol is one row from CentralSymbols — a symbol record plus its
// computed importance score.
type CentralSymbol struct {
	SymbolRecord
	Importance float64
}

// CentralSymbols returns the top-N symbols by importance, optionally
// restricted by SQL GLOB path filter and kind filter.
func (s *Store) CentralSymbols(pathGlob, kind string, limit int) ([]CentralSymbol, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
	             start_byte, end_byte, importance
	      FROM symbols
	      WHERE importance > 0`
	args := []any{}
	if kind != "" {
		q += " AND kind = ?"
		args = append(args, kind)
	}
	if pathGlob != "" {
		q += " AND file_path GLOB ?"
		args = append(args, pathGlob)
	}
	q += " ORDER BY importance DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("central_symbols: %w", err)
	}
	defer rows.Close()
	var out []CentralSymbol
	for rows.Next() {
		var c CentralSymbol
		var imp sql.NullFloat64
		if err := rows.Scan(
			&c.Name, &c.Kind, &c.FilePath,
			&c.StartLine, &c.StartCol, &c.EndLine, &c.EndCol,
			&c.StartByte, &c.EndByte, &imp,
		); err != nil {
			return nil, fmt.Errorf("scanning central symbol: %w", err)
		}
		c.Importance = imp.Float64
		out = append(out, c)
	}
	return out, rows.Err()
}
