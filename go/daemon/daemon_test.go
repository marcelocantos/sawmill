// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
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

// startTestDaemon creates a project, loads a model, and starts an mcpbridge
// server on a temp socket. Returns cleanup function.
func startTestDaemon(t *testing.T, projectDir string) string {
	t.Helper()

	// Use /tmp for the socket to stay under the ~104-byte Unix socket path limit.
	h := fmt.Sprintf("%x", time.Now().UnixNano())
	socketPath := fmt.Sprintf("/tmp/sm-test-%s.sock", h[:12])
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

// TestDaemonStartAndConnect verifies that a daemon accepts a connection and
// responds to ListTools and CallTool.
func TestDaemonStartAndConnect(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	socketPath := startTestDaemon(t, projectDir)

	client, err := mcpbridge.Dial(socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	proxy := mcpbridge.NewToolProxy(client)

	// ListTools should return definitions.
	tools, err := proxy.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Error("expected at least one tool definition")
	}

	// CallTool parse should work (model already loaded).
	result, err := proxy.CallTool("parse", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool parse: %v", err)
	}
	if result.IsError {
		t.Errorf("parse returned error: %s", result.Text)
	}
}

// TestDaemonShutdown verifies that closing the server removes the socket.
func TestDaemonShutdown(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	h := fmt.Sprintf("%x", time.Now().UnixNano())
	socketPath := fmt.Sprintf("/tmp/sm-test-%s.sock", h[:12])
	os.Remove(socketPath)

	m, err := model.Load(projectDir)
	if err != nil {
		t.Fatalf("loading model: %v", err)
	}
	defer m.Close()

	handler := mcp.NewHandlerWithModel(m)

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		Handler:    handler,
	})
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}

	go srv.Serve()

	// Wait for socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close the server.
	srv.Close()

	// Give the OS a moment to clean up.
	time.Sleep(50 * time.Millisecond)

	// Connections should fail.
	_, err = mcpbridge.Dial(socketPath)
	if err == nil {
		t.Error("expected dial to fail after shutdown")
	}

	// Clean up socket file.
	os.Remove(socketPath)
}
