// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Teach-by-example: extract templates from exemplar code.
//!
//! Given an exemplar file and a mapping of parameter names to their values
//! in the exemplar, extracts a reusable template by replacing all
//! occurrences of parameter values with `$param_name` placeholders.

use std::collections::HashMap;
use std::path::Path;

use anyhow::{Context, Result};

/// A template extracted from an exemplar.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ExemplarTemplate {
    /// Template name.
    pub name: String,
    /// Description.
    pub description: String,
    /// Parameter names.
    pub params: Vec<String>,
    /// File templates: relative path template → content template.
    /// Path templates can contain $param_name (e.g., "src/handlers/$name.rs").
    pub files: Vec<FileTemplate>,
}

/// A single file within a multi-file template.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FileTemplate {
    /// Path template (e.g., "src/handlers/$name.rs").
    pub path_template: String,
    /// Content template with $param_name placeholders.
    pub content_template: String,
}

/// Extract a template from an exemplar file.
///
/// `params` maps parameter names to their values in the exemplar.
/// For example: `{"name": "users", "entity": "User"}`.
///
/// The extraction replaces all occurrences of each parameter value
/// (and its case variants) with `$param_name`.
pub fn extract_template(
    name: &str,
    description: &str,
    exemplar_path: &Path,
    params: &HashMap<String, String>,
    also_affects: &[String],
    root: &Path,
) -> Result<ExemplarTemplate> {
    let mut file_templates = Vec::new();

    // Extract the main exemplar.
    let exemplar_content = std::fs::read_to_string(exemplar_path)
        .with_context(|| format!("reading exemplar: {}", exemplar_path.display()))?;

    let relative_path = exemplar_path
        .strip_prefix(root)
        .unwrap_or(exemplar_path)
        .to_string_lossy()
        .to_string();

    let path_template = templatize(&relative_path, params);
    let content_template = templatize(&exemplar_content, params);

    file_templates.push(FileTemplate {
        path_template,
        content_template,
    });

    // Extract also_affects files.
    for pattern in also_affects {
        let affected_path = root.join(pattern);
        if affected_path.is_file() {
            let content = std::fs::read_to_string(&affected_path)
                .with_context(|| format!("reading affected file: {}", affected_path.display()))?;

            let rel = affected_path
                .strip_prefix(root)
                .unwrap_or(&affected_path)
                .to_string_lossy()
                .to_string();

            file_templates.push(FileTemplate {
                path_template: templatize(&rel, params),
                content_template: templatize(&content, params),
            });
        } else if affected_path.is_dir() {
            // Walk the directory looking for files containing parameter values.
            for entry in walkdir(affected_path.as_path()) {
                let content = match std::fs::read_to_string(&entry) {
                    Ok(c) => c,
                    Err(_) => continue,
                };

                // Only include files that contain at least one parameter value.
                let contains_param = params.values().any(|v| content.contains(v));
                if !contains_param {
                    continue;
                }

                let rel = entry
                    .strip_prefix(root)
                    .unwrap_or(&entry)
                    .to_string_lossy()
                    .to_string();

                file_templates.push(FileTemplate {
                    path_template: templatize(&rel, params),
                    content_template: templatize(&content, params),
                });
            }
        }
    }

    Ok(ExemplarTemplate {
        name: name.to_string(),
        description: description.to_string(),
        params: params.keys().cloned().collect(),
        files: file_templates,
    })
}

/// Instantiate a template with specific parameter values.
/// Returns a list of (file_path, content) pairs.
pub fn instantiate_template(
    template: &ExemplarTemplate,
    params: &HashMap<String, String>,
) -> Result<Vec<(String, String)>> {
    // Validate all required params are provided.
    for p in &template.params {
        if !params.contains_key(p) {
            anyhow::bail!("missing parameter '${p}' for template '{}'", template.name);
        }
    }

    let mut files = Vec::new();
    for ft in &template.files {
        let path = substitute(&ft.path_template, params);
        let content = substitute(&ft.content_template, params);
        files.push((path, content));
    }

    Ok(files)
}

/// Replace all occurrences of parameter values with $param_name placeholders.
/// Handles multiple case variants of each value.
fn templatize(text: &str, params: &HashMap<String, String>) -> String {
    let mut result = text.to_string();

    // Sort by value length descending so longer values are replaced first
    // (avoids partial matches when one value is a substring of another).
    let mut sorted_params: Vec<(&String, &String)> = params.iter().collect();
    sorted_params.sort_by(|a, b| b.1.len().cmp(&a.1.len()));

    for (name, value) in &sorted_params {
        if value.is_empty() {
            continue;
        }

        let placeholder = format!("${name}");

        // Replace exact value.
        result = result.replace(value.as_str(), &placeholder);

        // Replace common case variants.
        let variants = case_variants(value);
        let name_variants = case_variants(name);

        for (i, variant) in variants.iter().enumerate() {
            if variant != value.as_str() && result.contains(variant.as_str()) {
                let ph = if i < name_variants.len() {
                    format!("${}", name_variants[i])
                } else {
                    placeholder.clone()
                };
                result = result.replace(variant.as_str(), &ph);
            }
        }
    }

    result
}

/// Substitute $param_name placeholders with actual values.
/// Also handles case-variant placeholders.
fn substitute(template: &str, params: &HashMap<String, String>) -> String {
    let mut result = template.to_string();

    for (name, value) in params {
        let variants_name = case_variants(name);
        let variants_value = case_variants(value);

        // Replace case variants.
        for (i, nv) in variants_name.iter().enumerate() {
            let placeholder = format!("${nv}");
            let replacement = if i < variants_value.len() {
                &variants_value[i]
            } else {
                value
            };
            result = result.replace(&placeholder, replacement);
        }

        // Replace base placeholder.
        result = result.replace(&format!("${name}"), value);
    }

    result
}

/// Generate case variants of a string:
/// [original, UPPER, lower, Capitalized, camelCase]
fn case_variants(s: &str) -> Vec<String> {
    let mut variants = vec![s.to_string()];

    let upper = s.to_uppercase();
    if upper != s {
        variants.push(upper);
    }

    let lower = s.to_lowercase();
    if lower != s {
        variants.push(lower);
    }

    // Capitalize first letter.
    let capitalized = capitalize(s);
    if capitalized != s && !variants.contains(&capitalized) {
        variants.push(capitalized);
    }

    // Uncapitalize first letter (camelCase).
    let uncapitalized = uncapitalize(s);
    if uncapitalized != s && !variants.contains(&uncapitalized) {
        variants.push(uncapitalized);
    }

    variants
}

fn capitalize(s: &str) -> String {
    let mut chars = s.chars();
    match chars.next() {
        None => String::new(),
        Some(c) => c.to_uppercase().collect::<String>() + chars.as_str(),
    }
}

fn uncapitalize(s: &str) -> String {
    let mut chars = s.chars();
    match chars.next() {
        None => String::new(),
        Some(c) => c.to_lowercase().collect::<String>() + chars.as_str(),
    }
}

/// Simple directory walker (no gitignore awareness needed for small scopes).
fn walkdir(dir: &Path) -> Vec<std::path::PathBuf> {
    let mut files = Vec::new();
    if let Ok(entries) = std::fs::read_dir(dir) {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_file() {
                files.push(path);
            } else if path.is_dir() {
                files.extend(walkdir(&path));
            }
        }
    }
    files
}

/// Substitute parameters in a JSON string, handling case variants.
pub fn substitute_in_json(json: &str, params: &HashMap<String, String>) -> String {
    substitute(json, params)
}

/// Convert an ExemplarTemplate to a recipe format (for storage via the existing recipe system).
pub fn template_to_recipe_steps(template: &ExemplarTemplate) -> serde_json::Value {
    let steps: Vec<serde_json::Value> = template
        .files
        .iter()
        .map(|ft| {
            serde_json::json!({
                "action": "create_file",
                "path": ft.path_template,
                "content": ft.content_template,
            })
        })
        .collect();

    serde_json::json!(steps)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn templatize_basic() {
        let mut params = HashMap::new();
        params.insert("name".to_string(), "users".to_string());
        params.insert("entity".to_string(), "User".to_string());

        let input = "fn list_users() -> Vec<User> { get_users() }";
        let result = templatize(input, &params);

        assert!(result.contains("$name"), "should replace 'users': {result}");
        assert!(
            result.contains("$entity"),
            "should replace 'User': {result}"
        );
        assert!(
            !result.contains("users"),
            "should not contain original: {result}"
        );
    }

    #[test]
    fn templatize_case_variants() {
        let mut params = HashMap::new();
        params.insert("name".to_string(), "users".to_string());

        let input = "USERS_TABLE users Users";
        let result = templatize(input, &params);

        assert!(result.contains("$NAME"), "should replace USERS: {result}");
        assert!(result.contains("$name"), "should replace users: {result}");
        assert!(result.contains("$Name"), "should replace Users: {result}");
    }

    #[test]
    fn substitute_basic() {
        let mut params = HashMap::new();
        params.insert("name".to_string(), "products".to_string());
        params.insert("entity".to_string(), "Product".to_string());

        let template = "fn list_$name() -> Vec<$entity> { get_$name() }";
        let result = substitute(template, &params);

        assert_eq!(
            result,
            "fn list_products() -> Vec<Product> { get_products() }"
        );
    }

    #[test]
    fn substitute_case_variants() {
        let mut params = HashMap::new();
        params.insert("name".to_string(), "products".to_string());

        let template = "$NAME_TABLE $name $Name";
        let result = substitute(template, &params);

        assert!(result.contains("PRODUCTS"), "should expand $NAME: {result}");
        assert!(result.contains("products"), "should expand $name: {result}");
        assert!(result.contains("Products"), "should expand $Name: {result}");
    }

    #[test]
    fn roundtrip() {
        let mut params = HashMap::new();
        params.insert("name".to_string(), "users".to_string());
        params.insert("entity".to_string(), "User".to_string());

        let original = "struct UserHandler {\n    fn list_users(&self) -> Vec<User> {\n        self.db.get_users()\n    }\n}\n";
        let template = templatize(original, &params);

        // Now instantiate with different values.
        let mut new_params = HashMap::new();
        new_params.insert("name".to_string(), "orders".to_string());
        new_params.insert("entity".to_string(), "Order".to_string());

        let result = substitute(&template, &new_params);

        assert!(
            result.contains("OrderHandler"),
            "should have OrderHandler: {result}"
        );
        assert!(
            result.contains("list_orders"),
            "should have list_orders: {result}"
        );
        assert!(
            result.contains("Vec<Order>"),
            "should have Vec<Order>: {result}"
        );
        assert!(
            result.contains("get_orders"),
            "should have get_orders: {result}"
        );
        assert!(
            !result.contains("User"),
            "should not contain User: {result}"
        );
        assert!(
            !result.contains("users"),
            "should not contain users: {result}"
        );
    }
}
