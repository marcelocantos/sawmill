// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"bytes"
	"strings"
)

// tryBodyMerge runs a line-level diff3 between base, ours and theirs.
// It returns the merged body (with conflict markers inlined) and a
// list of residual conflicts. The third return is always true — the
// signature mirrors a hypothetical "did we attempt the fallback" flag
// for callers that want to track Stats.DeclsTextMerged.
func tryBodyMerge(base, ours, theirs []byte, key declKey, kind string, opts Options) ([]byte, []Conflict, bool) {
	merged, hunks := diff3(base, ours, theirs)
	if len(hunks) == 0 {
		return merged, nil, true
	}
	// Wrap each conflicting hunk with markers and produce a Conflict
	// record per hunk. The merged buffer is reassembled with the
	// markers in the right place.
	var out bytes.Buffer
	var conflicts []Conflict
	cursor := 0
	for _, h := range hunks {
		out.Write(merged[cursor:h.Start])
		startByte := out.Len()
		out.Write(makeConflictMarker(h.Ours, h.Base, h.Theirs, opts))
		endByte := out.Len()
		conflicts = append(conflicts, Conflict{
			Path:  opts.Path,
			Start: startByte,
			End:   endByte,
			Kind:  kind,
			Decl:  declLabel(key),
		})
		cursor = h.End
	}
	out.Write(merged[cursor:])
	_ = key
	return out.Bytes(), conflicts, true
}

// textOnlyMerge bypasses AST analysis entirely and runs diff3 over the
// whole file. Used when a parse failure on any side would make
// declaration-level merging unsafe.
func textOnlyMerge(base, ours, theirs []byte, opts Options) Result {
	merged, hunks := diff3(base, ours, theirs)
	r := Result{}
	if len(hunks) == 0 {
		r.Merged = merged
		return r
	}
	var out bytes.Buffer
	var conflicts []Conflict
	cursor := 0
	for _, h := range hunks {
		out.Write(merged[cursor:h.Start])
		startByte := out.Len()
		out.Write(makeConflictMarker(h.Ours, h.Base, h.Theirs, opts))
		endByte := out.Len()
		conflicts = append(conflicts, Conflict{
			Path:  opts.Path,
			Start: startByte,
			End:   endByte,
			Kind:  "text",
		})
		cursor = h.End
	}
	out.Write(merged[cursor:])
	r.Merged = out.Bytes()
	r.Conflicts = conflicts
	r.Stats.Conflicts = len(conflicts)
	return r
}

// hunk is one section of the diff3 output. Start/End are byte offsets
// into the *placeholder* merged buffer (the buffer with one conflict
// placeholder per unresolved hunk: the placeholder occupies bytes
// [Start:End)). Conflicting hunks contain Base, Ours and Theirs text
// to feed into makeConflictMarker.
type hunk struct {
	Start, End   int
	Base, Ours, Theirs []byte
}

// diff3 implements a minimal line-level three-way merge.
//
// Algorithm:
//  1. Compute LCS(base, ours) and LCS(base, theirs) to align lines.
//  2. Walk base lines in order, classifying each region as either
//     "stable" (matched in both ours and theirs) or "unstable" (some
//     edit on at least one side).
//  3. For each unstable region, compare the ours-slice against the
//     theirs-slice: equal → take it; one side equals base → take the
//     other; otherwise → conflict.
//
// This is the textbook diff3 algorithm (Smith, "GNU diff3"); it is
// not as clever as merge3 in jujutsu but is sufficient for the body
// fallback and avoids a CGo dependency.
func diff3(base, ours, theirs []byte) ([]byte, []hunk) {
	bLines := splitLines(base)
	oLines := splitLines(ours)
	tLines := splitLines(theirs)

	// LCS-derived alignments: for each base line, what ours/theirs line
	// (if any) corresponds. matchOurs[i] = j means base[i] matches
	// ours[j]; -1 means no match.
	matchOurs := lcsAlign(bLines, oLines)
	matchTheirs := lcsAlign(bLines, tLines)

	var out bytes.Buffer
	var hunks []hunk
	bIdx, oIdx, tIdx := 0, 0, 0
	for bIdx < len(bLines) {
		// Find the next line where both sides agree on the match.
		runStart := bIdx
		for bIdx < len(bLines) {
			mo, mt := matchOurs[bIdx], matchTheirs[bIdx]
			// "Stable" means: this base line is matched in both
			// sides AND those matches are at the next expected
			// position (no insertions on either side relative to
			// where we are).
			if mo >= 0 && mt >= 0 && mo == oIdx+(bIdx-runStart) && mt == tIdx+(bIdx-runStart) {
				bIdx++
				continue
			}
			break
		}
		// Emit the stable run (matches in both sides).
		runLen := bIdx - runStart
		if runLen > 0 {
			for k := 0; k < runLen; k++ {
				out.Write(bLines[runStart+k])
			}
			oIdx += runLen
			tIdx += runLen
		}
		if bIdx >= len(bLines) {
			break
		}
		// Find the next stable anchor.
		anchor := bIdx
		for anchor < len(bLines) {
			mo, mt := matchOurs[anchor], matchTheirs[anchor]
			if mo >= 0 && mt >= 0 {
				break
			}
			anchor++
		}
		// Determine ours and theirs slices that align to base[bIdx:anchor].
		var oEnd, tEnd int
		if anchor < len(bLines) {
			oEnd = matchOurs[anchor]
			tEnd = matchTheirs[anchor]
		} else {
			oEnd = len(oLines)
			tEnd = len(tLines)
		}
		bSlice := bLines[bIdx:anchor]
		oSlice := oLines[oIdx:oEnd]
		tSlice := tLines[tIdx:tEnd]

		// Resolve the unstable region.
		switch {
		case linesEqual(oSlice, tSlice):
			for _, l := range oSlice {
				out.Write(l)
			}
		case linesEqual(bSlice, oSlice):
			for _, l := range tSlice {
				out.Write(l)
			}
		case linesEqual(bSlice, tSlice):
			for _, l := range oSlice {
				out.Write(l)
			}
		default:
			start := out.Len()
			// Placeholder length is 0 — caller substitutes the
			// conflict marker at this offset.
			h := hunk{
				Start:  start,
				End:    start,
				Base:   joinLines(bSlice),
				Ours:   joinLines(oSlice),
				Theirs: joinLines(tSlice),
			}
			hunks = append(hunks, h)
		}
		bIdx = anchor
		oIdx = oEnd
		tIdx = tEnd
	}
	// Trailing additions in ours/theirs after base is exhausted.
	tailO := oLines[oIdx:]
	tailT := tLines[tIdx:]
	switch {
	case linesEqual(tailO, tailT):
		for _, l := range tailO {
			out.Write(l)
		}
	case len(tailO) == 0:
		for _, l := range tailT {
			out.Write(l)
		}
	case len(tailT) == 0:
		for _, l := range tailO {
			out.Write(l)
		}
	default:
		start := out.Len()
		hunks = append(hunks, hunk{
			Start:  start,
			End:    start,
			Base:   nil,
			Ours:   joinLines(tailO),
			Theirs: joinLines(tailT),
		})
	}
	return out.Bytes(), hunks
}

// splitLines splits b into lines, keeping the trailing newline on
// each line so that joining preserves the original byte content. The
// final line may not have a trailing newline.
func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}

func joinLines(lines [][]byte) []byte {
	var out bytes.Buffer
	for _, l := range lines {
		out.Write(l)
	}
	return out.Bytes()
}

func linesEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(stripEOL(a[i]), stripEOL(b[i])) {
			return false
		}
	}
	return true
}

func stripEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// lcsAlign returns, for each line in a, the index of the matching
// line in b (or -1 if it does not appear in the LCS). Matching ignores
// trailing newlines.
//
// Implements the standard O(len(a)*len(b)) dynamic programming LCS.
// Body merges are scoped to single declarations, so quadratic in line
// count is fine.
func lcsAlign(a, b [][]byte) []int {
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		out := make([]int, la)
		for i := range out {
			out[i] = -1
		}
		return out
	}
	// dp[i][j] = LCS length of a[:i] and b[:j]
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	keyA := make([]string, la)
	keyB := make([]string, lb)
	for i, l := range a {
		keyA[i] = string(stripEOL(l))
	}
	for j, l := range b {
		keyB[j] = string(stripEOL(l))
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			if keyA[i-1] == keyB[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	out := make([]int, la)
	for i := range out {
		out[i] = -1
	}
	// Backtrack to recover alignment.
	i, j := la, lb
	for i > 0 && j > 0 {
		switch {
		case keyA[i-1] == keyB[j-1]:
			out[i-1] = j - 1
			i--
			j--
		case dp[i-1][j] >= dp[i][j-1]:
			i--
		default:
			j--
		}
	}
	return out
}

// helper to silence unused import if strings drops out — used by
// declLabel via algebra.go and by other future helpers.
var _ = strings.TrimSpace
