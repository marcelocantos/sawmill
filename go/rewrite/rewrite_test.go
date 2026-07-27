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

// parseGo parses a Go source string for rename tests.
func parseGo(t *testing.T, source string) ([]byte, *tree_sitter.Tree, adapters.LanguageAdapter) {
	t.Helper()
	adapter := adapters.LanguageAdapter(&adapters.GoAdapter{})
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

func mustRename(t *testing.T, src []byte, tree *tree_sitter.Tree, adapter adapters.LanguageAdapter, from, to string, opts *rewrite.RenameOpts) string {
	t.Helper()
	result, err := rewrite.RenameInFileOpts(src, tree, adapter, from, to, opts)
	if err != nil {
		t.Fatalf("RenameInFileOpts(%q→%q): %v", from, to, err)
	}
	return string(result)
}

func offsetOf(src []byte, substr string) uint {
	i := strings.Index(string(src), substr)
	if i < 0 {
		panic("substr not found: " + substr)
	}
	return uint(i)
}

// TestRenameShadowedLocalUntouched is the 🎯T50 regression: renaming the
// module-level binding must not touch a shadowed local of the same name.
func TestRenameShadowedLocalUntouched(t *testing.T) {
	source := "" +
		"x = 1\n" +
		"def f():\n" +
		"    x = 2\n" +
		"    return x\n" +
		"print(x)\n"
	src, tree, adapter := parsePython(t, source)

	// Default (no offset): module-level x only.
	got := mustRename(t, src, tree, adapter, "x", "y", nil)
	want := "" +
		"y = 1\n" +
		"def f():\n" +
		"    x = 2\n" +
		"    return x\n" +
		"print(y)\n"
	if got != want {
		t.Errorf("module rename:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// Offset on the shadowed local: only the local binding.
	off := offsetOf(src, "x = 2")
	got = mustRename(t, src, tree, adapter, "x", "y", &rewrite.RenameOpts{Offset: &off})
	want = "" +
		"x = 1\n" +
		"def f():\n" +
		"    y = 2\n" +
		"    return y\n" +
		"print(x)\n"
	if got != want {
		t.Errorf("local rename via offset:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenameIndependentParams leaves the other function's parameter alone
// when an offset selects one binding.
func TestRenameIndependentParams(t *testing.T) {
	source := "" +
		"def f(x):\n" +
		"    return x + 1\n" +
		"def g(x):\n" +
		"    return x * 2\n"
	src, tree, adapter := parsePython(t, source)

	// Without offset: two nested bindings, no module-level → ambiguous, no edit.
	got := mustRename(t, src, tree, adapter, "x", "y", nil)
	if got != source {
		t.Errorf("ambiguous nested rename should be a no-op:\ngot:\n%s", got)
	}

	// Offset on f's parameter.
	off := offsetOf(src, "def f(x)") + uint(len("def f("))
	got = mustRename(t, src, tree, adapter, "x", "y", &rewrite.RenameOpts{Offset: &off})
	want := "" +
		"def f(y):\n" +
		"    return y + 1\n" +
		"def g(x):\n" +
		"    return x * 2\n"
	if got != want {
		t.Errorf("param rename:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenameGoShadowedBlock is the Go half of 🎯T50: block-scoped short decls
// shadow outer params without being rewritten.
func TestRenameGoShadowedBlock(t *testing.T) {
	source := "" +
		"package p\n" +
		"func f(x int) int {\n" +
		"	y := x\n" +
		"	{\n" +
		"		x := 2\n" +
		"		_ = x\n" +
		"	}\n" +
		"	return y\n" +
		"}\n" +
		"func g(x int) int { return x }\n"
	src, tree, adapter := parseGo(t, source)

	// Offset on f's parameter `x`.
	off := offsetOf(src, "func f(x int)") + uint(len("func f("))
	got := mustRename(t, src, tree, adapter, "x", "val", &rewrite.RenameOpts{Offset: &off})

	if !strings.Contains(got, "func f(val int)") {
		t.Errorf("param should be renamed:\n%s", got)
	}
	if !strings.Contains(got, "y := val") {
		t.Errorf("use of param should be renamed:\n%s", got)
	}
	// Shadowed block local must stay `x`.
	if !strings.Contains(got, "x := 2") {
		t.Errorf("shadowed local must be untouched:\n%s", got)
	}
	if !strings.Contains(got, "\t\t_ = x\n") {
		t.Errorf("use of shadowed local must be untouched:\n%s", got)
	}
	// g's parameter is a different binding.
	if !strings.Contains(got, "func g(x int)") {
		t.Errorf("g's parameter must be untouched:\n%s", got)
	}
	if !strings.Contains(got, "return x }") {
		t.Errorf("g's use of x must be untouched:\n%s", got)
	}
}

// TestRenameGoStructFieldsAreDistinct renames only Point.X, not Size.X, when
// anchored on Point's field declaration.
func TestRenameGoStructFieldsAreDistinct(t *testing.T) {
	source := "" +
		"package p\n" +
		"type Point struct{ X int }\n" +
		"type Size struct{ X int }\n" +
		"func (p Point) Get() int { return p.X }\n"
	src, tree, adapter := parseGo(t, source)

	// Without offset: two field bindings named X, no package-level X → no-op.
	got := mustRename(t, src, tree, adapter, "X", "Width", nil)
	if got != source {
		t.Errorf("ambiguous fields should not all rename:\ngot:\n%s", got)
	}

	off := offsetOf(src, "struct{ X int }") + uint(len("struct{ "))
	got = mustRename(t, src, tree, adapter, "X", "Width", &rewrite.RenameOpts{Offset: &off})
	if !strings.Contains(got, "type Point struct{ Width int }") {
		t.Errorf("Point.X should rename:\n%s", got)
	}
	if !strings.Contains(got, "type Size struct{ X int }") {
		t.Errorf("Size.X must be untouched:\n%s", got)
	}
	if !strings.Contains(got, "return p.Width") {
		t.Errorf("Point method selector should rename when type is known:\n%s", got)
	}
}

// TestRenameGoPackageLevelFunc renames a top-level function and its call site
// without an offset, matching the common agent workflow.
func TestRenameGoPackageLevelFunc(t *testing.T) {
	source := "" +
		"package p\n" +
		"func foo() int { return 1 }\n" +
		"func bar() int { return foo() }\n"
	src, tree, adapter := parseGo(t, source)
	got := mustRename(t, src, tree, adapter, "foo", "baz", nil)
	want := "" +
		"package p\n" +
		"func baz() int { return 1 }\n" +
		"func bar() int { return baz() }\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
