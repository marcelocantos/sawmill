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

This starts the sawmill daemon which manages parsed codebases and
persists state across sessions. The daemon auto-starts on login.

If you don't use Homebrew services, the daemon is auto-started by
`sawmill` on first use — no manual step needed.

### 3. Register the MCP server

**Claude Code** (one command — installs globally for all projects):

```bash
claude mcp add --scope user sawmill -- sawmill
```

This writes the server entry to `~/.claude.json`. Restart Claude Code
to pick up the new server.

**Other MCP clients** — add to your client's MCP configuration file
(e.g. `.mcp.json` for project scope, or the client's global config):

```json
{
  "mcpServers": {
    "sawmill": {
      "command": "sawmill"
    }
  }
}
```

### 4. Verify

In a new session, call the `get_agent_prompt` tool. If it returns the
agents guide, installation is complete.

## Architecture

```
AI Agent ──stdio──▶ sawmill ──socket──▶ sawmill serve (daemon)
                                            │
                                            ├─ CodebaseModel (per project)
                                            │    ├─ Forest (Tree-sitter CSTs)
                                            │    ├─ Store (SQLite)
                                            │    └─ Watcher (fsnotify)
                                            └─ MCP Server (33 tools)
```

- `sawmill` (no args) is the MCP stdio server that clients launch
- `sawmill serve` is the background daemon, started via `brew services`
- The daemon auto-starts if not running when `sawmill` connects

## MCP tools

| Tool | Description |
|---|---|
| `parse` | Load and index the codebase (auto-loaded from working directory) |
| `rename` | Rename a symbol across files (diff preview) |
| `rename_file` | Rename a file and update all import paths |
| `query` | Search for structural patterns (functions, classes, calls, imports) |
| `find_symbol` | Find all definitions of a symbol by name |
| `find_references` | Find all usages of a symbol by name |
| `dependency_usage` | Analyse dependency imports, symbols used, and public API exposure |
| `transform` | Match/act structural transform with declarative or JS actions |
| `transform_batch` | Apply multiple transforms sequentially |
| `add_parameter` | Add a parameter to a function definition |
| `remove_parameter` | Remove a parameter from a function definition |
| `add_field` | Add a field to a struct/class and propagate to constructors |
| `clone_and_adapt` | Copy a symbol with string substitutions to a new location |
| `migrate_type` | Rewrite all usage sites of a type (construction, access, rename) |
| `codegen` | Execute a JavaScript code generator against the codebase |
| `teach_by_example` | Extract a reusable template from exemplar code |
| `teach_recipe` | Define a named sequence of transforms with parameters |
| `instantiate` | Create new code from a taught recipe |
| `teach_convention` | Define an enforceable project rule |
| `check_conventions` | Scan for convention violations |
| `list_recipes` | List all taught recipes |
| `list_conventions` | List all taught conventions |
| `teach_invariant` | Define a structural invariant (JSON rule) for types/functions |
| `check_invariants` | Scan for invariant violations |
| `list_invariants` | List all taught invariants |
| `delete_invariant` | Remove an invariant |
| `hover` | Type information at a source position (LSP) |
| `definition` | Go to definition at a source position (LSP) |
| `lsp_references` | Find all references via LSP |
| `diagnostics` | Get compile errors and warnings (LSP) |
| `get_agent_prompt` | Return the agent guide with all tool documentation |
| `apply` | Write pending changes to disk (with backup) |
| `undo` | Revert the last apply from backups |

Every transform tool returns a diff preview. Call `apply` to write
changes. Call `undo` to revert.

## Supported languages

| Language | Parsing | Formatting |
|---|---|---|
| Python | Yes | autopep8 |
| TypeScript | Yes | — |
| Rust | Yes | rustfmt |
| Go | Yes | gofmt |
| C/C++ | Yes | clang-format |

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
