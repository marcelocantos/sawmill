// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import "testing"

// TestPatternCaptureRespectsBracketNesting is the regression for F6 (T46): a
// $placeholder capture must respect balanced brackets. Against
// "foo(bar(1, 2), 3)" the pattern "foo($a, $b)" must bind $a to the whole
// nested call "bar(1, 2)" and $b to "3" — not split on the inner comma (which
// produced a="bar(1", b="2), 3" and emitted syntactically corrupt code).
func TestPatternCaptureRespectsBracketNesting(t *testing.T) {
	p := ParsePattern("foo($a, $b)")
	caps, ok := p.Match("foo(bar(1, 2), 3)")
	if !ok {
		t.Fatal("expected the pattern to match")
	}
	if caps["a"] != "bar(1, 2)" {
		t.Errorf("$a = %q, want %q", caps["a"], "bar(1, 2)")
	}
	if caps["b"] != "3" {
		t.Errorf("$b = %q, want %q", caps["b"], "3")
	}

	// The arg-swap rewrite that motivates the finding must be well-formed.
	if out := Apply("foo($b, $a)", caps); out != "foo(3, bar(1, 2))" {
		t.Errorf("rewrite = %q, want %q", out, "foo(3, bar(1, 2))")
	}
}

// TestPatternCaptureNonNestedUnchanged guards against a regression in the
// common (non-nested) case when adding bracket-depth tracking.
func TestPatternCaptureNonNestedUnchanged(t *testing.T) {
	p := ParsePattern("foo($a, $b)")
	caps, ok := p.Match("foo(1, 2)")
	if !ok || caps["a"] != "1" || caps["b"] != "2" {
		t.Fatalf("simple match regressed: ok=%v caps=%v", ok, caps)
	}
}

// TestPatternCaptureNestedBrackets checks depth tracking across [] and {} too.
func TestPatternCaptureNestedBrackets(t *testing.T) {
	p := ParsePattern("f($a, $b)")
	caps, ok := p.Match("f(m[1, 2], k)")
	if !ok {
		t.Fatal("expected match")
	}
	if caps["a"] != "m[1, 2]" || caps["b"] != "k" {
		t.Fatalf("bracket depth mis-tracked: a=%q b=%q", caps["a"], caps["b"])
	}
}
