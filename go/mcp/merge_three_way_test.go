// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeThreeWay_InlineClean(t *testing.T) {
	h := NewHandler()
	args := map[string]any{
		"language": "py",
		"path":     "calc.py",
		"base_content": `class Calc:
    def add(self, a, b):
        return a + b
`,
		"ours_content": `class Calc:
    def add(self, a, b):
        return a + b

    def sub(self, a, b):
        return a - b
`,
		"theirs_content": `class Calc:
    def add(self, a, b):
        return a + b

    def mul(self, a, b):
        return a * b
`,
	}
	out, isErr, err := h.handleMergeThreeWay(args)
	if err != nil || isErr {
		t.Fatalf("merge_three_way err=%v isErr=%v out=%s", err, isErr, out)
	}
	var resp mergeThreeWayResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, out)
	}
	if !resp.Clean {
		t.Fatalf("expected clean merge; conflicts=%+v\nmerged=%s", resp.Conflicts, resp.Merged)
	}
	for _, want := range []string{"def add", "def sub", "def mul"} {
		if !strings.Contains(resp.Merged, want) {
			t.Fatalf("merged output missing %q:\n%s", want, resp.Merged)
		}
	}
}

func TestMergeThreeWay_InlineConflict(t *testing.T) {
	h := NewHandler()
	args := map[string]any{
		"language": "py",
		"path":     "calc.py",
		"base_content": `def doomed():
    return 2
`,
		"ours_content": ``,
		"theirs_content": `def doomed():
    return 999
`,
	}
	out, isErr, err := h.handleMergeThreeWay(args)
	if err != nil || isErr {
		t.Fatalf("merge_three_way err=%v isErr=%v out=%s", err, isErr, out)
	}
	var resp mergeThreeWayResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, out)
	}
	if resp.Clean {
		t.Fatalf("expected residual conflicts; got clean merge:\n%s", resp.Merged)
	}
	if !strings.Contains(resp.Merged, "<<<<<<<") || !strings.Contains(resp.Merged, ">>>>>>>") {
		t.Fatalf("expected conflict markers in merged output:\n%s", resp.Merged)
	}
}

func TestMergeThreeWay_MissingSide(t *testing.T) {
	h := NewHandler()
	args := map[string]any{
		"language":     "py",
		"base_content": "x = 1\n",
		"ours_content": "x = 2\n",
		// theirs missing
	}
	out, isErr, _ := h.handleMergeThreeWay(args)
	if !isErr || !strings.Contains(out, "theirs") {
		t.Fatalf("expected isErr with theirs message; got isErr=%v out=%s", isErr, out)
	}
}

func TestMergeThreeWay_AdapterFromPath(t *testing.T) {
	h := NewHandler()
	args := map[string]any{
		"path":           "x.go",
		"base_content":   "package x\n\nfunc A() int { return 1 }\n",
		"ours_content":   "package x\n\nfunc A() int { return 1 }\n\nfunc B() int { return 2 }\n",
		"theirs_content": "package x\n\nfunc A() int { return 1 }\n",
	}
	out, isErr, err := h.handleMergeThreeWay(args)
	if err != nil || isErr {
		t.Fatalf("merge_three_way err=%v isErr=%v out=%s", err, isErr, out)
	}
	var resp mergeThreeWayResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, out)
	}
	if !resp.Clean || !strings.Contains(resp.Merged, "func B()") {
		t.Fatalf("expected clean merge with func B; got clean=%v merged=%s", resp.Clean, resp.Merged)
	}
}
