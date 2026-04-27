// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/adapters"
)

// fixtureCase loads base/ours/theirs from disk and asserts:
//   - if expected.<ext> is present: Result.Merged is byte-equal AND
//     Result.Conflicts is empty.
//   - if expected.<ext> is absent: Result.Conflicts is non-empty AND
//     the merged buffer contains a "<<<<<<<" marker.
type fixtureCase struct {
	dir          string
	ext          string
	wantClean    bool
	wantContains []string
}

func TestMergePythonFixtures(t *testing.T) {
	cases := []fixtureCase{
		{dir: "testdata/python/parallel_methods", ext: "py", wantClean: true},
		{dir: "testdata/python/parallel_imports", ext: "py", wantClean: true},
		{dir: "testdata/python/identical_add", ext: "py", wantClean: true},
		{dir: "testdata/python/format_vs_logic", ext: "py", wantClean: true},
		{dir: "testdata/python/delete_vs_modify", ext: "py", wantClean: false, wantContains: []string{"<<<<<<<", ">>>>>>>"}},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.dir), func(t *testing.T) {
			runFixtureCase(t, c, &adapters.PythonAdapter{})
		})
	}
}

func TestMergeGoFixtures(t *testing.T) {
	cases := []fixtureCase{
		{dir: "testdata/go/parallel_methods", ext: "go", wantClean: true},
		{dir: "testdata/go/parallel_imports", ext: "go", wantClean: true},
		{dir: "testdata/go/identical_add", ext: "go", wantClean: true},
		{dir: "testdata/go/format_vs_logic", ext: "go", wantClean: true},
		{dir: "testdata/go/parallel_fields", ext: "go", wantClean: true},
		{dir: "testdata/go/delete_vs_modify", ext: "go", wantClean: false, wantContains: []string{"<<<<<<<", ">>>>>>>"}},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.dir), func(t *testing.T) {
			runFixtureCase(t, c, &adapters.GoAdapter{})
		})
	}
}

func runFixtureCase(t *testing.T, c fixtureCase, adapter adapters.LanguageAdapter) {
	t.Helper()
	base := mustRead(t, filepath.Join(c.dir, "base."+c.ext))
	ours := mustRead(t, filepath.Join(c.dir, "ours."+c.ext))
	theirs := mustRead(t, filepath.Join(c.dir, "theirs."+c.ext))

	res, err := Merge(base, ours, theirs, adapter, Options{Path: filepath.Base(c.dir) + "." + c.ext})
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if c.wantClean {
		if len(res.Conflicts) != 0 {
			t.Fatalf("expected clean merge, got %d conflicts:\n%s", len(res.Conflicts), res.Merged)
		}
		expectedPath := filepath.Join(c.dir, "expected."+c.ext)
		if _, err := os.Stat(expectedPath); err == nil {
			expected := mustRead(t, expectedPath)
			if !bytes.Equal(res.Merged, expected) {
				t.Fatalf("merged output differs from expected.\n--- got ---\n%s\n--- want ---\n%s", res.Merged, expected)
			}
		}
		return
	}

	if len(res.Conflicts) == 0 {
		t.Fatalf("expected at least one conflict, got clean merge:\n%s", res.Merged)
	}
	for _, want := range c.wantContains {
		if !bytes.Contains(res.Merged, []byte(want)) {
			t.Fatalf("merged output missing %q:\n%s", want, res.Merged)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
