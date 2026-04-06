// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashMap;
use std::path::Path;

use streaming_iterator::StreamingIterator;
use tree_sitter::{Parser, Query, QueryCursor};

use crate::forest::{FileChange, Forest, ParsedFile};
use crate::transform;

// ---------------------------------------------------------------------------
// Helpers for transform_batch
// ---------------------------------------------------------------------------

/// Apply a single transform step (rename or match/act) against the current
/// state of all accumulated files.
///
/// `accumulated` maps file path → (original bytes, current bytes). After each
/// step we update the "current bytes" for changed files, keeping the original
/// bytes (for the final diff) fixed at what they were before this entire batch.
pub(super) fn apply_one_batch_step(
    root_path: &Path,
    transform_val: &serde_json::Value,
    format: bool,
    accumulated: &mut HashMap<std::path::PathBuf, (Vec<u8>, Vec<u8>)>,
) -> anyhow::Result<()> {
    // Determine whether this is a rename or a match/act transform.
    if let Some(rename_obj) = transform_val.get("rename") {
        // Rename step.
        let from = rename_obj
            .get("from")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("rename step missing 'from'"))?
            .to_string();
        let to = rename_obj
            .get("to")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("rename step missing 'to'"))?
            .to_string();

        // Build a forest from current state.
        let forest = build_current_forest(root_path, accumulated)?;
        let step_changes = forest.rename(&from, &to, format)?;
        merge_changes(accumulated, step_changes);
    } else {
        // Match/act transform step. Parse it as TransformParams fields.
        let obj = transform_val
            .as_object()
            .ok_or_else(|| anyhow::anyhow!("transform element must be an object"))?;

        let raw_query = obj
            .get("raw_query")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let capture = obj
            .get("capture")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let kind = obj
            .get("kind")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let name = obj
            .get("name")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let file_f = obj
            .get("file")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let action_s = obj
            .get("action")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("transform step missing 'action'"))?
            .to_string();
        let code = obj
            .get("code")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let before = obj
            .get("before")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());
        let after = obj
            .get("after")
            .and_then(|v| v.as_str())
            .map(|s| s.to_string());

        let match_spec = if let Some(rq) = raw_query {
            transform::Match::Raw {
                raw_query: rq,
                capture,
            }
        } else if let Some(k) = kind {
            transform::Match::Abstract {
                kind: k,
                name,
                file: file_f,
            }
        } else {
            anyhow::bail!("transform step must specify 'kind' or 'raw_query'");
        };

        let action = parse_action(&action_s, code, before, after)?;

        let forest = build_current_forest(root_path, accumulated)?;
        let step_changes = forest.transform(&match_spec, &action, format)?;
        merge_changes(accumulated, step_changes);
    }

    Ok(())
}

/// Build a Forest reflecting the current (possibly modified) state of files.
/// Files that have been modified in `accumulated` are served from memory;
/// everything else is read from disk.
pub(super) fn build_current_forest(
    root_path: &Path,
    accumulated: &HashMap<std::path::PathBuf, (Vec<u8>, Vec<u8>)>,
) -> anyhow::Result<Forest> {
    // Start with a fresh parse of what's on disk.
    let mut forest = Forest::from_path(root_path)?;

    // Overlay any files that have been modified so far in this batch.
    for file in &mut forest.files {
        if let Some((_, current_bytes)) = accumulated.get(&file.path) {
            // Re-parse from the current (in-memory) state.
            let mut parser = Parser::new();
            parser.set_language(&file.adapter.language())?;
            if let Some(tree) = parser.parse(current_bytes, None) {
                file.original_source = current_bytes.clone();
                file.tree = tree;
            }
        }
    }

    Ok(forest)
}

/// Merge a set of step changes into the accumulated map.
/// If a file was already in `accumulated`, keep its original bytes but update
/// the current bytes. If it's new, insert it.
pub(super) fn merge_changes(
    accumulated: &mut HashMap<std::path::PathBuf, (Vec<u8>, Vec<u8>)>,
    step_changes: Vec<FileChange>,
) {
    for change in step_changes {
        accumulated
            .entry(change.path.clone())
            .and_modify(|(_, cur)| *cur = change.new_source.clone())
            .or_insert((change.original, change.new_source));
    }
}

/// Parse an action string + supporting fields into a `transform::Action`.
pub(super) fn parse_action(
    action_s: &str,
    code: Option<String>,
    before: Option<String>,
    after: Option<String>,
) -> anyhow::Result<transform::Action> {
    match action_s {
        "replace" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace' action requires 'code'"))?;
            Ok(transform::Action::Replace { code: c })
        }
        "wrap" => Ok(transform::Action::Wrap {
            before: before.unwrap_or_default(),
            after: after.unwrap_or_default(),
        }),
        "unwrap" => Ok(transform::Action::Unwrap),
        "prepend_statement" => {
            let c =
                code.ok_or_else(|| anyhow::anyhow!("'prepend_statement' action requires 'code'"))?;
            Ok(transform::Action::PrependStatement { code: c })
        }
        "append_statement" => {
            let c =
                code.ok_or_else(|| anyhow::anyhow!("'append_statement' action requires 'code'"))?;
            Ok(transform::Action::AppendStatement { code: c })
        }
        "remove" => Ok(transform::Action::Remove),
        "replace_name" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace_name' action requires 'code'"))?;
            Ok(transform::Action::ReplaceName { code: c })
        }
        "replace_body" => {
            let c = code.ok_or_else(|| anyhow::anyhow!("'replace_body' action requires 'code'"))?;
            Ok(transform::Action::ReplaceBody { code: c })
        }
        other => anyhow::bail!("unknown action '{other}'"),
    }
}

// ---------------------------------------------------------------------------
// Helpers for add_parameter / remove_parameter
// ---------------------------------------------------------------------------

/// Build the text for a new parameter.
pub(super) fn build_param_text(
    name: &str,
    param_type: Option<&str>,
    default_value: Option<&str>,
) -> String {
    match (param_type, default_value) {
        (Some(ty), Some(def)) => format!("{name}: {ty} = {def}"),
        (Some(ty), None) => format!("{name}: {ty}"),
        (None, Some(def)) => format!("{name}={def}"),
        (None, None) => name.to_string(),
    }
}

/// Find the function named `func_name` in `file`, locate its parameter list,
/// and insert `param_text` at the specified position.
///
/// Returns `Ok(Some(new_source))` if the function was found and changed,
/// `Ok(None)` if the function was not found in this file.
pub(super) fn add_param_in_file(
    file: &ParsedFile,
    func_name: &str,
    param_text: &str,
    position: &str,
) -> anyhow::Result<Option<Vec<u8>>> {
    let param_list_range = match find_param_list(file, func_name)? {
        Some(r) => r,
        None => return Ok(None),
    };

    let (list_start, list_end) = param_list_range;
    let source = &file.original_source;
    let inner_start = list_start + 1; // skip '('
    let inner_end = list_end - 1; // before ')'
    let inner = std::str::from_utf8(&source[inner_start..inner_end])
        .unwrap_or("")
        .trim();

    let new_inner = if inner.is_empty() {
        param_text.to_string()
    } else {
        let existing_params: Vec<&str> = inner.split(',').map(|s| s.trim()).collect();

        let insert_idx = match position {
            "first" => 0,
            "last" => existing_params.len(),
            pos if pos.starts_with("after:") => {
                let after_name = &pos["after:".len()..];
                let found = existing_params.iter().position(|p| {
                    p.split(':').next().unwrap_or("").trim() == after_name
                        || p.split('=').next().unwrap_or("").trim() == after_name
                });
                match found {
                    Some(idx) => idx + 1,
                    None => {
                        return Err(anyhow::anyhow!(
                            "parameter '{after_name}' not found in function '{func_name}'"
                        ));
                    }
                }
            }
            _ => return Err(anyhow::anyhow!("invalid position '{position}'")),
        };

        let mut params = existing_params
            .iter()
            .map(|s| s.to_string())
            .collect::<Vec<_>>();
        params.insert(insert_idx, param_text.to_string());
        params.join(", ")
    };

    let new_param_list = format!("({new_inner})");
    let mut result = Vec::with_capacity(source.len());
    result.extend_from_slice(&source[..list_start]);
    result.extend_from_slice(new_param_list.as_bytes());
    result.extend_from_slice(&source[list_end..]);

    if result == source.as_slice() {
        return Ok(None);
    }

    Ok(Some(result))
}

/// Find the function named `func_name` in `file`, locate its parameter list,
/// and remove the parameter named `param_name`.
///
/// Returns `Ok(Some(new_source))` if the parameter was found and removed,
/// `Ok(None)` if the function or parameter was not found in this file.
pub(super) fn remove_param_in_file(
    file: &ParsedFile,
    func_name: &str,
    param_name: &str,
) -> anyhow::Result<Option<Vec<u8>>> {
    let param_list_range = match find_param_list(file, func_name)? {
        Some(r) => r,
        None => return Ok(None),
    };

    let (list_start, list_end) = param_list_range;
    let source = &file.original_source;
    let inner_start = list_start + 1;
    let inner_end = list_end - 1;
    let inner = std::str::from_utf8(&source[inner_start..inner_end])
        .unwrap_or("")
        .trim();

    if inner.is_empty() {
        return Ok(None);
    }

    let existing_params: Vec<&str> = inner.split(',').map(|s| s.trim()).collect();

    let found_idx = existing_params.iter().position(|p| {
        let bare = p.split(':').next().unwrap_or("").trim();
        let bare2 = bare.split('=').next().unwrap_or("").trim();
        bare2 == param_name
    });

    let idx = match found_idx {
        Some(i) => i,
        None => return Ok(None),
    };

    let mut params = existing_params
        .iter()
        .map(|s| s.to_string())
        .collect::<Vec<_>>();
    params.remove(idx);

    let new_inner = params.join(", ");
    let new_param_list = format!("({new_inner})");

    let mut result = Vec::with_capacity(source.len());
    result.extend_from_slice(&source[..list_start]);
    result.extend_from_slice(new_param_list.as_bytes());
    result.extend_from_slice(&source[list_end..]);

    if result == source.as_slice() {
        return Ok(None);
    }

    Ok(Some(result))
}

/// Find the byte range of the parameter list `(...)` of the function named
/// `func_name` in `file`. Returns `(open_paren_byte, close_paren_exclusive)`.
fn find_param_list(file: &ParsedFile, func_name: &str) -> anyhow::Result<Option<(usize, usize)>> {
    let query_str = format!(
        "({} (#eq? @name \"{func_name}\"))",
        file.adapter.function_def_query()
    );
    let query = Query::new(&file.adapter.language(), &query_str)
        .map_err(|e| anyhow::anyhow!("compiling param-list query: {e}"))?;

    let func_idx = query
        .capture_index_for_name("func")
        .ok_or_else(|| anyhow::anyhow!("function_def_query must capture @func"))?;

    let mut cursor = QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    while let Some(m) = matches.next() {
        let func_node = m
            .captures
            .iter()
            .find(|c| c.index == func_idx)
            .map(|c| c.node);

        if let Some(node) = func_node {
            if let Some(params_node) = node.child_by_field_name("parameters") {
                return Ok(Some((params_node.start_byte(), params_node.end_byte())));
            }
            let mut walk = node.walk();
            for child in node.children(&mut walk) {
                let kind = child.kind();
                if matches!(
                    kind,
                    "parameters"
                        | "parameter_list"
                        | "formal_parameters"
                        | "parameter_clause"
                        | "param_list"
                ) {
                    return Ok(Some((child.start_byte(), child.end_byte())));
                }
            }
        }
    }

    Ok(None)
}
