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

// RubyAdapter implements LanguageAdapter for Ruby source files.
type RubyAdapter struct{ baseAdapter }

func (a *RubyAdapter) Language() *tree_sitter.Language {
	return tree_sitter.RubyLanguage()
}

func (a *RubyAdapter) Extensions() []string { return []string{"rb", "rake", "gemspec"} }

func (a *RubyAdapter) FunctionDefQuery() string {
	return "[(method name: (identifier) @name) (singleton_method name: (identifier) @name)] @func"
}

func (a *RubyAdapter) IdentifierQuery() string {
	return "[(identifier) (constant) (instance_variable)] @name"
}

func (a *RubyAdapter) CallExprQuery() string {
	return "(call method: (identifier) @name) @call"
}

// TypeDefQuery uses two separate patterns — bracket alternation over the
// class/module node pair matches nothing in the gotreesitter engine.
func (a *RubyAdapter) TypeDefQuery() string {
	return "(class name: (constant) @name) @type_def\n(module name: (constant) @name) @type_def"
}

// ImportQuery matches require/require_relative calls; @name captures the
// string content without quotes.
func (a *RubyAdapter) ImportQuery() string {
	return `(call method: (identifier) @_m arguments: (argument_list (string (string_content) @name)) (#eq? @_m "require")) @import
(call method: (identifier) @_m arguments: (argument_list (string (string_content) @name)) (#eq? @_m "require_relative")) @import`
}

func (a *RubyAdapter) LSPCommand() []string { return []string{"solargraph", "stdio"} }

func (a *RubyAdapter) LSPLanguageID() string { return "ruby" }

// FieldQuery matches instance-variable assignments (@name = ...), the
// closest Ruby analogue to field declarations.
func (a *RubyAdapter) FieldQuery() string {
	return "(assignment left: (instance_variable) @name) @field"
}

func (a *RubyAdapter) MethodQuery() string {
	return "(method name: (identifier) @name) @method"
}

func (a *RubyAdapter) DocCommentPrefix() string { return "#" }

func (a *RubyAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

// GenField generates an attr_accessor — Ruby has no typed field
// declarations, so the type is ignored.
func (a *RubyAdapter) GenField(name, _ string) string {
	return fmt.Sprintf("  attr_accessor :%s\n", name)
}

func (a *RubyAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

// GenMethod generates a Ruby method — the return type is ignored.
func (a *RubyAdapter) GenMethod(name, params, _, body string) string {
	if params == "" {
		return fmt.Sprintf("  def %s\n    %s\n  end\n", name, body)
	}
	return fmt.Sprintf("  def %s(%s)\n    %s\n  end\n", name, params, body)
}

func (a *RubyAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *RubyAdapter) GenImport(path string) string {
	return fmt.Sprintf("require %q\n", path)
}

func (a *RubyAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("%s = %s\n", name, value)
}

func (a *RubyAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("ENV[%q]", varName)
}

// ResolveImportPath resolves a require/require_relative target to a local
// .rb file. The captured import text has no quotes and no extension.
func (a *RubyAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" {
		return ""
	}
	if !strings.HasSuffix(importText, ".rb") {
		importText += ".rb"
	}
	// require_relative-style: relative to the importing file's directory;
	// require-style: relative to root or root/lib.
	candidates := []string{
		filepath.Join(filepath.Dir(importingFile), importText),
		filepath.Join(root, importText),
		filepath.Join(root, "lib", importText),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		rel, err := filepath.Rel(root, c)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		return rel
	}
	return ""
}

// BuildImportPath produces a require_relative-style path (no extension,
// no quotes — the capture is the bare string content).
func (a *RubyAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	rel, err := filepath.Rel(filepath.Dir(importingFile), targetFile)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".rb")
}

// StructLiteralQuery matches Type.new(...) constructions.
func (a *RubyAdapter) StructLiteralQuery() string {
	return `(call receiver: (constant) @name method: (identifier) @_new (#eq? @_new "new")) @literal`
}

func (a *RubyAdapter) FactoryFuncNames(_ string) []string {
	return []string{"new", "initialize"}
}

func (a *RubyAdapter) GenFieldInitializer(fieldName, value string) string {
	return fmt.Sprintf("%s: %s", fieldName, value)
}
