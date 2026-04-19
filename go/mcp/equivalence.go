// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// equivalenceMatch is one location where a pattern matched a file's source.
type equivalenceMatch struct {
	StartByte uint
	EndByte   uint
	Line      uint // 1-based
	Column    uint // 1-based
	Original  string
	Rewrite   string
}

// containerNodeTypes are tree-sitter node kinds that act as containers for
// multiple sub-statements or whole declarations. Pattern matching is never
// attempted at these levels — the walker descends into their children
// instead. This prevents a pattern like "foo($a, $b)" from greedily matching
// across a whole block of statements.
var containerNodeTypes = map[string]bool{
	// Roots
	"module": true, "program": true, "source_file": true, "translation_unit": true,
	// Function/method bodies
	"function_definition": true, "function_declaration": true,
	"function_item": true, "method_declaration": true, "method_definition": true,
	"function_signature_item": true,
	// Type/impl/trait definitions
	"class_definition": true, "class_declaration": true,
	"impl_item": true, "struct_item": true, "trait_item": true, "enum_item": true,
	"interface_declaration": true, "type_declaration": true,
	// Block containers
	"block":                   true,
	"suite":                   true,
	"compound_statement":      true,
	"statement_block":         true,
	"declaration_list":        true,
	"field_declaration_list":  true,
	"namespace_definition":    true,
	"linkage_specification":   true,
	// Control-flow statements that contain statement bodies
	"if_statement":     true,
	"for_statement":    true,
	"while_statement":  true,
	"do_statement":     true,
	"try_statement":    true,
	"with_statement":   true,
	"switch_statement": true,
	"match_statement":  true,
}

// findEquivalenceMatches walks the tree top-down and finds outermost nodes
// whose source text matches srcPattern. Returns one equivalenceMatch per
// non-overlapping match. dstPattern, if non-empty, is used to compute the
// suggested rewrite via Apply().
//
// Matching is anchored to the entire node text and is skipped on container
// nodes (blocks, function bodies, modules) to avoid greedy multi-statement
// matches. Outermost expression-like matches win — once a node matches, its
// descendants are skipped so we don't report nested duplicates of the same
// logical site.
func findEquivalenceMatches(file *forest.ParsedFile, srcPattern *Pattern, dstPattern string) []equivalenceMatch {
	var matches []equivalenceMatch
	root := file.Tree.RootNode()
	if root == nil {
		return nil
	}
	walkEquivalence(root, file.OriginalSource, srcPattern, dstPattern, &matches)
	return matches
}

func walkEquivalence(node *tree_sitter.Node, source []byte, srcPattern *Pattern, dstPattern string, out *[]equivalenceMatch) {
	if node == nil {
		return
	}
	start := node.StartByte()
	end := node.EndByte()
	if start >= end || end > uint(len(source)) {
		return
	}
	// Container nodes never match — they wrap multiple statements, and a
	// greedy placeholder would otherwise span statement boundaries.
	if !containerNodeTypes[node.Kind()] {
		text := string(source[start:end])
		captures, ok := srcPattern.Match(text)
		if ok {
			var rewrite string
			if dstPattern != "" {
				rewrite = Apply(dstPattern, captures)
			}
			if dstPattern == "" || rewrite != text {
				pos := node.StartPosition()
				*out = append(*out, equivalenceMatch{
					StartByte: start,
					EndByte:   end,
					Line:      pos.Row + 1,
					Column:    pos.Column + 1,
					Original:  text,
					Rewrite:   rewrite,
				})
				return // outermost match wins; skip descendants
			}
		}
	}
	count := node.ChildCount()
	for i := uint(0); i < count; i++ {
		child := node.Child(i)
		walkEquivalence(child, source, srcPattern, dstPattern, out)
	}
}

// equivalenceEdits converts matches into rewrite.Edits for application.
func equivalenceEdits(matches []equivalenceMatch) []rewrite.Edit {
	edits := make([]rewrite.Edit, len(matches))
	for i, m := range matches {
		edits[i] = rewrite.Edit{
			Start:       m.StartByte,
			End:         m.EndByte,
			Replacement: m.Rewrite,
		}
	}
	return edits
}
