# Convergence Targets

## 🎯T1–T5 Phases 2–6 — ACHIEVED
## 🎯T6 Frontier A — ACHIEVED
## 🎯T7 Frontier B — ACHIEVED

## 🎯T8 Frontier C: Convention invariants

**Status:** In progress

**Desired state:** Agents can declare project conventions as
enforceable rules. Conventions are checked on `apply` and
violations reported. Conventions persist in SQLite across sessions.

### Sub-targets

- 🎯T8.1 **Convention storage** — SQLite table for conventions
  (name, description, check program). CRUD on store.
- 🎯T8.2 **`teach_convention` MCP tool** — define a convention with
  a JS check program that returns violations.
- 🎯T8.3 **Check on apply** — before writing changes, run all
  conventions and warn on violations.
- 🎯T8.4 **`check_conventions` MCP tool** — scan the codebase for
  existing violations on demand.
