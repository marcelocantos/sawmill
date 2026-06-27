// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import "bytes"

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
