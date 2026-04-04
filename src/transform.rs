// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Match/act transformation engine.
//!
//! Combines two orthogonal dimensions:
//! - **Matching**: abstract (kind/name/scope) or raw Tree-sitter query
//! - **Acting**: declarative actions (replace, wrap, remove, etc.)

use anyhow::{Context, Result, bail};
use serde::Deserialize;
use streaming_iterator::StreamingIterator;
use tree_sitter::{Node, Query, QueryCursor};

use crate::adapters::LanguageAdapter;
use crate::forest::{ParsedFile, QueryResult};

/// How to find nodes.
#[derive(Debug, Clone, Deserialize, schemars::JsonSchema)]
#[serde(untagged)]
pub enum Match {
    /// Abstract matching — resolved per-language by the adapter.
    Abstract {
        /// Abstract node kind: "function", "class", "call", "import",
        /// "variable", "statement".
        kind: String,
        /// Symbol name filter (exact match or glob with `*`).
        #[serde(default)]
        name: Option<String>,
        /// Restrict to a specific file path.
        #[serde(default)]
        file: Option<String>,
    },
    /// Raw Tree-sitter S-expression query.
    Raw {
        /// Tree-sitter query string.
        raw_query: String,
        /// Which capture to act on (defaults to first capture).
        #[serde(default)]
        capture: Option<String>,
    },
}

/// What to do with matched nodes.
#[derive(Debug, Clone, Deserialize, schemars::JsonSchema)]
#[serde(tag = "action")]
pub enum Action {
    /// Replace the entire matched node with `code`.
    #[serde(rename = "replace")]
    Replace { code: String },
    /// Wrap the matched node with `before` and `after`.
    #[serde(rename = "wrap")]
    Wrap { before: String, after: String },
    /// Remove the wrapper around the matched node, keeping its contents.
    #[serde(rename = "unwrap")]
    Unwrap,
    /// Insert `code` before the matched node.
    #[serde(rename = "prepend_statement")]
    PrependStatement { code: String },
    /// Insert `code` after the matched node.
    #[serde(rename = "append_statement")]
    AppendStatement { code: String },
    /// Delete the matched node entirely.
    #[serde(rename = "remove")]
    Remove,
    /// Replace only the name/identifier of the matched node.
    #[serde(rename = "replace_name")]
    ReplaceName { code: String },
    /// Replace the body of a matched function/class.
    #[serde(rename = "replace_body")]
    ReplaceBody { code: String },
}

/// A single byte-range edit to apply to a source file.
#[derive(Debug, Clone)]
pub struct Edit {
    pub start: usize,
    pub end: usize,
    pub replacement: String,
}

/// Resolve an abstract kind to a Tree-sitter query using the language adapter.
fn resolve_abstract_query(
    adapter: &dyn LanguageAdapter,
    kind: &str,
    name: &Option<String>,
) -> Result<String> {
    let base_query = match kind {
        "function" => adapter.function_def_query(),
        "call" => adapter.call_expr_query(),
        "class" | "struct" | "type" => adapter.type_def_query(),
        "import" | "include" => adapter.import_query(),
        _ => bail!("unsupported abstract kind: {kind}"),
    };

    // If a name filter is given, wrap the query and add a predicate.
    // Tree-sitter requires predicates to be inside an outer grouping.
    match name {
        Some(n) if n.contains('*') => {
            let regex = n.replace('.', r"\.").replace('*', ".*");
            Ok(format!("({base_query} (#match? @name \"^{regex}$\"))"))
        }
        Some(n) => {
            Ok(format!("({base_query} (#eq? @name \"{n}\"))"))
        }
        None => Ok(base_query.to_string()),
    }
}

/// Find all matching nodes in a file and return edits for the given action.
pub fn transform_file(
    file: &ParsedFile,
    match_spec: &Match,
    action: &Action,
) -> Result<Vec<u8>> {
    let edits = collect_edits(file, match_spec, action)?;

    if edits.is_empty() {
        return Ok(file.original_source.clone());
    }

    apply_edits(&file.original_source, &edits)
}

/// Collect edits from matching nodes without applying them.
fn collect_edits(
    file: &ParsedFile,
    match_spec: &Match,
    action: &Action,
) -> Result<Vec<Edit>> {
    let (query_str, capture_name) = match match_spec {
        Match::Abstract { kind, name, file: file_filter } => {
            // Check file filter.
            if let Some(filter) = file_filter {
                let path_str = file.path.to_string_lossy();
                if !path_str.contains(filter.as_str()) {
                    return Ok(Vec::new());
                }
            }
            let q = resolve_abstract_query(file.adapter, kind, name)?;
            (q, None)
        }
        Match::Raw { raw_query, capture } => {
            (raw_query.clone(), capture.clone())
        }
    };

    let query = Query::new(&file.adapter.language(), &query_str)
        .with_context(|| format!("compiling query: {query_str}"))?;

    // Determine which capture to act on.
    // For abstract queries, prefer the "whole node" capture (@func, @call,
    // @type_def, @import) over @name.
    let target_capture = capture_name.as_deref().unwrap_or_else(|| {
        for candidate in &["func", "call", "type_def", "import"] {
            if query.capture_index_for_name(candidate).is_some() {
                return candidate;
            }
        }
        "name"
    });

    let target_idx = query.capture_index_for_name(target_capture)
        .with_context(|| format!("capture @{target_capture} not found in query"))?;

    let name_idx = query.capture_index_for_name("name");

    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    let mut edits = Vec::new();

    while let Some(m) = matches.next() {
        let target_node = m.captures.iter()
            .find(|c| c.index == target_idx)
            .map(|c| c.node);

        let name_node = name_idx.and_then(|idx| {
            m.captures.iter()
                .find(|c| c.index == idx)
                .map(|c| c.node)
        });

        if let Some(node) = target_node {
            let edit = make_edit(
                &file.original_source,
                node,
                name_node,
                action,
            )?;
            if let Some(e) = edit {
                edits.push(e);
            }
        }
    }

    // Sort by start position descending so we can apply back-to-front
    // without invalidating offsets. But for apply_edits we sort ascending.
    edits.sort_by_key(|e| e.start);
    Ok(edits)
}

/// Produce an edit for a single matched node + action.
fn make_edit(
    source: &[u8],
    node: Node,
    name_node: Option<Node>,
    action: &Action,
) -> Result<Option<Edit>> {
    let node_text = || std::str::from_utf8(&source[node.start_byte()..node.end_byte()])
        .unwrap_or("")
        .to_string();

    match action {
        Action::Replace { code } => Ok(Some(Edit {
            start: node.start_byte(),
            end: node.end_byte(),
            replacement: code.clone(),
        })),

        Action::Wrap { before, after } => {
            let text = node_text();
            Ok(Some(Edit {
                start: node.start_byte(),
                end: node.end_byte(),
                replacement: format!("{before}{text}{after}"),
            }))
        }

        Action::Unwrap => {
            // Find the first non-trivial child and use its text.
            let inner = find_body_or_inner(node, source);
            Ok(Some(Edit {
                start: node.start_byte(),
                end: node.end_byte(),
                replacement: inner,
            }))
        }

        Action::PrependStatement { code } => {
            // Insert before the node, preserving indentation.
            let indent = detect_indent(source, node.start_byte());
            Ok(Some(Edit {
                start: node.start_byte(),
                end: node.start_byte(),
                replacement: format!("{code}\n{indent}"),
            }))
        }

        Action::AppendStatement { code } => {
            let indent = detect_indent(source, node.start_byte());
            Ok(Some(Edit {
                start: node.end_byte(),
                end: node.end_byte(),
                replacement: format!("\n{indent}{code}"),
            }))
        }

        Action::Remove => Ok(Some(Edit {
            start: node.start_byte(),
            end: consume_trailing_newline(source, node.end_byte()),
            replacement: String::new(),
        })),

        Action::ReplaceName { code } => {
            match name_node {
                Some(n) => Ok(Some(Edit {
                    start: n.start_byte(),
                    end: n.end_byte(),
                    replacement: code.clone(),
                })),
                None => bail!("replace_name: no @name capture found for matched node"),
            }
        }

        Action::ReplaceBody { code } => {
            let body = find_body_node(node);
            match body {
                Some(b) => Ok(Some(Edit {
                    start: b.start_byte(),
                    end: b.end_byte(),
                    replacement: code.clone(),
                })),
                None => bail!("replace_body: no body found for matched node"),
            }
        }
    }
}

/// Apply a sorted list of non-overlapping edits to source bytes.
fn apply_edits(source: &[u8], edits: &[Edit]) -> Result<Vec<u8>> {
    let mut result = Vec::with_capacity(source.len());
    let mut last_end = 0;

    for edit in edits {
        if edit.start < last_end {
            bail!("overlapping edits at byte {}", edit.start);
        }
        result.extend_from_slice(&source[last_end..edit.start]);
        result.extend_from_slice(edit.replacement.as_bytes());
        last_end = edit.end;
    }
    result.extend_from_slice(&source[last_end..]);

    Ok(result)
}

/// Detect the indentation at a given byte offset by scanning backwards to the
/// start of the line.
fn detect_indent(source: &[u8], offset: usize) -> String {
    let line_start = source[..offset].iter().rposition(|&b| b == b'\n')
        .map(|p| p + 1)
        .unwrap_or(0);
    let indent_bytes = &source[line_start..offset];
    let indent: String = indent_bytes.iter()
        .take_while(|&&b| b == b' ' || b == b'\t')
        .map(|&b| b as char)
        .collect();
    indent
}

/// If the byte after `end` is a newline, consume it so deletions don't leave
/// blank lines.
fn consume_trailing_newline(source: &[u8], end: usize) -> usize {
    if end < source.len() && source[end] == b'\n' {
        end + 1
    } else {
        end
    }
}

/// Find the body/block child of a node (for replace_body).
fn find_body_node(node: Node) -> Option<Node> {
    // Try common field names first.
    for field in &["body", "block", "consequence"] {
        if let Some(child) = node.child_by_field_name(field) {
            return Some(child);
        }
    }
    None
}

/// Find the inner content of a wrapper node (for unwrap).
fn find_body_or_inner(node: Node, source: &[u8]) -> String {
    if let Some(body) = find_body_node(node) {
        return std::str::from_utf8(&source[body.start_byte()..body.end_byte()])
            .unwrap_or("")
            .to_string();
    }
    // Fallback: return text of all children joined.
    let mut parts = Vec::new();
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        let text = &source[child.start_byte()..child.end_byte()];
        if let Ok(s) = std::str::from_utf8(text) {
            parts.push(s.to_string());
        }
    }
    parts.join("")
}

/// Query a file for matching nodes (read-only, no edits).
pub fn query_file(
    file: &ParsedFile,
    match_spec: &Match,
) -> Result<Vec<QueryResult>> {
    let (query_str, capture_name) = match match_spec {
        Match::Abstract { kind, name, file: file_filter } => {
            if let Some(filter) = file_filter {
                let path_str = file.path.to_string_lossy();
                if !path_str.contains(filter.as_str()) {
                    return Ok(Vec::new());
                }
            }
            let q = resolve_abstract_query(file.adapter, kind, name)?;
            (q, None)
        }
        Match::Raw { raw_query, capture } => {
            (raw_query.clone(), capture.clone())
        }
    };

    let query = Query::new(&file.adapter.language(), &query_str)
        .with_context(|| format!("compiling query: {query_str}"))?;

    // For abstract queries, prefer the "whole node" capture (@func, @call,
    // @type_def, @import) over @name.
    let target_capture = capture_name.as_deref().unwrap_or_else(|| {
        for candidate in &["func", "call", "type_def", "import"] {
            if query.capture_index_for_name(candidate).is_some() {
                return candidate;
            }
        }
        "name"
    });

    let target_idx = query.capture_index_for_name(target_capture)
        .with_context(|| format!("capture @{target_capture} not found in query"))?;

    let name_idx = query.capture_index_for_name("name");

    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    let mut results = Vec::new();

    while let Some(m) = matches.next() {
        let target_node = m.captures.iter()
            .find(|c| c.index == target_idx)
            .map(|c| c.node);

        let name_text = name_idx.and_then(|idx| {
            m.captures.iter()
                .find(|c| c.index == idx)
                .map(|c| {
                    std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()])
                        .unwrap_or("")
                        .to_string()
                })
        });

        if let Some(node) = target_node {
            let text = std::str::from_utf8(&file.original_source[node.start_byte()..node.end_byte()])
                .unwrap_or("")
                .to_string();

            // Truncate text for readability.
            let display_text = if text.len() > 200 {
                format!("{}...", &text[..200])
            } else {
                text
            };

            results.push(QueryResult {
                path: file.path.clone(),
                start_line: node.start_position().row + 1,
                start_col: node.start_position().column + 1,
                kind: node.kind().to_string(),
                name: name_text,
                text: display_text,
            });
        }
    }

    Ok(results)
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

    fn transform_str(source: &str, match_spec: &Match, action: &Action) -> String {
        let file = parse_python(source);
        let result = transform_file(&file, match_spec, action).unwrap();
        String::from_utf8(result).unwrap()
    }

    #[test]
    fn remove_function() {
        let source = "def foo():\n    pass\n\ndef bar():\n    pass\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "function".into(),
                name: Some("foo".into()),
                file: None,
            },
            &Action::Remove,
        );
        assert_eq!(result, "\ndef bar():\n    pass\n");
    }

    #[test]
    fn wrap_call() {
        let source = "result = compute(x)\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "call".into(),
                name: Some("compute".into()),
                file: None,
            },
            &Action::Wrap {
                before: "try_catch(".into(),
                after: ")".into(),
            },
        );
        assert_eq!(result, "result = try_catch(compute(x))\n");
    }

    #[test]
    fn replace_function_name() {
        let source = "def old_func():\n    pass\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "function".into(),
                name: Some("old_func".into()),
                file: None,
            },
            &Action::ReplaceName { code: "new_func".into() },
        );
        assert_eq!(result, "def new_func():\n    pass\n");
    }

    #[test]
    fn prepend_statement() {
        let source = "def foo():\n    return 1\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "function".into(),
                name: Some("foo".into()),
                file: None,
            },
            &Action::PrependStatement { code: "# marker".into() },
        );
        assert_eq!(result, "# marker\ndef foo():\n    return 1\n");
    }

    #[test]
    fn append_statement() {
        let source = "def foo():\n    return 1\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "function".into(),
                name: Some("foo".into()),
                file: None,
            },
            &Action::AppendStatement { code: "# end".into() },
        );
        assert_eq!(result, "def foo():\n    return 1\n# end\n");
    }

    #[test]
    fn replace_with_code() {
        let source = "x = old_value\n";
        let result = transform_str(
            source,
            &Match::Raw {
                raw_query: r#"((identifier) @name (#eq? @name "old_value"))"#.into(),
                capture: Some("name".into()),
            },
            &Action::Replace { code: "new_value".into() },
        );
        assert_eq!(result, "x = new_value\n");
    }

    #[test]
    fn glob_name_match() {
        let source = "def test_foo():\n    pass\n\ndef test_bar():\n    pass\n\ndef helper():\n    pass\n";
        let result = transform_str(
            source,
            &Match::Abstract {
                kind: "function".into(),
                name: Some("test_*".into()),
                file: None,
            },
            &Action::Remove,
        );
        assert_eq!(result, "\n\ndef helper():\n    pass\n");
    }
}
