// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package diffharness implements a behavioural/trace equivalence oracle:
// two runnable implementations driven by a single materialised input tape,
// with per-horizon divergence percentiles, first-divergence localisation
// (paired states), and Lyapunov-bounded acceptance (short-horizon epsilon
// gates + long-horizon distributional/qualitative invariants).
//
// This is the first vertical slice of the migration-fidelity oracle
// (bullseye 🎯T51). It is behavioural, not syntactic — do not confuse it with
// semdiff (AST structural diff) or pattern equivalences (teach_equivalence).
//
// # Design (TEMPLATE.md / oracle-first skill rule 5)
//
// Eight parts; this package owns the reusable core:
//
//   - driver          — RunScenario / Batch
//   - differ plumbing — sample metrics at a horizon ladder
//   - horizon-reporter — percentiles, wild-tail breakdown, worst seeds
//   - study-replayer  — paired state + probes for one seed
//   - acceptance-policy — step bounds (≤ chaos horizon) + dist bounds
//
// The instance supplies reference/port engines, a tape generator, and a
// Differ. A reduced dynamical demo (particle box with exponential damping
// vs linear drag) ships in-package and reproduces the TiltBuggy diagnostic
// shape: linear short-horizon floor, exponential long-horizon tail.
//
// # Goodhart guard (mandatory reading)
//
// Fixes that improve an acceptance report MUST trace to a structural
// divergence in the reference source (different equations, missing
// constraints, collapsed discrete states). Never constant-tune against the
// diff merely because it shrinks a percentile. Gradient + structural
// constraint = system identification; gradient alone = curve fit.
//
// Structural enforcement in this package:
//
//  1. Tune vs holdout seed pools — Accept evaluates only holdout batches;
//     fix-loop mining uses tune seeds only.
//  2. Step bounds rejected beyond ChaosHorizon — bit-exact long-horizon
//     match across engines is a category error and cannot be expressed.
//  3. First-divergence localisation names the tick and paired states so
//     the fix loop always has a structural target to investigate.
//
// Reference instance: squz/ge sample/tiltbuggy/oracle (Chipmunk vs box2d).
// Spec: fable5-artifacts/C2-differential-harness/TEMPLATE.md.
package diffharness
