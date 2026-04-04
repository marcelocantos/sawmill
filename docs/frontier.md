# PolyRefactor Frontier Roadmap

**Date:** April 2026
**Status:** Post-Phase 6 — all design doc phases complete

This document maps the territory beyond the initial design phases.
The core infrastructure is built (18 MCP tools, 5 languages,
persistent model, LSP integration, codegen runtime, pattern system).
The frontier is about deepening the platform's intelligence and
closing the gap between agent intent and mechanical execution.

## What's Built vs What's Frontier

| Vision (Section 9) | Status | Gap |
|---|---|---|
| 9.1 Core shift | Partial | `ctx` API exists but nodes are thin — no `.fields()`, `.method()`, `.addField()` |
| 9.2 Teach by recipe | Done | Basic $substitution works |
| 9.2 Teach by example | Not started | Reverse-engineering templates from exemplar code |
| 9.3 Code generator runtime | Partial | `ctx` has query/find/edit but lacks semantic operations and LSP bridging |
| 9.4 Persistent model | Done | SQLite, file watching, incremental parse |
| 9.5 LSP integration | Done | hover, definition, references, diagnostics as MCP tools |
| 9.6 Pre-flight validation | Partial | Parse check done; structural check and convention check not started |
| 9.7 Change decomposition | Not started | Sharding large changes by package/dependency |
| 9.8 Interaction model | Partial | Program and Pattern levels work; Intent level not yet |

## Frontier Tiers

### Tier 1 — High Impact, Buildable Now

These directly increase what agents can accomplish with the platform.

#### A. Rich `ctx` API

**Problem:** The current `ctx` object returns flat JSON objects with
`name`, `kind`, `text`, `startByte`, `endByte`. An agent writing a
codegen program has to do string manipulation on raw source text to
accomplish structural operations. The vision shows:

```javascript
const user = ctx.findType("User");
user.fields();                    // → [{name: "id", type: "u64"}, ...]
user.method("new");               // → node for the constructor
user.addField("email", "String"); // semantic edit
```

**What's needed:**
- **Structural navigation** on nodes: `.fields()`, `.methods()`,
  `.parameters()`, `.returnType()`, `.body()`, `.decorators()`.
  These traverse Tree-sitter children using field names, returning
  structured data rather than raw text. Language-specific: a Rust
  struct's fields are different from a Python class's attributes.
  The adapter trait needs methods to map these concepts.

- **Semantic mutation** on nodes: `.addField(name, type)`,
  `.addMethod(name, params, body)`, `.addImport(path)`,
  `.setReturnType(type)`. These generate syntactically correct code
  for the target language and splice it at the right position.
  The adapter provides the code template per language.

- **Relationship queries**: `.implementors()` (types that implement
  this trait/interface), `.superclass()`, `.callers()`. These
  combine the symbol index with LSP queries.

**Impact:** Transforms the codegen tool from "powerful but awkward"
to "natural for structural refactoring." An agent can write a 10-line
program that restructures a type and all its consumers, instead of
a 50-line program doing string surgery.

**Approach:** Extend the JS helpers in `codegen.rs` to build richer
node objects. Add per-language structural methods to the adapter
trait (e.g., `field_query`, `method_query`, `struct_body_template`).
Use LSP for relationship queries where the symbol index is
insufficient.

#### B. Teach by Example

**Problem:** `teach_recipe` requires the agent to specify the exact
sequence of tool operations. This is fine for simple recipes but
tedious for complex patterns. The agent already understands the
pattern by looking at the code — it should be able to say "this
is the pattern, these are the variables."

**What's needed:**
- `teach_by_example` tool accepting an exemplar file path,
  parameter names mapped to their values in the exemplar, and
  a list of related files.

- Template extraction: parse the exemplar with Tree-sitter,
  find all subtrees whose text matches a parameter value,
  replace those subtrees with template holes. Preserve the
  rest of the tree structure exactly.

- Multi-file templates: the `also_affects` field triggers the
  same extraction on related files (route registration, test
  scaffolding, etc.).

- Instantiation: substitute parameter values into template holes,
  generate syntactically correct code using the Tree-sitter
  structure as a guide for indentation and formatting.

**Challenges:**
- A parameter value might appear in multiple syntactic contexts
  (type name, variable name, string literal, file path). Each
  context needs different substitution rules.
- The exemplar might contain code that's specific to the example
  and shouldn't be templated. The agent needs to be able to mark
  "this part is fixed, this part varies."
- Multi-file extraction requires matching corresponding structures
  across files (e.g., "the route registration line that references
  the same endpoint name").

**Approach:** Start with a simpler version — text-level
substitution of parameter values (like `teach_recipe` but derived
from an exemplar rather than manually specified). Upgrade to
structural substitution later as we learn which cases matter.

#### C. Convention Invariants

**Problem:** Projects have unwritten rules: every public function
has a doc comment, every endpoint has a test, every error type
implements `Display`. Agents learn these by reading code but
forget them across sessions. The platform should enforce them.

**What's needed:**
- `teach_convention` tool accepting a name, description, and a
  check (a query or codegen program that returns violations).

- Convention checking on `apply`: before writing changes, run all
  conventions and report violations.

- Convention checking on `parse`: when the model loads, scan for
  existing violations and report them.

- Convention-aware code generation: when `instantiate` or `codegen`
  produces output, automatically include convention-required
  elements (doc comments, tests, trait impls).

**Example:**
```json
{"tool": "teach_convention",
 "name": "public_functions_documented",
 "description": "Every public function must have a doc comment",
 "check_program": "
   var fns = ctx.query({kind: 'function'});
   var violations = [];
   for (var i = 0; i < fns.length; i++) {
     if (fns[i].text.includes('pub ') && !fns[i].text.startsWith('///')) {
       violations.push(fns[i].file + ':' + fns[i].startLine + ': ' + fns[i].name);
     }
   }
   return violations;
 "}
```

**Stored in SQLite alongside recipes.** Conventions and recipes
together form the project's "institutional memory" — the platform
knows not just how to do things but what must be done.

### Tier 2 — Structural Improvements

These improve reliability and power of existing capabilities.

#### D. Structural Pre-flight Checks

Beyond "does it parse?" — detect problems before writing to disk:

- **Dangling references**: renamed a symbol but a call site still
  uses the old name. Cross-reference the symbol index before and
  after the edit — any reference that pointed to a now-missing
  definition is a violation.

- **Missing imports**: moved a symbol to another file but didn't
  add an import at the use sites. Detectable by checking whether
  all references to the moved symbol have a visible import path.

- **Orphaned code**: deleted a function but its tests still exist
  (and will fail). Detectable by naming convention matching.

These use the symbol index and Tree-sitter queries, not LSP, so
they're fast and available even when no LSP server is running.

#### E. LSP on `ctx`

Bridge LSP tools into the codegen runtime so JS programs can make
decisions based on semantic information:

```javascript
const typ = ctx.typeOf(someNode);     // → "Vec<String>"
const impls = ctx.implementors("Handler"); // → [node, node, ...]
const def = ctx.definition(someNode);      // → node in another file
```

This requires the codegen runtime to hold a reference to the
`LspManager` and dispatch queries during JS execution. The tricky
part is that LSP operations are synchronous from the JS
perspective but involve IPC with the LSP server — need careful
timeout handling to avoid hanging if the LSP is slow.

#### F. Incremental Tree-sitter Re-parse

Tree-sitter supports telling the parser which byte ranges changed
and re-parsing only affected subtrees. Currently we re-parse entire
files. For large files (thousands of lines), incremental re-parsing
would be significantly faster.

**Implementation:** When the file watcher detects a change, diff the
new content against the old to find changed byte ranges, then use
`tree.edit()` + `parser.parse(new_source, Some(&old_tree))` for
incremental parsing.

### Tier 3 — Scale and Deployment

#### G. Change Decomposition

For large-scale changes (rename across 200 files, API migration):

- **Shard** the change by directory, package, or dependency boundary.
- **Verify** each shard independently (parse check, LSP diagnostics).
- **Apply** shards independently — if shard 47 fails, roll back
  just that shard, not the entire change.
- **Order** shards by dependency (change libraries before consumers).

**Implementation:** After `codegen` or `transform_batch` produces a
set of file changes, group them by the nearest `Cargo.toml` /
`package.json` / `go.mod` / build boundary. Validate each group
independently. Expose as a `shard` tool that takes pending changes
and returns grouped shards.

#### H. Multi-workspace Support

Track multiple project roots simultaneously:
- Monorepo with multiple packages
- Related repos that share types or interfaces
- Cross-repo rename / migration

**Implementation:** The `CodebaseModel` becomes a collection of
workspace roots, each with its own forest, store, and LSP
connection. Cross-workspace queries search all workspaces.

#### I. Automatic LSP Management

Currently the user must install LSP servers manually. The platform
should:
- Detect which LSP servers are missing at startup
- Report which languages have degraded capability
- Optionally install missing servers (`npm install -g`,
  `cargo install`, `go install`, etc.)
- Version-check running servers and warn on outdated versions

### Tier 4 — Ecosystem

#### J. Plugin System

- **Language plugins**: user-defined Tree-sitter grammars loaded at
  runtime (`.so`/`.dylib`), with adapter configuration via YAML/JSON.
  Enables community support for languages we don't bundle.

- **Recipe marketplace**: share recipes across projects. A recipe
  specifies its language requirements and any conventions it
  assumes. `polyrefactor install-recipe <url>`.

- **Convention packs**: pre-built convention sets for common
  frameworks (e.g., "Axum endpoint conventions", "React component
  conventions").

#### K. Agent Prompt Generation

The server generates a rich system prompt / tool description that
makes agents immediately effective:

- Available tools with usage examples
- Project-specific recipes and what they do
- Active conventions and their current violation count
- Codebase summary (languages, file count, top-level structure)
- Recently modified areas (from file watcher history)

This replaces the flat `instructions` string with context that
turns the MCP server from "a tool the agent needs to learn" to
"a tool that teaches the agent how to use it."

#### L. WASM Build

Compile the core (Tree-sitter + transform engine + QuickJS) to
WASM for a browser-based playground. Excludes file I/O, SQLite,
and LSP, but allows interactive testing of transforms and codegen
programs against pasted code.

## Critical Path

The highest-value sequence for agent productivity:

```
A (Rich ctx API)
  → E (LSP on ctx)
    → B (Teach by example)
      → C (Convention invariants)
        → K (Agent prompt generation)
```

Each step makes the platform significantly more useful:
- **A** makes codegen natural for structural refactoring
- **E** gives codegen programs semantic awareness
- **B** lets agents teach patterns without manual recipe authoring
- **C** gives the platform institutional memory
- **K** makes new agents immediately productive

Everything else (D, F, G, H, I, J, L) is valuable but orthogonal
— it can be built in parallel or deferred without blocking the
critical path.

## Design Principles for the Frontier

1. **The agent decides what; the platform decides how.** Every new
   capability should move mechanical work from agent to platform,
   not add new things the agent needs to learn.

2. **Degrade, don't fail.** If an LSP isn't available, structural
   queries still work. If a convention check fails, the change is
   still previewable. The platform should always be useful, with
   more capability when more infrastructure is present.

3. **Teach once, use forever.** Recipes, conventions, and patterns
   are the platform's value accumulator. Every interaction that
   teaches the platform something makes all future interactions
   faster — for this agent, other agents, and other projects.

4. **Diff is the universal interface.** Every operation produces a
   previewable diff. The agent and user always see what will happen
   before it happens. This is the trust foundation.

5. **Code is the language.** Agents think in code. The platform's
   input is code (JS programs, exemplar files, template code).
   The platform's output is code (diffs, generated files). No
   custom DSLs, no visual builders, no configuration languages.
