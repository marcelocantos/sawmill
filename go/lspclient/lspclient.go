// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package lspclient provides a client for Language Server Protocol servers.
// It manages LSP server processes and provides methods for common LSP queries
// (hover, definition, references, diagnostics).
package lspclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Location represents a source location returned by LSP.
type Location struct {
	File   string `json:"file"`
	Line   uint32 `json:"line"`   // 1-based
	Column uint32 `json:"column"` // 1-based
}

// Diagnostic represents an LSP diagnostic message.
type Diagnostic struct {
	File     string `json:"file"`
	Line     uint32 `json:"line"`
	Column   uint32 `json:"column"`
	Severity string `json:"severity"` // "error", "warning", "info", "hint"
	Message  string `json:"message"`
}

// Client manages a single LSP server process for one language+root pair.
type Client struct {
	cmd     *exec.Cmd
	conn    *conn
	langID  string
	rootURI string
	mu      sync.Mutex
	opened  map[string]int // URI -> version
}

// stdioPipe combines process stdin (writer) and stdout (reader) into an
// io.ReadWriteCloser suitable for JSON-RPC framing.
type stdioPipe struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (p *stdioPipe) Close() error {
	var firstErr error
	for _, c := range p.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// NewClient launches an LSP server subprocess, initializes the connection, and
// returns a ready-to-use Client. Returns an error if the process cannot be
// started or the initialize handshake fails.
func NewClient(command []string, rootDir, langID string) (*Client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty LSP command")
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolving root dir: %w", err)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting LSP server %v: %w", command, err)
	}

	rwc := &stdioPipe{
		Reader:  stdout,
		Writer:  stdin,
		closers: []io.Closer{stdin, stdout},
	}

	c := &Client{
		cmd:     cmd,
		conn:    newConn(rwc),
		langID:  langID,
		rootURI: fileURI(absRoot),
		opened:  make(map[string]int),
	}

	if err := c.initialize(); err != nil {
		c.Close()
		return nil, fmt.Errorf("LSP initialize: %w", err)
	}

	return c, nil
}

// initialize sends the LSP initialize request and initialized notification.
func (c *Client) initialize() error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   c.rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover": map[string]any{
					"contentFormat": []string{"plaintext", "markdown"},
				},
				"definition":     map[string]any{},
				"references":     map[string]any{},
				"synchronization": map[string]any{},
			},
		},
	}

	var result json.RawMessage
	if err := c.conn.call("initialize", params, &result); err != nil {
		return err
	}

	return c.conn.notify("initialized", map[string]any{})
}

// ensureOpen sends textDocument/didOpen if not already tracked for this file.
func (c *Client) ensureOpen(file string, content []byte) error {
	uri := fileURI(file)

	if _, ok := c.opened[uri]; ok {
		// Already open — send didChange with incremented version.
		c.opened[uri]++
		return c.conn.notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": c.opened[uri],
			},
			"contentChanges": []map[string]any{
				{"text": string(content)},
			},
		})
	}

	c.opened[uri] = 1
	return c.conn.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": c.langID,
			"version":    1,
			"text":       string(content),
		},
	})
}

// Hover returns the hover information at the given position, or "" if none.
// Line and column are 1-based; they are converted to 0-based for LSP.
func (c *Client) Hover(ctx context.Context, file string, line, col uint32) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	content, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", file, err)
	}
	if err := c.ensureOpen(file, content); err != nil {
		return "", fmt.Errorf("didOpen %s: %w", file, err)
	}

	var result struct {
		Contents any `json:"contents"`
	}
	err = c.conn.call("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(file)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}, &result)
	if err != nil {
		return "", err
	}

	return extractHoverContent(result.Contents), nil
}

// Definition returns the definition locations for the symbol at the given position.
func (c *Client) Definition(ctx context.Context, file string, line, col uint32) ([]Location, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := c.ensureOpen(file, content); err != nil {
		return nil, fmt.Errorf("didOpen %s: %w", file, err)
	}

	var raw json.RawMessage
	err = c.conn.call("textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(file)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}, &raw)
	if err != nil {
		return nil, err
	}

	return parseLSPLocations(raw), nil
}

// References returns all reference locations for the symbol at the given position.
func (c *Client) References(ctx context.Context, file string, line, col uint32) ([]Location, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := c.ensureOpen(file, content); err != nil {
		return nil, fmt.Errorf("didOpen %s: %w", file, err)
	}

	var raw json.RawMessage
	err = c.conn.call("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(file)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
		"context":      map[string]any{"includeDeclaration": true},
	}, &raw)
	if err != nil {
		return nil, err
	}

	return parseLSPLocations(raw), nil
}

// Diagnostics collects diagnostics for the given file. It opens/updates the
// file, waits briefly for publishDiagnostics notifications, and returns them.
func (c *Client) Diagnostics(ctx context.Context, file string) ([]Diagnostic, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := c.ensureOpen(file, content); err != nil {
		return nil, fmt.Errorf("didOpen %s: %w", file, err)
	}

	uri := fileURI(file)

	// Drain notification channel for a short time, collecting diagnostics.
	var diags []Diagnostic
	timeout := time.After(2 * time.Second)
	for {
		select {
		case notif := <-c.conn.notifications:
			if notif.Method == "textDocument/publishDiagnostics" {
				d := parseDiagnosticsNotification(notif.Params, uri, file)
				diags = append(diags, d...)
			}
		case <-timeout:
			return diags, nil
		case <-ctx.Done():
			return diags, ctx.Err()
		}
	}
}

// Close sends shutdown + exit and kills the process after a timeout.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Best-effort shutdown handshake.
	_ = c.conn.call("shutdown", nil, nil)
	_ = c.conn.notify("exit", nil)
	_ = c.conn.close()

	// Give the process 2s to exit gracefully.
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	return nil
}

// fileURI converts an absolute file path to a file:// URI.
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + url.PathEscape(abs)
}

// uriToFile converts a file:// URI back to a file path.
func uriToFile(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		path := strings.TrimPrefix(uri, "file://")
		decoded, err := url.PathUnescape(path)
		if err == nil {
			return decoded
		}
		return path
	}
	return uri
}

// extractHoverContent extracts text from LSP hover response contents, which
// can be a string, {kind, value} object, or an array of such.
func extractHoverContent(contents any) string {
	if contents == nil {
		return ""
	}
	switch v := contents.(type) {
	case string:
		return v
	case map[string]any:
		if val, ok := v["value"].(string); ok {
			return val
		}
	case []any:
		var parts []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			} else if m, ok := item.(map[string]any); ok {
				if val, ok := m["value"].(string); ok {
					parts = append(parts, val)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return fmt.Sprintf("%v", contents)
}

// lspLocation is the wire format for LSP Location.
type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct {
			Line      uint32 `json:"line"`
			Character uint32 `json:"character"`
		} `json:"start"`
	} `json:"range"`
}

// parseLSPLocations parses an LSP response that may be a single Location,
// an array of Locations, or null.
func parseLSPLocations(raw json.RawMessage) []Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	// Try as array first.
	var arr []lspLocation
	if err := json.Unmarshal(raw, &arr); err == nil {
		var locs []Location
		for _, l := range arr {
			locs = append(locs, Location{
				File:   uriToFile(l.URI),
				Line:   l.Range.Start.Line + 1,
				Column: l.Range.Start.Character + 1,
			})
		}
		return locs
	}

	// Try as single location.
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []Location{{
			File:   uriToFile(single.URI),
			Line:   single.Range.Start.Line + 1,
			Column: single.Range.Start.Character + 1,
		}}
	}

	return nil
}

// parseDiagnosticsNotification extracts diagnostics from a publishDiagnostics
// notification params.
func parseDiagnosticsNotification(params any, targetURI, filePath string) []Diagnostic {
	data, err := json.Marshal(params)
	if err != nil {
		return nil
	}

	var p struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start struct {
					Line      uint32 `json:"line"`
					Character uint32 `json:"character"`
				} `json:"start"`
			} `json:"range"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}

	// Only return diagnostics for the requested file.
	if p.URI != targetURI {
		return nil
	}

	severityNames := map[int]string{
		1: "error",
		2: "warning",
		3: "info",
		4: "hint",
	}

	var diags []Diagnostic
	for _, d := range p.Diagnostics {
		sev := severityNames[d.Severity]
		if sev == "" {
			sev = "info"
		}
		diags = append(diags, Diagnostic{
			File:     filePath,
			Line:     d.Range.Start.Line + 1,
			Column:   d.Range.Start.Character + 1,
			Severity: sev,
			Message:  d.Message,
		})
	}
	return diags
}
