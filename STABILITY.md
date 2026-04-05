# Canopy Stability

**Current version:** 0.3.0 (pre-1.0)

## Stability Commitment

A 1.0 release signals a backwards-compatibility contract. From that
point, every element of the public interaction surface documented below
will follow semantic versioning: breaking changes require a major
version bump, new additive features bump the minor version. Until 1.0,
any part of the surface may change between releases.

## Interaction Surface Catalogue

Each item is marked:

- **Stable** -- design is settled; breaking changes unlikely before 1.0.
- **Needs review** -- works but the interface may change after real-world usage.
- **Fluid** -- actively evolving or stub-only; expect changes.

---

### 1. CLI (clap derive)

#### Subcommand: `canopy parse`

| Element | Type | Default | Status |
|---|---|---|---|
| `path` (positional) | PathBuf | `"."` | Stable |

Parses files and displays a forest summary.

#### Subcommand: `canopy rename`

| Element | Type | Default | Status |
|---|---|---|---|
| `from` (positional, required) | String | -- | Stable |
| `to` (positional, required) | String | -- | Stable |
| `--path` | PathBuf | `"."` | Stable |

Renames a symbol and prints a unified diff.

#### Subcommand: `canopy serve`

No parameters. Runs the MCP server over stdio.

| Element | Status |
|---|---|
| Stdio transport | Stable |

#### Global flags (clap built-in)

| Flag | Status |
|---|---|
| `--version` | Stable |
| `--help` / `-h` | Stable |

---

### 2. MCP Tools

All tools are defined in `src/mcp.rs` via `#[tool(...)]` annotations on
`CanopyServer`.

#### `parse`

Parse source files into the persistent codebase model.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | yes | -- | Stable |

#### `rename`

Rename a symbol across the codebase. Returns a diff preview.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `from` | string | yes | -- | Stable |
| `to` | string | yes | -- | Stable |
| `path` | string | no | `"."` | Stable |
| `format` | bool | no | `false` | Needs review |

#### `query`

Search for structural patterns by abstract node kind.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `kind` | string | yes | -- | Needs review |
| `name` | string | no | `null` | Needs review |
| `file` | string | no | `null` | Stable |

`kind` accepts `"function"`, `"class"`, `"call"`, `"import"`. The set
of supported kinds may expand before 1.0.

#### `find_symbol`

Find all definitions of a symbol by name.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `symbol` | string | yes | -- | Stable |
| `path` | string | no | `"."` | Stable |

#### `find_references`

Find all references (usages) of a symbol by name (syntactic).

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `symbol` | string | yes | -- | Stable |
| `path` | string | no | `"."` | Stable |

#### `transform`

Apply a structural transformation via match/act.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `kind` | string | no | `null` | Needs review |
| `name` | string | no | `null` | Needs review |
| `file` | string | no | `null` | Stable |
| `raw_query` | string | no | `null` | Needs review |
| `capture` | string | no | `null` (first) | Needs review |
| `action` | string | no | `null` | Needs review |
| `code` | string | no | `null` | Needs review |
| `before` | string | no | `null` | Needs review |
| `after` | string | no | `null` | Needs review |
| `transform_fn` | string | no | `null` | Fluid |
| `format` | bool | no | `false` | Needs review |

`action` accepts: `"replace"`, `"wrap"`, `"unwrap"`,
`"prepend_statement"`, `"append_statement"`, `"remove"`,
`"replace_name"`, `"replace_body"`. The set may change.

`transform_fn` is a JavaScript function receiving a node object with
properties (`kind`, `name`, `text`, `body`, `parameters`, `file`,
`startLine`, `endLine`) and mutation methods (`replaceText`,
`replaceBody`, `replaceName`, `remove`, `wrap`, `insertBefore`,
`insertAfter`). The JS node API is fluid and likely to gain new
properties/methods.

#### `transform_batch`

Apply multiple transforms sequentially.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `format` | bool | no | `false` | Needs review |
| `transforms` | array of JSON objects | yes | -- | Needs review |

Each element is either `{"rename": {"from": "...", "to": "..."}}` or a
match/act transform with the same fields as `transform`.

#### `add_parameter`

Add a parameter to a function definition.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `function` | string | yes | -- | Stable |
| `param_name` | string | yes | -- | Stable |
| `param_type` | string | no | `null` | Stable |
| `default_value` | string | no | `null` | Stable |
| `position` | string | no | `"last"` | Needs review |
| `format` | bool | no | `false` | Needs review |

`position` accepts `"first"`, `"last"`, or `"after:<param_name>"`.

#### `remove_parameter`

Remove a parameter from a function definition by name.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `function` | string | yes | -- | Stable |
| `param_name` | string | yes | -- | Stable |
| `format` | bool | no | `false` | Needs review |

#### `teach_by_example`

Teach a reusable pattern from an exemplar file.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `name` | string | yes | -- | Stable |
| `description` | string | no | `""` | Stable |
| `exemplar` | string | yes | -- | Stable |
| `parameters` | object (string -> string) | yes | -- | Needs review |
| `also_affects` | array of strings | no | `[]` | Needs review |

#### `teach_recipe`

Teach a named sequence of transform steps with parameter variables.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `name` | string | yes | -- | Stable |
| `description` | string | no | `""` | Stable |
| `params` | array of strings | yes | -- | Stable |
| `steps` | JSON value | yes | -- | Needs review |

#### `instantiate`

Instantiate a taught recipe or teach-by-example template.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `recipe` | string | yes | -- | Stable |
| `params` | object (string -> string) | yes | -- | Stable |
| `path` | string | no | `"."` | Stable |
| `format` | bool | no | `false` | Needs review |

#### `list_recipes`

List all taught recipes. No parameters.

#### `teach_convention`

Define an enforceable project convention.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `name` | string | yes | -- | Stable |
| `description` | string | no | `""` | Stable |
| `check_program` | string (JavaScript) | yes | -- | Fluid |

The `check_program` JS API (`ctx` object) is shared with `codegen` and
is fluid.

#### `check_conventions`

Scan the codebase for convention violations.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |

#### `list_conventions`

List all taught conventions. No parameters.

#### `get_agent_prompt`

Generate a rich system prompt describing capabilities, recipes, and
conventions. No parameters. Returns the agents-guide content plus
dynamic project-specific recipes and conventions if `parse` has been
called. **Needs review** — new in v0.2.0; output format may evolve.

#### `hover`

Get type information at a position via LSP.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `file` | string | yes | -- | Stable |
| `line` | u32 | yes | -- | Stable |
| `column` | u32 | yes | -- | Stable |

Line and column are 1-based.

#### `definition`

Go to definition at a position via LSP.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `file` | string | yes | -- | Stable |
| `line` | u32 | yes | -- | Stable |
| `column` | u32 | yes | -- | Stable |

#### `lsp_references`

Find all references at a position via LSP.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `file` | string | yes | -- | Stable |
| `line` | u32 | yes | -- | Stable |
| `column` | u32 | yes | -- | Stable |

#### `diagnostics`

Get compile diagnostics for a file from LSP.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `file` | string | yes | -- | Stable |
| `content` | string | no | `null` | Needs review |

#### `codegen`

Execute a JavaScript code generator against the codebase model.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `path` | string | no | `"."` | Stable |
| `program` | string (JavaScript) | yes | -- | Fluid |
| `format` | bool | no | `false` | Needs review |
| `validate` | bool | no | `true` | Needs review |

The `ctx` object API available to `program`:
- `ctx.findFunction(name)` -- array of nodes
- `ctx.findType(name)` -- array of nodes
- `ctx.query({kind, name, file})` -- array of nodes (name supports glob)
- `ctx.references(name)` -- array of call-site nodes
- `ctx.readFile(path)` -- file content string or null
- `ctx.addFile(path, content)` -- create a new file
- `ctx.editFile(path, startByte, endByte, replacement)` -- raw byte-range edit
- Node methods: `replaceText`, `replaceBody`, `replaceName`, `remove`, `insertBefore`, `insertAfter`
- Rich ctx: `fields()`, `methods()`, `addField()`, `addMethod()`, `addImport()`

This API is **fluid** and will gain new methods (see Frontier E: LSP on
`ctx`).

#### `apply`

Apply pending changes to disk.

| Parameter | Type | Required | Default | Status |
|---|---|---|---|---|
| `confirm` | bool | yes | -- | Stable |

Creates `.canopy.bak` backups. Checks conventions and warns on
violations.

#### `undo`

Revert the last applied changes from `.canopy.bak` backups. No
parameters.

---

### 3. File Formats

#### `.canopy/` directory

| File | Purpose | Status |
|---|---|---|
| `store.db` | SQLite database (persistent state) | Needs review |
| `*.canopy.bak` | Backup files created by `apply` | Needs review |

#### `store.db` schema

**Table: `files`**

| Column | Type | Notes |
|---|---|---|
| `path` | TEXT | Primary key |
| `language` | TEXT | NOT NULL |
| `mtime_secs` | INTEGER | NOT NULL |
| `mtime_nanos` | INTEGER | NOT NULL |
| `content_hash` | TEXT | NOT NULL (BLAKE3) |

**Table: `symbols`**

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER | Primary key |
| `name` | TEXT | NOT NULL |
| `kind` | TEXT | NOT NULL |
| `file_path` | TEXT | NOT NULL, FK -> files(path) ON DELETE CASCADE |
| `start_line` | INTEGER | NOT NULL |
| `start_col` | INTEGER | NOT NULL |
| `end_line` | INTEGER | NOT NULL |
| `end_col` | INTEGER | NOT NULL |

Indexes: `idx_symbols_name(name)`, `idx_symbols_file(file_path)`,
`idx_symbols_kind(kind)`.

**Table: `recipes`**

| Column | Type | Notes |
|---|---|---|
| `name` | TEXT | Primary key |
| `description` | TEXT | NOT NULL, default `''` |
| `params_json` | TEXT | NOT NULL (JSON array of param names) |
| `steps_json` | TEXT | NOT NULL (JSON array of transform steps) |

**Table: `conventions`**

| Column | Type | Notes |
|---|---|---|
| `name` | TEXT | Primary key |
| `description` | TEXT | NOT NULL, default `''` |
| `check_program` | TEXT | NOT NULL (JavaScript source) |

Schema status: **Needs review.** The schema will likely gain columns
(e.g., teach-by-example templates are not yet persisted as a distinct
table; symbol kind vocabulary is not formalised).

---

## Gaps and Prerequisites for 1.0

1. **JavaScript runtime API stabilisation.** The `ctx` object (used by
   `codegen`, `transform_fn`, and `teach_convention`) is the most
   fluid surface. The node property set, mutation methods, and ctx
   query methods need to be inventoried, documented, and frozen.

2. **Action vocabulary.** The set of `action` strings accepted by
   `transform` (`"replace"`, `"wrap"`, etc.) is not validated against
   a closed enum -- invalid values produce runtime errors. Needs an
   explicit enum with versioning semantics.

3. **`kind` vocabulary.** The abstract node kinds (`"function"`,
   `"class"`, `"call"`, `"import"`) accepted by `query` and
   `transform` are implicit. Formalise as a documented, closed set.

4. **Schema migration strategy.** `store.db` has no versioning or
   migration mechanism. Adding columns or tables in a future release
   would break existing databases silently. Needs at minimum a
   `schema_version` table and migration runner.

5. ~~**Frontier D (structural pre-flight checks).**~~ Done (v0.2.0).

6. ~~**Frontier E (LSP on `ctx`).**~~ Done (v0.2.0).

7. **Error contract.** MCP tool error responses are unstructured
   strings. A structured error schema (error codes, categories) would
   let agents handle failures programmatically.

8. **Backup format.** The `.canopy.bak` sidecar approach is simple but
   has no multi-step history or atomicity guarantees. Review whether
   the undo model is sufficient for a 1.0 contract.

9. **Language coverage.** Adapters exist for Python, Rust,
   TypeScript, Go, and C++. The adapter trait surface and the
   per-language query behaviour need documented guarantees (which
   `kind` values each language maps, what rename covers, etc.).

10. **`format` flag.** Multiple tools accept `format: bool` but the
    formatter integration (which formatter, how it is discovered) is
    not documented or configurable.

11. **Test coverage for MCP tools.** The codebase has 35 unit tests.
    Integration-level tests exercising the MCP tool surface
    end-to-end (parse -> transform -> apply -> undo cycle) are needed
    before committing to stability.

## Out of Scope for 1.0

- **Plugin system / user-defined language adapters** (Frontier J).
- **Multi-workspace / monorepo support** (Frontier H).
- **Automatic LSP server management** (Frontier I).
- **WASM / browser build** (Frontier L).
- **Change decomposition / sharding** (Frontier G).
- ~~**Agent prompt generation** (Frontier K)~~ -- done (v0.2.0).
- **Incremental Tree-sitter re-parse** (Frontier F) -- current
  whole-file re-parse is fast enough for the target codebase sizes.
- **Deep semantic analysis** (type checking, control flow) beyond what
  LSP provides.
- **Non-stdio MCP transports** (HTTP, SSE). Stdio is sufficient for
  the agent-tool use case.
