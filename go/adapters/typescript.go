// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TypeScriptAdapter implements LanguageAdapter for TypeScript source files.
type TypeScriptAdapter struct{ baseAdapter }

func (a *TypeScriptAdapter) Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
}

func (a *TypeScriptAdapter) Extensions() []string { return []string{"ts", "tsx"} }

func (a *TypeScriptAdapter) FunctionDefQuery() string {
	return "(function_declaration name: (identifier) @name) @func"
}

func (a *TypeScriptAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier) (property_identifier) (shorthand_property_identifier)] @name"
}

func (a *TypeScriptAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *TypeScriptAdapter) TypeDefQuery() string {
	return "[(class_declaration name: (type_identifier) @name) (interface_declaration name: (type_identifier) @name) (type_alias_declaration name: (type_identifier) @name)] @type_def"
}

func (a *TypeScriptAdapter) ImportQuery() string {
	return "(import_statement source: (string) @name) @import"
}

func (a *TypeScriptAdapter) FormatterCommand() []string {
	return []string{"prettier", "--parser", "typescript"}
}

func (a *TypeScriptAdapter) LSPCommand() []string {
	return []string{"typescript-language-server", "--stdio"}
}

func (a *TypeScriptAdapter) LSPLanguageID() string { return "typescript" }

func (a *TypeScriptAdapter) FieldQuery() string {
	return "(property_signature name: (property_identifier) @name) @field"
}

func (a *TypeScriptAdapter) MethodQuery() string {
	return "(method_definition name: (property_identifier) @name) @method"
}

func (a *TypeScriptAdapter) DecoratorQuery() string { return "(decorator) @decorator" }

func (a *TypeScriptAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("  %s: %s;\n", name, typeName)
}

func (a *TypeScriptAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *TypeScriptAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("  %s(%s): %s {\n    %s\n  }\n", name, params, returnType, body)
}

func (a *TypeScriptAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *TypeScriptAdapter) GenImport(path string) string {
	return fmt.Sprintf("import { %s };\n", path)
}

// ResolveImportPath resolves relative TS imports like "./foo" or "../bar"
// to filesystem paths relative to root.
func (a *TypeScriptAdapter) ResolveImportPath(importText, importingFile, root string) string {
	// Strip quotes.
	importText = strings.Trim(strings.TrimSpace(importText), `"'`)

	// Only resolve relative imports.
	if !strings.HasPrefix(importText, "./") && !strings.HasPrefix(importText, "../") {
		return ""
	}

	importDir := filepath.Dir(importingFile)
	abs := filepath.Join(importDir, importText)

	// Try with common extensions if none present.
	ext := filepath.Ext(abs)
	candidates := []string{abs}
	if ext == "" {
		candidates = append(candidates, abs+".ts", abs+".tsx")
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		rel, err := filepath.Rel(root, c)
		if err != nil {
			continue
		}
		return rel
	}
	return ""
}

// BuildImportPath produces a relative TS import path from importingFile
// to targetFile. Returns with quotes to match the tree-sitter string node.
func (a *TypeScriptAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	importDir := filepath.Dir(importingFile)
	rel, err := filepath.Rel(importDir, targetFile)
	if err != nil {
		return ""
	}

	// Use forward slashes.
	rel = filepath.ToSlash(rel)

	// Strip .ts/.tsx extension.
	rel = strings.TrimSuffix(rel, ".tsx")
	rel = strings.TrimSuffix(rel, ".ts")

	// Ensure relative prefix.
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}

	// Return with quotes — the tree-sitter @name capture for TS import
	// source includes the surrounding quotes.
	return `"` + rel + `"`
}

func (a *TypeScriptAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"constructor", "create" + typeName}
}

func (a *TypeScriptAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value)
}
