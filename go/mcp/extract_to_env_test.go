// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractToEnvPython verifies the basic Python flow: literal becomes
// os.environ.get(VAR), .env.example and .gitignore are scaffolded.
func TestExtractToEnvPython(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `import os

def fetch():
    return get("http://example.com/api")
`,
	})

	text, isErr, err := h.handleExtractToEnv(map[string]any{
		"literal":  `"http://example.com/api"`,
		"var_name": "API_URL",
	})
	if err != nil || isErr {
		t.Fatalf("extract_to_env: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "1 replacement") {
		t.Errorf("expected 1 replacement in summary, got: %s", text)
	}

	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}

	got := readFile(t, h, "main.py")
	if !strings.Contains(got, `get(os.environ.get("API_URL"))`) {
		t.Errorf("call site not rewritten with env read:\n%s", got)
	}
	envEx := readFile(t, h, ".env.example")
	if !strings.Contains(envEx, "API_URL=http://example.com/api") {
		t.Errorf(".env.example missing API_URL line:\n%s", envEx)
	}
	gi := readFile(t, h, ".gitignore")
	if !strings.Contains(gi, ".env") {
		t.Errorf(".gitignore missing .env entry:\n%s", gi)
	}
}

// TestExtractToEnvGo verifies Go syntax: literal becomes os.Getenv("VAR").
func TestExtractToEnvGo(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import "os"

func main() {
	conn := connect("postgres://localhost:5432/db")
	_ = conn
	_ = os.Args
}

func connect(url string) any { return nil }
`,
	})

	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal":  `"postgres://localhost:5432/db"`,
		"var_name": "DATABASE_URL",
	}); isErr {
		t.Fatal("extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}

	got := readFile(t, h, "main.go")
	if !strings.Contains(got, `connect(os.Getenv("DATABASE_URL"))`) {
		t.Errorf("call site not rewritten:\n%s", got)
	}
}

// TestExtractToEnvAppendsToExistingFiles verifies that pre-existing
// .env.example and .gitignore are appended to, not overwritten.
func TestExtractToEnvAppendsToExistingFiles(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py":      `def f(): return "secret"`,
		".env.example": "EXISTING_KEY=value\n",
		".gitignore":   "node_modules/\n",
	})

	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal":  `"secret"`,
		"var_name": "TOKEN",
	}); isErr {
		t.Fatal("extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}

	envEx := readFile(t, h, ".env.example")
	if !strings.Contains(envEx, "EXISTING_KEY=value") {
		t.Errorf("existing key was clobbered:\n%s", envEx)
	}
	if !strings.Contains(envEx, "TOKEN=secret") {
		t.Errorf("new key not appended:\n%s", envEx)
	}
	gi := readFile(t, h, ".gitignore")
	if !strings.Contains(gi, "node_modules/") {
		t.Errorf("existing gitignore content was clobbered:\n%s", gi)
	}
	if !strings.Contains(gi, ".env") {
		t.Errorf(".env not added to existing .gitignore:\n%s", gi)
	}
}

// TestExtractToEnvIdempotent verifies that re-running with the same key
// doesn't duplicate .env.example entries or .gitignore lines.
func TestExtractToEnvIdempotent(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `def f(): return "x"`,
	})

	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal":  `"x"`,
		"var_name": "X",
	}); isErr {
		t.Fatal("first extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("first apply errored")
	}

	// Re-parse, run again with the same key. The literal is now gone from
	// source so we expect "not found in scope".
	if _, isErr, _ := h.handleParse(map[string]any{"path": h.model.Root}); isErr {
		t.Fatal("re-parse errored")
	}
	text, _, _ := h.handleExtractToEnv(map[string]any{
		"literal":  `"x"`,
		"var_name": "X",
	})
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not-found on second run, got: %s", text)
	}

	// And .env.example / .gitignore are unchanged (X appears once each).
	envEx := readFile(t, h, ".env.example")
	if strings.Count(envEx, "X=") != 1 {
		t.Errorf("expected one X= line, got:\n%s", envEx)
	}
	gi := readFile(t, h, ".gitignore")
	if strings.Count(gi, ".env") != 1 {
		t.Errorf("expected one .env line, got:\n%s", gi)
	}
}

// TestExtractToEnvUpdatesExistingKey verifies that if a key already exists
// in .env.example with a different value, the value is updated.
func TestExtractToEnvUpdatesExistingKey(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py":      `def f(): return "newval"`,
		".env.example": "MY_KEY=oldval\n",
	})

	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal":  `"newval"`,
		"var_name": "MY_KEY",
	}); isErr {
		t.Fatal("extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	envEx := readFile(t, h, ".env.example")
	if strings.Contains(envEx, "oldval") {
		t.Errorf("old value not replaced:\n%s", envEx)
	}
	if !strings.Contains(envEx, "MY_KEY=newval") {
		t.Errorf("new value not present:\n%s", envEx)
	}
	if strings.Count(envEx, "MY_KEY=") != 1 {
		t.Errorf("expected one MY_KEY entry, got:\n%s", envEx)
	}
}

// TestExtractToEnvPreservesGitignoreWildcard verifies that an existing
// `*.env` or similar wildcard pattern is honoured.
func TestExtractToEnvPreservesGitignoreWildcard(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py":    `def f(): return "v"`,
		".gitignore": "node_modules/\n.env.*\n",
	})
	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal": `"v"`, "var_name": "V",
	}); isErr {
		t.Fatal("extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	gi := readFile(t, h, ".gitignore")
	if strings.Count(gi, ".env") != 1 {
		t.Errorf(".env line should not be added when wildcard already covers it; got:\n%s", gi)
	}
}

// TestExtractToEnvValidation verifies parameter validation.
func TestExtractToEnvValidation(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing literal", map[string]any{"var_name": "X"}},
		{"missing var_name", map[string]any{"literal": `"x"`}},
		{"empty literal", map[string]any{"literal": "", "var_name": "X"}},
		{"bad var_name (digits first)", map[string]any{"literal": `"x"`, "var_name": "1BAD"}},
		{"bad var_name (special chars)", map[string]any{"literal": `"x"`, "var_name": "A-B"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, isErr, _ := h.handleExtractToEnv(c.args)
			if !isErr {
				t.Errorf("expected error")
			}
		})
	}
}

// TestUnquoteLiteral tests the helper.
func TestUnquoteLiteral(t *testing.T) {
	cases := map[string]string{
		`"foo"`:    "foo",
		`'bar'`:    "bar",
		"`baz`":    "baz",
		`42`:       "42",
		`""`:       "",
		`""""`:     `""`, // outer pair stripped, leaving inner pair
		`"unclosed`: `"unclosed`,
		``:         ``,
	}
	for in, want := range cases {
		if got := unquoteLiteral(in); got != want {
			t.Errorf("unquoteLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

// helper mirroring readFile but returning empty string if the file doesn't exist.
func readOptional(t *testing.T, h *Handler, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.model.Root, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// TestExtractToEnvCreatesGitignoreFromScratch verifies a project without a
// .gitignore file gets one created.
func TestExtractToEnvCreatesGitignoreFromScratch(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": `def f(): return "x"`})
	if got := readOptional(t, h, ".gitignore"); got != "" {
		t.Fatalf("test fixture unexpectedly already has .gitignore: %q", got)
	}
	if _, isErr, _ := h.handleExtractToEnv(map[string]any{
		"literal": `"x"`, "var_name": "X",
	}); isErr {
		t.Fatal("extract errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	gi := readFile(t, h, ".gitignore")
	if !strings.Contains(gi, ".env") {
		t.Errorf("expected .env to be added, got:\n%s", gi)
	}
}
