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

## 🎯T11 Rewrite in Go + daemon architecture — ACTIVE

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

- 🎯T11.1 Core logic port — ACHIEVED. All 7 packages ported: adapters,
  forest, rewrite, transform, index, exemplar, codegen/jsengine.
  29 Go tests passing. Uses modernc.org/quickjs (pure Go) for JS engine.
- 🎯T11.2 Store port — SQLite persistence layer (files, symbols,
  recipes, conventions). Same schema, Go bindings.
- 🎯T11.3 Daemon architecture — `sawmill daemon` listens on Unix socket
  (`~/.sawmill/sawmill.sock`), holds map of project roots to
  CodebaseModels, handles concurrent MCP connections via goroutines.
- 🎯T11.4 MCP server — all tool definitions reimplemented against the
  daemon's shared state. Multi-project aware (parse scopes a connection
  to a project root).
- 🎯T11.5 Stdio proxy — `sawmill serve` connects to daemon socket,
  relays MCP JSON-RPC over stdio for MCP client compatibility. Errors
  helpfully if daemon isn't running.
- 🎯T11.6 Brew services — launchd plist for `brew services start sawmill`.
  Daemon logs to `~/Library/Logs/sawmill/`.
- 🎯T11.7 Feature parity — all 35 existing tests pass in Go. All MCP
  tools work. agents-guide, --help-agent, --version all present. Release
  workflow updated for Go cross-compilation.
