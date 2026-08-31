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

// JavaScriptAdapter implements LanguageAdapter for plain JavaScript source
// files (including JSX, which the grammar parses natively).
type JavaScriptAdapter struct{ baseAdapter }

func (a *JavaScriptAdapter) Language() *tree_sitter.Language {
	return tree_sitter.JavascriptLanguage()
}

func (a *JavaScriptAdapter) Extensions() []string { return []string{"js", "jsx", "mjs", "cjs"} }

func (a *JavaScriptAdapter) FunctionDefQuery() string {
	return "(function_declaration name: (identifier) @name) @func"
}

func (a *JavaScriptAdapter) IdentifierQuery() string {
	return "[(identifier) (property_identifier) (shorthand_property_identifier)] @name"
}

func (a *JavaScriptAdapter) CallExprQuery() string {
	return "(call_expression function: (identifier) @name) @call"
}

func (a *JavaScriptAdapter) TypeDefQuery() string {
	return "(class_declaration name: (identifier) @name) @type_def"
}

func (a *JavaScriptAdapter) ImportQuery() string {
	return "(import_statement source: (string) @name) @import"
}

func (a *JavaScriptAdapter) FormatterCommand() []string {
	return []string{"prettier", "--parser", "babel"}
}

func (a *JavaScriptAdapter) LSPCommand() []string {
	return []string{"typescript-language-server", "--stdio"}
}

func (a *JavaScriptAdapter) LSPLanguageID() string { return "javascript" }

func (a *JavaScriptAdapter) FieldQuery() string {
	return "(field_definition property: [(property_identifier) (private_property_identifier)] @name) @field"
}

func (a *JavaScriptAdapter) MethodQuery() string {
	return "(method_definition name: (property_identifier) @name) @method"
}

func (a *JavaScriptAdapter) DecoratorQuery() string { return "(decorator) @decorator" }

func (a *JavaScriptAdapter) GenField(name, typeName string) string {
	if typeName == "" {
		return fmt.Sprintf("  %s;\n", name)
	}
	return fmt.Sprintf("  %s = %s;\n", name, typeName)
}

func (a *JavaScriptAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *JavaScriptAdapter) GenMethod(name, params, _, body string) string {
	return fmt.Sprintf("  %s(%s) {\n    %s\n  }\n", name, params, body)
}

func (a *JavaScriptAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *JavaScriptAdapter) GenImport(path string) string {
	return fmt.Sprintf("import { %s };\n", path)
}

func (a *JavaScriptAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const %s = %s;\n", name, value)
}

func (a *JavaScriptAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("process.env[%q]", varName)
}

// ResolveImportPath resolves relative JS imports like "./foo.js" or "../bar"
// to filesystem paths relative to root.
func (a *JavaScriptAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.Trim(strings.TrimSpace(importText), `"'`)
	if !strings.HasPrefix(importText, "./") && !strings.HasPrefix(importText, "../") {
		return ""
	}

	importDir := filepath.Dir(importingFile)
	abs := filepath.Join(importDir, importText)

	candidates := []string{abs}
	if filepath.Ext(abs) == "" {
		candidates = append(candidates, abs+".js", abs+".mjs", abs+".cjs", abs+".jsx")
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

// BuildImportPath produces a relative JS import path from importingFile to
// targetFile. The extension is kept — ESM resolution requires it. Returns
// with quotes to match the tree-sitter string node.
func (a *JavaScriptAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	importDir := filepath.Dir(importingFile)
	rel, err := filepath.Rel(importDir, targetFile)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return `"` + rel + `"`
}

func (a *JavaScriptAdapter) StructLiteralQuery() string {
	return "(new_expression constructor: (identifier) @name) @literal"
}

func (a *JavaScriptAdapter) FactoryFuncNames(typeName string) []string {
	return []string{"constructor", "create" + typeName}
}

func (a *JavaScriptAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value)
}
