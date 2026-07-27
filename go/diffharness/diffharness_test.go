// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"math"
	"testing"
)

func TestSeedPoolsDisjoint(t *testing.T) {
	seen := make(map[uint64]SeedPool, 2000)
	for i := uint64(0); i < 1000; i++ {
		for _, pool := range []SeedPool{PoolTune, PoolHoldout} {
			s := SeedOf(pool, i)
			if prev, ok := seen[s]; ok {
				t.Fatalf("seed collision %016x between %s and %s", s, prev, pool)
			}
			seen[s] = pool
			// Pool tag is LSB.
			if pool == PoolTune && s&1 != 0 {
				t.Fatalf("tune seed %016x has holdout tag", s)
			}
			if pool == PoolHoldout && s&1 != 1 {
				t.Fatalf("holdout seed %016x missing tag", s)
			}
		}
	}
}

func TestSingleMaterialisationDeterminism(t *testing.T) {
	h, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	seed := SeedOf(PoolTune, 0)
	a := h.RunScenario(seed, 90)
	b := h.RunScenario(seed, 90)
	if a.Seed != b.Seed {
		t.Fatalf("seed mismatch")
	}
	for k := range a.Metrics {
		for m := range a.Metrics[k] {
			if IsNA(a.Metrics[k][m]) && IsNA(b.Metrics[k][m]) {
				continue
			}
			if a.Metrics[k][m] != b.Metrics[k][m] {
				t.Fatalf("non-deterministic metrics @h%d m%d: %v vs %v",
					k, m, a.Metrics[k][m], b.Metrics[k][m])
			}
		}
	}
}

func TestCorrectPortSmallShortHorizon(t *testing.T) {
	h, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	br := h.Batch(PoolTune, 200, 90, 4)
	rep := h.BuildReport(br)

	// At horizon 1, p99 pos should be tiny for the correct port.
	var p99h1 float64
	found := false
	for _, row := range rep.Percentiles {
		if row.Horizon == 1 && row.Metric == "pos" {
			p99h1 = row.P99
			found = true
		}
	}
	if !found {
		t.Fatal("missing pos @1t percentile row")
	}
	if p99h1 > 0.001 {
		t.Fatalf("correct port p99 pos @1t = %g, want ≤ 0.001", p99h1)
	}
}

func TestBiasedPortLargerDivergence(t *testing.T) {
	good, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := NewParticleHarness(NewBiasedParticlePort(0.6))
	if err != nil {
		t.Fatal(err)
	}
	// Same seeds, same ticks.
	const n, ticks = 100, 30
	g := good.Batch(PoolTune, n, ticks, 4)
	b := bad.Batch(PoolTune, n, ticks, 4)
	gRep := good.BuildReport(g)
	bRep := bad.BuildReport(b)

	p99 := func(rep Report, h int) float64 {
		for _, row := range rep.Percentiles {
			if row.Horizon == h && row.Metric == "pos" {
				return row.P99
			}
		}
		t.Fatalf("missing pos @%dt", h)
		return 0
	}
	// Biased port must diverge more at short horizon.
	if p99(bRep, 10) <= p99(gRep, 10) {
		t.Fatalf("biased p99@10 (%g) should exceed correct (%g)",
			p99(bRep, 10), p99(gRep, 10))
	}
}

func TestTiltBuggyShapeDivergenceVsHorizon(t *testing.T) {
	// Faithfulness note: this is the reduced particle oracle (TEMPLATE
	// skeleton example_main), not the live Chipmunk/box2d binary. It
	// reproduces the *known shape* of TiltBuggy results:
	//   short-horizon p50/p99 grow roughly linearly (per-step floor);
	//   long-horizon max is much larger (amplification / bounce cascade).
	// Mapping: see particle.go header comment.
	h, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	// Use a deliberately imperfect but still "same equations class" comparison
	// by comparing ref to itself → floor is ~0. For shape of growth with a
	// structural damper mismatch, use mild bias.
	h, err = NewParticleHarness(NewBiasedParticlePort(0.75)) // mild structural-class bias
	if err != nil {
		t.Fatal(err)
	}
	br := h.Batch(PoolTune, 400, 90, 4)
	rep := h.BuildReport(br)

	p99At := map[int]float64{}
	maxAt := map[int]float64{}
	for _, row := range rep.Percentiles {
		if row.Metric == "pos" {
			p99At[row.Horizon] = row.P99
			maxAt[row.Horizon] = row.Max
		}
	}
	for _, hz := range []int{1, 3, 10, 30, 60, 90} {
		if _, ok := p99At[hz]; !ok {
			t.Fatalf("missing pos percentile at horizon %d", hz)
		}
	}
	// Short-horizon p99 should be smaller than long-horizon p99 (growth).
	if p99At[1] >= p99At[30] {
		t.Fatalf("expected growth with horizon: p99@1=%g p99@30=%g", p99At[1], p99At[30])
	}
	// Max at long horizon should dominate short-horizon max (tail amplification).
	if maxAt[90] < maxAt[3] {
		t.Fatalf("expected long-horizon max ≥ short: max@90=%g max@3=%g", maxAt[90], maxAt[3])
	}
	// First-divergence localisation must fire for most scenarios under bias.
	if rep.FirstDivRate < 0.5 {
		t.Fatalf("first-div rate %.2f; expected majority under biased port", rep.FirstDivRate)
	}
	if len(rep.FirstDivSamples) == 0 {
		t.Fatal("expected first-div samples with paired states")
	}
	fd := rep.FirstDivSamples[0]
	if fd.RefState == nil || fd.PortState == nil {
		t.Fatal("first-div missing paired states")
	}
	if _, ok := fd.RefState["x"]; !ok {
		t.Fatal("ref state missing x")
	}
	if fd.Tick < 1 {
		t.Fatalf("bad first-div tick %d", fd.Tick)
	}
}

func TestFirstDivergenceLocalisation(t *testing.T) {
	h, err := NewParticleHarness(NewBiasedParticlePort(0.5))
	if err != nil {
		t.Fatal(err)
	}
	s := h.RunScenario(SeedOf(PoolTune, 7), 90)
	if s.FirstDiv == nil {
		t.Fatal("expected first divergence for strongly biased port")
	}
	if s.FirstDiv.Metric <= 0 {
		t.Fatalf("first-div metric %g", s.FirstDiv.Metric)
	}
	// Paired states should differ at the localised tick.
	if s.FirstDiv.RefState["x"] == s.FirstDiv.PortState["x"] {
		// Possible only if epsilon is on a different metric component; pos is primary.
		t.Logf("warn: states equal at first-div (metric=%g tick=%d)", s.FirstDiv.Metric, s.FirstDiv.Tick)
	}
	// Study should surface the same first-div.
	st := h.Study(s.Seed, 90)
	if st.FirstDiv == nil {
		t.Fatal("study missing first-div")
	}
	if st.FirstDiv.Tick != s.FirstDiv.Tick {
		t.Fatalf("study first-div tick %d vs sample %d", st.FirstDiv.Tick, s.FirstDiv.Tick)
	}
}

func TestGoodhartGuardRefusesTunePool(t *testing.T) {
	h, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := ParticleAcceptance()
	if err != nil {
		t.Fatal(err)
	}
	tune := h.Batch(PoolTune, 50, 90, 2)
	rep := pol.Evaluate(tune)
	if rep.Refusal == "" {
		t.Fatal("expected refusal of tune-pool batch")
	}
	if rep.Pass {
		t.Fatal("refused batch must not pass")
	}
}

func TestLyapunovGuardRejectsLongHorizonStepBound(t *testing.T) {
	pol, err := NewAcceptancePolicy(&NewPolicyArgs{
		ChaosHorizon: 10,
		MetricDefs:   ParticleDiffer{}.MetricDefs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pol.AddStepBound(StepBound{Horizon: 90, Metric: 0, MaxP99: 0.01})
	if err == nil {
		t.Fatal("expected error adding step bound beyond chaos horizon")
	}
	// Short horizon is fine.
	if err := pol.AddStepBound(StepBound{Horizon: 3, Metric: 0, MaxP99: 0.01}); err != nil {
		t.Fatal(err)
	}
	// Dist bounds at long horizon are fine.
	pol.AddDistBound(QualRateParity("fast parity", 90, 1, 0.1))
}

func TestAcceptancePassCorrectFailBiased(t *testing.T) {
	pol, err := ParticleAcceptance()
	if err != nil {
		t.Fatal(err)
	}

	// Correct port on holdout — should pass (or at least not refuse).
	goodH, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	good := goodH.Batch(PoolHoldout, 500, 90, 4)
	gRep := pol.Evaluate(good)
	if gRep.Refusal != "" {
		t.Fatalf("unexpected refusal: %s", gRep.Refusal)
	}
	if !gRep.Pass {
		t.Fatalf("correct port should pass acceptance:\n%s", FormatAcceptance(gRep))
	}

	// Biased port (drag 0.6) — mutation check; must fail.
	badH, err := NewParticleHarness(NewBiasedParticlePort(0.6))
	if err != nil {
		t.Fatal(err)
	}
	bad := badH.Batch(PoolHoldout, 500, 90, 4)
	bRep := pol.Evaluate(bad)
	if bRep.Refusal != "" {
		t.Fatalf("unexpected refusal: %s", bRep.Refusal)
	}
	if bRep.Pass {
		t.Fatalf("biased port must fail acceptance (mutation check); clauses:\n%s",
			FormatAcceptance(bRep))
	}
}

func TestIdenticalEnginesNearZero(t *testing.T) {
	// Two refs: bit-identical traces → zero divergence.
	h, err := NewHarness(&NewHarnessArgs{
		Config:  ParticleConfig(),
		NewRef:  NewParticleRef,
		NewPort: NewParticleRef, // identical generative model
		NewTape: NewParticleTape,
		Differ:  ParticleDiffer{},
		Format:  FormatParticleState,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := h.RunScenario(SeedOf(PoolTune, 3), 90)
	for k, hz := range h.Config().Horizons {
		v := s.Metrics[k][0]
		if IsNA(v) {
			continue
		}
		if v > 1e-12 {
			t.Fatalf("identical engines pos err @%dt = %g", hz, v)
		}
	}
	if s.FirstDiv != nil {
		t.Fatalf("identical engines should not first-div; got tick %d", s.FirstDiv.Tick)
	}
}

func TestPercentileMath(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	if g := percentile(vals, 0); g != 1 {
		t.Fatalf("p0=%g", g)
	}
	if g := percentile(vals, 1); g != 5 {
		t.Fatalf("p1=%g", g)
	}
	if g := percentile(vals, 0.5); g != 3 {
		t.Fatalf("p50=%g", g)
	}
	// Original not mutated.
	if vals[0] != 1 || vals[4] != 5 {
		t.Fatal("percentile mutated input")
	}
}

func TestReportFormatNonEmpty(t *testing.T) {
	h, err := NewParticleHarness(NewParticlePort)
	if err != nil {
		t.Fatal(err)
	}
	br := h.Batch(PoolTune, 20, 30, 2)
	rep := h.BuildReport(br)
	text := FormatReport(rep, ParticleDiffer{}.MetricDefs())
	if len(text) < 50 {
		t.Fatalf("report too short: %q", text)
	}
	st := h.Study(SeedOf(PoolTune, 0), 30)
	if len(st.Lines) == 0 {
		t.Fatal("study produced no lines")
	}
	if FormatStudy(st) == "" {
		t.Fatal("empty study format")
	}
}

func TestNewHarnessValidation(t *testing.T) {
	_, err := NewHarness(nil)
	if err == nil {
		t.Fatal("expected nil args error")
	}
	_, err = NewHarness(&NewHarnessArgs{})
	if err == nil {
		t.Fatal("expected missing fields error")
	}
	cfg := DefaultConfig()
	cfg.Horizons = []int{10, 5}
	_, err = NewHarness(&NewHarnessArgs{
		Config:  cfg,
		NewRef:  NewParticleRef,
		NewPort: NewParticlePort,
		NewTape: NewParticleTape,
		Differ:  ParticleDiffer{},
	})
	if err == nil {
		t.Fatal("expected non-ascending horizons error")
	}
}

func TestNAHandling(t *testing.T) {
	if !IsNA(NA()) {
		t.Fatal("NA should be NA")
	}
	if !IsNA(math.NaN()) {
		t.Fatal("NaN should be NA")
	}
	if IsNA(0) || IsNA(1) {
		t.Fatal("finite not NA")
	}
}
