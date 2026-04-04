# Convergence Targets

## 🎯T1 Phase 2: Match/act engine + multi-language + querying

**Status:** In progress

**Desired state:** PolyRefactor's MCP server exposes a general-purpose
match/act transform engine alongside querying tools, with language
adapters for Python, Rust, TypeScript, C++, and Go. Agents can find
symbols, query structure, apply declarative transforms (replace, wrap,
remove, prepend/append), and optionally run formatters on changed
hunks.

### Sub-targets

- 🎯T1.1 **Language adapters for TypeScript, C++, Go** — each with
  identifier queries, function def queries, and type identifier
  handling where applicable.
- 🎯T1.2 **Query and symbol tools** — `query`, `find_symbol`,
  `find_references` MCP tools for structural search across the forest.
- 🎯T1.3 **Match/act engine** — abstract matching (kind/name/scope)
  + declarative actions (replace, wrap, unwrap, remove,
  prepend_statement, append_statement, replace_name, replace_body).
  Exposed as `transform` MCP tool.
- 🎯T1.4 **Hunk-level formatter integration** — optionally run
  external formatters (rustfmt, ruff, prettier, clang-format) only
  on changed byte ranges after a transform.
