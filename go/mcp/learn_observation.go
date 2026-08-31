// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/marcelocantos/sawmill/lspclient"
)

// LearnedFixCandidate is one suggested fix-catalogue entry inferred from a
// pre/post observation. Returned by learn_from_observation; the user may
// promote it to a permanent entry by passing the values into teach_fix.
type LearnedFixCandidate struct {
	// SuggestedName is a draft name the user can keep or rename — built from
	// the diagnostic code (if present) and a slugified prefix of the message.
	SuggestedName string `json:"suggested_name"`

	// DiagnosticRegex is a generalised regex that matches the original
	// message: quoted substrings become named captures (arg1, arg2, ...).
	DiagnosticRegex string `json:"diagnostic_regex"`

	// CandidateAction is a stub action JSON the user is expected to flesh
	// out before calling teach_fix. Defaults to a recipe placeholder so the
	// JSON validates.
	CandidateAction string `json:"action"`

	// Confidence on candidates is always "suggest" — promotion to "auto" is
	// the user's call.
	Confidence string `json:"confidence"`

	// Source records the diagnostic that resolved between the two snapshots.
	Source lspclient.Diagnostic `json:"source_diagnostic"`

	// Notes is a human-readable hint about what the user should fill in.
	Notes string `json:"notes,omitempty"`
}

// handleLearnFromObservation infers candidate fix entries from a pre/post
// diagnostic comparison. Diagnostics present in pre_diagnostics but absent
// from post_diagnostics are treated as "resolved by the user's recent
// edits"; each one yields a candidate the user can refine and save.
func (h *Handler) handleLearnFromObservation(args map[string]any) (string, bool, error) {
	preStr, err := requireString(args, "pre_diagnostics")
	if err != nil {
		return err.Error(), true, nil
	}
	postStr := optString(args, "post_diagnostics")
	if postStr == "" {
		postStr = "[]"
	}

	pre, err := unmarshalDiagnostics(preStr, "pre_diagnostics")
	if err != nil {
		return err.Error(), true, nil
	}
	post, err := unmarshalDiagnostics(postStr, "post_diagnostics")
	if err != nil {
		return err.Error(), true, nil
	}

	postSig := make(map[string]bool, len(post))
	for _, d := range post {
		postSig[diagnosticSignature(d)] = true
	}

	var resolved []lspclient.Diagnostic
	for _, d := range pre {
		if !postSig[diagnosticSignature(d)] {
			resolved = append(resolved, d)
		}
	}

	candidates := make([]LearnedFixCandidate, 0, len(resolved))
	seenNames := make(map[string]int)
	for _, d := range resolved {
		regex := generaliseDiagnosticMessage(d.Message)
		// Validate by compiling — protects users from regexes the helper
		// produced that need manual cleanup before teach_fix accepts them.
		if _, err := regexp.Compile(regex); err != nil {
			continue
		}
		base := suggestedFixName(d)
		// Disambiguate identical names by suffixing -2, -3, ...
		name := base
		seenNames[base]++
		if seenNames[base] > 1 {
			name = fmt.Sprintf("%s-%d", base, seenNames[base])
		}
		candidates = append(candidates, LearnedFixCandidate{
			SuggestedName:   name,
			DiagnosticRegex: regex,
			CandidateAction: `{"recipe":"<rename-me>","params":{}}`,
			Confidence:      "suggest",
			Source:          d,
			Notes:           "Edit the action JSON to reference your recipe or inline transform, then call teach_fix to make this permanent.",
		})
	}

	out, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling candidates: %v", err), true, nil
	}
	return string(out), false, nil
}

func unmarshalDiagnostics(s, paramName string) ([]lspclient.Diagnostic, error) {
	var diags []lspclient.Diagnostic
	if err := json.Unmarshal([]byte(s), &diags); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", paramName, err)
	}
	return diags, nil
}

// quotedRe matches double-quoted, single-quoted, and backtick-quoted runs.
// Used to extract the variable parts of a diagnostic message that should
// become regex captures.
var quotedRe = regexp.MustCompile("\"[^\"]*\"|'[^']*'|`[^`]*`")

// generaliseDiagnosticMessage turns a concrete diagnostic message into a
// regex that matches similar messages with different identifiers. Quoted
// runs are replaced with named captures (?P<argN>[^X]+) where X matches the
// quote character; the rest of the text is regex-escaped.
//
// Example:
//
//	imported and not used: "fmt"
//	→ ^imported and not used: "(?P<arg1>[^"]+)"$
func generaliseDiagnosticMessage(msg string) string {
	var sb strings.Builder
	sb.WriteByte('^')

	idx := 0
	argN := 1
	for _, loc := range quotedRe.FindAllStringIndex(msg, -1) {
		// Literal portion before the quoted run.
		sb.WriteString(regexp.QuoteMeta(msg[idx:loc[0]]))
		quote := msg[loc[0] : loc[0]+1]
		// Inner content character class excludes the quote character itself
		// so the capture stops at the closing quote.
		sb.WriteString(quote)
		fmt.Fprintf(&sb, "(?P<arg%d>[^%s]+)", argN, regexp.QuoteMeta(quote))
		sb.WriteString(quote)
		argN++
		idx = loc[1]
	}
	sb.WriteString(regexp.QuoteMeta(msg[idx:]))
	sb.WriteByte('$')
	return sb.String()
}

// suggestedFixName builds a draft fix-entry name from a diagnostic. Uses
// the LSP code if present, otherwise a slug from the start of the message.
func suggestedFixName(d lspclient.Diagnostic) string {
	if d.Code != "" {
		// Lower-case and strip non-identifier chars so it's a clean slug.
		return "learned-" + slugify(d.Code)
	}
	return "learned-" + slugify(firstWords(d.Message, 4))
}

func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func slugify(s string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && sb.Len() > 0 {
				sb.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := sb.String()
	out = strings.TrimRight(out, "-")
	if out == "" {
		out = "diagnostic"
	}
	return out
}
