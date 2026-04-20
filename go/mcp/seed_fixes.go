// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"strings"

	"github.com/marcelocantos/sawmill/store"
)

// seedFix is one entry in the starter fix catalogue installed by seed_fixes.
// Each entry's action is JSON validated by the same path as teach_fix.
type seedFix struct {
	Name            string
	Description     string
	DiagnosticRegex string
	ActionJSON      string
	Confidence      string
}

// seedFixes is the curated starter catalogue. confidence=auto entries are
// known-safe inline transforms; confidence=suggest entries describe a
// recommended fix without actually applying it (the action JSON is
// well-formed but downstream consumers should treat it as guidance until
// the user upgrades it via teach_fix).
var seedFixes = []seedFix{
	// --- Go --------------------------------------------------------------

	{
		Name:            "go-unused-import",
		Description:     "Remove a Go import gopls flagged as unused",
		DiagnosticRegex: `imported and not used: "(?P<pkg>[^"]+)"`,
		ActionJSON:      `{"transform":{"kind":"import","name":"${pkg}","action":"remove"}}`,
		Confidence:      store.FixConfidenceAuto,
	},
	{
		Name:            "go-missing-field",
		Description:     "Suggested fix for a struct literal missing a field — needs human judgement on the field's value",
		DiagnosticRegex: `unknown field (?P<field>\w+) in struct literal of type (?P<type>\S+)`,
		ActionJSON:      `{"recipe":"add-struct-field","params":{"type_name":"${type}","field_name":"${field}"}}`,
		Confidence:      store.FixConfidenceSuggest,
	},
	{
		Name:            "go-return-type-mismatch",
		Description:     "Suggested fix for a return-type mismatch — needs human judgement on the conversion",
		DiagnosticRegex: `cannot use (?P<expr>.+?) \(.+?\) as (?P<want>\S+) value in return statement`,
		ActionJSON:      `{"recipe":"convert-return","params":{"expr":"${expr}","target_type":"${want}"}}`,
		Confidence:      store.FixConfidenceSuggest,
	},

	// --- TypeScript ------------------------------------------------------

	{
		Name:            "ts-cannot-find-name",
		Description:     "TS2304: cannot find name — suggest adding the missing import",
		DiagnosticRegex: `Cannot find name '(?P<name>[^']+)'`,
		ActionJSON:      `{"recipe":"add-import","params":{"name":"${name}"}}`,
		Confidence:      store.FixConfidenceSuggest,
	},
	{
		Name:            "ts-unused-declaration",
		Description:     "TS6133: declared but never read — drop the unused variable/import",
		DiagnosticRegex: `'(?P<name>[^']+)' is declared but its value is never read`,
		ActionJSON:      `{"transform":{"kind":"call","name":"${name}","action":"remove"}}`,
		Confidence:      store.FixConfidenceAuto,
	},
	{
		Name:            "ts-possibly-undefined",
		Description:     "TS2532: object is possibly undefined — suggest a null check (needs human judgement on shape)",
		DiagnosticRegex: `Object is possibly '(?:undefined|null)'\.?`,
		ActionJSON:      `{"recipe":"add-null-check","params":{"placeholder":"true"}}`,
		Confidence:      store.FixConfidenceSuggest,
	},
}

// handleSeedFixes installs the starter catalogue. Existing entries with the
// same name are left untouched so users who customised them aren't clobbered.
func (h *Handler) handleSeedFixes(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	var installed, skipped []string
	for _, sf := range seedFixes {
		// Defensive: validate the regex+captures match (matches what teach_fix
		// would do at user save time).
		if err := validateFixCaptures(sf.DiagnosticRegex, sf.ActionJSON); err != nil {
			return fmt.Sprintf("seed entry %q invalid: %v", sf.Name, err), true, nil
		}
		existing, err := m.LoadFix(sf.Name)
		if err != nil {
			return fmt.Sprintf("loading existing fix %q: %v", sf.Name, err), true, nil
		}
		if existing != nil {
			skipped = append(skipped, sf.Name)
			continue
		}
		if err := m.SaveFix(sf.Name, sf.Description, sf.DiagnosticRegex, sf.ActionJSON, sf.Confidence); err != nil {
			return fmt.Sprintf("saving seed fix %q: %v", sf.Name, err), true, nil
		}
		installed = append(installed, sf.Name)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Seeded %d fix(es); kept %d existing entry(s).\n", len(installed), len(skipped))
	if len(installed) > 0 {
		sb.WriteString("\nInstalled:\n")
		for _, n := range installed {
			fmt.Fprintf(&sb, "  + %s\n", n)
		}
	}
	if len(skipped) > 0 {
		sb.WriteString("\nKept existing (use delete_fix first to overwrite):\n")
		for _, n := range skipped {
			fmt.Fprintf(&sb, "  · %s\n", n)
		}
	}
	return sb.String(), false, nil
}
