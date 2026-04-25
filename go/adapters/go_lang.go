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

// GoAdapter implements LanguageAdapter for Go source files.
type GoAdapter struct{ baseAdapter }

func (a *GoAdapter) Language() *tree_sitter.Language {
	return tree_sitter.GoLanguage()
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

func (a *GoAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s = %s\n", name, value)
}

func (a *GoAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("os.Getenv(%q)", varName)
}

// goModulePath reads the module path from go.mod in root (or an ancestor).
func goModulePath(root string) string {
	modPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// ResolveImportPath resolves a Go import path to a directory relative to root.
// Only works for local imports (matching the go.mod module path).
func (a *GoAdapter) ResolveImportPath(importText, _, root string) string {
	// Strip quotes.
	importText = strings.Trim(strings.TrimSpace(importText), `"`)

	modPath := goModulePath(root)
	if modPath == "" {
		return ""
	}

	if !strings.HasPrefix(importText, modPath) {
		return ""
	}

	// Strip module prefix to get the relative directory.
	rel := strings.TrimPrefix(importText, modPath)
	rel = strings.TrimPrefix(rel, "/")

	if rel == "" {
		return "."
	}
	return rel
}

// BuildImportPath produces a Go import path for the directory containing
// targetFile.
func (a *GoAdapter) BuildImportPath(targetFile, _, root string) string {
	modPath := goModulePath(root)
	if modPath == "" {
		return ""
	}

	targetDir := filepath.Dir(targetFile)
	rel, err := filepath.Rel(root, targetDir)
	if err != nil {
		return ""
	}

	if rel == "." {
		return `"` + modPath + `"`
	}

	return `"` + modPath + "/" + filepath.ToSlash(rel) + `"`
}

func (a *GoAdapter) StructLiteralQuery() string {
	return "(composite_literal type: (type_identifier) @name) @literal"
}

func (a *GoAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"New" + typeName}
}

func (a *GoAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value)
}
