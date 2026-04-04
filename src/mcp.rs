// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::path::PathBuf;
use std::sync::Mutex;

use rmcp::handler::server::router::tool::ToolRouter;
use rmcp::handler::server::wrapper::Parameters;
use rmcp::model::{ServerCapabilities, ServerInfo};
use rmcp::{schemars, tool, tool_handler, tool_router, ServerHandler, ServiceExt};

use crate::forest::{FileChange, Forest};
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

    // --- Action ---

    /// Action to perform: "replace", "wrap", "unwrap", "prepend_statement",
    /// "append_statement", "remove", "replace_name", "replace_body".
    action: String,
    /// Code to inject (for replace, wrap/before, prepend, append, replace_name, replace_body).
    #[serde(default)]
    code: Option<String>,
    /// "before" text for wrap action.
    #[serde(default)]
    before: Option<String>,
    /// "after" text for wrap action.
    #[serde(default)]
    after: Option<String>,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ApplyParams {
    /// Set to true to confirm applying the pending changes.
    confirm: bool,
}

fn default_path() -> String {
    ".".to_string()
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
        description = "Apply a structural transformation to matching nodes. Match by abstract kind (kind/name) or raw Tree-sitter query (raw_query). Actions: 'replace' (code), 'wrap' (before/after), 'unwrap', 'prepend_statement' (code), 'append_statement' (code), 'remove', 'replace_name' (code), 'replace_body' (code). Returns a diff preview. Call `apply` to write changes."
    )]
    fn transform(&self, Parameters(params): Parameters<TransformParams>) -> String {
        let path = PathBuf::from(&params.path);
        let forest = match Forest::from_path(&path) {
            Ok(f) => f,
            Err(e) => return format!("Error parsing: {e}"),
        };

        // Build match spec.
        let match_spec = if let Some(raw_query) = params.raw_query {
            transform::Match::Raw {
                raw_query,
                capture: params.capture,
            }
        } else if let Some(kind) = params.kind {
            transform::Match::Abstract {
                kind,
                name: params.name,
                file: params.file,
            }
        } else {
            return "Error: must specify either 'kind' or 'raw_query' for matching.".to_string();
        };

        // Build action.
        let action = match params.action.as_str() {
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
        let description = format!("transform ({})", params.action);

        *self.pending.lock().unwrap() = Some(PendingChanges {
            changes,
            description,
        });

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
                 - transform: apply structural transformations (replace, wrap, remove, etc.)\n\
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

pub async fn serve() -> anyhow::Result<()> {
    let server = PolyRefactorServer::new();
    let service = server.serve(rmcp::transport::stdio()).await?;
    service.waiting().await?;
    Ok(())
}
