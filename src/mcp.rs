// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::path::PathBuf;
use std::sync::Mutex;

use rmcp::handler::server::router::tool::ToolRouter;
use rmcp::handler::server::wrapper::Parameters;
use rmcp::model::{ServerCapabilities, ServerInfo};
use rmcp::{schemars, tool, tool_handler, tool_router, ServerHandler, ServiceExt};

use crate::forest::{FileChange, Forest};

/// Pending changes from the last transform, waiting to be applied.
struct PendingChanges {
    changes: Vec<FileChange>,
    description: String,
}

pub struct PolyRefactorServer {
    tool_router: ToolRouter<Self>,
    pending: Mutex<Option<PendingChanges>>,
}

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
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
struct ApplyParams {
    /// Set to true to confirm applying the pending changes.
    confirm: bool,
}

fn default_path() -> String {
    ".".to_string()
}

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

        let changes = match forest.rename(&params.from, &params.to) {
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
                "PolyRefactor: AST-level multi-language refactoring server. \
                 Use `parse` to scan a codebase, `rename` to preview symbol renames, \
                 and `apply` to write changes to disk."
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
