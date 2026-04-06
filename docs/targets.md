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
- **Status**: converging

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

All 35 existing tests pass in Go. All MCP tools work. agents-guide,
--help-agent, --version all present. Release workflow updated for Go
cross-compilation.

- **Parent**: 🎯T11
- **Weight**: 1 (value 5 / cost 5)
- **Status**: not started
- **Depends on**: 🎯T11.1, 🎯T11.4
