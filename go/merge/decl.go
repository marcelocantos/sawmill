// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
)

// declKind categorises a top-level (or one-level-nested) declaration for
// merge purposes. The classifier is intentionally coarse — only enough
// to drive the algebra and the import-list special case.
type declKind string

const (
	kindPackage  declKind = "package"
	kindFunction declKind = "function"
	kindMethod   declKind = "method"
	kindType     declKind = "type"
	kindField    declKind = "field"
	kindImport   declKind = "import"
	kindOther    declKind = "other"
)

// decl is a structural slice of a source file: one named declaration
// at top level, or one method/field nested inside a type declaration.
//
// Start/End delimit the *outer* byte range — including leading trivia
// (comments, decorators, the blank line above) up through the trailing
// newline of the declaration. This way replacing the slice in place
// doesn't require touching surrounding whitespace.
type decl struct {
	Kind      declKind
	Container string // empty for top-level; type name for nested method/field
	Name      string
	Start     int
	End       int
	// Body is the byte range of the declaration body for nested-merge
	// of class/struct contents. Zero End means "no body" (e.g. an
	// import statement).
	BodyStart int
	BodyEnd   int
	// Fingerprint hashes whitespace-normalised source over [Start:End)
	// for fast identity tests. Two decls with equal fingerprints are
	// byte-equivalent up to whitespace runs and trailing newlines.
	Fingerprint string
	// Members hold the nested decls (methods, fields) when this decl
	// is a container (kindType). Empty for leaves.
	Members []*decl
}

// declKey is the cross-version identity key.
type declKey struct {
	Container string
	Kind      declKind
	Name      string
}

func (d *decl) key() declKey {
	return declKey{Container: d.Container, Kind: d.Kind, Name: d.Name}
}

// extractDeclarations walks the top level of tree and returns one decl
// per syntactic unit, in source order. For type declarations whose body
// contains method/field children, the children are populated as
// Members and a corresponding flat list is also returned via the
// caller-side flattening.
//
// The function is best-effort: anything the classifier doesn't
// recognise is captured as kindOther and merged textually.
func extractDeclarations(source []byte, tree *tree_sitter.Tree, adapter adapters.LanguageAdapter) []*decl {
	if tree == nil {
		return nil
	}
	root := tree.RootNode()
	if root == nil {
		return nil
	}

	var decls []*decl
	count := root.NamedChildCount()
	prevEnd := 0
	for i := uint(0); i < count; i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		d := classifyTopLevel(source, child, adapter)
		if d == nil {
			continue
		}
		// Extend Start back over leading trivia (blank lines + adjacent
		// comments) so that re-splicing preserves visual blocking.
		d.Start = expandLeadingTrivia(source, prevEnd, d.Start)
		// Extend End forward to include the trailing newline if present.
		d.End = expandTrailingNewline(source, d.End)
		d.Fingerprint = fingerprint(source[d.Start:d.End])
		decls = append(decls, d)
		prevEnd = d.End
	}
	return decls
}

// classifyTopLevel inspects a top-level CST node and returns a decl
// (with Members populated for type containers) or nil to skip.
func classifyTopLevel(source []byte, node *tree_sitter.Node, adapter adapters.LanguageAdapter) *decl {
	kind := node.Kind()
	start := int(node.StartByte())
	end := int(node.EndByte())

	switch kind {
	// ─── Python ────────────────────────────────────────────────────
	case "import_statement", "import_from_statement", "future_import_statement":
		return &decl{Kind: kindImport, Name: importName(source, node), Start: start, End: end}
	case "function_definition":
		return &decl{Kind: kindFunction, Name: childText(source, node, "name"), Start: start, End: end}
	case "class_definition":
		return &decl{
			Kind:      kindType,
			Name:      childText(source, node, "name"),
			Start:     start,
			End:       end,
			BodyStart: bodyStart(node),
			BodyEnd:   bodyEnd(node),
			Members:   extractClassMembers(source, node, adapter),
		}
	case "decorated_definition":
		// Wrap a function or class with decorators; descend to the
		// inner definition to identify it, then widen the range to
		// include the decorators.
		inner := decoratedInner(node)
		if inner == nil {
			return nil
		}
		d := classifyTopLevel(source, inner, adapter)
		if d == nil {
			return nil
		}
		d.Start = start // decorators come first
		return d

	// ─── Go ────────────────────────────────────────────────────────
	case "import_declaration":
		// Go groups imports inside one declaration with one or more
		// import_spec children. Return one decl per spec so the
		// import-list merge can union them individually.
		return goImportContainer(source, node)
	case "function_declaration":
		return &decl{Kind: kindFunction, Name: childText(source, node, "name"), Start: start, End: end}
	case "method_declaration":
		// Top-level method (Go style: receiver-bound). Treat as a
		// nested member of the receiver type so parallel method adds
		// merge cleanly.
		recv := goReceiverType(source, node)
		return &decl{
			Kind:      kindMethod,
			Container: recv,
			Name:      childText(source, node, "name"),
			Start:     start,
			End:       end,
		}
	case "type_declaration":
		return goTypeDecl(source, node, adapter)
	case "var_declaration", "const_declaration":
		return &decl{Kind: kindOther, Name: firstName(source, node), Start: start, End: end}
	case "package_clause":
		// The package clause is anchor-only — never edited
		// independently — so it is treated as its own kindPackage
		// decl with a fixed name so it always matches across versions.
		// kindPackage gets its own ordering bucket (placed first).
		return &decl{Kind: kindPackage, Name: "__package__", Start: start, End: end}

	default:
		// Comments, blank lines, etc. are absorbed by the
		// trivia-expansion step in extractDeclarations.
		if !node.IsNamed() || isTriviaKind(kind) {
			return nil
		}
		return &decl{Kind: kindOther, Name: firstName(source, node), Start: start, End: end}
	}
}

// extractClassMembers walks a Python class body and returns one decl
// per method or field-like assignment.
func extractClassMembers(source []byte, classNode *tree_sitter.Node, adapter adapters.LanguageAdapter) []*decl {
	body := classNode.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	className := childText(source, classNode, "name")
	var members []*decl
	count := body.NamedChildCount()
	prevEnd := int(body.StartByte())
	for i := uint(0); i < count; i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		var m *decl
		switch child.Kind() {
		case "function_definition":
			m = &decl{
				Kind:      kindMethod,
				Container: className,
				Name:      childText(source, child, "name"),
				Start:     int(child.StartByte()),
				End:       int(child.EndByte()),
			}
		case "decorated_definition":
			inner := decoratedInner(child)
			if inner == nil || inner.Kind() != "function_definition" {
				continue
			}
			m = &decl{
				Kind:      kindMethod,
				Container: className,
				Name:      childText(source, inner, "name"),
				Start:     int(child.StartByte()),
				End:       int(child.EndByte()),
			}
		case "expression_statement":
			// e.g. `name: int = 0` style attribute or docstring; treat
			// as a named field if it looks like an assignment.
			name := assignmentTarget(source, child)
			if name == "" {
				continue
			}
			m = &decl{
				Kind:      kindField,
				Container: className,
				Name:      name,
				Start:     int(child.StartByte()),
				End:       int(child.EndByte()),
			}
		default:
			continue
		}
		m.Start = expandLeadingTrivia(source, prevEnd, m.Start)
		m.End = expandTrailingNewline(source, m.End)
		m.Fingerprint = fingerprint(source[m.Start:m.End])
		members = append(members, m)
		prevEnd = m.End
	}
	_ = adapter // reserved for future per-language member classifiers
	return members
}

// goImportContainer treats Go's `import (...)` block as a single
// container whose Members are the individual import_spec entries —
// this maps cleanly onto the import-list union merge.
func goImportContainer(source []byte, node *tree_sitter.Node) *decl {
	d := &decl{
		Kind:      kindImport,
		Name:      "__imports__",
		Start:     int(node.StartByte()),
		End:       int(node.EndByte()),
		BodyStart: int(node.StartByte()),
		BodyEnd:   int(node.EndByte()),
	}
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "import_spec":
			d.Members = append(d.Members, importSpecDecl(source, child))
		case "import_spec_list":
			scount := child.NamedChildCount()
			for j := uint(0); j < scount; j++ {
				spec := child.NamedChild(j)
				if spec != nil && spec.Kind() == "import_spec" {
					d.Members = append(d.Members, importSpecDecl(source, spec))
				}
			}
		}
	}
	return d
}

func importSpecDecl(source []byte, node *tree_sitter.Node) *decl {
	path := node.ChildByFieldName("path")
	name := ""
	if path != nil {
		name = string(source[path.StartByte():path.EndByte()])
	}
	return &decl{
		Kind:  kindImport,
		Name:  name,
		Start: int(node.StartByte()),
		End:   int(node.EndByte()),
	}
}

// goTypeDecl unwraps a Go `type Foo struct { ... }` (or alias) and
// returns a kindType decl with field Members where applicable.
func goTypeDecl(source []byte, node *tree_sitter.Node, adapter adapters.LanguageAdapter) *decl {
	var spec *tree_sitter.Node
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "type_spec" || c.Kind() == "type_alias" {
			spec = c
			break
		}
	}
	if spec == nil {
		return &decl{Kind: kindType, Start: int(node.StartByte()), End: int(node.EndByte())}
	}
	name := childText(source, spec, "name")
	d := &decl{
		Kind:  kindType,
		Name:  name,
		Start: int(node.StartByte()),
		End:   int(node.EndByte()),
	}
	// Find the underlying body (struct_type → field_declaration_list).
	typeBody := spec.ChildByFieldName("type")
	if typeBody == nil {
		return d
	}
	if typeBody.Kind() == "struct_type" {
		fl := findChildOfKind(typeBody, "field_declaration_list")
		if fl != nil {
			d.BodyStart = int(fl.StartByte())
			d.BodyEnd = int(fl.EndByte())
			fcount := fl.NamedChildCount()
			prevEnd := d.BodyStart
			for i := uint(0); i < fcount; i++ {
				field := fl.NamedChild(i)
				if field == nil || field.Kind() != "field_declaration" {
					continue
				}
				fname := childText(source, field, "name")
				if fname == "" {
					// embedded type — use the type identifier as the name
					if t := field.ChildByFieldName("type"); t != nil {
						fname = string(source[t.StartByte():t.EndByte()])
					}
				}
				m := &decl{
					Kind:      kindField,
					Container: name,
					Name:      fname,
					Start:     int(field.StartByte()),
					End:       int(field.EndByte()),
				}
				m.Start = expandLeadingTrivia(source, prevEnd, m.Start)
				m.End = expandTrailingNewline(source, m.End)
				m.Fingerprint = fingerprint(source[m.Start:m.End])
				d.Members = append(d.Members, m)
				prevEnd = m.End
			}
		}
	}
	_ = adapter
	return d
}

// importName extracts the module path from a Python import statement.
// Used purely for identity within the import-merge; never used for
// rewriting.
func importName(source []byte, node *tree_sitter.Node) string {
	// import_statement: child name field is dotted_name (or
	// aliased_import containing dotted_name + alias).
	// import_from_statement: module_name field is the source module.
	if mn := node.ChildByFieldName("module_name"); mn != nil {
		return string(source[mn.StartByte():mn.EndByte()])
	}
	if n := node.ChildByFieldName("name"); n != nil {
		return string(source[n.StartByte():n.EndByte()])
	}
	// Fall back to whatever appears between the leading keyword and
	// the trailing newline — uniqueness, not parseability, matters.
	text := string(source[node.StartByte():node.EndByte()])
	return strings.TrimSpace(text)
}

func childText(source []byte, node *tree_sitter.Node, field string) string {
	c := node.ChildByFieldName(field)
	if c == nil {
		return ""
	}
	return string(source[c.StartByte():c.EndByte()])
}

func decoratedInner(node *tree_sitter.Node) *tree_sitter.Node {
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "function_definition", "class_definition":
			return c
		}
	}
	return nil
}

func bodyStart(node *tree_sitter.Node) int {
	if b := node.ChildByFieldName("body"); b != nil {
		return int(b.StartByte())
	}
	return 0
}
func bodyEnd(node *tree_sitter.Node) int {
	if b := node.ChildByFieldName("body"); b != nil {
		return int(b.EndByte())
	}
	return 0
}

func goReceiverType(source []byte, methodNode *tree_sitter.Node) string {
	recv := methodNode.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	// receiver is a parameter_list with one parameter_declaration whose
	// type field is the receiver type (possibly a pointer_type).
	count := recv.NamedChildCount()
	for i := uint(0); i < count; i++ {
		p := recv.NamedChild(i)
		if p == nil {
			continue
		}
		t := p.ChildByFieldName("type")
		if t == nil {
			continue
		}
		// Strip a leading * for pointer receivers.
		txt := strings.TrimPrefix(string(source[t.StartByte():t.EndByte()]), "*")
		return strings.TrimSpace(txt)
	}
	return ""
}

func firstName(source []byte, node *tree_sitter.Node) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return string(source[n.StartByte():n.EndByte()])
	}
	// Otherwise fall back to the node's text — used only for
	// identity, not display.
	return string(source[node.StartByte():node.EndByte()])
}

func findChildOfKind(node *tree_sitter.Node, kind string) *tree_sitter.Node {
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

func assignmentTarget(source []byte, exprStmt *tree_sitter.Node) string {
	count := exprStmt.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := exprStmt.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "assignment":
			if left := c.ChildByFieldName("left"); left != nil {
				return string(source[left.StartByte():left.EndByte()])
			}
		case "string": // bare docstring — no name
			return ""
		}
	}
	return ""
}

func isTriviaKind(kind string) bool {
	switch kind {
	case "comment", "line_comment", "block_comment":
		return true
	}
	return false
}

// expandLeadingTrivia walks backward from start over blank (whitespace
// only) lines, stopping at the first non-blank line or at prevEnd
// (the end of the previous decl, or 0 for the first one). Comments
// are *not* absorbed: they stay attached to whatever decl they
// originally followed.
func expandLeadingTrivia(source []byte, prevEnd, start int) int {
	pos := start
	for pos > prevEnd {
		// Find start of the line that immediately precedes pos. The
		// previous line's last byte is at pos-1 (often '\n'). Walk
		// back to the byte after the newline that begins it.
		lineStart := pos - 1
		for lineStart > prevEnd && source[lineStart-1] != '\n' {
			lineStart--
		}
		// If pos already sits at a line boundary (source[pos-1] is
		// '\n'), the "line" we examined is just that lone newline —
		// treat it as blank and absorb it.
		line := source[lineStart:pos]
		if strings.TrimSpace(string(line)) != "" {
			break
		}
		if lineStart == pos {
			// Defensive: no progress would loop forever.
			break
		}
		pos = lineStart
	}
	if pos < prevEnd {
		pos = prevEnd
	}
	return pos
}

// expandTrailingNewline pushes End past a single trailing '\n' if
// present so the slice ends at a line boundary.
func expandTrailingNewline(source []byte, end int) int {
	if end < len(source) && source[end] == '\n' {
		return end + 1
	}
	return end
}

// fingerprint returns a short stable hash of the whitespace-normalised
// source range. Whitespace runs collapse to a single space and leading
// and trailing whitespace is trimmed, so cosmetic diffs don't move the
// fingerprint.
func fingerprint(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	inSpace := true
	for _, c := range b {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		sb.WriteByte(c)
		inSpace = false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(sb.String())))
	return hex.EncodeToString(sum[:8])
}
