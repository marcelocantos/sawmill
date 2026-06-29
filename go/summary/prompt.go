// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package summary drives LLM-distilled one-line summaries and typed
// knowledge-graph edge extraction for code symbols, routed through
// github.com/marcelocantos/claudia. The prompt is versioned so that any
// change to the wording forces a controlled re-summarisation; old rows
// remain in the database until the new prompt actually overwrites them,
// which lets callers see the per-symbol cost spent on each generation.
package summary

// PromptID identifies one wording of the summariser prompt. Bumping this
// constant forces every symbol to be re-summarised on the next pass. The
// id is persisted alongside each result so callers (and the index_status
// tool) can tell at a glance which summaries are stale.
const PromptID = "v1"

// systemPrompt instructs the LLM to act as a code summariser with NO tool
// use — we don't want claudia's Read/Bash/etc. firing for every symbol,
// since each tool call costs another round-trip. The snippet is included
// inline so the LLM has everything it needs in one shot.
const systemPrompt = `You are a code summariser. You will receive ONE source-code declaration. Output a SINGLE JSON object with this exact shape:

{
  "summary": "one sentence (max ~140 chars) describing what this declaration does",
  "edges": [
    {"kind": "reads"|"writes"|"calls"|"throws"|"returns", "dst": "symbol name", "confidence": 0.0-1.0}
  ]
}

Rules:
- Output JSON only. No prose, no markdown fences, no explanations before or after.
- "summary" must be present and non-empty.
- "edges" may be empty.
- "dst" is the BARE identifier (e.g. "Open", "ReadFile") — not a file path or qualified name.
- "kind" is one of: reads (reads from this symbol/state), writes (writes to it), calls (invokes it), throws (raises/returns this error), returns (constructs and returns this type).
- Do not invoke any tools. Everything you need is in the prompt.`

// userPromptFor builds the user message for one symbol. It deliberately
// keeps the formatting tight: one block, one declaration, one JSON
// response.
func userPromptFor(filePath, name, kind, signature, body string) string {
	const maxBodyBytes = 2048
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes]
	}
	return systemPrompt + "\n\n---\n" +
		"file: " + filePath + "\n" +
		"kind: " + kind + "\n" +
		"name: " + name + "\n" +
		"signature: " + signature + "\n" +
		"body:\n" + body + "\n"
}
