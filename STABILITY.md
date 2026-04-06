# Stability

## Commitment

Version 1.0 will represent a backwards-compatibility contract. After 1.0,
breaking changes to the CLI interface, MCP tool surface, configuration
conventions, or wire formats require forking into a new product. The
pre-1.0 period exists to get these right.

## Interaction surface catalogue

Snapshot as of v0.6.0. 107 public surface items.

### CLI

| Item | Type | Stability |
|---|---|---|
| `sawmill` (default) | MCP stdio mode | **Stable** |
| `sawmill serve` | Background daemon | **Stable** |
| `sawmill version` | Print version | **Stable** |
| `sawmill --help` | Usage | **Stable** |
| `sawmill --help-agent` | Agent guide | **Stable** |
| `--socket` (both modes) | string, default `~/.sawmill/sawmill.sock` | **Stable** |
| `--root` (default mode) | string, default cwd | **Stable** |

### MCP tools (20 tools, 63 parameters)

| Tool | Required params | Optional params | Stability |
|---|---|---|---|
| `parse` | — | `path` | **Stable** |
| `rename` | `from`, `to` | `path`, `format` | **Stable** |
| `query` | — | `kind`, `name`, `file`, `raw_query`, `capture`, `path` | **Stable** |
| `find_symbol` | `symbol` | `kind` | **Stable** |
| `find_references` | `symbol` | — | **Stable** |
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
| `get_agent_prompt` | — | — | **Stable** |
| `teach_by_example` | `name`, `exemplar`, `parameters` | `description`, `also_affects` | **Needs review** — `parameters` and `also_affects` are JSON strings |
| `add_parameter` | `function`, `param_name` | `path`, `param_type`, `default_value`, `position`, `format` | **Stable** |
| `remove_parameter` | `function`, `param_name` | `path`, `format` | **Stable** |

### Configuration conventions

| Item | Value | Stability |
|---|---|---|
| Socket path | `~/.sawmill/sawmill.sock` | **Stable** |
| Store path | `<root>/.sawmill/store.db` | **Stable** |
| Backup suffix | `.sawmill.bak` | **Stable** |
| Staging suffix | `.sawmill.new` | **Stable** |
| Languages | Python, TypeScript, Rust, Go, C/C++ | **Stable** (additive only) |
| JS runtime | QuickJS ES5 | **Needs review** — may upgrade to ES2020+ |

### Wire format

| Item | Value | Stability |
|---|---|---|
| Handshake: client→daemon | `<root>\n` | **Stable** |
| Handshake: daemon→client | `{"status":"ok","root":"...","files":N}\n` | **Stable** |
| MCP protocol | JSON-RPC 2.0 | **Stable** (standard) |
| Transport (stdio) | stdin/stdout | **Stable** |
| Transport (daemon) | Unix domain socket | **Stable** |

## Gaps and prerequisites for 1.0

- **LSP integration**: The Rust version had `hover`, `definition`,
  `lsp_references`, `diagnostics` tools. These are documented in the
  agents-guide but not yet ported to Go. Must be implemented or
  explicitly removed from documentation before 1.0.
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
