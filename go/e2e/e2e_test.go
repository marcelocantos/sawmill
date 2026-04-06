// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package e2e tests the full production path: daemon (socket) → MCP JSON-RPC →
// tool handlers → file changes on disk.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/daemon"
)

// --- JSON-RPC types --------------------------------------------------------

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// --- Test helpers -----------------------------------------------------------

// startDaemon launches a daemon on a temp socket and returns the daemon, socket
// path, and a cleanup function.
func startDaemon(t *testing.T) (*daemon.Daemon, string) {
	t.Helper()
	// Use /tmp for the socket to stay under the ~104-byte Unix socket path limit.
	// Include PID and a hash of the test name for parallel safety.
	h := fmt.Sprintf("%x", time.Now().UnixNano())
	socketPath := fmt.Sprintf("/tmp/sm-%s.sock", h[:12])
	os.Remove(socketPath) // clean up stale socket
	t.Cleanup(func() { os.Remove(socketPath) })

	d := daemon.New(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d.SetListener(ln)
	go d.Serve()
	t.Cleanup(d.Shutdown)
	return d, socketPath
}

// mcpConn connects to the daemon, performs the root handshake, and returns a
// reader/writer pair ready for MCP JSON-RPC.
type mcpConn struct {
	conn   net.Conn
	reader *bufio.Reader
	t      *testing.T
	nextID int
}

func dialMCP(t *testing.T, socketPath, projectRoot string) *mcpConn {
	t.Helper()

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
	t.Cleanup(func() { conn.Close() })

	// Handshake: send JSON with project root and binary hash.
	hs := map[string]string{"root": projectRoot, "binary_hash": ""}
	json.NewEncoder(conn).Encode(hs)

	reader := bufio.NewReader(conn)

	// Read status line.
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusLine), &status); err != nil {
		t.Fatalf("parsing status %q: %v", statusLine, err)
	}
	if status["status"] != "ok" {
		t.Fatalf("daemon rejected root: %v", status)
	}

	return &mcpConn{conn: conn, reader: reader, t: t, nextID: 1}
}

// call sends a JSON-RPC request and reads the response.
func (c *mcpConn) call(method string, params any) jsonrpcResponse {
	c.t.Helper()
	id := c.nextID
	c.nextID++

	req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		c.t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		c.t.Fatalf("write request: %v", err)
	}

	// Read lines until we get a response with our ID. The MCP server may send
	// notifications (no id) which we skip.
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				c.t.Fatalf("EOF reading response for %s (id=%d)", method, id)
			}
			c.t.Fatalf("reading response for %s: %v", method, err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Might be a notification — skip.
			continue
		}
		if resp.ID == id {
			return resp
		}
		// Not our response — keep reading.
	}
}

// resultText extracts the text content from an MCP tool call result.
func (c *mcpConn) resultText(resp jsonrpcResponse) string {
	c.t.Helper()
	if resp.Error != nil {
		c.t.Fatalf("unexpected error: %s", resp.Error)
	}
	// MCP result is {content: [{type: "text", text: "..."}]}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		c.t.Fatalf("parsing result: %v (raw: %s)", err, resp.Result)
	}
	if len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].Text
}

// --- Tests ------------------------------------------------------------------

// TestE2EParseQueryRenameApplyUndo exercises the full production path:
// daemon socket → MCP JSON-RPC → tool handlers → file changes on disk.
func TestE2EParseQueryRenameApplyUndo(t *testing.T) {
	// Set up a project directory with a Python file.
	projectDir := t.TempDir()
	pyFile := filepath.Join(projectDir, "lib.py")
	original := "def foo():\n    pass\n\ndef bar():\n    foo()\n"
	os.WriteFile(pyFile, []byte(original), 0o644)

	_, socketPath := startDaemon(t)
	c := dialMCP(t, socketPath, projectDir)

	// 1. Initialize MCP session.
	initResp := c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %s", initResp.Error)
	}

	// 2. Parse the project (the daemon already loaded the model, but parse
	//    ensures the MCP server's model field is set from the pre-loaded model).
	parseResp := c.call("tools/call", map[string]any{
		"name":      "parse",
		"arguments": map[string]any{"path": projectDir},
	})
	parseText := c.resultText(parseResp)
	if !strings.Contains(parseText, "python") {
		t.Errorf("parse should mention python: %s", parseText)
	}

	// 3. Query for function "foo".
	queryResp := c.call("tools/call", map[string]any{
		"name":      "query",
		"arguments": map[string]any{"kind": "function", "name": "foo"},
	})
	queryText := c.resultText(queryResp)
	if !strings.Contains(queryText, "foo") {
		t.Errorf("query should find foo: %s", queryText)
	}

	// 4. Rename foo → baz.
	renameResp := c.call("tools/call", map[string]any{
		"name":      "rename",
		"arguments": map[string]any{"from": "foo", "to": "baz"},
	})
	renameText := c.resultText(renameResp)
	if !strings.Contains(renameText, "baz") {
		t.Errorf("rename diff should mention baz: %s", renameText)
	}

	// 5. Apply the pending changes.
	applyResp := c.call("tools/call", map[string]any{
		"name":      "apply",
		"arguments": map[string]any{"confirm": true},
	})
	applyText := c.resultText(applyResp)
	if !strings.Contains(strings.ToLower(applyText), "applied") && !strings.Contains(strings.ToLower(applyText), "written") {
		t.Logf("apply response: %s", applyText)
	}

	// 6. Verify the file was actually changed on disk.
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

	// 7. Undo.
	undoResp := c.call("tools/call", map[string]any{
		"name":      "undo",
		"arguments": map[string]any{},
	})
	undoText := c.resultText(undoResp)
	if !strings.Contains(strings.ToLower(undoText), "restored") && !strings.Contains(strings.ToLower(undoText), "undo") {
		t.Logf("undo response: %s", undoText)
	}

	// 8. Verify the file was restored.
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

	_, socketPath := startDaemon(t)
	c := dialMCP(t, socketPath, projectDir)

	// Initialize.
	c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	})

	// Parse.
	c.call("tools/call", map[string]any{
		"name":      "parse",
		"arguments": map[string]any{"path": projectDir},
	})

	// Transform: remove function "hello".
	transformResp := c.call("tools/call", map[string]any{
		"name": "transform",
		"arguments": map[string]any{
			"kind":   "function",
			"name":   "hello",
			"action": "remove",
		},
	})
	transformText := c.resultText(transformResp)
	if !strings.Contains(transformText, "hello") {
		t.Errorf("transform diff should mention hello: %s", transformText)
	}

	// Apply.
	c.call("tools/call", map[string]any{
		"name":      "apply",
		"arguments": map[string]any{"confirm": true},
	})

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

// TestE2EMultipleSessionsSameProject verifies two concurrent MCP sessions
// against the same project root share the daemon's cached model.
func TestE2EMultipleSessionsSameProject(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "main.py"), []byte("x = 1\n"), 0o644)

	_, socketPath := startDaemon(t)

	// Two independent connections to the same project.
	c1 := dialMCP(t, socketPath, projectDir)
	c2 := dialMCP(t, socketPath, projectDir)

	// Both initialize.
	c1.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "session-1", "version": "1.0"},
	})
	c2.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "session-2", "version": "1.0"},
	})

	// Both parse the same project.
	r1 := c1.call("tools/call", map[string]any{
		"name":      "parse",
		"arguments": map[string]any{"path": projectDir},
	})
	r2 := c2.call("tools/call", map[string]any{
		"name":      "parse",
		"arguments": map[string]any{"path": projectDir},
	})

	// Both should succeed.
	t1 := c1.resultText(r1)
	t2 := c2.resultText(r2)
	if !strings.Contains(t1, "python") {
		t.Errorf("session 1 parse failed: %s", t1)
	}
	if !strings.Contains(t2, "python") {
		t.Errorf("session 2 parse failed: %s", t2)
	}
}

// TestE2EImplicitParse verifies that tools work without an explicit parse call
// when the daemon has pre-loaded the model via the handshake.
func TestE2EImplicitParse(t *testing.T) {
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "lib.py"), []byte("def greet():\n    pass\n"), 0o644)

	_, socketPath := startDaemon(t)
	c := dialMCP(t, socketPath, projectDir)

	// Initialize — but do NOT call parse.
	c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1.0"},
	})

	// Query should work immediately — daemon already loaded the model.
	queryResp := c.call("tools/call", map[string]any{
		"name":      "query",
		"arguments": map[string]any{"kind": "function", "name": "greet"},
	})
	queryText := c.resultText(queryResp)
	if !strings.Contains(queryText, "greet") {
		t.Errorf("query should find greet without explicit parse: %s", queryText)
	}

	// parse with no path should also work — returns summary of pre-loaded model.
	parseResp := c.call("tools/call", map[string]any{
		"name":      "parse",
		"arguments": map[string]any{},
	})
	parseText := c.resultText(parseResp)
	if !strings.Contains(parseText, "python") {
		t.Errorf("parse with no path should return pre-loaded summary: %s", parseText)
	}
}
