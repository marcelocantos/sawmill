# Convergence Targets

## 🎯T1–T5 Phases 2–6 — ACHIEVED
## 🎯T6 Frontier A: Rich ctx API — ACHIEVED
## 🎯T7 Frontier B: Teach by example — ACHIEVED
## 🎯T8 Frontier C: Convention invariants — ACHIEVED
## 🎯T9 Frontier D & E — ACHIEVED

- 🎯T9.1 LSP on ctx — ctx.typeOf, ctx.definition, ctx.lspReferences,
  ctx.diagnostics, ctx.hasLsp
- 🎯T9.2 Structural pre-flight checks — dangling references,
  removed symbols still referenced

## 🎯T10 Frontier K: Agent prompt generation — ACHIEVED

- 🎯T10.1 Static instructions — agents-guide served as MCP instructions
- 🎯T10.2 Dynamic prompt tool — `get_agent_prompt` returns guide +
  project-specific recipes and conventions

## Active

### 🎯T11 Rewrite in Go + daemon architecture

Sawmill runs as a single persistent daemon (`sawmill daemon`) managing
multiple projects, with `sawmill serve` as a thin stdio-to-socket proxy
for MCP clients. Rewrite from Rust to Go simultaneously with the
architecture change to avoid doing the hard design work twice.

**Why Go**: Sawmill is I/O-bound concurrent glue — goroutines, channels,
and Go's simpler concurrency model are a natural fit. Tree-sitter,
QuickJS, and SQLite all have working Go bindings. Cross-compilation is
trivial.

**Why daemon**: Eliminates concurrent-writer SQLite collisions, shares
state (recipes, conventions) across sessions, and enables brew services
integration.

- **Weight**: 21 (value 21 / cost 13)
- **Status**: achieved

#### 🎯T11.1 Core logic port

All 7 packages ported: adapters, forest, rewrite, transform, index,
exemplar, codegen/jsengine. 29 Go tests passing. Uses modernc.org/quickjs
(pure Go) for JS engine.

- **Parent**: 🎯T11
- **Weight**: 8 (value 8 / cost 5)
- **Status**: achieved
- **Gates**: 🎯T11.2, 🎯T11.4, 🎯T11.7

#### 🎯T11.2 Store port

SQLite persistence layer using modernc.org/sqlite (pure Go). Same schema
as Rust: files, symbols, recipes, conventions tables. 7 tests passing.

- **Parent**: 🎯T11
- **Weight**: 2.7 (value 8 / cost 3)
- **Status**: achieved
- **Depends on**: 🎯T11.1
- **Gates**: 🎯T11.3, 🎯T11.4

#### 🎯T11.3 Daemon architecture

`sawmill daemon` listens on Unix socket (`~/.sawmill/sawmill.sock`),
holds map of project roots to CodebaseModels, handles concurrent MCP
connections via goroutines.

- **Parent**: 🎯T11
- **Weight**: 1.6 (value 8 / cost 5)
- **Status**: achieved (daemon package + CLI entry point)
- **Depends on**: 🎯T11.2
- **Gates**: 🎯T11.5, 🎯T11.6

#### 🎯T11.4 MCP server

All tool definitions reimplemented against the daemon's shared state.
Multi-project aware (parse scopes a connection to a project root).

- **Parent**: 🎯T11
- **Weight**: 1.6 (value 8 / cost 5)
- **Status**: achieved — go/mcp/ package with all 20 tools; `sawmill serve`
  now proxies to daemon (🎯T11.5) when running, falls back to in-process.
- **Depends on**: 🎯T11.2
- **Gates**: 🎯T11.5, 🎯T11.7

#### 🎯T11.5 Stdio proxy

`sawmill serve` connects to daemon socket, relays MCP JSON-RPC over
stdio for MCP client compatibility. Errors helpfully if daemon isn't
running.

- **Parent**: 🎯T11
- **Weight**: 2.7 (value 5 / cost 2)
- **Status**: achieved — go/proxy/proxy.go with Run(); `sawmill serve`
  uses proxy mode when daemon is running, falls back to in-process with
  a warning otherwise. Tested in go/proxy/proxy_test.go.
- **Depends on**: 🎯T11.3, 🎯T11.4

#### 🎯T11.6 Brew services

launchd plist for `brew services start sawmill`. Daemon logs to
`~/Library/Logs/sawmill/`.

- **Parent**: 🎯T11
- **Weight**: 2.5 (value 5 / cost 2)
- **Status**: achieved — `homebrew/sawmill.rb` formula with `service` block;
  `homebrew/io.sawmill.daemon.plist` for non-Homebrew users; ldflags version
  injection in CLI; `--help-agent` flag added.
- **Depends on**: 🎯T11.3

#### 🎯T11.7 Feature parity

All 35 Rust tests ported to Go (46 Go tests passing). Rewrite package tests
added (`go/rewrite/rewrite_test.go`). Go CI workflow created
(`.github/workflows/go.yml`).

- **Parent**: 🎯T11
- **Weight**: 1 (value 5 / cost 5)
- **Status**: achieved
- **Depends on**: 🎯T11.1, 🎯T11.4

## Active

### 🎯T13 LSP client integration

Sawmill can query language servers (gopls, rust-analyzer, pyright,
clangd, tsserver) for type information, go-to-definition, find
references, and diagnostics. The adapter interface already declares
`LSPCommand()` and `LSPLanguageID()` for all five languages. This
target implements the client that launches, manages, and queries them.

New package `go/lspclient/` with `Client` (single server process) and
`Pool` (per-language-per-root management). Uses `go.lsp.dev/jsonrpc2` +
`go.lsp.dev/protocol` (pure Go, typed LSP 3.17 client). Integrates into
`CodebaseModel` as a `*lspclient.Pool`. Wires `ctx.typeOf`,
`ctx.definition`, `ctx.lspReferences`, `ctx.diagnostics` in the codegen
QuickJS runtime. Implements the four MCP tools already documented in the
agents-guide (`hover`, `definition`, `lsp_references`, `diagnostics`).
Degrades gracefully when the language server binary is not installed.

See `docs/agent-usage-archaeology.md` §4.1 for full implementation design.

- **Weight**: 13 (value 21 / cost 8)
- **Status**: designed — adapter plumbing exists, ctx stubs exist, need client implementation
- **Gates**: 🎯T15, 🎯T16, 🎯T17, 🎯T19

### 🎯T14 File rename with import cascade

`rename_file` MCP tool — renames a file on disk and updates all
import/include/require paths that reference it. Each language adapter
gains a `ResolveImportPath(importText, importingFile, root)` method
that maps import strings to filesystem paths.

Does not require LSP — uses `adapter.ImportQuery()` + the new resolver.

See `docs/agent-usage-archaeology.md` §4.2 for full implementation design.

- **Weight**: 5 (value 8 / cost 3)
- **Status**: designed
- **Depends on**: (independent)

### 🎯T15 Add field + propagate to construction sites

`add_field` MCP tool — adds a field to a struct/class, then propagates
to constructors, factory functions, struct literals, and their callers.
Two modes: syntactic (tree-sitter only, heuristic factory detection via
`New<Type>` naming) and type-aware (LSP references on the type
definition, hover on return types).

See `docs/agent-usage-archaeology.md` §4.3 for full implementation design.

- **Weight**: 8 (value 21 / cost 8)
- **Status**: designed
- **Depends on**: 🎯T13 (for type-aware mode; syntactic mode works without)
- **Gates**: 🎯T16

### 🎯T16 Type shape migration

`migrate_type` MCP tool — given a type name and a set of rewrite rules
(construction patterns, field access mappings), rewrites all usage sites.
Requires a mini pattern language for matching tree-sitter subtrees with
named holes — shares infrastructure with 🎯T12 (pattern equivalences).

See `docs/agent-usage-archaeology.md` §4.4 for full implementation design.

- **Weight**: 5 (value 21 / cost 13)
- **Status**: designed
- **Depends on**: 🎯T13, 🎯T15

### 🎯T17 Dependency impact analysis

`dependency_usage` MCP tool — given a package/module import path,
reports all import sites, symbols used, call sites, and public API
exposure. With LSP: resolves each identifier via `textDocument/definition`
to confirm it originates from the target package. Without LSP: heuristic
qualified-access matching.

See `docs/agent-usage-archaeology.md` §4.5 for full implementation design.

- **Weight**: 3.3 (value 8 / cost 3)
- **Status**: designed
- **Depends on**: 🎯T13 (for precision; heuristic mode works without)

### 🎯T18 Clone-and-adapt

`clone_and_adapt` MCP tool — copies a symbol or code region, applies
string substitutions, and inserts the result at a target location.
Handles import propagation. Intentionally simpler than `teach_by_example`
— one-shot copy-and-modify, no templatisation or recipe storage.

See `docs/agent-usage-archaeology.md` §4.6 for full implementation design.

- **Weight**: 4 (value 8 / cost 2)
- **Status**: designed
- **Depends on**: (independent)

### 🎯T19 Structural invariants

`teach_invariant` MCP tool — structured assertion language for
relational invariants between code elements (e.g. "every type
implementing interface X must have field Y"). Stored in SQLite alongside
recipes and conventions. Companion tools: `check_invariants`,
`list_invariants`, `delete_invariant`. `implementing` clauses require LSP
for interface satisfaction; degrade to syntactic heuristics without.

See `docs/agent-usage-archaeology.md` §4.7 for full implementation design.

- **Weight**: 5 (value 13 / cost 5)
- **Status**: designed
- **Depends on**: 🎯T13 (for interface checks; basic invariants work without)

## Future

### 🎯T12 Intra-language pattern equivalences

Declare bidirectional equivalences between code patterns within a single
language. A declaration like:

```
//python{logging.getLogger(${name}).${level}(${msg})}
<=>
//python{log.${level}(${msg}, logger=${name})}
```

states that two patterns are semantically equivalent. From this, the tool
derives bidirectional refactoring, convention enforcement with automatic
fixes, and migration planning via transitive equivalence chains.

Originates from Marcelo Cantos's arr.ai work on cross-language
transpilation as set relations. The intra-language case is a tractable
extraction that avoids the type-bridge and grammar-extension problems of
the general case. See `docs/papers/equivalences.md` for the research paper.

- **Weight**: not yet estimated
- **Status**: research — paper written, not yet designed or planned
