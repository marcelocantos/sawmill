// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package lspclient

import (
	"encoding/json"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
)

// TestPoolNilCommand verifies that Pool.Get returns nil when the adapter's
// LSPCommand() returns nil.
func TestPoolNilCommand(t *testing.T) {
	pool := NewPool()
	defer pool.Close()

	// baseAdapter returns nil for LSPCommand.
	adapter := &adapters.CppAdapter{}
	// CppAdapter returns ["clangd"], so use a custom nil-command test.
	// Instead, test with a mock that returns nil.
	_ = adapter

	// Directly test: an adapter with a non-existent binary should return nil.
	// We rely on the binary not being at a fake path.
	pool2 := NewPool()
	defer pool2.Close()

	c := pool2.Get(&nilLSPAdapter{}, "/tmp")
	if c != nil {
		c.Close()
		t.Fatal("expected nil client for adapter with nil LSPCommand")
	}
}

// TestPoolNonExistentBinary verifies that Pool.Get returns nil when the LSP
// binary does not exist on PATH.
func TestPoolNonExistentBinary(t *testing.T) {
	pool := NewPool()
	defer pool.Close()

	c := pool.Get(&fakeLSPAdapter{cmd: []string{"nonexistent-lsp-binary-xyz"}}, "/tmp")
	if c != nil {
		c.Close()
		t.Fatal("expected nil client for non-existent binary")
	}
}

// TestParseLSPLocationsNull verifies that null JSON returns nil locations.
func TestParseLSPLocationsNull(t *testing.T) {
	locs := parseLSPLocations(json.RawMessage("null"))
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations, got %d", len(locs))
	}
}

// TestParseLSPLocationsSingle verifies parsing a single LSP Location.
func TestParseLSPLocationsSingle(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///foo.go","range":{"start":{"line":9,"character":4}}}`)
	locs := parseLSPLocations(raw)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].File != "/foo.go" {
		t.Errorf("expected file /foo.go, got %s", locs[0].File)
	}
	if locs[0].Line != 10 || locs[0].Column != 5 {
		t.Errorf("expected 10:5, got %d:%d", locs[0].Line, locs[0].Column)
	}
}

// TestParseLSPLocationsArray verifies parsing an array of LSP Locations.
func TestParseLSPLocationsArray(t *testing.T) {
	raw := json.RawMessage(`[
		{"uri":"file:///a.go","range":{"start":{"line":0,"character":0}}},
		{"uri":"file:///b.go","range":{"start":{"line":5,"character":10}}}
	]`)
	locs := parseLSPLocations(raw)
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}
	if locs[1].Line != 6 || locs[1].Column != 11 {
		t.Errorf("expected 6:11, got %d:%d", locs[1].Line, locs[1].Column)
	}
}

// TestExtractHoverContent verifies hover content extraction from various formats.
func TestExtractHoverContent(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		expect string
	}{
		{"nil", nil, ""},
		{"string", "func main()", "func main()"},
		{"markup", map[string]any{"kind": "markdown", "value": "```go\nfunc main()\n```"}, "```go\nfunc main()\n```"},
		{"array", []any{"one", map[string]any{"value": "two"}}, "one\ntwo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHoverContent(tt.input)
			if got != tt.expect {
				t.Errorf("expected %q, got %q", tt.expect, got)
			}
		})
	}
}

// TestFileURIRoundTrip verifies fileURI and uriToFile are inverses.
func TestFileURIRoundTrip(t *testing.T) {
	path := "/tmp/test/foo.go"
	uri := fileURI(path)
	back := uriToFile(uri)
	if back != path {
		t.Errorf("round trip failed: %q -> %q -> %q", path, uri, back)
	}
}

// TestPoolClose verifies that closing an empty pool doesn't panic.
func TestPoolClose(t *testing.T) {
	pool := NewPool()
	pool.Close() // Should not panic.
}

// -- test helpers --

// nilLSPAdapter is a test adapter that returns nil for LSPCommand.
type nilLSPAdapter struct{ adapters.CppAdapter }

func (a *nilLSPAdapter) LSPCommand() []string    { return nil }
func (a *nilLSPAdapter) LSPLanguageID() string    { return "test" }
func (a *nilLSPAdapter) Extensions() []string     { return []string{"test"} }

// fakeLSPAdapter is a test adapter that returns a custom LSP command.
type fakeLSPAdapter struct {
	adapters.CppAdapter
	cmd []string
}

func (a *fakeLSPAdapter) LSPCommand() []string { return a.cmd }
func (a *fakeLSPAdapter) LSPLanguageID() string { return "fake" }
