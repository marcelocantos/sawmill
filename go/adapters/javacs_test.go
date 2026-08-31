// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

const javaSample = `package com.example.app;

import java.util.List;
import com.example.util.Helper;

@Deprecated
public class Foo extends Base implements Iface {
    private static final String NAME = "foo";
    private int count;
    private Helper helper;

    public Foo(int count) {
        this.count = count;
    }

    @Override
    public int getCount(List<String> items) {
        Helper h = new Helper();
        h.run();
        process(count);
        return this.count;
    }
}

interface Iface {}
enum Color { RED, GREEN }
record Point(int x, int y) {}
`

const csharpSample = `using System;
using System.Collections.Generic;

namespace Example.App
{
    [Serializable]
    public class Foo : Base, IFace
    {
        private const string Name = "foo";
        private int count;

        public Foo(int count)
        {
            this.count = count;
        }

        [Obsolete]
        public int GetCount(List<string> items)
        {
            var h = new Helper();
            h.Run();
            Process(count);
            return this.count;
        }
    }

    public interface IFace {}
    public struct Vec { public float X; }
    public enum Color { Red, Green }
    public record Point(int X, int Y);
}
`

// queryCaptures parses src with the adapter's language and returns the text
// of every @name capture produced by query.
func queryCaptures(t *testing.T, a LanguageAdapter, query string, src []byte) []string {
	t.Helper()
	if query == "" {
		t.Fatal("query is empty")
	}
	parser := tree_sitter.NewParser()
	if err := parser.SetLanguage(a.Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		t.Fatal("parse returned nil tree")
	}
	if tree.HasError() {
		t.Fatal("sample source has parse errors")
	}
	q, err := tree_sitter.NewQuery(a.Language(), query)
	if err != nil {
		t.Fatalf("query failed to compile: %v\nquery: %s", err, query)
	}
	nameIdx := slices.Index(q.CaptureNames(), "name")
	var names []string
	cursor := tree_sitter.NewQueryCursor()
	it := cursor.Matches(q, tree.RootNode(), src)
	for m := it.Next(); m != nil; m = it.Next() {
		for _, c := range m.Captures {
			if int(c.Index) == nameIdx {
				names = append(names, string(src[c.Node.StartByte():c.Node.EndByte()]))
			}
		}
	}
	return names
}

// assertCaptures fails unless every want string appears among the query's
// @name captures.
func assertCaptures(t *testing.T, a LanguageAdapter, query string, src []byte, want ...string) {
	t.Helper()
	got := queryCaptures(t, a, query, src)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing capture %q; got %v", w, got)
		}
	}
}

func TestJavaAdapterQueries(t *testing.T) {
	a := &JavaAdapter{}
	src := []byte(javaSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "getCount", "Foo")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo", "Iface", "Color", "Point")
	assertCaptures(t, a, a.FieldQuery(), src, "NAME", "count", "helper")
	assertCaptures(t, a, a.MethodQuery(), src, "getCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "run", "process")
	assertCaptures(t, a, a.ImportQuery(), src, "java.util.List", "com.example.util.Helper")
	assertCaptures(t, a, a.StructLiteralQuery(), src, "Helper")
	assertCaptures(t, a, a.TypeUseQuery(), src, "Base", "Iface", "String", "Helper", "List")
	assertCaptures(t, a, a.IdentifierQuery(), src, "getCount", "Helper")

	// DecoratorQuery captures @decorator, not @name — check it matches both
	// annotation forms.
	decorators := queryDecorators(t, a, a.DecoratorQuery(), src)
	for _, want := range []string{"@Deprecated", "@Override"} {
		if !slices.Contains(decorators, want) {
			t.Errorf("missing decorator %q; got %v", want, decorators)
		}
	}
}

func TestCSharpAdapterQueries(t *testing.T) {
	a := &CSharpAdapter{}
	src := []byte(csharpSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "GetCount", "Foo")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo", "IFace", "Vec", "Color", "Point")
	assertCaptures(t, a, a.FieldQuery(), src, "Name", "count", "X")
	assertCaptures(t, a, a.MethodQuery(), src, "GetCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "Process")
	assertCaptures(t, a, a.ImportQuery(), src, "System", "System.Collections.Generic")
	assertCaptures(t, a, a.StructLiteralQuery(), src, "Helper")
	assertCaptures(t, a, a.TypeUseQuery(), src, "Base", "IFace", "Helper")
	assertCaptures(t, a, a.IdentifierQuery(), src, "GetCount", "Helper")

	decorators := queryDecorators(t, a, a.DecoratorQuery(), src)
	for _, want := range []string{"[Serializable]", "[Obsolete]"} {
		if !slices.Contains(decorators, want) {
			t.Errorf("missing decorator %q; got %v", want, decorators)
		}
	}
}

// queryDecorators returns the source text of every @decorator capture.
func queryDecorators(t *testing.T, a LanguageAdapter, query string, src []byte) []string {
	t.Helper()
	parser := tree_sitter.NewParser()
	if err := parser.SetLanguage(a.Language()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse(src, nil)
	q, err := tree_sitter.NewQuery(a.Language(), query)
	if err != nil {
		t.Fatalf("decorator query failed to compile: %v", err)
	}
	var out []string
	cursor := tree_sitter.NewQueryCursor()
	it := cursor.Matches(q, tree.RootNode(), src)
	for m := it.Next(); m != nil; m = it.Next() {
		for _, c := range m.Captures {
			out = append(out, string(src[c.Node.StartByte():c.Node.EndByte()]))
		}
	}
	return out
}

func TestForExtensionJavaCSharp(t *testing.T) {
	if _, ok := ForExtension("java").(*JavaAdapter); !ok {
		t.Error(`ForExtension("java") did not return a JavaAdapter`)
	}
	if _, ok := ForExtension("cs").(*CSharpAdapter); !ok {
		t.Error(`ForExtension("cs") did not return a CSharpAdapter`)
	}
}

func TestJavaImportPathRoundTrip(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "main", "java", "com", "example", "util", "Helper.java")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package com.example.util;\nclass Helper {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &JavaAdapter{}
	if got := a.BuildImportPath(file, "", root); got != "com.example.util.Helper" {
		t.Errorf("BuildImportPath = %q, want com.example.util.Helper", got)
	}
	want := filepath.Join("src", "main", "java", "com", "example", "util", "Helper.java")
	if got := a.ResolveImportPath("com.example.util.Helper", "", root); got != want {
		t.Errorf("ResolveImportPath = %q, want %q", got, want)
	}
	if got := a.ResolveImportPath("com.example.util.*", "", root); got != "" {
		t.Errorf("wildcard import resolved to %q, want empty", got)
	}
}

func TestJavaCSharpConstDeclarations(t *testing.T) {
	java := &JavaAdapter{}
	cs := &CSharpAdapter{}
	cases := []struct {
		value    string
		javaWant string
		csWant   string
	}{
		{`"x"`, "String", "string"},
		{"42", "int", "int"},
		{"3.14", "double", "double"},
		{"true", "boolean", "bool"},
		{"'c'", "char", "char"},
	}
	for _, c := range cases {
		if got := java.GenConstDeclaration("N", c.value); !strings.Contains(got, c.javaWant) {
			t.Errorf("java const for %s = %q, want type %s", c.value, got, c.javaWant)
		}
		if got := cs.GenConstDeclaration("N", c.value); !strings.Contains(got, c.csWant) {
			t.Errorf("cs const for %s = %q, want type %s", c.value, got, c.csWant)
		}
	}
}
