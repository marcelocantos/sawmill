// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

// triple aligns one declaration across the three sides. Any field may
// be nil if the side does not contain that declaration.
type triple struct {
	Key            declKey
	Base           *decl
	Ours           *decl
	Theirs         *decl
	NestedChildren []triple // populated when Key.Kind == kindType (or import container)
}

// matchDeclarations builds a list of triples covering every declaration
// that appears on at least one side. The order tries to follow base's
// source order first, then appended additions in (ours, theirs) order.
func matchDeclarations(base, ours, theirs []*decl) []triple {
	// Three-bucket key ordering: package clauses first, then imports,
	// then everything else. This keeps newly-added imports next to
	// existing imports rather than appending them at the end of the
	// file, and keeps the package clause at the top where Go requires
	// it. Within each bucket, base order takes precedence; new keys
	// from ours and theirs append in that order.
	var packageOrder, importsOrder, otherOrder []declKey
	seen := map[declKey]bool{}
	add := func(k declKey) {
		if seen[k] {
			return
		}
		seen[k] = true
		switch k.Kind {
		case kindPackage:
			packageOrder = append(packageOrder, k)
		case kindImport:
			importsOrder = append(importsOrder, k)
		default:
			otherOrder = append(otherOrder, k)
		}
	}
	for _, d := range base {
		add(d.key())
	}
	for _, d := range ours {
		add(d.key())
	}
	for _, d := range theirs {
		add(d.key())
	}
	keyOrder := make([]declKey, 0, len(packageOrder)+len(importsOrder)+len(otherOrder))
	keyOrder = append(keyOrder, packageOrder...)
	keyOrder = append(keyOrder, importsOrder...)
	keyOrder = append(keyOrder, otherOrder...)

	baseByKey := indexByKey(base)
	oursByKey := indexByKey(ours)
	theirsByKey := indexByKey(theirs)

	triples := make([]triple, 0, len(keyOrder))
	for _, k := range keyOrder {
		t := triple{
			Key:    k,
			Base:   baseByKey[k],
			Ours:   oursByKey[k],
			Theirs: theirsByKey[k],
		}
		switch k.Kind {
		case kindType, kindImport:
			// Recurse into the container's members. Python imports
			// are leaf decls (no Members); Go's `import (...)` block
			// is a container whose Members are the individual specs.
			t.NestedChildren = matchMembers(t.Base, t.Ours, t.Theirs)
		}
		triples = append(triples, t)
	}
	return triples
}

func indexByKey(decls []*decl) map[declKey]*decl {
	m := make(map[declKey]*decl, len(decls))
	for _, d := range decls {
		m[d.key()] = d
	}
	return m
}

// matchMembers aligns the Members slice of a triple-matched container
// (one type/class declaration on each side). Only invoked when at
// least one of the three sides has Members.
func matchMembers(base, ours, theirs *decl) []triple {
	var b, o, t []*decl
	if base != nil {
		b = base.Members
	}
	if ours != nil {
		o = ours.Members
	}
	if theirs != nil {
		t = theirs.Members
	}
	if len(b)+len(o)+len(t) == 0 {
		return nil
	}
	return matchDeclarations(b, o, t)
}
