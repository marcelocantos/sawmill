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
  cmd/sawmill/main.go — CLI entry point (serve, version)
  mcp/              — MCP server (49 tools)
    server.go       — Per-session Handler + tool registration with mcp-go
    tools.go        — All tool handler implementations
    helpers.go      — Batch transform helpers, parameter editing
  model/model.go    — CodebaseModel (persistent state, incremental parsing)
  modelpool/        — Ref-counted shared CodebaseModel pool keyed by project root
  daemon/daemon.go  — HTTP MCP server (mcp-go streamable HTTP transport)
  forest/forest.go  — Forest/ParsedFile, apply_with_backup, undo
  store/store.go    — SQLite store (files, symbols, recipes, conventions)
  adapters/         — Language adapters (Python, Rust, TS, Go, C++)
  transform/        — Match/act engine
  rewrite/          — Range-based source rewriting, AST rename, diff
  codegen/          — JavaScript code generation runtime (ctx API)
  jsengine/         — QuickJS-based per-node JS transforms
  exemplar/         — Teach-by-example template extraction
  semdiff/          — Structural AST diffing (moves, renames, signatures, data formats)
  bisect/           — Semantic git bisect (binary search on structural predicates)
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

Sawmill runs as a single global HTTP MCP server (`sawmill serve`) listening
on `127.0.0.1:8765` by default. Stdio-based MCP clients (Claude Code, etc.)
connect through a transparent gateway such as mcpbridge, which translates
stdio ↔ streamable HTTP without altering the protocol.

Each MCP session must call `parse(path=...)` once to bind itself to a
project root. The server lazily loads and shares a `CodebaseModel` per
unique project root via `modelpool.Pool` — multiple sessions targeting
the same root amortise parsing cost.

Each session gets its own per-session state (pending changes, backups)
while sharing the underlying model.

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
