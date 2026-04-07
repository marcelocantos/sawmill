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
do not corrupt each other's state or the shared model.

- **Weight**: 3 (value 8 / cost 3)
- **Estimated-cost**: 3
- **Acceptance**:
  - Pending changes and backups are per-connection (already true — each
    HandlerFactory call creates a new Handler)
  - Shared CodebaseModel has a sync.RWMutex; reads (query, find_symbol,
    transform preview) take read locks, writes (Sync, applyEvent) take
    write locks
  - Apply triggers a model Sync so the forest reflects written files
    before the next read
  - SQLite store is safe under concurrent access (WAL mode, single writer)
  - Test exists: two sessions do independent renames and applies without
    corrupting each other's results or the shared model
- **Context**: Handler-level isolation is already correct (factory creates
  a fresh Handler per connection). The gap is the shared CodebaseModel:
  Forest reads and watcher-driven updates have no synchronisation, and
  apply doesn't re-sync the model. Two sessions reading/writing the
  forest concurrently can race. SQLite WAL mode handles store-level
  concurrency but the in-memory forest needs an RWMutex.
- **Status**: identified
- **Discovered**: 2026-04-07

### 🎯T21 Diagnostic-driven automatic fixes

Sawmill can ingest compiler/linter diagnostics, match them against a
catalogue of learned fixes, and apply corrections automatically. Safe
fixes are applied in a loop until the build is clean or no more
catalogue matches exist. Uncertain fixes are reported for human review.

- **Weight**: 2 (value 13 / cost 8)
- **Estimated-cost**: 8
- **Acceptance**:
  - `teach_fix` tool associates a diagnostic regex pattern with a recipe
    and parameter extraction rules; stored in SQLite
  - `auto_fix` tool runs diagnostics (via LSP or raw compiler output),
    matches against the fix catalogue, applies safe fixes, and reports
    uncertain ones
  - Fix loop re-runs diagnostics after each apply; terminates when clean,
    stuck (no new fixes matched), or iteration limit reached
  - Per-compiler normalisation handles at least Go, Rust, Python, and
    TypeScript diagnostic formats
  - Each fix entry has a confidence annotation (auto-apply vs. suggest)
- **Context**: The pieces exist — recipes (stored transforms), LSP
  diagnostics tool, pattern engine from T16. What's missing is the glue:
  diagnostic pattern → recipe binding, and the apply-recheck convergence
  loop. This closes the loop between "compiler says something is wrong"
  and "sawmill fixes it automatically."
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
