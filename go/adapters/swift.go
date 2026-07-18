// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// SwiftAdapter implements LanguageAdapter for Swift source files.
type SwiftAdapter struct{ baseAdapter }

func (a *SwiftAdapter) Language() *tree_sitter.Language {
	return tree_sitter.SwiftLanguage()
}

func (a *SwiftAdapter) Extensions() []string { return []string{"swift"} }

func (a *SwiftAdapter) FunctionDefQuery() string {
	return "(function_declaration (simple_identifier) @name) @func"
}

func (a *SwiftAdapter) IdentifierQuery() string {
	return "[(simple_identifier) (type_identifier)] @name"
}

func (a *SwiftAdapter) CallExprQuery() string {
	return "(call_expression (simple_identifier) @name) @call"
}

// TypeDefQuery matches classes and protocols. In this grammar struct and
// enum declarations also parse as class_declaration nodes.
func (a *SwiftAdapter) TypeDefQuery() string {
	return "[(class_declaration (type_identifier) @name) (protocol_declaration (type_identifier) @name)] @type_def"
}

func (a *SwiftAdapter) ImportQuery() string {
	return "(import_declaration (identifier) @name) @import"
}

func (a *SwiftAdapter) FormatterCommand() []string { return []string{"swift-format"} }

func (a *SwiftAdapter) LSPCommand() []string { return []string{"sourcekit-lsp"} }

func (a *SwiftAdapter) LSPLanguageID() string { return "swift" }

func (a *SwiftAdapter) FieldQuery() string {
	return "(property_declaration (pattern (simple_identifier) @name)) @field"
}

func (a *SwiftAdapter) MethodQuery() string {
	return "(function_declaration (simple_identifier) @name) @method"
}

func (a *SwiftAdapter) DecoratorQuery() string {
	return "(attribute) @decorator"
}

func (a *SwiftAdapter) TypeUseQuery() string {
	return "(user_type (type_identifier) @name)"
}

func (a *SwiftAdapter) DocCommentPrefix() string { return "///" }

func (a *SwiftAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

func (a *SwiftAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    var %s: %s\n", name, typeName)
}

func (a *SwiftAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *SwiftAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		return fmt.Sprintf("    func %s(%s) {\n        %s\n    }\n", name, params, body)
	}
	return fmt.Sprintf("    func %s(%s) -> %s {\n        %s\n    }\n", name, params, returnType, body)
}

func (a *SwiftAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *SwiftAdapter) GenImport(path string) string {
	return fmt.Sprintf("import %s\n", path)
}

func (a *SwiftAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("let %s = %s\n", name, value)
}

func (a *SwiftAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("ProcessInfo.processInfo.environment[%q]", varName)
}

// ResolveImportPath returns "" — Swift imports name modules, which have no
// direct filesystem correspondence.

func (a *SwiftAdapter) FactoryFuncNames(typeName string) []string {
	return []string{typeName, "init"}
}

func (a *SwiftAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value) // Swift argument labels
}
