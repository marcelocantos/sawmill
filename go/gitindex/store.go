// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package gitindex provides SQLite-backed relational storage for git-indexed
// Tree-sitter ASTs. Unlike the working-directory store, this package indexes
// blobs by their git SHA, enabling deduplication across commits and structural
// SQL queries over the parse tree.
package gitindex

import (
	"database/sql"
	"fmt"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	_ "modernc.org/sqlite" // SQLite driver registration.
)

// CommitFile associates a file path with its blob SHA at a given commit.
type CommitFile struct {
	FilePath string
	BlobSHA  string
}

// Store is a SQLite-backed store for git-indexed AST data.
type Store struct {
	db *sql.DB

	// In-memory caches to avoid repeated DB round-trips for interning.
	nodeTypeCache  map[string]int64
	fieldNameCache map[string]int64
}

// Open opens or creates the store at the given file path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening gitindex store at %s: %w", path, err)
	}
	// Serialise through one connection so busy_timeout and other PRAGMAs
	// set in init() apply to every query. SQLite is single-writer anyway.
	db.SetMaxOpenConns(1)
	s := &Store{
		db:             db,
		nodeTypeCache:  make(map[string]int64),
		fieldNameCache: make(map[string]int64),
	}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens an in-memory store, suitable for testing.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory gitindex store: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{
		db:             db,
		nodeTypeCache:  make(map[string]int64),
		fieldNameCache: make(map[string]int64),
	}
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
		PRAGMA busy_timeout=5000;

		CREATE TABLE IF NOT EXISTS node_types (
			id   INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL
		);

		CREATE TABLE IF NOT EXISTS field_names (
			id   INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL
		);

		CREATE TABLE IF NOT EXISTS blobs (
			sha      TEXT PRIMARY KEY,
			language TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS nodes (
			id            INTEGER PRIMARY KEY,
			blob_sha      TEXT    NOT NULL REFERENCES blobs(sha) ON DELETE CASCADE,
			parent_id     INTEGER REFERENCES nodes(id),
			node_type_id  INTEGER NOT NULL REFERENCES node_types(id),
			field_name_id INTEGER REFERENCES field_names(id),
			start_byte    INTEGER NOT NULL,
			end_byte      INTEGER NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_nodes_blob   ON nodes(blob_sha);
		CREATE INDEX IF NOT EXISTS idx_nodes_type   ON nodes(node_type_id);
		CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id);

		CREATE TABLE IF NOT EXISTS commit_files (
			commit_sha TEXT NOT NULL,
			file_path  TEXT NOT NULL,
			blob_sha   TEXT NOT NULL,
			PRIMARY KEY (commit_sha, file_path)
		);

		CREATE INDEX IF NOT EXISTS idx_commit_files_blob ON commit_files(blob_sha);

		CREATE TABLE IF NOT EXISTS indexed_commits (
			sha        TEXT PRIMARY KEY,
			indexed_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		return fmt.Errorf("initialising gitindex schema: %w", err)
	}

	// Warm the interning caches from any existing rows.
	if err := s.loadNodeTypeCache(); err != nil {
		return err
	}
	if err := s.loadFieldNameCache(); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadNodeTypeCache() error {
	rows, err := s.db.Query("SELECT id, name FROM node_types")
	if err != nil {
		return fmt.Errorf("loading node_types cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("scanning node_types row: %w", err)
		}
		s.nodeTypeCache[name] = id
	}
	return rows.Err()
}

func (s *Store) loadFieldNameCache() error {
	rows, err := s.db.Query("SELECT id, name FROM field_names")
	if err != nil {
		return fmt.Errorf("loading field_names cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("scanning field_names row: %w", err)
		}
		s.fieldNameCache[name] = id
	}
	return rows.Err()
}

// internNodeType returns the id for a node type name, inserting it if needed.
// Must be called within a transaction; tx is used for inserts, but selects
// use the tx too so they see the in-progress inserts.
func (s *Store) internNodeType(tx *sql.Tx, name string) (int64, error) {
	if id, ok := s.nodeTypeCache[name]; ok {
		return id, nil
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO node_types (name) VALUES (?)", name); err != nil {
		return 0, fmt.Errorf("inserting node_type %q: %w", name, err)
	}
	var id int64
	if err := tx.QueryRow("SELECT id FROM node_types WHERE name = ?", name).Scan(&id); err != nil {
		return 0, fmt.Errorf("selecting node_type id for %q: %w", name, err)
	}
	s.nodeTypeCache[name] = id
	return id, nil
}

// internFieldName returns the id for a field name, inserting if needed.
func (s *Store) internFieldName(tx *sql.Tx, name string) (int64, error) {
	if id, ok := s.fieldNameCache[name]; ok {
		return id, nil
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO field_names (name) VALUES (?)", name); err != nil {
		return 0, fmt.Errorf("inserting field_name %q: %w", name, err)
	}
	var id int64
	if err := tx.QueryRow("SELECT id FROM field_names WHERE name = ?", name).Scan(&id); err != nil {
		return 0, fmt.Errorf("selecting field_name id for %q: %w", name, err)
	}
	s.fieldNameCache[name] = id
	return id, nil
}

// IsIndexed reports whether the blob with the given SHA has already been
// indexed into the blobs table.
func (s *Store) IsIndexed(blobSHA string) (bool, error) {
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM blobs WHERE sha = ?)", blobSHA).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking if blob %s is indexed: %w", blobSHA, err)
	}
	return exists, nil
}

// IndexBlob stores the parse tree for a blob into the database.
// It walks the tree depth-first using a TreeCursor, inserting every node.
// If the blob is already indexed, it is a no-op.
func (s *Store) IndexBlob(blobSHA, language string, tree *tree_sitter.Tree) error {
	already, err := s.IsIndexed(blobSHA)
	if err != nil {
		return err
	}
	if already {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for IndexBlob: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// INSERT OR IGNORE — a concurrent indexer may have inserted this blob
	// between our IsIndexed check above and now. If RowsAffected is 0, the
	// blob already exists; skip walking nodes (the other indexer is doing it).
	res, err := tx.Exec("INSERT OR IGNORE INTO blobs (sha, language) VALUES (?, ?)", blobSHA, language)
	if err != nil {
		return fmt.Errorf("inserting blob %s: %w", blobSHA, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking insert for blob %s: %w", blobSHA, err)
	}
	if n == 0 {
		return nil
	}

	if err := s.walkAndInsertNodes(tx, blobSHA, tree); err != nil {
		return err
	}

	return tx.Commit()
}

// walkAndInsertNodes walks the tree depth-first and inserts every node.
func (s *Store) walkAndInsertNodes(tx *sql.Tx, blobSHA string, tree *tree_sitter.Tree) error {
	cursor := tree.Walk()
	defer cursor.Close()

	// parentStack tracks the database id of the ancestor nodes as we descend.
	// A nil pointer means "root level" (no parent).
	type frame struct {
		dbID int64
	}
	var parentStack []frame

	for {
		node := cursor.Node()
		fieldName := cursor.FieldName()
		nodeKind := node.Kind()
		startByte := node.StartByte()
		endByte := node.EndByte()

		// Resolve interned IDs.
		nodeTypeID, err := s.internNodeType(tx, nodeKind)
		if err != nil {
			return err
		}

		var fieldNameID sql.NullInt64
		if fieldName != "" {
			id, err := s.internFieldName(tx, fieldName)
			if err != nil {
				return err
			}
			fieldNameID = sql.NullInt64{Int64: id, Valid: true}
		}

		var parentID sql.NullInt64
		if len(parentStack) > 0 {
			parentID = sql.NullInt64{Int64: parentStack[len(parentStack)-1].dbID, Valid: true}
		}

		result, err := tx.Exec(
			`INSERT INTO nodes (blob_sha, parent_id, node_type_id, field_name_id, start_byte, end_byte)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			blobSHA, parentID, nodeTypeID, fieldNameID, startByte, endByte,
		)
		if err != nil {
			return fmt.Errorf("inserting node %q for blob %s: %w", nodeKind, blobSHA, err)
		}
		nodeDBID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("getting inserted node id: %w", err)
		}

		// Try to go deeper.
		if cursor.GotoFirstChild() {
			parentStack = append(parentStack, frame{dbID: nodeDBID})
			continue
		}

		// No children — try next sibling, unwinding as needed.
		for {
			if cursor.GotoNextSibling() {
				break
			}
			if !cursor.GotoParent() {
				// Back at root; done.
				return nil
			}
			if len(parentStack) > 0 {
				parentStack = parentStack[:len(parentStack)-1]
			}
		}
	}
}

// IsCommitIndexed reports whether the given commit SHA has been fully indexed.
func (s *Store) IsCommitIndexed(sha string) (bool, error) {
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM indexed_commits WHERE sha = ?)", sha).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking if commit %s is indexed: %w", sha, err)
	}
	return exists, nil
}

// MarkCommitIndexed records that the given commit SHA has been fully indexed.
func (s *Store) MarkCommitIndexed(sha string) error {
	if _, err := s.db.Exec("INSERT OR IGNORE INTO indexed_commits (sha) VALUES (?)", sha); err != nil {
		return fmt.Errorf("marking commit %s as indexed: %w", sha, err)
	}
	return nil
}

// RegisterCommitFiles inserts commit-to-file-to-blob mappings.
// Uses INSERT OR IGNORE so re-indexing the same commit is safe.
func (s *Store) RegisterCommitFiles(commitSHA string, files []CommitFile) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for RegisterCommitFiles: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		"INSERT OR IGNORE INTO commit_files (commit_sha, file_path, blob_sha) VALUES (?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("preparing commit_files insert: %w", err)
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(commitSHA, f.FilePath, f.BlobSHA); err != nil {
			return fmt.Errorf("inserting commit_file %s/%s: %w", commitSHA, f.FilePath, err)
		}
	}
	return tx.Commit()
}

// BlobSHAForFile returns the blob SHA for a given (commit, path) pair.
// The second return value is false if no such row exists.
func (s *Store) BlobSHAForFile(commitSHA, filePath string) (string, bool, error) {
	var sha string
	err := s.db.QueryRow(
		"SELECT blob_sha FROM commit_files WHERE commit_sha = ? AND file_path = ?",
		commitSHA, filePath,
	).Scan(&sha)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("looking up blob for %s/%s: %w", commitSHA, filePath, err)
	}
	return sha, true, nil
}

// CommitFiles returns all files recorded for a commit.
func (s *Store) CommitFiles(commitSHA string) ([]CommitFile, error) {
	rows, err := s.db.Query(
		"SELECT file_path, blob_sha FROM commit_files WHERE commit_sha = ? ORDER BY file_path",
		commitSHA,
	)
	if err != nil {
		return nil, fmt.Errorf("listing commit files for %s: %w", commitSHA, err)
	}
	defer rows.Close()

	var files []CommitFile
	for rows.Next() {
		var f CommitFile
		if err := rows.Scan(&f.FilePath, &f.BlobSHA); err != nil {
			return nil, fmt.Errorf("scanning commit_file row: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
