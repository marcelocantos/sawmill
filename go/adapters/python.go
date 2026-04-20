// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// PythonAdapter implements LanguageAdapter for Python source files.
type PythonAdapter struct{ baseAdapter }

func (a *PythonAdapter) Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_python.Language())
}

func (a *PythonAdapter) Extensions() []string { return []string{"py", "pyi"} }

func (a *PythonAdapter) FunctionDefQuery() string {
	return "(function_definition name: (identifier) @name) @func"
}

func (a *PythonAdapter) IdentifierQuery() string { return "(identifier) @name" }

func (a *PythonAdapter) CallExprQuery() string {
	return "(call function: (identifier) @name) @call"
}

func (a *PythonAdapter) TypeDefQuery() string {
	return "(class_definition name: (identifier) @name) @type_def"
}

func (a *PythonAdapter) ImportQuery() string {
	return "(import_statement name: (dotted_name) @name) @import"
}

func (a *PythonAdapter) FormatterCommand() []string { return []string{"ruff", "format", "-"} }

func (a *PythonAdapter) LSPCommand() []string { return []string{"pyright-langserver", "--stdio"} }

func (a *PythonAdapter) LSPLanguageID() string { return "python" }

// FieldQuery returns empty — Python has no typed struct fields in the grammar.
func (a *PythonAdapter) FieldQuery() string { return "" }

func (a *PythonAdapter) MethodQuery() string {
	return "(function_definition name: (identifier) @name) @method"
}

func (a *PythonAdapter) DecoratorQuery() string { return "(decorator) @decorator" }

func (a *PythonAdapter) DocCommentPrefix() string { return "#" }

func (a *PythonAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

func (a *PythonAdapter) GenField(name, _ string) string {
	return fmt.Sprintf("    %s = None\n", name)
}

func (a *PythonAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *PythonAdapter) GenMethod(name, params, _, body string) string {
	return fmt.Sprintf("    def %s(%s):\n        %s\n", name, params, body)
}

func (a *PythonAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *PythonAdapter) GenImport(path string) string {
	return fmt.Sprintf("import %s\n", path)
}

func (a *PythonAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("%s = %s\n", name, value)
}

func (a *PythonAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("os.environ.get(%q)", varName)
}

// ResolveImportPath resolves a Python import like "foo.bar" to
// "foo/bar.py" or "foo/bar/__init__.py" relative to root.
func (a *PythonAdapter) ResolveImportPath(importText, _, root string) string {
	// Strip leading/trailing whitespace.
	importText = strings.TrimSpace(importText)

	// Convert dotted module path to filesystem path.
	parts := strings.Split(importText, ".")
	relPath := filepath.Join(parts...) + ".py"

	// Check if it exists as a .py file.
	if _, err := os.Stat(filepath.Join(root, relPath)); err == nil {
		return relPath
	}

	// Check if it exists as a package (__init__.py).
	pkgInit := filepath.Join(filepath.Join(parts...), "__init__.py")
	if _, err := os.Stat(filepath.Join(root, pkgInit)); err == nil {
		return pkgInit
	}

	return ""
}

// BuildImportPath produces a dotted Python import for targetFile.
func (a *PythonAdapter) BuildImportPath(targetFile, _, root string) string {
	rel, err := filepath.Rel(root, targetFile)
	if err != nil {
		return ""
	}

	// Strip .py extension.
	rel = strings.TrimSuffix(rel, ".py")

	// Handle __init__.py — import the package directory.
	rel = strings.TrimSuffix(rel, string(filepath.Separator)+"__init__")

	// Convert path separators to dots.
	return strings.ReplaceAll(rel, string(filepath.Separator), ".")
}

func (a *PythonAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"__init__", "create_" + strings.ToLower(typeName)}
}

func (a *PythonAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s=%s", fieldName, value)
}
