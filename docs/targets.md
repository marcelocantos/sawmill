# Convergence Targets

## 🎯T1 Phase 2 — ACHIEVED
## 🎯T2 Phase 3 — ACHIEVED
## 🎯T3 Phase 4 — ACHIEVED

## 🎯T4 Phase 5: Code generator runtime

**Status:** In progress

**Desired state:** Agents can write JavaScript programs that operate
across the entire codebase model — querying symbols, navigating
relationships, and making coordinated edits across multiple files
from a single program. Patterns can be taught and instantiated.

### Sub-targets

- 🎯T4.1 **`ctx` API** — a new `codegen` MCP tool that accepts a JS
  program receiving a `ctx` object with: `findFunction`, `findType`,
  `query`, `references`, `addFile`, `editFile`. Edits across multiple
  files collected into a single diff.
- 🎯T4.2 **Pattern teaching** — `teach_recipe` tool to define named
  sequences of operations with variables. `instantiate` tool to
  execute them. Stored in SQLite.
- 🎯T4.3 **Pre-flight parse validation** — after applying edits
  in memory, re-parse with Tree-sitter and report any parse errors
  before committing.
