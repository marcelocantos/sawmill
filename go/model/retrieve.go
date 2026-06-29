// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"context"
	"sort"

	"github.com/marcelocantos/sawmill/embed"
	"github.com/marcelocantos/sawmill/store"
)

// SemanticHit is one fused result from SemanticSearch. The `Why` field lists
// which signals contributed to its rank, so the agent (and the user) can
// see *why* a hit surfaced and debug surprising results.
type SemanticHit struct {
	store.SymbolRecord
	Score float64
	Why   []string // any of "bm25", "vec", "graph"
}

// SemanticSearch fuses three signals over the discovery index:
//
//	BM25  — from the FTS5 search_code path
//	Cosine — from the in-memory vector index (Embedder + Vecs)
//	Graph  — 1-hop reverse expansion (symbols referenced from a BM25/vec hit
//	         get a bump, so a query about callers surfaces the called
//	         function too).
//
// Fusion is reciprocal-rank-fusion (RRF): score(s) = sum 1/(k + rank_i(s))
// across signals where s appears, k=60 by convention. RRF is simple,
// scale-invariant, and easy to debug — every component contributes
// at-most-one term per hit.
func (m *CodebaseModel) SemanticSearch(ctx context.Context, query, kind, pathGlob string, limit int) ([]SemanticHit, error) {
	if limit <= 0 {
		limit = 20
	}
	const rrfK = 60.0
	const candidateLimit = 200 // pull more candidates per signal than we'll return

	// Signal 1: BM25. A malformed FTS5 query shouldn't sink the whole
	// retrieval — vec + graph can still produce hits — so on error we drop
	// the BM25 contribution and continue.
	bmHits, err := m.Store.SearchCode(query, kind, pathGlob, candidateLimit)
	if err != nil {
		bmHits = nil
	}

	// Signal 2: cosine over the in-memory vector index. Skipped if no
	// embedder is configured.
	var vecHits []store.SearchHit
	if m.Embedder != nil {
		vecHits, err = m.cosineSearch(ctx, query, kind, pathGlob, candidateLimit)
		if err != nil {
			// Don't fail the whole search — log via score=0 fallback.
			vecHits = nil
		}
	}

	// Map each symbol id to its rank in each signal (1-based).
	type signalRanks struct {
		bm25  int
		vec   int
		graph int
		rec   store.SymbolRecord
	}
	ranks := map[int64]*signalRanks{}
	upsert := func(id int64, rec store.SymbolRecord) *signalRanks {
		r, ok := ranks[id]
		if !ok {
			r = &signalRanks{rec: rec}
			ranks[id] = r
		}
		return r
	}

	for i, h := range bmHits {
		r := upsert(h.ID, h.SymbolRecord)
		r.bm25 = i + 1
	}
	for i, h := range vecHits {
		r := upsert(h.ID, h.SymbolRecord)
		r.vec = i + 1
	}

	// Signal 3: graph expand. For every BM25/vec hit that's a function/type,
	// pull its reverse expansion — symbols that reference it. These get a
	// rank too, since "the caller of a relevant function is probably
	// relevant".
	graphRank := 1
	seenGraph := map[int64]bool{}
	for _, h := range bmHits {
		if graphRank > candidateLimit {
			break
		}
		neighbours, err := m.Store.ExpandReverse(h.Name, "", "call")
		if err != nil {
			continue
		}
		for _, e := range neighbours {
			// Resolve src to a symbol id via a name lookup.
			ids := m.lookupSymbolIDs(e.SrcName, "")
			for _, id := range ids {
				if seenGraph[id] {
					continue
				}
				seenGraph[id] = true
				r := upsert(id, store.SymbolRecord{})
				r.graph = graphRank
				graphRank++
			}
		}
	}

	// Compute fused score.
	var fused []SemanticHit
	for id, r := range ranks {
		score := 0.0
		var why []string
		if r.bm25 > 0 {
			score += 1.0 / (rrfK + float64(r.bm25))
			why = append(why, "bm25")
		}
		if r.vec > 0 {
			score += 1.0 / (rrfK + float64(r.vec))
			why = append(why, "vec")
		}
		if r.graph > 0 {
			score += 1.0 / (rrfK + float64(r.graph))
			why = append(why, "graph")
		}
		// If only graph fired, the hit came from a neighbour and we may not
		// have its symbol record cached — fill it in.
		if r.rec.Name == "" {
			rec, ok := m.symbolByID(id)
			if !ok {
				continue
			}
			r.rec = rec
		}
		fused = append(fused, SemanticHit{
			SymbolRecord: r.rec,
			Score:        score,
			Why:          why,
		})
	}

	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Score > fused[j].Score
	})
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused, nil
}

// cosineSearch ranks symbols by cosine similarity between the query
// embedding and the stored symbol vectors. Optional kind/pathGlob filters
// are applied AFTER ranking (so the filter doesn't accidentally shrink the
// candidate pool and lose recall).
func (m *CodebaseModel) cosineSearch(ctx context.Context, query, kind, pathGlob string, limit int) ([]store.SearchHit, error) {
	if m.Embedder == nil {
		return nil, nil
	}
	m.vecsMu.RLock()
	vecs := m.Vecs
	m.vecsMu.RUnlock()
	if len(vecs) == 0 {
		return nil, nil
	}

	q, err := m.Embedder.Embed(ctx, []string{query})
	if err != nil || len(q) == 0 {
		return nil, err
	}
	queryVec := q[0]

	type idScore struct {
		id    int64
		score float32
	}
	scored := make([]idScore, 0, len(vecs))
	for id, v := range vecs {
		scored = append(scored, idScore{id: id, score: embed.Cosine(queryVec, v)})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// Take the top 4× candidate limit before filtering so we don't lose
	// good-but-filtered matches.
	cap := limit * 4
	if cap > len(scored) {
		cap = len(scored)
	}
	scored = scored[:cap]

	out := make([]store.SearchHit, 0, cap)
	for _, s := range scored {
		rec, ok := m.symbolByID(s.id)
		if !ok {
			continue
		}
		if kind != "" && rec.Kind != kind {
			continue
		}
		if pathGlob != "" && !globMatch(pathGlob, rec.FilePath) {
			continue
		}
		out = append(out, store.SearchHit{SymbolRecord: rec, Score: float64(s.score)})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// symbolByID looks up one symbol row through the store.
func (m *CodebaseModel) symbolByID(id int64) (store.SymbolRecord, bool) {
	return m.Store.SymbolByID(id)
}

// lookupSymbolIDs returns up to 8 symbol ids matching name and optional
// kind. Used during graph expansion when only the name is known.
func (m *CodebaseModel) lookupSymbolIDs(name, kind string) []int64 {
	ids, _ := m.Store.SymbolIDsByName(name, kind, 8)
	return ids
}

// globMatch is a tiny SQL-GLOB-compatible matcher (just '*' and literal
// segments). Real SQL evaluates GLOB internally; this is for in-Go
// filtering of cosine results.
func globMatch(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	// Simple "*"-only glob: split by '*' and ensure each segment appears in
	// order within s.
	parts := splitStar(pattern)
	pos := 0
	for i, p := range parts {
		if p == "" {
			continue
		}
		idx := indexFrom(s, p, pos)
		if idx < 0 {
			return false
		}
		// First part must anchor at start unless pattern starts with '*'.
		if i == 0 && pattern[0] != '*' && idx != 0 {
			return false
		}
		pos = idx + len(p)
	}
	// Last part must extend to end unless pattern ends with '*'.
	if last := parts[len(parts)-1]; last != "" && pattern[len(pattern)-1] != '*' && pos != len(s) {
		return false
	}
	return true
}

func splitStar(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '*' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func indexFrom(s, sub string, start int) int {
	if start > len(s) {
		return -1
	}
	if sub == "" {
		return start
	}
	for i := start; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
