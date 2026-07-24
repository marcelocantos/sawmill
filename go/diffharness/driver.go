// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"fmt"
	"sync"
)

// Harness runs two engines under a shared tape, samples the Differ at
// configured horizons, and localises first divergence.
type Harness struct {
	cfg    Config
	newRef func() Engine
	newPort func() Engine
	newTape NewTape
	differ Differ
	format FormatState
}

// NewHarnessArgs constructs a Harness. All fields except Format are required.
type NewHarnessArgs struct {
	Config  Config
	NewRef  func() Engine
	NewPort func() Engine
	NewTape NewTape
	Differ  Differ
	// Format renders State for study mode; optional (defaults to fmt.Sprint).
	Format FormatState
}

// NewHarness validates config and returns a ready Harness.
func NewHarness(args *NewHarnessArgs) (*Harness, error) {
	if args == nil {
		return nil, fmt.Errorf("diffharness: NewHarnessArgs is nil")
	}
	if args.NewRef == nil || args.NewPort == nil {
		return nil, fmt.Errorf("diffharness: NewRef and NewPort are required")
	}
	if args.NewTape == nil {
		return nil, fmt.Errorf("diffharness: NewTape is required")
	}
	if args.Differ == nil {
		return nil, fmt.Errorf("diffharness: Differ is required")
	}
	cfg := args.Config
	if len(cfg.Horizons) == 0 {
		cfg = DefaultConfig()
	}
	defs := args.Differ.MetricDefs()
	if len(defs) == 0 {
		return nil, fmt.Errorf("diffharness: Differ must define at least one metric")
	}
	if cfg.PrimaryMetric < 0 || cfg.PrimaryMetric >= len(defs) {
		return nil, fmt.Errorf("diffharness: primary_metric %d out of range [0,%d)",
			cfg.PrimaryMetric, len(defs))
	}
	for i := 1; i < len(cfg.Horizons); i++ {
		if cfg.Horizons[i] <= cfg.Horizons[i-1] {
			return nil, fmt.Errorf("diffharness: horizons must be strictly ascending")
		}
	}
	if cfg.WorstCount <= 0 {
		cfg.WorstCount = 6
	}
	if cfg.StudyHeadTicks <= 0 {
		cfg.StudyHeadTicks = 12
	}
	if cfg.StudyPrintEvery <= 0 {
		cfg.StudyPrintEvery = 10
	}
	format := args.Format
	if format == nil {
		format = func(s Vec) string { return fmt.Sprint(map[string]float64(s)) }
	}
	return &Harness{
		cfg:     cfg,
		newRef:  args.NewRef,
		newPort: args.NewPort,
		newTape: args.NewTape,
		differ:  args.Differ,
		format:  format,
	}, nil
}

// Config returns a copy of the harness config.
func (h *Harness) Config() Config { return h.cfg }

// firstDivEps resolves the first-divergence epsilon.
func (h *Harness) firstDivEps() float64 {
	if h.cfg.FirstDivEpsilon > 0 {
		return h.cfg.FirstDivEpsilon
	}
	if !IsNA(h.cfg.WildThreshold) && h.cfg.WildThreshold > 0 &&
		h.cfg.WildThreshold < 1e300 {
		return h.cfg.WildThreshold
	}
	return 1e-9
}

// RunScenario drives both engines for ticks steps under seed.
func (h *Harness) RunScenario(seed uint64, ticks int) ScenarioSample {
	tape := h.newTape(seed)
	ref := h.newRef()
	port := h.newPort()

	init := tape.Init()
	ref.Reset(init)
	port.Reset(init)
	refPrev := ref.Observe()
	portPrev := port.Observe()

	nH := len(h.cfg.Horizons)
	nM := len(h.differ.MetricDefs())
	s := ScenarioSample{
		Seed:     seed,
		Metrics:  make([][]float64, nH),
		QualRef:  make([]uint8, nH),
		QualPort: make([]uint8, nH),
	}
	for k := range s.Metrics {
		s.Metrics[k] = make([]float64, nM)
		for m := range s.Metrics[k] {
			s.Metrics[k][m] = NA()
		}
	}

	horizonSet := make(map[int]int, nH)
	for k, hz := range h.cfg.Horizons {
		horizonSet[hz] = k
	}
	eps := h.firstDivEps()
	pm := h.cfg.PrimaryMetric

	for t := 0; t < ticks; t++ {
		// Invariant 2: input materialised ONCE and fed to both.
		in := tape.Next(t)
		ref.Step(in)
		port.Step(in)
		refCur := ref.Observe()
		portCur := port.Observe()
		d := h.differ.Diff(refPrev, refCur, portPrev, portCur)

		tick := t + 1
		if k, ok := horizonSet[tick]; ok {
			// Copy metrics (pad if differ returned short slice).
			for m := 0; m < nM; m++ {
				if m < len(d.Metrics) {
					s.Metrics[k][m] = d.Metrics[m]
				} else {
					s.Metrics[k][m] = NA()
				}
			}
			s.QualRef[k] = d.QualRef
			s.QualPort[k] = d.QualPort
		}

		// First-divergence localisation (earliest tick past epsilon).
		if s.FirstDiv == nil && pm < len(d.Metrics) {
			v := d.Metrics[pm]
			if !IsNA(v) && v > eps {
				s.FirstDiv = &FirstDivergence{
					Tick:      tick,
					Metric:    v,
					RefState:  refCur.Clone(),
					PortState: portCur.Clone(),
					QualRef:   d.QualRef,
					QualPort:  d.QualPort,
				}
			}
		}

		refPrev = refCur
		portPrev = portCur
	}
	return s
}

// Batch runs nScenarios scenarios from the given pool in parallel.
func (h *Harness) Batch(pool SeedPool, nScenarios int, ticks int, nThreads int) BatchResult {
	if nScenarios < 0 {
		nScenarios = 0
	}
	if nThreads < 1 {
		nThreads = 1
	}
	br := BatchResult{
		Pool:     pool,
		Ticks:    ticks,
		Horizons: append([]int(nil), h.cfg.Horizons...),
		Samples:  make([]ScenarioSample, nScenarios),
	}
	if nScenarios == 0 {
		return br
	}

	var next atomicCounter
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for {
			i := next.Inc()
			if i >= uint64(nScenarios) {
				return
			}
			br.Samples[i] = h.RunScenario(SeedOf(pool, i), ticks)
		}
	}
	for t := 0; t < nThreads; t++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	return br
}

// atomicCounter is a tiny work-stealing counter (avoids importing sync/atomic
// into the public surface for a one-liner).
type atomicCounter struct {
	mu sync.Mutex
	n  uint64
}

func (c *atomicCounter) Inc() uint64 {
	c.mu.Lock()
	i := c.n
	c.n++
	c.mu.Unlock()
	return i
}
