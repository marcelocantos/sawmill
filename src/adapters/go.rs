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

    fn formatter_command(&self) -> Option<&[&str]> {
        Some(&["gofmt"])
    }

    fn lsp_command(&self) -> Option<&[&str]> {
        Some(&["gopls"])
    }

    fn lsp_language_id(&self) -> &str {
        "go"
    }

    fn field_query(&self) -> &str {
        "(field_declaration name: (field_identifier) @name type: (_) @type) @field"
    }

    fn method_query(&self) -> &str {
        "(method_declaration name: (field_identifier) @name) @method"
    }

    fn decorator_query(&self) -> &str {
        // Go doesn't have decorators.
        ""
    }

    fn gen_field(&self, name: &str, type_name: &str) -> String {
        // Go puts type after name.
        format!("    {name} {type_name}\n")
    }

    fn gen_method(&self, name: &str, params: &str, return_type: &str, body: &str) -> String {
        format!("func {name}({params}) {return_type} {{\n    {body}\n}}\n")
    }

    fn gen_import(&self, path: &str) -> String {
        format!("import \"{path}\"\n")
    }
}
