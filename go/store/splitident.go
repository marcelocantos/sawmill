// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import "strings"

// splitIdentifier returns the input identifier joined with all of its
// camelCase / snake_case constituent words, separated by spaces, so that the
// default FTS5 tokeniser indexes each subword individually.
//
//	parseConnection      -> "parseConnection parse Connection"
//	parse_connection     -> "parse_connection parse connection"
//	getHTTPResponse      -> "getHTTPResponse get HTTP Response"
//
// Returned string is suitable as the input to an FTS5 column; the default
// unicode61 tokeniser will further lower-case and split on punctuation.
func splitIdentifier(s string) string {
	if s == "" {
		return ""
	}
	parts := []string{s}
	parts = append(parts, splitCamelSnake(s)...)
	return strings.Join(parts, " ")
}

// splitCamelSnake returns the camelCase / snake_case subwords of s, in order.
// "fooBar"     -> ["foo", "Bar"]
// "foo_bar"    -> ["foo", "bar"]
// "HTTPProxy"  -> ["HTTP", "Proxy"]
// "ID"         -> ["ID"]
func splitCamelSnake(s string) []string {
	var out []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.':
			flush()
		case isUpper(r):
			// Boundary cases:
			//   "aA"   -> split before "A"        (camelCase boundary)
			//   "AAa"  -> split before the LAST upper run before a lower  (acronym -> Capitalised)
			//   "AA"   -> keep together                                   (acronym continues)
			if i > 0 {
				prev := runes[i-1]
				next := rune(0)
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if isLower(prev) || isDigit(prev) {
					flush()
				} else if isUpper(prev) && isLower(next) {
					flush()
				}
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
