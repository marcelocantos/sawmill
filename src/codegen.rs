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
    // addField(name, type) or addField(name, type, doc)
    n.addField = function(name, type, doc) {
        var code = __genField(n._langId || "", name, type, doc || undefined);
        if (n.bodyEndByte !== null && n.bodyEndByte !== undefined) {
            __editFile(n.file, n.bodyEndByte, n.bodyEndByte, code);
        } else if (n.endByte) {
            __editFile(n.file, n.endByte - 1, n.endByte - 1, code);
        }
        return n;
    };
    // addMethod(name, params, returnType, body) or addMethod(name, params, returnType, body, doc)
    n.addMethod = function(name, params, returnType, body, doc) {
        var code = __genMethod(n._langId || "", name, params, returnType, body, doc || undefined);
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

/// Execute a codegen program against the forest (without LSP).
pub fn run_codegen(forest: &Forest, program: &str) -> Result<Vec<FileChange>> {
    run_codegen_inner(forest, program, None)
}

/// Execute a codegen program with LSP access.
pub fn run_codegen_with_lsp(
    forest: &Forest,
    program: &str,
    lsp: &mut crate::lsp::LspManager,
) -> Result<Vec<FileChange>> {
    run_codegen_inner(forest, program, Some(lsp))
}

/// Wraps a raw pointer to `LspManager` so it can be shared across JS
/// callbacks within a single-threaded QuickJS execution.
///
/// # Why a raw pointer?
///
/// `run_codegen_inner` receives `Option<&mut LspManager>`. The `&mut`
/// borrow needs to be stored in closures that are registered with the
/// QuickJS runtime, which requires `'static`-like lifetimes that the
/// borrow checker cannot verify. Converting to a raw pointer (via
/// `as *mut _`) sidesteps the lifetime check while preserving the
/// single-ownership invariant.
///
/// # Safety contract
///
/// 1. The pointer is derived from a `&mut LspManager` that outlives the
///    `LspProxy` (enforced by lexical scoping in `run_codegen_inner`).
/// 2. JS execution is single-threaded; no two callbacks run concurrently,
///    so only one call to `get()` is active at a time.
/// 3. `LspProxy` is not `Send` — it must stay on the thread that created it.
struct LspProxy {
    ptr: *mut crate::lsp::LspManager,
}

impl LspProxy {
    /// Return an exclusive reference to the wrapped `LspManager`.
    ///
    /// # Safety
    ///
    /// Safe to call when the contract documented on `LspProxy` holds:
    /// the pointer is valid, the original `&mut` is still live, and no
    /// other reference obtained through this method is alive.
    fn get(&self) -> &mut crate::lsp::LspManager {
        // SAFETY: see LspProxy safety contract.
        unsafe { &mut *self.ptr }
    }

    fn hover(&self, file: &str, lang_id: &str, line: u32, col: u32) -> Option<String> {
        let path = std::path::PathBuf::from(file);
        self.get().hover(&path, lang_id, line, col).ok().flatten()
    }

    fn definition(&self, file: &str, lang_id: &str, line: u32, col: u32) -> Vec<serde_json::Value> {
        let path = std::path::PathBuf::from(file);
        match self.get().definition(&path, lang_id, line, col) {
            Ok(locs) => locs
                .iter()
                .map(|l| {
                    serde_json::json!({
                        "file": l.path.to_string_lossy(),
                        "line": l.line + 1,
                        "column": l.column + 1,
                    })
                })
                .collect(),
            Err(_) => Vec::new(),
        }
    }

    fn references(&self, file: &str, lang_id: &str, line: u32, col: u32) -> Vec<serde_json::Value> {
        let path = std::path::PathBuf::from(file);
        match self.get().references(&path, lang_id, line, col) {
            Ok(locs) => locs
                .iter()
                .map(|l| {
                    serde_json::json!({
                        "file": l.path.to_string_lossy(),
                        "line": l.line + 1,
                        "column": l.column + 1,
                    })
                })
                .collect(),
            Err(_) => Vec::new(),
        }
    }

    fn diagnostics_for(&self, file: &str, lang_id: &str, text: &str) -> Vec<serde_json::Value> {
        let path = std::path::PathBuf::from(file);
        match self.get().get_diagnostics(&path, lang_id, text) {
            Ok(diags) => diags.iter().map(|d| serde_json::json!(d)).collect(),
            Err(_) => Vec::new(),
        }
    }
}

fn run_codegen_inner(
    forest: &Forest,
    program: &str,
    lsp: Option<&mut crate::lsp::LspManager>,
) -> Result<Vec<FileChange>> {
    let runtime = JsRuntime::new().context("creating QuickJS runtime")?;
    runtime.set_memory_limit(2 * 1024 * 1024 * 1024);
    runtime.set_max_stack_size(8 * 1024 * 1024);

    let context = JsContext::full(&runtime).context("creating QuickJS context")?;

    let collector = Rc::new(RefCell::new(EditCollector::new()));

    // Build file→langId map for LSP dispatch.
    let lang_map: HashMap<String, String> = forest
        .files
        .iter()
        .map(|f| {
            (
                f.path.to_string_lossy().to_string(),
                f.adapter.lsp_language_id().to_string(),
            )
        })
        .collect();

    // Create LSP proxy if available.
    let lsp_proxy: Option<Rc<LspProxy>> = lsp.map(|l| Rc::new(LspProxy { ptr: l as *mut _ }));

    let changes = context.with(|ctx| -> Result<Vec<FileChange>> {
        // Inject helpers.
        let _: Value = ctx
            .eval(CODEGEN_HELPERS.as_bytes())
            .context("injecting codegen helpers")?;

        // Register __editFile callback.
        let collector_ref = collector.clone();
        ctx.globals()
            .set(
                "__editFile",
                Function::new(
                    ctx.clone(),
                    move |file: String, start: usize, end: usize, replacement: String| {
                        collector_ref.borrow_mut().add_edit(
                            &file,
                            Edit {
                                start,
                                end,
                                replacement,
                            },
                        );
                    },
                )
                .context("creating __editFile")?,
            )
            .context("setting __editFile")?;

        // Register __addFile callback.
        let collector_ref = collector.clone();
        ctx.globals()
            .set(
                "__addFile",
                Function::new(ctx.clone(), move |path: String, content: String| {
                    collector_ref.borrow_mut().add_new_file(&path, &content);
                })
                .context("creating __addFile")?,
            )
            .context("setting __addFile")?;

        // Register code generation callbacks.
        // These dispatch to the appropriate language adapter.
        ctx.globals()
            .set(
                "__genField",
                Function::new(
                    ctx.clone(),
                    |lang_id: String,
                     name: String,
                     type_name: String,
                     doc: Option<String>|
                     -> String {
                        let doc = doc.unwrap_or_default();
                        gen_for_lang(&lang_id, |a| a.gen_field_with_doc(&name, &type_name, &doc))
                    },
                )
                .context("creating __genField")?,
            )
            .context("setting __genField")?;

        ctx.globals()
            .set(
                "__genMethod",
                Function::new(
                    ctx.clone(),
                    |lang_id: String,
                     name: String,
                     params: String,
                     return_type: String,
                     body: String,
                     doc: Option<String>|
                     -> String {
                        let doc = doc.unwrap_or_default();
                        gen_for_lang(&lang_id, |a| {
                            a.gen_method_with_doc(&name, &params, &return_type, &body, &doc)
                        })
                    },
                )
                .context("creating __genMethod")?,
            )
            .context("setting __genMethod")?;

        ctx.globals()
            .set(
                "__genImport",
                Function::new(ctx.clone(), |lang_id: String, path: String| -> String {
                    gen_for_lang(&lang_id, |a| a.gen_import(&path))
                })
                .context("creating __genImport")?,
            )
            .context("setting __genImport")?;

        // Register LSP callbacks if available.
        if let Some(proxy) = &lsp_proxy {
            let p = proxy.clone();
            let lm = lang_map.clone();
            ctx.globals()
                .set(
                    "__lspHover",
                    Function::new(
                        ctx.clone(),
                        move |file: String, line: u32, col: u32| -> Option<String> {
                            let lang_id = lm.get(&file).map(|s| s.as_str()).unwrap_or("");
                            p.hover(
                                &file,
                                lang_id,
                                line.saturating_sub(1),
                                col.saturating_sub(1),
                            )
                        },
                    )
                    .context("creating __lspHover")?,
                )
                .context("setting __lspHover")?;

            let p = proxy.clone();
            let lm = lang_map.clone();
            ctx.globals()
                .set(
                    "__lspDefinition",
                    Function::new(
                        ctx.clone(),
                        move |file: String, line: u32, col: u32| -> String {
                            let lang_id = lm.get(&file).map(|s| s.as_str()).unwrap_or("");
                            let locs = p.definition(
                                &file,
                                lang_id,
                                line.saturating_sub(1),
                                col.saturating_sub(1),
                            );
                            serde_json::to_string(&locs).unwrap_or("[]".to_string())
                        },
                    )
                    .context("creating __lspDefinition")?,
                )
                .context("setting __lspDefinition")?;

            let p = proxy.clone();
            let lm = lang_map.clone();
            ctx.globals()
                .set(
                    "__lspReferences",
                    Function::new(
                        ctx.clone(),
                        move |file: String, line: u32, col: u32| -> String {
                            let lang_id = lm.get(&file).map(|s| s.as_str()).unwrap_or("");
                            let locs = p.references(
                                &file,
                                lang_id,
                                line.saturating_sub(1),
                                col.saturating_sub(1),
                            );
                            serde_json::to_string(&locs).unwrap_or("[]".to_string())
                        },
                    )
                    .context("creating __lspReferences")?,
                )
                .context("setting __lspReferences")?;

            let p = proxy.clone();
            let lm = lang_map.clone();
            ctx.globals()
                .set(
                    "__lspDiagnostics",
                    Function::new(ctx.clone(), move |file: String, text: String| -> String {
                        let lang_id = lm.get(&file).map(|s| s.as_str()).unwrap_or("");
                        let diags = p.diagnostics_for(&file, lang_id, &text);
                        serde_json::to_string(&diags).unwrap_or("[]".to_string())
                    })
                    .context("creating __lspDiagnostics")?,
                )
                .context("setting __lspDiagnostics")?;
        }

        // Build ctx object.
        let ctx_obj = rquickjs::Object::new(ctx.clone()).context("creating ctx object")?;

        // ctx.files() — list all files in the forest.
        let file_paths: Vec<String> = forest
            .files
            .iter()
            .map(|f| f.path.to_string_lossy().to_string())
            .collect();
        let file_paths_json = serde_json::to_string(&file_paths).unwrap_or_default();
        ctx_obj
            .set(
                "files",
                ctx.eval::<Value, _>(format!("(function() {{ return {file_paths_json}; }})()"))
                    .unwrap_or(Value::new_null(ctx.clone())),
            )
            .context("setting ctx.files")?;

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

                // LSP methods (available when LSP servers are connected).
                ctx.typeOf = function(file, line, col) {{
                    if (typeof __lspHover === "undefined") return null;
                    return __lspHover(file, line, col);
                }};

                ctx.definition = function(file, line, col) {{
                    if (typeof __lspDefinition === "undefined") return [];
                    return JSON.parse(__lspDefinition(file, line, col));
                }};

                ctx.lspReferences = function(file, line, col) {{
                    if (typeof __lspReferences === "undefined") return [];
                    return JSON.parse(__lspReferences(file, line, col));
                }};

                ctx.diagnostics = function(file, text) {{
                    if (typeof __lspDiagnostics === "undefined") return [];
                    return JSON.parse(__lspDiagnostics(file, text || ""));
                }};

                ctx.hasLsp = typeof __lspHover !== "undefined";
            }})
            "#
        );

        let setup_fn: Function = ctx
            .eval(setup_code.as_bytes())
            .context("compiling ctx setup")?;
        setup_fn
            .call::<_, ()>((ctx_obj.clone(),))
            .context("running ctx setup")?;

        // Set ctx as global.
        ctx.globals()
            .set("ctx", ctx_obj)
            .context("setting global ctx")?;

        // Execute the user's program.
        let wrapped = format!("(function(ctx) {{ {program} }})(ctx)");
        let _: Value = ctx
            .eval(wrapped.as_bytes())
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

/// Run a convention check program against the forest.
/// The program should return an array of violation strings, or an empty array.
pub fn run_convention_check(forest: &Forest, check_program: &str) -> Result<Vec<String>> {
    let runtime = JsRuntime::new().context("creating QuickJS runtime")?;
    runtime.set_memory_limit(2 * 1024 * 1024 * 1024);
    runtime.set_max_stack_size(8 * 1024 * 1024);

    let context = JsContext::full(&runtime).context("creating QuickJS context")?;

    context.with(|ctx| -> Result<Vec<String>> {
        let _: Value = ctx
            .eval(CODEGEN_HELPERS.as_bytes())
            .context("injecting helpers")?;

        // We don't need edit/addFile callbacks for checks — just dummy them.
        ctx.globals()
            .set(
                "__editFile",
                Function::new(ctx.clone(), |_: String, _: usize, _: usize, _: String| {})
                    .context("creating __editFile")?,
            )
            .context("setting __editFile")?;
        ctx.globals()
            .set(
                "__addFile",
                Function::new(ctx.clone(), |_: String, _: String| {})
                    .context("creating __addFile")?,
            )
            .context("setting __addFile")?;
        ctx.globals()
            .set(
                "__genField",
                Function::new(
                    ctx.clone(),
                    |_: String, _: String, _: String, _: Option<String>| -> String {
                        String::new()
                    },
                )
                .context("creating __genField")?,
            )
            .context("setting __genField")?;
        ctx.globals()
            .set(
                "__genMethod",
                Function::new(
                    ctx.clone(),
                    |_: String,
                     _: String,
                     _: String,
                     _: String,
                     _: String,
                     _: Option<String>|
                     -> String { String::new() },
                )
                .context("creating __genMethod")?,
            )
            .context("setting __genMethod")?;
        ctx.globals()
            .set(
                "__genImport",
                Function::new(ctx.clone(), |_: String, _: String| -> String {
                    String::new()
                })
                .context("creating __genImport")?,
            )
            .context("setting __genImport")?;

        let all_symbols = build_all_symbol_json(forest);
        let all_files_json = build_all_files_json(forest);

        // Build ctx with query capabilities (same as codegen).
        let ctx_obj = rquickjs::Object::new(ctx.clone()).context("creating ctx object")?;

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
                ctx.readFile = function(path) {{
                    return __files[path] || null;
                }};
            }})
            "#
        );

        let setup_fn: Function = ctx
            .eval(setup_code.as_bytes())
            .context("compiling check setup")?;
        setup_fn
            .call::<_, ()>((ctx_obj.clone(),))
            .context("running check setup")?;

        ctx.globals()
            .set("ctx", ctx_obj)
            .context("setting global ctx")?;

        // Execute the check program. It should return an array of strings.
        let wrapped = format!("(function(ctx) {{ {check_program} }})(ctx)");
        let result: Value = ctx
            .eval(wrapped.as_bytes())
            .context("executing convention check")?;

        // Parse the result as an array of strings.
        if result.is_null() || result.is_undefined() {
            return Ok(Vec::new());
        }

        if let Some(arr) = result.as_array() {
            let mut violations = Vec::new();
            for i in 0..arr.len() {
                if let Ok(s) = arr.get::<String>(i) {
                    violations.push(s);
                }
            }
            return Ok(violations);
        }

        // Single string.
        if let Some(s) = result.as_string() {
            let text = s.to_string().unwrap_or_default();
            if text.is_empty() {
                return Ok(Vec::new());
            }
            return Ok(vec![text]);
        }

        Ok(Vec::new())
    })
}

/// Dispatch a code generation call to the appropriate language adapter.
fn gen_for_lang(
    lang_id: &str,
    f: impl FnOnce(&dyn crate::adapters::LanguageAdapter) -> String,
) -> String {
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
            let text =
                std::str::from_utf8(&file.original_source[sym.start_byte..end]).unwrap_or("");

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

/// Extract a doc comment from the preceding sibling of a node.
/// Returns the comment text with prefixes stripped, or None.
fn extract_preceding_comment(node: tree_sitter::Node, source: &[u8]) -> Option<String> {
    let mut prev = node.prev_sibling()?;

    // Collect consecutive comment lines going backwards.
    let mut comment_lines = Vec::new();
    loop {
        let kind = prev.kind();
        if kind == "comment" || kind == "line_comment" || kind == "block_comment" {
            let text =
                std::str::from_utf8(&source[prev.start_byte()..prev.end_byte()]).unwrap_or("");
            // Strip common prefixes.
            let stripped = text
                .trim_start_matches("///")
                .trim_start_matches("//!")
                .trim_start_matches("//")
                .trim_start_matches('#')
                .trim_start();
            comment_lines.push(stripped.to_string());
            match prev.prev_sibling() {
                Some(p) => prev = p,
                None => break,
            }
        } else {
            break;
        }
    }

    if comment_lines.is_empty() {
        return None;
    }

    comment_lines.reverse();
    Some(comment_lines.join("\n"))
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

    let node = match file
        .tree
        .root_node()
        .descendant_for_byte_range(node_start, node_end)
    {
        Some(n) => n,
        None => return Vec::new(),
    };

    let mut cursor = tree_sitter::QueryCursor::new();
    // Restrict to within the type node.
    cursor.set_byte_range(node_start..node_end);
    let mut matches = cursor.matches(&query, node, file.original_source.as_slice());

    let mut fields = Vec::new();
    while let Some(m) = matches.next() {
        let name = name_idx.and_then(|idx| {
            m.captures.iter().find(|c| c.index == idx).map(|c| {
                std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()])
                    .unwrap_or("")
            })
        });
        let type_text = type_idx.and_then(|idx| {
            m.captures.iter().find(|c| c.index == idx).map(|c| {
                std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()])
                    .unwrap_or("")
            })
        });

        if let Some(name) = name {
            // Look for a doc comment on the preceding sibling.
            let field_idx = query.capture_index_for_name("field");
            let field_node = field_idx
                .and_then(|idx| m.captures.iter().find(|c| c.index == idx).map(|c| c.node));
            let doc = field_node.and_then(|n| extract_preceding_comment(n, &file.original_source));

            let mut entry = serde_json::json!({
                "name": name,
                "type": type_text.unwrap_or(""),
            });
            if let Some(doc) = doc {
                entry["doc"] = serde_json::json!(doc);
            }
            fields.push(entry);
        }
    }

    fields
}

/// Extract methods from a type node using the adapter's method query.
fn extract_methods(
    file: &ParsedFile,
    node_start: usize,
    node_end: usize,
) -> Vec<serde_json::Value> {
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

    let node = match file
        .tree
        .root_node()
        .descendant_for_byte_range(node_start, node_end)
    {
        Some(n) => n,
        None => return Vec::new(),
    };

    let mut cursor = tree_sitter::QueryCursor::new();
    cursor.set_byte_range(node_start..node_end);
    let mut matches = cursor.matches(&query, node, file.original_source.as_slice());

    let mut methods = Vec::new();
    while let Some(m) = matches.next() {
        let name = name_idx.and_then(|idx| {
            m.captures.iter().find(|c| c.index == idx).map(|c| {
                std::str::from_utf8(&file.original_source[c.node.start_byte()..c.node.end_byte()])
                    .unwrap_or("")
            })
        });
        let method_node =
            method_idx.and_then(|idx| m.captures.iter().find(|c| c.index == idx).map(|c| c.node));

        if let (Some(name), Some(mnode)) = (name, method_node) {
            let text =
                std::str::from_utf8(&file.original_source[mnode.start_byte()..mnode.end_byte()])
                    .unwrap_or("");
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
    let node = file
        .tree
        .root_node()
        .descendant_for_byte_range(start, end)?;

    let body_node = node.child_by_field_name("body");
    let params_node = node.child_by_field_name("parameters");

    // Only return if there's useful info (body or params).
    if body_node.is_none() && params_node.is_none() {
        return None;
    }

    Some(NodeInfo {
        body_start: body_node.map(|n| n.start_byte()),
        body_end: body_node.map(|n| n.end_byte()),
        body_text: body_node.map(|n| {
            std::str::from_utf8(&file.original_source[n.start_byte()..n.end_byte()])
                .unwrap_or("")
                .to_string()
        }),
        params_text: params_node.map(|n| {
            std::str::from_utf8(&file.original_source[n.start_byte()..n.end_byte()])
                .unwrap_or("")
                .to_string()
        }),
    })
}

/// Pre-flight validation: re-parse modified files and check for parse errors.
/// Also includes structural checks via `structural_checks`.
pub fn validate_changes(changes: &[FileChange]) -> Vec<String> {
    let mut errors = Vec::new();

    for change in changes {
        let ext = change
            .path
            .extension()
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

/// Structural pre-flight checks: detect removed symbols still referenced,
/// using the symbol index and Tree-sitter (not LSP).
///
/// Builds a "post-change" symbol index by re-parsing changed files and
/// combining with unchanged files' existing symbols from the forest.
/// Only reports issues for symbols that *existed* before the changes,
/// avoiding false positives for built-ins and external functions.
pub fn structural_checks(forest: &Forest, changes: &[FileChange]) -> Vec<String> {
    use std::collections::{HashMap, HashSet};

    // Build a map of changed file paths for quick lookup.
    let changed_paths: HashMap<String, &FileChange> = changes
        .iter()
        .map(|c| (c.path.to_string_lossy().to_string(), c))
        .collect();

    // Pre-change: collect function/type definition names from the forest.
    let mut pre_functions: HashSet<String> = HashSet::new();
    for file in &forest.files {
        for sym in index::extract_symbols(file) {
            if sym.kind == "function" || sym.kind == "type" {
                pre_functions.insert(sym.name.clone());
            }
        }
    }

    // Helper: parse new_source from a FileChange into a temporary ParsedFile.
    let parse_change = |change: &FileChange| -> Option<ParsedFile> {
        let ext = change
            .path
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("");
        let adapter = crate::adapters::adapter_for_extension(ext)?;
        let mut parser = tree_sitter::Parser::new();
        parser.set_language(&adapter.language()).ok()?;
        let tree = parser.parse(&change.new_source, None)?;
        Some(ParsedFile {
            path: change.path.clone(),
            original_source: change.new_source.clone(),
            tree,
            adapter,
        })
    };

    // Post-change: build symbol lists by combining:
    // - Re-parsed changed files (using new_source)
    // - Unchanged forest files (using their existing parsed trees)
    let mut post_functions: HashSet<String> = HashSet::new();
    let mut post_calls: Vec<index::Symbol> = Vec::new();

    for file in &forest.files {
        let file_key = file.path.to_string_lossy().to_string();
        let syms = if let Some(change) = changed_paths.get(&file_key) {
            if let Some(tmp) = parse_change(change) {
                index::extract_symbols(&tmp)
            } else {
                continue;
            }
        } else {
            index::extract_symbols(file)
        };
        for sym in syms {
            match sym.kind.as_str() {
                "function" | "type" => {
                    post_functions.insert(sym.name.clone());
                }
                "call" => post_calls.push(sym),
                _ => {}
            }
        }
    }

    // Also handle brand-new files (not yet in the forest) from the changes list.
    for change in changes {
        let file_key = change.path.to_string_lossy().to_string();
        if forest
            .files
            .iter()
            .any(|f| f.path.to_string_lossy() == file_key)
        {
            continue; // already handled above
        }
        if let Some(tmp) = parse_change(change) {
            for sym in index::extract_symbols(&tmp) {
                match sym.kind.as_str() {
                    "function" | "type" => {
                        post_functions.insert(sym.name.clone());
                    }
                    "call" => post_calls.push(sym),
                    _ => {}
                }
            }
        }
    }

    // Symbols removed by the changes: existed pre-change, missing post-change.
    let removed: HashSet<&String> = pre_functions
        .iter()
        .filter(|name| !post_functions.contains(*name))
        .collect();

    let mut warnings = Vec::new();

    for call in &post_calls {
        if removed.contains(&call.name) {
            warnings.push(format!(
                "Removed symbol `{}` still referenced at {}:{}",
                call.name, call.file_path, call.start_line,
            ));
        }
    }

    warnings
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::LanguageAdapter;
    use crate::adapters::python::PythonAdapter;
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

        let changes = run_codegen(
            &forest,
            r#"
            var fns = ctx.findFunction("foo");
            for (var i = 0; i < fns.length; i++) {
                fns[i].replaceName("bar");
            }
            var refs = ctx.references("foo");
            for (var i = 0; i < refs.length; i++) {
                refs[i].replaceName("bar");
            }
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 2);
        let a_change = changes
            .iter()
            .find(|c| c.path == PathBuf::from("a.py"))
            .unwrap();
        assert_eq!(
            String::from_utf8_lossy(&a_change.new_source),
            "def bar():\n    pass\n"
        );

        let b_change = changes
            .iter()
            .find(|c| c.path == PathBuf::from("b.py"))
            .unwrap();
        assert_eq!(String::from_utf8_lossy(&b_change.new_source), "bar()\n");
    }

    #[test]
    fn codegen_query_with_glob() {
        let forest = make_forest(vec![(
            "test.py",
            "def test_a():\n    pass\n\ndef test_b():\n    pass\n\ndef helper():\n    pass\n",
        )]);

        let changes = run_codegen(
            &forest,
            r#"
            var tests = ctx.query({kind: "function", name: "test_*"});
            for (var i = 0; i < tests.length; i++) {
                tests[i].remove();
            }
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(result.contains("helper"), "helper should remain: {result}");
        assert!(
            !result.contains("test_a"),
            "test_a should be removed: {result}"
        );
    }

    #[test]
    fn codegen_add_new_file() {
        let forest = make_forest(vec![("main.py", "pass\n")]);

        let changes = run_codegen(
            &forest,
            r##"
            ctx.addFile("new_module.py", "# Generated\ndef generated():\n    pass\n");
        "##,
        )
        .unwrap();

        let new_file = changes
            .iter()
            .find(|c| c.path == PathBuf::from("new_module.py"));
        assert!(new_file.is_some(), "new file should be created");
        assert!(String::from_utf8_lossy(&new_file.unwrap().new_source).contains("Generated"));
    }

    #[test]
    fn codegen_read_file() {
        let forest = make_forest(vec![("config.py", "DB_HOST = 'localhost'\n")]);

        let changes = run_codegen(
            &forest,
            r#"
            var content = ctx.readFile("config.py");
            if (content && content.includes("localhost")) {
                ctx.addFile("warning.txt", "Config uses localhost!\n");
            }
        "#,
        )
        .unwrap();

        let warning = changes
            .iter()
            .find(|c| c.path == PathBuf::from("warning.txt"));
        assert!(warning.is_some(), "warning file should be created");
    }

    fn make_rust_forest(files: Vec<(&str, &str)>) -> Forest {
        let adapter: &'static dyn crate::adapters::LanguageAdapter =
            &crate::adapters::rust::RustAdapter;
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
        let forest = make_rust_forest(vec![(
            "lib.rs",
            "struct User {\n    name: String,\n    age: u32,\n}\n",
        )]);

        let changes = run_codegen(
            &forest,
            r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                var user = types[0];
                var fields = user.fields();
                // Add a comment listing all fields
                var fieldNames = fields.map(function(f) { return f.name; }).join(", ");
                user.insertBefore("// Fields: " + fieldNames);
            }
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(
            result.contains("// Fields: name, age"),
            "should list fields: {result}"
        );
    }

    #[test]
    fn codegen_add_field() {
        let forest = make_rust_forest(vec![("lib.rs", "struct User {\n    name: String,\n}\n")]);

        let changes = run_codegen(
            &forest,
            r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                types[0].addField("email", "String");
            }
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(
            result.contains("email: String"),
            "should contain new field: {result}"
        );
    }

    #[test]
    fn codegen_gen_import() {
        let forest = make_rust_forest(vec![("lib.rs", "fn main() {}\n")]);

        let changes = run_codegen(
            &forest,
            r#"
            ctx.addImport("lib.rs", "std::collections::HashMap");
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(
            result.contains("use std::collections::HashMap;"),
            "should contain import: {result}"
        );
    }

    #[test]
    fn codegen_add_field_with_doc() {
        let forest = make_rust_forest(vec![(
            "lib.rs",
            "struct User {\n    /// The user's name.\n    name: String,\n}\n",
        )]);

        let changes = run_codegen(
            &forest,
            r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                types[0].addField("email", "String", "The user's email address.");
            }
        "#,
        )
        .unwrap();

        assert_eq!(changes.len(), 1);
        let result = String::from_utf8_lossy(&changes[0].new_source);
        assert!(
            result.contains("/// The user's email address."),
            "should contain doc comment: {result}"
        );
        assert!(
            result.contains("email: String"),
            "should contain field: {result}"
        );
    }

    #[test]
    fn codegen_read_field_docs() {
        let forest = make_rust_forest(vec![(
            "lib.rs",
            "struct User {\n    /// The user's name.\n    name: String,\n    age: u32,\n}\n",
        )]);

        let changes = run_codegen(
            &forest,
            r#"
            var types = ctx.findType("User");
            if (types.length > 0) {
                var fields = types[0].fields();
                var docs = [];
                for (var i = 0; i < fields.length; i++) {
                    docs.push(fields[i].name + ":" + (fields[i].doc || "none"));
                }
                ctx.addFile("fields.txt", docs.join("\n") + "\n");
            }
        "#,
        )
        .unwrap();

        let fields_file = changes
            .iter()
            .find(|c| c.path == PathBuf::from("fields.txt"));
        assert!(fields_file.is_some(), "fields.txt should be created");
        let content = String::from_utf8_lossy(&fields_file.unwrap().new_source);
        assert!(
            content.contains("name:The user's name."),
            "should have name doc: {content}"
        );
        assert!(
            content.contains("age:none"),
            "age should have no doc: {content}"
        );
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

    #[test]
    fn structural_check_removed_function() {
        // Forest has two files: one defines `compute`, one calls it.
        let forest = make_forest(vec![
            ("lib.py", "def compute():\n    pass\n"),
            ("main.py", "compute()\n"),
        ]);

        // Change removes the definition of `compute` entirely.
        let changes = vec![FileChange {
            path: PathBuf::from("lib.py"),
            original: b"def compute():\n    pass\n".to_vec(),
            new_source: b"# compute was removed\n".to_vec(),
        }];

        let warnings = structural_checks(&forest, &changes);
        assert!(
            !warnings.is_empty(),
            "should detect removed symbol still referenced: {warnings:?}"
        );
        assert!(
            warnings.iter().any(|w| w.contains("compute")),
            "warning should mention `compute`: {warnings:?}"
        );
        assert!(
            warnings.iter().any(|w| w.contains("main.py")),
            "warning should mention the call site file: {warnings:?}"
        );
    }

    #[test]
    fn structural_check_renamed_function_missing_call_update() {
        // Forest has two files: one defines `foo`, one calls `foo`.
        let forest = make_forest(vec![
            ("defs.py", "def foo():\n    pass\n"),
            ("caller.py", "foo()\n"),
        ]);

        // Change renames the definition from `foo` to `bar` but leaves the call site unchanged.
        let changes = vec![FileChange {
            path: PathBuf::from("defs.py"),
            original: b"def foo():\n    pass\n".to_vec(),
            new_source: b"def bar():\n    pass\n".to_vec(),
        }];

        let warnings = structural_checks(&forest, &changes);
        assert!(
            !warnings.is_empty(),
            "should detect that `foo` was removed but still called: {warnings:?}"
        );
        assert!(
            warnings.iter().any(|w| w.contains("foo")),
            "warning should mention `foo`: {warnings:?}"
        );
        assert!(
            warnings.iter().any(|w| w.contains("caller.py")),
            "warning should mention the caller file: {warnings:?}"
        );
    }
}
