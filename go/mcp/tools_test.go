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

// --- rename_file tests ---

func TestRenameFilePython(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"utils.py": "def helper():\n    pass\n",
		"main.py":  "import utils\n\nutils.helper()\n",
	})

	text, isErr, err := h.handleRenameFile(map[string]any{
		"from": "utils.py",
		"to":   "helpers.py",
	})
	if err != nil {
		t.Fatalf("handleRenameFile error: %v", err)
	}
	if isErr {
		t.Fatalf("handleRenameFile returned tool error: %s", text)
	}

	if !strings.Contains(text, "Rename") {
		t.Errorf("expected 'Rename' in output, got: %s", text)
	}
	if !strings.Contains(text, "helpers") {
		t.Errorf("expected 'helpers' in diff, got: %s", text)
	}

	// Apply changes.
	text, _, err = h.handleApply(map[string]any{"confirm": true})
	if err != nil {
		t.Fatalf("handleApply error: %v", err)
	}
	if !strings.Contains(text, "Applied") {
		t.Errorf("expected 'Applied' in output, got: %s", text)
	}

	// Verify main.py was updated on disk.
	content, err := os.ReadFile(filepath.Join(dir, "main.py"))
	if err != nil {
		t.Fatalf("reading main.py: %v", err)
	}
	if !strings.Contains(string(content), "helpers") {
		t.Errorf("expected 'helpers' import in main.py, got: %s", string(content))
	}
	if strings.Contains(string(content), "import utils") {
		t.Errorf("expected 'utils' import to be replaced, got: %s", string(content))
	}

	// Verify file was renamed.
	if _, err := os.Stat(filepath.Join(dir, "helpers.py")); os.IsNotExist(err) {
		t.Error("expected helpers.py to exist after apply")
	}
	if _, err := os.Stat(filepath.Join(dir, "utils.py")); !os.IsNotExist(err) {
		t.Error("expected utils.py to be renamed away")
	}
}

func TestRenameFileTypeScript(t *testing.T) {
	h, _ := testHandlerWithDir(t, map[string]string{
		"utils.ts": "export function helper() {}\n",
		"main.ts":  "import { helper } from \"./utils\";\n\nhelper();\n",
	})

	text, isErr, err := h.handleRenameFile(map[string]any{
		"from": "utils.ts",
		"to":   "helpers.ts",
	})
	if err != nil {
		t.Fatalf("handleRenameFile error: %v", err)
	}
	if isErr {
		t.Fatalf("handleRenameFile returned tool error: %s", text)
	}

	if !strings.Contains(text, "import updates") {
		t.Errorf("expected import updates in output, got: %s", text)
	}
	if !strings.Contains(text, "helpers") {
		t.Errorf("expected 'helpers' in diff, got: %s", text)
	}
}

func TestRenameFileCpp(t *testing.T) {
	h, _ := testHandlerWithDir(t, map[string]string{
		"util.h":  "void helper();\n",
		"main.cpp": "#include \"util.h\"\n\nint main() { helper(); }\n",
	})

	text, isErr, err := h.handleRenameFile(map[string]any{
		"from": "util.h",
		"to":   "helper.h",
	})
	if err != nil {
		t.Fatalf("handleRenameFile error: %v", err)
	}
	if isErr {
		t.Fatalf("handleRenameFile returned tool error: %s", text)
	}

	if !strings.Contains(text, "helper.h") {
		t.Errorf("expected 'helper.h' in diff, got: %s", text)
	}
}

func TestRenameFileNoImporters(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"lonely.py": "x = 1\n",
		"other.py":  "y = 2\n",
	})

	text, isErr, err := h.handleRenameFile(map[string]any{
		"from": "lonely.py",
		"to":   "solo.py",
	})
	if err != nil {
		t.Fatalf("handleRenameFile error: %v", err)
	}
	if isErr {
		t.Fatalf("handleRenameFile returned tool error: %s", text)
	}

	if !strings.Contains(text, "Rename") {
		t.Errorf("expected 'Rename' in output, got: %s", text)
	}
	// No import updates expected.
	if strings.Contains(text, "import updates") {
		t.Errorf("expected no import updates, got: %s", text)
	}

	// Apply and verify the rename still happens.
	text, _, err = h.handleApply(map[string]any{"confirm": true})
	if err != nil {
		t.Fatalf("handleApply error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "solo.py")); os.IsNotExist(err) {
		t.Error("expected solo.py to exist after apply")
	}
}

func TestRenameFileNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": "x = 1\n",
	})

	text, isErr, err := h.handleRenameFile(map[string]any{
		"from": "nonexistent.py",
		"to":   "something.py",
	})
	if err != nil {
		t.Fatalf("handleRenameFile error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error for nonexistent file")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

// --- clone_and_adapt tests ---

func TestCloneAndAdaptBySymbolName(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"source.py": "def hello(name):\n    print('Hello, ' + name)\n",
		"target.py": "# target file\n",
	})

	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        "hello",
		"substitutions": `{"hello": "greet", "Hello": "Greetings"}`,
		"target_file":   filepath.Join(dir, "target.py"),
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCloneAndAdapt returned tool error: %s", text)
	}

	if !strings.Contains(text, "Cloned and adapted") {
		t.Errorf("expected 'Cloned and adapted' in output, got: %s", text)
	}
	if !strings.Contains(text, "greet") {
		t.Errorf("expected 'greet' in diff, got: %s", text)
	}
	if !strings.Contains(text, "Greetings") {
		t.Errorf("expected 'Greetings' in diff, got: %s", text)
	}
}

func TestCloneAndAdaptByLineRange(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"source.py": "# line 1\ndef foo():\n    return 42\n# line 4\n",
		"target.py": "# target\n",
	})

	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        filepath.Join(dir, "source.py") + ":2-3",
		"substitutions": `{"foo": "bar", "42": "99"}`,
		"target_file":   filepath.Join(dir, "target.py"),
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCloneAndAdapt returned tool error: %s", text)
	}

	if !strings.Contains(text, "bar") {
		t.Errorf("expected 'bar' in diff, got: %s", text)
	}
	if !strings.Contains(text, "99") {
		t.Errorf("expected '99' in diff, got: %s", text)
	}
}

func TestCloneAndAdaptAfterSymbol(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"source.py": "def original():\n    return 1\n",
		"target.py": "def existing():\n    return 0\n\ndef another():\n    pass\n",
	})

	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        "original",
		"substitutions": `{"original": "cloned", "1": "2"}`,
		"target_file":   filepath.Join(dir, "target.py"),
		"position":      "after:existing",
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCloneAndAdapt returned tool error: %s", text)
	}

	if !strings.Contains(text, "cloned") {
		t.Errorf("expected 'cloned' in diff, got: %s", text)
	}
	// The cloned function should appear between existing and another.
	if !strings.Contains(text, "Cloned and adapted") {
		t.Errorf("expected 'Cloned and adapted' in output, got: %s", text)
	}
}

func TestCloneAndAdaptMultipleSubstitutions(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"source.py": "def process_user(user_name, user_id):\n    print(user_name, user_id)\n",
		"target.py": "# target\n",
	})

	// "user_name" is longer than "user" — longest-first ordering should
	// replace "user_name" before "user" to avoid partial matches.
	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        "process_user",
		"substitutions": `{"user_name": "account_holder", "user_id": "account_number", "user": "account", "process": "handle"}`,
		"target_file":   filepath.Join(dir, "target.py"),
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCloneAndAdapt returned tool error: %s", text)
	}

	// Verify longest-first: "user_name" should become "account_holder", NOT "account_name".
	if !strings.Contains(text, "account_holder") {
		t.Errorf("expected 'account_holder' (longest-first sub), got: %s", text)
	}
	if !strings.Contains(text, "account_number") {
		t.Errorf("expected 'account_number', got: %s", text)
	}
	if !strings.Contains(text, "handle_account") {
		t.Errorf("expected 'handle_account', got: %s", text)
	}
}

func TestCloneAndAdaptSymbolNotFound(t *testing.T) {
	h, dir := testHandlerWithDir(t, map[string]string{
		"source.py": "def foo():\n    pass\n",
		"target.py": "# target\n",
	})

	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        "nonexistent_symbol",
		"substitutions": `{"a": "b"}`,
		"target_file":   filepath.Join(dir, "target.py"),
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error for missing symbol")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

func TestCloneAndAdaptTargetNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"source.py": "def foo():\n    pass\n",
	})

	text, isErr, err := h.handleCloneAndAdapt(map[string]any{
		"source":        "foo",
		"substitutions": `{"foo": "bar"}`,
		"target_file":   "/nonexistent/path/target.py",
	})
	if err != nil {
		t.Fatalf("handleCloneAndAdapt error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error for missing target file")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

// --- LSP tool tests (graceful degradation) ---

func TestHoverNoModel(t *testing.T) {
	h := NewHandler()
	text, isErr, err := h.handleHover(map[string]any{
		"file":   "/tmp/test.py",
		"line":   float64(1),
		"column": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error when no model is loaded")
	}
	if !strings.Contains(text, "no codebase loaded") {
		t.Errorf("expected 'no codebase loaded', got: %s", text)
	}
}

func TestHoverNoLSP(t *testing.T) {
	// Use a file extension with no real LSP server (.test) to test degradation.
	h := testHandler(t, map[string]string{
		"hello.py": "def hello():\n    pass\n",
	})
	text, isErr, err := h.handleHover(map[string]any{
		"file":   "/tmp/test.foobar",
		"line":   float64(1),
		"column": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isErr {
		t.Errorf("expected graceful degradation, not tool error: %s", text)
	}
	// Should return a helpful message about no adapter for this extension.
	if !strings.Contains(text, "No language adapter") {
		t.Errorf("expected 'No language adapter' message, got: %s", text)
	}
}

func TestDefinitionNoModel(t *testing.T) {
	h := NewHandler()
	text, isErr, err := h.handleDefinition(map[string]any{
		"file":   "/tmp/test.go",
		"line":   float64(1),
		"column": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error when no model is loaded")
	}
	if !strings.Contains(text, "no codebase loaded") {
		t.Errorf("expected 'no codebase loaded', got: %s", text)
	}
}

func TestLspReferencesNoModel(t *testing.T) {
	h := NewHandler()
	text, isErr, err := h.handleLspReferences(map[string]any{
		"file":   "/tmp/test.rs",
		"line":   float64(1),
		"column": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error when no model is loaded")
	}
	if !strings.Contains(text, "no codebase loaded") {
		t.Errorf("expected 'no codebase loaded', got: %s", text)
	}
}

func TestDiagnosticsNoModel(t *testing.T) {
	h := NewHandler()
	text, isErr, err := h.handleDiagnostics(map[string]any{
		"file": "/tmp/test.ts",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error when no model is loaded")
	}
	if !strings.Contains(text, "no codebase loaded") {
		t.Errorf("expected 'no codebase loaded', got: %s", text)
	}
}

func TestHoverMissingParams(t *testing.T) {
	h := NewHandler()
	text, isErr, err := h.handleHover(map[string]any{
		"file": "/tmp/test.py",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isErr {
		t.Error("expected tool error for missing line param")
	}
	if !strings.Contains(text, "line is required") {
		t.Errorf("expected 'line is required', got: %s", text)
	}
}

func TestHoverUnknownExtension(t *testing.T) {
	h := testHandler(t, map[string]string{
		"hello.py": "x = 1\n",
	})
	text, isErr, err := h.handleHover(map[string]any{
		"file":   "/tmp/test.xyz",
		"line":   float64(1),
		"column": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isErr {
		t.Errorf("expected graceful degradation, not tool error: %s", text)
	}
	if !strings.Contains(text, "No language adapter") {
		t.Errorf("expected 'No language adapter' message, got: %s", text)
	}
}

// --- add_field tests ---

func TestAddFieldGoStruct(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type Foo struct {
	Name string
}

func NewFoo(name string) Foo {
	return Foo{Name: name}
}

var f = NewFoo("hello")
`,
	})

	text, isErr, err := h.handleAddField(map[string]any{
		"type_name":     "Foo",
		"field_name":    "Age",
		"field_type":    "int",
		"default_value": "0",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}
	if isErr {
		t.Fatalf("handleAddField returned tool error: %s", text)
	}

	// Should report success.
	if !strings.Contains(text, "Added field") {
		t.Errorf("expected 'Added field' in output, got: %s", text)
	}

	// Field should be added to struct.
	if !strings.Contains(text, "Age int") || !strings.Contains(text, "+") {
		t.Errorf("expected field addition in diff, got: %s", text)
	}

	// Factory function should get new parameter.
	if !strings.Contains(text, "Age") {
		t.Errorf("expected 'Age' parameter in diff, got: %s", text)
	}

	// Caller should get new argument.
	if !strings.Contains(text, "0") {
		t.Errorf("expected default value '0' in diff, got: %s", text)
	}
}

func TestAddFieldGoStructLiteral(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type Point struct {
	X int
}

var p = Point{X: 1}
`,
	})

	text, isErr, err := h.handleAddField(map[string]any{
		"type_name":     "Point",
		"field_name":    "Y",
		"field_type":    "int",
		"default_value": "0",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}
	if isErr {
		t.Fatalf("handleAddField returned tool error: %s", text)
	}

	// Struct literal should get the new field initializer.
	if !strings.Contains(text, "Y: 0") {
		t.Errorf("expected 'Y: 0' field initializer in diff, got: %s", text)
	}
}

func TestAddFieldTypeNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type Foo struct {
	Name string
}
`,
	})

	text, _, err := h.handleAddField(map[string]any{
		"type_name":     "Bar",
		"field_name":    "Age",
		"field_type":    "int",
		"default_value": "0",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

func TestAddFieldNoConstructionSites(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type Config struct {
	Debug bool
}
`,
	})

	text, isErr, err := h.handleAddField(map[string]any{
		"type_name":     "Config",
		"field_name":    "Verbose",
		"field_type":    "bool",
		"default_value": "false",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}
	if isErr {
		t.Fatalf("handleAddField returned tool error: %s", text)
	}

	// Should still add the field to the struct.
	if !strings.Contains(text, "Added field") {
		t.Errorf("expected 'Added field' in output, got: %s", text)
	}
	if !strings.Contains(text, "Verbose bool") {
		t.Errorf("expected 'Verbose bool' in diff, got: %s", text)
	}
}

func TestAddFieldMultipleFiles(t *testing.T) {
	h := testHandler(t, map[string]string{
		"types.go": `package main

type Foo struct {
	Name string
}

func NewFoo(name string) Foo {
	return Foo{Name: name}
}
`,
		"use.go": `package main

var f = NewFoo("hello")
`,
	})

	text, isErr, err := h.handleAddField(map[string]any{
		"type_name":     "Foo",
		"field_name":    "Age",
		"field_type":    "int",
		"default_value": "0",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}
	if isErr {
		t.Fatalf("handleAddField returned tool error: %s", text)
	}

	// Should affect multiple files.
	if !strings.Contains(text, "2 file(s)") {
		t.Errorf("expected '2 file(s)' in output, got: %s", text)
	}
}

func TestAddFieldPythonClass(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `class Foo:
    def __init__(self, name):
        self.name = name

f = Foo("hello")
`,
	})

	text, isErr, err := h.handleAddField(map[string]any{
		"type_name":     "Foo",
		"field_name":    "age",
		"field_type":    "int",
		"default_value": "0",
	})
	if err != nil {
		t.Fatalf("handleAddField error: %v", err)
	}
	if isErr {
		t.Fatalf("handleAddField returned tool error: %s", text)
	}

	// Python class should get the field.
	if !strings.Contains(text, "Added field") {
		t.Errorf("expected 'Added field' in output, got: %s", text)
	}

	// The __init__ method should get new parameter.
	if !strings.Contains(text, "age") {
		t.Errorf("expected 'age' in diff, got: %s", text)
	}

	// The caller Foo("hello") should get new argument.
	if !strings.Contains(text, "0") {
		t.Errorf("expected '0' in diff, got: %s", text)
	}
}

// ---- dependency_usage tests -------------------------------------------------

func TestDependencyUsageGoPackage(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import "fmt"

func main() {
	fmt.Println("hello")
	fmt.Printf("%d\n", 42)
}
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "fmt",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	if !strings.Contains(text, `"fmt" used in 1 file`) {
		t.Errorf("expected package in 1 file, got: %s", text)
	}
	if !strings.Contains(text, "Println") {
		t.Errorf("expected 'Println' in output, got: %s", text)
	}
	if !strings.Contains(text, "Printf") {
		t.Errorf("expected 'Printf' in output, got: %s", text)
	}
}

func TestDependencyUsageGoAliasedImport(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import f "fmt"

func main() {
	f.Println("hello")
}
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "fmt",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	if !strings.Contains(text, `"fmt" used in 1 file`) {
		t.Errorf("expected package in 1 file, got: %s", text)
	}
	if !strings.Contains(text, "Println") {
		t.Errorf("expected 'Println' in output, got: %s", text)
	}
}

func TestDependencyUsageGoPublicAPIExposure(t *testing.T) {
	h := testHandler(t, map[string]string{
		"lib.go": `package lib

import "fmt"

// Printer wraps fmt.Stringer.
type Printer struct {
	Value fmt.Stringer
}

func NewPrinter(s fmt.Stringer) Printer {
	return Printer{Value: s}
}
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "fmt",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	if !strings.Contains(text, "Public API exposure") {
		t.Errorf("expected 'Public API exposure' section, got: %s", text)
	}
	// Printer or NewPrinter should appear as exported symbols using fmt.Stringer.
	if !strings.Contains(text, "fmt.Stringer") {
		t.Errorf("expected 'fmt.Stringer' in exposure, got: %s", text)
	}
}

func TestDependencyUsagePythonImport(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `import os

path = os.path.join("a", "b")
os.makedirs(path)
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "os",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	if !strings.Contains(text, `"os" used in 1 file`) {
		t.Errorf("expected package in 1 file, got: %s", text)
	}
	if !strings.Contains(text, "makedirs") {
		t.Errorf("expected 'makedirs' in output, got: %s", text)
	}
}

func TestDependencyUsageNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import "fmt"

func main() { fmt.Println("hi") }
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "nonexistent/pkg",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned Go error: %s", text)
	}

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

func TestDependencyUsagePathFilter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"cmd/main.go": `package main

import "fmt"

func main() { fmt.Println("hi") }
`,
		"lib/lib.go": `package lib

import "fmt"

func Greet() { fmt.Println("hello") }
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "fmt",
		"path":    "cmd",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	// Should only find the cmd file.
	if !strings.Contains(text, `"fmt" used in 1 file`) {
		t.Errorf("expected 1 file with path filter, got: %s", text)
	}
}

func TestDependencyUsageMultipleFiles(t *testing.T) {
	h := testHandler(t, map[string]string{
		"a.go": `package main

import "strings"

func A() string { return strings.ToUpper("hello") }
`,
		"b.go": `package main

import "strings"

func B() string { return strings.ToLower("WORLD") }
`,
	})

	text, isErr, err := h.handleDependencyUsage(map[string]any{
		"package": "strings",
	})
	if err != nil {
		t.Fatalf("handleDependencyUsage error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDependencyUsage returned tool error: %s", text)
	}

	if !strings.Contains(text, `"strings" used in 2 file`) {
		t.Errorf("expected 2 files, got: %s", text)
	}
}

// ---- Invariant tests ---------------------------------------------------------

func TestTeachInvariantAndCheckField(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type GoodConfig struct {
	Name string
	Value int
}

type BadConfig struct {
	Value int
}
`,
	})

	// Teach an invariant: types matching *Config must have a "Name" field.
	rule := `{"for_each":{"kind":"type","name":"*Config"},"require":[{"has_field":{"name":"Name"}}]}`
	text, isErr, err := h.handleTeachInvariant(map[string]any{
		"name":        "config-has-name",
		"description": "All Config types must have a Name field",
		"rule":        rule,
	})
	if err != nil {
		t.Fatalf("handleTeachInvariant error: %v", err)
	}
	if isErr {
		t.Fatalf("handleTeachInvariant returned tool error: %s", text)
	}
	if !strings.Contains(text, "saved") {
		t.Errorf("expected 'saved' in output, got: %s", text)
	}

	// Check invariants — should find BadConfig as a violation.
	text, isErr, err = h.handleCheckInvariants(map[string]any{})
	if err != nil {
		t.Fatalf("handleCheckInvariants error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCheckInvariants returned tool error: %s", text)
	}
	if !strings.Contains(text, "BadConfig") {
		t.Errorf("expected 'BadConfig' in violations, got: %s", text)
	}
	if strings.Contains(text, "GoodConfig") {
		t.Errorf("GoodConfig should not be a violation, got: %s", text)
	}
}

func TestTeachInvariantHasMethod(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type WithMethod struct {
	X int
}

func (w *WithMethod) Process() {}

type WithoutMethod struct {
	X int
}
`,
	})

	// Teach an invariant: types matching With* must have a "Process" method.
	rule := `{"for_each":{"kind":"type","name":"With*"},"require":[{"has_method":{"name":"Process"}}]}`
	_, isErr, err := h.handleTeachInvariant(map[string]any{
		"name": "has-process-method",
		"rule": rule,
	})
	if err != nil {
		t.Fatalf("handleTeachInvariant error: %v", err)
	}
	if isErr {
		t.Fatalf("handleTeachInvariant returned tool error (bool=true)")
	}

	text, isErr, err := h.handleCheckInvariants(map[string]any{})
	if err != nil {
		t.Fatalf("handleCheckInvariants error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCheckInvariants returned tool error: %s", text)
	}
	if !strings.Contains(text, "WithoutMethod") {
		t.Errorf("expected 'WithoutMethod' in violations, got: %s", text)
	}
	if strings.Contains(text, "WithMethod:") && strings.Contains(text, "missing method") {
		t.Errorf("WithMethod should satisfy the invariant, got: %s", text)
	}
}

func TestCheckInvariantsNoInvariants(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\ntype Foo struct{}\n",
	})

	text, isErr, err := h.handleCheckInvariants(map[string]any{})
	if err != nil {
		t.Fatalf("handleCheckInvariants error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCheckInvariants returned tool error: %s", text)
	}
	if !strings.Contains(text, "No invariants defined") {
		t.Errorf("expected 'No invariants defined', got: %s", text)
	}
}

func TestListInvariants(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\ntype Foo struct{}\n",
	})

	// Teach two invariants.
	for _, tc := range []struct{ name, rule string }{
		{"inv-one", `{"for_each":{"kind":"type","name":"*"},"require":[{"has_field":{"name":"ID"}}]}`},
		{"inv-two", `{"for_each":{"kind":"type","name":"*"},"require":[{"has_method":{"name":"String"}}]}`},
	} {
		_, isErr, err := h.handleTeachInvariant(map[string]any{
			"name": tc.name,
			"rule": tc.rule,
		})
		if err != nil || isErr {
			t.Fatalf("handleTeachInvariant(%s) error: err=%v isErr=%v", tc.name, err, isErr)
		}
	}

	text, isErr, err := h.handleListInvariants(map[string]any{})
	if err != nil {
		t.Fatalf("handleListInvariants error: %v", err)
	}
	if isErr {
		t.Fatalf("handleListInvariants returned tool error: %s", text)
	}
	if !strings.Contains(text, "inv-one") {
		t.Errorf("expected 'inv-one' in list, got: %s", text)
	}
	if !strings.Contains(text, "inv-two") {
		t.Errorf("expected 'inv-two' in list, got: %s", text)
	}
	if !strings.Contains(text, "2 invariant") {
		t.Errorf("expected '2 invariant' count, got: %s", text)
	}
}

func TestDeleteInvariant(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": "package main\n\ntype Foo struct{}\n",
	})

	// Teach then delete.
	_, _, _ = h.handleTeachInvariant(map[string]any{
		"name": "to-delete",
		"rule": `{"for_each":{"kind":"type","name":"*"},"require":[{"has_field":{"name":"ID"}}]}`,
	})

	text, isErr, err := h.handleDeleteInvariant(map[string]any{"name": "to-delete"})
	if err != nil {
		t.Fatalf("handleDeleteInvariant error: %v", err)
	}
	if isErr {
		t.Fatalf("handleDeleteInvariant returned tool error: %s", text)
	}
	if !strings.Contains(text, "deleted") {
		t.Errorf("expected 'deleted' in output, got: %s", text)
	}

	// List should be empty.
	text, _, _ = h.handleListInvariants(map[string]any{})
	if !strings.Contains(text, "No invariants saved") {
		t.Errorf("expected 'No invariants saved', got: %s", text)
	}
}

func TestCheckInvariantsPathFilter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"pkg1/types.go": `package pkg1

type FooConfig struct {
	Name string
}
`,
		"pkg2/types.go": `package pkg2

type BarConfig struct {
	Value int
}
`,
	})

	// Teach invariant: *Config types must have Name field.
	_, _, _ = h.handleTeachInvariant(map[string]any{
		"name": "config-has-name",
		"rule": `{"for_each":{"kind":"type","name":"*Config"},"require":[{"has_field":{"name":"Name"}}]}`,
	})

	// Check with path filter on pkg1 — should be OK (FooConfig has Name).
	text, isErr, err := h.handleCheckInvariants(map[string]any{"path": "pkg1"})
	if err != nil {
		t.Fatalf("handleCheckInvariants error: %v", err)
	}
	if isErr {
		t.Fatalf("handleCheckInvariants returned tool error: %s", text)
	}
	if strings.Contains(text, "violation") && strings.Contains(text, "BarConfig") {
		t.Errorf("pkg1 filter should not show pkg2 violations, got: %s", text)
	}

	// Check without filter — should find BarConfig violation.
	text, _, _ = h.handleCheckInvariants(map[string]any{})
	if !strings.Contains(text, "BarConfig") {
		t.Errorf("expected 'BarConfig' in violations without filter, got: %s", text)
	}
}

// ---- migrate_type tests -----------------------------------------------------

func TestMigrateTypeConstruction(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func main() {
	x := EqArgs{Eq: cmpFunc, Hash: hashFunc}
	_ = x
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"construction": {"old": "EqArgs{Eq: cmpFunc, Hash: hashFunc}", "new": "NewDefaultEqOps(cmpFunc, hashFunc)"}}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "NewDefaultEqOps(cmpFunc, hashFunc)") {
		t.Errorf("expected construction replacement in diff, got: %s", text)
	}
}

func TestMigrateTypeConstructionWithCaptures(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func main() {
	x := EqArgs{Eq: cmpFunc, Hash: hashFunc}
	_ = x
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"construction": {"old": "EqArgs{Eq: $eq, Hash: $hash}", "new": "NewDefaultEqOps($eq, $hash)"}}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "NewDefaultEqOps(cmpFunc, hashFunc)") {
		t.Errorf("expected construction replacement in diff, got: %s", text)
	}
}

func TestMigrateTypeFieldAccess(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func main() {
	args := EqArgs{Eq: cmpFunc, Hash: hashFunc}
	result := args.Eq(a, b)
	_ = result
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"field_access": {"$.Eq($a, $b)": "$.Equal($a, $b)"}}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "args.Equal(a, b)") {
		t.Errorf("expected field access replacement in diff, got: %s", text)
	}
}

func TestMigrateTypeFieldAccessProperty(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func main() {
	args := EqArgs{Eq: cmpFunc, Hash: hashFunc}
	if args.FullHash {
		doSomething()
	}
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"field_access": {"$.FullHash": "$.IsFullHash()"}}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "args.IsFullHash()") {
		t.Errorf("expected property->method replacement in diff, got: %s", text)
	}
}

func TestMigrateTypeRename(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type EqArgs struct {
	Eq   func(a, b int) bool
	Hash func(v int) uint64
}

var x EqArgs
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"type_rename": "EqOps"}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "EqOps") {
		t.Errorf("expected type rename in diff, got: %s", text)
	}
	if !strings.Contains(text, "+type EqOps struct") {
		t.Errorf("expected '+type EqOps struct' in diff, got: %s", text)
	}
}

func TestMigrateTypeCombined(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type EqArgs struct {
	Eq   func(a, b int) bool
	Hash func(v int) uint64
}

func main() {
	args := EqArgs{Eq: cmpFunc, Hash: hashFunc}
	result := args.Eq(a, b)
	h := args.Hash(v)
	_ = result
	_ = h
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules": `{
			"type_rename": "EqOps",
			"construction": {"old": "EqArgs{Eq: $eq, Hash: $hash}", "new": "NewDefaultEqOps($eq, $hash)"},
			"field_access": {
				"$.Eq($a, $b)": "$.Equal($a, $b)",
				"$.Hash($v)": "$.HashValue($v)"
			}
		}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "Migrated type") {
		t.Errorf("expected 'Migrated type' in output, got: %s", text)
	}
	if !strings.Contains(text, "EqOps") {
		t.Errorf("expected type rename in diff, got: %s", text)
	}
}

func TestMigrateTypeNotFound(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type Foo struct {
	Name string
}
`,
	})

	text, _, err := h.handleMigrateType(map[string]any{
		"type_name": "NonExistent",
		"rules":     `{"type_rename": "NewName"}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
}

func TestMigrateTypePathFilter(t *testing.T) {
	h := testHandler(t, map[string]string{
		"types.go": `package main

type EqArgs struct {
	Eq func(a, b int) bool
}
`,
		"use.go": `package main

type EqArgs2 struct {
	Eq func(a, b int) bool
}

var x EqArgs
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"type_rename": "EqOps"}`,
		"path":      "types.go",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "1 file(s)") {
		t.Errorf("expected '1 file(s)' in output (path filter should limit), got: %s", text)
	}
}

func TestMigrateTypeMultipleFiles(t *testing.T) {
	h := testHandler(t, map[string]string{
		"types.go": `package main

type EqArgs struct {
	Eq func(a, b int) bool
}
`,
		"use.go": `package main

var x EqArgs
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"type_rename": "EqOps"}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "2 file(s)") {
		t.Errorf("expected '2 file(s)' in output, got: %s", text)
	}
}

func TestMigrateTypeInvalidRules(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main
type Foo struct{}
`,
	})

	text, isErr, _ := h.handleMigrateType(map[string]any{
		"type_name": "Foo",
		"rules":     `{not valid json`,
	})
	if !isErr {
		t.Errorf("expected tool error for invalid JSON, got: %s", text)
	}

	text, isErr, _ = h.handleMigrateType(map[string]any{
		"type_name": "Foo",
		"rules":     `{}`,
	})
	if !isErr {
		t.Errorf("expected tool error for empty rules, got: %s", text)
	}
}

func TestMigrateTypeFunctionParam(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

type EqArgs struct {
	Eq func(a, b int) bool
}

func process(args EqArgs) {
	result := args.Eq(a, b)
	_ = result
}
`,
	})

	text, isErr, err := h.handleMigrateType(map[string]any{
		"type_name": "EqArgs",
		"rules":     `{"field_access": {"$.Eq($a, $b)": "$.Equal($a, $b)"}}`,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if isErr {
		t.Fatalf("tool error: %s", text)
	}

	if !strings.Contains(text, "args.Equal(a, b)") {
		t.Errorf("expected field access replacement for function param, got: %s", text)
	}
}
