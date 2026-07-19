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

// ZigAdapter implements LanguageAdapter for Zig source files.
type ZigAdapter struct{ baseAdapter }

func (a *ZigAdapter) Language() *tree_sitter.Language {
	return tree_sitter.ZigLanguage()
}

func (a *ZigAdapter) Extensions() []string { return []string{"zig"} }

func (a *ZigAdapter) FunctionDefQuery() string {
	return "(function_declaration (identifier) @name) @func"
}

func (a *ZigAdapter) IdentifierQuery() string {
	return "(identifier) @name"
}

func (a *ZigAdapter) CallExprQuery() string {
	return "(call_expression (identifier) @name) @call"
}

// TypeDefQuery matches `const Name = struct { ... }` (and similar container
// decls). @type_def is the struct_declaration so add_field can find braces.
func (a *ZigAdapter) TypeDefQuery() string {
	return `(variable_declaration (identifier) @name (struct_declaration) @type_def)`
}

func (a *ZigAdapter) ImportQuery() string {
	return `(builtin_function (arguments (string (string_content) @name))) @import`
}

func (a *ZigAdapter) FormatterCommand() []string { return []string{"zig", "fmt", "--stdin"} }

func (a *ZigAdapter) LSPCommand() []string { return []string{"zls"} }

func (a *ZigAdapter) LSPLanguageID() string { return "zig" }

func (a *ZigAdapter) FieldQuery() string {
	return "(container_field (identifier) @name) @field"
}

func (a *ZigAdapter) MethodQuery() string {
	return "(function_declaration (identifier) @name) @method"
}

func (a *ZigAdapter) TypeUseQuery() string {
	return "(identifier) @name"
}

func (a *ZigAdapter) DocCommentPrefix() string { return "///" }

func (a *ZigAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

func (a *ZigAdapter) GenField(name, typeName string) string {
	if typeName == "" {
		typeName = "i32"
	}
	return fmt.Sprintf("    %s: %s,\n", name, typeName)
}

func (a *ZigAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *ZigAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		returnType = "void"
	}
	return fmt.Sprintf("    pub fn %s(%s) %s {\n        %s\n    }\n", name, params, returnType, body)
}

func (a *ZigAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *ZigAdapter) GenImport(path string) string {
	path = strings.Trim(path, `"`)
	return fmt.Sprintf("const %s = @import(%q);\n", importAlias(path), path)
}

func (a *ZigAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s = %s;\n", name, value)
}

func (a *ZigAdapter) GenEnvRead(varName string) string {
	// Zig has no single stdlib getenv without an allocator; emit a clear stub.
	return fmt.Sprintf("/* getenv(%q) */", varName)
}

// ResolveImportPath resolves @import("file.zig") relative to the importing
// file. Package imports ("std") return "".
func (a *ZigAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" || !strings.Contains(importText, ".") {
		return ""
	}
	if !strings.HasSuffix(importText, ".zig") {
		importText += ".zig"
	}
	abs := filepath.Join(filepath.Dir(importingFile), importText)
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return rel
}

func (a *ZigAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	rel, err := filepath.Rel(filepath.Dir(importingFile), targetFile)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (a *ZigAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"init", "create", typeName + ".init"}
}

func (a *ZigAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf(".%s = %s", fieldName, value)
}

func importAlias(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return "mod"
	}
	// Zig identifiers cannot contain hyphens.
	return strings.ReplaceAll(base, "-", "_")
}
