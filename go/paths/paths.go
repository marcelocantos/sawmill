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

// SocketPath returns the Unix domain socket path for a project's daemon.
// e.g. ~/.sawmill/sockets/a1b2c3d4e5f6a7b8.sock
func SocketPath(root string) string {
	return filepath.Join(Base(), "sockets", rootHash(root)+".sock")
}

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

// BackupPath maps an original file path to its backup location under the
// central backup directory.
// e.g. /home/user/project/src/main.py → ~/.sawmill/backups/<hash>/src/main.py.bak
func BackupPath(root, originalPath string) string {
	rel, err := filepath.Rel(root, originalPath)
	if err != nil {
		// Fallback: use the full path hash.
		h := sha256.Sum256([]byte(originalPath))
		rel = hex.EncodeToString(h[:16])
	}
	return filepath.Join(BackupDir(root), rel+".bak")
}

// TempPath maps an original file path to a staging location for atomic writes.
// e.g. /home/user/project/src/main.py → ~/.sawmill/backups/<hash>/src/main.py.new
func TempPath(root, originalPath string) string {
	rel, err := filepath.Rel(root, originalPath)
	if err != nil {
		h := sha256.Sum256([]byte(originalPath))
		rel = hex.EncodeToString(h[:16])
	}
	return filepath.Join(BackupDir(root), rel+".new")
}

// OriginalPath recovers the original file path from a backup path.
// e.g. ~/.sawmill/backups/<hash>/src/main.py.bak → /root/src/main.py
func OriginalPath(root, backupPath string) string {
	rel, err := filepath.Rel(BackupDir(root), backupPath)
	if err != nil {
		return ""
	}
	// Strip .bak suffix.
	if len(rel) > 4 && rel[len(rel)-4:] == ".bak" {
		rel = rel[:len(rel)-4]
	}
	return filepath.Join(root, rel)
}
