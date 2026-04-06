// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package mcp implements the Sawmill MCP server using mcp-go. It exposes all
// sawmill tools over the Model Context Protocol and can be served over stdio
// or any other transport supported by mcp-go.
package mcp

import (
	"context"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/model"
)

// PendingChanges holds the last set of pending file changes produced by a
// transform/rename/codegen call, waiting for an explicit apply.
type PendingChanges struct {
	Changes []forest.FileChange
	Diffs   []string
}

// LastBackups holds the backup paths written by the most recent apply, so
// that undo can restore them.
type LastBackups struct {
	Paths []string
}

// SawmillServer is the top-level MCP server. It wraps mcp-go's MCPServer and
// holds session state (the active codebase model, pending changes, and backups).
//
// All exported methods are safe for concurrent use — the mu field serialises
// access to model, pending, and lastBackups.
type SawmillServer struct {
	mu          sync.Mutex
	model       *model.CodebaseModel
	pending     *PendingChanges
	lastBackups *LastBackups
}

// NewServer creates a new SawmillServer. The model is nil until the first
// successful parse call.
func NewServer() *SawmillServer {
	return &SawmillServer{}
}

// Serve registers all tools, then blocks serving MCP requests over stdio until
// the process exits or ctx is cancelled.
func (s *SawmillServer) Serve(ctx context.Context) error {
	srv := server.NewMCPServer(
		"sawmill",
		"0.5.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	s.registerTools(srv)

	// ServeStdio blocks until EOF on stdin or an error occurs. Context
	// cancellation is honoured by the caller (the process) exiting.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ServeStdio(srv)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// registerTools adds all Sawmill MCP tools to srv.
func (s *SawmillServer) registerTools(srv *server.MCPServer) {
	// parse
	srv.AddTool(mcpgo.NewTool("parse",
		mcpgo.WithDescription("Parse a source tree. Must be called before any other tool. Returns a summary of the parsed codebase."),
		mcpgo.WithString("path",
			mcpgo.Required(),
			mcpgo.Description("Root directory or single file to parse"),
		),
	), s.handleParse)

	// rename
	srv.AddTool(mcpgo.NewTool("rename",
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
	), s.handleRename)

	// query
	srv.AddTool(mcpgo.NewTool("query",
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
	), s.handleQuery)

	// find_symbol
	srv.AddTool(mcpgo.NewTool("find_symbol",
		mcpgo.WithDescription("Look up a symbol by name in the persistent symbol index."),
		mcpgo.WithString("symbol",
			mcpgo.Required(),
			mcpgo.Description("Symbol name to find"),
		),
		mcpgo.WithString("kind",
			mcpgo.Description("Optional kind filter (function, type, call, …)"),
		),
	), s.handleFindSymbol)

	// find_references
	srv.AddTool(mcpgo.NewTool("find_references",
		mcpgo.WithDescription("Find all call sites for a symbol."),
		mcpgo.WithString("symbol",
			mcpgo.Required(),
			mcpgo.Description("Symbol name to search for call sites"),
		),
	), s.handleFindReferences)

	// transform
	srv.AddTool(mcpgo.NewTool("transform",
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
	), s.handleTransform)

	// transform_batch
	srv.AddTool(mcpgo.NewTool("transform_batch",
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
	), s.handleTransformBatch)

	// codegen
	srv.AddTool(mcpgo.NewTool("codegen",
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
	), s.handleCodegen)

	// apply
	srv.AddTool(mcpgo.NewTool("apply",
		mcpgo.WithDescription("Apply pending changes to disk. Call after any transform/rename/codegen that produced diffs you want to keep."),
		mcpgo.WithBoolean("confirm",
			mcpgo.Required(),
			mcpgo.Description("Must be true to actually write files"),
		),
	), s.handleApply)

	// undo
	srv.AddTool(mcpgo.NewTool("undo",
		mcpgo.WithDescription("Restore files from the backups created by the last apply."),
	), s.handleUndo)

	// teach_recipe
	srv.AddTool(mcpgo.NewTool("teach_recipe",
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
	), s.handleTeachRecipe)

	// instantiate
	srv.AddTool(mcpgo.NewTool("instantiate",
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
	), s.handleInstantiate)

	// list_recipes
	srv.AddTool(mcpgo.NewTool("list_recipes",
		mcpgo.WithDescription("List all saved recipes."),
	), s.handleListRecipes)

	// teach_convention
	srv.AddTool(mcpgo.NewTool("teach_convention",
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
	), s.handleTeachConvention)

	// check_conventions
	srv.AddTool(mcpgo.NewTool("check_conventions",
		mcpgo.WithDescription("Run all saved convention checks against the codebase and report violations."),
		mcpgo.WithString("path",
			mcpgo.Description("Restrict to a specific file or directory"),
		),
	), s.handleCheckConventions)

	// list_conventions
	srv.AddTool(mcpgo.NewTool("list_conventions",
		mcpgo.WithDescription("List all saved convention checks."),
	), s.handleListConventions)

	// get_agent_prompt
	srv.AddTool(mcpgo.NewTool("get_agent_prompt",
		mcpgo.WithDescription("Return the Sawmill agent guide — a detailed reference for AI coding agents on how to use all Sawmill tools."),
	), s.handleGetAgentPrompt)

	// teach_by_example
	srv.AddTool(mcpgo.NewTool("teach_by_example",
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
	), s.handleTeachByExample)

	// add_parameter
	srv.AddTool(mcpgo.NewTool("add_parameter",
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
	), s.handleAddParameter)

	// remove_parameter
	srv.AddTool(mcpgo.NewTool("remove_parameter",
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
	), s.handleRemoveParameter)
}
