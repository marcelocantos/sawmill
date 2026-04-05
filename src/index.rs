// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Symbol extraction and indexing.
//!
//! Parses a file's Tree-sitter tree and extracts symbols (functions, types,
//! imports, calls) for indexing in the store.

use streaming_iterator::StreamingIterator;
use tree_sitter::Query;

use crate::forest::ParsedFile;

/// A symbol extracted from a parsed file.
#[derive(Debug, Clone)]
pub struct Symbol {
    pub name: String,
    pub kind: String,
    pub file_path: String,
    pub start_line: usize,
    pub start_col: usize,
    pub end_line: usize,
    pub end_col: usize,
    pub start_byte: usize,
    pub end_byte: usize,
    /// Byte range of the name/identifier node.
    pub name_start_byte: usize,
    pub name_end_byte: usize,
}

/// Extract all symbols from a parsed file.
pub fn extract_symbols(file: &ParsedFile) -> Vec<Symbol> {
    let mut symbols = Vec::new();
    let file_path = file.path.to_string_lossy().to_string();

    // Extract functions.
    extract_with_query(
        file, "function", file.adapter.function_def_query(),
        &file_path, &mut symbols,
    );

    // Extract types.
    let type_query = file.adapter.type_def_query();
    if !type_query.is_empty() {
        extract_with_query(file, "type", type_query, &file_path, &mut symbols);
    }

    // Extract imports.
    let import_query = file.adapter.import_query();
    if !import_query.is_empty() {
        extract_with_query(file, "import", import_query, &file_path, &mut symbols);
    }

    // Extract calls.
    let call_query = file.adapter.call_expr_query();
    if !call_query.is_empty() {
        extract_with_query(file, "call", call_query, &file_path, &mut symbols);
    }

    symbols
}

fn extract_with_query(
    file: &ParsedFile,
    kind: &str,
    query_str: &str,
    file_path: &str,
    symbols: &mut Vec<Symbol>,
) {
    let query = match Query::new(&file.adapter.language(), query_str) {
        Ok(q) => q,
        Err(_) => return, // Skip if query doesn't compile for this grammar.
    };

    let name_idx = match query.capture_index_for_name("name") {
        Some(idx) => idx,
        None => return,
    };

    // Find the "whole node" capture.
    let whole_idx = ["func", "call", "type_def", "import"].iter()
        .find_map(|name| query.capture_index_for_name(name))
        .unwrap_or(name_idx);

    let mut cursor = tree_sitter::QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    while let Some(m) = matches.next() {
        let name_node = m.captures.iter()
            .find(|c| c.index == name_idx)
            .map(|c| c.node);
        let whole_node = m.captures.iter()
            .find(|c| c.index == whole_idx)
            .map(|c| c.node);

        if let (Some(name_n), Some(whole_n)) = (name_node, whole_node) {
            let name = std::str::from_utf8(
                &file.original_source[name_n.start_byte()..name_n.end_byte()]
            ).unwrap_or("").to_string();

            if !name.is_empty() {
                symbols.push(Symbol {
                    name,
                    kind: kind.to_string(),
                    file_path: file_path.to_string(),
                    start_line: whole_n.start_position().row + 1,
                    start_col: whole_n.start_position().column + 1,
                    end_line: whole_n.end_position().row + 1,
                    end_col: whole_n.end_position().column + 1,
                    start_byte: whole_n.start_byte(),
                    end_byte: whole_n.end_byte(),
                    name_start_byte: name_n.start_byte(),
                    name_end_byte: name_n.end_byte(),
                });
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::python::PythonAdapter;
    use crate::adapters::LanguageAdapter;
    use std::path::PathBuf;
    use tree_sitter::Parser;

    fn parse_python(source: &str) -> ParsedFile {
        let adapter: &'static dyn LanguageAdapter = &PythonAdapter;
        let source_bytes = source.as_bytes().to_vec();
        let mut parser = Parser::new();
        parser.set_language(&adapter.language()).unwrap();
        let tree = parser.parse(&source_bytes, None).unwrap();
        ParsedFile {
            path: PathBuf::from("test.py"),
            original_source: source_bytes,
            tree,
            adapter,
        }
    }

    #[test]
    fn extract_python_symbols() {
        let source = r#"
import os

class MyClass:
    pass

def my_function():
    os.path.join("a", "b")
"#;
        let file = parse_python(source);
        let symbols = extract_symbols(&file);

        let names: Vec<&str> = symbols.iter().map(|s| s.name.as_str()).collect();
        assert!(names.contains(&"my_function"), "should find function: {names:?}");
        assert!(names.contains(&"MyClass"), "should find class: {names:?}");

        let kinds: Vec<&str> = symbols.iter()
            .filter(|s| s.name == "my_function")
            .map(|s| s.kind.as_str())
            .collect();
        assert_eq!(kinds, vec!["function"]);
    }
}
