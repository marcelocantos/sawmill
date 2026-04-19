// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// handleMigratePattern implements the migrate_pattern MCP tool (🎯T8.3) —
// generalised structural rewriting with explicit add/drop import semantics.
//
// Differences from apply_equivalence:
//   - One-shot: take old → new (no bidirectional pair); no need to teach a
//     persistent equivalence first.
//   - Optional add_import is added to every file that was actually rewritten.
//   - Optional drop_import is removed from each rewritten file iff the
//     import's symbol is no longer referenced anywhere else in the file.
func (h *Handler) handleMigratePattern(args map[string]any) (string, bool, error) {
	oldPatternStr, err := requireString(args, "old_pattern")
	if err != nil {
		return err.Error(), true, nil
	}
	newPatternStr, err := requireString(args, "new_pattern")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	addImport := optString(args, "add_import")
	dropImport := optString(args, "drop_import")
	format := optBool(args, "format")

	if oldPatternStr == newPatternStr {
		return "old_pattern and new_pattern must differ", true, nil
	}

	srcPat := ParsePattern(oldPatternStr)

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	var changes []forest.FileChange
	var diffs []string
	totalRewrites := 0
	importsAdded := 0
	importsDropped := 0

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			adapter := acc.Adapter()
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: source,
				Tree:           tree,
				Adapter:        adapter,
			}

			matches := findEquivalenceMatches(pf, srcPat, newPatternStr)
			if len(matches) == 0 {
				return nil
			}

			edits := equivalenceEdits(matches)
			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
			}

			added := false
			dropped := false

			if addImport != "" {
				if updated, didAdd := addImportToSource(newSource, adapter, addImport); didAdd {
					newSource = updated
					added = true
				}
			}
			if dropImport != "" {
				if updated, didDrop := dropImportFromSourceIfUnused(newSource, adapter, dropImport); didDrop {
					newSource = updated
					dropped = true
				}
			}

			if format {
				newSource, _ = rewrite.FormatSource(adapter, newSource)
			}

			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			totalRewrites += len(matches)
			if added {
				importsAdded++
			}
			if dropped {
				importsDropped++
			}
			return nil
		})
		if err != nil {
			return fmt.Sprintf("processing %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return "Pattern not found in scope.", false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Migrated pattern in %d file(s) (%d rewrite(s); %d import(s) added, %d dropped). Call apply to write.\n\n",
		len(changes), totalRewrites, importsAdded, importsDropped)
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// addImportToSource splices an import line into source after the file's
// preamble (package + existing imports), if not already present. Returns
// (new_source, added).
func addImportToSource(source []byte, adapter adapters.LanguageAdapter, importPath string) ([]byte, bool) {
	importLine := adapter.GenImport(importPath)
	if bytes.Contains(source, []byte(importLine)) {
		return source, false
	}
	pos := findImportInsertionPos(source, adapter)
	out := make([]byte, 0, len(source)+len(importLine))
	out = append(out, source[:pos]...)
	out = append(out, importLine...)
	out = append(out, source[pos:]...)
	return out, true
}

// dropImportFromSourceIfUnused removes the import line if its referenced
// symbol no longer appears elsewhere in the file. Returns
// (new_source, dropped). The "symbol" is the trailing path component of
// importPath — sufficient for the common case (e.g. "fmt" → look for "fmt.")
// without parsing per-language import grammars.
func dropImportFromSourceIfUnused(source []byte, adapter adapters.LanguageAdapter, importPath string) ([]byte, bool) {
	importLine := adapter.GenImport(importPath)
	idx := bytes.Index(source, []byte(importLine))
	if idx < 0 {
		return source, false
	}
	// Source with the import line excised — used for the "still referenced?" check.
	without := make([]byte, 0, len(source)-len(importLine))
	without = append(without, source[:idx]...)
	without = append(without, source[idx+len(importLine):]...)

	symbol := lastImportPathComponent(importPath)
	if symbol != "" && containsIdentifierToken(without, symbol) {
		return source, false
	}
	return without, true
}

// findImportInsertionPos returns the byte offset at which to insert a new
// import line. Defaults to the start of the file; for Go, after the package
// declaration line.
func findImportInsertionPos(source []byte, adapter adapters.LanguageAdapter) uint {
	if _, ok := adapter.(*adapters.GoAdapter); ok {
		// After "package X\n".
		if nl := bytes.IndexByte(source, '\n'); nl >= 0 {
			return uint(nl + 1)
		}
	}
	return 0
}

// lastImportPathComponent returns the trailing identifier component of an
// import path (e.g. "encoding/json" → "json", "std::env" → "env",
// "react" → "react"). Heuristic, supports the common path separators.
func lastImportPathComponent(importPath string) string {
	for _, sep := range []string{"::", "/", ".", "\\"} {
		if i := strings.LastIndex(importPath, sep); i >= 0 {
			return importPath[i+len(sep):]
		}
	}
	return importPath
}

// containsIdentifierToken reports whether token appears in source as a
// complete identifier (bounded by non-identifier characters). A weaker
// form of grepping that avoids matching inside other words.
var identTokenCache = map[string]*regexp.Regexp{}

func containsIdentifierToken(source []byte, token string) bool {
	re, ok := identTokenCache[token]
	if !ok {
		re = regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(token) + `($|[^A-Za-z0-9_])`)
		identTokenCache[token] = re
	}
	return re.Match(source)
}
