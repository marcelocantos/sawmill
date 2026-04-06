// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFileUpsertAndCheck(t *testing.T) {
	s := openTestStore(t)

	mtime := time.Date(2026, 4, 6, 12, 0, 0, 123456789, time.UTC)

	if err := s.UpsertFile("src/main.go", "go", mtime, "abc123"); err != nil {
		t.Fatal(err)
	}

	// Check with correct mtime.
	hash, err := s.CheckFile("src/main.go", mtime)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "abc123" {
		t.Errorf("expected hash abc123, got %q", hash)
	}

	// Check with wrong mtime.
	hash, err = s.CheckFile("src/main.go", mtime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Errorf("expected empty hash for stale mtime, got %q", hash)
	}

	// Check missing file.
	hash, err = s.CheckFile("nonexistent.go", mtime)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Errorf("expected empty hash for missing file, got %q", hash)
	}
}

func TestFileRemoveAndTracked(t *testing.T) {
	s := openTestStore(t)

	mtime := time.Now()
	s.UpsertFile("a.go", "go", mtime, "h1")
	s.UpsertFile("b.go", "go", mtime, "h2")

	files, err := s.TrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 tracked files, got %d", len(files))
	}

	if err := s.RemoveFile("a.go"); err != nil {
		t.Fatal(err)
	}

	files, err = s.TrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "b.go" {
		t.Errorf("expected [b.go], got %v", files)
	}
}

func TestSymbolsCRUD(t *testing.T) {
	s := openTestStore(t)

	mtime := time.Now()
	s.UpsertFile("lib.py", "python", mtime, "h1")

	symbols := []SymbolRecord{
		{Name: "compute", Kind: "function", FilePath: "lib.py", StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 8, StartByte: 0, EndByte: 20},
		{Name: "helper", Kind: "function", FilePath: "lib.py", StartLine: 4, StartCol: 1, EndLine: 5, EndCol: 8, StartByte: 21, EndByte: 40},
		{Name: "Config", Kind: "type", FilePath: "lib.py", StartLine: 7, StartCol: 1, EndLine: 10, EndCol: 1, StartByte: 41, EndByte: 80},
	}

	if err := s.UpdateSymbols("lib.py", symbols); err != nil {
		t.Fatal(err)
	}

	// Exact name match.
	found, err := s.FindSymbols("compute", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "compute" {
		t.Errorf("expected [compute], got %v", found)
	}

	// Prefix match.
	found, err = s.FindSymbols("comp*", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "compute" {
		t.Errorf("prefix match: expected [compute], got %v", found)
	}

	// Kind filter.
	found, err = s.FindSymbols("Config", "type")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Kind != "type" {
		t.Errorf("kind filter: expected type, got %v", found)
	}

	// Kind filter mismatch.
	found, err = s.FindSymbols("Config", "function")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("kind mismatch: expected empty, got %v", found)
	}

	// Symbols in file.
	found, err = s.SymbolsInFile("lib.py")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Errorf("expected 3 symbols in file, got %d", len(found))
	}
}

func TestSymbolsCascadeDelete(t *testing.T) {
	s := openTestStore(t)

	mtime := time.Now()
	s.UpsertFile("lib.py", "python", mtime, "h1")
	s.UpdateSymbols("lib.py", []SymbolRecord{
		{Name: "foo", Kind: "function", FilePath: "lib.py", StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1},
	})

	// Removing file should cascade-delete symbols.
	s.RemoveFile("lib.py")

	found, err := s.SymbolsInFile("lib.py")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 symbols after cascade delete, got %d", len(found))
	}
}

func TestRecipesCRUD(t *testing.T) {
	s := openTestStore(t)

	steps := json.RawMessage(`[{"type":"rename","from":"old","to":"new"}]`)
	if err := s.SaveRecipe("rename-func", "Rename a function", []string{"from", "to"}, steps); err != nil {
		t.Fatal(err)
	}

	// Load.
	recipe, err := s.LoadRecipe("rename-func")
	if err != nil {
		t.Fatal(err)
	}
	if recipe == nil {
		t.Fatal("expected recipe, got nil")
	}
	if recipe.Description != "Rename a function" {
		t.Errorf("description: got %q", recipe.Description)
	}
	if len(recipe.Params) != 2 || recipe.Params[0] != "from" || recipe.Params[1] != "to" {
		t.Errorf("params: got %v", recipe.Params)
	}

	// List.
	recipes, err := s.ListRecipes()
	if err != nil {
		t.Fatal(err)
	}
	if len(recipes) != 1 || recipes[0].Name != "rename-func" {
		t.Errorf("list: got %v", recipes)
	}

	// Load missing.
	missing, err := s.LoadRecipe("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing recipe, got %v", missing)
	}

	// Delete.
	deleted, err := s.DeleteRecipe("rename-func")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected delete to return true")
	}

	deleted, err = s.DeleteRecipe("rename-func")
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Error("expected second delete to return false")
	}
}

func TestConventionsCRUD(t *testing.T) {
	s := openTestStore(t)

	if err := s.SaveConvention("no-print", "No print statements", `return ctx.query({kind:"call",name:"print"}).map(function(c){return c.file+":"+c.startLine;})`); err != nil {
		t.Fatal(err)
	}

	// List.
	conventions, err := s.ListConventions()
	if err != nil {
		t.Fatal(err)
	}
	if len(conventions) != 1 || conventions[0].Name != "no-print" {
		t.Errorf("list: got %v", conventions)
	}
	if conventions[0].CheckProgram == "" {
		t.Error("check_program should not be empty")
	}

	// Delete.
	deleted, err := s.DeleteConvention("no-print")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected delete to return true")
	}

	conventions, err = s.ListConventions()
	if err != nil {
		t.Fatal(err)
	}
	if len(conventions) != 0 {
		t.Errorf("expected empty after delete, got %v", conventions)
	}
}

func TestSymbolUpdateReplacesOld(t *testing.T) {
	s := openTestStore(t)

	mtime := time.Now()
	s.UpsertFile("lib.py", "python", mtime, "h1")

	// First set.
	s.UpdateSymbols("lib.py", []SymbolRecord{
		{Name: "old_func", Kind: "function", FilePath: "lib.py", StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1},
	})

	// Replace with new set.
	s.UpdateSymbols("lib.py", []SymbolRecord{
		{Name: "new_func", Kind: "function", FilePath: "lib.py", StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1},
	})

	found, err := s.FindSymbols("old_func", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("old_func should be gone, got %v", found)
	}

	found, err = s.FindSymbols("new_func", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Errorf("expected new_func, got %v", found)
	}
}
