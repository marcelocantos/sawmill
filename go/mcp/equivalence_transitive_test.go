// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

// TestTransitiveDerivedPairsListed verifies that list_equivalences shows
// derived pairs alongside taught ones when patterns transitively connect.
func TestTransitiveDerivedPairsListed(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})

	teach(t, h, "ab", "patternA", "patternB", "")
	teach(t, h, "bc", "patternB", "patternC", "")

	text, _, _ := h.handleListEquivalences(nil)
	if !strings.Contains(text, "[taught]") {
		t.Errorf("expected taught section header, got: %s", text)
	}
	if !strings.Contains(text, "[derived via transitive closure]") {
		t.Errorf("expected derived section header, got: %s", text)
	}
	// patternA ↔ patternC should appear as derived (sorted alphabetically).
	if !strings.Contains(text, "patternA ↔ patternC") {
		t.Errorf("expected derived pair patternA ↔ patternC, got: %s", text)
	}
}

// TestTransitiveDerivedNotShownForSinglePair verifies that a single pair
// (class size 2) doesn't generate spurious derived pairs.
func TestTransitiveDerivedNotShownForSinglePair(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})

	teach(t, h, "ab", "alpha", "beta", "")

	text, _, _ := h.handleListEquivalences(nil)
	if strings.Contains(text, "derived") {
		t.Errorf("single pair should not produce derived entries, got: %s", text)
	}
}

// TestTransitiveCheckHonoursDerivedPairs verifies that check_equivalences
// flags violations of derived equivalences (not only taught ones).
//
// Setup: teach foo↔bar (prefers foo) and bar↔baz (no preference). The class
// {foo, bar, baz} has preferred=foo (unanimous from one vote). Source uses
// `baz(...)` — should be flagged with suggested rewrite to `foo(...)`,
// even though foo↔baz was never directly taught.
func TestTransitiveCheckHonoursDerivedPairs(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def m():\n    baz(1, 2)\n",
	})

	// Taught: foo↔bar [prefers foo], bar↔baz [no pref]
	teach(t, h, "ab", "foo($a, $b)", "bar($a, $b)", "left")
	teach(t, h, "bc", "bar($a, $b)", "baz($a, $b)", "")

	text, isErr, err := h.handleCheckEquivalences(map[string]any{})
	if err != nil || isErr {
		t.Fatalf("check: err=%v isErr=%v text=%s", err, isErr, text)
	}
	// Violation should be reported for baz (non-preferred derived pattern)
	// with suggested rewrite to foo (the class's preferred pattern).
	for _, want := range []string{"baz(1, 2)", "foo(1, 2)", "derived"} {
		if !strings.Contains(text, want) {
			t.Errorf("check output missing %q, got:\n%s", want, text)
		}
	}
}

// TestTransitiveCheckIgnoresConflictingPreferences verifies that when
// preferences in a class conflict, the class has no preferred pattern and
// nothing is flagged.
func TestTransitiveCheckIgnoresConflictingPreferences(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def m():\n    foo(1, 2)\n    baz(3, 4)\n",
	})

	// Conflict: ab prefers foo, bc prefers baz. Class {foo, bar, baz}: no consensus.
	teach(t, h, "ab", "foo($a, $b)", "bar($a, $b)", "left")
	teach(t, h, "bc", "bar($a, $b)", "baz($a, $b)", "right")

	text, _, _ := h.handleCheckEquivalences(map[string]any{})
	if !strings.Contains(strings.ToLower(text), "no equivalences with a preferred") {
		t.Errorf("expected no-preference message for conflicting class, got: %s", text)
	}
}

// TestTransitiveApplyExpandsToClass verifies that apply_equivalence rewrites
// not only the named pair's source but also every other pattern in the class
// (i.e. derived sources are also rewritten to the chosen target).
//
// Setup: teach foo↔bar and bar↔baz (no preferences). The class is
// {foo, bar, baz}. Calling apply_equivalence(name=ab, direction=left_to_right)
// rewrites foo → bar (the named direction) AND baz → bar (derived source
// also folds to the chosen target bar).
func TestTransitiveApplyExpandsToClass(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def m():\n    foo(1, 2)\n    baz(3, 4)\n    other(5)\n",
	})

	teach(t, h, "ab", "foo($a, $b)", "bar($a, $b)", "")
	teach(t, h, "bc", "bar($a, $b)", "baz($a, $b)", "")

	text, isErr, err := h.handleApplyEquivalence(map[string]any{
		"name":      "ab",
		"direction": "left_to_right",
	})
	if err != nil || isErr {
		t.Fatalf("apply: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "derived source pattern") {
		t.Errorf("expected derived-source note in summary, got: %s", text)
	}

	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply confirm errored")
	}
	got := readFile(t, h, "main.py")
	for _, want := range []string{"bar(1, 2)", "bar(3, 4)", "other(5)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in result, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "foo(1, 2)") || strings.Contains(got, "baz(3, 4)") {
		t.Errorf("foo/baz should be rewritten, got:\n%s", got)
	}
}

// TestTransitiveCycleDetection verifies that a cycle (A↔B + B↔C + C↔A) is
// stored without producing duplicate entries — the third edge closes the
// loop but adds no new derivations.
func TestTransitiveCycleDetection(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "x = 1\n"})

	teach(t, h, "ab", "alpha", "beta", "")
	teach(t, h, "bc", "beta", "gamma", "")
	teach(t, h, "ca", "gamma", "alpha", "") // closes the cycle

	text, _, _ := h.handleListEquivalences(nil)
	// All three taught pairs should appear in the taught section.
	for _, want := range []string{"alpha ↔ beta", "beta ↔ gamma", "gamma"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in taught section, got:\n%s", want, text)
		}
	}
	// Derived section should NOT include any duplicates of taught pairs.
	// Class is {alpha, beta, gamma}: 3 possible pairs, all taught → 0 derived.
	if strings.Contains(text, "[derived via transitive closure]") {
		t.Errorf("cycle should produce no derived pairs, got:\n%s", text)
	}
}
