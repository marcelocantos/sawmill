// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

// TestPromoteConstantPython verifies the basic Python case: a string literal
// is replaced with the constant name and the declaration appears at the top
// of the file.
func TestPromoteConstantPython(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `import os

def fetch():
    return get("http://example.com/api")

def post():
    send("http://example.com/api", payload)
`,
	})

	text, isErr, err := h.handlePromoteConstant(map[string]any{
		"literal": `"http://example.com/api"`,
		"name":    "API_URL",
	})
	if err != nil || isErr {
		t.Fatalf("promote_constant: err=%v isErr=%v text=%s", err, isErr, text)
	}
	for _, want := range []string{"2 replacement", "1 declaration"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in summary, got: %s", want, text)
		}
	}

	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	got := readFile(t, h, "main.py")

	// Both call sites should now reference the constant.
	if strings.Count(got, `get(API_URL)`) != 1 {
		t.Errorf("expected get(API_URL) once, got:\n%s", got)
	}
	if strings.Count(got, `send(API_URL, payload)`) != 1 {
		t.Errorf("expected send(API_URL, payload) once, got:\n%s", got)
	}
	// Declaration should be present.
	if !strings.Contains(got, `API_URL = "http://example.com/api"`) {
		t.Errorf("expected declaration in file, got:\n%s", got)
	}
	// Original literal should not appear except inside the declaration.
	if strings.Count(got, `"http://example.com/api"`) != 1 {
		t.Errorf("expected literal to appear only in declaration, got:\n%s", got)
	}
}

// TestPromoteConstantGo verifies Go syntax: const NAME = "..." is generated
// after the package declaration.
func TestPromoteConstantGo(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import "fmt"

func main() {
	fmt.Println("hello")
	greet("hello")
}

func greet(s string) {}
`,
	})

	if _, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": `"hello"`,
		"name":    "Greeting",
	}); isErr {
		t.Fatal("promote_constant errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}

	got := readFile(t, h, "main.go")
	if !strings.Contains(got, `const Greeting = "hello"`) {
		t.Errorf("missing const declaration:\n%s", got)
	}
	if !strings.Contains(got, `fmt.Println(Greeting)`) {
		t.Errorf("call site not rewritten:\n%s", got)
	}
	if !strings.Contains(got, `greet(Greeting)`) {
		t.Errorf("greet call not rewritten:\n%s", got)
	}
	// Package declaration must remain on the first line.
	if !strings.HasPrefix(got, "package main\n") {
		t.Errorf("expected file to still start with `package main`, got:\n%s", got)
	}
}

// TestPromoteConstantNumber verifies that numeric literals are also handled.
func TestPromoteConstantNumber(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `def retry():
    for i in range(3):
        try:
            return call()
        except:
            pass
    raise

MAX = 3  # already named for one usage
`,
	})

	if _, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": "3",
		"name":    "RETRIES",
	}); isErr {
		t.Fatal("promote errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	got := readFile(t, h, "main.py")
	if !strings.Contains(got, `RETRIES = 3`) {
		t.Errorf("declaration missing:\n%s", got)
	}
	if !strings.Contains(got, `range(RETRIES)`) {
		t.Errorf("range not rewritten:\n%s", got)
	}
	// MAX = 3 line: the 3 should also become RETRIES (we replace every
	// numeric literal whose source equals "3", regardless of context).
	if !strings.Contains(got, `MAX = RETRIES`) {
		t.Errorf("MAX assignment not rewritten:\n%s", got)
	}
}

// TestPromoteConstantIdempotent verifies that running the tool twice with
// the same name+value is a no-op the second time (no duplicate declaration,
// no "no matches" since occurrences are already gone).
func TestPromoteConstantIdempotent(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `def f():
    return "secret-token"
`,
	})

	if _, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": `"secret-token"`,
		"name":    "TOKEN",
	}); isErr {
		t.Fatal("first promote errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("first apply errored")
	}
	first := readFile(t, h, "main.py")

	// Re-parse so the model sees the new file.
	if _, isErr, _ := h.handleParse(map[string]any{"path": h.model.Root}); isErr {
		t.Fatal("re-parse errored")
	}

	// Second run: only the declaration's RHS contains the literal; we should
	// detect it and not rewrite (or duplicate the declaration).
	text, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": `"secret-token"`,
		"name":    "TOKEN",
	})
	if isErr {
		t.Errorf("second promote errored: %s", text)
	}
	// File should be unchanged: either "not found in scope" or no edits applied.
	second := readFile(t, h, "main.py")
	if first != second {
		t.Errorf("idempotent run changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// And only one declaration line.
	if strings.Count(second, `TOKEN = "secret-token"`) != 1 {
		t.Errorf("duplicate declaration after second run:\n%s", second)
	}
}

// TestPromoteConstantNotFound verifies the "no matches" return shape.
func TestPromoteConstantNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	text, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": `"never-here"`,
		"name":    "X",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not-found message, got: %s", text)
	}
}

// TestPromoteConstantValidation verifies parameter validation.
func TestPromoteConstantValidation(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing literal", map[string]any{"name": "X"}},
		{"missing name", map[string]any{"literal": "1"}},
		{"empty literal", map[string]any{"literal": "", "name": "X"}},
		{"bad name (digits first)", map[string]any{"literal": "1", "name": "1bad"}},
		{"bad name (special chars)", map[string]any{"literal": "1", "name": "a-b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, isErr, _ := h.handlePromoteConstant(c.args)
			if !isErr {
				t.Errorf("expected error")
			}
		})
	}
}

// TestPromoteConstantPathFilter verifies that the path filter restricts
// the rewrite to matching files.
func TestPromoteConstantPathFilter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py":  `x = "target"`,
		"other.py": `y = "target"`,
	})

	if _, isErr, _ := h.handlePromoteConstant(map[string]any{
		"literal": `"target"`,
		"name":    "T",
		"path":    "main.py",
	}); isErr {
		t.Fatal("promote errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	main := readFile(t, h, "main.py")
	other := readFile(t, h, "other.py")
	if !strings.Contains(main, "T = ") {
		t.Errorf("main.py not promoted:\n%s", main)
	}
	if strings.Contains(other, "T = ") {
		t.Errorf("other.py was incorrectly modified:\n%s", other)
	}
}

// TestLooksLikeIdentifier tests the helper used to validate const names.
func TestLooksLikeIdentifier(t *testing.T) {
	cases := map[string]bool{
		"":         false,
		"X":        true,
		"_x":       true,
		"x1":       true,
		"1x":       false,
		"hello":    true,
		"with-dash": false,
		"with space": false,
		"a.b":      false,
		"UPPER_CASE": true,
	}
	for in, want := range cases {
		if got := looksLikeIdentifier(in); got != want {
			t.Errorf("looksLikeIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}
