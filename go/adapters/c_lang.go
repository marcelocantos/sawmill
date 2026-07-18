// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"path/filepath"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// CAdapter implements LanguageAdapter for C source files. Headers (.h)
// remain routed to the C++ adapter, whose grammar parses C declarations.
type CAdapter struct{ baseAdapter }

func (a *CAdapter) Language() *tree_sitter.Language {
	return tree_sitter.CLanguage()
}

func (a *CAdapter) Extensions() []string { return []string{"c"} }

func (a *CAdapter) FunctionDefQuery() string {
	return "(function_definition declarator: (function_declarator declarator: (identifier) @name)) @func"
}

func (a *CAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier) (field_identifier)] @name"
}

func (a *CAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *CAdapter) TypeDefQuery() string {
	return "[(struct_specifier name: (type_identifier) @name) (union_specifier name: (type_identifier) @name) (enum_specifier name: (type_identifier) @name) (type_definition declarator: (type_identifier) @name)] @type_def"
}

func (a *CAdapter) ImportQuery() string {
	return "(preproc_include path: (_) @name) @import"
}

func (a *CAdapter) FormatterCommand() []string { return []string{"clang-format"} }

func (a *CAdapter) LSPCommand() []string { return []string{"clangd"} }

func (a *CAdapter) LSPLanguageID() string { return "c" }

func (a *CAdapter) FieldQuery() string {
	// type: must precede declarator: — pattern children match in child order.
	return "(field_declaration type: (_) @type declarator: (field_identifier) @name) @field"
}

func (a *CAdapter) TypeUseQuery() string {
	return "(type_identifier) @name"
}

// GenField generates a C struct field. C puts the type before the name.
func (a *CAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    %s %s;\n", typeName, name)
}

func (a *CAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *CAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("%s %s(%s) {\n    %s\n}\n", returnType, name, params, body)
}

func (a *CAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *CAdapter) GenImport(path string) string {
	return fmt.Sprintf("#include \"%s\"\n", path)
}

// GenConstDeclaration uses #define — the idiomatic type-free C constant.
func (a *CAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("#define %s %s\n", name, value)
}

func (a *CAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("getenv(%q)", varName)
}

// ResolveImportPath resolves #include "path" to a filesystem path relative
// to root. Returns "" for system includes (#include <...>).
func (a *CAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)
	if strings.HasPrefix(importText, "<") {
		return ""
	}
	importText = strings.Trim(importText, `"`)
	abs := filepath.Join(filepath.Dir(importingFile), importText)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return rel
}

// BuildImportPath produces a relative #include path from importingFile to
// targetFile, quoted to match the tree-sitter string_literal node.
func (a *CAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	rel, err := filepath.Rel(filepath.Dir(importingFile), targetFile)
	if err != nil {
		return ""
	}
	return `"` + filepath.ToSlash(rel) + `"`
}
