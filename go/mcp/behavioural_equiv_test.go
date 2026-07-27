// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"
)

func TestBehaviouralEquivBatch(t *testing.T) {
	h := &Handler{}
	out, isErr, err := h.handleBehaviouralEquiv(map[string]any{
		"mode":        "batch",
		"n_scenarios": float64(40),
		"ticks":       float64(30),
		"threads":     float64(2),
	})
	if err != nil || isErr {
		t.Fatalf("batch: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, "horizon") || !strings.Contains(out, "pos") {
		t.Fatalf("expected percentile table, got:\n%s", out)
	}
	if !strings.Contains(out, "first-divergence") {
		t.Fatalf("expected first-divergence section:\n%s", out)
	}
}

func TestBehaviouralEquivAcceptCorrectAndBiased(t *testing.T) {
	h := &Handler{}
	// Correct port should pass.
	out, isErr, err := h.handleBehaviouralEquiv(map[string]any{
		"mode":         "accept",
		"n_scenarios":  float64(300),
		"ticks":        float64(90),
		"threads":      float64(4),
		"port_damping": float64(0.8),
	})
	if err != nil || isErr {
		t.Fatalf("accept correct: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, "ACCEPTANCE PASS") {
		t.Fatalf("correct port should PASS:\n%s", out)
	}

	// Biased mutant must fail (mutation check).
	out, isErr, err = h.handleBehaviouralEquiv(map[string]any{
		"mode":         "accept",
		"n_scenarios":  float64(300),
		"ticks":        float64(90),
		"threads":      float64(4),
		"port_damping": float64(0.6),
	})
	if err != nil || isErr {
		t.Fatalf("accept biased: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, "ACCEPTANCE FAIL") {
		t.Fatalf("biased port should FAIL:\n%s", out)
	}
}

func TestBehaviouralEquivStudy(t *testing.T) {
	h := &Handler{}
	// Run a tiny batch to get a seed, or just study seed 0.
	out, isErr, err := h.handleBehaviouralEquiv(map[string]any{
		"mode":    "study",
		"seed":    "0",
		"ticks":   float64(20),
		"format":  "text",
	})
	if err != nil || isErr {
		t.Fatalf("study: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, "study seed") {
		t.Fatalf("expected study header:\n%s", out)
	}
}

func TestBehaviouralEquivJSON(t *testing.T) {
	h := &Handler{}
	out, isErr, err := h.handleBehaviouralEquiv(map[string]any{
		"mode":        "batch",
		"n_scenarios": float64(20),
		"ticks":       float64(30),
		"format":      "json",
	})
	if err != nil || isErr {
		t.Fatalf("json batch: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, `"percentiles"`) {
		t.Fatalf("expected json percentiles:\n%s", out)
	}
}
