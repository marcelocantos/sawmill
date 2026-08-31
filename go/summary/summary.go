// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcelocantos/claudia"
)

// Edge is one LLM-extracted knowledge-graph edge.
type Edge struct {
	Kind       string  `json:"kind"`
	Dst        string  `json:"dst"`
	Confidence float64 `json:"confidence"`
}

// Result is the parsed payload of one summarisation, plus the accounting
// claudia returns alongside its final result event.
type Result struct {
	Summary  string
	Edges    []Edge
	ModelID  string  // claude model id reported by claudia, when available
	PromptID string  // PromptID constant
	CostUSD  float64 // billed cost for this summarisation
	Tokens   int     // total tokens (input + output)
	Duration time.Duration
}

// Request is one input to Summarise.
type Request struct {
	ID        string // unique task id (e.g. "sawmill-summary-{symbolID}-{ts}")
	WorkDir   string // claudia requires a working directory; pass the project root
	Model     string // empty -> claudia's default ("" passes through to claude CLI)
	FilePath  string
	Name      string
	Kind      string
	Signature string
	Body      string
}

// errParse signals that the LLM returned text that wasn't valid JSON in the
// expected shape. Caller may choose to retry with a tighter prompt; we just
// record the failure.
type errParse struct{ raw string }

func (e *errParse) Error() string {
	preview := e.raw
	if len(preview) > 120 {
		preview = preview[:120] + "…"
	}
	return fmt.Sprintf("summariser parse error: %q", preview)
}

// Summarise runs one summarisation through claudia.NewTask and returns the
// parsed result. The caller is responsible for cost-capping; we just report
// what claudia tells us.
func Summarise(ctx context.Context, req Request) (Result, error) {
	task := claudia.NewTask(claudia.TaskConfig{
		ID:      req.ID,
		WorkDir: req.WorkDir,
		Model:   req.Model,
	})
	prompt := userPromptFor(req.FilePath, req.Name, req.Kind, req.Signature, req.Body)
	events, err := task.Run(ctx, prompt)
	if err != nil {
		return Result{}, fmt.Errorf("claudia.Run: %w", err)
	}

	var (
		finalText string
		cost      float64
		tokens    int
		dur       time.Duration
	)
	for ev := range events {
		switch ev.Type {
		case claudia.TaskEventText:
			// Accumulate streamed text — we'll look at the final result event
			// for the canonical content.
		case claudia.TaskEventResult:
			finalText = ev.Content
			cost = ev.CostUSD
			tokens = int(ev.Usage.InputTokens + ev.Usage.OutputTokens)
			dur = time.Duration(ev.DurationMs) * time.Millisecond
		case claudia.TaskEventError:
			return Result{}, fmt.Errorf("claudia task error: %s", ev.ErrorMsg)
		}
	}

	res := Result{
		PromptID: PromptID,
		CostUSD:  cost,
		Tokens:   tokens,
		Duration: dur,
	}

	// Find the JSON object in the response. Claude sometimes wraps it in a
	// code fence even when told not to, so we tolerate that.
	jsonText, ok := extractJSONObject(finalText)
	if !ok {
		return res, &errParse{raw: finalText}
	}
	var parsed struct {
		Summary string `json:"summary"`
		Edges   []Edge `json:"edges"`
	}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return res, &errParse{raw: finalText}
	}
	if parsed.Summary == "" {
		return res, &errParse{raw: finalText}
	}
	res.Summary = parsed.Summary
	res.Edges = parsed.Edges
	// Normalise edges.
	for i := range res.Edges {
		res.Edges[i].Kind = strings.ToLower(strings.TrimSpace(res.Edges[i].Kind))
		res.Edges[i].Dst = strings.TrimSpace(res.Edges[i].Dst)
	}
	return res, nil
}

// extractJSONObject finds the first balanced { ... } block in s and returns
// its contents. Tolerates markdown code fences and leading prose.
func extractJSONObject(s string) (string, bool) {
	// Strip code fences if present.
	if i := strings.Index(s, "```"); i >= 0 {
		// Skip past the opening fence and any "json" language tag.
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			s = rest[:end]
		} else {
			s = rest
		}
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		case '"':
			// Skip past string content (including escapes).
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				i++
			}
		}
	}
	return "", false
}
