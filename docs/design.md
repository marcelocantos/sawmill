# PolyRefactor Design Document

**Project Name:** PolyRefactor
**Version:** 0.5
**Date:** April 2026
**Status:** Revised — two-tier transform model, orthogonal match/act

## 1. Overview

PolyRefactor is an MCP server that models a codebase as a forest of
Concrete Syntax Trees (CSTs) and exposes safe, programmable,
multi-language structural transformations to AI coding agents. It
allows agents to request refactoring operations without repeatedly
processing or regenerating large volumes of source code in their
context windows.

The server parses source files via Tree-sitter, exposes transformation
tools through the MCP protocol, and writes changes back to the
original files using a **minimal, range-based patching strategy** that
produces clean, git-diff-friendly output while preserving original
formatting, comments, whitespace, and language-specific constructs
(e.g., C++ preprocessor directives).

### Core Goals
- Support multiple languages with good (but not necessarily dominant)
  C++ coverage.
- Work directly with Tree-sitter's concrete node types — no
  artificial abstraction layer. Cross-language operations use
  Tree-sitter queries and language-adapter traits.
- Minimize token usage for AI models by handling mechanical rewriting
  deterministically.
- Ensure output changes are small and human-like for easy review and
  merging.
- Serve as an MCP server that AI coding agents can call as a tool.

## 2. Requirements

### Functional Requirements
- Parse an entire directory or selected files into a `Forest`
  containing one parsed tree per file.
- Expose Tree-sitter node types directly. Provide cross-language
  capability through Tree-sitter queries and a `LanguageAdapter` trait
  that maps language-specific patterns to common operations (e.g.,
  "find all function definitions" dispatches to the right query per
  grammar).
- Support a two-tier transformation model: named operations for
  common refactorings, and a general-purpose match/act engine with
  orthogonal matching (abstract or raw Tree-sitter queries) and
  action (declarative code injection or programmable JavaScript
  functions) strategies.
- Use immutable tree representation — transforms produce new trees,
  originals are retained for diffing.
- Rewrite transformed trees back to source files by copying unchanged
  regions verbatim and only regenerating modified portions.
- Produce output that results in minimal, readable git diffs.
- Expose all functionality as MCP tools.
- Provide a CLI for direct invocation and testing.
- Support dry-run, diff preview, and safe application modes.

### Non-Functional Requirements
- **Multi-language support**: Python, TypeScript/JavaScript, Rust, Go,
  Java, C/C++, and extensible to others. C++ must work reliably for
  common constructs (templates, preprocessor).
- **Performance**: Near-instant startup. Efficient handling of large
  repositories via parallel processing and Tree-sitter's speed.
- **Fidelity**: Preserve as much original source layout as possible.
- **Extensibility**: Easy addition of new languages via Tree-sitter
  grammars and `LanguageAdapter` implementations.
- **Safety**: Validation, error recovery, and preview capabilities.

### Out of Scope
- Deep semantic analysis (type checking, control flow) — can be
  layered later or delegated to LSP servers.
- Replacement for general-purpose formatters/linters.
- Full IDE features.

## 3. High-Level Architecture

```
AI Agent ──MCP──▶ PolyRefactor Server
                    │
                    ├─ Parsing Layer (Tree-sitter)
                    │    └─ Per-language grammars
                    ├─ Forest (immutable parsed files)
                    ├─ Language Adapters (query dispatch)
                    ├─ Transform Engine
                    │    ├─ Named Operations (rename, extract, etc.)
                    │    └─ Match/Act Engine
                    │         ├─ Match: abstract or raw query
                    │         └─ Act: declarative or JS function
                    ├─ Rewrite Engine (range-based patching)
                    └─ Output (diffs, patches, in-place writes)
```

Pipeline:

1. **Parsing** → Tree-sitter CSTs (one per file) with original source
   bytes and byte ranges retained.
2. **Forest Construction** → `Forest` holding all `ParsedFile`
   objects, indexed by path and language.
3. **Language Adapters** → Per-language `LanguageAdapter`
   implementations that provide Tree-sitter queries for common
   structural patterns (function defs, class defs, call sites, imports,
   etc.) without imposing a unified node type hierarchy.
4. **Transformation** → Two-tier model: named operations for common
   refactorings, and a general-purpose match/act engine. Each
   transform produces a new immutable tree; the original is retained.
5. **Minimal Rewrite** → Range-based patching engine that splices
   original source bytes with new content for changed nodes only.
6. **Output** → Unified diffs, patch files, or in-place updates
   (with backup).

## 4. Core Components

### 4.1 Parsing Layer
- Use **Tree-sitter** via the `tree-sitter` Rust crate.
- Each file is parsed into a Tree-sitter `Tree` with associated
  original source bytes stored alongside.
- Language grammars compiled into the binary (via `tree-sitter-*`
  Rust crates) or loaded dynamically from `.so`/`.dylib` files for
  extensibility.
- Support for error-tolerant parsing (Tree-sitter's default
  behaviour).

### 4.2 Forest and ParsedFile
- `Forest`: Container for multiple `ParsedFile` instances.
  - Constructed from a directory path (with gitignore-aware
    traversal), a file list, or incrementally.
  - Indexed by path and by language for efficient querying.
- `ParsedFile`:
  - `path: PathBuf`
  - `language: Language` (enum + grammar reference)
  - `original_source: Vec<u8>` (owned, immutable)
  - `tree: Tree` (Tree-sitter tree, immutable after parse)

### 4.3 Language Adapters

Rather than wrapping Tree-sitter nodes in a unified type hierarchy,
each supported language implements a `LanguageAdapter` trait:

```rust
trait LanguageAdapter {
    /// Tree-sitter query for function/method definitions.
    fn function_def_query(&self) -> &str;
    /// Tree-sitter query for class/struct/type definitions.
    fn type_def_query(&self) -> &str;
    /// Tree-sitter query for call expressions.
    fn call_expr_query(&self) -> &str;
    /// Tree-sitter query for import/include statements.
    fn import_query(&self) -> &str;
    /// Map a capture name to the "name" node of a definition.
    fn name_capture(&self) -> &str { "name" }
    // ... extensible with more patterns as needed
}
```

This approach:
- **Preserves full Tree-sitter fidelity** — agents and transforms
  work with real CST nodes, not lossy abstractions.
- **Is naturally extensible** — adding a language means writing
  queries, not fitting square pegs into a generic type hierarchy.
- **Supports cross-language operations** — "rename all functions
  named X" dispatches the right query per language via the adapter.
- **Allows language-specific depth** — a C++ adapter can expose
  queries for template parameters, preprocessor directives, etc.
  without polluting a shared interface.

### 4.4 Transformation Engine

The transformation engine has two tiers. Named operations provide
convenient shorthand for common refactorings. The match/act engine
provides full generality. Named operations are implemented as sugar
over the match/act engine internally.

#### Named Operations

High-level, symbol-oriented operations. The agent specifies intent
using names and paths — no knowledge of grammar node types required.
The server resolves everything via language adapters.

```json
{"tool": "rename", "symbol": "old_api", "to": "new_api", "scope": "src/"}

{"tool": "add_parameter", "function": "connect",
 "param": {"name": "timeout", "type": "Duration",
           "default": "Duration::from_secs(30)"},
 "after": "host"}

{"tool": "extract_function", "file": "main.py",
 "start_line": 42, "end_line": 58, "name": "validate_input"}

{"tool": "move_symbol", "symbol": "parse_config",
 "from": "src/main.rs", "to": "src/config.rs"}
```

Available operations:
- `rename` — rename a symbol across files.
- `add_parameter` / `remove_parameter` — modify function signatures.
- `extract_function` — extract a range into a new function.
- `inline` — inline a function at all call sites.
- `move_symbol` — move a definition between files, updating imports.
- `wrap` / `unwrap` — wrap or unwrap matched code.
- `replace_body` — replace the body of a function or method.
- `change_type` — change the type of a variable or parameter.

#### Match/Act Engine

The general-purpose engine combines two orthogonal dimensions:
**matching** (how to find nodes) and **acting** (what to do with
them). The agent picks one strategy from each dimension independently.

**Matching strategies:**

*Abstract matching* — the agent describes patterns using abstract
node kinds resolved per-language by the adapter. This is the default
and requires no grammar knowledge.

Fields:
- `kind` — abstract node kind (`function`, `class`, `call`,
  `import`, `variable`, `statement`, etc.), resolved to
  language-specific Tree-sitter types by the adapter.
- `name` — symbol name, with glob support (`process_*`,
  `*Handler`).
- `name_regex` — regex match on symbol name.
- `file` / `scope` — restrict to specific files or directories.
- `has_decorator` / `has_annotation` — language-specific filters.
- `parent` — match only within a parent of given kind/name.

*Raw query matching* — the agent supplies a Tree-sitter S-expression
query directly. This is language-specific and exists for cases where
abstract matching can't express the desired pattern.

```json
{"raw_query": "(call_expression function: (member_expression object: (identifier) @obj (#eq? @obj \"console\")) @call)",
 "capture": "call"}
```

**Acting strategies:**

*Declarative actions* — the agent specifies an action and literal
code to inject. No programming required.

Actions:
- `replace` — replace the matched node with `code`.
- `wrap` — wrap with `before`/`after`.
- `unwrap` — remove wrapper, keep contents.
- `prepend_statement` / `append_statement` — inject before/after.
- `remove` — delete the matched node.
- `replace_name` — rename the matched node's identifier.
- `replace_body` — replace the body of a matched function/class.

*Programmable actions (embedded JavaScript)* — the agent supplies a
JavaScript function that receives each matched node and returns a
transformation. The function runs in an embedded **QuickJS** sandbox
— no filesystem, no network, deterministic execution.

The server exposes matched nodes to JavaScript as objects with a
consistent API:

```typescript
interface TransformNode {
  // Identity
  kind: string;           // abstract kind ("function", "call", etc.)
  tsKind: string;         // raw Tree-sitter kind ("function_definition")
  name: string | null;    // symbol name if applicable
  text: string;           // full source text of this node

  // Structure
  children: TransformNode[];
  parent: TransformNode | null;
  body: string | null;    // body text for functions/classes/blocks
  parameters: Parameter[];// for functions
  arguments: TransformNode[]; // for calls
  returnType: string | null;

  // Location
  file: string;
  startLine: number;
  endLine: number;

  // Mutation (returns new node — immutable)
  replaceText(newText: string): TransformNode;
  replaceBody(newBody: string): TransformNode;
  replaceName(newName: string): TransformNode;
  remove(): null;
  wrap(before: string, after: string): TransformNode;
  insertBefore(code: string): TransformNode;
  insertAfter(code: string): TransformNode;

  // Language-specific access
  field(name: string): TransformNode | null; // Tree-sitter field
  query(pattern: string): TransformNode[];   // sub-query
}
```

The function returns:
- The original `node` unchanged → no modification.
- A mutated node (via `.replaceBody()` etc.) → apply the change.
- `null` → delete the node.
- A string → replace the node's text entirely.

**Combining match and act:**

Any matching strategy can be combined with any acting strategy. The
four combinations serve different use cases:

| | Declarative action | JS function |
|---|---|---|
| **Abstract match** | Simple bulk edits — no grammar or programming knowledge needed. | Complex conditional logic over familiar node kinds. |
| **Raw query match** | Precise grammar-level targeting with simple modifications. | Full power — arbitrary matching and arbitrary logic. |

Examples:

```json
// Abstract match + declarative action
{"tool": "transform",
 "match": {"kind": "function", "has_decorator": "deprecated"},
 "action": "remove"}

// Abstract match + JS function
{"tool": "transform",
 "match": {"kind": "function"},
 "transform_fn": "
   (node) => {
     if (node.name.startsWith('test_')) return node;
     return node.replaceBody(`
       const _start = performance.now();
       ${node.body}
       log_timing('${node.name}', performance.now() - _start);
     `);
   }
 "}

// Raw query + declarative action
{"tool": "transform",
 "raw_query": "(call_expression function: (member_expression object: (identifier) @obj property: (property_identifier) @method) (#eq? @obj \"console\") (#eq? @method \"log\")) @call",
 "capture": "call",
 "action": "remove"}

// Raw query + JS function
{"tool": "transform",
 "raw_query": "(function_definition name: (identifier) @name body: (block) @body) @func",
 "capture": "func",
 "transform_fn": "(node) => node.body.includes('unsafe') ? node.wrap('// SAFETY: reviewed\\n', '') : node"}
```

#### Transform Composition

Named operations and match/act transforms produce the same internal
representation (an immutable transformed tree), so they compose
naturally. An agent can chain multiple transforms:

```json
{"tool": "transform_batch", "transforms": [
  {"rename": {"symbol": "oldName", "to": "newName"}},
  {"match": {"kind": "function", "name": "newName"},
   "action": "prepend_statement",
   "code": "validate_args();"},
  {"match": {"kind": "call", "name": "newName"},
   "transform_fn": "(node) => node.arguments.length < 2 ? node.insertArgument('null') : node"}
]}
```

Each transform in the batch sees the result of the previous one.
The entire batch produces a single diff.

All transforms produce new immutable trees. The original tree is
retained on the `ParsedFile` for diffing.

### 4.5 Rewrite Engine

The rewrite engine is the critical component. It produces minimal
patches by comparing the transformed tree against the original source
bytes.

**Algorithm** (handles inter-child gaps, insertions, and deletions):

```rust
fn rewrite(original: &[u8], old_tree: &Tree, new_tree: &Tree) -> Vec<u8> {
    let mut result = Vec::new();
    let mut last_end: usize = 0;

    fn recurse(
        node: &Node,          // new tree node
        orig_node: &Node,     // corresponding old tree node (if any)
        original: &[u8],
        result: &mut Vec<u8>,
        last_end: &mut usize,
    ) {
        // Unchanged subtree: copy original bytes verbatim
        if structurally_equal(node, orig_node) {
            let start = orig_node.start_byte();
            let end = orig_node.end_byte();
            // Copy gap before this node (inter-sibling whitespace, commas, etc.)
            result.extend_from_slice(&original[*last_end..start]);
            // Copy node verbatim
            result.extend_from_slice(&original[start..end]);
            *last_end = end;
            return;
        }

        // Changed leaf: emit new text
        if node.child_count() == 0 {
            let start = orig_node.start_byte();
            result.extend_from_slice(&original[*last_end..start]);
            result.extend_from_slice(node.utf8_text().as_bytes());
            *last_end = orig_node.end_byte();
            return;
        }

        // Changed interior node: recurse into children, preserving
        // inter-child gaps from original source.
        // Handles insertions (no orig counterpart) and deletions
        // (orig child skipped) via alignment.
        let pairs = align_children(node, orig_node);
        for (new_child, orig_child) in pairs {
            match (new_child, orig_child) {
                (Some(nc), Some(oc)) => recurse(nc, oc, original, result, last_end),
                (Some(nc), None) => {
                    // Insertion: emit new text at current position
                    result.extend_from_slice(nc.utf8_text().as_bytes());
                }
                (None, Some(oc)) => {
                    // Deletion: skip original bytes, but copy preceding gap
                    result.extend_from_slice(&original[*last_end..oc.start_byte()]);
                    *last_end = oc.end_byte();
                }
                (None, None) => unreachable!(),
            }
        }
    }

    recurse(&new_tree.root_node(), &old_tree.root_node(), original, &mut result, &mut last_end);

    // Trailing content
    result.extend_from_slice(&original[last_end..]);
    result
}
```

Key design points:
- **Inter-child gaps**: Whitespace, commas, semicolons, and other
  punctuation between children are copied from the original source
  by tracking `last_end` through sibling boundaries.
- **Insertions**: New nodes with no original counterpart emit their
  text at the current position.
- **Deletions**: Original nodes with no new counterpart are skipped
  (their byte range is consumed without copying).
- **`align_children`**: Pairs children between old and new trees
  using a longest-common-subsequence or keyed matching strategy
  (matching by node kind + name where available).
- **`structurally_equal`**: Compares node kind, text, and child
  structure recursively. Results are cached (memoised by node ID
  pair) to avoid redundant traversals.

**Hunk-level post-processing** (optional):
- After rewriting, identify changed byte ranges by diffing against
  the original.
- Optionally run language-native formatters only on changed hunks
  (e.g., `rustfmt` on changed Rust functions, `clang-format` on
  changed C++ regions).

**Output modes**:
- Preview unified diff (default for MCP tool responses).
- Write patch file.
- Apply changes in-place (with `.orig` backup or git stash).

### 4.6 MCP Server Interface

The MCP server exposes tools organised by function:

**Querying tools** (read-only):

| Tool | Description |
|------|-------------|
| `parse` | Parse files/directory into the forest. Returns summary (file count, languages, parse errors). |
| `query` | Run a structural or raw query across the forest. Returns matched nodes with context. |
| `find_symbol` | Find definitions of a symbol by name across languages. |
| `find_references` | Find all references to a symbol (call sites, usages). |
| `languages` | List supported languages and their capabilities. |

**Named operation tools** (return diff preview):

| Tool | Description |
|------|-------------|
| `rename` | Rename a symbol across the codebase. |
| `add_parameter` | Add a parameter to a function signature and optionally to call sites. |
| `remove_parameter` | Remove a parameter from a function signature and call sites. |
| `extract_function` | Extract a code range into a new function. |
| `inline` | Inline a function at all call sites. |
| `move_symbol` | Move a definition between files, updating imports. |
| `replace_body` | Replace the body of a function or method. |
| `wrap` / `unwrap` | Wrap or unwrap matched code. |
| `change_type` | Change the type of a variable or parameter. |

**Match/act tools** (return diff preview):

| Tool | Description |
|------|-------------|
| `transform` | Match (abstract or raw query) + act (declarative action or `transform_fn`). |
| `transform_batch` | Sequence of named ops and/or transforms, applied as a single diff. |

**Lifecycle tools**:

| Tool | Description |
|------|-------------|
| `apply` | Apply a previously previewed transform to disk. |
| `undo` | Revert the last applied transform (restores `.orig` backups). |

All mutating tools default to dry-run mode, returning a unified diff.
The agent must explicitly call `apply` to write changes to disk. This
two-step pattern (preview → apply) gives agents and users a chance to
review before committing.

The server also exposes MCP resources:
- `forest://status` — current parse state, file count, languages.
- `forest://file/{path}` — parsed structure of a specific file.

### 4.7 CLI Interface

For testing and direct invocation:

```sh
# Parse and show forest summary
polyrefactor parse ./src

# Find all functions named "process"
polyrefactor find-symbol process --kind function

# Rename across codebase (dry-run by default)
polyrefactor rename old_name new_name --path ./src

# Structural match + declarative action
polyrefactor transform \
  --match 'kind=call,name=old_api' \
  --action replace_name \
  --code new_api \
  --path ./src

# Raw query + declarative action
polyrefactor transform \
  --raw-query '(call_expression function: (identifier) @fn (#eq? @fn "old_api"))' \
  --capture fn \
  --action replace \
  --code new_api \
  --path ./src

# Match + JS function
polyrefactor transform \
  --match 'kind=function' \
  --transform-fn '(node) => node.name.startsWith("_") ? node.remove() : node' \
  --path ./src

# Apply changes (writes to disk)
polyrefactor apply --path ./src
```

## 5. Technology Choices

- **Language**: Rust
- **Parsing**: `tree-sitter` crate + per-language grammar crates
  (`tree-sitter-python`, `tree-sitter-cpp`, `tree-sitter-typescript`,
  `tree-sitter-rust`, `tree-sitter-go`, `tree-sitter-java`,
  `tree-sitter-c`)
- **MCP**: `mcp-server` or `rmcp` Rust crate (evaluate at
  implementation time)
- **CLI**: `clap` for argument parsing
- **Parallelism**: `rayon` for parallel file processing
- **File traversal**: `ignore` crate (gitignore-aware directory
  walking, same as ripgrep)
- **Diffing**: `similar` crate for unified diff generation
- **Embedded JS**: `rquickjs` crate (QuickJS engine) for
  programmable transforms — ~200KB, sandboxed, no external runtime
- **Dependencies**: Minimal. Formatter integration via `subprocess`
  calls to external tools (`clang-format`, `rustfmt`, `ruff`,
  `prettier`, etc.)

### Why Rust
- Self-contained static binary — no runtime, no interpreter, trivial
  to distribute and configure as an MCP server.
- Near-zero startup latency — critical for MCP tool calls that may
  be invoked frequently.
- Tree-sitter is written in C; Rust's FFI is zero-overhead.
- Memory safety without GC, suitable for long-running server process
  holding large forests in memory.
- Strong ecosystem for CLI tools, file I/O, and parallelism.

### Why Tree-sitter nodes directly (no abstraction layer)
- A unified `GenericNode` hierarchy is a leaky abstraction — every
  language has constructs that don't fit (Python decorators, C++
  templates, Go interfaces, Rust lifetimes). The abstraction either
  becomes a lowest-common-denominator that loses important structure,
  or it accumulates language-specific escape hatches until it's more
  complex than the thing it abstracts.
- Tree-sitter's query language already provides the cross-language
  pattern matching capability. Language adapters supply the right
  queries per grammar without imposing type-level uniformity.
- Agents interacting via MCP don't need to know Rust types — they
  see JSON representations of nodes and work with tool parameters.
  The abstraction boundary is the MCP protocol, not a type hierarchy.

### Why two tiers (not four)
- The original four-tier model (named ops → structural match →
  programmable JS → raw queries) had a false hierarchy: raw
  Tree-sitter queries are *less* general than JS functions, not more.
  They're a lower-abstraction matching mechanism, not a higher-
  generality transform mechanism.
- Recognising that matching and acting are orthogonal dimensions
  collapses the model: any match strategy (abstract or raw) combines
  with any act strategy (declarative or JS). This gives four
  combinations from two knobs instead of four tiers.
- Named operations remain as convenient sugar — they're the 80% path
  and shouldn't require the agent to think about matching at all.

## 6. Implementation Phases

### Phase 1 (MVP)
- Rust project setup with Tree-sitter integration
- `Forest` and `ParsedFile` — parse a directory of Python files
- Identity round-trip test: parse → rewrite with no transforms →
  assert byte-identical output
- Single named operation: `rename` (single file)
- Rewrite engine with inter-child gap handling
- Unified diff output
- Basic CLI (`parse`, `rename`)

### Phase 2
- MCP server with querying tools + `rename` + `apply`
- Match/act engine: abstract matching + declarative actions
- Language adapters for Python + TypeScript + C++
- Cross-file rename
- Preview → apply two-step workflow
- Hunk-level formatter integration

### Phase 3
- Embedded QuickJS runtime for programmable actions
- `TransformNode` API exposed to JS
- Raw query matching
- `transform_batch` for composed transforms
- Additional named operations (`extract_function`, `inline`,
  `move_symbol`, `add_parameter`, `remove_parameter`, `replace_body`)
- Cross-file reference tracking (basic symbol index)

### Phase 4 — Persistent Codebase Model
- SQLite-backed store for file metadata, symbol index, and
  cross-references (persistent across sessions)
- Stateful MCP server — holds a `Forest` in memory, `parse`
  loads from cache and incrementally updates changed files,
  all tools operate against the live model
- File watching via `notify` — re-parse changed files and
  update indexes automatically
- Symbol index — queryable index of all symbols with
  cross-references, replacing ad-hoc Tree-sitter queries

### Phase 5 — Code Generator Runtime
- `ctx` API in QuickJS — cross-file discovery and coordinated
  edits from a single JS program
- Pattern teaching (recipe-based, then exemplar-based)
- Pre-flight parse validation

### Phase 6 — LSP Integration + Semantics
- Connect to language servers for type info, references, impls
- Pre-flight compile validation via LSP diagnostics
- Richer `ctx` API with semantic queries
- Change decomposition for large-scale edits

## 7. Testing Strategy

- **Round-trip fidelity**: For every supported language, parse a
  corpus of real-world files, apply an identity transform, and assert
  byte-identical output. This is a Phase 1 gate.
- **Transform correctness**: For each named operation, test against
  known input/output pairs across languages.
- **Diff minimality**: Measure diff size for common transforms and
  assert it's within a bound (e.g., rename produces only the changed
  identifiers in the diff, not surrounding context).
- **Real-world corpora**: Use popular open-source projects as test
  inputs (e.g., CPython, TypeScript compiler, Linux kernel headers
  for C++).
- **MCP integration tests**: Test the full MCP tool-call flow
  (parse → transform → preview → apply) against a test codebase.
- **JS sandbox tests**: Verify QuickJS isolation — no filesystem,
  no network, bounded execution time. Test `TransformNode` API
  against each supported language.

## 8. Risks and Mitigations

- **Fidelity variations across languages**: Round-trip identity test
  as a Phase 1 gate catches issues early. Measure diff size on real
  files.
- **C++ complexity** (templates, macros): Provide language-specific
  adapter queries; preserve preprocessor blocks verbatim where
  Tree-sitter's error-tolerant parsing retains them.
- **Performance on huge repos**: `rayon` for parallel file processing;
  `ignore` crate for fast traversal; incremental re-parsing in
  Phase 4.
- **Grammar differences**: Language adapters absorb the variation —
  each adapter is tested independently against its language's idioms.
- **MCP protocol evolution**: Isolate MCP layer behind a trait so the
  transport can be swapped without touching core logic.
- **JS sandbox escapes**: QuickJS is well-audited and sandboxed by
  default. Add execution time limits and memory caps. No host
  bindings beyond the `TransformNode` API.

## 9. Long-Term Vision: Codebase Operations Platform

The current tool handles one layer: **find AST nodes → apply
mechanical edits**. The long-term vision is a **codebase operations
platform** — the thing that stands between an agent's intent and
the filesystem, handling all mechanical complexity so the agent
focuses entirely on what to do, never on how to do it.

### 9.1 The Core Shift

Today, agents write code. They read files, understand context,
generate new code, and apply edits — spending most of their tokens
on bookkeeping (finding the right location, preserving formatting,
maintaining consistency across files) rather than on the creative
decisions that actually require intelligence.

The platform absorbs all the bookkeeping. The agent becomes a
**programmer of code generators** — it writes small programs that
describe intent, and the platform executes them against a live,
semantic model of the codebase.

### 9.2 Agent-Taught Patterns

Agents understand codebases deeply but waste tokens re-deriving
the same structural patterns across sessions. The platform lets
agents teach patterns that persist:

**Teaching by example:** The agent points at existing code, names
the variable parts, and the platform extracts a reusable template.

```json
{"tool": "teach_by_example",
 "name": "api_endpoint",
 "exemplar": "src/handlers/users.rs",
 "parameters": {
   "name": "users",
   "entity": "User",
   "path": "/users"
 },
 "also_affects": [
   "src/routes/mod.rs",
   "tests/api/"
 ]}
```

The platform uses Tree-sitter to parse the exemplar, identify
which subtrees contain parameter values, and extract a structural
template. The `also_affects` field tells it to look for
corresponding patterns in related files (route registration,
tests, etc.).

**Teaching by recipe:** The agent composes existing tool operations
into a reusable sequence with variables.

```json
{"tool": "teach_recipe",
 "name": "add_field",
 "params": ["struct", "field", "type", "default"],
 "steps": [
   {"action": "add_parameter",
    "function": "$struct",
    "param_name": "$field", "param_type": "$type"},
   {"action": "transform", "kind": "function", "name": "new",
    "parent": "$struct",
    "transform_fn": "(node) => /* add $field to constructor */"},
   {"action": "transform", "kind": "function", "name_regex": "^test_",
    "transform_fn": "(node) => /* add $field to test fixtures */"}
 ]}
```

**Instantiation** is mechanical — the agent (or a different agent
in a later session) invokes the pattern with specific values and
the platform produces the code:

```json
{"tool": "instantiate", "pattern": "api_endpoint",
 "params": {"name": "products", "entity": "Product",
            "path": "/products"}}
```

Patterns are persistent (stored in SQLite per-project). The agent
teaches once, the platform remembers indefinitely.

### 9.3 Code Generator Runtime

The `transform_fn` evolves from operating on a single node to
operating on the entire codebase model. The agent writes a
JavaScript program — a **code generator** — that the platform
executes:

```javascript
(ctx) => {
  const user = ctx.findType("User");
  const fields = user.fields();
  const constructor = user.method("new");
  const tests = ctx.query({kind: "function", name: "test_*user*"});

  // Add field
  user.addField("avatar_url", "Option<String>");

  // Update constructor
  constructor.addParameter("avatar_url", "Option<String>", "None");
  constructor.appendToBody("avatar_url,");

  // Update all test fixtures
  for (const test of tests) {
    test.transform(node => {
      if (node.text.includes("User {")) {
        return node.replaceText(
          node.text.replace("}", "avatar_url: None, }")
        );
      }
      return node;
    });
  }
}
```

The `ctx` object exposes:
- **Discovery** — `findType`, `findFunction`, `query`, `references`
- **Semantic projection** — `.fields()`, `.parameters()`,
  `.returnType()`, `.traitImpls()` — structured data, not raw text
- **Edit primitives** — `addField`, `addParameter`,
  `appendToBody`, `addImport` — semantic-level mutations
- **Cross-file coordination** — a single program touches multiple
  files atomically
- **Validation** — `ctx.willParse()` checks the result before
  committing

### 9.4 Persistent Codebase Model

The platform maintains a **live, indexed model** of the codebase:

**First parse:** Walk the directory, parse all files, build
indexes (symbol table, cross-references, type information from
LSP), persist to SQLite.

**File watching:** `notify` crate monitors the filesystem.
Changed files are re-parsed incrementally (Tree-sitter supports
this natively — you specify which byte ranges changed and it
re-parses only affected subtrees). Indexes are updated in place.

**In-memory hot cache:** The forest + indexes live in memory for
sub-millisecond queries. SQLite is the durable backing store for
cross-session persistence.

**Session continuity:** An agent starting a new session gets
immediate access to the full codebase model without re-scanning.
The platform loads from SQLite, checks mtimes, incrementally
updates changed files, and is ready.

The SQLite store holds:
- Parsed file metadata (path, language, mtime, content hash)
- Symbol index (name → file, line, kind, scope)
- Cross-references (definition → usages)
- Cached LSP results (type info, trait impls, diagnostics)
- Learned patterns and recipes
- Project conventions and invariants

### 9.5 LSP Integration for Semantics

Tree-sitter gives us syntax. LSP servers give us semantics.
Rather than reimplementing type inference per language, the
platform connects to existing LSP servers (rust-analyzer,
tsserver, gopls, pyright, clangd) and proxies semantic queries:

- **Type information** — `textDocument/hover` → "this variable
  is a `Vec<String>`"
- **Go to definition** — `textDocument/definition` → precise
  cross-file symbol resolution
- **Find implementations** — `textDocument/implementation` →
  "these types implement this trait/interface"
- **References** — `textDocument/references` → all usages, not
  just syntactic name matches
- **Diagnostics** — after applying edits in memory, ask the LSP
  if the result has errors before writing to disk

This gives code generator programs access to rich semantic
information (`ctx.typeOf(expr)`, `ctx.implementors("Handler")`)
without us building language-specific analysis.

### 9.6 Pre-flight Validation

Before applying changes, the platform can verify:

1. **Parse check** — does every modified file still parse? This
   is fast (Tree-sitter) and catches basic syntax errors.
2. **Structural check** — are there dangling references? (renamed
   a function but missed a call site)
3. **LSP check** — feed the modified source to the LSP and check
   for diagnostic errors. This catches type errors, missing
   imports, and other semantic issues.
4. **Convention check** — does the result satisfy the project's
   taught patterns and invariants?

Validation happens before `apply`, so the agent (and user) see
any issues in the diff preview, not after files are written.

### 9.7 Change Decomposition

Inspired by Google's Rosie tool for large-scale changes across
their monorepo: a single logical change may need to be split
into independent, individually-testable units.

The platform can **shard** a large change by:
- Directory or package boundary
- Ownership (if configured)
- Dependency order (change libraries before dependents)

Each shard is independently verified (parses, passes LSP checks),
can be applied and rolled back independently, and produces its
own diff for review.

This matters less for single-repo work but becomes critical when
the platform is used for large-scale migrations — "update all
usages of API v1 to API v2 across 200 files" needs to land
safely even if some files have edge cases.

### 9.8 Interaction Model Summary

The agent interacts with the platform at three levels of
abstraction, choosing the one that fits:

| Level | Agent provides | Platform handles |
|---|---|---|
| **Intent** | "Add field X to struct Y" | Discovery, editing all affected locations, validation |
| **Program** | JS code generator against `ctx` | Execution, coordination, validation, file I/O |
| **Pattern** | "Do it like the users endpoint" | Template extraction, parameterised instantiation |

All three levels produce the same output: a set of verified,
previewable file changes that the agent reviews as a diff and
commits with `apply`.

The platform is the **mechanical layer** — fast, precise,
consistent, persistent. The agent is the **semantic layer** —
understanding intent, making design decisions, choosing what to
build. The boundary between them is the `ctx` API and the
pattern/recipe system.
