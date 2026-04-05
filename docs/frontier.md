# PolyRefactor Frontier Roadmap

**Date:** April 2026
**Status:** Frontiers A, B, C complete. E and D are next.

## Completed

| Item | What was built |
|---|---|
| **A. Rich ctx API** | `.fields()`, `.methods()`, `.addField()`, `.addMethod()`, `ctx.addImport()`, doc comment support |
| **B. Teach by example** | `teach_by_example` tool with case-aware template extraction and multi-file patterns |
| **C. Convention invariants** | `teach_convention`, `check_conventions`, convention checking on `apply` |

## Active Frontier

### D. Structural Pre-flight Checks

Beyond "does it parse?" — detect problems before writing to disk:

- **Dangling references**: renamed a symbol but a call site still
  uses the old name. Cross-reference the symbol index before and
  after the edit.
- **Missing imports**: moved a symbol to another file but didn't
  add an import at the use sites.
- **Orphaned code**: deleted a function but its tests still exist.

Uses the symbol index and Tree-sitter queries, not LSP. Fast and
available even when no LSP server is running.

### E. LSP on `ctx`

Bridge LSP tools into the codegen runtime so JS programs can make
decisions based on semantic information:

```javascript
var typ = ctx.typeOf(someNode);           // → "Vec<String>"
var impls = ctx.implementors("Handler");  // → [node, node, ...]
var def = ctx.definition(someNode);       // → node in another file
var diags = ctx.diagnostics("file.rs");   // → [{line, message}, ...]
```

Requires plumbing the LspManager into the QuickJS runtime. The
tricky part is synchronous IPC from JS — needs careful timeout
handling.

## Deferred (Future Work)

These are valuable but not needed for the near-term use case.

- **F. Incremental Tree-sitter re-parse** — performance for large
  files via `tree.edit()` + incremental parse. Current whole-file
  re-parse is fast enough.
- **G. Change decomposition** — sharding large changes by package
  boundary with per-shard validation. Matters for large-scale
  migrations, not daily work.
- **H. Multi-workspace support** — tracking multiple project roots
  for monorepos or cross-repo operations.
- **I. Automatic LSP management** — detect/install missing LSP
  servers automatically.
- **J. Plugin system** — user-defined language adapters, recipe
  marketplace, convention packs.
- **K. Agent prompt generation** — server generates a rich system
  prompt describing its capabilities, recipes, and conventions.
  High value but depends on real-world usage to know what to
  include.
- **L. WASM build** — browser playground. Needs a user community
  first.

## Design Principles

1. **The agent decides what; the platform decides how.**
2. **Degrade, don't fail.** LSP unavailable? Structural queries
   still work.
3. **Teach once, use forever.** Recipes, conventions, and patterns
   accumulate value across sessions.
4. **Diff is the universal interface.** Every operation produces a
   previewable diff.
5. **Code is the language.** No custom DSLs. Input and output are
   code.
