// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Code generator runtime.
//!
//! Executes a JavaScript program against the entire codebase model.
//! The program receives a `ctx` object with methods for querying symbols,
//! navigating code structure, and making coordinated edits across files.

use std::cell::RefCell;
use std::collections::HashMap;
use std::path::PathBuf;
use std::rc::Rc;

use anyhow::{Context, Result};
use rquickjs::{Context as JsContext, Function, Runtime as JsRuntime, Value};
use streaming_iterator::StreamingIterator;

use crate::forest::{FileChange, Forest, ParsedFile};
use crate::index;
use crate::transform::Edit;

/// Accumulated edits across multiple files.
struct EditCollector {
    /// file_path → list of edits
    edits: HashMap<String, Vec<Edit>>,
    /// file_path → new file content (for addFile)
    new_files: HashMap<String, String>,
}

impl EditCollector {
    fn new() -> Self {
        EditCollector {
            edits: HashMap::new(),
            new_files: HashMap::new(),
        }
    }

    fn add_edit(&mut self, file: &str, edit: Edit) {
        self.edits.entry(file.to_string()).or_default().push(edit);
    }

    fn add_new_file(&mut self, path: &str, content: &str) {
        self.new_files.insert(path.to_string(), content.to_string());
    }
}

/// JS helpers for the codegen context.
const CODEGEN_HELPERS: &str = r#"
globalThis.__makeNode = function(props) {
    var n = Object.assign({}, props);

    // --- Mutation methods ---
    n.replaceText = function(text) {
        __editFile(n.file, n.startByte, n.endByte, text);
        return n;
    };
    n.replaceBody = function(body) {
        if (n.bodyStartByte !== null) {
            __editFile(n.file, n.bodyStartByte, n.bodyEndByte, body);
        }
        return n;
    };
    n.replaceName = function(name) {
        if (n.nameStartByte !== null) {
            __editFile(n.file, n.nameStartByte, n.nameEndByte, name);
        }
        return n;
    };
    n.remove = function() {
        __editFile(n.file, n.startByte, n.endByte, "");
        return n;
    };
    n.insertBefore = function(code) {
        __editFile(n.file, n.startByte, n.startByte, code + "\n");
        return n;
    };
    n.insertAfter = function(code) {
        __editFile(n.file, n.endByte, n.endByte, "\n" + code);
        return n;
    };

    // --- Structural navigation ---
    n.fields = function() {
        return n._fields || [];
    };
    n.methods = function() {
        return (n._methods || []).map(function(m) { return __makeNode(m); });
    };
    n.method = function(name) {
        var ms = n.methods();
        for (var i = 0; i < ms.length; i++) {
            if (ms[i].name === name) return ms[i];
        }
        return null;
    };
    n.returnType = function() {
        // Extracted from text heuristically if not provided.
        return n._returnType || null;
    };

    // --- Semantic mutations ---
    n.addField = function(name, type) {
        var code = __genField(n._langId || "", name, type);
        if (n.bodyEndByte !== null && n.bodyEndByte !== undefined) {
            // Insert before the end of the body.
            __editFile(n.file, n.bodyEndByte, n.bodyEndByte, code);
        } else if (n.endByte) {
            // Insert before the end of the node.
            __editFile(n.file, n.endByte - 1, n.endByte - 1, code);
        }
        return n;
    };
    n.addMethod = function(name, params, returnType, body) {
        var code = __genMethod(n._langId || "", name, params, returnType, body);
        if (n.bodyEndByte !== null && n.bodyEndByte !== undefined) {
            __editFile(n.file, n.bodyEndByte, n.bodyEndByte, "\n" + code);
        } else if (n.endByte) {
            __editFile(n.file, n.endByte, n.endByte, "\n" + code);
        }
        return n;
    };

    return n;
};
"#;

/// Execute a codegen program against the forest.
/// Returns file changes (edits + new files).
pub fn run_codegen(
    forest: &Forest,
    program: &str,
) -> Result<Vec<FileChange>> {
    let runtime = JsRuntime::new()
        .context("creating QuickJS runtime")?;
    runtime.set_memory_limit(2 * 1024 * 1024 * 1024);
    runtime.set_max_stack_size(8 * 1024 * 1024);

    let context = JsContext::full(&runtime)
        .context("creating QuickJS context")?;

    let collector = Rc::new(RefCell::new(EditCollector::new()));

    let changes = context.with(|ctx| -> Result<Vec<FileChange>> {
        // Inject helpers.
        let _: Value = ctx.eval(CODEGEN_HELPERS.as_bytes())
            .context("injecting codegen helpers")?;

        // Register __editFile callback.
        let collector_ref = collector.clone();
        ctx.globals().set("__editFile", Function::new(ctx.clone(),
            move |file: String, start: usize, end: usize, replacement: String| {
                collector_ref.borrow_mut().add_edit(&file, Edit {
                    start,
                    end,
                    replacement,
                });
            },
        ).context("creating __editFile")?).context("setting __editFile")?;

        // Register __addFile callback.
        let collector_ref = collector.clone();
        ctx.globals().set("__addFile", Function::new(ctx.clone(),
            move |path: String, content: String| {
                collector_ref.borrow_mut().add_new_file(&path, &content);
            },
        ).context("creating __addFile")?).context("setting __addFile")?;

        // Register code generation callbacks.
        // These dispatch to the appropriate language adapter.
        ctx.globals().set("__genField", Function::new(ctx.clone(),
            |lang_id: String, name: String, type_name: String| -> String {
                gen_for_lang(&lang_id, |a| a.gen_field(&name, &type_name))
            },
        ).context("creating __genField")?).context("setting __genField")?;

        ctx.globals().set("__genMethod", Function::new(ctx.clone(),
            |lang_id: String, name: String, params: String, return_type: String, body: String| -> String {
                gen_for_lang(&lang_id, |a| a.gen_method(&name, &params, &return_type, &body))
            },
        ).context("creating __genMethod")?).context("setting __genMethod")?;

        ctx.globals().set("__genImport", Function::new(ctx.clone(),
            |lang_id: String, path: String| -> String {
                gen_for_lang(&lang_id, |a| a.gen_import(&path))
            },
        ).context("creating __genImport")?).context("setting __genImport")?;

        // Build ctx object.
        let ctx_obj = rquickjs::Object::new(ctx.clone())
            .context("creating ctx object")?;

        // ctx.files() — list all files in the forest.
        let file_paths: Vec<String> = forest.files.iter()
            .map(|f| f.path.to_string_lossy().to_string())
            .collect();
        let file_paths_json = serde_json::to_string(&file_paths).unwrap_or_default();
        ctx_obj.set("files", ctx.eval::<Value, _>(
            format!("(function() {{ return {file_paths_json}; }})()")
        ).unwrap_or(Value::new_null(ctx.clone()))).context("setting ctx.files")?;

        let all_symbols = build_all_symbol_json(forest);
        let all_files_json = build_all_files_json(forest);

        // Inject the data and query functions as JS.
        let setup_code = format!(
            r#"
            (function(ctx) {{
                var __symbols = {all_symbols};
                var __files = {all_files_json};

                ctx.findFunction = function(name) {{
                    return __symbols.filter(function(s) {{
                        return s.kind === "function" && s.name === name;
                    }}).map(function(s) {{ return __makeNode(s); }});
                }};

                ctx.findType = function(name) {{
                    return __symbols.filter(function(s) {{
                        return s.kind === "type" && s.name === name;
                    }}).map(function(s) {{ return __makeNode(s); }});
                }};

                ctx.query = function(opts) {{
                    return __symbols.filter(function(s) {{
                        if (opts.kind && s.kind !== opts.kind) return false;
                        if (opts.name) {{
                            if (opts.name.includes("*")) {{
                                var regex = new RegExp("^" + opts.name.replace(/\*/g, ".*") + "$");
                                if (!regex.test(s.name)) return false;
                            }} else {{
                                if (s.name !== opts.name) return false;
                            }}
                        }}
                        if (opts.file && !s.file.includes(opts.file)) return false;
                        return true;
                    }}).map(function(s) {{ return __makeNode(s); }});
                }};

                ctx.references = function(name) {{
                    return __symbols.filter(function(s) {{
                        return s.kind === "call" && s.name === name;
                    }}).map(function(s) {{ return __makeNode(s); }});
                }};

                ctx.readFile = function(path) {{
                    var f = __files[path];
                    return f !== undefined ? f : null;
                }};

                ctx.addFile = function(path, content) {{
                    __addFile(path, content);
                }};

                ctx.editFile = function(path, startByte, endByte, replacement) {{
                    __editFile(path, startByte, endByte, replacement);
                }};

                ctx.addImport = function(filePath, importPath) {{
                    var langId = "";
                    // Determine language from file extension.
                    var ext = filePath.split(".").pop();
                    if (ext === "py") langId = "python";
                    else if (ext === "rs") langId = "rust";
                    else if (ext === "ts" || ext === "tsx") langId = "typescript";
                    else if (ext === "go") langId = "go";
                    else if (ext === "cpp" || ext === "cc" || ext === "h") langId = "cpp";
                    var code = __genImport(langId, importPath);
                    if (code) {{
                        // Insert at the beginning of the file.
                        __editFile(filePath, 0, 0, code);
                    }}
                }};

                ctx.genField = function(langId, name, type) {{
                    return __genField(langId, name, type);
                }};

                ctx.genMethod = function(langId, name, params, returnType, body) {{
                    return __genMethod(langId, name, params, returnType, body);
                }};
            }})
            "#
        );

        let setup_fn: Function = ctx.eval(setup_code.as_bytes())
            .context("compiling ctx setup")?;
        setup_fn.call::<_, ()>((ctx_obj.clone(),))
            .context("running ctx setup")?;

        // Set ctx as global.
        ctx.globals().set("ctx", ctx_obj)
            .context("setting global ctx")?;

        // Execute the user's program.
        let wrapped = format!("(function(ctx) {{ {program} }})(ctx)");
        let _: Value = ctx.eval(wrapped.as_bytes())
            .context("executing codegen program")?;

        // Collect results.
        let collector = collector.borrow();

        let mut changes = Vec::new();

        // Apply edits to existing files.
        for file in &forest.files {
            let file_key = file.path.to_string_lossy().to_string();
            if let Some(edits) = collector.edits.get(&file_key) {
                let mut sorted_edits = edits.clone();
                sorted_edits.sort_by_key(|e| e.start);

                match crate::transform::apply_edits_pub(&file.original_source, &sorted_edits) {
                    Ok(new_source) if new_source != file.original_source => {
                        changes.push(FileChange {
                            path: file.path.clone(),
                            original: file.original_source.clone(),
                            new_source,
                        });
                    }
                    _ => {}
                }
            }
        }

        // Add new files.
        for (path, content) in &collector.new_files {
            changes.push(FileChange {
                path: PathBuf::from(path),
                original: Vec::new(),
                new_source: content.as_bytes().to_vec(),
            });
        }

        Ok(changes)
    })?;

    Ok(changes)
}

/// Dispatch a code generation call to the appropriate language adapter.
fn gen_for_lang(lang_id: &str, f: impl FnOnce(&dyn crate::adapters::LanguageAdapter) -> String) -> String {
    let adapter: Option<&dyn crate::adapters::LanguageAdapter> = match lang_id {
        "python" => Some(&crate::adapters::python::PythonAdapter),
        "rust" => Some(&crate::adapters::rust::RustAdapter),
        "typescript" => Some(&crate::adapters::typescript::TypeScriptAdapter),
        "cpp" => Some(&crate::adapters::cpp::CppAdapter),
        "go" => Some(&crate::adapters::go::GoAdapter),
        _ => None,
    };
    adapter.map(f).unwrap_or_default()
}

/// Build JSON array of all symbols from the forest.
fn build_all_symbol_json(forest: &Forest) -> String {
    let mut all_symbols = Vec::new();

    for file in &forest.files {
        let symbols = index::extract_symbols(file);
        let file_path = file.path.to_string_lossy().to_string();

        for sym in &symbols {
            let end = sym.end_byte.min(file.original_source.len());
            let text = std::str::from_utf8(&file.original_source[sym.start_byte..end])
                .unwrap_or("");

            // Always use the name byte range from the symbol (captured from query).
            let mut entry = serde_json::json!({
                "name": sym.name,
                "kind": sym.kind,
                "file": file_path,
                "startLine": sym.start_line,
                "endLine": sym.end_line,
                "startByte": sym.start_byte,
                "endByte": sym.end_byte,
                "text": text,
                "nameStartByte": sym.name_start_byte,
                "nameEndByte": sym.name_end_byte,
            });

            // Try to find body and parameters from the tree.
            if let Some(info) = find_node_info(file, sym.start_byte, sym.end_byte) {
                entry["bodyStartByte"] = serde_json::json!(info.body_start);
                entry["bodyEndByte"] = serde_json::json!(info.body_end);
                if let Some(body) = info.body_text {
                    entry["body"] = serde_json::json!(body);
                }
                if let Some(params) = info.params_text {
                    entry["parameters"] = serde_json::json!(params);
                }
            }

            // For type symbols, extract fields and methods.
            if sym.kind == "type" {
                let fields = extract_fields(file, sym.start_byte, sym.end_byte);
                if !fields.is_empty() {
                    entry["_fields"] = serde_json::json!(fields);
                }
                let methods = extract_methods(file, sym.start_byte, sym.end_byte);
                if !methods.is_empty() {
                    entry["_methods"] = serde_json::json!(methods);
                }
            }

            // Store the language ID for code generation.
            entry["_langId"] = serde_json::json!(file.adapter.lsp_language_id());

            all_symbols.push(entry);
        }
    }

    serde_json::to_string(&all_symbols).unwrap_or("[]".to_string())
}

/// Build JSON object mapping file paths to their source text.
fn build_all_files_json(forest: &Forest) -> String {
    let mut files = serde_json::Map::new();
    for file in &forest.files {
        let path = file.path.to_string_lossy().to_string();
        let text = String::from_utf8_lossy(&file.original_source).to_string();
        files.insert(path, serde_json::Value::String(text));
    }
    serde_json::to_string(&serde_json::Value::Object(files)).unwrap_or("{}".to_string())
}

struct NodeInfo {
    body_start: Option<usize>,
    body_end: Option<usize>,
    body_text: Option<String>,
    params_text: Option<String>,
}

/// Extract fields from a type node using the adapter's field query.
fn extract_fields(file: &ParsedFile, node_start: usize, node_end: usize) -> Vec<serde_json::Value> {
    let field_query_str = file.adapter.field_query();
    if field_query_str.is_empty() {
        return Vec::new();
    }

    let query = match tree_sitter::Query::new(&file.adapter.language(), field_query_str) {
        Ok(q) => q,
        Err(_) => return Vec::new(),
    };

    let name_idx = query.capture_index_for_name("name");
    let type_idx = query.capture_index_for_name("type");

    let node = match file.tree.root_node().descendant_for_byte_range(node_start, node_end) {
        Some(n) => n,
        None => return Vec::new(),
    };

    let mut cursor = tree_sitter::QueryCursor::new();
    // Restrict to within the type node.
    cursor.set_byte_range(node_start..node_end);
    let mut matches = cursor.matches(&query, node, file.original_source.as_slice());

    let mut fields = Vec::new();
    while let Some(m) = matches.next() {
        let name = name_idx.and_then(|idx|
            m.captures.iter().find(|c| c.index == idx)
                .map(|c| std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()]).unwrap_or(""))
        );
        let type_text = type_idx.and_then(|idx|
            m.captures.iter().find(|c| c.index == idx)
                .map(|c| std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()]).unwrap_or(""))
        );

        if let Some(name) = name {
            fields.push(serde_json::json!({
                "name": name,
                "type": type_text.unwrap_or(""),
            }));
        }
    }

    fields
}

/// Extract methods from a type node using the adapter's method query.
fn extract_methods(file: &ParsedFile, node_start: usize, node_end: usize) -> Vec<serde_json::Value> {
    let method_query_str = file.adapter.method_query();
    if method_query_str.is_empty() {
        return Vec::new();
    }

    let query = match tree_sitter::Query::new(&file.adapter.language(), method_query_str) {
        Ok(q) => q,
        Err(_) => return Vec::new(),
    };

    let name_idx = query.capture_index_for_name("name");
    let method_idx = query.capture_index_for_name("method");

    let node = match file.tree.root_node().descendant_for_byte_range(node_start, node_end) {
        Some(n) => n,
        None => return Vec::new(),
    };

    let mut cursor = tree_sitter::QueryCursor::new();
    cursor.set_byte_range(node_start..node_end);
    let mut matches = cursor.matches(&query, node, file.original_source.as_slice());

    let mut methods = Vec::new();
    while let Some(m) = matches.next() {
        let name = name_idx.and_then(|idx|
            m.captures.iter().find(|c| c.index == idx)
                .map(|c| std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()]).unwrap_or(""))
        );
        let method_node = method_idx.and_then(|idx|
            m.captures.iter().find(|c| c.index == idx).map(|c| c.node)
        );

        if let (Some(name), Some(mnode)) = (name, method_node) {
            let text = std::str::from_utf8(&file.original_source[mnode.start_byte()..mnode.end_byte()]).unwrap_or("");
            methods.push(serde_json::json!({
                "name": name,
                "startByte": mnode.start_byte(),
                "endByte": mnode.end_byte(),
                "startLine": mnode.start_position().row + 1,
                "text": text,
            }));
        }
    }

    methods
}

/// Find detailed node info (name range, body range) for a node at given byte range.
fn find_node_info(file: &ParsedFile, start: usize, end: usize) -> Option<NodeInfo> {
    let node = file.tree.root_node().descendant_for_byte_range(start, end)?;

    let body_node = node.child_by_field_name("body");
    let params_node = node.child_by_field_name("parameters");

    // Only return if there's useful info (body or params).
    if body_node.is_none() && params_node.is_none() {
        return None;
    }

    Some(NodeInfo {
        body_start: body_node.map(|n| n.start_byte()),
        body_end: body_node.map(|n| n.end_byte()),
        body_text: body_node.map(|n|
            std::str::from_utf8(&file.original_source[n.start_byte()..n.end_byte()])
                .unwrap_or("").to_string()
        ),
        params_text: params_node.map(|n|
            std::str::from_utf8(&file.original_source[n.start_byte()..n.end_byte()])
                .unwrap_or("").to_string()
        ),
    })
}

/// Pre-flight validation: re-parse modified files and check for parse errors.
pub fn validate_changes(changes: &[FileChange]) -> Vec<String> {
    let mut errors = Vec::new();

    for change in changes {
        let ext = change.path.extension()
            .and_then(|e| e.to_str())
            .unwrap_or("");

        let adapter = match crate::adapters::adapter_for_extension(ext) {
            Some(a) => a,
            None => continue,
        };

        let mut parser = tree_sitter::Parser::new();
        if parser.set_language(&adapter.language()).is_err() {
            continue;
        }

        if let Some(tree) = parser.parse(&change.new_source, None) {
            if tree.root_node().has_error() {
                errors.push(format!(
                    "{}: parse error after transformation",
                    change.path.display()
                ));
            }
        } else {
            errors.push(format!(
                "{}: failed to parse after transformation",
                change.path.display()
            ));
        }
    }

    errors
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::python::PythonAdapter;
    use crate::adapters::LanguageAdapter;
    use std::path::PathBuf;
    use tree_sitter::Parser;

    fn make_forest(files: Vec<(&str, &str)>) -> Forest {
        let adapter: &'static dyn LanguageAdapter = &PythonAdapter;
        let mut parsed = Vec::new();

        for (path, source) in files {
            let source_bytes = source.as_bytes().to_vec();
            let mut parser = Parser::new();
            parser.set_language(&adapter.language()).unwrap();
            let tree = parser.parse(&source_bytes, None).unwrap();
            parsed.push(ParsedFile {
                path: PathBuf::from(path),
                original_source: source_bytes,
                tree,
                adapter,
            });
        }

        Forest { files: parsed }
    }

    #[test]
    fn codegen_rename_across_files() {
        let forest = make_forest(vec![
            ("a.py", "def foo():\n    pass\n"),
            ("b.py", "foo()\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            var fns = ctx.findFunction("foo");
            for (var i = 0; i < fns.length; i++) {
                fns[i].replaceName("bar");
            }
            var refs = ctx.references("foo");
            for (var i = 0; i < refs.length; i++) {
                refs[i].replaceName("bar");
            }
        "#).unwrap();

        assert_eq!(changes.len(), 2);
        let a_change = changes.iter().find(|c| c.path == PathBuf::from("a.py")).unwrap();
        assert_eq!(String::from_utf8_lossy(&a_change.new_source), "def bar():\n    pass\n");

        let b_change = changes.iter().find(|c| c.path == PathBuf::from("b.py")).unwrap();
        assert_eq!(String::from_utf8_lossy(&b_change.new_source), "bar()\n");
    }

    #[test]
    fn codegen_query_with_glob() {
        let forest = make_forest(vec![
            ("test.py", "def test_a():\n    pass\n\ndef test_b():\n    pass\n\ndef helper():\n    pass\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            var tests = ctx.query({kind: "function", name: "test_*"});
            for (var i = 0; i < tests.length; i++) {
                tests[i].remove();
            }
        "#).unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(result.contains("helper"), "helper should remain: {result}");
        assert!(!result.contains("test_a"), "test_a should be removed: {result}");
    }

    #[test]
    fn codegen_add_new_file() {
        let forest = make_forest(vec![
            ("main.py", "pass\n"),
        ]);

        let changes = run_codegen(&forest, r##"
            ctx.addFile("new_module.py", "# Generated\ndef generated():\n    pass\n");
        "##).unwrap();

        let new_file = changes.iter().find(|c| c.path == PathBuf::from("new_module.py"));
        assert!(new_file.is_some(), "new file should be created");
        assert!(String::from_utf8_lossy(&new_file.unwrap().new_source).contains("Generated"));
    }

    #[test]
    fn codegen_read_file() {
        let forest = make_forest(vec![
            ("config.py", "DB_HOST = 'localhost'\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            var content = ctx.readFile("config.py");
            if (content && content.includes("localhost")) {
                ctx.addFile("warning.txt", "Config uses localhost!\n");
            }
        "#).unwrap();

        let warning = changes.iter().find(|c| c.path == PathBuf::from("warning.txt"));
        assert!(warning.is_some(), "warning file should be created");
    }

    fn make_rust_forest(files: Vec<(&str, &str)>) -> Forest {
        let adapter: &'static dyn crate::adapters::LanguageAdapter = &crate::adapters::rust::RustAdapter;
        let mut parsed = Vec::new();
        for (path, source) in files {
            let source_bytes = source.as_bytes().to_vec();
            let mut parser = tree_sitter::Parser::new();
            parser.set_language(&adapter.language()).unwrap();
            let tree = parser.parse(&source_bytes, None).unwrap();
            parsed.push(ParsedFile {
                path: PathBuf::from(path),
                original_source: source_bytes,
                tree,
                adapter,
            });
        }
        Forest { files: parsed }
    }

    #[test]
    fn codegen_fields_and_methods() {
        let forest = make_rust_forest(vec![
            ("lib.rs", "struct User {\n    name: String,\n    age: u32,\n}\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                var user = types[0];
                var fields = user.fields();
                // Add a comment listing all fields
                var fieldNames = fields.map(function(f) { return f.name; }).join(", ");
                user.insertBefore("// Fields: " + fieldNames);
            }
        "#).unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(result.contains("// Fields: name, age"), "should list fields: {result}");
    }

    #[test]
    fn codegen_add_field() {
        let forest = make_rust_forest(vec![
            ("lib.rs", "struct User {\n    name: String,\n}\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                types[0].addField("email", "String");
            }
        "#).unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(result.contains("email: String"), "should contain new field: {result}");
    }

    #[test]
    fn codegen_gen_import() {
        let forest = make_rust_forest(vec![
            ("lib.rs", "fn main() {}\n"),
        ]);

        let changes = run_codegen(&forest, r#"
            ctx.addImport("lib.rs", "std::collections::HashMap");
        "#).unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(result.contains("use std::collections::HashMap;"), "should contain import: {result}");
    }

    #[test]
    fn validate_catches_parse_errors() {
        let changes = vec![FileChange {
            path: PathBuf::from("broken.py"),
            original: b"x = 1\n".to_vec(),
            new_source: b"def (\n".to_vec(), // invalid syntax
        }];

        let errors = validate_changes(&changes);
        assert!(!errors.is_empty(), "should detect parse error");
        assert!(errors[0].contains("broken.py"));
    }
}
