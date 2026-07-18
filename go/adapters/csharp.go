// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// CSharpAdapter implements LanguageAdapter for C# source files.
type CSharpAdapter struct{ baseAdapter }

func (a *CSharpAdapter) Language() *tree_sitter.Language {
	return tree_sitter.CSharpLanguage()
}

func (a *CSharpAdapter) Extensions() []string { return []string{"cs"} }

func (a *CSharpAdapter) FunctionDefQuery() string {
	return "[(method_declaration name: (identifier) @name) (constructor_declaration name: (identifier) @name)] @func"
}

func (a *CSharpAdapter) IdentifierQuery() string {
	return "(identifier) @name"
}

func (a *CSharpAdapter) CallExprQuery() string {
	return "(invocation_expression function: (identifier) @name) @call"
}

func (a *CSharpAdapter) TypeDefQuery() string {
	return "[(class_declaration name: (identifier) @name) (interface_declaration name: (identifier) @name) (struct_declaration name: (identifier) @name) (enum_declaration name: (identifier) @name) (record_declaration name: (identifier) @name)] @type_def"
}

func (a *CSharpAdapter) ImportQuery() string {
	return "(using_directive [(identifier) (qualified_name)] @name) @import"
}

// FormatterCommand uses clang-format, which supports C# when given a .cs
// assume-filename.
func (a *CSharpAdapter) FormatterCommand() []string {
	return []string{"clang-format", "--assume-filename=a.cs"}
}

func (a *CSharpAdapter) LSPCommand() []string { return []string{"csharp-ls"} }

func (a *CSharpAdapter) LSPLanguageID() string { return "csharp" }

func (a *CSharpAdapter) FieldQuery() string {
	return "(field_declaration (variable_declaration type: (_) @type (variable_declarator name: (identifier) @name))) @field"
}

func (a *CSharpAdapter) MethodQuery() string {
	return "(method_declaration name: (identifier) @name) @method"
}

func (a *CSharpAdapter) DecoratorQuery() string {
	return "(attribute_list) @decorator"
}

// TypeUseQuery captures identifiers in type positions: local/field variable
// types, parameter types, base-class/interface lists, and constructions.
func (a *CSharpAdapter) TypeUseQuery() string {
	return `
[
  (variable_declaration type: (identifier) @name)
  (parameter type: (identifier) @name)
  (base_list (identifier) @name)
  (object_creation_expression type: (identifier) @name)
] @type_use`
}

func (a *CSharpAdapter) DocCommentPrefix() string { return "///" }

func (a *CSharpAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

// GenField generates a C# field declaration. C# puts the type before the name.
func (a *CSharpAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    private %s %s;\n", typeName, name)
}

func (a *CSharpAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *CSharpAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("    public %s %s(%s)\n    {\n        %s\n    }\n", returnType, name, params, body)
}

func (a *CSharpAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *CSharpAdapter) GenImport(path string) string {
	return fmt.Sprintf("using %s;\n", path)
}

// GenConstDeclaration infers the C# type from the literal since C# constants
// require an explicit type.
func (a *CSharpAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s %s = %s;\n", csharpLiteralType(value), name, value)
}

// csharpLiteralType infers a C# type from a literal's source text.
func csharpLiteralType(value string) string {
	switch {
	case strings.HasPrefix(value, `"`) || strings.HasPrefix(value, `@"`) || strings.HasPrefix(value, `$"`):
		return "string"
	case strings.HasPrefix(value, "'"):
		return "char"
	case value == "true" || value == "false":
		return "bool"
	case strings.ContainsAny(value, ".eE") && !strings.HasPrefix(value, "0x"):
		return "double"
	default:
		return "int"
	}
}

func (a *CSharpAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("Environment.GetEnvironmentVariable(%q)", varName)
}

// ResolveImportPath returns "" — C# using directives name namespaces, which
// have no filesystem correspondence.

// StructLiteralQuery matches "new T(...)" constructions.
func (a *CSharpAdapter) StructLiteralQuery() string {
	return "(object_creation_expression type: (identifier) @name) @literal"
}

func (a *CSharpAdapter) FactoryFuncNames(typeName string) []string {
	return []string{typeName, "Create" + typeName}
}

// GenFieldInitializer uses C# object-initializer syntax: new T { Name = value }.
func (a *CSharpAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s = %s", fieldName, value)
}
