// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

pub mod cpp;
pub mod go;
pub mod python;
pub mod rust;
pub mod typescript;

use tree_sitter::Language;

/// Maps abstract structural patterns to language-specific Tree-sitter queries.
pub trait LanguageAdapter: Send + Sync {
    /// The Tree-sitter language grammar.
    fn language(&self) -> Language;

    /// File extensions this adapter handles.
    fn extensions(&self) -> &[&str];

    /// Tree-sitter query for function/method definitions.
    /// Must capture `@name` for the identifier and `@func` for the whole node.
    fn function_def_query(&self) -> &str;

    /// Tree-sitter query for identifier references (usages of a name).
    /// Must capture `@name`.
    fn identifier_query(&self) -> &str;

    /// Tree-sitter query for call expressions.
    /// Must capture `@name` for the function name and `@call` for the whole expression.
    fn call_expr_query(&self) -> &str;

    /// Tree-sitter query for class/struct/type definitions.
    /// Must capture `@name` for the type name and `@type_def` for the whole node.
    fn type_def_query(&self) -> &str;

    /// Tree-sitter query for import/include statements.
    /// Must capture `@name` for the module name and `@import` for the whole statement.
    fn import_query(&self) -> &str;
}

/// Select the appropriate adapter for a file extension.
pub fn adapter_for_extension(ext: &str) -> Option<&'static dyn LanguageAdapter> {
    match ext {
        "py" | "pyi" => Some(&python::PythonAdapter),
        "rs" => Some(&rust::RustAdapter),
        "ts" | "tsx" => Some(&typescript::TypeScriptAdapter),
        "cpp" | "cc" | "cxx" | "hpp" | "hxx" | "h" => Some(&cpp::CppAdapter),
        "go" => Some(&go::GoAdapter),
        _ => None,
    }
}
