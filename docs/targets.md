# Targets

<!-- last-evaluated: a841e45 -->

## Active

### 🎯T12 Intra-language pattern equivalences

Bidirectional equivalences between code patterns within a single
language. A single declaration derives forward/backward refactoring,
convention enforcement, and migration planning via transitive chains.

See `docs/papers/equivalences.md` for the research paper.

- **Weight**: 1 (value 21 / cost 21)
- **Estimated-cost**: 21
- **Acceptance**:
  - `teach_equivalence` tool stores bidirectional pattern pairs
  - `apply_equivalence` rewrites matches in either direction
  - `check_equivalences` flags non-preferred forms as violations
  - Transitive chains produce derived equivalences
- **Context**: Originates from arr.ai work on cross-language transpilation
  as set relations. The intra-language case avoids type-bridge and
  grammar-extension problems. T16's pattern engine provides the foundation.
- **Status**: identified
- **Discovered**: 2026-04-06

### 🎯T20 Model manager is an active process

The per-project CodebaseModel is managed by a goroutine (actor) that
owns all mutable state. MCP handlers interact with it via channels,
not direct field access.

- **Weight**: 2 (value 13 / cost 8)
- **Estimated-cost**: 8
- **Acceptance**:
  - Model manager goroutine owns the forest, store, and symbol index
  - Watcher goroutine feeds file events to the model manager (not to
    a channel that nobody drains)
  - On startup, the manager reconciles filesystem state against the
    (potentially stale) SQLite database before accepting queries
  - MCP handlers send requests to the manager via channels and receive
    responses — no direct access to forest or store
  - After apply writes files, the manager observes the watcher events
    and re-parses automatically (no manual Sync call needed)
  - Multiple concurrent MCP sessions on the same root are safe by
    construction — the manager serialises all state access
  - Test exists: two sessions do independent transforms and applies;
    both see a consistent, up-to-date model throughout
- **Context**: The current CodebaseModel is a passive struct with no
  concurrency control. The watcher produces events on a channel that
  is only drained by an explicit Sync() call (inside handleParse).
  After apply, the model is stale until someone calls parse again.
  Multiple handlers sharing the model have unsynchronised access to
  the forest and store. The fix is not a mutex — it's making the
  model an active subsystem (actor pattern) that owns its state and
  serves queries through a channel-based protocol.
- **Status**: achieved
- **Discovered**: 2026-04-07

### 🎯T21 Diagnostic-driven automatic fixes

Sawmill can ingest compiler/linter diagnostics, match them against a
catalogue of fixes, and apply corrections automatically. The catalogue
is populated two ways: pre-built entries for common compiler errors
(scraped from error code catalogues), and learned entries observed from
the agent's own fix behaviour during sessions.

- **Weight**: 1 (value 13 / cost 13)
- **Estimated-cost**: 13
- **Acceptance**:
  - `teach_fix` tool associates a diagnostic pattern with a fix action
    (inline transform or recipe reference) with parameter extraction
    from regex captures; stored in SQLite
  - `auto_fix` tool runs diagnostics (via LSP or raw compiler output),
    matches against the fix catalogue, applies safe fixes, reports
    uncertain ones; convergence loop re-runs diagnostics after each
    apply, terminates when clean, stuck, or iteration limit reached
  - Pre-populated catalogue covers common Go and TypeScript errors
    (unused import, missing return, type mismatch, unreachable code)
    out of the box — no cold start for bread-and-butter cases
  - Observation-based learning: when `auto_fix` reports unmatched
    diagnostics and a subsequent sawmill operation resolves them,
    sawmill offers to save the diagnostic→fix pairing as a new
    catalogue entry
  - Per-compiler diagnostic normalisation for at least Go and TypeScript;
    Rust and Python as follow-on
  - Each fix entry has a confidence annotation (auto-apply vs. suggest)
  - Cycle detection: if a diagnostic reappears after its fix was applied,
    skip it and flag the fix as broken
- **Context**: The pieces exist — recipes (stored transforms), LSP
  diagnostics tool, pattern engine from T16. Two bootstrapping paths:
  pre-populated entries from compiler error catalogues for common cases,
  and learn-from-observation for project-specific patterns. The agent
  already fixes errors manually — sawmill just needs to watch and remember.
- **Status**: identified
- **Discovered**: 2026-04-07

## Achieved

### 🎯T1–T10 Early milestones
- **Status**: achieved
- **Context**: Phases 2–6, rich ctx API, teach by example, convention
  invariants, LSP on ctx, structural pre-flight checks, agent prompt
  generation. All completed during the Rust era.

### 🎯T11 Rewrite in Go + daemon architecture
- **Weight**: 21 (value 21 / cost 13)
- **Estimated-cost**: 13
- **Status**: achieved
- **Context**: Full rewrite from Rust to Go with daemon architecture.
  7 sub-targets (T11.1–T11.7) all achieved.

### 🎯T13 LSP client integration
- **Weight**: 13 (value 21 / cost 8)
- **Estimated-cost**: 8
- **Status**: achieved
- **Context**: `go/lspclient/` package. hover/definition/references/diagnostics
  MCP tools. Codegen ctx LSP methods. Graceful degradation without LSP binary.

### 🎯T14 File rename with import cascade
- **Weight**: 5 (value 8 / cost 3)
- **Estimated-cost**: 3
- **Status**: achieved
- **Context**: `rename_file` tool with import path updates across 5 languages.

### 🎯T15 Add field + propagate to construction sites
- **Weight**: 8 (value 21 / cost 8)
- **Estimated-cost**: 8
- **Status**: achieved
- **Context**: `add_field` tool with struct literal and factory propagation.

### 🎯T16 Type shape migration
- **Weight**: 5 (value 21 / cost 13)
- **Estimated-cost**: 13
- **Status**: achieved
- **Context**: `migrate_type` tool with pattern matching engine for
  construction rewriting, field/method access rewriting, and type renaming.

### 🎯T17 Dependency impact analysis
- **Weight**: 3 (value 8 / cost 3)
- **Estimated-cost**: 3
- **Status**: achieved
- **Context**: `dependency_usage` tool with heuristic qualified-access matching.

### 🎯T18 Clone-and-adapt
- **Weight**: 4 (value 8 / cost 2)
- **Estimated-cost**: 2
- **Status**: achieved
- **Context**: `clone_and_adapt` tool with symbol/range extraction.

### 🎯T19 Structural invariants
- **Weight**: 5 (value 13 / cost 5)
- **Estimated-cost**: 5
- **Status**: achieved
- **Context**: `teach_invariant`, `check_invariants`, `list_invariants`,
  `delete_invariant` tools with JSON rule language.
