// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// EvidenceFor builds the evidence token bag for a symbol given its name, the
// file path it lives in, and the raw bytes of its whole-node body. The result
// is lowercased, deduplicated, sorted, and space-padded on both ends so that
// `LIKE '% alias %'` queries match exact tokens.
//
// Token sources:
//   - Symbol name, split into CamelCase + snake_case pieces.
//   - File-path basename and parent directory, split the same way.
//   - All alphanumeric/underscore runs inside the body bytes — captures
//     identifiers, doc/comment words, string-literal contents, and imported
//     type names without needing a per-language extractor.
//
// Bodies are truncated at EvidenceBodyLimit bytes to bound the work for very
// large symbols. The truncation is at a byte boundary; the tokenizer never
// emits a partial token because it splits on non-token boundaries before
// indexing.
func EvidenceFor(name, filePath string, body []byte) string {
	tokens := make(map[string]struct{}, 32)

	addSplit(tokens, name)
	addSplit(tokens, filepath.Base(filePath))
	if parent := filepath.Base(filepath.Dir(filePath)); parent != "" && parent != "." && parent != "/" {
		addSplit(tokens, parent)
	}

	body = clipBody(body, EvidenceBodyLimit)
	addBodyTokens(tokens, body)

	if len(tokens) == 0 {
		return ""
	}

	words := make([]string, 0, len(tokens))
	for t := range tokens {
		words = append(words, t)
	}
	// Sort for stable storage so equal evidence compares equal byte-for-byte.
	// SQLite blob comparison and human diffs both benefit; query correctness
	// does not depend on order.
	sort.Strings(words)

	var sb strings.Builder
	sb.Grow(1 + len(words)*8)
	sb.WriteByte(' ')
	for _, w := range words {
		sb.WriteString(w)
		sb.WriteByte(' ')
	}
	return sb.String()
}

// EvidenceBodyLimit caps the body bytes scanned per symbol. 16 KiB covers
// almost all real-world function/method/type definitions; beyond that the
// marginal concept signal is low.
const EvidenceBodyLimit = 16 * 1024

// MinTokenLen filters out very short tokens (single letters and 'i', 'a',
// etc.) that would dominate evidence rows without helping concept matching.
const MinTokenLen = 2

// addSplit splits an identifier-like word on CamelCase and snake_case
// boundaries and adds each lowercased piece (≥ MinTokenLen) to the set. The
// whole lowercased word is also added so that exact-name queries still hit.
func addSplit(out map[string]struct{}, word string) {
	word = strings.TrimSpace(word)
	if word == "" {
		return
	}
	lower := strings.ToLower(word)
	if len(lower) >= MinTokenLen {
		out[lower] = struct{}{}
	}
	for _, piece := range splitIdentifier(word) {
		piece = strings.ToLower(piece)
		if len(piece) >= MinTokenLen {
			out[piece] = struct{}{}
		}
	}
}

// splitIdentifier breaks an identifier on CamelCase and non-alphanumeric
// separators. "OnSwipeGesture_v2" → ["On", "Swipe", "Gesture", "v2"];
// "v2Beta" → ["v2", "Beta"]; "HTTPClient" stays as one token since acronym
// runs read better whole.
func splitIdentifier(s string) []string {
	var out []string
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			out = append(out, string(buf))
			buf = buf[:0]
		}
	}
	// prevSplittable is true after a lowercase letter or digit — the next
	// uppercase starts a new word. Acronym runs (consecutive uppercase) stay
	// together because uppercase letters don't set prevSplittable.
	prevSplittable := false
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			if prevSplittable {
				flush()
			}
			buf = append(buf, r)
			prevSplittable = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			buf = append(buf, r)
			prevSplittable = true
		default:
			flush()
			prevSplittable = false
		}
	}
	flush()
	return out
}

// addBodyTokens walks body bytes and adds every alphanumeric/underscore run
// (lowercased, ≥ MinTokenLen) to the set. This is language-agnostic and
// captures identifiers, comment words, and string-literal contents in one
// pass. Stopwords are skipped to keep evidence rows compact.
func addBodyTokens(out map[string]struct{}, body []byte) {
	var start int = -1
	for i := 0; i <= len(body); i++ {
		var c byte
		if i < len(body) {
			c = body[i]
		}
		if i < len(body) && isTokenByte(c) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tok := strings.ToLower(string(body[start:i]))
			if len(tok) >= MinTokenLen && !isStopword(tok) {
				out[tok] = struct{}{}
			}
			start = -1
		}
	}
}

// isTokenByte reports whether the byte can be part of an identifier-like
// token. We only need 7-bit ASCII because source files in supported
// languages keep keywords and identifiers in ASCII; UTF-8 continuation
// bytes are non-token under this rule, which is the same effect as
// treating them as separators (matches Tree-sitter's identifier rules
// closely enough for evidence purposes).
func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '_':
		return true
	}
	return false
}

// clipBody returns body truncated to at most limit bytes. The tokenizer's
// next non-token boundary still flushes a clean token, so truncating
// mid-token is harmless beyond losing one possibly-partial word.
func clipBody(body []byte, limit int) []byte {
	if len(body) > limit {
		return body[:limit]
	}
	return body
}

// stopwords is a small set of high-frequency language keywords and noise
// words that would otherwise inflate evidence rows. Concept queries almost
// never use these as anchors — they would have no discrimination power
// against the bulk of the codebase.
var stopwords = map[string]struct{}{
	// Common control / declaration keywords across supported languages.
	"if": {}, "else": {}, "for": {}, "while": {}, "do": {}, "switch": {},
	"case": {}, "default": {}, "break": {}, "continue": {}, "return": {},
	"func": {}, "function": {}, "fn": {}, "def": {}, "class": {}, "struct": {},
	"interface": {}, "type": {}, "enum": {}, "var": {}, "let": {}, "const": {},
	"new": {}, "this": {}, "self": {}, "super": {}, "true": {}, "false": {},
	"null": {}, "none": {}, "nil": {}, "void": {}, "auto": {},
	"import": {}, "from": {}, "package": {}, "module": {}, "use": {},
	"as": {}, "in": {}, "is": {}, "and": {}, "or": {}, "not": {},
	"pub": {}, "public": {}, "private": {}, "protected": {}, "static": {},
	"final": {}, "extern": {}, "inline": {},
	// Common short noise words.
	"the": {}, "to": {}, "of": {}, "on": {}, "at": {}, "by": {}, "an": {},
	"it": {}, "be": {}, "we": {}, "us": {}, "you": {},
}

func isStopword(tok string) bool {
	_, ok := stopwords[tok]
	return ok
}
