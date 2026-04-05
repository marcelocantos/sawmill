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

    /// Formatter command that reads source from stdin and writes formatted
    /// output to stdout. Returns None if no formatter is configured.
    fn formatter_command(&self) -> Option<&[&str]> {
        None
    }

    /// LSP server command and arguments. Returns None if no LSP is configured.
    fn lsp_command(&self) -> Option<&[&str]> {
        None
    }

    /// LSP language ID (e.g. "rust", "python", "typescript").
    fn lsp_language_id(&self) -> &str {
        ""
    }

    // --- Structural navigation queries ---

    /// Tree-sitter query for fields/attributes within a struct/class.
    /// Must capture `@name` for the field name, `@type` for the type (if typed),
    /// and `@field` for the whole field node.
    fn field_query(&self) -> &str {
        ""
    }

    /// Tree-sitter query for methods within a class/impl block.
    /// Must capture `@name` and `@method` for the whole node.
    fn method_query(&self) -> &str {
        ""
    }

    /// Tree-sitter query for decorators/attributes on a node.
    /// Must capture `@decorator` for the whole decorator node.
    fn decorator_query(&self) -> &str {
        ""
    }

    // --- Code generation templates ---

    /// Doc comment prefix for this language (e.g., "///" for Rust, "#" for Python).
    fn doc_comment_prefix(&self) -> &str {
        "//"
    }

    /// Format a doc comment string. Each line is prefixed with the
    /// language's doc comment prefix and appropriate indentation.
    fn format_doc_comment(&self, doc: &str, indent: &str) -> String {
        let prefix = self.doc_comment_prefix();
        doc.lines()
            .map(|line| {
                if line.is_empty() {
                    format!("{indent}{prefix}\n")
                } else {
                    format!("{indent}{prefix} {line}\n")
                }
            })
            .collect()
    }

    /// Generate a field/attribute declaration, optionally with a doc comment.
    /// Returns the text to insert (e.g., "    /// User's email.\n    email: String,\n").
    fn gen_field(&self, name: &str, type_name: &str) -> String {
        format!("{name}: {type_name}")
    }

    /// Generate a field with a doc comment.
    fn gen_field_with_doc(&self, name: &str, type_name: &str, doc: &str) -> String {
        if doc.is_empty() {
            return self.gen_field(name, type_name);
        }
        let field = self.gen_field(name, type_name);
        // Extract indentation from the generated field.
        let indent: String = field.chars().take_while(|c| c.is_whitespace()).collect();
        let comment = self.format_doc_comment(doc, &indent);
        format!("{comment}{field}")
    }

    /// Generate a method stub.
    fn gen_method(&self, name: &str, params: &str, return_type: &str, body: &str) -> String {
        let _ = (name, params, return_type, body);
        String::new()
    }

    /// Generate a method with a doc comment.
    fn gen_method_with_doc(
        &self,
        name: &str,
        params: &str,
        return_type: &str,
        body: &str,
        doc: &str,
    ) -> String {
        if doc.is_empty() {
            return self.gen_method(name, params, return_type, body);
        }
        let method = self.gen_method(name, params, return_type, body);
        let indent: String = method.chars().take_while(|c| c.is_whitespace()).collect();
        let comment = self.format_doc_comment(doc, &indent);
        format!("{comment}{method}")
    }

    /// Generate an import statement.
    fn gen_import(&self, path: &str) -> String {
        let _ = path;
        String::new()
    }
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
