// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/store"
)

// teachFixForTest is a small helper that saves a fix entry on the given handler.
func teachFixForTest(t *testing.T, h *Handler, name, regex, action, confidence string) {
	t.Helper()
	args := map[string]any{
		"name":             name,
		"diagnostic_regex": regex,
		"action":           action,
	}
	if confidence != "" {
		args["confidence"] = confidence
	}
	if _, isErr, err := h.handleTeachFix(args); err != nil || isErr {
		t.Fatalf("teach_fix %s failed", name)
	}
}

// stubDeps builds an autoFixDeps where collect returns the given sequence of
// diagnostic batches (one per iteration, then empty), and apply records each
// invocation in callOrder.
func stubDeps(batches [][]lspclient.Diagnostic, applyErr error, callOrder *[]string) autoFixDeps {
	idx := 0
	return autoFixDeps{
		collect: func(file string) ([]lspclient.Diagnostic, error) {
			if idx >= len(batches) {
				return nil, nil
			}
			d := batches[idx]
			idx++
			return d, nil
		},
		apply: func(entry store.Fix, captures map[string]string) error {
			*callOrder = append(*callOrder, entry.Name)
			return applyErr
		},
	}
}

// TestAutoFixCleanTermination verifies that an empty initial diagnostic
// batch terminates immediately with reason="clean".
func TestAutoFixCleanTermination(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	var calls []string
	deps := stubDeps([][]lspclient.Diagnostic{nil}, nil, &calls)

	text, isErr, err := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	if err != nil || isErr {
		t.Fatalf("runAutoFix: err=%v isErr=%v text=%s", err, isErr, text)
	}
	var result AutoFixResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if result.TerminationReason != "clean" {
		t.Errorf("termination = %q, want clean", result.TerminationReason)
	}
	if len(calls) != 0 {
		t.Errorf("expected no apply calls, got %v", calls)
	}
}

// TestAutoFixAppliesAndConverges verifies the loop applies a matching auto
// fix, then re-runs diagnostics, sees a clean state, and terminates.
func TestAutoFixAppliesAndConverges(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h,
		"unused-import",
		`imported and not used: "(?P<pkg>[^"]+)"`,
		`{"recipe":"remove-import","params":{"name":"${pkg}"}}`,
		"auto",
	)

	batches := [][]lspclient.Diagnostic{
		{{File: "main.go", Line: 3, Column: 1, Severity: "error",
			Code: "UnusedImport", Source: "gopls",
			Message: `imported and not used: "fmt"`}},
		nil, // post-apply: clean
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	var result AutoFixResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshalling: %v\nraw:\n%s", err, text)
	}
	if result.TerminationReason != "clean" {
		t.Errorf("termination = %q, want clean", result.TerminationReason)
	}
	if len(result.Iterations) != 1 {
		t.Fatalf("expected 1 iteration, got %d", len(result.Iterations))
	}
	if len(result.Iterations[0].FixesApplied) != 1 {
		t.Fatalf("expected 1 applied fix, got %d", len(result.Iterations[0].FixesApplied))
	}
	applied := result.Iterations[0].FixesApplied[0]
	if applied.Fix != "unused-import" {
		t.Errorf("applied fix = %q", applied.Fix)
	}
	if applied.Captures["pkg"] != "fmt" {
		t.Errorf("captures.pkg = %q, want fmt", applied.Captures["pkg"])
	}
	if !equalStringSlice(calls, []string{"unused-import"}) {
		t.Errorf("apply calls = %v", calls)
	}
}

// TestAutoFixSuggestNotApplied verifies that confidence=suggest matches are
// reported but never applied.
func TestAutoFixSuggestNotApplied(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h,
		"maybe-undef",
		`object is possibly undefined: (?P<name>\w+)`,
		`{"recipe":"add-null-check","params":{"name":"${name}"}}`,
		"suggest",
	)

	batches := [][]lspclient.Diagnostic{
		{{File: "main.ts", Line: 1, Column: 1, Severity: "warning",
			Code: "2532", Source: "tsserver",
			Message: "object is possibly undefined: foo"}},
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.ts"}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	// suggest matches don't count as fixes-applied → loop terminates as stuck.
	if result.TerminationReason != "stuck" {
		t.Errorf("termination = %q, want stuck", result.TerminationReason)
	}
	if len(result.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(result.Suggestions))
	}
	if result.Suggestions[0].Reason != "confidence_suggest" {
		t.Errorf("suggestion reason = %q", result.Suggestions[0].Reason)
	}
	if len(calls) != 0 {
		t.Errorf("apply called for suggest entry: %v", calls)
	}
}

// TestAutoFixDryRunDoesNotApply verifies dry_run=true converts auto entries
// into suggestions instead of applying them.
func TestAutoFixDryRunDoesNotApply(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "x", `boom`, `{"recipe":"r"}`, "auto")

	batches := [][]lspclient.Diagnostic{
		{{File: "main.go", Line: 1, Column: 1, Severity: "error", Message: "boom"}},
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go", "dry_run": true}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if !result.DryRun {
		t.Error("expected DryRun=true in result")
	}
	if len(result.Suggestions) != 1 || result.Suggestions[0].Reason != "dry_run" {
		t.Errorf("expected dry_run suggestion, got %+v", result.Suggestions)
	}
	if len(calls) != 0 {
		t.Errorf("apply called during dry run: %v", calls)
	}
}

// TestAutoFixCycleDetection verifies that a diagnostic that reappears after
// its fix was applied is flagged as a cycle and the loop doesn't retry the
// same fix forever.
func TestAutoFixCycleDetection(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "broken-fix", `boom`, `{"recipe":"noop"}`, "auto")

	// The diagnostic persists across iterations even though we "applied" a fix.
	diag := lspclient.Diagnostic{
		File: "main.go", Line: 1, Column: 1, Severity: "error",
		Code: "BOOM", Message: "boom",
	}
	batches := [][]lspclient.Diagnostic{
		{diag}, // iter 1: apply
		{diag}, // iter 2: cycle warning, no fix applied → stuck
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if result.TerminationReason != "stuck" {
		t.Errorf("termination = %q, want stuck", result.TerminationReason)
	}
	if len(result.CycleWarnings) != 1 {
		t.Fatalf("expected 1 cycle warning, got %d", len(result.CycleWarnings))
	}
	if result.CycleWarnings[0].Fix != "broken-fix" {
		t.Errorf("cycle warning fix = %q", result.CycleWarnings[0].Fix)
	}
	// First apply happens; second iteration recognises the cycle and skips.
	if !equalStringSlice(calls, []string{"broken-fix"}) {
		t.Errorf("apply calls = %v, want one call", calls)
	}
}

// TestAutoFixIterationLimit verifies the loop stops at the iteration cap
// even if diagnostics persist after fixes are applied (different ones each
// iteration so cycle detection doesn't kick in).
func TestAutoFixIterationLimit(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "tweak",
		`boom (?P<n>\d+)`,
		`{"recipe":"r","params":{"x":"${n}"}}`,
		"auto",
	)

	// Each iteration brings a different diagnostic so cycle detection
	// doesn't fire — we want to test the iteration-limit path.
	batches := make([][]lspclient.Diagnostic, 0, 5)
	for i := 1; i <= 5; i++ {
		batches = append(batches, []lspclient.Diagnostic{
			{File: "main.go", Line: uint32(i), Column: 1, Severity: "error",
				Message: "boom " + itoaStub(i)},
		})
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(
		map[string]any{"file": "main.go", "max_iterations": float64(3)},
		deps,
	)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if result.TerminationReason != "iteration_limit" {
		t.Errorf("termination = %q, want iteration_limit", result.TerminationReason)
	}
	if len(result.Iterations) != 3 {
		t.Errorf("expected 3 iterations, got %d", len(result.Iterations))
	}
	if len(calls) != 3 {
		t.Errorf("expected 3 apply calls, got %d (%v)", len(calls), calls)
	}
}

// TestAutoFixNoMatch verifies that diagnostics with no matching fix entry
// terminate the loop as stuck (no fixes applied).
func TestAutoFixNoMatch(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "wrong-pattern", `does not match anything`, `{"recipe":"r"}`, "auto")

	batches := [][]lspclient.Diagnostic{
		{{File: "main.go", Line: 1, Column: 1, Severity: "error", Message: "an unrelated error"}},
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if result.TerminationReason != "stuck" {
		t.Errorf("termination = %q, want stuck", result.TerminationReason)
	}
	if len(result.RemainingDiagnostics) != 1 {
		t.Errorf("expected 1 remaining diagnostic, got %d", len(result.RemainingDiagnostics))
	}
	if len(calls) != 0 {
		t.Errorf("expected no apply calls, got %v", calls)
	}
}

// TestSubstituteCaptures verifies the placeholder substitution helper
// (used to bind regex captures into action JSON before dispatch).
func TestSubstituteCaptures(t *testing.T) {
	cases := []struct {
		in       string
		captures map[string]string
		want     string
	}{
		{`{"name":"${pkg}"}`, map[string]string{"pkg": "fmt"}, `{"name":"fmt"}`},
		{`{"a":"${x}","b":"${y}"}`, map[string]string{"x": "1", "y": "2"}, `{"a":"1","b":"2"}`},
		{`{"name":"${pkg}"}`, map[string]string{}, `{"name":"${pkg}"}`}, // missing left as-is
		{`{"x":"a${one}b${two}c"}`, map[string]string{"one": "1", "two": "2"}, `{"x":"a1b2c"}`},
		{`{"x":"escape ${q}"}`, map[string]string{"q": `"quote"`}, `{"x":"escape \"quote\""}`},
	}
	for _, c := range cases {
		got := substituteCaptures(c.in, c.captures)
		if got != c.want {
			t.Errorf("substituteCaptures(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDiagnosticSignature verifies that two diagnostics with the same
// file+code+message produce the same signature, even with different
// line/column.
func TestDiagnosticSignature(t *testing.T) {
	a := lspclient.Diagnostic{File: "x.go", Line: 1, Column: 1, Code: "C", Message: "m"}
	b := lspclient.Diagnostic{File: "x.go", Line: 99, Column: 99, Code: "C", Message: "m"}
	c := lspclient.Diagnostic{File: "y.go", Line: 1, Column: 1, Code: "C", Message: "m"}
	if diagnosticSignature(a) != diagnosticSignature(b) {
		t.Error("same file+code+message should produce same signature regardless of position")
	}
	if diagnosticSignature(a) == diagnosticSignature(c) {
		t.Error("different file should produce different signature")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// itoaStub avoids pulling in strconv just for one call site in the iteration-limit test.
func itoaStub(n int) string {
	switch n {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	default:
		// Use string concatenation via Sprintf inline if needed.
		s := ""
		for n > 0 {
			s = string(rune('0'+(n%10))) + s
			n /= 10
		}
		return s
	}
}

// TestAutoFixRequiresFile verifies the file param is required.
func TestAutoFixRequiresFile(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	_, isErr, _ := h.runAutoFix(map[string]any{}, autoFixDeps{
		collect: func(string) ([]lspclient.Diagnostic, error) { return nil, nil },
		apply:   func(store.Fix, map[string]string) error { return nil },
	})
	if !isErr {
		t.Error("expected error when file is missing")
	}
}

// TestAutoFixDefaultMaxIterations verifies max_iterations defaults to 10.
func TestAutoFixDefaultMaxIterations(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "tweak",
		`boom (?P<n>\d+)`,
		`{"recipe":"r","params":{"x":"${n}"}}`,
		"auto",
	)
	// 12 distinct diagnostics — should stop at iteration 10.
	batches := make([][]lspclient.Diagnostic, 12)
	for i := range batches {
		batches[i] = []lspclient.Diagnostic{
			{File: "main.go", Line: uint32(i + 1), Column: 1, Severity: "error",
				Message: "boom " + itoaStub(i+1)},
		}
	}
	var calls []string
	deps := stubDeps(batches, nil, &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if result.TerminationReason != "iteration_limit" {
		t.Errorf("termination = %q, want iteration_limit", result.TerminationReason)
	}
	if len(result.Iterations) != 10 {
		t.Errorf("expected 10 iterations (default), got %d", len(result.Iterations))
	}
}

// TestAutoFixApplyErrorRecorded verifies that an apply error is recorded as
// a cycle warning rather than aborting the whole loop.
func TestAutoFixApplyErrorRecorded(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	teachFixForTest(t, h, "x", `err: (?P<m>.+)`, `{"recipe":"r","params":{"name":"${m}"}}`, "auto")

	// Diagnostic disappears on iter 2 even though apply errored — proves the
	// loop kept going and the warning was captured.
	batches := [][]lspclient.Diagnostic{
		{{File: "main.go", Line: 1, Column: 1, Severity: "error", Message: "err: foo"}},
		nil,
	}
	var calls []string
	deps := stubDeps(batches, fakeError("recipe not found"), &calls)

	text, _, _ := h.runAutoFix(map[string]any{"file": "main.go"}, deps)
	var result AutoFixResult
	_ = json.Unmarshal([]byte(text), &result)

	if result.TerminationReason != "stuck" {
		t.Errorf("termination = %q, want stuck (no successful fixes applied)", result.TerminationReason)
	}
	if len(result.CycleWarnings) != 1 {
		t.Fatalf("expected 1 cycle warning, got %d", len(result.CycleWarnings))
	}
	if !strings.Contains(result.CycleWarnings[0].Fix, "apply error") {
		t.Errorf("cycle warning fix = %q, want \"apply error\" mention", result.CycleWarnings[0].Fix)
	}
}

type fakeError string

func (e fakeError) Error() string { return string(e) }
