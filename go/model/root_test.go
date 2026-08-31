// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/paths"
)

// Load is what the parse tool calls, so validating there covers every entry
// point. Both of these roots had real stores on disk — 2.9 GB for "/" and
// 25 GB for the home directory — before anything rejected them.
func TestLoadRejectsImplausibleRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	for _, root := range []string{string(filepath.Separator), home} {
		m, err := model.Load(root)
		if !errors.Is(err, paths.ErrImplausibleRoot) {
			if m != nil {
				m.Close()
			}
			t.Errorf("Load(%q) err = %v, want ErrImplausibleRoot", root, err)
		}
		if m != nil {
			t.Errorf("Load(%q) returned a model; it must refuse before opening a store", root)
			m.Close()
		}
	}
}
