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
}
