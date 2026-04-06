# Convergence Report

**Evaluated**: 2026-04-07
**Branch**: master
**SHA**: 5385a77

## Standing invariants

- Go tests: **PASSING** (all packages, 0 failures)
- CI: **GREEN** (last 3 runs successful)
- No open PRs

## Movement

- 🎯T13: designed -> **achieved** (LSP client package, 4 MCP tools, 17 tests)
- 🎯T14: designed -> **achieved** (rename_file tool with import cascade, 5 tests)
- 🎯T18: designed -> **achieved** (clone_and_adapt tool, 6 tests)
- 🎯T11: (unchanged) -- achieved
- 🎯T15: (unchanged) -- designed, now unblocked (T13 achieved)
- 🎯T16: (unchanged) -- designed, still blocked by T15
- 🎯T17: (unchanged) -- designed, now unblocked (T13 achieved)
- 🎯T19: (unchanged) -- designed, now unblocked (T13 achieved)
- 🎯T12: (unchanged) -- research

## Gap report

### 🎯T15 Add field + propagate to construction sites  [weight 2.6]
Gap: **not started**
Design exists in docs/agent-usage-archaeology.md. No code written -- `add_field` tool not registered in mcp/server.go. Now unblocked: T13 (LSP client) is achieved, enabling the type-aware mode. Syntactic mode could be implemented independently.

### 🎯T17 Dependency impact analysis  [weight 2.7]
Gap: **not started**
Design exists. No code written -- `dependency_usage` tool not registered. Now unblocked: T13 achieved. Heuristic mode could be implemented first, LSP precision layered on.

### 🎯T19 Structural invariants  [weight 2.6]
Gap: **not started**
Design exists. No code written -- `teach_invariant`, `check_invariants`, `list_invariants`, `delete_invariant` tools not registered. Now unblocked: T13 achieved. Basic invariants (without `implementing` clauses) could be implemented first.

### 🎯T16 Type shape migration  [weight 1.6]
Gap: **not started** (blocked by 🎯T15)
Design exists. Depends on T15 (add_field) for shared infrastructure. Also shares pattern language with T12. Cannot proceed until T15 is achieved.

### 🎯T12 Intra-language pattern equivalences  [weight 1.0]
Gap: **not started** (research stage)
Paper written (docs/papers/equivalences.md). No design or implementation. Weight 1.0 (value/cost ratio) suggests cost matches value -- lowest priority among active targets.

### Achieved (delivered)

- [x] 🎯T11 Rewrite in Go + daemon architecture (7/7 sub-targets)
- [x] 🎯T13 LSP client integration -- 4 MCP tools, ctx.* wiring, graceful degradation
- [x] 🎯T14 File rename with import cascade -- 5-language support
- [x] 🎯T18 Clone-and-adapt -- symbol/range extraction, substitution, insertion

## Recommendation

Work on: **🎯T15 Add field + propagate to construction sites**

Reason: Highest effective weight (2.6) among unblocked non-achieved targets, tied with T19 but T15 gates T16 (unlocking further work). T17 has slightly higher weight (2.7) but T15's gating relationship gives it higher strategic value -- achieving T15 unblocks T16 and moves the dependency graph forward. The design already exists in docs/agent-usage-archaeology.md, and the syntactic mode can be implemented immediately using tree-sitter alone.

## Suggested action

Read `docs/agent-usage-archaeology.md` section 4.3 for the T15 implementation design, then implement the `add_field` MCP tool in `go/mcp/tools.go` starting with the syntactic (tree-sitter-only) mode: struct/class field insertion, constructor propagation via `New<Type>` naming heuristic, and struct literal propagation. Add tests in `go/mcp/tools_test.go`. The type-aware LSP mode can be layered on afterward.

<!-- convergence-deps
evaluated: 2026-04-07T00:00:00Z
sha: 5385a77

🎯T11:
  gap: achieved
  assessment: "All 7/7 sub-targets achieved. Delivered to master."
  read:
    - docs/targets.md

🎯T13:
  gap: achieved
  assessment: "LSP client package with raw JSON-RPC 2.0, Pool, Client. 4 MCP tools. 17 tests passing. Delivered."
  read:
    - go/lspclient/jsonrpc.go
    - go/lspclient/lspclient.go
    - go/lspclient/pool.go
    - go/lspclient/lspclient_test.go
    - go/mcp/server.go
    - go/mcp/tools.go

🎯T14:
  gap: achieved
  assessment: "rename_file tool with import cascade for 5 languages. 5 tests passing. Delivered."
  read:
    - go/mcp/tools.go
    - go/mcp/tools_test.go

🎯T15:
  gap: not started
  assessment: "Design exists in agent-usage-archaeology.md. No code written. add_field not in mcp/server.go."
  read:
    - go/mcp/server.go
    - go/mcp/tools.go

🎯T16:
  gap: not started
  assessment: "Design exists. Blocked by T15. No code written."
  read:
    - go/mcp/tools.go

🎯T17:
  gap: not started
  assessment: "Design exists. No code written. dependency_usage not in mcp/server.go."
  read:
    - go/mcp/tools.go

🎯T18:
  gap: achieved
  assessment: "clone_and_adapt tool implemented. 6 tests passing. Delivered."
  read:
    - go/mcp/tools.go

🎯T19:
  gap: not started
  assessment: "Design exists. No code written. teach_invariant not in mcp/server.go."
  read:
    - go/mcp/tools.go

🎯T12:
  gap: not started
  assessment: "Research paper written. No design or implementation."
  read:
    - docs/papers/equivalences.md
-->
