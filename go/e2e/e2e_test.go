// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package e2e tests the full production path: daemon (mcpbridge server) →
// RPC → tool handlers → file changes on disk.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
)

// startDaemon launches an mcpbridge server for the given project directory.
// Returns the socket path. Cleanup is handled via t.Cleanup.
func startDaemon(t *testing.T, projectDir string) string {
	t.Helper()

	h := fmt.Sprintf("%x", time.Now().UnixNano())
	socketPath := fmt.Sprintf("/tmp/sm-e2e-%s.sock", h[:12])
	os.Remove(socketPath)
	t.Cleanup(func() { os.Remove(socketPath) })

	m, err := model.Load(projectDir)
	if err != nil {
		t.Fatalf("loading model: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	handler := mcp.NewHandlerWithModel(m)

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		Handler:    handler,
	})
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	go srv.Serve()

	// Wait for the socket to be ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client, err := mcpbridge.Dial(socketPath)
		if err == nil {
			client.Close()
			return socketPath
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not start in time")
	return ""
}

// dialProxy connects to the daemon and returns a ToolProxy.
func dialProxy(t *testing.T, socketPath string) *mcpbridge.ToolProxy {
	t.Helper()
	client, err := mcpbridge.Dial(socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return mcpbridge.NewToolProxy(client)
}

// callTool calls a tool and returns the text result. Fails the test on error.
func callTool(t *testing.T, proxy *mcpbridge.ToolProxy, name string, args map[string]any) string {
	t.Helper()
	result, err := proxy.CallTool(name, args)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool %s returned tool error: %s", name, result.Text)
	}
	return result.Text
}

// --- Tests ------------------------------------------------------------------

// TestE2EParseQueryRenameApplyUndo exercises the full production path:
// daemon → mcpbridge RPC → tool handlers → file changes on disk.
func TestE2EParseQueryRenameApplyUndo(t *testing.T) {
	projectDir := t.TempDir()
	pyFile := filepath.Join(projectDir, "lib.py")
	original := "def foo():\n    pass\n\ndef bar():\n    foo()\n"
	os.WriteFile(pyFile, []byte(original), 0o644)

	socketPath := startDaemon(t, projectDir)
	proxy := dialProxy(t, socketPath)

	// 1. Parse (model already loaded, but parse syncs).
	parseText := callTool(t, proxy, "parse", map[string]any{})
	if !strings.Contains(parseText, "python") {
		t.Errorf("parse should mention python: %s", parseText)
	}

	// 2. Query for function "foo".
	queryText := callTool(t, proxy, "query", map[string]any{"kind": "function", "name": "foo"})
	if !strings.Contains(queryText, "foo") {
		t.Errorf("query should find foo: %s", queryText)
	}

	// 3. Rename foo → baz.
	renameText := callTool(t, proxy, "rename", map[string]any{"from": "foo", "to": "baz"})
	if !strings.Contains(renameText, "baz") {
		t.Errorf("rename diff should mention baz: %s", renameText)
	}

	// 4. Apply the pending changes.
	callTool(t, proxy, "apply", map[string]any{"confirm": true})

	// 5. Verify the file was actually changed on disk.
	content, err := os.ReadFile(pyFile)
	if err != nil {
		t.Fatalf("reading file after apply: %v", err)
	}
	if !strings.Contains(string(content), "def baz()") {
		t.Errorf("file should contain 'def baz()' after apply, got:\n%s", content)
	}
	if strings.Contains(string(content), "def foo()") {
		t.Errorf("file should NOT contain 'def foo()' after apply, got:\n%s", content)
	}

	// 6. Undo.
	undoText := callTool(t, proxy, "undo", map[string]any{})
	if !strings.Contains(strings.ToLower(undoText), "restored") {
		t.Logf("undo response: %s", undoText)
	}

	// 7. Verify the file was restored.
	content, err = os.ReadFile(pyFile)
	if err != nil {
		t.Fatalf("reading file after undo: %v", err)
	}
	if string(content) != original {
		t.Errorf("file should be restored to original after undo, got:\n%s", content)
	}
}

// TestE2ETransformApply exercises transform → apply via the daemon.
func TestE2ETransformApply(t *testing.T) {
	projectDir := t.TempDir()
	pyFile := filepath.Join(projectDir, "app.py")
	os.WriteFile(pyFile, []byte("def hello():\n    pass\n\ndef world():\n    pass\n"), 0o644)

	socketPath := startDaemon(t, projectDir)
	proxy := dialProxy(t, socketPath)

	// Parse (model already loaded).
	callTool(t, proxy, "parse", map[string]any{})

	// Transform: remove function "hello".
	transformText := callTool(t, proxy, "transform", map[string]any{
		"kind":   "function",
		"name":   "hello",
		"action": "remove",
	})
	if !strings.Contains(transformText, "hello") {
		t.Errorf("transform diff should mention hello: %s", transformText)
	}

	// Apply.
	callTool(t, proxy, "apply", map[string]any{"confirm": true})

	// Verify hello is gone, world remains.
	content, err := os.ReadFile(pyFile)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if strings.Contains(string(content), "def hello()") {
		t.Errorf("hello should be removed, got:\n%s", content)
	}
	if !strings.Contains(string(content), "def world()") {
		t.Errorf("world should remain, got:\n%s", content)
	}
}

// TestE2EImplicitParse verifies that tools work without an explicit parse call
// when the daemon has pre-loaded the model.
func TestE2EImplicitParse(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "lib.py"), []byte("def greet():\n    pass\n"), 0o644)

	socketPath := startDaemon(t, projectDir)
	proxy := dialProxy(t, socketPath)

	// Query should work immediately — daemon already loaded the model.
	queryText := callTool(t, proxy, "query", map[string]any{"kind": "function", "name": "greet"})
	if !strings.Contains(queryText, "greet") {
		t.Errorf("query should find greet without explicit parse: %s", queryText)
	}

	// parse with no path should also work — returns summary of pre-loaded model.
	parseText := callTool(t, proxy, "parse", map[string]any{})
	if !strings.Contains(parseText, "python") {
		t.Errorf("parse with no path should return pre-loaded summary: %s", parseText)
	}
}
