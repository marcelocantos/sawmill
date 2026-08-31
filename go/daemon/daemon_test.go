// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon_test

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

// writeTempGoFile creates a minimal Go source file in dir so the model has
// something to parse.
func writeTempGoFile(t *testing.T, dir string) {
	t.Helper()
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp go file: %v", err)
	}
}

// freePort returns a port number that was free at the moment of the call.
// Best-effort — there's an inherent race between releasing it and binding it.
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
// its base URL plus a teardown func.
func startServer(t *testing.T) string {
	t.Helper()

	addr := freePort(t)
	srv := daemon.New("test")

	go func() {
		_ = srv.Start(addr)
	}()

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

// dialClient connects an in-process streamable HTTP MCP client to baseURL
// and completes the initialize handshake.
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
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// callTool invokes a tool and returns the textual result.
func callTool(t *testing.T, c *mcpclient.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	return sb.String(), res.IsError
}

// TestServerListAndCall verifies basic round-trip: list tools and call parse.
func TestServerListAndCall(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	baseURL := startServer(t)
	c := dialClient(t, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("expected at least one tool")
	}

	text, isErr := callTool(t, c, "parse", map[string]any{"path": projectDir})
	if isErr {
		t.Errorf("parse returned error: %s", text)
	}
}

// TestSessionsShareModel verifies that two MCP sessions calling parse on the
// same project share the same underlying CodebaseModel via the pool.
func TestSessionsShareModel(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	baseURL := startServer(t)

	c1 := dialClient(t, baseURL)
	t1, isErr := callTool(t, c1, "parse", map[string]any{"path": projectDir})
	if isErr {
		t.Fatalf("parse 1: %s", t1)
	}

	c2 := dialClient(t, baseURL)
	t2, isErr := callTool(t, c2, "parse", map[string]any{"path": projectDir})
	if isErr {
		t.Fatalf("parse 2: %s", t2)
	}

	if t1 != t2 {
		t.Errorf("expected identical parse summaries:\n  1: %s\n  2: %s", t1, t2)
	}
}

// TestSessionsIndependentRoots verifies sessions on different roots don't see
// each other's symbols.
func TestSessionsIndependentRoots(t *testing.T) {
	projectA := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectA, "a.go"), []byte("package a\n\nfunc alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectB := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectB, "b.go"), []byte("package b\n\nfunc beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseURL := startServer(t)

	cA := dialClient(t, baseURL)
	if text, isErr := callTool(t, cA, "parse", map[string]any{"path": projectA}); isErr {
		t.Fatalf("parse A: %s", text)
	}
	cB := dialClient(t, baseURL)
	if text, isErr := callTool(t, cB, "parse", map[string]any{"path": projectB}); isErr {
		t.Fatalf("parse B: %s", text)
	}

	findA, _ := callTool(t, cA, "find_symbol", map[string]any{"symbol": "alpha"})
	if !strings.Contains(findA, "alpha") {
		t.Errorf("expected alpha in project A, got: %s", findA)
	}
	findB, _ := callTool(t, cB, "find_symbol", map[string]any{"symbol": "alpha"})
	if !strings.Contains(findB, "not found") {
		t.Errorf("expected alpha not found in project B, got: %s", findB)
	}
}

// TestParseRequiresPath verifies that parse without a path errors when no
// model is loaded yet.
func TestParseRequiresPath(t *testing.T) {
	baseURL := startServer(t)
	c := dialClient(t, baseURL)

	text, isErr := callTool(t, c, "parse", map[string]any{})
	if !isErr {
		t.Errorf("expected error when parsing without a path, got: %s", text)
	}
}
