// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package forest_test

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/forest"
)

func TestShouldParseAcceptsNormalSource(t *testing.T) {
	src := []byte("package main\n\nfunc main() {}\n")
	if !forest.ShouldParse(src) {
		t.Errorf("normal source rejected")
	}
}

func TestShouldParseRejectsOversize(t *testing.T) {
	src := make([]byte, forest.MaxFileSize+1)
	for i := range src {
		src[i] = '\n'
	}
	if forest.ShouldParse(src) {
		t.Errorf("oversized source accepted")
	}
}

func TestShouldParseRejectsMinifiedMultiLine(t *testing.T) {
	line := strings.Repeat("a", forest.MaxAvgLineLength*2)
	src := []byte(line + "\n" + line + "\n" + line + "\n")
	if forest.ShouldParse(src) {
		t.Errorf("multi-line minified source accepted")
	}
}

func TestShouldParseRejectsSingleLineHuge(t *testing.T) {
	src := []byte(strings.Repeat("a", forest.MaxAvgLineLength*2))
	if forest.ShouldParse(src) {
		t.Errorf("no-newline source over threshold accepted")
	}
}

func TestShouldParseAcceptsEmpty(t *testing.T) {
	if !forest.ShouldParse(nil) {
		t.Errorf("nil source rejected")
	}
	if !forest.ShouldParse([]byte{}) {
		t.Errorf("empty source rejected")
	}
}
