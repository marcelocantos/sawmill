// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// PhpAdapter implements LanguageAdapter for PHP source files.
type PhpAdapter struct{ baseAdapter }

func (a *PhpAdapter) Language() *tree_sitter.Language {
	return tree_sitter.PhpLanguage()
}

func (a *PhpAdapter) Extensions() []string { return []string{"php"} }

func (a *PhpAdapter) FunctionDefQuery() string {
	return "[(function_definition name: (name) @name) (method_declaration name: (name) @name)] @func"
}

func (a *PhpAdapter) IdentifierQuery() string {
	return "(name) @name"
}

func (a *PhpAdapter) CallExprQuery() string {
	return "(function_call_expression function: (name) @name) @call"
}

func (a *PhpAdapter) TypeDefQuery() string {
	return "[(class_declaration name: (name) @name) (interface_declaration name: (name) @name) (enum_declaration name: (name) @name) (trait_declaration name: (name) @name)] @type_def"
}

func (a *PhpAdapter) ImportQuery() string {
	return "(namespace_use_declaration (namespace_use_clause [(name) (qualified_name)] @name)) @import"
}

func (a *PhpAdapter) LSPCommand() []string { return []string{"intelephense", "--stdio"} }

func (a *PhpAdapter) LSPLanguageID() string { return "php" }

func (a *PhpAdapter) FieldQuery() string {
	return "(property_declaration (property_element (variable_name (name) @name))) @field"
}

func (a *PhpAdapter) MethodQuery() string {
	return "(method_declaration name: (name) @name) @method"
}

func (a *PhpAdapter) DecoratorQuery() string {
	return "(attribute_list) @decorator"
}

// GenField generates a typed PHP property declaration.
func (a *PhpAdapter) GenField(name, typeName string) string {
	if typeName == "" {
		return fmt.Sprintf("    private $%s;\n", name)
	}
	return fmt.Sprintf("    private %s $%s;\n", typeName, name)
}

func (a *PhpAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *PhpAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		return fmt.Sprintf("    public function %s(%s)\n    {\n        %s\n    }\n", name, params, body)
	}
	return fmt.Sprintf("    public function %s(%s): %s\n    {\n        %s\n    }\n", name, params, returnType, body)
}

func (a *PhpAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *PhpAdapter) GenImport(path string) string {
	return fmt.Sprintf("use %s;\n", path)
}

func (a *PhpAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s = %s;\n", name, value)
}

func (a *PhpAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("getenv(%q)", varName)
}

// ResolveImportPath returns "" — PHP use statements name namespaces whose
// filesystem mapping depends on composer PSR-4 configuration.

func (a *PhpAdapter) StructLiteralQuery() string {
	return "(object_creation_expression (name) @name) @literal"
}

func (a *PhpAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"__construct", "create" + typeName}
}

func (a *PhpAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value) // PHP 8 named arguments
}
