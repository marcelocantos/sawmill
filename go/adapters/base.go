// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"strings"
)

// baseAdapter provides default implementations for the optional parts of
// LanguageAdapter. Language-specific adapters embed this struct and override
// the methods they need.
type baseAdapter struct{}

// FormatterCommand returns nil (no formatter configured).
func (b *baseAdapter) FormatterCommand() []string { return nil }

// LSPCommand returns nil (no LSP configured).
func (b *baseAdapter) LSPCommand() []string { return nil }

// LSPLanguageID returns an empty string.
func (b *baseAdapter) LSPLanguageID() string { return "" }

// FieldQuery returns an empty string (no field query).
func (b *baseAdapter) FieldQuery() string { return "" }

// MethodQuery returns an empty string (no method query).
func (b *baseAdapter) MethodQuery() string { return "" }

// DecoratorQuery returns an empty string (no decorator query).
func (b *baseAdapter) DecoratorQuery() string { return "" }

// DocCommentPrefix returns "//" as the default doc comment prefix.
func (b *baseAdapter) DocCommentPrefix() string { return "//" }

// FormatDocComment formats doc by prefixing each line with the language's
// doc comment prefix and the supplied indentation.
//
// Note: concrete adapters that override DocCommentPrefix must also override
// FormatDocComment and call FormatDocCommentWith(doc, indent, prefix) so that
// the correct prefix is used.
func (b *baseAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, b.DocCommentPrefix())
}

// FormatDocCommentWith formats doc using the supplied prefix and indentation.
// Each line is prefixed as "<indent><prefix> <line>" (or "<indent><prefix>"
// for empty lines).
func FormatDocCommentWith(doc, indent, prefix string) string {
	var sb strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			fmt.Fprintf(&sb, "%s%s\n", indent, prefix)
		} else {
			fmt.Fprintf(&sb, "%s%s %s\n", indent, prefix, line)
		}
	}
	return sb.String()
}

// GenFieldWithDoc generates a field preceded by a doc comment. Falls back to
// GenField when doc is empty. Concrete adapters that embed baseAdapter and
// override GenField will have their GenField called here because this method
// accepts a LanguageAdapter to avoid losing the override.
func GenFieldWithDoc(a LanguageAdapter, name, typeName, doc string) string {
	if doc == "" {
		return a.GenField(name, typeName)
	}
	field := a.GenField(name, typeName)
	indent := leadingWhitespace(field)
	comment := a.FormatDocComment(doc, indent)
	return comment + field
}

// GenMethodWithDoc generates a method preceded by a doc comment. Falls back to
// GenMethod when doc is empty.
func GenMethodWithDoc(a LanguageAdapter, name, params, returnType, body, doc string) string {
	if doc == "" {
		return a.GenMethod(name, params, returnType, body)
	}
	method := a.GenMethod(name, params, returnType, body)
	indent := leadingWhitespace(method)
	comment := a.FormatDocComment(doc, indent)
	return comment + method
}

// ResolveImportPath returns "" (unknown resolution).
func (b *baseAdapter) ResolveImportPath(_, _, _ string) string { return "" }

// BuildImportPath returns "" (unknown resolution).
func (b *baseAdapter) BuildImportPath(_, _, _ string) string { return "" }

// StructLiteralQuery returns an empty string (no struct literal query).
func (b *baseAdapter) StructLiteralQuery() string { return "" }

// FactoryFuncNames returns nil (no factory patterns).
func (b *baseAdapter) FactoryFuncNames(_ string) []string { return nil }

// GenFieldInitializer returns an empty string.
func (b *baseAdapter) GenFieldInitializer(_, _ string) string { return "" }


// leadingWhitespace returns the leading whitespace characters of s.
func leadingWhitespace(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return s[:i]
		}
	}
	return s
}
