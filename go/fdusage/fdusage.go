// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package fdusage measures how much of the process's file-descriptor budget
// sawmill is using.
//
// This exists because of a specific failure. On macOS, fsnotify's kqueue
// backend opens one descriptor per watched file, so descriptor usage scales
// with the size of every watched tree rather than with anything the daemon
// does. A daemon once sat at 245,780 open descriptors against a
// kern.maxfilesperproc of 245,760 — 92% of the machine's entire file table.
// At that point accept() fails with EMFILE, so the listener accepted
// connections and immediately reset them, and the MCP server was unreachable
// while looking, from the outside, like a network fault.
//
// Nothing measured it, so nothing noticed. The point of this package is that
// the daemon watches its own budget and says so before the ceiling arrives.
package fdusage

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// Budget is the fraction of the effective limit that counts as healthy.
// Crossing it is not a failure — it is the point at which the number stops
// being background noise and starts being the thing to look at first.
const Budget = 0.5

// fdDir is where the kernel exposes this process's open descriptors.
func fdDir() string {
	if runtime.GOOS == "linux" {
		return "/proc/self/fd"
	}
	return "/dev/fd" // darwin and the BSDs
}

// Count returns the number of descriptors this process has open.
//
// It reads the kernel's own listing rather than counting lsof rows: on macOS,
// lsof reports a single descriptor on many lines, which inflated an early
// reading of this very problem by a factor of twenty-four.
func Count() (int, error) {
	dir := fdDir()
	f, err := os.Open(dir)
	if err != nil {
		return 0, fmt.Errorf("fdusage: opening %s: %w", dir, err)
	}
	defer f.Close()

	// Readdirnames, not ReadDir: this directory is a live view of the
	// descriptor table, so stat-ing each entry races with the very
	// descriptor doing the reading and fails with EBADF on darwin.
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, fmt.Errorf("fdusage: reading %s: %w", dir, err)
	}
	// The listing includes f's own descriptor, which the caller does not own.
	return len(names) - 1, nil
}

// Limit returns the effective per-process descriptor ceiling: the soft
// RLIMIT_NOFILE, which is what open(2) enforces for this process.
func Limit() (uint64, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return 0, fmt.Errorf("fdusage: getrlimit: %w", err)
	}
	return uint64(rl.Cur), nil
}

// Usage is a point-in-time reading.
type Usage struct {
	Open     int
	Limit    uint64
	Fraction float64
}

// OverBudget reports whether usage has crossed Budget.
func (u Usage) OverBudget() bool { return u.Fraction > Budget }

// String renders a reading for a log line.
func (u Usage) String() string {
	return fmt.Sprintf("%d/%d descriptors (%.1f%% of limit)", u.Open, u.Limit, u.Fraction*100)
}

// Read takes a reading. A zero limit yields a zero fraction rather than a
// division by zero.
func Read() (Usage, error) {
	open, err := Count()
	if err != nil {
		return Usage{}, err
	}
	limit, err := Limit()
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Open: open, Limit: limit}
	if limit > 0 {
		u.Fraction = float64(open) / float64(limit)
	}
	return u, nil
}
