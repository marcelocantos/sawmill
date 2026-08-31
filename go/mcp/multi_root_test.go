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
			Code:     new(`"world"`),
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
			Code:     new(`"world"`),
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

// TestTransformMultiRootThreeRoots verifies N>2 scaling: two good roots and one
// bad root in a single call. Both good roots produce independent diff bundles;
// the bad root records a per-root error without aborting the other two.
func TestTransformMultiRootThreeRoots(t *testing.T) {
	root1 := makeRoot(t, map[string]string{
		"alpha.py": "msg = \"hello\"\n",
	})
	root2 := makeRoot(t, map[string]string{
		"beta.py":  "greeting = \"hello\"\n",
		"gamma.py": "farewell = \"bye\"\n",
	})
	badRoot := "/nonexistent/does-not-exist-for-t25"

	h := NewHandlerWithLoader(loaderForDirs())

	rootsJSON, _ := json.Marshal([]string{root1, root2, badRoot})
	transformsJSON, _ := json.Marshal([]transformSpec{
		{
			RawQuery: `(string) @s`,
			Capture:  "s",
			Action:   "replace",
			Code:     new(`"world"`),
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
		t.Fatalf("unmarshalling results: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 entries in results, got %d", len(results))
	}

	b1, ok := results[root1]
	if !ok {
		t.Fatalf("root1 missing from results")
	}
	if b1.Error != "" {
		t.Errorf("root1 unexpected error: %s", b1.Error)
	}
	if b1.FileCount != 1 {
		t.Errorf("root1 FileCount: want 1, got %d", b1.FileCount)
	}
	if len(b1.Diffs) != 1 {
		t.Errorf("root1 Diffs: want 1, got %d", len(b1.Diffs))
	}

	b2, ok := results[root2]
	if !ok {
		t.Fatalf("root2 missing from results")
	}
	if b2.Error != "" {
		t.Errorf("root2 unexpected error: %s", b2.Error)
	}
	if b2.FileCount != 2 {
		t.Errorf("root2 FileCount: want 2 (beta.py + gamma.py), got %d", b2.FileCount)
	}
	if len(b2.Diffs) != 2 {
		t.Errorf("root2 Diffs: want 2, got %d", len(b2.Diffs))
	}

	bad, ok := results[badRoot]
	if !ok {
		t.Fatalf("bad root missing from results")
	}
	if bad.Error == "" {
		t.Error("bad root: expected Error field to be set")
	}

	// Summary line must name all three roots.
	if !strings.Contains(text, "3 root(s)") {
		t.Errorf("expected '3 root(s)' in summary, got: %s", text[:min(200, len(text))])
	}
	// At least 3 files changed (alpha.py + beta.py + gamma.py).
	if !strings.Contains(text, "3 file(s)") {
		t.Errorf("expected '3 file(s)' in summary, got: %s", text[:min(200, len(text))])
	}
	// Error count must appear.
	if !strings.Contains(text, "1 root(s) with errors") {
		t.Errorf("expected '1 root(s) with errors' in summary, got: %s", text[:min(200, len(text))])
	}
}

