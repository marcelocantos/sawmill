// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

// LanguageAdapter maps abstract structural patterns to language-specific
// Tree-sitter queries and code-generation templates.
type LanguageAdapter interface {
	// Language returns the tree-sitter Language for this adapter.
	Language() *tree_sitter.Language

	// Extensions returns the file extensions handled by this adapter (without
	// leading dot, e.g. "go", "py").
	Extensions() []string

	// FunctionDefQuery is a Tree-sitter query for function/method definitions.
	// Must capture @name for the identifier and @func for the whole node.
	FunctionDefQuery() string

	// IdentifierQuery is a Tree-sitter query for identifier references.
	// Must capture @name.
	IdentifierQuery() string

	// CallExprQuery is a Tree-sitter query for call expressions.
	// Must capture @name for the function name and @call for the whole expression.
	CallExprQuery() string

	// TypeDefQuery is a Tree-sitter query for class/struct/type definitions.
	// Must capture @name for the type name and @type_def for the whole node.
	TypeDefQuery() string

	// ImportQuery is a Tree-sitter query for import/include statements.
	// Must capture @name for the module name and @import for the whole statement.
	ImportQuery() string

	// FormatterCommand returns the formatter command and arguments that read
	// source from stdin and write formatted output to stdout. Returns nil if
	// no formatter is configured.
	FormatterCommand() []string

	// LSPCommand returns the LSP server command and arguments. Returns nil if
	// no LSP is configured.
	LSPCommand() []string

	// LSPLanguageID returns the LSP language identifier (e.g. "rust", "python").
	LSPLanguageID() string

	// FieldQuery is a Tree-sitter query for fields/attributes within a
	// struct/class. Must capture @name, optionally @type, and @field for the
	// whole node. Returns empty string if not applicable.
	FieldQuery() string

	// MethodQuery is a Tree-sitter query for methods within a class/impl block.
	// Must capture @name and @method for the whole node. Returns empty string
	// if not applicable.
	MethodQuery() string

	// DecoratorQuery is a Tree-sitter query for decorators/attributes on a
	// node. Must capture @decorator. Returns empty string if not applicable.
	DecoratorQuery() string

	// DocCommentPrefix returns the doc comment prefix (e.g. "///" for Rust,
	// "#" for Python, "//" for Go/TypeScript).
	DocCommentPrefix() string

	// FormatDocComment formats a multi-line doc string by prefixing each line
	// with the language's doc comment prefix and the given indentation.
	FormatDocComment(doc, indent string) string

	// GenField generates a field/attribute declaration (without doc comment).
	GenField(name, typeName string) string

	// GenFieldWithDoc generates a field declaration preceded by a doc comment.
	// Falls back to GenField when doc is empty.
	GenFieldWithDoc(name, typeName, doc string) string

	// GenMethod generates a method stub (without doc comment).
	GenMethod(name, params, returnType, body string) string

	// GenMethodWithDoc generates a method preceded by a doc comment.
	// Falls back to GenMethod when doc is empty.
	GenMethodWithDoc(name, params, returnType, body, doc string) string

	// GenImport generates an import statement for the given path.
	GenImport(path string) string

	// ResolveImportPath maps an import string (as it appears in source code)
	// to the filesystem path it refers to, relative to root. importingFile is
	// the absolute path of the file containing the import. Returns "" if the
	// import cannot be resolved to a local file (e.g. external packages,
	// system headers, intra-module use statements).
	ResolveImportPath(importText, importingFile, root string) string

	// BuildImportPath produces the import text that should appear in
	// importingFile to reference targetFile. Both paths are absolute.
	// root is the project root. Returns "" if the adapter cannot build
	// a suitable import path.
	BuildImportPath(targetFile, importingFile, root string) string

	// StructLiteralQuery is a Tree-sitter query for struct/class literal
	// constructions (e.g. Go composite literals, Rust struct expressions).
	// Must capture @name for the type name and @literal for the whole
	// expression. Returns empty string if the language has no struct literal
	// syntax.
	StructLiteralQuery() string

	// FactoryFuncNames returns the conventional factory function names for
	// the given type name (e.g. "NewFoo" for Go, "__init__" for Python).
	FactoryFuncNames(typeName string) []string

	// GenFieldInitializer generates a field initializer for use inside a
	// struct literal or constructor call (e.g. "Name: value" for Go,
	// "name: value" for Rust). Returns empty string if not applicable.
	GenFieldInitializer(fieldName, value string) string
}

// ForExtension returns the LanguageAdapter for the given file extension
// (without leading dot). Returns nil if the extension is unknown.
func ForExtension(ext string) LanguageAdapter {
	switch ext {
	case "py", "pyi":
		return &PythonAdapter{}
	case "rs":
		return &RustAdapter{}
	case "go":
		return &GoAdapter{}
	case "ts", "tsx":
		return &TypeScriptAdapter{}
	case "cpp", "cc", "cxx", "hpp", "hxx", "h":
		return &CppAdapter{}
	default:
		return nil
	}
}
