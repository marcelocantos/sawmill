// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/marcelocantos/sawmill/store"
)

// builtinConcepts is the small seed dictionary shipped with Sawmill. It is
// intentionally narrow: v1 expects users to teach project-specific concepts
// via teach_concept. Built-ins cover four high-frequency themes drawn from
// the grep-audit corpus.
//
// Stored concepts shadow built-ins of the same name (the user-defined entry
// wins on conflict).
var builtinConcepts = []store.Concept{
	{
		Name:        "swipe",
		Description: "Touch gesture recognition (swipe, pan, pinch, drag, fling)",
		Aliases: []string{
			"swipe", "gesture", "fling", "pan", "drag", "pinch", "scroll",
			"tap", "longpress", "doubletap", "touch", "pointer",
			"uiswipegesturerecognizer", "uipangesturerecognizer",
			"uitapgesturerecognizer", "uilongpressgesturerecognizer",
			"gesturedetector", "pangesturehandler", "tapgesturehandler",
			"scalegesturedetector", "hammer",
		},
	},
	{
		Name:        "retry",
		Description: "Retry with backoff / transient-failure handling",
		Aliases: []string{
			"retry", "retries", "backoff", "exponential", "jitter",
			"attempt", "attempts", "transient", "retryable",
			"retrypolicy", "retrytemplate", "withretry",
		},
	},
	{
		Name:        "auth",
		Description: "Authentication / authorization / credential handling",
		Aliases: []string{
			"auth", "authn", "authz", "authenticate", "authorize",
			"authorization", "credential", "credentials", "token",
			"bearer", "jwt", "oauth", "oauth2", "openid", "login",
			"logout", "signin", "signout", "signup", "session",
		},
	},
	{
		Name:        "logging",
		Description: "Logging / structured logging frameworks",
		Aliases: []string{
			"log", "logger", "logging", "slog", "logf", "logln",
			"logrus", "zap", "zerolog", "log4j", "log4cpp", "spdlog",
			"debugf", "infof", "warnf", "errorf",
		},
	},
}

// builtinConceptIndex maps name → built-in entry for O(1) lookup.
var builtinConceptIndex = func() map[string]store.Concept {
	m := make(map[string]store.Concept, len(builtinConcepts))
	for _, c := range builtinConcepts {
		m[c.Name] = c
	}
	return m
}()

// expandQuery turns a free-text concept query into the lowercased alias set
// used by store.FindByConcept. Each whitespace-separated word is checked
// against the stored concept dictionary, then the built-in dictionary; both
// the concept name and its aliases are added to the set when a match
// occurs. Unrecognised words are added as their own aliases so the query
// always anchors something.
func expandQuery(query string, stored []store.Concept) []string {
	storedIndex := make(map[string]store.Concept, len(stored))
	for _, c := range stored {
		storedIndex[strings.ToLower(c.Name)] = c
	}

	seen := make(map[string]struct{})
	add := func(a string) {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			return
		}
		seen[a] = struct{}{}
	}

	for _, word := range strings.Fields(query) {
		w := strings.ToLower(strings.Trim(word, ".,;:!?\"'`"))
		if w == "" {
			continue
		}
		add(w)
		if c, ok := storedIndex[w]; ok {
			for _, a := range c.Aliases {
				add(a)
			}
			continue
		}
		if c, ok := builtinConceptIndex[w]; ok {
			for _, a := range c.Aliases {
				add(a)
			}
		}
	}

	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// handleTeachConcept persists a concept entry. aliases is a JSON array of
// strings; empty/whitespace entries are dropped.
func (h *Handler) handleTeachConcept(args map[string]any) (string, bool, error) {
	name, err := requireString(args, "name")
	if err != nil {
		return err.Error(), true, nil
	}
	description := optString(args, "description")
	aliasesArg := optString(args, "aliases")
	if aliasesArg == "" {
		return "aliases is required (JSON array of strings)", true, nil
	}
	var aliases []string
	if err := json.Unmarshal([]byte(aliasesArg), &aliases); err != nil {
		return fmt.Sprintf("decoding aliases JSON: %v", err), true, nil
	}
	if len(aliases) == 0 {
		return "aliases must contain at least one entry", true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	if err := m.SaveConcept(name, description, aliases); err != nil {
		return fmt.Sprintf("saving concept: %v", err), true, nil
	}
	return fmt.Sprintf("Concept %q saved with %d alias(es).", name, len(aliases)), false, nil
}

// handleListConcepts lists stored concepts plus built-ins not shadowed by a
// stored entry.
func (h *Handler) handleListConcepts(_ map[string]any) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	stored, err := m.ListConcepts()
	if err != nil {
		return fmt.Sprintf("listing concepts: %v", err), true, nil
	}

	storedNames := make(map[string]struct{}, len(stored))
	for _, c := range stored {
		storedNames[strings.ToLower(c.Name)] = struct{}{}
	}

	var sb strings.Builder
	if len(stored) > 0 {
		fmt.Fprintf(&sb, "%d taught concept(s):\n", len(stored))
		for _, c := range stored {
			writeConceptLine(&sb, c)
		}
	} else {
		sb.WriteString("No taught concepts yet.\n")
	}

	// Surface built-ins that aren't shadowed so the user knows what's
	// available out of the box.
	var unshadowed []store.Concept
	for _, c := range builtinConcepts {
		if _, ok := storedNames[strings.ToLower(c.Name)]; !ok {
			unshadowed = append(unshadowed, c)
		}
	}
	if len(unshadowed) > 0 {
		fmt.Fprintf(&sb, "\n%d built-in concept(s):\n", len(unshadowed))
		for _, c := range unshadowed {
			writeConceptLine(&sb, c)
		}
	}
	return sb.String(), false, nil
}

func writeConceptLine(sb *strings.Builder, c store.Concept) {
	fmt.Fprintf(sb, "  %s", c.Name)
	if c.Description != "" {
		fmt.Fprintf(sb, " — %s", c.Description)
	}
	fmt.Fprintf(sb, "\n    aliases: %s\n", strings.Join(c.Aliases, ", "))
}

// handleDeleteConcept removes a stored concept. Built-ins cannot be deleted;
// to mask one, teach a same-named concept with the desired aliases.
func (h *Handler) handleDeleteConcept(args map[string]any) (string, bool, error) {
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

	deleted, err := m.DeleteConcept(name)
	if err != nil {
		return fmt.Sprintf("deleting concept: %v", err), true, nil
	}
	if !deleted {
		if _, isBuiltin := builtinConceptIndex[strings.ToLower(name)]; isBuiltin {
			return fmt.Sprintf("Concept %q is a built-in and cannot be deleted. Teach a same-named concept with different aliases to override it.", name), false, nil
		}
		return fmt.Sprintf("Concept %q not found.", name), false, nil
	}
	return fmt.Sprintf("Concept %q deleted.", name), false, nil
}

// handleFindByConcept runs a concept search. The query is expanded via the
// stored + built-in dictionaries; each word maps to a concept's aliases (or
// to itself if unrecognised). Results are ranked by alias hits, with name
// matches weighted more than body matches.
func (h *Handler) handleFindByConcept(args map[string]any) (string, bool, error) {
	query, err := requireString(args, "query")
	if err != nil {
		return err.Error(), true, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "query must be non-empty", true, nil
	}
	limit := optInt(args, "limit")
	if limit <= 0 {
		limit = 20
	}
	scopeArg := optString(args, "scope")
	scopes := parseScopeFilter(scopeArg)
	format := optString(args, "format")

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	stored, err := m.ListConcepts()
	if err != nil {
		return fmt.Sprintf("loading concepts: %v", err), true, nil
	}

	aliases := expandQuery(query, stored)
	if len(aliases) == 0 {
		return "Query produced no aliases after normalisation.", false, nil
	}

	matches, err := m.FindByConcept(aliases, scopes, limit)
	if err != nil {
		return fmt.Sprintf("find_by_concept: %v", err), true, nil
	}

	if format == "json" {
		return renderConceptMatchesJSON(query, aliases, matches), false, nil
	}
	return renderConceptMatchesText(query, aliases, matches), false, nil
}

// parseScopeFilter maps a "scope" tool argument to a list of scope names
// passed to the store. "" or "all" means no filter. "owned" (default
// recommendation) restricts to project-local code.
func parseScopeFilter(s string) []string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "owned":
		return []string{"owned"}
	case "all":
		return nil
	case "owned+library", "library+owned":
		return []string{"owned", "library"}
	case "library":
		return []string{"library"}
	default:
		// Fall back to owned for any unrecognised value rather than
		// surfacing a noisy error — concept search is a discovery tool.
		return []string{"owned"}
	}
}

func renderConceptMatchesText(query string, aliases []string, matches []store.ConceptMatch) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "concept query %q expanded to %d alias(es): %s\n",
		query, len(aliases), strings.Join(aliases, ", "))
	if len(matches) == 0 {
		sb.WriteString("\nNo matching symbols.\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "\n%d match(es):\n", len(matches))
	for _, m := range matches {
		fmt.Fprintf(&sb, "  %s:%d  [%s] %s  (score %d",
			m.Symbol.FilePath, m.Symbol.StartLine, m.Symbol.Kind, m.Symbol.Name, m.Score)
		if len(m.NameHitAliases) > 0 {
			fmt.Fprintf(&sb, ", name: %s", strings.Join(m.NameHitAliases, ","))
		}
		bodyOnly := subtractAliases(m.MatchedAliases, m.NameHitAliases)
		if len(bodyOnly) > 0 {
			fmt.Fprintf(&sb, ", body: %s", strings.Join(bodyOnly, ","))
		}
		sb.WriteString(")\n")
	}
	return sb.String()
}

// subtractAliases returns the elements of all not present in subset.
func subtractAliases(all, subset []string) []string {
	if len(subset) == 0 {
		return all
	}
	subIdx := make(map[string]struct{}, len(subset))
	for _, s := range subset {
		subIdx[s] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, a := range all {
		if _, ok := subIdx[a]; !ok {
			out = append(out, a)
		}
	}
	return out
}

func renderConceptMatchesJSON(query string, aliases []string, matches []store.ConceptMatch) string {
	type matchJSON struct {
		File           string   `json:"file"`
		Line           int      `json:"line"`
		Column         int      `json:"column"`
		Kind           string   `json:"kind"`
		Name           string   `json:"name"`
		Score          int      `json:"score"`
		MatchedAliases []string `json:"matched_aliases"`
		NameHitAliases []string `json:"name_hit_aliases"`
	}
	type response struct {
		Query   string      `json:"query"`
		Aliases []string    `json:"aliases"`
		Matches []matchJSON `json:"matches"`
	}
	out := response{Query: query, Aliases: aliases, Matches: make([]matchJSON, 0, len(matches))}
	for _, m := range matches {
		out.Matches = append(out.Matches, matchJSON{
			File:           m.Symbol.FilePath,
			Line:           m.Symbol.StartLine,
			Column:         m.Symbol.StartCol,
			Kind:           m.Symbol.Kind,
			Name:           m.Symbol.Name,
			Score:          m.Score,
			MatchedAliases: m.MatchedAliases,
			NameHitAliases: m.NameHitAliases,
		})
	}
	bytes, _ := json.MarshalIndent(out, "", "  ")
	return string(bytes)
}
