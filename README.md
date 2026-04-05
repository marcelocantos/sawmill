# Canopy

Canopy is an MCP server that models a codebase as a forest of Concrete
Syntax Trees and exposes safe, programmable, multi-language structural
transformations to AI coding agents.

Agents request refactoring operations — renames, structural transforms,
code generation — without repeatedly processing or regenerating large
volumes of source code in their context windows. Canopy handles the
mechanical rewriting deterministically, producing minimal, git-diff-friendly
changes that preserve formatting, comments, and whitespace.

## Features

- **Multi-language**: Python, TypeScript, Rust, Go, C/C++ via Tree-sitter
- **MCP server**: Runs over stdio; works with any MCP-compatible AI agent
- **Structural transforms**: Rename, query, match/act with declarative
  actions or JavaScript transform functions
- **Teach by example**: Point at existing code, name the variable parts,
  get a reusable template
- **Conventions**: Define enforceable rules as JavaScript checks, verified
  on every apply
- **Code generation**: JavaScript programs with a rich `ctx` API for
  coordinated multi-file edits
- **LSP bridge**: Type info, definitions, references, and diagnostics from
  language servers
- **Safe by default**: Diff preview before every write, backup/undo on
  every apply

## Installation

### 1. Install the binary

**Homebrew** (macOS / Linux):

```bash
brew install marcelocantos/tap/canopy
```

**From source** (requires Rust 1.85+):

```bash
cargo install --git https://github.com/marcelocantos/canopy
```

Or clone and build:

```bash
git clone https://github.com/marcelocantos/canopy.git
cd canopy
cargo build --release
# binary is at target/release/canopy
```

### 2. Register the MCP server

Canopy communicates over stdio. Register it with your MCP client so it
starts automatically.

**Claude Code** (one command — installs globally for all projects):

```bash
claude mcp add --scope user canopy -- canopy serve
```

This writes the server entry to `~/.claude.json`. Restart Claude Code
to pick up the new server.

**Other MCP clients** — add to your client's MCP configuration file
(e.g. `.mcp.json` for project scope, or the client's global config):

```json
{
  "mcpServers": {
    "canopy": {
      "command": "canopy",
      "args": ["serve"]
    }
  }
}
```

### 3. Verify

In a new session, call the `parse` tool on your project root. If
canopy is running, it will respond with a file/language summary.

## CLI usage

```bash
# Parse and summarise a codebase
canopy parse src/

# Rename a symbol (prints diff)
canopy rename old_name new_name --path src/

# Run as MCP server (used by MCP clients — you don't need to run this manually)
canopy serve
```

## MCP tools

| Tool | Description |
|---|---|
| `parse` | Load and index the codebase (incremental on subsequent calls) |
| `rename` | Rename a symbol across files (diff preview) |
| `query` | Search for structural patterns (functions, classes, calls, imports) |
| `find_symbol` | Find all definitions of a symbol by name |
| `find_references` | Find all usages of a symbol by name |
| `transform` | Match/act structural transform with declarative or JS actions |
| `transform_batch` | Apply multiple transforms sequentially |
| `add_parameter` | Add a parameter to a function definition |
| `remove_parameter` | Remove a parameter from a function definition |
| `codegen` | Execute a JavaScript code generator against the codebase |
| `teach_by_example` | Extract a reusable template from exemplar code |
| `teach_recipe` | Define a named sequence of transforms with parameters |
| `instantiate` | Create new code from a taught recipe |
| `teach_convention` | Define an enforceable project rule |
| `check_conventions` | Scan for convention violations |
| `list_recipes` | List all taught recipes |
| `list_conventions` | List all taught conventions |
| `get_agent_prompt` | Generate a rich system prompt with capabilities, recipes, and conventions |
| `hover` | Get type info at a position (via LSP) |
| `definition` | Go to definition (via LSP) |
| `lsp_references` | Find all references (via LSP) |
| `diagnostics` | Get compile errors/warnings (via LSP) |
| `apply` | Write pending changes to disk (with backup) |
| `undo` | Revert the last apply from backups |

Every transform tool returns a diff preview. Call `apply` to write
changes. Call `undo` to revert.

## How it works

```
AI Agent ──MCP──▶ Canopy Server
                    │
                    ├─ Parsing Layer (Tree-sitter)
                    ├─ Forest (immutable parsed files)
                    ├─ Language Adapters (query dispatch)
                    ├─ Transform Engine
                    │    ├─ Named Operations (rename, extract)
                    │    └─ Match/Act Engine
                    ├─ Rewrite Engine (range-based patching)
                    └─ Output (diffs, patches, in-place writes)
```

1. **Parse** source files into Tree-sitter CSTs, one per file
2. **Match** nodes using abstract kind queries or raw Tree-sitter queries
3. **Transform** via declarative actions or JavaScript functions
4. **Rewrite** by copying unchanged regions verbatim, regenerating only
   modified portions
5. **Preview** as a unified diff; apply only on confirmation

## Supported languages

| Language | Parsing | Formatting | LSP |
|---|---|---|---|
| Python | Yes | autopep8 | pylsp |
| TypeScript | Yes | — | — |
| Rust | Yes | rustfmt | rust-analyzer |
| Go | Yes | gofmt | gopls |
| C/C++ | Yes | clang-format | clangd |

## For AI agents

### Installing canopy (agent instructions)

If a user asks you to install canopy, follow these steps:

1. **Install the binary.** Run `brew install marcelocantos/tap/canopy`.
   If Homebrew is not available, use
   `cargo install --git https://github.com/marcelocantos/canopy`.

2. **Register the MCP server.** For Claude Code, run:
   ```bash
   claude mcp add --scope user canopy -- canopy serve
   ```
   For other MCP clients, add the JSON entry shown in the Installation
   section above to the client's MCP configuration.

3. **Tell the user** they need to restart their agent session (or start
   a new one) for the MCP server to become available.

### Using canopy

See [`agents-guide.md`](agents-guide.md) for a full reference on
canopy's tools, workflows, and gotchas. You can also run:

```bash
canopy --help-agent
```

## Design

See [`docs/design.md`](docs/design.md) for the full architecture and
rationale. See [`docs/frontier.md`](docs/frontier.md) for the roadmap.

## License

[Apache 2.0](LICENSE)
