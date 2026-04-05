// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

use rmcp::handler::server::router::tool::ToolRouter;
use rmcp::handler::server::wrapper::Parameters;
use rmcp::model::{ServerCapabilities, ServerInfo};
use rmcp::{schemars, tool, tool_handler, tool_router, ServerHandler, ServiceExt};
use tree_sitter::{Parser, Query, QueryCursor};
use streaming_iterator::StreamingIterator;

use crate::codegen;
use crate::exemplar;
use crate::forest::{FileChange, Forest, ParsedFile};
use crate::model::CodebaseModel;
use crate::rewrite;
use crate::transform;

/// Pending changes from the last transform, waiting to be applied.
struct PendingChanges {
    changes: Vec<FileChange>,
    description: String,
}

pub struct CanopyServer {
    tool_router: ToolRouter<Self>,
    pending: Mutex<Option<PendingChanges>>,
    /// Backup paths from the last apply, for undo.
    last_backups: Mutex<Option<(Vec<PathBuf>, String)>>,
    /// Persistent codebase model, loaded on first `parse` call.
    model: Mutex<Option<CodebaseModel>>,
}

// --- Parameter types ---

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ParseParams {
    /// Path to parse (file or directory).
    path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct RenameParams {
    /// Current symbol name.
    from: String,
    /// New symbol name.
    to: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,
    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct QueryParams {
    /// Path scope (file or directory).
    #[serde(default = "default_path")]
    path: String,
    /// Abstract node kind to search for: "function", "class", "call", "import".
    kind: String,
    /// Optional name filter (exact match or glob with *).
    #[serde(default)]
    name: Option<String>,
    /// Optional file path filter.
    #[serde(default)]
    file: Option<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct FindSymbolParams {
    /// Symbol name to find.
    symbol: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct FindReferencesParams {
    /// Symbol name to find references of.
    symbol: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct HoverParams {
    /// File path containing the symbol.
    file: String,
    /// Line number (1-based).
    line: u32,
    /// Column number (1-based).
    column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct DefinitionParams {
    /// File path containing the symbol.
    file: String,
    /// Line number (1-based).
    line: u32,
    /// Column number (1-based).
    column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct LspReferencesParams {
    /// File path containing the symbol.
    file: String,
    /// Line number (1-based).
    line: u32,
    /// Column number (1-based).
    column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct DiagnosticsParams {
    /// File path to check.
    file: String,
    /// Optional modified content to check (if omitted, checks the file on disk).
    #[serde(default)]
    content: Option<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct TransformParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,

    // --- Matching (pick one) ---

    /// Abstract node kind: "function", "class", "call", "import".
    #[serde(default)]
    kind: Option<String>,
    /// Name filter for abstract matching (exact or glob with *).
    #[serde(default)]
    name: Option<String>,
    /// File filter for abstract matching.
    #[serde(default)]
    file: Option<String>,
    /// Raw Tree-sitter query (alternative to kind/name matching).
    #[serde(default)]
    raw_query: Option<String>,
    /// Capture name to act on for raw queries (defaults to first).
    #[serde(default)]
    capture: Option<String>,

    // --- Action (pick one: action or transform_fn) ---

    /// Action to perform: "replace", "wrap", "unwrap", "prepend_statement",
    /// "append_statement", "remove", "replace_name", "replace_body".
    /// Omit if using transform_fn.
    #[serde(default)]
    action: Option<String>,
    /// Code to inject (for replace, wrap/before, prepend, append, replace_name, replace_body).
    #[serde(default)]
    code: Option<String>,
    /// "before" text for wrap action.
    #[serde(default)]
    before: Option<String>,
    /// "after" text for wrap action.
    #[serde(default)]
    after: Option<String>,

    /// JavaScript transform function (alternative to action).
    /// Receives a node object with properties (kind, name, text, body, parameters,
    /// file, startLine, endLine) and mutation methods (replaceText, replaceBody,
    /// replaceName, remove, wrap, insertBefore, insertAfter).
    /// Return the original node (unchanged), a mutated node, null (delete), or a string (replace).
    #[serde(default)]
    transform_fn: Option<String>,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct CodegenParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,

    /// JavaScript program to execute against the codebase model.
    /// Receives a global `ctx` object with methods:
    /// - ctx.findFunction(name) → array of nodes
    /// - ctx.findType(name) → array of nodes
    /// - ctx.query({kind, name, file}) → array of nodes (name supports * glob)
    /// - ctx.references(name) → array of call-site nodes
    /// - ctx.readFile(path) → file content string or null
    /// - ctx.addFile(path, content) → create a new file
    /// - ctx.editFile(path, startByte, endByte, replacement) → raw byte-range edit
    /// Nodes have methods: replaceText, replaceBody, replaceName, remove, insertBefore, insertAfter
    program: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,

    /// Validate that modified files parse correctly. Defaults to true.
    #[serde(default = "default_true")]
    validate: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ApplyParams {
    /// Set to true to confirm applying the pending changes.
    confirm: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct UndoParams {}

fn default_true() -> bool { true }

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct TransformBatchParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,

    /// Ordered list of transforms to apply. Each element is either:
    /// - `{"rename": {"from": "old", "to": "new"}}` for a rename
    /// - A match/act transform (same fields as the `transform` tool)
    transforms: Vec<serde_json::Value>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct TeachRecipeParams {
    /// Unique name for this recipe.
    name: String,
    /// Human-readable description.
    #[serde(default)]
    description: String,
    /// Parameter names that will be substituted (e.g. ["name", "type"]).
    params: Vec<String>,
    /// Ordered list of transform steps (same format as transform_batch).
    /// Use $param_name in string values for substitution.
    steps: serde_json::Value,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct InstantiateParams {
    /// Name of the recipe to instantiate.
    recipe: String,
    /// Parameter values to substitute (e.g. {"name": "User", "type": "struct"}).
    params: HashMap<String, String>,
    /// Path scope. Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,
    /// Run the language formatter. Defaults to false.
    #[serde(default)]
    format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ListRecipesParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct TeachConventionParams {
    /// Unique name for this convention.
    name: String,
    /// Human-readable description (e.g., "Every public function must have a doc comment").
    #[serde(default)]
    description: String,
    /// JavaScript check program. Receives a global `ctx` object (same as codegen).
    /// Must return an array of violation strings, or an empty array if the convention is satisfied.
    check_program: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct CheckConventionsParams {
    /// Path scope. Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ListConventionsParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct TeachByExampleParams {
    /// Unique name for this pattern.
    name: String,
    /// Human-readable description.
    #[serde(default)]
    description: String,
    /// Path to the exemplar file.
    exemplar: String,
    /// Parameter name → value mapping. For example:
    /// {"name": "users", "entity": "User"}.
    /// All occurrences of these values (and their case variants)
    /// will be replaced with $param_name placeholders.
    parameters: HashMap<String, String>,
    /// Additional files or directories affected by this pattern.
    /// Files containing parameter values will be included in the template.
    #[serde(default)]
    also_affects: Vec<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct AddParameterParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,

    /// Name of the function to modify.
    function: String,

    /// Name of the new parameter to add.
    param_name: String,

    /// Type annotation for the new parameter (e.g. "Duration").
    /// For typed languages, the parameter is inserted as `{param_name}: {param_type}`.
    /// For Python, only `param_name` is used.
    #[serde(default)]
    param_type: Option<String>,

    /// Default value for the new parameter (e.g. "Duration::from_secs(30)").
    #[serde(default)]
    default_value: Option<String>,

    /// Where to insert the parameter: "first", "last", or "after:<existing_param>".
    #[serde(default = "default_position")]
    position: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct RemoveParameterParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    path: String,

    /// Name of the function to modify.
    function: String,

    /// Name of the parameter to remove.
    param_name: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,
}

fn default_path() -> String {
    ".".to_string()
}

fn default_position() -> String {
    "last".to_string()
}

// --- Tool implementations ---

#[tool_router]
impl CanopyServer {
    #[tool(
        name = "parse",
        description = "Parse source files into the persistent codebase model. First call loads and indexes all files; subsequent calls sync changes. Returns a summary of files and languages."
    )]
    fn parse(&self, Parameters(params): Parameters<ParseParams>) -> String {
        let path = PathBuf::from(&params.path);

        let mut model_lock = self.model.lock().unwrap();

        match &mut *model_lock {
            Some(model) => {
                // Model already loaded — sync any file changes.
                if let Err(e) = model.sync() {
                    return format!("Error syncing: {e}");
                }
                format!(
                    "Codebase model updated. {} file(s) tracked.\n{}",
                    model.file_count(),
                    model.forest,
                )
            }
            None => {
                // First load — create the model.
                match CodebaseModel::load(&path) {
                    Ok(model) => {
                        let summary = format!(
                            "Codebase model loaded. {} file(s) indexed.\n{}",
                            model.file_count(),
                            model.forest,
                        );
                        *model_lock = Some(model);
                        summary
                    }
                    Err(e) => format!("Error loading codebase: {e}"),
                }
            }
        }
    }

    #[tool(
        name = "rename",
        description = "Rename a symbol across the codebase. Returns a unified diff preview. Call `apply` to write changes to disk."
    )]
    fn rename(&self, Parameters(params): Parameters<RenameParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        let changes = match forest.rename(&params.from, &params.to, params.format) {
            Ok(c) => c,
            Err(e) => return format!("Error renaming: {e}"),
        };

        if changes.is_empty() {
            return format!("No occurrences of '{}' found.", params.from);
        }

        let diff: String = changes.iter().map(|c| c.diff()).collect();
        let file_count = changes.len();
        let description = format!("rename '{}' → '{}'", params.from, params.to);

        *self.pending.lock().unwrap() = Some(PendingChanges {
            changes,
            description,
        });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "query",
        description = "Search for structural patterns in the codebase. Returns matching nodes with file, line, kind, name, and text. Use kind='function' to find functions, kind='class' for classes, kind='call' for function calls, kind='import' for imports. Optionally filter by name (supports * glob)."
    )]
    fn query(&self, Parameters(params): Parameters<QueryParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        let match_spec = transform::Match::Abstract {
            kind: params.kind,
            name: params.name,
            file: params.file,
        };

        let results = match forest.query(&match_spec) {
            Ok(r) => r,
            Err(e) => return format!("Error querying: {e}"),
        };

        if results.is_empty() {
            return "No matches found.".to_string();
        }

        let mut output = format!("{} match(es):\n\n", results.len());
        for r in &results {
            let name_str = r.name.as_deref().unwrap_or("(anonymous)");
            output.push_str(&format!(
                "{}:{}:{} [{}] {}\n  {}\n\n",
                r.path.display(),
                r.start_line,
                r.start_col,
                r.kind,
                name_str,
                r.text.lines().next().unwrap_or(""),
            ));
        }

        output
    }

    #[tool(
        name = "find_symbol",
        description = "Find all definitions of a symbol by name across the codebase. Searches function, class/struct/type, and import definitions."
    )]
    fn find_symbol(&self, Parameters(params): Parameters<FindSymbolParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        let mut all_results = Vec::new();

        // Search functions, classes, and imports.
        for kind in &["function", "class", "import"] {
            let match_spec = transform::Match::Abstract {
                kind: kind.to_string(),
                name: Some(params.symbol.clone()),
                file: None,
            };
            if let Ok(results) = forest.query(&match_spec) {
                all_results.extend(results);
            }
        }

        if all_results.is_empty() {
            return format!("No definitions of '{}' found.", params.symbol);
        }

        let mut output = format!("{} definition(s) of '{}':\n\n", all_results.len(), params.symbol);
        for r in &all_results {
            output.push_str(&format!(
                "{}:{}:{} [{}]\n  {}\n\n",
                r.path.display(),
                r.start_line,
                r.start_col,
                r.kind,
                r.text.lines().next().unwrap_or(""),
            ));
        }

        output
    }

    #[tool(
        name = "find_references",
        description = "Find all references (usages) of a symbol by name across the codebase. Includes call sites, variable usages, and type references."
    )]
    fn find_references(&self, Parameters(params): Parameters<FindReferencesParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        // Search call sites.
        let match_spec = transform::Match::Abstract {
            kind: "call".to_string(),
            name: Some(params.symbol.clone()),
            file: None,
        };

        let results = match forest.query(&match_spec) {
            Ok(r) => r,
            Err(e) => return format!("Error querying: {e}"),
        };

        if results.is_empty() {
            return format!("No references to '{}' found.", params.symbol);
        }

        let mut output = format!("{} reference(s) to '{}':\n\n", results.len(), params.symbol);
        for r in &results {
            output.push_str(&format!(
                "{}:{}:{} [{}]\n  {}\n\n",
                r.path.display(),
                r.start_line,
                r.start_col,
                r.kind,
                r.text.lines().next().unwrap_or(""),
            ));
        }

        output
    }

    #[tool(
        name = "transform",
        description = "Apply a structural transformation to matching nodes. Match by abstract kind (kind/name) or raw Tree-sitter query (raw_query). Either specify an action ('replace', 'wrap', 'remove', etc.) with code, OR provide a transform_fn (JavaScript function). Returns a diff preview. Call `apply` to write changes."
    )]
    fn transform(&self, Parameters(params): Parameters<TransformParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        // Build match spec.
        let match_spec = if let Some(raw_query) = params.raw_query.clone() {
            transform::Match::Raw {
                raw_query,
                capture: params.capture.clone(),
            }
        } else if let Some(kind) = params.kind.clone() {
            transform::Match::Abstract {
                kind,
                name: params.name.clone(),
                file: params.file.clone(),
            }
        } else {
            return "Error: must specify either 'kind' or 'raw_query' for matching.".to_string();
        };

        // JS transform path.
        if let Some(transform_fn) = &params.transform_fn {
            let changes = match forest.transform_js(&match_spec, transform_fn, params.format) {
                Ok(c) => c,
                Err(e) => return format!("Error in JS transform: {e}"),
            };

            if changes.is_empty() {
                return "No matches found (no changes).".to_string();
            }

            let diff: String = changes.iter().map(|c| c.diff()).collect();
            let file_count = changes.len();

            *self.pending.lock().unwrap() = Some(PendingChanges {
                changes,
                description: "transform (JS)".to_string(),
            });

            return format!(
                "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
            );
        }

        // Declarative action path.
        let action_str = match &params.action {
            Some(a) => a.as_str(),
            None => return "Error: must specify either 'action' or 'transform_fn'.".to_string(),
        };

        let action = match action_str {
            "replace" => {
                let code = match params.code {
                    Some(c) => c,
                    None => return "Error: 'replace' action requires 'code'.".to_string(),
                };
                transform::Action::Replace { code }
            }
            "wrap" => {
                let before = params.before.unwrap_or_default();
                let after = params.after.unwrap_or_default();
                transform::Action::Wrap { before, after }
            }
            "unwrap" => transform::Action::Unwrap,
            "prepend_statement" => {
                let code = match params.code {
                    Some(c) => c,
                    None => return "Error: 'prepend_statement' action requires 'code'.".to_string(),
                };
                transform::Action::PrependStatement { code }
            }
            "append_statement" => {
                let code = match params.code {
                    Some(c) => c,
                    None => return "Error: 'append_statement' action requires 'code'.".to_string(),
                };
                transform::Action::AppendStatement { code }
            }
            "remove" => transform::Action::Remove,
            "replace_name" => {
                let code = match params.code {
                    Some(c) => c,
                    None => return "Error: 'replace_name' action requires 'code'.".to_string(),
                };
                transform::Action::ReplaceName { code }
            }
            "replace_body" => {
                let code = match params.code {
                    Some(c) => c,
                    None => return "Error: 'replace_body' action requires 'code'.".to_string(),
                };
                transform::Action::ReplaceBody { code }
            }
            other => return format!("Error: unknown action '{other}'."),
        };

        let changes = match forest.transform(&match_spec, &action, params.format) {
            Ok(c) => c,
            Err(e) => return format!("Error transforming: {e}"),
        };

        if changes.is_empty() {
            return "No matches found (no changes).".to_string();
        }

        let diff: String = changes.iter().map(|c| c.diff()).collect();
        let file_count = changes.len();
        let description = format!("transform ({})", action_str);

        *self.pending.lock().unwrap() = Some(PendingChanges {
            changes,
            description,
        });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "transform_batch",
        description = "Apply multiple transforms sequentially in a single operation. Each transform is either a rename ({\"rename\": {\"from\": \"...\", \"to\": \"...\"}}) or a match/act transform (same fields as the `transform` tool). Files are re-parsed between steps so each transform sees the output of the previous one. Returns a combined diff preview. Call `apply` to write changes."
    )]
    fn transform_batch(&self, Parameters(params): Parameters<TransformBatchParams>) -> String {
        let path = PathBuf::from(&params.path);

        // Map from file path → (original bytes, current bytes).
        // We accumulate all changes relative to the original.
        let mut accumulated: HashMap<PathBuf, (Vec<u8>, Vec<u8>)> = HashMap::new();

        // Helper closure: get current source for a file (from accumulator or disk).
        // We'll thread state manually through the loop.

        for (i, transform_val) in params.transforms.iter().enumerate() {
            let step_result = apply_one_batch_step(
                &path,
                transform_val,
                params.format,
                &mut accumulated,
            );
            if let Err(e) = step_result {
                return format!("Error in transform[{i}]: {e}");
            }
        }

        if accumulated.is_empty() {
            return "No changes produced by any transform.".to_string();
        }

        // Build combined FileChanges.
        let changes: Vec<FileChange> = accumulated
            .into_iter()
            .map(|(path, (original, new_source))| FileChange { path, original, new_source })
            .collect();

        let diff: String = changes.iter().map(|c| c.diff()).collect();
        let file_count = changes.len();
        let description = format!("transform_batch ({} step(s))", params.transforms.len());

        *self.pending.lock().unwrap() = Some(PendingChanges { changes, description });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "add_parameter",
        description = "Add a parameter to a function definition. Does not update call sites. position can be 'first', 'last', or 'after:<param_name>'. Returns a diff preview. Call `apply` to write changes."
    )]
    fn add_parameter(&self, Parameters(params): Parameters<AddParameterParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        // Build the parameter text to insert.
        let param_text = build_param_text(&params.param_name, params.param_type.as_deref(), params.default_value.as_deref());

        let mut changes: Vec<FileChange> = Vec::new();

        for file in &forest.files {
            match add_param_in_file(file, &params.function, &param_text, &params.position) {
                Ok(Some(new_source)) => {
                    let mut new_source = new_source;
                    if params.format {
                        new_source = rewrite::format_source(&new_source, file.adapter);
                    }
                    changes.push(FileChange {
                        path: file.path.clone(),
                        original: file.original_source.clone(),
                        new_source,
                    });
                }
                Ok(None) => {}
                Err(e) => return format!("Error in {}: {e}", file.path.display()),
            }
        }

        if changes.is_empty() {
            return format!("Function '{}' not found or parameter list not modifiable.", params.function);
        }

        let diff: String = changes.iter().map(|c| c.diff()).collect();
        let file_count = changes.len();
        let description = format!("add_parameter '{}' to '{}'", params.param_name, params.function);

        *self.pending.lock().unwrap() = Some(PendingChanges { changes, description });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "remove_parameter",
        description = "Remove a parameter from a function definition by name. Does not update call sites. Returns a diff preview. Call `apply` to write changes."
    )]
    fn remove_parameter(&self, Parameters(params): Parameters<RemoveParameterParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        let mut changes: Vec<FileChange> = Vec::new();

        for file in &forest.files {
            match remove_param_in_file(file, &params.function, &params.param_name) {
                Ok(Some(new_source)) => {
                    let mut new_source = new_source;
                    if params.format {
                        new_source = rewrite::format_source(&new_source, file.adapter);
                    }
                    changes.push(FileChange {
                        path: file.path.clone(),
                        original: file.original_source.clone(),
                        new_source,
                    });
                }
                Ok(None) => {}
                Err(e) => return format!("Error in {}: {e}", file.path.display()),
            }
        }

        if changes.is_empty() {
            return format!(
                "Function '{}' not found or parameter '{}' not present.",
                params.function, params.param_name
            );
        }

        let diff: String = changes.iter().map(|c| c.diff()).collect();
        let file_count = changes.len();
        let description = format!("remove_parameter '{}' from '{}'", params.param_name, params.function);

        *self.pending.lock().unwrap() = Some(PendingChanges { changes, description });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "teach_by_example",
        description = "Teach a reusable pattern by pointing at existing code. Specify an exemplar file and which parts are variable (parameter name → value in the exemplar). The platform extracts a template by replacing all occurrences of parameter values (and their case variants) with $param_name placeholders. Use `instantiate` to create new code from the template."
    )]
    fn teach_by_example(&self, Parameters(params): Parameters<TeachByExampleParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        let model = match &*model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let root = model.root().to_owned();
        let exemplar_path = PathBuf::from(&params.exemplar);
        let abs_exemplar = if exemplar_path.is_absolute() {
            exemplar_path
        } else {
            root.join(&exemplar_path)
        };

        let also_affects: Vec<String> = params.also_affects.iter()
            .map(|p| {
                let ap = PathBuf::from(p);
                if ap.is_absolute() {
                    p.clone()
                } else {
                    root.join(p).to_string_lossy().to_string()
                }
            })
            .collect();

        // Convert back to paths relative to root for the also_affects.
        let also_refs: Vec<String> = also_affects.iter()
            .map(|p| PathBuf::from(p).strip_prefix(&root)
                .unwrap_or(Path::new(p))
                .to_string_lossy().to_string())
            .collect();
        let _ = also_refs;

        let template = match exemplar::extract_template(
            &params.name,
            &params.description,
            &abs_exemplar,
            &params.parameters,
            &params.also_affects,
            &root,
        ) {
            Ok(t) => t,
            Err(e) => return format!("Error extracting template: {e}"),
        };

        // Store as a recipe with create_file steps.
        let steps = exemplar::template_to_recipe_steps(&template);
        let param_names: Vec<String> = template.params.clone();

        match model.save_recipe(&params.name, &params.description, &param_names, &steps) {
            Ok(()) => {
                let mut output = format!(
                    "Pattern '{}' extracted from {} with {} file(s).\n",
                    params.name,
                    params.exemplar,
                    template.files.len(),
                );
                output.push_str(&format!("Parameters: [{}]\n", param_names.join(", ")));
                for ft in &template.files {
                    output.push_str(&format!("  {} → template ({} bytes)\n",
                        ft.path_template, ft.content_template.len()));
                }
                output.push_str("\nUse `instantiate` with this pattern name to create new code.");
                output
            }
            Err(e) => format!("Error saving pattern: {e}"),
        }
    }

    #[tool(
        name = "teach_recipe",
        description = "Teach a reusable recipe — a named sequence of transform operations with parameter variables. Use $param_name in string values for substitution. Recipes persist across sessions."
    )]
    fn teach_recipe(&self, Parameters(params): Parameters<TeachRecipeParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        if let Some(model) = &*model_lock {
            match model.save_recipe(&params.name, &params.description, &params.params, &params.steps) {
                Ok(()) => format!(
                    "Recipe '{}' saved with parameters: [{}]",
                    params.name,
                    params.params.join(", "),
                ),
                Err(e) => format!("Error saving recipe: {e}"),
            }
        } else {
            // No model loaded — use ephemeral store.
            "Error: call `parse` first to load the codebase model.".to_string()
        }
    }

    #[tool(
        name = "instantiate",
        description = "Instantiate a taught recipe with specific parameter values. Substitutes $param_name in all string values of the recipe steps, then executes the steps as a transform_batch. Returns a diff preview."
    )]
    fn instantiate(&self, Parameters(params): Parameters<InstantiateParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        let (recipe_params, steps, _desc) = if let Some(model) = &*model_lock {
            match model.load_recipe(&params.recipe) {
                Ok(Some(r)) => r,
                Ok(None) => return format!("Recipe '{}' not found.", params.recipe),
                Err(e) => return format!("Error loading recipe: {e}"),
            }
        } else {
            return "Error: call `parse` first to load the codebase model.".to_string();
        };
        drop(model_lock);

        // Check all required params are provided.
        for p in &recipe_params {
            if !params.params.contains_key(p) {
                return format!("Missing parameter '${p}' for recipe '{}'.", params.recipe);
            }
        }

        // Substitute parameters using case-aware substitution.
        let steps_str = serde_json::to_string(&steps).unwrap_or_default();
        let substituted_str = exemplar::substitute_in_json(&steps_str, &params.params);

        let substituted_steps: serde_json::Value = match serde_json::from_str(&substituted_str) {
            Ok(v) => v,
            Err(e) => return format!("Error substituting parameters: {e}"),
        };

        // Check if these are create_file steps (from teach_by_example).
        if let Some(steps_arr) = substituted_steps.as_array() {
            let is_create_file = steps_arr.iter()
                .all(|s| s["action"].as_str() == Some("create_file"));

            if is_create_file {
                // Handle create_file steps directly.
                let mut changes = Vec::new();
                let root = PathBuf::from(&params.path);
                for step in steps_arr {
                    let path_str = step["path"].as_str().unwrap_or("");
                    let content = step["content"].as_str().unwrap_or("");
                    let full_path = root.join(path_str);
                    changes.push(FileChange {
                        path: full_path,
                        original: Vec::new(),
                        new_source: content.as_bytes().to_vec(),
                    });
                }

                if changes.is_empty() {
                    return "No files to create.".to_string();
                }

                let diff: String = changes.iter().map(|c| c.diff()).collect();
                let file_count = changes.len();
                let description = format!("instantiate '{}'", params.recipe);

                *self.pending.lock().unwrap() = Some(PendingChanges {
                    changes,
                    description,
                });

                return format!(
                    "{diff}\n---\n{file_count} file(s) to create. Call `apply` with confirm=true to write to disk."
                );
            }
        }

        // Otherwise, execute as transform_batch.
        let batch_params = serde_json::json!({
            "path": params.path,
            "format": params.format,
            "transforms": substituted_steps,
        });

        let batch: crate::mcp::TransformBatchParams = match serde_json::from_value(batch_params) {
            Ok(b) => b,
            Err(e) => return format!("Error building batch: {e}"),
        };

        self.transform_batch(Parameters(batch))
    }

    #[tool(
        name = "list_recipes",
        description = "List all taught recipes with their descriptions and parameters."
    )]
    fn list_recipes(&self, Parameters(_params): Parameters<ListRecipesParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        if let Some(model) = &*model_lock {
            match model.list_recipes() {
                Ok(recipes) if recipes.is_empty() => "No recipes defined.".to_string(),
                Ok(recipes) => {
                    let mut output = format!("{} recipe(s):\n\n", recipes.len());
                    for (name, desc) in &recipes {
                        let desc_str = if desc.is_empty() { "" } else { &format!(" — {desc}") };
                        output.push_str(&format!("  {name}{desc_str}\n"));
                    }
                    output
                }
                Err(e) => format!("Error listing recipes: {e}"),
            }
        } else {
            "Error: call `parse` first to load the codebase model.".to_string()
        }
    }

    #[tool(
        name = "teach_convention",
        description = "Define a project convention as an enforceable rule. The check_program is JavaScript that receives a `ctx` object and must return an array of violation strings (or empty array if satisfied). Conventions are checked on `apply` and can be scanned with `check_conventions`."
    )]
    fn teach_convention(&self, Parameters(params): Parameters<TeachConventionParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        if let Some(model) = &*model_lock {
            match model.save_convention(&params.name, &params.description, &params.check_program) {
                Ok(()) => format!("Convention '{}' saved.", params.name),
                Err(e) => format!("Error saving convention: {e}"),
            }
        } else {
            "Error: call `parse` first.".to_string()
        }
    }

    #[tool(
        name = "check_conventions",
        description = "Scan the codebase for convention violations. Runs all taught convention check programs and reports any violations found."
    )]
    fn check_conventions(&self, Parameters(_params): Parameters<CheckConventionsParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        let model = match &*model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let conventions = match model.list_conventions() {
            Ok(c) => c,
            Err(e) => return format!("Error loading conventions: {e}"),
        };

        if conventions.is_empty() {
            return "No conventions defined.".to_string();
        }

        let mut all_violations = Vec::new();

        for (name, description, check_program) in &conventions {
            match codegen::run_convention_check(&model.forest, check_program) {
                Ok(violations) if violations.is_empty() => {
                    all_violations.push(format!("  {} — OK", name));
                }
                Ok(violations) => {
                    all_violations.push(format!("  {} — {} violation(s):", name, violations.len()));
                    for v in &violations {
                        all_violations.push(format!("    - {v}"));
                    }
                }
                Err(e) => {
                    all_violations.push(format!("  {} — error: {e}", name));
                }
            }
        }

        format!("{} convention(s) checked:\n{}", conventions.len(), all_violations.join("\n"))
    }

    #[tool(
        name = "list_conventions",
        description = "List all taught conventions with their descriptions."
    )]
    fn list_conventions(&self, Parameters(_params): Parameters<ListConventionsParams>) -> String {
        let model_lock = self.model.lock().unwrap();
        if let Some(model) = &*model_lock {
            match model.list_conventions() {
                Ok(convs) if convs.is_empty() => "No conventions defined.".to_string(),
                Ok(convs) => {
                    let mut output = format!("{} convention(s):\n\n", convs.len());
                    for (name, desc, _) in &convs {
                        let desc_str = if desc.is_empty() { "" } else { &format!(" — {desc}") };
                        output.push_str(&format!("  {name}{desc_str}\n"));
                    }
                    output
                }
                Err(e) => format!("Error: {e}"),
            }
        } else {
            "Error: call `parse` first.".to_string()
        }
    }

    #[tool(
        name = "hover",
        description = "Get type information for a symbol at a specific position using the language's LSP server. Returns type signature, documentation, etc. Requires `parse` to be called first."
    )]
    fn hover(&self, Parameters(params): Parameters<HoverParams>) -> String {
        let mut model_lock = self.model.lock().unwrap();
        let model = match &mut *model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let lsp = match &mut model.lsp {
            Some(l) => l,
            None => return "No LSP servers available.".to_string(),
        };

        let path = PathBuf::from(&params.file);
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let lang_id = crate::adapters::adapter_for_extension(ext)
            .map(|a| a.lsp_language_id())
            .unwrap_or("");

        match lsp.hover(&path, lang_id, params.line - 1, params.column - 1) {
            Ok(Some(info)) => info,
            Ok(None) => "No hover information available at this position.".to_string(),
            Err(e) => format!("LSP error: {e}"),
        }
    }

    #[tool(
        name = "definition",
        description = "Go to the definition of a symbol at a specific position using the language's LSP server. Returns file path and position. Requires `parse` to be called first."
    )]
    fn definition(&self, Parameters(params): Parameters<DefinitionParams>) -> String {
        let mut model_lock = self.model.lock().unwrap();
        let model = match &mut *model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let lsp = match &mut model.lsp {
            Some(l) => l,
            None => return "No LSP servers available.".to_string(),
        };

        let path = PathBuf::from(&params.file);
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let lang_id = crate::adapters::adapter_for_extension(ext)
            .map(|a| a.lsp_language_id())
            .unwrap_or("");

        match lsp.definition(&path, lang_id, params.line - 1, params.column - 1) {
            Ok(locs) if locs.is_empty() => "No definition found.".to_string(),
            Ok(locs) => {
                let mut output = format!("{} definition(s):\n", locs.len());
                for loc in &locs {
                    output.push_str(&format!("  {loc}\n"));
                }
                output
            }
            Err(e) => format!("LSP error: {e}"),
        }
    }

    #[tool(
        name = "lsp_references",
        description = "Find all references to a symbol at a specific position using the language's LSP server. More accurate than syntactic search — includes aliased imports, trait methods, etc. Requires `parse` to be called first."
    )]
    fn lsp_references(&self, Parameters(params): Parameters<LspReferencesParams>) -> String {
        let mut model_lock = self.model.lock().unwrap();
        let model = match &mut *model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let lsp = match &mut model.lsp {
            Some(l) => l,
            None => return "No LSP servers available.".to_string(),
        };

        let path = PathBuf::from(&params.file);
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let lang_id = crate::adapters::adapter_for_extension(ext)
            .map(|a| a.lsp_language_id())
            .unwrap_or("");

        match lsp.references(&path, lang_id, params.line - 1, params.column - 1) {
            Ok(locs) if locs.is_empty() => "No references found.".to_string(),
            Ok(locs) => {
                let mut output = format!("{} reference(s):\n", locs.len());
                for loc in &locs {
                    output.push_str(&format!("  {loc}\n"));
                }
                output
            }
            Err(e) => format!("LSP error: {e}"),
        }
    }

    #[tool(
        name = "diagnostics",
        description = "Get compile diagnostics (errors/warnings) for a file from the language's LSP server. Optionally provide modified content to check before writing to disk. Requires `parse` to be called first."
    )]
    fn diagnostics(&self, Parameters(params): Parameters<DiagnosticsParams>) -> String {
        let mut model_lock = self.model.lock().unwrap();
        let model = match &mut *model_lock {
            Some(m) => m,
            None => return "Error: call `parse` first.".to_string(),
        };

        let lsp = match &mut model.lsp {
            Some(l) => l,
            None => return "No LSP servers available.".to_string(),
        };

        let path = PathBuf::from(&params.file);
        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let lang_id = crate::adapters::adapter_for_extension(ext)
            .map(|a| a.lsp_language_id())
            .unwrap_or("");

        let text = match &params.content {
            Some(c) => c.clone(),
            None => match std::fs::read_to_string(&path) {
                Ok(t) => t,
                Err(e) => return format!("Error reading file: {e}"),
            },
        };

        match lsp.get_diagnostics(&path, lang_id, &text) {
            Ok(diags) if diags.is_empty() => "No errors or warnings.".to_string(),
            Ok(diags) => {
                let mut output = format!("{} diagnostic(s):\n", diags.len());
                for d in &diags {
                    output.push_str(&format!("  {d}\n"));
                }
                output
            }
            Err(e) => format!("LSP error: {e}"),
        }
    }

    #[tool(
        name = "codegen",
        description = "Execute a JavaScript code generator program against the codebase. The program receives a global `ctx` object for querying symbols, reading files, and making coordinated edits across multiple files. Returns a diff preview. Call `apply` to write changes."
    )]
    fn codegen(&self, Parameters(params): Parameters<CodegenParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match self.get_forest(&path) {
            Ok(f) => f,
            Err(e) => return e,
        };

        // Try to use LSP if model is loaded.
        let changes = {
            let mut model_lock = self.model.lock().unwrap();
            if let Some(model) = &mut *model_lock {
                if let Some(lsp) = &mut model.lsp {
                    codegen::run_codegen_with_lsp(&forest, &params.program, lsp)
                } else {
                    codegen::run_codegen(&forest, &params.program)
                }
            } else {
                codegen::run_codegen(&forest, &params.program)
            }
        };
        let changes = match changes {
            Ok(c) => c,
            Err(e) => return format!("Error in codegen program: {e}"),
        };

        if changes.is_empty() {
            return "Program produced no changes.".to_string();
        }

        // Pre-flight validation.
        if params.validate {
            let mut all_warnings: Vec<String> = codegen::validate_changes(&changes);
            all_warnings.extend(codegen::structural_checks(&forest, &changes));
            let errors = all_warnings;
            if !errors.is_empty() {
                let mut output = "WARNING: pre-flight checks detected issues after transformation:\n".to_string();
                for err in &errors {
                    output.push_str(&format!("  - {err}\n"));
                }
                output.push_str("\nDiff preview (NOT applied):\n\n");
                for c in &changes {
                    output.push_str(&c.diff());
                }
                return output;
            }
        }

        // Format if requested.
        let final_changes: Vec<FileChange> = if params.format {
            changes.into_iter().map(|mut c| {
                if let Some(ext) = c.path.extension().and_then(|e| e.to_str()) {
                    if let Some(adapter) = crate::adapters::adapter_for_extension(ext) {
                        c.new_source = rewrite::format_source(&c.new_source, adapter);
                    }
                }
                c
            }).collect()
        } else {
            changes
        };

        let diff: String = final_changes.iter().map(|c| c.diff()).collect();
        let file_count = final_changes.len();

        *self.pending.lock().unwrap() = Some(PendingChanges {
            changes: final_changes,
            description: "codegen".to_string(),
        });

        format!(
            "{diff}\n---\n{file_count} file(s) changed. Call `apply` with confirm=true to write to disk."
        )
    }

    #[tool(
        name = "apply",
        description = "Apply the pending changes from the last transform to disk. Requires confirm=true. Creates .canopy.bak backups for each changed file — use `undo` to revert. Checks conventions and warns on violations."
    )]
    fn apply(&self, Parameters(params): Parameters<ApplyParams>) -> String {
        if !params.confirm {
            return "Set confirm=true to apply changes.".to_string();
        }

        let pending = self.pending.lock().unwrap().take();
        match pending {
            None => "No pending changes to apply.".to_string(),
            Some(p) => {
                let convention_warnings = self.check_conventions_on_changes(&p.changes);
                let file_count = p.changes.len();

                match crate::forest::apply_with_backup(&p.changes) {
                    Ok(backup_paths) => {
                        // Store backups for undo.
                        *self.last_backups.lock().unwrap() = Some((
                            backup_paths,
                            p.description.clone(),
                        ));

                        let mut output = format!(
                            "Applied {} to {file_count} file(s). Backups created — use `undo` to revert.",
                            p.description
                        );

                        if !convention_warnings.is_empty() {
                            output.push_str(&format!(
                                "\n\nConvention warnings:\n{}",
                                convention_warnings
                            ));
                        }

                        output
                    }
                    Err(e) => format!("Error applying changes: {e}"),
                }
            }
        }
    }

    #[tool(
        name = "undo",
        description = "Revert the last applied changes by restoring from .canopy.bak backup files."
    )]
    fn undo(&self, Parameters(_params): Parameters<UndoParams>) -> String {
        let backups = self.last_backups.lock().unwrap().take();
        match backups {
            None => "No changes to undo.".to_string(),
            Some((paths, description)) => {
                match crate::forest::undo_from_backups(&paths) {
                    Ok(restored) => {
                        format!("Undone {description}: {restored} file(s) restored.")
                    }
                    Err(e) => format!("Error during undo: {e}"),
                }
            }
        }
    }
}

#[tool_handler]
impl ServerHandler for CanopyServer {
    fn get_info(&self) -> ServerInfo {
        ServerInfo::new(ServerCapabilities::builder().enable_tools().build())
            .with_instructions(
                "Canopy: codebase operations platform.\n\n\
                 Tools:\n\
                 - parse: scan a codebase and list files/languages\n\
                 - query: search for structural patterns (functions, classes, calls, imports)\n\
                 - find_symbol: find definitions of a symbol\n\
                 - find_references: find call sites of a symbol\n\
                 - rename: rename a symbol across the codebase (preview)\n\
                 - transform: apply a single structural transformation\n\
                 - transform_batch: apply multiple transforms sequentially in one operation\n\
                 - add_parameter: add a parameter to a function definition\n\
                 - remove_parameter: remove a parameter from a function definition\n\
                 - apply: write pending changes to disk\n\n\
                 All mutating tools return a diff preview. Call `apply` with confirm=true to write changes.\n\
                 Supported languages: Python, Rust, TypeScript, C++, Go."
                    .to_string(),
            )
    }
}

impl CanopyServer {
    pub fn new() -> Self {
        Self {
            tool_router: Self::tool_router(),
            pending: Mutex::new(None),
            last_backups: Mutex::new(None),
            model: Mutex::new(None),
        }
    }

    /// Get a forest for the given path. Uses the persistent model if loaded
    /// and the path is within the model's root; otherwise parses fresh.
    fn get_forest(&self, path: &Path) -> Result<Forest, String> {
        // Check if the model is loaded and covers this path.
        let mut model_lock = self.model.lock().unwrap();
        if let Some(model) = &mut *model_lock {
            let _ = model.sync();
            let model_root = model.root().to_owned();
            let abs_path = path.canonicalize().unwrap_or_else(|_| path.to_owned());
            if abs_path.starts_with(&model_root) {
                // Filter forest to files under the requested path.
                return Ok(Forest {
                    files: model.forest.files.iter()
                        .filter(|f| f.path.starts_with(&abs_path))
                        .cloned()
                        .collect(),
                });
            }
        }
        drop(model_lock);

        // Fall back to parsing fresh.
        Forest::from_path(path).map_err(|e| format!("Error parsing: {e}"))
    }

    /// Run convention checks against a set of changes.
    /// Returns a warning string (empty if no violations).
    fn check_conventions_on_changes(&self, _changes: &[FileChange]) -> String {
        let model_lock = self.model.lock().unwrap();
        let model = match &*model_lock {
            Some(m) => m,
            None => return String::new(), // No model, skip checks.
        };

        let conventions = match model.list_conventions() {
            Ok(c) => c,
            Err(_) => return String::new(),
        };

        if conventions.is_empty() {
            return String::new();
        }

        // Run checks against the current forest (which reflects pre-change state).
        // Ideally we'd check the post-change state, but that would require
        // re-parsing the changed files. For now, check the current state.
        let mut warnings = Vec::new();
        for (name, _, check_program) in &conventions {
            match codegen::run_convention_check(&model.forest, check_program) {
                Ok(violations) if !violations.is_empty() => {
                    warnings.push(format!("  {} — {} violation(s):", name, violations.len()));
                    for v in &violations {
                        warnings.push(format!("    - {v}"));
                    }
                }
                _ => {}
            }
        }

        warnings.join("\n")
    }
}

// ---------------------------------------------------------------------------
// Helpers for transform_batch
// ---------------------------------------------------------------------------

/// Apply a single transform step (rename or match/act) against the current
/// state of all accumulated files.
///
/// `accumulated` maps file path → (original bytes, current bytes). After each
/// step we update the "current bytes" for changed files, keeping the original
/// bytes (for the final diff) fixed at what they were before this entire batch.
fn apply_one_batch_step(
    root_path: &PathBuf,
    transform_val: &serde_json::Value,
    format: bool,
    accumulated: &mut HashMap<PathBuf, (Vec<u8>, Vec<u8>)>,
) -> anyhow::Result<()> {
    // Determine whether this is a rename or a match/act transform.
    if let Some(rename_obj) = transform_val.get("rename") {
        // Rename step.
        let from = rename_obj.get("from")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("rename step missing 'from'"))?
            .to_string();
        let to = rename_obj.get("to")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("rename step missing 'to'"))?
            .to_string();

        // Build a forest from current state.
        let forest = build_current_forest(root_path, accumulated)?;
        let step_changes = forest.rename(&from, &to, format)?;
        merge_changes(accumulated, step_changes);
    } else {
        // Match/act transform step. Parse it as TransformParams fields.
        let obj = transform_val.as_object()
            .ok_or_else(|| anyhow::anyhow!("transform element must be an object"))?;

        let raw_query = obj.get("raw_query").and_then(|v| v.as_str()).map(|s| s.to_string());
        let capture   = obj.get("capture").and_then(|v| v.as_str()).map(|s| s.to_string());
        let kind      = obj.get("kind").and_then(|v| v.as_str()).map(|s| s.to_string());
        let name      = obj.get("name").and_then(|v| v.as_str()).map(|s| s.to_string());
        let file_f    = obj.get("file").and_then(|v| v.as_str()).map(|s| s.to_string());
        let action_s  = obj.get("action").and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("transform step missing 'action'"))?
            .to_string();
        let code      = obj.get("code").and_then(|v| v.as_str()).map(|s| s.to_string());
        let before    = obj.get("before").and_then(|v| v.as_str()).map(|s| s.to_string());
        let after     = obj.get("after").and_then(|v| v.as_str()).map(|s| s.to_string());

        let match_spec = if let Some(rq) = raw_query {
            transform::Match::Raw { raw_query: rq, capture }
        } else if let Some(k) = kind {
            transform::Match::Abstract { kind: k, name, file: file_f }
        } else {
            anyhow::bail!("transform step must specify 'kind' or 'raw_query'");
        };

        let action = parse_action(&action_s, code, before, after)?;

        let forest = build_current_forest(root_path, accumulated)?;
        let step_changes = forest.transform(&match_spec, &action, format)?;
        merge_changes(accumulated, step_changes);
    }

    Ok(())
}

/// Build a Forest reflecting the current (possibly modified) state of files.
/// Files that have been modified in `accumulated` are served from memory;
/// everything else is read from disk.
fn build_current_forest(
    root_path: &PathBuf,
    accumulated: &HashMap<PathBuf, (Vec<u8>, Vec<u8>)>,
) -> anyhow::Result<Forest> {
    // Start with a fresh parse of what's on disk.
    let mut forest = Forest::from_path(root_path)?;

    // Overlay any files that have been modified so far in this batch.
    for file in &mut forest.files {
        if let Some((_, current_bytes)) = accumulated.get(&file.path) {
            // Re-parse from the current (in-memory) state.
            let mut parser = Parser::new();
            parser.set_language(&file.adapter.language())?;
            if let Some(tree) = parser.parse(current_bytes, None) {
                file.original_source = current_bytes.clone();
                file.tree = tree;
            }
        }
    }

    Ok(forest)
}

/// Merge a set of step changes into the accumulated map.
/// If a file was already in `accumulated`, keep its original bytes but update
/// the current bytes. If it's new, insert it.
fn merge_changes(
    accumulated: &mut HashMap<PathBuf, (Vec<u8>, Vec<u8>)>,
    step_changes: Vec<FileChange>,
) {
    for change in step_changes {
        accumulated
            .entry(change.path.clone())
            .and_modify(|(_, cur)| *cur = change.new_source.clone())
            .or_insert((change.original, change.new_source));
    }
}

/// Parse an action string + supporting fields into a `transform::Action`.
fn parse_action(
    action_s: &str,
    code: Option<String>,
    before: Option<String>,
    after: Option<String>,
) -> anyhow::Result<transform::Action> {
    match action_s {
        "replace" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace' action requires 'code'"))?;
            Ok(transform::Action::Replace { code: c })
        }
        "wrap" => Ok(transform::Action::Wrap {
            before: before.unwrap_or_default(),
            after: after.unwrap_or_default(),
        }),
        "unwrap" => Ok(transform::Action::Unwrap),
        "prepend_statement" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'prepend_statement' action requires 'code'"))?;
            Ok(transform::Action::PrependStatement { code: c })
        }
        "append_statement" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'append_statement' action requires 'code'"))?;
            Ok(transform::Action::AppendStatement { code: c })
        }
        "remove" => Ok(transform::Action::Remove),
        "replace_name" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace_name' action requires 'code'"))?;
            Ok(transform::Action::ReplaceName { code: c })
        }
        "replace_body" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace_body' action requires 'code'"))?;
            Ok(transform::Action::ReplaceBody { code: c })
        }
        other => anyhow::bail!("unknown action '{other}'"),
    }
}

// ---------------------------------------------------------------------------
// Helpers for add_parameter / remove_parameter
// ---------------------------------------------------------------------------

/// Build the text for a new parameter.
/// For typed languages (where `param_type` is provided): `name: type = default`
/// or language-appropriate forms. We simply let the user provide the full type
/// annotation string and assemble accordingly.
fn build_param_text(name: &str, param_type: Option<&str>, default_value: Option<&str>) -> String {
    match (param_type, default_value) {
        (Some(ty), Some(def)) => format!("{name}: {ty} = {def}"),
        (Some(ty), None) => format!("{name}: {ty}"),
        (None, Some(def)) => format!("{name}={def}"),
        (None, None) => name.to_string(),
    }
}

/// Find the function named `func_name` in `file`, locate its parameter list,
/// and insert `param_text` at the specified position.
///
/// Returns `Ok(Some(new_source))` if the function was found and changed,
/// `Ok(None)` if the function was not found in this file.
fn add_param_in_file(
    file: &ParsedFile,
    func_name: &str,
    param_text: &str,
    position: &str,
) -> anyhow::Result<Option<Vec<u8>>> {
    let param_list_range = match find_param_list(file, func_name)? {
        Some(r) => r,
        None => return Ok(None),
    };

    let (list_start, list_end) = param_list_range;
    let source = &file.original_source;
    // The text inside the parens (excluding the parens themselves).
    let inner_start = list_start + 1; // skip '('
    let inner_end = list_end - 1;     // before ')'
    let inner = std::str::from_utf8(&source[inner_start..inner_end])
        .unwrap_or("")
        .trim();

    let new_inner = if inner.is_empty() {
        // No existing params.
        param_text.to_string()
    } else {
        // Split on commas at the top level (we do a simple split since tree-sitter
        // already validated the source).
        let existing_params: Vec<&str> = inner.split(',').map(|s| s.trim()).collect();

        let insert_idx = match position {
            "first" => 0,
            "last" => existing_params.len(),
            pos if pos.starts_with("after:") => {
                let after_name = &pos["after:".len()..];
                let found = existing_params.iter().position(|p| {
                    // Check if this param starts with or equals the after_name.
                    p.split(':').next().unwrap_or("").trim() == after_name
                        || p.split('=').next().unwrap_or("").trim() == after_name
                });
                match found {
                    Some(idx) => idx + 1,
                    None => return Err(anyhow::anyhow!(
                        "parameter '{after_name}' not found in function '{func_name}'"
                    )),
                }
            }
            _ => return Err(anyhow::anyhow!("invalid position '{position}'")),
        };

        let mut params = existing_params.iter().map(|s| s.to_string()).collect::<Vec<_>>();
        params.insert(insert_idx, param_text.to_string());
        params.join(", ")
    };

    // Rebuild the full param list.
    let new_param_list = format!("({new_inner})");
    let mut result = Vec::with_capacity(source.len());
    result.extend_from_slice(&source[..list_start]);
    result.extend_from_slice(new_param_list.as_bytes());
    result.extend_from_slice(&source[list_end..]);

    if result == source.as_slice() {
        return Ok(None);
    }

    Ok(Some(result))
}

/// Find the function named `func_name` in `file`, locate its parameter list,
/// and remove the parameter named `param_name`.
///
/// Returns `Ok(Some(new_source))` if the parameter was found and removed,
/// `Ok(None)` if the function or parameter was not found in this file.
fn remove_param_in_file(
    file: &ParsedFile,
    func_name: &str,
    param_name: &str,
) -> anyhow::Result<Option<Vec<u8>>> {
    let param_list_range = match find_param_list(file, func_name)? {
        Some(r) => r,
        None => return Ok(None),
    };

    let (list_start, list_end) = param_list_range;
    let source = &file.original_source;
    let inner_start = list_start + 1;
    let inner_end = list_end - 1;
    let inner = std::str::from_utf8(&source[inner_start..inner_end])
        .unwrap_or("")
        .trim();

    if inner.is_empty() {
        return Ok(None);
    }

    let existing_params: Vec<&str> = inner.split(',').map(|s| s.trim()).collect();

    // Find the parameter by name (handles `name`, `name: type`, `name=default`).
    let found_idx = existing_params.iter().position(|p| {
        let bare = p.split(':').next().unwrap_or("").trim();
        let bare2 = bare.split('=').next().unwrap_or("").trim();
        bare2 == param_name
    });

    let idx = match found_idx {
        Some(i) => i,
        None => return Ok(None), // Parameter not in this file's function.
    };

    let mut params = existing_params.iter().map(|s| s.to_string()).collect::<Vec<_>>();
    params.remove(idx);

    let new_inner = params.join(", ");
    let new_param_list = format!("({new_inner})");

    let mut result = Vec::with_capacity(source.len());
    result.extend_from_slice(&source[..list_start]);
    result.extend_from_slice(new_param_list.as_bytes());
    result.extend_from_slice(&source[list_end..]);

    if result == source.as_slice() {
        return Ok(None);
    }

    Ok(Some(result))
}

/// Find the byte range of the parameter list `(...)` of the function named
/// `func_name` in `file`. Returns `(open_paren_byte, close_paren_exclusive)`.
fn find_param_list(
    file: &ParsedFile,
    func_name: &str,
) -> anyhow::Result<Option<(usize, usize)>> {
    let query_str = format!(
        "({} (#eq? @name \"{func_name}\"))",
        file.adapter.function_def_query()
    );
    let query = Query::new(&file.adapter.language(), &query_str)
        .map_err(|e| anyhow::anyhow!("compiling param-list query: {e}"))?;

    let func_idx = query.capture_index_for_name("func")
        .ok_or_else(|| anyhow::anyhow!("function_def_query must capture @func"))?;

    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    while let Some(m) = matches.next() {
        let func_node = m.captures.iter()
            .find(|c| c.index == func_idx)
            .map(|c| c.node);

        if let Some(node) = func_node {
            // Navigate to the parameter list child.
            // Try common field names first, then walk children by node kind.
            if let Some(params_node) = node.child_by_field_name("parameters") {
                return Ok(Some((params_node.start_byte(), params_node.end_byte())));
            }
            // Walk children looking for parameter-list-like node kinds.
            let mut walk = node.walk();
            for child in node.children(&mut walk) {
                let kind = child.kind();
                if matches!(kind,
                    "parameters" | "parameter_list" | "formal_parameters" |
                    "parameter_clause" | "param_list"
                ) {
                    return Ok(Some((child.start_byte(), child.end_byte())));
                }
            }
        }
    }

    Ok(None)
}

pub async fn serve() -> anyhow::Result<()> {
    let server = CanopyServer::new();
    let service = server.serve(rmcp::transport::stdio()).await?;
    service.waiting().await?;
    Ok(())
}
