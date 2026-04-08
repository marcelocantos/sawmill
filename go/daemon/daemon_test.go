// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/daemon"
	"github.com/marcelocantos/sawmill/mcp"
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

// tempSocket returns a short-lived socket path under /tmp (stays under the
// ~104-byte Unix socket path limit).
func tempSocket(t *testing.T) string {
	t.Helper()
	h := fmt.Sprintf("%x", time.Now().UnixNano())
	socketPath := fmt.Sprintf("/tmp/sm-test-%s.sock", h[:12])
	os.Remove(socketPath)
	t.Cleanup(func() { os.Remove(socketPath) })
	return socketPath
}

// startFactoryDaemon starts a daemon using HandlerFactory with a ref-counted
// pool. Returns the socket path and pool.
func startFactoryDaemon(t *testing.T) (string, *daemon.ModelPool) {
	t.Helper()

	socketPath := tempSocket(t)
	pool := daemon.NewModelPool()
	t.Cleanup(func() { pool.CloseAll() })

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		HandlerFactory: func(root string) (mcpbridge.ToolHandler, func()) {
			if root == "" {
				return mcp.NewHandler(), nil
			}
			m, loadErr := pool.Get(root)
			if loadErr != nil {
				return mcp.NewHandler(), nil
			}
			return mcp.NewHandlerWithModel(m), func() { pool.Release(root) }
		},
	})
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	go srv.Serve()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client, err := mcpbridge.Dial(socketPath)
		if err == nil {
			client.Close()
			return socketPath, pool
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not start in time")
	return "", nil
}

// TestDaemonStartAndConnect verifies that the global daemon accepts a
// connection with a project root, lazily loads the model, and responds
// to ListTools and CallTool.
func TestDaemonStartAndConnect(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	socketPath, _ := startFactoryDaemon(t)

	client, err := mcpbridge.Dial(socketPath, projectDir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	proxy := mcpbridge.NewToolProxy(client)

	tools, err := proxy.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Error("expected at least one tool definition")
	}

	result, err := proxy.CallTool("parse", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool parse: %v", err)
	}
	if result.IsError {
		t.Errorf("parse returned error: %s", result.Text)
	}
}

// TestDaemonMultipleRootsShareModel verifies that two connections to the same
// root share a model (amortised parsing).
func TestDaemonMultipleRootsShareModel(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	socketPath, _ := startFactoryDaemon(t)

	c1, err := mcpbridge.Dial(socketPath, projectDir)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()

	p1 := mcpbridge.NewToolProxy(c1)
	result, err := p1.CallTool("parse", map[string]any{})
	if err != nil {
		t.Fatalf("parse 1: %v", err)
	}
	if result.IsError {
		t.Fatalf("parse 1 error: %s", result.Text)
	}

	c2, err := mcpbridge.Dial(socketPath, projectDir)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()

	p2 := mcpbridge.NewToolProxy(c2)
	result2, err := p2.CallTool("parse", map[string]any{})
	if err != nil {
		t.Fatalf("parse 2: %v", err)
	}
	if result2.IsError {
		t.Fatalf("parse 2 error: %s", result2.Text)
	}

	if result.Text != result2.Text {
		t.Errorf("expected same parse result, got:\n  1: %s\n  2: %s", result.Text, result2.Text)
	}
}

// TestDaemonMultipleRoots verifies that connections to different project roots
// get independent models.
func TestDaemonMultipleRoots(t *testing.T) {
	projectA := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectA, "a.go"),
		[]byte("package a\n\nfunc alpha() {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	projectB := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectB, "b.go"),
		[]byte("package b\n\nfunc beta() {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	socketPath, _ := startFactoryDaemon(t)

	cA, err := mcpbridge.Dial(socketPath, projectA)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer cA.Close()
	pA := mcpbridge.NewToolProxy(cA)

	resultA, err := pA.CallTool("parse", map[string]any{})
	if err != nil || resultA.IsError {
		t.Fatalf("parse A: err=%v text=%s", err, resultA.Text)
	}

	cB, err := mcpbridge.Dial(socketPath, projectB)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer cB.Close()
	pB := mcpbridge.NewToolProxy(cB)

	resultB, err := pB.CallTool("parse", map[string]any{})
	if err != nil || resultB.IsError {
		t.Fatalf("parse B: err=%v text=%s", err, resultB.Text)
	}

	findA, err := pA.CallTool("find_symbol", map[string]any{"symbol": "alpha"})
	if err != nil {
		t.Fatalf("find_symbol A: %v", err)
	}
	if findA.IsError || !strings.Contains(findA.Text, "alpha") {
		t.Errorf("expected alpha in project A, got: %s", findA.Text)
	}

	findB, err := pB.CallTool("find_symbol", map[string]any{"symbol": "alpha"})
	if err != nil {
		t.Fatalf("find_symbol B: %v", err)
	}
	if !strings.Contains(findB.Text, "not found") {
		t.Errorf("expected alpha not found in project B, got: %s", findB.Text)
	}

	findBeta, err := pB.CallTool("find_symbol", map[string]any{"symbol": "beta"})
	if err != nil {
		t.Fatalf("find_symbol B beta: %v", err)
	}
	if findBeta.IsError || !strings.Contains(findBeta.Text, "beta") {
		t.Errorf("expected beta in project B, got: %s", findBeta.Text)
	}

	findBetaA, err := pA.CallTool("find_symbol", map[string]any{"symbol": "beta"})
	if err != nil {
		t.Fatalf("find_symbol A beta: %v", err)
	}
	if !strings.Contains(findBetaA.Text, "not found") {
		t.Errorf("expected beta not found in project A, got: %s", findBetaA.Text)
	}
}

// TestDaemonShutdown verifies that closing the server stops accepting connections.
func TestDaemonShutdown(t *testing.T) {
	socketPath := tempSocket(t)

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		HandlerFactory: func(root string) (mcpbridge.ToolHandler, func()) {
			return mcp.NewHandler(), nil
		},
	})
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}

	go srv.Serve()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	srv.Close()
	time.Sleep(50 * time.Millisecond)

	_, err = mcpbridge.Dial(socketPath)
	if err == nil {
		t.Error("expected dial to fail after shutdown")
	}
}

// TestDaemonNoRoot verifies that connecting without a root still works —
// the handler starts with no model and requires parse with an explicit path.
func TestDaemonNoRoot(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	socketPath, _ := startFactoryDaemon(t)

	client, err := mcpbridge.Dial(socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	proxy := mcpbridge.NewToolProxy(client)

	result, err := proxy.CallTool("parse", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when parsing without root or path")
	}

	result, err = proxy.CallTool("parse", map[string]any{"path": projectDir})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Errorf("parse with path returned error: %s", result.Text)
	}
}
