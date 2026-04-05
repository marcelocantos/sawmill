// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use super::LanguageAdapter;
use tree_sitter::Language;

pub struct PythonAdapter;

impl LanguageAdapter for PythonAdapter {
    fn language(&self) -> Language {
        tree_sitter_python::LANGUAGE.into()
    }

    fn extensions(&self) -> &[&str] {
        &["py", "pyi"]
    }

    fn function_def_query(&self) -> &str {
        "(function_definition name: (identifier) @name) @func"
    }

    fn identifier_query(&self) -> &str {
        "(identifier) @name"
    }

    fn call_expr_query(&self) -> &str {
        "(call function: (identifier) @name) @call"
    }

    fn type_def_query(&self) -> &str {
        "(class_definition name: (identifier) @name) @type_def"
    }

    fn import_query(&self) -> &str {
        "(import_statement name: (dotted_name) @name) @import"
    }

    fn formatter_command(&self) -> Option<&[&str]> {
        Some(&["ruff", "format", "-"])
    }

    fn lsp_command(&self) -> Option<&[&str]> {
        Some(&["pyright-langserver", "--stdio"])
    }

    fn lsp_language_id(&self) -> &str {
        "python"
    }

    fn field_query(&self) -> &str {
        // Python doesn't have typed struct fields in the grammar; skip.
        ""
    }

    fn method_query(&self) -> &str {
        "(function_definition name: (identifier) @name) @method"
    }

    fn decorator_query(&self) -> &str {
        "(decorator) @decorator"
    }

    fn doc_comment_prefix(&self) -> &str {
        "#"
    }

    fn gen_field(&self, name: &str, _type_name: &str) -> String {
        format!("    {name} = None\n")
    }

    fn gen_method(&self, name: &str, params: &str, _return_type: &str, body: &str) -> String {
        format!("    def {name}({params}):\n        {body}\n")
    }

    fn gen_import(&self, path: &str) -> String {
        format!("import {path}\n")
    }
}
