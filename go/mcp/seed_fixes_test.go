// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/store"
)

func TestSeedFixesInstallsAllEntries(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	text, isErr, err := h.handleSeedFixes(nil)
	if err != nil || isErr {
		t.Fatalf("seed_fixes: err=%v isErr=%v text=%s", err, isErr, text)
	}
	// Every seed entry should land.
	for _, sf := range seedFixes {
		if !strings.Contains(text, sf.Name) {
			t.Errorf("seed output missing %q:\n%s", sf.Name, text)
		}
	}

	// Now list_fixes should return them all.
	listText, _, _ := h.handleListFixes(nil)
	for _, sf := range seedFixes {
		if !strings.Contains(listText, sf.Name) {
			t.Errorf("list missing seeded entry %q:\n%s", sf.Name, listText)
		}
	}
}

// TestSeedFixesIsIdempotent verifies that running seed_fixes twice doesn't
// duplicate entries — and skips them on the second run.
func TestSeedFixesIsIdempotent(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	// First run.
	if _, isErr, _ := h.handleSeedFixes(nil); isErr {
		t.Fatal("first seed errored")
	}

	// Second run — should report kept-existing for every entry.
	text, isErr, _ := h.handleSeedFixes(nil)
	if isErr {
		t.Fatalf("second seed errored: %s", text)
	}
	if !strings.Contains(text, "Seeded 0 fix(es)") {
		t.Errorf("second run should install zero new entries; got:\n%s", text)
	}

	// And list count matches the seed catalogue size, not double.
	listText, _, _ := h.handleListFixes(nil)
	for _, sf := range seedFixes {
		if strings.Count(listText, sf.Name) != 1 {
			t.Errorf("entry %q appears %d times after double-seed:\n%s",
				sf.Name, strings.Count(listText, sf.Name), listText)
		}
	}
}

// TestSeedFixesPreservesUserCustomisations verifies that an entry the user
// already customised isn't overwritten when seed_fixes is re-run.
func TestSeedFixesPreservesUserCustomisations(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	// User pre-empts a seed name with their own definition.
	if _, isErr, _ := h.handleTeachFix(map[string]any{
		"name":             "go-unused-import",
		"diagnostic_regex": `my custom regex`,
		"action":           `{"recipe":"my-custom-recipe"}`,
		"confidence":       "suggest",
	}); isErr {
		t.Fatal("user pre-seed failed")
	}

	if _, isErr, _ := h.handleSeedFixes(nil); isErr {
		t.Fatal("seed errored")
	}

	// User's version should still be there, not the seed version.
	listText, _, _ := h.handleListFixes(nil)
	if !strings.Contains(listText, "my custom regex") {
		t.Errorf("user customisation lost; got:\n%s", listText)
	}
	if strings.Contains(listText, "imported and not used") {
		t.Errorf("seed version overwrote user customisation; got:\n%s", listText)
	}
}

// TestSeedFixesConfidenceMix verifies the seed catalogue contains both
// auto and suggest confidences (per the acceptance criteria).
func TestSeedFixesConfidenceMix(t *testing.T) {
	auto, suggest := 0, 0
	for _, sf := range seedFixes {
		switch sf.Confidence {
		case store.FixConfidenceAuto:
			auto++
		case store.FixConfidenceSuggest:
			suggest++
		default:
			t.Errorf("seed %q has unexpected confidence %q", sf.Name, sf.Confidence)
		}
	}
	if auto == 0 {
		t.Error("expected at least one auto-confidence seed entry")
	}
	if suggest == 0 {
		t.Error("expected at least one suggest-confidence seed entry")
	}
}

// TestSeedFixesCoversBothLanguages verifies the catalogue spans both Go and
// TypeScript (per the acceptance criteria).
func TestSeedFixesCoversBothLanguages(t *testing.T) {
	hasGo, hasTS := false, false
	for _, sf := range seedFixes {
		if strings.HasPrefix(sf.Name, "go-") {
			hasGo = true
		}
		if strings.HasPrefix(sf.Name, "ts-") {
			hasTS = true
		}
	}
	if !hasGo {
		t.Error("expected at least one go-* seed entry")
	}
	if !hasTS {
		t.Error("expected at least one ts-* seed entry")
	}
}

// TestSeedFixesAllValid verifies every seed entry passes the same validation
// the user-facing teach_fix runs.
func TestSeedFixesAllValid(t *testing.T) {
	for _, sf := range seedFixes {
		if err := validateFixCaptures(sf.DiagnosticRegex, sf.ActionJSON); err != nil {
			t.Errorf("seed %q fails capture validation: %v", sf.Name, err)
		}
	}
}
