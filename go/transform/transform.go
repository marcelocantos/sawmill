// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package transform provides the match/act transformation engine.
//
// Combines two orthogonal dimensions:
//   - Matching: abstract (kind/name/scope) or raw Tree-sitter query
//   - Acting: declarative actions (replace, wrap, remove, etc.)
package transform

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// Match describes how to find nodes. It is either Abstract (resolved
// per-language by the adapter) or Raw (a verbatim Tree-sitter S-expression).
type Match struct {
	// Abstract fields — set when using abstract matching.
	Kind string
	Name string // empty means no filter; may contain '*' glob
	File string // restrict to files whose path contains this substring

	// Raw fields — set when using a raw Tree-sitter query.
	RawQuery string
	Capture  string // which capture to act on; defaults to first whole-node capture

	// IsRaw distinguishes the two variants.
	IsRaw bool
}

// AbstractMatch constructs an abstract Match.
func AbstractMatch(kind, name, file string) *Match {
	return &Match{Kind: kind, Name: name, File: file}
}

// RawMatch constructs a raw Match.
func RawMatch(rawQuery, capture string) *Match {
	return &Match{IsRaw: true, RawQuery: rawQuery, Capture: capture}
}

// Action describes what to do with each matched node.
type ActionKind int

const (
	ActionReplace ActionKind = iota
	ActionWrap
	ActionUnwrap
	ActionPrependStatement
	ActionAppendStatement
	ActionRemove
	ActionReplaceName
	ActionReplaceBody
)

// Action holds the type and parameters for a transform action.
type Action struct {
	Kind   ActionKind
	Code   string // Replace, PrependStatement, AppendStatement, ReplaceName, ReplaceBody
	Before string // Wrap
	After  string // Wrap
}

// Replace returns a Replace action.
func Replace(code string) *Action { return &Action{Kind: ActionReplace, Code: code} }

// Wrap returns a Wrap action.
func Wrap(before, after string) *Action { return &Action{Kind: ActionWrap, Before: before, After: after} }

// Unwrap returns an Unwrap action.
func Unwrap() *Action { return &Action{Kind: ActionUnwrap} }

// PrependStatement returns a PrependStatement action.
func PrependStatement(code string) *Action { return &Action{Kind: ActionPrependStatement, Code: code} }

// AppendStatement returns an AppendStatement action.
func AppendStatement(code string) *Action { return &Action{Kind: ActionAppendStatement, Code: code} }

// Remove returns a Remove action.
func Remove() *Action { return &Action{Kind: ActionRemove} }

// ReplaceName returns a ReplaceName action.
func ReplaceName(code string) *Action { return &Action{Kind: ActionReplaceName, Code: code} }

// ReplaceBody returns a ReplaceBody action.
func ReplaceBody(code string) *Action { return &Action{Kind: ActionReplaceBody, Code: code} }

// ResolveQueryStr converts a Match into a Tree-sitter query string for the
// given adapter.
func ResolveQueryStr(adapter adapters.LanguageAdapter, matchSpec *Match) (string, error) {
	if matchSpec.IsRaw {
		return matchSpec.RawQuery, nil
	}
	return resolveAbstractQuery(adapter, matchSpec.Kind, matchSpec.Name)
}

// resolveAbstractQuery maps an abstract kind + optional name filter to a
// concrete Tree-sitter query string.
func resolveAbstractQuery(adapter adapters.LanguageAdapter, kind, name string) (string, error) {
	var baseQuery string
	switch kind {
	case "function":
		baseQuery = adapter.FunctionDefQuery()
	case "call":
		baseQuery = adapter.CallExprQuery()
	case "class", "struct", "type":
		baseQuery = adapter.TypeDefQuery()
	case "import", "include":
		baseQuery = adapter.ImportQuery()
	default:
		return "", fmt.Errorf("unsupported abstract kind: %s", kind)
	}

	if name == "" {
		return baseQuery, nil
	}

	if strings.Contains(name, "*") {
		// Glob → regex predicate.
		regex := strings.NewReplacer(".", `\.`, "*", ".*").Replace(name)
		return fmt.Sprintf("(%s (#match? @name \"^%s$\"))", baseQuery, regex), nil
	}
	return fmt.Sprintf("(%s (#eq? @name \"%s\"))", baseQuery, name), nil
}

// TransformFile finds all nodes matching matchSpec in file and applies action,
// returning the transformed source bytes.
func TransformFile(file *forest.ParsedFile, matchSpec *Match, action *Action) ([]byte, error) {
	return TransformSource(file.OriginalSource, file.Tree, file.Adapter, file.Path, matchSpec, action)
}

// TransformSource is the decomposed form of TransformFile that accepts raw
// source, tree, and adapter. Use this with FileAccessor.WithTree.
func TransformSource(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	path string,
	matchSpec *Match,
	action *Action,
) ([]byte, error) {
	edits, err := collectEditsFromParts(source, tree, adapter, path, matchSpec, action)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return source, nil
	}
	return applyEdits(source, edits)
}

// QueryFile finds all nodes matching matchSpec in file and returns query
// results without making any edits.
func QueryFile(file *forest.ParsedFile, matchSpec *Match) ([]forest.QueryResult, error) {
	return QuerySource(file.OriginalSource, file.Tree, file.Adapter, file.Path, matchSpec)
}

// QuerySource is the decomposed form of QueryFile that accepts raw source,
// tree, and adapter.
func QuerySource(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	path string,
	matchSpec *Match,
) ([]forest.QueryResult, error) {
	queryStr, captureName, err := resolveMatchSpecFromParts(adapter, path, matchSpec)
	if err != nil {
		return nil, err
	}
	if queryStr == "" {
		return nil, nil
	}

	lang := adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil, fmt.Errorf("compiling query %q: %v", queryStr, qErr)
	}
	defer query.Close()

	targetIdx, nameIdx, err := resolveCaptureIndices(query, captureName)
	if err != nil {
		return nil, err
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), source)

	var results []forest.QueryResult
	for match := matches.Next(); match != nil; match = matches.Next() {
		targetNode := findCapture(match.Captures, targetIdx)
		if targetNode == nil {
			continue
		}

		nameText := ""
		if nameIdx >= 0 {
			if nameNode := findCapture(match.Captures, uint32(nameIdx)); nameNode != nil {
				nameText = string(source[nameNode.StartByte():nameNode.EndByte()])
			}
		}

		text := string(source[targetNode.StartByte():targetNode.EndByte()])
		if len(text) > 200 {
			text = text[:200] + "..."
		}

		startPos := targetNode.StartPosition()
		results = append(results, forest.QueryResult{
			Path:      path,
			StartLine: uint(startPos.Row) + 1,
			StartCol:  uint(startPos.Column) + 1,
			Kind:      targetNode.Kind(),
			Name:      nameText,
			Text:      text,
		})
	}

	return results, nil
}

// collectEdits runs the query and builds the list of edits to apply.
func collectEdits(file *forest.ParsedFile, matchSpec *Match, action *Action) ([]rewrite.Edit, error) {
	return collectEditsFromParts(file.OriginalSource, file.Tree, file.Adapter, file.Path, matchSpec, action)
}

// collectEditsFromParts is the decomposed form of collectEdits.
func collectEditsFromParts(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	path string,
	matchSpec *Match,
	action *Action,
) ([]rewrite.Edit, error) {
	queryStr, captureName, err := resolveMatchSpecFromParts(adapter, path, matchSpec)
	if err != nil {
		return nil, err
	}
	if queryStr == "" {
		return nil, nil
	}

	lang := adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil, fmt.Errorf("compiling query %q: %v", queryStr, qErr)
	}
	defer query.Close()

	targetIdx, nameIdx, err := resolveCaptureIndices(query, captureName)
	if err != nil {
		return nil, err
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), source)

	var edits []rewrite.Edit
	for match := matches.Next(); match != nil; match = matches.Next() {
		targetNode := findCapture(match.Captures, targetIdx)
		if targetNode == nil {
			continue
		}

		var nameNode *tree_sitter.Node
		if nameIdx >= 0 {
			nameNode = findCapture(match.Captures, uint32(nameIdx))
		}

		edit, err := makeEdit(source, targetNode, nameNode, action)
		if err != nil {
			return nil, err
		}
		if edit != nil {
			edits = append(edits, *edit)
		}
	}

	sortEdits(edits)
	return edits, nil
}

// resolveMatchSpec extracts the query string and capture-name hint from a
// Match, applying any file-path filter. Returns ("", "", nil) when the file
// filter excludes this file.
func resolveMatchSpec(file *forest.ParsedFile, matchSpec *Match) (queryStr, captureName string, err error) {
	return resolveMatchSpecFromParts(file.Adapter, file.Path, matchSpec)
}

// resolveMatchSpecFromParts is the decomposed form of resolveMatchSpec.
func resolveMatchSpecFromParts(adapter adapters.LanguageAdapter, path string, matchSpec *Match) (queryStr, captureName string, err error) {
	if matchSpec.IsRaw {
		return matchSpec.RawQuery, matchSpec.Capture, nil
	}
	if matchSpec.File != "" && !strings.Contains(path, matchSpec.File) {
		return "", "", nil
	}
	q, err := resolveAbstractQuery(adapter, matchSpec.Kind, matchSpec.Name)
	return q, "", err
}

// preferredWholeCaptureNames are the "whole node" capture names checked in order.
var preferredWholeCaptureNames = []string{"func", "call", "type_def", "import"}

// resolveCaptureIndices returns the target capture index and, if present, the
// name capture index (-1 if absent).
func resolveCaptureIndices(query *tree_sitter.Query, captureName string) (targetIdx uint32, nameIdx int, err error) {
	captureNames := query.CaptureNames()

	// Helper: find index of a capture name.
	indexOf := func(name string) int {
		for i, n := range captureNames {
			if n == name {
				return i
			}
		}
		return -1
	}

	// Resolve target capture.
	if captureName != "" {
		idx := indexOf(captureName)
		if idx < 0 {
			return 0, -1, fmt.Errorf("capture @%s not found in query", captureName)
		}
		targetIdx = uint32(idx)
	} else {
		// Prefer whole-node captures; fall back to @name.
		found := false
		for _, candidate := range preferredWholeCaptureNames {
			idx := indexOf(candidate)
			if idx >= 0 {
				targetIdx = uint32(idx)
				found = true
				break
			}
		}
		if !found {
			idx := indexOf("name")
			if idx < 0 {
				return 0, -1, fmt.Errorf("no usable capture found in query")
			}
			targetIdx = uint32(idx)
		}
	}

	nameIdx = indexOf("name")
	// nameIdx may be -1 (absent); callers check before using.
	return targetIdx, nameIdx, nil
}

// findCapture returns a pointer to the node for the first capture with the
// given index, or nil. The pointer is into the captures slice and is valid as
// long as the underlying QueryMatch is alive.
func findCapture(captures []tree_sitter.QueryCapture, idx uint32) *tree_sitter.Node {
	for i := range captures {
		if captures[i].Index == idx {
			return &captures[i].Node
		}
	}
	return nil
}

// makeEdit produces a rewrite.Edit for a matched node and action.
func makeEdit(source []byte, node, nameNode *tree_sitter.Node, action *Action) (*rewrite.Edit, error) {
	nodeText := func() string {
		return string(source[node.StartByte():node.EndByte()])
	}

	switch action.Kind {
	case ActionReplace:
		return &rewrite.Edit{
			Start:       node.StartByte(),
			End:         node.EndByte(),
			Replacement: action.Code,
		}, nil

	case ActionWrap:
		return &rewrite.Edit{
			Start:       node.StartByte(),
			End:         node.EndByte(),
			Replacement: action.Before + nodeText() + action.After,
		}, nil

	case ActionUnwrap:
		inner := findBodyOrInner(node, source)
		return &rewrite.Edit{
			Start:       node.StartByte(),
			End:         node.EndByte(),
			Replacement: inner,
		}, nil

	case ActionPrependStatement:
		indent := detectIndent(source, node.StartByte())
		return &rewrite.Edit{
			Start:       node.StartByte(),
			End:         node.StartByte(),
			Replacement: action.Code + "\n" + indent,
		}, nil

	case ActionAppendStatement:
		indent := detectIndent(source, node.StartByte())
		return &rewrite.Edit{
			Start:       node.EndByte(),
			End:         node.EndByte(),
			Replacement: "\n" + indent + action.Code,
		}, nil

	case ActionRemove:
		return &rewrite.Edit{
			Start:       node.StartByte(),
			End:         consumeTrailingNewline(source, node.EndByte()),
			Replacement: "",
		}, nil

	case ActionReplaceName:
		if nameNode == nil {
			return nil, fmt.Errorf("replace_name: no @name capture found for matched node")
		}
		return &rewrite.Edit{
			Start:       nameNode.StartByte(),
			End:         nameNode.EndByte(),
			Replacement: action.Code,
		}, nil

	case ActionReplaceBody:
		bodyNode := findBodyNode(node)
		if bodyNode == nil {
			return nil, fmt.Errorf("replace_body: no body found for matched node")
		}
		return &rewrite.Edit{
			Start:       bodyNode.StartByte(),
			End:         bodyNode.EndByte(),
			Replacement: action.Code,
		}, nil

	default:
		return nil, fmt.Errorf("unknown action kind: %d", action.Kind)
	}
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

// consumeTrailingNewline advances end past the next byte if it is '\n'.
func consumeTrailingNewline(source []byte, end uint) uint {
	if end < uint(len(source)) && source[end] == '\n' {
		return end + 1
	}
	return end
}

// findBodyNode looks for the body/block child of a node by common field names.
func findBodyNode(node *tree_sitter.Node) *tree_sitter.Node {
	for _, field := range []string{"body", "block", "consequence"} {
		if child := node.ChildByFieldName(field); child != nil {
			return child
		}
	}
	return nil
}

// findBodyOrInner returns the body text if available, otherwise concatenates
// all children's text.
func findBodyOrInner(node *tree_sitter.Node, source []byte) string {
	if body := findBodyNode(node); body != nil {
		return string(source[body.StartByte():body.EndByte()])
	}

	cursor := node.Walk()
	defer cursor.Close()

	var parts []string
	for _, child := range node.Children(cursor) {
		parts = append(parts, string(source[child.StartByte():child.EndByte()]))
	}
	return strings.Join(parts, "")
}

// applyEdits applies a sorted list of non-overlapping edits to source bytes.
// It wraps rewrite.ApplyEdits and adds an overlap check.
func applyEdits(source []byte, edits []rewrite.Edit) ([]byte, error) {
	// Verify no overlaps.
	for i := 1; i < len(edits); i++ {
		if edits[i].Start < edits[i-1].End {
			return nil, fmt.Errorf("overlapping edits at byte %d", edits[i].Start)
		}
	}
	return rewrite.ApplyEdits(source, edits), nil
}

// sortEdits sorts edits by start position ascending.
func sortEdits(edits []rewrite.Edit) {
	for i := 1; i < len(edits); i++ {
		for j := i; j > 0 && edits[j].Start < edits[j-1].Start; j-- {
			edits[j], edits[j-1] = edits[j-1], edits[j]
		}
	}
}
