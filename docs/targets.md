# Convergence Targets

## 🎯T1 Phase 2: Match/act engine + multi-language + querying

**Status:** Near complete — 🎯T1.4 remains

**Desired state:** PolyRefactor's MCP server exposes a general-purpose
match/act transform engine alongside querying tools, with language
adapters for Python, Rust, TypeScript, C++, and Go. Agents can find
symbols, query structure, apply declarative transforms (replace, wrap,
remove, prepend/append), and optionally run formatters on changed
hunks.

### Sub-targets

- 🎯T1.1 **Language adapters for TypeScript, C++, Go** — DONE.
- 🎯T1.2 **Query and symbol tools** — DONE. `query`, `find_symbol`,
  `find_references` MCP tools.
- 🎯T1.3 **Match/act engine** — DONE. Abstract + raw matching,
  8 declarative actions, `transform` MCP tool.
- 🎯T1.4 **Hunk-level formatter integration** — optionally run
  external formatters (rustfmt, ruff, prettier, clang-format) only
  on changed byte ranges after a transform.
