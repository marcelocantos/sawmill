// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestGeneraliseDiagnosticMessage(t *testing.T) {
	cases := []struct {
		name      string
		msg       string
		want      string
		matches   string
		shouldHit bool
	}{
		{
			name:      "double-quoted",
			msg:       `imported and not used: "fmt"`,
			want:      `^imported and not used: "(?P<arg1>[^"]+)"$`,
			matches:   `imported and not used: "strings"`,
			shouldHit: true,
		},
		{
			name:      "single-quoted",
			msg:       `'foo' is declared but its value is never read`,
			want:      `^'(?P<arg1>[^']+)' is declared but its value is never read$`,
			matches:   `'bar' is declared but its value is never read`,
			shouldHit: true,
		},
		{
			name:      "two captures",
			msg:       `cannot use "x" as "int" value`,
			want:      `^cannot use "(?P<arg1>[^"]+)" as "(?P<arg2>[^"]+)" value$`,
			matches:   `cannot use "y" as "string" value`,
			shouldHit: true,
		},
		{
			name:      "regex special chars escaped",
			msg:       `unexpected '.' (period)`,
			want:      `^unexpected '(?P<arg1>[^']+)' \(period\)$`,
			matches:   `unexpected ',' (period)`,
			shouldHit: true,
		},
		{
			name:      "no quotes — pure literal",
			msg:       `something boring`,
			want:      `^something boring$`,
			matches:   `something boring`,
			shouldHit: true,
		},
		{
			name:      "literal portion mismatch",
			msg:       `imported and not used: "fmt"`,
			want:      `^imported and not used: "(?P<arg1>[^"]+)"$`,
			matches:   `something else: "fmt"`,
			shouldHit: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := generaliseDiagnosticMessage(c.msg)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			re := regexp.MustCompile(got)
			if re.MatchString(c.matches) != c.shouldHit {
				t.Errorf("regex %q matching %q: got %v, want %v", got, c.matches, re.MatchString(c.matches), c.shouldHit)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"UnusedImport":         "unusedimport",
		"TS2304":               "ts2304",
		"hello world!":         "hello-world",
		"--hello--":            "hello",
		"":                     "diagnostic",
		"!!!":                  "diagnostic",
		"Cannot find name":     "cannot-find-name",
		"with: punctuation, .": "with-punctuation",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLearnFromObservationDetectsResolvedDiagnostics verifies the basic flow:
// give pre with N entries and post with N-1, expect 1 candidate matching
// the resolved one.
func TestLearnFromObservationDetectsResolvedDiagnostics(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	pre := `[
		{"file":"main.go","line":3,"column":1,"severity":"error","code":"UnusedImport","source":"gopls","message":"imported and not used: \"fmt\""},
		{"file":"main.go","line":5,"column":1,"severity":"error","code":"TypeMismatch","message":"cannot use foo as bar"}
	]`
	// fmt-import diagnostic resolves; TypeMismatch persists.
	post := `[
		{"file":"main.go","line":5,"column":1,"severity":"error","code":"TypeMismatch","message":"cannot use foo as bar"}
	]`

	text, isErr, err := h.handleLearnFromObservation(map[string]any{
		"pre_diagnostics":  pre,
		"post_diagnostics": post,
	})
	if err != nil || isErr {
		t.Fatalf("learn: err=%v isErr=%v text=%s", err, isErr, text)
	}
	var candidates []LearnedFixCandidate
	if err := json.Unmarshal([]byte(text), &candidates); err != nil {
		t.Fatalf("unmarshalling: %v\nraw:\n%s", err, text)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Source.Code != "UnusedImport" {
		t.Errorf("candidate source code = %q, want UnusedImport", c.Source.Code)
	}
	if !strings.Contains(c.SuggestedName, "unusedimport") {
		t.Errorf("suggested name = %q, expected to contain 'unusedimport'", c.SuggestedName)
	}
	if !strings.Contains(c.DiagnosticRegex, "(?P<arg1>") {
		t.Errorf("regex missing capture group: %q", c.DiagnosticRegex)
	}
	if c.Confidence != "suggest" {
		t.Errorf("confidence = %q, want suggest", c.Confidence)
	}
	// The candidate's regex should round-trip — match the original message
	// it was generalised from.
	re := regexp.MustCompile(c.DiagnosticRegex)
	if !re.MatchString(`imported and not used: "fmt"`) {
		t.Errorf("regex %q failed to match its source diagnostic", c.DiagnosticRegex)
	}
}

// TestLearnFromObservationEmptyPostDefaults verifies that omitting
// post_diagnostics treats every pre entry as resolved.
func TestLearnFromObservationEmptyPostDefaults(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	pre := `[
		{"file":"a.go","severity":"error","code":"X","message":"alpha 'one'"},
		{"file":"b.go","severity":"error","code":"Y","message":"beta 'two'"}
	]`
	text, _, _ := h.handleLearnFromObservation(map[string]any{"pre_diagnostics": pre})
	var candidates []LearnedFixCandidate
	_ = json.Unmarshal([]byte(text), &candidates)
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates with empty post, got %d", len(candidates))
	}
}

// TestLearnFromObservationNoResolved verifies that pre==post returns no candidates.
func TestLearnFromObservationNoResolved(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	pre := `[{"file":"x.go","severity":"error","code":"C","message":"unchanged"}]`
	text, _, _ := h.handleLearnFromObservation(map[string]any{
		"pre_diagnostics":  pre,
		"post_diagnostics": pre,
	})
	if text != "[]" {
		t.Errorf("expected [], got %q", text)
	}
}

// TestLearnFromObservationDeduplicatesNames verifies that two resolved
// diagnostics with the same code (and thus same suggested name) produce
// distinct candidate names (-2, -3 suffixes).
func TestLearnFromObservationDeduplicatesNames(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	pre := `[
		{"file":"a.go","severity":"error","code":"DUP","message":"variant 'one'"},
		{"file":"b.go","severity":"error","code":"DUP","message":"variant 'two'"}
	]`
	text, _, _ := h.handleLearnFromObservation(map[string]any{"pre_diagnostics": pre})
	var candidates []LearnedFixCandidate
	_ = json.Unmarshal([]byte(text), &candidates)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].SuggestedName == candidates[1].SuggestedName {
		t.Errorf("expected distinct names, both = %q", candidates[0].SuggestedName)
	}
	if !strings.HasSuffix(candidates[1].SuggestedName, "-2") {
		t.Errorf("second candidate name = %q, expected -2 suffix", candidates[1].SuggestedName)
	}
}

// TestLearnFromObservationCandidateActionIsValidJSON verifies the placeholder
// action stub the candidate ships with parses as JSON (so the user can plug
// it into teach_fix without first cleaning it up).
func TestLearnFromObservationCandidateActionIsValidJSON(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	pre := `[{"file":"a.go","severity":"error","code":"X","message":"sample 'arg'"}]`
	text, _, _ := h.handleLearnFromObservation(map[string]any{"pre_diagnostics": pre})
	var candidates []LearnedFixCandidate
	_ = json.Unmarshal([]byte(text), &candidates)
	if len(candidates) != 1 {
		t.Fatal("expected 1 candidate")
	}
	var actionObj map[string]any
	if err := json.Unmarshal([]byte(candidates[0].CandidateAction), &actionObj); err != nil {
		t.Errorf("candidate action is not valid JSON: %v", err)
	}
}

// TestLearnFromObservationRequiresPre verifies the required param check.
func TestLearnFromObservationRequiresPre(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	_, isErr, _ := h.handleLearnFromObservation(map[string]any{})
	if !isErr {
		t.Error("expected error when pre_diagnostics is missing")
	}
}

// TestLearnFromObservationRejectsBadJSON verifies the JSON parse error path.
func TestLearnFromObservationRejectsBadJSON(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	text, isErr, _ := h.handleLearnFromObservation(map[string]any{
		"pre_diagnostics": "not-json",
	})
	if !isErr {
		t.Errorf("expected error for malformed pre_diagnostics; got: %s", text)
	}
}
