# Canopy

MCP server for AST-level multi-language code transformations. Built in Rust
with Tree-sitter.

## Build & test

```bash
cargo build          # Debug build
cargo build --release
cargo test           # Run all tests (35 unit tests)
```

Rust 2024 edition. Requires rustc 1.85+.

## Project layout

```
src/
  main.rs         — CLI entry point (clap: parse, rename, serve)
  mcp.rs          — MCP server (CanopyServer, all tool definitions)
  forest.rs       — Forest/ParsedFile, apply_with_backup, undo
  model.rs        — CodebaseModel (persistent state, incremental parsing)
  store.rs        — SQLite store (files, symbols, recipes, conventions)
  adapters/       — Language adapters (Python, Rust, TS, Go, C++)
  transform.rs    — Match/act engine
  rewrite.rs      — Range-based source rewriting
  codegen.rs      — JavaScript code generation runtime (ctx API)
  exemplar.rs     — Teach-by-example template extraction
  index.rs        — Symbol extraction from Tree-sitter trees
  js_engine.rs    — QuickJS integration for JS transforms
  lsp.rs          — LSP client for semantic queries
  watcher.rs      — File system watcher with debouncing
docs/
  design.md       — Architecture and design rationale
  frontier.md     — Roadmap (completed and planned frontiers)
  targets.md      — Convergence targets
```

## Architecture

The server holds a `CodebaseModel` that ties together:
- A `Forest` of `ParsedFile`s (Tree-sitter CSTs + original source bytes)
- A `Store` (SQLite: file metadata, symbol index, recipes, conventions)
- A `FileWatcher` for live incremental updates
- An `LspManager` for semantic queries

All transforms produce diff previews. Changes are only written on explicit
`apply` (with backup files for `undo`).

## Key conventions

- All MCP tools are defined in `src/mcp.rs` via the `#[tool_router]` macro
- Language adapters implement `LanguageAdapter` trait in `src/adapters/`
- Many public APIs exist as stubs for planned features (see `docs/frontier.md`)
  — the crate uses `#![allow(dead_code)]` to suppress warnings
- JavaScript code generation receives a `ctx` object with `fields()`,
  `methods()`, `addField()`, `addMethod()`, `addImport()`, etc.

## Delivery

Merged to master.

## Gates

profile: base
