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
}
