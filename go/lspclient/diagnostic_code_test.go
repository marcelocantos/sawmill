// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package lspclient

import (
	"encoding/json"
	"testing"
)

func TestNormaliseDiagnosticCode(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"absent (empty raw)", "", ""},
		{"explicit null", "null", ""},
		{"string code (gopls style)", `"UnusedImport"`, "UnusedImport"},
		{"integer code (TS style)", `2304`, "2304"},
		{"large integer", `9007199254740993`, "9007199254740993"},
		{"empty string", `""`, ""},
		{"object form", `{}`, ""}, // unsupported shapes degrade to empty
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normaliseDiagnosticCode(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("normaliseDiagnosticCode(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestParseDiagnosticsNotificationCodeAndSource verifies that an LSP
// publishDiagnostics payload's `code` and `source` fields populate the
// matching Diagnostic struct fields. Covers both the gopls (string code)
// and TypeScript (integer code) shapes plus the absent-code case.
func TestParseDiagnosticsNotificationCodeAndSource(t *testing.T) {
	const targetURI = "file:///tmp/x.go"
	params := map[string]any{
		"uri": targetURI,
		"diagnostics": []map[string]any{
			{
				"range": map[string]any{
					"start": map[string]any{"line": 0, "character": 0},
				},
				"severity": 1,
				"code":     "UnusedImport",
				"source":   "gopls",
				"message":  `imported and not used: "fmt"`,
			},
			{
				"range": map[string]any{
					"start": map[string]any{"line": 5, "character": 12},
				},
				"severity": 1,
				"code":     2304,
				"source":   "tsserver",
				"message":  "Cannot find name 'foo'",
			},
			{
				"range": map[string]any{
					"start": map[string]any{"line": 1, "character": 0},
				},
				"severity": 2,
				"message":  "no code here",
			},
		},
	}
	diags := parseDiagnosticsNotification(params, targetURI, "/tmp/x.go")
	if len(diags) != 3 {
		t.Fatalf("got %d diagnostics, want 3", len(diags))
	}
	// String code preserved.
	if diags[0].Code != "UnusedImport" {
		t.Errorf("diag[0].Code = %q, want UnusedImport", diags[0].Code)
	}
	if diags[0].Source != "gopls" {
		t.Errorf("diag[0].Source = %q, want gopls", diags[0].Source)
	}
	// Integer code stringified.
	if diags[1].Code != "2304" {
		t.Errorf("diag[1].Code = %q, want \"2304\"", diags[1].Code)
	}
	if diags[1].Source != "tsserver" {
		t.Errorf("diag[1].Source = %q, want tsserver", diags[1].Source)
	}
	// Absent code → empty string (not "0", not "null").
	if diags[2].Code != "" {
		t.Errorf("diag[2].Code = %q, want empty", diags[2].Code)
	}
	if diags[2].Source != "" {
		t.Errorf("diag[2].Source = %q, want empty", diags[2].Source)
	}
}

// TestParseDiagnosticsFiltersByURI verifies the existing filter behaviour
// is preserved after the schema change.
func TestParseDiagnosticsFiltersByURI(t *testing.T) {
	params := map[string]any{
		"uri": "file:///tmp/other.go",
		"diagnostics": []map[string]any{
			{
				"range":    map[string]any{"start": map[string]any{"line": 0, "character": 0}},
				"severity": 1,
				"message":  "noise",
			},
		},
	}
	diags := parseDiagnosticsNotification(params, "file:///tmp/x.go", "/tmp/x.go")
	if len(diags) != 0 {
		t.Errorf("got %d diagnostics for non-matching URI; want 0", len(diags))
	}
}
