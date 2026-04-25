// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package jsengine provides a QuickJS-based transform engine that executes
// user-supplied JavaScript functions against matched AST nodes.
//
// The JS function receives a node object and may return:
//   - The original node (unchanged) → no edit
//   - null → delete the node
//   - A string → replace the node's text entirely
//   - node.replaceText(...), node.replaceName(...), etc. → specific mutation
package jsengine

import (
	"encoding/json"
	"fmt"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
	"modernc.org/quickjs"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/rewrite"
)

// matchData holds pre-computed information about a query match, serialised to
// JSON and injected into the JS runtime.
type matchData struct {
	StartByte          uint    `json:"startByte"`
	EndByte            uint    `json:"endByte"`
	NameStartByte      *uint   `json:"nameStartByte"`
	NameEndByte        *uint   `json:"nameEndByte"`
	BodyStartByte      *uint   `json:"bodyStartByte"`
	BodyEndByte        *uint   `json:"bodyEndByte"`
	Text               string  `json:"text"`
	Name               *string `json:"name"`
	Body               *string `json:"body"`
	Parameters         *string `json:"parameters"`
	Kind               string  `json:"kind"`
	File               string  `json:"file"`
	StartLine          int     `json:"startLine"`
	EndLine            int     `json:"endLine"`
	Indent             string  `json:"indent"`
	HasTrailingNewline bool    `json:"hasTrailingNewline"`
}

// jsEdit is the JSON structure returned from the JS program.
type jsEdit struct {
	Start       uint   `json:"start"`
	End         uint   `json:"end"`
	Replacement string `json:"replacement"`
}

// jsHelpers defines the node constructor injected into every transform context.
const jsHelpers = `
globalThis.__makeNode = function(props) {
    var n = Object.assign({}, props);
    n.replaceText = function(text) { return { _mutation_type: "replaceText", _mutation: text }; };
    n.replaceBody = function(body) { return { _mutation_type: "replaceBody", _mutation: body }; };
    n.replaceName = function(name) { return { _mutation_type: "replaceName", _mutation: name }; };
    n.remove = function() { return null; };
    n.wrap = function(before, after) { return { _mutation_type: "wrap", _before: before, _after: after }; };
    n.insertBefore = function(code) { return { _mutation_type: "insertBefore", _mutation: code }; };
    n.insertAfter = function(code) { return { _mutation_type: "insertAfter", _mutation: code }; };
    return n;
};
`

// RunJSTransform executes a JavaScript transform function against nodes
// matching a Tree-sitter query, returning the transformed source bytes.
func RunJSTransform(
	source []byte,
	tree *tree_sitter.Tree,
	queryStr string,
	transformFn string,
	filePath string,
	adapter adapters.LanguageAdapter,
) ([]byte, error) {
	lang := adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil, fmt.Errorf("compiling query %q: %v", queryStr, qErr)
	}
	defer query.Close()

	targetIdx, nameIdx := resolveCaptureIndices(query)

	matches := collectMatches(source, tree, query, targetIdx, nameIdx, filePath)
	if len(matches) == 0 {
		return source, nil
	}

	matchesJSON, err := json.Marshal(matches)
	if err != nil {
		return nil, fmt.Errorf("marshaling matches: %w", err)
	}

	// Build a single JS program that processes all matches and returns edits
	// as a JSON array. This avoids per-match Go↔JS round-trips.
	program := fmt.Sprintf(`%s

var __edits = [];
var __transformFn = (%s);
var __matches = %s;

for (var i = 0; i < __matches.length; i++) {
    var m = __matches[i];
    var node = __makeNode(m);
    var result = __transformFn(node);

    if (result === null) {
        __edits.push({
            start: m.startByte,
            end: m.hasTrailingNewline ? m.endByte + 1 : m.endByte,
            replacement: ""
        });
    } else if (typeof result === "string") {
        __edits.push({start: m.startByte, end: m.endByte, replacement: result});
    } else if (result && result._mutation_type) {
        switch (result._mutation_type) {
        case "replaceText":
            __edits.push({start: m.startByte, end: m.endByte, replacement: result._mutation});
            break;
        case "replaceBody":
            if (m.bodyStartByte !== null && m.bodyStartByte !== undefined) {
                __edits.push({start: m.bodyStartByte, end: m.bodyEndByte, replacement: result._mutation});
            }
            break;
        case "replaceName":
            if (m.nameStartByte !== null && m.nameStartByte !== undefined) {
                __edits.push({start: m.nameStartByte, end: m.nameEndByte, replacement: result._mutation});
            }
            break;
        case "remove":
            __edits.push({
                start: m.startByte,
                end: m.hasTrailingNewline ? m.endByte + 1 : m.endByte,
                replacement: ""
            });
            break;
        case "wrap":
            __edits.push({start: m.startByte, end: m.endByte, replacement: result._before + m.text + result._after});
            break;
        case "insertBefore":
            __edits.push({start: m.startByte, end: m.startByte, replacement: result._mutation + "\n" + m.indent});
            break;
        case "insertAfter":
            __edits.push({start: m.endByte, end: m.endByte, replacement: "\n" + m.indent + result._mutation});
            break;
        }
    }
}
JSON.stringify(__edits);
`, jsHelpers, transformFn, string(matchesJSON))

	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("creating QuickJS VM: %w", err)
	}
	defer vm.Close()

	result, err := vm.Eval(program, quickjs.EvalGlobal)
	if err != nil {
		return nil, fmt.Errorf("executing transform: %w", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		return source, nil
	}

	var edits []jsEdit
	if err := json.Unmarshal([]byte(resultStr), &edits); err != nil {
		return nil, fmt.Errorf("parsing edit results: %w", err)
	}

	if len(edits) == 0 {
		return source, nil
	}

	rwEdits := make([]rewrite.Edit, len(edits))
	for i, e := range edits {
		rwEdits[i] = rewrite.Edit{
			Start:       e.Start,
			End:         e.End,
			Replacement: e.Replacement,
		}
	}

	return rewrite.ApplyEdits(source, rwEdits), nil
}

// preferredCaptureNames are checked in order for the "whole node" capture.
var preferredCaptureNames = []string{"func", "call", "type_def", "import"}

// resolveCaptureIndices determines the target and name capture indices from
// a Tree-sitter query.
func resolveCaptureIndices(query *tree_sitter.Query) (targetIdx uint32, nameIdx int) {
	captureNames := query.CaptureNames()
	indexOf := func(name string) int {
		for i, n := range captureNames {
			if n == name {
				return i
			}
		}
		return -1
	}

	for _, name := range preferredCaptureNames {
		if idx := indexOf(name); idx >= 0 {
			targetIdx = uint32(idx)
			nameIdx = indexOf("name")
			return
		}
	}

	nameIdx = indexOf("name")
	return
}

// collectMatches runs the query and builds the match data with all byte ranges
// pre-computed for injection into the JS runtime.
func collectMatches(
	source []byte,
	tree *tree_sitter.Tree,
	query *tree_sitter.Query,
	targetIdx uint32,
	nameIdx int,
	filePath string,
) []matchData {
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	qmatches := cursor.Matches(query, tree.RootNode(), source)

	var matches []matchData
	for m := qmatches.Next(); m != nil; m = qmatches.Next() {
		var targetNode *tree_sitter.Node
		for i := range m.Captures {
			if m.Captures[i].Index == targetIdx {
				targetNode = &m.Captures[i].Node
				break
			}
		}
		if targetNode == nil {
			continue
		}

		var nameNode *tree_sitter.Node
		if nameIdx >= 0 {
			for i := range m.Captures {
				if m.Captures[i].Index == uint32(nameIdx) {
					nameNode = &m.Captures[i].Node
					break
				}
			}
		}

		md := matchData{
			StartByte:          targetNode.StartByte(),
			EndByte:            targetNode.EndByte(),
			Text:               string(source[targetNode.StartByte():targetNode.EndByte()]),
			Kind:               targetNode.Kind(),
			File:               filePath,
			StartLine:          int(targetNode.StartPosition().Row) + 1,
			EndLine:            int(targetNode.EndPosition().Row) + 1,
			Indent:             detectIndent(source, targetNode.StartByte()),
			HasTrailingNewline: targetNode.EndByte() < uint(len(source)) && source[targetNode.EndByte()] == '\n',
		}

		if nameNode != nil {
			nsb := nameNode.StartByte()
			neb := nameNode.EndByte()
			md.NameStartByte = &nsb
			md.NameEndByte = &neb
			nameText := string(source[nsb:neb])
			md.Name = &nameText
		}

		if bodyNode := targetNode.ChildByFieldName("body"); bodyNode != nil {
			bsb := bodyNode.StartByte()
			beb := bodyNode.EndByte()
			md.BodyStartByte = &bsb
			md.BodyEndByte = &beb
			bodyText := string(source[bsb:beb])
			md.Body = &bodyText
		}

		if paramsNode := targetNode.ChildByFieldName("parameters"); paramsNode != nil {
			paramsText := string(source[paramsNode.StartByte():paramsNode.EndByte()])
			md.Parameters = &paramsText
		}

		matches = append(matches, md)
	}

	return matches
}

// detectIndent returns the whitespace prefix of the line containing offset.
func detectIndent(source []byte, offset uint) string {
	lineStart := 0
	for i := int(offset) - 1; i >= 0; i-- {
		if source[i] == '\n' {
			lineStart = i + 1
			break
		}
	}
	var sb strings.Builder
	for i := lineStart; i < int(offset); i++ {
		if source[i] == ' ' || source[i] == '\t' {
			sb.WriteByte(source[i])
		} else {
			break
		}
	}
	return sb.String()
}
