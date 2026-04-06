// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/codegen"
	"github.com/marcelocantos/sawmill/exemplar"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/jsengine"
	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/rewrite"
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
		// No path and model already loaded (e.g. by daemon handshake) — just
		// sync and return the summary.
		_ = h.model.Sync()
	} else {
		// Load a new model (or re-load if path changed).
		if path == "" {
			return "path is required when no model is pre-loaded", true, nil
		}
		if h.model != nil {
			_ = h.model.Close()
		}
		h.pending = nil
		h.lastBackups = nil

		m, err := model.Load(path)
		if err != nil {
			return fmt.Sprintf("parsing %q: %v", path, err), true, nil
		}
		h.model = m
	}

	// Build summary.
	summary := h.model.SummaryByLanguage()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Parsed %d file(s) in %s\n", h.model.FileCount(), h.model.Root)
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

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := rewrite.RenameInFile(file.OriginalSource, file.Tree, file.Adapter, from, to)
		if err != nil {
			return fmt.Sprintf("renaming in %s: %v", file.Path, err), true, nil
		}

		if string(newSource) == string(file.OriginalSource) {
			continue
		}

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)

		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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
	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}
		fileResults, err := transform.QueryFile(file, matchSpec)
		if err != nil {
			return fmt.Sprintf("querying %s: %v", file.Path, err), true, nil
		}
		results = append(results, fileResults...)
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

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	records, err := m.FindSymbols(symbol, "call")
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

	for _, file := range m.Forest.Files {
		if spec.Path != "" && !strings.Contains(file.Path, spec.Path) {
			continue
		}

		var newSource []byte
		var err error

		if spec.TransformFn != "" {
			// JS-based transform.
			queryStr := ""
			if matchSpec != nil {
				queryStr, err = transform.ResolveQueryStr(file.Adapter, matchSpec)
				if err != nil {
					return nil, nil, fmt.Errorf("resolving query for %s: %w", file.Path, err)
				}
			}
			if queryStr == "" {
				continue
			}
			newSource, err = jsengine.RunJSTransform(
				file.OriginalSource, file.Tree, queryStr, spec.TransformFn, file.Path, file.Adapter,
			)
		} else {
			// Declarative match/act transform.
			newSource, err = transform.TransformFile(file, matchSpec, action)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("transforming %s: %w", file.Path, err)
		}

		if string(newSource) == string(file.OriginalSource) {
			continue
		}

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)
		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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

	for _, file := range m.Forest.Files {
		if spec.Path != "" && !strings.Contains(file.Path, spec.Path) {
			continue
		}

		// Use the overridden source if available.
		currentSource := file.OriginalSource
		if ov, ok := overrides[file.Path]; ok {
			currentSource = ov
		}

		// Build a temporary file view with the overridden source.
		tmpFile := &forest.ParsedFile{
			Path:           file.Path,
			OriginalSource: currentSource,
			Tree:           file.Tree,
			Adapter:        file.Adapter,
		}

		var newSource []byte
		var err error

		if spec.TransformFn != "" {
			queryStr := ""
			if matchSpec != nil {
				queryStr, err = transform.ResolveQueryStr(file.Adapter, matchSpec)
				if err != nil {
					return nil, nil, err
				}
			}
			if queryStr == "" {
				continue
			}
			newSource, err = jsengine.RunJSTransform(
				currentSource, file.Tree, queryStr, spec.TransformFn, file.Path, file.Adapter,
			)
		} else {
			newSource, err = transform.TransformFile(tmpFile, matchSpec, action)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("transforming %s: %w", file.Path, err)
		}

		if string(newSource) == string(currentSource) {
			continue
		}

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		// Diff against the original file (not the step-accumulated source).
		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)
		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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
		changes, err = codegen.RunCodegenWithLSP(m.Forest, program, m.LSP, m.Root)
	} else {
		changes, err = codegen.RunCodegen(m.Forest, program)
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
		structErrs := codegen.StructuralChecks(m.Forest, changes)
		warnings = append(parseErrs, structErrs...)
	}

	if format {
		for i, c := range changes {
			ext := filepath.Ext(c.Path)
			if ext == "" {
				continue
			}
			// Reuse the existing file's adapter if possible.
			for _, file := range m.Forest.Files {
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

	h.lastBackups = &LastBackups{Paths: backupPaths}
	applied := totalPending
	h.pending = nil

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
		return "No conventions defined.", false, nil
	}

	// Build a filtered forest view if a path filter is given.
	f := m.Forest
	if pathFilter != "" {
		var filtered []*forest.ParsedFile
		for _, file := range f.Files {
			if strings.Contains(file.Path, pathFilter) {
				filtered = append(filtered, file)
			}
		}
		f = &forest.Forest{Files: filtered}
	}

	var sb strings.Builder
	totalViolations := 0

	for _, conv := range conventions {
		violations, err := codegen.RunConventionCheck(f, conv.CheckProgram)
		if err != nil {
			fmt.Fprintf(&sb, "Convention %q: error: %v\n", conv.Name, err)
			continue
		}
		if len(violations) == 0 {
			fmt.Fprintf(&sb, "Convention %q: OK\n", conv.Name)
		} else {
			fmt.Fprintf(&sb, "Convention %q: %d violation(s):\n", conv.Name, len(violations))
			for _, v := range violations {
				fmt.Fprintf(&sb, "  %s\n", v)
				totalViolations++
			}
		}
	}

	if totalViolations > 0 {
		fmt.Fprintf(&sb, "\n%d total violation(s).\n", totalViolations)
	} else {
		sb.WriteString("\nAll conventions satisfied.\n")
	}

	return sb.String(), false, nil
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

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := addParamInFile(file, funcName, paramText, position)
		if err != nil {
			return fmt.Sprintf("adding param to %s: %v", file.Path, err), true, nil
		}

		if string(newSource) == string(file.OriginalSource) {
			continue
		}

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)
		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := removeParamInFile(file, funcName, paramName)
		if err != nil {
			return fmt.Sprintf("removing param from %s: %v", file.Path, err), true, nil
		}

		if string(newSource) == string(file.OriginalSource) {
			continue
		}

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)
		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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

	// Verify the source file exists in the forest.
	var found bool
	for _, file := range m.Forest.Files {
		if file.Path == absFrom {
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
	for _, file := range m.Forest.Files {
		importQuery := file.Adapter.ImportQuery()
		if importQuery == "" {
			continue
		}

		query, qErr := tree_sitter.NewQuery(file.Adapter.Language(), importQuery)
		if qErr != nil {
			continue
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
			continue
		}

		cursor := tree_sitter.NewQueryCursor()
		matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

		var edits []rewrite.Edit
		for match := matches.Next(); match != nil; match = matches.Next() {
			for _, capture := range match.Captures {
				if capture.Index != nameIdx {
					continue
				}
				node := capture.Node
				importText := string(file.OriginalSource[node.StartByte():node.EndByte()])

				resolved := file.Adapter.ResolveImportPath(importText, file.Path, root)
				if resolved == "" {
					continue
				}

				// ResolveImportPath may return absolute or relative paths
				// depending on the adapter. Normalise to absolute for comparison.
				absResolved := resolved
				if !filepath.IsAbs(resolved) {
					absResolved = filepath.Join(root, resolved)
				}

				if absResolved != absFrom {
					continue
				}

				// This import references the file being renamed — compute the
				// new import text.
				newImport := file.Adapter.BuildImportPath(absTo, file.Path, root)
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
			continue
		}

		newSource := rewrite.ApplyEdits(file.OriginalSource, edits)

		if format {
			newSource, _ = rewrite.FormatSource(file.Adapter, newSource)
		}

		diff := rewrite.UnifiedDiff(file.Path, file.OriginalSource, newSource)
		diffs = append(diffs, diff)
		changes = append(changes, forest.FileChange{
			Path:      file.Path,
			Original:  file.OriginalSource,
			NewSource: newSource,
		})
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
	sourceText, sourceFile, err := extractCloneSource(m, source)
	if err != nil {
		return err.Error(), true, nil
	}
	_ = sourceFile // used for future import propagation

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

	// Step 3: Find target file in the forest.
	var targetPF *forest.ParsedFile
	for _, file := range m.Forest.Files {
		if file.Path == targetFile || strings.HasSuffix(file.Path, targetFile) {
			targetPF = file
			break
		}
	}
	if targetPF == nil {
		return fmt.Sprintf("Target file %q not found in the parsed forest.", targetFile), true, nil
	}

	// Step 4: Determine insertion point.
	insertOffset, err := resolveInsertPosition(targetPF, position)
	if err != nil {
		return err.Error(), true, nil
	}

	// Step 5: Build the new source with the cloned text inserted.
	original := targetPF.OriginalSource
	var newSource []byte
	// Ensure the cloned text is separated by blank lines.
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
		newSource, _ = rewrite.FormatSource(targetPF.Adapter, newSource)
	}

	diff := rewrite.UnifiedDiff(targetPF.Path, original, newSource)
	changes := []forest.FileChange{{
		Path:      targetPF.Path,
		Original:  original,
		NewSource: newSource,
	}}
	diffs := []string{diff}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Cloned and adapted into %s. Call apply to write.\n", targetPF.Path)
	sb.WriteString("Note: You may need to add imports manually.\n\n")
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return sb.String(), false, nil
}

// extractCloneSource extracts source text from either a file:line_range spec or
// a symbol name search across the forest. Returns the text and the source file.
func extractCloneSource(m *model.CodebaseModel, source string) (string, *forest.ParsedFile, error) {
	// Check for file:start-end range syntax.
	if idx := strings.LastIndex(source, ":"); idx > 0 {
		filePart := source[:idx]
		rangePart := source[idx+1:]
		if dashIdx := strings.Index(rangePart, "-"); dashIdx > 0 {
			startLine, err1 := strconv.Atoi(rangePart[:dashIdx])
			endLine, err2 := strconv.Atoi(rangePart[dashIdx+1:])
			if err1 == nil && err2 == nil && startLine > 0 && endLine >= startLine {
				// Find the file in the forest.
				for _, file := range m.Forest.Files {
					if file.Path == filePart || strings.HasSuffix(file.Path, filePart) {
						lines := strings.Split(string(file.OriginalSource), "\n")
						if startLine > len(lines) {
							return "", nil, fmt.Errorf("start line %d exceeds file length %d", startLine, len(lines))
						}
						if endLine > len(lines) {
							endLine = len(lines)
						}
						// Lines are 1-based.
						text := strings.Join(lines[startLine-1:endLine], "\n")
						return text, file, nil
					}
				}
				return "", nil, fmt.Errorf("file %q not found in the parsed forest", filePart)
			}
		}
	}

	// Treat source as a symbol name — search all files for a matching
	// function or type definition.
	for _, file := range m.Forest.Files {
		text, found := findSymbolNode(file, source)
		if found {
			return text, file, nil
		}
	}

	return "", nil, fmt.Errorf("symbol %q not found in any parsed file", source)
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

	diags, err := client.Diagnostics(context.Background(), file)
	if err != nil {
		return fmt.Sprintf("diagnostics: %v", err), true, nil
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

// formatDiagnostics formats a slice of Diagnostics as "file:line:col [severity] message".
func formatDiagnostics(diags []lspclient.Diagnostic) string {
	var sb strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&sb, "%s:%d:%d [%s] %s\n", d.File, d.Line, d.Column, d.Severity, d.Message)
	}
	return sb.String()
}
