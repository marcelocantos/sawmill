// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use super::LanguageAdapter;
use tree_sitter::Language;

pub struct CppAdapter;

impl LanguageAdapter for CppAdapter {
    fn language(&self) -> Language {
        tree_sitter_cpp::LANGUAGE.into()
    }

    fn extensions(&self) -> &[&str] {
        &["cpp", "cc", "cxx", "hpp", "hxx", "h"]
    }

    fn function_def_query(&self) -> &str {
        "(function_definition declarator: (function_declarator declarator: (identifier) @name)) @func"
    }

    fn identifier_query(&self) -> &str {
        "[(identifier) (type_identifier) (field_identifier) (namespace_identifier)] @name"
    }

    fn call_expr_query(&self) -> &str {
        "(call_expression function: (identifier) @name) @call"
    }

    fn type_def_query(&self) -> &str {
        "[(class_specifier name: (type_identifier) @name) (struct_specifier name: (type_identifier) @name)] @type_def"
    }

    fn import_query(&self) -> &str {
        "(preproc_include path: (_) @name) @import"
    }
}
