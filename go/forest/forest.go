// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package forest provides types and functions for loading, parsing, and
// manipulating collections of source files via Tree-sitter.
package forest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"github.com/marcelocantos/sawmill/paths"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

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

// SkipFn decides whether to skip a directory during a walk. Returns true to
// skip. A nil SkipFn skips nothing.
type SkipFn func(absPath string) bool

// FromPath parses all recognised source files under path (file or directory).
// All directories are walked. Callers that want to skip directories should
// use FromPathSkip with a SkipFn (e.g. backed by scope.Classifier.ShouldSkipDir).
func FromPath(path string) (*Forest, error) {
	return FromPathSkip(path, nil)
}

// FromPathSkip is FromPath with a directory-skip predicate.
func FromPathSkip(path string, skip SkipFn) (*Forest, error) {
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
			if p != path && skip != nil && skip(p) {
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

// ErrStaleContent is returned by ApplyWithBackup when a target file's on-disk
// content no longer matches the FileChange.Original it was previewed against —
// i.e. the file changed between preview and apply (a concurrent apply, an
// external edit, or a watcher-observed reparse). Applying anyway would silently
// clobber the newer content (a lost update), so the whole apply is aborted.
var ErrStaleContent = errors.New("file changed on disk since preview")

// BackupEntry records everything needed to reverse one file's change on undo.
type BackupEntry struct {
	// OriginalPath is the absolute path of the changed file.
	OriginalPath string
	// BackupFile is the absolute path of the saved pre-apply copy. Empty when
	// Existed is false (the file was created by this apply and undo removes it).
	BackupFile string
	// Existed reports whether OriginalPath existed before the apply. Tracked
	// explicitly rather than inferred from empty backup content, so an
	// originally-empty file is restored to empty on undo instead of deleted.
	Existed bool
	// Mode is the original file mode, re-applied on both apply and undo so a
	// transform never silently strips (e.g.) the execute bit. Meaningful only
	// when Existed is true.
	Mode fs.FileMode
}

// ApplyManifest is the undo record produced by ApplyWithBackup: the per-apply
// staging/backup directory plus one entry per changed file.
type ApplyManifest struct {
	Dir     string
	Entries []BackupEntry
}

// ApplyWithBackup applies a set of file changes atomically with backups stored
// under a fresh per-apply directory inside ~/.sawmill/backups/ (never in the
// project tree).
//
// Strategy:
//  0. Pre-flight: verify each existing target still matches FileChange.Original
//     (optimistic-concurrency guard); abort the whole apply on mismatch before
//     writing anything.
//  1. Write all new content to staging files, carrying the original file mode.
//  2. Back up all originals, recording existence + mode in the manifest.
//  3. Rename staging files into place; on a partial failure, roll every
//     already-renamed file back to its pre-apply state.
//
// Returns the manifest (consumed by UndoFromBackups) on success.
func ApplyWithBackup(root string, changes []FileChange) (*ApplyManifest, error) {
	// Step 0: Optimistic-concurrency pre-flight. Read-only; nothing is written
	// until every target is confirmed to still match what the preview saw.
	for _, change := range changes {
		info, err := os.Stat(change.Path)
		if err != nil || info.IsDir() {
			continue // new file (or a directory we can't back up) — no baseline
		}
		current, rerr := os.ReadFile(change.Path)
		if rerr != nil {
			return nil, fmt.Errorf("reading %s for concurrency check: %w", change.Path, rerr)
		}
		if !bytes.Equal(current, change.Original) {
			return nil, fmt.Errorf("%s: %w", change.Path, ErrStaleContent)
		}
	}

	dir, err := paths.NewApplyDir(root)
	if err != nil {
		return nil, fmt.Errorf("creating apply dir: %w", err)
	}

	manifest := &ApplyManifest{Dir: dir}
	tempPaths := make([]string, len(changes))

	// Step 1: Write new content to staging files, preserving the original mode
	// so the post-rename inode keeps its permission bits.
	for i, change := range changes {
		mode := fs.FileMode(0o644)
		if info, err := os.Stat(change.Path); err == nil {
			mode = info.Mode().Perm()
		}
		temp := filepath.Join(dir, fmt.Sprintf("%d.new", i))
		if err := os.WriteFile(temp, change.NewSource, mode); err != nil {
			return nil, fmt.Errorf("writing temp %s: %w", temp, err)
		}
		tempPaths[i] = temp
	}

	// Step 2: Back up originals with explicit existence + mode.
	manifest.Entries = make([]BackupEntry, len(changes))
	for i, change := range changes {
		entry := BackupEntry{OriginalPath: change.Path}
		if info, err := os.Stat(change.Path); err == nil {
			entry.Existed = true
			entry.Mode = info.Mode().Perm()
			entry.BackupFile = filepath.Join(dir, fmt.Sprintf("%d.bak", i))
			if err := copyFile(change.Path, entry.BackupFile); err != nil {
				return nil, fmt.Errorf("backing up %s: %w", change.Path, err)
			}
		}
		manifest.Entries[i] = entry
	}

	// Step 3: Rename staging files into place. On any failure, restore every
	// file already renamed this pass so the tree is left in its pre-apply state.
	var renamed []int
	for i, change := range changes {
		if parent := filepath.Dir(change.Path); parent != "" {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				rollbackRenames(manifest, renamed)
				return nil, fmt.Errorf("creating directory %s: %w", parent, err)
			}
		}
		if err := os.Rename(tempPaths[i], change.Path); err != nil {
			rollbackRenames(manifest, renamed)
			return nil, fmt.Errorf("renaming temp to %s: %w", change.Path, err)
		}
		// os.Rename replaces the inode with the staging temp's; re-assert the
		// original mode exactly so umask can't alter it.
		if manifest.Entries[i].Existed {
			_ = os.Chmod(change.Path, manifest.Entries[i].Mode)
		}
		renamed = append(renamed, i)
	}

	return manifest, nil
}

// rollbackRenames reverses the already-completed renames of a failed apply,
// restoring each file from its backup (or removing it if it was newly created),
// then discards the per-apply directory. Best effort: rolling back a
// half-applied change must not itself abort partway and leave things worse.
func rollbackRenames(manifest *ApplyManifest, renamed []int) {
	for j := len(renamed) - 1; j >= 0; j-- {
		e := manifest.Entries[renamed[j]]
		if !e.Existed {
			_ = os.Remove(e.OriginalPath)
			continue
		}
		if data, err := os.ReadFile(e.BackupFile); err == nil {
			_ = os.WriteFile(e.OriginalPath, data, e.Mode)
			_ = os.Chmod(e.OriginalPath, e.Mode)
		}
	}
	_ = os.RemoveAll(manifest.Dir)
}

// UndoFromBackups restores every file recorded in the manifest to its
// pre-apply state and removes the per-apply directory. Files that did not
// exist before the apply are deleted; files that existed (including
// originally-empty ones) are rewritten with their saved content and mode.
// Returns the number of files successfully restored.
func UndoFromBackups(manifest *ApplyManifest) (int, error) {
	if manifest == nil {
		return 0, nil
	}
	restored := 0
	for i := len(manifest.Entries) - 1; i >= 0; i-- {
		e := manifest.Entries[i]
		if !e.Existed {
			// File was created by the apply — remove it.
			if err := os.Remove(e.OriginalPath); err != nil && !os.IsNotExist(err) {
				return restored, fmt.Errorf("removing %s: %w", e.OriginalPath, err)
			}
			restored++
			continue
		}
		data, err := os.ReadFile(e.BackupFile)
		if err != nil {
			return restored, fmt.Errorf("reading backup %s: %w", e.BackupFile, err)
		}
		if err := os.WriteFile(e.OriginalPath, data, e.Mode); err != nil {
			return restored, fmt.Errorf("restoring %s: %w", e.OriginalPath, err)
		}
		if err := os.Chmod(e.OriginalPath, e.Mode); err != nil {
			return restored, fmt.Errorf("chmod %s: %w", e.OriginalPath, err)
		}
		restored++
	}
	_ = os.RemoveAll(manifest.Dir)
	return restored, nil
}

// CleanupBackups removes a completed apply's staging/backup directory, called
// when the backups are no longer needed for undo.
func CleanupBackups(manifest *ApplyManifest) {
	if manifest == nil {
		return
	}
	_ = os.RemoveAll(manifest.Dir)
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
