// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package paths computes central storage paths for sawmill data.
// All persistent state lives under ~/.sawmill/ — nothing is written
// into project directories.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// Base returns the sawmill data directory (~/.sawmill).
func Base() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".sawmill"
	}
	return filepath.Join(home, ".sawmill")
}

// rootHash returns a short hex hash of the project root for use as a
// directory name. Uses the first 16 hex chars of SHA-256 (64 bits) —
// collision-resistant enough for local directory naming.
func rootHash(root string) string {
	h := sha256.Sum256([]byte(root))
	return hex.EncodeToString(h[:8])
}

// DefaultListenAddr returns the default HTTP listen address for the
// sawmill MCP server.
const DefaultListenAddr = "127.0.0.1:8765"

// StoreDir returns the directory for a project's SQLite store.
// e.g. ~/.sawmill/stores/a1b2c3d4e5f6a7b8/
func StoreDir(root string) string {
	return filepath.Join(Base(), "stores", rootHash(root))
}

// StorePath returns the path to a project's SQLite database.
// e.g. ~/.sawmill/stores/a1b2c3d4e5f6a7b8/store.db
func StorePath(root string) string {
	return filepath.Join(StoreDir(root), "store.db")
}

// BackupDir returns the directory for a project's file backups.
// e.g. ~/.sawmill/backups/a1b2c3d4e5f6a7b8/
func BackupDir(root string) string {
	return filepath.Join(Base(), "backups", rootHash(root))
}

// NewApplyDir creates and returns a fresh, uniquely-named staging/backup
// directory for a single apply operation, e.g.
// ~/.sawmill/backups/<roothash>/apply-1234567890.
//
// Uniqueness matters for correctness, not just tidiness: a previous design
// derived staging and backup paths deterministically from (root, relpath), so
// two concurrent (or repeated) applies touching the same file collided on the
// same .new/.bak paths — last-writer-wins corrupted backups and broke undo.
// A per-apply directory guarantees every in-flight apply owns its own staging
// and backup files, so no two applies can clobber each other's originals.
func NewApplyDir(root string) (string, error) {
	base := BackupDir(root)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	// MkdirTemp guarantees a unique directory name even under concurrency.
	return os.MkdirTemp(base, "apply-")
}
