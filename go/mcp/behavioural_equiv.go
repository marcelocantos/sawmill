// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"

	"github.com/marcelocantos/sawmill/diffharness"
)

// handleBehaviouralEquiv runs the behavioural/trace equivalence oracle
// (diffharness). Sibling of semantic_diff (which is AST-structural, not
// behavioural). Stateless — does not require parse().
//
// First-slice instance: the built-in particle demo (TiltBuggy shape).
// Custom engines use the Go package API (Engine/Tape/Differ interfaces).
func (h *Handler) handleBehaviouralEquiv(args map[string]any) (string, bool, error) {
	mode := optString(args, "mode")
	if mode == "" {
		mode = "batch"
	}
	instance := optString(args, "instance")
	if instance == "" {
		instance = "particle"
	}
	if instance != "particle" {
		return fmt.Sprintf("unknown instance %q (first slice supports \"particle\" only; custom engines use the Go diffharness package)", instance), true, nil
	}

	nScenarios := optInt(args, "n_scenarios")
	if nScenarios <= 0 {
		nScenarios = 200
	}
	ticks := optInt(args, "ticks")
	if ticks <= 0 {
		ticks = 90
	}
	threads := optInt(args, "threads")
	if threads <= 0 {
		threads = runtime.NumCPU()
		if threads < 1 {
			threads = 1
		}
	}

	// port_damping: 0.8 = correct linear-drag port; other values bias for demos.
	portDamping := 0.8
	if v, ok := args["port_damping"]; ok {
		switch n := v.(type) {
		case float64:
			portDamping = n
		case int:
			portDamping = float64(n)
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				portDamping = f
			}
		}
	}

	var newPort func() diffharness.Engine
	if portDamping == 0.8 {
		newPort = diffharness.NewParticlePort
	} else {
		newPort = diffharness.NewBiasedParticlePort(portDamping)
	}

	harness, err := diffharness.NewParticleHarness(newPort)
	if err != nil {
		return fmt.Sprintf("building harness: %v", err), true, nil
	}

	format := optString(args, "format")
	if format == "" {
		format = "text"
	}

	switch mode {
	case "batch":
		pool := diffharness.PoolTune
		if optString(args, "pool") == "holdout" {
			pool = diffharness.PoolHoldout
		}
		br := harness.Batch(pool, nScenarios, ticks, threads)
		rep := harness.BuildReport(br)
		if format == "json" {
			out, err := json.MarshalIndent(rep, "", "  ")
			if err != nil {
				return fmt.Sprintf("marshal: %v", err), true, nil
			}
			return string(out), false, nil
		}
		return diffharness.FormatReport(rep, diffharness.ParticleDiffer{}.MetricDefs()), false, nil

	case "study":
		seedStr := optString(args, "seed")
		if seedStr == "" {
			return "seed is required for mode=study (hex uint64, e.g. from batch worst-offenders)", true, nil
		}
		seed, err := strconv.ParseUint(seedStr, 16, 64)
		if err != nil {
			// Also accept decimal.
			seed, err = strconv.ParseUint(seedStr, 10, 64)
			if err != nil {
				return fmt.Sprintf("invalid seed %q: %v", seedStr, err), true, nil
			}
		}
		st := harness.Study(seed, ticks)
		if format == "json" {
			out, err := json.MarshalIndent(st, "", "  ")
			if err != nil {
				return fmt.Sprintf("marshal: %v", err), true, nil
			}
			return string(out), false, nil
		}
		return diffharness.FormatStudy(st), false, nil

	case "accept":
		// Always holdout — Goodhart guard is structural.
		br := harness.Batch(diffharness.PoolHoldout, nScenarios, ticks, threads)
		rep := harness.BuildReport(br)
		pol, err := diffharness.ParticleAcceptance()
		if err != nil {
			return fmt.Sprintf("building policy: %v", err), true, nil
		}
		acc := pol.Evaluate(br)
		if format == "json" {
			payload := map[string]any{
				"report":     rep,
				"acceptance": acc,
			}
			out, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return fmt.Sprintf("marshal: %v", err), true, nil
			}
			return string(out), false, nil
		}
		text := diffharness.FormatReport(rep, diffharness.ParticleDiffer{}.MetricDefs())
		text += diffharness.FormatAcceptance(acc)
		return text, false, nil

	default:
		return fmt.Sprintf("unknown mode %q; use batch, study, or accept", mode), true, nil
	}
}
