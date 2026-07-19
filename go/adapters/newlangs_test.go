// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"os"
	"path/filepath"
	"testing"
)

const luaSample = `Widget = {
  w = 1,
}

function calc(x)
  return x
end

local helper = require("helper")

calc(1)
`

const protoSample = `syntax = "proto3";

import "other.proto";

message Widget {
  int32 w = 1;
  string name = 2;
}

service Math {
  rpc calc(Req) returns (Req);
}
`

const zigSample = `const std = @import("std");

const Widget = struct {
    w: i32,

    pub fn get(self: Widget) i32 {
        return self.w;
    }
};

fn calc(x: i32) i32 {
    return x;
}

pub fn main() void {
    _ = calc(1);
}
`

const bashSample = `#!/usr/bin/env bash
source ./helper.sh
. ./lib.sh

calc() {
  local x="$1"
  echo "$x"
}

use() {
  calc 1
}
`

const sqlSample = `CREATE TABLE Widget (
  w INTEGER,
  name TEXT
);

CREATE OR REPLACE FUNCTION calc(x int) RETURNS int AS 'SELECT $1' LANGUAGE sql;

SELECT calc(1);
`

func TestLuaAdapterQueries(t *testing.T) {
	a := &LuaAdapter{}
	src := []byte(luaSample)
	assertCaptures(t, a, a.FunctionDefQuery(), src, "calc")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Widget")
	assertCaptures(t, a, a.FieldQuery(), src, "w")
	assertCaptures(t, a, a.CallExprQuery(), src, "calc", "require")
	assertCaptures(t, a, a.ImportQuery(), src, "helper")
	assertCaptures(t, a, a.IdentifierQuery(), src, "calc", "Widget", "w")
}

func TestProtoAdapterQueries(t *testing.T) {
	a := &ProtoAdapter{}
	src := []byte(protoSample)
	assertCaptures(t, a, a.FunctionDefQuery(), src, "calc")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Widget")
	assertCaptures(t, a, a.FieldQuery(), src, "w", "name")
	assertCaptures(t, a, a.ImportQuery(), src, `"other.proto"`)
	assertCaptures(t, a, a.IdentifierQuery(), src, "Widget", "calc")
}

func TestZigAdapterQueries(t *testing.T) {
	a := &ZigAdapter{}
	src := []byte(zigSample)
	assertCaptures(t, a, a.FunctionDefQuery(), src, "calc", "main", "get")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Widget")
	assertCaptures(t, a, a.FieldQuery(), src, "w")
	assertCaptures(t, a, a.CallExprQuery(), src, "calc")
	assertCaptures(t, a, a.ImportQuery(), src, "std")
	assertCaptures(t, a, a.IdentifierQuery(), src, "calc", "Widget")
}

func TestBashAdapterQueries(t *testing.T) {
	a := &BashAdapter{}
	src := []byte(bashSample)
	assertCaptures(t, a, a.FunctionDefQuery(), src, "calc", "use")
	assertCaptures(t, a, a.CallExprQuery(), src, "calc", "echo", "source")
	assertCaptures(t, a, a.ImportQuery(), src, "./helper.sh", "./lib.sh")
	assertCaptures(t, a, a.IdentifierQuery(), src, "calc", "use")
	if q := a.TypeDefQuery(); q != "" {
		t.Errorf("Bash TypeDefQuery should be empty, got %q", q)
	}
	if got := a.GenField("x", "int"); got != "" {
		t.Errorf("Bash GenField should be empty, got %q", got)
	}
}

func TestSqlAdapterQueries(t *testing.T) {
	a := &SqlAdapter{}
	src := []byte(sqlSample)
	assertCaptures(t, a, a.FunctionDefQuery(), src, "calc")
	assertCaptures(t, a, a.TypeDefQuery(), src, "Widget")
	assertCaptures(t, a, a.FieldQuery(), src, "w", "name")
	assertCaptures(t, a, a.CallExprQuery(), src, "calc")
	assertCaptures(t, a, a.IdentifierQuery(), src, "Widget", "calc")
}

func TestForExtensionNewLanguages(t *testing.T) {
	cases := map[string]LanguageAdapter{
		"lua":   &LuaAdapter{},
		"proto": &ProtoAdapter{},
		"zig":   &ZigAdapter{},
		"sh":    &BashAdapter{},
		"bash":  &BashAdapter{},
		"sql":   &SqlAdapter{},
	}
	for ext, want := range cases {
		got := ForExtension(ext)
		if got == nil {
			t.Errorf("ForExtension(%q) = nil", ext)
			continue
		}
		switch want.(type) {
		case *LuaAdapter:
			if _, ok := got.(*LuaAdapter); !ok {
				t.Errorf("ForExtension(%q) type = %T", ext, got)
			}
		case *ProtoAdapter:
			if _, ok := got.(*ProtoAdapter); !ok {
				t.Errorf("ForExtension(%q) type = %T", ext, got)
			}
		case *ZigAdapter:
			if _, ok := got.(*ZigAdapter); !ok {
				t.Errorf("ForExtension(%q) type = %T", ext, got)
			}
		case *BashAdapter:
			if _, ok := got.(*BashAdapter); !ok {
				t.Errorf("ForExtension(%q) type = %T", ext, got)
			}
		case *SqlAdapter:
			if _, ok := got.(*SqlAdapter); !ok {
				t.Errorf("ForExtension(%q) type = %T", ext, got)
			}
		}
	}
}

func TestLuaImportPathResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "helper.lua")
	importing := filepath.Join(root, "app.lua")
	if err := os.WriteFile(target, []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &LuaAdapter{}
	if got := a.ResolveImportPath("helper", importing, root); got != "helper.lua" {
		t.Errorf("ResolveImportPath = %q", got)
	}
	if got := a.BuildImportPath(target, importing, root); got != "helper" {
		t.Errorf("BuildImportPath = %q", got)
	}
}

func TestProtoImportPathResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "other.proto")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("syntax = \"proto3\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &ProtoAdapter{}
	if got := a.ResolveImportPath("pkg/other.proto", "", root); got != filepath.Join("pkg", "other.proto") {
		t.Errorf("ResolveImportPath = %q", got)
	}
	if got := a.BuildImportPath(target, "", root); got != "pkg/other.proto" {
		t.Errorf("BuildImportPath = %q", got)
	}
}

func TestNewLanguagesEnvAndConst(t *testing.T) {
	for _, tc := range []struct {
		adapter LanguageAdapter
		envWant string
	}{
		{&LuaAdapter{}, `os.getenv("HOME")`},
		{&ZigAdapter{}, `/* getenv("HOME") */`},
		{&BashAdapter{}, `${HOME}`},
		{&SqlAdapter{}, `current_setting("HOME")`},
	} {
		if got := tc.adapter.GenEnvRead("HOME"); got != tc.envWant {
			t.Errorf("%T GenEnvRead = %q, want %q", tc.adapter, got, tc.envWant)
		}
	}
}
