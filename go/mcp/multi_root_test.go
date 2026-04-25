// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/model"
)

// makeRoot creates a temp directory with the given files and returns its path.
func makeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating dir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

// loaderForDirs returns a ModelLoader that loads a fresh model for each root.
func loaderForDirs() ModelLoader {
	return func(root string) (*model.CodebaseModel, func(), error) {
		m, err := model.Load(root)
		if err != nil {
			return nil, nil, err
		}
		return m, func() { _ = m.Close() }, nil
	}
}

// TestTransformMultiRootTwoRoots verifies that transform_multi_root applies a
// transform to two independent toy repos and returns per-root diffs.
func TestTransformMultiRootTwoRoots(t *testing.T) {
	root1 := makeRoot(t, map[string]string{
		"main.py": "x = \"hello\"\n",
	})
	root2 := makeRoot(t, map[string]string{
		"lib.py": "y = \"hello\"\n",
	})

	h := NewHandlerWithLoader(loaderForDirs())

	rootsJSON, _ := json.Marshal([]string{root1, root2})
	transformsJSON, _ := json.Marshal([]transformSpec{
		{
			RawQuery: `(string) @s`,
			Capture:  "s",
			Action:   "replace",
			Code:     strPtr(`"world"`),
		},
	})

	text, isErr, err := h.handleTransformMultiRoot(map[string]any{
		"roots":      string(rootsJSON),
		"transforms": string(transformsJSON),
	})
	if err != nil {
		t.Fatalf("handleTransformMultiRoot error: %v", err)
	}
	if isErr {
		t.Fatalf("handleTransformMultiRoot returned tool error: %s", text)
	}

	// Both roots should appear in the output.
	if !strings.Contains(text, root1) {
		t.Errorf("expected root1 %q in output", root1)
	}
	if !strings.Contains(text, root2) {
		t.Errorf("expected root2 %q in output", root2)
	}

	// Diffs should mention the replacement.
	if !strings.Contains(text, "world") {
		t.Errorf("expected 'world' in diffs, got: %s", text)
	}

	// Parse the JSON portion.
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output: %s", text)
	}
	var results map[string]RootDiffBundle
	if err := json.Unmarshal([]byte(text[jsonStart:]), &results); err != nil {
		t.Fatalf("unmarshalling result JSON: %v", err)
	}

	b1, ok := results[root1]
	if !ok {
		t.Fatalf("root1 not in results")
	}
	if b1.Error != "" {
		t.Errorf("root1 error: %s", b1.Error)
	}
	if b1.FileCount != 1 {
		t.Errorf("root1 FileCount: want 1, got %d", b1.FileCount)
	}
	if len(b1.Diffs) != 1 {
		t.Errorf("root1 Diffs: want 1, got %d", len(b1.Diffs))
	}

	b2, ok := results[root2]
	if !ok {
		t.Fatalf("root2 not in results")
	}
	if b2.Error != "" {
		t.Errorf("root2 error: %s", b2.Error)
	}
	if b2.FileCount != 1 {
		t.Errorf("root2 FileCount: want 1, got %d", b2.FileCount)
	}
}

// TestTransformMultiRootPendingIsolation verifies that applying a multi-root
// transform does not affect the session's single-root pending state.
func TestTransformMultiRootPendingIsolation(t *testing.T) {
	root1 := makeRoot(t, map[string]string{
		"a.py": "x = \"hello\"\n",
	})
	root2 := makeRoot(t, map[string]string{
		"b.py": "y = \"hello\"\n",
	})

	h := NewHandlerWithLoader(loaderForDirs())

	// Prime a single-root pending change on the handler's own session model.
	_, _, _ = h.handleParse(map[string]any{"path": root1})
	text, isErr, err := h.handleTransform(map[string]any{
		"raw_query": `(string) @s`,
		"capture":   "s",
		"action":    "replace",
		"code":      `"session"`,
	})
	if err != nil || isErr {
		t.Fatalf("session transform failed: %v / %s", err, text)
	}

	pendingBefore := h.pending

	// Run multi-root — should not touch h.pending.
	rootsJSON, _ := json.Marshal([]string{root2})
	transformsJSON, _ := json.Marshal([]transformSpec{
		{
			RawQuery: `(string) @s`,
			Capture:  "s",
			Action:   "replace",
			Code:     strPtr(`"world"`),
		},
	})
	_, isErr2, err2 := h.handleTransformMultiRoot(map[string]any{
		"roots":      string(rootsJSON),
		"transforms": string(transformsJSON),
	})
	if err2 != nil || isErr2 {
		t.Fatalf("multi-root transform failed: %v", err2)
	}

	if h.pending != pendingBefore {
		t.Error("handleTransformMultiRoot must not modify h.pending")
	}
}

// TestTransformMultiRootBadRoot verifies that a single bad root does not abort
// the entire call — the error is captured per-root.
func TestTransformMultiRootBadRoot(t *testing.T) {
	goodRoot := makeRoot(t, map[string]string{
		"ok.py": "z = 1\n",
	})
	badRoot := "/nonexistent/path/that/does/not/exist"

	h := NewHandlerWithLoader(loaderForDirs())

	rootsJSON, _ := json.Marshal([]string{goodRoot, badRoot})
	transformsJSON, _ := json.Marshal([]transformSpec{
		{
			Kind:   "function",
			Name:   "*",
			Action: "remove",
		},
	})

	text, isErr, err := h.handleTransformMultiRoot(map[string]any{
		"roots":      string(rootsJSON),
		"transforms": string(transformsJSON),
	})
	if err != nil {
		t.Fatalf("unexpected system error: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}

	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output: %s", text)
	}
	var results map[string]RootDiffBundle
	if err := json.Unmarshal([]byte(text[jsonStart:]), &results); err != nil {
		t.Fatalf("unmarshalling result: %v", err)
	}

	badBundle, ok := results[badRoot]
	if !ok {
		t.Fatalf("bad root not in results")
	}
	if badBundle.Error == "" {
		t.Error("expected Error field for bad root")
	}

	goodBundle, ok := results[goodRoot]
	if !ok {
		t.Fatalf("good root not in results")
	}
	if goodBundle.Error != "" {
		t.Errorf("unexpected error for good root: %s", goodBundle.Error)
	}
}

// strPtr is a test helper returning a pointer to s.
func strPtr(s string) *string { return &s }
