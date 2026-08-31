// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCheckConventionsLegacyTextMode verifies the prose path is unchanged
// when a JS check returns []string and no format flag is passed.
func TestCheckConventionsLegacyTextMode(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})

	if _, isErr, _ := h.handleTeachConvention(map[string]any{
		"name":          "always-fails",
		"check_program": "return ['something is wrong'];",
	}); isErr {
		t.Fatal("teach convention failed")
	}

	text, isErr, err := h.handleCheckConventions(map[string]any{})
	if err != nil || isErr {
		t.Fatalf("check: err=%v isErr=%v text=%s", err, isErr, text)
	}
	for _, want := range []string{`Convention "always-fails": 1 violation(s):`, "something is wrong"} {
		if !strings.Contains(text, want) {
			t.Errorf("text mode missing %q in:\n%s", want, text)
		}
	}
}

// TestCheckConventionsJSONMode verifies that format=json returns a parseable
// array of structured Violation objects, even for legacy []string returns.
func TestCheckConventionsJSONMode(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	if _, isErr, _ := h.handleTeachConvention(map[string]any{
		"name":          "legacy-strings",
		"check_program": "return ['v1', 'v2'];",
	}); isErr {
		t.Fatal("teach legacy convention failed")
	}

	text, isErr, err := h.handleCheckConventions(map[string]any{"format": "json"})
	if err != nil || isErr {
		t.Fatalf("check json: err=%v isErr=%v text=%s", err, isErr, text)
	}

	var got []Violation
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshalling json output: %v\nraw:\n%s", err, text)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(got))
	}
	for i, v := range got {
		if v.Source != "convention:legacy-strings" {
			t.Errorf("violation[%d].Source = %q, want convention:legacy-strings", i, v.Source)
		}
		if v.Rule != "legacy-strings" {
			t.Errorf("violation[%d].Rule = %q, want legacy-strings", i, v.Rule)
		}
		if v.Severity != "error" {
			t.Errorf("violation[%d].Severity = %q, want error (default)", i, v.Severity)
		}
		if v.Message == "" {
			t.Errorf("violation[%d].Message empty", i)
		}
	}
}

// TestCheckConventionsStructuredReturn verifies that a JS program returning
// objects (not plain strings) flows through to structured output.
func TestCheckConventionsStructuredReturn(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	prog := `return [{
		file: "src/foo.go",
		line: 42,
		column: 10,
		severity: "warning",
		rule: "no-bare-except",
		message: "use specific exception",
		snippet: "except:",
		suggested_fix: "except Exception:"
	}];`
	if _, isErr, _ := h.handleTeachConvention(map[string]any{
		"name":          "structured",
		"check_program": prog,
	}); isErr {
		t.Fatal("teach structured convention failed")
	}

	text, isErr, _ := h.handleCheckConventions(map[string]any{"format": "json"})
	if isErr {
		t.Fatalf("check json error: %s", text)
	}
	var got []Violation
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshalling: %v\nraw:\n%s", err, text)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(got))
	}
	v := got[0]
	if v.File != "src/foo.go" || v.Line != 42 || v.Column != 10 {
		t.Errorf("location wrong: %+v", v)
	}
	if v.Severity != "warning" {
		t.Errorf("severity = %q, want warning", v.Severity)
	}
	if v.Rule != "no-bare-except" {
		t.Errorf("rule = %q, want no-bare-except", v.Rule)
	}
	if v.SuggestedFix != "except Exception:" {
		t.Errorf("suggested_fix = %q", v.SuggestedFix)
	}
	if v.Source != "convention:structured" {
		t.Errorf("source = %q, want convention:structured", v.Source)
	}
}

// TestCheckConventionsTextRendersStructured verifies that structured returns
// also render nicely in text mode (file:line:col: message).
func TestCheckConventionsTextRendersStructured(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	prog := `return [{file: "x.go", line: 7, column: 3, message: "boom"}];`
	if _, isErr, _ := h.handleTeachConvention(map[string]any{
		"name":          "structured-text",
		"check_program": prog,
	}); isErr {
		t.Fatal("teach failed")
	}
	text, _, _ := h.handleCheckConventions(map[string]any{})
	if !strings.Contains(text, "x.go:7:3: boom") {
		t.Errorf("expected file:line:col: rendering, got:\n%s", text)
	}
}

// TestCheckConventionsBadFormat verifies the format param is validated.
func TestCheckConventionsBadFormat(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})
	_, isErr, _ := h.handleCheckConventions(map[string]any{"format": "xml"})
	if !isErr {
		t.Error("expected error for invalid format")
	}
}

// TestCheckInvariantsJSONMode verifies that check_invariants emits a
// structured Violation array under format=json.
func TestCheckInvariantsJSONMode(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\ntype Config struct{}\n",
	})
	rule := `{"for_each":{"kind":"type","name":"Config"},"require":[{"has_field":{"name":"Name","type":"string"}}]}`
	if _, isErr, _ := h.handleTeachInvariant(map[string]any{
		"name":        "config-needs-name",
		"description": "Config types must declare a Name string field",
		"rule":        rule,
	}); isErr {
		t.Fatal("teach invariant failed")
	}

	text, isErr, _ := h.handleCheckInvariants(map[string]any{"format": "json"})
	if isErr {
		t.Fatalf("check json error: %s", text)
	}
	var got []Violation
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshalling: %v\nraw:\n%s", err, text)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(got))
	}
	v := got[0]
	if v.Source != "invariant:config-needs-name" {
		t.Errorf("source = %q", v.Source)
	}
	if !strings.Contains(v.Message, "missing field") {
		t.Errorf("message lacks 'missing field': %q", v.Message)
	}
	if !strings.HasSuffix(v.File, "main.go") {
		t.Errorf("file should be main.go, got %q", v.File)
	}
	if v.Line == 0 {
		t.Errorf("line should be populated, got %d", v.Line)
	}
}

// TestCheckInvariantsTextStillWorks verifies the prose mode is unchanged.
func TestCheckInvariantsTextStillWorks(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\ntype Config struct{}\n",
	})
	rule := `{"for_each":{"kind":"type","name":"Config"},"require":[{"has_field":{"name":"Name","type":"string"}}]}`
	if _, isErr, _ := h.handleTeachInvariant(map[string]any{
		"name": "needs-name", "rule": rule,
	}); isErr {
		t.Fatal("teach failed")
	}
	text, _, _ := h.handleCheckInvariants(map[string]any{})
	for _, want := range []string{`Invariant "needs-name": 1 violation(s):`, "missing field"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q:\n%s", want, text)
		}
	}
}

// TestQueryJSONMode verifies that query with format=json returns a structured
// array of QueryMatch objects.
func TestQueryJSONMode(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def alpha():\n    pass\n\ndef beta():\n    pass\n",
	})
	text, isErr, _ := h.handleQuery(map[string]any{
		"kind":   "function",
		"name":   "*",
		"format": "json",
	})
	if isErr {
		t.Fatalf("query error: %s", text)
	}
	var got []QueryMatch
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshalling: %v\nraw:\n%s", err, text)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(got))
	}
	for i, m := range got {
		if m.File == "" {
			t.Errorf("match[%d].File empty", i)
		}
		if m.Line == 0 {
			t.Errorf("match[%d].Line zero", i)
		}
		// Kind is the underlying tree-sitter node type (e.g. "function_definition"
		// for Python). The text-mode output shows the same raw kind, so JSON
		// faithfully mirrors it.
		if !strings.Contains(m.Kind, "function") {
			t.Errorf("match[%d].Kind = %q, want something containing 'function'", i, m.Kind)
		}
		if m.Name == "" {
			t.Errorf("match[%d].Name empty", i)
		}
	}
}

// TestQueryTextStillWorks verifies the prose mode for query is unchanged.
func TestQueryTextStillWorks(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def alpha():\n    pass\n",
	})
	text, _, _ := h.handleQuery(map[string]any{
		"kind": "function",
		"name": "alpha",
	})
	if !strings.Contains(text, "alpha") {
		t.Errorf("expected alpha in prose, got:\n%s", text)
	}
	if !strings.Contains(text, "match(es)") {
		t.Errorf("expected human-readable header, got:\n%s", text)
	}
}

// TestEmptyResultsInJSONMode verifies that empty result sets return [] (a
// valid JSON array), not a prose "no matches" message — important for
// programmatic consumers.
func TestEmptyResultsInJSONMode(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})

	// No conventions defined.
	text, _, _ := h.handleCheckConventions(map[string]any{"format": "json"})
	if text != "[]" {
		t.Errorf("expected [], got: %s", text)
	}
	// No invariants defined.
	text, _, _ = h.handleCheckInvariants(map[string]any{"format": "json"})
	if text != "[]" {
		t.Errorf("expected [], got: %s", text)
	}
	// Query with no matches.
	text, _, _ = h.handleQuery(map[string]any{
		"kind":   "function",
		"name":   "nonexistent",
		"format": "json",
	})
	var matches []QueryMatch
	if err := json.Unmarshal([]byte(text), &matches); err != nil {
		t.Errorf("expected valid JSON array, got: %s (err: %v)", text, err)
	}
	if len(matches) != 0 {
		t.Errorf("expected empty array, got %d matches", len(matches))
	}
}
