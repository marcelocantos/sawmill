// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
)

// CppAdapter implements LanguageAdapter for C++ source files.
type CppAdapter struct{ baseAdapter }

func (a *CppAdapter) Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_cpp.Language())
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
