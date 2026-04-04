# Convergence Targets

## 🎯T1–T4 Phases 2–5 — ACHIEVED

## 🎯T5 Phase 6: LSP integration

**Status:** In progress

**Desired state:** The server connects to language-specific LSP
servers for semantic information — type info, go-to-definition,
find references, find implementations, diagnostics. This enriches
the `ctx` API and enables pre-flight compile validation.

### Sub-targets

- 🎯T5.1 **LSP client** — manage LSP server lifecycle (spawn, init,
  shutdown). Each adapter declares its LSP command. Opportunistic —
  skip languages where the server binary isn't available.
- 🎯T5.2 **Document sync** — open/change documents with the LSP
  server. Needed before any queries work.
- 🎯T5.3 **Semantic queries** — hover (type info), definition,
  references, implementation. Exposed as MCP tools and on `ctx`.
- 🎯T5.4 **Diagnostic validation** — after applying edits in memory,
  feed modified source to the LSP and check for errors. Upgrade
  codegen's pre-flight validation from parse-only to compile-check.
