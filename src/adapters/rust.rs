// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use super::LanguageAdapter;
use tree_sitter::Language;

pub struct RustAdapter;

impl LanguageAdapter for RustAdapter {
    fn language(&self) -> Language {
        tree_sitter_rust::LANGUAGE.into()
    }

    fn extensions(&self) -> &[&str] {
        &["rs"]
    }

    fn function_def_query(&self) -> &str {
        "(function_item name: (identifier) @name) @func"
    }

    fn identifier_query(&self) -> &str {
        "[(identifier) (type_identifier)] @name"
    }

    fn call_expr_query(&self) -> &str {
        "(call_expression function: (identifier) @name) @call"
    }

    fn type_def_query(&self) -> &str {
        "[(struct_item name: (type_identifier) @name) (enum_item name: (type_identifier) @name) (trait_item name: (type_identifier) @name)] @type_def"
    }

    fn import_query(&self) -> &str {
        "(use_declaration argument: (_) @name) @import"
    }

    fn formatter_command(&self) -> Option<&[&str]> {
        Some(&["rustfmt"])
    }

    fn lsp_command(&self) -> Option<&[&str]> {
        Some(&["rust-analyzer"])
    }

    fn lsp_language_id(&self) -> &str {
        "rust"
    }
}
