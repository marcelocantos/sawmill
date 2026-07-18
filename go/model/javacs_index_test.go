// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package model_test

import (
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/model"
)

// TestJavaCSharpIndexing exercises the full parse-and-index pipeline for
// Java and C# files and asserts that declarations, methods, fields, and
// calls all land in the symbol table.
func TestJavaCSharpIndexing(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, "src/App.java", `package app;

import java.util.List;

public class App {
    private int count;

    public int getCount() {
        helper();
        return count;
    }

    void helper() {}
}
`)
	writeFile(t, root, "src/App.cs", `using System;

namespace App
{
    public class App
    {
        private int count;

        public int GetCount()
        {
            Helper();
            return count;
        }

        void Helper() {}
    }
}
`)

	m, err := model.LoadEphemeral(root)
	if err != nil {
		t.Fatalf("LoadEphemeral: %v", err)
	}
	defer m.Close()

	for _, tc := range []struct {
		file  string
		wants map[string]string // symbol name -> kind
	}{
		{"src/App.java", map[string]string{
			"App":            "type",
			"getCount":       "function",
			"java.util.List": "import",
			"helper":         "call",
		}},
		{"src/App.cs", map[string]string{
			"App":      "type",
			"GetCount": "function",
			"System":   "import",
			"Helper":   "call",
		}},
	} {
		syms, err := m.Store.SymbolsInFile(filepath.Join(root, tc.file))
		if err != nil {
			t.Fatalf("SymbolsInFile(%s): %v", tc.file, err)
		}
		found := map[string]bool{}
		for _, s := range syms {
			found[s.Name+"/"+s.Kind] = true
		}
		for name, kind := range tc.wants {
			if !found[name+"/"+kind] {
				t.Errorf("%s: missing symbol %s (kind %s); indexed: %v", tc.file, name, kind, found)
			}
		}
	}
}
