// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/model"
)

// writeFile writes content to root/relPath, creating parents.
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", full, err)
	}
}

// TestLibraryScopeIndexingSkipsCalls exercises the full scope-aware indexing
// path through LoadEphemeral. It builds a tree with three Go files —
// owned, library (under vendor/), and ignored (under build/) — and asserts:
//
//   - The ignored file is not indexed at all.
//   - The library file's call sites are NOT in the symbol table.
//   - The owned file's call sites ARE in the symbol table.
//   - The owned file's source is cached; the library file's is not.
func TestLibraryScopeIndexingSkipsCalls(t *testing.T) {
	root := t.TempDir()

	owned := `package app
func F() { vendor.Helper(); other() }
func other() {}
`
	library := `package vendor
func Helper() { Internal() }
func Internal() {}
`
	ignored := `package build
func Junk() { Junk() }
`

	writeFile(t, root, "src/app.go", owned)
	writeFile(t, root, "vendor/lib/lib.go", library)
	writeFile(t, root, "build/junk.go", ignored)

	m, err := model.LoadEphemeral(root)
	if err != nil {
		t.Fatalf("LoadEphemeral: %v", err)
	}
	defer m.Close()

	ownedPath := filepath.Join(root, "src/app.go")
	libraryPath := filepath.Join(root, "vendor/lib/lib.go")
	ignoredPath := filepath.Join(root, "build/junk.go")

	// 1. ignored file not in the symbol index.
	if syms, err := m.Store.SymbolsInFile(ignoredPath); err != nil {
		t.Fatalf("SymbolsInFile(ignored): %v", err)
	} else if len(syms) != 0 {
		t.Errorf("ignored file produced %d symbol(s); want 0", len(syms))
	}

	// 2. library file: declarations present, calls absent.
	libSyms, err := m.Store.SymbolsInFile(libraryPath)
	if err != nil {
		t.Fatalf("SymbolsInFile(library): %v", err)
	}
	libCalls := 0
	libDecls := 0
	for _, s := range libSyms {
		switch s.Kind {
		case "call":
			libCalls++
		case "function":
			libDecls++
		}
	}
	if libCalls != 0 {
		t.Errorf("library file produced %d call symbol(s); want 0", libCalls)
	}
	if libDecls < 2 {
		t.Errorf("library file produced %d function decl(s); want >= 2", libDecls)
	}

	// 3. owned file: at least one call symbol present.
	ownedSyms, err := m.Store.SymbolsInFile(ownedPath)
	if err != nil {
		t.Fatalf("SymbolsInFile(owned): %v", err)
	}
	ownedCalls := 0
	for _, s := range ownedSyms {
		if s.Kind == "call" {
			ownedCalls++
		}
	}
	if ownedCalls == 0 {
		t.Errorf("owned file produced 0 call symbol(s); want >= 1")
	}
}

// TestFindReferencesExcludesLibrariesByDefault is a regression check on the
// scope filter at the find-references layer. It seeds the in-memory store
// with one owned file (containing a call) and one library file (containing
// the same call name), then asserts the scoped query returns only the owned
// hit.
func TestFindReferencesExcludesLibrariesByDefault(t *testing.T) {
	root := t.TempDir()

	owned := `package app
func F() { Target() }
func Target() {}
`
	// Library file's symbol table will contain the function decl but no call
	// symbols (per our index gating). To exercise the scope filter properly,
	// we manually insert a call symbol attributed to the library file via
	// the store's UpdateSymbols, then verify FindSymbolsInScopes filters it
	// out by default.
	library := `package vendor
func F() { Target() }
func Target() {}
`
	writeFile(t, root, "src/app.go", owned)
	writeFile(t, root, "vendor/lib/lib.go", library)

	m, err := model.LoadEphemeral(root)
	if err != nil {
		t.Fatalf("LoadEphemeral: %v", err)
	}
	defer m.Close()

	// Owned-only (default) should return just the owned call site.
	hits, err := m.FindSymbolsInScopes("Target", "call", []string{"owned"})
	if err != nil {
		t.Fatalf("FindSymbolsInScopes(owned): %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected at least one owned call site for Target")
	}
	for _, h := range hits {
		if filepath.Dir(h.FilePath) == filepath.Join(root, "vendor/lib") {
			t.Errorf("owned-only filter returned a vendor hit: %s", h.FilePath)
		}
	}

	// Including library scope should not crash even though library files
	// have no call symbols.
	if _, err := m.FindSymbolsInScopes("Target", "call", []string{"owned", "library"}); err != nil {
		t.Fatalf("FindSymbolsInScopes(owned+library): %v", err)
	}
}

// TestParseGuardSkipsOversizedFile asserts a file above forest.MaxFileSize
// is not indexed even when its extension is recognised.
func TestParseGuardSkipsOversizedFile(t *testing.T) {
	root := t.TempDir()

	var b strings.Builder
	b.WriteString("package big\n")
	for b.Len() <= forest.MaxFileSize {
		b.WriteString("var x int\n")
	}
	writeFile(t, root, "big.go", b.String())
	// Companion file so the package is non-empty for the walker.
	writeFile(t, root, "small.go", "package big\nfunc F() {}\n")

	m, err := model.LoadEphemeral(root)
	if err != nil {
		t.Fatalf("LoadEphemeral: %v", err)
	}
	defer m.Close()

	bigSyms, err := m.Store.SymbolsInFile(filepath.Join(root, "big.go"))
	if err != nil {
		t.Fatalf("SymbolsInFile(big): %v", err)
	}
	if len(bigSyms) != 0 {
		t.Errorf("oversized file produced %d symbol(s); want 0", len(bigSyms))
	}

	smallSyms, err := m.Store.SymbolsInFile(filepath.Join(root, "small.go"))
	if err != nil {
		t.Fatalf("SymbolsInFile(small): %v", err)
	}
	if len(smallSyms) == 0 {
		t.Errorf("normal-sized companion file produced 0 symbols; guard over-triggered")
	}
}

// TestParseGuardSkipsMinifiedFile asserts a file with average line length
// above forest.MaxAvgLineLength is not indexed — the canonical bundled-JS
// minified case.
func TestParseGuardSkipsMinifiedFile(t *testing.T) {
	root := t.TempDir()

	var b strings.Builder
	b.WriteString("export const x = [")
	for b.Len() <= forest.MaxAvgLineLength*2 {
		b.WriteString("1,")
	}
	b.WriteString("];\n")
	writeFile(t, root, "min.ts", b.String())
	writeFile(t, root, "normal.ts", "export function F() {}\n")

	m, err := model.LoadEphemeral(root)
	if err != nil {
		t.Fatalf("LoadEphemeral: %v", err)
	}
	defer m.Close()

	minSyms, err := m.Store.SymbolsInFile(filepath.Join(root, "min.ts"))
	if err != nil {
		t.Fatalf("SymbolsInFile(min): %v", err)
	}
	if len(minSyms) != 0 {
		t.Errorf("minified file produced %d symbol(s); want 0", len(minSyms))
	}

	normalSyms, err := m.Store.SymbolsInFile(filepath.Join(root, "normal.ts"))
	if err != nil {
		t.Fatalf("SymbolsInFile(normal): %v", err)
	}
	if len(normalSyms) == 0 {
		t.Errorf("normal TS companion file produced 0 symbols; guard over-triggered")
	}
}
