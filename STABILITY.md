# Stability

## Commitment

Version 1.0 will represent a backwards-compatibility contract. After 1.0,
breaking changes to the CLI interface, MCP tool surface, configuration
conventions, or wire formats require forking into a new product. The
pre-1.0 period exists to get these right.

## Interaction surface catalogue

Snapshot as of v0.11.0. 240 public surface items.

### CLI

| Item | Type | Stability |
|---|---|---|
| `sawmill serve` | HTTP MCP server | **Stable** |
| `sawmill version` | Print version | **Stable** |
| `sawmill --help` | Usage | **Stable** |
| `sawmill --help-agent` | Agent guide | **Stable** |
| `--addr HOST:PORT` (serve) | string, default `127.0.0.1:8765` | **Stable** |

### MCP tools (56 tools, 165 parameters)

| Tool | Required params | Optional params | Stability |
|---|---|---|---|
| `parse` | — | `path` | **Stable** |
| `rename` | `from`, `to` | `path`, `format` | **Stable** |
| `rename_file` | `from`, `to` | `format` | **Stable** |
| `query` | — | `kind`, `name`, `file`, `raw_query`, `capture`, `path`, `format` | **Stable** |
| `find_symbol` | `symbol` | `kind` | **Stable** |
| `find_references` | `symbol` | — | **Stable** |
| `dependency_usage` | `package` | `path` | **Stable** |
| `transform` | — | `path`, `kind`, `name`, `file`, `raw_query`, `capture`, `action`, `code`, `before`, `after`, `transform_fn`, `format` | **Stable** |
| `transform_batch` | `transforms` | `path`, `format` | **Needs review** — `transforms` is a JSON string, not a native array |
| `transform_multi_root` | `roots`, `transforms` | `format` | **Needs review** — `roots` and `transforms` are JSON strings; new in v0.11.0 |
| `apply_multi_root_pr` | `bundles`, `branch_template`, `title_template` | `body_template`, `commit_message` | **Needs review** — new in v0.11.0; shells out to `git`/`gh`, idempotency relies on remote branch/PR state |
| `codegen` | `program` | `path`, `format`, `validate` | **Stable** |
| `apply` | `confirm` | — | **Stable** |
| `undo` | — | — | **Stable** |
| `teach_recipe` | `name`, `steps` | `description`, `params` | **Stable** |
| `instantiate` | `recipe` | `params`, `path`, `format` | **Stable** |
| `list_recipes` | — | — | **Stable** |
| `teach_convention` | `name`, `check_program` | `description` | **Stable** |
| `check_conventions` | — | `path`, `format` | **Stable** |
| `list_conventions` | — | — | **Stable** |
| `teach_invariant` | `name`, `rule` | `description` | **Stable** |
| `check_invariants` | — | `path`, `format` | **Stable** |
| `list_invariants` | — | — | **Stable** |
| `delete_invariant` | `name` | — | **Stable** |
| `hover` | `file`, `line`, `column` | — | **Stable** |
| `definition` | `file`, `line`, `column` | — | **Stable** |
| `lsp_references` | `file`, `line`, `column` | — | **Stable** |
| `diagnostics` | `file` | `format` | **Stable** |
| `get_agent_prompt` | — | — | **Stable** |
| `teach_by_example` | `name`, `exemplar`, `parameters` | `description`, `also_affects` | **Needs review** — `parameters` and `also_affects` are JSON strings |
| `add_parameter` | `function`, `param_name` | `path`, `param_type`, `default_value`, `position`, `format` | **Stable** |
| `remove_parameter` | `function`, `param_name` | `path`, `format` | **Stable** |
| `add_field` | `type_name`, `field_name`, `field_type`, `default_value` | `path`, `format` | **Stable** |
| `clone_and_adapt` | `source`, `substitutions`, `target_file` | `position`, `format` | **Stable** |
| `migrate_type` | `type_name`, `rules` | `path`, `format` | **Needs review** — pattern language is new, may evolve |
| `git_index` | — | `ref`, `limit` | **Stable** |
| `git_log` | — | `ref`, `limit`, `path` | **Stable** |
| `git_diff_summary` | `base` | `head`, `path` | **Stable** |
| `git_blame_symbol` | `path`, `symbol` | `ref` | **Stable** |
| `semantic_diff` | `base` | `head`, `path` | **Stable** |
| `api_changelog` | `base` | `head` | **Stable** |
| `git_semantic_bisect` | `predicate`, `good`, `bad` | — | **Stable** |
| `teach_equivalence` | `name`, `left_pattern`, `right_pattern` | `description`, `preferred_direction` | **Stable** |
| `list_equivalences` | — | — | **Stable** |
| `delete_equivalence` | `name` | — | **Stable** |
| `apply_equivalence` | `name`, `direction` | `path`, `format` | **Stable** |
| `check_equivalences` | — | `path` | **Stable** |
| `teach_fix` | `name`, `diagnostic_regex`, `action` | `confidence`, `description` | **Stable** |
| `list_fixes` | — | — | **Stable** |
| `delete_fix` | `name` | — | **Stable** |
| `migrate_pattern` | `old_pattern`, `new_pattern` | `add_import`, `drop_import`, `path`, `format` | **Stable** |
| `extract_to_env` | `literal`, `var_name` | `path`, `format` | **Stable** |
| `promote_constant` | `literal`, `name` | `path`, `format` | **Stable** |
| `learn_from_observation` | `pre_diagnostics` | `post_diagnostics` | **Needs review** — heuristic regex generalisation may need refinement |
| `seed_fixes` | — | — | **Stable** |
| `auto_fix` | `file` | `max_iterations`, `dry_run` | **Needs review** — convergence loop semantics (cycle detection, termination conditions) may evolve |

### Configuration conventions

| Item | Value | Stability |
|---|---|---|
| Default listen address | `127.0.0.1:8765` (HTTP) | **Stable** |
| MCP endpoint path | `/mcp` (streamable HTTP) | **Stable** |
| Store path | `~/.sawmill/stores/<hash>/store.db` | **Stable** |
| Backup dir | `~/.sawmill/backups/<hash>/` | **Stable** |
| Backup suffix | `.bak` | **Stable** |
| Staging suffix | `.new` | **Stable** |
| Languages | Python, TypeScript, Rust, Go, C/C++ | **Stable** (additive only) |
| JS runtime | QuickJS ES5 | **Needs review** — may upgrade to ES2020+ |

### Wire format

| Item | Value | Stability |
|---|---|---|
| MCP protocol | JSON-RPC 2.0 (via mcp-go) | **Stable** (standard) |
| Transport | Streamable HTTP (mcp-go `NewStreamableHTTPServer`) | **Stable** |
| Stdio compatibility | Via external transparent gateway (e.g. mcpbridge) | **Stable** |

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
- **`migrate_type` / `$placeholder` pattern language**: The `$placeholder`
  pattern syntax (shared by `migrate_type`, `migrate_pattern`,
  `teach_equivalence`, `apply_equivalence`) is new and may need refinement
  before freezing. Evaluate coverage of complex migration patterns.
- **Delete recipe tool**: No `delete_recipe` MCP tool exists (symmetry is
  now narrower — `delete_equivalence` and `delete_fix` ship in v0.10.0,
  but recipes still lack a delete operation). Add for symmetry before 1.0.
- **Error recovery**: No test coverage for server crashes, transport
  disconnections, or store corruption.
- **`auto_fix` and `learn_from_observation` maturity**: Both tools ship
  in v0.10.0 but carry algorithmic novelty (convergence loop, heuristic
  regex generalisation). Mark stable after soak time and real-world
  validation of cycle detection and candidate quality.
- **Multi-repo orchestration maturity**: `transform_multi_root` and
  `apply_multi_root_pr` ship in v0.11.0. The latter shells out to
  `git`/`gh` and depends on the user's credential helper / SSH agent
  for push and PR creation. Soak across real cross-repo refactors
  before promoting to **Stable** — error-isolation and
  branch/PR-idempotency semantics may need refinement.
- **Tree-sitter runtime swap**: v0.11.0 swapped to the pure-Go
  `gotreesitter` runtime (🎯T7.0). Existing tests pass, but watch for
  parser regressions in real-world codebases (especially
  large/edge-case grammars) before treating the swap as fully settled.

## Out of scope for 1.0

- Windows support (Unix socket architecture)
- Remote/networked daemon access
- Multi-user access control
- Plugin system for custom language adapters
- LSP server mode (sawmill as an LSP, not just MCP)
