// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package rewrite

import (
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// analyseGo builds bindings and resolves identifier occurrences for a Go
// source file. Supported:
//
//   - package-level var/const/func/type bindings
//   - function/method parameters and results (named)
//   - short var declarations and var specs in blocks
//   - nested blocks (if/for/switch/select bodies)
//   - struct/interface field declarations (keyed by owner type)
//   - selector field uses when the base's simple type is known
//
// Not modelled: label names, type parameters, promoted fields, package
// imports beyond the local alias, or cross-file type resolution.
func analyseGo(source []byte, tree *tree_sitter.Tree) *fileAnalysis {
	rootNode := tree.RootNode()
	a := &fileAnalysis{
		source: source,
		root:   newScope(nil, scopeModule, rootNode.StartByte(), rootNode.EndByte()),
	}
	a.trackScope(a.root)

	goCollect(a, rootNode, a.root)
	goResolve(a, rootNode, a.root)
	return a
}

func goCollect(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_declaration":
		nameNode := childByField(n, "name")
		if nameNode != nil {
			b := a.newBinding(a.text(nameNode), "func", nameNode.StartByte(), nameNode.EndByte(), sc)
			a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
		}
		fnScope := a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		// Parameters.
		if params := childByField(n, "parameters"); params != nil {
			goCollectParams(a, params, fnScope)
		}
		// Named result parameters.
		if result := childByField(n, "result"); result != nil {
			goCollectResultParams(a, result, fnScope)
			// Result types are references — handled in resolve.
		}
		if body := childByField(n, "body"); body != nil {
			goCollect(a, body, fnScope)
		}
		return

	case "method_declaration":
		// Receiver params bind in the method scope; method name is a field
		// on the receiver type (we still bind it as a func at package level
		// under the method name for simple renames of the method identifier).
		nameNode := childByField(n, "name")
		recvType := ""
		if recv := childByField(n, "receiver"); recv != nil {
			recvType = goFirstTypeName(a, recv)
		}
		if nameNode != nil {
			// Method names are package-visible as field_identifier; bind as func.
			b := a.newBinding(a.text(nameNode), "func", nameNode.StartByte(), nameNode.EndByte(), sc)
			if recvType != "" {
				b.ownerType = recvType
			}
			a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
		}
		fnScope := a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		if recv := childByField(n, "receiver"); recv != nil {
			goCollectParams(a, recv, fnScope)
		}
		if params := childByField(n, "parameters"); params != nil {
			goCollectParams(a, params, fnScope)
		}
		if result := childByField(n, "result"); result != nil {
			goCollectResultParams(a, result, fnScope)
		}
		if body := childByField(n, "body"); body != nil {
			goCollect(a, body, fnScope)
		}
		return

	case "type_declaration":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "type_spec" {
				goCollectTypeSpec(a, ch, sc)
			}
		}
		return

	case "type_spec":
		goCollectTypeSpec(a, n, sc)
		return

	case "var_declaration", "const_declaration":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "var_spec" || ch.Kind() == "const_spec" {
				goCollectVarSpec(a, ch, sc)
			}
		}
		return

	case "short_var_declaration":
		// left := right
		left := childByField(n, "left")
		if left == nil && n.NamedChildCount() > 0 {
			left = n.NamedChild(0)
		}
		goBindExprList(a, left, sc, "var")
		// right-hand side may contain function literals
		right := childByField(n, "right")
		if right == nil && n.NamedChildCount() > 1 {
			right = n.NamedChild(1)
		}
		if right != nil {
			goCollect(a, right, sc)
		}
		return

	case "block":
		// Function body block shares the function scope (params visible).
		// Nested bare blocks introduce a new scope.
		parentKind := ""
		if p := n.Parent(); p != nil {
			parentKind = p.Kind()
		}
		bodyScope := sc
		if parentKind != "function_declaration" && parentKind != "method_declaration" &&
			parentKind != "func_literal" {
			bodyScope = a.trackScope(newScope(sc, scopeBlock, n.StartByte(), n.EndByte()))
		}
		for _, ch := range namedChildren(n) {
			goCollect(a, ch, bodyScope)
		}
		return

	case "if_statement", "for_statement", "expression_switch_statement",
		"type_switch_statement", "select_statement":
		// Optional init statement shares a scope with the body.
		stmtScope := a.trackScope(newScope(sc, scopeBlock, n.StartByte(), n.EndByte()))
		for _, ch := range namedChildren(n) {
			goCollect(a, ch, stmtScope)
		}
		return

	case "func_literal":
		fnScope := a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		if params := childByField(n, "parameters"); params != nil {
			goCollectParams(a, params, fnScope)
		}
		if result := childByField(n, "result"); result != nil {
			goCollectResultParams(a, result, fnScope)
		}
		if body := childByField(n, "body"); body != nil {
			goCollect(a, body, fnScope)
		}
		return

	case "parameter_list", "parameter_declaration", "variadic_parameter_declaration":
		// Handled by goCollectParams from function contexts.
		return

	case "range_clause":
		// for i, v := range xs
		left := childByField(n, "left")
		if left != nil {
			goBindExprList(a, left, sc, "var")
		}
		if right := childByField(n, "right"); right != nil {
			goCollect(a, right, sc)
		}
		return
	}

	for _, ch := range namedChildren(n) {
		goCollect(a, ch, sc)
	}
}

func goCollectTypeSpec(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	nameNode := childByField(n, "name")
	var typeName string
	if nameNode != nil {
		typeName = a.text(nameNode)
		b := a.newBinding(typeName, "type", nameNode.StartByte(), nameNode.EndByte(), sc)
		a.addOcc(nameNode.StartByte(), nameNode.EndByte(), typeName, b, true)
	}
	// Struct / interface fields.
	typeNode := childByField(n, "type")
	if typeNode == nil && n.NamedChildCount() > 1 {
		typeNode = n.NamedChild(1)
	}
	if typeNode == nil {
		return
	}
	typeScope := a.trackScope(newScope(sc, scopeType, typeNode.StartByte(), typeNode.EndByte()))
	goCollectFields(a, typeNode, typeScope, typeName)
}

func goCollectFields(a *fileAnalysis, n *tree_sitter.Node, sc *scope, ownerType string) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "field_declaration":
		// One or more field_identifier names, then a type.
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "field_identifier" {
				name := a.text(ch)
				b := a.newFieldBinding(name, ownerType, ch.StartByte(), ch.EndByte(), sc)
				a.addFieldOcc(ch.StartByte(), ch.EndByte(), name, b, true, ownerType)
			}
		}
		return
	case "method_spec":
		if name := childByField(n, "name"); name != nil {
			b := a.newFieldBinding(a.text(name), ownerType, name.StartByte(), name.EndByte(), sc)
			a.addFieldOcc(name.StartByte(), name.EndByte(), b.name, b, true, ownerType)
		}
		return
	}
	for _, ch := range namedChildren(n) {
		goCollectFields(a, ch, sc, ownerType)
	}
}

func goCollectVarSpec(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	// var_spec: name(s) type? value?
	// Names are identifier children before the type/value.
	var typeName string
	if t := childByField(n, "type"); t != nil {
		typeName = goSimpleTypeName(a, t)
	}
	for _, ch := range namedChildren(n) {
		if ch.Kind() == "identifier" {
			b := a.newBinding(a.text(ch), "var", ch.StartByte(), ch.EndByte(), sc)
			a.addOcc(ch.StartByte(), ch.EndByte(), b.name, b, true)
			if typeName != "" {
				sc.types[b.name] = typeName
			}
		}
		if ch.Kind() == "expression_list" || ch.Kind() == "type_identifier" ||
			ch.Kind() == "pointer_type" || ch.Kind() == "slice_type" ||
			ch.Kind() == "qualified_type" {
			// values / types — collect nested func literals in values
			if childByField(n, "type") != ch {
				goCollect(a, ch, sc)
			}
		}
	}
	if val := childByField(n, "value"); val != nil {
		goCollect(a, val, sc)
	}
}

func goCollectParams(a *fileAnalysis, params *tree_sitter.Node, sc *scope) {
	for _, ch := range namedChildren(params) {
		switch ch.Kind() {
		case "parameter_declaration", "variadic_parameter_declaration":
			typeName := ""
			if t := childByField(ch, "type"); t != nil {
				typeName = goSimpleTypeName(a, t)
			}
			// Names may be multiple identifiers sharing a type.
			for _, nameNode := range namedChildren(ch) {
				if nameNode.Kind() == "identifier" {
					// Skip if this is actually the type (type-only param).
					if t := childByField(ch, "type"); t != nil && nodeContains(t, nameNode) {
						continue
					}
					// identifier children that are names have field "name" or
					// appear before the type.
					b := a.newBinding(a.text(nameNode), "param", nameNode.StartByte(), nameNode.EndByte(), sc)
					a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
					if typeName != "" {
						sc.types[b.name] = typeName
					}
				}
			}
			// Also try field "name"
			if nameNode := childByField(ch, "name"); nameNode != nil && nameNode.Kind() == "identifier" {
				if _, ok := sc.defs[a.text(nameNode)]; !ok {
					b := a.newBinding(a.text(nameNode), "param", nameNode.StartByte(), nameNode.EndByte(), sc)
					a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
					if typeName != "" {
						sc.types[b.name] = typeName
					}
				}
			}
		}
	}
}

func goCollectResultParams(a *fileAnalysis, result *tree_sitter.Node, sc *scope) {
	if result.Kind() == "parameter_list" {
		goCollectParams(a, result, sc)
	}
	// Bare type result — no names to bind.
}

func goBindExprList(a *fileAnalysis, list *tree_sitter.Node, sc *scope, kind string) {
	if list == nil {
		return
	}
	if list.Kind() == "identifier" {
		name := a.text(list)
		if name == "_" {
			return
		}
		b := a.newBinding(name, kind, list.StartByte(), list.EndByte(), sc)
		a.addOcc(list.StartByte(), list.EndByte(), name, b, true)
		return
	}
	for _, ch := range namedChildren(list) {
		if ch.Kind() == "identifier" {
			name := a.text(ch)
			if name == "_" {
				continue
			}
			b := a.newBinding(name, kind, ch.StartByte(), ch.EndByte(), sc)
			a.addOcc(ch.StartByte(), ch.EndByte(), name, b, true)
		}
	}
}

func goSimpleTypeName(a *fileAnalysis, n *tree_sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "type_identifier":
		return a.text(n)
	case "pointer_type", "slice_type", "channel_type":
		if n.NamedChildCount() > 0 {
			return goSimpleTypeName(a, n.NamedChild(0))
		}
	case "qualified_type":
		// pkg.Type — use the type name.
		if name := childByField(n, "name"); name != nil {
			return a.text(name)
		}
		if n.NamedChildCount() > 0 {
			return a.text(n.NamedChild(n.NamedChildCount() - 1))
		}
	}
	// Fallback: first type_identifier descendant.
	var found string
	walkNamed(n, func(ch *tree_sitter.Node) bool {
		if ch.Kind() == "type_identifier" && found == "" {
			found = a.text(ch)
			return false
		}
		return found == ""
	})
	return found
}

func goFirstTypeName(a *fileAnalysis, recv *tree_sitter.Node) string {
	var found string
	walkNamed(recv, func(ch *tree_sitter.Node) bool {
		if ch.Kind() == "type_identifier" && found == "" {
			found = a.text(ch)
			return false
		}
		return found == ""
	})
	return found
}

// goResolve records identifier / type_identifier / field_identifier uses.
func goResolve(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_declaration", "method_declaration", "func_literal":
		fnScope := a.scopeAt(n.StartByte(), n.EndByte(), scopeFunction)
		if fnScope == nil {
			fnScope = sc
		}
		// Walk children; params already have def occs.
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" || ch.Kind() == "field_identifier" {
				// function/method name — already recorded
				if goAlreadyRecorded(a, ch.StartByte(), ch.EndByte()) {
					continue
				}
			}
			if ch.Kind() == "parameter_list" {
				goResolveParamTypes(a, ch, sc) // types resolve in outer scope
				continue
			}
			if ch.Kind() == "block" {
				goResolve(a, ch, fnScope)
				continue
			}
			// result type, etc.
			goResolve(a, ch, fnScope)
		}
		return

	case "block":
		bodyScope := sc
		if p := n.Parent(); p != nil {
			pk := p.Kind()
			if pk != "function_declaration" && pk != "method_declaration" && pk != "func_literal" {
				if s := a.scopeAt(n.StartByte(), n.EndByte(), scopeBlock); s != nil {
					bodyScope = s
				}
			}
		}
		for _, ch := range namedChildren(n) {
			goResolve(a, ch, bodyScope)
		}
		return

	case "if_statement", "for_statement", "expression_switch_statement",
		"type_switch_statement", "select_statement":
		stmtScope := a.scopeAt(n.StartByte(), n.EndByte(), scopeBlock)
		if stmtScope == nil {
			stmtScope = sc
		}
		for _, ch := range namedChildren(n) {
			goResolve(a, ch, stmtScope)
		}
		return

	case "type_declaration", "type_spec":
		// Field defs already recorded; resolve type references inside.
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "type_identifier" {
				// LHS of type_spec is the def.
				if goAlreadyRecorded(a, ch.StartByte(), ch.EndByte()) {
					continue
				}
			}
			if ch.Kind() == "field_identifier" && goAlreadyRecorded(a, ch.StartByte(), ch.EndByte()) {
				continue
			}
			goResolve(a, ch, sc)
		}
		return

	case "short_var_declaration", "var_spec", "const_spec":
		// LHS defs already recorded; resolve RHS and type.
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" && goAlreadyRecorded(a, ch.StartByte(), ch.EndByte()) {
				continue
			}
			goResolve(a, ch, sc)
		}
		return

	case "range_clause":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" && goAlreadyRecorded(a, ch.StartByte(), ch.EndByte()) {
				continue
			}
			if childByField(n, "left") != nil && nodeContains(childByField(n, "left"), ch) &&
				ch.Kind() == "expression_list" {
				// skip LHS list children individually
				for _, id := range namedChildren(ch) {
					if id.Kind() == "identifier" && goAlreadyRecorded(a, id.StartByte(), id.EndByte()) {
						continue
					}
					goResolve(a, id, sc)
				}
				continue
			}
			goResolve(a, ch, sc)
		}
		return

	case "selector_expression":
		// operand.field
		operand := childByField(n, "operand")
		field := childByField(n, "field")
		if operand != nil {
			goResolve(a, operand, sc)
		}
		if field != nil && (field.Kind() == "field_identifier" || field.Kind() == "identifier") {
			name := a.text(field)
			owner := ""
			if operand != nil && operand.Kind() == "identifier" {
				owner = lookupTypeOf(sc, a.text(operand))
			}
			b := lookupFieldBinding(a, owner, name)
			a.addFieldOcc(field.StartByte(), field.EndByte(), name, b, false, owner)
		}
		return

	case "identifier":
		name := a.text(n)
		if name == "_" {
			return
		}
		if goAlreadyRecorded(a, n.StartByte(), n.EndByte()) {
			return
		}
		if goIsPackageIdent(n) {
			return
		}
		b := resolveName(sc, name, true)
		a.addOcc(n.StartByte(), n.EndByte(), name, b, false)
		return

	case "type_identifier":
		name := a.text(n)
		if goAlreadyRecorded(a, n.StartByte(), n.EndByte()) {
			return
		}
		b := resolveName(sc, name, true)
		a.addOcc(n.StartByte(), n.EndByte(), name, b, false)
		return

	case "field_identifier":
		if goAlreadyRecorded(a, n.StartByte(), n.EndByte()) {
			return
		}
		// Standalone field_identifier outside selector (e.g. keyed composite
		// literal Field: value) — resolve as field if unique, else free.
		name := a.text(n)
		b := lookupFieldBinding(a, "", name)
		a.addFieldOcc(n.StartByte(), n.EndByte(), name, b, false, "")
		return

	case "package_identifier":
		// package clause / import aliases — skip as variable rename targets
		// unless it's an import alias binding (handled separately if needed).
		return

	case "parameter_list":
		goResolveParamTypes(a, n, sc)
		return
	}

	for _, ch := range namedChildren(n) {
		goResolve(a, ch, sc)
	}
}

func goResolveParamTypes(a *fileAnalysis, params *tree_sitter.Node, outer *scope) {
	for _, ch := range namedChildren(params) {
		if ch.Kind() == "parameter_declaration" || ch.Kind() == "variadic_parameter_declaration" {
			if t := childByField(ch, "type"); t != nil {
				goResolve(a, t, outer)
			} else {
				// type-only: last child may be type
				for _, c := range namedChildren(ch) {
					if c.Kind() != "identifier" || !goAlreadyRecorded(a, c.StartByte(), c.EndByte()) {
						goResolve(a, c, outer)
					}
				}
			}
		}
	}
}

func goAlreadyRecorded(a *fileAnalysis, start, end uint) bool {
	for _, o := range a.occurrences {
		if o.start == start && o.end == end {
			return true
		}
	}
	return false
}

func goIsPackageIdent(n *tree_sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	return p.Kind() == "package_clause"
}
