// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// teach is a small helper that saves an equivalence on the given handler.
func teach(t *testing.T, h *Handler, name, left, right, dir string) {
	t.Helper()
	args := map[string]any{
		"name":          name,
		"left_pattern":  left,
		"right_pattern": right,
	}
	if dir != "" {
		args["preferred_direction"] = dir
	}
	if _, isErr, err := h.handleTeachEquivalence(args); err != nil || isErr {
		t.Fatalf("teach %s: err=%v isErr=%v", name, err, isErr)
	}
}

// readFile reads a file under the handler's project root.
func readFile(t *testing.T, h *Handler, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.model.Root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

func TestApplyEquivalenceLeftToRight(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def main():\n    foo(1, 2)\n    foo(3, 4)\n",
	})

	teach(t, h, "swap-args", "foo($a, $b)", "foo($b, $a)", "")

	text, isErr, err := h.handleApplyEquivalence(map[string]any{
		"name":      "swap-args",
		"direction": "left_to_right",
	})
	if err != nil || isErr {
		t.Fatalf("apply: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "2 match") {
		t.Errorf("expected 2 matches in summary, got: %s", text)
	}

	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply confirm errored")
	}

	got := readFile(t, h, "main.py")
	wantSubstrings := []string{"foo(2, 1)", "foo(4, 3)"}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in result, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "foo(1, 2)") {
		t.Errorf("expected foo(1, 2) to be rewritten, got:\n%s", got)
	}
}

func TestApplyEquivalenceRoundTrip(t *testing.T) {
	original := "def main():\n    foo(1, 2)\n    foo(3, 4)\n"
	h := testHandler(t, map[string]string{"main.py": original})

	teach(t, h, "swap-args", "foo($a, $b)", "foo($b, $a)", "")

	// left_to_right then apply
	if _, isErr, _ := h.handleApplyEquivalence(map[string]any{
		"name": "swap-args", "direction": "left_to_right",
	}); isErr {
		t.Fatal("first apply staged tool errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("first apply confirm errored")
	}

	// re-parse so the model sees the updated source
	if _, isErr, _ := h.handleParse(map[string]any{"path": h.model.Root}); isErr {
		t.Fatal("re-parse errored")
	}

	// right_to_left then apply
	if _, isErr, _ := h.handleApplyEquivalence(map[string]any{
		"name": "swap-args", "direction": "right_to_left",
	}); isErr {
		t.Fatal("reverse apply staged tool errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("reverse apply confirm errored")
	}

	got := readFile(t, h, "main.py")
	if got != original {
		t.Errorf("round-trip not byte-identical:\nwant:\n%s\ngot:\n%s", original, got)
	}
}

func TestApplyEquivalenceNoMatches(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def main():\n    pass\n"})
	teach(t, h, "noop", "foo($x)", "bar($x)", "")

	text, isErr, err := h.handleApplyEquivalence(map[string]any{
		"name": "noop", "direction": "left_to_right",
	})
	if err != nil || isErr {
		t.Fatalf("apply: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "no matches") {
		t.Errorf("expected no-match message, got: %s", text)
	}
}

func TestApplyEquivalenceUnknownName(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def main():\n    pass\n"})
	_, isErr, _ := h.handleApplyEquivalence(map[string]any{
		"name": "missing", "direction": "left_to_right",
	})
	if !isErr {
		t.Error("expected error for unknown equivalence name")
	}
}

func TestApplyEquivalenceBadDirection(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def main():\n    pass\n"})
	teach(t, h, "x", "a", "b", "")
	_, isErr, _ := h.handleApplyEquivalence(map[string]any{
		"name": "x", "direction": "sideways",
	})
	if !isErr {
		t.Error("expected error for invalid direction")
	}
}

func TestCheckEquivalencesReportsViolations(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def main():\n    foo(1, 2)\n    bar(3)\n    foo(7, 8)\n",
	})

	// Prefer left → right-side matches are violations.
	teach(t, h, "swap-args", "foo($a, $b)", "foo($b, $a)", "right")

	text, isErr, err := h.handleCheckEquivalences(map[string]any{})
	if err != nil || isErr {
		t.Fatalf("check: err=%v isErr=%v text=%s", err, isErr, text)
	}
	for _, want := range []string{"swap-args", "foo(1, 2)", "foo(2, 1)", "foo(7, 8)", "foo(8, 7)", "main.py"} {
		if !strings.Contains(text, want) {
			t.Errorf("check output missing %q:\n%s", want, text)
		}
	}
}

func TestCheckEquivalencesNoPreferredDirection(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def main():\n    foo(1, 2)\n"})
	teach(t, h, "no-pref", "foo($a, $b)", "foo($b, $a)", "")

	text, _, _ := h.handleCheckEquivalences(map[string]any{})
	if !strings.Contains(strings.ToLower(text), "no equivalences with a preferred") {
		t.Errorf("expected skip message, got: %s", text)
	}
}

func TestCheckEquivalencesSatisfied(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def main():\n    pass\n",
	})
	teach(t, h, "swap-args", "foo($a, $b)", "foo($b, $a)", "left")

	text, _, _ := h.handleCheckEquivalences(map[string]any{})
	if !strings.Contains(strings.ToLower(text), "satisfied") {
		t.Errorf("expected satisfied message, got: %s", text)
	}
}

func TestCheckEquivalencesPathFilter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py":  "def m():\n    foo(1, 2)\n",
		"other.py": "def o():\n    foo(3, 4)\n",
	})
	teach(t, h, "swap", "foo($a, $b)", "foo($b, $a)", "left")

	text, _, _ := h.handleCheckEquivalences(map[string]any{"path": "other.py"})
	if !strings.Contains(text, "other.py") {
		t.Errorf("expected other.py violations, got: %s", text)
	}
	if strings.Contains(text, "main.py") {
		t.Errorf("path filter should exclude main.py:\n%s", text)
	}
}
