// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// GoAdapter implements LanguageAdapter for Go source files.
type GoAdapter struct{ baseAdapter }

func (a *GoAdapter) Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_go.Language())
}

func (a *GoAdapter) Extensions() []string { return []string{"go"} }

func (a *GoAdapter) FunctionDefQuery() string {
	return "(function_declaration name: (identifier) @name) @func"
}

func (a *GoAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier) (field_identifier) (package_identifier)] @name"
}

func (a *GoAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *GoAdapter) TypeDefQuery() string {
	return "(type_declaration (type_spec name: (type_identifier) @name)) @type_def"
}

func (a *GoAdapter) ImportQuery() string {
	return "(import_spec path: (interpreted_string_literal) @name) @import"
}

func (a *GoAdapter) FormatterCommand() []string { return []string{"gofmt"} }

func (a *GoAdapter) LSPCommand() []string { return []string{"gopls"} }

func (a *GoAdapter) LSPLanguageID() string { return "go" }

func (a *GoAdapter) FieldQuery() string {
	return "(field_declaration name: (field_identifier) @name type: (_) @type) @field"
}

func (a *GoAdapter) MethodQuery() string {
	return "(method_declaration name: (field_identifier) @name) @method"
}

// DecoratorQuery returns empty — Go has no decorators.
func (a *GoAdapter) DecoratorQuery() string { return "" }

// GenField generates a Go field declaration. Go puts the type after the name.
func (a *GoAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    %s %s\n", name, typeName)
}

func (a *GoAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *GoAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("func %s(%s) %s {\n    %s\n}\n", name, params, returnType, body)
}

func (a *GoAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *GoAdapter) GenImport(path string) string {
	return fmt.Sprintf("import \"%s\"\n", path)
}
