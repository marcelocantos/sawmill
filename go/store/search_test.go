// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"
)

func TestSearchCodeBasic(t *testing.T) {
	s := openTestStore(t)
	mtime := time.Now()
	if err := s.UpsertFile("lib.py", "py", mtime, "h1", nil, "owned"); err != nil {
		t.Fatal(err)
	}
	syms := []SymbolRecord{
		{
			Name: "parseConnectionString", Kind: "function", FilePath: "lib.py",
			StartLine: 1, EndLine: 5, StartByte: 0, EndByte: 100,
			Signature: "def parseConnectionString(s: str) -> Connection:",
			Doc:       "Parse a DSN-style connection string into a Connection object.",
		},
		{
			Name: "ignored_helper", Kind: "function", FilePath: "lib.py",
			StartLine: 7, EndLine: 8, StartByte: 110, EndByte: 200,
			Signature: "def ignored_helper(x): return x",
			Doc:       "",
		},
	}
	if err := s.UpdateSymbols("lib.py", syms); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		query string
		want  string
	}{
		{"parseConnectionString", "parseConnectionString"}, // exact identifier
		{"parse", "parseConnectionString"},                 // camelCase subword
		{"connection", "parseConnectionString"},            // camelCase subword
		{"DSN", "parseConnectionString"},                   // doc text
	}
	for _, c := range cases {
		hits, err := s.SearchCode(c.query, "", "", 5)
		if err != nil {
			t.Fatalf("SearchCode(%q): %v", c.query, err)
		}
		if len(hits) == 0 {
			t.Errorf("SearchCode(%q): no hits", c.query)
			continue
		}
		if hits[0].Name != c.want {
			t.Errorf("SearchCode(%q): top hit %q, want %q", c.query, hits[0].Name, c.want)
		}
	}
}

func TestSearchCodeSnakeCase(t *testing.T) {
	s := openTestStore(t)
	mtime := time.Now()
	s.UpsertFile("lib.rs", "rs", mtime, "h1", nil, "owned")
	s.UpdateSymbols("lib.rs", []SymbolRecord{
		{
			Name: "parse_connection_string", Kind: "function", FilePath: "lib.rs",
			StartLine: 1, EndLine: 3, StartByte: 0, EndByte: 60,
			Signature: "fn parse_connection_string(s: &str) -> Connection",
		},
	})

	// snake_case word must match individual subwords.
	for _, q := range []string{"parse_connection_string", "parse", "connection", "string"} {
		hits, err := s.SearchCode(q, "", "", 5)
		if err != nil {
			t.Fatalf("SearchCode(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Errorf("SearchCode(%q): no hits", q)
		}
	}
}

func TestSearchCodeKindAndPathFilter(t *testing.T) {
	s := openTestStore(t)
	mtime := time.Now()
	s.UpsertFile("a.go", "go", mtime, "h1", nil, "owned")
	s.UpsertFile("b.go", "go", mtime, "h2", nil, "owned")
	s.UpdateSymbols("a.go", []SymbolRecord{
		{Name: "Parse", Kind: "function", FilePath: "a.go", StartLine: 1, EndLine: 1, Signature: "func Parse() {}"},
		{Name: "Parser", Kind: "type", FilePath: "a.go", StartLine: 3, EndLine: 3, Signature: "type Parser struct{}"},
	})
	s.UpdateSymbols("b.go", []SymbolRecord{
		{Name: "Parse", Kind: "function", FilePath: "b.go", StartLine: 1, EndLine: 1, Signature: "func Parse() {}"},
	})

	hits, err := s.SearchCode("parse", "type", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "Parser" {
		t.Errorf("kind filter: expected [Parser], got %v", hits)
	}

	hits, err = s.SearchCode("parse", "", "a.*", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.FilePath != "a.go" {
			t.Errorf("path filter: got %s, want a.go", h.FilePath)
		}
	}
}

func TestSearchCodeUpdateRemovesStaleFTS(t *testing.T) {
	s := openTestStore(t)
	mtime := time.Now()
	s.UpsertFile("lib.go", "go", mtime, "h1", nil, "owned")
	s.UpdateSymbols("lib.go", []SymbolRecord{
		{Name: "OldName", Kind: "function", FilePath: "lib.go", Signature: "func OldName() {}"},
	})

	if hits, _ := s.SearchCode("OldName", "", "", 5); len(hits) == 0 {
		t.Fatal("expected OldName to be searchable")
	}

	s.UpdateSymbols("lib.go", []SymbolRecord{
		{Name: "NewName", Kind: "function", FilePath: "lib.go", Signature: "func NewName() {}"},
	})

	if hits, _ := s.SearchCode("OldName", "", "", 5); len(hits) != 0 {
		t.Errorf("expected OldName to be evicted from FTS after re-index, got %v", hits)
	}
	if hits, _ := s.SearchCode("NewName", "", "", 5); len(hits) == 0 {
		t.Errorf("expected NewName to be searchable")
	}
}

func TestSearchCodeFileRemoveEvictsFTS(t *testing.T) {
	s := openTestStore(t)
	mtime := time.Now()
	s.UpsertFile("lib.go", "go", mtime, "h1", nil, "owned")
	s.UpdateSymbols("lib.go", []SymbolRecord{
		{Name: "Doomed", Kind: "function", FilePath: "lib.go", Signature: "func Doomed() {}"},
	})

	if err := s.RemoveFile("lib.go"); err != nil {
		t.Fatal(err)
	}

	if hits, _ := s.SearchCode("Doomed", "", "", 5); len(hits) != 0 {
		t.Errorf("expected Doomed to be evicted from FTS after file remove, got %v", hits)
	}
}
