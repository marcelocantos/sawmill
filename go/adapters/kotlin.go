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

// KotlinAdapter implements LanguageAdapter for Kotlin source files.
type KotlinAdapter struct{ baseAdapter }

func (a *KotlinAdapter) Language() *tree_sitter.Language {
	return tree_sitter.KotlinLanguage()
}

func (a *KotlinAdapter) Extensions() []string { return []string{"kt", "kts"} }

func (a *KotlinAdapter) FunctionDefQuery() string {
	return "(function_declaration (simple_identifier) @name) @func"
}

func (a *KotlinAdapter) IdentifierQuery() string {
	return "[(simple_identifier) (type_identifier)] @name"
}

func (a *KotlinAdapter) CallExprQuery() string {
	return "(call_expression (simple_identifier) @name) @call"
}

func (a *KotlinAdapter) TypeDefQuery() string {
	return "[(class_declaration (type_identifier) @name) (object_declaration (type_identifier) @name)] @type_def"
}

// ImportQuery captures the dotted identifier of an import header.
func (a *KotlinAdapter) ImportQuery() string {
	return "(import_header (identifier) @name) @import"
}

func (a *KotlinAdapter) LSPCommand() []string { return []string{"kotlin-language-server"} }

func (a *KotlinAdapter) LSPLanguageID() string { return "kotlin" }

func (a *KotlinAdapter) FieldQuery() string {
	return "(property_declaration (variable_declaration (simple_identifier) @name)) @field"
}

func (a *KotlinAdapter) MethodQuery() string {
	return "(function_declaration (simple_identifier) @name) @method"
}

func (a *KotlinAdapter) DecoratorQuery() string {
	return "(annotation) @decorator"
}

func (a *KotlinAdapter) TypeUseQuery() string {
	return "(user_type (type_identifier) @name)"
}

// GenField generates a Kotlin property declaration.
func (a *KotlinAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    val %s: %s\n", name, typeName)
}

func (a *KotlinAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *KotlinAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		return fmt.Sprintf("    fun %s(%s) {\n        %s\n    }\n", name, params, body)
	}
	return fmt.Sprintf("    fun %s(%s): %s {\n        %s\n    }\n", name, params, returnType, body)
}

func (a *KotlinAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *KotlinAdapter) GenImport(path string) string {
	return fmt.Sprintf("import %s\n", path)
}

func (a *KotlinAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("const val %s = %s\n", name, value)
}

func (a *KotlinAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("System.getenv(%q)", varName)
}

// kotlinSourceRoots lists the directories, relative to the project root,
// that Kotlin package paths are commonly resolved against.
var kotlinSourceRoots = []string{"", "src/main/kotlin", "src/test/kotlin", "src"}

// ResolveImportPath resolves "import com.example.Foo" to the file
// com/example/Foo.kt under one of the conventional source roots.
func (a *KotlinAdapter) ResolveImportPath(importText, _, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" || strings.HasSuffix(importText, "*") {
		return ""
	}
	relFile := strings.ReplaceAll(importText, ".", string(filepath.Separator)) + ".kt"
	for _, srcRoot := range kotlinSourceRoots {
		abs := filepath.Join(root, srcRoot, relFile)
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		return rel
	}
	return ""
}

// BuildImportPath produces the dotted import path for targetFile by
// stripping the conventional source root and the .kt extension.
func (a *KotlinAdapter) BuildImportPath(targetFile, _, root string) string {
	rel, err := filepath.Rel(root, targetFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	rel = filepath.ToSlash(strings.TrimSuffix(rel, ".kt"))
	for _, srcRoot := range kotlinSourceRoots[1:] {
		if rest, ok := strings.CutPrefix(rel, srcRoot+"/"); ok {
			rel = rest
			break
		}
	}
	return strings.ReplaceAll(rel, "/", ".")
}

// StructLiteralQuery matches constructor invocations in delegation
// specifiers and annotations. Plain `Type(...)` constructions parse as
// ordinary call expressions and cannot be distinguished structurally.
func (a *KotlinAdapter) StructLiteralQuery() string {
	return "(constructor_invocation (user_type (type_identifier) @name)) @literal"
}

func (a *KotlinAdapter) FactoryFuncNames(typeName string) []string {
	return []string{typeName}
}

func (a *KotlinAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s = %s", fieldName, value) // Kotlin named arguments
}
