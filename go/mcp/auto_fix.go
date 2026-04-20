// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/store"
)

// AutoFixApplied records one fix that was applied during a single iteration.
type AutoFixApplied struct {
	Fix        string               `json:"fix"`
	Diagnostic lspclient.Diagnostic `json:"diagnostic"`
	Captures   map[string]string    `json:"captures,omitempty"`
}

// AutoFixSuggestion records a fix that matched a diagnostic but was not
// applied — either because confidence=suggest or because the call was a
// dry run.
type AutoFixSuggestion struct {
	Fix        string               `json:"fix"`
	Diagnostic lspclient.Diagnostic `json:"diagnostic"`
	Captures   map[string]string    `json:"captures,omitempty"`
	Reason     string               `json:"reason"` // "confidence_suggest" | "dry_run"
}

// AutoFixCycleWarning records a diagnostic that reappeared after its fix
// was applied — i.e. the fix didn't actually resolve the issue.
type AutoFixCycleWarning struct {
	Fix        string               `json:"fix"`
	Diagnostic lspclient.Diagnostic `json:"diagnostic"`
	Iteration  int                  `json:"iteration"`
}

// AutoFixIteration is the per-iteration outcome.
type AutoFixIteration struct {
	Iteration       int              `json:"iteration"`
	DiagnosticsSeen int              `json:"diagnostics_seen"`
	FixesApplied    []AutoFixApplied `json:"fixes_applied"`
}

// AutoFixResult is the structured response returned by auto_fix.
type AutoFixResult struct {
	File                 string                 `json:"file"`
	TerminationReason    string                 `json:"termination_reason"` // clean | stuck | iteration_limit
	Iterations           []AutoFixIteration     `json:"iterations"`
	RemainingDiagnostics []lspclient.Diagnostic `json:"remaining_diagnostics"`
	Suggestions          []AutoFixSuggestion    `json:"suggestions"`
	CycleWarnings        []AutoFixCycleWarning  `json:"cycle_warnings"`
	DryRun               bool                   `json:"dry_run"`
}

// preparedFix bundles a stored fix entry with its compiled regex, so the
// loop can match without recompiling each iteration.
type preparedFix struct {
	entry store.Fix
	re    *regexp.Regexp
}

// autoFixDeps groups the side-effecting calls handleAutoFix makes so tests
// can substitute stubs (real LSP and on-disk apply aren't friendly to unit
// tests).
type autoFixDeps struct {
	collect func(file string) ([]lspclient.Diagnostic, error)
	apply   func(entry store.Fix, captures map[string]string) error
}

// handleAutoFix implements the MCP auto_fix tool — the convergence loop
// that drives diagnostic-driven fixes (🎯T3.2).
func (h *Handler) handleAutoFix(args map[string]any) (string, bool, error) {
	return h.runAutoFix(args, autoFixDeps{
		collect: h.collectDiagnostics,
		apply:   h.applyFixAction,
	})
}

func (h *Handler) runAutoFix(args map[string]any, deps autoFixDeps) (string, bool, error) {
	file, err := requireString(args, "file")
	if err != nil {
		return err.Error(), true, nil
	}
	maxIterFloat, _ := args["max_iterations"].(float64)
	maxIter := int(maxIterFloat)
	if maxIter <= 0 {
		maxIter = 10
	}
	dryRun := optBool(args, "dry_run")

	// Snapshot the catalogue once at start; new entries added during the
	// loop don't take effect until the next call.
	h.mu.Lock()
	m, err := h.requireModel()
	h.mu.Unlock()
	if err != nil {
		return err.Error(), true, nil
	}
	rawFixes, err := m.ListFixes()
	if err != nil {
		return fmt.Sprintf("listing fixes: %v", err), true, nil
	}
	fixes := make([]preparedFix, 0, len(rawFixes))
	for _, f := range rawFixes {
		re, rerr := regexp.Compile(f.DiagnosticRegex)
		if rerr != nil {
			// Skip entries with invalid regexes; they shouldn't pass teach_fix
			// but we don't want one bad entry to break the whole loop.
			continue
		}
		fixes = append(fixes, preparedFix{entry: f, re: re})
	}

	result := &AutoFixResult{
		File:   file,
		DryRun: dryRun,
	}

	// Cycle detection: which (file, code, message) signatures have we
	// already applied a fix for?
	appliedFor := make(map[string]string) // signature → fix name

	for iter := 1; iter <= maxIter; iter++ {
		diags, err := deps.collect(file)
		if err != nil {
			return fmt.Sprintf("collecting diagnostics on iteration %d: %v", iter, err), true, nil
		}

		if len(diags) == 0 {
			result.TerminationReason = "clean"
			break
		}

		iterRecord := AutoFixIteration{
			Iteration:       iter,
			DiagnosticsSeen: len(diags),
		}

		for _, d := range diags {
			sig := diagnosticSignature(d)

			// If we've previously applied a fix for an identical diagnostic
			// and it's still here, the fix is broken. Record once and skip.
			if prev, seen := appliedFor[sig]; seen {
				result.CycleWarnings = append(result.CycleWarnings, AutoFixCycleWarning{
					Fix:        prev,
					Diagnostic: d,
					Iteration:  iter,
				})
				delete(appliedFor, sig) // don't re-flag on subsequent iterations
				continue
			}

			pf, captures, ok := matchFix(fixes, d)
			if !ok {
				continue
			}

			// Suggest mode: never apply.
			if pf.entry.Confidence == store.FixConfidenceSuggest || dryRun {
				reason := "confidence_suggest"
				if dryRun {
					reason = "dry_run"
				}
				result.Suggestions = append(result.Suggestions, AutoFixSuggestion{
					Fix:        pf.entry.Name,
					Diagnostic: d,
					Captures:   captures,
					Reason:     reason,
				})
				continue
			}

			// Apply: instantiate the action with substituted captures, then apply.
			if applyErr := deps.apply(pf.entry, captures); applyErr != nil {
				// Treat apply errors as non-fatal: record as a cycle warning so
				// the orchestrator sees the fix didn't take, then move on.
				result.CycleWarnings = append(result.CycleWarnings, AutoFixCycleWarning{
					Fix:        pf.entry.Name + " (apply error: " + applyErr.Error() + ")",
					Diagnostic: d,
					Iteration:  iter,
				})
				continue
			}
			appliedFor[sig] = pf.entry.Name
			iterRecord.FixesApplied = append(iterRecord.FixesApplied, AutoFixApplied{
				Fix:        pf.entry.Name,
				Diagnostic: d,
				Captures:   captures,
			})
		}

		result.Iterations = append(result.Iterations, iterRecord)

		if len(iterRecord.FixesApplied) == 0 {
			result.TerminationReason = "stuck"
			result.RemainingDiagnostics = diags
			break
		}
	}

	if result.TerminationReason == "" {
		result.TerminationReason = "iteration_limit"
		// Capture remaining diagnostics for visibility.
		if diags, err := deps.collect(file); err == nil {
			result.RemainingDiagnostics = diags
		}
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling auto_fix result: %v", err), true, nil
	}
	return string(out), false, nil
}

// collectDiagnostics calls handleDiagnostics in JSON mode and parses the
// result into a slice. Returns (nil, nil) when there are none.
func (h *Handler) collectDiagnostics(file string) ([]lspclient.Diagnostic, error) {
	text, isErr, err := h.handleDiagnostics(map[string]any{"file": file, "format": "json"})
	if err != nil {
		return nil, err
	}
	if isErr {
		return nil, fmt.Errorf("diagnostics: %s", text)
	}
	if text == "" || text == "[]" {
		return nil, nil
	}
	var diags []lspclient.Diagnostic
	if uerr := json.Unmarshal([]byte(text), &diags); uerr != nil {
		return nil, fmt.Errorf("parsing diagnostics: %w", uerr)
	}
	return diags, nil
}

// diagnosticSignature is the cycle-detection key for a single diagnostic.
// Two diagnostics with the same file+code+message are considered the same
// for cycle purposes.
func diagnosticSignature(d lspclient.Diagnostic) string {
	return fmt.Sprintf("%s\x00%s\x00%s", d.File, d.Code, d.Message)
}

// matchFix returns the first prepared fix whose diagnostic regex matches
// the diagnostic message, along with the named captures bound from the
// match. Match order is the order returned by ListFixes (alphabetical by
// name), so behaviour is deterministic.
func matchFix(fixes []preparedFix, d lspclient.Diagnostic) (preparedFix, map[string]string, bool) {
	for _, pf := range fixes {
		m := pf.re.FindStringSubmatch(d.Message)
		if m == nil {
			continue
		}
		captures := make(map[string]string)
		for i, name := range pf.re.SubexpNames() {
			if name == "" || i >= len(m) {
				continue
			}
			captures[name] = m[i]
		}
		// Built-in captures so actions can reference the diagnostic location.
		captures["__file"] = d.File
		captures["__code"] = d.Code
		return pf, captures, true
	}
	return preparedFix{}, nil, false
}

// applyFixAction interprets a fix entry's action JSON, substitutes captures,
// dispatches to handleInstantiate or handleTransform, then calls handleApply
// to write the changes to disk.
func (h *Handler) applyFixAction(entry store.Fix, captures map[string]string) error {
	substituted := substituteCaptures(entry.ActionJSON, captures)

	var action map[string]any
	if err := json.Unmarshal([]byte(substituted), &action); err != nil {
		return fmt.Errorf("parsing substituted action: %w", err)
	}

	switch {
	case action["recipe"] != nil:
		recipeName, _ := action["recipe"].(string)
		paramsJSON := "{}"
		if p, ok := action["params"]; ok {
			b, _ := json.Marshal(p)
			paramsJSON = string(b)
		}
		text, isErr, err := h.handleInstantiate(map[string]any{
			"recipe": recipeName,
			"params": paramsJSON,
		})
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("instantiate: %s", text)
		}

	case action["transform"] != nil:
		spec, ok := action["transform"].(map[string]any)
		if !ok {
			return fmt.Errorf("action.transform must be an object")
		}
		text, isErr, err := h.handleTransform(spec)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("transform: %s", text)
		}

	default:
		return fmt.Errorf("action must contain recipe or transform")
	}

	// Apply pending changes immediately so the next iteration's diagnostics
	// reflect the fix.
	text, isErr, err := h.handleApply(map[string]any{"confirm": true})
	if err != nil {
		return err
	}
	if isErr {
		return fmt.Errorf("apply: %s", text)
	}
	if strings.HasPrefix(text, "No pending changes") {
		// The action ran but produced no edits — treat as a no-op so the
		// cycle detector flags it on the next iteration if the diagnostic
		// remains.
		return fmt.Errorf("action produced no changes")
	}
	return nil
}

// substituteCaptures replaces ${name} placeholders in s with their values
// from captures. Unknown references are left as-is (validated at teach_fix
// time, so this should not happen in practice).
func substituteCaptures(s string, captures map[string]string) string {
	return fixCaptureRefRe.ReplaceAllStringFunc(s, func(match string) string {
		// match is "${name}"; strip ${ and }.
		name := match[2 : len(match)-1]
		if v, ok := captures[name]; ok {
			// Escape for JSON string contexts. ReplaceAllStringFunc operates
			// on the raw text, so we need to JSON-encode the replacement
			// minus the surrounding quotes.
			b, _ := json.Marshal(v)
			s := string(b)
			return s[1 : len(s)-1]
		}
		return match
	})
}
