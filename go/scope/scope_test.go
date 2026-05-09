// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package scope_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/scope"
)

// mkdir creates dir under root, recursively. Fails the test on error.
func mkdir(t *testing.T, root, dir string) string {
	t.Helper()
	abs := filepath.Join(root, dir)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", abs, err)
	}
	return abs
}

// touch writes empty content to root/path. Returns the absolute path.
func touch(t *testing.T, root, path string) string {
	t.Helper()
	abs := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", abs, err)
	}
	return abs
}

func TestClassifyDefaults(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "src")
	mkdir(t, root, "node_modules/foo")
	mkdir(t, root, "vendor/bar")
	mkdir(t, root, "Library/Bee")
	mkdir(t, root, "build")
	mkdir(t, root, ".idea")

	c, err := scope.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		path  string
		isDir bool
		want  scope.Kind
	}{
		{"src/main.go", false, scope.Owned},
		{"src", true, scope.Owned},
		{"node_modules/foo/index.js", false, scope.Library},
		{"node_modules", true, scope.Library},
		{"vendor/bar/lib.go", false, scope.Library},
		{"Library/Bee/x.cpp", false, scope.Ignored},
		{"build/output.o", false, scope.Ignored},
		{".idea/workspace.xml", false, scope.Ignored},
	}

	for _, tc := range cases {
		got := c.Classify(filepath.Join(root, tc.path), tc.isDir)
		if got != tc.want {
			t.Errorf("Classify(%s, isDir=%v) = %s; want %s", tc.path, tc.isDir, got, tc.want)
		}
	}
}

func TestScopesYAMLOverride(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, ".sawmill")
	mkdir(t, root, "scripts")
	mkdir(t, root, "third_party/pkg")
	mkdir(t, root, "generated")

	// Override: force scripts into ignored, force generated into ignored,
	// force third_party into owned (overriding the library default).
	yaml := `
ignored:
  - "generated/**"
  - "scripts/secrets/**"
owned:
  - "third_party/**"
`
	if err := os.WriteFile(filepath.Join(root, ".sawmill/scopes.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write scopes.yaml: %v", err)
	}

	c, err := scope.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		path string
		want scope.Kind
	}{
		{"third_party/pkg/lib.go", scope.Owned},
		{"generated/foo.go", scope.Ignored},
		{"scripts/run.sh", scope.Owned},
	}
	for _, tc := range cases {
		got := c.Classify(filepath.Join(root, tc.path), false)
		if got != tc.want {
			t.Errorf("Classify(%s) = %s; want %s", tc.path, got, tc.want)
		}
	}
}

func TestGitignoreClassifiesAsIgnored(t *testing.T) {
	root := t.TempDir()

	// Initialise a git repo so the classifier consults gitignore.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build/\n*.log\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mkdir(t, root, "build")
	touch(t, root, "build/a.o")
	touch(t, root, "trace.log")
	touch(t, root, "src/main.go")

	c, err := scope.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		path  string
		isDir bool
		want  scope.Kind
	}{
		{"build/a.o", false, scope.Ignored},   // gitignored AND in default-ignored "build"
		{"trace.log", false, scope.Ignored},   // gitignored
		{"src/main.go", false, scope.Owned},   // not gitignored
		{".gitignore", false, scope.Ignored},  // hidden file
	}
	for _, tc := range cases {
		got := c.Classify(filepath.Join(root, tc.path), tc.isDir)
		if got != tc.want {
			t.Errorf("Classify(%s) = %s; want %s", tc.path, got, tc.want)
		}
	}
}

func TestLibraryBeatsGitignore(t *testing.T) {
	// node_modules is typically gitignored but should still classify as
	// library, not ignored — that's the whole point of the middle tier.
	root := t.TempDir()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	touch(t, root, "node_modules/leftpad/index.js")

	c, err := scope.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Classify(filepath.Join(root, "node_modules/leftpad/index.js"), false)
	if got != scope.Library {
		t.Errorf("gitignored node_modules entry classified as %s; want library", got)
	}
}

// TestNewDoesNotRecurseIntoTree is a regression check on the scope.New()
// gitignore loader. The first cut used go-git's gitignore.ReadPatterns,
// which recursively descends the entire tree to collect every nested
// .gitignore — that blocked Load for tens of minutes on a Unity-sized
// repo before the indexing walker even started. New() must read only
// the top-level .gitignore (and .git/info/exclude); it must complete
// quickly regardless of tree depth or width.
func TestNewDoesNotRecurseIntoTree(t *testing.T) {
	root := t.TempDir()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Build a wide+deep tree the recursive loader would choke on.
	// 10 top-level branches × 10 mid × 10 leaf = 1000 dirs, each with a
	// .gitignore the recursive loader would have parsed eagerly.
	for i := range 10 {
		for j := range 10 {
			for k := range 10 {
				dir := filepath.Join(root, fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", j), fmt.Sprintf("c%d", k))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("MkdirAll %s: %v", dir, err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp\n"), 0o644); err != nil {
					t.Fatalf("WriteFile %s/.gitignore: %v", dir, err)
				}
			}
		}
	}

	start := time.Now()
	if _, err := scope.New(root); err != nil {
		t.Fatalf("scope.New: %v", err)
	}
	elapsed := time.Since(start)

	// scope.New should be fast even with 1000+ nested .gitignore files,
	// because it must not descend into the tree. 500ms is generous; a
	// recursive implementation on this tree would run in seconds.
	if elapsed > 500*time.Millisecond {
		t.Errorf("scope.New took %v on a 1000-dir tree; recursion regression likely", elapsed)
	}
}

func TestShouldSkipDir(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "src")
	mkdir(t, root, "Library")
	mkdir(t, root, "node_modules")

	c, err := scope.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if c.ShouldSkipDir(filepath.Join(root, "Library")) != true {
		t.Errorf("ShouldSkipDir(Library) = false; want true")
	}
	if c.ShouldSkipDir(filepath.Join(root, "node_modules")) != false {
		t.Errorf("ShouldSkipDir(node_modules) = true; want false (library, not ignored)")
	}
	if c.ShouldSkipDir(filepath.Join(root, "src")) != false {
		t.Errorf("ShouldSkipDir(src) = true; want false")
	}
}
