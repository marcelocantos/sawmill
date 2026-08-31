// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
)

func TestLanguagesList(t *testing.T) {
	h := &Handler{}
	text, isErr, err := h.handleLanguages(map[string]any{})
	if err != nil || isErr {
		t.Fatalf("list: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "lua") || !strings.Contains(text, "bash") {
		t.Fatalf("list missing expected languages:\n%s", text)
	}
	if !strings.Contains(text, "languages(language=") {
		t.Fatalf("list should tell agents how to get detail:\n%s", text)
	}
}

func TestLanguagesDetailBash(t *testing.T) {
	h := &Handler{}
	text, isErr, err := h.handleLanguages(map[string]any{"language": "bash"})
	if err != nil || isErr {
		t.Fatalf("detail: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "add_field:") || !strings.Contains(text, "false") {
		t.Fatalf("bash detail should show add_field false:\n%s", text)
	}
	if !strings.Contains(text, "notes:") {
		t.Fatalf("bash detail should include notes:\n%s", text)
	}
	if !strings.Contains(strings.ToLower(text), "best-effort") {
		t.Fatalf("bash notes should mention best-effort rename:\n%s", text)
	}
}

func TestLanguagesJSON(t *testing.T) {
	h := &Handler{}
	text, isErr, err := h.handleLanguages(map[string]any{
		"language": "proto",
		"format":   "json",
	})
	if err != nil || isErr {
		t.Fatalf("json: err=%v isErr=%v text=%s", err, isErr, text)
	}
	var info adapters.LanguageInfo
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
	}
	if info.ID != "protobuf" {
		t.Fatalf("id = %q", info.ID)
	}
	if len(info.Notes) == 0 {
		t.Fatal("expected notes on protobuf")
	}
}

func TestLanguagesUnknown(t *testing.T) {
	h := &Handler{}
	text, isErr, err := h.handleLanguages(map[string]any{"language": "brainfuck"})
	if err != nil {
		t.Fatal(err)
	}
	if !isErr {
		t.Fatalf("expected tool error, got: %s", text)
	}
}

func TestLanguagesToolRegistered(t *testing.T) {
	found := false
	for _, def := range Definitions() {
		if def.Name == "languages" {
			found = true
			if !strings.Contains(def.Description, "caveat") && !strings.Contains(def.Description, "capability") {
				t.Errorf("languages tool description should mention capabilities/caveats: %s", def.Description)
			}
			break
		}
	}
	if !found {
		t.Fatal("languages tool not registered in Definitions()")
	}
}
