// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package store provides SQLite-backed persistence for codebase metadata
// including file records, symbol indices, recipes, and conventions.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite driver registration.
)

// SymbolRecord is a single symbol record stored in the database.
type SymbolRecord struct {
	Name      string
	Kind      string
	FilePath  string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	StartByte int
	EndByte   int
}

// Recipe is a saved transformation recipe.
type Recipe struct {
	Name        string
	Description string
	Params      []string
	Steps       json.RawMessage
}

// Convention is a saved convention check.
type Convention struct {
	Name         string
	Description  string
	CheckProgram string
}

// Store is a SQLite-backed persistence layer for codebase metadata.
type Store struct {
	db *sql.DB
}

// Open opens or creates the store at the given path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening store at %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenInMemory opens an in-memory store (for testing).
func OpenInMemory() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory store: %w", err)
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA cache_size=-16000;
		PRAGMA mmap_size=268435456;

		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			language TEXT NOT NULL,
			mtime_secs INTEGER NOT NULL,
			mtime_nanos INTEGER NOT NULL,
			content_hash TEXT NOT NULL
		);`)
	if err != nil {
		return fmt.Errorf("initialising store schema: %w", err)
	}

	// Migration: add source column if it doesn't exist yet.
	var hasSource bool
	rows, err := s.db.Query("PRAGMA table_info(files)")
	if err != nil {
		return fmt.Errorf("checking files schema: %w", err)
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scanning table_info: %w", err)
		}
		if name == "source" {
			hasSource = true
		}
	}
	rows.Close()
	if !hasSource {
		if _, err := s.db.Exec("ALTER TABLE files ADD COLUMN source BLOB"); err != nil {
			return fmt.Errorf("adding source column: %w", err)
		}
	}

	_, err = s.db.Exec(`

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

		CREATE INDEX IF NOT EXISTS idx_files_language ON files(language);

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
		);

		CREATE TABLE IF NOT EXISTS invariants (
			name TEXT PRIMARY KEY,
			description TEXT NOT NULL DEFAULT '',
			rule_json TEXT NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("initialising store schema: %w", err)
	}
	return nil
}

// splitMtime splits a time.Time into (unix_secs, nanos) for storage.
func splitMtime(t time.Time) (int64, int64) {
	return t.Unix(), int64(t.Nanosecond())
}

// --- Files ---

// CheckFile checks if a file is cached and up-to-date. Returns the stored
// content hash if cached and current, empty string if stale/missing.
func (s *Store) CheckFile(path string, mtime time.Time) (string, error) {
	secs, nanos := splitMtime(mtime)
	var hash string
	err := s.db.QueryRow(
		"SELECT content_hash FROM files WHERE path = ? AND mtime_secs = ? AND mtime_nanos = ?",
		path, secs, nanos,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("checking file %s: %w", path, err)
	}
	return hash, nil
}

// UpsertFile records a parsed file's metadata and source bytes.
func (s *Store) UpsertFile(path, language string, mtime time.Time, contentHash string, source []byte) error {
	secs, nanos := splitMtime(mtime)
	_, err := s.db.Exec(
		`INSERT INTO files (path, language, mtime_secs, mtime_nanos, content_hash, source)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
			language = excluded.language,
			mtime_secs = excluded.mtime_secs,
			mtime_nanos = excluded.mtime_nanos,
			content_hash = excluded.content_hash,
			source = excluded.source`,
		path, language, secs, nanos, contentHash, source,
	)
	if err != nil {
		return fmt.Errorf("upserting file %s: %w", path, err)
	}
	return nil
}

// ReadSource returns the stored source bytes for a file.
func (s *Store) ReadSource(path string) ([]byte, error) {
	var source []byte
	err := s.db.QueryRow("SELECT source FROM files WHERE path = ?", path).Scan(&source)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file %s not found in store", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading source for %s: %w", path, err)
	}
	return source, nil
}

// FileLanguage returns the stored language extension for a file.
func (s *Store) FileLanguage(path string) (string, error) {
	var lang string
	err := s.db.QueryRow("SELECT language FROM files WHERE path = ?", path).Scan(&lang)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("file %s not found in store", path)
	}
	if err != nil {
		return "", fmt.Errorf("reading language for %s: %w", path, err)
	}
	return lang, nil
}

// LanguageSummary returns a map of language -> file count from the store.
func (s *Store) LanguageSummary() (map[string]int, error) {
	rows, err := s.db.Query("SELECT language, COUNT(*) FROM files GROUP BY language")
	if err != nil {
		return nil, fmt.Errorf("querying language summary: %w", err)
	}
	defer rows.Close()
	summary := make(map[string]int)
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("scanning language summary: %w", err)
		}
		summary[lang] = count
	}
	return summary, rows.Err()
}

// FileCount returns the total number of tracked files.
func (s *Store) FileCount() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting files: %w", err)
	}
	return count, nil
}

// RemoveFile removes a file and its symbols (via ON DELETE CASCADE).
func (s *Store) RemoveFile(path string) error {
	_, err := s.db.Exec("DELETE FROM files WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("removing file %s: %w", path, err)
	}
	return nil
}

// TrackedFiles lists all tracked file paths.
func (s *Store) TrackedFiles() ([]string, error) {
	rows, err := s.db.Query("SELECT path FROM files ORDER BY path")
	if err != nil {
		return nil, fmt.Errorf("listing tracked files: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("reading tracked file path: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// TrackedFileRecord is the metadata for a tracked file, without source bytes.
type TrackedFileRecord struct {
	Path        string
	Language    string
	ContentHash string
}

// TrackedFilesWithMeta returns metadata for all tracked files.
func (s *Store) TrackedFilesWithMeta() ([]TrackedFileRecord, error) {
	rows, err := s.db.Query("SELECT path, language, content_hash FROM files ORDER BY path")
	if err != nil {
		return nil, fmt.Errorf("listing tracked files: %w", err)
	}
	defer rows.Close()

	var records []TrackedFileRecord
	for rows.Next() {
		var r TrackedFileRecord
		if err := rows.Scan(&r.Path, &r.Language, &r.ContentHash); err != nil {
			return nil, fmt.Errorf("scanning tracked file: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// --- Symbols ---

// UpdateSymbols clears and re-inserts symbols for a file.
func (s *Store) UpdateSymbols(filePath string, symbols []SymbolRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec("DELETE FROM symbols WHERE file_path = ?", filePath); err != nil {
		return fmt.Errorf("clearing symbols for %s: %w", filePath, err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO symbols
		 (name, kind, file_path, start_line, start_col, end_line, end_col, start_byte, end_byte)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing symbol insert: %w", err)
	}
	defer stmt.Close()

	for _, sym := range symbols {
		if _, err := stmt.Exec(
			sym.Name, sym.Kind, filePath,
			sym.StartLine, sym.StartCol, sym.EndLine, sym.EndCol,
			sym.StartByte, sym.EndByte,
		); err != nil {
			return fmt.Errorf("inserting symbol %q for %s: %w", sym.Name, filePath, err)
		}
	}

	return tx.Commit()
}

// FindSymbols finds symbols by name (exact or prefix match if name ends with
// '*') and optional kind filter.
func (s *Store) FindSymbols(name string, kind string) ([]SymbolRecord, error) {
	var queryName string
	var useLike bool

	if prefix, ok := strings.CutSuffix(name, "*"); ok {
		// Escape LIKE special chars, then append %.
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
		queryName = escaped + "%"
		useLike = true
	} else {
		queryName = name
	}

	var sqlStr string
	var args []any

	if useLike {
		if kind != "" {
			sqlStr = `SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
					  start_byte, end_byte FROM symbols WHERE name LIKE ? ESCAPE '\' AND kind = ?`
			args = []any{queryName, kind}
		} else {
			sqlStr = `SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
					  start_byte, end_byte FROM symbols WHERE name LIKE ? ESCAPE '\'`
			args = []any{queryName}
		}
	} else {
		if kind != "" {
			sqlStr = `SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
					  start_byte, end_byte FROM symbols WHERE name = ? AND kind = ?`
			args = []any{queryName, kind}
		} else {
			sqlStr = `SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
					  start_byte, end_byte FROM symbols WHERE name = ?`
			args = []any{queryName}
		}
	}

	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("finding symbols %q: %w", name, err)
	}
	defer rows.Close()

	return scanSymbolRows(rows)
}

// SymbolsInFile returns all symbols in a file.
func (s *Store) SymbolsInFile(path string) ([]SymbolRecord, error) {
	rows, err := s.db.Query(
		`SELECT name, kind, file_path, start_line, start_col, end_line, end_col,
		 start_byte, end_byte FROM symbols WHERE file_path = ?`,
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("listing symbols in %s: %w", path, err)
	}
	defer rows.Close()

	return scanSymbolRows(rows)
}

func scanSymbolRows(rows *sql.Rows) ([]SymbolRecord, error) {
	var records []SymbolRecord
	for rows.Next() {
		var r SymbolRecord
		if err := rows.Scan(
			&r.Name, &r.Kind, &r.FilePath,
			&r.StartLine, &r.StartCol, &r.EndLine, &r.EndCol,
			&r.StartByte, &r.EndByte,
		); err != nil {
			return nil, fmt.Errorf("scanning symbol row: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// --- Recipes ---

// SaveRecipe saves or updates a recipe.
func (s *Store) SaveRecipe(name, description string, params []string, steps json.RawMessage) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("serialising recipe params: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO recipes (name, description, params_json, steps_json)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			params_json = excluded.params_json,
			steps_json = excluded.steps_json`,
		name, description, string(paramsJSON), string(steps),
	)
	if err != nil {
		return fmt.Errorf("saving recipe %q: %w", name, err)
	}
	return nil
}

// LoadRecipe loads a recipe by name. Returns nil if not found.
func (s *Store) LoadRecipe(name string) (*Recipe, error) {
	var paramsJSON, stepsJSON, description string
	err := s.db.QueryRow(
		"SELECT params_json, steps_json, description FROM recipes WHERE name = ?",
		name,
	).Scan(&paramsJSON, &stepsJSON, &description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading recipe %q: %w", name, err)
	}

	var params []string
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return nil, fmt.Errorf("deserialising recipe params: %w", err)
	}

	return &Recipe{
		Name:        name,
		Description: description,
		Params:      params,
		Steps:       json.RawMessage(stepsJSON),
	}, nil
}

// ListRecipes returns all recipe names with descriptions.
func (s *Store) ListRecipes() ([]Recipe, error) {
	rows, err := s.db.Query("SELECT name, description FROM recipes ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing recipes: %w", err)
	}
	defer rows.Close()

	var recipes []Recipe
	for rows.Next() {
		var r Recipe
		if err := rows.Scan(&r.Name, &r.Description); err != nil {
			return nil, fmt.Errorf("reading recipe row: %w", err)
		}
		recipes = append(recipes, r)
	}
	return recipes, rows.Err()
}

// DeleteRecipe deletes a recipe. Returns true if a recipe was deleted.
func (s *Store) DeleteRecipe(name string) (bool, error) {
	result, err := s.db.Exec("DELETE FROM recipes WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("deleting recipe %q: %w", name, err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// --- Conventions ---

// SaveConvention saves or updates a convention.
func (s *Store) SaveConvention(name, description, checkProgram string) error {
	_, err := s.db.Exec(
		`INSERT INTO conventions (name, description, check_program)
		 VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			check_program = excluded.check_program`,
		name, description, checkProgram,
	)
	if err != nil {
		return fmt.Errorf("saving convention %q: %w", name, err)
	}
	return nil
}

// ListConventions returns all conventions.
func (s *Store) ListConventions() ([]Convention, error) {
	rows, err := s.db.Query("SELECT name, description, check_program FROM conventions ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing conventions: %w", err)
	}
	defer rows.Close()

	var conventions []Convention
	for rows.Next() {
		var c Convention
		if err := rows.Scan(&c.Name, &c.Description, &c.CheckProgram); err != nil {
			return nil, fmt.Errorf("reading convention row: %w", err)
		}
		conventions = append(conventions, c)
	}
	return conventions, rows.Err()
}

// DeleteConvention deletes a convention. Returns true if one was deleted.
func (s *Store) DeleteConvention(name string) (bool, error) {
	result, err := s.db.Exec("DELETE FROM conventions WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("deleting convention %q: %w", name, err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// --- Invariants ---

// Invariant is a saved structural invariant check.
type Invariant struct {
	Name        string
	Description string
	RuleJSON    string
}

// SaveInvariant saves or updates an invariant.
func (s *Store) SaveInvariant(name, description, ruleJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO invariants (name, description, rule_json)
		 VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			rule_json = excluded.rule_json`,
		name, description, ruleJSON,
	)
	if err != nil {
		return fmt.Errorf("saving invariant %q: %w", name, err)
	}
	return nil
}

// ListInvariants returns all invariants.
func (s *Store) ListInvariants() ([]Invariant, error) {
	rows, err := s.db.Query("SELECT name, description, rule_json FROM invariants ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing invariants: %w", err)
	}
	defer rows.Close()

	var invariants []Invariant
	for rows.Next() {
		var inv Invariant
		if err := rows.Scan(&inv.Name, &inv.Description, &inv.RuleJSON); err != nil {
			return nil, fmt.Errorf("reading invariant row: %w", err)
		}
		invariants = append(invariants, inv)
	}
	return invariants, rows.Err()
}

// DeleteInvariant deletes an invariant. Returns true if one was deleted.
func (s *Store) DeleteInvariant(name string) (bool, error) {
	result, err := s.db.Exec("DELETE FROM invariants WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("deleting invariant %q: %w", name, err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// LoadInvariant loads an invariant by name. Returns nil if not found.
func (s *Store) LoadInvariant(name string) (*Invariant, error) {
	var inv Invariant
	err := s.db.QueryRow(
		"SELECT name, description, rule_json FROM invariants WHERE name = ?",
		name,
	).Scan(&inv.Name, &inv.Description, &inv.RuleJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading invariant %q: %w", name, err)
	}
	return &inv, nil
}
