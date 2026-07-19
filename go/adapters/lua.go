// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// LuaAdapter implements LanguageAdapter for Lua source files.
// Tables stand in for types; fields are table-constructor entries.
type LuaAdapter struct{ baseAdapter }

func (a *LuaAdapter) Language() *tree_sitter.Language {
	return tree_sitter.LuaLanguage()
}

func (a *LuaAdapter) Extensions() []string { return []string{"lua"} }

func (a *LuaAdapter) FunctionDefQuery() string {
	return "(function_declaration name: (identifier) @name) @func"
}

func (a *LuaAdapter) IdentifierQuery() string {
	return "(identifier) @name"
}

func (a *LuaAdapter) CallExprQuery() string {
	return "(function_call name: (identifier) @name) @call"
}

// TypeDefQuery treats top-level table assignments (Widget = { ... }) as types.
func (a *LuaAdapter) TypeDefQuery() string {
	return `(assignment_statement (variable_list (identifier) @name) (expression_list (table_constructor) @type_def))`
}

func (a *LuaAdapter) ImportQuery() string {
	return `(function_call (identifier) @_r (arguments (string (string_content) @name)) (#eq? @_r "require")) @import`
}

func (a *LuaAdapter) FormatterCommand() []string { return []string{"stylua", "-"} }

func (a *LuaAdapter) LSPCommand() []string { return []string{"lua-language-server"} }

func (a *LuaAdapter) LSPLanguageID() string { return "lua" }

func (a *LuaAdapter) FieldQuery() string {
	return "(field (identifier) @name) @field"
}

func (a *LuaAdapter) MethodQuery() string {
	return `(function_declaration name: (dot_index_expression (identifier) @name)) @method`
}

func (a *LuaAdapter) DocCommentPrefix() string { return "--" }

func (a *LuaAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

// GenField generates a table field entry; Lua is untyped so typeName is ignored.
func (a *LuaAdapter) GenField(name, _ string) string {
	return fmt.Sprintf("  %s = nil,\n", name)
}

func (a *LuaAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *LuaAdapter) GenMethod(name, params, _, body string) string {
	if params == "" {
		return fmt.Sprintf("function %s()\n  %s\nend\n", name, body)
	}
	return fmt.Sprintf("function %s(%s)\n  %s\nend\n", name, params, body)
}

func (a *LuaAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *LuaAdapter) GenImport(path string) string {
	return fmt.Sprintf("require(%q)\n", path)
}

func (a *LuaAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("local %s = %s\n", name, value)
}

func (a *LuaAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("os.getenv(%q)", varName)
}

// ResolveImportPath resolves require("mod") to mod.lua under the importing
// file's directory or the project root.
func (a *LuaAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" {
		return ""
	}
	rel := strings.ReplaceAll(importText, ".", string(filepath.Separator))
	if !strings.HasSuffix(rel, ".lua") {
		rel += ".lua"
	}
	for _, base := range []string{filepath.Dir(importingFile), root} {
		abs := filepath.Join(base, rel)
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		r, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(r, "..") {
			continue
		}
		return r
	}
	return ""
}

func (a *LuaAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	rel, err := filepath.Rel(filepath.Dir(importingFile), targetFile)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(strings.TrimSuffix(rel, ".lua"))
}

func (a *LuaAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"new", typeName + ".new"}
}

func (a *LuaAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s = %s", fieldName, value)
}
