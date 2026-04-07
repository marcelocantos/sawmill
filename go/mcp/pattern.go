// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"regexp"
	"strings"
)

// Pattern represents a parsed pattern with literal segments and placeholders.
// The pattern language uses $name for named placeholders and bare $ (followed
// by a non-word character or end of string) for the instance placeholder.
type Pattern struct {
	Segments []PatternSegment
}

// PatternSegment is one piece of a parsed pattern — either a literal fragment,
// a placeholder, or both (literal followed by placeholder).
type PatternSegment struct {
	Literal     string // literal text to match (empty for pure placeholder)
	Placeholder string // placeholder name ("" for trailing literal, "$" for instance)
}

// placeholderRe matches $identifier or bare $.
var placeholderRe = regexp.MustCompile(`\$([a-zA-Z_]\w*)?`)

// ParsePattern parses a pattern string like "Foo{$a, $b}" into segments.
func ParsePattern(s string) *Pattern {
	var segments []PatternSegment
	lastEnd := 0

	for _, loc := range placeholderRe.FindAllStringIndex(s, -1) {
		start, end := loc[0], loc[1]
		literal := s[lastEnd:start]

		name := s[start:end]
		if name == "$" {
			// Bare $ — instance placeholder.
			segments = append(segments, PatternSegment{Literal: literal, Placeholder: "$"})
		} else {
			// $identifier — named placeholder.
			segments = append(segments, PatternSegment{Literal: literal, Placeholder: name[1:]}) // strip leading $
		}
		lastEnd = end
	}

	// Trailing literal after the last placeholder.
	if lastEnd < len(s) {
		segments = append(segments, PatternSegment{Literal: s[lastEnd:], Placeholder: ""})
	}

	// If there were no placeholders at all, the whole thing is a literal.
	if len(segments) == 0 {
		segments = append(segments, PatternSegment{Literal: s, Placeholder: ""})
	}

	return &Pattern{Segments: segments}
}

// Match tries to match source against this pattern. Returns captured bindings
// (placeholder name -> captured text) and whether the match succeeded.
// Matching is non-greedy: each placeholder captures the shortest string that
// allows the remaining pattern to match.
func (p *Pattern) Match(source string) (map[string]string, bool) {
	captures := make(map[string]string)
	return captures, p.matchFrom(source, 0, 0, captures)
}

func (p *Pattern) matchFrom(source string, srcPos int, segIdx int, captures map[string]string) bool {
	if segIdx >= len(p.Segments) {
		// All segments consumed — source must also be consumed.
		return srcPos == len(source)
	}

	seg := p.Segments[segIdx]

	// If this segment has a literal prefix, it must match exactly.
	if seg.Literal != "" {
		if !hasAtPos(source, srcPos, seg.Literal) {
			return false
		}
		srcPos += len(seg.Literal)
	}

	// If no placeholder, we've consumed the literal and move to the next segment.
	if seg.Placeholder == "" {
		return p.matchFrom(source, srcPos, segIdx+1, captures)
	}

	// Placeholder: find the shortest capture that allows the rest to match.
	// Look ahead to find the next literal boundary.
	nextLiteral := ""
	if segIdx+1 < len(p.Segments) {
		nextLiteral = p.Segments[segIdx+1].Literal
	}

	if nextLiteral == "" && segIdx+1 >= len(p.Segments) {
		// Last segment with a placeholder — capture everything remaining.
		captures[seg.Placeholder] = source[srcPos:]
		return true
	}

	// Try shortest match: from srcPos to the first occurrence of nextLiteral.
	for end := srcPos; end <= len(source); end++ {
		if nextLiteral != "" && !hasAtPos(source, end, nextLiteral) {
			continue
		}
		// Try this capture length.
		captures[seg.Placeholder] = source[srcPos:end]
		if p.matchFrom(source, end, segIdx+1, captures) {
			return true
		}
	}

	return false
}

// hasAtPos returns true if source contains literal starting at position pos.
func hasAtPos(source string, pos int, literal string) bool {
	if pos+len(literal) > len(source) {
		return false
	}
	return source[pos:pos+len(literal)] == literal
}

// Apply produces the result text by substituting captures into the replacement
// pattern. Placeholders in the replacement are replaced with their captured
// values. A bare $ is replaced with the instance name (captures["$"]).
func Apply(replacement string, captures map[string]string) string {
	var sb strings.Builder
	lastEnd := 0

	for _, loc := range placeholderRe.FindAllStringIndex(replacement, -1) {
		start, end := loc[0], loc[1]
		sb.WriteString(replacement[lastEnd:start])

		name := replacement[start:end]
		if name == "$" {
			sb.WriteString(captures["$"])
		} else {
			sb.WriteString(captures[name[1:]]) // strip leading $
		}
		lastEnd = end
	}

	sb.WriteString(replacement[lastEnd:])
	return sb.String()
}
