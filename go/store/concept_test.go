// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"
)

func TestSaveAndLoadConcept(t *testing.T) {
	s := mustOpenStore(t)
	if err := s.SaveConcept("swipe", "gesture stuff", []string{"swipe", "fling", "Gesture"}); err != nil {
		t.Fatalf("SaveConcept: %v", err)
	}
	got, err := s.LoadConcept("swipe")
	if err != nil {
		t.Fatalf("LoadConcept: %v", err)
	}
	if got == nil {
		t.Fatal("expected concept, got nil")
	}
	if got.Name != "swipe" || got.Description != "gesture stuff" {
		t.Errorf("wrong header: %+v", got)
	}
	wantAliases := []string{"fling", "gesture", "swipe"} // normalized: lowercased, sorted, deduped
	if len(got.Aliases) != len(wantAliases) {
		t.Fatalf("alias count: got %d want %d (%v)", len(got.Aliases), len(wantAliases), got.Aliases)
	}
	for i, a := range wantAliases {
		if got.Aliases[i] != a {
			t.Errorf("alias[%d]: got %q want %q", i, got.Aliases[i], a)
		}
	}
}

func TestSaveConceptUpsert(t *testing.T) {
	s := mustOpenStore(t)
	if err := s.SaveConcept("c", "v1", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConcept("c", "v2", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.LoadConcept("c")
	if got.Description != "v2" {
		t.Errorf("description not updated: %q", got.Description)
	}
	if len(got.Aliases) != 2 {
		t.Errorf("alias count after upsert: %d", len(got.Aliases))
	}
}

func TestListAndDeleteConcept(t *testing.T) {
	s := mustOpenStore(t)
	_ = s.SaveConcept("a", "", []string{"x"})
	_ = s.SaveConcept("b", "", []string{"y"})
	list, err := s.ListConcepts()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListConcepts len: %d", len(list))
	}
	deleted, _ := s.DeleteConcept("a")
	if !deleted {
		t.Error("expected DeleteConcept to report deleted")
	}
	list, _ = s.ListConcepts()
	if len(list) != 1 || list[0].Name != "b" {
		t.Errorf("after delete, list: %+v", list)
	}
}

func TestFindByConceptRanksNameHitsHigher(t *testing.T) {
	s := mustOpenStore(t)
	mustInsertSymbol(t, s, "/proj/a.go", "OnSwipe", "function", " on swipe onswipe ")
	mustInsertSymbol(t, s, "/proj/b.go", "Handle", "function", " handle gesture pan ")
	mustInsertSymbol(t, s, "/proj/c.go", "Unrelated", "function", " hello world ")

	matches, err := s.FindByConcept([]string{"swipe", "gesture"}, []string{"owned"}, 0)
	if err != nil {
		t.Fatalf("FindByConcept: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(matches), matches)
	}
	// OnSwipe hits the "swipe" alias via the name tokens → name bonus.
	// Handle hits "gesture" only in the body → 1 point.
	if matches[0].Symbol.Name != "OnSwipe" {
		t.Errorf("expected OnSwipe first, got %s (score %d)", matches[0].Symbol.Name, matches[0].Score)
	}
	if matches[0].Score <= matches[1].Score {
		t.Errorf("expected OnSwipe to outrank Handle: %d vs %d", matches[0].Score, matches[1].Score)
	}
	if len(matches[0].NameHitAliases) == 0 {
		t.Error("expected NameHitAliases to be populated for OnSwipe")
	}
}

func TestFindByConceptScopeFilter(t *testing.T) {
	s := mustOpenStore(t)
	mustInsertFile(t, s, "/owned/a.go", "owned")
	mustInsertFile(t, s, "/lib/b.go", "library")
	mustInsertSymbolNoFile(t, s, "/owned/a.go", "Owned", " swipe ")
	mustInsertSymbolNoFile(t, s, "/lib/b.go", "Library", " swipe ")

	owned, _ := s.FindByConcept([]string{"swipe"}, []string{"owned"}, 0)
	if len(owned) != 1 || owned[0].Symbol.Name != "Owned" {
		t.Errorf("owned-only filter wrong: %+v", owned)
	}
	all, _ := s.FindByConcept([]string{"swipe"}, nil, 0)
	if len(all) != 2 {
		t.Errorf("no-filter expected 2 matches, got %d", len(all))
	}
}

func TestFindByConceptLimit(t *testing.T) {
	s := mustOpenStore(t)
	for i := range 5 {
		path := "/proj/f" + string(rune('0'+i)) + ".go"
		mustInsertSymbol(t, s, path, "swipe", "function", " swipe ")
	}
	matches, _ := s.FindByConcept([]string{"swipe"}, []string{"owned"}, 3)
	if len(matches) != 3 {
		t.Errorf("limit not applied: got %d", len(matches))
	}
}

// --- test helpers ---

func mustOpenStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustInsertFile(t *testing.T, s *Store, path, scope string) {
	t.Helper()
	if err := s.UpsertFile(path, "go", time.Time{}, "h", []byte("x"), scope); err != nil {
		t.Fatalf("UpsertFile %s: %v", path, err)
	}
}

func mustInsertSymbol(t *testing.T, s *Store, path, name, kind, evidence string) {
	t.Helper()
	mustInsertFile(t, s, path, "owned")
	rec := SymbolRecord{
		Name: name, Kind: kind, FilePath: path,
		StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1,
		StartByte: 0, EndByte: 0, Evidence: evidence,
	}
	// UpdateSymbols replaces the row set for the file, so accumulate.
	prior, _ := s.SymbolsInFile(path)
	if err := s.UpdateSymbols(path, append(prior, rec)); err != nil {
		t.Fatalf("UpdateSymbols: %v", err)
	}
}

func mustInsertSymbolNoFile(t *testing.T, s *Store, path, name, evidence string) {
	t.Helper()
	rec := SymbolRecord{
		Name: name, Kind: "function", FilePath: path,
		StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1,
		StartByte: 0, EndByte: 0, Evidence: evidence,
	}
	prior, _ := s.SymbolsInFile(path)
	if err := s.UpdateSymbols(path, append(prior, rec)); err != nil {
		t.Fatalf("UpdateSymbols: %v", err)
	}
}
