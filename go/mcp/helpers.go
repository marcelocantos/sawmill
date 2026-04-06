// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"strconv"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
	"github.com/marcelocantos/sawmill/transform"
)

// parseAction converts an action name string and optional code/before/after
// parameters into a transform.Action.
func parseAction(action string, code, before, after *string) (*transform.Action, error) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	switch action {
	case "replace":
		return transform.Replace(str(code)), nil
	case "wrap":
		return transform.Wrap(str(before), str(after)), nil
	case "unwrap":
		return transform.Unwrap(), nil
	case "prepend_statement":
		return transform.PrependStatement(str(code)), nil
	case "append_statement":
		return transform.AppendStatement(str(code)), nil
	case "remove":
		return transform.Remove(), nil
	case "replace_name":
		return transform.ReplaceName(str(code)), nil
	case "replace_body":
		return transform.ReplaceBody(str(code)), nil
	default:
		return nil, fmt.Errorf("unknown action %q; valid actions: replace, wrap, unwrap, prepend_statement, append_statement, remove, replace_name, replace_body", action)
	}
}

// buildParamText constructs the parameter text to insert into a function
// signature. It returns something like "name: type = default" or "name",
// depending on which optional fields are provided.
func buildParamText(name string, paramType, defaultValue *string) string {
	var sb strings.Builder
	sb.WriteString(name)
	if paramType != nil && *paramType != "" {
		sb.WriteString(": ")
		sb.WriteString(*paramType)
	}
	if defaultValue != nil && *defaultValue != "" {
		sb.WriteString(" = ")
		sb.WriteString(*defaultValue)
	}
	return sb.String()
}

// addParamInFile finds the named function in file and inserts paramText into
// its parameter list at the specified position ("first", "last", or a 1-based
// integer). Returns the modified source bytes, or the original bytes if the
// function is not found.
func addParamInFile(file *forest.ParsedFile, funcName, paramText, position string) ([]byte, error) {
	queryStr := file.Adapter.FunctionDefQuery()
	if queryStr == "" {
		return file.OriginalSource, nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return file.OriginalSource, nil
	}
	defer query.Close()

	// Find the @name and @func capture indices.
	nameIdx := -1
	funcIdx := -1
	for i, n := range query.CaptureNames() {
		switch n {
		case "name":
			nameIdx = i
		case "func":
			funcIdx = i
		}
	}
	if nameIdx < 0 || funcIdx < 0 {
		return file.OriginalSource, nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit

	for m := matches.Next(); m != nil; m = matches.Next() {
		// Find the whole function node (@func capture) and the name node (@name capture).
		var funcNode, nameNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(funcIdx) && funcNode == nil {
				funcNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nameNode == nil {
				nameNode = &c.Node
			}
		}

		if nameNode == nil || funcNode == nil {
			continue
		}

		name := string(file.OriginalSource[nameNode.StartByte():nameNode.EndByte()])
		if name != funcName {
			continue
		}

		// Find the parameters node.
		paramsNode := funcNode.ChildByFieldName("parameters")
		if paramsNode == nil {
			continue
		}

		// Determine insertion point based on position.
		insertOffset, insertPrefix, insertSuffix, err := resolveParamPosition(
			file.OriginalSource, paramsNode, paramText, position,
		)
		if err != nil {
			return nil, err
		}

		edits = append(edits, rewrite.Edit{
			Start:       insertOffset,
			End:         insertOffset,
			Replacement: insertPrefix + paramText + insertSuffix,
		})
	}

	if len(edits) == 0 {
		return file.OriginalSource, nil
	}

	return rewrite.ApplyEdits(file.OriginalSource, edits), nil
}

// resolveParamPosition determines where and how to insert a new parameter
// inside a parameters node. Returns the byte offset for insertion and the
// surrounding comma/space punctuation.
func resolveParamPosition(
	source []byte,
	paramsNode *tree_sitter.Node,
	paramText string,
	position string,
) (offset uint, prefix, suffix string, err error) {
	childCount := paramsNode.ChildCount()
	cursor := paramsNode.Walk()
	defer cursor.Close()

	// Collect non-punctuation children (actual parameters).
	var paramNodes []*tree_sitter.Node
	for _, child := range paramsNode.Children(cursor) {
		kind := child.Kind()
		if kind != "," && kind != "(" && kind != ")" && kind != "parameters" {
			// Skip only the outer parens and commas; include actual parameter nodes.
			if child.IsNamed() {
				cn := child
				paramNodes = append(paramNodes, &cn)
			}
		}
	}
	_ = childCount // suppress unused warning

	// Compute the opening paren position. The parameters node itself may be
	// "(param1, param2)" — look for the first "(" character.
	openParen := paramsNode.StartByte()
	for i := openParen; i < paramsNode.EndByte(); i++ {
		if source[i] == '(' {
			openParen = i
			break
		}
	}
	closeParen := paramsNode.EndByte()
	for i := closeParen - 1; i > openParen; i-- {
		if source[i] == ')' {
			closeParen = i
			break
		}
	}

	if position == "" || position == "last" {
		// Insert before the closing paren, after a comma if there are existing params.
		if len(paramNodes) > 0 {
			return closeParen, ", ", "", nil
		}
		return closeParen, "", "", nil
	}

	if position == "first" {
		// Insert after the opening paren.
		if len(paramNodes) > 0 {
			return openParen + 1, "", ", ", nil
		}
		return openParen + 1, "", "", nil
	}

	// Numeric position (1-based).
	idx, convErr := strconv.Atoi(position)
	if convErr != nil {
		return 0, "", "", fmt.Errorf("invalid position %q: must be first, last, or a positive integer", position)
	}
	if idx < 1 {
		return 0, "", "", fmt.Errorf("position must be >= 1, got %d", idx)
	}

	if idx > len(paramNodes) || len(paramNodes) == 0 {
		// Append at end.
		if len(paramNodes) > 0 {
			return closeParen, ", ", "", nil
		}
		return closeParen, "", "", nil
	}

	// Insert before the idx-th parameter (1-based).
	target := paramNodes[idx-1]
	if idx == 1 {
		return target.StartByte(), "", ", ", nil
	}
	return target.StartByte(), "", ", ", nil
}

// removeParamInFile finds the named function in file and removes paramName from
// its parameter list. Returns the modified source bytes.
func removeParamInFile(file *forest.ParsedFile, funcName, paramName string) ([]byte, error) {
	queryStr := file.Adapter.FunctionDefQuery()
	if queryStr == "" {
		return file.OriginalSource, nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return file.OriginalSource, nil
	}
	defer query.Close()

	nameIdx := -1
	funcIdx := -1
	for i, n := range query.CaptureNames() {
		switch n {
		case "name":
			nameIdx = i
		case "func":
			funcIdx = i
		}
	}
	if nameIdx < 0 || funcIdx < 0 {
		return file.OriginalSource, nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit

	for m := matches.Next(); m != nil; m = matches.Next() {
		var funcNode, nameNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(funcIdx) && funcNode == nil {
				funcNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nameNode == nil {
				nameNode = &c.Node
			}
		}
		if nameNode == nil || funcNode == nil {
			continue
		}

		name := string(file.OriginalSource[nameNode.StartByte():nameNode.EndByte()])
		if name != funcName {
			continue
		}

		paramsNode := funcNode.ChildByFieldName("parameters")
		if paramsNode == nil {
			continue
		}

		edit, found := removeParamFromParamsNode(file.OriginalSource, paramsNode, paramName)
		if found {
			edits = append(edits, edit)
		}
	}

	if len(edits) == 0 {
		return file.OriginalSource, nil
	}

	return rewrite.ApplyEdits(file.OriginalSource, edits), nil
}

// removeParamFromParamsNode locates paramName inside a Tree-sitter parameters
// node and returns the edit that removes it (including its surrounding comma).
func removeParamFromParamsNode(
	source []byte,
	paramsNode *tree_sitter.Node,
	paramName string,
) (rewrite.Edit, bool) {
	cursor := paramsNode.Walk()
	defer cursor.Close()

	type paramEntry struct {
		node *tree_sitter.Node
		name string
	}

	var params []paramEntry
	for _, child := range paramsNode.Children(cursor) {
		if !child.IsNamed() {
			continue
		}
		kind := child.Kind()
		if kind == "," {
			continue
		}
		// The parameter's name is either the node text itself (for simple
		// identifiers) or the value of a nested identifier/name node.
		text := extractParamName(source, child)
		cn := child
		params = append(params, paramEntry{node: &cn, name: text})
	}

	for i, p := range params {
		if p.name != paramName {
			continue
		}

		start := p.node.StartByte()
		end := p.node.EndByte()

		if len(params) == 1 {
			// Only param — remove just the param text.
			return rewrite.Edit{Start: start, End: end, Replacement: ""}, true
		}

		if i == 0 {
			// First param — remove trailing ", ".
			end = params[1].node.StartByte()
		} else {
			// Non-first param — remove preceding ", ".
			// Walk backwards from start to include the comma and whitespace.
			s := start
			for s > 0 && (source[s-1] == ' ' || source[s-1] == '\t') {
				s--
			}
			if s > 0 && source[s-1] == ',' {
				s--
			}
			start = s
		}

		return rewrite.Edit{Start: start, End: end, Replacement: ""}, true
	}

	return rewrite.Edit{}, false
}

// extractParamName returns the name text of a parameter node. It first checks
// for a child named "name" or "identifier", then falls back to the full text.
func extractParamName(source []byte, node tree_sitter.Node) string {
	// Try common child field names.
	for _, field := range []string{"name", "identifier", "pattern"} {
		if child := node.ChildByFieldName(field); child != nil {
			return string(source[child.StartByte():child.EndByte()])
		}
	}
	// For simple identifier nodes, the node text is the name.
	kind := node.Kind()
	if kind == "identifier" || strings.HasSuffix(kind, "_identifier") {
		return string(source[node.StartByte():node.EndByte()])
	}
	// For typed parameters like "name: type", the first named child is typically
	// the identifier.
	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.Children(cursor) {
		if child.IsNamed() {
			ck := child.Kind()
			if ck == "identifier" || strings.HasSuffix(ck, "_identifier") {
				return string(source[child.StartByte():child.EndByte()])
			}
		}
	}
	return string(source[node.StartByte():node.EndByte()])
}
