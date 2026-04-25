// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RootDiffBundle is the per-root result returned by transform_multi_root.
type RootDiffBundle struct {
	// FileCount is the number of files changed in this root.
	FileCount int `json:"file_count"`
	// Diffs is the ordered list of unified diffs, one per changed file.
	Diffs []string `json:"diffs"`
	// Error is non-empty if this root could not be processed.
	Error string `json:"error,omitempty"`
}

// handleTransformMultiRoot implements the transform_multi_root tool. It loads
// (or reuses) a CodebaseModel for each requested root, applies the transform
// batch, and returns per-root diffs without touching the session's pending
// state.
func (h *Handler) handleTransformMultiRoot(args map[string]any) (string, bool, error) {
	rootsJSON, err := requireString(args, "roots")
	if err != nil {
		return err.Error(), true, nil
	}
	transformsJSON, err := requireString(args, "transforms")
	if err != nil {
		return err.Error(), true, nil
	}
	format := optBool(args, "format")

	var roots []string
	if err := json.Unmarshal([]byte(rootsJSON), &roots); err != nil {
		return fmt.Sprintf("parsing roots JSON: %v", err), true, nil
	}
	if len(roots) == 0 {
		return "roots must be a non-empty JSON array of absolute paths", true, nil
	}

	var specs []transformSpec
	if err := json.Unmarshal([]byte(transformsJSON), &specs); err != nil {
		return fmt.Sprintf("parsing transforms JSON: %v", err), true, nil
	}
	if len(specs) == 0 {
		return "transforms must be a non-empty JSON array", true, nil
	}

	// Capture the loader under the lock, then release it so per-root model
	// loading does not hold the session lock.
	h.mu.Lock()
	loader := h.loader
	if loader == nil {
		loader = directLoader
	}
	h.mu.Unlock()

	results := make(map[string]RootDiffBundle, len(roots))

	for _, root := range roots {
		bundle := applySpecsToRoot(loader, root, specs, format)
		results[root] = bundle
	}

	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling results: %v", err), true, nil
	}

	// Also emit a human-readable summary before the JSON blob so the output is
	// easy to scan in a terminal.
	var sb strings.Builder
	totalFiles := 0
	errorRoots := 0
	for _, b := range results {
		totalFiles += b.FileCount
		if b.Error != "" {
			errorRoots++
		}
	}
	fmt.Fprintf(&sb, "transform_multi_root: %d root(s), %d file(s) changed", len(roots), totalFiles)
	if errorRoots > 0 {
		fmt.Fprintf(&sb, ", %d root(s) with errors", errorRoots)
	}
	sb.WriteString("\n\n")
	sb.WriteString(string(out))

	return sb.String(), false, nil
}

// applySpecsToRoot loads the model for root, applies specs, and returns a
// RootDiffBundle. Errors during model loading or individual transform steps
// are captured in RootDiffBundle.Error rather than propagated, so one failing
// root does not abort the others.
func applySpecsToRoot(loader ModelLoader, root string, specs []transformSpec, format bool) RootDiffBundle {
	m, release, err := loader(root)
	if err != nil {
		return RootDiffBundle{Error: fmt.Sprintf("loading model: %v", err)}
	}
	if release != nil {
		defer release()
	}

	pending := make(map[string][]byte)
	var allDiffs []string
	totalFiles := 0

	for stepIdx, spec := range specs {
		changes, diffs, err := applyTransformSpecWithOverrides(m, spec, format, pending)
		if err != nil {
			return RootDiffBundle{Error: fmt.Sprintf("step %d: %v", stepIdx+1, err)}
		}
		for _, c := range changes {
			if _, seen := pending[c.Path]; !seen {
				totalFiles++
			}
			pending[c.Path] = c.NewSource
		}
		allDiffs = append(allDiffs, diffs...)
	}

	return RootDiffBundle{
		FileCount: totalFiles,
		Diffs:     allDiffs,
	}
}
