// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import (
	"bytes"
	"errors"
	"time"
)

// MaxFileSize is the largest source file, in bytes, that ShouldParse will
// accept. Files above this are skipped before tree-sitter is invoked.
// The threshold accommodates large generated source (protobuf, TLA+ models)
// while excluding multi-megabyte bundle output.
const MaxFileSize = 4 * 1024 * 1024

// MaxAvgLineLength is the highest average line length, in bytes, that
// ShouldParse will accept. Minified bundles average thousands of bytes per
// line — well past anything a human writes — and reliably hang the GLR
// parser's retryFullParseWithDFA fallback.
const MaxAvgLineLength = 1000

// MaxParseDuration bounds a single ParseSource call. ShouldParse's size and
// line-length tests catch minified bundles, but they cannot catch everything:
// the GLR parser's cost compounds across the constructs within one file, so an
// ordinary-looking 19 KB bash script has been observed to consume a core
// indefinitely while the size heuristics wave it through. No cheap property of
// the source predicts that, so the parse itself is what needs the bound.
// The value sits well above any legitimate parse — the slowest real file
// observed is a 950 KB vendored C++ header at ~8s — so that the bound only
// ever converts a hang into a skipped file, never drops a file that would
// have parsed. The stall it permits is paid at most once per distinct
// content, since a source that reaches the bound is quarantined.
const MaxParseDuration = 20 * time.Second

// ErrParseTimeout is returned by ParseSource when a parse hits
// MaxParseDuration. The parser yields a partial tree in that case; ParseSource
// discards it, so callers should skip the file exactly as they skip one that
// ShouldParse rejects.
var ErrParseTimeout = errors.New("parse exceeded MaxParseDuration")

// ShouldParse reports whether source bytes are safe to hand to tree-sitter.
// It rejects oversized files and files whose average line length exceeds
// MaxAvgLineLength.
func ShouldParse(source []byte) bool {
	if len(source) > MaxFileSize {
		return false
	}
	lines := bytes.Count(source, []byte{'\n'}) + 1
	return len(source)/lines <= MaxAvgLineLength
}
