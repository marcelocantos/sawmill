// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/marcelocantos/sawmill/store"
)

// fixCaptureRefRe finds ${name} placeholders in a fix action JSON. Used to
// verify every reference resolves to a named capture in the diagnostic regex.
var fixCaptureRefRe = regexp.MustCompile(`\$\{([a-zA-Z_]\w*)\}`)

// validateFixCaptures compiles the diagnostic regex, scans the action JSON
// for ${name} references, and returns an error if any reference doesn't
// resolve to a named capture group.
func validateFixCaptures(diagnosticRegex, actionJSON string) error {
	re, err := regexp.Compile(diagnosticRegex)
	if err != nil {
		return fmt.Errorf("compiling diagnostic_regex: %w", err)
	}
	captureSet := make(map[string]bool)
	for _, name := range re.SubexpNames() {
		if name != "" {
			captureSet[name] = true
		}
	}
	refs := fixCaptureRefRe.FindAllStringSubmatch(actionJSON, -1)
	var missing []string
	seen := make(map[string]bool)
	for _, m := range refs {
		ref := m[1]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if !captureSet[ref] {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		var available []string
		for name := range captureSet {
			available = append(available, name)
		}
		sort.Strings(available)
		availMsg := "(none)"
		if len(available) > 0 {
			availMsg = strings.Join(available, ", ")
		}
		return fmt.Errorf("fix action references unknown captures %v; available named captures: %s",
			missing, availMsg)
	}
	return nil
}

func (h *Handler) handleTeachFix(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	diagnosticRegex, err := requireString(args, "diagnostic_regex")
	if err != nil {
		return err.Error(), true, nil
	}
	actionStr, err := requireString(args, "action")
	if err != nil {
		return err.Error(), true, nil
	}
	confidence := optString(args, "confidence")
	description := optString(args, "description")

	// The action must parse as a JSON object.
	var actionObj map[string]any
	if err := json.Unmarshal([]byte(actionStr), &actionObj); err != nil {
		return fmt.Sprintf("action is not valid JSON: %v", err), true, nil
	}
	// And it must specify either a recipe reference or an inline transform.
	hasRecipe := actionObj["recipe"] != nil
	hasTransform := actionObj["transform"] != nil
	if !hasRecipe && !hasTransform {
		return "action must contain either a \"recipe\" key or a \"transform\" key", true, nil
	}
	if hasRecipe && hasTransform {
		return "action must contain exactly one of \"recipe\" or \"transform\", not both", true, nil
	}

	if err := validateFixCaptures(diagnosticRegex, actionStr); err != nil {
		return err.Error(), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveFix(name, description, diagnosticRegex, actionStr, confidence); err != nil {
		return fmt.Sprintf("saving fix: %v", err), true, nil
	}

	resolvedConf := confidence
	if resolvedConf == "" {
		resolvedConf = store.FixConfidenceSuggest
	}
	return fmt.Sprintf("Fix %q saved (confidence: %s).", name, resolvedConf), false, nil
}

func (h *Handler) handleListFixes(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	fixes, err := m.ListFixes()
	if err != nil {
		return fmt.Sprintf("listing fixes: %v", err), true, nil
	}
	if len(fixes) == 0 {
		return "No fixes saved.", false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d fix(es):\n", len(fixes))
	for _, f := range fixes {
		fmt.Fprintf(&sb, "  %s [%s]\n", f.Name, f.Confidence)
		fmt.Fprintf(&sb, "    regex:  %s\n", f.DiagnosticRegex)
		fmt.Fprintf(&sb, "    action: %s\n", summariseFixAction(f.ActionJSON))
		if f.Description != "" {
			fmt.Fprintf(&sb, "    note:   %s\n", f.Description)
		}
	}
	return sb.String(), false, nil
}

func (h *Handler) handleDeleteFix(args map[string]any) (string, bool, error) {
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

	deleted, err := m.DeleteFix(name)
	if err != nil {
		return fmt.Sprintf("deleting fix: %v", err), true, nil
	}
	if !deleted {
		return fmt.Sprintf("No fix named %q.", name), false, nil
	}
	return fmt.Sprintf("Fix %q deleted.", name), false, nil
}

// summariseFixAction reduces an action JSON to a one-line summary for list output.
func summariseFixAction(actionJSON string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(actionJSON), &obj); err != nil {
		return actionJSON
	}
	if recipe, ok := obj["recipe"].(string); ok {
		summary := "recipe:" + recipe
		if params, ok := obj["params"].(map[string]any); ok && len(params) > 0 {
			var keys []string
			for k := range params {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			summary += " (params: " + strings.Join(keys, ", ") + ")"
		}
		return summary
	}
	if t, ok := obj["transform"].(map[string]any); ok {
		var summary string
		if action, ok := t["action"].(string); ok {
			summary = "transform:" + action
		} else {
			summary = "transform"
		}
		return summary
	}
	return actionJSON
}
