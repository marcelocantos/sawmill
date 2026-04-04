// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use super::LanguageAdapter;
use tree_sitter::Language;

pub struct TypeScriptAdapter;

impl LanguageAdapter for TypeScriptAdapter {
    fn language(&self) -> Language {
        tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into()
    }

    fn extensions(&self) -> &[&str] {
        &["ts", "tsx"]
    }

    fn function_def_query(&self) -> &str {
        "(function_declaration name: (identifier) @name) @func"
    }

    fn identifier_query(&self) -> &str {
        "[(identifier) (type_identifier) (property_identifier) (shorthand_property_identifier)] @name"
    }

    fn call_expr_query(&self) -> &str {
        "(call_expression function: (identifier) @name) @call"
    }

    fn type_def_query(&self) -> &str {
        "[(class_declaration name: (type_identifier) @name) (interface_declaration name: (type_identifier) @name) (type_alias_declaration name: (type_identifier) @name)] @type_def"
    }

    fn import_query(&self) -> &str {
        "(import_statement source: (string) @name) @import"
    }

    fn formatter_command(&self) -> Option<&[&str]> {
        Some(&["prettier", "--parser", "typescript"])
    }

    fn lsp_command(&self) -> Option<&[&str]> {
        Some(&["typescript-language-server", "--stdio"])
    }

    fn lsp_language_id(&self) -> &str {
        "typescript"
    }

    fn field_query(&self) -> &str {
        "(property_signature name: (property_identifier) @name) @field"
    }

    fn method_query(&self) -> &str {
        "(method_definition name: (property_identifier) @name) @method"
    }

    fn decorator_query(&self) -> &str {
        "(decorator) @decorator"
    }

    fn gen_field(&self, name: &str, type_name: &str) -> String {
        format!("  {name}: {type_name};\n")
    }

    fn gen_method(&self, name: &str, params: &str, return_type: &str, body: &str) -> String {
        format!("  {name}({params}): {return_type} {{\n    {body}\n  }}\n")
    }

    fn gen_import(&self, path: &str) -> String {
        format!("import {{ {path} }};\n")
    }
}
