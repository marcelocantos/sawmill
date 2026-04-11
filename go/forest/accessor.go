// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import (
	"fmt"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/store"
)

// FileAccessor provides scoped, on-demand access to a file's source and
// Tree-sitter tree. Trees are obtained via the cache and never escape the
// WithTree callback, ensuring bounded memory.
type FileAccessor struct {
	FilePath    string
	Lang        string
	ContentHash string
	Adapter_    adapters.LanguageAdapter
	store       *store.Store
	cache       *TreeCache
}

// NewFileAccessor creates a FileAccessor backed by the given store and cache.
func NewFileAccessor(path, lang, contentHash string, adapter adapters.LanguageAdapter, s *store.Store, cache *TreeCache) *FileAccessor {
	return &FileAccessor{
		FilePath:    path,
		Lang:        lang,
		ContentHash: contentHash,
		Adapter_:    adapter,
		store:       s,
		cache:       cache,
	}
}

// Path returns the file's absolute path.
func (a *FileAccessor) Path() string { return a.FilePath }

// Adapter returns the language adapter for the file.
func (a *FileAccessor) Adapter() adapters.LanguageAdapter { return a.Adapter_ }

// Source reads the file's source bytes from the store.
func (a *FileAccessor) Source() ([]byte, error) {
	return a.store.ReadSource(a.FilePath)
}

// WithTree provides scoped access to the file's source and parsed tree. The
// tree is obtained from the cache (or parsed on demand) and must not be
// retained after fn returns.
func (a *FileAccessor) WithTree(fn func(source []byte, tree *tree_sitter.Tree) error) error {
	source, tree, err := a.cache.GetOrParse(a.FilePath, a.ContentHash, func() ([]byte, *tree_sitter.Tree, error) {
		src, err := a.store.ReadSource(a.FilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("reading source for %s: %w", a.FilePath, err)
		}
		t, err := ParseSource(src, a.Adapter_)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", a.FilePath, err)
		}
		if t == nil {
			return nil, nil, fmt.Errorf("parsing %s: tree-sitter returned nil", a.FilePath)
		}
		return src, t, nil
	})
	if err != nil {
		return err
	}
	return fn(source, tree)
}

// MemFileAccessor wraps an in-memory ParsedFile as a FileAccessor-compatible
// type. Used during migration and for ephemeral models.
type MemFileAccessor struct {
	File *ParsedFile
}

// Path returns the file's path.
func (m *MemFileAccessor) Path() string { return m.File.Path }

// Adapter returns the file's language adapter.
func (m *MemFileAccessor) Adapter() adapters.LanguageAdapter { return m.File.Adapter }

// Source returns the file's source bytes (already in memory).
func (m *MemFileAccessor) Source() ([]byte, error) { return m.File.OriginalSource, nil }

// WithTree calls fn with the in-memory source and tree.
func (m *MemFileAccessor) WithTree(fn func(source []byte, tree *tree_sitter.Tree) error) error {
	return fn(m.File.OriginalSource, m.File.Tree)
}
