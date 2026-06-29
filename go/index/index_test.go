// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

// extractAll parses src as language ext and returns extracted symbols.
func extractAll(t *testing.T, ext string, src []byte) []Symbol {
	t.Helper()
	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		t.Fatalf("no adapter for %s", ext)
	}
	tree, err := forest.ParseSource(src, adapter)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Close()
	return ExtractSymbolsFromParts(src, tree, adapter, "test."+ext)
}

func TestExtractSignatureAndDocGo(t *testing.T) {
	src := []byte(`package main

// Parser reads tokens off a stream and emits AST nodes.
// It is safe for concurrent use.
type Parser struct{}

// Parse runs the parser over input and returns the AST root.
func Parse(input string) error {
	return nil
}
`)
	syms := extractAll(t, "go", src)
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	p, ok := byName["Parse"]
	if !ok {
		t.Fatalf("no Parse symbol in %v", byName)
	}
	if !strings.HasPrefix(p.Signature, "func Parse(input string)") {
		t.Errorf("Parse.Signature = %q, want a 'func Parse(...)' prefix", p.Signature)
	}
	if !strings.Contains(p.Doc, "Parse runs the parser") {
		t.Errorf("Parse.Doc = %q, want it to capture the leading // comment", p.Doc)
	}

	pt, ok := byName["Parser"]
	if !ok {
		t.Fatalf("no Parser symbol")
	}
	if !strings.Contains(pt.Doc, "Parser reads tokens") {
		t.Errorf("Parser.Doc = %q, want multi-line leading comment", pt.Doc)
	}
	if !strings.Contains(pt.Doc, "safe for concurrent use") {
		t.Errorf("Parser.Doc = %q, want it to include the second comment line", pt.Doc)
	}
}

func TestExtractDocPython(t *testing.T) {
	src := []byte(`# Compute the sum of the inputs.
# Returns 0 when inputs is empty.
def compute(inputs):
    return sum(inputs)
`)
	syms := extractAll(t, "py", src)
	if len(syms) == 0 {
		t.Fatal("no symbols extracted")
	}
	var compute *Symbol
	for i := range syms {
		if syms[i].Name == "compute" {
			compute = &syms[i]
			break
		}
	}
	if compute == nil {
		t.Fatalf("no compute symbol")
	}
	if !strings.Contains(compute.Doc, "Compute the sum") {
		t.Errorf("Doc = %q, want it to capture leading # comments", compute.Doc)
	}
	if !strings.HasPrefix(compute.Signature, "def compute(inputs)") {
		t.Errorf("Signature = %q, want 'def compute(inputs)' prefix", compute.Signature)
	}
}

func TestExtractNoDocAboveCallSite(t *testing.T) {
	src := []byte(`package main

func main() {
	// this comment shouldn't become a "doc" of the call below
	doStuff()
}

func doStuff() {}
`)
	syms := extractAll(t, "go", src)
	for _, s := range syms {
		if s.Kind == "call" && s.Doc != "" {
			t.Errorf("call site %s should not carry doc text, got %q", s.Name, s.Doc)
		}
	}
}
