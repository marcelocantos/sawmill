// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
)

// TestMigratePatternBasicRewrite verifies a simple pattern migration with
// no import changes: foo($a, $b) → bar($a, $b) across multiple call sites.
func TestMigratePatternBasicRewrite(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.py": `def m():
    foo(1, 2)
    foo(3, 4)
`,
	})

	text, isErr, _ := h.handleMigratePattern(map[string]any{
		"old_pattern": "foo($a, $b)",
		"new_pattern": "bar($a, $b)",
	})
	if isErr {
		t.Fatalf("migrate_pattern: %s", text)
	}
	if !strings.Contains(text, "2 rewrite") {
		t.Errorf("expected 2 rewrites in summary: %s", text)
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	got := readFile(t, h, "main.py")
	for _, want := range []string{"bar(1, 2)", "bar(3, 4)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in result:\n%s", want, got)
		}
	}
	if strings.Contains(got, "foo(") {
		t.Errorf("foo calls not fully rewritten:\n%s", got)
	}
}

// TestMigratePatternAddsImport verifies that add_import injects an import
// line into every rewritten file (Go: after the package declaration).
func TestMigratePatternAddsImport(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

func main() {
	foo(1, 2)
}

func foo(a, b int) int { return a + b }
`,
	})

	if _, isErr, _ := h.handleMigratePattern(map[string]any{
		"old_pattern": "foo($a, $b)",
		"new_pattern": "newpkg.Add($a, $b)",
		"add_import":  "newpkg",
	}); isErr {
		t.Fatal("migrate_pattern errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	got := readFile(t, h, "main.go")
	if !strings.Contains(got, `import "newpkg"`) {
		t.Errorf("import not added:\n%s", got)
	}
	if !strings.Contains(got, `newpkg.Add(1, 2)`) {
		t.Errorf("call site not rewritten:\n%s", got)
	}
	// Package decl still on the first line.
	if !strings.HasPrefix(got, "package main\n") {
		t.Errorf("package decl displaced:\n%s", got)
	}
}

// TestMigratePatternDropsUnusedImport verifies that drop_import removes
// the import line iff the symbol is no longer referenced.
func TestMigratePatternDropsUnusedImport(t *testing.T) {
	// Rewriting the only fmt.Sprintf call; fmt.Println still present so the
	// import should NOT be dropped.
	t.Run("import still used", func(t *testing.T) {
		h := testHandler(t, map[string]string{
			"main.go": `package main

import "fmt"

func main() {
	fmt.Println(fmt.Sprintf("hi %s", "there"))
}
`,
		})
		if _, isErr, _ := h.handleMigratePattern(map[string]any{
			"old_pattern": `fmt.Sprintf($fmt, $args)`,
			"new_pattern": `format.Format($fmt, $args)`,
			"drop_import": "fmt",
		}); isErr {
			t.Fatal("migrate errored")
		}
		if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
			t.Fatal("apply errored")
		}
		got := readFile(t, h, "main.go")
		if !strings.Contains(got, `import "fmt"`) {
			t.Errorf("fmt import dropped despite still being used:\n%s", got)
		}
		if !strings.Contains(got, `format.Format(`) {
			t.Errorf("rewrite didn't take:\n%s", got)
		}
	})

	// Now the only fmt usage is the rewritten call, so the import SHOULD drop.
	t.Run("import becomes unused", func(t *testing.T) {
		h := testHandler(t, map[string]string{
			"main.go": `package main

import "fmt"

func main() {
	x := fmt.Sprintf("hi %s", "there")
	_ = x
}
`,
		})
		if _, isErr, _ := h.handleMigratePattern(map[string]any{
			"old_pattern": `fmt.Sprintf($fmt, $args)`,
			"new_pattern": `format.Format($fmt, $args)`,
			"drop_import": "fmt",
		}); isErr {
			t.Fatal("migrate errored")
		}
		if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
			t.Fatal("apply errored")
		}
		got := readFile(t, h, "main.go")
		if strings.Contains(got, `import "fmt"`) {
			t.Errorf("fmt import not dropped despite no remaining usage:\n%s", got)
		}
		if !strings.Contains(got, `format.Format(`) {
			t.Errorf("rewrite didn't take:\n%s", got)
		}
	})
}

// TestMigratePatternAddAndDropTogether verifies that adding one import and
// dropping another both fire on the same rewrite.
func TestMigratePatternAddAndDropTogether(t *testing.T) {
	h := testHandler(t, map[string]string{
		"main.go": `package main

import "old"

func main() {
	x := old.Func(1)
	_ = x
}
`,
	})
	if _, isErr, _ := h.handleMigratePattern(map[string]any{
		"old_pattern": "old.Func($x)",
		"new_pattern": "new.Func($x)",
		"add_import":  "new",
		"drop_import": "old",
	}); isErr {
		t.Fatal("migrate errored")
	}
	if _, isErr, _ := h.handleApply(map[string]any{"confirm": true}); isErr {
		t.Fatal("apply errored")
	}
	got := readFile(t, h, "main.go")
	if !strings.Contains(got, `import "new"`) {
		t.Errorf("new import not added:\n%s", got)
	}
	if strings.Contains(got, `import "old"`) {
		t.Errorf("old import not dropped:\n%s", got)
	}
}

// TestMigratePatternNoMatch verifies the no-match return shape.
func TestMigratePatternNoMatch(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	text, isErr, _ := h.handleMigratePattern(map[string]any{
		"old_pattern": "doesnotexist($x)",
		"new_pattern": "irrelevant($x)",
	})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not-found message: %s", text)
	}
}

// TestMigratePatternRequiresDifferentPatterns verifies validation.
func TestMigratePatternRequiresDifferentPatterns(t *testing.T) {
	h := testHandler(t, map[string]string{"main.py": "def f(): pass\n"})
	_, isErr, _ := h.handleMigratePattern(map[string]any{
		"old_pattern": "foo($x)",
		"new_pattern": "foo($x)",
	})
	if !isErr {
		t.Error("expected error for identical patterns")
	}
}

// TestLastImportPathComponent verifies the helper's path-separator handling.
func TestLastImportPathComponent(t *testing.T) {
	cases := map[string]string{
		"fmt":             "fmt",
		"encoding/json":   "json",
		"std::env":        "env",
		"react":           "react",
		"foo.bar.baz":     "baz",
		"":                "",
		"nested/std::sub": "sub", // last separator wins, but `::` checked first
	}
	for in, want := range cases {
		if got := lastImportPathComponent(in); got != want {
			t.Errorf("lastImportPathComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestContainsIdentifierToken tests the word-boundary identifier search.
func TestContainsIdentifierToken(t *testing.T) {
	cases := []struct {
		source, token string
		want          bool
	}{
		{"fmt.Println(x)", "fmt", true},
		{"format.Format(x)", "fmt", false},  // substring of "format" — must not match
		{"x_fmt_y", "fmt", false},           // surrounded by underscores
		{"// fmt was here", "fmt", true},   // comment context still counts
		{"", "fmt", false},
	}
	for _, c := range cases {
		got := containsIdentifierToken([]byte(c.source), c.token)
		if got != c.want {
			t.Errorf("containsIdentifierToken(%q, %q) = %v, want %v", c.source, c.token, got, c.want)
		}
	}
}

// TestAddImportToSourceIdempotent verifies that adding the same import
// twice is a no-op the second time.
func TestAddImportToSourceIdempotent(t *testing.T) {
	src := []byte("package main\n\nfunc f() {}\n")
	adapter := &adapters.GoAdapter{}
	out, added := addImportToSource(src, adapter, "fmt")
	if !added {
		t.Fatal("first add should report added=true")
	}
	if !strings.Contains(string(out), `import "fmt"`) {
		t.Fatalf("first add should produce import line, got:\n%s", out)
	}
	out2, added2 := addImportToSource(out, adapter, "fmt")
	if added2 {
		t.Error("second add should be a no-op")
	}
	if string(out2) != string(out) {
		t.Errorf("second add should not change source:\n%s", out2)
	}
}
