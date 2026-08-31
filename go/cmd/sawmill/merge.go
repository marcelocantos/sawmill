// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/merge"
)

const (
	exitOK       = 0
	exitConflict = 1
	exitError    = 2
)

// runMerge implements the `sawmill merge` git-mergetool driver.
func runMerge(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base file path (required)")
	local := fs.String("local", "", "local/ours file path (required)")
	remote := fs.String("remote", "", "remote/theirs file path (required)")
	output := fs.String("output", "", "output file path (required)")
	language := fs.String("language", "", "language adapter override (e.g. python, go)")
	markerStyle := fs.String("marker-style", "diff3", "conflict marker style: diff3 or merge")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	if *base == "" || *local == "" || *remote == "" || *output == "" {
		fmt.Fprintln(stderr, "merge: --base, --local, --remote, and --output are required")
		fs.Usage()
		return exitError
	}

	adapter, err := resolveAdapter(*language, *output)
	if err != nil {
		fmt.Fprintf(stderr, "merge: %v\n", err)
		return exitError
	}

	baseBytes, err := os.ReadFile(*base)
	if err != nil {
		fmt.Fprintf(stderr, "merge: reading --base: %v\n", err)
		return exitError
	}
	oursBytes, err := os.ReadFile(*local)
	if err != nil {
		fmt.Fprintf(stderr, "merge: reading --local: %v\n", err)
		return exitError
	}
	theirsBytes, err := os.ReadFile(*remote)
	if err != nil {
		fmt.Fprintf(stderr, "merge: reading --remote: %v\n", err)
		return exitError
	}

	result, err := merge.Merge(baseBytes, oursBytes, theirsBytes, adapter, merge.Options{
		Path:  *output,
		Style: *markerStyle,
	})
	if err != nil {
		fmt.Fprintf(stderr, "merge: %v\n", err)
		return exitError
	}

	if err := os.WriteFile(*output, result.Merged, 0o644); err != nil {
		fmt.Fprintf(stderr, "merge: writing --output: %v\n", err)
		return exitError
	}

	if len(result.Conflicts) > 0 {
		return exitConflict
	}
	return exitOK
}

// runMergeDriver implements the `sawmill merge-driver` git low-level driver.
// Git invokes it as: sawmill merge-driver %O %A %B %P
// i.e. argv: base local remote pathname
func runMergeDriver(args []string, stderr io.Writer) int {
	if len(args) != 4 {
		fmt.Fprintf(stderr, "merge-driver: expected 4 arguments (%%O %%A %%B %%P), got %d\n", len(args))
		return exitError
	}
	basePath := args[0]
	localPath := args[1]
	remotePath := args[2]
	pathname := args[3]

	adapter, err := resolveAdapter("", pathname)
	if err != nil {
		fmt.Fprintf(stderr, "merge-driver: %v\n", err)
		return exitError
	}

	baseBytes, err := os.ReadFile(basePath)
	if err != nil {
		fmt.Fprintf(stderr, "merge-driver: reading base: %v\n", err)
		return exitError
	}
	oursBytes, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Fprintf(stderr, "merge-driver: reading local: %v\n", err)
		return exitError
	}
	theirsBytes, err := os.ReadFile(remotePath)
	if err != nil {
		fmt.Fprintf(stderr, "merge-driver: reading remote: %v\n", err)
		return exitError
	}

	result, err := merge.Merge(baseBytes, oursBytes, theirsBytes, adapter, merge.Options{
		Path: pathname,
	})
	if err != nil {
		fmt.Fprintf(stderr, "merge-driver: %v\n", err)
		return exitError
	}

	if err := os.WriteFile(localPath, result.Merged, 0o644); err != nil {
		fmt.Fprintf(stderr, "merge-driver: writing merged result: %v\n", err)
		return exitError
	}

	if len(result.Conflicts) > 0 {
		return exitConflict
	}
	return exitOK
}

// resolveAdapter returns a LanguageAdapter for the given language name or file path.
// If language is non-empty it takes precedence over path-based detection.
func resolveAdapter(language, path string) (adapters.LanguageAdapter, error) {
	if language != "" {
		adapter := adapters.ForExtension(language)
		if adapter == nil {
			return nil, fmt.Errorf("unknown language %q", language)
		}
		return adapter, nil
	}

	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return nil, fmt.Errorf("cannot detect language from path %q (no extension); use --language", path)
	}
	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		return nil, fmt.Errorf("no adapter for extension %q", ext)
	}
	return adapter, nil
}
