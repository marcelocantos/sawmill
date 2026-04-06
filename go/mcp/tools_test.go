// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testHandler creates a Handler and parses a temp directory containing
// the given files. The map keys are relative paths (e.g. "foo.py") and values
// are file contents.
func testHandler(t *testing.T, files map[string]string) *Handler {
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
	h := NewHandler()
	text, isErr, err := h.handleParse(map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("handleParse: %v", err)
	}
	if isErr {
		t.Fatalf("handleParse returned error: %s", text)
	}
	if strings.Contains(text, "error") || strings.Contains(text, "Error") {
		t.Fatalf("parse returned error: %s", text)
	}
	return h
}

// testHandlerWithDir is like testHandler but also returns the temp directory path.
func testHandlerWithDir(t *testing.T, files map[string]string) (*Handler, string) {
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
	h := NewHandler()
	text, isErr, err := h.handleParse(map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("handleParse: %v", err)
	}
	if isErr {
		t.Fatalf("handleParse returned error: %s", text)
	}
	if strings.Contains(text, "error") || strings.Contains(text, "Error") {
		t.Fatalf("parse returned error: %s", text)
	}
	return h, dir
}

// --- Core workflow tests ---

func TestParseDirectory(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"hello.py": "def hello():\n    pass\n",
		"lib.rs":   "fn greet() {}\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	text, isErr, err := h.handleParse(map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("handleParse error: %v", err)
	}
	if isErr {
		t.Fatalf("handleParse returned tool error: %s", text)
	}

	if !strings.Contains(text, "2 file(s)") {
		t.Errorf("expected '2 file(s)' in summary, got: %s", text)
	}
	if !strings.Contains(text, "python") {
		t.Errorf("expected 'python' in summary, got: %s", text)
	}
	if !strings.Contains(text, "rust") {
		t.Errorf("expected 'rust' in summary, got: %s", text)
	}
}

func TestRenameProducesDiff(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n\nfoo()\n",
	})

	text, _, err := h.handleRename(map[string]any{
		"from": "foo",
		"to":   "bar",
	})
	if err != nil {
		t.Fatalf("handleRename error: %v", err)
	}

	if !strings.Contains(text, "foo") {
		t.Errorf("expected 'foo' in diff, got: %s", text)
	}
	if !strings.Contains(text, "bar") {
		t.Errorf("expected 'bar' in diff, got: %s", text)
	}
	if !strings.Contains(text, "Renamed") {
		t.Errorf("expected 'Renamed' in output, got: %s", text)
	}
}

func TestQueryFindsFunction(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n\ndef bar():\n    return 1\n",
	})

	text, _, err := h.handleQuery(map[string]any{
		"kind": "function",
		"name": "foo",
	})
	if err != nil {
		t.Fatalf("handleQuery error: %v", err)
	}

	if !strings.Contains(text, "1 match") {
		t.Errorf("expected '1 match' in output, got: %s", text)
	}
	if !strings.Contains(text, "foo") {
		t.Errorf("expected 'foo' in output, got: %s", text)
	}
}

func TestQueryWithGlob(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def test_one():\n    pass\n\ndef test_two():\n    pass\n\ndef helper():\n    pass\n",
	})

	text, _, err := h.handleQuery(map[string]any{
		"kind": "function",
		"name": "test_*",
	})
	if err != nil {
		t.Fatalf("handleQuery error: %v", err)
	}

	if !strings.Contains(text, "2 match") {
		t.Errorf("expected '2 match' in output, got: %s", text)
	}
	if !strings.Contains(text, "test_one") {
		t.Errorf("expected 'test_one' in output, got: %s", text)
	}
	if !strings.Contains(text, "test_two") {
		t.Errorf("expected 'test_two' in output, got: %s", text)
	}
}

func TestTransformReplace(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n",
	})

	text, _, err := h.handleTransform(map[string]any{
		"kind":   "function",
		"name":   "foo",
		"action": "replace",
		"code":   "# replaced\n",
	})
	if err != nil {
		t.Fatalf("handleTransform error: %v", err)
	}

	if !strings.Contains(text, "changes in") {
		t.Errorf("expected 'changes in' in output, got: %s", text)
	}
	if !strings.Contains(text, "replaced") {
		t.Errorf("expected 'replaced' in diff, got: %s", text)
	}
}

func TestTransformRemove(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n\ndef bar():\n    return 1\n",
	})

	text, _, err := h.handleTransform(map[string]any{
		"kind":   "function",
		"name":   "foo",
		"action": "remove",
	})
	if err != nil {
		t.Fatalf("handleTransform error: %v", err)
	}

	if !strings.Contains(text, "changes in") {
		t.Errorf("expected 'changes in' in output, got: %s", text)
	}
	if !strings.Contains(text, "foo") {
		t.Errorf("expected 'foo' in diff output, got: %s", text)
	}
}

func TestTransformJS(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def old_name():\n    pass\n",
	})

	// JS transform that renames the function by modifying its text.
	jsFn := `function(node) { return node.text.replace("old_name", "new_name"); }`

	text, _, err := h.handleTransform(map[string]any{
		"kind":         "function",
		"name":         "old_name",
		"transform_fn": jsFn,
	})
	if err != nil {
		t.Fatalf("handleTransform error: %v", err)
	}

	if !strings.Contains(text, "new_name") {
		t.Errorf("expected 'new_name' in diff, got: %s", text)
	}
}

func TestApplyAndUndo(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"main.py": "def foo():\n    pass\n\nfoo()\n",
	})
	filePath := filepath.Join(dir, "main.py")

	// Rename foo -> bar.
	_, _, err := h.handleRename(map[string]any{
		"from": "foo",
		"to":   "bar",
	})
	if err != nil {
		t.Fatalf("handleRename error: %v", err)
	}

	// Apply with confirm=true.
	text, _, err := h.handleApply(map[string]any{
		"confirm": true,
	})
	if err != nil {
		t.Fatalf("handleApply error: %v", err)
	}
	if !strings.Contains(text, "Applied") {
		t.Errorf("expected 'Applied' in output, got: %s", text)
	}

	// Verify file changed on disk.
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if !strings.Contains(string(content), "bar") {
		t.Errorf("expected 'bar' in file after apply, got: %s", string(content))
	}
	if strings.Contains(string(content), "def foo") {
		t.Errorf("expected 'foo' to be renamed in file, got: %s", string(content))
	}

	// Undo.
	text, _, err = h.handleUndo(nil)
	if err != nil {
		t.Fatalf("handleUndo error: %v", err)
	}
	if !strings.Contains(text, "Restored") {
		t.Errorf("expected 'Restored' in output, got: %s", text)
	}

	// Verify file restored on disk.
	content, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file after undo: %v", err)
	}
	if !strings.Contains(string(content), "def foo") {
		t.Errorf("expected original 'foo' restored, got: %s", string(content))
	}
}

func TestCodegenProgram(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def greet():\n    pass\n",
	})

	// A codegen program that renames 'greet' to 'hello' using ctx.
	program := `
		var fns = ctx.findFunction("greet");
		for (var i = 0; i < fns.length; i++) {
			fns[i].replaceName("hello");
		}
	`

	text, _, err := h.handleCodegen(map[string]any{
		"program": program,
	})
	if err != nil {
		t.Fatalf("handleCodegen error: %v", err)
	}

	if !strings.Contains(text, "hello") {
		t.Errorf("expected 'hello' in diff, got: %s", text)
	}
}

// --- Recipe tests ---

func TestTeachAndInstantiateRecipe(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n",
	})

	// Teach a recipe that renames a function.
	stepsJSON := `[{"kind":"function","name":"$old","action":"replace_name","code":"$new"}]`
	text, _, err := h.handleTeachRecipe(map[string]any{
		"name":        "rename-func",
		"description": "Rename a function",
		"params":      `["old","new"]`,
		"steps":       stepsJSON,
	})
	if err != nil {
		t.Fatalf("handleTeachRecipe error: %v", err)
	}
	if !strings.Contains(text, "saved") {
		t.Errorf("expected 'saved' in output, got: %s", text)
	}

	// List recipes.
	text, _, err = h.handleListRecipes(nil)
	if err != nil {
		t.Fatalf("handleListRecipes error: %v", err)
	}
	if !strings.Contains(text, "rename-func") {
		t.Errorf("expected 'rename-func' in list, got: %s", text)
	}

	// Instantiate with params.
	text, _, err = h.handleInstantiate(map[string]any{
		"recipe": "rename-func",
		"params": `{"old":"foo","new":"bar"}`,
	})
	if err != nil {
		t.Fatalf("handleInstantiate error: %v", err)
	}
	if !strings.Contains(text, "bar") {
		t.Errorf("expected 'bar' in instantiate output, got: %s", text)
	}
}

func TestListRecipesEmpty(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	text, _, err := h.handleListRecipes(nil)
	if err != nil {
		t.Fatalf("handleListRecipes error: %v", err)
	}
	if !strings.Contains(text, "No recipes") {
		t.Errorf("expected 'No recipes' in output, got: %s", text)
	}
}

// --- Convention tests ---

func TestTeachAndCheckConvention(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "print('hello')\ndef foo():\n    print('world')\n",
	})

	checkProgram := `
		var violations = [];
		for (var i = 0; i < ctx.files.length; i++) {
			var f = ctx.files[i];
			var src = ctx.readFile(f);
			if (src === null) continue;
			var lines = src.split("\n");
			for (var j = 0; j < lines.length; j++) {
				if (lines[j].indexOf("print(") >= 0) {
					violations.push(f + ":" + (j+1) + ": print() call found");
				}
			}
		}
		return violations;
	`

	text, _, err := h.handleTeachConvention(map[string]any{
		"name":          "no-print",
		"description":   "No print() calls allowed",
		"check_program": checkProgram,
	})
	if err != nil {
		t.Fatalf("handleTeachConvention error: %v", err)
	}
	if !strings.Contains(text, "saved") {
		t.Errorf("expected 'saved' in output, got: %s", text)
	}

	// Check conventions.
	text, _, err = h.handleCheckConventions(map[string]any{})
	if err != nil {
		t.Fatalf("handleCheckConventions error: %v", err)
	}
	if !strings.Contains(text, "violation") {
		t.Errorf("expected violations in output, got: %s", text)
	}
	if !strings.Contains(text, "print()") || !strings.Contains(text, "no-print") {
		t.Errorf("expected convention name and violation details, got: %s", text)
	}
}

func TestListConventions(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	// Teach a convention.
	_, _, err := h.handleTeachConvention(map[string]any{
		"name":          "test-conv",
		"description":   "A test convention",
		"check_program": "[]",
	})
	if err != nil {
		t.Fatalf("handleTeachConvention error: %v", err)
	}

	// List conventions.
	text, _, err := h.handleListConventions(nil)
	if err != nil {
		t.Fatalf("handleListConventions error: %v", err)
	}
	if !strings.Contains(text, "test-conv") {
		t.Errorf("expected 'test-conv' in list, got: %s", text)
	}
	if !strings.Contains(text, "1 convention") {
		t.Errorf("expected '1 convention' in output, got: %s", text)
	}
}

func TestDeleteConvention(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	// Teach a convention.
	_, _, err := h.handleTeachConvention(map[string]any{
		"name":          "doomed",
		"description":   "Will be deleted",
		"check_program": "[]",
	})
	if err != nil {
		t.Fatalf("handleTeachConvention error: %v", err)
	}

	// Delete via the model (no handler for delete_convention).
	h.mu.Lock()
	deleted, err := h.model.DeleteConvention("doomed")
	h.mu.Unlock()
	if err != nil {
		t.Fatalf("DeleteConvention error: %v", err)
	}
	if !deleted {
		t.Error("expected convention to be deleted")
	}

	// List conventions — should be empty.
	text, _, err := h.handleListConventions(nil)
	if err != nil {
		t.Fatalf("handleListConventions error: %v", err)
	}
	if !strings.Contains(text, "No conventions") {
		t.Errorf("expected 'No conventions' in output, got: %s", text)
	}
}

// --- Other tools ---

func TestFindSymbol(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def my_function():\n    pass\n",
	})

	text, _, err := h.handleFindSymbol(map[string]any{
		"symbol": "my_function",
	})
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}
	if !strings.Contains(text, "my_function") {
		t.Errorf("expected 'my_function' in output, got: %s", text)
	}
	if !strings.Contains(text, "1 occurrence") {
		t.Errorf("expected '1 occurrence' in output, got: %s", text)
	}
}

func TestFindReferences(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def helper():\n    pass\n\nhelper()\nhelper()\n",
	})

	text, _, err := h.handleFindReferences(map[string]any{
		"symbol": "helper",
	})
	if err != nil {
		t.Fatalf("handleFindReferences error: %v", err)
	}
	if !strings.Contains(text, "call site") {
		t.Errorf("expected 'call site' in output, got: %s", text)
	}
	if !strings.Contains(text, "helper") {
		t.Errorf("expected 'helper' in output, got: %s", text)
	}
}

func TestGetAgentPrompt(t *testing.T) {
	h := NewHandler()
	text, _, err := h.handleGetAgentPrompt(nil)
	if err != nil {
		t.Fatalf("handleGetAgentPrompt error: %v", err)
	}
	if text == "" {
		t.Error("expected non-empty agent prompt")
	}
	// The embedded guide or fallback should contain "Sawmill" or "sawmill".
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "sawmill") {
		t.Errorf("expected 'sawmill' in agent prompt, got: %s", text[:min(200, len(text))])
	}
}

func TestAddParameter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def greet(name):\n    print(name)\n",
	})

	text, _, err := h.handleAddParameter(map[string]any{
		"function":   "greet",
		"param_name": "greeting",
	})
	if err != nil {
		t.Fatalf("handleAddParameter error: %v", err)
	}
	if !strings.Contains(text, "greeting") {
		t.Errorf("expected 'greeting' in diff, got: %s", text)
	}
	if !strings.Contains(text, "Added parameter") {
		t.Errorf("expected 'Added parameter' in output, got: %s", text)
	}
}

func TestRemoveParameter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def greet(name, greeting):\n    print(name, greeting)\n",
	})

	text, _, err := h.handleRemoveParameter(map[string]any{
		"function":   "greet",
		"param_name": "greeting",
	})
	if err != nil {
		t.Fatalf("handleRemoveParameter error: %v", err)
	}
	if !strings.Contains(text, "Removed parameter") {
		t.Errorf("expected 'Removed parameter' in output, got: %s", text)
	}
	if !strings.Contains(text, "greeting") {
		t.Errorf("expected 'greeting' in diff, got: %s", text)
	}
}

// --- Edge case tests ---

func TestTransformNoMatches(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n",
	})

	text, _, err := h.handleTransform(map[string]any{
		"kind":   "function",
		"name":   "nonexistent",
		"action": "remove",
	})
	if err != nil {
		t.Fatalf("handleTransform error: %v", err)
	}
	if !strings.Contains(text, "No matches") {
		t.Errorf("expected 'No matches' message, got: %s", text)
	}
}

func TestApplyWithoutPending(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	text, _, err := h.handleApply(map[string]any{
		"confirm": true,
	})
	if err != nil {
		t.Fatalf("handleApply error: %v", err)
	}
	if !strings.Contains(text, "No pending") {
		t.Errorf("expected 'No pending' message, got: %s", text)
	}
}

func TestUndoWithoutBackups(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	text, _, err := h.handleUndo(nil)
	if err != nil {
		t.Fatalf("handleUndo error: %v", err)
	}
	if !strings.Contains(text, "No backups") {
		t.Errorf("expected 'No backups' message, got: %s", text)
	}
}

func TestApplyConfirmFalse(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n\nfoo()\n",
	})

	// Create pending changes.
	_, _, err := h.handleRename(map[string]any{
		"from": "foo",
		"to":   "bar",
	})
	if err != nil {
		t.Fatalf("handleRename error: %v", err)
	}

	// Apply with confirm=false should not write.
	text, _, err := h.handleApply(map[string]any{
		"confirm": false,
	})
	if err != nil {
		t.Fatalf("handleApply error: %v", err)
	}
	if !strings.Contains(text, "Pending") {
		t.Errorf("expected 'Pending' message with confirm=false, got: %s", text)
	}
}

func TestRenameNoOccurrences(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n",
	})

	text, _, err := h.handleRename(map[string]any{
		"from": "nonexistent_name",
		"to":   "something_else",
	})
	if err != nil {
		t.Fatalf("handleRename error: %v", err)
	}
	if !strings.Contains(text, "No occurrences") {
		t.Errorf("expected 'No occurrences' message, got: %s", text)
	}
}

func TestRequireModelBeforeParse(t *testing.T) {
	h := NewHandler()

	// All tools except parse should fail before parse is called.
	text, isErr, err := h.handleRename(map[string]any{
		"from": "a",
		"to":   "b",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error for rename before parse")
	}
	if !strings.Contains(text, "parse first") {
		t.Errorf("expected 'parse first' error, got: %s", text)
	}
}

func TestTeachByExample(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	text, _, err := h.handleTeachByExample(map[string]any{
		"name":       "greeting-template",
		"exemplar":   "def greet_alice():\n    print('Hello, Alice!')\n",
		"parameters": `{"person":"Alice"}`,
	})
	if err != nil {
		t.Fatalf("handleTeachByExample error: %v", err)
	}
	if !strings.Contains(text, "greeting-template") {
		t.Errorf("expected recipe name in output, got: %s", text)
	}
	if !strings.Contains(text, "person") {
		t.Errorf("expected parameter 'person' in output, got: %s", text)
	}
}

func TestTransformBatch(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def alpha():\n    pass\n\ndef beta():\n    pass\n",
	})

	transformsJSON := `[
		{"kind":"function","name":"alpha","action":"replace_name","code":"one"},
		{"kind":"function","name":"beta","action":"replace_name","code":"two"}
	]`

	text, _, err := h.handleTransformBatch(map[string]any{
		"transforms": transformsJSON,
	})
	if err != nil {
		t.Fatalf("handleTransformBatch error: %v", err)
	}
	if !strings.Contains(text, "changes in") {
		t.Errorf("expected 'changes in' in output, got: %s", text)
	}
}

func TestFindSymbolNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def foo():\n    pass\n",
	})

	text, _, err := h.handleFindSymbol(map[string]any{
		"symbol": "nonexistent_symbol",
	})
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

func TestAddParameterWithTypeAndDefault(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def greet():\n    pass\n",
	})

	text, _, err := h.handleAddParameter(map[string]any{
		"function":      "greet",
		"param_name":    "name",
		"param_type":    "str",
		"default_value": "'World'",
		"position":      "first",
	})
	if err != nil {
		t.Fatalf("handleAddParameter error: %v", err)
	}
	if !strings.Contains(text, "name") {
		t.Errorf("expected 'name' in diff, got: %s", text)
	}
	if !strings.Contains(text, "Added parameter") {
		t.Errorf("expected 'Added parameter' in output, got: %s", text)
	}
}

func TestRemoveParameterNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "def greet(name):\n    pass\n",
	})

	text, _, err := h.handleRemoveParameter(map[string]any{
		"function":   "greet",
		"param_name": "nonexistent",
	})
	if err != nil {
		t.Fatalf("handleRemoveParameter error: %v", err)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}
