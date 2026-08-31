// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

func TestTeachFixRoundTrip(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	text, isErr, err := h.handleTeachFix(map[string]any{
		"name":             "remove-unused-import",
		"diagnostic_regex": `imported and not used: "(?P<pkg>[^"]+)"`,
		"action":           `{"recipe":"remove-import","params":{"name":"${pkg}"}}`,
		"confidence":       "auto",
		"description":      "Drop an unused Go import that gopls flagged",
	})
	if err != nil || isErr {
		t.Fatalf("teach_fix: err=%v isErr=%v text=%s", err, isErr, text)
	}
	if !strings.Contains(text, "remove-unused-import") || !strings.Contains(text, "auto") {
		t.Errorf("expected name+confidence in confirmation, got: %s", text)
	}

	listText, _, _ := h.handleListFixes(nil)
	for _, want := range []string{"remove-unused-import", "[auto]", "(?P<pkg>", "recipe:remove-import", "params: name"} {
		if !strings.Contains(listText, want) {
			t.Errorf("list missing %q in:\n%s", want, listText)
		}
	}

	delText, _, _ := h.handleDeleteFix(map[string]any{"name": "remove-unused-import"})
	if !strings.Contains(delText, "deleted") {
		t.Errorf("expected deletion confirmation, got: %s", delText)
	}

	listText, _, _ = h.handleListFixes(nil)
	if !strings.Contains(strings.ToLower(listText), "no fixes saved") {
		t.Errorf("expected empty list, got: %s", listText)
	}
}

func TestTeachFixDefaultsConfidenceToSuggest(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	text, isErr, _ := h.handleTeachFix(map[string]any{
		"name":             "no-conf",
		"diagnostic_regex": `something`,
		"action":           `{"recipe":"r"}`,
	})
	if isErr {
		t.Fatalf("teach errored: %s", text)
	}
	if !strings.Contains(text, "suggest") {
		t.Errorf("expected default confidence=suggest in confirmation: %s", text)
	}
}

func TestTeachFixUpsert(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	for _, conf := range []string{"suggest", "auto"} {
		if _, isErr, _ := h.handleTeachFix(map[string]any{
			"name":             "upsert-me",
			"diagnostic_regex": `boom`,
			"action":           `{"recipe":"r"}`,
			"confidence":       conf,
		}); isErr {
			t.Fatalf("save with confidence=%s errored", conf)
		}
	}
	listText, _, _ := h.handleListFixes(nil)
	if strings.Count(listText, "upsert-me") != 1 {
		t.Errorf("expected exactly one entry, got:\n%s", listText)
	}
	if !strings.Contains(listText, "[auto]") {
		t.Errorf("expected updated confidence to be auto, got:\n%s", listText)
	}
}

func TestTeachFixValidation(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "missing name",
			args: map[string]any{
				"diagnostic_regex": `x`,
				"action":           `{"recipe":"r"}`,
			},
			want: "name",
		},
		{
			name: "missing regex",
			args: map[string]any{
				"name":   "x",
				"action": `{"recipe":"r"}`,
			},
			want: "diagnostic_regex",
		},
		{
			name: "missing action",
			args: map[string]any{
				"name":             "x",
				"diagnostic_regex": `y`,
			},
			want: "action",
		},
		{
			name: "invalid regex",
			args: map[string]any{
				"name":             "bad-regex",
				"diagnostic_regex": `(unclosed`,
				"action":           `{"recipe":"r"}`,
			},
			want: "compiling diagnostic_regex",
		},
		{
			name: "invalid json",
			args: map[string]any{
				"name":             "bad-json",
				"diagnostic_regex": `x`,
				"action":           `not json`,
			},
			want: "not valid JSON",
		},
		{
			name: "missing recipe and transform",
			args: map[string]any{
				"name":             "no-action-key",
				"diagnostic_regex": `x`,
				"action":           `{"foo":"bar"}`,
			},
			want: "recipe",
		},
		{
			name: "both recipe and transform",
			args: map[string]any{
				"name":             "both-keys",
				"diagnostic_regex": `x`,
				"action":           `{"recipe":"r","transform":{}}`,
			},
			want: "exactly one",
		},
		{
			name: "invalid confidence",
			args: map[string]any{
				"name":             "bad-conf",
				"diagnostic_regex": `x`,
				"action":           `{"recipe":"r"}`,
				"confidence":       "maybe",
			},
			want: "invalid confidence",
		},
		{
			name: "capture reference missing",
			args: map[string]any{
				"name":             "bad-cap",
				"diagnostic_regex": `error: (?P<msg>.+)`,
				"action":           `{"recipe":"r","params":{"x":"${pkg}"}}`,
			},
			want: "unknown captures",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, isErr, err := h.handleTeachFix(c.args)
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if !isErr {
				t.Errorf("expected isErr=true, got text=%s", text)
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("expected error containing %q, got: %s", c.want, text)
			}
		})
	}
}

func TestTeachFixCaptureValidationAccepts(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})

	// Multiple captures, all referenced — should pass.
	if _, isErr, _ := h.handleTeachFix(map[string]any{
		"name":             "multi-capture",
		"diagnostic_regex": `cannot find (?P<kind>type|name) "(?P<name>[^"]+)"`,
		"action":           `{"recipe":"add-${kind}","params":{"identifier":"${name}"}}`,
	}); isErr {
		t.Error("expected multi-capture fix to be accepted")
	}
	// No references at all — should pass (regex captures don't all need to be used).
	if _, isErr, _ := h.handleTeachFix(map[string]any{
		"name":             "no-refs",
		"diagnostic_regex": `(?P<unused>foo)`,
		"action":           `{"recipe":"static"}`,
	}); isErr {
		t.Error("expected fix with unreferenced captures to be accepted")
	}
}

func TestDeleteFixMissing(t *testing.T) {
	h := testHandler(t, map[string]string{"main.go": "package main\n"})
	text, isErr, _ := h.handleDeleteFix(map[string]any{"name": "ghost"})
	if isErr {
		t.Errorf("delete-missing should not be a tool error, got: %s", text)
	}
	if !strings.Contains(text, "No fix named") {
		t.Errorf("expected friendly missing message, got: %s", text)
	}
}

func TestFixPersistsAcrossHandlers(t *testing.T) {
	h1 := testHandler(t, map[string]string{"main.go": "package main\n"})
	root := h1.model.Root

	if _, isErr, _ := h1.handleTeachFix(map[string]any{
		"name":             "persist-me",
		"diagnostic_regex": `x`,
		"action":           `{"recipe":"r"}`,
	}); isErr {
		t.Fatal("save failed")
	}
	h1.Close()

	h2 := NewHandler()
	defer h2.Close()
	if text, isErr, _ := h2.handleParse(map[string]any{"path": root}); isErr {
		t.Fatalf("re-parse: %s", text)
	}

	listText, _, _ := h2.handleListFixes(nil)
	if !strings.Contains(listText, "persist-me") {
		t.Errorf("expected persisted fix to survive handler restart; got:\n%s", listText)
	}
}
