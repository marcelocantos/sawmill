// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package mcp implements the Sawmill MCP tool handler. Each MCP session gets
// its own *Handler instance via the SessionPool, which holds the per-session
// CodebaseModel, pending changes, and backups.
package mcp

import (
	"context"
	"fmt"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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

// ModelLoader resolves a project root to a CodebaseModel and a release
// callback. Implementations can ref-count shared models (see modelpool.Pool)
// or return one-shot models that the release callback closes directly.
//
// Returning (nil, nil, err) signals a load failure.
type ModelLoader func(root string) (*model.CodebaseModel, func(), error)

// directLoader loads a fresh model and closes it on release. Used when no
// shared pool is configured (notably for in-memory tests).
func directLoader(root string) (*model.CodebaseModel, func(), error) {
	m, err := model.Load(root)
	if err != nil {
		return nil, nil, err
	}
	return m, func() { _ = m.Close() }, nil
}

// Handler holds the per-session state for a single MCP client: the active
// codebase model, pending changes, and backups.
//
// All exported methods are safe for concurrent use — the mu field serialises
// access to model, pending, and lastBackups.
type Handler struct {
	mu          sync.Mutex
	model       *model.CodebaseModel
	pending     *PendingChanges
	lastBackups *LastBackups

	loader  ModelLoader
	release func() // set when a model is loaded, cleared on Close
}

// NewHandler creates a new Handler with the default direct loader. The model
// is nil until the first successful parse call.
func NewHandler() *Handler {
	return &Handler{loader: directLoader}
}

// NewHandlerWithLoader creates a new Handler whose parse calls use the given
// loader. Use this when sharing models across sessions via a pool.
func NewHandlerWithLoader(loader ModelLoader) *Handler {
	if loader == nil {
		loader = directLoader
	}
	return &Handler{loader: loader}
}

// NewHandlerWithModel creates a Handler pre-loaded with an existing
// CodebaseModel. The release callback (if non-nil) is invoked on Close.
// Subsequent parse calls fall back to a direct loader.
func NewHandlerWithModel(m *model.CodebaseModel, release func()) *Handler {
	return &Handler{model: m, loader: directLoader, release: release}
}

// Close releases any resources held by the handler. Safe to call multiple
// times.
func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.release != nil {
		h.release()
		h.release = nil
	}
	h.model = nil
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
	case "migrate_type":
		return h.handleMigrateType(args)
	case "git_index":
		return h.handleGitIndex(args)
	case "git_log":
		return h.handleGitLog(args)
	case "git_diff_summary":
		return h.handleGitDiffSummary(args)
	case "git_blame_symbol":
		return h.handleGitBlameSymbol(args)
	case "semantic_diff":
		return h.handleSemanticDiff(args)
	case "api_changelog":
		return h.handleAPIChangelog(args)
	case "git_semantic_bisect":
		return h.handleGitSemanticBisect(args)
	case "teach_equivalence":
		return h.handleTeachEquivalence(args)
	case "list_equivalences":
		return h.handleListEquivalences(args)
	case "delete_equivalence":
		return h.handleDeleteEquivalence(args)
	case "apply_equivalence":
		return h.handleApplyEquivalence(args)
	case "check_equivalences":
		return h.handleCheckEquivalences(args)
	case "teach_fix":
		return h.handleTeachFix(args)
	case "list_fixes":
		return h.handleListFixes(args)
	case "delete_fix":
		return h.handleDeleteFix(args)
	case "auto_fix":
		return h.handleAutoFix(args)
	case "seed_fixes":
		return h.handleSeedFixes(args)
	case "learn_from_observation":
		return h.handleLearnFromObservation(args)
	case "promote_constant":
		return h.handlePromoteConstant(args)
	default:
		return "", false, fmt.Errorf("unknown tool: %s", name)
	}
}

// HandlerResolver returns the Handler bound to the calling MCP session,
// creating one on first use. A nil ctx returns a transient handler.
type HandlerResolver func(ctx context.Context) *Handler

// RegisterTools registers every Sawmill tool with the given mcp-go server.
// Each tool's handler resolves the per-session *Handler via resolve() and
// dispatches by tool name through Handler.Call.
func RegisterTools(srv *server.MCPServer, resolve HandlerResolver) {
	for _, def := range Definitions() {
		name := def.Name
		srv.AddTool(def, makeToolHandler(resolve, name))
	}
}

func makeToolHandler(resolve HandlerResolver, name string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		h := resolve(ctx)
		if h == nil {
			return mcpgo.NewToolResultError("no handler available for session"), nil
		}
		text, isError, err := h.Call(name, req.GetArguments())
		if err != nil {
			return mcpgo.NewToolResultErrorFromErr("internal error", err), nil
		}
		if isError {
			return mcpgo.NewToolResultError(text), nil
		}
		return mcpgo.NewToolResultText(text), nil
	}
}

// Definitions returns the MCP tool definitions for all Sawmill tools.
// They are registered automatically by RegisterTools; exposed here for
// introspection and testing.
func Definitions() []mcpgo.Tool {
	return []mcpgo.Tool{
		// parse
		mcpgo.NewTool("parse",
			mcpgo.WithDescription("Parse a source tree. The first call in an MCP session must specify path; subsequent calls re-use the loaded model. Returns a summary of the parsed codebase."),
			mcpgo.WithString("path",
				mcpgo.Description("Root directory or single file to parse (required on first call in a session)"),
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
			mcpgo.WithDescription("Find AST nodes matching a kind/name pattern or a raw Tree-sitter query. Returns matches without modifying files. Set format=\"json\" for a structured array of {file, line, column, kind, name, snippet} objects suitable for programmatic consumption."),
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
			mcpgo.WithString("format",
				mcpgo.Description("Output format: \"text\" (default, human-readable) or \"json\" (array of QueryMatch objects)"),
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
			mcpgo.WithDescription("Run all saved convention checks against the codebase and report violations. Convention check programs may return either an array of strings (legacy) or an array of {file, line, column, severity, rule, message, snippet, suggested_fix} objects. Set format=\"json\" to receive a structured array of Violation objects suitable for programmatic consumption."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithString("format",
				mcpgo.Description("Output format: \"text\" (default, human-readable) or \"json\" (Violation array)"),
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
			mcpgo.WithDescription("Query the language server for diagnostics (errors, warnings) in a file. Set format=\"json\" for a structured array of {file, line, column, severity, code, source, message} objects suitable for programmatic consumption (e.g. driving auto_fix)."),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path"),
			),
			mcpgo.WithString("format",
				mcpgo.Description("Output format: \"text\" (default, human-readable) or \"json\" (Diagnostic array)"),
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
			mcpgo.WithDescription("Run all saved structural invariants against the codebase and report violations. Set format=\"json\" to receive a structured array of {source, file, line, column, severity, rule, message} Violation objects suitable for programmatic consumption."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to a specific file or directory"),
			),
			mcpgo.WithString("format",
				mcpgo.Description("Output format: \"text\" (default, human-readable) or \"json\" (Violation array)"),
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

		// migrate_type
		mcpgo.NewTool("migrate_type",
			mcpgo.WithDescription("Rewrite all usage sites of a type: construction patterns, field/method access, and optionally rename the type. Uses a pattern language with $placeholder captures."),
			mcpgo.WithString("type_name",
				mcpgo.Required(),
				mcpgo.Description("Name of the type to migrate"),
			),
			mcpgo.WithString("rules",
				mcpgo.Required(),
				mcpgo.Description("JSON object with construction, field_access, and/or type_rename rules"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter after migration"),
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

		// git_index
		mcpgo.NewTool("git_index",
			mcpgo.WithDescription("Index git commits by parsing their files with Tree-sitter and storing AST nodes in the git index. Enables structural queries over git history."),
			mcpgo.WithString("ref",
				mcpgo.Description("Starting ref (branch, tag, or commit SHA). Defaults to HEAD."),
			),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Maximum number of commits to index (0 = all, default 0)."),
			),
		),

		// git_log
		mcpgo.NewTool("git_log",
			mcpgo.WithDescription("Structured commit history with file-change metadata from the semantic git index."),
			mcpgo.WithString("ref",
				mcpgo.Description("Starting ref (branch, tag, SHA). Default: HEAD"),
			),
			mcpgo.WithNumber("limit",
				mcpgo.Description("Max commits to return. Default: 20"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Filter to commits touching this file path"),
			),
		),

		// git_diff_summary
		mcpgo.NewTool("git_diff_summary",
			mcpgo.WithDescription("Symbol-level diff between two refs — shows added/removed/modified functions and types per file."),
			mcpgo.WithString("base",
				mcpgo.Required(),
				mcpgo.Description("Base ref (branch, tag, SHA)"),
			),
			mcpgo.WithString("head",
				mcpgo.Description("Head ref. Default: HEAD"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Filter to a specific file path"),
			),
		),

		// git_blame_symbol
		mcpgo.NewTool("git_blame_symbol",
			mcpgo.WithDescription("Trace a symbol's history. Returns the commit that introduced it, the commit that last modified its declaration, and (for functions) separate commits for body_last_modified and signature_last_changed — distinguishing whose interface changed vs. whose implementation changed."),
			mcpgo.WithString("path",
				mcpgo.Required(),
				mcpgo.Description("File path containing the symbol"),
			),
			mcpgo.WithString("symbol",
				mcpgo.Required(),
				mcpgo.Description("Symbol name to trace"),
			),
			mcpgo.WithString("ref",
				mcpgo.Description("Starting ref. Default: HEAD"),
			),
		),

		// semantic_diff
		mcpgo.NewTool("semantic_diff",
			mcpgo.WithDescription("Structural AST-level diff between two refs. Detects moves (symbol deleted from one file, added to another), renames (name changed but structure preserved), parameter/return type changes, and key-level changes in data formats (JSON, YAML). Produces richer output than git_diff_summary."),
			mcpgo.WithString("base",
				mcpgo.Required(),
				mcpgo.Description("Base ref (branch, tag, SHA)"),
			),
			mcpgo.WithString("head",
				mcpgo.Description("Head ref. Default: HEAD"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Filter to a specific file path"),
			),
		),

		// api_changelog
		mcpgo.NewTool("api_changelog",
			mcpgo.WithDescription("Generate a markdown API surface changelog between two refs. Lists added/removed/changed public symbols with signature changes, moves, and renames."),
			mcpgo.WithString("base",
				mcpgo.Required(),
				mcpgo.Description("Base ref (tag, branch, SHA) — typically the older release"),
			),
			mcpgo.WithString("head",
				mcpgo.Description("Head ref. Default: HEAD"),
			),
		),

		// teach_equivalence
		mcpgo.NewTool("teach_equivalence",
			mcpgo.WithDescription(`Save a named bidirectional code-pattern pair (e.g. errors.Is(err, X) ↔ err == X). Patterns use the same $placeholder DSL as migrate_type and teach_by_example. Captures bound on the matched side are reused when rewriting to the other side. The optional preferred_direction lets check_equivalences flag the non-preferred form as a violation.`),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Equivalence name (used for apply_equivalence and delete_equivalence)"),
			),
			mcpgo.WithString("left_pattern",
				mcpgo.Required(),
				mcpgo.Description("Left-hand pattern (e.g. \"errors.Is($err, $target)\")"),
			),
			mcpgo.WithString("right_pattern",
				mcpgo.Required(),
				mcpgo.Description("Right-hand pattern (e.g. \"$err == $target\")"),
			),
			mcpgo.WithString("description",
				mcpgo.Description("Human-readable description of what the equivalence captures"),
			),
			mcpgo.WithString("preferred_direction",
				mcpgo.Description("Which side to prefer: \"left\" or \"right\". Omit for no preference."),
			),
		),

		// list_equivalences
		mcpgo.NewTool("list_equivalences",
			mcpgo.WithDescription("List all saved equivalence pairs."),
		),

		// delete_equivalence
		mcpgo.NewTool("delete_equivalence",
			mcpgo.WithDescription("Delete a saved equivalence by name."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Equivalence name to delete"),
			),
		),

		// apply_equivalence
		mcpgo.NewTool("apply_equivalence",
			mcpgo.WithDescription("Rewrite all matches of an equivalence pair in the chosen direction. Produces a unified diff per file gated by the standard apply/undo flow."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Name of the saved equivalence to apply"),
			),
			mcpgo.WithString("direction",
				mcpgo.Required(),
				mcpgo.Description("\"left_to_right\" rewrites left-pattern matches to the right pattern; \"right_to_left\" rewrites the other way"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter on changed files"),
			),
		),

		// check_equivalences
		mcpgo.NewTool("check_equivalences",
			mcpgo.WithDescription("Scan the codebase for matches of any equivalence's non-preferred side. Reports each as a violation with file, location, the matched text, and the suggested rewrite. Equivalences with no preferred direction are skipped."),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
		),

		// teach_fix
		mcpgo.NewTool("teach_fix",
			mcpgo.WithDescription(`Save a diagnostic-pattern → fix-action mapping. The diagnostic regex matches against an LSP diagnostic message; named captures (e.g. (?P<pkg>\w+)) are bound and may be referenced in the action via ${pkg}. The action is JSON: either {"recipe": "name", "params": {...}} for a recipe reference, or {"transform": {...}} for an inline transform spec. Confidence "auto" means auto_fix applies the fix automatically; "suggest" means it's reported but not applied.`),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Fix entry name (used for delete_fix and as the catalogue key)"),
			),
			mcpgo.WithString("diagnostic_regex",
				mcpgo.Required(),
				mcpgo.Description(`Regex matched against the diagnostic message. Use Go regexp syntax with named captures, e.g. "imported and not used: \"(?P<pkg>[^\"]+)\""`),
			),
			mcpgo.WithString("action",
				mcpgo.Required(),
				mcpgo.Description(`JSON action spec. Examples: {"recipe":"remove-import","params":{"name":"${pkg}"}} or {"transform":{"kind":"import","name":"${pkg}","action":"remove"}}`),
			),
			mcpgo.WithString("confidence",
				mcpgo.Description(`"auto" (apply automatically) or "suggest" (report only). Default: "suggest"`),
			),
			mcpgo.WithString("description",
				mcpgo.Description("Human-readable description of what the fix does"),
			),
		),

		// list_fixes
		mcpgo.NewTool("list_fixes",
			mcpgo.WithDescription("List all saved diagnostic-fix entries with their regex, confidence, and action."),
		),

		// delete_fix
		mcpgo.NewTool("delete_fix",
			mcpgo.WithDescription("Delete a saved fix entry by name."),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Fix name to delete"),
			),
		),

		// promote_constant
		mcpgo.NewTool("promote_constant",
			mcpgo.WithDescription("Replace every occurrence of a literal value with a reference to a named constant. The constant declaration is generated in idiomatic per-language form (Go: const, Python: UPPER_CASE = ..., TS: const, Rust: const NAME: &str = ..., C++: constexpr auto) and inserted after the file's preamble (package/imports/includes). Idempotent: re-running with the same name+value detects an existing declaration and only rewrites occurrences."),
			mcpgo.WithString("literal",
				mcpgo.Required(),
				mcpgo.Description(`Literal source text to replace, e.g. "\"foo\"" for a string, "42" for a number — match is exact`),
			),
			mcpgo.WithString("name",
				mcpgo.Required(),
				mcpgo.Description("Identifier for the new constant (e.g. DefaultTimeout, MAX_RETRIES)"),
			),
			mcpgo.WithString("path",
				mcpgo.Description("Restrict to files matching this path substring"),
			),
			mcpgo.WithBoolean("format",
				mcpgo.Description("Run the language formatter on changed files"),
			),
		),

		// learn_from_observation
		mcpgo.NewTool("learn_from_observation",
			mcpgo.WithDescription("Infer candidate fix-catalogue entries from a pre/post diagnostic snapshot. Takes pre_diagnostics and post_diagnostics (both JSON arrays from `diagnostics format=json`); diagnostics that disappeared in the post-state become candidate entries. Each candidate has a generalised regex (quoted runs become named captures), a draft suggested name, and a placeholder action JSON the user fills in before calling teach_fix to make it permanent."),
			mcpgo.WithString("pre_diagnostics",
				mcpgo.Required(),
				mcpgo.Description("Pre-edit diagnostic snapshot — a JSON array of Diagnostic objects (typically captured via `diagnostics format=json` before applying changes)"),
			),
			mcpgo.WithString("post_diagnostics",
				mcpgo.Description("Post-edit diagnostic snapshot. Defaults to []; resolved diagnostics are those present in pre but absent from post."),
			),
		),

		// seed_fixes
		mcpgo.NewTool("seed_fixes",
			mcpgo.WithDescription("Install a curated starter catalogue of fix entries for common Go (unused import, missing struct field, return-type mismatch) and TypeScript (TS2304 cannot find name, TS6133 declared but never read, TS2532 possibly undefined) errors. Idempotent: existing entries with the same name are kept (delete them first to overwrite). Auto-confidence entries are known-safe inline transforms; suggest entries describe a recommended fix without applying it."),
		),

		// auto_fix
		mcpgo.NewTool("auto_fix",
			mcpgo.WithDescription(`Convergence loop driving diagnostic-driven fixes. Each iteration: pulls diagnostics for the file, matches each against the saved fix catalogue (taught via teach_fix), applies entries marked confidence="auto", and reports entries marked confidence="suggest". Terminates clean (no diagnostics), stuck (no fixes applied this iteration), or at the iteration limit. Cycle detection: a diagnostic that reappears verbatim after its fix was applied flags the fix as broken. Returns a structured JSON result with per-iteration outcomes, suggestions, and cycle warnings.`),
			mcpgo.WithString("file",
				mcpgo.Required(),
				mcpgo.Description("Absolute file path to drive diagnostics for"),
			),
			mcpgo.WithNumber("max_iterations",
				mcpgo.Description("Stop after this many iterations even if diagnostics remain. Default: 10"),
			),
			mcpgo.WithBoolean("dry_run",
				mcpgo.Description("If true, report what would be applied without modifying any files. Default: false"),
			),
		),

		// git_semantic_bisect
		mcpgo.NewTool("git_semantic_bisect",
			mcpgo.WithDescription(`Find the commit where a structural predicate over the AST flipped, without running the code. Binary-searches the first-parent chain between good and bad refs, indexing commits lazily, and returns the flip commit plus the specific structural change that caused it.

The predicate is a JSON object. Supported kinds:
  {"kind":"symbol_exists","name":"Foo"}
  {"kind":"function_has_param","function":"Foo","param":"ctx"}
  {"kind":"type_has_field","type":"Config","field":"Verbose"}

All predicates accept an optional "file" key to restrict evaluation to one path.`),
			mcpgo.WithString("predicate",
				mcpgo.Required(),
				mcpgo.Description("JSON-encoded structural predicate (see tool description)"),
			),
			mcpgo.WithString("good",
				mcpgo.Required(),
				mcpgo.Description("Good ref — commit where the predicate has its expected value"),
			),
			mcpgo.WithString("bad",
				mcpgo.Required(),
				mcpgo.Description("Bad ref — commit where the predicate has its unexpected value (must be a descendant of good)"),
			),
		),
	}
}
