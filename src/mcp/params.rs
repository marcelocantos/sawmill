// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashMap;

use rmcp::schemars;

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct ParseParams {
    /// Path to parse (file or directory).
    pub path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct RenameParams {
    /// Current symbol name.
    pub from: String,
    /// New symbol name.
    pub to: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,
    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct QueryParams {
    /// Path scope (file or directory).
    #[serde(default = "default_path")]
    pub path: String,
    /// Abstract node kind to search for: "function", "class", "call", "import".
    pub kind: String,
    /// Optional name filter (exact match or glob with *).
    #[serde(default)]
    pub name: Option<String>,
    /// Optional file path filter.
    #[serde(default)]
    pub file: Option<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct FindSymbolParams {
    /// Symbol name to find.
    pub symbol: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct FindReferencesParams {
    /// Symbol name to find references of.
    pub symbol: String,
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct HoverParams {
    /// File path containing the symbol.
    pub file: String,
    /// Line number (1-based).
    pub line: u32,
    /// Column number (1-based).
    pub column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct DefinitionParams {
    /// File path containing the symbol.
    pub file: String,
    /// Line number (1-based).
    pub line: u32,
    /// Column number (1-based).
    pub column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct LspReferencesParams {
    /// File path containing the symbol.
    pub file: String,
    /// Line number (1-based).
    pub line: u32,
    /// Column number (1-based).
    pub column: u32,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct DiagnosticsParams {
    /// File path to check.
    pub file: String,
    /// Optional modified content to check (if omitted, checks the file on disk).
    #[serde(default)]
    pub content: Option<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct TransformParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,

    // --- Matching (pick one) ---
    /// Abstract node kind: "function", "class", "call", "import".
    #[serde(default)]
    pub kind: Option<String>,
    /// Name filter for abstract matching (exact or glob with *).
    #[serde(default)]
    pub name: Option<String>,
    /// File filter for abstract matching.
    #[serde(default)]
    pub file: Option<String>,
    /// Raw Tree-sitter query (alternative to kind/name matching).
    #[serde(default)]
    pub raw_query: Option<String>,
    /// Capture name to act on for raw queries (defaults to first).
    #[serde(default)]
    pub capture: Option<String>,

    // --- Action (pick one: action or transform_fn) ---
    /// Action to perform: "replace", "wrap", "unwrap", "prepend_statement",
    /// "append_statement", "remove", "replace_name", "replace_body".
    /// Omit if using transform_fn.
    #[serde(default)]
    pub action: Option<String>,
    /// Code to inject (for replace, wrap/before, prepend, append, replace_name, replace_body).
    #[serde(default)]
    pub code: Option<String>,
    /// "before" text for wrap action.
    #[serde(default)]
    pub before: Option<String>,
    /// "after" text for wrap action.
    #[serde(default)]
    pub after: Option<String>,

    /// JavaScript transform function (alternative to action).
    /// Receives a node object with properties (kind, name, text, body, parameters,
    /// file, startLine, endLine) and mutation methods (replaceText, replaceBody,
    /// replaceName, remove, wrap, insertBefore, insertAfter).
    /// Return the original node (unchanged), a mutated node, null (delete), or a string (replace).
    #[serde(default)]
    pub transform_fn: Option<String>,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct CodegenParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,

    /// JavaScript program to execute against the codebase model.
    /// Receives a global `ctx` object with methods:
    /// - ctx.findFunction(name) → array of nodes
    /// - ctx.findType(name) → array of nodes
    /// - ctx.query({kind, name, file}) → array of nodes (name supports * glob)
    /// - ctx.references(name) → array of call-site nodes
    /// - ctx.readFile(path) → file content string or null
    /// - ctx.addFile(path, content) → create a new file
    /// - ctx.editFile(path, startByte, endByte, replacement) → raw byte-range edit
    ///
    /// Nodes have methods: replaceText, replaceBody, replaceName, remove, insertBefore, insertAfter
    pub program: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,

    /// Validate that modified files parse correctly. Defaults to true.
    #[serde(default = "default_true")]
    pub validate: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct ApplyParams {
    /// Set to true to confirm applying the pending changes.
    pub confirm: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct UndoParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct TransformBatchParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,

    /// Ordered list of transforms to apply. Each element is either:
    /// - `{"rename": {"from": "old", "to": "new"}}` for a rename
    /// - A match/act transform (same fields as the `transform` tool)
    pub transforms: Vec<serde_json::Value>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct TeachRecipeParams {
    /// Unique name for this recipe.
    pub name: String,
    /// Human-readable description.
    #[serde(default)]
    pub description: String,
    /// Parameter names that will be substituted (e.g. ["name", "type"]).
    pub params: Vec<String>,
    /// Ordered list of transform steps (same format as transform_batch).
    /// Use $param_name in string values for substitution.
    pub steps: serde_json::Value,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct InstantiateParams {
    /// Name of the recipe to instantiate.
    pub recipe: String,
    /// Parameter values to substitute (e.g. {"name": "User", "type": "struct"}).
    pub params: HashMap<String, String>,
    /// Path scope. Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,
    /// Run the language formatter. Defaults to false.
    #[serde(default)]
    pub format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct ListRecipesParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct TeachConventionParams {
    /// Unique name for this convention.
    pub name: String,
    /// Human-readable description (e.g., "Every public function must have a doc comment").
    #[serde(default)]
    pub description: String,
    /// JavaScript check program. Receives a global `ctx` object (same as codegen).
    /// Must return an array of violation strings, or an empty array if the convention is satisfied.
    pub check_program: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct CheckConventionsParams {
    /// Path scope. Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct ListConventionsParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct GetAgentPromptParams {}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct TeachByExampleParams {
    /// Unique name for this pattern.
    pub name: String,
    /// Human-readable description.
    #[serde(default)]
    pub description: String,
    /// Path to the exemplar file.
    pub exemplar: String,
    /// Parameter name → value mapping. For example:
    /// {"name": "users", "entity": "User"}.
    /// All occurrences of these values (and their case variants)
    /// will be replaced with $param_name placeholders.
    pub parameters: HashMap<String, String>,
    /// Additional files or directories affected by this pattern.
    /// Files containing parameter values will be included in the template.
    #[serde(default)]
    pub also_affects: Vec<String>,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct AddParameterParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,

    /// Name of the function to modify.
    pub function: String,

    /// Name of the new parameter to add.
    pub param_name: String,

    /// Type annotation for the new parameter (e.g. "Duration").
    /// For typed languages, the parameter is inserted as `{param_name}: {param_type}`.
    /// For Python, only `param_name` is used.
    #[serde(default)]
    pub param_type: Option<String>,

    /// Default value for the new parameter (e.g. "Duration::from_secs(30)").
    #[serde(default)]
    pub default_value: Option<String>,

    /// Where to insert the parameter: "first", "last", or "after:<existing_param>".
    #[serde(default = "default_position")]
    pub position: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,
}

#[derive(serde::Deserialize, schemars::JsonSchema)]
pub(super) struct RemoveParameterParams {
    /// Path scope (file or directory). Defaults to current directory.
    #[serde(default = "default_path")]
    pub path: String,

    /// Name of the function to modify.
    pub function: String,

    /// Name of the parameter to remove.
    pub param_name: String,

    /// Run the language formatter on changed files. Defaults to false.
    #[serde(default)]
    pub format: bool,
}

pub(super) fn default_path() -> String {
    ".".to_string()
}

pub(super) fn default_position() -> String {
    "last".to_string()
}

pub(super) fn default_true() -> bool {
    true
}
