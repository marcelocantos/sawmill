// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import "strings"

// expandFTSQuery turns a bare term list ("parse connection") into a prefix-
// match FTS5 query ("parse* connection*"), so a search for "parse" matches
// "Parser" and "parseConnection" the way users expect.
//
// If the caller already wrote an FTS5 expression (one containing any of the
// FTS5 operators: ', ", *, :, (, ), - or one of the keywords AND/OR/NOT/NEAR),
// the query is passed through verbatim.
func expandFTSQuery(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return q
	}
	if hasFTS5Operator(q) {
		return q
	}
	parts := strings.Fields(q)
	for i, p := range parts {
		if !isPlainTerm(p) {
			continue
		}
		parts[i] = p + "*"
	}
	return strings.Join(parts, " ")
}

func hasFTS5Operator(q string) bool {
	if strings.ContainsAny(q, `"*:()-^`) {
		return true
	}
	for _, kw := range []string{" AND ", " OR ", " NOT ", " NEAR "} {
		if strings.Contains(" "+q+" ", kw) {
			return true
		}
	}
	return false
}

// isPlainTerm reports whether s is a bare token suitable for prefix
// expansion. Anything containing punctuation (already trimmed of FTS5
// operators) is passed through verbatim.
func isPlainTerm(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '_' {
			return false
		}
	}
	return s != ""
}
