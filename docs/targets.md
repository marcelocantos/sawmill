# Convergence Targets

## 🎯T1 Phase 2 — ACHIEVED
## 🎯T2 Phase 3 — ACHIEVED

## 🎯T3 Phase 4: Persistent codebase model

**Status:** In progress

**Desired state:** The MCP server maintains a live, indexed model of
the codebase that persists across sessions (SQLite), stays current via
file watching, and supports incremental re-parsing. Tool calls operate
against the in-memory model rather than re-parsing from disk on every
invocation.

### Sub-targets

- 🎯T3.1 **SQLite store** — schema for file metadata (path, language,
  mtime, content hash), symbol index (name, kind, file, line, scope),
  cross-references. Read/write operations.
- 🎯T3.2 **Stateful forest** — the MCP server holds a persistent
  `Forest` in memory. `parse` loads from cache and incrementally
  updates changed files. All other tools operate against the
  in-memory forest without re-parsing.
- 🎯T3.3 **File watcher** — `notify` crate monitors parsed
  directories. Changed files are re-parsed and indexes updated
  incrementally.
- 🎯T3.4 **Symbol index** — queryable index of all symbols (functions,
  types, imports) with cross-references. Replaces ad-hoc Tree-sitter
  queries for `find_symbol` and `find_references`.
