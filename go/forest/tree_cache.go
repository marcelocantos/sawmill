// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
)

// DefaultCacheSize is the default maximum number of trees held in the cache.
const DefaultCacheSize = 100

// cacheEntry is a single cached tree with its associated data.
type cacheEntry struct {
	path        string
	contentHash string
	source      []byte
	tree        *tree_sitter.Tree
}

// TreeCache is a bounded LRU cache of parsed Tree-sitter trees. It is safe for
// concurrent use.
type TreeCache struct {
	mu       sync.Mutex
	maxSize  int
	items    map[string]*list.Element // key: path
	order    *list.List               // front = most recent
}

// NewTreeCache creates a cache that holds at most maxSize parsed trees.
func NewTreeCache(maxSize int) *TreeCache {
	if maxSize <= 0 {
		maxSize = DefaultCacheSize
	}
	return &TreeCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

// GetOrParse returns the cached tree and source for path if the content hash
// matches. On a miss it calls parseFn to produce a new tree, caches it, and
// returns it. The returned tree must not be closed by the caller — the cache
// owns it.
func (c *TreeCache) GetOrParse(
	path string,
	contentHash string,
	parseFn func() ([]byte, *tree_sitter.Tree, error),
) ([]byte, *tree_sitter.Tree, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[path]; ok {
		entry := elem.Value.(*cacheEntry)
		if entry.contentHash == contentHash {
			c.order.MoveToFront(elem)
			return entry.source, entry.tree, nil
		}
		// Stale — evict and re-parse.
		c.removeLocked(elem)
	}

	// Cache miss — parse outside the lock would be better for concurrency,
	// but the manager goroutine serialises access anyway, and keeping it
	// simple avoids double-parse races.
	source, tree, err := parseFn()
	if err != nil {
		return nil, nil, err
	}

	c.addLocked(path, contentHash, source, tree)
	return source, tree, nil
}

// Evict removes the entry for path from the cache (if present) and closes its
// tree. Call this when a file changes on disk.
func (c *TreeCache) Evict(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[path]; ok {
		c.removeLocked(elem)
	}
}

// Clear removes and closes all cached trees.
func (c *TreeCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, elem := range c.items {
		elem.Value.(*cacheEntry).tree.Close()
	}
	c.items = make(map[string]*list.Element)
	c.order.Init()
}

// Size returns the current number of cached entries.
func (c *TreeCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *TreeCache) addLocked(path, contentHash string, source []byte, tree *tree_sitter.Tree) {
	// Evict LRU entries if at capacity.
	for c.order.Len() >= c.maxSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeLocked(oldest)
	}

	entry := &cacheEntry{
		path:        path,
		contentHash: contentHash,
		source:      source,
		tree:        tree,
	}
	elem := c.order.PushFront(entry)
	c.items[path] = elem
}

func (c *TreeCache) removeLocked(elem *list.Element) {
	entry := c.order.Remove(elem).(*cacheEntry)
	delete(c.items, entry.path)
	entry.tree.Close()
}

// ParseSource parses source bytes with the given adapter and returns a new tree.
// This is a convenience function — callers that don't need caching can use it
// directly.
//
// A parse that outruns MaxParseDuration yields ErrParseTimeout and no tree.
// The check is on the stop reason rather than tree.ParseStoppedEarly() so that
// ordinary malformed source, which stops early for its own reasons and still
// produces a useful ERROR-bearing tree, keeps being indexed.
func ParseSource(source []byte, adapter adapters.LanguageAdapter) (*tree_sitter.Tree, error) {
	return ParseSourceWithTimeout(source, adapter, MaxParseDuration)
}

// ParseSourceWithTimeout is ParseSource with an explicit deadline, so that
// tests can exercise the timeout path without waiting out MaxParseDuration.
// A timeout of zero leaves the parse unbounded.
func ParseSourceWithTimeout(
	source []byte,
	adapter adapters.LanguageAdapter,
	timeout time.Duration,
) (*tree_sitter.Tree, error) {
	if timeout > 0 && quarantine.has(source) {
		return nil, ErrParseTimeout
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(adapter.Language()); err != nil {
		return nil, err
	}

	// Two independent bounds, because neither is sufficient alone. The
	// parser's own timeout reports itself accurately but is only consulted in
	// the primary parse loop, so a long one can be missed entirely — a 16s
	// bound on a known-pathological script was observed running 58s and then
	// claiming success. The cancellation flag is honoured promptly wherever
	// the parse has got to, but the retry path can overwrite the stop reason
	// it leaves behind with "accepted".
	//
	// So the timeout supplies an early, well-labelled exit, and the flag
	// supplies the dependable ceiling. Neither verdict is taken on trust:
	// elapsed time is measured here, and a parse that reaches the ceiling is
	// truncated no matter what the tree says about itself.
	parser.SetTimeoutMicros(uint64(timeout / time.Microsecond))

	var cancel uint32
	if timeout > 0 {
		parser.SetCancellationFlag(&cancel)
		watchdog := time.AfterFunc(timeout, func() { atomic.StoreUint32(&cancel, 1) })
		defer watchdog.Stop()
	}

	start := time.Now()
	tree := parser.Parse(source, nil)
	elapsed := time.Since(start)

	if tree == nil {
		return nil, nil
	}
	// Comparing elapsed against the bound, rather than reading the flag,
	// keeps a watchdog that fires just as a good parse lands from discarding
	// it — and quarantining it.
	if (timeout > 0 && elapsed >= timeout) || tree.ParseStopReason() == tree_sitter.ParseStopTimeout {
		tree.Close()
		quarantine.add(source)
		return nil, ErrParseTimeout
	}
	return tree, nil
}
