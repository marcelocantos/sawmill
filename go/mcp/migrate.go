// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// MigrateRules defines the rules for a type shape migration.
type MigrateRules struct {
	Construction *ConstructionRule `json:"construction,omitempty"`
	FieldAccess  map[string]string `json:"field_access,omitempty"`
	TypeRename   string            `json:"type_rename,omitempty"`
}

// ConstructionRule defines old/new patterns for rewriting type construction.
type ConstructionRule struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// parseMigrateRules parses a JSON string into MigrateRules.
func parseMigrateRules(rulesJSON string) (*MigrateRules, error) {
	var rules MigrateRules
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("parsing rules JSON: %w", err)
	}
	if rules.Construction == nil && len(rules.FieldAccess) == 0 && rules.TypeRename == "" {
		return nil, fmt.Errorf("rules must specify at least one of: construction, field_access, type_rename")
	}
	return &rules, nil
}

// migrateTypeInFile applies migration rules to a single file and returns the
// edits to apply. The typeName is the identifier to search for.
func migrateTypeInFile(file *forest.ParsedFile, typeName string, rules *MigrateRules) []rewrite.Edit {
	var edits []rewrite.Edit

	// 1. Type rename: rename all identifier references.
	if rules.TypeRename != "" {
		edits = append(edits, collectIdentifierEdits(file, typeName, rules.TypeRename)...)
	}

	// 2. Construction rewriting.
	if rules.Construction != nil {
		edits = append(edits, collectConstructionEdits(file, typeName, rules.Construction)...)
	}

	// 3. Field access rewriting.
	if len(rules.FieldAccess) > 0 {
		edits = append(edits, collectFieldAccessEdits(file, typeName, rules.FieldAccess)...)
	}

	return removeOverlappingEdits(edits)
}

// removeOverlappingEdits removes edits that overlap with larger edits.
// When two edits overlap, the larger one (by span) wins. This handles the
// case where a construction rewrite subsumes a type rename edit.
func removeOverlappingEdits(edits []rewrite.Edit) []rewrite.Edit {
	if len(edits) <= 1 {
		return edits
	}

	// Sort by start position, then by descending span length (larger first).
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Start != edits[j].Start {
			return edits[i].Start < edits[j].Start
		}
		return (edits[i].End - edits[i].Start) > (edits[j].End - edits[j].Start)
	})

	var result []rewrite.Edit
	var lastEnd uint
	for _, e := range edits {
		if e.Start < lastEnd {
			// This edit overlaps with the previous one — skip it.
			continue
		}
		result = append(result, e)
		if e.End > lastEnd {
			lastEnd = e.End
		}
	}
	return result
}

// collectIdentifierEdits finds all identifier nodes matching oldName and
// returns edits to rename them to newName.
func collectIdentifierEdits(file *forest.ParsedFile, oldName, newName string) []rewrite.Edit {
	queryStr := file.Adapter.IdentifierQuery()
	if queryStr == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, err := tree_sitter.NewQuery(lang, queryStr)
	if err != nil {
		return nil
	}
	defer query.Close()

	nameIdx := -1
	for i, n := range query.CaptureNames() {
		if n == "name" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit
	for m := matches.Next(); m != nil; m = matches.Next() {
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index != uint32(nameIdx) {
				continue
			}
			text := string(file.OriginalSource[c.Node.StartByte():c.Node.EndByte()])
			if text == oldName {
				edits = append(edits, rewrite.Edit{
					Start:       c.Node.StartByte(),
					End:         c.Node.EndByte(),
					Replacement: newName,
				})
			}
		}
	}
	return edits
}

// collectConstructionEdits finds struct literal and call expression sites that
// reference typeName and tries to match the old construction pattern, producing
// a replacement from the new pattern.
func collectConstructionEdits(file *forest.ParsedFile, typeName string, rule *ConstructionRule) []rewrite.Edit {
	oldPat := ParsePattern(rule.Old)
	newPat := rule.New

	var edits []rewrite.Edit

	// Try struct literals.
	edits = append(edits, matchConstructionNodes(file, typeName, oldPat, newPat, file.Adapter.StructLiteralQuery(), "literal")...)

	// Try call expressions.
	edits = append(edits, matchConstructionNodes(file, typeName, oldPat, newPat, file.Adapter.CallExprQuery(), "call")...)

	return edits
}

// matchConstructionNodes runs a tree-sitter query that captures @name and
// @<captureLabel> and tries to match the whole expression text against the
// old pattern. If it matches, the new pattern with captures filled in is the
// replacement.
func matchConstructionNodes(
	file *forest.ParsedFile,
	typeName string,
	oldPat *Pattern,
	newPat string,
	queryStr string,
	captureLabel string,
) []rewrite.Edit {
	if queryStr == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, err := tree_sitter.NewQuery(lang, queryStr)
	if err != nil {
		return nil
	}
	defer query.Close()

	nameIdx := -1
	exprIdx := -1
	for i, n := range query.CaptureNames() {
		if n == "name" {
			nameIdx = i
		}
		if n == captureLabel {
			exprIdx = i
		}
	}
	if nameIdx < 0 || exprIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var edits []rewrite.Edit
	seen := make(map[uint]bool) // avoid duplicate edits at same position

	for m := matches.Next(); m != nil; m = matches.Next() {
		var exprNode, nNode *tree_sitter.Node
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index == uint32(exprIdx) && exprNode == nil {
				exprNode = &c.Node
			}
			if c.Index == uint32(nameIdx) && nNode == nil {
				nNode = &c.Node
			}
		}
		if exprNode == nil || nNode == nil {
			continue
		}

		name := string(file.OriginalSource[nNode.StartByte():nNode.EndByte()])
		if name != typeName {
			continue
		}

		if seen[exprNode.StartByte()] {
			continue
		}

		exprText := string(file.OriginalSource[exprNode.StartByte():exprNode.EndByte()])
		captures, ok := oldPat.Match(exprText)
		if !ok {
			continue
		}

		replacement := Apply(newPat, captures)
		seen[exprNode.StartByte()] = true
		edits = append(edits, rewrite.Edit{
			Start:       exprNode.StartByte(),
			End:         exprNode.EndByte(),
			Replacement: replacement,
		})
	}

	return edits
}

// collectFieldAccessEdits finds selector expressions (e.g. x.Method(a, b) or
// x.Field) where x might be an instance of the target type, and tries to match
// against each field_access rule.
//
// Heuristic for identifying instances: scan for short variable declarations or
// assignments where the RHS constructs the target type (struct literal or call
// to a factory named New<TypeName>).
func collectFieldAccessEdits(file *forest.ParsedFile, typeName string, rules map[string]string) []rewrite.Edit {
	// Collect variable names that are likely instances of the target type.
	instanceVars := findInstanceVariables(file, typeName)
	if len(instanceVars) == 0 {
		return nil
	}

	// Parse and prepare the rule patterns.
	type fieldRule struct {
		oldPat *Pattern
		newPat string
	}
	var parsedRules []fieldRule
	for oldStr, newStr := range rules {
		parsedRules = append(parsedRules, fieldRule{
			oldPat: ParsePattern(oldStr),
			newPat: newStr,
		})
	}
	// Sort for deterministic edit order.
	sort.Slice(parsedRules, func(i, j int) bool {
		return parsedRules[i].newPat < parsedRules[j].newPat
	})

	// Walk the entire source looking for <varName>.something patterns.
	// We use a simple text scan rather than tree-sitter queries to keep this
	// straightforward and language-agnostic.
	source := string(file.OriginalSource)
	var edits []rewrite.Edit
	seen := make(map[uint]bool)

	for varName := range instanceVars {
		// Find all occurrences of "varName." in the source.
		search := varName + "."
		pos := 0
		for {
			idx := strings.Index(source[pos:], search)
			if idx < 0 {
				break
			}
			absPos := pos + idx

			// Find the extent of this expression. We need to find the end:
			// it could be varName.Field or varName.Method(args).
			// Scan forward to find the end of the expression.
			exprEnd := findExpressionEnd(source, absPos)
			exprText := source[absPos:exprEnd]

			// Try each rule, binding $ to varName.
			for _, rule := range parsedRules {
				captures, ok := rule.oldPat.Match(exprText)
				if !ok {
					continue
				}
				// Ensure the $ capture (if any) matches our varName.
				if inst, hasInst := captures["$"]; hasInst && inst != varName {
					continue
				}
				captures["$"] = varName

				replacement := Apply(rule.newPat, captures)
				startByte := uint(absPos)
				if !seen[startByte] {
					seen[startByte] = true
					edits = append(edits, rewrite.Edit{
						Start:       startByte,
						End:         uint(exprEnd),
						Replacement: replacement,
					})
				}
				break // first matching rule wins
			}

			pos = absPos + 1
		}
	}

	return edits
}

// findExpressionEnd finds the end of an expression starting at pos.
// It handles identifiers, dots, and balanced parentheses.
func findExpressionEnd(source string, pos int) int {
	i := pos
	n := len(source)

	for i < n {
		ch := source[i]
		if ch == '(' {
			// Scan forward to matching close paren, respecting nesting.
			depth := 1
			i++
			for i < n && depth > 0 {
				switch source[i] {
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
			return i
		}
		if isIdentChar(ch) || ch == '.' {
			i++
			continue
		}
		break
	}
	return i
}

// isIdentChar returns true if ch is a valid identifier character.
func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_'
}

// findInstanceVariables scans the file for variable declarations where the RHS
// is a construction of the target type (struct literal or factory call).
func findInstanceVariables(file *forest.ParsedFile, typeName string) map[string]bool {
	vars := make(map[string]bool)

	// Strategy 1: Find short variable declarations where RHS is a struct literal
	// of the target type. We scan for patterns like "varName := TypeName{" or
	// "varName = TypeName{" in the source text.
	source := string(file.OriginalSource)

	// Look for struct literal assignments: "ident := TypeName{" or "ident = TypeName{"
	patterns := []string{
		":= " + typeName + "{",
		"= " + typeName + "{",
		":= " + typeName + "\n",
		"= " + typeName + "\n",
	}

	for _, pat := range patterns {
		pos := 0
		for {
			idx := strings.Index(source[pos:], pat)
			if idx < 0 {
				break
			}
			absPos := pos + idx

			// Walk backwards from absPos to find the variable name.
			varName := extractPrecedingIdentifier(source, absPos)
			if varName != "" {
				vars[varName] = true
			}

			pos = absPos + 1
		}
	}

	// Strategy 2: Look for factory function calls: "ident := NewTypeName(" or
	// "ident := newTypeName(" etc.
	factoryNames := file.Adapter.FactoryFuncNames(typeName)
	for _, fname := range factoryNames {
		pat := fname + "("
		pos := 0
		for {
			idx := strings.Index(source[pos:], pat)
			if idx < 0 {
				break
			}
			absPos := pos + idx

			// Check if preceded by ":= " or "= ".
			before := source[:absPos]
			trimmed := strings.TrimRight(before, " \t")
			if strings.HasSuffix(trimmed, ":=") || strings.HasSuffix(trimmed, "=") {
				assignPos := len(trimmed)
				if strings.HasSuffix(trimmed, ":=") {
					assignPos -= 2
				} else {
					assignPos -= 1
				}
				varName := extractPrecedingIdentifier(source, assignPos)
				if varName != "" {
					vars[varName] = true
				}
			}

			pos = absPos + 1
		}
	}

	// Strategy 3: Look for function parameter declarations with the type name.
	// Use the FunctionDefQuery to find parameters typed as the target type.
	paramVars := findTypedParameters(file, typeName)
	for _, v := range paramVars {
		vars[v] = true
	}

	return vars
}

// extractPrecedingIdentifier walks backwards from pos (exclusive) through
// whitespace, then extracts the preceding identifier.
func extractPrecedingIdentifier(source string, pos int) string {
	// Skip whitespace.
	i := pos - 1
	for i >= 0 && (source[i] == ' ' || source[i] == '\t') {
		i--
	}
	if i < 0 {
		return ""
	}

	// The character at i should be the last character of the identifier.
	if !isIdentChar(source[i]) {
		return ""
	}

	end := i + 1
	for i >= 0 && isIdentChar(source[i]) {
		i--
	}
	return source[i+1 : end]
}

// findTypedParameters searches function definitions for parameters whose type
// annotation references typeName.
func findTypedParameters(file *forest.ParsedFile, typeName string) []string {
	queryStr := file.Adapter.FunctionDefQuery()
	if queryStr == "" {
		return nil
	}

	lang := file.Adapter.Language()
	query, err := tree_sitter.NewQuery(lang, queryStr)
	if err != nil {
		return nil
	}
	defer query.Close()

	funcIdx := -1
	for i, n := range query.CaptureNames() {
		if n == "func" {
			funcIdx = i
			break
		}
	}
	if funcIdx < 0 {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, file.Tree.RootNode(), file.OriginalSource)

	var result []string
	for m := matches.Next(); m != nil; m = matches.Next() {
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Index != uint32(funcIdx) {
				continue
			}

			paramsNode := c.Node.ChildByFieldName("parameters")
			if paramsNode == nil {
				continue
			}

			paramCursor := paramsNode.Walk()
			for _, child := range paramsNode.Children(paramCursor) {
				if !child.IsNamed() {
					continue
				}
				// Check if the parameter's type contains the typeName.
				paramText := string(file.OriginalSource[child.StartByte():child.EndByte()])
				if strings.Contains(paramText, typeName) {
					name := extractParamName(file.OriginalSource, child)
					if name != "" && name != typeName {
						result = append(result, name)
					}
				}
			}
			paramCursor.Close()
		}
	}
	return result
}
