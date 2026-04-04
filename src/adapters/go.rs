// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use super::LanguageAdapter;
use tree_sitter::Language;

pub struct GoAdapter;

impl LanguageAdapter for GoAdapter {
    fn language(&self) -> Language {
        tree_sitter_go::LANGUAGE.into()
    }

    fn extensions(&self) -> &[&str] {
        &["go"]
    }

    fn function_def_query(&self) -> &str {
        "(function_declaration name: (identifier) @name) @func"
    }

    fn identifier_query(&self) -> &str {
        "[(identifier) (type_identifier) (field_identifier) (package_identifier)] @name"
    }

    fn call_expr_query(&self) -> &str {
        "(call_expression function: (identifier) @name) @call"
    }

    fn type_def_query(&self) -> &str {
        "(type_declaration (type_spec name: (type_identifier) @name)) @type_def"
    }

    fn import_query(&self) -> &str {
        "(import_spec path: (interpreted_string_literal) @name) @import"
    }
}
