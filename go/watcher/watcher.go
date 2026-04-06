// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package watcher watches a directory tree for file changes with debouncing.
package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventKind describes the type of file system change.
type EventKind int

const (
	Created  EventKind = iota
	Modified           // file content changed
	Removed            // file deleted or renamed away
)

// FileEvent represents a single debounced file system change.
type FileEvent struct {
	Path string
	Kind EventKind
}

// debounceDuration is the window within which duplicate events for the same
// path are collapsed into one.
const debounceDuration = 100 * time.Millisecond

// skipDirs is the set of directory names that are never watched.
var skipDirs = map[string]bool{
	".git":         true,
	"target":       true,
	"node_modules": true,
	".svn":         true,
	".hg":          true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	"vendor":       true,
}

// Watcher watches a directory tree for file changes.
type Watcher struct {
	fw     *fsnotify.Watcher
	done   chan struct{}
	closed sync.Once
}

// Watch starts watching root and all non-skipped subdirectories.
// It returns a Watcher and a channel that receives debounced FileEvents.
// The caller must call Close when done to release resources.
func Watch(root string) (*Watcher, <-chan FileEvent, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}

	if _, err := os.Stat(absRoot); err != nil {
		return nil, nil, err
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	// Add root and all subdirectories recursively.
	if err := addDirsRecursive(fw, absRoot); err != nil {
		fw.Close()
		return nil, nil, err
	}

	events := make(chan FileEvent, 64)
	done := make(chan struct{})

	w := &Watcher{fw: fw, done: done}
	go w.run(events)

	return w, events, nil
}

// Close stops the watcher and closes the events channel.
func (w *Watcher) Close() error {
	var err error
	w.closed.Do(func() {
		close(w.done)
		err = w.fw.Close()
	})
	return err
}

// run is the background goroutine that debounces events and forwards them.
func (w *Watcher) run(out chan<- FileEvent) {
	defer close(out)

	// pending holds the earliest-seen kind and the flush deadline per path.
	type entry struct {
		kind     EventKind
		deadline time.Time
	}
	pending := make(map[string]entry)

	timer := time.NewTimer(time.Hour) // will be reset below
	timer.Stop()

	resetTimer := func() {
		// Find the earliest deadline.
		var earliest time.Time
		for _, e := range pending {
			if earliest.IsZero() || e.deadline.Before(earliest) {
				earliest = e.deadline
			}
		}
		if !earliest.IsZero() {
			timer.Reset(time.Until(earliest))
		}
	}

	flush := func() {
		now := time.Now()
		for path, e := range pending {
			if !e.deadline.After(now) {
				select {
				case out <- FileEvent{Path: path, Kind: e.kind}:
				default:
					// Drop if consumer is not keeping up.
				}
				delete(pending, path)
			}
		}
		resetTimer()
	}

	for {
		select {
		case <-w.done:
			// Flush remaining events before exiting.
			for path, e := range pending {
				select {
				case out <- FileEvent{Path: path, Kind: e.kind}:
				default:
				}
				delete(pending, path)
			}
			timer.Stop()
			return

		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}

			var kind EventKind
			switch {
			case ev.Has(fsnotify.Create):
				kind = Created
				// If a new directory is created, start watching it.
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = addDirsRecursive(w.fw, ev.Name)
					continue
				}
			case ev.Has(fsnotify.Write) || ev.Has(fsnotify.Chmod):
				kind = Modified
			case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
				kind = Removed
			default:
				continue
			}

			if !isRelevant(ev.Name) {
				continue
			}

			deadline := time.Now().Add(debounceDuration)
			if existing, ok := pending[ev.Name]; ok {
				// Preserve the original kind; push the deadline forward.
				pending[ev.Name] = entry{kind: existing.kind, deadline: deadline}
			} else {
				pending[ev.Name] = entry{kind: kind, deadline: deadline}
			}
			resetTimer()

		case <-timer.C:
			flush()

		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			// Suppress watcher errors — non-critical, keep running.
			_ = err
		}
	}
}

// addDirsRecursive adds dir and all non-skipped subdirectories to fw.
func addDirsRecursive(fw *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if shouldSkip(name) {
			return filepath.SkipDir
		}
		return fw.Add(path)
	})
}

// shouldSkip reports whether a directory name should be skipped.
func shouldSkip(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return skipDirs[name]
}

// isRelevant reports whether the path points to a file we care about:
// not inside a skipped directory and not a hidden file.
func isRelevant(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if shouldSkip(component) {
			return false
		}
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	return true
}
