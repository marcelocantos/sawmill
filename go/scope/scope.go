// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package scope classifies files in a project tree into one of three scopes —
// owned, library, or ignored — driving how (and whether) sawmill indexes them.
//
// The classifier consults, in priority order:
//
//  1. Project-level overrides loaded from .sawmill/scopes.yaml (if present).
//  2. Hardcoded library-dir basenames (vendor/, node_modules/, etc.).
//  3. Hardcoded ignored-dir basenames (build artefacts, IDE state, etc.).
//  4. Hidden directories (anything starting with ".").
//  5. Gitignore (when a .git directory is present at the project root).
//  6. Default: owned.
//
// The library-before-gitignore ordering matters: directories like node_modules
// are typically gitignored but should still be indexed (in API-only mode).
package scope

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"gopkg.in/yaml.v3"
)

// Kind classifies how sawmill treats a file or directory.
type Kind int

const (
	// Owned files are full-fidelity indexed: declarations and call sites,
	// source bytes cached in the store. This is the default for project-local
	// source code.
	Owned Kind = iota

	// Library files are indexed at the API level only — declarations, types,
	// methods, fields — without call sites. Source bytes are not cached;
	// re-parses pull them from disk on demand. This applies to vendored or
	// external code that the project consumes but doesn't maintain.
	Library

	// Ignored files are not walked or indexed at all. Build artefacts,
	// generated output, IDE state, and gitignored non-source land here.
	Ignored
)

func (k Kind) String() string {
	switch k {
	case Owned:
		return "owned"
	case Library:
		return "library"
	case Ignored:
		return "ignored"
	default:
		return fmt.Sprintf("scope(%d)", int(k))
	}
}

// ParseKind parses a scope string. Returns Owned and false for unrecognised
// input, so callers can decide whether to error or fall back.
func ParseKind(s string) (Kind, bool) {
	switch s {
	case "owned":
		return Owned, true
	case "library":
		return Library, true
	case "ignored":
		return Ignored, true
	default:
		return Owned, false
	}
}

// libraryBasenames are directory names that classify as Library scope by
// default. These are ecosystem conventions for vendored / external code.
var libraryBasenames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"third_party":  true,
	"external":     true,
	"Pods":         true,
	"deps":         true,
	"site-packages": true,
}

// ignoredBasenames are directory names that classify as Ignored scope by
// default. These are build outputs, IDE state, and version-control internals.
var ignoredBasenames = map[string]bool{
	// Generic build outputs
	"target":      true,
	"dist":        true,
	"build":       true,
	"__pycache__": true,
	".next":       true,

	// Unity-specific
	"Library":        true,
	"Builds":         true,
	"Build":          true,
	"Logs":           true,
	"obj":            true,
	"Temp":           true,
	"MemoryCaptures": true,
	"il2cppOutput":   true,
	"Il2CppBackup":   true,
	"Bee":            true,
}

// Classifier assigns scopes to paths under a project root.
type Classifier struct {
	root    string
	matcher gitignore.Matcher // nil if root is not a git repo
	rules   []rule
}

// rule is a single override loaded from .sawmill/scopes.yaml. Patterns reuse
// gitignore.Pattern so they support gitignore-style globs (** included).
type rule struct {
	pattern gitignore.Pattern
	kind    Kind
}

// scopesYAML is the serialised form of .sawmill/scopes.yaml.
type scopesYAML struct {
	Owned   []string `yaml:"owned"`
	Library []string `yaml:"library"`
	Ignored []string `yaml:"ignored"`
}

// New constructs a Classifier rooted at absRoot. absRoot must be an absolute
// path. Missing .sawmill/scopes.yaml or .gitignore files are not errors.
func New(absRoot string) (*Classifier, error) {
	c := &Classifier{root: absRoot}

	if err := c.loadScopesYAML(); err != nil {
		return nil, fmt.Errorf("loading scopes.yaml: %w", err)
	}

	if err := c.loadGitignore(); err != nil {
		return nil, fmt.Errorf("loading gitignore patterns: %w", err)
	}

	return c, nil
}

func (c *Classifier) loadScopesYAML() error {
	path := filepath.Join(c.root, ".sawmill", "scopes.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var doc scopesYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	addRules := func(globs []string, kind Kind) {
		for _, g := range globs {
			c.rules = append(c.rules, rule{
				pattern: gitignore.ParsePattern(g, nil),
				kind:    kind,
			})
		}
	}
	addRules(doc.Owned, Owned)
	addRules(doc.Library, Library)
	addRules(doc.Ignored, Ignored)
	return nil
}

func (c *Classifier) loadGitignore() error {
	if _, err := os.Stat(filepath.Join(c.root, ".git")); err != nil {
		// Not a git repo (or .git inaccessible); skip gitignore consultation.
		return nil
	}

	// Read only the top-level .gitignore and .git/info/exclude. We deliberately
	// do NOT use go-git's gitignore.ReadPatterns: that variant recursively
	// descends the entire tree to collect every nested .gitignore file before
	// the indexing walker even starts, which on a Unity-sized project blocks
	// Load for tens of minutes. Most real-world projects have a single
	// top-level .gitignore that is sufficient to identify ignored build dirs;
	// nested .gitignore files (e.g. inside a vendored library) are rarely
	// load-bearing for sawmill's classification — and any project that needs
	// finer control can override via .sawmill/scopes.yaml.
	var patterns []gitignore.Pattern
	patterns = append(patterns, readGitignoreFile(filepath.Join(c.root, ".gitignore"), nil)...)
	patterns = append(patterns, readGitignoreFile(filepath.Join(c.root, ".git", "info", "exclude"), nil)...)
	if len(patterns) == 0 {
		return nil
	}
	c.matcher = gitignore.NewMatcher(patterns)
	return nil
}

// readGitignoreFile parses a gitignore-format file at path, returning its
// patterns. Missing files are not an error — they return nil. base is the
// pattern's base path (nil = repo root).
func readGitignoreFile(path string, base []string) []gitignore.Pattern {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, base))
	}
	return patterns
}

// Classify returns the scope for absPath. isDir indicates whether the path
// refers to a directory; this matters for gitignore matching (gitignore
// patterns can target dirs only).
//
// If absPath is outside c.root, the file is treated as Owned — the caller is
// responsible for not asking about external paths in normal operation.
func (c *Classifier) Classify(absPath string, isDir bool) Kind {
	rel, err := filepath.Rel(c.root, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return Owned
	}
	if rel == "." {
		return Owned
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")

	// 1. Project-level overrides win.
	if k, ok := c.matchOverrides(parts, isDir); ok {
		return k
	}

	// 2. Library-by-basename — checked before ignored/hidden/gitignore so that
	// dirs like node_modules (typically gitignored) still get indexed.
	for _, p := range parts {
		if libraryBasenames[p] {
			return Library
		}
	}

	// 3. Ignored-by-basename.
	for _, p := range parts {
		if ignoredBasenames[p] {
			return Ignored
		}
	}

	// 4. Hidden directories/files.
	for _, p := range parts {
		if strings.HasPrefix(p, ".") && p != "." && p != ".." {
			return Ignored
		}
	}

	// 5. Gitignore.
	if c.matcher != nil && c.matcher.Match(parts, isDir) {
		return Ignored
	}

	return Owned
}

// matchOverrides walks the rules in declaration order and returns the most
// recent matching kind. Later rules take priority over earlier ones, mirroring
// gitignore's last-match-wins semantics.
func (c *Classifier) matchOverrides(parts []string, isDir bool) (Kind, bool) {
	var matched Kind
	var ok bool
	for _, r := range c.rules {
		if r.pattern.Match(parts, isDir) > gitignore.NoMatch {
			matched = r.kind
			ok = true
		}
	}
	return matched, ok
}

// ShouldSkipDir is a convenience that returns true if a directory should be
// skipped during a walk. Equivalent to Classify(absPath, true) == Ignored.
func (c *Classifier) ShouldSkipDir(absPath string) bool {
	return c.Classify(absPath, true) == Ignored
}
