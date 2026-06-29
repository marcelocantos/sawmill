// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// Edge is one outgoing reference recorded in the symbol_refs table. Its
// source is determined later by byte containment against the file's
// function/method symbols; its destination is identified by name only and
// resolved via name-join at query time.
type Edge struct {
	Kind      string // "call", "type_use", "import_use"
	DstName   string // raw name as captured
	StartByte uint
	EndByte   uint
	StartLine int
	StartCol  int
}

// EdgeKind constants for symbol_refs.kind.
const (
	EdgeCall      = "call"
	EdgeTypeUse   = "type_use"
	EdgeImportUse = "import_use"
)

// ExtractEdges runs the call/import/type_use queries over a parsed file and
// returns all outgoing edges. Call sites and imports are extracted in all
// modes; type-use edges are skipped in APIOnlyMode because library-scope code
// doesn't need a cross-call graph.
func ExtractEdges(file *forest.ParsedFile) []Edge {
	return ExtractEdgesFromParts(file.OriginalSource, file.Tree, file.Adapter, FullMode)
}

// ExtractEdgesFromParts is the decomposed form of ExtractEdges that accepts
// raw source, tree, and adapter. mode controls which queries are run.
func ExtractEdgesFromParts(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	mode Mode,
) []Edge {
	var edges []Edge
	if q := adapter.CallExprQuery(); q != "" {
		edges = append(edges, extractEdgeQuery(source, tree, adapter, q, EdgeCall)...)
	}
	if q := adapter.ImportQuery(); q != "" {
		edges = append(edges, extractEdgeQuery(source, tree, adapter, q, EdgeImportUse)...)
	}
	if mode == FullMode {
		if q := adapter.TypeUseQuery(); q != "" {
			edges = append(edges, extractEdgeQuery(source, tree, adapter, q, EdgeTypeUse)...)
		}
	}
	return edges
}

func extractEdgeQuery(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	queryStr, kind string,
) []Edge {
	lang := adapter.Language()
	query, err := tree_sitter.NewQuery(lang, queryStr)
	if err != nil {
		return nil
	}
	defer query.Close()

	captureNames := query.CaptureNames()
	nameIdx := -1
	for i, n := range captureNames {
		if n == "name" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	var edges []Edge
	matches := cursor.Matches(query, tree.RootNode(), source)
	for match := matches.Next(); match != nil; match = matches.Next() {
		nameNode := captureNode(match.Captures, uint32(nameIdx))
		if nameNode == nil {
			continue
		}
		name := string(source[nameNode.StartByte():nameNode.EndByte()])
		if name == "" {
			continue
		}
		pos := nameNode.StartPosition()
		edges = append(edges, Edge{
			Kind:      kind,
			DstName:   name,
			StartByte: nameNode.StartByte(),
			EndByte:   nameNode.EndByte(),
			StartLine: int(pos.Row) + 1,
			StartCol:  int(pos.Column) + 1,
		})
	}
	return edges
}
