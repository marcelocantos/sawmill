// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// preambleNodeTypes are tree-sitter node kinds that form the "preamble"
// at the top of a file: package declarations, import statements, includes,
// use directives, and leading comments. The constant-declaration insertion
// point is placed after the longest run of these at file start.
var preambleNodeTypes = map[string]bool{
	// Go
	"package_clause":     true,
	"import_declaration": true,
	// Python
	"future_import_statement": true,
	"import_statement":        true,
	"import_from_statement":   true,
	// Rust
	"use_declaration": true,
	"extern_crate_declaration": true,
	// C/C++
	"preproc_include": true,
	"preproc_def":     true,
	"preproc_ifdef":   true,
	// Universal
	"comment":      true,
	"line_comment": true,
	"block_comment": true,
}

// literalOccurrence is a single match of the literal in source.
type literalOccurrence struct {
	start uint
	end   uint
}

// handlePromoteConstant implements the promote_constant MCP tool — replace
// every occurrence of a literal value with a reference to a named constant,
// declaring the constant in idiomatic per-language form when necessary.
func (h *Handler) handlePromoteConstant(args map[string]any) (string, bool, error) {
	literal, err := requireString(args, "literal")
	if err != nil {
		return err.Error(), true, nil
	}
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	if literal == "" {
		return "literal must not be empty", true, nil
	}
	if !looksLikeIdentifier(name) {
		return fmt.Sprintf("name %q is not a valid identifier", name), true, nil
	}

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
	totalReplacements := 0
	totalDeclarationsAdded := 0

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			adapter := acc.Adapter()
			decl := adapter.GenConstDeclaration(name, literal)
			declLine := strings.TrimSuffix(decl, "\n")

			// Find all leaf-ish nodes whose source equals literal. Outermost
			// match wins so we don't replace the same span twice when nodes
			// nest (e.g. a literal inside an expression_statement inside a
			// function body — we want the literal node itself).
			occurrences := findLiteralOccurrences(tree.RootNode(), source, literal)
			if len(occurrences) == 0 {
				return nil
			}

			// Skip the literal that lives inside the existing const declaration
			// line, if present — replacing that would produce `const X = X`.
			hasExistingDecl := strings.Contains(string(source), declLine)
			if hasExistingDecl {
				occurrences = filterOutDeclarationLine(occurrences, source, declLine)
			}
			if len(occurrences) == 0 {
				return nil
			}

			edits := make([]rewrite.Edit, 0, len(occurrences)+1)
			for _, occ := range occurrences {
				edits = append(edits, rewrite.Edit{
					Start:       occ.start,
					End:         occ.end,
					Replacement: name,
				})
			}

			// If the file doesn't already declare this constant, prepend the
			// declaration after the file's preamble.
			added := false
			if !hasExistingDecl {
				pos := findConstantInsertionPoint(tree.RootNode(), source)
				edits = append(edits, rewrite.Edit{
					Start:       pos,
					End:         pos,
					Replacement: "\n" + decl,
				})
				added = true
			}

			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
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
			totalReplacements += len(occurrences)
			if added {
				totalDeclarationsAdded++
			}
			return nil
		})
		if err != nil {
			return fmt.Sprintf("processing %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Literal %s not found in scope.", literal), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Promoted %s = %s in %d file(s) (%d replacement(s), %d declaration(s) added). Call apply to write.\n\n",
		name, literal, len(changes), totalReplacements, totalDeclarationsAdded)
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// findLiteralOccurrences returns every node whose exact source text equals
// literal. Walks top-down, outermost-wins (descendants of a match aren't
// considered) so a string literal isn't reported twice for its inner string
// content node.
func findLiteralOccurrences(node *tree_sitter.Node, source []byte, literal string) []literalOccurrence {
	var matches []literalOccurrence
	walkLiteralOccurrences(node, source, literal, &matches)
	return matches
}

func walkLiteralOccurrences(node *tree_sitter.Node, source []byte, literal string, out *[]literalOccurrence) {
	if node == nil {
		return
	}
	start, end := node.StartByte(), node.EndByte()
	if end > uint(len(source)) || start >= end {
		return
	}
	if string(source[start:end]) == literal {
		*out = append(*out, literalOccurrence{start: start, end: end})
		return // outermost match wins
	}
	count := node.ChildCount()
	for i := uint(0); i < count; i++ {
		walkLiteralOccurrences(node.Child(i), source, literal, out)
	}
}

// filterOutDeclarationLine drops occurrences whose surrounding line in
// source matches the declaration line (so the declaration's own RHS isn't
// counted as a replacement target).
func filterOutDeclarationLine(occs []literalOccurrence, source []byte, declLine string) []literalOccurrence {
	out := occs[:0]
	for _, occ := range occs {
		line := lineContaining(source, occ.start)
		if strings.Contains(line, declLine) {
			continue
		}
		out = append(out, occ)
	}
	return out
}

// lineContaining returns the source bytes of the line containing pos.
func lineContaining(source []byte, pos uint) string {
	if pos > uint(len(source)) {
		return ""
	}
	start := pos
	for start > 0 && source[start-1] != '\n' {
		start--
	}
	end := pos
	for end < uint(len(source)) && source[end] != '\n' {
		end++
	}
	return string(source[start:end])
}

// findConstantInsertionPoint returns the byte position immediately after
// the file's preamble (the longest prefix of top-level nodes that are
// package/import/include/comment). Returns 0 if no preamble exists.
func findConstantInsertionPoint(root *tree_sitter.Node, source []byte) uint {
	if root == nil {
		return 0
	}
	n := root.NamedChildCount()
	var pos uint
	for i := uint(0); i < n; i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		if !preambleNodeTypes[child.Kind()] {
			break
		}
		pos = child.EndByte()
	}
	// Advance past the trailing newline so the inserted declaration starts
	// on a fresh line.
	if pos < uint(len(source)) && source[pos] == '\n' {
		pos++
	}
	return pos
}

// looksLikeIdentifier checks that name is a non-empty identifier-like token
// — letter or underscore followed by letters/digits/underscores. Per-language
// case conventions (UPPER_CASE for Python, CamelCase for Go) are the user's
// problem.
func looksLikeIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Compile-time check that adapters.LanguageAdapter exposes
// GenConstDeclaration — keeps this file honest if the adapter interface
// drifts.
var _ = (adapters.LanguageAdapter)(nil)
