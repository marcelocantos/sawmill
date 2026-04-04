# Convergence Targets

## 🎯T1–T5 Phases 2–6 — ACHIEVED

## 🎯T6 Frontier A: Rich `ctx` API

**Status:** In progress

**Desired state:** Codegen programs can navigate node structure
(fields, methods, parameters, return type, decorators) and perform
semantic mutations (addField, addMethod, addImport, setReturnType)
through the `ctx` API. Language adapters provide the structural
queries and code templates that make this work per-language.

### Sub-targets

- 🎯T6.1 **Structural navigation** — nodes returned by `ctx` have
  `.fields()`, `.methods()`, `.parameters()`, `.returnType()`,
  `.body()`, `.decorators()` that return structured data.
- 🎯T6.2 **Semantic mutations** — nodes have `.addField(name, type)`,
  `.addMethod(name, params, body)`, `.addImport(path)` that generate
  syntactically correct code per-language and splice it in.
- 🎯T6.3 **Adapter extensions** — `LanguageAdapter` trait gains
  methods for field queries, method queries, and code generation
  templates per-language.
