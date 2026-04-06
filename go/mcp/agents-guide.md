# Sawmill Agents Guide

Reference for AI coding agents using Sawmill as an MCP server.

## What Sawmill Does

Sawmill models a codebase as a forest of Concrete Syntax Trees
(Tree-sitter) and exposes structural, multi-language transformations
over MCP. Instead of regenerating large blocks of source code, agents
describe the transformation they want and Sawmill performs the
mechanical rewriting -- producing minimal, diff-friendly changes that
preserve formatting, comments, and whitespace.

Supported languages: Python, TypeScript, Rust, Go, C/C++.

## Installation

Installation is a **multi-step process**. It is not complete until all
steps succeed — installing the binary alone is not enough.

**Step 1 — Install the binary:**

```bash
brew install marcelocantos/tap/sawmill
```

**Step 2 — Start the background service:**

```bash
brew services start sawmill
```

This starts the sawmill daemon which manages parsed codebases and
persists state across sessions. The daemon starts automatically on
login.

**Step 3 — Register as an MCP server:**

For Claude Code (global install — available in all projects):

```bash
claude mcp add --scope user sawmill -- sawmill
```

For other MCP clients, add to the client's MCP configuration
(e.g. `.mcp.json` for project scope):

```json
{
  "mcpServers": {
    "sawmill": {
      "command": "sawmill"
    }
  }
}
```

**Step 4 — Restart the agent session.** MCP servers are loaded at
session start — sawmill won't be available until the next session.

**Verification:** After restarting, call the `get_agent_prompt` tool to
confirm end-to-end integration. If it returns this guide, installation
is complete.

## Recommended Workflow

1. **Parse first.** Call `parse` with the project root. This loads and
   indexes all source files. Subsequent `parse` calls are incremental.
2. **Query/find.** Use `query`, `find_symbol`, or `find_references` to
   locate the code you need to change.
3. **Transform.** Use `rename`, `transform`, `codegen`,
   `add_parameter`, etc. Every transform returns a unified diff
   preview -- no files are modified yet.
4. **Review the diff.** Inspect the returned diff before proceeding.
5. **Apply.** Call `apply` with `confirm: true` to write changes to
   disk. Sawmill creates backup files automatically.
6. **Undo if needed.** Call `undo` to revert the last apply.

Only one set of pending changes exists at a time. A new transform
replaces any unapplied pending changes.

## Tool Reference

### Indexing

| Tool | Purpose | Key params |
|---|---|---|
| `parse` | Load/refresh the codebase model | `path` |

### Discovery

| Tool | Purpose | Key params |
|---|---|---|
| `query` | Structural search by node kind | `kind` ("function", "class", "call", "import"), `name` (glob), `file` |
| `find_symbol` | Find definitions by name | `symbol` |
| `find_references` | Find usages by name | `symbol` |

### Transforms

| Tool | Purpose | Key params |
|---|---|---|
| `rename` | Rename a symbol across files | `from`, `to` |
| `transform` | Match/act structural transform | See below |
| `transform_batch` | Multiple transforms in sequence | `transforms` (array) |
| `codegen` | JavaScript program against the codebase | `program` |
| `add_parameter` | Add a parameter to a function | `function`, `param_name`, `param_type`, `position` |
| `remove_parameter` | Remove a parameter from a function | `function`, `param_name` |

### Teaching

| Tool | Purpose | Key params |
|---|---|---|
| `teach_by_example` | Extract a reusable template from exemplar code | `name`, `exemplar`, `parameters` |
| `teach_recipe` | Define a named transform sequence | `name`, `params`, `steps` |
| `instantiate` | Create code from a taught recipe | `recipe`, `params` |
| `teach_convention` | Define an enforceable project rule | `name`, `check_program` |
| `check_conventions` | Scan for convention violations | `path` |
| `list_recipes` | List all taught recipes | -- |
| `list_conventions` | List all taught conventions | -- |

### LSP (when language servers are available)

| Tool | Purpose | Key params |
|---|---|---|
| `hover` | Type info at a position | `file`, `line`, `column` (1-based) |
| `definition` | Go to definition | `file`, `line`, `column` |
| `lsp_references` | Find all references via LSP | `file`, `line`, `column` |
| `diagnostics` | Get compile errors/warnings | `file`, `content` (optional) |

### Application

| Tool | Purpose | Key params |
|---|---|---|
| `apply` | Write pending changes to disk | `confirm: true` |
| `undo` | Revert the last apply | -- |

## The `transform` Tool

The `transform` tool combines orthogonal matching and action
strategies.

**Matching** (pick one):
- Abstract: `kind` + optional `name` (glob) + optional `file`
- Raw Tree-sitter query: `raw_query` + optional `capture`

**Action** (pick one):
- Declarative `action` with `code`/`before`/`after`:
  - `"replace"` -- replace matched node with `code`
  - `"wrap"` -- wrap with `before`/`after`
  - `"unwrap"` -- remove wrapper, keep contents
  - `"prepend_statement"` / `"append_statement"` -- inject `code`
  - `"remove"` -- delete the matched node
  - `"replace_name"` -- rename just the identifier
  - `"replace_body"` -- replace just the body
- JavaScript `transform_fn`: receives a node, returns modified node,
  string, or null

### `transform_fn` node object

Properties: `kind`, `name`, `text`, `body`, `parameters`, `file`,
`startLine`, `endLine`, `startByte`, `endByte`.

Mutation methods: `replaceText(text)`, `replaceBody(body)`,
`replaceName(name)`, `remove()`, `insertBefore(code)`,
`insertAfter(code)`.

Structural navigation: `fields()`, `methods()`, `method(name)`,
`returnType()`.

Semantic mutations: `addField(name, type, doc?)`,
`addMethod(name, params, returnType, body, doc?)`.

### Examples

Rename all functions matching a glob:
```json
{
  "kind": "function",
  "name": "get_*",
  "action": "replace_name",
  "code": "fetch_${1}"
}
```

Add logging to every function via JS:
```json
{
  "kind": "function",
  "transform_fn": "node.insertBefore('console.log(\"entering ' + node.name + '\");'); return node;"
}
```

## The `codegen` ctx API

The `codegen` tool executes a JavaScript program with a global `ctx`
object. Use it for coordinated multi-file edits that go beyond
pattern matching.

### Query methods

| Method | Returns | Description |
|---|---|---|
| `ctx.findFunction(name)` | node[] | Find function definitions by exact name |
| `ctx.findType(name)` | node[] | Find type/class definitions by exact name |
| `ctx.query({kind, name, file})` | node[] | General query; `name` supports `*` globs |
| `ctx.references(name)` | node[] | Find call sites for a function |
| `ctx.files` | string[] | All file paths in the codebase |
| `ctx.readFile(path)` | string or null | Read file contents |

### Edit methods

| Method | Description |
|---|---|
| `ctx.addFile(path, content)` | Create a new file |
| `ctx.editFile(path, startByte, endByte, replacement)` | Raw byte-range edit |
| `ctx.addImport(filePath, importPath)` | Insert a language-appropriate import at file top |
| `ctx.genField(langId, name, type)` | Generate a field declaration string |
| `ctx.genMethod(langId, name, params, returnType, body)` | Generate a method declaration string |

### LSP methods (available when `ctx.hasLsp` is true)

| Method | Returns | Description |
|---|---|---|
| `ctx.typeOf(file, line, col)` | string or null | Hover/type info (1-based line/col) |
| `ctx.definition(file, line, col)` | {file, line, column}[] | Go to definition |
| `ctx.lspReferences(file, line, col)` | {file, line, column}[] | Find references |
| `ctx.diagnostics(file, text?)` | diagnostic[] | Compile errors/warnings |

### Node objects

Nodes returned by query methods have the same properties and mutation
methods as `transform_fn` nodes (see above). Calling mutation methods
on a node queues edits that are collected when the program finishes.

### Example

Add a `toString` method to every class that has a `name` field:

```javascript
var types = ctx.query({kind: "type"});
for (var i = 0; i < types.length; i++) {
    var t = types[i];
    var fields = t.fields();
    var hasName = false;
    for (var j = 0; j < fields.length; j++) {
        if (fields[j].name === "name") hasName = true;
    }
    if (hasName && !t.method("toString")) {
        t.addMethod("toString", "", "String",
            "return this.name;");
    }
}
```

## Tips and Gotchas

- **Always `parse` first.** Every other tool requires the codebase
  model to be loaded. Call `parse` once at the start of a session.

- **One pending changeset at a time.** Calling a transform tool
  replaces any unapplied pending changes. Apply or discard before
  running the next transform.

- **Diffs are previews, not writes.** Nothing touches disk until you
  call `apply` with `confirm: true`.

- **Backup/undo is per-apply.** Each `apply` creates backups. `undo`
  reverts only the most recent apply.

- **Use `format: true` sparingly.** It runs the language formatter
  (rustfmt, gofmt, etc.) on changed files. Useful for generated code,
  but adds latency.

- **`path` scoping.** Most tools accept a `path` parameter that
  limits the scope to a file or directory. Default is `"."` (current
  directory). Use it to avoid touching unrelated files.

- **JS runtime is ES5.** The embedded QuickJS engine runs ES5
  JavaScript. Use `var`, not `let`/`const`. Use `function(){}`, not
  arrow functions.

- **Conventions are checked on apply.** If you have taught
  conventions via `teach_convention`, they are automatically verified
  when `apply` runs. Violations block the apply.

- **Recipes persist across sessions.** Taught recipes and conventions
  are stored in SQLite and survive server restarts. Use `list_recipes`
  and `list_conventions` to see what already exists.

- **LSP is optional.** Structural queries (`query`, `find_symbol`,
  `find_references`) work without LSP. The `hover`, `definition`,
  `lsp_references`, and `diagnostics` tools require running language
  servers. Check `ctx.hasLsp` in codegen programs.

- **Node byte offsets are into original source.** When using
  `ctx.editFile` with raw byte ranges, the offsets refer to the
  original (pre-edit) source. Multiple edits to the same file are
  resolved by Sawmill -- do not adjust offsets yourself.

- **`transform_batch` for atomic multi-step edits.** When you need
  several transforms to land together (e.g., rename + add import),
  use `transform_batch` so they share a single pending changeset and
  a single `apply`.
