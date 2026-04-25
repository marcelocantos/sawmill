// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package model provides the persistent codebase model. Source and metadata are
// stored in SQLite; trees are parsed on demand via a bounded LRU cache. A
// manager goroutine watches for file changes and updates the store. Handlers
// access files via FileAccessors (scoped tree access) or ForestSnapshot
// (transient full parse for codegen).
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"log"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
	"github.com/marcelocantos/sawmill/index"
	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/paths"
	"github.com/marcelocantos/sawmill/store"
	"github.com/marcelocantos/sawmill/watcher"
)

// CodebaseModel is a persistent, live-updating codebase model. A manager
// goroutine watches for file changes and updates the store. Handlers access
// files via FileAccessors (on-demand parsing) or ForestSnapshot (transient
// full parse for codegen).
type CodebaseModel struct {
	// Root directory being tracked.
	Root string
	// SQLite-backed persistent store. Safe for concurrent reads under WAL.
	Store *store.Store
	// LSP is the pool of language server clients (may be nil).
	LSP *lspclient.Pool
	// Cache holds recently-parsed trees for on-demand access.
	Cache *forest.TreeCache
	// GitIndex is the lazy git commit indexer (nil if the root is not a git repo).
	GitIndex *gitindex.Indexer

	// forest is only set for ephemeral models (no manager goroutine).
	forest *forest.Forest
	// w watches root for file changes (nil for ephemeral models).
	w *watcher.Watcher
	// events is the channel of debounced file events from w.
	events <-chan watcher.FileEvent
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

	if err := incrementalParse(absRoot, s); err != nil {
		s.Close()
		return nil, err
	}

	w, events, err := watcher.Watch(absRoot)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("starting watcher: %w", err)
	}

	m := &CodebaseModel{
		Root:    absRoot,
		Store:   s,
		LSP:     lspclient.NewPool(),
		Cache:   forest.NewTreeCache(forest.DefaultCacheSize),
		w:       w,
		events:  events,
		notify:  make(chan []string, 16),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	// Open the git index if this directory is inside a git repo. Index HEAD in
	// the background so startup is not blocked.
	if ix, err := openGitIndex(absRoot); err == nil {
		m.GitIndex = ix
		go func() {
			if err := ix.IndexHead(); err != nil {
				log.Printf("sawmill: git index HEAD: %v", err)
			}
		}()
	}

	go m.runManager()
	return m, nil
}

// openGitIndex opens (or creates) the git index store for absRoot and returns
// an Indexer. Returns an error if absRoot is not inside a git repo.
func openGitIndex(absRoot string) (*gitindex.Indexer, error) {
	repo, err := gitrepo.Open(absRoot)
	if err != nil {
		return nil, err
	}
	storeDir := paths.StoreDir(absRoot)
	giPath := filepath.Join(storeDir, "gitindex.db")
	giStore, err := gitindex.Open(giPath)
	if err != nil {
		return nil, err
	}
	return gitindex.NewIndexer(giStore, repo), nil
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

// Close stops the manager goroutine, the file watcher, LSP clients, the git
// index, and the store.
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
	if m.GitIndex != nil {
		_ = m.GitIndex.Close()
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

// ForestSnapshot builds a transient forest by parsing all tracked files on
// demand. The returned trees are cached via the TreeCache for reuse. This
// method exists for codegen/convention/invariant tools that need a full
// Forest; most tool handlers should use FileAccessors + WithTree instead.
//
// For ephemeral models (no manager goroutine), returns the forest directly.
func (m *CodebaseModel) ForestSnapshot() *forest.Forest {
	if m.forest != nil {
		// Ephemeral model — has an in-memory forest.
		return m.forest
	}

	records, err := m.Store.TrackedFilesWithMeta()
	if err != nil {
		return &forest.Forest{}
	}

	var files []*forest.ParsedFile
	for _, rec := range records {
		adapter := adapters.ForExtension(rec.Language)
		if adapter == nil {
			continue
		}
		source, tree, err := m.Cache.GetOrParse(rec.Path, rec.ContentHash, func() ([]byte, *tree_sitter.Tree, error) {
			src, err := m.Store.ReadSource(rec.Path)
			if err != nil {
				return nil, nil, err
			}
			t, err := forest.ParseSource(src, adapter)
			if err != nil {
				return nil, nil, err
			}
			if t == nil {
				return nil, nil, fmt.Errorf("parsing %s: nil tree", rec.Path)
			}
			return src, t, nil
		})
		if err != nil {
			continue
		}
		files = append(files, &forest.ParsedFile{
			Path:           rec.Path,
			OriginalSource: source,
			Tree:           tree,
			Adapter:        adapter,
		})
	}
	return &forest.Forest{Files: files}
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
	n, _ := m.Store.FileCount()
	return n
}

// FindSymbols finds symbols by name, using the persistent index.
func (m *CodebaseModel) FindSymbols(name, kind string) ([]store.SymbolRecord, error) {
	return m.Store.FindSymbols(name, kind)
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
		case <-m.done:
			return
		}
	}
}

// reparse re-parses a single file and updates the store and cache.
func (m *CodebaseModel) reparse(path string) {
	if m.Cache != nil {
		m.Cache.Evict(path)
	}
	m.parseAndIndexFile(path)
}

// applyEvent updates the store and cache for a single file event.
func (m *CodebaseModel) applyEvent(ev watcher.FileEvent) {
	switch ev.Kind {
	case watcher.Removed:
		if m.Cache != nil {
			m.Cache.Evict(ev.Path)
		}
		_ = m.Store.RemoveFile(ev.Path)
	case watcher.Created, watcher.Modified:
		m.reparse(ev.Path)
	}
}

// parseAndIndexFile re-parses a single file and updates the store.
func (m *CodebaseModel) parseAndIndexFile(path string) {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return
	}

	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		return
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mtime := info.ModTime()
	contentHash := hashBytes(source)

	// Parse to extract symbols, then discard the tree.
	tree, err := forest.ParseSource(source, adapter)
	if err != nil || tree == nil {
		return
	}
	defer tree.Close()

	symbols := index.ExtractSymbolsFromParts(source, tree, adapter, path)
	records := symbolsToRecords(symbols, path)

	_ = m.Store.UpsertFile(path, ext, mtime, contentHash, source)
	_ = m.Store.UpdateSymbols(path, records)
}

// --- Static helpers ---

// incrementalParse walks root, parses changed files, and populates the store.
// Trees are parsed only to extract symbols, then discarded.
func incrementalParse(root string, s *store.Store) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
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
		if storedHash == contentHash && storedHash != "" {
			// File unchanged — source is already in the store.
			return nil
		}

		// Parse to extract symbols, then discard.
		tree, err := forest.ParseSource(source, adapter)
		if err != nil || tree == nil {
			return nil // skip unparseable files
		}

		symbols := index.ExtractSymbolsFromParts(source, tree, adapter, path)
		tree.Close()

		records := symbolsToRecords(symbols, path)
		_ = s.UpsertFile(path, ext, mtime, contentHash, source)
		_ = s.UpdateSymbols(path, records)

		return nil
	})
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
func (m *CodebaseModel) SaveEquivalence(name, description, leftPattern, rightPattern, preferredDirection string) error {
	return m.Store.SaveEquivalence(name, description, leftPattern, rightPattern, preferredDirection)
}
func (m *CodebaseModel) ListEquivalences() ([]store.Equivalence, error) {
	return m.Store.ListEquivalences()
}
func (m *CodebaseModel) LoadEquivalence(name string) (*store.Equivalence, error) {
	return m.Store.LoadEquivalence(name)
}
func (m *CodebaseModel) DeleteEquivalence(name string) (bool, error) {
	return m.Store.DeleteEquivalence(name)
}
func (m *CodebaseModel) SaveFix(name, description, diagnosticRegex, actionJSON, confidence string) error {
	return m.Store.SaveFix(name, description, diagnosticRegex, actionJSON, confidence)
}
func (m *CodebaseModel) ListFixes() ([]store.Fix, error) {
	return m.Store.ListFixes()
}
func (m *CodebaseModel) LoadFix(name string) (*store.Fix, error) {
	return m.Store.LoadFix(name)
}
func (m *CodebaseModel) DeleteFix(name string) (bool, error) {
	return m.Store.DeleteFix(name)
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
