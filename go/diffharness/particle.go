// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

import (
	"fmt"
	"math"
	"math/rand"
)

// Particle demo: 1-D box with exponential damping (reference) vs linear drag
// (port). Reproduces the TiltBuggy diagnostic shape in miniature:
//
//   - Reference: semi-implicit Euler + v *= exp(-damping·dt)  (Chipmunk-like
//     velocity operation; TiltBuggy's steering damper class).
//   - Port:      explicit Euler + linear drag force.
//   - Wall bounces are decision boundaries: tiny per-step error eventually
//     flips bounce timing → discontinuous trajectory separation (1-D stand-in
//     for Lyapunov amplification).
//
// Mapping to TiltBuggy (squz/ge sample/tiltbuggy/oracle):
//
//	particle pos error  ↔  buggy position / heading error
//	|v| > 3 "fast" bit  ↔  |spin| > 2 rad/s fishtail bit
//	wall bounce cascade ↔  chaos / grip-regime bifurcation
//	horizons {1,3,10,30,60,90}  ↔  identical ladder (frames)
//
// The correct port (Damping=0.8 matching reference) has a tiny linear
// short-horizon floor; a biased port (e.g. Damping=0.6) fails calibrated
// step bounds. Long-horizon equality is not required.

const (
	particleDt      = 1.0 / 60.0
	particleBoxHalf = 10.0
	particleMass    = 1.0
	particleDamp    = 0.8 // 1/s
	particleBounce  = 0.9
	particleSeg     = 30 // force resampled every 0.5 s
)

// ParticleTape implements Tape for the particle demo.
type ParticleTape struct {
	init  Vec
	force float64
	rng   *rand.Rand
}

// NewParticleTape builds a deterministic tape from seed.
func NewParticleTape(seed uint64) Tape {
	rng := rand.New(rand.NewSource(int64(seed))) //nolint:gosec // deterministic harness seed
	x0 := -0.7*particleBoxHalf + rng.Float64()*(1.4*particleBoxHalf)
	v0 := -5 + rng.Float64()*10
	return &ParticleTape{
		init: Vec{"x": x0, "v": v0},
		rng:  rng,
	}
}

// Init returns start pose.
func (t *ParticleTape) Init() Vec { return t.init.Clone() }

// Next returns the force for this tick (resampled every particleSeg ticks).
func (t *ParticleTape) Next(tick int) Vec {
	if tick%particleSeg == 0 {
		t.force = -30 + t.rng.Float64()*60
	}
	return Vec{"force": t.force}
}

// particleBase holds shared box/bounce geometry.
type particleBase struct {
	x, v    float64
	bounces int
}

func (p *particleBase) reset(init Vec) {
	p.x = init["x"]
	p.v = init["v"]
	p.bounces = 0
}

func (p *particleBase) bounce() {
	if p.x > particleBoxHalf {
		p.x = 2*particleBoxHalf - p.x
		p.v = -particleBounce * p.v
		p.bounces++
	}
	if p.x < -particleBoxHalf {
		p.x = -2*particleBoxHalf - p.x
		p.v = -particleBounce * p.v
		p.bounces++
	}
}

func (p *particleBase) observe() Vec { return Vec{"x": p.x} }

func (p *particleBase) probe() string {
	return fmt.Sprintf("b=%d", p.bounces)
}

// ParticleRef is the reference: exponential velocity damping.
type ParticleRef struct {
	particleBase
	damping float64
}

// NewParticleRef returns a reference engine with the canonical damping.
func NewParticleRef() Engine {
	return &ParticleRef{damping: particleDamp}
}

// Reset implements Engine.
func (e *ParticleRef) Reset(init Vec) { e.reset(init) }

// Step implements Engine.
func (e *ParticleRef) Step(in Vec) {
	e.v += in["force"] / particleMass * particleDt
	e.v *= math.Exp(-e.damping * particleDt)
	e.x += e.v * particleDt
	e.bounce()
}

// Observe implements Engine.
func (e *ParticleRef) Observe() Vec { return e.observe() }

// Probe implements Engine.
func (e *ParticleRef) Probe() string { return e.probe() }

// ParticlePort is the port: linear drag under explicit Euler.
// Damping should match particleDamp for the "correct" port; use a different
// value to simulate a structural/parameter bug for mutation tests.
type ParticlePort struct {
	particleBase
	Damping float64 // linear drag coefficient
}

// NewParticlePort returns a correct port (damping matches reference).
func NewParticlePort() Engine {
	return &ParticlePort{Damping: particleDamp}
}

// NewBiasedParticlePort returns a systematically biased port (wrong drag).
// Used in mutation checks: acceptance must fail.
func NewBiasedParticlePort(damping float64) func() Engine {
	return func() Engine {
		return &ParticlePort{Damping: damping}
	}
}

// Reset implements Engine.
func (e *ParticlePort) Reset(init Vec) { e.reset(init) }

// Step implements Engine.
func (e *ParticlePort) Step(in Vec) {
	e.v += (in["force"]/particleMass - e.Damping*e.v) * particleDt
	e.x += e.v * particleDt
	e.bounce()
}

// Observe implements Engine.
func (e *ParticlePort) Observe() Vec { return e.observe() }

// Probe implements Engine.
func (e *ParticlePort) Probe() string { return e.probe() }

// ParticleDiffer computes pos and finite-difference velocity error.
type ParticleDiffer struct{}

// MetricDefs implements Differ.
func (ParticleDiffer) MetricDefs() []MetricDef {
	return []MetricDef{
		{Name: "pos", Unit: "m", DisplayScale: 1},
		{Name: "vel", Unit: "m/s", DisplayScale: 1},
	}
}

// Diff implements Differ.
func (ParticleDiffer) Diff(rp, rc, pp, pc Vec) TickDiff {
	rv := (rc["x"] - rp["x"]) / particleDt
	pv := (pc["x"] - pp["x"]) / particleDt
	d := TickDiff{
		Metrics: []float64{
			math.Abs(rc["x"] - pc["x"]),
			math.Abs(rv - pv),
		},
	}
	// Qualitative: "moving fast" — 1-D analogue of TiltBuggy fishtail.
	if math.Abs(rv) > 3 {
		d.QualRef = 1
	}
	if math.Abs(pv) > 3 {
		d.QualPort = 1
	}
	return d
}

// FormatParticleState renders a particle state for study mode.
func FormatParticleState(s Vec) string {
	return fmt.Sprintf("x=%+8.4f", s["x"])
}

// ParticleConfig returns a Config tuned for the particle demo / TiltBuggy shape.
func ParticleConfig() Config {
	cfg := DefaultConfig()
	cfg.PrimaryMetric = 0 // pos
	cfg.WildThreshold = 1.0
	cfg.DisagreeHorizon = 30
	cfg.StudyAttentionThreshold = 0.5
	cfg.FirstDivEpsilon = 0.01 // first meaningful position split
	return cfg
}

// NewParticleHarness builds a harness comparing ParticleRef vs a port factory.
func NewParticleHarness(newPort func() Engine) (*Harness, error) {
	return NewHarness(&NewHarnessArgs{
		Config:  ParticleConfig(),
		NewRef:  NewParticleRef,
		NewPort: newPort,
		NewTape: NewParticleTape,
		Differ:  ParticleDiffer{},
		Format:  FormatParticleState,
	})
}

// ParticleAcceptance builds a calibrated policy for the correct particle port.
// Chaos horizon 10 is the measured knee for this demo (not for TiltBuggy —
// measure yours). Step bounds are ~2× the measured p99 floor of the correct
// port; a biased port (drag 0.6) must fail them.
func ParticleAcceptance() (*AcceptancePolicy, error) {
	p, err := NewAcceptancePolicy(&NewPolicyArgs{
		ChaosHorizon: 10,
		MetricDefs:   ParticleDiffer{}.MetricDefs(),
	})
	if err != nil {
		return nil, err
	}
	// Calibrated to correct-port floor with headroom (see skeleton example_main).
	if err := p.AddStepBound(StepBound{Horizon: 1, Metric: 0, MaxP99: 0.0002}); err != nil {
		return nil, err
	}
	if err := p.AddStepBound(StepBound{Horizon: 3, Metric: 0, MaxP99: 0.0015}); err != nil {
		return nil, err
	}
	if err := p.AddStepBound(StepBound{Horizon: 10, Metric: 0, MaxP99: 0.012}); err != nil {
		return nil, err
	}
	p.AddDistBound(QualRateParity("|P(fast|ref)-P(fast|port)|", 90, 1, 0.05))
	p.AddDistBound(DisagreementFraction("disagreement fraction of wild tail", 90, 0, 1.0, 0.25))
	return p, nil
}
