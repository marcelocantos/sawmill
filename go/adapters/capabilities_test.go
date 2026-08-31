// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"testing"
)

func TestAllLanguagesNonEmpty(t *testing.T) {
	all := AllLanguages()
	if len(all) < 18 {
		t.Fatalf("expected at least 18 languages, got %d", len(all))
	}
	seen := map[string]bool{}
	for _, info := range all {
		if info.ID == "" || info.Name == "" {
			t.Errorf("empty id/name: %+v", info)
		}
		if seen[info.ID] {
			t.Errorf("duplicate id %q", info.ID)
		}
		seen[info.ID] = true
		if len(info.Extensions) == 0 {
			t.Errorf("%s has no extensions", info.ID)
		}
		// Every catalog extension must resolve via ForExtension.
		for _, ext := range info.Extensions {
			if ForExtension(ext) == nil {
				t.Errorf("%s ext %q not registered in ForExtension", info.ID, ext)
			}
		}
	}
}

func TestLookupLanguage(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"go", "go"},
		{"Go", "go"},
		{"lua", "lua"},
		{".lua", "lua"},
		{"bash", "bash"},
		{"sh", "bash"},
		{"shell", "bash"},
		{"proto", "protobuf"},
		{".proto", "protobuf"},
		{"protobuf", "protobuf"},
		{"sql", "sql"},
		{"zig", "zig"},
		{"c++", "cpp"},
		{"ts", "typescript"},
		{"js", "javascript"},
	}
	for _, tc := range cases {
		got := LookupLanguage(tc.key)
		if got == nil {
			t.Errorf("LookupLanguage(%q) = nil, want %s", tc.key, tc.want)
			continue
		}
		if got.ID != tc.want {
			t.Errorf("LookupLanguage(%q).ID = %q, want %q", tc.key, got.ID, tc.want)
		}
	}
	if LookupLanguage("brainfuck") != nil {
		t.Error("expected nil for unknown language")
	}
}

func TestBashAndSQLCaveatsPresent(t *testing.T) {
	bash := LookupLanguage("bash")
	if bash == nil || bash.AddField {
		t.Fatalf("bash should exist and not support add_field: %+v", bash)
	}
	if len(bash.Notes) == 0 {
		t.Error("bash should carry agent-facing notes")
	}
	sql := LookupLanguage("sql")
	if sql == nil || len(sql.Notes) == 0 {
		t.Fatalf("sql should exist with notes: %+v", sql)
	}
	proto := LookupLanguage("proto")
	if proto == nil || len(proto.Notes) == 0 {
		t.Fatalf("protobuf should exist with notes: %+v", proto)
	}
}
