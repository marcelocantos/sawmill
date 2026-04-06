// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// bodyKinds are tree-sitter node kinds that represent a struct/class body.
var bodyKinds = map[string]bool{
	"field_declaration_list": true,
	"struct_body":            true,
	"class_body":             true,
	"block":                  true,
	"body":                   true,
	"declaration_list":       true,
}

// findClosingBrace walks backwards from endByte to find the '}' character.
func findClosingBrace(source []byte, startByte, endByte uint) uint {
	for i := endByte - 1; i > startByte; i-- {
		if source[i] == '}' {
			return i
		}
	}
	return endByte
}

// findBodyInsertPos searches the subtree rooted at node for a body-like child
// and returns the byte position just before its closing brace. Returns 0 and
// false if no body is found.
func findBodyInsertPos(source []byte, node tree_sitter.Node) (uint, bool) {
	// BFS through children up to 4 levels deep.
	type entry struct {
		start, end uint
		kind       string
		node       tree_sitter.Node
	}

	// Check the node itself first.
	if bodyKinds[node.Kind()] {
		pos := findClosingBrace(source, node.StartByte(), node.EndByte())
		return pos, true
	}

	queue := []tree_sitter.Node{node}
	for depth := 0; depth < 4 && len(queue) > 0; depth++ {
		var next []tree_sitter.Node
		for _, n := range queue {
			cursor := n.Walk()
			for _, child := range n.Children(cursor) {
				if bodyKinds[child.Kind()] {
					pos := findClosingBrace(source, child.StartByte(), child.EndByte())
					cursor.Close()
					return pos, true
				}
				next = append(next, child)
			}
			cursor.Close()
		}
		queue = next
	}
	return 0, false
}

// collectTypeDefFieldEdits finds type definitions matching typeName and returns
// edits to insert a new field declaration.
func collectTypeDefFieldEdits(file *forest.ParsedFile, typeName, fieldName, fieldType string) []rewrite.Edit {
	queryStr := file.Adapter.TypeDefQuery()
	if queryStr == "" {
		return nil
	}

	fieldText := file.Adapter.GenField(fieldName, fieldType)
	if fieldText == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil
	}
	defer query.Close()

	nameIdx := -1
	typeDefIdx := -1
	for i, n := range query.CaptureNames() {
		switch n {
		case "name":
			nameIdx = i
		case "type_def":
			typeDefIdx = i
		}
	}
	if nameIdx < 0 || typeDefIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit
	for m := matches.Next(); m != nil; m = matches.Next() {
		var tdNode, nNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(typeDefIdx) && tdNode == nil {
				tdNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nNode == nil {
				nNode = &c.Node
			}
		}
		if tdNode == nil || nNode == nil {
			continue
		}
		name := string(file.OriginalSource[nNode.StartByte():nNode.EndByte()])
		if name != typeName {
			continue
		}

		// Find the body insert position while the node is still valid.
		insertPos, found := findBodyInsertPos(file.OriginalSource, *tdNode)
		if !found {
			continue
		}

		edits = append(edits, rewrite.Edit{
			Start:       insertPos,
			End:         insertPos,
			Replacement: fieldText,
		})
	}
	return edits
}

// collectStructLiteralEdits finds struct literal expressions matching typeName
// and returns edits to add a field initializer.
func collectStructLiteralEdits(file *forest.ParsedFile, typeName, fieldName, defaultValue string) []rewrite.Edit {
	queryStr := file.Adapter.StructLiteralQuery()
	if queryStr == "" {
		return nil
	}

	initText := file.Adapter.GenFieldInitializer(fieldName, defaultValue)
	if initText == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil
	}
	defer query.Close()

	nameIdx := -1
	literalIdx := -1
	for i, n := range query.CaptureNames() {
		switch n {
		case "name":
			nameIdx = i
		case "literal":
			literalIdx = i
		}
	}
	if nameIdx < 0 || literalIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit
	for m := matches.Next(); m != nil; m = matches.Next() {
		var litNode, nNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(literalIdx) && litNode == nil {
				litNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nNode == nil {
				nNode = &c.Node
			}
		}
		if litNode == nil || nNode == nil {
			continue
		}
		name := string(file.OriginalSource[nNode.StartByte():nNode.EndByte()])
		if name != typeName {
			continue
		}

		// Find the body/literal_value node to insert into.
		edit := computeLiteralInitEdit(file.OriginalSource, *litNode, initText)
		if edit != nil {
			edits = append(edits, *edit)
		}
	}
	return edits
}

// computeLiteralInitEdit computes an edit to add a field initializer to a
// struct literal. It works with the node while it's still valid.
func computeLiteralInitEdit(source []byte, litNode tree_sitter.Node, initText string) *rewrite.Edit {
	// Look for the literal_value or field_initializer_list child.
	var bodyNode *tree_sitter.Node
	cursor := litNode.Walk()
	defer cursor.Close()
	for _, child := range litNode.Children(cursor) {
		kind := child.Kind()
		if kind == "literal_value" || kind == "field_initializer_list" {
			cn := child
			bodyNode = &cn
			break
		}
	}

	if bodyNode == nil {
		// Use the literal node itself as the body.
		bodyNode = &litNode
	}

	// Check if there are existing field initializers.
	hasExisting := false
	var lastFieldEnd uint
	cursor2 := bodyNode.Walk()
	defer cursor2.Close()
	for _, child := range bodyNode.Children(cursor2) {
		if child.IsNamed() {
			kind := child.Kind()
			if kind == "keyed_element" || kind == "literal_element" ||
				kind == "field_initializer" || kind == "element" ||
				kind == "keyword_argument" {
				hasExisting = true
				end := child.EndByte()
				if end > lastFieldEnd {
					lastFieldEnd = end
				}
			}
		}
	}

	// Find closing brace.
	closeBrace := findClosingBrace(source, bodyNode.StartByte(), bodyNode.EndByte())

	if hasExisting {
		return &rewrite.Edit{
			Start:       lastFieldEnd,
			End:         lastFieldEnd,
			Replacement: ", " + initText,
		}
	}

	return &rewrite.Edit{
		Start:       closeBrace,
		End:         closeBrace,
		Replacement: initText,
	}
}

// collectCallSiteEdits finds calls to funcName and adds argText as a new argument.
func collectCallSiteEdits(file *forest.ParsedFile, funcName, argText string) []rewrite.Edit {
	queryStr := file.Adapter.CallExprQuery()
	if queryStr == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil
	}
	defer query.Close()

	nameIdx := -1
	callIdx := -1
	for i, n := range query.CaptureNames() {
		switch n {
		case "name":
			nameIdx = i
		case "call":
			callIdx = i
		}
	}
	if nameIdx < 0 || callIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit
	for m := matches.Next(); m != nil; m = matches.Next() {
		var cNode, nNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(callIdx) && cNode == nil {
				cNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nNode == nil {
				nNode = &c.Node
			}
		}
		if cNode == nil || nNode == nil {
			continue
		}
		name := string(file.OriginalSource[nNode.StartByte():nNode.EndByte()])
		if name != funcName {
			continue
		}

		edit := computeAddArgEdit(file.OriginalSource, *cNode, argText)
		if edit != nil {
			edits = append(edits, *edit)
		}
	}
	return edits
}

// computeAddArgEdit computes an edit to append an argument to a call expression.
func computeAddArgEdit(source []byte, callNode tree_sitter.Node, argText string) *rewrite.Edit {
	// Find the arguments node.
	var argsNode *tree_sitter.Node
	cursor := callNode.Walk()
	defer cursor.Close()
	for _, child := range callNode.Children(cursor) {
		kind := child.Kind()
		if kind == "argument_list" || kind == "arguments" {
			cn := child
			argsNode = &cn
			break
		}
	}
	if argsNode == nil {
		return nil
	}

	// Find existing arguments.
	cursor2 := argsNode.Walk()
	defer cursor2.Close()
	hasArgs := false
	var lastArgEnd uint
	for _, child := range argsNode.Children(cursor2) {
		if child.IsNamed() {
			hasArgs = true
			end := child.EndByte()
			if end > lastArgEnd {
				lastArgEnd = end
			}
		}
	}

	// Find closing paren.
	endByte := argsNode.EndByte()
	closeParen := endByte
	for i := endByte - 1; i > argsNode.StartByte(); i-- {
		if source[i] == ')' {
			closeParen = i
			break
		}
	}

	if hasArgs {
		return &rewrite.Edit{
			Start:       lastArgEnd,
			End:         lastArgEnd,
			Replacement: ", " + argText,
		}
	}

	return &rewrite.Edit{
		Start:       closeParen,
		End:         closeParen,
		Replacement: argText,
	}
}

// computeFactoryParamEdits returns the edits needed to add a parameter to a
// factory function, without applying them.
func computeFactoryParamEdits(file *forest.ParsedFile, funcName, paramText string) []rewrite.Edit {
	queryStr := file.Adapter.FunctionDefQuery()
	if queryStr == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return nil
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
		return nil
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

		insertOffset, insertPrefix, insertSuffix, err := resolveParamPosition(
			file.OriginalSource, paramsNode, paramText, "last",
		)
		if err != nil {
			continue
		}

		edits = append(edits, rewrite.Edit{
			Start:       insertOffset,
			End:         insertOffset,
			Replacement: insertPrefix + paramText + insertSuffix,
		})
	}

	return edits
}

// collectAddFieldEdits gathers all edits needed to add a field to a type and
// propagate to construction sites within a single file.
func collectAddFieldEdits(
	file *forest.ParsedFile,
	typeName, fieldName, fieldType, defaultValue string,
) []rewrite.Edit {
	var edits []rewrite.Edit

	// 1. Find type definition and insert field.
	edits = append(edits, collectTypeDefFieldEdits(file, typeName, fieldName, fieldType)...)

	// 2. Find factory functions and add parameter.
	factoryNames := file.Adapter.FactoryFuncNames(typeName)
	for _, fname := range factoryNames {
		paramText := buildParamText(fieldName, ptr(fieldType), nil)
		factoryEdits := computeFactoryParamEdits(file, fname, paramText)
		edits = append(edits, factoryEdits...)
	}

	// 3. Find struct literal constructions and add field initializer.
	edits = append(edits, collectStructLiteralEdits(file, typeName, fieldName, defaultValue)...)

	// 4. Find callers of factory functions and add argument.
	for _, fname := range factoryNames {
		argText := file.Adapter.GenFieldInitializer(fieldName, defaultValue)
		if argText == "" {
			argText = defaultValue
		}
		edits = append(edits, collectCallSiteEdits(file, fname, argText)...)
	}

	return edits
}
