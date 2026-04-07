# Convergence Report

**Evaluated**: 2026-04-07
**Branch**: master
**SHA**: 3feb133

## Standing invariants

- Go tests: **PASSING** (all packages, 0 failures)
- CI: **GREEN** (last 3 runs successful)
- No open PRs

## Movement

- 🎯T15: not started -> **achieved** (add_field tool for 5 languages, 6 tests)
- 🎯T16: blocked -> **unblocked** (T13+T15 both achieved)
- 🎯T17: (unchanged) -- designed, unblocked
- 🎯T19: (unchanged) -- designed, unblocked
- 🎯T11: (unchanged) -- achieved
- 🎯T13: (unchanged) -- achieved
- 🎯T14: (unchanged) -- achieved
- 🎯T18: (unchanged) -- achieved
- 🎯T12: (unchanged) -- research

## Gap report

### 🎯T17 Dependency impact analysis  [weight 3.3]
Gap: **not started**
Design exists in docs/agent-usage-archaeology.md. No code written -- `dependency_usage` tool not registered in mcp/server.go. Unblocked: T13 achieved. Heuristic mode (qualified-access matching) can be implemented first, LSP precision layered on.

### 🎯T19 Structural invariants  [weight 2.6]
Gap: **not started**
Design exists. No code written -- `teach_invariant`, `check_invariants`, `list_invariants`, `delete_invariant` tools not registered. Unblocked: T13 achieved. Basic invariants (without `implementing` clauses) can be implemented first.

### 🎯T16 Type shape migration  [weight 1.6]
Gap: **not started** (newly unblocked)
Design exists. `migrate_type` tool not registered. Both dependencies achieved (T13 LSP client, T15 add_field). Shares pattern language with T12.

### 🎯T12 Intra-language pattern equivalences  [weight 1.0]
Gap: **not started** (research stage, future)
Paper written (docs/papers/equivalences.md). No design or implementation. Weight 1.0 suggests cost matches value -- lowest priority.

### Achieved (delivered)

- [x] 🎯T11 Rewrite in Go + daemon architecture (7/7 sub-targets)
- [x] 🎯T13 LSP client integration -- 4 MCP tools, ctx.* wiring, graceful degradation
- [x] 🎯T14 File rename with import cascade -- 5-language support
- [x] 🎯T15 Add field + propagate to construction sites -- 5-language support, 6 tests
- [x] 🎯T18 Clone-and-adapt -- symbol/range extraction, substitution, insertion

## Recommendation

Work on: **🎯T17 Dependency impact analysis**

Reason: Highest effective weight (3.3) among unblocked non-achieved targets. Independent of T19 and T16, so achieving it doesn't gate other work, but its high weight-to-cost ratio (value 8 / cost 3) makes it the cheapest high-value target. The design already exists, and the heuristic mode can be implemented using tree-sitter alone without LSP.

## Suggested action

Read `docs/agent-usage-archaeology.md` section 4.5 for the T17 implementation design, then implement the `dependency_usage` MCP tool in `go/mcp/tools.go`. Start with the heuristic mode: scan all files for import statements matching the target package path (using `adapter.ImportQuery()`), extract qualified symbol accesses from those files, and report import sites, symbols used, and public API exposure. Add tests in `go/mcp/tools_test.go`. The LSP precision mode (resolving identifiers via `textDocument/definition`) can be layered on afterward.

<!-- convergence-deps
evaluated: 2026-04-07T00:07:29Z
sha: 3feb133

🎯T11:
  gap: achieved
  assessment: "All 7/7 sub-targets achieved. Delivered to master."
  read:
    - docs/targets.md

🎯T13:
  gap: achieved
  assessment: "LSP client package with raw JSON-RPC 2.0, Pool, Client. 4 MCP tools. 17 tests passing. Delivered."
  read:
    - go/mcp/server.go

🎯T14:
  gap: achieved
  assessment: "rename_file tool with import cascade for 5 languages. 5 tests passing. Delivered."
  read:
    - go/mcp/server.go

🎯T15:
  gap: achieved
  assessment: "add_field tool implemented for Go, Python, Rust, TypeScript, C++. 6 tests passing. Delivered."
  read:
    - go/mcp/server.go
    - go/mcp/tools.go

🎯T16:
  gap: not started
  assessment: "Design exists. Now unblocked (T13+T15 achieved). No code written."
  read:
    - go/mcp/server.go

🎯T17:
  gap: not started
  assessment: "Design exists. Unblocked. No code written. dependency_usage not in mcp/server.go."
  read:
    - go/mcp/server.go

🎯T18:
  gap: achieved
  assessment: "clone_and_adapt tool implemented. 6 tests passing. Delivered."
  read:
    - go/mcp/server.go

🎯T19:
  gap: not started
  assessment: "Design exists. Unblocked. No code written. teach_invariant not in mcp/server.go."
  read:
    - go/mcp/server.go

🎯T12:
  gap: not started
  assessment: "Research paper written. No design or implementation. Future."
  read:
    - docs/papers/equivalences.md
-->
