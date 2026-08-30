// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

// pathologicalC builds a C source the GLR parser is superlinear on: one large
// static table of numeric literals, the shape codec tables take. 500 rows
// (~53 KB) costs about 1.2s against microseconds for ordinary C of the same
// size, and the generator is deterministic so that cost is reproducible.
//
// It replaces the bash generator this file used to carry. Under gotreesitter
// v0.15.2 runs of bare-word bash lines were the cheapest trigger; v0.22.0 made
// that construct linear, so from the pinned v0.47.1 no bash input we know of
// is slow enough to exercise a bound. Data tables still are.
func pathologicalC(rows int) []byte {
	var b strings.Builder
	b.WriteString("static const short t[] = {\n")
	v := uint32(0x9e3779b9)
	for r := range rows {
		b.WriteString("    ")
		for c := range 12 {
			v = v*1664525 + 1013904223
			if c > 0 {
				b.WriteString(", ")
			}
			if v&1 == 0 {
				fmt.Fprintf(&b, "-0x%04x", v>>16&0xffff)
			} else {
				fmt.Fprintf(&b, "0x%04x", v>>16&0xffff)
			}
		}
		if r < rows-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("};\n")
	return []byte(b.String())
}

// loadPathologicalFixture returns the script that actually wedged the daemon:
// 19 KB, 483 lines, syntactically valid (bash -n passes), and nothing about it
// looks unusual. No single construct is to blame — the GLR cost compounds
// across the whole file.
//
// It is kept as a version witness rather than a slow input. Under v0.15.2 it
// ran over a minute; under the pinned version it parses in milliseconds, and
// TestBashFixtureIsNoLongerPathological fails if a bump ever reintroduces the
// blowup.
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
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	src := pathologicalC(500)
	if !forest.ShouldParse(src) {
		t.Fatal("ShouldParse rejected the fixture; the timeout path would never be reached")
	}

	const budget = 100 * time.Millisecond
	start := time.Now()
	tree, err := forest.ParseSourceWithTimeout(src, adapters.ForExtension("c"), budget)
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

// TestParseSourceBoundIsEnforcedOnItsOwnTerms checks that the bound comes from
// ParseSource's own measurement, not from whatever the parser reports.
//
// Under gotreesitter v0.15.2 this distinction was load-bearing and directly
// testable: the parser consulted its own timeout only in the primary parse
// loop, so a 16s budget on the bash fixture ran 58s and then reported success,
// and the watchdog was the only thing that actually capped it.
//
// The pinned v0.47.1 honours its timeout accurately at every budget we can
// reach — measured at 100ms/500ms/1000ms, and no input we have found is slow
// enough under it to probe the multi-second regime where the old leak lived.
// So this test can no longer distinguish the watchdog from the parser's
// timeout, and it is not claimed to. It pins the observable contract — the
// parse is abandoned near the budget and reports ErrParseTimeout — while the
// watchdog stays in place as defence in depth. That is not paranoia: v0.48.0
// reintroduced a blowup of exactly this shape in C, so the property that no
// single version is trusted to bound itself is worth keeping.
func TestParseSourceBoundIsEnforcedOnItsOwnTerms(t *testing.T) {
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	// Comfortably below the ~1.2s the input costs unbounded, and comfortably
	// above the sub-millisecond regime where any implementation looks right.
	const budget = 400 * time.Millisecond
	start := time.Now()
	_, err := forest.ParseSourceWithTimeout(pathologicalC(500), adapters.ForExtension("c"), budget)
	elapsed := time.Since(start)

	if !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("err = %v after %s, want ErrParseTimeout", err, elapsed)
	}
	if elapsed > budget+2*time.Second {
		t.Errorf("parse ran %s against a %s bound; the bound is not being enforced", elapsed, budget)
	}
}

// TestBashFixtureIsNoLongerPathological is the version guard. The committed
// fixture wedged the daemon under v0.15.2; v0.22.0 made the construct linear.
// If a future bump reintroduces the bash blowup, this fails loudly rather than
// leaving the parse bound to absorb it silently on every watcher event.
func TestBashFixtureIsNoLongerPathological(t *testing.T) {
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	start := time.Now()
	tree, err := forest.ParseSource(loadPathologicalFixture(t), adapters.ForExtension("sh"))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ParseSource on the bash fixture: %v after %s", err, elapsed)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	defer tree.Close()

	// Two orders of magnitude of headroom over the ~80ms observed, so this
	// fails on a real regression rather than on a slow machine.
	if elapsed > 5*time.Second {
		t.Errorf("bash fixture parsed in %s; the GLR blowup appears to be back", elapsed)
	}
}

// TestParseSourceQuarantinesTimedOutSource covers the resiliency half: the
// deadline alone still lets a watcher-touched pathological file burn
// MaxParseDuration of a core on every single event. The second attempt must be
// free.
func TestParseSourceQuarantinesTimedOutSource(t *testing.T) {
	forest.ResetQuarantine()
	t.Cleanup(forest.ResetQuarantine)

	src := pathologicalC(500)
	c := adapters.ForExtension("c")
	const budget = 100 * time.Millisecond

	if _, err := forest.ParseSourceWithTimeout(src, c, budget); !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("first parse err = %v, want ErrParseTimeout", err)
	}
	if got := forest.QuarantinedCount(); got != 1 {
		t.Errorf("QuarantinedCount() = %d, want 1", got)
	}

	start := time.Now()
	_, err := forest.ParseSourceWithTimeout(src, c, budget)
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

	c := adapters.ForExtension("c")
	const budget = 100 * time.Millisecond

	if _, err := forest.ParseSourceWithTimeout(pathologicalC(500), c, budget); !errors.Is(err, forest.ErrParseTimeout) {
		t.Fatalf("setup parse err = %v, want ErrParseTimeout", err)
	}

	// The "edited" file: same path in practice, different bytes, parseable.
	tree, err := forest.ParseSourceWithTimeout([]byte("int fixed(void) { return 0; }\n"), c, budget)
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
