// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use anyhow::{Context, Result};
use similar::TextDiff;
use std::path::Path;
use std::process::{Command, Stdio};
use streaming_iterator::StreamingIterator;
use tree_sitter::Query;

use crate::adapters::LanguageAdapter;
use crate::forest::ParsedFile;

/// Rename all occurrences of identifier `from` to `to` in a single file.
/// Returns the new source bytes with minimal changes.
pub fn rename_in_file(file: &ParsedFile, from: &str, to: &str) -> Result<Vec<u8>> {
    let query_src = file.adapter.identifier_query();
    let query = Query::new(&file.adapter.language(), query_src)
        .with_context(|| "compiling identifier query")?;

    let name_idx = query
        .capture_index_for_name("name")
        .with_context(|| "identifier query must capture @name")?;

    let mut cursor = tree_sitter::QueryCursor::new();
    let mut matches = cursor.matches(
        &query,
        file.tree.root_node(),
        file.original_source.as_slice(),
    );

    // Collect byte ranges of matching identifiers (sorted by start position).
    let mut edits: Vec<(usize, usize)> = Vec::new();
    while let Some(m) = matches.next() {
        for capture in m.captures {
            if capture.index == name_idx {
                let node = capture.node;
                let text = &file.original_source[node.start_byte()..node.end_byte()];
                if text == from.as_bytes() {
                    edits.push((node.start_byte(), node.end_byte()));
                }
            }
        }
    }

    if edits.is_empty() {
        return Ok(file.original_source.clone());
    }

    // Build new source by copying unchanged regions and splicing replacements.
    let mut result = Vec::with_capacity(file.original_source.len());
    let mut last_end = 0;

    for (start, end) in &edits {
        result.extend_from_slice(&file.original_source[last_end..*start]);
        result.extend_from_slice(to.as_bytes());
        last_end = *end;
    }
    result.extend_from_slice(&file.original_source[last_end..]);

    Ok(result)
}

/// Run the language's formatter on source bytes via stdin→stdout.
/// Returns the formatted output, or the original source unchanged if
/// the formatter is not available or fails.
pub fn format_source(source: &[u8], adapter: &dyn LanguageAdapter) -> Vec<u8> {
    let cmd_parts = match adapter.formatter_command() {
        Some(parts) if !parts.is_empty() => parts,
        _ => return source.to_vec(),
    };

    let mut child = match Command::new(cmd_parts[0])
        .args(&cmd_parts[1..])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(c) => c,
        Err(_) => return source.to_vec(), // Formatter not installed.
    };

    if let Some(mut stdin) = child.stdin.take() {
        use std::io::Write;
        let _ = stdin.write_all(source);
    }

    match child.wait_with_output() {
        Ok(output) if output.status.success() && !output.stdout.is_empty() => output.stdout,
        _ => source.to_vec(), // Formatter failed; return original.
    }
}

/// Produce a unified diff between original and new source for a file.
pub fn unified_diff(path: &Path, original: &[u8], new: &[u8]) -> String {
    let old_text = String::from_utf8_lossy(original);
    let new_text = String::from_utf8_lossy(new);

    let diff = TextDiff::from_lines(old_text.as_ref(), new_text.as_ref());

    let mut output = String::new();
    output.push_str(&format!("--- a/{}\n", path.display()));
    output.push_str(&format!("+++ b/{}\n", path.display()));

    for hunk in diff.unified_diff().context_radius(3).iter_hunks() {
        output.push_str(&format!("{hunk}"));
    }

    output
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::adapters::LanguageAdapter;
    use crate::adapters::python::PythonAdapter;
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
    fn identity_round_trip() {
        let source = r#"
def hello(name):
    print(f"Hello, {name}!")

class Greeter:
    def greet(self, name):
        return f"Hi, {name}"

x = hello("world")
"#;
        let file = parse_python(source);
        let result = rename_in_file(&file, "nonexistent", "whatever").unwrap();
        assert_eq!(result, file.original_source, "identity round-trip failed");
    }

    #[test]
    fn rename_single_identifier() {
        let source = "x = 1\nprint(x)\n";
        let file = parse_python(source);
        let result = rename_in_file(&file, "x", "y").unwrap();
        assert_eq!(String::from_utf8(result).unwrap(), "y = 1\nprint(y)\n");
    }

    #[test]
    fn rename_function() {
        let source = "def foo():\n    pass\n\nfoo()\n";
        let file = parse_python(source);
        let result = rename_in_file(&file, "foo", "bar").unwrap();
        assert_eq!(
            String::from_utf8(result).unwrap(),
            "def bar():\n    pass\n\nbar()\n"
        );
    }

    #[test]
    fn rename_preserves_formatting() {
        let source = "x   =   1  # a comment\nprint(  x  )\n";
        let file = parse_python(source);
        let result = rename_in_file(&file, "x", "value").unwrap();
        let result_str = String::from_utf8(result).unwrap();
        // Whitespace and comments around the identifier must be preserved.
        assert!(result_str.contains("value   =   1  # a comment"));
        assert!(result_str.contains("print(  value  )"));
    }

    #[test]
    fn diff_output() {
        let source = "x = 1\n";
        let file = parse_python(source);
        let new_source = rename_in_file(&file, "x", "y").unwrap();
        let diff = unified_diff(&file.path, &file.original_source, &new_source);
        assert!(diff.contains("--- a/test.py"));
        assert!(diff.contains("+++ b/test.py"));
        assert!(diff.contains("-x = 1"));
        assert!(diff.contains("+y = 1"));
    }
}
