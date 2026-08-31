// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

// Violation is the structured payload emitted by sawmill's verification
// tools (check_conventions, check_invariants) when a check fails. It is
// designed for programmatic consumption by orchestrators (e.g. bullseye's
// rework flow) that need machine-readable diagnoses rather than prose.
//
// All check tools accept a format parameter ("text" or "json"); the prose
// rendering — which existing human-facing callers depend on — is unchanged
// and remains the default.
type Violation struct {
	// Source identifies the check family and the specific rule, e.g.
	// "convention:no-bare-except", "invariant:config-has-name".
	Source string `json:"source"`

	// File is the path (relative or absolute, as the underlying check
	// returned it) of the file containing the violation. Required.
	File string `json:"file"`

	// Line is 1-based. Zero means "no specific line" (a file-scoped
	// violation), in which case it is omitted from JSON output.
	Line int `json:"line,omitempty"`

	// Column is 1-based. Zero means "no specific column".
	Column int `json:"column,omitempty"`

	// Severity is "error" or "warning". Defaults to "error".
	Severity string `json:"severity"`

	// Rule is a short stable identifier the consumer can group by, e.g.
	// the convention or invariant name (without the "convention:" prefix).
	Rule string `json:"rule"`

	// Message is human-readable text describing the violation.
	Message string `json:"message"`

	// Snippet is an optional source excerpt around the violation site.
	Snippet string `json:"snippet,omitempty"`

	// SuggestedFix is an optional rewrite the orchestrator can apply.
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

// QueryMatch is the structured shape for a single query result, returned
// when handleQuery is called with format="json".
type QueryMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Snippet string `json:"snippet"`
}
