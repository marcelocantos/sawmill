// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package rewrite provides source-level rewriting utilities: identifier
// renaming, unified diffs, source formatting, and raw byte-range edits.
package rewrite

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
)

// Edit represents a single byte-range replacement within a source buffer.
// Edits must not overlap.
type Edit struct {
	Start       uint
	End         uint
	Replacement string
}

// RenameInFile renames all identifier occurrences of from to to within the
// given parsed source.
//
// It operates at the AST level — only nodes captured by the adapter's
// identifier query are considered, so string literals and comments are left
// untouched.
//
// The function accepts the individual fields of a ParsedFile rather than a
// *forest.ParsedFile directly, to avoid a circular import between the forest
// and rewrite packages.
func RenameInFile(
	source []byte,
	tree *tree_sitter.Tree,
	adapter adapters.LanguageAdapter,
	from, to string,
) ([]byte, error) {
	querySrc := adapter.IdentifierQuery()
	lang := adapter.Language()

	query, err := tree_sitter.NewQuery(lang, querySrc)
	if err != nil {
		return nil, fmt.Errorf("compiling identifier query: %w", err)
	}
	defer query.Close()

	// Determine the capture index for "@name".
	nameIdx := uint32(0)
	nameFound := false
	for i, name := range query.CaptureNames() {
		if name == "name" {
			nameIdx = uint32(i)
			nameFound = true
			break
		}
	}
	if !nameFound {
		return nil, fmt.Errorf("identifier query must capture @name")
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, tree.RootNode(), source)

	var edits []Edit
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, capture := range match.Captures {
			if capture.Index == nameIdx {
				node := capture.Node
				text := source[node.StartByte():node.EndByte()]
				if string(text) == from {
					edits = append(edits, Edit{
						Start:       uint(node.StartByte()),
						End:         uint(node.EndByte()),
						Replacement: to,
					})
				}
			}
		}
	}

	if len(edits) == 0 {
		return source, nil
	}

	return ApplyEdits(source, edits), nil
}

// ApplyEdits applies a list of non-overlapping byte-range edits to source,
// returning the new source. The edits are sorted by Start position before
// application.
func ApplyEdits(source []byte, edits []Edit) []byte {
	// Sort by start position (ascending).
	sort.Slice(edits, func(i, j int) bool {
		return edits[i].Start < edits[j].Start
	})

	result := make([]byte, 0, len(source))
	lastEnd := uint(0)

	for _, edit := range edits {
		result = append(result, source[lastEnd:edit.Start]...)
		result = append(result, edit.Replacement...)
		lastEnd = edit.End
	}
	result = append(result, source[lastEnd:]...)

	return result
}

// UnifiedDiff produces a unified diff string between original and newContent
// for the named file.
func UnifiedDiff(path string, original, newContent []byte) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(original)),
		B:        difflib.SplitLines(string(newContent)),
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	}

	var buf strings.Builder
	if err := difflib.WriteUnifiedDiff(&buf, diff); err != nil {
		return fmt.Sprintf("error generating diff for %s: %v", path, err)
	}
	return buf.String()
}

// FormatSource runs the language formatter on source bytes via stdin→stdout.
// If the formatter is not configured, unavailable, or fails the original
// source is returned unchanged. The error return is always nil; the source
// fallback is silent, matching the Rust implementation.
func FormatSource(adapter adapters.LanguageAdapter, source []byte) ([]byte, error) {
	cmdParts := adapter.FormatterCommand()
	if len(cmdParts) == 0 {
		return source, nil
	}

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...) //nolint:gosec
	cmd.Stdin = bytes.NewReader(source)

	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		// Formatter not installed or failed — return original unchanged.
		return source, nil
	}

	return out, nil
}
