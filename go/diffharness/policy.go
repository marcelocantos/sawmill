// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"fmt"
	"math"
)

// UnboundedChaosHorizon means the instance is deterministic (no Lyapunov
// knee); step bounds may be placed at any horizon, expressing strict
// per-pair equality when desired (e.g. transpiler strict-XML tier).
const UnboundedChaosHorizon = math.MaxInt

// StepBound is a short-horizon per-pair gate: p99(metric @ horizon) ≤ MaxP99.
// Horizon must be ≤ ChaosHorizon (enforced at AddStepBound).
type StepBound struct {
	Horizon int     `json:"horizon"`
	Metric  int     `json:"metric"`  // index into Differ.MetricDefs
	MaxP99  float64 `json:"max_p99"` // RAW units
}

// DistStatistic computes an ensemble statistic over a batch at one horizon index.
type DistStatistic func(br BatchResult, horizonIdx int) float64

// DistBound is a long-horizon distributional/qualitative invariant:
// lo ≤ statistic(batch @ horizon) ≤ hi. Legal at any horizon.
type DistBound struct {
	Name     string        `json:"name"`
	Horizon  int           `json:"horizon"`
	Statistic DistStatistic `json:"-"`
	Lo       float64       `json:"lo"`
	Hi       float64       `json:"hi"`
}

// AcceptancePolicy is the executable gate (TEMPLATE.md §2.8).
//
// Lyapunov discipline (§7.2): constructed with a measured ChaosHorizon;
// AddStepBound rejects any per-pair bound beyond it. Long-horizon clauses
// exist only as ensemble statistics (AddDistBound). Bit-exact long-horizon
// match across engines is a category error and cannot be expressed when
// ChaosHorizon is finite.
//
// Goodhart discipline (§7.1): Evaluate refuses a tune-pool batch — the gate
// can only be satisfied on seeds the fix loop never mined.
type AcceptancePolicy struct {
	chaosHorizon int
	metricDefs   []MetricDef
	stepBounds   []StepBound
	distBounds   []DistBound
}

// NewPolicyArgs constructs an AcceptancePolicy.
type NewPolicyArgs struct {
	// ChaosHorizon is MEASURED (the knee in the batch percentile table),
	// never assumed. Use UnboundedChaosHorizon for deterministic instances.
	ChaosHorizon int
	// MetricDefs is required for clause descriptions (from Differ.MetricDefs).
	MetricDefs []MetricDef
}

// NewAcceptancePolicy returns an empty policy with the given chaos horizon.
func NewAcceptancePolicy(args *NewPolicyArgs) (*AcceptancePolicy, error) {
	if args == nil {
		return nil, fmt.Errorf("diffharness: NewPolicyArgs is nil")
	}
	if args.ChaosHorizon < 0 {
		return nil, fmt.Errorf("diffharness: ChaosHorizon must be ≥ 0")
	}
	return &AcceptancePolicy{
		chaosHorizon: args.ChaosHorizon,
		metricDefs:   append([]MetricDef(nil), args.MetricDefs...),
	}, nil
}

// ChaosHorizon returns the configured chaos horizon.
func (p *AcceptancePolicy) ChaosHorizon() int { return p.chaosHorizon }

// AddStepBound registers a short-horizon per-pair bound.
// Returns an error if horizon > ChaosHorizon (Lyapunov guard).
func (p *AcceptancePolicy) AddStepBound(b StepBound) error {
	if b.Metric < 0 || b.Metric >= len(p.metricDefs) {
		return fmt.Errorf("diffharness: step bound metric %d out of range", b.Metric)
	}
	if b.Horizon > p.chaosHorizon {
		return fmt.Errorf(
			"diffharness: step bound at horizon %d exceeds chaos horizon %d: "+
				"per-pair bounds beyond the chaos horizon are a category error "+
				"(Lyapunov-bounded acceptance); use AddDistBound. "+
				"Bit-exact long-horizon match across engines is not the bar",
			b.Horizon, p.chaosHorizon)
	}
	p.stepBounds = append(p.stepBounds, b)
	return nil
}

// AddDistBound registers a distributional/qualitative ensemble bound.
func (p *AcceptancePolicy) AddDistBound(b DistBound) {
	p.distBounds = append(p.distBounds, b)
}

// ClauseResult is one evaluated acceptance clause.
type ClauseResult struct {
	Desc  string  `json:"desc"`
	Value float64 `json:"value"`
	Pass  bool    `json:"pass"`
}

// AcceptanceReport is the gate result. Refusal means not evaluated (Goodhart).
type AcceptanceReport struct {
	Pass     bool           `json:"pass"`
	Refusal  string         `json:"refusal,omitempty"`
	Clauses  []ClauseResult `json:"clauses,omitempty"`
}

// Evaluate runs the policy on a holdout batch. Refuses tune-pool batches.
func (p *AcceptancePolicy) Evaluate(br BatchResult) AcceptanceReport {
	var r AcceptanceReport
	if br.Pool != PoolHoldout {
		r.Refusal = fmt.Sprintf(
			"refused: acceptance must run on the holdout seed pool "+
				"(Goodhart guard) — this batch is %q. Fixes must trace to "+
				"structural divergences in the reference source, never "+
				"constant-tuning against the diff",
			br.Pool.String())
		return r
	}
	r.Pass = true
	for _, b := range p.stepBounds {
		hIdx := br.HorizonIndex(b.Horizon)
		if hIdx < 0 {
			r.Pass = false
			r.Clauses = append(r.Clauses, ClauseResult{
				Desc:  fmt.Sprintf("step  horizon %d missing from batch", b.Horizon),
				Pass:  false,
			})
			continue
		}
		vals := make([]float64, 0, len(br.Samples))
		for _, s := range br.Samples {
			if hIdx < len(s.Metrics) && b.Metric < len(s.Metrics[hIdx]) {
				v := s.Metrics[hIdx][b.Metric]
				if !IsNA(v) {
					vals = append(vals, v)
				}
			}
		}
		p99 := percentile(vals, 0.99)
		def := p.metricDefs[b.Metric]
		scale := def.DisplayScale
		if scale == 0 {
			scale = 1
		}
		desc := fmt.Sprintf("step  p99(%s @%dt) <= %g %s",
			def.Name, b.Horizon, b.MaxP99*scale, def.Unit)
		ok := p99 <= b.MaxP99
		r.Clauses = append(r.Clauses, ClauseResult{
			Desc:  desc,
			Value: p99 * scale,
			Pass:  ok,
		})
		r.Pass = r.Pass && ok
	}
	for _, b := range p.distBounds {
		hIdx := br.HorizonIndex(b.Horizon)
		if hIdx < 0 {
			r.Pass = false
			r.Clauses = append(r.Clauses, ClauseResult{
				Desc:  fmt.Sprintf("dist  horizon %d missing from batch", b.Horizon),
				Pass:  false,
			})
			continue
		}
		if b.Statistic == nil {
			r.Pass = false
			r.Clauses = append(r.Clauses, ClauseResult{
				Desc:  fmt.Sprintf("dist  %s @%dt (nil statistic)", b.Name, b.Horizon),
				Pass:  false,
			})
			continue
		}
		v := b.Statistic(br, hIdx)
		desc := fmt.Sprintf("dist  %s @%dt in [%g, %g]", b.Name, b.Horizon, b.Lo, b.Hi)
		ok := b.Lo <= v && v <= b.Hi
		r.Clauses = append(r.Clauses, ClauseResult{
			Desc:  desc,
			Value: v,
			Pass:  ok,
		})
		r.Pass = r.Pass && ok
	}
	return r
}

// FormatAcceptance renders an AcceptanceReport.
func FormatAcceptance(r AcceptanceReport) string {
	if r.Refusal != "" {
		return "ACCEPTANCE " + r.Refusal + "\n"
	}
	var s string
	s += "\nacceptance clauses:\n"
	for _, c := range r.Clauses {
		tag := "PASS"
		if !c.Pass {
			tag = "FAIL"
		}
		s += fmt.Sprintf("  [%s] %-58s value=%.4f\n", tag, c.Desc, c.Value)
	}
	if r.Pass {
		s += "ACCEPTANCE PASS\n"
	} else {
		s += "ACCEPTANCE FAIL\n"
	}
	return s
}

// QualRateParity returns |P(qual==q|ref) − P(qual==q|port)| at horizon,
// bounded to [0, tol]. Canonical long-horizon qualitative invariant.
func QualRateParity(name string, horizon int, q uint8, tol float64) DistBound {
	return DistBound{
		Name:    name,
		Horizon: horizon,
		Lo:      0,
		Hi:      tol,
		Statistic: func(br BatchResult, k int) float64 {
			var rc, pc int
			for _, s := range br.Samples {
				if k < len(s.QualRef) && s.QualRef[k] == q {
					rc++
				}
				if k < len(s.QualPort) && s.QualPort[k] == q {
					pc++
				}
			}
			n := float64(len(br.Samples))
			if n < 1 {
				n = 1
			}
			return math.Abs(float64(rc)/n - float64(pc)/n)
		},
	}
}

// DisagreementFraction returns the fraction of the wild tail that is a
// qualitative DISAGREEMENT (rather than bifurcation). Low = amplification
// is chaos picking phases, not a behavioural split ("same game semantics").
func DisagreementFraction(name string, horizon int, primaryMetric int, wildThreshold, tol float64) DistBound {
	return DistBound{
		Name:    name,
		Horizon: horizon,
		Lo:      0,
		Hi:      tol,
		Statistic: func(br BatchResult, k int) float64 {
			var wild, dis int
			for _, s := range br.Samples {
				if k >= len(s.Metrics) || primaryMetric >= len(s.Metrics[k]) {
					continue
				}
				v := s.Metrics[k][primaryMetric]
				if IsNA(v) || v <= wildThreshold {
					continue
				}
				wild++
				if k < len(s.QualRef) && k < len(s.QualPort) && s.QualRef[k] != s.QualPort[k] {
					dis++
				}
			}
			if wild == 0 {
				return 0
			}
			return float64(dis) / float64(wild)
		},
	}
}
