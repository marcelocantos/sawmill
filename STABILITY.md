# Stability

## Commitment

Version 1.0 will represent a backwards-compatibility contract. After 1.0,
breaking changes to the CLI interface, MCP tool surface, configuration
conventions, or wire formats require forking into a new product. The
pre-1.0 period exists to get these right.

## Interaction surface catalogue

Snapshot as of v0.9.0. 153 public surface items.

### CLI

| Item | Type | Stability |
|---|---|---|
| `sawmill` (default) | MCP stdio proxy (auto-starts daemon) | **Stable** |
| `sawmill serve` | Background daemon | **Stable** |
| `sawmill version` | Print version | **Stable** |
| `sawmill --help` | Usage | **Stable** |
| `sawmill --help-agent` | Agent guide | **Stable** |
| `--root` (both modes) | string, default cwd | **Stable** |

### MCP tools (33 tools, 99 parameters)

| Tool | Required params | Optional params | Stability |
|---|---|---|---|
| `parse` | — | `path` | **Stable** |
| `rename` | `from`, `to` | `path`, `format` | **Stable** |
| `rename_file` | `from`, `to` | `format` | **Stable** |
| `query` | — | `kind`, `name`, `file`, `raw_query`, `capture`, `path` | **Stable** |
| `find_symbol` | `symbol` | `kind` | **Stable** |
| `find_references` | `symbol` | — | **Stable** |
| `dependency_usage` | `package` | `path` | **Stable** |
| `transform` | — | `path`, `kind`, `name`, `file`, `raw_query`, `capture`, `action`, `code`, `before`, `after`, `transform_fn`, `format` | **Stable** |
| `transform_batch` | `transforms` | `path`, `format` | **Needs review** — `transforms` is a JSON string, not a native array |
| `codegen` | `program` | `path`, `format`, `validate` | **Stable** |
| `apply` | `confirm` | — | **Stable** |
| `undo` | — | — | **Stable** |
| `teach_recipe` | `name`, `steps` | `description`, `params` | **Stable** |
| `instantiate` | `recipe` | `params`, `path`, `format` | **Stable** |
| `list_recipes` | — | — | **Stable** |
| `teach_convention` | `name`, `check_program` | `description` | **Stable** |
| `check_conventions` | — | `path` | **Stable** |
| `list_conventions` | — | — | **Stable** |
| `teach_invariant` | `name`, `rule` | `description` | **Stable** |
| `check_invariants` | — | `path` | **Stable** |
| `list_invariants` | — | — | **Stable** |
| `delete_invariant` | `name` | — | **Stable** |
| `hover` | `file`, `line`, `column` | — | **Stable** |
| `definition` | `file`, `line`, `column` | — | **Stable** |
| `lsp_references` | `file`, `line`, `column` | — | **Stable** |
| `diagnostics` | `file` | `content` | **Stable** |
| `get_agent_prompt` | — | — | **Stable** |
| `teach_by_example` | `name`, `exemplar`, `parameters` | `description`, `also_affects` | **Needs review** — `parameters` and `also_affects` are JSON strings |
| `add_parameter` | `function`, `param_name` | `path`, `param_type`, `default_value`, `position`, `format` | **Stable** |
| `remove_parameter` | `function`, `param_name` | `path`, `format` | **Stable** |
| `add_field` | `type_name`, `field_name`, `field_type`, `default_value` | `path`, `format` | **Stable** |
| `clone_and_adapt` | `source`, `substitutions`, `target_file` | `position`, `format` | **Stable** |
| `migrate_type` | `type_name`, `rules` | `path`, `format` | **Needs review** — pattern language is new, may evolve |

### Configuration conventions

| Item | Value | Stability |
|---|---|---|
| Global socket | `~/.sawmill/sawmill.sock` | **Stable** |
| Store path | `~/.sawmill/stores/<hash>/store.db` | **Stable** |
| Backup dir | `~/.sawmill/backups/<hash>/` | **Stable** |
| Backup suffix | `.bak` | **Stable** |
| Staging suffix | `.new` | **Stable** |
| Languages | Python, TypeScript, Rust, Go, C/C++ | **Stable** (additive only) |
| JS runtime | QuickJS ES5 | **Needs review** — may upgrade to ES2020+ |

### Wire format

| Item | Value | Stability |
|---|---|---|
| Proxy ↔ daemon | mcpbridge RPC over Unix domain socket | **Stable** |
| MCP protocol | JSON-RPC 2.0 (via mcp-go) | **Stable** (standard) |
| Transport (stdio) | stdin/stdout (mcpbridge proxy) | **Stable** |
| Transport (daemon) | Single global Unix domain socket | **Stable** |

## Gaps and prerequisites for 1.0

- **JSON string parameters**: `transform_batch.transforms`,
  `teach_by_example.parameters`, and `teach_by_example.also_affects` are
  passed as JSON-encoded strings rather than native JSON arrays/objects.
  This is an mcp-go limitation — review whether the library supports
  structured parameters before freezing.
- **QuickJS ES version**: The JS runtime is ES5-only (no `let`, `const`,
  arrow functions). Evaluate upgrading before 1.0 — changing later would
  break user-saved recipes and conventions.
- **File watcher robustness**: The watcher is new and lightly tested (4
  tests). Needs soak time before 1.0.
- **`migrate_type` pattern language**: The `$placeholder` pattern syntax is
  new and may need refinement before freezing. Evaluate whether it handles
  all common migration patterns.
- **Delete recipe tool**: No `delete_recipe` MCP tool exists (only
  `delete_convention`). Add for symmetry.
- **Error recovery**: No test coverage for daemon crashes, socket
  disconnections, or store corruption.

## Out of scope for 1.0

- Windows support (Unix socket architecture)
- Remote/networked daemon access
- Multi-user access control
- Plugin system for custom language adapters
- LSP server mode (sawmill as an LSP, not just MCP)
