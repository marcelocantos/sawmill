# Sawmill

MCP server for AST-level multi-language code transformations. Built in Go
with Tree-sitter, QuickJS, and SQLite (all pure Go, no CGo).

## Build & test

```bash
cd go
go build ./...                    # Build all packages
go build ./cmd/sawmill            # Build the CLI binary
go test ./... -count=1            # Run all tests (80 tests)
make build                        # Build binary to bin/sawmill (from repo root)
make test                         # Run all tests (from repo root)
```

Requires Go 1.26+.

## Project layout

```
go/
  cmd/sawmill/main.go — CLI entry point (daemon, serve, version)
  mcp/              — MCP server (20 tools)
    server.go       — SawmillServer, tool registration, Serve/ServeConn
    tools.go        — All tool handler implementations
    helpers.go      — Batch transform helpers, parameter editing
  model/model.go    — CodebaseModel (persistent state, incremental parsing)
  daemon/daemon.go  — Unix socket daemon, multi-project model management
  proxy/proxy.go    — Stdio-to-socket proxy for MCP clients
  forest/forest.go  — Forest/ParsedFile, apply_with_backup, undo
  store/store.go    — SQLite store (files, symbols, recipes, conventions)
  adapters/         — Language adapters (Python, Rust, TS, Go, C++)
  transform/        — Match/act engine
  rewrite/          — Range-based source rewriting, AST rename, diff
  codegen/          — JavaScript code generation runtime (ctx API)
  jsengine/         — QuickJS-based per-node JS transforms
  exemplar/         — Teach-by-example template extraction
  index/            — Symbol extraction from Tree-sitter trees
  watcher/          — File system watcher with debouncing (fsnotify)
homebrew/
  sawmill.rb        — Homebrew formula with brew services support
  io.sawmill.daemon.plist — Standalone launchd plist
docs/
  design.md         — Architecture and design rationale
  targets.md        — Convergence targets
agents-guide.md     — Reference for AI coding agents (embedded in binary)
```

## Architecture

Sawmill runs as a persistent daemon (`sawmill daemon`) managing multiple
projects. `sawmill serve` is a thin stdio-to-socket proxy for MCP clients.

The daemon holds a pool of `CodebaseModel` instances (one per project root).
Each model ties together:
- A `Forest` of `ParsedFile`s (Tree-sitter CSTs + original source bytes)
- A `Store` (SQLite: file metadata, symbol index, recipes, conventions)
- A `Watcher` for live incremental updates (fsnotify with debouncing)

All transforms produce diff previews. Changes are only written on explicit
`apply` (with backup files for `undo`).

## Key conventions

- All MCP tools are registered in `mcp/server.go` via mcp-go's AddTool
- Language adapters implement the `LanguageAdapter` interface in `adapters/`
- JavaScript code generation receives a `ctx` object with `fields()`,
  `methods()`, `addField()`, `addMethod()`, `addImport()`, etc.
- Pure Go dependencies throughout: modernc.org/quickjs, modernc.org/sqlite,
  fsnotify, go-tree-sitter

## Delivery

Merged to master.

## Gates

profile: base
