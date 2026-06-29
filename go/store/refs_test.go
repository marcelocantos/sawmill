// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"
)

// seedFile registers a file row and the given symbols + edges.
func seedFile(t *testing.T, s *Store, path string, syms []SymbolRecord, edges []EdgeRecord) {
	t.Helper()
	if err := s.UpsertFile(path, "go", time.Now(), "h", nil, "owned"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSymbols(path, syms); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRefs(path, edges); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefsContainment(t *testing.T) {
	s := openTestStore(t)

	// Two functions in a.go; calls inside their byte ranges should resolve
	// to the enclosing function.
	seedFile(t, s, "a.go",
		[]SymbolRecord{
			{Name: "Outer", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
			{Name: "Inner", Kind: "function", FilePath: "a.go", StartByte: 150, EndByte: 200},
			{Name: "Helper", Kind: "function", FilePath: "a.go", StartByte: 300, EndByte: 400},
		},
		[]EdgeRecord{
			{Kind: "call", DstName: "Helper", StartByte: 50, EndByte: 56, StartLine: 5, StartCol: 1},
			{Kind: "call", DstName: "Helper", StartByte: 170, EndByte: 176, StartLine: 12, StartCol: 1},
			{Kind: "call", DstName: "topLevel", StartByte: 500, EndByte: 510, StartLine: 30, StartCol: 1}, // outside any function
		},
	)

	// Forward: Outer should have one call edge to Helper.
	out, err := s.ExpandForward("Outer", "", "call")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].DstName != "Helper" {
		t.Errorf("Outer forward: got %v, want one call to Helper", out)
	}

	// Reverse: Helper should have two callers, both resolving src to Outer/Inner.
	in, err := s.ExpandReverse("Helper", "", "call")
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 2 {
		t.Fatalf("Helper reverse: got %d edges, want 2", len(in))
	}
	seenSrc := map[string]bool{}
	for _, e := range in {
		seenSrc[e.SrcName] = true
		if e.DstFile != "a.go" || e.DstKind != "function" {
			t.Errorf("expected resolved dst (a.go, function), got file=%q kind=%q", e.DstFile, e.DstKind)
		}
	}
	if !seenSrc["Outer"] || !seenSrc["Inner"] {
		t.Errorf("expected callers {Outer, Inner}, got %v", seenSrc)
	}

	// The top-level (file-scope) call to topLevel should appear with no src.
	in, err = s.ExpandReverse("topLevel", "", "call")
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 {
		t.Fatalf("topLevel reverse: got %d, want 1", len(in))
	}
	if in[0].SrcName != "" {
		t.Errorf("expected empty src for top-level call, got %q", in[0].SrcName)
	}
}

func TestUpdateRefsReplaceClears(t *testing.T) {
	s := openTestStore(t)
	seedFile(t, s, "a.go",
		[]SymbolRecord{{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100}},
		[]EdgeRecord{{Kind: "call", DstName: "Old", StartByte: 5, EndByte: 10}},
	)

	// Re-index with different edges: Old must disappear, New must appear.
	if err := s.UpdateRefs("a.go", []EdgeRecord{
		{Kind: "call", DstName: "New", StartByte: 5, EndByte: 10},
	}); err != nil {
		t.Fatal(err)
	}

	in, _ := s.ExpandReverse("Old", "", "")
	if len(in) != 0 {
		t.Errorf("Old should be evicted, got %v", in)
	}
	in, _ = s.ExpandReverse("New", "", "")
	if len(in) != 1 {
		t.Errorf("New should be present, got %v", in)
	}
}

func TestUpdateRefsFileCascade(t *testing.T) {
	s := openTestStore(t)
	seedFile(t, s, "a.go",
		[]SymbolRecord{{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100}},
		[]EdgeRecord{{Kind: "call", DstName: "G", StartByte: 5, EndByte: 10}},
	)

	if err := s.RemoveFile("a.go"); err != nil {
		t.Fatal(err)
	}

	in, _ := s.ExpandReverse("G", "", "")
	if len(in) != 0 {
		t.Errorf("file removal should cascade-delete refs, got %v", in)
	}
}

func TestExpandKindFilters(t *testing.T) {
	s := openTestStore(t)
	seedFile(t, s, "a.go",
		[]SymbolRecord{
			{Name: "F", Kind: "function", FilePath: "a.go", StartByte: 0, EndByte: 100},
			{Name: "T", Kind: "type", FilePath: "a.go", StartByte: 200, EndByte: 220},
		},
		[]EdgeRecord{
			{Kind: "call", DstName: "G", StartByte: 5, EndByte: 10},
			{Kind: "type_use", DstName: "T", StartByte: 50, EndByte: 55},
			{Kind: "import_use", DstName: "fmt", StartByte: 80, EndByte: 90},
		},
	)

	out, _ := s.ExpandForward("F", "", "call")
	if len(out) != 1 || out[0].Kind != "call" {
		t.Errorf("call filter: got %v", out)
	}
	out, _ = s.ExpandForward("F", "", "type_use")
	if len(out) != 1 || out[0].DstName != "T" {
		t.Errorf("type_use filter: got %v", out)
	}
	// type_use edge to T should resolve dst.
	if out[0].DstFile != "a.go" || out[0].DstKind != "type" {
		t.Errorf("expected resolved type dst, got file=%q kind=%q", out[0].DstFile, out[0].DstKind)
	}
}
