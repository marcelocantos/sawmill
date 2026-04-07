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

### 🎯T20 Concurrent sessions on the same repo are safe

Multiple MCP sessions connected to the same project root via the daemon
do not corrupt each other's pending changes, backups, or model state.

- **Weight**: 3 (value 8 / cost 3)
- **Estimated-cost**: 3
- **Acceptance**:
  - Each daemon connection gets independent pending-changes and backup state
  - A rename on session A does not appear as pending on session B
  - Applying on session A does not clobber session B's pending changes
  - Test exists exercising two concurrent sessions with independent transforms
- **Context**: Currently, the daemon creates a per-connection Handler via
  HandlerFactory, but all handlers for the same root share a single Handler
  instance (including pending state). Two Claude Code sessions in the same
  repo will race on pending changes. The model (parsed ASTs, symbol index)
  should remain shared; only session state needs isolation.
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
