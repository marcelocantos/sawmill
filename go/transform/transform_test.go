// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package transform_test

import (
	"testing"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/transform"
)

// parsePython creates a ParsedFile for a Python source string.
func parsePython(t *testing.T, source string) *forest.ParsedFile {
	t.Helper()
	adapter := &adapters.PythonAdapter{}
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

	return &forest.ParsedFile{
		Path:           "test.py",
		OriginalSource: src,
		Tree:           tree,
		Adapter:        adapter,
	}
}

// transformStr is a helper that parses Python, applies a transform, and returns
// the result as a string.
func transformStr(t *testing.T, source string, matchSpec *transform.Match, action *transform.Action) string {
	t.Helper()
	file := parsePython(t, source)
	result, err := transform.TransformFile(file, matchSpec, action)
	if err != nil {
		t.Fatalf("TransformFile: %v", err)
	}
	return string(result)
}

func TestRemoveFunction(t *testing.T) {
	source := "def foo():\n    pass\n\ndef bar():\n    pass\n"
	result := transformStr(t, source,
		transform.AbstractMatch("function", "foo", ""),
		transform.Remove(),
	)
	want := "\ndef bar():\n    pass\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestWrapCall(t *testing.T) {
	source := "result = compute(x)\n"
	result := transformStr(t, source,
		transform.AbstractMatch("call", "compute", ""),
		transform.Wrap("try_catch(", ")"),
	)
	want := "result = try_catch(compute(x))\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestReplaceFunctionName(t *testing.T) {
	source := "def old_func():\n    pass\n"
	result := transformStr(t, source,
		transform.AbstractMatch("function", "old_func", ""),
		transform.ReplaceName("new_func"),
	)
	want := "def new_func():\n    pass\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestPrependStatement(t *testing.T) {
	source := "def foo():\n    return 1\n"
	result := transformStr(t, source,
		transform.AbstractMatch("function", "foo", ""),
		transform.PrependStatement("# marker"),
	)
	want := "# marker\ndef foo():\n    return 1\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestAppendStatement(t *testing.T) {
	source := "def foo():\n    return 1\n"
	result := transformStr(t, source,
		transform.AbstractMatch("function", "foo", ""),
		transform.AppendStatement("# end"),
	)
	want := "def foo():\n    return 1\n# end\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestReplaceWithCode(t *testing.T) {
	source := "x = old_value\n"
	result := transformStr(t, source,
		transform.RawMatch(`((identifier) @name (#eq? @name "old_value"))`, "name"),
		transform.Replace("new_value"),
	)
	want := "x = new_value\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}

func TestGlobNameMatch(t *testing.T) {
	source := "def test_foo():\n    pass\n\ndef test_bar():\n    pass\n\ndef helper():\n    pass\n"
	result := transformStr(t, source,
		transform.AbstractMatch("function", "test_*", ""),
		transform.Remove(),
	)
	want := "\n\ndef helper():\n    pass\n"
	if result != want {
		t.Errorf("got:\n%q\nwant:\n%q", result, want)
	}
}
