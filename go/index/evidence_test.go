// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"strings"
	"testing"
)

func TestEvidenceForExtractsNameTokens(t *testing.T) {
	ev := EvidenceFor("OnSwipeGesture", "/proj/main.go", nil)
	for _, want := range []string{" on ", " swipe ", " gesture ", " onswipegesture "} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence missing %q: %q", want, ev)
		}
	}
}

func TestEvidenceForExtractsPathTokens(t *testing.T) {
	ev := EvidenceFor("Foo", "/proj/gestures/swipe_handler.go", nil)
	for _, want := range []string{" swipe ", " handler ", " gestures "} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence missing %q: %q", want, ev)
		}
	}
}

func TestEvidenceForExtractsBodyTokens(t *testing.T) {
	body := []byte(`
		// Handle a transient retry with exponential backoff and jitter.
		func attempt() error {
			for i := 0; i < maxRetries; i++ {
				time.Sleep(backoff)
			}
		}
	`)
	ev := EvidenceFor("attempt", "/proj/retry.go", body)
	for _, want := range []string{" retry ", " backoff ", " jitter ", " exponential ", " maxretries "} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence missing %q: %q", want, ev)
		}
	}
	// Stopwords must not appear.
	for _, banned := range []string{" func ", " for ", " return ", " var "} {
		if strings.Contains(ev, banned) {
			t.Errorf("evidence unexpectedly contains stopword %q: %q", banned, ev)
		}
	}
}

func TestEvidenceForIsPadded(t *testing.T) {
	ev := EvidenceFor("Foo", "/proj/bar.go", nil)
	if ev == "" {
		t.Fatal("expected non-empty evidence")
	}
	if ev[0] != ' ' || ev[len(ev)-1] != ' ' {
		t.Errorf("evidence must be space-padded on both ends: %q", ev)
	}
}

func TestEvidenceForDedupes(t *testing.T) {
	body := []byte("swipe swipe swipe gesture gesture")
	ev := EvidenceFor("Swipe", "/proj/swipe.go", body)
	// Each token should appear exactly once in the bag.
	if got := strings.Count(ev, " swipe "); got != 1 {
		t.Errorf("expected one 'swipe' token, got %d: %q", got, ev)
	}
	if got := strings.Count(ev, " gesture "); got != 1 {
		t.Errorf("expected one 'gesture' token, got %d: %q", got, ev)
	}
}

func TestEvidenceForRespectsBodyLimit(t *testing.T) {
	// A body padded with one distinctive token at the end — past the limit,
	// it should not appear in evidence.
	var sb strings.Builder
	for sb.Len() < EvidenceBodyLimit+1024 {
		sb.WriteString("filler ")
	}
	sb.WriteString("uniquetail")
	ev := EvidenceFor("Foo", "/proj/foo.go", []byte(sb.String()))
	if strings.Contains(ev, " uniquetail ") {
		t.Errorf("body limit not enforced: evidence contains tail token")
	}
	if !strings.Contains(ev, " filler ") {
		t.Errorf("expected leading body tokens to be indexed: %q", ev)
	}
}

func TestSplitIdentifierCases(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"OnSwipeGesture", []string{"On", "Swipe", "Gesture"}},
		{"snake_case_id", []string{"snake", "case", "id"}},
		{"mixed_CaseID", []string{"mixed", "Case", "ID"}},
		{"HTTPClient", []string{"HTTPClient"}},
		{"v2Beta", []string{"v2", "Beta"}},
	}
	for _, c := range cases {
		got := splitIdentifier(c.in)
		if !sliceEq(got, c.want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
