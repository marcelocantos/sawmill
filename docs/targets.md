# Convergence Targets

## 🎯T1–T5 Phases 2–6 — ACHIEVED
## 🎯T6 Frontier A: Rich ctx API — ACHIEVED

## 🎯T7 Frontier B: Teach by example

**Status:** In progress

**Desired state:** An agent can point at existing code, name the
variable parts, and the platform extracts a reusable template that
can be instantiated with different parameter values. Multi-file
patterns supported via `also_affects`.

### Sub-targets

- 🎯T7.1 **Template extraction** — `teach_by_example` tool accepts
  an exemplar file path, parameter name→value mapping, and extracts
  a template by replacing parameter values with template holes.
- 🎯T7.2 **Multi-file patterns** — `also_affects` field triggers
  extraction on related files, creating a multi-file template.
- 🎯T7.3 **Instantiation** — the existing `instantiate` tool works
  with example-derived templates (stored as recipes with the
  extracted template as the step).
