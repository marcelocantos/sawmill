# Sawmill

Sawmill is an MCP server that models a codebase as a forest of Concrete
Syntax Trees and exposes safe, programmable, multi-language structural
transformations to AI coding agents.

Agents request refactoring operations — renames, structural transforms,
code generation — without repeatedly processing or regenerating large
volumes of source code in their context windows. Sawmill handles the
mechanical rewriting deterministically, producing minimal, git-diff-friendly
changes that preserve formatting, comments, and whitespace.

## Features

- **Multi-language**: Python, TypeScript, Rust, Go, C/C++ via Tree-sitter
- **MCP server**: Runs over stdio; works with any MCP-compatible AI agent
- **Persistent daemon**: Background process shares parsed state across
  sessions via Unix socket — auto-started on first use
- **Structural transforms**: Rename, query, match/act with declarative
  actions or JavaScript transform functions
- **Teach by example**: Point at existing code, name the variable parts,
  get a reusable template
- **Conventions**: Define enforceable rules as JavaScript checks, verified
  on every apply
- **Code generation**: JavaScript programs with a rich `ctx` API for
  coordinated multi-file edits
- **Safe by default**: Diff preview before every write, backup/undo on
  every apply
- **LSP integration**: Optional language server queries for type info,
  go-to-definition, references, and diagnostics (gopls, pyright, etc.)
- **Structural invariants**: Declarative rules that assert properties of
  types and functions — checked across the codebase
- **Dependency analysis**: Impact analysis showing where a package is
  imported, which symbols are used, and public API exposure
- **Type migration**: Rewrite construction patterns, field access, and
  type names across a codebase with a pattern language
- **AST-aware merge**: Three-way merge that resolves edits which don't
  overlap structurally (parallel method additions, parallel imports,
  format vs logic). Drops in as a `git mergetool` / merge driver for
  Python and Go.

## Quick start

Give your AI coding agent this prompt:

```
Install sawmill from https://github.com/marcelocantos/sawmill — brew
install, start the service, register it as an MCP server, and restart
the session. Follow the agents-guide.md in the repo.
```

## Installation

### 1. Install the binary

**Homebrew** (macOS / Linux):

```bash
brew install marcelocantos/tap/sawmill
```

**From source** (requires Go 1.26+):

```bash
git clone https://github.com/marcelocantos/sawmill.git
cd sawmill/go
go build -o sawmill ./cmd/sawmill
```

### 2. Start the background service

```bash
brew services start sawmill
```

This launches `sawmill serve --addr 127.0.0.1:8765`, the HTTP MCP
server. It starts automatically on login and persists parsed state
across sessions.

Confirm it's listening:

```bash
lsof -iTCP:8765 -sTCP:LISTEN
```

(Don't probe with `curl`. MCP only accepts POST + JSON-RPC, so a plain
GET returns nothing — easy to mistake for "not running".)

### 3. Register the MCP server

**Claude Code** (one command — installs globally for all projects):

```bash
claude mcp add --scope user --transport http sawmill http://127.0.0.1:8765/mcp
```

This writes the server entry to `~/.claude.json`. Restart Claude Code
to pick up the new server.

**Other MCP clients** — every client that supports streamable HTTP can
use the same JSON shape:

```json
{
  "mcpServers": {
    "sawmill": {
      "transport": "http",
      "url": "http://127.0.0.1:8765/mcp"
    }
  }
}
```

Stdio-only clients can route through a transparent gateway like
[mcpbridge](https://github.com/marcelocantos/mcpbridge):

```bash
claude mcp add --scope user sawmill -- mcpbridge http://127.0.0.1:8765/mcp
```

### 4. Verify

In a new session, call the `get_agent_prompt` tool. If it returns the
agents guide, installation is complete.

## Architecture

```
AI Agent ──HTTP──▶ sawmill serve (HTTP MCP server, port 8765)
                       │
                       ├─ CodebaseModel (per project)
                       │    ├─ Forest (Tree-sitter CSTs)
                       │    ├─ Store (SQLite)
                       │    └─ Watcher (fsnotify)
                       ├─ GitIndex (lazy AST snapshots per commit)
                       └─ MCP Server (57 tools, streamable HTTP)
```

- `sawmill serve` is the HTTP MCP server, listening on `127.0.0.1:8765`
  (overridable with `--addr`). Started by `brew services start sawmill`.
- Each MCP session runs in its own handler with per-session pending
  changes/backups; sessions targeting the same project root share a
  parsed model via the internal model pool (amortised parsing).
- Stdio-only MCP clients connect through a transparent gateway such as
  [mcpbridge](https://github.com/marcelocantos/mcpbridge).

## MCP tools

57 tools, grouped by purpose. Every transform returns a diff preview;
call `apply` to write changes, `undo` to revert.

**Discovery & navigation**

| Tool | Description |
|---|---|
| `parse` | Load and index the codebase for the current session |
| `query` | Search for structural patterns by kind/name or raw Tree-sitter query (`format=json` for structured output) |
| `find_symbol` | Find all definitions of a symbol by name |
| `find_references` | Find all usages of a symbol by name |
| `dependency_usage` | Analyse package imports, symbols used, public API exposure |

**Transforms**

| Tool | Description |
|---|---|
| `rename` / `rename_file` | Rename an identifier or a file (with import cascade) |
| `transform` / `transform_batch` | Match/act with declarative or JS actions |
| `codegen` | JavaScript program against the whole codebase |
| `add_parameter` / `remove_parameter` | Modify function signatures across call sites |
| `add_field` | Add a field to a struct/class and propagate to constructors |
| `clone_and_adapt` | Copy a symbol with string substitutions to a new location |
| `migrate_type` | Rewrite construction patterns, field access, and type names |
| `migrate_pattern` | One-shot pattern rewrite with explicit add/drop import semantics |
| `promote_constant` | Magic literals → named constants in idiomatic per-language form |
| `extract_to_env` | Literal → env-var read; scaffolds `.env.example` and `.gitignore` |

**Teaching & convention**

| Tool | Description |
|---|---|
| `teach_recipe` / `instantiate` / `list_recipes` | Save a parameterised transform sequence and run it |
| `teach_by_example` | Extract a template from exemplar code |
| `teach_convention` / `check_conventions` / `list_conventions` | Define and check JS-based project rules (`format=json` for structured violations) |
| `teach_invariant` / `check_invariants` / `list_invariants` / `delete_invariant` | Declarative structural assertions for types/functions (`format=json` for structured output) |
| `teach_equivalence` / `apply_equivalence` / `check_equivalences` / `list_equivalences` / `delete_equivalence` | Bidirectional pattern pairs with transitive closure (e.g. `errors.Is(err, X) ↔ err == X`) |

**Diagnostic-driven fixes**

| Tool | Description |
|---|---|
| `teach_fix` / `list_fixes` / `delete_fix` | Save diagnostic-pattern → fix-action mappings |
| `seed_fixes` | Install a curated starter catalogue (Go + TypeScript common errors) |
| `auto_fix` | Convergence loop: pull diagnostics → match → apply (or suggest) → re-run, with cycle detection |
| `learn_from_observation` | Infer candidate fix entries from pre/post diagnostic snapshots |

**Git history (semantic)**

| Tool | Description |
|---|---|
| `git_index` / `git_log` | Lazily index commits and walk structured history |
| `git_diff_summary` | Symbol-level diff (added/removed/modified) between refs |
| `git_blame_symbol` | Trace a symbol's introduction, last modification, body change, and signature change |
| `semantic_diff` | Structural AST diff: detects moves, renames, signature changes, key-level data format changes |
| `api_changelog` | Markdown API surface changelog between two refs |
| `git_semantic_bisect` | Binary-search the commit where a structural predicate flipped — without running the code |

**LSP**

| Tool | Description |
|---|---|
| `hover` / `definition` / `lsp_references` | Language-server queries at a source position |
| `diagnostics` | Compile errors/warnings (`format=json` for structured `{code, source, severity, ...}`) |

**Multi-repo orchestration**

| Tool | Description |
|---|---|
| `transform_multi_root` | Apply an ordered list of transforms across multiple project roots in one call; returns per-root diff bundles |
| `apply_multi_root_pr` | Take per-root diff bundles, create per-repo feature branches, commit, push, and open PRs via `git`/`gh`; per-repo errors don't abort siblings |

**Merge**

| Tool | Description |
|---|---|
| `merge_three_way` | AST-aware three-way merge of (base, ours, theirs); resolves edits that don't overlap structurally (parallel method additions, parallel imports, format vs logic) and falls back to git-style conflict markers only on true body conflicts. Stateless — does not require `parse` first. |

**Application**

| Tool | Description |
|---|---|
| `apply` | Write pending changes to disk (with backup) |
| `undo` | Revert the last apply from backups |
| `get_agent_prompt` | Return the agent guide |

## Supported languages

| Language | Parsing | Formatting | AST merge |
|---|---|---|---|
| Python | Yes | `ruff format` | Yes |
| TypeScript | Yes | `prettier` | — |
| Rust | Yes | `rustfmt` | — |
| Go | Yes | `gofmt` | Yes |
| C/C++ | Yes | `clang-format` | — |

## Git merge integration

The `sawmill` binary ships two subcommands that drop into git's standard
merge plumbing. Both share the AST-aware engine and emit standard
git-style conflict markers for any residual hunks.

**As a `git mergetool`** — interactive, opt-in:

```ini
# ~/.gitconfig
[mergetool "sawmill"]
    cmd = sawmill merge --base "$BASE" --local "$LOCAL" --remote "$REMOTE" --output "$MERGED"
```

Then run `git mergetool` after a conflicted merge.

**As a low-level merge driver** — automatic, gated by file pattern:

```ini
# ~/.gitconfig
[merge "sawmill"]
    name = AST-aware merge
    driver = sawmill merge-driver %O %A %B %P
```

```gitattributes
# .gitattributes
*.py merge=sawmill
*.go merge=sawmill
```

With this in place, `git merge`, `git rebase`, and `git cherry-pick`
silently resolve commuting AST edits (parallel method additions,
parallel imports, etc.) and only surface text conflicts when two sides
edit the same declaration body.

## For AI agents

See [`agents-guide.md`](agents-guide.md) for a full reference on
sawmill's tools, workflows, and gotchas. You can also run:

```bash
sawmill --help-agent
```

## Design

See [`docs/design.md`](docs/design.md) for the architecture and
rationale.

## License

[Apache 2.0](LICENSE)
