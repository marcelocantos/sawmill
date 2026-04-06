// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package index provides symbol extraction from parsed source files.
//
// It runs Tree-sitter queries (function definitions, type definitions,
// imports, and call expressions) against a ParsedFile and returns a flat list
// of Symbol values suitable for indexing in a store.
package index

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
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
}

// ExtractSymbols runs function_def, type_def, import, and call queries against
// the file and returns all symbols found.
func ExtractSymbols(file *forest.ParsedFile) []Symbol {
	var symbols []Symbol
	filePath := file.Path

	// Functions.
	extractWithQuery(file, "function", file.Adapter.FunctionDefQuery(), filePath, &symbols)

	// Types (only if the adapter supplies a query).
	if q := file.Adapter.TypeDefQuery(); q != "" {
		extractWithQuery(file, "type", q, filePath, &symbols)
	}

	// Imports.
	if q := file.Adapter.ImportQuery(); q != "" {
		extractWithQuery(file, "import", q, filePath, &symbols)
	}

	// Calls.
	if q := file.Adapter.CallExprQuery(); q != "" {
		extractWithQuery(file, "call", q, filePath, &symbols)
	}

	return symbols
}

// wholeCaptureNames are the "whole node" capture names checked in order.
var wholeCaptureNames = []string{"func", "call", "type_def", "import"}

// extractWithQuery runs a single query against file and appends discovered
// symbols to *out.
func extractWithQuery(file *forest.ParsedFile, kind, queryStr, filePath string, out *[]Symbol) {
	if queryStr == "" {
		return
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		// Skip — query doesn't compile for this grammar variant.
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
		// No @name capture — nothing to index.
		return
	}

	// Find the "whole node" capture; fall back to @name itself.
	wholeIdx := nameIdx
	for _, candidate := range wholeCaptureNames {
		if idx := indexOf(candidate); idx >= 0 {
			wholeIdx = idx
			break
		}
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	for match := matches.Next(); match != nil; match = matches.Next() {
		nameNode := captureNode(match.Captures, uint32(nameIdx))
		wholeNode := captureNode(match.Captures, uint32(wholeIdx))

		if nameNode == nil || wholeNode == nil {
			continue
		}

		name := string(file.OriginalSource[nameNode.StartByte():nameNode.EndByte()])
		if name == "" {
			continue
		}

		startPos := wholeNode.StartPosition()
		endPos := wholeNode.EndPosition()

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
		})
	}
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
