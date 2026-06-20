// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestFindByConceptAgainstAuditCorpus is the empirical verify checkpoint for
// 🎯T36. It exercises find_by_concept against three concept queries drawn
// from the May 2026 grep-audit (docs/agent-usage-archaeology.md) — swipe /
// gesture handling, BackBoard-style app-state plumbing, and retry/backoff.
//
// Each scenario builds a small representative codebase (mixed Go + Python),
// runs find_by_concept exactly once (no manual grep rounds), and checks two
// metrics against thresholds:
//
//   - recall = |groundTruth ∩ top10| / |groundTruth|. The "must-find" anchor
//     symbols that an agent would expect to retrieve in a single call.
//   - precision@5 = |relevant ∩ top5| / 5. A guard on top-5 dominance by
//     genuinely concept-relevant symbols (relevant ⊇ groundTruth and may
//     include peripheral matches).
//
// The recorded numbers also feed docs/find-by-concept-verification.md.
func TestFindByConceptAgainstAuditCorpus(t *testing.T) {
	scenarios := []conceptScenario{
		gestureSwipeScenario(),
		backboardAppStateScenario(),
		retryBackoffScenario(),
	}

	var summary []scenarioResult
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			res := runScenario(t, sc)
			summary = append(summary, res)
		})
	}

	t.Run("summary", func(t *testing.T) {
		t.Log("\n" + renderVerifySummary(summary))
	})
}

// conceptScenario describes one audit-derived verify case.
type conceptScenario struct {
	name        string            // short id used as subtest name
	description string            // human-readable summary surfaced in docs
	query       string            // natural-language query the agent would issue
	teach       []teachedConcept  // concepts to teach before searching
	files       map[string]string // representative codebase
	groundTruth []string          // anchor symbol names that must be in top-10
	relevant    []string          // broader set of concept-relevant symbols (⊇ groundTruth)
	distractors []string          // documented unrelated symbols (must not dominate top-5)

	minRecall       float64 // threshold for groundTruth ∩ top10
	minPrecisionAt5 float64 // threshold for relevant ∩ top5
}

type teachedConcept struct {
	name        string
	description string
	aliases     []string
}

type scenarioResult struct {
	name         string
	query        string
	aliases      []string
	topNames     []string
	recall       float64
	precisionAt5 float64
}

func runScenario(t *testing.T, sc conceptScenario) scenarioResult {
	t.Helper()
	h := testHandler(t, sc.files)

	for _, tc := range sc.teach {
		aliasesJSON, err := json.Marshal(tc.aliases)
		if err != nil {
			t.Fatalf("marshalling aliases for %q: %v", tc.name, err)
		}
		text, isErr, err := h.handleTeachConcept(map[string]any{
			"name":        tc.name,
			"description": tc.description,
			"aliases":     string(aliasesJSON),
		})
		if err != nil || isErr {
			t.Fatalf("teach_concept %q: err=%v isErr=%v text=%s", tc.name, err, isErr, text)
		}
	}

	out, isErr, err := h.handleFindByConcept(map[string]any{
		"query":  sc.query,
		"limit":  10,
		"format": "json",
		"scope":  "all", // testHandler temp dirs lack a configured scope; "all" disables the filter
	})
	if err != nil || isErr {
		t.Fatalf("find_by_concept: err=%v isErr=%v out=%s", err, isErr, out)
	}

	type verifyMatch struct {
		Name           string   `json:"name"`
		File           string   `json:"file"`
		Score          int      `json:"score"`
		MatchedAliases []string `json:"matched_aliases"`
	}
	var resp struct {
		Aliases []string      `json:"aliases"`
		Matches []verifyMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decoding JSON: %v\noutput: %s", err, out)
	}

	// Symbol indexer can emit duplicate entries for the same symbol (e.g. a
	// function declaration + a call site that references its own name). For
	// recall/precision purposes, collapse to one row per symbol name while
	// preserving rank order.
	names := make([]string, len(resp.Matches))
	for i, m := range resp.Matches {
		names[i] = m.Name
	}
	topNames := dedupePreservingOrder(names)

	recall := computeRecall(sc.groundTruth, topNames)
	precision5 := computePrecisionAtK(sc.relevant, topNames, 5)

	t.Logf("scenario=%s query=%q aliases=%d (%s)", sc.name, sc.query, len(resp.Aliases), strings.Join(resp.Aliases, ","))
	t.Logf("  top-10 matches: %v", topNames)
	t.Logf("  recall(groundTruth)=%.2f precision@5(relevant)=%.2f", recall, precision5)

	if recall < sc.minRecall {
		t.Errorf("recall %.2f < threshold %.2f. ground truth: %v. top-10: %v",
			recall, sc.minRecall, sc.groundTruth, topNames)
	}
	if precision5 < sc.minPrecisionAt5 {
		t.Errorf("precision@5 %.2f < threshold %.2f. relevant: %v. top-5: %v",
			precision5, sc.minPrecisionAt5, sc.relevant, firstN(topNames, 5))
	}
	if intersects(topNames[:minInt(5, len(topNames))], sc.distractors) {
		t.Errorf("top-5 contains documented distractor(s). top-5: %v, distractors: %v",
			firstN(topNames, 5), sc.distractors)
	}

	return scenarioResult{
		name:         sc.name,
		query:        sc.query,
		aliases:      resp.Aliases,
		topNames:     topNames,
		recall:       recall,
		precisionAt5: precision5,
	}
}

func dedupePreservingOrder(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func computeRecall(groundTruth, got []string) float64 {
	if len(groundTruth) == 0 {
		return 1
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, g := range got {
		gotSet[g] = struct{}{}
	}
	hit := 0
	for _, g := range groundTruth {
		if _, ok := gotSet[g]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(groundTruth))
}

func computePrecisionAtK(relevant, got []string, k int) float64 {
	if k > len(got) {
		k = len(got)
	}
	if k == 0 {
		return 0
	}
	relSet := make(map[string]struct{}, len(relevant))
	for _, r := range relevant {
		relSet[r] = struct{}{}
	}
	hit := 0
	for i := 0; i < k; i++ {
		if _, ok := relSet[got[i]]; ok {
			hit++
		}
	}
	return float64(hit) / float64(k)
}

func firstN(xs []string, n int) []string {
	if n > len(xs) {
		n = len(xs)
	}
	return xs[:n]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intersects(a, b []string) bool {
	bs := make(map[string]struct{}, len(b))
	for _, x := range b {
		bs[x] = struct{}{}
	}
	for _, x := range a {
		if _, ok := bs[x]; ok {
			return true
		}
	}
	return false
}

func renderVerifySummary(results []scenarioResult) string {
	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })
	var sb strings.Builder
	sb.WriteString("find_by_concept verify summary (single call per scenario, limit=10):\n")
	fmt.Fprintf(&sb, "  %-32s %-10s %-12s\n", "scenario", "recall", "precision@5")
	for _, r := range results {
		fmt.Fprintf(&sb, "  %-32s %-10.2f %-12.2f\n", r.name, r.recall, r.precisionAt5)
	}
	return sb.String()
}

// --- Scenario 1: gesture / swipe handling --------------------------------

func gestureSwipeScenario() conceptScenario {
	return conceptScenario{
		name:        "gesture-swipe-handling",
		description: "Audit query: 'where does this codebase handle swipe / gesture input?' — built-in 'swipe' concept (no project teach).",
		query:       "swipe handling",
		files: map[string]string{
			"ui/swipe.go": `package ui

// OnSwipeLeft handles a leftward swipe on a card.
func OnSwipeLeft(event *Event) { dismissCard() }

// OnSwipeRight handles a rightward swipe on a card.
func OnSwipeRight(event *Event) { acceptCard() }

// HandlePan tracks an in-progress pan gesture between frames.
func HandlePan(start, end Point) {}

// DetectFlingDirection classifies a high-velocity pan as a fling.
func DetectFlingDirection(velocity float64) int { return 0 }

func dismissCard() {}
func acceptCard()  {}

type Event struct{}
type Point struct{ X, Y float64 }
`,
			"ui/touch_input.py": `"""Touch-input dispatcher and recognisers."""


class GestureDetector:
    """Top-level dispatcher for pan, pinch, and tap gestures."""

    def on_pan(self, event):
        return self._dispatch("pan", event)

    def on_pinch(self, event):
        return self._dispatch("pinch", event)

    def on_double_tap(self, event):
        return self._dispatch("double_tap", event)

    def _dispatch(self, kind, event):
        return (kind, event)


class SwipeRecognizer:
    """Recogniser specialised for swipe gestures."""

    def recognise_swipe(self, points):
        return len(points) > 5

    def on_long_press(self, event):
        return event
`,
			"core/math.go": `package core

// ComputeTaxRate is a deliberately unrelated distractor.
func ComputeTaxRate(amount float64) float64 { return amount * 0.1 }

// NormaliseVector is a distractor.
func NormaliseVector(x, y float64) (float64, float64) { return x, y }

// ClampInt is a distractor.
func ClampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
`,
			"config/loader.py": `"""Config loading helpers — unrelated to gestures."""


def load_config(path):
    return path


def parse_yaml(text):
    return text


def merge_dicts(a, b):
    out = dict(a)
    out.update(b)
    return out
`,
		},
		groundTruth: []string{
			"OnSwipeLeft", "HandlePan", "DetectFlingDirection",
			"SwipeRecognizer", "recognise_swipe",
		},
		relevant: []string{
			"OnSwipeLeft", "OnSwipeRight", "HandlePan", "DetectFlingDirection",
			"GestureDetector", "SwipeRecognizer",
			"on_pan", "on_pinch", "on_double_tap", "on_long_press", "recognise_swipe",
		},
		distractors: []string{
			"ComputeTaxRate", "NormaliseVector", "ClampInt",
			"load_config", "parse_yaml", "merge_dicts",
		},
		minRecall:       1.0, // every ground-truth anchor must surface in top-10
		minPrecisionAt5: 0.8, // top-5 must be ≥4/5 concept-relevant
	}
}

// --- Scenario 2: BackBoard-style app-state plumbing ----------------------

func backboardAppStateScenario() conceptScenario {
	return conceptScenario{
		name: "backboard-app-state",
		description: "Audit query: 'where is the app-lifecycle / BackBoard-style state machine plumbed?' — taught concept 'app_state' " +
			"because BackBoard/foreground/background terminology is not a built-in.",
		query: "app_state",
		teach: []teachedConcept{
			{
				name:        "app_state",
				description: "Application lifecycle plumbing (foreground/background/inactive transitions, BackBoard-style state machines).",
				aliases: []string{
					"app_state", "appstate", "lifecycle", "foreground", "background",
					"willenterforeground", "didenterbackground", "didbecomeactive",
					"willresignactive", "willterminate", "applicationstate",
					"backboard", "scenedelegate", "applicationdelegate",
					"on_resume", "on_pause", "on_stop", "on_start",
				},
			},
		},
		files: map[string]string{
			"app/lifecycle.go": `package app

// AppLifecycle owns the foreground/background transition logic.
type AppLifecycle struct{ state int }

// WillEnterForeground is invoked when the app is about to become active.
func WillEnterForeground(l *AppLifecycle) { l.state = 1 }

// DidEnterBackground is invoked when the app has been pushed to the background.
func DidEnterBackground(l *AppLifecycle) { l.state = 2 }

// DidBecomeActive transitions the app to the active foreground state.
func DidBecomeActive(l *AppLifecycle) { l.state = 3 }

// WillResignActive transitions the app to inactive (e.g. incoming call).
func WillResignActive(l *AppLifecycle) { l.state = 4 }
`,
			"app/backboard.py": `"""BackBoard-style observer for application lifecycle events."""


class BackBoardObserver:
    """Observes foreground/background transitions and forwards them."""

    def on_resume(self):
        """Called when the app resumes from background."""

    def on_pause(self):
        """Called when the app pauses (analogous to willResignActive)."""

    def on_stop(self):
        """Called when the app enters the background."""


class SceneDelegate:
    """Per-scene application state hooks."""

    def applicationDidBecomeActive(self): ...

    def applicationWillTerminate(self): ...
`,
			"util/strings.go": `package util

func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func Titlecase(s string) string { return s }

func CountWords(s string) int { return len(s) }
`,
			"util/json_helpers.py": `"""Generic JSON helpers — not related to lifecycle."""


def serialise(value):
    return value


def deserialise(text):
    return text


def pretty_print(obj):
    return repr(obj)
`,
		},
		groundTruth: []string{
			"WillEnterForeground", "DidEnterBackground", "DidBecomeActive",
			"BackBoardObserver", "SceneDelegate",
		},
		relevant: []string{
			"AppLifecycle",
			"WillEnterForeground", "DidEnterBackground", "DidBecomeActive", "WillResignActive",
			"BackBoardObserver", "SceneDelegate",
			"on_resume", "on_pause", "on_stop",
			"applicationDidBecomeActive", "applicationWillTerminate",
		},
		distractors: []string{
			"Reverse", "Titlecase", "CountWords",
			"serialise", "deserialise", "pretty_print",
		},
		minRecall:       1.0,
		minPrecisionAt5: 0.8,
	}
}

// --- Scenario 3: retry / backoff -----------------------------------------

func retryBackoffScenario() conceptScenario {
	return conceptScenario{
		name:        "retry-backoff",
		description: "Audit query: 'where do we retry transient failures with backoff?' — built-in 'retry' concept (no project teach).",
		query:       "retry backoff",
		files: map[string]string{
			"net/retry.go": `package net

// CallWithRetry retries the operation with exponential backoff and jitter.
func CallWithRetry(op func() error, maxAttempts int) error {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := op(); err == nil {
			return nil
		}
		sleepBackoff(attempt)
	}
	return nil
}

// ExponentialBackoff returns the wait duration for a given attempt number.
func ExponentialBackoff(attempt int) int { return 1 << attempt }

// IsTransient reports whether an error is worth retrying.
func IsTransient(err error) bool { return err != nil }

// RetryPolicy bundles retry knobs.
type RetryPolicy struct {
	MaxAttempts int
	Jitter      bool
}

func sleepBackoff(attempt int) {}
`,
			"net/http_client.py": `"""HTTP client with retry helpers."""


def request_with_retry(url, max_attempts=3):
    """Issue an HTTP request, retrying transient failures with backoff."""
    for attempt in range(max_attempts):
        try:
            return _do_request(url)
        except TransientError:
            _sleep_backoff(attempt)
    return None


class RetryableSession:
    """Session that retries idempotent requests on transient errors."""

    def attempt(self, request):
        return self._send_with_backoff(request)

    def _send_with_backoff(self, request):
        return request


class TransientError(Exception):
    pass


def _do_request(url):
    return url


def _sleep_backoff(attempt):
    return attempt
`,
			"data/transform.go": `package data

// ParseRecord is a distractor unrelated to retries.
func ParseRecord(text string) (string, error) { return text, nil }

// EncodeRecord is a distractor.
func EncodeRecord(rec string) string { return rec }

// HashRecord is a distractor.
func HashRecord(rec string) uint64 { return 0 }
`,
			"data/validation.py": `"""Validation helpers — unrelated to retries."""


def is_email(value):
    return "@" in value


def is_phone(value):
    return value.isdigit()


def normalise_whitespace(value):
    return " ".join(value.split())
`,
		},
		groundTruth: []string{
			"CallWithRetry", "ExponentialBackoff", "RetryPolicy",
			"request_with_retry", "RetryableSession",
		},
		relevant: []string{
			"CallWithRetry", "ExponentialBackoff", "IsTransient", "RetryPolicy",
			"request_with_retry", "RetryableSession", "TransientError", "attempt",
		},
		distractors: []string{
			"ParseRecord", "EncodeRecord", "HashRecord",
			"is_email", "is_phone", "normalise_whitespace",
		},
		minRecall:       1.0,
		minPrecisionAt5: 0.8,
	}
}
