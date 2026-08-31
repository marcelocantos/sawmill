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

// RustAdapter implements LanguageAdapter for Rust source files.
type RustAdapter struct{ baseAdapter }

func (a *RustAdapter) Language() *tree_sitter.Language {
	return tree_sitter.RustLanguage()
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

func (a *RustAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s: &str = %s;\n", name, value)
}

func (a *RustAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("std::env::var(%q).unwrap_or_default()", varName)
}

// ResolveImportPath resolves Rust `mod foo;` declarations to filesystem paths.
// Returns "" for `use` statements (intra-module, not file references).
func (a *RustAdapter) ResolveImportPath(importText, importingFile, _ string) string {
	importText = strings.TrimSpace(importText)

	// Only handle `mod <name>` — `use` statements are not file references.
	// The import text from tree-sitter is just the argument part, not the
	// full statement. But the ImportQuery captures use_declaration arguments
	// too, so we filter: mod declarations produce simple identifiers.
	// If it contains "::" it's a use path, skip.
	if strings.Contains(importText, "::") {
		return ""
	}

	importDir := filepath.Dir(importingFile)
	name := importText

	// Check <name>.rs
	candidate := filepath.Join(importDir, name+".rs")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	// Check <name>/mod.rs
	candidate = filepath.Join(importDir, name, "mod.rs")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ""
}

// BuildImportPath produces a Rust module name for targetFile relative to
// importingFile's directory.
func (a *RustAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	importDir := filepath.Dir(importingFile)
	rel, err := filepath.Rel(importDir, targetFile)
	if err != nil {
		return ""
	}

	// Strip .rs extension.
	name := strings.TrimSuffix(rel, ".rs")

	// Handle mod.rs — use the directory name.
	if strings.HasSuffix(name, string(filepath.Separator)+"mod") {
		name = strings.TrimSuffix(name, string(filepath.Separator)+"mod")
	}

	// Should be a simple name, not a path.
	if strings.Contains(name, string(filepath.Separator)) {
		return ""
	}

	return name
}

func (a *RustAdapter) StructLiteralQuery() string {
	return "(struct_expression name: (type_identifier) @name) @literal"
}

func (a *RustAdapter) FactoryFuncNames(_ string) []string {
	return []string{"new"}
}

func (a *RustAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value)
}
