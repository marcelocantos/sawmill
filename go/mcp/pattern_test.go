// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"testing"
)

func TestParsePatternSimple(t *testing.T) {
	p := ParsePattern("EqArgs{Eq: $eq, Hash: $hash}")
	// Segments: "EqArgs{Eq: "+$eq, ", Hash: "+$hash, "}"+none
	if len(p.Segments) != 3 {
		t.Fatalf("expected 3 segments, got %d: %+v", len(p.Segments), p.Segments)
	}
}

func TestPatternMatchConstruction(t *testing.T) {
	p := ParsePattern("EqArgs{Eq: $eq, Hash: $hash}")
	caps, ok := p.Match("EqArgs{Eq: cmpFunc, Hash: hashFunc}")
	if !ok {
		t.Fatal("expected match")
	}
	if caps["eq"] != "cmpFunc" {
		t.Errorf("expected eq=cmpFunc, got %q", caps["eq"])
	}
	if caps["hash"] != "hashFunc" {
		t.Errorf("expected hash=hashFunc, got %q", caps["hash"])
	}
}

func TestPatternMatchNoMatch(t *testing.T) {
	p := ParsePattern("EqArgs{Eq: $eq, Hash: $hash}")
	_, ok := p.Match("OtherType{Foo: 1}")
	if ok {
		t.Fatal("expected no match")
	}
}

func TestPatternApply(t *testing.T) {
	captures := map[string]string{"eq": "cmpFunc", "hash": "hashFunc"}
	result := Apply("NewDefaultEqOps($eq, $hash)", captures)
	if result != "NewDefaultEqOps(cmpFunc, hashFunc)" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestPatternInstancePlaceholder(t *testing.T) {
	p := ParsePattern("$.Eq($a, $b)")
	caps, ok := p.Match("args.Eq(a, b)")
	if !ok {
		t.Fatal("expected match")
	}
	if caps["$"] != "args" {
		t.Errorf("expected $=args, got %q", caps["$"])
	}
	if caps["a"] != "a" {
		t.Errorf("expected a=a, got %q", caps["a"])
	}
	if caps["b"] != "b" {
		t.Errorf("expected b=b, got %q", caps["b"])
	}

	result := Apply("$.Equal($a, $b)", caps)
	if result != "args.Equal(a, b)" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestPatternFieldAccess(t *testing.T) {
	p := ParsePattern("$.FullHash")
	caps, ok := p.Match("args.FullHash")
	if !ok {
		t.Fatal("expected match")
	}
	if caps["$"] != "args" {
		t.Errorf("expected $=args, got %q", caps["$"])
	}

	result := Apply("$.IsFullHash()", caps)
	if result != "args.IsFullHash()" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestPatternPureLiteral(t *testing.T) {
	p := ParsePattern("hello")
	_, ok := p.Match("hello")
	if !ok {
		t.Fatal("expected match")
	}
	_, ok = p.Match("world")
	if ok {
		t.Fatal("expected no match")
	}
}
