// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// BuildReport aggregates a batch into the divergence-vs-horizon picture
// (TEMPLATE.md §2.6) plus first-divergence summary.
func (h *Harness) BuildReport(br BatchResult) Report {
	defs := h.differ.MetricDefs()
	total := len(br.Samples)
	rep := Report{
		NScenarios: total,
		Ticks:      br.Ticks,
		Pool:       br.Pool.String(),
	}
	if total == 0 {
		return rep
	}

	// 1. Percentile table.
	for k, hz := range br.Horizons {
		for m, def := range defs {
			vals := make([]float64, 0, total)
			for _, s := range br.Samples {
				if k < len(s.Metrics) && m < len(s.Metrics[k]) {
					v := s.Metrics[k][m]
					if !IsNA(v) {
						vals = append(vals, v)
					}
				}
			}
			if len(vals) == 0 {
				continue
			}
			scale := def.DisplayScale
			if scale == 0 {
				scale = 1
			}
			rep.Percentiles = append(rep.Percentiles, PercentileRow{
				Horizon: hz,
				Metric:  def.Name,
				Unit:    def.Unit,
				P50:     percentile(vals, 0.50) * scale,
				P90:     percentile(vals, 0.90) * scale,
				P99:     percentile(vals, 0.99) * scale,
				Max:     maxFloat(vals) * scale,
				N:       len(vals),
			})
		}
	}

	// 2. Wild-tail semantic breakdown.
	pm := h.cfg.PrimaryMetric
	for k, hz := range br.Horizons {
		wild, agree, differ := 0, 0, 0
		for _, s := range br.Samples {
			if k >= len(s.Metrics) || pm >= len(s.Metrics[k]) {
				continue
			}
			v := s.Metrics[k][pm]
			if IsNA(v) || v <= h.cfg.WildThreshold {
				continue
			}
			wild++
			if k < len(s.QualRef) && k < len(s.QualPort) && s.QualRef[k] == s.QualPort[k] {
				agree++
			} else {
				differ++
			}
		}
		row := WildTailRow{Horizon: hz, WildCount: wild, DisagreeCount: differ}
		if total > 0 {
			row.WildPct = 100 * float64(wild) / float64(total)
		}
		if wild > 0 {
			row.QualAgreePct = 100 * float64(agree) / float64(wild)
			row.QualDifferPct = 100 * float64(differ) / float64(wild)
		}
		rep.WildTail = append(rep.WildTail, row)
	}

	// 3. Worst disagreements at DisagreeHorizon.
	hIdx := br.HorizonIndex(h.cfg.DisagreeHorizon)
	if hIdx >= 0 {
		type pair struct {
			v    float64
			seed uint64
		}
		var dis []pair
		for _, s := range br.Samples {
			if hIdx >= len(s.Metrics) || pm >= len(s.Metrics[hIdx]) {
				continue
			}
			v := s.Metrics[hIdx][pm]
			if IsNA(v) || v <= h.cfg.WildThreshold {
				continue
			}
			if hIdx < len(s.QualRef) && hIdx < len(s.QualPort) &&
				s.QualRef[hIdx] != s.QualPort[hIdx] {
				dis = append(dis, pair{v, s.Seed})
			}
		}
		sort.Slice(dis, func(i, j int) bool { return dis[i].v > dis[j].v })
		n := h.cfg.WorstCount
		if n > len(dis) {
			n = len(dis)
		}
		scale := 1.0
		if pm < len(defs) && defs[pm].DisplayScale != 0 {
			scale = defs[pm].DisplayScale
		}
		for i := 0; i < n; i++ {
			rep.WorstOffenders = append(rep.WorstOffenders, WorstOffender{
				Seed:          dis[i].seed,
				PrimaryMetric: dis[i].v * scale,
				Horizon:       h.cfg.DisagreeHorizon,
			})
		}
	}

	// 4. First-divergence summary.
	var ticks []float64
	var samples []FirstDivergence
	for _, s := range br.Samples {
		if s.FirstDiv != nil {
			ticks = append(ticks, float64(s.FirstDiv.Tick))
			if len(samples) < h.cfg.WorstCount {
				samples = append(samples, *s.FirstDiv)
			}
		}
	}
	if total > 0 {
		rep.FirstDivRate = float64(len(ticks)) / float64(total)
	}
	if len(ticks) > 0 {
		rep.FirstDivMedianTick = percentile(ticks, 0.50)
	}
	rep.FirstDivSamples = samples

	return rep
}

// FormatReport renders a Report as human-readable text (CLI / MCP).
func FormatReport(rep Report, defs []MetricDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Differential harness: %d scenarios x %d ticks (%s pool)\n",
		rep.NScenarios, rep.Ticks, rep.Pool)

	fmt.Fprintf(&b, "\nhorizon  metric            (unit)      p50      p90      p99      max     n\n")
	for _, r := range rep.Percentiles {
		fmt.Fprintf(&b, "%5dt   %-16s (%-5s) %8.3f %8.3f %8.3f %8.3f %6d\n",
			r.Horizon, r.Metric, r.Unit, r.P50, r.P90, r.P99, r.Max, r.N)
	}

	pmName := "primary"
	pmUnit := ""
	if len(defs) > 0 {
		// Infer from first percentile row if possible.
		for _, r := range rep.Percentiles {
			pmName = r.Metric
			pmUnit = r.Unit
			break
		}
	}
	fmt.Fprintf(&b, "\nwild tail (%s, unit %s) — qualitative agreement breakdown:\n", pmName, pmUnit)
	fmt.Fprintf(&b, "horizon  wild%%   of wild: qual-agree(bifurcation)  qual-differ(DISAGREEMENT)\n")
	for _, w := range rep.WildTail {
		if w.WildCount == 0 {
			fmt.Fprintf(&b, "%5dt    0.0%%   (none)\n", w.Horizon)
			continue
		}
		fmt.Fprintf(&b, "%5dt   %4.1f%%   %6.1f%%                    %6.1f%%\n",
			w.Horizon, w.WildPct, w.QualAgreePct, w.QualDifferPct)
	}

	fmt.Fprintf(&b, "\nworst %d disagreements (run study on seed):\n", len(rep.WorstOffenders))
	for _, w := range rep.WorstOffenders {
		fmt.Fprintf(&b, "  %016x  primary=%.3f @%dt\n", w.Seed, w.PrimaryMetric, w.Horizon)
	}

	fmt.Fprintf(&b, "\nfirst-divergence: rate=%.1f%%  median_tick=%.1f\n",
		100*rep.FirstDivRate, rep.FirstDivMedianTick)
	for _, fd := range rep.FirstDivSamples {
		fmt.Fprintf(&b, "  tick=%d metric=%.4g ref=%v port=%v\n",
			fd.Tick, fd.Metric, fd.RefState, fd.PortState)
	}
	return b.String()
}

// StudyLine is one printed tick of study mode.
type StudyLine struct {
	Tick      int     `json:"tick"`
	RefState  string  `json:"ref_state"`
	PortState string  `json:"port_state"`
	Primary   float64 `json:"primary"` // display units; -1 if NA
	RefProbe  string  `json:"ref_probe"`
	PortProbe string  `json:"port_probe"`
	QualSplit bool    `json:"qual_split"`
}

// StudyResult is the structured study-mode output.
type StudyResult struct {
	Seed  uint64      `json:"seed"`
	Ticks int         `json:"ticks"`
	Lines []StudyLine `json:"lines"`
	// FirstDiv is the first tick past epsilon, if any, with raw paired states.
	FirstDiv *FirstDivergence `json:"first_divergence,omitempty"`
}

// Study replays one seed with paired state + probes (TEMPLATE.md §2.7).
func (h *Harness) Study(seed uint64, ticks int) StudyResult {
	tape := h.newTape(seed)
	ref := h.newRef()
	port := h.newPort()
	init := tape.Init()
	ref.Reset(init)
	port.Reset(init)
	refPrev := ref.Observe()
	portPrev := port.Observe()

	defs := h.differ.MetricDefs()
	pm := h.cfg.PrimaryMetric
	scale := 1.0
	if pm < len(defs) && defs[pm].DisplayScale != 0 {
		scale = defs[pm].DisplayScale
	}
	eps := h.firstDivEps()

	out := StudyResult{Seed: seed, Ticks: ticks}
	for t := 0; t < ticks; t++ {
		in := tape.Next(t)
		ref.Step(in)
		port.Step(in)
		refCur := ref.Observe()
		portCur := port.Observe()
		d := h.differ.Diff(refPrev, refCur, portPrev, portCur)

		var primary float64 = -1
		spike := false
		if pm < len(d.Metrics) {
			v := d.Metrics[pm]
			if !IsNA(v) {
				primary = v * scale
				spike = v > h.cfg.StudyAttentionThreshold
			}
			if out.FirstDiv == nil && !IsNA(v) && v > eps {
				out.FirstDiv = &FirstDivergence{
					Tick:      t + 1,
					Metric:    v,
					RefState:  refCur.Clone(),
					PortState: portCur.Clone(),
					QualRef:   d.QualRef,
					QualPort:  d.QualPort,
				}
			}
		}

		if t < h.cfg.StudyHeadTicks || (h.cfg.StudyPrintEvery > 0 && t%h.cfg.StudyPrintEvery == 0) || spike {
			out.Lines = append(out.Lines, StudyLine{
				Tick:      t,
				RefState:  h.format(refCur),
				PortState: h.format(portCur),
				Primary:   primary,
				RefProbe:  ref.Probe(),
				PortProbe: port.Probe(),
				QualSplit: d.QualRef != d.QualPort,
			})
		}
		refPrev = refCur
		portPrev = portCur
	}
	return out
}

// FormatStudy renders StudyResult as text.
func FormatStudy(sr StudyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "study seed %016x  (%d ticks)\n", sr.Seed, sr.Ticks)
	fmt.Fprintf(&b, "tick   REF                       PORT                      primary  probes\n")
	for _, ln := range sr.Lines {
		split := ""
		if ln.QualSplit {
			split = "  <-- qual split"
		}
		fmt.Fprintf(&b, "%4d   %-25s %-25s %7.3f  [ref %s | port %s]%s\n",
			ln.Tick, ln.RefState, ln.PortState, ln.Primary, ln.RefProbe, ln.PortProbe, split)
	}
	if sr.FirstDiv != nil {
		fmt.Fprintf(&b, "\nfirst divergence @ tick %d  metric=%.6g\n  ref=%v\n  port=%v\n",
			sr.FirstDiv.Tick, sr.FirstDiv.Metric, sr.FirstDiv.RefState, sr.FirstDiv.PortState)
	}
	return b.String()
}

// percentile returns the p-quantile of vals (p in [0,1]) via sort.
// Mutates a copy; vals is not modified.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	idx := int(p * float64(len(cp)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func maxFloat(vals []float64) float64 {
	m := math.Inf(-1)
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	if math.IsInf(m, -1) {
		return 0
	}
	return m
}
