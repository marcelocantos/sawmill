// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"sort"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
	"github.com/marcelocantos/sawmill/store"
)

// equivalenceGraph is the transitive closure of taught equivalence pairs.
// Patterns connected (directly or transitively) form an equivalence class.
// Each class may carry a single "preferred" pattern, derived from the
// preferred_direction annotations of the taught pairs that compose it —
// only assigned when every preference in the class agrees on one pattern.
type equivalenceGraph struct {
	// class index → list of patterns in that class
	classes [][]string
	// pattern → class index
	classOf map[string]int
	// class index → preferred pattern, "" if none/conflicting
	preferred []string
	// (left, right) → true if this pair is directly taught (vs. derived)
	taught map[[2]string]bool
}

// buildEquivalenceGraph computes the transitive closure over equivs using
// union-find. Patterns are nodes; each taught equivalence is a bidirectional
// edge. Each class's preferred pattern is the unanimous winner (if any) of
// preferred_direction votes from the taught pairs in that class.
func buildEquivalenceGraph(equivs []store.Equivalence) *equivalenceGraph {
	parent := map[string]string{}
	addNode := func(p string) {
		if _, ok := parent[p]; !ok {
			parent[p] = p
		}
	}
	var find func(x string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for _, e := range equivs {
		addNode(e.LeftPattern)
		addNode(e.RightPattern)
		union(e.LeftPattern, e.RightPattern)
	}

	// Group patterns by their root.
	groups := map[string][]string{}
	for p := range parent {
		groups[find(p)] = append(groups[find(p)], p)
	}

	classes := make([][]string, 0, len(groups))
	classOf := map[string]int{}
	for _, members := range groups {
		sort.Strings(members)
		idx := len(classes)
		classes = append(classes, members)
		for _, m := range members {
			classOf[m] = idx
		}
	}

	// Tally preferred-pattern votes per class.
	votes := make([]map[string]int, len(classes))
	for i := range votes {
		votes[i] = map[string]int{}
	}
	for _, e := range equivs {
		idx := classOf[e.LeftPattern]
		switch e.PreferredDirection {
		case store.EquivalenceDirectionLeft:
			votes[idx][e.LeftPattern]++
		case store.EquivalenceDirectionRight:
			votes[idx][e.RightPattern]++
		}
	}
	preferred := make([]string, len(classes))
	for i, v := range votes {
		// Unanimous: exactly one pattern in this class has votes.
		if len(v) == 1 {
			for p := range v {
				preferred[i] = p
			}
		}
	}

	// Mark every taught (unordered) pair so list_equivalences and the
	// derived-pair generator can distinguish taught vs derived.
	taught := map[[2]string]bool{}
	for _, e := range equivs {
		taught[unorderedPair(e.LeftPattern, e.RightPattern)] = true
	}

	return &equivalenceGraph{classes: classes, classOf: classOf, preferred: preferred, taught: taught}
}

func unorderedPair(a, b string) [2]string {
	if a < b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

// nonPreferredPatternsIn returns the patterns in the same class as `pattern`
// that are not `pattern` itself. Used by apply_equivalence to expand the set
// of source patterns when the class has more than two members.
func (g *equivalenceGraph) nonPreferredPatternsIn(pattern string) []string {
	idx, ok := g.classOf[pattern]
	if !ok {
		return nil
	}
	var others []string
	for _, p := range g.classes[idx] {
		if p != pattern {
			others = append(others, p)
		}
	}
	return others
}

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
					Line:      uint(pos.Row) + 1,
					Column:    uint(pos.Column) + 1,
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
