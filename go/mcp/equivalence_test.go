// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

func TestTeachEquivalenceRoundTrip(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	// Teach an equivalence with a preferred direction.
	text, isErr, err := h.handleTeachEquivalence(map[string]any{
		"name":                "errors-is",
		"left_pattern":        "errors.Is($err, $target)",
		"right_pattern":       "$err == $target",
		"description":         "Prefer errors.Is over direct equality for error sentinels",
		"preferred_direction": "left",
	})
	if err != nil || isErr {
		t.Fatalf("teach_equivalence: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "errors-is") {
		t.Errorf("expected confirmation mentioning name, got: %s", text)
	}

	// List should now include the saved pair.
	listText, isErr, err := h.handleListEquivalences(nil)
	if err != nil || isErr {
		t.Fatalf("list_equivalences: err=%v isErr=%v text=%s", err, isErr, listText)
	}
	for _, want := range []string{"errors-is", "errors.Is($err, $target)", "$err == $target", "prefers left"} {
		if !strings.Contains(listText, want) {
			t.Errorf("list output missing %q:\n%s", want, listText)
		}
	}

	// Delete should remove it.
	delText, isErr, err := h.handleDeleteEquivalence(map[string]any{"name": "errors-is"})
	if err != nil || isErr {
		t.Fatalf("delete_equivalence: err=%v isErr=%v text=%s", err, isErr, delText)
	}
	if !strings.Contains(delText, "deleted") {
		t.Errorf("expected delete confirmation, got: %s", delText)
	}

	// List should now be empty.
	listText, _, _ = h.handleListEquivalences(nil)
	if !strings.Contains(strings.ToLower(listText), "no equivalences") {
		t.Errorf("expected empty list message, got: %s", listText)
	}
}

func TestTeachEquivalenceUpsert(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	// First save with description "v1".
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"name":          "swap",
		"left_pattern":  "a == b",
		"right_pattern": "b == a",
		"description":   "v1",
	}); isErr {
		t.Fatal("first save errored")
	}

	// Re-save with description "v2" — should update in place.
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"name":          "swap",
		"left_pattern":  "a == b",
		"right_pattern": "b == a",
		"description":   "v2",
	}); isErr {
		t.Fatal("upsert errored")
	}

	listText, _, _ := h.handleListEquivalences(nil)
	if !strings.Contains(listText, "v2") {
		t.Errorf("expected upsert to overwrite description; got:\n%s", listText)
	}
	if strings.Contains(listText, "v1") {
		t.Errorf("expected v1 to be replaced; got:\n%s", listText)
	}
	// And only one entry, not two.
	if strings.Count(listText, "swap") != 1 {
		t.Errorf("expected exactly one swap entry; got:\n%s", listText)
	}
}

func TestTeachEquivalenceValidation(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	// Missing name.
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"left_pattern":  "a",
		"right_pattern": "b",
	}); !isErr {
		t.Error("expected error for missing name")
	}

	// Missing left_pattern.
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"name":          "x",
		"right_pattern": "b",
	}); !isErr {
		t.Error("expected error for missing left_pattern")
	}

	// Identical patterns.
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"name":          "noop",
		"left_pattern":  "a",
		"right_pattern": "a",
	}); !isErr {
		t.Error("expected error for identical patterns")
	}

	// Invalid preferred_direction.
	if _, isErr, _ := h.handleTeachEquivalence(map[string]any{
		"name":                "bad-dir",
		"left_pattern":        "a",
		"right_pattern":       "b",
		"preferred_direction": "sideways",
	}); !isErr {
		t.Error("expected error for bogus preferred_direction")
	}
}

func TestDeleteEquivalenceMissing(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	text, isErr, err := h.handleDeleteEquivalence(map[string]any{"name": "never-existed"})
	if err != nil || isErr {
		t.Fatalf("delete should not error for missing name: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "No equivalence named") {
		t.Errorf("expected friendly missing message, got: %s", text)
	}
}

func TestEquivalencePersistsAcrossHandlers(t *testing.T) {
	// Two handlers sharing the same store directory should see the same
	// equivalences (the persistence story).
	h1 := testHandler(t, map[string]string{"main.go": "package main\n"})
	root := h1.model.Root

	if _, isErr, _ := h1.handleTeachEquivalence(map[string]any{
		"name":          "shared",
		"left_pattern":  "x",
		"right_pattern": "y",
	}); isErr {
		t.Fatal("save errored")
	}
	h1.Close()

	// Open a fresh handler at the same root.
	h2 := NewHandler()
	if text, isErr, err := h2.handleParse(map[string]any{"path": root}); err != nil || isErr {
		t.Fatalf("re-parse: err=%v isErr=%v text=%s", err, isErr, text)
	}
	defer h2.Close()

	listText, _, _ := h2.handleListEquivalences(nil)
	if !strings.Contains(listText, "shared") {
		t.Errorf("expected persisted equivalence to survive handler restart; got:\n%s", listText)
	}
}
