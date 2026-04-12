// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package gitindex_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"github.com/marcelocantos/sawmill/gitindex"
)

const testSource = "package main\n\nfunc hello() {}\n"
const testBlobSHA = "deadbeef000000000000000000000000deadbeef"
const testCommitSHA = "aabbccdd000000000000000000000000aabbccdd"

// parseGo parses Go source and returns the tree. Caller must close the tree.
func parseGo(t *testing.T, src []byte) *tree_sitter.Tree {
	t.Helper()
	parser := tree_sitter.NewParser()
	t.Cleanup(func() { parser.Close() })
	if err := parser.SetLanguage(tree_sitter.NewLanguage(tree_sitter_go.Language())); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		t.Fatal("parser returned nil tree")
	}
	t.Cleanup(func() { tree.Close() })
	return tree
}

func openStore(t *testing.T) *gitindex.Store {
	t.Helper()
	s, err := gitindex.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestSchemaCreation verifies that OpenMemory succeeds (schema is created).
func TestSchemaCreation(t *testing.T) {
	openStore(t) // success is sufficient
}

// TestIndexBlob indexes a small Go file and checks that nodes were stored.
func TestIndexBlob(t *testing.T) {
	s := openStore(t)
	src := []byte(testSource)
	tree := parseGo(t, src)

	if err := s.IndexBlob(testBlobSHA, "go", tree); err != nil {
		t.Fatalf("IndexBlob: %v", err)
	}

	// Should find exactly one function_declaration.
	funcs, err := s.QueryNodes(testBlobSHA, "function_declaration")
	if err != nil {
		t.Fatalf("QueryNodes function_declaration: %v", err)
	}
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function_declaration, got %d", len(funcs))
	}

	// The function_declaration should have children including identifier,
	// parameter_list, and block.
	children, err := s.QueryChildren(funcs[0].ID)
	if err != nil {
		t.Fatalf("QueryChildren: %v", err)
	}
	if len(children) == 0 {
		t.Fatal("expected children of function_declaration, got none")
	}

	childTypes := make(map[string]bool)
	for _, c := range children {
		childTypes[c.NodeType] = true
	}
	for _, want := range []string{"identifier", "parameter_list", "block"} {
		if !childTypes[want] {
			t.Errorf("expected child %q among function_declaration children; got %v", want, childTypes)
		}
	}
}

// TestIsIndexed checks the before/after state of blob indexing.
func TestIsIndexed(t *testing.T) {
	s := openStore(t)
	src := []byte(testSource)
	tree := parseGo(t, src)

	before, err := s.IsIndexed(testBlobSHA)
	if err != nil {
		t.Fatalf("IsIndexed (before): %v", err)
	}
	if before {
		t.Fatal("expected IsIndexed=false before indexing")
	}

	if err := s.IndexBlob(testBlobSHA, "go", tree); err != nil {
		t.Fatalf("IndexBlob: %v", err)
	}

	after, err := s.IsIndexed(testBlobSHA)
	if err != nil {
		t.Fatalf("IsIndexed (after): %v", err)
	}
	if !after {
		t.Fatal("expected IsIndexed=true after indexing")
	}
}

// TestRegisterCommitFiles checks commit-file registration and lookup.
func TestRegisterCommitFiles(t *testing.T) {
	s := openStore(t)
	src := []byte(testSource)
	tree := parseGo(t, src)

	// Must index the blob first (foreign key constraint).
	if err := s.IndexBlob(testBlobSHA, "go", tree); err != nil {
		t.Fatalf("IndexBlob: %v", err)
	}

	files := []gitindex.CommitFile{
		{FilePath: "main.go", BlobSHA: testBlobSHA},
	}
	if err := s.RegisterCommitFiles(testCommitSHA, files); err != nil {
		t.Fatalf("RegisterCommitFiles: %v", err)
	}

	sha, ok, err := s.BlobSHAForFile(testCommitSHA, "main.go")
	if err != nil {
		t.Fatalf("BlobSHAForFile: %v", err)
	}
	if !ok {
		t.Fatal("expected BlobSHAForFile to find the row")
	}
	if sha != testBlobSHA {
		t.Fatalf("expected SHA %s, got %s", testBlobSHA, sha)
	}

	// Unknown file should return ok=false.
	_, missing, err := s.BlobSHAForFile(testCommitSHA, "nonexistent.go")
	if err != nil {
		t.Fatalf("BlobSHAForFile (missing): %v", err)
	}
	if missing {
		t.Fatal("expected ok=false for missing file")
	}

	// CommitFiles should return the registered entry.
	got, err := s.CommitFiles(testCommitSHA)
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 commit file, got %d", len(got))
	}
	if got[0].FilePath != "main.go" || got[0].BlobSHA != testBlobSHA {
		t.Fatalf("unexpected commit file: %+v", got[0])
	}
}

// TestDedupBlob verifies that indexing the same blob SHA twice does not
// duplicate nodes.
func TestDedupBlob(t *testing.T) {
	s := openStore(t)
	src := []byte(testSource)

	tree1 := parseGo(t, src)
	if err := s.IndexBlob(testBlobSHA, "go", tree1); err != nil {
		t.Fatalf("IndexBlob (first): %v", err)
	}

	nodes1, err := s.QueryNodes(testBlobSHA, "function_declaration")
	if err != nil {
		t.Fatalf("QueryNodes (first): %v", err)
	}

	tree2 := parseGo(t, src)
	if err := s.IndexBlob(testBlobSHA, "go", tree2); err != nil {
		t.Fatalf("IndexBlob (second): %v", err)
	}

	nodes2, err := s.QueryNodes(testBlobSHA, "function_declaration")
	if err != nil {
		t.Fatalf("QueryNodes (second): %v", err)
	}

	if len(nodes1) != len(nodes2) {
		t.Fatalf("expected same node count after dedup: first=%d, second=%d",
			len(nodes1), len(nodes2))
	}
}

// TestQueryAncestors verifies that we can walk up the parent chain.
func TestQueryAncestors(t *testing.T) {
	s := openStore(t)
	src := []byte(testSource)
	tree := parseGo(t, src)

	if err := s.IndexBlob(testBlobSHA, "go", tree); err != nil {
		t.Fatalf("IndexBlob: %v", err)
	}

	// Find an "identifier" node inside the function — it should have ancestors.
	idents, err := s.QueryNodes(testBlobSHA, "identifier")
	if err != nil {
		t.Fatalf("QueryNodes identifier: %v", err)
	}
	if len(idents) == 0 {
		t.Fatal("expected at least one identifier node")
	}

	ancestors, err := s.QueryAncestors(idents[0].ID)
	if err != nil {
		t.Fatalf("QueryAncestors: %v", err)
	}
	if len(ancestors) == 0 {
		t.Fatal("expected identifier to have at least one ancestor")
	}

	// The nearest ancestor of the function name identifier should be
	// function_declaration.
	if ancestors[0].NodeType != "function_declaration" {
		t.Errorf("expected first ancestor to be function_declaration, got %q", ancestors[0].NodeType)
	}

	// The last ancestor should be the root (source_file) with no parent.
	root := ancestors[len(ancestors)-1]
	if root.ParentID != nil {
		t.Errorf("expected root ancestor to have nil ParentID, got %v", root.ParentID)
	}
}
