// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// languageMatrixCase drives the core tool workflow — parse, find_symbol,
// rename, add_field, apply — for one language. Every supported language
// must complete the workflow; per-tool exceptions are explicit fields so
// gaps are documented rather than silent.
type languageMatrixCase struct {
	lang         string
	filename     string
	source       string // defines type Widget (with a field) and function calc, plus a call to calc
	typeName     string // Widget, or the language's casing of it
	fieldType    string
	defaultValue string
}

var languageMatrix = []languageMatrixCase{
	{
		lang:     "python",
		filename: "app.py",
		source: `class Widget:
    def __init__(self):
        self.w = 1

def calc(x):
    return x

calc(1)
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "go",
		filename: "app.go",
		source: `package app

type Widget struct {
	W int
}

func calc(x int) int { return x }

func use() int { return calc(1) }
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "rust",
		filename: "app.rs",
		source: `struct Widget {
    w: i32,
}

fn calc(x: i32) -> i32 { x }

fn main() { calc(1); }
`,
		typeName: "Widget", fieldType: "i32", defaultValue: "0",
	},
	{
		lang:     "typescript",
		filename: "app.ts",
		source: `class Widget {
  w: number = 1;
}

function calc(x: number): number { return x; }

calc(1);
`,
		typeName: "Widget", fieldType: "number", defaultValue: "0",
	},
	{
		lang:     "javascript",
		filename: "app.js",
		source: `class Widget {
  w = 1;
}

function calc(x) { return x; }

calc(1);
`,
		typeName: "Widget", fieldType: "0", defaultValue: "0",
	},
	{
		lang:     "cpp",
		filename: "app.cpp",
		source: `class Widget {
  int w;
};

int calc(int x) { return x; }

int use() { return calc(1); }
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "c",
		filename: "app.c",
		source: `struct widget {
    int w;
};

static int calc(int x) { return x; }

int use(void) { return calc(1); }
`,
		typeName: "widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "java",
		filename: "Widget.java",
		source: `public class Widget {
    int w;

    int calc(int x) { return x; }

    int use() { return calc(1); }
}
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "csharp",
		filename: "Widget.cs",
		source: `public class Widget
{
    int w;

    int calc(int x) { return x; }

    int Use() { return calc(1); }
}
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "ruby",
		filename: "app.rb",
		source: `class Widget
  def initialize
    @w = 1
  end
end

def calc(x)
  x
end

calc(1)
`,
		// fieldType is ignored by the Ruby adapter (attr_accessor) but the
		// tool requires a non-empty value.
		typeName: "Widget", fieldType: "Object", defaultValue: "0",
	},
	{
		lang:     "php",
		filename: "app.php",
		source: `<?php
class Widget
{
    private int $w;
}

function calc($x) { return $x; }

calc(1);
`,
		typeName: "Widget", fieldType: "int", defaultValue: "0",
	},
	{
		lang:     "kotlin",
		filename: "App.kt",
		source: `class Widget {
    val w: Int = 1
}

fun calc(x: Int): Int = x

val r = calc(1)
`,
		typeName: "Widget", fieldType: "Int", defaultValue: "0",
	},
	{
		lang:     "swift",
		filename: "App.swift",
		source: `class Widget {
    var w: Int = 1
}

func calc(x: Int) -> Int { return x }

let r = calc(x: 1)
`,
		typeName: "Widget", fieldType: "Int", defaultValue: "0",
	},
}

// TestLanguageMatrixSmoke drives parse → find_symbol → rename → add_field →
// apply for every supported language and asserts each step's observable
// output. This is the per-language floor: if a language regresses at the
// tool level, this fails before any user notices.
func TestLanguageMatrixSmoke(t *testing.T) {
	for _, tc := range languageMatrix {
		t.Run(tc.lang, func(t *testing.T) {
			h, dir := testHandlerWithDir(t, map[string]string{tc.filename: tc.source})

			// 1. find_symbol locates calc as a function.
			text, isErr, err := h.handleFindSymbol(map[string]any{"symbol": "calc"})
			if err != nil || isErr {
				t.Fatalf("find_symbol failed: err=%v text=%s", err, text)
			}
			if !strings.Contains(text, tc.filename) {
				t.Fatalf("find_symbol(calc) did not list %s: %s", tc.filename, text)
			}

			// 2. rename calc → compute produces a diff.
			text, isErr, err = h.handleRename(map[string]any{"from": "calc", "to": "compute"})
			if err != nil || isErr {
				t.Fatalf("rename failed: err=%v text=%s", err, text)
			}
			if !strings.Contains(text, "compute") {
				t.Fatalf("rename diff lacks 'compute': %s", text)
			}

			// 3. apply the rename and check the file on disk.
			text, isErr, err = h.handleApply(map[string]any{"confirm": true})
			if err != nil || isErr {
				t.Fatalf("apply(rename) failed: err=%v text=%s", err, text)
			}
			content, err := os.ReadFile(filepath.Join(dir, tc.filename))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), "calc") {
				t.Errorf("'calc' still present after rename apply:\n%s", content)
			}
			if !strings.Contains(string(content), "compute") {
				t.Errorf("'compute' missing after rename apply:\n%s", content)
			}

			// 4. add_field inserts a new field into the type.
			text, isErr, err = h.handleAddField(map[string]any{
				"type_name":     tc.typeName,
				"field_name":    "size",
				"field_type":    tc.fieldType,
				"default_value": tc.defaultValue,
			})
			if err != nil || isErr {
				t.Fatalf("add_field failed: err=%v text=%s", err, text)
			}
			if !strings.Contains(text, "size") {
				t.Fatalf("add_field diff lacks 'size': %s", text)
			}

			// 5. apply the field addition and check the file on disk.
			text, isErr, err = h.handleApply(map[string]any{"confirm": true})
			if err != nil || isErr {
				t.Fatalf("apply(add_field) failed: err=%v text=%s", err, text)
			}
			content, err = os.ReadFile(filepath.Join(dir, tc.filename))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), "size") {
				t.Errorf("'size' missing after add_field apply:\n%s", content)
			}
		})
	}
}
