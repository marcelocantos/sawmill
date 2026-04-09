// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package model provides the persistent codebase model. A manager goroutine
// owns all mutable state (the forest of parsed files) and serves snapshots to
// MCP handlers via a channel-based protocol. File-system changes are picked up
// by the watcher and applied by the manager — no explicit Sync is needed.
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
	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/paths"
	"github.com/marcelocantos/sawmill/store"
	"github.com/marcelocantos/sawmill/watcher"
)

// snapshotReq is sent by handlers to request a point-in-time forest snapshot.
type snapshotReq struct {
	reply chan *forest.Forest
}

// CodebaseModel is a persistent, live-updating codebase model. A manager
// goroutine owns the forest; handlers interact via ForestSnapshot and
// NotifyChanged.
type CodebaseModel struct {
	// Root directory being tracked.
	Root string
	// SQLite-backed persistent store. Safe for concurrent reads under WAL.
	Store *store.Store
	// LSP is the pool of language server clients (may be nil).
	LSP *lspclient.Pool
	// Cache holds recently-parsed trees for on-demand access.
	Cache *forest.TreeCache

	// forest is owned exclusively by the manager goroutine. Never access
	// directly from handlers — use ForestSnapshot().
	forest *forest.Forest
	// w watches root for file changes (nil for ephemeral models).
	w *watcher.Watcher
	// events is the channel of debounced file events from w.
	events <-chan watcher.FileEvent
	// snapshots receives snapshot requests from handlers.
	snapshots chan snapshotReq
	// notify receives post-apply re-parse requests (file paths).
	notify chan []string
	// done signals the manager goroutine to exit.
	done chan struct{}
	// stopped is closed when the manager goroutine exits.
	stopped chan struct{}
}

// Load loads a codebase model for the given directory.
//
//  1. Opens (or creates) the SQLite store at ~/.sawmill/stores/<hash>/store.db
//  2. Walks the directory, checks each file against the store
//  3. Re-parses only files that have changed (mtime or content hash mismatch)
//  4. Builds the symbol index for changed files
//  5. Starts the manager goroutine to process watcher events
func Load(root string) (*CodebaseModel, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", root, err)
	}

	storeDir := paths.StoreDir(absRoot)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", storeDir, err)
	}

	s, err := store.Open(paths.StorePath(absRoot))
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

	m := &CodebaseModel{
		Root:      absRoot,
		Store:     s,
		LSP:       lspclient.NewPool(),
		Cache:     forest.NewTreeCache(forest.DefaultCacheSize),
		forest:    f,
		w:         w,
		events:    events,
		snapshots: make(chan snapshotReq),
		notify:    make(chan []string, 16),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	go m.runManager()
	return m, nil
}

// LoadEphemeral loads without persistence (for testing or one-shot CLI use).
// No watcher or manager goroutine — ForestSnapshot returns the forest directly.
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

	for _, file := range f.Files {
		symbols := index.ExtractSymbols(file)
		records := symbolsToRecords(symbols, file.Path)
		_ = s.UpdateSymbols(file.Path, records)
	}

	return &CodebaseModel{Root: absRoot, Store: s, LSP: lspclient.NewPool(), Cache: forest.NewTreeCache(forest.DefaultCacheSize), forest: f}, nil
}

// Close stops the manager goroutine, the file watcher, LSP clients, and the
// store.
func (m *CodebaseModel) Close() error {
	if m.done != nil {
		close(m.done)
		<-m.stopped // wait for manager to exit
	}
	if m.LSP != nil {
		m.LSP.Close()
	}
	if m.Cache != nil {
		m.Cache.Clear()
	}
	if m.w != nil {
		_ = m.w.Close()
	}
	return m.Store.Close()
}

// FileAccessors returns a FileAccessor for each tracked file, optionally
// filtered by a path substring. These accessors load source from SQLite and
// parse trees on demand via the cache.
func (m *CodebaseModel) FileAccessors(pathFilter string) ([]*forest.FileAccessor, error) {
	records, err := m.Store.TrackedFilesWithMeta()
	if err != nil {
		return nil, err
	}

	var accessors []*forest.FileAccessor
	for _, rec := range records {
		if pathFilter != "" && !strings.Contains(rec.Path, pathFilter) {
			continue
		}
		adapter := adapters.ForExtension(rec.Language)
		if adapter == nil {
			continue
		}
		accessors = append(accessors, forest.NewFileAccessor(
			rec.Path, rec.Language, rec.ContentHash, adapter, m.Store, m.Cache,
		))
	}
	return accessors, nil
}

// ForestSnapshot returns a point-in-time snapshot of the parsed forest. The
// returned Forest has its own Files slice but shares the underlying ParsedFile
// values (which are immutable — replaced, not mutated, on re-parse).
//
// For ephemeral models (no manager goroutine), returns the forest directly.
func (m *CodebaseModel) ForestSnapshot() *forest.Forest {
	if m.snapshots == nil {
		// Ephemeral model — no manager, single-threaded access.
		return m.forest
	}
	req := snapshotReq{reply: make(chan *forest.Forest, 1)}
	m.snapshots <- req
	return <-req.reply
}

// NotifyChanged tells the manager to immediately re-parse the given paths.
// Call this after applying changes to disk so the model reflects the new
// content without waiting for the watcher's debounce delay.
func (m *CodebaseModel) NotifyChanged(changedPaths []string) {
	if m.notify == nil {
		return
	}
	select {
	case m.notify <- changedPaths:
	default:
		// Channel full — watcher will catch up.
	}
}

// FileCount returns the number of tracked files.
func (m *CodebaseModel) FileCount() int {
	return len(m.ForestSnapshot().Files)
}

// FindSymbols finds symbols by name, using the persistent index.
func (m *CodebaseModel) FindSymbols(name, kind string) ([]store.SymbolRecord, error) {
	return m.Store.FindSymbols(name, kind)
}

// SummaryByLanguage returns a map of language → file count.
func (m *CodebaseModel) SummaryByLanguage() map[string]int {
	snap := m.ForestSnapshot()
	summary := make(map[string]int)
	for _, file := range snap.Files {
		lang := file.Adapter.LSPLanguageID()
		if lang == "" {
			lang = "unknown"
		}
		summary[lang]++
	}
	return summary
}

// --- Manager goroutine ---

// runManager is the event loop that owns all mutable forest state. It
// processes watcher events, post-apply notifications, and snapshot requests.
func (m *CodebaseModel) runManager() {
	defer close(m.stopped)
	for {
		select {
		case ev, ok := <-m.events:
			if !ok {
				return
			}
			m.applyEvent(ev)
		case changedPaths := <-m.notify:
			for _, p := range changedPaths {
				m.reparse(p)
			}
		case req := <-m.snapshots:
			files := make([]*forest.ParsedFile, len(m.forest.Files))
			copy(files, m.forest.Files)
			req.reply <- &forest.Forest{Files: files}
		case <-m.done:
			return
		}
	}
}

// reparse re-parses a single file and updates the forest and store.
func (m *CodebaseModel) reparse(path string) {
	if m.Cache != nil {
		m.Cache.Evict(path)
	}
	parsed, err := m.parseAndIndexFile(path)
	if err != nil || parsed == nil {
		return
	}
	replaced := false
	for i, f := range m.forest.Files {
		if f.Path == path {
			m.forest.Files[i] = parsed
			replaced = true
			break
		}
	}
	if !replaced {
		m.forest.Files = append(m.forest.Files, parsed)
	}
}

// applyEvent updates the forest and store for a single file event.
func (m *CodebaseModel) applyEvent(ev watcher.FileEvent) {
	switch ev.Kind {
	case watcher.Removed:
		if m.Cache != nil {
			m.Cache.Evict(ev.Path)
		}
		// Allocate a new slice — outstanding snapshots hold references to
		// the old backing array.
		newFiles := make([]*forest.ParsedFile, 0, len(m.forest.Files))
		for _, f := range m.forest.Files {
			if f.Path != ev.Path {
				newFiles = append(newFiles, f)
			}
		}
		m.forest.Files = newFiles
		_ = m.Store.RemoveFile(ev.Path)
	case watcher.Created, watcher.Modified:
		m.reparse(ev.Path)
	}
}

// parseAndIndexFile re-parses a single file and updates the store.
func (m *CodebaseModel) parseAndIndexFile(path string) (*forest.ParsedFile, error) {
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

	_ = m.Store.UpsertFile(path, ext, mtime, contentHash, source)
	symbols := index.ExtractSymbols(parsed)
	records := symbolsToRecords(symbols, path)
	_ = m.Store.UpdateSymbols(path, records)

	return parsed, nil
}

// --- Static helpers ---

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

		if !isCached {
			_ = s.UpsertFile(path, ext, mtime, contentHash, source)
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

var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "__pycache__": true,
	".git": true, ".svn": true, ".hg": true,
	"vendor": true, "dist": true, "build": true,
	".idea": true, ".vscode": true,
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

// --- Store delegates ---

func (m *CodebaseModel) SaveRecipe(name, description string, params []string, steps []byte) error {
	return m.Store.SaveRecipe(name, description, params, steps)
}
func (m *CodebaseModel) LoadRecipe(name string) (*store.Recipe, error) {
	return m.Store.LoadRecipe(name)
}
func (m *CodebaseModel) ListRecipes() ([]store.Recipe, error) { return m.Store.ListRecipes() }
func (m *CodebaseModel) SaveConvention(name, description, checkProgram string) error {
	return m.Store.SaveConvention(name, description, checkProgram)
}
func (m *CodebaseModel) ListConventions() ([]store.Convention, error) {
	return m.Store.ListConventions()
}
func (m *CodebaseModel) DeleteConvention(name string) (bool, error) {
	return m.Store.DeleteConvention(name)
}
func (m *CodebaseModel) SaveInvariant(name, description, ruleJSON string) error {
	return m.Store.SaveInvariant(name, description, ruleJSON)
}
func (m *CodebaseModel) ListInvariants() ([]store.Invariant, error) {
	return m.Store.ListInvariants()
}
func (m *CodebaseModel) DeleteInvariant(name string) (bool, error) {
	return m.Store.DeleteInvariant(name)
}

// MtimeForPath returns the modification time for the given path.
func MtimeForPath(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Removed: Sync() — the manager goroutine drains events continuously.
// Removed: ParseAndIndexFile() as public method — now internal to manager.
