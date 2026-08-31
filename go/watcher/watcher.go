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
	fw   *fsnotify.Watcher
	done chan struct{}
	// root is the absolute directory this watcher is confined to, left
	// unresolved so paths match the classifier's own root.
	root string
	// rootResolved is root with symlinks resolved. Containment compares
	// resolved paths, while the walk stays on the unresolved root so paths
	// line up with the classifier, whose root is unresolved too. Conflating
	// the two made every path look outside the classifier's root, which
	// silently reclassified node_modules as owned.
	rootResolved string
	closed       sync.Once
	classifier   *scope.Classifier
}

// Watch starts watching root and its non-ignored, non-library subdirectories.
// classifier must not be nil.
//
// Two properties are load-bearing, both learned from a daemon that ended up
// watching /Applications and / itself:
//
//   - Nothing outside root is ever watched. Directory symlinks are not
//     followed, and every candidate path is re-checked against root, so a
//     symlink inside the tree cannot drag an unrelated subtree into the watch
//     set.
//   - Library directories (node_modules, vendor, …) are indexed but not
//     watched. On macOS, fsnotify's kqueue backend costs one file descriptor
//     per watched *file*, and dependency trees dominate the file count by an
//     order of magnitude while almost never changing under us.
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

	// Resolve the root once, for containment comparisons only. On macOS a
	// temp dir is reached through /var -> /private/var, so without this every
	// path would resolve "outside" its own root.
	rootResolved := absRoot
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		rootResolved = resolved
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	w := &Watcher{
		fw:           fw,
		done:         make(chan struct{}),
		root:         absRoot,
		rootResolved: rootResolved,
		classifier:   classifier,
	}

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
				// If a new *real* directory is created, start watching it.
				// Lstat, not Stat: Stat follows symlinks, so a link to an
				// outside directory looked like a directory to watch and the
				// walk then pulled that whole tree in — which is how a project
				// watcher ended up holding /Applications.
				if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
					_ = w.addDirsRecursive(ev.Name)
					continue
				}
			case ev.Has(fsnotify.Write):
				kind = Modified
			case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
				kind = Removed
			default:
				// Chmod lands here deliberately. On macOS the kqueue backend
				// reports NOTE_ATTRIB as Chmod, and an atime update counts —
				// so treating it as a modification meant that merely *reading*
				// a file scheduled a reparse. Anything that walks the tree
				// (ripgrep, a backup pass, Spotlight) then triggered a reparse
				// of everything it touched. Content changes arrive as Write.
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

// contains reports whether path is root itself or lies beneath it. Paths are
// compared after symlink resolution, so a symlink pointing out of the tree is
// rejected on where it leads rather than where it sits.
//
// A path that cannot be resolved (it was deleted between the event and this
// check, most likely) is rejected: refusing to watch something that may be
// outside the root is always the safe direction.
func (w *Watcher) contains(path string) bool {
	if w.rootResolved == "" {
		return true
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if resolved == w.rootResolved {
		return true
	}
	rel, err := filepath.Rel(w.rootResolved, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// shouldWatchDir reports whether a directory belongs in the watch set.
// Ignored directories are skipped outright; library directories are indexed
// but deliberately not watched (see Watch).
func (w *Watcher) shouldWatchDir(path string) bool {
	if w.classifier == nil {
		return true
	}
	switch w.classifier.Classify(path, true) {
	case scope.Ignored, scope.Library:
		return false
	}
	return true
}

// addDirsRecursive adds dir and its watchable subdirectories to fw.
//
// filepath.WalkDir does not follow symlinks, so a symlinked directory arrives
// here as a non-directory entry and is skipped by the d.IsDir() test. The
// explicit contains check covers the other way in — dir itself having been
// reached through a link.
func (w *Watcher) addDirsRecursive(dir string) error {
	if !w.contains(dir) {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		if !w.shouldWatchDir(path) {
			return filepath.SkipDir
		}
		if !w.contains(path) {
			return filepath.SkipDir
		}
		return w.fw.Add(path)
	})
}

// isRelevant reports whether a file change should be forwarded. Hidden files,
// files in ignored directories, and symlinks are dropped.
//
// Symlinks are dropped for two reasons. Indexing one would follow it and pull
// content from outside the root into the model. And on macOS the kqueue
// backend opens the link path — which resolves to the target — so a change
// anywhere in a linked-to directory surfaces as a modification of the link
// itself. That happens inside fsnotify, below the containment check, so the
// only place to stop it is here.
//
// A path that no longer exists is not treated as a symlink: removals must
// still be reported.
func (w *Watcher) isRelevant(path string) bool {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if w.classifier != nil && w.classifier.Classify(path, false) == scope.Ignored {
		return false
	}
	if strings.HasPrefix(filepath.Base(path), ".") {
		return false
	}
	return true
}
