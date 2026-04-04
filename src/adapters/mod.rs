// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

pub mod python;
pub mod rust;

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
}

/// Select the appropriate adapter for a file extension.
pub fn adapter_for_extension(ext: &str) -> Option<&'static dyn LanguageAdapter> {
    match ext {
        "py" | "pyi" => Some(&python::PythonAdapter),
        "rs" => Some(&rust::RustAdapter),
        _ => None,
    }
}
