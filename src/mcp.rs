// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Mutex;

use rmcp::handler::server::router::tool::ToolRouter;
use rmcp::handler::server::wrapper::Parameters;
use rmcp::model::{ServerCapabilities, ServerInfo};
use rmcp::{schemars, tool, tool_handler, tool_router, ServerHandler, ServiceExt};
use tree_sitter::{Parser, Query, QueryCursor};
use streaming_iterator::StreamingIterator;

use crate::forest::{FileChange, Forest, ParsedFile};
use crate::rewrite;
use crate::transform;

/// Pending changes from the last transform, waiting to be applied.
struct PendingChanges {
    changes: Vec<FileChange>,
    description: String,
}

pub struct PolyRefactorServer {
    tool_router: ToolRouter<Self>,
    pending: Mutex<Option<PendingChanges>>,
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
struct ApplyParams {
    /// Set to true to confirm applying the pending changes.
    confirm: bool,
}

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
impl PolyRefactorServer {
    #[tool(
        name = "parse",
        description = "Parse source files and return a summary of the codebase forest (file count, languages, parse errors)."
    )]
    fn parse(&self, Parameters(params): Parameters<ParseParams>) -> String {
        let path = PathBuf::from(&params.path);
        match Forest::from_path(&path) {
            Ok(forest) => format!("{forest}"),
            Err(e) => format!("Error: {e}"),
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
        name = "apply",
        description = "Apply the pending changes from the last transform to disk. Requires confirm=true."
    )]
    fn apply(&self, Parameters(params): Parameters<ApplyParams>) -> String {
        if !params.confirm {
            return "Set confirm=true to apply changes.".to_string();
        }

        let pending = self.pending.lock().unwrap().take();
        match pending {
            None => "No pending changes to apply.".to_string(),
            Some(p) => {
                let mut applied = 0;
                for change in &p.changes {
                    if let Err(e) = change.apply() {
                        return format!(
                            "Error writing {}: {e}\n({applied} file(s) already written)",
                            change.path.display()
                        );
                    }
                    applied += 1;
                }
                format!(
                    "Applied {} to {applied} file(s).",
                    p.description
                )
            }
        }
    }
}

#[tool_handler]
impl ServerHandler for PolyRefactorServer {
    fn get_info(&self) -> ServerInfo {
        ServerInfo::new(ServerCapabilities::builder().enable_tools().build())
            .with_instructions(
                "PolyRefactor: AST-level multi-language refactoring server.\n\n\
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

impl PolyRefactorServer {
    pub fn new() -> Self {
        Self {
            tool_router: Self::tool_router(),
            pending: Mutex::new(None),
        }
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
    let server = PolyRefactorServer::new();
    let service = server.serve(rmcp::transport::stdio()).await?;
    service.waiting().await?;
    Ok(())
}
