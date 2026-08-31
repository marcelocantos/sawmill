// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package watcher_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/fdusage"
	"github.com/marcelocantos/sawmill/watcher"
)

// buildTree writes owned source files at the root and libFiles files under
// node_modules, and returns the root.
func buildTree(t *testing.T, owned, libFiles int) string {
	t.Helper()
	root := t.TempDir()
	for i := range owned {
		p := filepath.Join(root, fmt.Sprintf("owned%d.go", i))
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	lib := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for i := range libFiles {
		p := filepath.Join(lib, fmt.Sprintf("dep%d.js", i))
		if err := os.WriteFile(p, []byte("module.exports = 1\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

// watchCost reports how many descriptors a watcher on this tree costs.
func watchCost(t *testing.T, root string) int {
	t.Helper()
	before, err := fdusage.Count()
	if err != nil {
		t.Skipf("descriptor counting unavailable: %v", err)
	}
	w, _, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	during, err := fdusage.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	w.Close()
	return during - before
}

// TestWatchCostIsIndependentOfDependencyTreeSize is the descriptor-budget
// oracle.
//
// On macOS the kqueue backend opens one descriptor per watched file, so
// before library directories were excluded, watch cost scaled with total file
// count — and dependency trees are where the files are. One project model was
// measured holding 25,620 descriptors, so an ordinary working set reached the
// per-process ceiling on its own.
//
// Tenfold more dependency files must not cost meaningfully more descriptors.
// The assertion is on the shape of the curve rather than an absolute number,
// which keeps it honest across machines and Go versions.
func TestWatchCostIsIndependentOfDependencyTreeSize(t *testing.T) {
	small := watchCost(t, buildTree(t, 5, 50))
	large := watchCost(t, buildTree(t, 5, 500))

	t.Logf("watch cost: %d descriptors with 50 dependency files, %d with 500", small, large)

	// A tenfold increase in dependency files would previously have cost
	// roughly tenfold more descriptors. Allow generous slack for runtime
	// noise while still failing loudly if the scaling ever returns.
	if large > small+50 {
		t.Errorf("watch cost grew from %d to %d when dependency files went 50 -> 500; "+
			"library directories appear to be watched again", small, large)
	}
}

// TestWatchCostStaysWithinBudget checks the reading the daemon logs against
// the same limit that the wedged daemon exceeded.
func TestWatchCostStaysWithinBudget(t *testing.T) {
	root := buildTree(t, 20, 500)

	w, _, err := watcher.Watch(root, newTestClassifier(t, root))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	u, err := fdusage.Read()
	if err != nil {
		t.Skipf("descriptor reading unavailable: %v", err)
	}
	t.Logf("descriptor usage while watching: %s", u)

	if u.Limit == 0 {
		t.Fatal("Limit() = 0; the budget check would be vacuous")
	}
	if u.OverBudget() {
		t.Errorf("usage %s is over the %.0f%% budget for a single small tree", u, fdusage.Budget*100)
	}
}
