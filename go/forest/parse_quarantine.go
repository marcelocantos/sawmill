// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import (
	"crypto/sha256"
	"sync"
)

// MaxQuarantineEntries bounds the quarantine's memory. Evicting the oldest
// entry only costs a re-parse, which the deadline already bounds, so a modest
// cap is safe.
const MaxQuarantineEntries = 4096

// parseQuarantine remembers source content that has already blown the parse
// deadline so that seeing it again costs a map lookup rather than another
// MaxParseDuration of a burnt core.
//
// It is keyed by content hash rather than path deliberately. A file that gets
// edited is retried automatically — the fix for a pathological file is usually
// to change it — while a file that keeps being touched without changing (the
// watcher's steady state) stays cheap. Two paths holding identical bytes also
// share the one verdict.
type parseQuarantine struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]struct{}
	order   [][sha256.Size]byte // insertion order, for FIFO eviction
}

var quarantine = &parseQuarantine{
	entries: make(map[[sha256.Size]byte]struct{}),
}

// has reports whether source has previously exceeded the parse deadline.
func (q *parseQuarantine) has(source []byte) bool {
	key := sha256.Sum256(source)
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.entries[key]
	return ok
}

// add records source as having exceeded the parse deadline. It reports whether
// this was the first time, so callers can log the event once rather than on
// every repeat.
func (q *parseQuarantine) add(source []byte) bool {
	key := sha256.Sum256(source)
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.entries[key]; ok {
		return false
	}
	if len(q.order) >= MaxQuarantineEntries {
		delete(q.entries, q.order[0])
		q.order = q.order[1:]
	}
	q.entries[key] = struct{}{}
	q.order = append(q.order, key)
	return true
}

// QuarantinedCount returns how many distinct sources are currently quarantined
// for exceeding MaxParseDuration. A steadily climbing count means the parser is
// meeting input it cannot handle, and is worth surfacing in diagnostics.
func QuarantinedCount() int {
	quarantine.mu.Lock()
	defer quarantine.mu.Unlock()
	return len(quarantine.entries)
}

// ResetQuarantine clears the quarantine. Intended for tests.
func ResetQuarantine() {
	quarantine.mu.Lock()
	defer quarantine.mu.Unlock()
	quarantine.entries = make(map[[sha256.Size]byte]struct{})
	quarantine.order = nil
}
