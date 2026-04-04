// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use ignore::WalkBuilder;
use tree_sitter::{Parser, Tree};

use crate::adapters::{self, LanguageAdapter};
use crate::rewrite;

/// A single parsed source file.
pub struct ParsedFile {
    pub path: PathBuf,
    pub original_source: Vec<u8>,
    pub tree: Tree,
    pub adapter: &'static dyn LanguageAdapter,
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
    /// Returns a unified diff string.
    pub fn rename(&mut self, from: &str, to: &str) -> Result<String> {
        let mut all_diffs = String::new();

        for file in &self.files {
            let new_source = rewrite::rename_in_file(file, from, to)?;
            if new_source != file.original_source {
                let diff = rewrite::unified_diff(
                    &file.path,
                    &file.original_source,
                    &new_source,
                );
                all_diffs.push_str(&diff);
            }
        }

        Ok(all_diffs)
    }
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
