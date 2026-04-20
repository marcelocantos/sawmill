// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package bisect implements semantic git bisect — finding the commit where a
// structural predicate over the AST flipped its value, without ever running
// the code. Builds on the gitindex layer (lazy commit indexing) and the
// semdiff package (structural change attribution).
package bisect

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
)

// PredicateKind enumerates the supported structural predicates.
const (
	KindSymbolExists     = "symbol_exists"
	KindFunctionHasParam = "function_has_param"
	KindTypeHasField     = "type_has_field"
)

// Predicate is a structural property of the codebase at a commit. Predicates
// are JSON-encoded for transport and parsed by ParsePredicate.
type Predicate struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`     // for symbol_exists
	Function string `json:"function,omitempty"` // for function_has_param
	Type     string `json:"type,omitempty"`     // for type_has_field
	Param    string `json:"param,omitempty"`    // for function_has_param
	Field    string `json:"field,omitempty"`    // for type_has_field
	File     string `json:"file,omitempty"`     // optional: restrict to one file
}

// ParsePredicate parses a JSON-encoded predicate string.
func ParsePredicate(s string) (*Predicate, error) {
	var p Predicate
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, fmt.Errorf("parsing predicate JSON: %w", err)
	}
	switch p.Kind {
	case KindSymbolExists:
		if p.Name == "" {
			return nil, fmt.Errorf("symbol_exists requires name")
		}
	case KindFunctionHasParam:
		if p.Function == "" || p.Param == "" {
			return nil, fmt.Errorf("function_has_param requires function and param")
		}
	case KindTypeHasField:
		if p.Type == "" || p.Field == "" {
			return nil, fmt.Errorf("type_has_field requires type and field")
		}
	case "":
		return nil, fmt.Errorf("predicate kind is required")
	default:
		return nil, fmt.Errorf("unknown predicate kind %q", p.Kind)
	}
	return &p, nil
}

// String returns a human-readable description of the predicate.
func (p *Predicate) String() string {
	switch p.Kind {
	case KindSymbolExists:
		return fmt.Sprintf("symbol %q exists", p.Name)
	case KindFunctionHasParam:
		return fmt.Sprintf("function %q has parameter %q", p.Function, p.Param)
	case KindTypeHasField:
		return fmt.Sprintf("type %q has field %q", p.Type, p.Field)
	default:
		return p.Kind
	}
}

// Subject returns the symbol name the predicate is about (function name,
// type name, or symbol name). Used to locate the relevant SymbolChange when
// attributing a structural change.
func (p *Predicate) Subject() string {
	switch p.Kind {
	case KindSymbolExists:
		return p.Name
	case KindFunctionHasParam:
		return p.Function
	case KindTypeHasField:
		return p.Type
	}
	return ""
}

// Eval evaluates the predicate against an indexed commit. The caller is
// responsible for ensuring the commit is indexed before calling.
func (p *Predicate) Eval(store *gitindex.Store, repo *gitrepo.Repo, commitSHA string) (bool, error) {
	files, err := store.CommitFiles(commitSHA)
	if err != nil {
		return false, fmt.Errorf("listing files for %s: %w", commitSHA, err)
	}
	for _, f := range files {
		if p.File != "" && f.FilePath != p.File {
			continue
		}
		ok, err := p.evalBlob(store, repo, f.BlobSHA, f.FilePath)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (p *Predicate) evalBlob(store *gitindex.Store, repo *gitrepo.Repo, blobSHA, _ string) (bool, error) {
	indexed, err := store.IsIndexed(blobSHA)
	if err != nil {
		return false, err
	}
	if !indexed {
		// Unsupported language or unparseable — predicate is false here.
		return false, nil
	}
	source, err := repo.ReadBlob(blobSHA)
	if err != nil {
		return false, err
	}
	symbols, err := store.SymbolNames(blobSHA, source)
	if err != nil {
		return false, err
	}

	switch p.Kind {
	case KindSymbolExists:
		for _, s := range symbols {
			if s.Name == p.Name {
				return true, nil
			}
		}
		return false, nil

	case KindFunctionHasParam:
		for _, s := range symbols {
			if s.Name != p.Function || s.Kind != "function" {
				continue
			}
			params, err := paramNames(store, source, s.NodeID)
			if err != nil {
				return false, err
			}
			for _, param := range params {
				if containsIdentifier(param, p.Param) {
					return true, nil
				}
			}
		}
		return false, nil

	case KindTypeHasField:
		for _, s := range symbols {
			if s.Name != p.Type || s.Kind != "type" {
				continue
			}
			if s.DeclStartByte < 0 || s.DeclEndByte > len(source) {
				continue
			}
			decl := string(source[s.DeclStartByte:s.DeclEndByte])
			if containsIdentifier(decl, p.Field) {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("unknown predicate kind: %s", p.Kind)
}

// paramNames extracts parameter source text from a function declaration by
// querying the parameter_list children.
func paramNames(store *gitindex.Store, source []byte, fnNodeID int64) ([]string, error) {
	children, err := store.QueryChildren(fnNodeID)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.FieldName == "parameters" || child.NodeType == "parameter_list" {
			return paramNamesFromList(store, source, child.ID)
		}
	}
	return nil, nil
}

func paramNamesFromList(store *gitindex.Store, source []byte, listID int64) ([]string, error) {
	children, err := store.QueryChildren(listID)
	if err != nil {
		return nil, err
	}
	var params []string
	for _, c := range children {
		if c.StartByte < 0 || c.EndByte > len(source) {
			continue
		}
		text := strings.TrimSpace(string(source[c.StartByte:c.EndByte]))
		if text == "" || text == "(" || text == ")" || text == "," {
			continue
		}
		params = append(params, text)
	}
	return params, nil
}

// containsIdentifier reports whether ident appears in text as a complete
// identifier (bounded by non-identifier characters or text edges).
func containsIdentifier(text, ident string) bool {
	if ident == "" {
		return false
	}
	idx := 0
	for {
		i := strings.Index(text[idx:], ident)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(ident)
		var before, after byte = ' ', ' '
		if start > 0 {
			before = text[start-1]
		}
		if end < len(text) {
			after = text[end]
		}
		if !isIdentChar(before) && !isIdentChar(after) {
			return true
		}
		idx = start + 1
	}
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
