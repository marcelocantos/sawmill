// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package rewrite_test

import (
	"strings"
	"testing"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/rewrite"
)

// parsePython parses a Python source string and returns the components needed
// by RenameInFile.
func parsePython(t *testing.T, source string) ([]byte, *tree_sitter.Tree, adapters.LanguageAdapter) {
	t.Helper()
	adapter := adapters.LanguageAdapter(&adapters.PythonAdapter{})
	src := []byte(source)

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(adapter.Language()); err != nil {
		t.Fatalf("set language: %v", err)
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		t.Fatal("tree-sitter returned nil tree")
	}

	return src, tree, adapter
}

func TestIdentityRoundTrip(t *testing.T) {
	source := `
def hello(name):
    print(f"Hello, {name}!")

class Greeter:
    def greet(self, name):
        return f"Hi, {name}"

x = hello("world")
`
	src, tree, adapter := parsePython(t, source)
	result, err := rewrite.RenameInFile(src, tree, adapter, "nonexistent", "whatever")
	if err != nil {
		t.Fatalf("RenameInFile: %v", err)
	}
	if string(result) != source {
		t.Errorf("identity round-trip failed:\ngot:  %q\nwant: %q", result, source)
	}
}

func TestRenameSingleIdentifier(t *testing.T) {
	source := "x = 1\nprint(x)\n"
	src, tree, adapter := parsePython(t, source)
	result, err := rewrite.RenameInFile(src, tree, adapter, "x", "y")
	if err != nil {
		t.Fatalf("RenameInFile: %v", err)
	}
	want := "y = 1\nprint(y)\n"
	if string(result) != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestRenameFunction(t *testing.T) {
	source := "def foo():\n    pass\n\nfoo()\n"
	src, tree, adapter := parsePython(t, source)
	result, err := rewrite.RenameInFile(src, tree, adapter, "foo", "bar")
	if err != nil {
		t.Fatalf("RenameInFile: %v", err)
	}
	want := "def bar():\n    pass\n\nbar()\n"
	if string(result) != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestRenamePreservesFormatting(t *testing.T) {
	source := "x   =   1  # a comment\nprint(  x  )\n"
	src, tree, adapter := parsePython(t, source)
	result, err := rewrite.RenameInFile(src, tree, adapter, "x", "value")
	if err != nil {
		t.Fatalf("RenameInFile: %v", err)
	}
	resultStr := string(result)
	if !strings.Contains(resultStr, "value   =   1  # a comment") {
		t.Errorf("whitespace and comment should be preserved: %s", resultStr)
	}
	if !strings.Contains(resultStr, "print(  value  )") {
		t.Errorf("whitespace in call should be preserved: %s", resultStr)
	}
}

func TestDiffOutput(t *testing.T) {
	source := "x = 1\n"
	src, tree, adapter := parsePython(t, source)
	newSource, err := rewrite.RenameInFile(src, tree, adapter, "x", "y")
	if err != nil {
		t.Fatalf("RenameInFile: %v", err)
	}
	diff := rewrite.UnifiedDiff("test.py", src, newSource)
	checks := []struct {
		substr string
		desc   string
	}{
		{"--- a/test.py", "should contain from-file header"},
		{"+++ b/test.py", "should contain to-file header"},
		{"-x = 1", "should contain removed line"},
		{"+y = 1", "should contain added line"},
	}
	for _, c := range checks {
		if !strings.Contains(diff, c.substr) {
			t.Errorf("%s: diff=%q", c.desc, diff)
		}
	}
}
