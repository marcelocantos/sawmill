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
}
