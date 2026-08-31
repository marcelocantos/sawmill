// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit initialises a git repo in dir with a default identity and an
// initial empty commit so subsequent commits have a parent.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "master")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "init")
}

// gitCommit writes content to dir/path, stages it, and commits with msg.
// Returns the commit SHA.
func gitCommit(t *testing.T, dir, path, content, msg string) string {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("add", path)
	run("commit", "-m", msg)
	sha := run("rev-parse", "HEAD")
	return sha[:len(sha)-1] // strip newline
}

// TestGitBlameSymbolSignatureVsBody verifies that body and signature
// modifications are attributed to different commits.
func TestGitBlameSymbolSignatureVsBody(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	// v1: introduce Foo.
	c1 := gitCommit(t, dir, "lib.go", `package lib

func Foo(x int) int {
	return x
}
`, "introduce Foo")

	// v2: change body only — signature unchanged.
	gitCommit(t, dir, "lib.go", `package lib

func Foo(x int) int {
	return x * 2
}
`, "tweak Foo body")

	// v3: change signature — adds parameter.
	c3 := gitCommit(t, dir, "lib.go", `package lib

func Foo(x int, y int) int {
	return x * 2
}
`, "add y parameter to Foo")

	// v4: tweak body again with the new signature in place.
	c4 := gitCommit(t, dir, "lib.go", `package lib

func Foo(x int, y int) int {
	return (x + y) * 2
}
`, "use y in Foo body")

	// Parse and run blame.
	h := NewHandler()
	if text, isErr, err := h.handleParse(map[string]any{"path": dir}); err != nil {
		t.Fatalf("parse error: %v", err)
	} else if isErr {
		t.Fatalf("parse returned error: %s", text)
	}

	text, isErr, err := h.handleGitBlameSymbol(map[string]any{
		"path":   "lib.go",
		"symbol": "Foo",
	})
	if err != nil {
		t.Fatalf("git_blame_symbol: %v", err)
	}
	if isErr {
		t.Fatalf("blame returned error: %s", text)
	}

	var result blameResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshalling result: %v\nraw:\n%s", err, text)
	}

	if result.Introduced == nil || result.Introduced.SHA != c1 {
		t.Errorf("introduced = %+v, want SHA %s", result.Introduced, c1[:7])
	}
	// LastModified is the most recent commit where the declaration text
	// differed from the newest version. Newest decl is from c4. c3 has a
	// different body (no `(x+y)*2`), so the change to current decl was at c4.
	if result.LastModified == nil || result.LastModified.SHA != c4 {
		t.Errorf("last_modified = %+v, want SHA %s", result.LastModified, c4[:7])
	}
	// BodyLastModified: newest body is at c4; previous distinct body at c3
	// (still `return x * 2` with same body text? Actually c3 keeps `return x * 2`)
	// — c4 was where the body changed from `return x * 2` to `return (x+y)*2`.
	if result.BodyLastModified == nil || result.BodyLastModified.SHA != c4 {
		t.Errorf("body_last_modified = %+v, want SHA %s", result.BodyLastModified, c4[:7])
	}
	// SignatureLastChanged: newest sig is `(x int, y int)` (introduced at c3).
	// c2 still has `(x int)` — so the signature flipped at c3.
	if result.SignatureLastChanged == nil || result.SignatureLastChanged.SHA != c3 {
		t.Errorf("signature_last_changed = %+v, want SHA %s", result.SignatureLastChanged, c3[:7])
	}

	// Sanity: body and signature should have flipped at *different* commits.
	if result.BodyLastModified != nil && result.SignatureLastChanged != nil &&
		result.BodyLastModified.SHA == result.SignatureLastChanged.SHA {
		t.Errorf("body and signature should be attributed to different commits, both = %s",
			result.BodyLastModified.SHA[:7])
	}
}

// TestGitBlameSymbolUnchanged verifies that a never-modified function reports
// introduction as the answer for all three "last X" fields.
func TestGitBlameSymbolUnchanged(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	c1 := gitCommit(t, dir, "lib.go", `package lib

func Foo() int { return 1 }
`, "introduce Foo")
	gitCommit(t, dir, "other.go", "package lib\n\nfunc Other() {}\n", "add Other")
	gitCommit(t, dir, "another.go", "package lib\n\nfunc Another() {}\n", "add Another")

	h := NewHandler()
	if text, isErr, err := h.handleParse(map[string]any{"path": dir}); err != nil || isErr {
		t.Fatalf("parse: err=%v text=%s", err, text)
	}

	text, isErr, err := h.handleGitBlameSymbol(map[string]any{"path": "lib.go", "symbol": "Foo"})
	if err != nil || isErr {
		t.Fatalf("blame: err=%v text=%s", err, text)
	}
	var result blameResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if result.Introduced == nil || result.Introduced.SHA != c1 {
		t.Errorf("introduced = %+v, want %s", result.Introduced, c1[:7])
	}
	if result.LastModified == nil || result.LastModified.SHA != c1 {
		t.Errorf("last_modified = %+v, want introduction (%s)", result.LastModified, c1[:7])
	}
	if result.BodyLastModified == nil || result.BodyLastModified.SHA != c1 {
		t.Errorf("body_last_modified = %+v, want introduction", result.BodyLastModified)
	}
	if result.SignatureLastChanged == nil || result.SignatureLastChanged.SHA != c1 {
		t.Errorf("signature_last_changed = %+v, want introduction", result.SignatureLastChanged)
	}
}

// TestGitBlameSymbolType verifies that types don't get spurious body/signature
// attribution (those fields are function-only).
func TestGitBlameSymbolType(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	gitCommit(t, dir, "config.go", `package cfg

type Config struct {
	Name string
}
`, "add Config")

	h := NewHandler()
	if text, isErr, err := h.handleParse(map[string]any{"path": dir}); err != nil || isErr {
		t.Fatalf("parse: err=%v text=%s", err, text)
	}

	text, isErr, err := h.handleGitBlameSymbol(map[string]any{"path": "config.go", "symbol": "Config"})
	if err != nil || isErr {
		t.Fatalf("blame: err=%v text=%s", err, text)
	}
	var result blameResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if result.Introduced == nil {
		t.Error("expected introduced to be set")
	}
	if result.LastModified == nil {
		t.Error("expected last_modified to be set")
	}
	// For non-functions, body/signature fields are intentionally omitted.
	if result.BodyLastModified != nil {
		t.Errorf("body_last_modified should be nil for types, got %+v", result.BodyLastModified)
	}
	if result.SignatureLastChanged != nil {
		t.Errorf("signature_last_changed should be nil for types, got %+v", result.SignatureLastChanged)
	}
}
