// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package merge implements AST-aware three-way merge.
//
// Given (base, ours, theirs) source blobs and a language adapter, Merge
// returns a single merged source plus a residual list of conflicts that
// could not be resolved structurally. Edits that touch disjoint AST
// declarations always commute; edits to the same declaration body fall
// back to a line-level diff3 merge with standard git-style conflict
// markers in the output.
//
// The package is consumed by:
//   - cmd/sawmill (sawmill merge — git mergetool driver)
//   - cmd/sawmill (sawmill merge-driver — git low-level merge driver)
//   - mcp/server (merge_three_way tool — agent-callable)
package merge

import (
	"fmt"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

// Conflict describes a residual conflict that could not be auto-resolved.
type Conflict struct {
	// Path is the file path (set by the caller; empty when unset).
	Path string
	// Start, End are byte offsets into Result.Merged delimiting the
	// conflict markers (inclusive of the leading <<<<<<< line through the
	// trailing >>>>>>> line, terminated by a trailing newline).
	Start, End int
	// Kind classifies the conflict: "modify-modify", "delete-modify",
	// "modify-delete", "add-add", or "body".
	Kind string
	// Decl identifies the declaration the conflict pertains to (e.g.
	// "func foo", "class Bar.method baz"). Empty for body-level hunks
	// that fall through diff3.
	Decl string
}

// Result is the outcome of a three-way merge.
type Result struct {
	// Merged is the merged source. Always returned, even when conflicts
	// remain — residual hunks are wrapped in git-style conflict markers
	// so the output is a valid file from git's perspective.
	Merged []byte
	// Conflicts lists the residual hunks. Empty means a clean merge.
	Conflicts []Conflict
	// Stats reports merge accounting.
	Stats Stats
}

// Stats reports per-merge accounting.
type Stats struct {
	// DeclsResolved counts declaration-level edits that committed
	// without falling back to text merge (commuting inserts, identical
	// adds, body-fingerprint matches, single-side modifies, agreed
	// deletes, applied renames).
	DeclsResolved int
	// DeclsTextMerged counts declarations whose bodies went through
	// the diff3 line-level fallback (irrespective of whether diff3
	// produced clean output).
	DeclsTextMerged int
	// Conflicts counts residual conflicts (== len(Result.Conflicts)).
	Conflicts int
}

// Options configures a Merge call.
type Options struct {
	// Path is the file path used in conflict labels (default: "file").
	Path string
	// Style controls conflict marker style: "diff3" (default, includes
	// the |||||||  base section) or "merge" (ours/theirs only).
	Style string
}

// Merge performs an AST-aware three-way merge.
//
// base, ours, theirs are the file contents at the merge base, the local
// (HEAD) side, and the remote (incoming) side respectively. adapter
// supplies language-specific AST queries.
//
// The returned Result.Merged is always a complete file. When conflicts
// remain, they are inlined as standard git conflict markers and also
// returned in Result.Conflicts for programmatic consumers.
func Merge(base, ours, theirs []byte, adapter adapters.LanguageAdapter, opts Options) (Result, error) {
	if adapter == nil {
		return Result{}, fmt.Errorf("merge: adapter is required")
	}
	if opts.Path == "" {
		opts.Path = "file"
	}
	if opts.Style == "" {
		opts.Style = "diff3"
	}

	// Trivial fast paths — no parsing needed.
	if eq(ours, theirs) {
		return Result{Merged: append([]byte(nil), ours...)}, nil
	}
	if eq(base, ours) {
		return Result{Merged: append([]byte(nil), theirs...)}, nil
	}
	if eq(base, theirs) {
		return Result{Merged: append([]byte(nil), ours...)}, nil
	}

	baseTree, err := forest.ParseSource(base, adapter)
	if err != nil {
		return Result{}, fmt.Errorf("merge: parsing base: %w", err)
	}
	oursTree, err := forest.ParseSource(ours, adapter)
	if err != nil {
		return Result{}, fmt.Errorf("merge: parsing ours: %w", err)
	}
	theirsTree, err := forest.ParseSource(theirs, adapter)
	if err != nil {
		return Result{}, fmt.Errorf("merge: parsing theirs: %w", err)
	}

	// Any side that fails to parse cleanly forfeits AST-level merging
	// — we fall through to a whole-file diff3 instead. (A syntactic
	// error on one side often reflects an in-progress edit; better to
	// hand the user a textual conflict than to silently mis-merge.)
	if baseTree == nil || oursTree == nil || theirsTree == nil ||
		baseTree.HasError() || oursTree.HasError() || theirsTree.HasError() {
		return textOnlyMerge(base, ours, theirs, opts), nil
	}

	baseDecls := extractDeclarations(base, baseTree, adapter)
	oursDecls := extractDeclarations(ours, oursTree, adapter)
	theirsDecls := extractDeclarations(theirs, theirsTree, adapter)

	plan, err := planMerge(base, ours, theirs, baseDecls, oursDecls, theirsDecls, opts)
	if err != nil {
		return Result{}, err
	}

	return assembleResult(base, ours, theirs, plan, opts), nil
}

func eq(a, b []byte) bool {
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

