// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Persistent codebase model.
//!
//! Ties together the forest (in-memory parsed files), SQLite store
//! (persistent metadata and symbol index), and file watcher (live updates).
//! The MCP server holds a single `CodebaseModel` and all tool calls
//! operate against it.

use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::time::SystemTime;

use anyhow::{Context, Result};

use crate::adapters;
use crate::forest::{Forest, ParsedFile};
use crate::index;
use crate::lsp::LspManager;
use crate::store::{Store, SymbolRecord};
use crate::watcher::{FileEvent, FileWatcher};

/// Persistent, live-updating codebase model.
pub struct CodebaseModel {
    /// Root directory being tracked.
    root: PathBuf,
    /// In-memory parsed files.
    pub forest: Forest,
    /// SQLite-backed persistent store.
    store: Store,
    /// File system watcher (optional — None if watching failed to start).
    _watcher: Option<FileWatcher>,
    /// Channel for receiving file events.
    events_rx: Option<mpsc::Receiver<FileEvent>>,
    /// LSP connections (optional — None for ephemeral models).
    pub lsp: Option<LspManager>,
}

impl CodebaseModel {
    /// Load a codebase model for the given directory.
    ///
    /// 1. Opens (or creates) the SQLite store at `{root}/.sawmill/store.db`
    /// 2. Walks the directory, checks each file against the store
    /// 3. Re-parses only files that have changed (mtime or content hash mismatch)
    /// 4. Builds the symbol index for changed files
    /// 5. Starts a file watcher for live updates
    pub fn load(root: &Path) -> Result<Self> {
        let root = root
            .canonicalize()
            .with_context(|| format!("canonicalising {}", root.display()))?;

        // Ensure .sawmill directory exists.
        let store_dir = root.join(".sawmill");
        std::fs::create_dir_all(&store_dir)
            .with_context(|| format!("creating {}", store_dir.display()))?;

        let store = Store::open(&store_dir.join("store.db"))?;

        // Parse the directory, using the store to skip unchanged files.
        let forest = Self::incremental_parse(&root, &store)?;

        // Start file watcher.
        let (watcher, events_rx) = match FileWatcher::watch(&root) {
            Ok((w, rx)) => (Some(w), Some(rx)),
            Err(_) => (None, None),
        };

        // Start LSP servers.
        let adapters: Vec<&'static dyn crate::adapters::LanguageAdapter> = vec![
            &crate::adapters::python::PythonAdapter,
            &crate::adapters::rust::RustAdapter,
            &crate::adapters::typescript::TypeScriptAdapter,
            &crate::adapters::cpp::CppAdapter,
            &crate::adapters::go::GoAdapter,
        ];
        let mut lsp = LspManager::new(&root, &adapters);

        // Open all parsed files with their LSP servers.
        for file in &forest.files {
            let lang_id = file.adapter.lsp_language_id();
            if !lang_id.is_empty() {
                let text = String::from_utf8_lossy(&file.original_source);
                let _ = lsp.did_open(&file.path, lang_id, &text);
            }
        }

        Ok(CodebaseModel {
            root,
            forest,
            store,
            _watcher: watcher,
            events_rx,
            lsp: Some(lsp),
        })
    }

    /// Load without persistence or watching (for testing or one-shot CLI use).
    pub fn load_ephemeral(root: &Path) -> Result<Self> {
        let root = root.canonicalize().unwrap_or_else(|_| root.to_owned());
        let store = Store::open_in_memory()?;
        let forest = Forest::from_path(&root)?;

        // Index all files.
        for file in &forest.files {
            let symbols = index::extract_symbols(file);
            let records = symbols_to_records(&symbols);
            let _ = store.update_symbols(&file.path, &records);
        }

        Ok(CodebaseModel {
            root,
            forest,
            store,
            _watcher: None,
            events_rx: None,
            lsp: None,
        })
    }

    /// Process any pending file events from the watcher.
    /// Call this before operations to ensure the model is up-to-date.
    pub fn sync(&mut self) -> Result<()> {
        let rx = match &self.events_rx {
            Some(rx) => rx,
            None => return Ok(()),
        };

        // Drain all pending events (non-blocking).
        let mut changed_paths = Vec::new();
        let mut removed_paths = Vec::new();

        while let Ok(event) = rx.try_recv() {
            match event {
                FileEvent::Created(p) | FileEvent::Modified(p) => {
                    if !changed_paths.contains(&p) {
                        changed_paths.push(p);
                    }
                }
                FileEvent::Removed(p) => {
                    removed_paths.push(p);
                }
            }
        }

        // Remove deleted files.
        for path in &removed_paths {
            self.forest.files.retain(|f| f.path != *path);
            let _ = self.store.remove_file(path);
        }

        // Re-parse changed/created files.
        for path in &changed_paths {
            if !path.exists() {
                continue;
            }
            match self.parse_and_index_file(path) {
                Ok(Some(parsed)) => {
                    // Replace or add in forest.
                    if let Some(existing) = self.forest.files.iter_mut().find(|f| f.path == *path) {
                        *existing = parsed;
                    } else {
                        self.forest.files.push(parsed);
                    }
                }
                Ok(None) => {} // Unsupported file type.
                Err(_) => {}   // Parse error; keep old version.
            }
        }

        Ok(())
    }

    /// Re-parse a single file, update the store and symbol index.
    fn parse_and_index_file(&self, path: &Path) -> Result<Option<ParsedFile>> {
        let ext = match path.extension().and_then(|e| e.to_str()) {
            Some(ext) => ext,
            None => return Ok(None),
        };

        let adapter = match adapters::adapter_for_extension(ext) {
            Some(a) => a,
            None => return Ok(None),
        };

        let source = std::fs::read(path).with_context(|| format!("reading {}", path.display()))?;

        let mtime = std::fs::metadata(path)
            .and_then(|m| m.modified())
            .unwrap_or(SystemTime::UNIX_EPOCH);

        let content_hash = blake3::hash(&source).to_hex().to_string();

        let mut parser = tree_sitter::Parser::new();
        parser
            .set_language(&adapter.language())
            .with_context(|| format!("setting language for {}", path.display()))?;

        let tree = parser
            .parse(&source, None)
            .with_context(|| format!("parsing {}", path.display()))?;

        let parsed = ParsedFile {
            path: path.to_owned(),
            original_source: source,
            tree,
            adapter,
        };

        // Update store.
        let lang_name = ext; // Simple mapping for now.
        self.store
            .upsert_file(path, lang_name, mtime, &content_hash)?;

        // Update symbol index.
        let symbols = index::extract_symbols(&parsed);
        let records = symbols_to_records(&symbols);
        self.store.update_symbols(path, &records)?;

        Ok(Some(parsed))
    }

    /// Parse the directory incrementally, skipping unchanged files.
    fn incremental_parse(root: &Path, store: &Store) -> Result<Forest> {
        let mut files = Vec::new();

        let walker = ignore::WalkBuilder::new(root).build();
        for entry in walker {
            let entry = entry?;
            if !entry.file_type().is_some_and(|ft| ft.is_file()) {
                continue;
            }

            let path = entry.path();
            let ext = match path.extension().and_then(|e| e.to_str()) {
                Some(ext) => ext,
                None => continue,
            };

            let adapter = match adapters::adapter_for_extension(ext) {
                Some(a) => a,
                None => continue,
            };

            let mtime = std::fs::metadata(path)
                .and_then(|m| m.modified())
                .unwrap_or(SystemTime::UNIX_EPOCH);

            let source =
                std::fs::read(path).with_context(|| format!("reading {}", path.display()))?;

            let content_hash = blake3::hash(&source).to_hex().to_string();

            // Check if the file is cached and unchanged.
            let is_cached = store
                .check_file(path, mtime)?
                .is_some_and(|stored_hash| stored_hash == content_hash);

            let mut parser = tree_sitter::Parser::new();
            parser
                .set_language(&adapter.language())
                .with_context(|| format!("setting language for {}", path.display()))?;

            let tree = parser
                .parse(&source, None)
                .with_context(|| format!("parsing {}", path.display()))?;

            let parsed = ParsedFile {
                path: path.to_owned(),
                original_source: source,
                tree,
                adapter,
            };

            // Always parse into memory (we need the tree for queries), but only
            // update the store if the file changed.
            if !is_cached {
                store.upsert_file(path, ext, mtime, &content_hash)?;
                let symbols = index::extract_symbols(&parsed);
                let records = symbols_to_records(&symbols);
                store.update_symbols(path, &records)?;
            }

            files.push(parsed);
        }

        Ok(Forest { files })
    }

    /// Find symbols by name, using the persistent index.
    pub fn find_symbols(&self, name: &str, kind: Option<&str>) -> Result<Vec<SymbolRecord>> {
        self.store.find_symbols(name, kind)
    }

    /// Get the root directory.
    pub fn root(&self) -> &Path {
        &self.root
    }

    /// Get the number of tracked files.
    pub fn file_count(&self) -> usize {
        self.forest.files.len()
    }

    /// Save a recipe to the store.
    pub fn save_recipe(
        &self,
        name: &str,
        description: &str,
        params: &[String],
        steps: &serde_json::Value,
    ) -> Result<()> {
        self.store.save_recipe(name, description, params, steps)
    }

    /// Load a recipe from the store.
    pub fn load_recipe(
        &self,
        name: &str,
    ) -> Result<Option<(Vec<String>, serde_json::Value, String)>> {
        self.store.load_recipe(name)
    }

    /// List all recipes.
    pub fn list_recipes(&self) -> Result<Vec<(String, String)>> {
        self.store.list_recipes()
    }

    /// Save a convention.
    pub fn save_convention(
        &self,
        name: &str,
        description: &str,
        check_program: &str,
    ) -> Result<()> {
        self.store.save_convention(name, description, check_program)
    }

    /// List all conventions.
    pub fn list_conventions(&self) -> Result<Vec<(String, String, String)>> {
        self.store.list_conventions()
    }

    /// Delete a convention.
    pub fn delete_convention(&self, name: &str) -> Result<bool> {
        self.store.delete_convention(name)
    }
}

/// Convert index::Symbol to store::SymbolRecord.
fn symbols_to_records(symbols: &[index::Symbol]) -> Vec<SymbolRecord> {
    symbols
        .iter()
        .map(|s| SymbolRecord {
            name: s.name.clone(),
            kind: s.kind.clone(),
            file_path: PathBuf::from(&s.file_path),
            start_line: s.start_line,
            start_col: s.start_col,
            end_line: s.end_line,
            end_col: s.end_col,
            start_byte: s.start_byte,
            end_byte: s.end_byte,
        })
        .collect()
}
