// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// RustAdapter implements LanguageAdapter for Rust source files.
type RustAdapter struct{ baseAdapter }

func (a *RustAdapter) Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_rust.Language())
}

func (a *RustAdapter) Extensions() []string { return []string{"rs"} }

func (a *RustAdapter) FunctionDefQuery() string {
	return "(function_item name: (identifier) @name) @func"
}

func (a *RustAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier)] @name"
}

func (a *RustAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *RustAdapter) TypeDefQuery() string {
	return "[(struct_item name: (type_identifier) @name) (enum_item name: (type_identifier) @name) (trait_item name: (type_identifier) @name)] @type_def"
}

func (a *RustAdapter) ImportQuery() string {
	return "(use_declaration argument: (_) @name) @import"
}

func (a *RustAdapter) FormatterCommand() []string { return []string{"rustfmt"} }

func (a *RustAdapter) LSPCommand() []string { return []string{"rust-analyzer"} }

func (a *RustAdapter) LSPLanguageID() string { return "rust" }

func (a *RustAdapter) FieldQuery() string {
	return "(field_declaration name: (field_identifier) @name type: (_) @type) @field"
}

func (a *RustAdapter) MethodQuery() string {
	return "(function_item name: (identifier) @name) @method"
}

func (a *RustAdapter) DecoratorQuery() string { return "(attribute_item) @decorator" }

func (a *RustAdapter) DocCommentPrefix() string { return "///" }

func (a *RustAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

func (a *RustAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    %s: %s,\n", name, typeName)
}

func (a *RustAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *RustAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		return fmt.Sprintf("    fn %s(%s) {\n        %s\n    }\n", name, params, body)
	}
	return fmt.Sprintf("    fn %s(%s) -> %s {\n        %s\n    }\n", name, params, returnType, body)
}

func (a *RustAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *RustAdapter) GenImport(path string) string {
	return fmt.Sprintf("use %s;\n", path)
}
