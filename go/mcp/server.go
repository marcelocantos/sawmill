// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package mcp implements the Sawmill MCP tool handler using mcpbridge. It
// exposes all sawmill tools via the ToolHandler interface and provides tool
// definitions for the daemon to serve.
package mcp

import (
	"fmt"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/model"
)

// FileRename records a pending file rename (from -> to, both absolute).
type FileRename struct {
	From string
	To   string
}

// PendingChanges holds the last set of pending file changes produced by a
// transform/rename/codegen call, waiting for an explicit apply.
type PendingChanges struct {
	Changes []forest.FileChange
	Diffs   []string
	Renames []FileRename // file renames to perform on apply
}

// LastBackups holds the backup paths written by the most recent apply, so
// that undo can restore them.
type LastBackups struct {
	Paths []string
}

// Handler implements mcpbridge.ToolHandler for Sawmill. It holds session
// state (the active codebase model, pending changes, and backups).
//
// All exported methods are safe for concurrent use — the mu field serialises
// access to model, pending, and lastBackups.
type Handler struct {
	mu          sync.Mutex
	model       *model.CodebaseModel
	pending     *PendingChanges
	lastBackups *LastBackups
}

// NewHandler creates a new Handler. The model is nil until the first
// successful parse call.
func NewHandler() *Handler {
	return &Handler{}
}

// NewHandlerWithModel creates a Handler pre-loaded with an existing
// CodebaseModel. Use this when the daemon has already resolved the project
// root and loaded the model; the MCP parse tool will still work but the
// handler can also operate on the pre-loaded state immediately.
func NewHandlerWithModel(m *model.CodebaseModel) *Handler {
	return &Handler{model: m}
}

// Call dispatches an MCP tool call by name.
// Returns (text, isError, err) where isError means a tool-level error
// (returned to the user) vs err which is a transport/system error.
func (h *Handler) Call(name string, args map[string]any) (string, bool, error) {
	switch name {
	case "parse":
		return h.handleParse(args)
	case "rename":
		return h.handleRename(args)
	case "rename_file":
		return h.handleRenameFile(args)
	case "query":
		return h.handleQuery(args)
	case "find_symbol":
		return h.handleFindSymbol(args)
	case "find_references":
		return h.handleFindReferences(args)
	case "transform":
		return h.handleTransform(args)
	case "transform_batch":
		return h.handleTransformBatch(args)
	case "codegen":
		return h.handleCodegen(args)
	case "apply":
		return h.handleApply(args)
	case "undo":
		return h.handleUndo(args)
	case "teach_recipe":
		return h.handleTeachRecipe(args)
	case "instantiate":
		return h.handleInstantiate(args)
	case "list_recipes":
		return h.handleListRecipes(args)
	case "teach_convention":
		return h.handleTeachConvention(args)
	case "check_conventions":
		return h.handleCheckConventions(args)
	case "list_conventions":
		return h.handleListConventions(args)
	case "get_agent_prompt":
		return h.handleGetAgentPrompt(args)
	case "teach_by_example":
		return h.handleTeachByExample(args)
	case "add_parameter":
		return h.handleAddParameter(args)
	case "remove_parameter":
		return h.handleRemoveParameter(args)
	case "clone_and_adapt":
		return h.handleCloneAndAdapt(args)
	case "hover":
		return h.handleHover(args)
	case "definition":
		return h.handleDefinition(args)
	case "lsp_references":
		return h.handleLspReferences(args)
	case "diagnostics":
		return h.handleDiagnostics(args)
	case "add_field":
		return h.handleAddField(args)
	case "dependency_usage":
		return h.handleDependencyUsage(args)
	case "teach_invariant":
		return h.handleTeachInvariant(args)
	case "check_invariants":
		return h.handleCheckInvariants(args)
	case "list_invariants":
		return h.handleListInvariants(args)
	case "delete_invariant":
		return h.handleDeleteInvariant(args)
	default:
		return "", false, fmt.Errorf("unknown tool: %s", name)
	}
}

// Definitions returns the MCP tool definitions for all Sawmill tools.
// These are served to the proxy via the ListTools RPC method.
func Definitions() []mcpgo.Tool {
	return []mcpgo.Tool{
		// parse
		mcpgo.NewTool("parse",
			mcpgo.WithDescription("Parse a source tree. When connected via the daemon, the working directory is used automatically and path can be omitted. Returns a summary of the parsed codebase."),
			mcpgo.WithString("path",
				mcpgo.Description("Root directory or single file to parse (default: daemon working directory)"),
			),
		),

		// rename
		mcpgo.NewTool("rename",
			mcpgo.WithDescription("AST-level rename of an identifier across the codebase. Produces a diff preview; call apply to write changes."),
			mcpgo.WithString("from",
				mcpgo.Required(),
				mcpgo.Description("Identifier to rename"),
			),
			mcpgo.WithString("to",
				mcpgo.Required(),
				mcpgo.Description("New identifier name"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after renaming"),
			),
		),

		// rename_file
		mcpgo.NewTool("rename_file",
			mcpgo.WithDescription("Rename a file and update all import/include/require paths that reference it across the codebase. Produces a diff preview; call apply to write changes."),
			mcpgo.WithString("from",
				mcpgo.Required(),
				mcpgo.Description("Current file path (relative to project root)"),
			),
			mcpgo.WithString("to",
				mcpgo.Required(),
				mcpgo.Description("New file path (relative to project root)"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter on files with updated imports"),
			),
		),

		// query
		mcpgo.NewTool("query",
			mcpgo.WithDescription("Find AST nodes matching a kind/name pattern or a raw Tree-sitter query. Returns matches without modifying files."),
			mcpgo.WithString("kind",
				mcpgo.Description("Abstract kind: function, call, class/struct/type, import"),
			),
			mcpgo.WithString("name",
				mcpgo.Description("Name filter (supports * glob)"),
			),
			mcpgo.WithString("file",
				mcpgo.Description("Restrict to files whose path contains this substring"),
			),
			mcpgo.WithString("raw_query",
				mcpgo.Description("Raw Tree-sitter S-expression query (alternative to kind/name/file)"),
			),
			mcpgo.WithString("capture",
				mcpgo.Description("Which capture to use from the raw query"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
		),

		// find_symbol
		mcpgo.NewTool("find_symbol",
			mcpgo.WithDescription("Look up a symbol by name in the persistent symbol index."),
			mcpgo.WithString("symbol",
				mcpgo.Required(),
				mcpgo.Description("Symbol name to find"),
			),
			mcpgo.WithString("kind",
				mcpgo.Description("Optional kind filter (function, type, call, …)"),
			),
		),

		// find_references
		mcpgo.NewTool("find_references",
			mcpgo.WithDescription("Find all call sites for a symbol."),
			mcpgo.WithString("symbol",
				mcpgo.Required(),
				mcpgo.Description("Symbol name to search for call sites"),
			),
		),

		// transform
		mcpgo.NewTool("transform",
			mcpgo.WithDescription("Apply a structural transformation to AST nodes. Supports declarative match/act, teach-by-example templates, and JavaScript transforms."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithString("kind",
				mcpgo.Description("Abstract kind: function, call, class/struct/type, import"),
			),
			mcpgo.WithString("name",
				mcpgo.Description("Name filter (supports * glob)"),
			),
			mcpgo.WithString("file",
				mcpgo.Description("Restrict to files whose path contains this substring"),
			),
			mcpgo.WithString("raw_query",
				mcpgo.Description("Raw Tree-sitter S-expression query"),
			),
			mcpgo.WithString("capture",
				mcpgo.Description("Which capture to use from the raw query"),
			),
			mcpgo.WithString("action",
				mcpgo.Description("Declarative action: replace, wrap, unwrap, prepend_statement, append_statement, remove, replace_name, replace_body"),
			),
			mcpgo.WithString("code",
				mcpgo.Description("Replacement code for replace/prepend/append/replace_name/replace_body actions"),
			),
			mcpgo.WithString("before",
				mcpgo.Description("Code to insert before the matched node for the wrap action"),
			),
			mcpgo.WithString("after",
				mcpgo.Description("Code to insert after the matched node for the wrap action"),
			),
			mcpgo.WithString("transform_fn",
				mcpgo.Description("JavaScript function (node) => result for JS-based transforms"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after transforming"),
			),
		),

		// transform_batch
		mcpgo.NewTool("transform_batch",
			mcpgo.WithDescription("Apply an ordered list of transforms to a codebase. Each element of transforms is a JSON object with the same fields as the transform tool."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after all transforms"),
			),
			mcpgo.WithString("transforms",
				mcpgo.Required(),
				mcpgo.Description("JSON array of transform specifications"),
			),
		),

		// codegen
		mcpgo.NewTool("codegen",
			mcpgo.WithDescription("Run a JavaScript codegen program against the entire codebase. The program receives a ctx object with methods for querying symbols, navigating structure, and making edits."),
			mcpgo.WithString("path",
				mcpgo.Description("Root directory to operate on (defaults to parsed root)"),
			),
			mcpgo.WithString("program",
				mcpgo.Required(),
				mcpgo.Description("JavaScript codegen program"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter on changed files"),
			),
			mcpgo.WithBoolean("validate",
				mcpgo.Description("Validate changes for parse errors and structural issues"),
			),
		),

		// apply
		mcpgo.NewTool("apply",
			mcpgo.WithDescription("Apply pending changes to disk. Call after any transform/rename/codegen that produced diffs you want to keep."),
			mcpgo.WithBoolean("confirm",
				mcpgo.Required(),
				mcpgo.Description("Must be true to actually write files"),
			),
		),

		// undo
		mcpgo.NewTool("undo",
			mcpgo.WithDescription("Restore files from the backups created by the last apply."),
		),

		// teach_recipe
		mcpgo.NewTool("teach_recipe",
			mcpgo.WithDescription("Save a named, parameterised transformation recipe for later use with instantiate."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Recipe name"),
			),
			mcpgo.WithString("description",
				mcpgo.Description("Human-readable description of what the recipe does"),
			),
			mcpgo.WithString("params",
				mcpgo.Description("JSON array of parameter names"),
			),
			mcpgo.WithString("steps",
				mcpgo.Required(),
				mcpgo.Description("JSON array of transform step specifications"),
			),
		),

		// instantiate
		mcpgo.NewTool("instantiate",
			mcpgo.WithDescription("Run a saved recipe with concrete parameter values."),
			mcpgo.WithString("recipe",
				mcpgo.Required(),
				mcpgo.Description("Recipe name"),
			),
			mcpgo.WithString("params",
				mcpgo.Description("JSON object mapping parameter names to values"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after instantiating"),
			),
		),

		// list_recipes
		mcpgo.NewTool("list_recipes",
			mcpgo.WithDescription("List all saved recipes."),
		),

		// teach_convention
		mcpgo.NewTool("teach_convention",
			mcpgo.WithDescription("Save a named convention check (JavaScript program) that can be run with check_conventions."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Convention name"),
			),
			mcpgo.WithString("description",
				mcpgo.Description("What the convention enforces"),
			),
			mcpgo.WithString("check_program",
				mcpgo.Required(),
				mcpgo.Description("JavaScript program that returns an array of violation strings"),
			),
		),

		// check_conventions
		mcpgo.NewTool("check_conventions",
			mcpgo.WithDescription("Run all saved convention checks against the codebase and report violations."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
		),

		// list_conventions
		mcpgo.NewTool("list_conventions",
			mcpgo.WithDescription("List all saved convention checks."),
		),

		// get_agent_prompt
		mcpgo.NewTool("get_agent_prompt",
			mcpgo.WithDescription("Return the Sawmill agent guide — a detailed reference for AI coding agents on how to use all Sawmill tools."),
		),

		// teach_by_example
		mcpgo.NewTool("teach_by_example",
			mcpgo.WithDescription("Extract a reusable transform template from an exemplar code snippet by replacing concrete values with parameter placeholders."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Recipe name to save the extracted template as"),
			),
			mcpgo.WithString("description",
				mcpgo.Description("What the template does"),
			),
			mcpgo.WithString("exemplar",
				mcpgo.Required(),
				mcpgo.Description("Example source snippet to templatise"),
			),
			mcpgo.WithString("parameters",
				mcpgo.Required(),
				mcpgo.Description("JSON object mapping parameter names to their concrete values in the exemplar"),
			),
			mcpgo.WithString("also_affects",
				mcpgo.Description("JSON array of additional files or patterns also affected by the template"),
			),
		),

		// add_parameter
		mcpgo.NewTool("add_parameter",
			mcpgo.WithDescription("Add a parameter to a function signature across the codebase."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithString("function",
				mcpgo.Required(),
				mcpgo.Description("Name of the function to modify"),
			),
			mcpgo.WithString("param_name",
				mcpgo.Required(),
				mcpgo.Description("New parameter name"),
			),
			mcpgo.WithString("param_type",
				mcpgo.Description("Parameter type (for typed languages)"),
			),
			mcpgo.WithString("default_value",
				mcpgo.Description("Default value for the parameter"),
			),
			mcpgo.WithString("position",
				mcpgo.Description("Where to insert: first, last (default), or a 1-based index"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after modifying"),
			),
		),

		// add_field
		mcpgo.NewTool("add_field",
			mcpgo.WithDescription("Add a field to a struct/class and propagate to construction sites: factory function signatures, struct literals, and factory callers."),
			mcpgo.WithString("type_name",
				mcpgo.Required(),
				mcpgo.Description("Name of the struct/class/type to modify"),
			),
			mcpgo.WithString("field_name",
				mcpgo.Required(),
				mcpgo.Description("Name of the new field"),
			),
			mcpgo.WithString("field_type",
				mcpgo.Required(),
				mcpgo.Description("Type of the new field"),
			),
			mcpgo.WithString("default_value",
				mcpgo.Required(),
				mcpgo.Description("Expression to use at construction sites"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after changes"),
			),
		),

		// remove_parameter
		mcpgo.NewTool("remove_parameter",
			mcpgo.WithDescription("Remove a parameter from a function signature across the codebase."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithString("function",
				mcpgo.Required(),
				mcpgo.Description("Name of the function to modify"),
			),
			mcpgo.WithString("param_name",
				mcpgo.Required(),
				mcpgo.Description("Parameter name to remove"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after modifying"),
			),
		),

		// clone_and_adapt
		mcpgo.NewTool("clone_and_adapt",
			mcpgo.WithDescription("Copy a symbol or code region, apply string substitutions, and insert at a target location. One-shot copy-and-modify without templatisation."),
			mcpgo.WithString("source",
				mcpgo.Required(),
				mcpgo.Description("Symbol name or file:start_line-end_line range to clone"),
			),
			mcpgo.WithString("substitutions",
				mcpgo.Required(),
				mcpgo.Description("JSON object mapping old strings to new strings"),
			),
			mcpgo.WithString("target_file",
				mcpgo.Required(),
				mcpgo.Description("File path where the clone should be inserted"),
			),
			mcpgo.WithString("position",
				mcpgo.Description("Where to insert: end (default), start, or after:<symbol_name>"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after insertion"),
			),
		),

		// hover
		mcpgo.NewTool("hover",
			mcpgo.WithDescription("Query the language server for type/hover information at a position."),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path"),
			),
			mcpgo.WithNumber("line",
				mcpgo.Required(),
				mcpgo.Description("Line number (1-based)"),
			),
			mcpgo.WithNumber("column",
				mcpgo.Required(),
				mcpgo.Description("Column number (1-based)"),
			),
		),

		// definition
		mcpgo.NewTool("definition",
			mcpgo.WithDescription("Query the language server for the definition location of a symbol at a position."),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path"),
			),
			mcpgo.WithNumber("line",
				mcpgo.Required(),
				mcpgo.Description("Line number (1-based)"),
			),
			mcpgo.WithNumber("column",
				mcpgo.Required(),
				mcpgo.Description("Column number (1-based)"),
			),
		),

		// lsp_references
		mcpgo.NewTool("lsp_references",
			mcpgo.WithDescription("Query the language server for all references to a symbol at a position."),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path"),
			),
			mcpgo.WithNumber("line",
				mcpgo.Required(),
				mcpgo.Description("Line number (1-based)"),
			),
			mcpgo.WithNumber("column",
				mcpgo.Required(),
				mcpgo.Description("Column number (1-based)"),
			),
		),

		// diagnostics
		mcpgo.NewTool("diagnostics",
			mcpgo.WithDescription("Query the language server for diagnostics (errors, warnings) in a file."),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path"),
			),
		),

		// teach_invariant
		mcpgo.NewTool("teach_invariant",
			mcpgo.WithDescription("Save a named structural invariant (JSON rule) that can be checked with check_invariants."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Invariant name"),
			),
			mcpgo.WithString("description",
				mcpgo.Description("Human-readable description of what the invariant enforces"),
			),
			mcpgo.WithString("rule",
				mcpgo.Required(),
				mcpgo.Description(`JSON rule object. Example: {"for_each":{"kind":"type","name":"*Config"},"require":[{"has_field":{"name":"Name","type":"string"}}]}`),
			),
		),

		// check_invariants
		mcpgo.NewTool("check_invariants",
			mcpgo.WithDescription("Run all saved structural invariants against the codebase and report violations."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
		),

		// list_invariants
		mcpgo.NewTool("list_invariants",
			mcpgo.WithDescription("List all saved structural invariants."),
		),

		// delete_invariant
		mcpgo.NewTool("delete_invariant",
			mcpgo.WithDescription("Delete a saved structural invariant by name."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Invariant name to delete"),
			),
		),

		// dependency_usage
		mcpgo.NewTool("dependency_usage",
			mcpgo.WithDescription("Analyse dependency usage: import sites, symbols used, call sites, and public API exposure for a given package."),
			mcpgo.WithString("package",
				mcpgo.Required(),
				mcpgo.Description("Import path or module name to analyse"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
		),
	}
}
