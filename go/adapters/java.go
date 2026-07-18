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

// JavaAdapter implements LanguageAdapter for Java source files.
type JavaAdapter struct{ baseAdapter }

func (a *JavaAdapter) Language() *tree_sitter.Language {
	return tree_sitter.JavaLanguage()
}

func (a *JavaAdapter) Extensions() []string { return []string{"java"} }

func (a *JavaAdapter) FunctionDefQuery() string {
	return "[(method_declaration name: (identifier) @name) (constructor_declaration name: (identifier) @name)] @func"
}

func (a *JavaAdapter) IdentifierQuery() string {
	return "[(identifier) (type_identifier)] @name"
}

func (a *JavaAdapter) CallExprQuery() string {
	return "(method_invocation name: (identifier) @name) @call"
}

func (a *JavaAdapter) TypeDefQuery() string {
	return "[(class_declaration name: (identifier) @name) (interface_declaration name: (identifier) @name) (enum_declaration name: (identifier) @name) (record_declaration name: (identifier) @name)] @type_def"
}

func (a *JavaAdapter) ImportQuery() string {
	return "(import_declaration [(identifier) (scoped_identifier)] @name) @import"
}

// FormatterCommand uses clang-format, which supports Java when given a
// .java assume-filename.
func (a *JavaAdapter) FormatterCommand() []string {
	return []string{"clang-format", "--assume-filename=a.java"}
}

func (a *JavaAdapter) LSPCommand() []string { return []string{"jdtls"} }

func (a *JavaAdapter) LSPLanguageID() string { return "java" }

func (a *JavaAdapter) FieldQuery() string {
	return "(field_declaration type: (_) @type declarator: (variable_declarator name: (identifier) @name)) @field"
}

func (a *JavaAdapter) MethodQuery() string {
	return "(method_declaration name: (identifier) @name) @method"
}

func (a *JavaAdapter) DecoratorQuery() string {
	return "[(marker_annotation) (annotation)] @decorator"
}

// TypeUseQuery captures type references. In the Java grammar type_identifier
// only ever appears in type positions (declared type names are plain
// identifier nodes), so the bare node query is precise.
func (a *JavaAdapter) TypeUseQuery() string {
	return "(type_identifier) @name"
}

// GenField generates a Java field declaration. Java puts the type before
// the name.
func (a *JavaAdapter) GenField(name, typeName string) string {
	return fmt.Sprintf("    private %s %s;\n", typeName, name)
}

func (a *JavaAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *JavaAdapter) GenMethod(name, params, returnType, body string) string {
	return fmt.Sprintf("    public %s %s(%s) {\n        %s\n    }\n", returnType, name, params, body)
}

func (a *JavaAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *JavaAdapter) GenImport(path string) string {
	return fmt.Sprintf("import %s;\n", path)
}

// GenConstDeclaration infers the Java type from the literal since Java
// constants require an explicit type.
func (a *JavaAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("static final %s %s = %s;\n", javaLiteralType(value), name, value)
}

// javaLiteralType infers a Java type from a literal's source text.
func javaLiteralType(value string) string {
	switch {
	case strings.HasPrefix(value, `"`):
		return "String"
	case strings.HasPrefix(value, "'"):
		return "char"
	case value == "true" || value == "false":
		return "boolean"
	case strings.ContainsAny(value, ".eE") && !strings.HasPrefix(value, "0x"):
		return "double"
	default:
		return "int"
	}
}

func (a *JavaAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("System.getenv(%q)", varName)
}

// javaSourceRoots lists the directories, relative to the project root, that
// Java package paths are commonly resolved against.
var javaSourceRoots = []string{"", "src/main/java", "src/test/java", "src"}

// ResolveImportPath resolves "import com.example.Foo;" to the file
// com/example/Foo.java under one of the conventional source roots.
// Wildcard imports and imports that don't map to a local file return "".
func (a *JavaAdapter) ResolveImportPath(importText, _, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" || strings.HasSuffix(importText, "*") {
		return ""
	}
	relFile := strings.ReplaceAll(importText, ".", string(filepath.Separator)) + ".java"
	for _, srcRoot := range javaSourceRoots {
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
// stripping the conventional source root and the .java extension.
func (a *JavaAdapter) BuildImportPath(targetFile, _, root string) string {
	rel, err := filepath.Rel(root, targetFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	rel = filepath.ToSlash(strings.TrimSuffix(rel, ".java"))
	for _, srcRoot := range javaSourceRoots[1:] {
		if rest, ok := strings.CutPrefix(rel, srcRoot+"/"); ok {
			rel = rest
			break
		}
	}
	return strings.ReplaceAll(rel, "/", ".")
}

func (a *JavaAdapter) StructLiteralQuery() string {
	return "(object_creation_expression type: (type_identifier) @name) @literal"
}

func (a *JavaAdapter) FactoryFuncNames(typeName string) []string {
	return []string{typeName} // Java constructor has same name as class
}

func (a *JavaAdapter) GenFieldInitializer(_, value string) string {
	return value // Java uses positional constructor arguments
}
