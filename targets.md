# Targets

## Active

### 🎯T1 Intra-language pattern equivalences
- **Weight**: 1 (value 21 / cost 21)
- **Estimated-cost**: 21
- **Acceptance**:
  - teach_equivalence tool stores bidirectional pattern pairs
  - apply_equivalence rewrites matches in either direction
  - check_equivalences flags non-preferred forms as violations
  - Transitive chains produce derived equivalences
- **Context**: Originates from arr.ai work on cross-language transpilation as set relations. The intra-language case avoids type-bridge and grammar-extension problems. T16's pattern engine provides the foundation.
- **Tags**: research, pattern-matching
- **Status**: Identified
- **Discovered**: 2026-04-07

### 🎯T3 Diagnostic-driven automatic fixes
- **Weight**: 1 (value 13 / cost 13)
- **Estimated-cost**: 13
- **Acceptance**:
  - teach_fix tool associates a diagnostic pattern with a fix action (inline transform or recipe reference) with parameter extraction from regex captures; stored in SQLite
  - auto_fix tool runs diagnostics, matches against catalogue, applies safe fixes, reports uncertain ones; convergence loop terminates when clean, stuck, or iteration limit reached
  - Pre-populated catalogue covers common Go and TypeScript errors out of the box
  - Observation-based learning: when auto_fix reports unmatched diagnostics and a subsequent operation resolves them, sawmill offers to save the pairing
  - Per-compiler diagnostic normalisation for at least Go and TypeScript
  - Each fix entry has a confidence annotation (auto-apply vs. suggest)
  - Cycle detection: if a diagnostic reappears after its fix was applied, skip it and flag the fix as broken
- **Context**: Two bootstrapping paths: pre-populated entries from compiler error catalogues for common cases, and learn-from-observation for project-specific patterns. The agent already fixes errors manually — sawmill just needs to watch and remember.
- **Tags**: diagnostics, automation
- **Status**: Identified
- **Discovered**: 2026-04-07

### 🎯T5 Sawmill supports coordinated transforms across multiple repositories
- **Weight**: 1 (value 8 / cost 13)
- **Estimated-cost**: 13
- **Acceptance**:
  - Transforms can target multiple project roots in a single operation
  - Daemon manages models for multiple repos concurrently (already partially true)
  - Cross-repo batch operations produce per-repo diffs/previews
  - PR lifecycle support: create branches and PRs across target repos
- **Context**: Sawmill's architecture (global daemon, per-project models, MCP interface) is naturally extensible to multi-repo workflows. This would cover the same ground as Sourcegraph Batch Changes but with native AST-level transformation intelligence built in, rather than BYO container scripts. The daemon already manages multiple project roots — the gap is orchestration: repo discovery, coordinated cross-repo transforms, and PR lifecycle management.
- **Tags**: multi-repo, orchestration, feature
- **Origin**: Discussion comparing sawmill to Sourcegraph — identified cross-repo as a natural extension
- **Status**: Identified
- **Discovered**: 2026-04-10

## Achieved

### 🎯T4 CST node tree is stored in SQLite — in-memory CSTs are transient parse artifacts only
- **Weight**: 1 (value 13 / cost 20)
- **Estimated-cost**: 20
- **Acceptance**:
  - Nodes table stores full tree structure (type, field, parent, byte ranges) for all parsed files
  - All structural queries (find_references, query, pattern matching) run against SQLite, not in-memory CSTs
  - In-memory CSTs exist only during parse of a single file, then are discarded
  - Daemon memory stays bounded regardless of project size — scales with SQLite page cache, not file count
  - Cold start is instant — no full-project reparse needed, nodes persist across restarts
- **Context**: Tree-sitter CSTs (10-100x source size) were held in memory for every file indefinitely, causing the daemon to consume multiple GB on large projects. The fix is to serialize the full node tree into SQLite after parsing, then discard the in-memory CST. Structural queries become SQL joins against the nodes table — which is actually more powerful than S-expression queries (cross-file joins, aggregation, set operations). Tree-sitter is still used for parsing, but SQLite becomes the query engine.
- **Tags**: performance, daemon, memory
- **Origin**: User report — daemon killed due to memory pressure
- **Status**: Achieved
- **Discovered**: 2026-04-09
- **Achieved**: 2026-04-10

### 🎯T2 Model manager is an active process
- **Weight**: 2 (value 13 / cost 8)
- **Estimated-cost**: 8
- **Acceptance**:
  - Model manager goroutine owns the forest, store, and symbol index
  - Watcher goroutine feeds file events to the model manager
  - On startup, manager reconciles filesystem state against stale SQLite database before accepting queries
  - MCP handlers send requests to the manager via channels — no direct access to forest or store
  - After apply writes files, the manager observes watcher events and re-parses automatically
  - Multiple concurrent MCP sessions on the same root are safe by construction
  - Test exists: two sessions do independent transforms and applies with consistent model
- **Context**: The current CodebaseModel is a passive struct with no concurrency control. The watcher produces events on a channel only drained by explicit Sync(). After apply, the model is stale. Multiple handlers sharing the model have unsynchronised access. The fix is making the model an active subsystem (actor pattern) that owns its state and serves queries through channels.
- **Tags**: daemon, concurrency, architecture
- **Status**: Achieved
- **Discovered**: 2026-04-07
- **Achieved**: 2026-04-08

## Graph

```mermaid
graph TD
    T1["Intra-language pattern equiva…"]
    T3["Diagnostic-driven automatic f…"]
    T5["Sawmill supports coordinated …"]
```
