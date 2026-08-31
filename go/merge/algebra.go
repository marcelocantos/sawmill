// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"sort"
	"strings"
)

// resolution describes the merge plan's outcome for a single triple:
// what bytes (if any) should appear in the merged output, plus any
// residual conflicts attributed to this declaration.
type resolution struct {
	Key declKey
	// Source identifies where Bytes came from for diagnostics.
	Source string
	// Bytes is the rendered declaration. For an outright deletion,
	// Bytes is nil.
	Bytes []byte
	// Conflicts is the set of residual conflicts. Bytes has any
	// conflict markers already inlined.
	Conflicts []Conflict
	// TextMerged is true when this resolution went through the
	// line-level diff3 fallback (regardless of whether the fallback
	// produced clean output).
	TextMerged bool
}

// mergePlan is the per-container plan: an ordered list of resolutions
// to splice together.
type mergePlan struct {
	Resolutions []resolution
}

// planMerge produces the top-level mergePlan and recursively resolves
// nested containers (types/classes).
func planMerge(
	base, ours, theirs []byte,
	baseDecls, oursDecls, theirsDecls []*decl,
	opts Options,
) (mergePlan, error) {
	triples := matchDeclarations(baseDecls, oursDecls, theirsDecls)
	plan := mergePlan{}
	for _, t := range triples {
		switch t.Key.Kind {
		case kindImport:
			plan.Resolutions = append(plan.Resolutions, resolveImport(base, ours, theirs, t, opts))
		case kindType:
			plan.Resolutions = append(plan.Resolutions, resolveType(base, ours, theirs, t, opts))
		default:
			plan.Resolutions = append(plan.Resolutions, resolveLeaf(base, ours, theirs, t, opts))
		}
	}
	return plan, nil
}

// resolveLeaf handles a non-container declaration (function, method,
// field, var/const, etc.).
func resolveLeaf(base, ours, theirs []byte, t triple, opts Options) resolution {
	r := resolution{Key: t.Key}
	b, o, h := t.Base, t.Ours, t.Theirs

	// Categorise.
	op1 := classify(b, o) // base → ours
	op2 := classify(b, h) // base → theirs

	switch {
	case op1 == opUnchanged && op2 == opUnchanged:
		// Both sides are fingerprint-equal to base (the fingerprint
		// is whitespace-normalised). If a side has byte-different
		// bytes, that side reformatted — keep the reformat. Prefer
		// ours when both reformatted differently (arbitrary but
		// deterministic; reformat conflicts are visually noisy and
		// rarely meaningful).
		switch {
		case o != nil && b != nil && !byteEqual(bytesOf(ours, o), bytesOf(base, b)):
			r.Source = "unchanged-ours-reformat"
			r.Bytes = bytesOf(ours, o)
		case h != nil && b != nil && !byteEqual(bytesOf(theirs, h), bytesOf(base, b)):
			r.Source = "unchanged-theirs-reformat"
			r.Bytes = bytesOf(theirs, h)
		default:
			r.Source = "unchanged"
			if b != nil {
				r.Bytes = bytesOf(base, b)
			}
		}
	case op1 == opUnchanged:
		r.Source = "theirs"
		r.Bytes = renderSide(theirs, h)
	case op2 == opUnchanged:
		r.Source = "ours"
		r.Bytes = renderSide(ours, o)
	case op1 == opAdded && op2 == opAdded:
		// Both sides added a declaration with the same key.
		if equalFingerprint(o, h) {
			r.Source = "add-add-identical"
			r.Bytes = bytesOf(ours, o)
			break
		}
		// Different bodies — try a body-level diff3 with empty base.
		merged, conflicts, ok := tryBodyMerge([]byte{}, bytesOf(ours, o), bytesOf(theirs, h), t.Key, "add-add", opts)
		r.Bytes = merged
		r.Conflicts = conflicts
		r.TextMerged = ok
		r.Source = "add-add-merged"
	case op1 == opDeleted && op2 == opDeleted:
		r.Source = "delete-delete"
		r.Bytes = nil
	case op1 == opDeleted && op2 == opModified, op1 == opModified && op2 == opDeleted:
		// Delete vs modify — irreducible conflict. Render with diff3
		// markers showing the deletion as an empty side.
		oursBytes := renderSide(ours, o) // nil when op1==Deleted
		theirsBytes := renderSide(theirs, h)
		baseBytes := bytesOf(base, b)
		marker := makeConflictMarker(oursBytes, baseBytes, theirsBytes, opts)
		r.Bytes = marker
		r.Conflicts = []Conflict{{
			Path: opts.Path,
			Kind: deleteModifyKind(op1, op2),
			Decl: declLabel(t.Key),
		}}
		r.Source = "delete-modify"
	case op1 == opModified && op2 == opModified:
		if equalFingerprint(o, h) {
			r.Source = "modify-modify-identical"
			r.Bytes = bytesOf(ours, o)
			break
		}
		merged, conflicts, ok := tryBodyMerge(bytesOf(base, b), bytesOf(ours, o), bytesOf(theirs, h), t.Key, "modify-modify", opts)
		r.Bytes = merged
		r.Conflicts = conflicts
		r.TextMerged = ok
		r.Source = "modify-modify-merged"
	case op1 == opAdded:
		r.Source = "add-only-ours"
		r.Bytes = bytesOf(ours, o)
	case op2 == opAdded:
		r.Source = "add-only-theirs"
		r.Bytes = bytesOf(theirs, h)
	case op1 == opDeleted:
		r.Source = "delete-only-ours"
		r.Bytes = nil
	case op2 == opDeleted:
		r.Source = "delete-only-theirs"
		r.Bytes = nil
	case op1 == opModified:
		r.Source = "modify-only-ours"
		r.Bytes = bytesOf(ours, o)
	case op2 == opModified:
		r.Source = "modify-only-theirs"
		r.Bytes = bytesOf(theirs, h)
	default:
		// Should not happen — fall back to base.
		if b != nil {
			r.Bytes = bytesOf(base, b)
		}
	}
	return r
}

// resolveImport merges import-list containers (Go's import block) by
// taking the union of import specs by canonical text.
func resolveImport(base, ours, theirs []byte, t triple, opts Options) resolution {
	// If the kindImport decl is a leaf (Python imports), defer to the
	// leaf resolver — Python imports are individually keyed by module
	// name.
	if len(t.NestedChildren) == 0 {
		// Treat as leaf only if at least one side actually exists.
		return resolveLeaf(base, ours, theirs, t, opts)
	}
	// Container case (Go import block): union the children by name.
	r := resolution{Key: t.Key}
	keep := map[string][]byte{}
	order := []string{}
	add := func(specs []triple, side []byte) {
		for _, c := range specs {
			if c.Ours != nil && side != nil {
				if _, ok := keep[c.Key.Name]; !ok {
					order = append(order, c.Key.Name)
				}
				keep[c.Key.Name] = renderSide(side, c.Ours)
			}
		}
	}
	// Collect: base, then add ours and theirs additions.
	for _, c := range t.NestedChildren {
		if c.Base != nil {
			// Did either side delete it?
			if c.Ours == nil && c.Theirs != nil {
				continue // ours deleted
			}
			if c.Theirs == nil && c.Ours != nil {
				continue // theirs deleted
			}
			if c.Ours == nil && c.Theirs == nil {
				continue // both deleted
			}
			if _, ok := keep[c.Key.Name]; !ok {
				order = append(order, c.Key.Name)
			}
			keep[c.Key.Name] = bytesOf(base, c.Base)
		}
	}
	add(t.NestedChildren, ours)
	add(t.NestedChildren, theirs)
	for _, c := range t.NestedChildren {
		if c.Theirs != nil {
			if _, ok := keep[c.Key.Name]; !ok {
				order = append(order, c.Key.Name)
				keep[c.Key.Name] = renderSide(theirs, c.Theirs)
			}
		}
	}
	// Render as Go import block.
	sort.Strings(order)
	var sb strings.Builder
	sb.WriteString("import (\n")
	for _, name := range order {
		sb.WriteString("\t")
		sb.Write(stripTrailingNewline(keep[name]))
		sb.WriteString("\n")
	}
	sb.WriteString(")\n")
	r.Bytes = []byte(sb.String())
	r.Source = "import-union"
	return r
}

// resolveType handles a class/struct declaration and merges its
// members (methods/fields) recursively.
func resolveType(base, ours, theirs []byte, t triple, opts Options) resolution {
	r := resolution{Key: t.Key}
	b, o, h := t.Base, t.Ours, t.Theirs

	// If the type only exists on one side or two-side adds match, fall
	// back to the leaf resolver — there are no nested members to
	// reconcile.
	if len(t.NestedChildren) == 0 {
		return resolveLeaf(base, ours, theirs, t, opts)
	}
	// If a type was deleted on one side and modified on the other,
	// kick out a plain conflict — body-merging across deletion is too
	// risky to auto-resolve.
	bExists, oExists, hExists := b != nil, o != nil, h != nil
	if bExists && (!oExists || !hExists) && (oExists != hExists) {
		return resolveLeaf(base, ours, theirs, t, opts)
	}

	// Recurse into members.
	memberPlan := mergePlan{}
	for _, child := range t.NestedChildren {
		switch child.Key.Kind {
		case kindType:
			memberPlan.Resolutions = append(memberPlan.Resolutions, resolveType(base, ours, theirs, child, opts))
		default:
			memberPlan.Resolutions = append(memberPlan.Resolutions, resolveLeaf(base, ours, theirs, child, opts))
		}
	}

	// Render the type wrapper using ours when present (else theirs,
	// else base) so signature edits to the wrapper survive.
	// We splice the assembled body into the wrapper between BodyStart
	// and BodyEnd.
	wrapperSide, wrapperDecl, wrapperSrc := pickWrapper(base, ours, theirs, b, o, h)

	bodyBytes := assembleMembers(memberPlan)
	// Track conflicts surfaced by member resolution.
	for _, mr := range memberPlan.Resolutions {
		r.Conflicts = append(r.Conflicts, mr.Conflicts...)
		if mr.TextMerged {
			r.TextMerged = true
		}
	}

	if wrapperDecl == nil {
		r.Bytes = bodyBytes
		r.Source = "type-body-only"
		return r
	}
	// Splice the merged member content into the wrapper, anchored on
	// the wrapper side's first and last members. This avoids guessing
	// at delimiter shape (Python's `:` vs Go's `{...}`) — the wrapper
	// side already knows its own punctuation.
	wrapperBytes := bytesOf(wrapperSrc, wrapperDecl)
	if len(wrapperDecl.Members) == 0 {
		r.Bytes = wrapperBytes
		r.Source = "type-wrapper-only"
		return r
	}
	first := wrapperDecl.Members[0]
	last := wrapperDecl.Members[len(wrapperDecl.Members)-1]
	relPrefixEnd := first.Start - wrapperDecl.Start
	relSuffixStart := last.End - wrapperDecl.Start
	if relPrefixEnd < 0 || relSuffixStart > len(wrapperBytes) || relPrefixEnd > relSuffixStart {
		r.Bytes = wrapperBytes
		r.Source = "type-anchor-out-of-range"
		return r
	}
	prefix := wrapperBytes[:relPrefixEnd]
	suffix := wrapperBytes[relSuffixStart:]
	merged := make([]byte, 0, len(prefix)+len(bodyBytes)+len(suffix))
	merged = append(merged, prefix...)
	merged = append(merged, bodyBytes...)
	merged = append(merged, suffix...)
	r.Bytes = merged
	r.Source = "type-merged-" + wrapperSide
	return r
}

func pickWrapper(base, ours, theirs []byte, b, o, h *decl) (string, *decl, []byte) {
	switch {
	case o != nil:
		return "ours", o, ours
	case h != nil:
		return "theirs", h, theirs
	case b != nil:
		return "base", b, base
	}
	return "", nil, nil
}

// assembleMembers concatenates a member-level mergePlan into a single
// byte slice with consistent newlines.
func assembleMembers(plan mergePlan) []byte {
	var buf []byte
	for _, r := range plan.Resolutions {
		if len(r.Bytes) == 0 {
			continue
		}
		buf = append(buf, r.Bytes...)
		if len(buf) > 0 && buf[len(buf)-1] != '\n' {
			buf = append(buf, '\n')
		}
	}
	return buf
}

// editOp categorises the (base → side) diff for one declaration.
type editOp int

const (
	opUnchanged editOp = iota
	opAdded
	opDeleted
	opModified
)

func classify(base, side *decl) editOp {
	switch {
	case base == nil && side == nil:
		return opUnchanged // shouldn't appear in matched triples
	case base == nil && side != nil:
		return opAdded
	case base != nil && side == nil:
		return opDeleted
	case equalFingerprint(base, side):
		return opUnchanged
	default:
		return opModified
	}
}

func equalFingerprint(a, b *decl) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Fingerprint == b.Fingerprint
}

func renderSide(src []byte, d *decl) []byte {
	if d == nil {
		return nil
	}
	return bytesOf(src, d)
}

func bytesOf(src []byte, d *decl) []byte {
	if d == nil || src == nil {
		return nil
	}
	if d.Start < 0 || d.End > len(src) || d.End <= d.Start {
		return nil
	}
	return append([]byte(nil), src[d.Start:d.End]...)
}

func byteEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stripTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func deleteModifyKind(op1, op2 editOp) string {
	if op1 == opDeleted {
		return "delete-modify"
	}
	return "modify-delete"
}

func declLabel(k declKey) string {
	if k.Container != "" {
		return string(k.Kind) + " " + k.Container + "." + k.Name
	}
	return string(k.Kind) + " " + k.Name
}
