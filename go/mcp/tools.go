// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/marcelocantos/sawmill/codegen"
	"github.com/marcelocantos/sawmill/exemplar"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/jsengine"
	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/rewrite"
	"github.com/marcelocantos/sawmill/transform"
)

// optString returns the string argument named key, or "" if absent/not a string.
func optString(req mcpgo.CallToolRequest, key string) string {
	v, _ := req.RequireString(key)
	return v
}

// optBool returns the bool argument named key, or false if absent.
func optBool(req mcpgo.CallToolRequest, key string) bool {
	v, _ := req.RequireBool(key)
	return v
}

// ptr returns a pointer to s, or nil if s == "".
func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// toolErr returns a tool-error result for the given error.
func toolErr(err error) (*mcpgo.CallToolResult, error) {
	return mcpgo.NewToolResultError(err.Error()), nil
}

// toolText returns a successful tool result with the given text.
func toolText(text string) (*mcpgo.CallToolResult, error) {
	return mcpgo.NewToolResultText(text), nil
}

// requireModel returns the active model or an error if parse has not been called.
func (s *SawmillServer) requireModel() (*model.CodebaseModel, error) {
	if s.model == nil {
		return nil, fmt.Errorf("no codebase loaded — call parse first")
	}
	return s.model, nil
}

// ---- parse ----------------------------------------------------------------

func (s *SawmillServer) handleParse(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return toolErr(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Close any existing model.
	if s.model != nil {
		_ = s.model.Close()
		s.model = nil
	}
	s.pending = nil
	s.lastBackups = nil

	m, err := model.Load(path)
	if err != nil {
		return toolErr(fmt.Errorf("parsing %q: %w", path, err))
	}

	s.model = m

	// Build summary.
	summary := m.SummaryByLanguage()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Parsed %d file(s) in %s\n", m.FileCount(), m.Root)
	for lang, count := range summary {
		fmt.Fprintf(&sb, "  %s: %d\n", lang, count)
	}

	return toolText(sb.String())
}

// ---- rename ---------------------------------------------------------------

func (s *SawmillServer) handleRename(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	from, err := req.RequireString("from")
	if err != nil {
		return toolErr(err)
	}
	to, err := req.RequireString("to")
	if err != nil {
		return toolErr(err)
	}
	pathFilter := optString(req, "path")
	format := optBool(req, "format")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	var changes []forest.FileChange
	var diffs []string

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := rewrite.RenameInFile(file.OriginalSource, file.Tree, file.Adapter, from, to)
		if err != nil {
			return toolErr(fmt.Errorf("renaming in %s: %w", file.Path, err))
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
		return toolText(fmt.Sprintf("No occurrences of %q found.", from))
	}

	s.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Renamed %q → %q in %d file(s). Call apply to write changes.\n\n", from, to, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
}

// ---- query ----------------------------------------------------------------

func (s *SawmillServer) handleQuery(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	kind := optString(req, "kind")
	name := optString(req, "name")
	fileFilter := optString(req, "file")
	rawQuery := optString(req, "raw_query")
	capture := optString(req, "capture")
	pathFilter := optString(req, "path")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	var matchSpec *transform.Match
	if rawQuery != "" {
		matchSpec = transform.RawMatch(rawQuery, capture)
	} else if kind != "" {
		matchSpec = transform.AbstractMatch(kind, name, fileFilter)
	} else {
		return toolErr(fmt.Errorf("provide either kind or raw_query"))
	}

	var results []forest.QueryResult
	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}
		fileResults, err := transform.QueryFile(file, matchSpec)
		if err != nil {
			return toolErr(fmt.Errorf("querying %s: %w", file.Path, err))
		}
		results = append(results, fileResults...)
	}

	if len(results) == 0 {
		return toolText("No matches found.")
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
	return toolText(sb.String())
}

// ---- find_symbol ----------------------------------------------------------

func (s *SawmillServer) handleFindSymbol(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	symbol, err := req.RequireString("symbol")
	if err != nil {
		return toolErr(err)
	}
	kind := optString(req, "kind")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	records, err := m.FindSymbols(symbol, kind)
	if err != nil {
		return toolErr(fmt.Errorf("finding symbols: %w", err))
	}

	if len(records) == 0 {
		return toolText(fmt.Sprintf("Symbol %q not found.", symbol))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d occurrence(s) of %q:\n", len(records), symbol)
	for _, r := range records {
		fmt.Fprintf(&sb, "  %s:%d  [%s] %s\n", r.FilePath, r.StartLine, r.Kind, r.Name)
	}
	return toolText(sb.String())
}

// ---- find_references ------------------------------------------------------

func (s *SawmillServer) handleFindReferences(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	symbol, err := req.RequireString("symbol")
	if err != nil {
		return toolErr(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	records, err := m.FindSymbols(symbol, "call")
	if err != nil {
		return toolErr(fmt.Errorf("finding references: %w", err))
	}

	if len(records) == 0 {
		return toolText(fmt.Sprintf("No call sites found for %q.", symbol))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d call site(s) for %q:\n", len(records), symbol)
	for _, r := range records {
		fmt.Fprintf(&sb, "  %s:%d\n", r.FilePath, r.StartLine)
	}
	return toolText(sb.String())
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

func (s *SawmillServer) handleTransform(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	spec := transformSpec{
		Kind:        optString(req, "kind"),
		Name:        optString(req, "name"),
		File:        optString(req, "file"),
		RawQuery:    optString(req, "raw_query"),
		Capture:     optString(req, "capture"),
		Action:      optString(req, "action"),
		TransformFn: optString(req, "transform_fn"),
		Path:        optString(req, "path"),
	}
	code := optString(req, "code")
	before := optString(req, "before")
	after := optString(req, "after")
	if code != "" {
		spec.Code = &code
	}
	if before != "" {
		spec.Before = &before
	}
	if after != "" {
		spec.After = &after
	}
	format := optBool(req, "format")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	changes, diffs, err := applyTransformSpec(m, spec, format)
	if err != nil {
		return toolErr(err)
	}

	if len(changes) == 0 {
		return toolText("No matches found; no changes made.")
	}

	s.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Transform produced changes in %d file(s). Call apply to write.\n\n", len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
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

func (s *SawmillServer) handleTransformBatch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	transformsJSON, err := req.RequireString("transforms")
	if err != nil {
		return toolErr(err)
	}
	pathFilter := optString(req, "path")
	format := optBool(req, "format")

	var specs []transformSpec
	if err := json.Unmarshal([]byte(transformsJSON), &specs); err != nil {
		return toolErr(fmt.Errorf("parsing transforms JSON: %w", err))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
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
			return toolErr(fmt.Errorf("step %d: %w", stepIdx+1, err))
		}

		// Merge into the accumulated pending map.
		for _, c := range changes {
			pending[c.Path] = c.NewSource
		}
		allChanges = mergeChanges(allChanges, changes)
		allDiffs = append(allDiffs, diffs...)
	}

	if len(allChanges) == 0 {
		return toolText("No matches found; no changes made.")
	}

	s.pending = &PendingChanges{Changes: allChanges, Diffs: allDiffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Batch transform produced changes in %d file(s). Call apply to write.\n\n", len(allChanges))
	for _, d := range allDiffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
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

func (s *SawmillServer) handleCodegen(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	program, err := req.RequireString("program")
	if err != nil {
		return toolErr(err)
	}
	format := optBool(req, "format")
	validate := optBool(req, "validate")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	changes, err := codegen.RunCodegen(m.Forest, program)
	if err != nil {
		return toolErr(fmt.Errorf("codegen: %w", err))
	}

	if len(changes) == 0 {
		return toolText("Codegen produced no changes.")
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

	s.pending = &PendingChanges{Changes: changes, Diffs: diffs}

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
	return toolText(sb.String())
}

// ---- apply ----------------------------------------------------------------

func (s *SawmillServer) handleApply(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	confirm, err := req.RequireBool("confirm")
	if err != nil {
		return toolErr(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pending == nil || len(s.pending.Changes) == 0 {
		return toolText("No pending changes to apply.")
	}

	if !confirm {
		return toolText(fmt.Sprintf("Pending %d change(s). Set confirm=true to apply.", len(s.pending.Changes)))
	}

	backupPaths, err := forest.ApplyWithBackup(s.pending.Changes)
	if err != nil {
		return toolErr(fmt.Errorf("applying changes: %w", err))
	}

	s.lastBackups = &LastBackups{Paths: backupPaths}
	applied := len(s.pending.Changes)
	s.pending = nil

	return toolText(fmt.Sprintf("Applied %d change(s). Backups created. Call undo to revert.", applied))
}

// ---- undo -----------------------------------------------------------------

func (s *SawmillServer) handleUndo(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastBackups == nil || len(s.lastBackups.Paths) == 0 {
		return toolText("No backups available to restore.")
	}

	restored, err := forest.UndoFromBackups(s.lastBackups.Paths)
	if err != nil {
		return toolErr(fmt.Errorf("undoing changes: %w", err))
	}

	s.lastBackups = nil
	return toolText(fmt.Sprintf("Restored %d file(s) from backup.", restored))
}

// ---- teach_recipe ---------------------------------------------------------

func (s *SawmillServer) handleTeachRecipe(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return toolErr(err)
	}
	description := optString(req, "description")
	paramsJSON := optString(req, "params")
	stepsJSON, err := req.RequireString("steps")
	if err != nil {
		return toolErr(err)
	}

	var params []string
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return toolErr(fmt.Errorf("parsing params JSON: %w", err))
		}
	}

	// Validate steps JSON.
	if !json.Valid([]byte(stepsJSON)) {
		return toolErr(fmt.Errorf("steps is not valid JSON"))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	if err := m.SaveRecipe(name, description, params, []byte(stepsJSON)); err != nil {
		return toolErr(fmt.Errorf("saving recipe: %w", err))
	}

	return toolText(fmt.Sprintf("Recipe %q saved.", name))
}

// ---- instantiate ----------------------------------------------------------

func (s *SawmillServer) handleInstantiate(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	recipeName, err := req.RequireString("recipe")
	if err != nil {
		return toolErr(err)
	}
	paramsJSON := optString(req, "params")
	pathFilter := optString(req, "path")
	format := optBool(req, "format")

	var params map[string]string
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return toolErr(fmt.Errorf("parsing params JSON: %w", err))
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	recipe, err := m.LoadRecipe(recipeName)
	if err != nil {
		return toolErr(fmt.Errorf("loading recipe %q: %w", recipeName, err))
	}

	// Unmarshal the steps.
	var steps []json.RawMessage
	if err := json.Unmarshal(recipe.Steps, &steps); err != nil {
		return toolErr(fmt.Errorf("parsing recipe steps: %w", err))
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
			return toolErr(fmt.Errorf("parsing step %d: %w", i+1, err))
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
			return toolErr(fmt.Errorf("step %d: %w", stepIdx+1, err))
		}
		for _, c := range changes {
			pending[c.Path] = c.NewSource
		}
		allChanges = mergeChanges(allChanges, changes)
		allDiffs = append(allDiffs, diffs...)
	}

	if len(allChanges) == 0 {
		return toolText("Recipe produced no changes.")
	}

	s.pending = &PendingChanges{Changes: allChanges, Diffs: allDiffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Recipe %q produced changes in %d file(s). Call apply to write.\n\n", recipeName, len(allChanges))
	for _, d := range allDiffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
}

// ---- list_recipes ---------------------------------------------------------

func (s *SawmillServer) handleListRecipes(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	recipes, err := m.ListRecipes()
	if err != nil {
		return toolErr(fmt.Errorf("listing recipes: %w", err))
	}

	if len(recipes) == 0 {
		return toolText("No recipes saved.")
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
	return toolText(sb.String())
}

// ---- teach_convention -----------------------------------------------------

func (s *SawmillServer) handleTeachConvention(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return toolErr(err)
	}
	description := optString(req, "description")
	checkProgram, err := req.RequireString("check_program")
	if err != nil {
		return toolErr(err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	if err := m.SaveConvention(name, description, checkProgram); err != nil {
		return toolErr(fmt.Errorf("saving convention: %w", err))
	}

	return toolText(fmt.Sprintf("Convention %q saved.", name))
}

// ---- check_conventions ----------------------------------------------------

func (s *SawmillServer) handleCheckConventions(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	pathFilter := optString(req, "path")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	conventions, err := m.ListConventions()
	if err != nil {
		return toolErr(fmt.Errorf("listing conventions: %w", err))
	}

	if len(conventions) == 0 {
		return toolText("No conventions defined.")
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

	return toolText(sb.String())
}

// ---- list_conventions -----------------------------------------------------

func (s *SawmillServer) handleListConventions(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	conventions, err := m.ListConventions()
	if err != nil {
		return toolErr(fmt.Errorf("listing conventions: %w", err))
	}

	if len(conventions) == 0 {
		return toolText("No conventions saved.")
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
	return toolText(sb.String())
}

// ---- get_agent_prompt -----------------------------------------------------

func (s *SawmillServer) handleGetAgentPrompt(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	// Try to read agents-guide.md from the binary's directory or from the
	// project root. Fall back to a minimal inline description.
	for _, candidate := range agentsGuideSearchPaths() {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return toolText(string(data))
		}
	}

	// Minimal fallback.
	return toolText(`Sawmill: AST-level multi-language code transformations via MCP.
Tools: parse, rename, query, find_symbol, find_references, transform, transform_batch, codegen, apply, undo, teach_recipe, instantiate, list_recipes, teach_convention, check_conventions, list_conventions, teach_by_example, add_parameter, remove_parameter.
Call parse <path> first, then use any other tool. Call apply after transforms to write changes to disk.`)
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

func (s *SawmillServer) handleTeachByExample(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return toolErr(err)
	}
	description := optString(req, "description")
	exem, err := req.RequireString("exemplar")
	if err != nil {
		return toolErr(err)
	}
	parametersJSON, err := req.RequireString("parameters")
	if err != nil {
		return toolErr(err)
	}
	alsoAffectsJSON := optString(req, "also_affects")

	var params map[string]string
	if err := json.Unmarshal([]byte(parametersJSON), &params); err != nil {
		return toolErr(fmt.Errorf("parsing parameters JSON: %w", err))
	}

	template := exemplar.Templatize(exem, params)

	var alsoAffects []string
	if alsoAffectsJSON != "" {
		if err := json.Unmarshal([]byte(alsoAffectsJSON), &alsoAffects); err != nil {
			return toolErr(fmt.Errorf("parsing also_affects JSON: %w", err))
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
		return toolErr(fmt.Errorf("marshalling steps: %w", err))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	if err := m.SaveRecipe(name, description, paramNames, stepsData); err != nil {
		return toolErr(fmt.Errorf("saving recipe: %w", err))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Template extracted and saved as recipe %q.\n", name)
	fmt.Fprintf(&sb, "Parameters: %s\n", strings.Join(paramNames, ", "))
	fmt.Fprintf(&sb, "Template:\n%s\n", template)
	if len(alsoAffects) > 0 {
		fmt.Fprintf(&sb, "Also affects: %s\n", strings.Join(alsoAffects, ", "))
	}
	return toolText(sb.String())
}

// ---- add_parameter --------------------------------------------------------

func (s *SawmillServer) handleAddParameter(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	funcName, err := req.RequireString("function")
	if err != nil {
		return toolErr(err)
	}
	paramName, err := req.RequireString("param_name")
	if err != nil {
		return toolErr(err)
	}
	pathFilter := optString(req, "path")
	paramType := optString(req, "param_type")
	defaultValue := optString(req, "default_value")
	position := optString(req, "position")
	format := optBool(req, "format")

	paramText := buildParamText(paramName, ptr(paramType), ptr(defaultValue))

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	var changes []forest.FileChange
	var diffs []string

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := addParamInFile(file, funcName, paramText, position)
		if err != nil {
			return toolErr(fmt.Errorf("adding param to %s: %w", file.Path, err))
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
		return toolText(fmt.Sprintf("Function %q not found.", funcName))
	}

	s.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Added parameter %q to %q in %d file(s). Call apply to write.\n\n", paramName, funcName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
}

// ---- remove_parameter -----------------------------------------------------

func (s *SawmillServer) handleRemoveParameter(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	funcName, err := req.RequireString("function")
	if err != nil {
		return toolErr(err)
	}
	paramName, err := req.RequireString("param_name")
	if err != nil {
		return toolErr(err)
	}
	pathFilter := optString(req, "path")
	format := optBool(req, "format")

	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.requireModel()
	if err != nil {
		return toolErr(err)
	}

	var changes []forest.FileChange
	var diffs []string

	for _, file := range m.Forest.Files {
		if pathFilter != "" && !strings.Contains(file.Path, pathFilter) {
			continue
		}

		newSource, err := removeParamInFile(file, funcName, paramName)
		if err != nil {
			return toolErr(fmt.Errorf("removing param from %s: %w", file.Path, err))
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
		return toolText(fmt.Sprintf("Parameter %q not found in function %q.", paramName, funcName))
	}

	s.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Removed parameter %q from %q in %d file(s). Call apply to write.\n\n", paramName, funcName, len(changes))
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	return toolText(sb.String())
}
