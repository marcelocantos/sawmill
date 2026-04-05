// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use rusqlite::{Connection, params};

/// A single symbol record stored in the database.
pub struct SymbolRecord {
    pub name: String,
    pub kind: String,
    pub file_path: PathBuf,
    pub start_line: usize,
    pub start_col: usize,
    pub end_line: usize,
    pub end_col: usize,
    pub start_byte: usize,
    pub end_byte: usize,
}

/// SQLite-backed persistence layer for codebase metadata.
pub struct Store {
    conn: Connection,
}

impl Store {
    /// Open or create the store at the given path.
    pub fn open(path: &Path) -> Result<Self> {
        let conn = Connection::open(path)
            .with_context(|| format!("opening store at {}", path.display()))?;
        let store = Store { conn };
        store.init()?;
        Ok(store)
    }

    /// Open an in-memory store (for testing).
    pub fn open_in_memory() -> Result<Self> {
        let conn = Connection::open_in_memory()
            .context("opening in-memory store")?;
        let store = Store { conn };
        store.init()?;
        Ok(store)
    }

    fn init(&self) -> Result<()> {
        self.conn.execute_batch(
            "PRAGMA journal_mode=WAL;
             PRAGMA foreign_keys=ON;

             CREATE TABLE IF NOT EXISTS files (
                 path TEXT PRIMARY KEY,
                 language TEXT NOT NULL,
                 mtime_secs INTEGER NOT NULL,
                 mtime_nanos INTEGER NOT NULL,
                 content_hash TEXT NOT NULL
             );

             CREATE TABLE IF NOT EXISTS symbols (
                 id INTEGER PRIMARY KEY,
                 name TEXT NOT NULL,
                 kind TEXT NOT NULL,
                 file_path TEXT NOT NULL REFERENCES files(path) ON DELETE CASCADE,
                 start_line INTEGER NOT NULL,
                 start_col INTEGER NOT NULL,
                 end_line INTEGER NOT NULL,
                 end_col INTEGER NOT NULL,
                 start_byte INTEGER NOT NULL,
                 end_byte INTEGER NOT NULL
             );

             CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
             CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path);
             CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind);

             CREATE TABLE IF NOT EXISTS recipes (
                 name TEXT PRIMARY KEY,
                 description TEXT NOT NULL DEFAULT '',
                 params_json TEXT NOT NULL,
                 steps_json TEXT NOT NULL
             );

             CREATE TABLE IF NOT EXISTS conventions (
                 name TEXT PRIMARY KEY,
                 description TEXT NOT NULL DEFAULT '',
                 check_program TEXT NOT NULL
             );",
        )
        .context("initialising store schema")?;
        Ok(())
    }

    /// Check if a file is cached and up-to-date (same mtime and hash).
    /// Returns the stored content hash if cached and current, None if stale/missing.
    pub fn check_file(&self, path: &Path, mtime: SystemTime) -> Result<Option<String>> {
        let (mtime_secs, mtime_nanos) = split_mtime(mtime)?;
        let path_str = path_to_str(path)?;

        let result = self.conn.query_row(
            "SELECT content_hash FROM files \
             WHERE path = ?1 AND mtime_secs = ?2 AND mtime_nanos = ?3",
            params![path_str, mtime_secs, mtime_nanos],
            |row| row.get::<_, String>(0),
        );

        match result {
            Ok(hash) => Ok(Some(hash)),
            Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
            Err(e) => Err(e).with_context(|| format!("checking file {}", path.display())),
        }
    }

    /// Record a parsed file's metadata.
    pub fn upsert_file(
        &self,
        path: &Path,
        language: &str,
        mtime: SystemTime,
        content_hash: &str,
    ) -> Result<()> {
        let (mtime_secs, mtime_nanos) = split_mtime(mtime)?;
        let path_str = path_to_str(path)?;

        self.conn.execute(
            "INSERT INTO files (path, language, mtime_secs, mtime_nanos, content_hash)
             VALUES (?1, ?2, ?3, ?4, ?5)
             ON CONFLICT(path) DO UPDATE SET
                 language = excluded.language,
                 mtime_secs = excluded.mtime_secs,
                 mtime_nanos = excluded.mtime_nanos,
                 content_hash = excluded.content_hash",
            params![path_str, language, mtime_secs, mtime_nanos, content_hash],
        )
        .with_context(|| format!("upserting file {}", path.display()))?;

        Ok(())
    }

    /// Remove a file and its symbols.
    pub fn remove_file(&self, path: &Path) -> Result<()> {
        let path_str = path_to_str(path)?;

        self.conn
            .execute("DELETE FROM files WHERE path = ?1", params![path_str])
            .with_context(|| format!("removing file {}", path.display()))?;

        Ok(())
    }

    /// Clear and re-insert symbols for a file.
    pub fn update_symbols(&self, file_path: &Path, symbols: &[SymbolRecord]) -> Result<()> {
        let path_str = path_to_str(file_path)?;

        self.conn
            .execute(
                "DELETE FROM symbols WHERE file_path = ?1",
                params![path_str],
            )
            .with_context(|| format!("clearing symbols for {}", file_path.display()))?;

        for sym in symbols {
            self.conn
                .execute(
                    "INSERT INTO symbols \
                     (name, kind, file_path, start_line, start_col, end_line, end_col, \
                      start_byte, end_byte) \
                     VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
                    params![
                        sym.name,
                        sym.kind,
                        path_str,
                        sym.start_line as i64,
                        sym.start_col as i64,
                        sym.end_line as i64,
                        sym.end_col as i64,
                        sym.start_byte as i64,
                        sym.end_byte as i64,
                    ],
                )
                .with_context(|| {
                    format!("inserting symbol '{}' for {}", sym.name, file_path.display())
                })?;
        }

        Ok(())
    }

    /// Find symbols by name (exact or prefix match).
    /// If `name` ends with `*`, performs a prefix match; otherwise exact.
    pub fn find_symbols(&self, name: &str, kind: Option<&str>) -> Result<Vec<SymbolRecord>> {
        let (query_name, use_like) = if let Some(prefix) = name.strip_suffix('*') {
            // Escape any LIKE special chars in the prefix, then append %.
            let escaped = prefix.replace('\\', "\\\\").replace('%', "\\%").replace('_', "\\_");
            (format!("{escaped}%"), true)
        } else {
            (name.to_owned(), false)
        };

        let sql = if use_like {
            match kind {
                Some(_) => {
                    "SELECT name, kind, file_path, start_line, start_col, end_line, end_col, \
                             start_byte, end_byte \
                      FROM symbols WHERE name LIKE ?1 ESCAPE '\\' AND kind = ?2"
                }
                None => {
                    "SELECT name, kind, file_path, start_line, start_col, end_line, end_col, \
                             start_byte, end_byte \
                      FROM symbols WHERE name LIKE ?1 ESCAPE '\\'"
                }
            }
        } else {
            match kind {
                Some(_) => {
                    "SELECT name, kind, file_path, start_line, start_col, end_line, end_col, \
                             start_byte, end_byte \
                      FROM symbols WHERE name = ?1 AND kind = ?2"
                }
                None => {
                    "SELECT name, kind, file_path, start_line, start_col, end_line, end_col, \
                             start_byte, end_byte \
                      FROM symbols WHERE name = ?1"
                }
            }
        };

        let mut stmt = self.conn.prepare(sql).context("preparing find_symbols query")?;

        let rows = match kind {
            Some(k) => stmt.query(params![query_name, k]),
            None => stmt.query(params![query_name]),
        }
        .context("executing find_symbols query")?;

        collect_symbol_rows(rows)
    }

    /// Find all symbols in a file.
    pub fn symbols_in_file(&self, path: &Path) -> Result<Vec<SymbolRecord>> {
        let path_str = path_to_str(path)?;

        let mut stmt = self
            .conn
            .prepare(
                "SELECT name, kind, file_path, start_line, start_col, end_line, end_col, \
                         start_byte, end_byte \
                  FROM symbols WHERE file_path = ?1",
            )
            .context("preparing symbols_in_file query")?;

        let rows = stmt
            .query(params![path_str])
            .context("executing symbols_in_file query")?;

        collect_symbol_rows(rows)
    }

    /// List all tracked file paths.
    pub fn tracked_files(&self) -> Result<Vec<PathBuf>> {
        let mut stmt = self
            .conn
            .prepare("SELECT path FROM files ORDER BY path")
            .context("preparing tracked_files query")?;

        let rows = stmt
            .query_map([], |row| row.get::<_, String>(0))
            .context("executing tracked_files query")?;

        rows.map(|r| {
            r.map(PathBuf::from)
                .context("reading tracked file path")
        })
        .collect()
    }

    /// Save a recipe (upsert).
    pub fn save_recipe(
        &self,
        name: &str,
        description: &str,
        params: &[String],
        steps: &serde_json::Value,
    ) -> Result<()> {
        let params_json = serde_json::to_string(params)
            .context("serialising recipe params")?;
        let steps_json = serde_json::to_string(steps)
            .context("serialising recipe steps")?;

        self.conn.execute(
            "INSERT INTO recipes (name, description, params_json, steps_json)
             VALUES (?1, ?2, ?3, ?4)
             ON CONFLICT(name) DO UPDATE SET
                 description = excluded.description,
                 params_json = excluded.params_json,
                 steps_json = excluded.steps_json",
            params![name, description, params_json, steps_json],
        ).with_context(|| format!("saving recipe '{name}'"))?;

        Ok(())
    }

    /// Load a recipe by name.
    pub fn load_recipe(&self, name: &str) -> Result<Option<(Vec<String>, serde_json::Value, String)>> {
        let result = self.conn.query_row(
            "SELECT params_json, steps_json, description FROM recipes WHERE name = ?1",
            params![name],
            |row| {
                let params_json: String = row.get(0)?;
                let steps_json: String = row.get(1)?;
                let description: String = row.get(2)?;
                Ok((params_json, steps_json, description))
            },
        );

        match result {
            Ok((params_json, steps_json, description)) => {
                let params: Vec<String> = serde_json::from_str(&params_json)
                    .context("deserialising recipe params")?;
                let steps: serde_json::Value = serde_json::from_str(&steps_json)
                    .context("deserialising recipe steps")?;
                Ok(Some((params, steps, description)))
            }
            Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
            Err(e) => Err(e).with_context(|| format!("loading recipe '{name}'")),
        }
    }

    /// List all recipe names with descriptions.
    pub fn list_recipes(&self) -> Result<Vec<(String, String)>> {
        let mut stmt = self.conn.prepare(
            "SELECT name, description FROM recipes ORDER BY name",
        ).context("preparing list_recipes")?;

        let rows = stmt.query_map([], |row| {
            Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
        }).context("listing recipes")?;

        rows.map(|r| r.context("reading recipe row"))
            .collect()
    }

    /// Delete a recipe.
    pub fn delete_recipe(&self, name: &str) -> Result<bool> {
        let count = self.conn.execute(
            "DELETE FROM recipes WHERE name = ?1",
            params![name],
        ).with_context(|| format!("deleting recipe '{name}'"))?;
        Ok(count > 0)
    }

    // --- Conventions ---

    /// Save a convention (upsert).
    pub fn save_convention(&self, name: &str, description: &str, check_program: &str) -> Result<()> {
        self.conn.execute(
            "INSERT INTO conventions (name, description, check_program)
             VALUES (?1, ?2, ?3)
             ON CONFLICT(name) DO UPDATE SET
                 description = excluded.description,
                 check_program = excluded.check_program",
            params![name, description, check_program],
        ).with_context(|| format!("saving convention '{name}'"))?;
        Ok(())
    }

    /// Load all conventions.
    pub fn list_conventions(&self) -> Result<Vec<(String, String, String)>> {
        let mut stmt = self.conn.prepare(
            "SELECT name, description, check_program FROM conventions ORDER BY name",
        ).context("preparing list_conventions")?;

        let rows = stmt.query_map([], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, String>(2)?,
            ))
        }).context("listing conventions")?;

        rows.map(|r| r.context("reading convention row"))
            .collect()
    }

    /// Delete a convention.
    pub fn delete_convention(&self, name: &str) -> Result<bool> {
        let count = self.conn.execute(
            "DELETE FROM conventions WHERE name = ?1",
            params![name],
        ).with_context(|| format!("deleting convention '{name}'"))?;
        Ok(count > 0)
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn split_mtime(mtime: SystemTime) -> Result<(i64, i64)> {
    let dur = mtime
        .duration_since(UNIX_EPOCH)
        .context("mtime is before UNIX epoch")?;
    Ok((dur.as_secs() as i64, dur.subsec_nanos() as i64))
}

fn path_to_str(path: &Path) -> Result<String> {
    path.to_str()
        .map(|s| s.to_owned())
        .with_context(|| format!("path is not valid UTF-8: {}", path.display()))
}

fn collect_symbol_rows(mut rows: rusqlite::Rows<'_>) -> Result<Vec<SymbolRecord>> {
    let mut records = Vec::new();
    while let Some(row) = rows.next().context("iterating symbol rows")? {
        records.push(SymbolRecord {
            name: row.get(0)?,
            kind: row.get(1)?,
            file_path: PathBuf::from(row.get::<_, String>(2)?),
            start_line: row.get::<_, i64>(3)? as usize,
            start_col: row.get::<_, i64>(4)? as usize,
            end_line: row.get::<_, i64>(5)? as usize,
            end_col: row.get::<_, i64>(6)? as usize,
            start_byte: row.get::<_, i64>(7)? as usize,
            end_byte: row.get::<_, i64>(8)? as usize,
        });
    }
    Ok(records)
}
