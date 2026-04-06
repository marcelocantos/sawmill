// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package forest provides types and functions for loading, parsing, and
// manipulating collections of source files via Tree-sitter.
package forest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
)

// ParsedFile represents a single parsed source file.
type ParsedFile struct {
	Path           string
	OriginalSource []byte
	Tree           *tree_sitter.Tree
	Adapter        adapters.LanguageAdapter
}

// FileChange represents a pending file change (original + new content).
type FileChange struct {
	Path      string
	Original  []byte
	NewSource []byte
}

// Diff returns a unified diff between the original and new content.
// A diffFn is accepted to avoid a circular import between forest and rewrite.
// Callers typically pass rewrite.UnifiedDiff.
func (fc *FileChange) Diff(diffFn func(path string, original, newContent []byte) string) string {
	return diffFn(fc.Path, fc.Original, fc.NewSource)
}

// Apply writes the new content directly to the file's path.
func (fc *FileChange) Apply() error {
	if err := os.WriteFile(fc.Path, fc.NewSource, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", fc.Path, err)
	}
	return nil
}

// Forest is a collection of parsed source files.
type Forest struct {
	Files []*ParsedFile
}

// skipDirs lists directory names that are always skipped during a walk.
var skipDirs = map[string]bool{
	"node_modules": true,
	"target":       true,
	"__pycache__":  true,
	".git":         true,
	".svn":         true,
	".hg":          true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return skipDirs[name]
}

// FromPath parses all recognised source files under path (file or directory).
// Directory walks skip hidden directories and well-known build/dependency dirs.
func FromPath(path string) (*Forest, error) {
	var files []*ParsedFile

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	if !info.IsDir() {
		parsed, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		if parsed != nil {
			files = append(files, parsed)
		}
		return &Forest{Files: files}, nil
	}

	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		parsed, err := parseFile(p)
		if err != nil {
			return err
		}
		if parsed != nil {
			files = append(files, parsed)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", path, err)
	}

	return &Forest{Files: files}, nil
}

// parseFile reads and parses a single file, returning nil if the extension
// is not recognised by any adapter.
func parseFile(path string) (*ParsedFile, error) {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return nil, nil
	}

	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		return nil, nil
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(adapter.Language()); err != nil {
		return nil, fmt.Errorf("setting language for %s: %w", path, err)
	}

	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parsing %s: tree-sitter returned nil tree", path)
	}

	return &ParsedFile{
		Path:           path,
		OriginalSource: source,
		Tree:           tree,
		Adapter:        adapter,
	}, nil
}

// String returns a human-readable summary of the forest.
func (f *Forest) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Forest: %d file(s)\n", len(f.Files))
	for _, file := range f.Files {
		status := ""
		if file.Tree.RootNode().HasError() {
			status = " [parse errors]"
		}
		fmt.Fprintf(&sb, "  %s%s\n", file.Path, status)
	}
	return sb.String()
}

// QueryResult is a single query match result.
type QueryResult struct {
	Path      string
	StartLine uint
	StartCol  uint
	Kind      string
	Name      string // empty if not applicable
	Text      string
}

// ApplyWithBackup applies a set of file changes atomically with backup files.
//
// Strategy:
//  1. Write all new content to ".sawmill.new" temp files.
//  2. Back up all originals to ".sawmill.bak".
//  3. Rename ".new" files to their final paths.
//
// If anything fails during step 3 the ".bak" files allow recovery.
// Returns the list of backup paths on success.
func ApplyWithBackup(changes []FileChange) ([]string, error) {
	tempPaths := make([]string, 0, len(changes))
	backupPaths := make([]string, 0, len(changes))

	// Step 1: Write new content to temp files.
	for _, change := range changes {
		temp := replaceExt(change.Path, "sawmill.new")
		if err := os.WriteFile(temp, change.NewSource, 0o644); err != nil {
			return nil, fmt.Errorf("writing temp %s: %w", temp, err)
		}
		tempPaths = append(tempPaths, temp)
	}

	// Step 2: Back up originals.
	for _, change := range changes {
		backup := replaceExt(change.Path, "sawmill.bak")
		if _, err := os.Stat(change.Path); err == nil {
			if err := copyFile(change.Path, backup); err != nil {
				return nil, fmt.Errorf("backing up %s: %w", change.Path, err)
			}
		} else {
			// New file — write an empty marker so undo knows to delete it.
			if err := os.WriteFile(backup, []byte{}, 0o644); err != nil {
				return nil, fmt.Errorf("creating backup marker %s: %w", backup, err)
			}
		}
		backupPaths = append(backupPaths, backup)
	}

	// Step 3: Rename temp files to final paths.
	for i, change := range changes {
		// Ensure parent directory exists (for new files).
		if parent := filepath.Dir(change.Path); parent != "" {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, fmt.Errorf("creating directory %s: %w", parent, err)
			}
		}
		if err := os.Rename(tempPaths[i], change.Path); err != nil {
			return nil, fmt.Errorf("renaming temp to %s: %w", change.Path, err)
		}
	}

	return backupPaths, nil
}

// UndoFromBackups restores files from their ".sawmill.bak" backups.
// Returns the number of files successfully restored.
func UndoFromBackups(backupPaths []string) (int, error) {
	restored := 0
	for _, backup := range backupPaths {
		const suffix = ".sawmill.bak"
		if !strings.HasSuffix(backup, suffix) {
			continue
		}
		original := strings.TrimSuffix(backup, suffix)

		if _, err := os.Stat(backup); os.IsNotExist(err) {
			continue
		}

		backupContent, err := os.ReadFile(backup)
		if err != nil {
			return restored, fmt.Errorf("reading backup %s: %w", backup, err)
		}

		_, origErr := os.Stat(original)
		origExists := !os.IsNotExist(origErr)

		if len(backupContent) == 0 && !origExists {
			// Empty marker for a file that was newly created — nothing to restore.
			_ = os.Remove(backup)
			continue
		}

		if len(backupContent) == 0 {
			// File was newly created — remove it.
			_ = os.Remove(original)
		} else {
			// Restore original content.
			if err := os.WriteFile(original, backupContent, 0o644); err != nil {
				return restored, fmt.Errorf("restoring %s: %w", original, err)
			}
		}

		_ = os.Remove(backup)
		restored++
	}
	return restored, nil
}

// CleanupBackups removes backup files after the user has confirmed the
// applied changes are good. Stale ".sawmill.new" files are also removed.
func CleanupBackups(backupPaths []string) {
	for _, backup := range backupPaths {
		_ = os.Remove(backup)
	}
	for _, backup := range backupPaths {
		newPath := strings.ReplaceAll(backup, ".sawmill.bak", ".sawmill.new")
		_ = os.Remove(newPath)
	}
}

// replaceExt replaces the file extension of path with newExt.
// e.g. replaceExt("foo/bar.go", "sawmill.bak") → "foo/bar.sawmill.bak"
func replaceExt(path, newExt string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + "." + newExt
}

// copyFile copies src to dst, preserving content and permissions.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}
