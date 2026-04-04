// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Embedded QuickJS engine for programmable transforms.
//!
//! Executes user-supplied JavaScript functions against matched AST nodes.
//! The JS function receives a node object and returns either:
//! - The original node (unchanged) → no edit
//! - `null` → delete the node
//! - A string → replace the node's text entirely
//! - `node.replaceText(...)`, `node.replaceName(...)`, etc. → specific mutation

use anyhow::{Context, Result, bail};
use rquickjs::{Context as JsContext, Runtime as JsRuntime, Function, Value};
use streaming_iterator::StreamingIterator;

use crate::adapters::LanguageAdapter;
use crate::transform::Edit;

/// Convert a JS return value into an Edit for a specific node.
fn interpret_and_edit(
    value: &Value,
    node: tree_sitter::Node,
    name_node: Option<tree_sitter::Node>,
    source: &[u8],
) -> Result<Option<Edit>> {
    // null → remove
    if value.is_null() {
        let end = if node.end_byte() < source.len() && source[node.end_byte()] == b'\n' {
            node.end_byte() + 1
        } else {
            node.end_byte()
        };
        return Ok(Some(Edit {
            start: node.start_byte(),
            end,
            replacement: String::new(),
        }));
    }

    // string → replace entire node text
    if let Some(s) = value.as_string() {
        let text = s.to_string().context("converting JS string")?;
        return Ok(Some(Edit {
            start: node.start_byte(),
            end: node.end_byte(),
            replacement: text,
        }));
    }

    // object → check _mutation_type
    if let Some(obj) = value.as_object() {
        let mutation_type: Option<String> = obj.get::<_, Option<String>>("_mutation_type")
            .unwrap_or(None);

        match mutation_type.as_deref() {
            None => return Ok(None), // No mutation — unchanged
            Some("replaceText") => {
                let text: String = obj.get("_mutation")
                    .context("getting _mutation for replaceText")?;
                return Ok(Some(Edit {
                    start: node.start_byte(),
                    end: node.end_byte(),
                    replacement: text,
                }));
            }
            Some("replaceBody") => {
                let text: String = obj.get("_mutation")
                    .context("getting _mutation for replaceBody")?;
                let body = node.child_by_field_name("body")
                    .context("node has no body for replaceBody")?;
                return Ok(Some(Edit {
                    start: body.start_byte(),
                    end: body.end_byte(),
                    replacement: text,
                }));
            }
            Some("replaceName") => {
                let text: String = obj.get("_mutation")
                    .context("getting _mutation for replaceName")?;
                let name = name_node.context("no @name capture for replaceName")?;
                return Ok(Some(Edit {
                    start: name.start_byte(),
                    end: name.end_byte(),
                    replacement: text,
                }));
            }
            Some("remove") => {
                let end = if node.end_byte() < source.len() && source[node.end_byte()] == b'\n' {
                    node.end_byte() + 1
                } else {
                    node.end_byte()
                };
                return Ok(Some(Edit {
                    start: node.start_byte(),
                    end,
                    replacement: String::new(),
                }));
            }
            Some("wrap") => {
                let before: String = obj.get("_before")
                    .context("getting _before for wrap")?;
                let after: String = obj.get("_after")
                    .context("getting _after for wrap")?;
                let text = std::str::from_utf8(&source[node.start_byte()..node.end_byte()])
                    .unwrap_or("");
                return Ok(Some(Edit {
                    start: node.start_byte(),
                    end: node.end_byte(),
                    replacement: format!("{before}{text}{after}"),
                }));
            }
            Some("insertBefore") => {
                let code: String = obj.get("_mutation")
                    .context("getting _mutation for insertBefore")?;
                let indent = detect_indent(source, node.start_byte());
                return Ok(Some(Edit {
                    start: node.start_byte(),
                    end: node.start_byte(),
                    replacement: format!("{code}\n{indent}"),
                }));
            }
            Some("insertAfter") => {
                let code: String = obj.get("_mutation")
                    .context("getting _mutation for insertAfter")?;
                let indent = detect_indent(source, node.start_byte());
                return Ok(Some(Edit {
                    start: node.end_byte(),
                    end: node.end_byte(),
                    replacement: format!("\n{indent}{code}"),
                }));
            }
            Some(other) => bail!("unknown _mutation_type: {other}"),
        }
    }

    // undefined → unchanged
    if value.is_undefined() {
        return Ok(None);
    }

    bail!("transform_fn returned unexpected type")
}

fn detect_indent(source: &[u8], offset: usize) -> String {
    let line_start = source[..offset].iter().rposition(|&b| b == b'\n')
        .map(|p| p + 1)
        .unwrap_or(0);
    source[line_start..offset].iter()
        .take_while(|&&b| b == b' ' || b == b'\t')
        .map(|&b| b as char)
        .collect()
}

/// JavaScript helper functions injected into every transform context.
/// These create plain objects with _mutation_type markers — no closures
/// over JS objects, avoiding GC circular reference issues.
const JS_HELPERS: &str = r#"
globalThis.__makeNode = function(props) {
    var n = Object.assign({}, props);
    n.replaceText = function(text) { return { _mutation_type: "replaceText", _mutation: text }; };
    n.replaceBody = function(body) { return { _mutation_type: "replaceBody", _mutation: body }; };
    n.replaceName = function(name) { return { _mutation_type: "replaceName", _mutation: name }; };
    n.remove = function() { return null; };
    n.wrap = function(before, after) { return { _mutation_type: "wrap", _before: before, _after: after }; };
    n.insertBefore = function(code) { return { _mutation_type: "insertBefore", _mutation: code }; };
    n.insertAfter = function(code) { return { _mutation_type: "insertAfter", _mutation: code }; };
    return n;
};
"#;

/// Execute a JavaScript transform function against nodes matching a query.
pub fn run_js_transform(
    source: &[u8],
    tree: &tree_sitter::Tree,
    query_str: &str,
    transform_fn: &str,
    file_path: &str,
    _adapter: &dyn LanguageAdapter,
) -> Result<Vec<u8>> {
    let lang = _adapter.language();
    let query = tree_sitter::Query::new(&lang, query_str)
        .with_context(|| format!("compiling query: {query_str}"))?;

    // Determine target capture.
    let target_idx = ["func", "call", "type_def", "import"].iter()
        .find_map(|name| query.capture_index_for_name(name))
        .unwrap_or(0);
    let name_idx = query.capture_index_for_name("name");

    // Collect matched node byte ranges first (to avoid lifetime issues with cursor).
    let mut matched: Vec<(usize, usize, Option<(usize, usize)>)> = Vec::new();
    {
        let mut cursor = tree_sitter::QueryCursor::new();
        let mut matches = cursor.matches(&query, tree.root_node(), source);
        while let Some(m) = matches.next() {
            let target = m.captures.iter()
                .find(|c| c.index == target_idx)
                .map(|c| (c.node.start_byte(), c.node.end_byte()));
            let name = name_idx.and_then(|idx|
                m.captures.iter().find(|c| c.index == idx)
                    .map(|c| (c.node.start_byte(), c.node.end_byte()))
            );
            if let Some((start, end)) = target {
                matched.push((start, end, name));
            }
        }
    }

    if matched.is_empty() {
        return Ok(source.to_vec());
    }

    let runtime = JsRuntime::new()
        .context("creating QuickJS runtime")?;
    runtime.set_memory_limit(64 * 1024 * 1024); // 64MB
    runtime.set_max_stack_size(1024 * 1024); // 1MB stack

    let context = JsContext::full(&runtime)
        .context("creating QuickJS context")?;

    let edits = context.with(|ctx| -> Result<Vec<Edit>> {
        // Inject helper functions.
        let _: Value = ctx.eval(JS_HELPERS.as_bytes())
            .context("injecting JS helpers")?;

        // Compile the user's transform function.
        let fn_source = format!("({transform_fn})");
        let func: Function = ctx.eval(fn_source.as_bytes())
            .context("compiling transform_fn — must be a function expression like (node) => {{ ... }}")?;

        let mut edits = Vec::new();

        for &(start, end, name_range) in &matched {
            // Find the tree-sitter nodes by byte range.
            let node = tree.root_node().descendant_for_byte_range(start, end)
                .context("finding node by byte range")?;
            let name_node = name_range.and_then(|(ns, ne)|
                tree.root_node().descendant_for_byte_range(ns, ne)
            );

            // Build a plain JS object with node properties.
            let node_text = std::str::from_utf8(&source[start..end]).unwrap_or("");
            let name_text = name_range.map(|(ns, ne)|
                std::str::from_utf8(&source[ns..ne]).unwrap_or("")
            );
            let body_text = node.child_by_field_name("body").map(|b|
                std::str::from_utf8(&source[b.start_byte()..b.end_byte()]).unwrap_or("")
            );
            let params_text = node.child_by_field_name("parameters").map(|p|
                std::str::from_utf8(&source[p.start_byte()..p.end_byte()]).unwrap_or("")
            );

            // Build node via JS helper to avoid Rust→JS object lifetime issues.
            let make_node: Function = ctx.globals().get("__makeNode")
                .context("getting __makeNode")?;

            let props = rquickjs::Object::new(ctx.clone())
                .context("creating props object")?;
            props.set("kind", node.kind())?;
            props.set("tsKind", node.kind())?;
            props.set("text", node_text)?;
            props.set("file", file_path)?;
            props.set("startLine", node.start_position().row + 1)?;
            props.set("endLine", node.end_position().row + 1)?;
            match name_text {
                Some(n) => props.set("name", n)?,
                None => props.set("name", Value::new_null(ctx.clone()))?,
            }
            match body_text {
                Some(b) => props.set("body", b)?,
                None => props.set("body", Value::new_null(ctx.clone()))?,
            }
            match params_text {
                Some(p) => props.set("parameters", p)?,
                None => props.set("parameters", Value::new_null(ctx.clone()))?,
            }

            let node_obj: Value = make_node.call((props,))
                .context("calling __makeNode")?;

            let result: Value = func.call((node_obj,))
                .context("calling transform_fn")?;

            if let Some(edit) = interpret_and_edit(&result, node, name_node, source)? {
                edits.push(edit);
            }
        }

        Ok(edits)
    })?;

    let mut sorted_edits = edits;
    sorted_edits.sort_by_key(|e| e.start);
    crate::transform::apply_edits_pub(source, &sorted_edits)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::python::PythonAdapter;
    use crate::adapters::LanguageAdapter;
    use crate::forest::ParsedFile;
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

    fn js_transform(source: &str, transform_fn: &str) -> String {
        let file = parse_python(source);
        let query_str = file.adapter.function_def_query();
        let result = run_js_transform(
            &file.original_source,
            &file.tree,
            query_str,
            transform_fn,
            "test.py",
            file.adapter,
        ).unwrap();
        String::from_utf8(result).unwrap()
    }

    #[test]
    fn js_rename_function() {
        let result = js_transform(
            "def foo():\n    pass\n\ndef bar():\n    pass\n",
            r#"(node) => node.name === "foo" ? node.replaceName("baz") : node"#,
        );
        assert_eq!(result, "def baz():\n    pass\n\ndef bar():\n    pass\n");
    }

    #[test]
    fn js_remove_by_condition() {
        let result = js_transform(
            "def test_a():\n    pass\n\ndef helper():\n    pass\n",
            r#"(node) => node.name.startsWith("test_") ? node.remove() : node"#,
        );
        assert_eq!(result, "\ndef helper():\n    pass\n");
    }

    #[test]
    fn js_wrap_function() {
        let result = js_transform(
            "def foo():\n    pass\n",
            r##"(node) => node.wrap("# BEGIN\n", "\n# END")"##,
        );
        assert_eq!(result, "# BEGIN\ndef foo():\n    pass\n# END\n");
    }

    #[test]
    fn js_return_string() {
        let result = js_transform(
            "def foo():\n    pass\n",
            r##"(node) => "# replaced\n""##,
        );
        assert_eq!(result, "# replaced\n\n");
    }

    #[test]
    fn js_unchanged() {
        let source = "def foo():\n    pass\n";
        let result = js_transform(source, "(node) => node");
        assert_eq!(result, source);
    }
}
