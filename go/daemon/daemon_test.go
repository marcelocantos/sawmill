// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/daemon"
)

// startTestDaemon launches a Daemon on a temp socket and returns the daemon
// and the socket path. The caller must call d.Shutdown() when done.
func startTestDaemon(t *testing.T) (*daemon.Daemon, string) {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	d := daemon.New(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d.SetListener(ln)

	go d.Serve()
	return d, socketPath
}

// connectAndSend connects to the socket, sends root, and returns the response.
func connectAndSend(t *testing.T, socketPath, root string) map[string]any {
	t.Helper()

	// Retry briefly to allow the daemon goroutine to start.
	var conn net.Conn
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send JSON handshake.
	hs := map[string]string{"root": root, "binary_hash": ""}
	hsData, _ := json.Marshal(hs)
	hsData = append(hsData, '\n')
	if _, err := conn.Write(hsData); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	// Read JSON response line.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if e := scanner.Err(); e != nil {
			t.Fatalf("reading response: %v", e)
		}
		t.Fatal("connection closed before response")
	}
	line := scanner.Text()

	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	return resp
}

// writeTempGoFile creates a minimal Go source file in dir so the model has
// something to parse.
func writeTempGoFile(t *testing.T, dir string) {
	t.Helper()
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp go file: %v", err)
	}
}

// TestDaemonStartAndConnect verifies that a daemon accepts a connection, loads
// the model for the given root, and responds with status "ok".
func TestDaemonStartAndConnect(t *testing.T) {
	projectDir := t.TempDir()
	writeTempGoFile(t, projectDir)

	d, socketPath := startTestDaemon(t)
	defer d.Shutdown()

	resp := connectAndSend(t, socketPath, projectDir)

	if got := resp["status"]; got != "ok" {
		t.Errorf("status = %q, want %q (full response: %v)", got, "ok", resp)
	}
	if got, ok := resp["root"].(string); !ok || got == "" {
		t.Errorf("root missing or empty in response: %v", resp)
	}
	if _, ok := resp["files"].(float64); !ok {
		t.Errorf("files field missing or not a number: %v", resp)
	}
}

// TestDaemonMultipleProjects verifies that two connections with different roots
// both get models and that a second connection to the same root reuses the
// cached model (no error).
func TestDaemonMultipleProjects(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeTempGoFile(t, dir1)
	writeTempGoFile(t, dir2)

	d, socketPath := startTestDaemon(t)
	defer d.Shutdown()

	resp1 := connectAndSend(t, socketPath, dir1)
	resp2 := connectAndSend(t, socketPath, dir2)

	for i, resp := range []map[string]any{resp1, resp2} {
		if got := resp["status"]; got != "ok" {
			t.Errorf("project %d: status = %q, want %q", i+1, got, "ok")
		}
	}

	// Roots must be different.
	root1, _ := resp1["root"].(string)
	root2, _ := resp2["root"].(string)
	if root1 == root2 {
		t.Errorf("expected different roots, both got %q", root1)
	}

	// A second connection to dir1 should also succeed (cache hit).
	resp1b := connectAndSend(t, socketPath, dir1)
	if got := resp1b["status"]; got != "ok" {
		t.Errorf("cached project: status = %q, want %q", got, "ok")
	}
}

// TestDaemonShutdown verifies that Shutdown closes the listener and removes
// the socket file.
func TestDaemonShutdown(t *testing.T) {
	d, socketPath := startTestDaemon(t)

	// Verify socket exists.
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket file should exist before shutdown")
	}

	d.Shutdown()

	// Socket should be gone.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket file should be removed after shutdown, err=%v", err)
	}

	// Further connections should fail.
	_, err := net.Dial("unix", socketPath)
	if err == nil {
		t.Error("expected dial to fail after shutdown")
	}
}

// TestDaemonEmptyRoot verifies that sending an empty root yields an error
// status rather than panicking.
func TestDaemonEmptyRoot(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	defer d.Shutdown()

	resp := connectAndSend(t, socketPath, strings.TrimSpace("  "))
	if got := resp["status"]; got != "error" {
		t.Errorf("status = %q, want %q (full response: %v)", got, "error", resp)
	}
}
