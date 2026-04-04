// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use ignore::WalkBuilder;
use tree_sitter::{Parser, Tree};

use crate::adapters::{self, LanguageAdapter};
use crate::rewrite;
use crate::transform;

/// A single parsed source file.
pub struct ParsedFile {
    pub path: PathBuf,
    pub original_source: Vec<u8>,
    pub tree: Tree,
    pub adapter: &'static dyn LanguageAdapter,
}

/// A pending file change (original + new content).
pub struct FileChange {
    pub path: PathBuf,
    pub original: Vec<u8>,
    pub new_source: Vec<u8>,
}

impl FileChange {
    pub fn diff(&self) -> String {
        rewrite::unified_diff(&self.path, &self.original, &self.new_source)
    }

    pub fn apply(&self) -> Result<()> {
        std::fs::write(&self.path, &self.new_source)
            .with_context(|| format!("writing {}", self.path.display()))
    }
}

/// A collection of parsed source files.
pub struct Forest {
    pub files: Vec<ParsedFile>,
}

impl Forest {
    /// Parse all supported files under `path` (file or directory).
    pub fn from_path(path: &Path) -> Result<Self> {
        let mut files = Vec::new();

        if path.is_file() {
            if let Some(parsed) = Self::parse_file(path)? {
                files.push(parsed);
            }
        } else {
            for entry in WalkBuilder::new(path).build() {
                let entry = entry?;
                if entry.file_type().is_some_and(|ft| ft.is_file()) {
                    if let Some(parsed) = Self::parse_file(entry.path())? {
                        files.push(parsed);
                    }
                }
            }
        }

        Ok(Forest { files })
    }

    fn parse_file(path: &Path) -> Result<Option<ParsedFile>> {
        let ext = match path.extension().and_then(|e| e.to_str()) {
            Some(ext) => ext,
            None => return Ok(None),
        };

        let adapter = match adapters::adapter_for_extension(ext) {
            Some(a) => a,
            None => return Ok(None),
        };

        let source = std::fs::read(path)
            .with_context(|| format!("reading {}", path.display()))?;

        let mut parser = Parser::new();
        parser.set_language(&adapter.language())
            .with_context(|| format!("setting language for {}", path.display()))?;

        let tree = parser.parse(&source, None)
            .with_context(|| format!("parsing {}", path.display()))?;

        Ok(Some(ParsedFile {
            path: path.to_owned(),
            original_source: source,
            tree,
            adapter,
        }))
    }

    /// Rename all occurrences of `from` to `to` across the forest.
    /// If `format` is true, runs the language formatter on changed files.
    pub fn rename(&self, from: &str, to: &str, format: bool) -> Result<Vec<FileChange>> {
        let mut changes = Vec::new();

        for file in &self.files {
            let mut new_source = rewrite::rename_in_file(file, from, to)?;
            if new_source != file.original_source {
                if format {
                    new_source = rewrite::format_source(&new_source, file.adapter);
                }
                changes.push(FileChange {
                    path: file.path.clone(),
                    original: file.original_source.clone(),
                    new_source,
                });
            }
        }

        Ok(changes)
    }

    /// Convenience: rename and return unified diff string.
    pub fn rename_diff(&self, from: &str, to: &str) -> Result<String> {
        let changes = self.rename(from, to, false)?;
        Ok(changes.iter().map(|c| c.diff()).collect())
    }

    /// Apply a match/act transform across the forest.
    /// If `format` is true, runs the language formatter on changed files.
    pub fn transform(
        &self,
        match_spec: &transform::Match,
        action: &transform::Action,
        format: bool,
    ) -> Result<Vec<FileChange>> {
        let mut changes = Vec::new();

        for file in &self.files {
            let mut new_source = transform::transform_file(file, match_spec, action)?;
            if new_source != file.original_source {
                if format {
                    new_source = rewrite::format_source(&new_source, file.adapter);
                }
                changes.push(FileChange {
                    path: file.path.clone(),
                    original: file.original_source.clone(),
                    new_source,
                });
            }
        }

        Ok(changes)
    }

    /// Query the forest for nodes matching a pattern.
    /// Returns a list of (file_path, line, column, node_kind, node_text) tuples.
    pub fn query(
        &self,
        match_spec: &transform::Match,
    ) -> Result<Vec<QueryResult>> {
        let mut results = Vec::new();

        for file in &self.files {
            let matches = transform::query_file(file, match_spec)?;
            results.extend(matches);
        }

        Ok(results)
    }
}

/// A single query match result.
pub struct QueryResult {
    pub path: PathBuf,
    pub start_line: usize,
    pub start_col: usize,
    pub kind: String,
    pub name: Option<String>,
    pub text: String,
}

impl fmt::Display for Forest {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(f, "Forest: {} file(s)", self.files.len())?;
        for file in &self.files {
            let has_errors = file.tree.root_node().has_error();
            let status = if has_errors { " [parse errors]" } else { "" };
            writeln!(f, "  {}{status}", file.path.display())?;
        }
        Ok(())
    }
}
