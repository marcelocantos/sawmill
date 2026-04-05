// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use ignore::WalkBuilder;
use tree_sitter::{Parser, Tree};

use crate::adapters::{self, LanguageAdapter};
use crate::js_engine;
use crate::rewrite;
use crate::transform;

/// A single parsed source file.
pub struct ParsedFile {
    pub path: PathBuf,
    pub original_source: Vec<u8>,
    pub tree: Tree,
    pub adapter: &'static dyn LanguageAdapter,
}

impl Clone for ParsedFile {
    fn clone(&self) -> Self {
        ParsedFile {
            path: self.path.clone(),
            original_source: self.original_source.clone(),
            tree: self.tree.clone(),
            adapter: self.adapter,
        }
    }
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

/// Apply a set of file changes atomically with backup.
///
/// Strategy:
/// 1. Write all new content to `.canopy.new` temp files
/// 2. Back up all originals to `.canopy.bak`
/// 3. Rename `.new` files to their final paths
///
/// If anything fails during step 3, the `.bak` files allow recovery.
/// Returns the list of backup paths for undo.
pub fn apply_with_backup(changes: &[FileChange]) -> Result<Vec<PathBuf>> {
    let mut temp_paths = Vec::new();
    let mut backup_paths = Vec::new();

    // Step 1: Write new content to temp files.
    for change in changes {
        let temp = change.path.with_extension("canopy.new");
        std::fs::write(&temp, &change.new_source)
            .with_context(|| format!("writing temp {}", temp.display()))?;
        temp_paths.push(temp);
    }

    // Step 2: Back up originals.
    for change in changes {
        let backup = change.path.with_extension("canopy.bak");
        if change.path.exists() {
            std::fs::copy(&change.path, &backup)
                .with_context(|| format!("backing up {}", change.path.display()))?;
        } else {
            // New file — write an empty marker so undo knows to delete it.
            std::fs::write(&backup, b"")
                .with_context(|| format!("creating backup marker {}", backup.display()))?;
        }
        backup_paths.push(backup);
    }

    // Step 3: Rename temp files to final paths.
    for (i, change) in changes.iter().enumerate() {
        // Ensure parent directory exists (for new files).
        if let Some(parent) = change.path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("creating directory {}", parent.display()))?;
        }
        std::fs::rename(&temp_paths[i], &change.path)
            .with_context(|| format!("renaming temp to {}", change.path.display()))?;
    }

    Ok(backup_paths)
}

/// Undo a previously applied change by restoring from `.canopy.bak` files.
pub fn undo_from_backups(backup_paths: &[PathBuf]) -> Result<usize> {
    let mut restored = 0;
    for backup in backup_paths {
        // Derive the original path from the backup path.
        let _original = backup.with_extension(""); // strips .canopy.bak
        // Actually, with_extension only strips the last extension.
        // .canopy.bak → .canopy → need to strip again.
        let original = PathBuf::from(backup.to_string_lossy().trim_end_matches(".canopy.bak"));

        if !backup.exists() {
            continue;
        }

        let backup_content = std::fs::read(backup)
            .with_context(|| format!("reading backup {}", backup.display()))?;

        if backup_content.is_empty() && !original.exists() {
            // Empty marker for a file that was newly created — nothing to restore.
            let _ = std::fs::remove_file(backup);
            continue;
        }

        if backup_content.is_empty() {
            // File was newly created — remove it.
            let _ = std::fs::remove_file(&original);
        } else {
            // Restore original content.
            std::fs::write(&original, &backup_content)
                .with_context(|| format!("restoring {}", original.display()))?;
        }

        let _ = std::fs::remove_file(backup);
        restored += 1;
    }
    Ok(restored)
}

/// Clean up backup files after a successful apply (user confirmed the changes are good).
pub fn cleanup_backups(backup_paths: &[PathBuf]) {
    for backup in backup_paths {
        let _ = std::fs::remove_file(backup);
    }
    // Also clean up any stale .new files.
    for backup in backup_paths {
        let new_path = PathBuf::from(
            backup
                .to_string_lossy()
                .replace(".canopy.bak", ".canopy.new"),
        );
        let _ = std::fs::remove_file(new_path);
    }
}

/// A collection of parsed source files.
pub struct Forest {
    pub files: Vec<ParsedFile>,
}

impl Clone for Forest {
    fn clone(&self) -> Self {
        Forest {
            files: self.files.clone(),
        }
    }
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
                if entry.file_type().is_some_and(|ft| ft.is_file())
                    && let Some(parsed) = Self::parse_file(entry.path())?
                {
                    files.push(parsed);
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

        let source = std::fs::read(path).with_context(|| format!("reading {}", path.display()))?;

        let mut parser = Parser::new();
        parser
            .set_language(&adapter.language())
            .with_context(|| format!("setting language for {}", path.display()))?;

        let tree = parser
            .parse(&source, None)
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

    /// Apply a JS transform function across the forest.
    pub fn transform_js(
        &self,
        match_spec: &transform::Match,
        transform_fn: &str,
        format: bool,
    ) -> Result<Vec<FileChange>> {
        let mut changes = Vec::new();

        for file in &self.files {
            let query_str = transform::resolve_query_str(file.adapter, match_spec)?;
            let mut new_source = js_engine::run_js_transform(
                &file.original_source,
                &file.tree,
                &query_str,
                transform_fn,
                &file.path.to_string_lossy(),
                file.adapter,
            )?;
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
    pub fn query(&self, match_spec: &transform::Match) -> Result<Vec<QueryResult>> {
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
