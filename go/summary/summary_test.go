// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package summary

import (
	"testing"
)

func TestExtractJSONObjectPlain(t *testing.T) {
	in := `{"summary":"hi","edges":[]}`
	got, ok := extractJSONObject(in)
	if !ok || got != in {
		t.Errorf("got %q ok=%v, want %q", got, ok, in)
	}
}

func TestExtractJSONObjectWithFence(t *testing.T) {
	in := "Here you go:\n```json\n{\"summary\":\"hi\",\"edges\":[]}\n```\n"
	got, ok := extractJSONObject(in)
	if !ok {
		t.Fatal("expected ok")
	}
	if got != `{"summary":"hi","edges":[]}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractJSONObjectIgnoresBracesInStrings(t *testing.T) {
	in := `{"summary":"this { is } a string with braces","edges":[]}`
	got, ok := extractJSONObject(in)
	if !ok {
		t.Fatal("expected ok")
	}
	if got != in {
		t.Errorf("got %q", got)
	}
}

func TestExtractJSONObjectNested(t *testing.T) {
	in := `{"summary":"hi","edges":[{"kind":"calls","dst":"foo","confidence":0.9}]}`
	got, ok := extractJSONObject(in)
	if !ok || got != in {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestExtractJSONObjectFailsOnNonJSON(t *testing.T) {
	if _, ok := extractJSONObject("nothing to see here"); ok {
		t.Error("expected ok=false on input with no JSON")
	}
}

func TestPromptVersion(t *testing.T) {
	if PromptID == "" {
		t.Error("PromptID must not be empty")
	}
}
