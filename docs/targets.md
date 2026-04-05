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

## 🎯T10 Frontier K: Agent prompt generation — ACTIVE

The MCP server generates a rich, dynamic system prompt describing its
capabilities, taught recipes, and taught conventions. Agents connecting
to Canopy receive enough context to use it effectively without external
documentation.

- 🎯T10.1 Static instructions — the agents-guide content is served as
  MCP instructions on connection
- 🎯T10.2 Dynamic prompt tool — a `get_agent_prompt` tool returns the
  full prompt including project-specific recipes and conventions from
  the store
