// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package rewrite

import (
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
)

// bindingID uniquely identifies a declaration within one analysed file.
type bindingID int

// scopeKind classifies a lexical scope.
type scopeKind int

const (
	scopeModule scopeKind = iota // file / module / package
	scopeFunction
	scopeClass  // Python class body (skipped for LEGB from nested functions)
	scopeBlock  // Go bare block, if/for/switch bodies
	scopeType   // Go type body (struct/interface fields)
)

// scope is one frame in the lexical scope chain.
type scope struct {
	parent *scope
	kind   scopeKind
	// start/end are the byte range of the construct that opened this scope.
	start, end uint
	// defs maps name → binding declared in this scope.
	defs map[string]*binding
	// Python: names declared global / nonlocal in a function scope.
	globals   map[string]bool
	nonlocals map[string]bool
	// Go: simple local type of a name when known (param/var with type_identifier).
	types map[string]string
}

func newScope(parent *scope, kind scopeKind, start, end uint) *scope {
	return &scope{
		parent:    parent,
		kind:      kind,
		start:     start,
		end:       end,
		defs:      make(map[string]*binding),
		globals:   make(map[string]bool),
		nonlocals: make(map[string]bool),
		types:     make(map[string]string),
	}
}

// binding is a named declaration.
type binding struct {
	id       bindingID
	name     string
	kind     string // "var", "param", "func", "class", "type", "field", "import", "const"
	defStart uint
	defEnd   uint
	scope    *scope
	// ownerType is set for Go struct/interface fields (e.g. "Point").
	ownerType string
}

// occurrence is one identifier node in the source, optionally resolved.
type occurrence struct {
	start, end uint
	name       string
	// binding is the resolved declaration; nil means free/builtin/unresolved.
	binding *binding
	// isDef is true when this node is a defining occurrence of binding.
	isDef bool
	// isFieldUse is true for Go field_identifier uses (selectors).
	isFieldUse bool
	// fieldOwner is the simple type of the selector base when known.
	fieldOwner string
}

// fileAnalysis holds the scope/binding model for one source file.
type fileAnalysis struct {
	source      []byte
	root        *scope
	scopes      []*scope // all scopes, for range lookup during resolve
	bindings    []*binding
	occurrences []occurrence
	nextID      bindingID
}

// trackScope records a scope for later lookup by source range.
func (a *fileAnalysis) trackScope(sc *scope) *scope {
	a.scopes = append(a.scopes, sc)
	return sc
}

// scopeAt returns the tracked scope whose range matches start/end and kind.
func (a *fileAnalysis) scopeAt(start, end uint, kind scopeKind) *scope {
	for _, sc := range a.scopes {
		if sc.start == start && sc.end == end && sc.kind == kind {
			return sc
		}
	}
	return nil
}

func (a *fileAnalysis) newBinding(name, kind string, defStart, defEnd uint, sc *scope) *binding {
	b := &binding{
		id:       a.nextID,
		name:     name,
		kind:     kind,
		defStart: defStart,
		defEnd:   defEnd,
		scope:    sc,
	}
	a.nextID++
	a.bindings = append(a.bindings, b)
	// First declaration of a name in a scope wins (redeclarations share it).
	if existing, ok := sc.defs[name]; ok {
		return existing
	}
	sc.defs[name] = b
	return b
}

func (a *fileAnalysis) newFieldBinding(name, ownerType string, defStart, defEnd uint, sc *scope) *binding {
	// Fields are keyed by ownerType.name so Point.X and Size.X stay distinct
	// even though they share a scope node type.
	key := ownerType + "." + name
	if existing, ok := sc.defs[key]; ok {
		return existing
	}
	b := &binding{
		id:        a.nextID,
		name:      name,
		kind:      "field",
		defStart:  defStart,
		defEnd:    defEnd,
		scope:     sc,
		ownerType: ownerType,
	}
	a.nextID++
	a.bindings = append(a.bindings, b)
	sc.defs[key] = b
	// Also index bare name → first field (used only when unique).
	if _, ok := sc.defs[name]; !ok {
		sc.defs[name] = b
	}
	return b
}

func (a *fileAnalysis) addOcc(start, end uint, name string, b *binding, isDef bool) {
	a.occurrences = append(a.occurrences, occurrence{
		start:   start,
		end:     end,
		name:    name,
		binding: b,
		isDef:   isDef,
	})
}

func (a *fileAnalysis) addFieldOcc(start, end uint, name string, b *binding, isDef bool, owner string) {
	a.occurrences = append(a.occurrences, occurrence{
		start:      start,
		end:        end,
		name:       name,
		binding:    b,
		isDef:      isDef,
		isFieldUse: !isDef,
		fieldOwner: owner,
	})
}

// text returns the source slice for a node.
func (a *fileAnalysis) text(n *tree_sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(a.source[n.StartByte():n.EndByte()])
}

// analyseFile builds a scope model for languages that support it.
// Returns nil when the language has no analyser (caller falls back to
// text-equality rename).
func analyseFile(source []byte, tree *tree_sitter.Tree, adapter adapters.LanguageAdapter) *fileAnalysis {
	if tree == nil || adapter == nil {
		return nil
	}
	switch adapter.(type) {
	case *adapters.PythonAdapter:
		return analysePython(source, tree)
	case *adapters.GoAdapter:
		return analyseGo(source, tree)
	default:
		return nil
	}
}

// freeBinding is a sentinel used when renaming free/unresolved references of
// a name (e.g. call sites in files that don't define the symbol).
var freeBinding = &binding{id: -1, name: "", kind: "free"}

// selectTargets chooses which binding(s) a rename should affect.
//
// With offset: the binding that the identifier at that byte offset resolves to
// (or free refs of that name if the occurrence is free).
//
// Without offset:
//   - file/module-scope bindings named `from`, plus free refs of `from`
//   - else the unique nested binding named `from`, if exactly one exists
//   - else free refs only (cross-file uses with no local def)
//   - else nil (ambiguous nested bindings — rename nothing rather than all)
func selectTargets(a *fileAnalysis, from string, offset *uint) []*binding {
	if a == nil {
		return nil
	}
	if offset != nil {
		occ := findOccurrence(a, *offset, from)
		if occ == nil {
			return nil
		}
		if occ.binding != nil {
			return []*binding{occ.binding}
		}
		// Free occurrence of the name — rename free refs of from.
		return []*binding{freeBinding}
	}

	var top, nested []*binding
	seen := make(map[bindingID]bool)
	for _, b := range a.bindings {
		if b.name != from {
			continue
		}
		if seen[b.id] {
			continue
		}
		seen[b.id] = true
		if b.scope != nil && (b.scope.kind == scopeModule) {
			top = append(top, b)
		} else {
			nested = append(nested, b)
		}
	}

	if len(top) > 0 {
		// Include freeBinding so free refs of the same name are rewritten
		// (call sites that resolve to this module-level symbol, and
		// cross-file uses when the def lives elsewhere).
		return append(top, freeBinding)
	}
	if len(nested) == 1 {
		return nested
	}
	if hasFreeRefs(a, from) && len(nested) == 0 {
		return []*binding{freeBinding}
	}
	// Multiple nested bindings, no module-level name: refuse without offset.
	return nil
}

func hasFreeRefs(a *fileAnalysis, name string) bool {
	for _, o := range a.occurrences {
		if o.name == name && o.binding == nil {
			return true
		}
	}
	return false
}

// findOccurrence returns the occurrence whose range covers offset, preferring
// an exact name match when provided.
func findOccurrence(a *fileAnalysis, offset uint, name string) *occurrence {
	var fallback *occurrence
	for i := range a.occurrences {
		o := &a.occurrences[i]
		if offset < o.start || offset >= o.end {
			// Also allow a caret sitting right at end of a single-char id.
			if !(offset == o.end && o.end == o.start+1) {
				continue
			}
		}
		if name == "" || o.name == name {
			return o
		}
		if fallback == nil {
			fallback = o
		}
	}
	return fallback
}

// editsForTargets builds rename edits for every occurrence of the selected
// bindings (and free refs when freeBinding is among targets).
func editsForTargets(a *fileAnalysis, from, to string, targets []*binding) []Edit {
	if len(targets) == 0 {
		return nil
	}
	want := make(map[bindingID]bool)
	wantFree := false
	for _, b := range targets {
		if b == freeBinding {
			wantFree = true
			continue
		}
		want[b.id] = true
	}

	var edits []Edit
	seen := make(map[uint]bool) // start byte de-dupe
	for _, o := range a.occurrences {
		if o.name != from {
			continue
		}
		match := false
		if o.binding != nil && want[o.binding.id] {
			match = true
		}
		if o.binding == nil && wantFree {
			match = true
		}
		if !match {
			continue
		}
		if seen[o.start] {
			continue
		}
		seen[o.start] = true
		edits = append(edits, Edit{
			Start:       o.start,
			End:         o.end,
			Replacement: to,
		})
	}
	return edits
}

// resolveName walks the scope chain for name, applying Python LEGB rules
// (skip class scopes when resolving from a function) and Go block scoping.
func resolveName(from *scope, name string, fromFunction bool) *binding {
	for s := from; s != nil; s = s.parent {
		if s.kind == scopeFunction {
			if s.globals[name] {
				// Jump to module scope.
				mod := s
				for mod.parent != nil {
					mod = mod.parent
				}
				if b, ok := mod.defs[name]; ok {
					return b
				}
				return nil
			}
			if s.nonlocals[name] {
				// Search enclosing non-class, non-module scopes.
				for p := s.parent; p != nil; p = p.parent {
					if p.kind == scopeClass {
						continue
					}
					if p.kind == scopeModule {
						break
					}
					if b, ok := p.defs[name]; ok {
						return b
					}
				}
				return nil
			}
		}
		if s.kind == scopeClass && fromFunction {
			// Class body is not visible to nested functions (Python LEGB).
			continue
		}
		if b, ok := s.defs[name]; ok {
			// Don't resolve bare names to field bindings (fields use
			// lookupFieldBinding via selectors).
			if b.kind == "field" && b.ownerType != "" {
				continue
			}
			return b
		}
	}
	return nil
}

// lookupFieldBinding scans collected bindings for ownerType.name.
func lookupFieldBinding(a *fileAnalysis, ownerType, name string) *binding {
	for _, b := range a.bindings {
		if b.kind == "field" && b.name == name && b.ownerType == ownerType {
			return b
		}
	}
	// Unique field of this name across the file?
	if ownerType == "" {
		var found *binding
		for _, b := range a.bindings {
			if b.kind == "field" && b.name == name {
				if found != nil {
					return nil // ambiguous
				}
				found = b
			}
		}
		return found
	}
	return nil
}

// lookupTypeOf returns the simple type of name in scope, if recorded.
func lookupTypeOf(from *scope, name string) string {
	for s := from; s != nil; s = s.parent {
		if t, ok := s.types[name]; ok {
			return t
		}
	}
	return ""
}

// sameNode reports whether a and b cover the same byte range (tscompat
// wrappers are not pointer-stable across ChildByFieldName calls).
func sameNode(a, b *tree_sitter.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

// nodeContains reports whether inner's range is inside outer's range.
func nodeContains(outer, inner *tree_sitter.Node) bool {
	if outer == nil || inner == nil {
		return false
	}
	return inner.StartByte() >= outer.StartByte() && inner.EndByte() <= outer.EndByte()
}

// childByField is a nil-safe ChildByFieldName.
func childByField(n *tree_sitter.Node, field string) *tree_sitter.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldName(field)
}

// namedChildren returns all named children of n.
func namedChildren(n *tree_sitter.Node) []*tree_sitter.Node {
	if n == nil {
		return nil
	}
	out := make([]*tree_sitter.Node, 0, n.NamedChildCount())
	for i := uint(0); i < n.NamedChildCount(); i++ {
		out = append(out, n.NamedChild(i))
	}
	return out
}

// walkNamed pre-order walks named descendants, calling fn for each.
// If fn returns false, children of that node are not visited.
func walkNamed(n *tree_sitter.Node, fn func(*tree_sitter.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		walkNamed(n.NamedChild(i), fn)
	}
}
