// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package injection tests that the SQL, regex, and GraphQL grammars are
// reachable via gotreesitter and can parse representative input without errors.
// These grammars are the first targets for 🎯T7.1 injection-point detection.
package injection_test

import (
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// parseOrFail parses src with lang and fatally fails the test if parsing
// returns a nil tree or a tree with parse errors.
func parseOrFail(t *testing.T, lang *gts.Language, src string) *gts.Tree {
	t.Helper()
	parser := gts.NewParser(lang)
	tree, err := parser.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tree == nil {
		t.Fatal("parser returned nil tree")
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}
	if root.HasError() {
		t.Fatalf("parse produced error nodes for input: %q", src)
	}
	return tree
}

func TestSQLGrammar(t *testing.T) {
	lang := grammars.SqlLanguage()
	if lang == nil {
		t.Fatal("SqlLanguage() returned nil")
	}

	src := "SELECT id FROM users WHERE name = 'foo'"
	tree := parseOrFail(t, lang, src)
	root := tree.RootNode()

	if root.ChildCount() == 0 {
		t.Fatal("expected at least one child of the root node")
	}
	first := root.Child(0)
	if got := first.Type(lang); got != "select_statement" {
		t.Errorf("expected first child type %q, got %q", "select_statement", got)
	}
}

func TestRegexGrammar(t *testing.T) {
	lang := grammars.RegexLanguage()
	if lang == nil {
		t.Fatal("RegexLanguage() returned nil")
	}

	src := `^\d+\s*$`
	tree := parseOrFail(t, lang, src)
	root := tree.RootNode()

	if got := root.Type(lang); got != "pattern" {
		t.Errorf("expected root type %q, got %q", "pattern", got)
	}
}

func TestGraphQLGrammar(t *testing.T) {
	lang := grammars.GraphqlLanguage()
	if lang == nil {
		t.Fatal("GraphqlLanguage() returned nil")
	}

	src := "query { user(id: 1) { name } }"
	tree := parseOrFail(t, lang, src)
	root := tree.RootNode()

	if root.ChildCount() == 0 {
		t.Fatal("expected at least one child of the root node")
	}
	first := root.Child(0)
	if got := first.Type(lang); got != "document" {
		t.Errorf("expected first child type %q, got %q", "document", got)
	}
}
