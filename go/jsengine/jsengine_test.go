// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package jsengine

import (
	"testing"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

func parsePython(source string) *forest.ParsedFile {
	adapter := &adapters.PythonAdapter{}
	sourceBytes := []byte(source)
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(adapter.Language()); err != nil {
		panic(err)
	}
	tree := parser.Parse(sourceBytes, nil)
	return &forest.ParsedFile{
		Path:           "test.py",
		OriginalSource: sourceBytes,
		Tree:           tree,
		Adapter:        adapter,
	}
}

func jsTransform(t *testing.T, source, transformFn string) string {
	t.Helper()
	file := parsePython(source)
	queryStr := file.Adapter.FunctionDefQuery()
	result, err := RunJSTransform(
		file.OriginalSource,
		file.Tree,
		queryStr,
		transformFn,
		"test.py",
		file.Adapter,
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}

func TestJSRenameFunction(t *testing.T) {
	result := jsTransform(t,
		"def foo():\n    pass\n\ndef bar():\n    pass\n",
		`(node) => node.name === "foo" ? node.replaceName("baz") : node`,
	)
	expected := "def baz():\n    pass\n\ndef bar():\n    pass\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestJSRemoveByCondition(t *testing.T) {
	result := jsTransform(t,
		"def test_a():\n    pass\n\ndef helper():\n    pass\n",
		`(node) => node.name.startsWith("test_") ? node.remove() : node`,
	)
	expected := "\ndef helper():\n    pass\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestJSWrapFunction(t *testing.T) {
	result := jsTransform(t,
		"def foo():\n    pass\n",
		`(node) => node.wrap("# BEGIN\n", "\n# END")`,
	)
	expected := "# BEGIN\ndef foo():\n    pass\n# END\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestJSReturnString(t *testing.T) {
	result := jsTransform(t,
		"def foo():\n    pass\n",
		`(node) => "# replaced\n"`,
	)
	expected := "# replaced\n\n"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestJSUnchanged(t *testing.T) {
	source := "def foo():\n    pass\n"
	result := jsTransform(t, source, "(node) => node")
	if result != source {
		t.Errorf("got %q, want %q", result, source)
	}
}
