// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package e2e tests the full production path: HTTP MCP server →
// streamable HTTP transport → tool handlers → file changes on disk.
package e2e

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/marcelocantos/sawmill/daemon"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// startServer launches a sawmill HTTP MCP server on a free port and returns
// the base URL.
func startServer(t *testing.T) string {
	t.Helper()
	addr := freePort(t)
	srv := daemon.New("test")

	go func() { _ = srv.Start(addr) }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	baseURL := "http://" + addr + "/mcp"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL)
		if err == nil {
			resp.Body.Close()
			return baseURL
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start in time")
	return ""
}

func dialClient(t *testing.T, baseURL string) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewStreamableHttpClient(baseURL)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client Start: %v", err)
	}
	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "e2e", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func callTool(t *testing.T, c *mcpclient.Client, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcpgo.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if res.IsError {
		t.Fatalf("CallTool %s tool error: %s", name, sb.String())
	}
	return sb.String()
}

// TestE2EParseQueryRenameApplyUndo exercises the full production path:
// HTTP server → MCP transport → tool handlers → file changes on disk.
func TestE2EParseQueryRenameApplyUndo(t *testing.T) {
	projectDir := t.TempDir()
	pyFile := filepath.Join(projectDir, "lib.py")
	original := "def foo():\n    pass\n\ndef bar():\n    foo()\n"
	if err := os.WriteFile(pyFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	baseURL := startServer(t)
	c := dialClient(t, baseURL)

	parseText := callTool(t, c, "parse", map[string]any{"path": projectDir})
	if !strings.Contains(parseText, "python") {
		t.Errorf("parse should mention python: %s", parseText)
	}

	queryText := callTool(t, c, "query", map[string]any{"kind": "function", "name": "foo"})
	if !strings.Contains(queryText, "foo") {
		t.Errorf("query should find foo: %s", queryText)
	}

	renameText := callTool(t, c, "rename", map[string]any{"from": "foo", "to": "baz"})
	if !strings.Contains(renameText, "baz") {
		t.Errorf("rename diff should mention baz: %s", renameText)
	}

	callTool(t, c, "apply", map[string]any{"confirm": true})

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

	undoText := callTool(t, c, "undo", map[string]any{})
	if !strings.Contains(strings.ToLower(undoText), "restored") {
		t.Logf("undo response: %s", undoText)
	}

	content, err = os.ReadFile(pyFile)
	if err != nil {
		t.Fatalf("reading file after undo: %v", err)
	}
	if string(content) != original {
		t.Errorf("file should be restored to original after undo, got:\n%s", content)
	}
}

// TestE2ETransformApply exercises transform → apply via the HTTP server.
func TestE2ETransformApply(t *testing.T) {
	projectDir := t.TempDir()
	pyFile := filepath.Join(projectDir, "app.py")
	if err := os.WriteFile(pyFile, []byte("def hello():\n    pass\n\ndef world():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseURL := startServer(t)
	c := dialClient(t, baseURL)

	callTool(t, c, "parse", map[string]any{"path": projectDir})

	transformText := callTool(t, c, "transform", map[string]any{
		"kind":   "function",
		"name":   "hello",
		"action": "remove",
	})
	if !strings.Contains(transformText, "hello") {
		t.Errorf("transform diff should mention hello: %s", transformText)
	}

	callTool(t, c, "apply", map[string]any{"confirm": true})

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
