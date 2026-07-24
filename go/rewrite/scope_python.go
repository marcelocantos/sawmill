// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package rewrite

import (
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// analysePython builds bindings and resolves identifier occurrences for a
// Python module. It implements a practical LEGB subset:
//
//   - function / lambda scopes with params and assigned locals
//   - class scopes (skipped when resolving from nested functions)
//   - global / nonlocal declarations
//   - comprehension scopes
//
// Attribute loads/stores (obj.x) are ignored — only bare identifiers.
func analysePython(source []byte, tree *tree_sitter.Tree) *fileAnalysis {
	rootNode := tree.RootNode()
	a := &fileAnalysis{
		source: source,
		root:   newScope(nil, scopeModule, rootNode.StartByte(), rootNode.EndByte()),
	}
	a.trackScope(a.root)

	// Pass 1: collect scopes + definitions (including all assignment targets
	// so Python's "assigned anywhere → local for whole function" holds).
	pyCollect(a, rootNode, a.root)

	// Pass 2: resolve every identifier occurrence.
	pyResolve(a, rootNode, a.root, false)

	return a
}

// pyCollect walks the tree collecting definitions into scopes.
func pyCollect(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_definition", "lambda":
		nameNode := childByField(n, "name")
		if nameNode != nil && nameNode.Kind() == "identifier" {
			// Function name binds in the enclosing scope.
			b := a.newBinding(a.text(nameNode), "func", nameNode.StartByte(), nameNode.EndByte(), sc)
			a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
		}
		params := childByField(n, "parameters")
		body := childByField(n, "body")
		// Lambdas store the body under "body" as an expression; params may be
		// a lambda_parameters node — ChildByFieldName("parameters") works for
		// both function_definition and lambda in tree-sitter-python.
		if params == nil {
			// lambda: (lambda parameters: body) — field names vary; fall back.
			for _, ch := range namedChildren(n) {
				switch ch.Kind() {
				case "lambda_parameters", "parameters":
					params = ch
				}
			}
		}
		fnScope := a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		if params != nil {
			pyCollectParams(a, params, fnScope)
		}
		if body != nil {
			// Pre-scan body for locals (assignments, nested defs, imports…).
			pyCollectBodyDefs(a, body, fnScope)
			// Recurse into nested functions/classes for their own scopes.
			pyCollectNested(a, body, fnScope)
		}
		return

	case "class_definition":
		nameNode := childByField(n, "name")
		if nameNode != nil {
			b := a.newBinding(a.text(nameNode), "class", nameNode.StartByte(), nameNode.EndByte(), sc)
			a.addOcc(nameNode.StartByte(), nameNode.EndByte(), b.name, b, true)
		}
		clsScope := a.trackScope(newScope(sc, scopeClass, n.StartByte(), n.EndByte()))
		// Superclasses are expressions in enclosing scope — resolve pass handles them.
		body := childByField(n, "body")
		if body != nil {
			pyCollectBodyDefs(a, body, clsScope)
			pyCollectNested(a, body, clsScope)
		}
		return

	case "list_comprehension", "set_comprehension", "dictionary_comprehension", "generator_expression":
		// Comprehension introduces a scope for the for-targets.
		compScope := a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "for_in_clause" {
				// left side is the target pattern
				if left := ch.NamedChild(0); left != nil {
					pyBindPattern(a, left, compScope, "var")
				}
			}
		}
		// Recurse into nested comps / nested functions inside the comp.
		for _, ch := range namedChildren(n) {
			pyCollect(a, ch, compScope)
		}
		return
	}

	// Default: recurse, staying in the same scope.
	for _, ch := range namedChildren(n) {
		pyCollect(a, ch, sc)
	}
}

// pyCollectNested only descends into nested function/class/comprehension
// constructs (whose bodies were already def-scanned by pyCollectBodyDefs).
func pyCollectNested(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_definition", "lambda", "class_definition",
		"list_comprehension", "set_comprehension", "dictionary_comprehension",
		"generator_expression":
		pyCollect(a, n, sc)
		return
	}
	for _, ch := range namedChildren(n) {
		pyCollectNested(a, ch, sc)
	}
}

// pyCollectBodyDefs records local bindings in a function/class body without
// entering nested function/class scopes (those get their own collect).
func pyCollectBodyDefs(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_definition", "lambda", "class_definition",
		"list_comprehension", "set_comprehension", "dictionary_comprehension",
		"generator_expression":
		// Nested scope: name already (or will be) bound in current scope by
		// pyCollect; don't harvest internals here.
		if n.Kind() == "function_definition" || n.Kind() == "class_definition" {
			if nameNode := childByField(n, "name"); nameNode != nil {
				// Binding is created when pyCollectNested → pyCollect runs;
				// pre-bind here so the name is local for the whole body.
				_ = a.newBinding(a.text(nameNode), kindForDef(n.Kind()), nameNode.StartByte(), nameNode.EndByte(), sc)
			}
		}
		return

	case "global_statement":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" {
				sc.globals[a.text(ch)] = true
			}
		}
		return

	case "nonlocal_statement":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" {
				sc.nonlocals[a.text(ch)] = true
			}
		}
		return

	case "assignment", "augmented_assignment", "named_expression":
		// Left-hand side(s) are binding sites.
		left := childByField(n, "left")
		if left == nil {
			// walrus: name = value fields are "name" / "value"
			left = childByField(n, "name")
		}
		if left != nil {
			pyBindPattern(a, left, sc, "var")
		}
		// Right-hand side may contain nested defs — walk it for nested only
		// via the recursive default below, but skip re-binding the left.
		right := childByField(n, "right")
		if right == nil {
			right = childByField(n, "value")
		}
		if right != nil {
			pyCollectBodyDefs(a, right, sc)
		}
		// Type annotation on assignment may hold names — not bindings.
		return

	case "for_statement":
		// for target in iterable: body
		// target is first named child typically; use field if present.
		left := childByField(n, "left")
		if left == nil {
			// tree-sitter-python: field "left" for the target.
			for _, ch := range namedChildren(n) {
				if ch.Kind() != "identifier" && ch.Kind() != "pattern_list" &&
					ch.Kind() != "tuple_pattern" && ch.Kind() != "list_pattern" &&
					ch.Kind() != "attribute" && ch.Kind() != "subscript" {
					continue
				}
				// Heuristic: first pattern-like child before the iterable.
				left = ch
				break
			}
		}
		if left != nil {
			pyBindPattern(a, left, sc, "var")
		}
		for _, ch := range namedChildren(n) {
			if ch == left {
				continue
			}
			pyCollectBodyDefs(a, ch, sc)
		}
		return

	case "with_statement":
		for _, ch := range namedChildren(n) {
			pyCollectBodyDefs(a, ch, sc)
		}
		return

	case "with_item":
		// as pattern
		if alias := childByField(n, "alias"); alias != nil {
			pyBindPattern(a, alias, sc, "var")
		}
		for _, ch := range namedChildren(n) {
			pyCollectBodyDefs(a, ch, sc)
		}
		return

	case "except_clause":
		// except E as name
		var foundAs bool
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "as" || a.text(ch) == "as" {
				foundAs = true
				continue
			}
			if foundAs && ch.Kind() == "identifier" {
				_ = a.newBinding(a.text(ch), "var", ch.StartByte(), ch.EndByte(), sc)
				foundAs = false
			}
			pyCollectBodyDefs(a, ch, sc)
		}
		return

	case "import_statement":
		// import foo, bar as baz
		for _, ch := range namedChildren(n) {
			pyBindImportName(a, ch, sc)
		}
		return

	case "import_from_statement":
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "dotted_name" || ch.Kind() == "relative_import" {
				continue // module path, not a binding
			}
			pyBindImportName(a, ch, sc)
		}
		return

	case "aliased_import":
		pyBindImportName(a, n, sc)
		return
	}

	for _, ch := range namedChildren(n) {
		pyCollectBodyDefs(a, ch, sc)
	}
}

func kindForDef(nodeKind string) string {
	if nodeKind == "class_definition" {
		return "class"
	}
	return "func"
}

func pyCollectParams(a *fileAnalysis, params *tree_sitter.Node, sc *scope) {
	walkNamed(params, func(n *tree_sitter.Node) bool {
		switch n.Kind() {
		case "identifier":
			// Bare param name. Skip if it's inside a default value expression
			// (parent is not a parameter-shaped node).
			parent := n.Parent()
			if parent == nil {
				return false
			}
			switch parent.Kind() {
			case "parameters", "lambda_parameters", "default_parameter",
				"typed_parameter", "typed_default_parameter",
				"list_splat_pattern", "dictionary_splat_pattern",
				"tuple_pattern", "list_pattern":
				b := a.newBinding(a.text(n), "param", n.StartByte(), n.EndByte(), sc)
				a.addOcc(n.StartByte(), n.EndByte(), b.name, b, true)
			}
			return false
		case "default_parameter", "typed_default_parameter":
			// Walk name but not the default expression for bindings.
			if name := childByField(n, "name"); name != nil {
				pyCollectParams(a, name, sc)
			} else if n.NamedChildCount() > 0 {
				// first named child is the name/pattern
				first := n.NamedChild(0)
				if first.Kind() == "identifier" || first.Kind() == "typed_parameter" {
					pyCollectParams(a, first, sc)
				}
			}
			return false
		}
		return true
	})
}

// pyBindPattern binds names in an assignment/for/with target pattern.
func pyBindPattern(a *fileAnalysis, n *tree_sitter.Node, sc *scope, kind string) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "identifier":
		// Skip if this is an attribute write (handled by attribute case).
		_ = a.newBinding(a.text(n), kind, n.StartByte(), n.EndByte(), sc)
	case "attribute", "subscript":
		// obj.x = … / obj[i] = … do not bind a bare name.
		return
	case "tuple", "list", "tuple_pattern", "list_pattern", "pattern_list":
		for _, ch := range namedChildren(n) {
			pyBindPattern(a, ch, sc, kind)
		}
	case "list_splat_pattern", "dictionary_splat_pattern":
		for _, ch := range namedChildren(n) {
			pyBindPattern(a, ch, sc, kind)
		}
	default:
		// typed assignment left may wrap identifier
		if n.Kind() == "identifier" {
			_ = a.newBinding(a.text(n), kind, n.StartByte(), n.EndByte(), sc)
		}
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "identifier" || ch.Kind() == "tuple_pattern" ||
				ch.Kind() == "list_pattern" || ch.Kind() == "pattern_list" ||
				ch.Kind() == "tuple" || ch.Kind() == "list" ||
				ch.Kind() == "list_splat_pattern" {
				pyBindPattern(a, ch, sc, kind)
			}
		}
	}
}

func pyBindImportName(a *fileAnalysis, n *tree_sitter.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "aliased_import":
		// import foo as bar → bind bar (field "alias"); else bind name.
		if alias := childByField(n, "alias"); alias != nil {
			_ = a.newBinding(a.text(alias), "import", alias.StartByte(), alias.EndByte(), sc)
			return
		}
		if name := childByField(n, "name"); name != nil {
			// Use the first identifier of a dotted name.
			id := firstIdentifier(name)
			if id != nil {
				_ = a.newBinding(a.text(id), "import", id.StartByte(), id.EndByte(), sc)
			}
		}
	case "dotted_name":
		id := firstIdentifier(n)
		if id != nil {
			_ = a.newBinding(a.text(id), "import", id.StartByte(), id.EndByte(), sc)
		}
	case "identifier":
		_ = a.newBinding(a.text(n), "import", n.StartByte(), n.EndByte(), sc)
	case "wildcard_import":
		// from x import * — no local names we can track.
	default:
		for _, ch := range namedChildren(n) {
			pyBindImportName(a, ch, sc)
		}
	}
}

func firstIdentifier(n *tree_sitter.Node) *tree_sitter.Node {
	if n == nil {
		return nil
	}
	if n.Kind() == "identifier" {
		return n
	}
	for _, ch := range namedChildren(n) {
		if id := firstIdentifier(ch); id != nil {
			return id
		}
	}
	return nil
}

// pyResolve walks the tree recording every identifier occurrence and
// resolving it against the scope chain.
func pyResolve(a *fileAnalysis, n *tree_sitter.Node, sc *scope, inFunction bool) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_definition", "lambda":
		fnScope := a.scopeAt(n.StartByte(), n.EndByte(), scopeFunction)
		if fnScope == nil {
			fnScope = a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		}
		// Name was already recorded as a def occ in collect.
		params := childByField(n, "parameters")
		if params == nil {
			for _, ch := range namedChildren(n) {
				if ch.Kind() == "lambda_parameters" || ch.Kind() == "parameters" {
					params = ch
				}
			}
		}
		// Param identifiers already have def occs; still walk defaults for refs.
		if params != nil {
			pyResolveParamDefaults(a, params, sc, inFunction)
		}
		body := childByField(n, "body")
		if body != nil {
			pyResolve(a, body, fnScope, true)
		}
		// lambda body may not be under "body"
		if body == nil && n.Kind() == "lambda" {
			for _, ch := range namedChildren(n) {
				if ch.Kind() != "lambda_parameters" && ch.Kind() != "parameters" {
					pyResolve(a, ch, fnScope, true)
				}
			}
		}
		return

	case "class_definition":
		clsScope := a.scopeAt(n.StartByte(), n.EndByte(), scopeClass)
		if clsScope == nil {
			clsScope = a.trackScope(newScope(sc, scopeClass, n.StartByte(), n.EndByte()))
		}
		// Superclasses resolve in enclosing scope.
		for _, ch := range namedChildren(n) {
			if ch.Kind() == "argument_list" || ch.Kind() == "identifier" {
				// type params / superclasses — resolve in outer scope
				if ch != childByField(n, "name") && ch != childByField(n, "body") {
					pyResolve(a, ch, sc, inFunction)
				}
			}
		}
		if body := childByField(n, "body"); body != nil {
			// Class body itself is not "in function" for LEGB-from-class-body
			// lookups, but methods inside will set inFunction=true.
			pyResolve(a, body, clsScope, false)
		}
		return

	case "list_comprehension", "set_comprehension", "dictionary_comprehension", "generator_expression":
		compScope := a.scopeAt(n.StartByte(), n.EndByte(), scopeFunction)
		if compScope == nil {
			compScope = a.trackScope(newScope(sc, scopeFunction, n.StartByte(), n.EndByte()))
		}
		for _, ch := range namedChildren(n) {
			pyResolve(a, ch, compScope, true)
		}
		return

	case "identifier":
		// Skip if this identifier is a definition site already recorded, or
		// a binding target we should attribute as def.
		name := a.text(n)
		parent := n.Parent()
		if parent != nil {
			switch parent.Kind() {
			case "attribute":
				// obj.attr — only the object is a bare-name ref; attr is a field.
				if attr := childByField(parent, "attribute"); sameNode(attr, n) {
					return
				}
				if parent.NamedChildCount() >= 2 {
					last := parent.NamedChild(parent.NamedChildCount() - 1)
					if sameNode(last, n) {
						return
					}
				}
			case "global_statement", "nonlocal_statement":
				// Declarations, not refs — still record as free of binding change.
				a.addOcc(n.StartByte(), n.EndByte(), name, nil, false)
				return
			case "keyword_argument":
				// func(name=value) — the keyword name is not a variable.
				if sameNode(childByField(parent, "name"), n) {
					return
				}
			}
		}
		// If collect already added a def occ at this exact range, skip.
		if pyAlreadyRecorded(a, n.StartByte(), n.EndByte()) {
			return
		}
		// Is this a binding target (LHS)?
		if pyIsBindingTarget(n) {
			b := sc.defs[name]
			// May be in an enclosing scope for class-body attributes etc.
			if b == nil {
				b = resolveName(sc, name, inFunction)
			}
			// Prefer the defining scope's binding.
			if b == nil {
				// Should have been collected; treat as def in current scope.
				b = a.newBinding(name, "var", n.StartByte(), n.EndByte(), sc)
			}
			a.addOcc(n.StartByte(), n.EndByte(), name, b, true)
			return
		}
		b := resolveName(sc, name, inFunction)
		a.addOcc(n.StartByte(), n.EndByte(), name, b, false)
		return

	case "type":
		// Type annotations — resolve names as refs (not defs).
		for _, ch := range namedChildren(n) {
			pyResolve(a, ch, sc, inFunction)
		}
		return
	}

	for _, ch := range namedChildren(n) {
		pyResolve(a, ch, sc, inFunction)
	}
}

func pyResolveParamDefaults(a *fileAnalysis, params *tree_sitter.Node, outer *scope, inFunction bool) {
	// Default values resolve in the enclosing scope, not the function scope.
	walkNamed(params, func(n *tree_sitter.Node) bool {
		switch n.Kind() {
		case "default_parameter", "typed_default_parameter":
			if val := childByField(n, "value"); val != nil {
				pyResolve(a, val, outer, inFunction)
			} else if n.NamedChildCount() >= 2 {
				// last named child is often the default
				pyResolve(a, n.NamedChild(n.NamedChildCount()-1), outer, inFunction)
			}
			return false
		}
		return true
	})
}

func pyAlreadyRecorded(a *fileAnalysis, start, end uint) bool {
	for i := len(a.occurrences) - 1; i >= 0; i-- {
		o := a.occurrences[i]
		if o.start == start && o.end == end {
			return true
		}
		// Occurrences are roughly in order; early exit if we've gone past.
		if o.start < start-256 {
			break
		}
	}
	// Full scan fallback for safety on small files.
	for _, o := range a.occurrences {
		if o.start == start && o.end == end {
			return true
		}
	}
	return false
}

// pyIsBindingTarget reports whether identifier node n is on the binding side
// of an assignment/for/with/import/param (and should be treated as a def).
func pyIsBindingTarget(n *tree_sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case "parameters", "lambda_parameters", "default_parameter",
		"typed_parameter", "typed_default_parameter",
		"list_splat_pattern", "dictionary_splat_pattern":
		return true
	case "assignment", "augmented_assignment":
		left := childByField(parent, "left")
		return nodeContains(left, n)
	case "named_expression":
		return sameNode(childByField(parent, "name"), n)
	case "for_statement":
		left := childByField(parent, "left")
		return nodeContains(left, n)
	case "tuple", "list", "tuple_pattern", "list_pattern", "pattern_list":
		// Could be LHS or RHS — walk up.
		return pyIsBindingTarget(parent)
	case "with_item":
		alias := childByField(parent, "alias")
		return nodeContains(alias, n)
	case "aliased_import":
		alias := childByField(parent, "alias")
		if alias != nil {
			return nodeContains(alias, n)
		}
		return nodeContains(childByField(parent, "name"), n)
	case "import_statement", "import_from_statement", "dotted_name":
		// Import names are defs — but dotted_name insides need care.
		return true
	case "except_clause":
		return true // approximate
	case "function_definition", "class_definition":
		return sameNode(childByField(parent, "name"), n)
	}
	return false
}


