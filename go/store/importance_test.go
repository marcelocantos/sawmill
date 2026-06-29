// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
)

func TestRecomputeImportanceRanksCentralFunction(t *testing.T) {
	s := openTestStore(t)

	// Three functions in one file: Helper is called by both other functions.
	// PageRank should rank Helper highest.
	seedFile(t, s, "a.go",
		[]SymbolRecord{
			{Name: "Outer", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
			{Name: "Inner", Kind: "function", FilePath: "a.go", StartByte: 150, EndByte: 200},
			{Name: "Helper", Kind: "function", FilePath: "a.go", StartByte: 300, EndByte: 400},
		},
		[]EdgeRecord{
			{Kind: "call", DstName: "Helper", StartByte: 50, EndByte: 56},
			{Kind: "call", DstName: "Helper", StartByte: 170, EndByte: 176},
		},
	)

	if err := s.RecomputeImportance(); err != nil {
		t.Fatal(err)
	}

	results, err := s.CentralSymbols("", "function", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one ranked symbol")
	}
	// Helper should be ranked first.
	if results[0].Name != "Helper" {
		t.Errorf("top central symbol = %q, want Helper", results[0].Name)
	}
	// Importance is normalised — should be > base rate (1/N).
	if results[0].Importance <= 1.0/3.0 {
		t.Errorf("Helper importance %.4f should exceed base 1/N=%.4f", results[0].Importance, 1.0/3.0)
	}
}

func TestCentralSymbolsPathFilter(t *testing.T) {
	s := openTestStore(t)
	seedFile(t, s, "a.go",
		[]SymbolRecord{
			{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
			{Name: "G", Kind: "function", FilePath: "a.go", StartByte: 150, EndByte: 200},
		},
		[]EdgeRecord{{Kind: "call", DstName: "G", StartByte: 5, EndByte: 10}},
	)
	seedFile(t, s, "b/x.go",
		[]SymbolRecord{
			{Name: "X", Kind: "function", FilePath: "b/x.go", StartByte: 0, EndByte: 100},
			{Name: "Y", Kind: "function", FilePath: "b/x.go", StartByte: 150, EndByte: 200},
		},
		[]EdgeRecord{{Kind: "call", DstName: "Y", StartByte: 5, EndByte: 10}},
	)
	if err := s.RecomputeImportance(); err != nil {
		t.Fatal(err)
	}
	res, err := s.CentralSymbols("b/*", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.FilePath != "b/x.go" {
			t.Errorf("path glob 'b/*' returned %s", r.FilePath)
		}
	}
}

func TestRecomputeImportanceEmptyStore(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecomputeImportance(); err != nil {
		t.Errorf("RecomputeImportance on empty store: %v", err)
	}
}
