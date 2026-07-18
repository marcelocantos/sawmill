// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"slices"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// runSelectorQuery compiles the depSelectorQuery for the adapter and returns
// the @field captures found in src.
func runSelectorQuery(t *testing.T, adapter adapters.LanguageAdapter, alias string, src []byte) []string {
	t.Helper()
	queryText := depSelectorQuery(adapter, alias)
	if queryText == "" {
		t.Fatal("depSelectorQuery returned empty")
	}
	parser := tree_sitter.NewParser()
	if err := parser.SetLanguage(adapter.Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil || tree.HasError() {
		t.Fatal("sample source failed to parse")
	}
	q, err := tree_sitter.NewQuery(adapter.Language(), queryText)
	if err != nil {
		t.Fatalf("selector query failed to compile: %v\nquery: %s", err, queryText)
	}
	fieldIdx := slices.Index(q.CaptureNames(), "field")
	var fields []string
	cursor := tree_sitter.NewQueryCursor()
	it := cursor.Matches(q, tree.RootNode(), src)
	for m := it.Next(); m != nil; m = it.Next() {
		for _, c := range m.Captures {
			if int(c.Index) == fieldIdx {
				fields = append(fields, string(src[c.Node.StartByte():c.Node.EndByte()]))
			}
		}
	}
	return fields
}

func TestDepSelectorQueryJava(t *testing.T) {
	src := []byte(`class A { void f() { util.helper(); int n = util.count; other.x(); } }`)
	fields := runSelectorQuery(t, &adapters.JavaAdapter{}, "util", src)
	if !slices.Contains(fields, "helper") {
		t.Errorf("expected util.helper capture, got %v", fields)
	}
	if !slices.Contains(fields, "count") {
		t.Errorf("expected util.count capture, got %v", fields)
	}
	if slices.Contains(fields, "x") {
		t.Errorf("captured other.x despite alias filter: %v", fields)
	}
}

func TestDepSelectorQueryCSharp(t *testing.T) {
	src := []byte(`class A { void F() { Util.Helper(); Other.X(); } }`)
	fields := runSelectorQuery(t, &adapters.CSharpAdapter{}, "Util", src)
	if !slices.Contains(fields, "Helper") {
		t.Errorf("expected Util.Helper capture, got %v", fields)
	}
	if slices.Contains(fields, "X") {
		t.Errorf("captured Other.X despite alias filter: %v", fields)
	}
}
