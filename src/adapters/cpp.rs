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

    fn formatter_command(&self) -> Option<&[&str]> {
        Some(&["clang-format"])
    }

    fn lsp_command(&self) -> Option<&[&str]> {
        Some(&["clangd"])
    }

    fn lsp_language_id(&self) -> &str {
        "cpp"
    }

    fn field_query(&self) -> &str {
        "(field_declaration declarator: (field_identifier) @name type: (_) @type) @field"
    }

    fn method_query(&self) -> &str {
        "(function_definition declarator: (function_declarator declarator: (_) @name)) @method"
    }

    fn decorator_query(&self) -> &str {
        // C++ doesn't have decorators in the Tree-sitter grammar.
        ""
    }

    fn gen_field(&self, name: &str, type_name: &str) -> String {
        // C++ puts type before name.
        format!("  {type_name} {name};\n")
    }

    fn gen_method(&self, name: &str, params: &str, return_type: &str, body: &str) -> String {
        format!("  {return_type} {name}({params}) {{\n    {body}\n  }}\n")
    }

    fn gen_import(&self, path: &str) -> String {
        format!("#include \"{path}\"\n")
    }
}
