// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"
)

func TestUpsertSummaryAndExpand(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "Parse", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100, Signature: "func Parse() error"},
		{Name: "Helper", Kind: "function", FilePath: "a.go", StartByte: 150, EndByte: 200, Signature: "func Helper() int"},
	})
	var parseID, helperID int64
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='Parse'`).Scan(&parseID)
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='Helper'`).Scan(&helperID)

	rec := SummaryRecord{
		SymbolID:    parseID,
		Summary:     "Parses input and returns an error.",
		PromptID:    "v1",
		ModelID:     "haiku",
		CostUSD:     0.0001,
		Tokens:      120,
		GeneratedAt: time.Now(),
	}
	edges := []KGEdgeRecord{
		{SrcSymbolID: parseID, DstName: "Helper", Kind: "calls", Confidence: 0.95, PromptID: "v1"},
		{SrcSymbolID: parseID, DstName: "ParseError", Kind: "throws", Confidence: 0.8, PromptID: "v1"},
	}
	if err := s.UpsertSummary(rec, edges); err != nil {
		t.Fatal(err)
	}

	got, ok := s.SummaryByID(parseID, "v1")
	if !ok || got.Summary != rec.Summary {
		t.Errorf("SummaryByID: got %v, want %v", got, rec)
	}

	// Forward KG expansion from Parse should include Helper (calls).
	fwd, err := s.ExpandKGForward("Parse", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(fwd) != 2 {
		t.Errorf("expected 2 KG edges out of Parse, got %d", len(fwd))
	}
	// The "calls Helper" edge should resolve dst.
	found := false
	for _, e := range fwd {
		if e.DstName == "Helper" && e.DstFile != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected resolved Helper destination among %v", fwd)
	}

	// Reverse — Helper has one inbound call edge from Parse.
	rev, _ := s.ExpandKGReverse("Helper", "calls")
	if len(rev) != 1 {
		t.Errorf("expected 1 caller of Helper, got %d", len(rev))
	}
}

func TestUpsertSummaryReplacesEdgesForPrompt(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
	})
	var fID int64
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='F'`).Scan(&fID)

	rec := SummaryRecord{SymbolID: fID, Summary: "old", PromptID: "v1", GeneratedAt: time.Now()}
	s.UpsertSummary(rec, []KGEdgeRecord{{SrcSymbolID: fID, DstName: "Old", Kind: "calls", Confidence: 0.9, PromptID: "v1"}})

	rec.Summary = "new"
	s.UpsertSummary(rec, []KGEdgeRecord{{SrcSymbolID: fID, DstName: "New", Kind: "calls", Confidence: 0.9, PromptID: "v1"}})

	fwd, _ := s.ExpandKGForward("F", "")
	if len(fwd) != 1 || fwd[0].DstName != "New" {
		t.Errorf("expected only New after replace, got %v", fwd)
	}

	got, _ := s.SummaryByID(fID, "v1")
	if got.Summary != "new" {
		t.Errorf("expected updated summary, got %q", got.Summary)
	}
}

func TestSummaryStatusReportsCounts(t *testing.T) {
	s := openTestStore(t)
	s.UpsertFile("a.go", "go", time.Now(), "h", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
	})
	var fID int64
	s.db.QueryRow(`SELECT id FROM symbols WHERE name='F'`).Scan(&fID)

	// One v1 summary; one v0 (stale) summary.
	s.UpsertSummary(SummaryRecord{SymbolID: fID, Summary: "x", PromptID: "v1", CostUSD: 0.01, Tokens: 50, GeneratedAt: time.Now()}, nil)

	st, err := s.SummaryStatusForPrompt("v1")
	if err != nil {
		t.Fatal(err)
	}
	if st.SummariesCurrent != 1 {
		t.Errorf("current = %d, want 1", st.SummariesCurrent)
	}
	if st.TotalCostUSD <= 0 {
		t.Errorf("expected positive cost, got %v", st.TotalCostUSD)
	}
	if err := s.RecordSummaryFailure(fID, "v1", "parse error", 0); err != nil {
		t.Fatal(err)
	}
	st, _ = s.SummaryStatusForPrompt("v1")
	if st.Failures != 1 {
		t.Errorf("failures = %d, want 1", st.Failures)
	}
}
