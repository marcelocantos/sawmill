// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/codegen"
	"github.com/marcelocantos/sawmill/exemplar"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
	"github.com/marcelocantos/sawmill/jsengine"
	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/merge"
	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/bisect"
	"github.com/marcelocantos/sawmill/rewrite"
	"github.com/marcelocantos/sawmill/semdiff"
	"github.com/marcelocantos/sawmill/store"
	"github.com/marcelocantos/sawmill/transform"
)

//go:embed agents-guide.md
var embeddedAgentsGuide string

// requireString returns the string argument named key, or an error if absent/empty.
func requireString(args map[string]any, key string) (string, error) {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// optString returns the string argument named key, or "" if absent/not a string.
func optString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// optBool returns the bool argument named key, or false if absent.
func optBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// ptr returns a pointer to s, or nil if s == "".
func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// requireModel returns the active model or an error if parse has not been called.
func (h *Handler) requireModel() (*model.CodebaseModel, error) {
	if h.model == nil {
		return nil, fmt.Errorf("no codebase loaded — call parse first")
	}
	return h.model, nil
}

// ---- parse ----------------------------------------------------------------

func (h *Handler) handleParse(args map[string]any) (string, bool, error) {
	path := optString(args, "path")

	h.mu.Lock()
	defer h.mu.Unlock()

	if path == "" && h.model != nil {
		// No path and model already loaded — re-use it, the watcher keeps
		// the underlying state in sync.
	} else {
		if path == "" {
			return "path is required when no model is pre-loaded", true, nil
		}
		// Switch (or first-time) load. Release any previously-borrowed model
		// before acquiring the new one.
		if h.release != nil {
			h.release()
			h.release = nil
		}
		h.model = nil
		h.pending = nil
		h.lastBackups = nil

		loader := h.loader
		if loader == nil {
			loader = directLoader
		}
		m, release, err := loader(path)
		if err != nil {
			return fmt.Sprintf("parsing %q: %v", path, err), true, nil
		}
		h.model = m
		h.release = release
	}

	// Build summary from the store (avoids ForestSnapshot).
	fileCount, err := h.model.Store.FileCount()
	if err != nil {
		return fmt.Sprintf("counting files: %v", err), true, nil
	}
	extSummary, err := h.model.Store.LanguageSummary()
	if err != nil {
		return fmt.Sprintf("language summary: %v", err), true, nil
	}
	// Map extensions to human-readable language names via adapters.
	summary := make(map[string]int, len(extSummary))
	for ext, count := range extSummary {
		lang := ext
		if adapter := adapters.ForExtension(ext); adapter != nil {
			if id := adapter.LSPLanguageID(); id != "" {
				lang = id
			}
		}
		summary[lang] += count
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Parsed %d file(s) in %s\n", fileCount, h.model.Root)
	for lang, count := range summary {
		fmt.Fprintf(&sb, "  %s: %d\n", lang, count)
	}

	return sb.String(), false, nil
}

// ---- rename ---------------------------------------------------------------

func (h *Handler) handleRename(args map[string]any) (string, bool, error) {
	from, err := requireString(args, "from")
	if err != nil {
		return err.Error(), true, nil
	}
	to, err := requireString(args, "to")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			newSource, rerr := rewrite.RenameInFile(source, tree, acc.Adapter(), from, to)
			if rerr != nil {
				return rerr
			}
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("renaming in %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("No occurrences of %q found.", from), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Renamed %q → %q in %d file(s). Call apply to write changes.\n\n", from, to, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- query ----------------------------------------------------------------

func (h *Handler) handleQuery(args map[string]any) (string, bool, error) {
	kind := optString(args, "kind")
	name := optString(args, "name")
	fileFilter := optString(args, "file")
	rawQuery := optString(args, "raw_query")
	capture := optString(args, "capture")
	pathFilter := optString(args, "path")
	format := optString(args, "format")
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return fmt.Sprintf("invalid format %q (want \"text\" or \"json\")", format), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var matchSpec *transform.Match
	if rawQuery != "" {
		matchSpec = transform.RawMatch(rawQuery, capture)
	} else if kind != "" {
		matchSpec = transform.AbstractMatch(kind, name, fileFilter)
	} else {
		return "provide either kind or raw_query", true, nil
	}

	var results []forest.QueryResult
	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}
	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			fileResults, qerr := transform.QuerySource(source, tree, acc.Adapter(), acc.Path(), matchSpec)
			if qerr != nil {
				return qerr
			}
			results = append(results, fileResults...)
			return nil
		})
		if err != nil {
			return fmt.Sprintf("querying %s: %v", acc.Path(), err), true, nil
		}
	}

	if format == "json" {
		matches := make([]QueryMatch, 0, len(results))
		for _, r := range results {
			matches = append(matches, QueryMatch{
				File:    r.Path,
				Line:    int(r.StartLine),
				Column:  int(r.StartCol),
				Kind:    r.Kind,
				Name:    r.Name,
				Snippet: r.Text,
			})
		}
		out, err := json.MarshalIndent(matches, "", "  ")
		if err != nil {
			return fmt.Sprintf("marshalling: %v", err), true, nil
		}
		return string(out), false, nil
	}

	if len(results) == 0 {
		return "No matches found.", false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es):\n", len(results))
	for _, r := range results {
		if r.Name != "" {
			fmt.Fprintf(&sb, "  %s:%d:%d  [%s] %s\n    %s\n", r.Path, r.StartLine, r.StartCol, r.Kind, r.Name, r.Text)
		} else {
			fmt.Fprintf(&sb, "  %s:%d:%d  [%s]\n    %s\n", r.Path, r.StartLine, r.StartCol, r.Kind, r.Text)
		}
	}
	return sb.String(), false, nil
}

// ---- find_symbol ----------------------------------------------------------

func (h *Handler) handleFindSymbol(args map[string]any) (string, bool, error) {
	symbol, err := requireString(args, "symbol")
	if err != nil {
		return err.Error(), true, nil
	}
	kind := optString(args, "kind")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	records, err := m.FindSymbols(symbol, kind)
	if err != nil {
		return fmt.Sprintf("finding symbols: %v", err), true, nil
	}

	if len(records) == 0 {
		return fmt.Sprintf("Symbol %q not found.", symbol), false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d occurrence(s) of %q:\n", len(records), symbol)
	for _, r := range records {
		fmt.Fprintf(&sb, "  %s:%d  [%s] %s\n", r.FilePath, r.StartLine, r.Kind, r.Name)
	}
	return sb.String(), false, nil
}

// ---- find_references ------------------------------------------------------

func (h *Handler) handleFindReferences(args map[string]any) (string, bool, error) {
	symbol, err := requireString(args, "symbol")
	if err != nil {
		return err.Error(), true, nil
	}
	includeLibraries := optBool(args, "include_libraries")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	// Library files have no call symbols (they're indexed in API-only mode),
	// so the scope filter is implicitly satisfied — we restrict to "owned"
	// files explicitly to make the contract clear and to remain robust if a
	// library file's classification ever changes.
	scopes := []string{"owned"}
	if includeLibraries {
		scopes = append(scopes, "library")
	}

	records, err := m.FindSymbolsInScopes(symbol, "call", scopes)
	if err != nil {
		return fmt.Sprintf("finding references: %v", err), true, nil
	}

	if len(records) == 0 {
		return fmt.Sprintf("No call sites found for %q.", symbol), false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d call site(s) for %q:\n", len(records), symbol)
	for _, r := range records {
		fmt.Fprintf(&sb, "  %s:%d\n", r.FilePath, r.StartLine)
	}
	return sb.String(), false, nil
}

// ---- transform ------------------------------------------------------------

// transformSpec is the decoded form of a single transform step (used by both
// transform and transform_batch).
type transformSpec struct {
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	File        string  `json:"file"`
	RawQuery    string  `json:"raw_query"`
	Capture     string  `json:"capture"`
	Action      string  `json:"action"`
	Code        *string `json:"code"`
	Before      *string `json:"before"`
	After       *string `json:"after"`
	TransformFn string  `json:"transform_fn"`
	Path        string  `json:"path"`
}

func (h *Handler) handleTransform(args map[string]any) (string, bool, error) {
	spec := transformSpec{
		Kind:        optString(args, "kind"),
		Name:        optString(args, "name"),
		File:        optString(args, "file"),
		RawQuery:    optString(args, "raw_query"),
		Capture:     optString(args, "capture"),
		Action:      optString(args, "action"),
		TransformFn: optString(args, "transform_fn"),
		Path:        optString(args, "path"),
	}
	code := optString(args, "code")
	before := optString(args, "before")
	after := optString(args, "after")
	if code != "" {
		spec.Code = &code
	}
	if before != "" {
		spec.Before = &before
	}
	if after != "" {
		spec.After = &after
	}
	format := optBool(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	changes, diffs, err := applyTransformSpec(m, spec, format)
	if err != nil {
		return err.Error(), true, nil
	}

	if len(changes) == 0 {
		return "No matches found; no changes made.", false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Transform produced changes in %d file(s). Call apply to write.\n\n", len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// applyTransformSpec applies a single transformSpec to the model's forest and
// returns the resulting changes and diffs.
func applyTransformSpec(m *model.CodebaseModel, spec transformSpec, format bool) ([]forest.FileChange, []string, error) {
	var matchSpec *transform.Match
	if spec.RawQuery != "" {
		matchSpec = transform.RawMatch(spec.RawQuery, spec.Capture)
	} else if spec.Kind != "" {
		matchSpec = transform.AbstractMatch(spec.Kind, spec.Name, spec.File)
	} else if spec.TransformFn == "" {
		return nil, nil, fmt.Errorf("provide kind, raw_query, or transform_fn")
	}

	// Resolve action (only needed for declarative transforms).
	var action *transform.Action
	if spec.TransformFn == "" {
		if spec.Action == "" {
			return nil, nil, fmt.Errorf("action is required for declarative transforms")
		}
		var err error
		action, err = parseAction(spec.Action, spec.Code, spec.Before, spec.After)
		if err != nil {
			return nil, nil, err
		}
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(spec.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("listing files: %w", err)
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			var newSource []byte
			var terr error

			if spec.TransformFn != "" {
				// JS-based transform.
				queryStr := ""
				if matchSpec != nil {
					queryStr, terr = transform.ResolveQueryStr(acc.Adapter(), matchSpec)
					if terr != nil {
						return fmt.Errorf("resolving query for %s: %w", acc.Path(), terr)
					}
				}
				if queryStr == "" {
					return nil
				}
				newSource, terr = jsengine.RunJSTransform(
					source, tree, queryStr, spec.TransformFn, acc.Path(), acc.Adapter(),
				)
			} else {
				// Declarative match/act transform.
				newSource, terr = transform.TransformSource(source, tree, acc.Adapter(), acc.Path(), matchSpec, action)
			}

			if terr != nil {
				return fmt.Errorf("transforming %s: %w", acc.Path(), terr)
			}

			if string(newSource) == string(source) {
				return nil
			}

			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}

			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return changes, diffs, nil
}

// ---- transform_batch ------------------------------------------------------

func (h *Handler) handleTransformBatch(args map[string]any) (string, bool, error) {
	transformsJSON, err := requireString(args, "transforms")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	var specs []transformSpec
	if err := json.Unmarshal([]byte(transformsJSON), &specs); err != nil {
		return fmt.Sprintf("parsing transforms JSON: %v", err), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	// Apply path filter override from the batch request.
	for i := range specs {
		if pathFilter != "" && specs[i].Path == "" {
			specs[i].Path = pathFilter
		}
	}

	var allChanges []forest.FileChange
	var allDiffs []string

	// Track accumulated new sources across steps (so each step sees the
	// previous step's output). We do this by mutating a local map.
	pending := make(map[string][]byte)

	for stepIdx, spec := range specs {
		changes, diffs, err := applyTransformSpecWithOverrides(m, spec, format, pending)
		if err != nil {
			return fmt.Sprintf("step %d: %v", stepIdx+1, err), true, nil
		}

		// Merge into the accumulated pending map.
		for _, c := range changes {
			pending[c.Path] = c.NewSource
		}
		allChanges = mergeChanges(allChanges, changes)
		allDiffs = append(allDiffs, diffs...)
	}

	if len(allChanges) == 0 {
		return "No matches found; no changes made.", false, nil
	}

	h.pending = &PendingChanges{Changes: allChanges, Diffs: allDiffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Batch transform produced changes in %d file(s). Call apply to write.\n\n", len(allChanges))
	for _, d := range allDiffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// applyTransformSpecWithOverrides is like applyTransformSpec but reads from
// the overrides map (accumulated pending sources) for files already modified.
func applyTransformSpecWithOverrides(
	m *model.CodebaseModel,
	spec transformSpec,
	format bool,
	overrides map[string][]byte,
) ([]forest.FileChange, []string, error) {
	var matchSpec *transform.Match
	if spec.RawQuery != "" {
		matchSpec = transform.RawMatch(spec.RawQuery, spec.Capture)
	} else if spec.Kind != "" {
		matchSpec = transform.AbstractMatch(spec.Kind, spec.Name, spec.File)
	} else if spec.TransformFn == "" {
		return nil, nil, fmt.Errorf("provide kind, raw_query, or transform_fn")
	}

	var action *transform.Action
	if spec.TransformFn == "" {
		if spec.Action == "" {
			return nil, nil, fmt.Errorf("action is required for declarative transforms")
		}
		var err error
		action, err = parseAction(spec.Action, spec.Code, spec.Before, spec.After)
		if err != nil {
			return nil, nil, err
		}
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(spec.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("listing files: %w", err)
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			// Use the overridden source if available.
			currentSource := source
			if ov, ok := overrides[acc.Path()]; ok {
				currentSource = ov
			}

			var newSource []byte
			var terr error

			if spec.TransformFn != "" {
				queryStr := ""
				if matchSpec != nil {
					queryStr, terr = transform.ResolveQueryStr(acc.Adapter(), matchSpec)
					if terr != nil {
						return terr
					}
				}
				if queryStr == "" {
					return nil
				}
				newSource, terr = jsengine.RunJSTransform(
					currentSource, tree, queryStr, spec.TransformFn, acc.Path(), acc.Adapter(),
				)
			} else {
				newSource, terr = transform.TransformSource(currentSource, tree, acc.Adapter(), acc.Path(), matchSpec, action)
			}

			if terr != nil {
				return fmt.Errorf("transforming %s: %w", acc.Path(), terr)
			}

			if string(newSource) == string(currentSource) {
				return nil
			}

			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}

			// Diff against the original file (not the step-accumulated source).
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return changes, diffs, nil
}

// mergeChanges merges new changes into accumulated changes. If a file already
// appears in accumulated, its NewSource is updated.
func mergeChanges(accumulated, newChanges []forest.FileChange) []forest.FileChange {
	idx := make(map[string]int, len(accumulated))
	for i, c := range accumulated {
		idx[c.Path] = i
	}
	for _, c := range newChanges {
		if i, ok := idx[c.Path]; ok {
			accumulated[i].NewSource = c.NewSource
		} else {
			idx[c.Path] = len(accumulated)
			accumulated = append(accumulated, c)
		}
	}
	return accumulated
}

// ---- codegen --------------------------------------------------------------

func (h *Handler) handleCodegen(args map[string]any) (string, bool, error) {
	program, err := requireString(args, "program")
	if err != nil {
		return err.Error(), true, nil
	}
	format := optBool(args, "format")
	validate := optBool(args, "validate")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	if m.LSP != nil {
		changes, err = codegen.RunCodegenWithLSP(m.ForestSnapshot(), program, m.LSP, m.Root)
	} else {
		changes, err = codegen.RunCodegen(m.ForestSnapshot(), program)
	}
	if err != nil {
		return fmt.Sprintf("codegen: %v", err), true, nil
	}

	if len(changes) == 0 {
		return "Codegen produced no changes.", false, nil
	}

	var warnings []string
	if validate {
		parseErrs := codegen.ValidateChanges(changes)
		structErrs := codegen.StructuralChecks(m.ForestSnapshot(), changes)
		warnings = append(parseErrs, structErrs...)
	}

	if format {
		for i, c := range changes {
			ext := filepath.Ext(c.Path)
			if ext == "" {
				continue
			}
			// Reuse the existing file's adapter if possible.
			for _, file := range m.ForestSnapshot().Files {
				if file.Path == c.Path {
					formatted, _ := rewrite.FormatSource(file.Adapter, c.NewSource)
					changes[i].NewSource = formatted
					break
				}
			}
		}
	}

	var diffs []string
	for _, c := range changes {
		diffs = append(diffs, rewrite.UnifiedDiff(c.Path, c.Original, c.NewSource))
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Codegen produced changes in %d file(s). Call apply to write.\n", len(changes))
	if len(warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, w := range warnings {
			fmt.Fprintf(&sb, "  %s\n", w)
		}
	}
	sb.WriteString("\n")
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- apply ----------------------------------------------------------------

func (h *Handler) handleApply(args map[string]any) (string, bool, error) {
	confirm := optBool(args, "confirm")

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.pending == nil || (len(h.pending.Changes) == 0 && len(h.pending.Renames) == 0) {
		return "No pending changes to apply.", false, nil
	}

	totalPending := len(h.pending.Changes) + len(h.pending.Renames)
	if !confirm {
		return fmt.Sprintf("Pending %d change(s). Set confirm=true to apply.", totalPending), false, nil
	}

	var backupPaths []string
	if len(h.pending.Changes) > 0 {
		bp, err := forest.ApplyWithBackup(h.model.Root, h.pending.Changes)
		if err != nil {
			return fmt.Sprintf("applying changes: %v", err), true, nil
		}
		backupPaths = append(backupPaths, bp...)
	}

	// Perform file renames.
	for _, r := range h.pending.Renames {
		if err := os.MkdirAll(filepath.Dir(r.To), 0o755); err != nil {
			return fmt.Sprintf("creating directory for %s: %v", r.To, err), true, nil
		}
		if err := os.Rename(r.From, r.To); err != nil {
			return fmt.Sprintf("renaming %s -> %s: %v", r.From, r.To, err), true, nil
		}
	}

	// Notify the model manager to re-parse changed files immediately,
	// bypassing the watcher's debounce delay.
	var changedPaths []string
	for _, c := range h.pending.Changes {
		changedPaths = append(changedPaths, c.Path)
	}
	for _, r := range h.pending.Renames {
		changedPaths = append(changedPaths, r.To)
	}

	h.lastBackups = &LastBackups{Paths: backupPaths}
	applied := totalPending
	h.pending = nil

	if len(changedPaths) > 0 {
		h.model.NotifyChanged(changedPaths)
	}

	return fmt.Sprintf("Applied %d change(s). Backups created. Call undo to revert.", applied), false, nil
}

// ---- undo -----------------------------------------------------------------

func (h *Handler) handleUndo(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.lastBackups == nil || len(h.lastBackups.Paths) == 0 {
		return "No backups available to restore.", false, nil
	}

	restored, err := forest.UndoFromBackups(h.model.Root, h.lastBackups.Paths)
	if err != nil {
		return fmt.Sprintf("undoing changes: %v", err), true, nil
	}

	h.lastBackups = nil
	return fmt.Sprintf("Restored %d file(s) from backup.", restored), false, nil
}

// ---- teach_recipe ---------------------------------------------------------

func (h *Handler) handleTeachRecipe(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	paramsJSON := optString(args, "params")
	stepsJSON, err := requireString(args, "steps")
	if err != nil {
		return err.Error(), true, nil
	}

	var params []string
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return fmt.Sprintf("parsing params JSON: %v", err), true, nil
		}
	}

	// Validate steps JSON.
	if !json.Valid([]byte(stepsJSON)) {
		return "steps is not valid JSON", true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveRecipe(name, description, params, []byte(stepsJSON)); err != nil {
		return fmt.Sprintf("saving recipe: %v", err), true, nil
	}

	return fmt.Sprintf("Recipe %q saved.", name), false, nil
}

// ---- instantiate ----------------------------------------------------------

func (h *Handler) handleInstantiate(args map[string]any) (string, bool, error) {
	recipeName, err := requireString(args, "recipe")
	if err != nil {
		return err.Error(), true, nil
	}
	paramsJSON := optString(args, "params")
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	var params map[string]string
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return fmt.Sprintf("parsing params JSON: %v", err), true, nil
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	recipe, err := m.LoadRecipe(recipeName)
	if err != nil {
		return fmt.Sprintf("loading recipe %q: %v", recipeName, err), true, nil
	}

	// Unmarshal the steps.
	var steps []json.RawMessage
	if err := json.Unmarshal(recipe.Steps, &steps); err != nil {
		return fmt.Sprintf("parsing recipe steps: %v", err), true, nil
	}

	// Substitute parameters in each step.
	var specs []transformSpec
	for i, stepRaw := range steps {
		// First substitute $param_name placeholders in the JSON text.
		stepStr := string(stepRaw)
		for pname, pval := range params {
			stepStr = strings.ReplaceAll(stepStr, "$"+pname, pval)
		}

		var spec transformSpec
		if err := json.Unmarshal([]byte(stepStr), &spec); err != nil {
			return fmt.Sprintf("parsing step %d: %v", i+1, err), true, nil
		}
		if pathFilter != "" && spec.Path == "" {
			spec.Path = pathFilter
		}
		specs = append(specs, spec)
	}

	pending := make(map[string][]byte)
	var allChanges []forest.FileChange
	var allDiffs []string

	for stepIdx, spec := range specs {
		changes, diffs, err := applyTransformSpecWithOverrides(m, spec, format, pending)
		if err != nil {
			return fmt.Sprintf("step %d: %v", stepIdx+1, err), true, nil
		}
		for _, c := range changes {
			pending[c.Path] = c.NewSource
		}
		allChanges = mergeChanges(allChanges, changes)
		allDiffs = append(allDiffs, diffs...)
	}

	if len(allChanges) == 0 {
		return "Recipe produced no changes.", false, nil
	}

	h.pending = &PendingChanges{Changes: allChanges, Diffs: allDiffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Recipe %q produced changes in %d file(s). Call apply to write.\n\n", recipeName, len(allChanges))
	for _, d := range allDiffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- list_recipes ---------------------------------------------------------

func (h *Handler) handleListRecipes(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	recipes, err := m.ListRecipes()
	if err != nil {
		return fmt.Sprintf("listing recipes: %v", err), true, nil
	}

	if len(recipes) == 0 {
		return "No recipes saved.", false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d recipe(s):\n", len(recipes))
	for _, r := range recipes {
		fmt.Fprintf(&sb, "  %s", r.Name)
		if r.Description != "" {
			fmt.Fprintf(&sb, " — %s", r.Description)
		}
		if len(r.Params) > 0 {
			fmt.Fprintf(&sb, " [params: %s]", strings.Join(r.Params, ", "))
		}
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- teach_convention -----------------------------------------------------

func (h *Handler) handleTeachConvention(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	checkProgram, err := requireString(args, "check_program")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveConvention(name, description, checkProgram); err != nil {
		return fmt.Sprintf("saving convention: %v", err), true, nil
	}

	return fmt.Sprintf("Convention %q saved.", name), false, nil
}

// ---- check_conventions ----------------------------------------------------

func (h *Handler) handleCheckConventions(args map[string]any) (string, bool, error) {
	pathFilter := optString(args, "path")
	format := optString(args, "format")
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return fmt.Sprintf("invalid format %q (want \"text\" or \"json\")", format), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	conventions, err := m.ListConventions()
	if err != nil {
		return fmt.Sprintf("listing conventions: %v", err), true, nil
	}

	if len(conventions) == 0 {
		if format == "json" {
			return "[]", false, nil
		}
		return "No conventions defined.", false, nil
	}

	// Build a filtered forest view if a path filter is given.
	f := m.ForestSnapshot()
	if pathFilter != "" {
		var filtered []*forest.ParsedFile
		for _, file := range f.Files {
			if strings.Contains(file.Path, pathFilter) {
				filtered = append(filtered, file)
			}
		}
		f = &forest.Forest{Files: filtered}
	}

	type convResult struct {
		name        string
		err         error
		structured  []Violation
		legacyTexts []string // populated only for plain-string violations to preserve prose output
	}
	results := make([]convResult, 0, len(conventions))

	for _, conv := range conventions {
		violations, err := codegen.RunConventionCheck(f, conv.CheckProgram)
		if err != nil {
			results = append(results, convResult{name: conv.Name, err: err})
			continue
		}
		var structured []Violation
		var legacy []string
		for _, v := range violations {
			structured = append(structured, conventionViolationToStructured(conv.Name, v))
			if isPlainStringViolation(v) {
				legacy = append(legacy, v.Message)
			}
		}
		results = append(results, convResult{name: conv.Name, structured: structured, legacyTexts: legacy})
	}

	if format == "json" {
		var allViolations []Violation
		for _, r := range results {
			allViolations = append(allViolations, r.structured...)
		}
		out, err := json.MarshalIndent(allViolations, "", "  ")
		if err != nil {
			return fmt.Sprintf("marshalling: %v", err), true, nil
		}
		return string(out), false, nil
	}

	var sb strings.Builder
	totalViolations := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(&sb, "Convention %q: error: %v\n", r.name, r.err)
			continue
		}
		if len(r.structured) == 0 {
			fmt.Fprintf(&sb, "Convention %q: OK\n", r.name)
			continue
		}
		fmt.Fprintf(&sb, "Convention %q: %d violation(s):\n", r.name, len(r.structured))
		// Preserve legacy prose for plain-string returns; render structured
		// returns as `file:line: message`.
		legacyIdx := 0
		for _, v := range r.structured {
			if v.File == "" && v.Line == 0 && legacyIdx < len(r.legacyTexts) {
				fmt.Fprintf(&sb, "  %s\n", r.legacyTexts[legacyIdx])
				legacyIdx++
			} else {
				fmt.Fprintf(&sb, "  %s\n", formatViolationLine(v))
			}
			totalViolations++
		}
	}

	if totalViolations > 0 {
		fmt.Fprintf(&sb, "\n%d total violation(s).\n", totalViolations)
	} else {
		sb.WriteString("\nAll conventions satisfied.\n")
	}

	return sb.String(), false, nil
}

// conventionViolationToStructured maps a codegen.ConventionViolation
// (returned by the JS check program) into the canonical mcp.Violation shape.
// Plain-string returns become Violations with only Message populated.
func conventionViolationToStructured(convName string, v codegen.ConventionViolation) Violation {
	severity := v.Severity
	if severity == "" {
		severity = "error"
	}
	rule := v.Rule
	if rule == "" {
		rule = convName
	}
	return Violation{
		Source:       "convention:" + convName,
		File:         v.File,
		Line:         v.Line,
		Column:       v.Column,
		Severity:     severity,
		Rule:         rule,
		Message:      v.Message,
		Snippet:      v.Snippet,
		SuggestedFix: v.SuggestedFix,
	}
}

// isPlainStringViolation reports whether the JS program returned just a
// string for this violation (vs. a structured object). Used to keep the
// existing prose-only output bytewise unchanged for the legacy case.
func isPlainStringViolation(v codegen.ConventionViolation) bool {
	return v.File == "" && v.Line == 0 && v.Column == 0 && v.Severity == "" && v.Rule == "" &&
		v.Snippet == "" && v.SuggestedFix == "" && v.Message != ""
}

// formatViolationLine renders a structured violation as
// "file:line:col: message" (omitting empty parts).
func formatViolationLine(v Violation) string {
	switch {
	case v.File != "" && v.Line > 0 && v.Column > 0:
		return fmt.Sprintf("%s:%d:%d: %s", v.File, v.Line, v.Column, v.Message)
	case v.File != "" && v.Line > 0:
		return fmt.Sprintf("%s:%d: %s", v.File, v.Line, v.Message)
	case v.File != "":
		return fmt.Sprintf("%s: %s", v.File, v.Message)
	default:
		return v.Message
	}
}

// ---- list_conventions -----------------------------------------------------

func (h *Handler) handleListConventions(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	conventions, err := m.ListConventions()
	if err != nil {
		return fmt.Sprintf("listing conventions: %v", err), true, nil
	}

	if len(conventions) == 0 {
		return "No conventions saved.", false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d convention(s):\n", len(conventions))
	for _, c := range conventions {
		fmt.Fprintf(&sb, "  %s", c.Name)
		if c.Description != "" {
			fmt.Fprintf(&sb, " — %s", c.Description)
		}
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- get_agent_prompt -----------------------------------------------------

func (h *Handler) handleGetAgentPrompt(_ map[string]any) (string, bool, error) {
	// Prefer the embedded guide (always available in release binaries).
	if embeddedAgentsGuide != "" {
		return embeddedAgentsGuide, false, nil
	}

	// Fall back to reading from disk (development).
	for _, candidate := range agentsGuideSearchPaths() {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return string(data), false, nil
		}
	}

	return "Sawmill: AST-level multi-language code transformations via MCP. Call parse <path> first.", false, nil
}

// agentsGuideSearchPaths returns candidate paths for agents-guide.md.
func agentsGuideSearchPaths() []string {
	exe, _ := os.Executable()
	var paths []string
	if exe != "" {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "agents-guide.md"))
		// Walk up a few levels (useful during development).
		dir := filepath.Dir(exe)
		for range 5 {
			paths = append(paths, filepath.Join(dir, "agents-guide.md"))
			dir = filepath.Dir(dir)
		}
	}
	paths = append(paths, "agents-guide.md")
	return paths
}

// ---- teach_by_example -----------------------------------------------------

func (h *Handler) handleTeachByExample(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	exem, err := requireString(args, "exemplar")
	if err != nil {
		return err.Error(), true, nil
	}
	parametersJSON, err := requireString(args, "parameters")
	if err != nil {
		return err.Error(), true, nil
	}
	alsoAffectsJSON := optString(args, "also_affects")

	var params map[string]string
	if err := json.Unmarshal([]byte(parametersJSON), &params); err != nil {
		return fmt.Sprintf("parsing parameters JSON: %v", err), true, nil
	}

	template := exemplar.Templatize(exem, params)

	var alsoAffects []string
	if alsoAffectsJSON != "" {
		if err := json.Unmarshal([]byte(alsoAffectsJSON), &alsoAffects); err != nil {
			return fmt.Sprintf("parsing also_affects JSON: %v", err), true, nil
		}
	}

	// Build a simple recipe with one replace step using the template.
	paramNames := make([]string, 0, len(params))
	for pname := range params {
		paramNames = append(paramNames, pname)
	}

	// The recipe step is a raw replace transform on the exemplar content.
	step := map[string]any{
		"raw_query": fmt.Sprintf("(source_file) @root"),
		"capture":   "root",
		"action":    "replace",
		"code":      template,
	}
	stepsData, err := json.Marshal([]any{step})
	if err != nil {
		return fmt.Sprintf("marshalling steps: %v", err), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveRecipe(name, description, paramNames, stepsData); err != nil {
		return fmt.Sprintf("saving recipe: %v", err), true, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Template extracted and saved as recipe %q.\n", name)
	fmt.Fprintf(&sb, "Parameters: %s\n", strings.Join(paramNames, ", "))
	fmt.Fprintf(&sb, "Template:\n%s\n", template)
	if len(alsoAffects) > 0 {
		fmt.Fprintf(&sb, "Also affects: %s\n", strings.Join(alsoAffects, ", "))
	}
	return sb.String(), false, nil
}

// ---- add_parameter --------------------------------------------------------

func (h *Handler) handleAddParameter(args map[string]any) (string, bool, error) {
	funcName, err := requireString(args, "function")
	if err != nil {
		return err.Error(), true, nil
	}
	paramName, err := requireString(args, "param_name")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	paramType := optString(args, "param_type")
	defaultValue := optString(args, "default_value")
	position := optString(args, "position")
	format := optBool(args, "format")

	paramText := buildParamText(paramName, ptr(paramType), ptr(defaultValue))

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			newSource, perr := addParamInSource(source, tree, acc.Adapter(), funcName, paramText, position)
			if perr != nil {
				return perr
			}
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("adding param to %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Function %q not found.", funcName), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Added parameter %q to %q in %d file(s). Call apply to write.\n\n", paramName, funcName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- remove_parameter -----------------------------------------------------

func (h *Handler) handleRemoveParameter(args map[string]any) (string, bool, error) {
	funcName, err := requireString(args, "function")
	if err != nil {
		return err.Error(), true, nil
	}
	paramName, err := requireString(args, "param_name")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			newSource, rerr := removeParamInSource(source, tree, acc.Adapter(), funcName, paramName)
			if rerr != nil {
				return rerr
			}
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("removing param from %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Parameter %q not found in function %q.", paramName, funcName), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Removed parameter %q from %q in %d file(s). Call apply to write.\n\n", paramName, funcName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- rename_file ----------------------------------------------------------

func (h *Handler) handleRenameFile(args map[string]any) (string, bool, error) {
	from, err := requireString(args, "from")
	if err != nil {
		return err.Error(), true, nil
	}
	to, err := requireString(args, "to")
	if err != nil {
		return err.Error(), true, nil
	}
	format := optBool(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	root := m.Root
	absFrom := filepath.Join(root, from)
	absTo := filepath.Join(root, to)

	// Verify the source file exists among tracked files.
	allAccessors, err := m.FileAccessors("")
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}
	var found bool
	for _, acc := range allAccessors {
		if acc.Path() == absFrom {
			found = true
			break
		}
	}
	if !found {
		return fmt.Sprintf("File %q not found in parsed codebase.", from), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	// Scan all files for imports that resolve to absFrom.
	for _, acc := range allAccessors {
		importQuery := acc.Adapter().ImportQuery()
		if importQuery == "" {
			continue
		}

		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			query, qErr := tree_sitter.NewQuery(acc.Adapter().Language(), importQuery)
			if qErr != nil {
				return nil
			}

			// Find the @name capture index.
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
				query.Close()
				return nil
			}

			cursor := tree_sitter.NewQueryCursor()
			matches := cursor.Matches(query, tree.RootNode(), source)

			var edits []rewrite.Edit
			for match := matches.Next(); match != nil; match = matches.Next() {
				for _, capture := range match.Captures {
					if capture.Index != nameIdx {
						continue
					}
					node := capture.Node
					importText := string(source[node.StartByte():node.EndByte()])

					resolved := acc.Adapter().ResolveImportPath(importText, acc.Path(), root)
					if resolved == "" {
						continue
					}

					absResolved := resolved
					if !filepath.IsAbs(resolved) {
						absResolved = filepath.Join(root, resolved)
					}

					if absResolved != absFrom {
						continue
					}

					newImport := acc.Adapter().BuildImportPath(absTo, acc.Path(), root)
					if newImport == "" {
						continue
					}

					edits = append(edits, rewrite.Edit{
						Start:       node.StartByte(),
						End:         node.EndByte(),
						Replacement: newImport,
					})
				}
			}

			cursor.Close()
			query.Close()

			if len(edits) == 0 {
				return nil
			}

			newSource := rewrite.ApplyEdits(source, edits)

			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}

			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("scanning imports in %s: %v", acc.Path(), err), true, nil
		}
	}

	renames := []FileRename{{From: absFrom, To: absTo}}

	h.pending = &PendingChanges{
		Changes: changes,
		Diffs:   diffs,
		Renames: renames,
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Rename %q → %q", from, to)
	if len(changes) > 0 {
		fmt.Fprintf(&sb, " with import updates in %d file(s)", len(changes))
	}
	sb.WriteString(". Call apply to write changes.\n\n")
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- clone_and_adapt ------------------------------------------------------

func (h *Handler) handleCloneAndAdapt(args map[string]any) (string, bool, error) {
	source, err := requireString(args, "source")
	if err != nil {
		return err.Error(), true, nil
	}
	subsJSON, err := requireString(args, "substitutions")
	if err != nil {
		return err.Error(), true, nil
	}
	targetFile, err := requireString(args, "target_file")
	if err != nil {
		return err.Error(), true, nil
	}
	position := optString(args, "position")
	format := optBool(args, "format")

	if position == "" {
		position = "end"
	}

	// Parse substitutions.
	var subs map[string]string
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return fmt.Sprintf("parsing substitutions JSON: %v", err), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	// Step 1: Extract source text.
	sourceText, err := extractCloneSource(m, source)
	if err != nil {
		return err.Error(), true, nil
	}

	// Step 2: Apply substitutions (longest keys first to avoid partial matches).
	keys := make([]string, 0, len(subs))
	for k := range subs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})
	clonedText := sourceText
	for _, k := range keys {
		clonedText = strings.ReplaceAll(clonedText, k, subs[k])
	}

	// Step 3: Find target file among tracked files.
	accessors, err := m.FileAccessors("")
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}
	var targetAcc *forest.FileAccessor
	for _, acc := range accessors {
		if acc.Path() == targetFile || strings.HasSuffix(acc.Path(), targetFile) {
			targetAcc = acc
			break
		}
	}
	if targetAcc == nil {
		return fmt.Sprintf("Target file %q not found in the parsed forest.", targetFile), true, nil
	}

	// Step 4+5: Within WithTree, determine insertion point and build new source.
	var changes []forest.FileChange
	var diffs []string

	err = targetAcc.WithTree(func(original []byte, tree *tree_sitter.Tree) error {
		pf := &forest.ParsedFile{
			Path:           targetAcc.Path(),
			OriginalSource: original,
			Tree:           tree,
			Adapter:        targetAcc.Adapter(),
		}
		insertOffset, perr := resolveInsertPosition(pf, position)
		if perr != nil {
			return perr
		}

		var newSource []byte
		prefix := "\n\n"
		suffix := "\n"
		if insertOffset == 0 {
			prefix = ""
			suffix = "\n\n"
		}
		newSource = make([]byte, 0, len(original)+len(clonedText)+4)
		newSource = append(newSource, original[:insertOffset]...)
		newSource = append(newSource, prefix...)
		newSource = append(newSource, clonedText...)
		newSource = append(newSource, suffix...)
		newSource = append(newSource, original[insertOffset:]...)

		if format {
			newSource, _ = rewrite.FormatSource(targetAcc.Adapter(), newSource)
		}

		diff := rewrite.UnifiedDiff(targetAcc.Path(), original, newSource)
		changes = append(changes, forest.FileChange{
			Path:      targetAcc.Path(),
			Original:  original,
			NewSource: newSource,
		})
		diffs = append(diffs, diff)
		return nil
	})
	if err != nil {
		return err.Error(), true, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Cloned and adapted into %s. Call apply to write.\n", targetAcc.Path())
	sb.WriteString("Note: You may need to add imports manually.\n\n")
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// extractCloneSource extracts source text from either a file:line_range spec or
// a symbol name search across tracked files. Returns the extracted text.
func extractCloneSource(m *model.CodebaseModel, source string) (string, error) {
	// Check for file:start-end range syntax.
	if idx := strings.LastIndex(source, ":"); idx > 0 {
		filePart := source[:idx]
		rangePart := source[idx+1:]
		if dashIdx := strings.Index(rangePart, "-"); dashIdx > 0 {
			startLine, err1 := strconv.Atoi(rangePart[:dashIdx])
			endLine, err2 := strconv.Atoi(rangePart[dashIdx+1:])
			if err1 == nil && err2 == nil && startLine > 0 && endLine >= startLine {
				accessors, err := m.FileAccessors("")
				if err != nil {
					return "", fmt.Errorf("listing files: %w", err)
				}
				for _, acc := range accessors {
					if acc.Path() == filePart || strings.HasSuffix(acc.Path(), filePart) {
						src, err := acc.Source()
						if err != nil {
							return "", fmt.Errorf("reading %s: %w", acc.Path(), err)
						}
						lines := strings.Split(string(src), "\n")
						if startLine > len(lines) {
							return "", fmt.Errorf("start line %d exceeds file length %d", startLine, len(lines))
						}
						if endLine > len(lines) {
							endLine = len(lines)
						}
						text := strings.Join(lines[startLine-1:endLine], "\n")
						return text, nil
					}
				}
				return "", fmt.Errorf("file %q not found in the parsed forest", filePart)
			}
		}
	}

	// Treat source as a symbol name — search all files for a matching
	// function or type definition.
	accessors, err := m.FileAccessors("")
	if err != nil {
		return "", fmt.Errorf("listing files: %w", err)
	}
	for _, acc := range accessors {
		var text string
		var found bool
		err := acc.WithTree(func(src []byte, tree *tree_sitter.Tree) error {
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: src,
				Tree:           tree,
				Adapter:        acc.Adapter(),
			}
			text, found = findSymbolNode(pf, source)
			return nil
		})
		if err != nil {
			return "", err
		}
		if found {
			return text, nil
		}
	}

	return "", fmt.Errorf("symbol %q not found in any parsed file", source)
}

// findSymbolNode searches a single file for a function or type definition with
// the given name and returns its full text.
func findSymbolNode(file *forest.ParsedFile, symbolName string) (string, bool) {
	// Try function definitions first, then type definitions.
	for _, queryStr := range []string{
		file.Adapter.FunctionDefQuery(),
		file.Adapter.TypeDefQuery(),
	} {
		if queryStr == "" {
			continue
		}

		lang := file.Adapter.Language()
		query, qErr := tree_sitter.NewQuery(lang, queryStr)
		if qErr != nil {
			continue
		}

		// Find @name and @func/@type_def capture indices.
		nameIdx := -1
		nodeIdx := -1
		for i, n := range query.CaptureNames() {
			switch n {
			case "name":
				nameIdx = i
			case "func":
				nodeIdx = i
			case "type_def":
				nodeIdx = i
			}
		}
		if nameIdx < 0 || nodeIdx < 0 {
			query.Close()
			continue
		}

		cursor := tree_sitter.NewQueryCursor()
		matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

		for match := matches.Next(); match != nil; match = matches.Next() {
			var nameNode, wholeNode *tree_sitter.Node
			for i := range match.Captures {
				c := &match.Captures[i]
				if c.Index == uint32(nameIdx) && nameNode == nil {
					nameNode = &c.Node
				}
				if c.Index == uint32(nodeIdx) && wholeNode == nil {
					wholeNode = &c.Node
				}
			}
			if nameNode == nil || wholeNode == nil {
				continue
			}
			name := string(file.OriginalSource[nameNode.StartByte():nameNode.EndByte()])
			if name == symbolName {
				text := string(file.OriginalSource[wholeNode.StartByte():wholeNode.EndByte()])
				cursor.Close()
				query.Close()
				return text, true
			}
		}
		cursor.Close()
		query.Close()
	}
	return "", false
}

// resolveInsertPosition determines the byte offset in the target file where
// cloned text should be inserted.
func resolveInsertPosition(file *forest.ParsedFile, position string) (uint, error) {
	source := file.OriginalSource

	switch {
	case position == "end":
		// Insert before the final newline if present.
		offset := uint(len(source))
		if offset > 0 && source[offset-1] == '\n' {
			offset--
		}
		return offset, nil

	case position == "start":
		// Insert after any leading comments and package/module declarations.
		// Heuristic: find the end of the first non-comment top-level node.
		root := file.Tree.RootNode()
		for i := uint(0); i < root.ChildCount(); i++ {
			child := root.Child(i)
			if child == nil {
				continue
			}
			kind := child.Kind()
			// Skip comments and package/module declarations.
			if strings.Contains(kind, "comment") ||
				kind == "package_clause" || kind == "module" ||
				kind == "package_statement" || kind == "shebang" {
				continue
			}
			// Insert before the first real content node.
			return child.StartByte(), nil
		}
		// If only comments/declarations, insert at end.
		return uint(len(source)), nil

	case strings.HasPrefix(position, "after:"):
		symbolName := strings.TrimPrefix(position, "after:")
		// Find the named symbol and insert after it.
		for _, queryStr := range []string{
			file.Adapter.FunctionDefQuery(),
			file.Adapter.TypeDefQuery(),
		} {
			if queryStr == "" {
				continue
			}

			lang := file.Adapter.Language()
			query, qErr := tree_sitter.NewQuery(lang, queryStr)
			if qErr != nil {
				continue
			}

			nameIdx := -1
			nodeIdx := -1
			for i, n := range query.CaptureNames() {
				switch n {
				case "name":
					nameIdx = i
				case "func", "type_def":
					nodeIdx = i
				}
			}
			if nameIdx < 0 || nodeIdx < 0 {
				query.Close()
				continue
			}

			cursor := tree_sitter.NewQueryCursor()
			matches := cursor.Matches(query, file.Tree.RootNode(), source)

			foundOffset := uint(0)
			found := false
			for match := matches.Next(); match != nil; match = matches.Next() {
				var nameNode, wholeNode *tree_sitter.Node
				for i := range match.Captures {
					c := &match.Captures[i]
					if c.Index == uint32(nameIdx) && nameNode == nil {
						nameNode = &c.Node
					}
					if c.Index == uint32(nodeIdx) && wholeNode == nil {
						wholeNode = &c.Node
					}
				}
				if nameNode == nil || wholeNode == nil {
					continue
				}
				name := string(source[nameNode.StartByte():nameNode.EndByte()])
				if name == symbolName {
					foundOffset = wholeNode.EndByte()
					found = true
					break
				}
			}
			cursor.Close()
			query.Close()
			if found {
				return foundOffset, nil
			}
		}
		return 0, fmt.Errorf("symbol %q not found in target file for after: position", symbolName)

	default:
		return 0, fmt.Errorf("invalid position %q: must be end, start, or after:<symbol_name>", position)
	}
}

// ---- LSP tools --------------------------------------------------------------

// requireInt extracts a required integer argument from the args map.
// JSON numbers arrive as float64.
func requireInt(args map[string]any, key string) (uint32, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch n := v.(type) {
	case float64:
		return uint32(n), nil
	case int:
		return uint32(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return uint32(i), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

// getLSPClient returns the LSP client for the given file, or (nil, msg) when
// no LSP is available. The second return is a user-facing message.
func (h *Handler) getLSPClient(m *model.CodebaseModel, file string) (*lspclient.Client, string) {
	ext := strings.TrimPrefix(filepath.Ext(file), ".")
	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		return nil, fmt.Sprintf("No language adapter for .%s files", ext)
	}
	if adapter.LSPCommand() == nil {
		return nil, fmt.Sprintf("No LSP server configured for %s", adapter.LSPLanguageID())
	}
	if m.LSP == nil {
		return nil, "LSP pool not initialized"
	}
	client := m.LSP.Get(adapter, m.Root)
	if client == nil {
		cmd := adapter.LSPCommand()
		return nil, fmt.Sprintf("LSP server %q not available (not installed or failed to start)", cmd[0])
	}
	return client, ""
}

func (h *Handler) handleHover(args map[string]any) (string, bool, error) {
	file, err := requireString(args, "file")
	if err != nil {
		return err.Error(), true, nil
	}
	line, err := requireInt(args, "line")
	if err != nil {
		return err.Error(), true, nil
	}
	col, err := requireInt(args, "column")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	client, msg := h.getLSPClient(m, file)
	if client == nil {
		return msg, false, nil
	}

	text, err := client.Hover(context.Background(), file, line, col)
	if err != nil {
		return fmt.Sprintf("hover: %v", err), true, nil
	}
	if text == "" {
		return "No type information available", false, nil
	}
	return text, false, nil
}

func (h *Handler) handleDefinition(args map[string]any) (string, bool, error) {
	file, err := requireString(args, "file")
	if err != nil {
		return err.Error(), true, nil
	}
	line, err := requireInt(args, "line")
	if err != nil {
		return err.Error(), true, nil
	}
	col, err := requireInt(args, "column")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	client, msg := h.getLSPClient(m, file)
	if client == nil {
		return msg, false, nil
	}

	locs, err := client.Definition(context.Background(), file, line, col)
	if err != nil {
		return fmt.Sprintf("definition: %v", err), true, nil
	}
	if len(locs) == 0 {
		return "No definition found", false, nil
	}
	return formatLocations(locs), false, nil
}

func (h *Handler) handleLspReferences(args map[string]any) (string, bool, error) {
	file, err := requireString(args, "file")
	if err != nil {
		return err.Error(), true, nil
	}
	line, err := requireInt(args, "line")
	if err != nil {
		return err.Error(), true, nil
	}
	col, err := requireInt(args, "column")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	client, msg := h.getLSPClient(m, file)
	if client == nil {
		return msg, false, nil
	}

	locs, err := client.References(context.Background(), file, line, col)
	if err != nil {
		return fmt.Sprintf("references: %v", err), true, nil
	}
	if len(locs) == 0 {
		return "No references found", false, nil
	}
	return formatLocations(locs), false, nil
}

func (h *Handler) handleDiagnostics(args map[string]any) (string, bool, error) {
	file, err := requireString(args, "file")
	if err != nil {
		return err.Error(), true, nil
	}
	format := optString(args, "format")
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return fmt.Sprintf("invalid format %q (want \"text\" or \"json\")", format), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	client, msg := h.getLSPClient(m, file)
	if client == nil {
		if format == "json" {
			return "[]", false, nil
		}
		return msg, false, nil
	}

	diags, err := client.Diagnostics(context.Background(), file)
	if err != nil {
		return fmt.Sprintf("diagnostics: %v", err), true, nil
	}

	if format == "json" {
		// Always return a non-nil slice so consumers see "[]" rather than "null".
		if diags == nil {
			diags = []lspclient.Diagnostic{}
		}
		out, err := json.MarshalIndent(diags, "", "  ")
		if err != nil {
			return fmt.Sprintf("marshalling diagnostics: %v", err), true, nil
		}
		return string(out), false, nil
	}

	if len(diags) == 0 {
		return "No diagnostics", false, nil
	}
	return formatDiagnostics(diags), false, nil
}

// formatLocations formats a slice of Locations as "file:line:col" entries.
func formatLocations(locs []lspclient.Location) string {
	var sb strings.Builder
	for _, l := range locs {
		fmt.Fprintf(&sb, "%s:%d:%d\n", l.File, l.Line, l.Column)
	}
	return sb.String()
}

// formatDiagnostics formats a slice of Diagnostics as
// "file:line:col [severity] [source code] message", omitting empty parts.
func formatDiagnostics(diags []lspclient.Diagnostic) string {
	var sb strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&sb, "%s:%d:%d [%s]", d.File, d.Line, d.Column, d.Severity)
		if d.Source != "" || d.Code != "" {
			sb.WriteString(" [")
			if d.Source != "" {
				sb.WriteString(d.Source)
				if d.Code != "" {
					sb.WriteByte(' ')
				}
			}
			if d.Code != "" {
				sb.WriteString(d.Code)
			}
			sb.WriteByte(']')
		}
		fmt.Fprintf(&sb, " %s\n", d.Message)
	}
	return sb.String()
}

// ---- dependency_usage -------------------------------------------------------

// depSymbolUsage records all usage sites for one imported symbol.
type depSymbolUsage struct {
	Name  string
	Kind  string // "type", "function", "value"
	Sites []depSite
}

// depSite records a single usage location.
type depSite struct {
	File string
	Line uint
}

// depPublicExposure records a symbol from the target package that appears in a
// public signature.
type depPublicExposure struct {
	Symbol  string
	Context string // e.g. "Key[T] uses hash.Hashable"
	File    string
	Line    uint
}

func (h *Handler) handleDependencyUsage(args map[string]any) (string, bool, error) {
	pkg, err := requireString(args, "package")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	// localName is the last path component (e.g. "hash" from "arr-ai/hash").
	localName := pkg
	if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
		localName = pkg[idx+1:]
	}

	// symbolUsage tracks usage per symbol across all files.
	symbolUsage := make(map[string]*depSymbolUsage)
	var exposures []depPublicExposure
	importCount := 0

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		importQuery := acc.Adapter().ImportQuery()
		if importQuery == "" {
			continue
		}

		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: source,
				Tree:           tree,
				Adapter:        acc.Adapter(),
			}

			// --- Step 1: find imports matching the target package ---
			alias, found := depFindImport(pf, importQuery, pkg, localName)
			if !found {
				return nil
			}

			importCount++

			if alias == "_" {
				return nil
			}

			// --- Step 2: find qualified access (alias.Symbol) ---
			localAlias := alias
			if localAlias == "" {
				localAlias = localName
			}

			if localAlias != "." {
				depCollectQualifiedAccess(pf, localAlias, symbolUsage)
			} else {
				depNoteDirectImport(pf, symbolUsage)
			}

			// --- Step 3: public API exposure ---
			if localAlias != "." && localAlias != "_" {
				depCollectPublicExposure(pf, localAlias, &exposures)
			}
			return nil
		})
		if err != nil {
			return fmt.Sprintf("scanning %s: %v", acc.Path(), err), true, nil
		}
	}

	if importCount == 0 {
		return fmt.Sprintf("Package %q not found in any imported file.", pkg), false, nil
	}

	// --- Format report ---
	var sb strings.Builder
	fmt.Fprintf(&sb, "Package %q used in %d file(s):\n", pkg, importCount)

	// Collect and sort symbols.
	var symbols []*depSymbolUsage
	for _, s := range symbolUsage {
		symbols = append(symbols, s)
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Kind != symbols[j].Kind {
			return symbols[i].Kind < symbols[j].Kind
		}
		return symbols[i].Name < symbols[j].Name
	})

	// Group by kind.
	kindGroups := make(map[string][]*depSymbolUsage)
	for _, s := range symbols {
		kindGroups[s.Kind] = append(kindGroups[s.Kind], s)
	}
	for _, kind := range []string{"type", "function", "value"} {
		group := kindGroups[kind]
		if len(group) == 0 {
			continue
		}
		label := map[string]string{
			"type":     "Types",
			"function": "Functions",
			"value":    "Values",
		}[kind]
		fmt.Fprintf(&sb, "  %s: ", label)
		parts := make([]string, 0, len(group))
		for _, s := range group {
			parts = append(parts, fmt.Sprintf("%s (%d site(s))", s.Name, len(s.Sites)))
		}
		sb.WriteString(strings.Join(parts, ", "))
		sb.WriteString("\n")
	}

	if len(exposures) > 0 {
		sb.WriteString("  Public API exposure:\n")
		for _, e := range exposures {
			fmt.Fprintf(&sb, "    %s (%s:%d)\n", e.Context, e.File, e.Line)
		}
	}

	return sb.String(), false, nil
}

// depFindImport searches the import statements in file for an import of pkg.
// Returns (alias, true) when found, where alias is the local name to use
// (defaultLocal if no explicit alias is present, "." for dot-imports, "_" for
// blank imports).
func depFindImport(
	file *forest.ParsedFile,
	importQuery string,
	pkg string,
	defaultLocal string,
) (alias string, found bool) {
	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, importQuery)
	if qErr != nil {
		return "", false
	}
	defer query.Close()

	// Find @name capture index.
	nameIdx := -1
	for i, name := range query.CaptureNames() {
		if name == "name" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return "", false
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, capture := range match.Captures {
			if int(capture.Index) != nameIdx {
				continue
			}
			node := capture.Node
			importText := string(file.OriginalSource[node.StartByte():node.EndByte()])
			stripped := strings.Trim(strings.TrimSpace(importText), `"'`)

			if depMatchesPackage(stripped, pkg) {
				localAlias := depExtractGoAlias(file, node)
				if localAlias == "" {
					localAlias = defaultLocal
				}
				return localAlias, true
			}

			// Python: "from X import Y" — the @name capture in Python's
			// import_statement query may only cover simple imports. Python
			// from-imports are covered by a separate parent node check.
			if depMatchesPythonFromImport(file, node, pkg) {
				return ".", true
			}
		}
	}
	return "", false
}

// depMatchesPackage returns true if the stripped import text refers to pkg.
func depMatchesPackage(stripped, pkg string) bool {
	if stripped == pkg {
		return true
	}
	// Suffix match: "hash" matches "arr-ai/hash" or "github.com/arr-ai/hash".
	if strings.HasSuffix(stripped, "/"+pkg) {
		return true
	}
	return false
}

// depMatchesPythonFromImport detects "from X import Y" by examining the parent
// import_from_statement node for module name matching pkg.
func depMatchesPythonFromImport(file *forest.ParsedFile, nameNode tree_sitter.Node, pkg string) bool {
	parent := nameNode.Parent()
	if parent == nil {
		return false
	}
	if parent.Kind() != "import_from_statement" {
		return false
	}
	text := string(file.OriginalSource[parent.StartByte():parent.EndByte()])
	// Match "from <pkg> import" accounting for dotted-path variants.
	pkgVariants := []string{
		pkg,
		strings.ReplaceAll(pkg, "/", "."),
		strings.ReplaceAll(strings.ReplaceAll(pkg, "/", "."), "-", "_"),
	}
	for _, v := range pkgVariants {
		if strings.HasPrefix(text, "from "+v+" import") {
			return true
		}
	}
	return false
}

// depExtractGoAlias extracts the explicit local alias from a Go import_spec
// node. Returns "" if no explicit alias is present.
func depExtractGoAlias(file *forest.ParsedFile, pathNode tree_sitter.Node) string {
	parent := pathNode.Parent()
	if parent == nil || parent.Kind() != "import_spec" {
		return ""
	}
	if parent.ChildCount() < 2 {
		return ""
	}
	first := parent.Child(0)
	if first == nil {
		return ""
	}
	kind := first.Kind()
	if kind == "package_identifier" || kind == "identifier" || kind == "blank_identifier" || kind == "dot" {
		text := string(file.OriginalSource[first.StartByte():first.EndByte()])
		switch text {
		case ".", "_":
			return text
		default:
			return text
		}
	}
	return ""
}

// depCollectQualifiedAccess finds alias.Symbol patterns in file and records
// them in symbolUsage.
func depCollectQualifiedAccess(
	file *forest.ParsedFile,
	alias string,
	symbolUsage map[string]*depSymbolUsage,
) {
	queryStr := depSelectorQuery(file.Adapter, alias)
	if queryStr == "" {
		return
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, queryStr)
	if qErr != nil {
		return
	}
	defer query.Close()

	// Find @field capture index.
	fieldIdx := -1
	for i, name := range query.CaptureNames() {
		if name == "field" {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 {
		return
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, capture := range match.Captures {
			if int(capture.Index) != fieldIdx {
				continue
			}
			node := capture.Node
			symbolName := string(file.OriginalSource[node.StartByte():node.EndByte()])
			pos := node.StartPosition()
			line := uint(pos.Row) + 1

			su, ok := symbolUsage[symbolName]
			if !ok {
				su = &depSymbolUsage{Name: symbolName, Kind: depInferSymbolKind(symbolName)}
				symbolUsage[symbolName] = su
			}
			su.Sites = append(su.Sites, depSite{File: file.Path, Line: line})
		}
	}
}

// depSelectorQuery returns a tree-sitter query that captures the field name in
// alias.Field expressions for the given adapter.
func depSelectorQuery(adapter adapters.LanguageAdapter, alias string) string {
	switch adapter.(type) {
	case *adapters.GoAdapter:
		return fmt.Sprintf(
			`(selector_expression operand: (identifier) @obj field: (_) @field (#eq? @obj %q))`,
			alias,
		)
	case *adapters.RustAdapter:
		return fmt.Sprintf(
			`[(scoped_identifier path: (identifier) @obj name: (identifier) @field (#eq? @obj %q)) `+
				`(scoped_type_identifier path: (identifier) @obj name: (type_identifier) @field (#eq? @obj %q))]`,
			alias, alias,
		)
	case *adapters.PythonAdapter:
		return fmt.Sprintf(
			`(attribute object: (identifier) @obj attribute: (identifier) @field (#eq? @obj %q))`,
			alias,
		)
	case *adapters.TypeScriptAdapter:
		return fmt.Sprintf(
			`(member_expression object: (identifier) @obj property: (property_identifier) @field (#eq? @obj %q))`,
			alias,
		)
	default:
		return ""
	}
}

// depNoteDirectImport records that a file uses a dot-import (Python "from X
// import *" or similar), where symbols cannot be attributed without LSP.
func depNoteDirectImport(file *forest.ParsedFile, symbolUsage map[string]*depSymbolUsage) {
	const dotImportNote = "<dot-import: symbols not attributable without LSP>"
	su, ok := symbolUsage[dotImportNote]
	if !ok {
		su = &depSymbolUsage{Name: dotImportNote, Kind: "value"}
		symbolUsage[dotImportNote] = su
	}
	su.Sites = append(su.Sites, depSite{File: file.Path, Line: 0})
}

// depInferSymbolKind heuristically classifies a symbol name:
//   - Starts with uppercase → "type" (Go exported type convention)
//   - Otherwise → "function"
func depInferSymbolKind(name string) string {
	if len(name) > 0 {
		ch := name[0]
		if ch >= 'A' && ch <= 'Z' {
			return "type"
		}
	}
	return "function"
}

// depCollectPublicExposure scans exported top-level functions/types in file for
// references to alias.Symbol patterns, recording them as public API exposures.
func depCollectPublicExposure(
	file *forest.ParsedFile,
	alias string,
	exposures *[]depPublicExposure,
) {
	for _, queryStr := range []string{
		file.Adapter.FunctionDefQuery(),
		file.Adapter.TypeDefQuery(),
	} {
		if queryStr == "" {
			continue
		}

		lang := file.Adapter.Language()
		query, qErr := tree_sitter.NewQuery(lang, queryStr)
		if qErr != nil {
			continue
		}

		nameIdx := -1
		nodeIdx := -1
		for i, n := range query.CaptureNames() {
			switch n {
			case "name":
				nameIdx = i
			case "func", "type_def":
				nodeIdx = i
			}
		}
		if nameIdx < 0 || nodeIdx < 0 {
			query.Close()
			continue
		}

		cursor := tree_sitter.NewQueryCursor()
		matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

		for match := matches.Next(); match != nil; match = matches.Next() {
			var nameNode, wholeNode *tree_sitter.Node
			for i := range match.Captures {
				c := &match.Captures[i]
				if int(c.Index) == nameIdx && nameNode == nil {
					nameNode = &c.Node
				}
				if int(c.Index) == nodeIdx && wholeNode == nil {
					wholeNode = &c.Node
				}
			}
			if nameNode == nil || wholeNode == nil {
				continue
			}

			symName := string(file.OriginalSource[nameNode.StartByte():nameNode.EndByte()])
			if !depIsExported(symName, file.Adapter) {
				continue
			}

			nodeText := string(file.OriginalSource[wholeNode.StartByte():wholeNode.EndByte()])
			pos := nameNode.StartPosition()
			line := uint(pos.Row) + 1

			prefix := alias + "."
			if idx := strings.Index(nodeText, prefix); idx >= 0 {
				rest := nodeText[idx+len(prefix):]
				end := 0
				for end < len(rest) && depIsIdentChar(rest[end]) {
					end++
				}
				if end > 0 {
					refSymbol := rest[:end]
					ctx := fmt.Sprintf("%s uses %s.%s", symName, alias, refSymbol)
					*exposures = append(*exposures, depPublicExposure{
						Symbol:  refSymbol,
						Context: ctx,
						File:    file.Path,
						Line:    line,
					})
				}
			}
		}

		cursor.Close()
		query.Close()
	}
}

// depIsExported returns true when a symbol is considered public/exported.
func depIsExported(name string, adapter adapters.LanguageAdapter) bool {
	if len(name) == 0 {
		return false
	}
	switch adapter.(type) {
	case *adapters.GoAdapter:
		ch := name[0]
		return ch >= 'A' && ch <= 'Z'
	default:
		return !strings.HasPrefix(name, "_")
	}
}

// depIsIdentChar returns true for valid identifier characters.
func depIsIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// ---- add_field --------------------------------------------------------------

func (h *Handler) handleAddField(args map[string]any) (string, bool, error) {
	typeName, err := requireString(args, "type_name")
	if err != nil {
		return err.Error(), true, nil
	}
	fieldName, err := requireString(args, "field_name")
	if err != nil {
		return err.Error(), true, nil
	}
	fieldType, err := requireString(args, "field_type")
	if err != nil {
		return err.Error(), true, nil
	}
	defaultValue, err := requireString(args, "default_value")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: source,
				Tree:           tree,
				Adapter:        acc.Adapter(),
			}
			edits := collectAddFieldEdits(pf, typeName, fieldName, fieldType, defaultValue)
			if len(edits) == 0 {
				return nil
			}
			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("adding field to %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Type %q not found.", typeName), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Added field %q to %q in %d file(s). Call apply to write.\n\n", fieldName, typeName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- migrate_type -----------------------------------------------------------

func (h *Handler) handleMigrateType(args map[string]any) (string, bool, error) {
	typeName, err := requireString(args, "type_name")
	if err != nil {
		return err.Error(), true, nil
	}
	rulesJSON, err := requireString(args, "rules")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	rules, err := parseMigrateRules(rulesJSON)
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var changes []forest.FileChange
	var diffs []string

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: source,
				Tree:           tree,
				Adapter:        acc.Adapter(),
			}
			edits := migrateTypeInFile(pf, typeName, rules)
			if len(edits) == 0 {
				return nil
			}
			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			return nil
		})
		if err != nil {
			return fmt.Sprintf("migrating type in %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Type %q not found or no rules matched.", typeName), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Migrated type %q in %d file(s). Call apply to write.\n\n", typeName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- teach_invariant ----------------------------------------------------------

func (h *Handler) handleTeachInvariant(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	ruleJSON, err := requireString(args, "rule")
	if err != nil {
		return err.Error(), true, nil
	}

	// Validate the rule before saving.
	if _, err := ParseInvariantRule(ruleJSON); err != nil {
		return fmt.Sprintf("invalid rule: %v", err), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveInvariant(name, description, ruleJSON); err != nil {
		return fmt.Sprintf("saving invariant: %v", err), true, nil
	}

	return fmt.Sprintf("Invariant %q saved.", name), false, nil
}

// ---- check_invariants ---------------------------------------------------------

func (h *Handler) handleCheckInvariants(args map[string]any) (string, bool, error) {
	pathFilter := optString(args, "path")
	format := optString(args, "format")
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return fmt.Sprintf("invalid format %q (want \"text\" or \"json\")", format), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	invariants, err := m.ListInvariants()
	if err != nil {
		return fmt.Sprintf("listing invariants: %v", err), true, nil
	}

	if len(invariants) == 0 {
		if format == "json" {
			return "[]", false, nil
		}
		return "No invariants defined.", false, nil
	}

	// Build a filtered forest view if a path filter is given.
	f := m.ForestSnapshot()
	if pathFilter != "" {
		var filtered []*forest.ParsedFile
		for _, file := range f.Files {
			if strings.Contains(file.Path, pathFilter) {
				filtered = append(filtered, file)
			}
		}
		f = &forest.Forest{Files: filtered}
	}

	type invResult struct {
		name       string
		err        error
		structured []Violation
	}
	results := make([]invResult, 0, len(invariants))

	for _, inv := range invariants {
		rule, perr := ParseInvariantRule(inv.RuleJSON)
		if perr != nil {
			results = append(results, invResult{name: inv.Name, err: fmt.Errorf("parse error: %w", perr)})
			continue
		}
		violations, cerr := CheckInvariant(f, inv.Name, rule)
		if cerr != nil {
			results = append(results, invResult{name: inv.Name, err: cerr})
			continue
		}
		results = append(results, invResult{name: inv.Name, structured: violations})
	}

	if format == "json" {
		var allViolations []Violation
		for _, r := range results {
			allViolations = append(allViolations, r.structured...)
		}
		out, err := json.MarshalIndent(allViolations, "", "  ")
		if err != nil {
			return fmt.Sprintf("marshalling: %v", err), true, nil
		}
		return string(out), false, nil
	}

	var sb strings.Builder
	totalViolations := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(&sb, "Invariant %q: %v\n", r.name, r.err)
			continue
		}
		if len(r.structured) == 0 {
			fmt.Fprintf(&sb, "Invariant %q: OK\n", r.name)
			continue
		}
		fmt.Fprintf(&sb, "Invariant %q: %d violation(s):\n", r.name, len(r.structured))
		for _, v := range r.structured {
			fmt.Fprintf(&sb, "  %s\n", FormatInvariantViolation(v))
			totalViolations++
		}
	}

	if totalViolations > 0 {
		fmt.Fprintf(&sb, "\n%d total violation(s).\n", totalViolations)
	} else {
		sb.WriteString("\nAll invariants satisfied.\n")
	}

	return sb.String(), false, nil
}

// ---- list_invariants ----------------------------------------------------------

func (h *Handler) handleListInvariants(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	invariants, err := m.ListInvariants()
	if err != nil {
		return fmt.Sprintf("listing invariants: %v", err), true, nil
	}

	if len(invariants) == 0 {
		return "No invariants saved.", false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d invariant(s):\n", len(invariants))
	for _, inv := range invariants {
		fmt.Fprintf(&sb, "  %s", inv.Name)
		if inv.Description != "" {
			fmt.Fprintf(&sb, " — %s", inv.Description)
		}
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- delete_invariant ---------------------------------------------------------

func (h *Handler) handleDeleteInvariant(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	deleted, err := m.DeleteInvariant(name)
	if err != nil {
		return fmt.Sprintf("deleting invariant: %v", err), true, nil
	}

	if !deleted {
		return fmt.Sprintf("Invariant %q not found.", name), true, nil
	}

	return fmt.Sprintf("Invariant %q deleted.", name), false, nil
}

// ---- git_log ----------------------------------------------------------------

func (h *Handler) handleGitLog(args map[string]any) (string, bool, error) {
	ref := optString(args, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	limitF, _ := args["limit"].(float64)
	limit := int(limitF)
	if limit <= 0 {
		limit = 20
	}
	pathFilter := optString(args, "path")

	h.mu.Lock()
	m, err := h.requireModel()
	h.mu.Unlock()
	if err != nil {
		return err.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	startSHA, err := repo.Resolve(ref)
	if err != nil {
		return fmt.Sprintf("resolving ref %q: %v", ref, err), true, nil
	}

	type commitEntry struct {
		SHA          string   `json:"sha"`
		ShortSHA     string   `json:"short_sha"`
		Author       string   `json:"author"`
		Email        string   `json:"email"`
		Date         string   `json:"date"`
		Message      string   `json:"message"`
		FilesChanged []string `json:"files_changed"`
	}

	var result []commitEntry
	prevBlobForPath := ""

	walkErr := repo.WalkCommits(startSHA, func(c *gitrepo.Commit) error {
		if err := m.GitIndex.EnsureCommitIndexed(c.SHA); err != nil {
			return fmt.Errorf("indexing commit %s: %w", c.SHA, err)
		}

		// Apply path filter if specified.
		if pathFilter != "" {
			blobSHA, ok, err := store.BlobSHAForFile(c.SHA, pathFilter)
			if err != nil {
				return err
			}
			if !ok {
				return nil // file not in this commit
			}
			if blobSHA == prevBlobForPath {
				return nil // file unchanged since last commit
			}
			prevBlobForPath = blobSHA
		}

		files, err := store.CommitFiles(c.SHA)
		if err != nil {
			return fmt.Errorf("getting files for commit %s: %w", c.SHA, err)
		}
		filePaths := make([]string, len(files))
		for i, f := range files {
			filePaths[i] = f.FilePath
		}

		msg := c.Message
		if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
			msg = msg[:idx]
		}
		msg = strings.TrimSpace(msg)

		sha := c.SHA
		shortSHA := sha
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}

		result = append(result, commitEntry{
			SHA:          sha,
			ShortSHA:     shortSHA,
			Author:       c.Author,
			Email:        c.Email,
			Date:         c.When.UTC().Format("2006-01-02T15:04:05Z"),
			Message:      msg,
			FilesChanged: filePaths,
		})

		if len(result) >= limit {
			return io.EOF
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Sprintf("walking commits: %v", walkErr), true, nil
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// ---- git_diff_summary -------------------------------------------------------

func (h *Handler) handleGitDiffSummary(args map[string]any) (string, bool, error) {
	base, err := requireString(args, "base")
	if err != nil {
		return err.Error(), true, nil
	}
	head := optString(args, "head")
	if head == "" {
		head = "HEAD"
	}
	pathFilter := optString(args, "path")

	h.mu.Lock()
	m, merr := h.requireModel()
	h.mu.Unlock()
	if merr != nil {
		return merr.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	baseSHA, err := repo.Resolve(base)
	if err != nil {
		return fmt.Sprintf("resolving base ref %q: %v", base, err), true, nil
	}
	headSHA, err := repo.Resolve(head)
	if err != nil {
		return fmt.Sprintf("resolving head ref %q: %v", head, err), true, nil
	}

	if err := m.GitIndex.EnsureCommitIndexed(baseSHA); err != nil {
		return fmt.Sprintf("indexing base commit: %v", err), true, nil
	}
	if err := m.GitIndex.EnsureCommitIndexed(headSHA); err != nil {
		return fmt.Sprintf("indexing head commit: %v", err), true, nil
	}

	baseFiles, err := store.CommitFiles(baseSHA)
	if err != nil {
		return fmt.Sprintf("getting base files: %v", err), true, nil
	}
	headFiles, err := store.CommitFiles(headSHA)
	if err != nil {
		return fmt.Sprintf("getting head files: %v", err), true, nil
	}

	baseMap := make(map[string]string, len(baseFiles))
	for _, f := range baseFiles {
		baseMap[f.FilePath] = f.BlobSHA
	}
	headMap := make(map[string]string, len(headFiles))
	for _, f := range headFiles {
		headMap[f.FilePath] = f.BlobSHA
	}

	type fileDiff struct {
		Path    string     `json:"path"`
		Status  string     `json:"status"`
		Symbols symbolDiff `json:"symbols"`
	}
	type diffResult struct {
		Files []fileDiff `json:"files"`
	}

	// Collect all paths from both commits.
	allPaths := make(map[string]bool)
	for p := range baseMap {
		allPaths[p] = true
	}
	for p := range headMap {
		allPaths[p] = true
	}

	var files []fileDiff
	for path := range allPaths {
		if pathFilter != "" && path != pathFilter {
			continue
		}
		baseBlobSHA, inBase := baseMap[path]
		headBlobSHA, inHead := headMap[path]

		var status string
		switch {
		case inBase && inHead && baseBlobSHA == headBlobSHA:
			continue // unchanged
		case inBase && inHead:
			status = "modified"
		case inHead:
			status = "added"
		default:
			status = "removed"
		}

		var syms symbolDiff
		if status == "modified" {
			baseSymbols, err := symbolsForBlob(store, repo, baseBlobSHA)
			if err != nil {
				return fmt.Sprintf("getting symbols for base blob %s: %v", baseBlobSHA, err), true, nil
			}
			headSymbols, err := symbolsForBlob(store, repo, headBlobSHA)
			if err != nil {
				return fmt.Sprintf("getting symbols for head blob %s: %v", headBlobSHA, err), true, nil
			}
			syms = diffSymbols(baseSymbols, headSymbols)
		} else if status == "added" {
			headSymbols, err := symbolsForBlob(store, repo, headBlobSHA)
			if err != nil {
				return fmt.Sprintf("getting symbols for head blob %s: %v", headBlobSHA, err), true, nil
			}
			names := symbolNames(headSymbols)
			syms = symbolDiff{Added: names, Removed: []string{}, Modified: []string{}}
		} else {
			baseSymbols, err := symbolsForBlob(store, repo, baseBlobSHA)
			if err != nil {
				return fmt.Sprintf("getting symbols for base blob %s: %v", baseBlobSHA, err), true, nil
			}
			names := symbolNames(baseSymbols)
			syms = symbolDiff{Added: []string{}, Removed: names, Modified: []string{}}
		}

		files = append(files, fileDiff{Path: path, Status: status, Symbols: syms})
	}

	// Sort by path for stable output.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	out, err := json.MarshalIndent(diffResult{Files: files}, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// symbolsForBlob retrieves SymbolInfo for a blob, reading source from git.
func symbolsForBlob(store *gitindex.Store, repo *gitrepo.Repo, blobSHA string) ([]gitindex.SymbolInfo, error) {
	indexed, err := store.IsIndexed(blobSHA)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, nil
	}
	source, err := repo.ReadBlob(blobSHA)
	if err != nil {
		return nil, err
	}
	return store.SymbolNames(blobSHA, source)
}

// symbolNames extracts just the names from a slice of SymbolInfo.
func symbolNames(syms []gitindex.SymbolInfo) []string {
	names := make([]string, len(syms))
	for i, s := range syms {
		names[i] = s.Name
	}
	return names
}

type symbolDiff struct {
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

// diffSymbols compares two sets of symbols and returns added/removed/modified lists.
// A symbol is "modified" if its name exists in both sets but at a different byte range.
func diffSymbols(base, head []gitindex.SymbolInfo) symbolDiff {
	type byteRange struct{ start, end int }
	baseMap := make(map[string]byteRange, len(base))
	for _, s := range base {
		baseMap[s.Name] = byteRange{s.StartByte, s.EndByte}
	}
	headMap := make(map[string]byteRange, len(head))
	for _, s := range head {
		headMap[s.Name] = byteRange{s.StartByte, s.EndByte}
	}

	var added, removed, modified []string
	for name, hr := range headMap {
		br, inBase := baseMap[name]
		if !inBase {
			added = append(added, name)
		} else if br != hr {
			modified = append(modified, name)
		}
	}
	for name := range baseMap {
		if _, inHead := headMap[name]; !inHead {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)
	return symbolDiff{
		Added:    nullSafeStringSlice(added),
		Removed:  nullSafeStringSlice(removed),
		Modified: nullSafeStringSlice(modified),
	}
}

func nullSafeStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- git_blame_symbol -------------------------------------------------------

type blameCommit struct {
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

type blameResult struct {
	Symbol               string       `json:"symbol"`
	Path                 string       `json:"path"`
	Introduced           *blameCommit `json:"introduced,omitempty"`
	LastModified         *blameCommit `json:"last_modified,omitempty"`
	BodyLastModified     *blameCommit `json:"body_last_modified,omitempty"`
	SignatureLastChanged *blameCommit `json:"signature_last_changed,omitempty"`
}

func (h *Handler) handleGitBlameSymbol(args map[string]any) (string, bool, error) {
	filePath, err := requireString(args, "path")
	if err != nil {
		return err.Error(), true, nil
	}
	symbolName, err := requireString(args, "symbol")
	if err != nil {
		return err.Error(), true, nil
	}
	ref := optString(args, "ref")
	if ref == "" {
		ref = "HEAD"
	}

	h.mu.Lock()
	m, merr := h.requireModel()
	h.mu.Unlock()
	if merr != nil {
		return merr.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	startSHA, err := repo.Resolve(ref)
	if err != nil {
		return fmt.Sprintf("resolving ref %q: %v", ref, err), true, nil
	}

	makeRef := func(c *gitrepo.Commit) *blameCommit {
		msg := c.Message
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return &blameCommit{
			SHA:     c.SHA,
			Author:  c.Author,
			Date:    c.When.UTC().Format("2006-01-02T15:04:05Z"),
			Message: strings.TrimSpace(msg),
		}
	}

	var introduced, lastModified, bodyLastModified, sigLastChanged *blameCommit
	var newestDecl, newestBody, newestSig string
	var lastModFound, bodyModFound, sigChangeFound bool
	var prevWithSymbol *blameCommit
	var prevBlobSHA string
	var symbolKind string

	walkErr := repo.WalkCommits(startSHA, func(c *gitrepo.Commit) error {
		if err := m.GitIndex.EnsureCommitIndexed(c.SHA); err != nil {
			return fmt.Errorf("indexing commit %s: %w", c.SHA, err)
		}

		blobSHA, ok, err := store.BlobSHAForFile(c.SHA, filePath)
		if err != nil {
			return err
		}
		if !ok {
			// File didn't exist at this commit — past the file's introduction.
			return io.EOF
		}

		// Same blob as the more-recent commit means same symbol content;
		// just push introduced back.
		if blobSHA == prevBlobSHA && prevWithSymbol != nil {
			introduced = makeRef(c)
			prevWithSymbol = introduced
			return nil
		}

		source, err := repo.ReadBlob(blobSHA)
		if err != nil {
			return err
		}
		syms, err := store.SymbolNames(blobSHA, source)
		if err != nil {
			return err
		}

		var sym *gitindex.SymbolInfo
		for i := range syms {
			if syms[i].Name == symbolName {
				sym = &syms[i]
				break
			}
		}
		if sym == nil {
			// Symbol absent in this commit — past the symbol's introduction.
			return io.EOF
		}

		decl, body, sig := extractSymbolSnapshot(store, source, *sym)
		cref := makeRef(c)

		if prevWithSymbol == nil {
			// First (newest) commit with the symbol — establish baseline.
			newestDecl = decl
			newestBody = body
			newestSig = sig
			symbolKind = sym.Kind
		} else {
			if !lastModFound && decl != newestDecl {
				lastModified = prevWithSymbol
				lastModFound = true
			}
			if !bodyModFound && body != newestBody {
				bodyLastModified = prevWithSymbol
				bodyModFound = true
			}
			if !sigChangeFound && sig != newestSig {
				sigLastChanged = prevWithSymbol
				sigChangeFound = true
			}
		}
		introduced = cref
		prevWithSymbol = cref
		prevBlobSHA = blobSHA
		return nil
	})

	if walkErr != nil {
		return fmt.Sprintf("walking commits: %v", walkErr), true, nil
	}
	if introduced == nil {
		return fmt.Sprintf("symbol %q not found in %s at %s", symbolName, filePath, ref), true, nil
	}

	// If an aspect never changed during the walk, it was last set at introduction.
	if !lastModFound {
		lastModified = introduced
	}

	result := blameResult{
		Symbol:       symbolName,
		Path:         filePath,
		Introduced:   introduced,
		LastModified: lastModified,
	}
	if symbolKind == "function" {
		if !bodyModFound {
			bodyLastModified = introduced
		}
		if !sigChangeFound {
			sigLastChanged = introduced
		}
		result.BodyLastModified = bodyLastModified
		result.SignatureLastChanged = sigLastChanged
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// extractSymbolSnapshot returns the source text of (declaration, body,
// signature) for a symbol. Body and sig are empty strings when sym is not a
// function — those distinctions only make sense for callable symbols.
func extractSymbolSnapshot(store *gitindex.Store, source []byte, sym gitindex.SymbolInfo) (decl, body, sig string) {
	if sym.DeclStartByte >= 0 && sym.DeclEndByte <= len(source) {
		decl = string(source[sym.DeclStartByte:sym.DeclEndByte])
	}
	if sym.Kind != "function" {
		return decl, "", ""
	}
	children, err := store.QueryChildren(sym.NodeID)
	if err != nil {
		return decl, "", ""
	}
	for _, c := range children {
		switch {
		case c.NodeType == "parameter_list" || c.FieldName == "parameters":
			if c.StartByte >= 0 && c.EndByte <= len(source) {
				sig = string(source[c.StartByte:c.EndByte])
			}
		case c.FieldName == "body" || c.NodeType == "block" || c.NodeType == "compound_statement":
			if c.StartByte >= 0 && c.EndByte <= len(source) {
				body = string(source[c.StartByte:c.EndByte])
			}
		}
	}
	return decl, body, sig
}

// ---- git_index ----------------------------------------------------------------

func (h *Handler) handleGitIndex(args map[string]any) (string, bool, error) {
	ref := optString(args, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	limitF, _ := args["limit"].(float64)
	limit := int(limitF)

	h.mu.Lock()
	m, err := h.requireModel()
	h.mu.Unlock()
	if err != nil {
		return err.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	if limit > 0 {
		n, err := m.GitIndex.IndexRange(ref, limit)
		if err != nil {
			return fmt.Sprintf("indexing commits: %v", err), true, nil
		}
		return fmt.Sprintf("Indexed %d commits from %s.", n, ref), false, nil
	}

	indexed := 0
	if err := m.GitIndex.IndexAll(ref, func(n int) { indexed = n }); err != nil {
		return fmt.Sprintf("indexing commits: %v", err), true, nil
	}
	return fmt.Sprintf("Indexed %d commits from %s.", indexed, ref), false, nil
}

// ---- semantic_diff ------------------------------------------------------------

func (h *Handler) handleSemanticDiff(args map[string]any) (string, bool, error) {
	base, err := requireString(args, "base")
	if err != nil {
		return err.Error(), true, nil
	}
	head := optString(args, "head")
	if head == "" {
		head = "HEAD"
	}
	pathFilter := optString(args, "path")

	h.mu.Lock()
	m, merr := h.requireModel()
	h.mu.Unlock()
	if merr != nil {
		return merr.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	baseSHA, err := repo.Resolve(base)
	if err != nil {
		return fmt.Sprintf("resolving base ref %q: %v", base, err), true, nil
	}
	headSHA, err := repo.Resolve(head)
	if err != nil {
		return fmt.Sprintf("resolving head ref %q: %v", head, err), true, nil
	}

	if err := m.GitIndex.EnsureCommitIndexed(baseSHA); err != nil {
		return fmt.Sprintf("indexing base commit: %v", err), true, nil
	}
	if err := m.GitIndex.EnsureCommitIndexed(headSHA); err != nil {
		return fmt.Sprintf("indexing head commit: %v", err), true, nil
	}

	result, err := semdiff.Diff(store, repo, baseSHA, headSHA)
	if err != nil {
		return fmt.Sprintf("computing semantic diff: %v", err), true, nil
	}

	// Apply path filter if specified.
	if pathFilter != "" {
		var filtered []semdiff.FileDiff
		for _, f := range result.Files {
			if f.Path == pathFilter || f.OldPath == pathFilter {
				filtered = append(filtered, f)
			}
		}
		result.Files = filtered
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// ---- api_changelog ------------------------------------------------------------

func (h *Handler) handleAPIChangelog(args map[string]any) (string, bool, error) {
	base, err := requireString(args, "base")
	if err != nil {
		return err.Error(), true, nil
	}
	head := optString(args, "head")
	if head == "" {
		head = "HEAD"
	}

	h.mu.Lock()
	m, merr := h.requireModel()
	h.mu.Unlock()
	if merr != nil {
		return merr.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	baseSHA, err := repo.Resolve(base)
	if err != nil {
		return fmt.Sprintf("resolving base ref %q: %v", base, err), true, nil
	}
	headSHA, err := repo.Resolve(head)
	if err != nil {
		return fmt.Sprintf("resolving head ref %q: %v", head, err), true, nil
	}

	if err := m.GitIndex.EnsureCommitIndexed(baseSHA); err != nil {
		return fmt.Sprintf("indexing base commit: %v", err), true, nil
	}
	if err := m.GitIndex.EnsureCommitIndexed(headSHA); err != nil {
		return fmt.Sprintf("indexing head commit: %v", err), true, nil
	}

	result, err := semdiff.Diff(store, repo, baseSHA, headSHA)
	if err != nil {
		return fmt.Sprintf("computing semantic diff: %v", err), true, nil
	}

	return semdiff.Changelog(result), false, nil
}

// ---- apply_equivalence -----------------------------------------------------

func (h *Handler) handleApplyEquivalence(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	direction, err := requireString(args, "direction")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	var srcPatStr, dstPatStr string
	switch direction {
	case "left_to_right":
		// patterns set after we look up the equivalence
	case "right_to_left":
	default:
		return fmt.Sprintf("invalid direction %q (want left_to_right or right_to_left)", direction), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	equiv, err := m.LoadEquivalence(name)
	if err != nil {
		return fmt.Sprintf("loading equivalence: %v", err), true, nil
	}
	if equiv == nil {
		return fmt.Sprintf("no equivalence named %q", name), true, nil
	}

	if direction == "left_to_right" {
		srcPatStr, dstPatStr = equiv.LeftPattern, equiv.RightPattern
	} else {
		srcPatStr, dstPatStr = equiv.RightPattern, equiv.LeftPattern
	}

	// Expand the source-pattern set via the transitive closure: any other
	// pattern in dstPatStr's equivalence class is also a valid source for
	// this rewrite. The destination is fixed by the chosen direction.
	allEquivs, err := m.ListEquivalences()
	if err != nil {
		return fmt.Sprintf("listing equivalences: %v", err), true, nil
	}
	graph := buildEquivalenceGraph(allEquivs)
	sources := []string{srcPatStr}
	for _, p := range graph.nonPreferredPatternsIn(dstPatStr) {
		if p != srcPatStr {
			sources = append(sources, p)
		}
	}

	type compiled struct {
		src    *Pattern
		srcStr string
	}
	patterns := make([]compiled, 0, len(sources))
	for _, s := range sources {
		patterns = append(patterns, compiled{src: ParsePattern(s), srcStr: s})
	}

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	var changes []forest.FileChange
	var diffs []string
	totalMatches := 0

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			pf := &forest.ParsedFile{
				Path:           acc.Path(),
				OriginalSource: source,
				Tree:           tree,
				Adapter:        acc.Adapter(),
			}
			var fileMatches []equivalenceMatch
			for _, p := range patterns {
				fileMatches = append(fileMatches, findEquivalenceMatches(pf, p.src, dstPatStr)...)
			}
			if len(fileMatches) == 0 {
				return nil
			}
			// Sort by start byte; drop overlaps (keep the first encountered).
			sort.Slice(fileMatches, func(i, j int) bool {
				return fileMatches[i].StartByte < fileMatches[j].StartByte
			})
			deduped := fileMatches[:0]
			var lastEnd uint
			for _, m := range fileMatches {
				if m.StartByte < lastEnd {
					continue
				}
				deduped = append(deduped, m)
				lastEnd = m.EndByte
			}
			edits := equivalenceEdits(deduped)
			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(acc.Adapter(), newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			totalMatches += len(deduped)
			return nil
		})
		if err != nil {
			return fmt.Sprintf("applying equivalence in %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Equivalence %q (%s) had no matches.", name, direction), false, nil
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	derivedNote := ""
	if len(sources) > 1 {
		derivedNote = fmt.Sprintf(" (including %d derived source pattern(s))", len(sources)-1)
	}
	fmt.Fprintf(&sb, "Applied equivalence %q (%s)%s: %d match(es) in %d file(s). Call apply to write.\n\n",
		name, direction, derivedNote, totalMatches, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// ---- check_equivalences ----------------------------------------------------

func (h *Handler) handleCheckEquivalences(args map[string]any) (string, bool, error) {
	pathFilter := optString(args, "path")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	equivs, err := m.ListEquivalences()
	if err != nil {
		return fmt.Sprintf("listing equivalences: %v", err), true, nil
	}
	graph := buildEquivalenceGraph(equivs)

	// Build the actionable scan list: for each class with a unanimous
	// preferred pattern, every other pattern in the class is "non-preferred"
	// and counts as a violation. This naturally honours derived equivalences
	// — patterns that share a class only via a transitive chain still appear.
	type scanPattern struct {
		nonPreferred string
		preferred    string
		classIdx     int
	}
	var scans []scanPattern
	classesWithPref := 0
	for idx, pref := range graph.preferred {
		if pref == "" {
			continue
		}
		classesWithPref++
		for _, p := range graph.classes[idx] {
			if p == pref {
				continue
			}
			scans = append(scans, scanPattern{nonPreferred: p, preferred: pref, classIdx: idx})
		}
	}
	if classesWithPref == 0 {
		return "No equivalences with a preferred direction defined.", false, nil
	}

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	type violation struct {
		source string // e.g. "equivalence:left↔right" or "equivalence:taught-name"
		path   string
		match  equivalenceMatch
	}
	var violations []violation

	for _, sp := range scans {
		srcPat := ParsePattern(sp.nonPreferred)
		// Try to find a taught equivalence connecting these two patterns to
		// give the violation a meaningful source label; fall back to "derived".
		label := equivalenceLabel(equivs, sp.nonPreferred, sp.preferred)

		for _, acc := range accessors {
			err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
				pf := &forest.ParsedFile{
					Path:           acc.Path(),
					OriginalSource: source,
					Tree:           tree,
					Adapter:        acc.Adapter(),
				}
				matches := findEquivalenceMatches(pf, srcPat, sp.preferred)
				for _, m := range matches {
					violations = append(violations, violation{
						source: label,
						path:   acc.Path(),
						match:  m,
					})
				}
				return nil
			})
			if err != nil {
				return fmt.Sprintf("checking equivalences in %s: %v", acc.Path(), err), true, nil
			}
		}
	}

	if len(violations) == 0 {
		return fmt.Sprintf("All %d equivalence class(es) with preferred direction are satisfied.", classesWithPref), false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d violation(s) across %d equivalence class(es):\n\n", len(violations), classesWithPref)
	for _, v := range violations {
		fmt.Fprintf(&sb, "  %s:%d:%d [%s]\n    %s\n    → %s\n\n",
			v.path, v.match.Line, v.match.Column, v.source, v.match.Original, v.match.Rewrite)
	}
	return sb.String(), false, nil
}

// equivalenceLabel returns the name of a taught equivalence directly
// connecting `from` and `to` (in either order), or "derived" if no such
// pair was directly taught.
func equivalenceLabel(equivs []store.Equivalence, from, to string) string {
	for _, e := range equivs {
		if (e.LeftPattern == from && e.RightPattern == to) || (e.LeftPattern == to && e.RightPattern == from) {
			return e.Name
		}
	}
	return "derived"
}

// ---- teach_equivalence / list_equivalences / delete_equivalence -------------

func (h *Handler) handleTeachEquivalence(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	leftPattern, err := requireString(args, "left_pattern")
	if err != nil {
		return err.Error(), true, nil
	}
	rightPattern, err := requireString(args, "right_pattern")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	direction := optString(args, "preferred_direction")

	if leftPattern == rightPattern {
		return "left_pattern and right_pattern must differ", true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveEquivalence(name, description, leftPattern, rightPattern, direction); err != nil {
		return fmt.Sprintf("saving equivalence: %v", err), true, nil
	}

	return fmt.Sprintf("Equivalence %q saved.", name), false, nil
}

func (h *Handler) handleListEquivalences(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	equivs, err := m.ListEquivalences()
	if err != nil {
		return fmt.Sprintf("listing equivalences: %v", err), true, nil
	}

	if len(equivs) == 0 {
		return "No equivalences saved.", false, nil
	}

	graph := buildEquivalenceGraph(equivs)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d equivalence(s) [taught]:\n", len(equivs))
	for _, e := range equivs {
		fmt.Fprintf(&sb, "  %s: %s ↔ %s", e.Name, e.LeftPattern, e.RightPattern)
		if e.PreferredDirection != "" {
			fmt.Fprintf(&sb, " [prefers %s]", e.PreferredDirection)
		}
		if e.Description != "" {
			fmt.Fprintf(&sb, " — %s", e.Description)
		}
		sb.WriteString("\n")
	}

	// Derived pairs: every (a, b) within a class that wasn't taught directly.
	type derivedPair struct {
		left, right, preferred string
		classIdx               int
	}
	var derived []derivedPair
	for idx, members := range graph.classes {
		if len(members) < 3 {
			continue // a class of 1 has no pairs; a class of 2 has only the taught pair
		}
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				pair := unorderedPair(members[i], members[j])
				if graph.taught[pair] {
					continue
				}
				derived = append(derived, derivedPair{
					left:      members[i],
					right:     members[j],
					preferred: graph.preferred[idx],
					classIdx:  idx,
				})
			}
		}
	}
	if len(derived) > 0 {
		fmt.Fprintf(&sb, "\n%d equivalence(s) [derived via transitive closure]:\n", len(derived))
		for _, d := range derived {
			fmt.Fprintf(&sb, "  %s ↔ %s", d.left, d.right)
			if d.preferred != "" {
				if d.preferred == d.left {
					sb.WriteString(" [prefers left]")
				} else if d.preferred == d.right {
					sb.WriteString(" [prefers right]")
				}
			}
			sb.WriteString("\n")
		}
	}

	return sb.String(), false, nil
}

func (h *Handler) handleDeleteEquivalence(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	deleted, err := m.DeleteEquivalence(name)
	if err != nil {
		return fmt.Sprintf("deleting equivalence: %v", err), true, nil
	}
	if !deleted {
		return fmt.Sprintf("No equivalence named %q.", name), false, nil
	}
	return fmt.Sprintf("Equivalence %q deleted.", name), false, nil
}

// ---- git_semantic_bisect ------------------------------------------------------

func (h *Handler) handleGitSemanticBisect(args map[string]any) (string, bool, error) {
	predicateStr, err := requireString(args, "predicate")
	if err != nil {
		return err.Error(), true, nil
	}
	good, err := requireString(args, "good")
	if err != nil {
		return err.Error(), true, nil
	}
	bad, err := requireString(args, "bad")
	if err != nil {
		return err.Error(), true, nil
	}

	pred, err := bisect.ParsePredicate(predicateStr)
	if err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	m, merr := h.requireModel()
	h.mu.Unlock()
	if merr != nil {
		return merr.Error(), true, nil
	}

	if m.GitIndex == nil {
		return "git index is not available (project root is not inside a git repository)", true, nil
	}

	repo := m.GitIndex.Repo()
	store := m.GitIndex.Store()

	goodSHA, err := repo.Resolve(good)
	if err != nil {
		return fmt.Sprintf("resolving good ref %q: %v", good, err), true, nil
	}
	badSHA, err := repo.Resolve(bad)
	if err != nil {
		return fmt.Sprintf("resolving bad ref %q: %v", bad, err), true, nil
	}

	result, err := bisect.Bisect(m.GitIndex, store, repo, pred, goodSHA, badSHA)
	if err != nil {
		return fmt.Sprintf("bisecting: %v", err), true, nil
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// ---- merge_three_way ---------------------------------------------------------

// mergeThreeWayResponse is the JSON shape returned by merge_three_way.
type mergeThreeWayResponse struct {
	Merged    string           `json:"merged"`
	Conflicts []merge.Conflict `json:"conflicts"`
	Stats     merge.Stats      `json:"stats"`
	Clean     bool             `json:"clean"`
}

// handleMergeThreeWay performs an AST-aware three-way merge between three
// blobs. Each side may be supplied as inline content or as a file path.
//
// The tool is stateless — it does not require a parsed model and works
// without an active project root. Callers (rebase bots, multi-root PR
// flows) can invoke it before binding a session via parse().
func (h *Handler) handleMergeThreeWay(args map[string]any) (string, bool, error) {
	base, err := loadMergeSide(args, "base")
	if err != nil {
		return err.Error(), true, nil
	}
	ours, err := loadMergeSide(args, "ours")
	if err != nil {
		return err.Error(), true, nil
	}
	theirs, err := loadMergeSide(args, "theirs")
	if err != nil {
		return err.Error(), true, nil
	}

	pathHint := optString(args, "path")
	if pathHint == "" {
		pathHint = optString(args, "ours_path")
	}

	language := optString(args, "language")
	adapter, err := pickMergeAdapter(language, pathHint)
	if err != nil {
		return err.Error(), true, nil
	}

	style := optString(args, "marker_style")
	res, err := merge.Merge(base, ours, theirs, adapter, merge.Options{Path: pathHint, Style: style})
	if err != nil {
		return fmt.Sprintf("merge: %v", err), true, nil
	}

	resp := mergeThreeWayResponse{
		Merged:    string(res.Merged),
		Conflicts: res.Conflicts,
		Stats:     res.Stats,
		Clean:     len(res.Conflicts) == 0,
	}
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling result: %v", err), true, nil
	}
	return string(out), false, nil
}

// loadMergeSide returns the bytes for one side of a merge given args
// containing either "<side>_content" (inline) or "<side>_path" (file).
// At least one must be provided.
func loadMergeSide(args map[string]any, side string) ([]byte, error) {
	if v, ok := args[side+"_content"].(string); ok {
		return []byte(v), nil
	}
	if p, ok := args[side+"_path"].(string); ok && p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s_path %q: %v", side, p, err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("either %s_content or %s_path is required", side, side)
}

// pickMergeAdapter resolves a language adapter from an explicit language
// hint (preferred) or from a file path's extension.
func pickMergeAdapter(language, path string) (adapters.LanguageAdapter, error) {
	if language != "" {
		a := adapters.ForExtension(language)
		if a == nil {
			return nil, fmt.Errorf("unknown language %q", language)
		}
		return a, nil
	}
	if path == "" {
		return nil, fmt.Errorf("specify language or supply path/ours_path with a recognised extension")
	}
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return nil, fmt.Errorf("no extension on path %q; supply language", path)
	}
	a := adapters.ForExtension(ext)
	if a == nil {
		return nil, fmt.Errorf("no adapter for extension %q", ext)
	}
	return a, nil
}
