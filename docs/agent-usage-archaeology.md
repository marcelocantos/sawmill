# Agent Usage Archaeology: Where Sawmill Would Have Helped

**Date:** 2026-04-07
**Method:** mnemo transcript search across all interactive sessions
**Purpose:** Identify real-world agent workflows where sawmill could
have saved significant time, and surface new feature opportunities
not yet on the roadmap.

## Executive Summary

Analysis of historical Claude Code sessions across multiple
repositories (CSP, frozen, den, sawmill, sqlpipe, yourworld2) reveals
six recurring work patterns where agents spend disproportionate time
on mechanical code transformations. Three of these map to existing or
planned sawmill features. Three represent genuinely new capabilities
that would extend sawmill's value proposition significantly.

The single largest opportunity is **type-level structural
refactoring** — adding fields, changing type representations, and
propagating those changes through all construction and usage sites.
This pattern consumed entire context windows in multiple sessions and
is currently handled through tedious sequential Edit calls.

---

## Part 1: Patterns That Match Existing/Planned Sawmill Features

### 1.1 Cross-file Symbol Rename

**Observed in:** CSP (`microthread` → `csp`, then `microthread` → `imp`)

The CSP repository underwent two major rename campaigns:

- **File-level rename** (`microthread.*` → `csp.*`): 4 file renames
  plus 35 `#include` directive updates across 43 files. Required
  `git mv` for the files, then manual Edit calls for every include
  path.
- **Symbol-level rename** (`Microthread` → `Imp`, `microthread_error`
  → `error`, `mt` → `imp`, `mt_local` → `imp_local`): Touched struct
  names, variable names, error types, file names, and documentation.
  Spanned multiple context windows and required session continuations.

The workflow was: (1) spawn an exploration agent to grep for all
occurrences and build an inventory, (2) create a multi-phase plan,
(3) work through files one by one with Edit calls, (4) grep again to
verify no stray references remain.

**Sawmill fit:** `rename` + `apply` handles the symbol rename.
`find_references` replaces the grep-based inventory. The operation
collapses from hours to minutes.

**Gap:** The *file* rename (git mv + import path cascade) is not
covered by `rename`, which operates on symbols. See §2.1.

### 1.2 Bulk Identical Structural Edit

**Observed in:** CSP (`#pragma once` across 29 headers)

Replacing include guards with `#pragma once` in all 29 project
headers. Each file needed the same transformation: remove the
`#ifndef`/`#define` guard pair and trailing `#endif`, insert
`#pragma once` at the top.

**Sawmill fit:** `teach_by_example` with one before/after pair, then
`transform_batch`. Or a `teach_recipe` encoding the pattern
"replace include guards with pragma once."

### 1.3 Convention Enforcement

**Observed in:** Global CLAUDE.md directives, frozen/den/CSP audits

Conventions currently enforced by CLAUDE.md instructions to the agent:
- Apache 2.0 licensing with SPDX headers on all source files
- No magic numbers (use named constants)
- `#pragma once` instead of include guards
- Specific error handling patterns

Agents running `/audit` across repos spend significant context
checking these manually. Each audit re-discovers the same issues.

**Sawmill fit:** `teach_convention` + `check_conventions`. Teach
once, enforce automatically on every `apply` and on demand.

---

## Part 2: Patterns Not Currently on the Roadmap

These represent genuinely new feature opportunities surfaced by the
transcript analysis.

### 2.1 File Rename with Import/Include Cascade

**Observed in:** CSP (`microthread.cc` → `csp.cc`, etc.)

Distinct from symbol rename. The agent used `git mv` to rename 4
files, then manually updated 35 `#include` directives referencing
those files. The import path is a string literal, not a symbol — it
lives outside tree-sitter's symbol resolution.

**Proposed feature:** `rename_file` — renames a file on disk and
updates all import/include/require paths that reference it. Needs to
understand language-specific import resolution:
- C/C++: `#include "path"` and `#include <path>`
- Go: `import "module/path"`
- Rust: `mod` declarations, `use` paths
- JS/TS: `import ... from "path"`, `require("path")`

**Complexity:** Medium. The file-to-import-path mapping varies by
language, but each language adapter already parses these constructs.

### 2.2 Add Field + Propagate to Constructors and Call Sites

**Observed in:** Frozen (h0/h128 on all HAMT node types)

Adding an `h0` field to the `leaf1`, `leaf2`, `leaf`, and `branch`
structs required updating:
1. The struct definition (add the field)
2. Every factory function (`newLeaf1`, `newLeaf2`, etc.) — add
   parameter
3. Every *caller* of those factories — pass the new argument
4. Validation/vet functions — check the new field
5. Accessor methods — expose the new field

The agent built a 9-step task list and worked through each file
sequentially. This single operation consumed an entire session and
required careful ordering to avoid intermediate compilation failures.

**Proposed feature:** `add_field` — adds a field to a struct/class
and propagates through:
- Constructor/factory function signatures (adds parameter)
- All callers of those constructors (adds argument, with a
  user-specified default expression)
- Struct literal constructions (adds field initializer)

This composes naturally with existing `add_parameter` but operates
at the type level rather than the function level. The key insight is
that adding a field is rarely just adding a field — it's a cascade
through the construction graph.

**Complexity:** High. Requires understanding the relationship between
types and their construction sites, which varies significantly by
language (Go struct literals, C++ constructors, Rust struct
expressions, etc.).

### 2.3 Type Shape Migration

**Observed in:** Frozen (EqArgs struct → EqOps interface)

The `EqArgs[T]` refactoring changed a struct with function pointer
fields into an embedded interface with multiple concrete
implementations. Every usage site needed mechanical but non-trivial
rewrites:
- `EqArgs{eq: f, hash: g}` → `NewDefaultEqOps[T]()`
- `args.eq(a, b)` → `args.Equal(a, b)`
- `args.hash(v)` → `args.Hash(v)`
- `args.fullHash` → type assertion or method call

This is **not** a rename — the type's entire shape changed. But the
rewrite at each usage site follows mechanical rules that an agent
could express once and sawmill could apply everywhere.

**Proposed feature:** `migrate_type` or a richer `transform` DSL
that accepts rewrite rules mapping old access patterns to new ones:

```javascript
// Pseudo-API for the migration
ctx.migrateType("EqArgs", {
  construction: (old) => `NewDefaultEqOps[${old.typeParam}]()`,
  fieldAccess: {
    "eq(a, b)": "Equal(a, b)",
    "hash(v)": "Hash(v)",
    "fullHash": "IsFullHash()",
  }
});
```

This sits between `rename` (too simple) and a full codegen program
(too manual). It's a structured migration specification.

**Complexity:** Very high. Requires pattern matching on usage
contexts, not just symbol names. May be better served by enriching
the `transform` DSL than creating a dedicated tool.

### 2.4 Dependency Impact Analysis

**Observed in:** Frozen (auditing `arr-ai/hash` usage before vendoring)

Before internalizing the `arr-ai/hash` dependency, an exploration
agent spent significant context building a complete inventory:
- 9 files importing the package
- Every type used (`Hashable`, `Seed`, etc.)
- Every function called
- The public API surface affected (`Key[T]` embeds `hash.Hashable`)

**Proposed feature:** `dependency_usage` — given a package/module
path, return a structured report of all import sites, types used,
functions called, and public API exposure. This is a specialized
query that combines `find_references` across multiple symbols with
import graph analysis.

**Complexity:** Medium. Builds on the existing symbol index and file
model. The novel part is aggregating across all symbols from a single
source.

### 2.5 Clone-and-Adapt from Exemplar

**Observed in:** Sawmill design discussion, frozen node operations,
MCP tool handlers across multiple repos

Agents regularly create new code by copying an existing exemplar and
adapting it. Examples:
- Adding a new node operation in frozen that follows the same
  traversal pattern as existing operations
- Adding a new MCP tool handler following the same structure as
  existing handlers
- Adding a new CSP combinator with the standard test structure

The current workflow is: read the exemplar, understand the pattern,
write the new version from scratch (or copy-paste and edit). The
agent understands the *intent* but spends tokens on mechanical
reproduction.

**Proposed feature:** `clone_and_adapt` — given a source symbol or
code region and a set of substitutions/adaptations, produce a new
version. Differs from `teach_recipe` in that it works from a
concrete instance rather than an abstracted template. The agent
points at real code and says "make another one like this, but with
these changes."

**Complexity:** Medium. The substitution part is straightforward; the
hard part is determining what's structural (keep) vs. what's specific
to the exemplar (adapt). Could leverage `teach_by_example` with a
single example + explicit parameter annotations.

### 2.6 Structural Invariant Assertions

**Observed in:** Frozen audits, den audits, CSP audits

Agents running `/audit` across repos repeatedly check cross-cutting
structural properties:
- "Every type implementing the `node` interface must have an `h0`
  field"
- "Every exported function in the MCP package must have a
  corresponding test"
- "Every MCP tool definition must have a matching entry in the agent
  guide"
- "Every public API type must have a doc comment"

These are not style conventions (which `check_conventions` handles).
They are **relational invariants** between code elements — asserting
that the presence of one thing implies the presence of another.

**Proposed feature:** `teach_invariant` — a richer assertion language
for structural relationships:

```
teach_invariant "node interface completeness":
  for each struct S implementing node:
    S must have field h0 of type H128
    S must have method H0() returning H128

teach_invariant "test coverage for MCP tools":
  for each function F in package mcp matching "handle*":
    there must exist a function "Test${F.name}" in *_test.go
```

**Complexity:** High. Requires a query language that can express
relationships between code elements. Could start simple (patterns
over `query` results) and grow. Related to Frontier D (structural
pre-flight checks) but broader in scope — invariants are
project-level assertions, not per-transform checks.

---

## Part 3: Cross-Cutting Observations

### Context Window Consumption

The most expensive pattern by far is **type-level structural
refactoring** (§2.2, §2.3). These operations routinely exhaust
context windows because:
1. The agent must read every affected file to understand the current
   state
2. Each Edit call adds both the old and new content to context
3. Build-fail-fix cycles add compiler output to context
4. The number of affected sites scales with codebase size

Sawmill's value proposition is strongest when it can replace N
sequential Edit calls with one tool invocation that produces a
previewable diff.

### The Validation Gap

Several patterns (§2.2 especially) suffer from intermediate
compilation failures — the agent changes a type definition but hasn't
yet updated all usage sites, so the build fails. The agent then
chases compiler errors one at a time.

Frontier D (structural pre-flight checks) partially addresses this,
but the full solution requires understanding the *transitive closure*
of a change — "if I add this field, what else must change for the
codebase to remain valid?" This is the province of §2.2's `add_field`
rather than a generic validation step.

### Agent Effort vs. Sawmill Effort

| Pattern | Agent time (observed) | Sawmill complexity | ROI |
|---|---|---|---|
| Cross-file rename | Hours, multi-session | Already built | Immediate |
| Bulk structural edit | 30-60 min | Already built | Immediate |
| Convention checking | Repeated per audit | Already built | Immediate |
| File rename + imports | 30-60 min | Medium | High |
| Add field + propagate | Full session | High | Very high |
| Type shape migration | Multi-session | Very high | Very high |
| Dependency impact | 30 min | Medium | Medium |
| Clone-and-adapt | 20-40 min | Medium | Medium |
| Structural invariants | Repeated per audit | High | High (amortised) |

### Relationship to Existing Roadmap

| New feature | Related roadmap item | Relationship |
|---|---|---|
| File rename + imports | — | Net new |
| Add field + propagate | `add_parameter` | Extension (type-level) |
| Type shape migration | `transform` DSL | Extension (richer rules) |
| Dependency impact | `find_references` | Aggregation layer |
| Clone-and-adapt | `teach_by_example` | Variant (single exemplar) |
| Structural invariants | Frontier D (pre-flight) | Broader scope |

---

## Part 4: Implementation Design

### 4.1 LSP Client — Foundation Layer (🎯T13)

All type-aware features (§2.2–2.6) depend on a shared LSP client
layer. The adapter interface already declares `LSPCommand()` and
`LSPLanguageID()` for all five languages. The missing piece is the
client that launches, manages, and queries these servers.

**Libraries**: `go.lsp.dev/jsonrpc2` (JSON-RPC 2.0 transport) +
`go.lsp.dev/protocol` (typed LSP 3.17 client via `ClientDispatcher`).
Both are pure Go, actively maintained, and used by gopls tooling.

**Package**: `go/lspclient/`

**Types**:

```go
// Client manages a single LSP server process for one language.
type Client struct {
    cmd     *exec.Cmd
    conn    jsonrpc2.Conn
    client  protocol.Client
    langID  string
    rootURI protocol.DocumentURI
    mu      sync.Mutex
    opened  map[protocol.DocumentURI]bool // tracks didOpen state
}

// Pool manages LSP clients per (language, project root) pair.
// Lazily starts servers on first query; reuses across tool calls.
type Pool struct {
    mu      sync.Mutex
    clients map[poolKey]*Client
}

type poolKey struct {
    language string
    root     string
}
```

**Lifecycle**:

1. `Pool.Get(adapter, root)` — returns existing client or launches
   one via `adapter.LSPCommand()`.
2. Launch: `exec.Command(cmd[0], cmd[1:]...)`, pipe stdin/stdout as
   `jsonrpc2.NewStream`. Send `initialize` with `rootUri` and
   `capabilities`. Send `initialized{}`.
3. Before any query on a file, send `textDocument/didOpen` if not
   already tracked in `opened` map.
4. Query methods: `Hover(file, line, col)`, `Definition(file, line, col)`,
   `References(file, line, col)`, `Diagnostics(file)`.
5. Shutdown: `shutdown` + `exit` on `Pool.Close()` or when the
   daemon shuts down. Kill the process if it doesn't exit within 2s.

**Integration points**:

- `CodebaseModel` holds a `*lspclient.Pool` (created in `model.Load`).
- The `codegen` package receives the pool and wires `ctx.typeOf`,
  `ctx.definition`, `ctx.lspReferences`, `ctx.diagnostics` as
  QuickJS callbacks that call through to the pool.
- `ctx.hasLsp` becomes true when the pool has a live client for the
  file's language.
- The MCP `hover`, `definition`, `lsp_references`, `diagnostics`
  tools call through to the pool directly from the handler.

**Graceful degradation**: If the LSP server binary is not installed
(e.g. `gopls` not on PATH), `Pool.Get` returns nil. All consumers
treat nil as "LSP unavailable" — syntax-only mode. No hard failures.

**Testing**: Mock LSP server that responds to initialize + hover with
canned responses. Tests verify the launch/handshake/query/shutdown
cycle. Integration tests use actual `gopls` (skip if not installed).

### 4.2 File Rename with Import Cascade (🎯T14)

**MCP tool**: `rename_file`

**Parameters**: `from` (current path), `to` (new path), `format`
(optional bool)

**Algorithm**:

1. Validate: `from` exists in the forest, `to` does not exist.
2. For each file in the forest, scan import/include nodes using
   `adapter.ImportQuery()`.
3. For each import node whose path resolves to `from`, rewrite the
   path string to `to`. Resolution rules per language:
   - **Python**: `import foo.bar` → module path `foo/bar.py`. Dot
     notation maps to directory structure.
   - **Go**: `import "module/path"` — resolve relative to module root
     in go.mod.
   - **Rust**: `mod foo;` → `foo.rs` or `foo/mod.rs`. `use` paths
     are module-internal, not file paths.
   - **TypeScript**: `import ... from "./path"` — resolve relative
     to importing file.
   - **C/C++**: `#include "path"` — resolve relative to file or
     include directories.
4. Produce a `FileChange` for each modified file (import path update)
   plus a `FileRename{From, To}` for the file itself.
5. On `apply`: rename the file on disk, then write import changes.
   Backup both the original file location and all modified importers.

**Complexity note**: The import resolution rules are the hard part.
Each language adapter needs a new method:

```go
// ResolveImportPath returns the filesystem path that an import node
// refers to, given the importing file's path and the project root.
// Returns "" if the import cannot be resolved to a local file.
ResolveImportPath(importText, importingFile, root string) string
```

### 4.3 Add Field + Propagate (🎯T15)

**MCP tool**: `add_field`

**Parameters**: `type_name`, `field_name`, `field_type`,
`default_value` (expression used at construction sites), `path`
(optional filter), `format` (optional bool)

**Algorithm**:

1. **Find the type definition** via `adapter.TypeDefQuery()` matching
   `type_name`.
2. **Insert the field** into the struct/class body using
   `adapter.GenField(field_name, field_type)` and tree-sitter node
   insertion (similar to `addParamInFile`).
3. **Find construction sites** — this is the hard step. Two
   sub-strategies:

   a. **Syntactic (no LSP)**: Use tree-sitter to find struct
      literals / constructor calls that name the type. Works for Go
      (`Foo{...}`), Rust (`Foo { ... }`), and Python
      (`Foo(...)`). Fragile for C++ (constructor overloads, implicit
      conversions). For each match, insert `field_name: default_value`
      (Go/Rust) or add `default_value` as an argument (Python/C++).

   b. **Type-aware (LSP available)**: Use `textDocument/references`
      on the type definition to find all usage sites. Filter to
      construction contexts by checking the surrounding tree-sitter
      node kind (struct literal, call expression, etc.). More
      reliable — catches factory functions, type aliases, and
      imports.

4. **Find factory functions** — functions whose return type matches
   `type_name`. With LSP: hover on the return type of each function
   found by `find_symbol` and check if it matches. Without LSP:
   heuristic — functions named `New<TypeName>` or `new_<type_name>`.
   For each factory, use `add_parameter` to add the new field as a
   parameter, then propagate to callers of that factory.

5. Collect all changes as pending. Preview diff. Apply on confirm.

**Language-specific construction patterns**:

| Language | Struct literal | Factory/constructor | Named fields |
|---|---|---|---|
| Go | `Foo{field: val}` | `NewFoo(args)` | Yes (or positional) |
| Rust | `Foo { field: val }` | `Foo::new(args)` | Yes |
| Python | `Foo(field=val)` | `Foo(args)` | kwargs or positional |
| C++ | `Foo{.field = val}` | `Foo(args)` or `make_foo(args)` | Designated initializers (C++20) or positional |
| TypeScript | `{ field: val } as Foo` | `new Foo(args)` | Object literal |

### 4.4 Type Shape Migration (🎯T16)

**MCP tool**: `migrate_type`

**Parameters**: `type_name`, `rules` (JSON object mapping old
patterns to new patterns), `path`, `format`

**Rules format**:

```json
{
  "construction": {"old": "EqArgs{eq: $eq, hash: $hash}", "new": "NewDefaultEqOps[$T]()"},
  "field_access": {
    "$.eq($a, $b)": "$.Equal($a, $b)",
    "$.hash($v)": "$.Hash($v)",
    "$.fullHash": "$.IsFullHash()"
  }
}
```

**Algorithm**:

1. **Parse rules** into match/replace pairs with named captures
   (`$eq`, `$a`, etc.).
2. **Find all references** to `type_name` via LSP
   `textDocument/references` on the type definition. Without LSP,
   fall back to `find_references` (syntactic, less precise).
3. **Classify each reference** by its tree-sitter context:
   - Construction: parent node is struct literal / call expression →
     apply `construction` rule.
   - Field access: parent node is member expression / field access →
     match against `field_access` rules.
   - Type annotation: parent is type expression → rename the type
     (or leave if the type name didn't change).
4. **Apply substitutions**: For each matched pattern, extract the
   captured variables and produce the replacement text.

**Key difficulty**: Step 1 requires a mini pattern language that
matches tree-sitter subtrees with named holes. This overlaps with
🎯T12 (pattern equivalences). Consider sharing the pattern matching
infrastructure — both features need "match a code pattern with
placeholders against a tree-sitter tree."

**Phasing**: Implement after 🎯T13 (LSP client) and 🎯T15
(add_field), which exercise the same reference-finding and
construction-site-detection machinery. Type migration extends that
with richer pattern matching.

### 4.5 Dependency Impact Analysis (🎯T17)

**MCP tool**: `dependency_usage`

**Parameters**: `package` (import path or module name), `path`
(optional filter)

**Algorithm**:

1. Find all import nodes across the forest where the import path
   matches `package` (using `adapter.ImportQuery()`).
2. For each importing file, find all identifiers that resolve to
   symbols from that package:
   - **With LSP**: For each identifier in the file, use
     `textDocument/definition` to check if the definition is in a
     file belonging to `package`. Batch this — hover all identifiers
     in importing files.
   - **Without LSP**: Heuristic — look for qualified access patterns
     (e.g., `pkg.Symbol` in Go, `package::symbol` in Rust).
3. Group results by symbol: `{symbol_name, kind, usage_sites: [{file, line}]}`.
4. Identify public API exposure — symbols from `package` that appear
   in the signatures of exported functions/types in the project.

**Output**: Structured report (text, not a diff):

```
Package "arr-ai/hash" used in 9 files:
  Types: Hashable (5 sites), Seed (3 sites)
  Functions: Hash (12 call sites), NewSeed (2 call sites)
  Public API exposure:
    Key[T] embeds hash.Hashable (frozen/key.go:15)
```

### 4.6 Clone-and-Adapt (🎯T18)

**MCP tool**: `clone_and_adapt`

**Parameters**: `source` (symbol name or `file:start-end` range),
`substitutions` (JSON object mapping old → new), `target_file`
(where to insert the clone), `position` (after which symbol, or
"end"), `format`

**Algorithm**:

1. **Extract the source**: Locate by symbol name (via `find_symbol`)
   or by file:range. Get the source text.
2. **Apply substitutions**: String replacement of each key→value pair.
   Order by longest key first to avoid partial matches.
3. **Insert at target**: Add the substituted text at the specified
   position in `target_file`.
4. **Handle imports**: If the source has imports that the target file
   doesn't, use `adapter.GenImport()` to add them.

This is intentionally simpler than `teach_by_example` — no
templatisation, no recipe storage. It's a one-shot "copy this and
change these strings" operation. The agent already knows what to
change; it just doesn't want to spend tokens on the mechanical
reproduction.

### 4.7 Structural Invariants (🎯T19)

**MCP tool**: `teach_invariant`

**Parameters**: `name`, `description`, `check` (structured rule, not
free-form JS like conventions)

**Rule language** (JSON):

```json
{
  "for_each": {"kind": "type", "name": "*", "implementing": "node"},
  "require": [
    {"has_field": {"name": "h0", "type": "H128"}},
    {"has_method": {"name": "H0", "returns": "H128"}}
  ]
}
```

**Algorithm**:

1. **Parse the rule**: `for_each` defines the iteration set (uses
   existing `query` infrastructure). `implementing` requires LSP for
   interface satisfaction checks.
2. **Evaluate `require`**: For each matched entity, check that the
   required fields/methods/tests exist. `has_field` and `has_method`
   use the adapter's `FieldQuery()` and `MethodQuery()`. `has_test`
   uses naming conventions (`Test<Name>` in Go, `test_<name>` in
   Python).
3. **Report violations**: List each entity that fails a requirement,
   with specific missing items.

**Storage**: Invariants are stored in the SQLite store alongside
recipes and conventions (new `invariants` table: name, description,
rule JSON).

**Companion tools**: `check_invariants` (run all), `list_invariants`,
`delete_invariant`.

**Without LSP**: `implementing` clauses degrade to syntactic
heuristics (e.g., Go: check if the type has all methods declared in
the interface). Less reliable but still useful for common cases.

### 4.8 Shared Infrastructure

Several features share common machinery:

| Component | Used by | Location |
|---|---|---|
| LSP client pool | §4.3, §4.4, §4.5, §4.7 | `go/lspclient/` |
| Construction site finder | §4.3, §4.4 | `go/typeutil/` |
| Pattern matcher with holes | §4.4, 🎯T12 | `go/pattern/` |
| Import resolver | §4.2, §4.6 | `adapters.ResolveImportPath()` |

Build the LSP client (🎯T13) first — it unblocks everything else.
Then file rename (🎯T14) and add_field (🎯T15) as independent
streams. Type migration (🎯T16) and structural invariants (🎯T19)
come last since they depend on the patterns established by earlier
features.

**Dependency graph**:

```
🎯T13 LSP Client
 ├─► 🎯T14 File Rename (also needs import resolver, no LSP required)
 ├─► 🎯T15 Add Field (syntactic path works without LSP; LSP improves it)
 │    └─► 🎯T16 Type Migration (needs pattern matcher + LSP)
 ├─► 🎯T17 Dependency Impact (needs LSP for precision)
 └─► 🎯T19 Structural Invariants (needs LSP for interface checks)

🎯T18 Clone-and-Adapt (independent — no LSP needed)
```

---

## Methodology

Transcript data was gathered via mnemo FTS5 search across all
interactive sessions. Search queries targeted: rename operations,
bulk edits, parameter changes, boilerplate generation, convention
enforcement, type refactoring, and audit patterns. Results were
cross-referenced with sawmill's current tool inventory and frontier
roadmap.

Repositories with significant signal: CSP (C++ concurrency library),
frozen (Go HAMT library), den (Go project), sawmill itself (design
discussions), sqlpipe (C++/Go SQL tool).
