// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"math"
)

// NA returns the not-applicable marker for a metric this tick (e.g.
// travel-direction divergence when either side is near-stationary).
// Percentile aggregation drops NA values rather than polluting them.
func NA() float64 { return math.NaN() }

// IsNA reports whether v is the not-applicable marker.
func IsNA(v float64) bool { return math.IsNaN(v) }

// MetricDef describes one named distance the Differ produces.
// DisplayScale converts raw units to printed units (e.g. rad→deg uses
// 180/π). Percentiles and policy bounds always operate on RAW values.
type MetricDef struct {
	Name         string  `json:"name"`
	Unit         string  `json:"unit"`
	DisplayScale float64 `json:"display_scale"`
}

// Vec is a numeric snapshot: Init, Input, or observable State.
// Keys are instance-defined (e.g. "x","y","angle" or "force").
type Vec map[string]float64

// Clone returns a shallow copy of v.
func (v Vec) Clone() Vec {
	if v == nil {
		return nil
	}
	out := make(Vec, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}

// Engine wraps one headless, deterministic, resettable implementation.
// The reference and port engines share Init/Input/State (Vec) types but
// must not share code beyond those types.
type Engine interface {
	Reset(init Vec)
	Step(input Vec)
	Observe() Vec
	// Probe is an optional study-mode side channel (internals worth printing
	// side-by-side). Empty string is fine.
	Probe() string
}

// Tape is a deterministic seed → (Init, Input sequence) map.
// The driver owns one Tape and materialises each Input once for both
// engines (single-materialisation invariant).
type Tape interface {
	Init() Vec
	Next(tick int) Vec
}

// NewTape constructs a Tape from a seed. Instances implement this as a
// function value so the harness stays free of generics on Init/Input types.
type NewTape func(seed uint64) Tape

// TickDiff is one tick's differ output.
type TickDiff struct {
	Metrics  []float64 `json:"metrics"` // RAW distances; NA allowed
	QualRef  uint8     `json:"qual_ref"`
	QualPort uint8     `json:"qual_port"`
}

// Differ computes per-tick divergence metrics and qualitative signatures
// from the observable trace. It localises; it does not judge.
type Differ interface {
	MetricDefs() []MetricDef
	// Diff receives previous and current states so finite-difference
	// velocities can be derived from the trace (never engine internals).
	Diff(refPrev, refCur, portPrev, portCur Vec) TickDiff
}

// FormatState renders a State for study-mode output.
type FormatState func(s Vec) string

// Config controls the driver, reporter, and study-replayer.
type Config struct {
	// Horizons are ticks at which divergence is sampled. Must be strictly ascending.
	Horizons []int `json:"horizons"`

	// PrimaryMetric indexes Differ.MetricDefs for wild-tail / first-divergence.
	PrimaryMetric int `json:"primary_metric"`

	// WildThreshold (RAW units): primary metric above this is "wild".
	WildThreshold float64 `json:"wild_threshold"`

	// DisagreeHorizon is the horizon used for worst-offender mining.
	DisagreeHorizon int `json:"disagree_horizon"`

	// WorstCount caps the worst-disagreement list.
	WorstCount int `json:"worst_count"`

	// Study print policy.
	StudyHeadTicks          int     `json:"study_head_ticks"`
	StudyPrintEvery         int     `json:"study_print_every"`
	StudyAttentionThreshold float64 `json:"study_attention_threshold"`

	// FirstDivEpsilon (RAW primary metric): first tick where primary metric
	// exceeds this is recorded as first divergence. Zero means use
	// WildThreshold if set, else a tiny positive default.
	FirstDivEpsilon float64 `json:"first_div_epsilon"`
}

// DefaultConfig returns the TiltBuggy-shaped defaults: horizons
// {1,3,10,30,60,90}, disagree @30, worst 6.
func DefaultConfig() Config {
	return Config{
		Horizons:                []int{1, 3, 10, 30, 60, 90},
		PrimaryMetric:           0,
		WildThreshold:           math.Inf(1),
		DisagreeHorizon:         30,
		WorstCount:              6,
		StudyHeadTicks:          12,
		StudyPrintEvery:         10,
		StudyAttentionThreshold: math.Inf(1),
		FirstDivEpsilon:         0,
	}
}

// ScenarioSample is one scenario's metrics at each horizon, plus first-div.
type ScenarioSample struct {
	Seed      uint64      `json:"seed"`
	Metrics   [][]float64 `json:"metrics"`    // [horizonIdx][metricIdx], RAW
	QualRef   []uint8     `json:"qual_ref"`   // per horizon
	QualPort  []uint8     `json:"qual_port"`  // per horizon
	FirstDiv  *FirstDivergence `json:"first_divergence,omitempty"`
}

// FirstDivergence localises the earliest tick where the primary metric
// exceeds the epsilon gate, with paired states for study.
type FirstDivergence struct {
	Tick      int     `json:"tick"`       // 1-based tick index (after this many steps)
	Metric    float64 `json:"metric"`     // RAW primary metric at that tick
	RefState  Vec     `json:"ref_state"`
	PortState Vec     `json:"port_state"`
	QualRef   uint8   `json:"qual_ref"`
	QualPort  uint8   `json:"qual_port"`
}

// BatchResult aggregates scenario samples for reporting and acceptance.
type BatchResult struct {
	Pool     SeedPool         `json:"pool"`
	Ticks    int              `json:"ticks"`
	Horizons []int            `json:"horizons"`
	Samples  []ScenarioSample `json:"samples"`
}

// HorizonIndex returns the index of horizon h in br.Horizons, or -1.
func (br *BatchResult) HorizonIndex(h int) int {
	for i, x := range br.Horizons {
		if x == h {
			return i
		}
	}
	return -1
}

// PercentileRow is one (horizon, metric) row of the reporter table.
type PercentileRow struct {
	Horizon int     `json:"horizon"`
	Metric  string  `json:"metric"`
	Unit    string  `json:"unit"`
	P50     float64 `json:"p50"` // display units
	P90     float64 `json:"p90"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
	N       int     `json:"n"`
}

// WildTailRow is the qualitative breakdown of the wild tail at one horizon.
type WildTailRow struct {
	Horizon           int     `json:"horizon"`
	WildPct           float64 `json:"wild_pct"`
	QualAgreePct      float64 `json:"qual_agree_pct"`      // bifurcation
	QualDifferPct     float64 `json:"qual_differ_pct"`     // DISAGREEMENT
	WildCount         int     `json:"wild_count"`
	DisagreeCount     int     `json:"disagree_count"`
}

// WorstOffender is one disagreement seed ready for study.
type WorstOffender struct {
	Seed          uint64  `json:"seed"`
	PrimaryMetric float64 `json:"primary_metric"` // display units
	Horizon       int     `json:"horizon"`
}

// Report is the structured horizon-reporter output.
type Report struct {
	NScenarios     int              `json:"n_scenarios"`
	Ticks          int              `json:"ticks"`
	Pool           string           `json:"pool"`
	Percentiles    []PercentileRow  `json:"percentiles"`
	WildTail       []WildTailRow    `json:"wild_tail"`
	WorstOffenders []WorstOffender  `json:"worst_offenders"`
	// FirstDivSummary: fraction of scenarios that diverged, and median
	// first-divergence tick among those that did.
	FirstDivRate     float64 `json:"first_div_rate"`
	FirstDivMedianTick float64 `json:"first_div_median_tick"`
	// Sample of first-divergence localisations (up to WorstCount).
	FirstDivSamples []FirstDivergence `json:"first_div_samples,omitempty"`
}
