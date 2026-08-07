// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

// pathologicalBash builds a bash source that the GLR parser is superlinear on.
// Runs of bare-word command lines are the cheapest reliable trigger: 40 of them
// (~1.5 KB) take over eight seconds, against microseconds for ordinary script
// syntax of the same size.
//
// It is only good for the short-budget tests. The cost plateaus near eight
// seconds however many lines are added, so it cannot exercise a bound set
// above that — which is exactly where the interesting failure lives. Use
// loadPathologicalFixture for those.
func pathologicalBash(lines int) []byte {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	for range lines {
		b.WriteString("this is line of plain prose text without shell syntax\n")
	}
	return []byte(b.String())
}

// loadPathologicalFixture returns the script that actually wedged the daemon:
// 19 KB, 483 lines, syntactically valid (bash -n passes), and nothing about it
// looks unusual. No single construct is to blame — the GLR cost compounds
// across the whole file, which is why no cheap property of the source predicts
// it and why the bound has to be on the parse itself.
//
// Left unbounded it ran for a minute in testing, and had pinned a core for
// half an hour in production before anyone noticed. Synthetic sources do not
// reproduce that, so this one is committed verbatim.
func loadPathologicalFixture(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "pathological_glr.sh"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return src
}

// TestParseSourceTimesOutOnPathologicalSource is the regression guard for the
// daemon wedge: without a deadline this parse never returns.
func TestParseSourceTimesOutOnPathologicalSource(t *testing.T) {
	src := pathologicalBash(40)
	if !forest.ShouldParse(src) {
		t.Fatal("ShouldParse rejected the fixture; the timeout path would never be reached")
	}

	const budget = 100 * time.Millisecond
	start := time.Now()
	tree, err := forest.ParseSourceWithTimeout(src, adapters.ForExtension("sh"), budget)
	elapsed := time.Since(start)

	if !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("err = %v, want ErrParseTimeout", err)
	}
	if tree != nil {
		t.Error("tree is non-nil; the partial tree must be discarded, not indexed")
	}
	// Generous slack over the budget: the assertion that matters is that the
	// parse is bounded at all, not that the parser is punctual.
	if elapsed > 10*budget {
		t.Errorf("parse took %s, want it abandoned near %s", elapsed, budget)
	}
}

// TestParseSourceBoundHoldsAtLongBudgets is the test that the obvious
// implementation fails. Relying on the parser's own timeout looks correct at
// the short budgets a fast test would use, but the parser only consults it in
// the primary parse loop: at 12s and beyond the bound was missed entirely, and
// a 16s budget on this input ran 58s and then reported success. Anything that
// reintroduces that dependence passes every other test in this file.
func TestParseSourceBoundHoldsAtLongBudgets(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a multi-second parse bound")
	}
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	const budget = 12 * time.Second
	start := time.Now()
	_, err := forest.ParseSourceWithTimeout(loadPathologicalFixture(t), adapters.ForExtension("sh"), budget)
	elapsed := time.Since(start)

	if !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("err = %v after %s, want ErrParseTimeout", err, elapsed)
	}
	if elapsed > budget+5*time.Second {
		t.Errorf("parse ran %s against a %s bound; the bound is not being enforced", elapsed, budget)
	}
}

// TestParseSourceQuarantinesTimedOutSource covers the resiliency half: the
// deadline alone still lets a watcher-touched pathological file burn
// MaxParseDuration of a core on every single event. The second attempt must be
// free.
func TestParseSourceQuarantinesTimedOutSource(t *testing.T) {
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	src := pathologicalBash(40)
	sh := adapters.ForExtension("sh")
	const budget = 100 * time.Millisecond

	if _, err := forest.ParseSourceWithTimeout(src, sh, budget); !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("first parse err = %v, want ErrParseTimeout", err)
	}
	if got := forest.QuarantinedCount(); got != 1 {
		t.Errorf("QuarantinedCount() = %d, want 1", got)
	}

	start := time.Now()
	_, err := forest.ParseSourceWithTimeout(src, sh, budget)
	elapsed := time.Since(start)

	if !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("repeat parse err = %v, want ErrParseTimeout", err)
	}
	if elapsed > budget/2 {
		t.Errorf("repeat parse took %s; a quarantined source must short-circuit, not re-parse", elapsed)
	}
}

// TestQuarantineIsKeyedByContent pins the property that makes the quarantine
// safe to leave on: editing the file releases it, so a fixed pathological
// source never becomes permanently unindexable by accident.
func TestQuarantineIsKeyedByContent(t *testing.T) {
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	sh := adapters.ForExtension("sh")
	const budget = 100 * time.Millisecond

	if _, err := forest.ParseSourceWithTimeout(pathologicalBash(40), sh, budget); !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("setup parse err = %v, want ErrParseTimeout", err)
	}

	// The "edited" file: same path in practice, different bytes, parseable.
	tree, err := forest.ParseSourceWithTimeout([]byte("echo fixed\n"), sh, budget)
	if err != nil {
		t.Fatalf("parse after edit: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil; an edited file must be parsed, not held by the quarantine")
	}
	tree.Close()
}

// TestParseSourceKeepsMaxParseDurationWired guards the constant against being
// quietly dropped or zeroed, which would restore the unbounded parse.
func TestParseSourceKeepsMaxParseDurationWired(t *testing.T) {
	if forest.MaxParseDuration <= 0 {
		t.Fatalf("MaxParseDuration = %s; a non-positive value disables the bound",
			forest.MaxParseDuration)
	}
}

// TestParseSourceDoesNotTimeOutOnOrdinarySource checks the deadline stays out
// of the way of real files.
func TestParseSourceDoesNotTimeOutOnOrdinarySource(t *testing.T) {
	src := []byte(`#!/usr/bin/env bash
set -euo pipefail

PORT="${PORT:-8080}"
LOG="${LOG:-$HOME/app.log}"

log() {
  printf '%s %s\n' "$(date)" "$*"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --force)   FORCE=1; shift ;;
    *)         die "unknown argument: $1" ;;
  esac
done
`)
	tree, err := forest.ParseSource(src, adapters.ForExtension("sh"))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	defer tree.Close()
}

// TestParseSourceStillIndexesMalformedSource pins the distinction the timeout
// check rests on: a parse that stops early for any reason other than the
// deadline still yields its ERROR-bearing tree. Checking ParseStoppedEarly
// instead of the stop reason would silently stop indexing broken files.
func TestParseSourceStillIndexesMalformedSource(t *testing.T) {
	src := []byte("func broken( { { { unclosed\n")
	tree, err := forest.ParseSource(src, adapters.ForExtension("go"))
	if err != nil {
		t.Fatalf("ParseSource on malformed source: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil; malformed source must still produce a tree")
	}
	defer tree.Close()
}
