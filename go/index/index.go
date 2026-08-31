// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package index provides symbol extraction from parsed source files.
//
// It runs Tree-sitter queries (function definitions, type definitions,
// imports, and call expressions) against a ParsedFile and returns a flat list
// of Symbol values suitable for indexing in a store.
package index

import (
	"bytes"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

// Caps on Signature / Doc text. The discovery index only needs enough to make
// hits readable; the actual source is always available via the byte range.
const (
	maxSignatureBytes = 240
	maxDocBytes       = 1024
	maxDocLines       = 20
)

// Symbol is a named code entity extracted from a parsed file.
type Symbol struct {
	Name     string
	Kind     string
	FilePath string

	// Location of the whole node (1-based line/col).
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int

	// Byte offsets of the whole node.
	StartByte uint
	EndByte   uint

	// Byte offsets of the name/identifier node within the whole node.
	NameStartByte uint
	NameEndByte   uint

	// Signature is the first line of the declaration's source (truncated to
	// keep FTS rows compact). Used to power the discovery index — never the
	// authoritative source of truth.
	Signature string

	// Doc is any contiguous line-comment block immediately preceding the
	// declaration, with the language's doc-comment prefix stripped. Empty if
	// the declaration has no leading comment.
	Doc string
}

// Mode controls which symbol queries the extractor runs against a file.
type Mode int

const (
	// FullMode extracts declarations, imports, and call sites — the default
	// for project-owned source.
	FullMode Mode = iota

	// APIOnlyMode extracts only the public-facing API surface: declarations
	// (functions, types, methods, fields), and imports. Call sites are
	// skipped. Used for library-scope files where the call graph is not
	// useful and dominates the index size.
	APIOnlyMode
)

// ExtractSymbols runs the default (full) symbol extraction against a parsed
// file and returns all symbols found.
func ExtractSymbols(file *forest.ParsedFile) []Symbol {
	return ExtractSymbolsFromPartsMode(file.OriginalSource, file.Tree, file.Adapter, file.Path, FullMode)
}

// ExtractSymbolsFromParts is the decomposed form of ExtractSymbols that
// accepts raw source, tree, adapter, and path. Runs in FullMode. Retained for
// callers that don't need to switch modes.
func ExtractSymbolsFromParts(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	filePath string,
) []Symbol {
	return ExtractSymbolsFromPartsMode(source, tree, adapter, filePath, FullMode)
}

// ExtractSymbolsFromPartsMode is the full form of the extractor: caller picks
// the mode. APIOnlyMode skips call-expression queries and emits methods and
// fields in addition to top-level decls.
func ExtractSymbolsFromPartsMode(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	filePath string,
	mode Mode,
) []Symbol {
	var symbols []Symbol

	extractFromParts(source, tree, adapter, "function", adapter.FunctionDefQuery(), filePath, &symbols)

	if q := adapter.TypeDefQuery(); q != "" {
		extractFromParts(source, tree, adapter, "type", q, filePath, &symbols)
	}

	if q := adapter.ImportQuery(); q != "" {
		extractFromParts(source, tree, adapter, "import", q, filePath, &symbols)
	}

	switch mode {
	case FullMode:
		if q := adapter.CallExprQuery(); q != "" {
			extractFromParts(source, tree, adapter, "call", q, filePath, &symbols)
		}
	case APIOnlyMode:
		if q := adapter.MethodQuery(); q != "" {
			extractFromParts(source, tree, adapter, "method", q, filePath, &symbols)
		}
		if q := adapter.FieldQuery(); q != "" {
			extractFromParts(source, tree, adapter, "field", q, filePath, &symbols)
		}
	}

	return symbols
}

// wholeCaptureNames are the "whole node" capture names checked in order.
var wholeCaptureNames = []string{"func", "call", "type_def", "import", "method", "field"}

// extractFromParts runs a single query against a (source, tree, adapter)
// triple and appends discovered symbols to *out.
func extractFromParts(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	kind, queryStr, filePath string,
	out *[]Symbol,
) {
	if queryStr == "" {
		return
	}

	lang := adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return
	}
	defer query.Close()

	captureNames := query.CaptureNames()
	indexOf := func(name string) int {
		for i, n := range captureNames {
			if n == name {
				return i
			}
		}
		return -1
	}

	nameIdx := indexOf("name")
	if nameIdx < 0 {
		return
	}

	wholeIdx := nameIdx
	for _, candidate := range wholeCaptureNames {
		if idx := indexOf(candidate); idx >= 0 {
			wholeIdx = idx
			break
		}
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), source)

	for match := matches.Next(); match != nil; match = matches.Next() {
		nameNode := captureNode(match.Captures, uint32(nameIdx))
		wholeNode := captureNode(match.Captures, uint32(wholeIdx))

		if nameNode == nil || wholeNode == nil {
			continue
		}

		name := string(source[nameNode.StartByte():nameNode.EndByte()])
		if name == "" {
			continue
		}

		startPos := wholeNode.StartPosition()
		endPos := wholeNode.EndPosition()

		var sig, doc string
		// Skip signature/doc extraction for call sites — they are noisy and
		// would dwarf the rest of the FTS payload on call-heavy files.
		//
		// Anchor on the name node's line rather than wholeNode.StartByte(): in
		// some grammars (notably tree-sitter-python) the @func node's extent
		// already absorbs the preceding comments, so wholeNode.StartByte() is
		// upstream of the actual declaration line.
		if kind != "call" {
			anchor := lineStart(source, nameNode.StartByte())
			sig = extractSignature(source, anchor, wholeNode.EndByte())
			doc = extractLeadingDoc(source, anchor, adapter.DocCommentPrefix())
		}

		*out = append(*out, Symbol{
			Name:          name,
			Kind:          kind,
			FilePath:      filePath,
			StartLine:     int(startPos.Row) + 1,
			StartCol:      int(startPos.Column) + 1,
			EndLine:       int(endPos.Row) + 1,
			EndCol:        int(endPos.Column) + 1,
			StartByte:     wholeNode.StartByte(),
			EndByte:       wholeNode.EndByte(),
			NameStartByte: nameNode.StartByte(),
			NameEndByte:   nameNode.EndByte(),
			Signature:     sig,
			Doc:           doc,
		})
	}
}

// extractSignature returns the first line of source[start:end], truncated to
// maxSignatureBytes. Leading whitespace is kept so that indentation hints
// survive into the index.
func extractSignature(source []byte, start, end uint) string {
	if start >= end || int(end) > len(source) {
		return ""
	}
	chunk := source[start:end]
	if nl := bytes.IndexByte(chunk, '\n'); nl >= 0 {
		chunk = chunk[:nl]
	}
	chunk = bytes.TrimRight(chunk, " \t\r")
	if len(chunk) > maxSignatureBytes {
		chunk = chunk[:maxSignatureBytes]
	}
	return string(chunk)
}

// extractLeadingDoc walks backwards from `start` collecting consecutive
// comment lines and returns their concatenated body with the comment
// prefix stripped. Accepts both the language's preferred doc prefix (e.g.
// "///") and the universal short forms ("//", "#") so that a file using
// plain "//" comments still contributes searchable doc text.
func extractLeadingDoc(source []byte, start uint, langPrefix string) string {
	if start == 0 {
		return ""
	}
	prefixes := commentPrefixes(langPrefix)
	if len(prefixes) == 0 {
		return ""
	}

	// Walk to the beginning of the line that starts the declaration.
	bol := lineStart(source, start)
	if bol == 0 {
		return ""
	}

	var lines []string
	pos := bol
	for pos > 0 && len(lines) < maxDocLines {
		prevEnd := pos - 1 // points at the '\n' that ends the prior line
		prevStart := lineStart(source, uint(prevEnd))
		line := source[prevStart:prevEnd]
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 {
			break
		}
		var stripped string
		matched := false
		for _, p := range prefixes {
			if bytes.HasPrefix(trimmed, []byte(p)) {
				stripped = string(bytes.TrimLeft(trimmed[len(p):], " \t"))
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		lines = append(lines, stripped)
		pos = prevStart
		if pos == 0 {
			break
		}
	}

	if len(lines) == 0 {
		return ""
	}

	// Reverse so the comment reads top-to-bottom.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	out := strings.Join(lines, " ")
	if len(out) > maxDocBytes {
		out = out[:maxDocBytes]
	}
	return out
}

// commentPrefixes returns the set of line-comment prefixes to accept above a
// declaration. We accept the language's preferred doc-comment prefix plus a
// short fallback so that pragma differences (e.g. Rust's "///" vs plain "//")
// don't silently drop searchable text.
func commentPrefixes(langPrefix string) []string {
	switch langPrefix {
	case "":
		return nil
	case "#":
		return []string{"#"}
	case "//":
		return []string{"//"}
	case "///", "//!":
		return []string{"///", "//!", "//"}
	default:
		return []string{langPrefix}
	}
}

// lineStart returns the byte offset of the start of the line containing pos.
func lineStart(source []byte, pos uint) uint {
	if int(pos) > len(source) {
		pos = uint(len(source))
	}
	for i := int(pos) - 1; i >= 0; i-- {
		if source[i] == '\n' {
			return uint(i + 1)
		}
	}
	return 0
}


// captureNode returns a pointer to the node for the first capture with the
// given index, or nil.
func captureNode(captures []tree_sitter.QueryCapture, idx uint32) *tree_sitter.Node {
	for i := range captures {
		if captures[i].Index == idx {
			return &captures[i].Node
		}
	}
	return nil
}
