// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package diffharness

// SeedPool is the structural half of the Goodhart guard.
//
// Tune:    exploration, fix loops, worst-offender mining, study.
// Holdout: acceptance ONLY. AcceptancePolicy.Evaluate refuses Tune batches.
//
// The pools are provably disjoint: after a SplitMix64 finalizer decorrelates
// the two Weyl streams, the low bit is reserved as a pool tag.
type SeedPool uint8

const (
	PoolTune SeedPool = iota
	PoolHoldout
)

// String returns "tune" or "holdout".
func (p SeedPool) String() string {
	if p == PoolHoldout {
		return "holdout"
	}
	return "tune"
}

// SeedOf derives a deterministic scenario seed from a pool and index.
// Matches the C++ skeleton (TEMPLATE.md §7.1 / skeleton/diffharness.hpp).
func SeedOf(pool SeedPool, i uint64) uint64 {
	// Golden-ratio gamma for tune (matches the ge TiltBuggy harness);
	// a second SplitMix64 constant for holdout.
	var gamma uint64 = 0x9e3779b97f4a7c15
	if pool == PoolHoldout {
		gamma = 0xbf58476d1ce4e5b9
	}
	z := gamma * (i + 1)
	z ^= z >> 30
	z *= 0xbf58476d1ce4e5b9
	z ^= z >> 27
	z *= 0x94d049bb133111eb
	z ^= z >> 31
	tag := uint64(0)
	if pool == PoolHoldout {
		tag = 1
	}
	return (z << 1) | tag
}
