// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"path/filepath"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// CppAdapter implements LanguageAdapter for C++ source files.
type CppAdapter struct{ baseAdapter }

func (a *CppAdapter) Language() *tree_sitter.Language {
	return tree_sitter.CppLanguage()
}

func (a *CppAdapter) Extensions() []string {
	return []string{"cpp", "cc", "cxx", "hpp", "hxx", "h"}
}

func (a *CppAdapter) FunctionDefQuery() string {
	return "(function_definition declarator: (function_declarator declarator: (identifier) @name)) @func"
}

func (a *CppAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier) (field_identifier) (namespace_identifier)] @name"
}

func (a *CppAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *CppAdapter) TypeDefQuery() string {
	return "[(class_specifier name: (type_identifier) @name) (struct_specifier name: (type_identifier) @name)] @type_def"
}

func (a *CppAdapter) ImportQuery() string {
	return "(preproc_include path: (_) @name) @import"
}

func (a *CppAdapter) FormatterCommand() []string { return []string{"clang-format"} }

func (a *CppAdapter) LSPCommand() []string { return []string{"clangd"} }

func (a *CppAdapter) LSPLanguageID() string { return "cpp" }

func (a *CppAdapter) FieldQuery() string {
	return "(field_declaration declarator: (field_identifier) @name type: (_) @type) @field"
}

func (a *CppAdapter) MethodQuery() string {
	return "(function_definition declarator: (function_declarator declarator: (_) @name)) @method"
}

// DecoratorQuery returns empty — C++ has no decorators in the Tree-sitter grammar.
func (a *CppAdapter) DecoratorQuery() string { return "" }

// GenField generates a C++ field declaration. C++ puts the type before the name.
func (a *CppAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("  %s %s;\n", typeName, name)
}

func (a *CppAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *CppAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("  %s %s(%s) {\n    %s\n  }\n", returnType, name, params, body)
}

func (a *CppAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *CppAdapter) GenImport(path string) string {
	return fmt.Sprintf("#include \"%s\"\n", path)
}

func (a *CppAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("constexpr auto %s = %s;\n", name, value)
}

func (a *CppAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("std::getenv(%q)", varName)
}

// ResolveImportPath resolves C/C++ #include "path" to a filesystem path
// relative to root. Returns "" for system includes (#include <...>).
func (a *CppAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)

	// System includes — angle brackets.
	if strings.HasPrefix(importText, "<") {
		return ""
	}

	// Strip quotes.
	importText = strings.Trim(importText, `"`)

	// Resolve relative to the importing file's directory.
	importDir := filepath.Dir(importingFile)
	abs := filepath.Join(importDir, importText)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return rel
}

// BuildImportPath produces a relative #include path from importingFile to
// targetFile. Returns with quotes to match the tree-sitter string_literal node.
func (a *CppAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	importDir := filepath.Dir(importingFile)
	rel, err := filepath.Rel(importDir, targetFile)
	if err != nil {
		return ""
	}
	// Use forward slashes in includes, with quotes to match the captured node.
	return `"` + filepath.ToSlash(rel) + `"`
}

func (a *CppAdapter) FactoryFuncNames(typeName string) []string {
	return []string{typeName} // C++ constructor has same name as class
}

func (a *CppAdapter) GenFieldInitializer(_, value string) string {
	return value // C++ uses positional arguments in constructors
}
