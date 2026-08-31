// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/lspclient"
)

// TestDiagnosticsBadFormat verifies the format param is validated.
func TestDiagnosticsBadFormat(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	_, isErr, _ := h.handleDiagnostics(map[string]any{"file": "main.go", "format": "yaml"})
	if !isErr {
		t.Error("expected error for invalid format")
	}
}

// TestDiagnosticsJSONNoLSP verifies that when no LSP is configured for the
// file's language, JSON mode returns "[]" (a valid empty array) rather than
// the prose "no LSP available" message — important for programmatic
// consumers like auto_fix.
func TestDiagnosticsJSONNoLSP(t *testing.T) {
	// .txt has no language adapter and no LSP.
	h := testHandler(t, map[string]string{"notes.txt": "just a note\n"})
	text, isErr, _ := h.handleDiagnostics(map[string]any{"file": "notes.txt", "format": "json"})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if text != "[]" {
		t.Errorf("expected []; got %q", text)
	}
}

// TestDiagnosticsTextNoLSPUnchanged verifies the text-mode fallback message
// is preserved (backwards compat).
func TestDiagnosticsTextNoLSPUnchanged(t *testing.T) {
	h := testHandler(t, map[string]string{"notes.txt": "just a note\n"})
	text, isErr, _ := h.handleDiagnostics(map[string]any{"file": "notes.txt"})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	// Should be a human-readable string mentioning the absence of LSP support.
	if text == "[]" || strings.HasPrefix(text, "[") {
		t.Errorf("text mode should return prose, not JSON; got: %q", text)
	}
}

// TestFormatDiagnosticsCodeAndSource verifies that the prose formatter
// includes the new code/source columns when present, and stays compact
// when both are absent (preserves backwards compat for prose consumers).
func TestFormatDiagnosticsCodeAndSource(t *testing.T) {
	cases := []struct {
		name string
		diag lspclient.Diagnostic
		want string
	}{
		{
			name: "no code or source — backwards compat",
			diag: lspclient.Diagnostic{File: "x.go", Line: 1, Column: 1, Severity: "error", Message: "boom"},
			want: "x.go:1:1 [error] boom\n",
		},
		{
			name: "source only",
			diag: lspclient.Diagnostic{File: "x.go", Line: 1, Column: 1, Severity: "error", Source: "gopls", Message: "boom"},
			want: "x.go:1:1 [error] [gopls] boom\n",
		},
		{
			name: "code only",
			diag: lspclient.Diagnostic{File: "x.ts", Line: 5, Column: 12, Severity: "error", Code: "2304", Message: "Cannot find name 'foo'"},
			want: "x.ts:5:12 [error] [2304] Cannot find name 'foo'\n",
		},
		{
			name: "both source and code",
			diag: lspclient.Diagnostic{File: "x.go", Line: 1, Column: 1, Severity: "error", Source: "gopls", Code: "UnusedImport", Message: `imported and not used: "fmt"`},
			want: `x.go:1:1 [error] [gopls UnusedImport] imported and not used: "fmt"` + "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatDiagnostics([]lspclient.Diagnostic{c.diag})
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestDiagnosticJSONShape verifies the JSON marshalling shape is the one
// downstream tools (auto_fix etc.) will rely on: {file, line, column,
// severity, code?, source?, message}.
func TestDiagnosticJSONShape(t *testing.T) {
	d := lspclient.Diagnostic{
		File:     "x.go",
		Line:     10,
		Column:   5,
		Severity: "error",
		Code:     "UnusedImport",
		Source:   "gopls",
		Message:  "imported and not used",
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"file":"x.go"`, `"line":10`, `"column":5`, `"severity":"error"`,
		`"code":"UnusedImport"`, `"source":"gopls"`, `"message":"imported and not used"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("JSON missing %q in: %s", want, out)
		}
	}

	// And empty code/source should be omitted (not appear as "code":"").
	dEmpty := lspclient.Diagnostic{File: "x.go", Line: 1, Column: 1, Severity: "info", Message: "hi"}
	out, _ = json.Marshal(dEmpty)
	if strings.Contains(string(out), `"code"`) || strings.Contains(string(out), `"source"`) {
		t.Errorf("expected code/source to be omitted when empty, got: %s", out)
	}
}
