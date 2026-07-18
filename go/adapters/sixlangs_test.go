// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const jsSample = `import { Helper } from "./helper.js";

class Foo extends Base {
  #count = 0;
  status = "new";

  constructor(count) {
    super();
    this.count = count;
  }

  getCount(items) {
    const h = new Helper();
    h.run();
    process(this.count);
    return this.count;
  }
}

function top(a, b) { return a + b; }
`

const rubySample = `require "json"
require_relative "helper"

module App
  class Foo < Base
    NAME = "foo"

    def initialize(count)
      @count = count
      @helper = Helper.new
    end

    def get_count(items)
      process(@count)
      @count
    end
  end
end
`

const phpSample = `<?php
namespace App;

use App\Util\Helper;
use JsonSerializable;

#[Attribute]
class Foo extends Base implements JsonSerializable
{
    private int $count;

    public function __construct(int $count)
    {
        $this->count = $count;
    }

    public function getCount(array $items): int
    {
        $h = new Helper();
        process($this->count);
        return $this->count;
    }
}

interface Iface {}
enum Color { case Red; }
function top($a) { return $a; }
`

const kotlinSample = `package com.example.app

import com.example.util.Helper

@Deprecated("old")
class Foo(private val count: Int) : Base(), Iface {
    val name: String = "foo"
    private var helper: Helper = Helper()

    fun getCount(items: List<String>): Int {
        val h = Helper()
        process(count)
        return count
    }
}

interface Iface
object Singleton
fun top(a: Int): Int = a
`

const swiftSample = `import Foundation

@objc
class Foo: Base, Iface {
    static let name = "foo"
    private var count: Int

    init(count: Int) {
        self.count = count
    }

    func getCount(items: [String]) -> Int {
        let h = Helper()
        process(count)
        return self.count
    }
}

protocol Iface {}
func top(a: Int) -> Int { return a }
`

const cSample = `#include <stdio.h>
#include "helper.h"

struct foo {
    int count;
};

typedef struct foo foo_t;

enum color { RED, GREEN };

static int get_count(struct foo *f, int n) {
    helper_run(f);
    process(f->count);
    return f->count + n;
}
`

func TestJavaScriptAdapterQueries(t *testing.T) {
	a := &JavaScriptAdapter{}
	src := []byte(jsSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "top")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo")
	assertCaptures(t, a, a.FieldQuery(), src, "#count", "status")
	assertCaptures(t, a, a.MethodQuery(), src, "constructor", "getCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "process")
	assertCaptures(t, a, a.ImportQuery(), src, `"./helper.js"`)
	assertCaptures(t, a, a.StructLiteralQuery(), src, "Helper")
	assertCaptures(t, a, a.IdentifierQuery(), src, "getCount", "Helper")
	// DecoratorQuery must at least compile (sample has no decorators).
	queryDecorators(t, a, a.DecoratorQuery(), src)
}

func TestRubyAdapterQueries(t *testing.T) {
	a := &RubyAdapter{}
	src := []byte(rubySample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "initialize", "get_count")
	assertCaptures(t, a, a.TypeDefQuery(), src, "App", "Foo")
	assertCaptures(t, a, a.FieldQuery(), src, "@count", "@helper")
	assertCaptures(t, a, a.MethodQuery(), src, "get_count")
	assertCaptures(t, a, a.CallExprQuery(), src, "process")
	assertCaptures(t, a, a.ImportQuery(), src, "json", "helper")
	assertCaptures(t, a, a.StructLiteralQuery(), src, "Helper")
	assertCaptures(t, a, a.IdentifierQuery(), src, "get_count", "Foo", "@count")
}

func TestPhpAdapterQueries(t *testing.T) {
	a := &PhpAdapter{}
	src := []byte(phpSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "top", "getCount", "__construct")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo", "Iface", "Color")
	assertCaptures(t, a, a.FieldQuery(), src, "count")
	assertCaptures(t, a, a.MethodQuery(), src, "getCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "process")
	assertCaptures(t, a, a.ImportQuery(), src, "JsonSerializable")
	assertCaptures(t, a, a.StructLiteralQuery(), src, "Helper")
	assertCaptures(t, a, a.IdentifierQuery(), src, "getCount", "Helper")

	decorators := queryDecorators(t, a, a.DecoratorQuery(), src)
	if len(decorators) == 0 {
		t.Error("expected at least one PHP attribute capture")
	}
}

func TestKotlinAdapterQueries(t *testing.T) {
	a := &KotlinAdapter{}
	src := []byte(kotlinSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "getCount", "top")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo", "Iface", "Singleton")
	assertCaptures(t, a, a.FieldQuery(), src, "name")
	assertCaptures(t, a, a.MethodQuery(), src, "getCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "process")
	assertCaptures(t, a, a.ImportQuery(), src, "com.example.util.Helper")
	assertCaptures(t, a, a.TypeUseQuery(), src, "Helper", "Int", "String")
	assertCaptures(t, a, a.IdentifierQuery(), src, "getCount", "Foo")

	decorators := queryDecorators(t, a, a.DecoratorQuery(), src)
	if len(decorators) == 0 {
		t.Error("expected at least one Kotlin annotation capture")
	}
}

func TestSwiftAdapterQueries(t *testing.T) {
	a := &SwiftAdapter{}
	src := []byte(swiftSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "getCount", "top")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Foo", "Iface")
	assertCaptures(t, a, a.FieldQuery(), src, "name", "count")
	assertCaptures(t, a, a.MethodQuery(), src, "getCount")
	assertCaptures(t, a, a.CallExprQuery(), src, "process")
	assertCaptures(t, a, a.ImportQuery(), src, "Foundation")
	assertCaptures(t, a, a.TypeUseQuery(), src, "Int", "String")
	assertCaptures(t, a, a.IdentifierQuery(), src, "getCount", "Foo")

	decorators := queryDecorators(t, a, a.DecoratorQuery(), src)
	if len(decorators) == 0 {
		t.Error("expected at least one Swift attribute capture")
	}
}

func TestCAdapterQueries(t *testing.T) {
	a := &CAdapter{}
	src := []byte(cSample)

	assertCaptures(t, a, a.FunctionDefQuery(), src, "get_count")
	assertCaptures(t, a, a.TypeDefQuery(), src, "foo", "color", "foo_t")
	assertCaptures(t, a, a.FieldQuery(), src, "count")
	assertCaptures(t, a, a.CallExprQuery(), src, "helper_run", "process")
	assertCaptures(t, a, a.ImportQuery(), src, "<stdio.h>")
	assertCaptures(t, a, a.TypeUseQuery(), src, "foo")
	assertCaptures(t, a, a.IdentifierQuery(), src, "get_count", "count")
}

// TestCppFieldQueryRegression pins the C++ field query, which silently
// matched nothing while its type:/declarator: pattern order was reversed.
func TestCppFieldQueryRegression(t *testing.T) {
	a := &CppAdapter{}
	src := []byte("class A {\n  int count;\n  Helper *helper;\n};\n")
	assertCaptures(t, a, a.FieldQuery(), src, "count")
}

func TestForExtensionSixLanguages(t *testing.T) {
	cases := map[string]LanguageAdapter{
		"js":    &JavaScriptAdapter{},
		"jsx":   &JavaScriptAdapter{},
		"mjs":   &JavaScriptAdapter{},
		"rb":    &RubyAdapter{},
		"php":   &PhpAdapter{},
		"kt":    &KotlinAdapter{},
		"swift": &SwiftAdapter{},
		"c":     &CAdapter{},
	}
	for ext, want := range cases {
		got := ForExtension(ext)
		if got == nil {
			t.Errorf("ForExtension(%q) = nil", ext)
			continue
		}
		if gotType, wantType := typeName(got), typeName(want); gotType != wantType {
			t.Errorf("ForExtension(%q) = %s, want %s", ext, gotType, wantType)
		}
	}
	// .h must stay routed to the C++ adapter.
	if _, ok := ForExtension("h").(*CppAdapter); !ok {
		t.Error(`ForExtension("h") no longer returns the C++ adapter`)
	}
}

func typeName(a LanguageAdapter) string {
	return filepath.Ext(reflectTypeName(a))
}

// reflectTypeName avoids importing reflect for one call site.
func reflectTypeName(a LanguageAdapter) string {
	switch a.(type) {
	case *JavaScriptAdapter:
		return ".JavaScriptAdapter"
	case *RubyAdapter:
		return ".RubyAdapter"
	case *PhpAdapter:
		return ".PhpAdapter"
	case *KotlinAdapter:
		return ".KotlinAdapter"
	case *SwiftAdapter:
		return ".SwiftAdapter"
	case *CAdapter:
		return ".CAdapter"
	default:
		return ".other"
	}
}

func TestJavaScriptImportPathResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "lib", "helper.js")
	importing := filepath.Join(root, "src", "app.js")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(importing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export function run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &JavaScriptAdapter{}
	if got := a.BuildImportPath(target, importing, root); got != `"../lib/helper.js"` {
		t.Errorf("BuildImportPath = %q, want \"../lib/helper.js\"", got)
	}
	if got := a.ResolveImportPath("../lib/helper.js", importing, root); got != filepath.Join("lib", "helper.js") {
		t.Errorf("ResolveImportPath = %q", got)
	}
	if got := a.ResolveImportPath("../lib/helper", importing, root); got != filepath.Join("lib", "helper.js") {
		t.Errorf("extensionless ResolveImportPath = %q", got)
	}
}

func TestRubyImportPathResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "lib", "helper.rb")
	importing := filepath.Join(root, "app.rb")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("class Helper\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &RubyAdapter{}
	if got := a.ResolveImportPath("helper", importing, root); got != filepath.Join("lib", "helper.rb") {
		t.Errorf("ResolveImportPath = %q", got)
	}
	if got := a.BuildImportPath(target, importing, root); got != "lib/helper" {
		t.Errorf("BuildImportPath = %q", got)
	}
}

func TestKotlinImportPathRoundTrip(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "main", "kotlin", "com", "example", "util", "Helper.kt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package com.example.util\nclass Helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &KotlinAdapter{}
	if got := a.BuildImportPath(file, "", root); got != "com.example.util.Helper" {
		t.Errorf("BuildImportPath = %q", got)
	}
	want := filepath.Join("src", "main", "kotlin", "com", "example", "util", "Helper.kt")
	if got := a.ResolveImportPath("com.example.util.Helper", "", root); got != want {
		t.Errorf("ResolveImportPath = %q, want %q", got, want)
	}
}

func TestSixLanguagesEnvAndConst(t *testing.T) {
	for _, tc := range []struct {
		adapter   LanguageAdapter
		envWant   string
		constWant string
	}{
		{&JavaScriptAdapter{}, `process.env["HOME"]`, `const N = 1;`},
		{&RubyAdapter{}, `ENV["HOME"]`, `N = 1`},
		{&PhpAdapter{}, `getenv("HOME")`, `const N = 1;`},
		{&KotlinAdapter{}, `System.getenv("HOME")`, `const val N = 1`},
		{&SwiftAdapter{}, `ProcessInfo.processInfo.environment["HOME"]`, `let N = 1`},
		{&CAdapter{}, `getenv("HOME")`, `#define N 1`},
	} {
		if got := tc.adapter.GenEnvRead("HOME"); got != tc.envWant {
			t.Errorf("GenEnvRead = %q, want %q", got, tc.envWant)
		}
		got := tc.adapter.GenConstDeclaration("N", "1")
		if !slices.Contains([]string{tc.constWant + "\n"}, got) {
			t.Errorf("GenConstDeclaration = %q, want %q", got, tc.constWant+"\n")
		}
	}
}
