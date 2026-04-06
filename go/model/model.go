// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package model provides the persistent codebase model that ties together the
// forest (in-memory parsed files), SQLite store (persistent metadata and symbol
// index), and file watching (planned). The MCP server holds a CodebaseModel and
// all tool calls operate against it.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/index"
	"github.com/marcelocantos/sawmill/store"
	"github.com/marcelocantos/sawmill/watcher"
)

// CodebaseModel is a persistent, live-updating codebase model.
type CodebaseModel struct {
	// Root directory being tracked.
	Root string
	// In-memory parsed files.
	Forest *forest.Forest
	// SQLite-backed persistent store.
	Store *store.Store
	// w watches root for file changes (nil for ephemeral models).
	w *watcher.Watcher
	// events is the channel of debounced file events from w.
	events <-chan watcher.FileEvent
}

// Load loads a codebase model for the given directory.
//
//  1. Opens (or creates) the SQLite store at {root}/.sawmill/store.db
//  2. Walks the directory, checks each file against the store
//  3. Re-parses only files that have changed (mtime or content hash mismatch)
//  4. Builds the symbol index for changed files
func Load(root string) (*CodebaseModel, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", root, err)
	}

	// Ensure .sawmill directory exists.
	storeDir := filepath.Join(absRoot, ".sawmill")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", storeDir, err)
	}

	s, err := store.Open(filepath.Join(storeDir, "store.db"))
	if err != nil {
		return nil, err
	}

	f, err := incrementalParse(absRoot, s)
	if err != nil {
		s.Close()
		return nil, err
	}

	w, events, err := watcher.Watch(absRoot)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("starting watcher: %w", err)
	}

	return &CodebaseModel{Root: absRoot, Forest: f, Store: s, w: w, events: events}, nil
}

// LoadEphemeral loads without persistence (for testing or one-shot CLI use).
func LoadEphemeral(root string) (*CodebaseModel, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}

	s, err := store.OpenInMemory()
	if err != nil {
		return nil, err
	}

	f, err := forest.FromPath(absRoot)
	if err != nil {
		s.Close()
		return nil, err
	}

	// Index all files.
	for _, file := range f.Files {
		symbols := index.ExtractSymbols(file)
		records := symbolsToRecords(symbols, file.Path)
		_ = s.UpdateSymbols(file.Path, records)
	}

	return &CodebaseModel{Root: absRoot, Forest: f, Store: s}, nil
}

// Close releases the store's database connection and stops the file watcher.
func (m *CodebaseModel) Close() error {
	if m.w != nil {
		_ = m.w.Close()
	}
	return m.Store.Close()
}

// FileCount returns the number of tracked files.
func (m *CodebaseModel) FileCount() int {
	return len(m.Forest.Files)
}

// FindSymbols finds symbols by name, using the persistent index.
func (m *CodebaseModel) FindSymbols(name, kind string) ([]store.SymbolRecord, error) {
	return m.Store.FindSymbols(name, kind)
}

// incrementalParse walks the directory and parses files, using the store to
// skip unchanged files.
func incrementalParse(root string, s *store.Store) (*forest.Forest, error) {
	var files []*forest.ParsedFile

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		if ext == "" {
			return nil
		}

		adapter := adapters.ForExtension(ext)
		if adapter == nil {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		mtime := info.ModTime()
		contentHash := hashBytes(source)

		// Check if cached and unchanged.
		storedHash, _ := s.CheckFile(path, mtime)
		isCached := storedHash == contentHash && storedHash != ""

		parser := tree_sitter.NewParser()
		defer parser.Close()

		if err := parser.SetLanguage(adapter.Language()); err != nil {
			return fmt.Errorf("setting language for %s: %w", path, err)
		}

		tree := parser.Parse(source, nil)
		if tree == nil {
			return fmt.Errorf("parsing %s: tree-sitter returned nil", path)
		}

		parsed := &forest.ParsedFile{
			Path:           path,
			OriginalSource: source,
			Tree:           tree,
			Adapter:        adapter,
		}

		// Always parse into memory; only update store if changed.
		if !isCached {
			_ = s.UpsertFile(path, ext, mtime, contentHash)
			symbols := index.ExtractSymbols(parsed)
			records := symbolsToRecords(symbols, path)
			_ = s.UpdateSymbols(path, records)
		}

		files = append(files, parsed)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}

	return &forest.Forest{Files: files}, nil
}

// ParseAndIndexFile re-parses a single file and updates the store.
func (m *CodebaseModel) ParseAndIndexFile(path string) (*forest.ParsedFile, error) {
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

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	mtime := info.ModTime()
	contentHash := hashBytes(source)

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(adapter.Language()); err != nil {
		return nil, fmt.Errorf("setting language for %s: %w", path, err)
	}

	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parsing %s: tree-sitter returned nil", path)
	}

	parsed := &forest.ParsedFile{
		Path:           path,
		OriginalSource: source,
		Tree:           tree,
		Adapter:        adapter,
	}

	_ = m.Store.UpsertFile(path, ext, mtime, contentHash)
	symbols := index.ExtractSymbols(parsed)
	records := symbolsToRecords(symbols, path)
	_ = m.Store.UpdateSymbols(path, records)

	return parsed, nil
}

// Sync drains pending file events from the watcher and re-parses changed files.
// It is safe to call Sync from multiple goroutines but events are processed
// sequentially. Returns nil if there is no watcher (ephemeral model).
func (m *CodebaseModel) Sync() error {
	if m.events == nil {
		return nil
	}
	for {
		select {
		case ev, ok := <-m.events:
			if !ok {
				return nil
			}
			if err := m.applyEvent(ev); err != nil {
				// Log but continue processing remaining events.
				_ = err
			}
		default:
			return nil
		}
	}
}

// applyEvent updates the in-memory forest and store for a single file event.
func (m *CodebaseModel) applyEvent(ev watcher.FileEvent) error {
	switch ev.Kind {
	case watcher.Removed:
		// Remove the file from the forest.
		newFiles := m.Forest.Files[:0]
		for _, f := range m.Forest.Files {
			if f.Path != ev.Path {
				newFiles = append(newFiles, f)
			}
		}
		m.Forest.Files = newFiles
		_ = m.Store.RemoveFile(ev.Path)
	case watcher.Created, watcher.Modified:
		parsed, err := m.ParseAndIndexFile(ev.Path)
		if err != nil {
			return err
		}
		if parsed == nil {
			return nil
		}
		// Replace or append in the forest.
		replaced := false
		for i, f := range m.Forest.Files {
			if f.Path == ev.Path {
				m.Forest.Files[i] = parsed
				replaced = true
				break
			}
		}
		if !replaced {
			m.Forest.Files = append(m.Forest.Files, parsed)
		}
	}
	return nil
}

// skipDirs lists directory names that are always skipped.
var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "__pycache__": true,
	".git": true, ".svn": true, ".hg": true,
	"vendor": true, "dist": true, "build": true,
	".idea": true, ".vscode": true, ".sawmill": true,
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return skipDirs[name]
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func symbolsToRecords(symbols []index.Symbol, filePath string) []store.SymbolRecord {
	records := make([]store.SymbolRecord, len(symbols))
	for i, s := range symbols {
		records[i] = store.SymbolRecord{
			Name:      s.Name,
			Kind:      s.Kind,
			FilePath:  filePath,
			StartLine: s.StartLine,
			StartCol:  s.StartCol,
			EndLine:   s.EndLine,
			EndCol:    s.EndCol,
			StartByte: int(s.StartByte),
			EndByte:   int(s.EndByte),
		}
	}
	return records
}

// SaveRecipe delegates to the store.
func (m *CodebaseModel) SaveRecipe(name, description string, params []string, steps []byte) error {
	return m.Store.SaveRecipe(name, description, params, steps)
}

// LoadRecipe delegates to the store.
func (m *CodebaseModel) LoadRecipe(name string) (*store.Recipe, error) {
	return m.Store.LoadRecipe(name)
}

// ListRecipes delegates to the store.
func (m *CodebaseModel) ListRecipes() ([]store.Recipe, error) {
	return m.Store.ListRecipes()
}

// SaveConvention delegates to the store.
func (m *CodebaseModel) SaveConvention(name, description, checkProgram string) error {
	return m.Store.SaveConvention(name, description, checkProgram)
}

// ListConventions delegates to the store.
func (m *CodebaseModel) ListConventions() ([]store.Convention, error) {
	return m.Store.ListConventions()
}

// DeleteConvention delegates to the store.
func (m *CodebaseModel) DeleteConvention(name string) (bool, error) {
	return m.Store.DeleteConvention(name)
}

// SummaryByLanguage returns a map of language → file count.
func (m *CodebaseModel) SummaryByLanguage() map[string]int {
	summary := make(map[string]int)
	for _, file := range m.Forest.Files {
		lang := file.Adapter.LSPLanguageID()
		if lang == "" {
			lang = "unknown"
		}
		summary[lang]++
	}
	return summary
}

// MtimeForPath returns the modification time for the given path. Used by
// incremental parsing. Returns zero time on error.
func MtimeForPath(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
