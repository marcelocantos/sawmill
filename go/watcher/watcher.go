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

	"github.com/marcelocantos/sawmill/scope"
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

// Watcher watches a directory tree for file changes.
type Watcher struct {
	fw         *fsnotify.Watcher
	done       chan struct{}
	closed     sync.Once
	classifier *scope.Classifier
}

// Watch starts watching root and all non-ignored subdirectories. The
// classifier decides which directories to watch and which to skip; library
// and owned dirs are watched, ignored dirs are not. classifier must not be
// nil.
//
// It returns a Watcher and a channel that receives debounced FileEvents.
// The caller must call Close when done to release resources.
func Watch(root string, classifier *scope.Classifier) (*Watcher, <-chan FileEvent, error) {
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

	w := &Watcher{fw: fw, done: make(chan struct{}), classifier: classifier}

	// Add root and all subdirectories recursively.
	if err := w.addDirsRecursive(absRoot); err != nil {
		fw.Close()
		return nil, nil, err
	}

	events := make(chan FileEvent, 64)
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
					_ = w.addDirsRecursive(ev.Name)
					continue
				}
			case ev.Has(fsnotify.Write) || ev.Has(fsnotify.Chmod):
				kind = Modified
			case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
				kind = Removed
			default:
				continue
			}

			if !w.isRelevant(ev.Name) {
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

// addDirsRecursive adds dir and all non-ignored subdirectories to fw.
// Ignored directories per the classifier are skipped; library and owned dirs
// are watched alike (the indexer decides what to do with their files).
func (w *Watcher) addDirsRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		if w.classifier != nil && w.classifier.ShouldSkipDir(path) {
			return filepath.SkipDir
		}
		return w.fw.Add(path)
	})
}

// isRelevant reports whether a file change should be forwarded. Hidden files
// and files in ignored directories are dropped.
func (w *Watcher) isRelevant(path string) bool {
	if w.classifier != nil && w.classifier.Classify(path, false) == scope.Ignored {
		return false
	}
	if strings.HasPrefix(filepath.Base(path), ".") {
		return false
	}
	return true
}
