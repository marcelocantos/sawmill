// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/transform"
)

// InvariantRule is the top-level rule structure for a structural invariant.
type InvariantRule struct {
	ForEach ForEachClause   `json:"for_each"`
	Require []RequireClause `json:"require"`
}

// ForEachClause defines the set of code entities to check.
type ForEachClause struct {
	Kind         string `json:"kind"`         // "type", "function"
	Name         string `json:"name"`         // glob pattern, "*" = all
	Implementing string `json:"implementing"` // optional interface name (requires LSP)
}

// RequireClause is a single requirement that must hold for each matched entity.
type RequireClause struct {
	HasField  *FieldRequirement  `json:"has_field,omitempty"`
	HasMethod *MethodRequirement `json:"has_method,omitempty"`
}

// FieldRequirement checks that a named field exists (with optional type).
type FieldRequirement struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"` // optional type constraint
}

// MethodRequirement checks that a named method exists (with optional return type).
type MethodRequirement struct {
	Name    string `json:"name"`
	Returns string `json:"returns,omitempty"` // optional return type constraint
}

// ParseInvariantRule parses a JSON rule string into an InvariantRule.
func ParseInvariantRule(ruleJSON string) (*InvariantRule, error) {
	var rule InvariantRule
	if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
		return nil, fmt.Errorf("parsing rule JSON: %w", err)
	}
	if rule.ForEach.Kind == "" {
		return nil, fmt.Errorf("rule must have a for_each.kind")
	}
	if len(rule.Require) == 0 {
		return nil, fmt.Errorf("rule must have at least one require clause")
	}
	for i, req := range rule.Require {
		if req.HasField == nil && req.HasMethod == nil {
			return nil, fmt.Errorf("require[%d]: must have has_field or has_method", i)
		}
		if req.HasField != nil && req.HasField.Name == "" {
			return nil, fmt.Errorf("require[%d].has_field: name is required", i)
		}
		if req.HasMethod != nil && req.HasMethod.Name == "" {
			return nil, fmt.Errorf("require[%d].has_method: name is required", i)
		}
	}
	return &rule, nil
}

// globMatch reports whether name matches the glob pattern.
// Only '*' is supported (matches any sequence of characters).
func globMatch(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		// If the pattern is invalid, treat it as a literal match.
		return pattern == name
	}
	return matched
}

// CheckInvariant evaluates a single InvariantRule against all files in the
// forest and returns structured violations. Each Violation has Source set to
// "invariant:<name>".
func CheckInvariant(f *forest.Forest, invName string, rule *InvariantRule) ([]Violation, error) {
	var violations []Violation
	src := "invariant:" + invName

	if rule.ForEach.Implementing != "" {
		violations = append(violations, Violation{
			Source:   src,
			Severity: "warning",
			Rule:     invName,
			Message:  "'implementing' clause requires LSP (not yet supported syntactically); skipping implementing filter",
		})
	}

	// Map the abstract kind to the transform kind string.
	queryKind := rule.ForEach.Kind
	switch queryKind {
	case "type":
		// OK
	case "function":
		// OK
	default:
		return nil, fmt.Errorf("unsupported for_each.kind: %q (supported: type, function)", rule.ForEach.Kind)
	}

	for _, file := range f.Files {
		matchSpec := transform.AbstractMatch(queryKind, rule.ForEach.Name, "")
		results, err := transform.QueryFile(file, matchSpec)
		if err != nil {
			// Skip files where the kind is not supported by the adapter.
			continue
		}

		for _, result := range results {
			entityName := result.Name
			if entityName == "" {
				continue
			}
			// Apply the glob filter (QueryFile already does this via AbstractMatch,
			// but double-check for correctness with our own glob logic).
			if !globMatch(rule.ForEach.Name, entityName) {
				continue
			}

			line := int(result.StartLine)
			col := int(result.StartCol)

			for _, req := range rule.Require {
				var msg string
				if req.HasField != nil {
					ok, err := checkHasField(file, entityName, req.HasField)
					if err != nil {
						msg = fmt.Sprintf("%s: checking has_field %q: %v", entityName, req.HasField.Name, err)
					} else if !ok {
						if req.HasField.Type != "" {
							msg = fmt.Sprintf("%s: missing field %q of type %q", entityName, req.HasField.Name, req.HasField.Type)
						} else {
							msg = fmt.Sprintf("%s: missing field %q", entityName, req.HasField.Name)
						}
					}
				} else if req.HasMethod != nil {
					ok, err := checkHasMethod(file, entityName, req.HasMethod)
					if err != nil {
						msg = fmt.Sprintf("%s: checking has_method %q: %v", entityName, req.HasMethod.Name, err)
					} else if !ok {
						if req.HasMethod.Returns != "" {
							msg = fmt.Sprintf("%s: missing method %q returning %q", entityName, req.HasMethod.Name, req.HasMethod.Returns)
						} else {
							msg = fmt.Sprintf("%s: missing method %q", entityName, req.HasMethod.Name)
						}
					}
				}
				if msg != "" {
					violations = append(violations, Violation{
						Source:   src,
						File:     file.Path,
						Line:     line,
						Column:   col,
						Severity: "error",
						Rule:     invName,
						Message:  msg,
					})
				}
			}
		}
	}

	return violations, nil
}

// FormatInvariantViolation renders a structured invariant violation as the
// human-readable line that handleCheckInvariants used to emit verbatim.
// Preserved for backwards-compatible prose output.
func FormatInvariantViolation(v Violation) string {
	if v.File == "" {
		return v.Message
	}
	return fmt.Sprintf("%s: %s", v.File, v.Message)
}

// typeByteRange holds the start and end byte offsets of a type definition node.
type typeByteRange struct {
	start, end uint
}

// findTypeByteRange finds the byte range of the named type definition in the file.
// Returns (0, 0) if not found.
func findTypeByteRange(file *forest.ParsedFile, typeName string) (typeByteRange, error) {
	typeDefQuery := file.Adapter.TypeDefQuery()
	if typeDefQuery == "" {
		return typeByteRange{}, nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, typeDefQuery)
	if qErr != nil {
		return typeByteRange{}, fmt.Errorf("compiling type def query: %v", qErr)
	}
	defer query.Close()

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	nameIdx := invCaptureIndex(query, "name")
	// Find the first "whole node" capture index (anything other than "name").
	typeDefIdx := -1
	for i, n := range query.CaptureNames() {
		if n != "name" {
			typeDefIdx = i
			break
		}
	}

	for match := matches.Next(); match != nil; match = matches.Next() {
		// Extract name from capture.
		name := invCaptureText(file, match.Captures, nameIdx)
		if name != typeName {
			continue
		}

		// Found the type. Return its byte range from the whole-node capture.
		if typeDefIdx >= 0 {
			for i := range match.Captures {
				if match.Captures[i].Index == uint32(typeDefIdx) {
					n := &match.Captures[i].Node
					return typeByteRange{start: n.StartByte(), end: n.EndByte()}, nil
				}
			}
		}

		// Fallback: use the name node's range extended to cover the whole line.
		for i := range match.Captures {
			if match.Captures[i].Index == uint32(nameIdx) {
				n := &match.Captures[i].Node
				// Return the immediate parent's range if available.
				// Since we can't reliably call Parent() without null-node risk,
				// just return the name node's range as a fallback.
				return typeByteRange{start: n.StartByte(), end: n.EndByte()}, nil
			}
		}
	}
	return typeByteRange{}, nil
}

// checkHasField checks whether the named type in file has a field matching req.
// For Go, fields are inside the struct body (field_declaration nodes).
func checkHasField(file *forest.ParsedFile, typeName string, req *FieldRequirement) (bool, error) {
	fieldQuery := file.Adapter.FieldQuery()
	if fieldQuery == "" {
		// Language doesn't support field queries; assume satisfied.
		return true, nil
	}

	// Find the type definition byte range.
	tr, err := findTypeByteRange(file, typeName)
	if err != nil {
		return false, err
	}
	if tr.start == 0 && tr.end == 0 {
		// Type not found in this file; not a violation for this file.
		return true, nil
	}

	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, fieldQuery)
	if qErr != nil {
		return false, fmt.Errorf("compiling field query: %v", qErr)
	}
	defer query.Close()

	// Get the subtree root node for the type's byte range.
	rootNode := file.Tree.RootNode()
	typeSubtree := rootNode.DescendantForByteRange(tr.start, tr.end)
	if typeSubtree == nil {
		return true, nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.SetByteRange(tr.start, tr.end)

	matches := cursor.Matches(query, typeSubtree, file.OriginalSource)

	nameIdx := invCaptureIndex(query, "name")
	typeIdx := invCaptureIndex(query, "type")

	for match := matches.Next(); match != nil; match = matches.Next() {
		fieldName := invCaptureText(file, match.Captures, nameIdx)
		if fieldName != req.Name {
			continue
		}
		// Field name matches. Check type if required.
		if req.Type == "" {
			return true, nil
		}
		if typeIdx >= 0 {
			fieldType := strings.TrimSpace(invCaptureText(file, match.Captures, typeIdx))
			if fieldType == req.Type {
				return true, nil
			}
		}
	}

	return false, nil
}

// checkHasMethod checks whether the named type in file has a method matching req.
// For Go, methods are standalone method_declaration nodes with a receiver type.
func checkHasMethod(file *forest.ParsedFile, typeName string, req *MethodRequirement) (bool, error) {
	methodQuery := file.Adapter.MethodQuery()
	if methodQuery == "" {
		// Language doesn't support method queries; assume satisfied.
		return true, nil
	}

	// First check: methods inside the type body (Python, TypeScript, C++).
	tr, err := findTypeByteRange(file, typeName)
	if err != nil {
		return false, err
	}

	if tr.start != 0 || tr.end != 0 {
		found, err := searchMethodsInByteRange(file, methodQuery, tr, req)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}

	// Second check: standalone methods (Go: method_declaration with receiver).
	// Search the whole file for methods with a receiver type matching typeName.
	found, err := searchStandaloneGoMethods(file, methodQuery, typeName, req)
	if err != nil {
		return false, err
	}
	return found, nil
}

// searchMethodsInByteRange searches for a method within a type's byte range.
func searchMethodsInByteRange(file *forest.ParsedFile, methodQuery string, tr typeByteRange, req *MethodRequirement) (bool, error) {
	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, methodQuery)
	if qErr != nil {
		return false, fmt.Errorf("compiling method query: %v", qErr)
	}
	defer query.Close()

	rootNode := file.Tree.RootNode()
	typeSubtree := rootNode.DescendantForByteRange(tr.start, tr.end)
	if typeSubtree == nil {
		return false, nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.SetByteRange(tr.start, tr.end)

	matches := cursor.Matches(query, typeSubtree, file.OriginalSource)

	nameIdx := invCaptureIndex(query, "name")

	for match := matches.Next(); match != nil; match = matches.Next() {
		methodName := invCaptureText(file, match.Captures, nameIdx)
		if methodName == req.Name {
			return true, nil
		}
	}
	return false, nil
}

// searchStandaloneGoMethods searches for Go-style standalone method declarations
// with a receiver type matching typeName.
func searchStandaloneGoMethods(file *forest.ParsedFile, methodQuery, typeName string, req *MethodRequirement) (bool, error) {
	lang := file.Adapter.Language()
	query, qErr := tree_sitter.NewQuery(lang, methodQuery)
	if qErr != nil {
		return false, fmt.Errorf("compiling method query: %v", qErr)
	}
	defer query.Close()

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	nameIdx := invCaptureIndex(query, "name")
	// Find the "method" or other whole-node capture for the method declaration.
	methodIdx := -1
	for i, n := range query.CaptureNames() {
		if n != "name" {
			methodIdx = i
			break
		}
	}

	for match := matches.Next(); match != nil; match = matches.Next() {
		methodName := invCaptureText(file, match.Captures, nameIdx)
		if methodName != req.Name {
			continue
		}

		// Check if this method's text contains the type name (receiver heuristic).
		if methodIdx >= 0 {
			methodText := invCaptureText(file, match.Captures, methodIdx)
			if strings.Contains(methodText, typeName) {
				return true, nil
			}
		}
	}
	return false, nil
}

// invCaptureIndex returns the index of a capture name in a query, or -1 if absent.
func invCaptureIndex(query *tree_sitter.Query, name string) int {
	for i, n := range query.CaptureNames() {
		if n == name {
			return i
		}
	}
	return -1
}

// invCaptureText returns the source text of the capture at the given index,
// or "" if the capture is not present.
func invCaptureText(file *forest.ParsedFile, captures []tree_sitter.QueryCapture, idx int) string {
	if idx < 0 {
		return ""
	}
	for i := range captures {
		if captures[i].Index == uint32(idx) {
			n := &captures[i].Node
			return string(file.OriginalSource[n.StartByte():n.EndByte()])
		}
	}
	return ""
}
