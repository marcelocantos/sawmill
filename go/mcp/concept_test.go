// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/store"
)

func TestTeachConceptRoundTrip(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\nfunc main() {}\n"})

	text, isErr, err := h.handleTeachConcept(map[string]any{
		"name":        "deploy",
		"description": "deployment plumbing",
		"aliases":     `["deploy", "rollout", "kustomize", "helm"]`,
	})
	if err != nil || isErr {
		t.Fatalf("teach_concept: err=%v isErr=%v text=%s", err, isErr, text)
	}

	listText, _, _ := h.handleListConcepts(nil)
	for _, want := range []string{"deploy", "deployment plumbing", "rollout", "helm"} {
		if !strings.Contains(listText, want) {
			t.Errorf("list output missing %q:\n%s", want, listText)
		}
	}

	// Built-ins should also surface in the list.
	for _, want := range []string{"swipe", "retry", "auth", "logging"} {
		if !strings.Contains(listText, want) {
			t.Errorf("list missing built-in %q:\n%s", want, listText)
		}
	}

	delText, _, _ := h.handleDeleteConcept(map[string]any{"name": "deploy"})
	if !strings.Contains(delText, "deleted") {
		t.Errorf("expected delete confirmation, got: %s", delText)
	}
}

func TestTeachConceptRejectsEmpty(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	_, isErr, _ := h.handleTeachConcept(map[string]any{
		"name":    "empty",
		"aliases": `[]`,
	})
	if !isErr {
		t.Error("expected error for empty aliases")
	}

	_, isErr, _ = h.handleTeachConcept(map[string]any{"name": "noalias"})
	if !isErr {
		t.Error("expected error when aliases missing")
	}
}

func TestDeleteBuiltinIsRefused(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	text, _, _ := h.handleDeleteConcept(map[string]any{"name": "swipe"})
	if !strings.Contains(text, "built-in") {
		t.Errorf("expected built-in refusal message, got: %s", text)
	}
}

func TestFindByConceptEndToEnd(t *testing.T) {
	h := testHandler(t, map[string]string{
		"swipe_handler.go": `package main

// OnSwipe handles a swipe gesture from the user.
func OnSwipe(direction string) {
	if direction == "left" {
		dismissCard()
	}
}

func dismissCard() {}
`,
		"unrelated.go": `package main

func ComputeTax(amount int) int {
	return amount / 10
}
`,
	})

	text, isErr, err := h.handleFindByConcept(map[string]any{
		"query": "swipe",
	})
	if err != nil || isErr {
		t.Fatalf("find_by_concept: err=%v isErr=%v text=%s", err, isErr, text)
	}

	if !strings.Contains(text, "OnSwipe") {
		t.Errorf("expected OnSwipe in result, got:\n%s", text)
	}
	if strings.Contains(text, "ComputeTax") {
		t.Errorf("ComputeTax should not match a swipe query:\n%s", text)
	}
	// Built-in expansion should have surfaced aliases like 'gesture'.
	if !strings.Contains(text, "gesture") {
		t.Errorf("expected 'gesture' in expanded alias list:\n%s", text)
	}
}

func TestFindByConceptJSON(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func attemptRetryWithBackoff() {}
`,
	})

	text, _, err := h.handleFindByConcept(map[string]any{
		"query":  "retry",
		"format": "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "\"matches\"") {
		t.Errorf("expected JSON matches key:\n%s", text)
	}
	if !strings.Contains(text, "attemptRetryWithBackoff") {
		t.Errorf("expected match name in JSON:\n%s", text)
	}
}

func TestFindByConceptUnrecognisedQueryFallsThrough(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func computeTaxRate() {}
`,
	})

	// "tax" isn't a built-in concept and hasn't been taught, but it should
	// still match the symbol whose name contains it.
	text, _, err := h.handleFindByConcept(map[string]any{
		"query": "tax",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "computeTaxRate") {
		t.Errorf("expected unrecognised-word query to anchor on itself:\n%s", text)
	}
}

func TestExpandQueryUsesStoredOverBuiltin(t *testing.T) {
	stored := []store.Concept{
		{Name: "swipe", Aliases: []string{"customswipealias"}},
	}
	got := expandQuery("swipe", stored)
	hasCustom := false
	hasBuiltinAlias := false
	for _, a := range got {
		if a == "customswipealias" {
			hasCustom = true
		}
		if a == "uipangesturerecognizer" {
			hasBuiltinAlias = true
		}
	}
	if !hasCustom {
		t.Errorf("expected custom alias in expansion: %v", got)
	}
	if hasBuiltinAlias {
		t.Errorf("stored concept should shadow built-in, but got built-in alias: %v", got)
	}
}

func TestExpandQueryFallsThroughUnknown(t *testing.T) {
	got := expandQuery("flammbledigook", nil)
	if len(got) != 1 || got[0] != "flammbledigook" {
		t.Errorf("unknown word should become its own alias, got: %v", got)
	}
}
