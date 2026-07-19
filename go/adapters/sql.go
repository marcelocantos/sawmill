// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"fmt"
	"strings"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"
)

// SqlAdapter implements LanguageAdapter for SQL files.
//
// Tables map to types, columns to fields, CREATE FUNCTION to functions.
// The underlying grammar is dialect-agnostic and best-effort — complex
// PL/pgSQL bodies may partially error while still exposing the DDL names.
type SqlAdapter struct{ baseAdapter }

func (a *SqlAdapter) Language() *tree_sitter.Language {
	return tree_sitter.SqlLanguage()
}

func (a *SqlAdapter) Extensions() []string { return []string{"sql"} }

func (a *SqlAdapter) FunctionDefQuery() string {
	return "(create_function_statement (identifier) @name) @func"
}

func (a *SqlAdapter) IdentifierQuery() string {
	return "(identifier) @name"
}

func (a *SqlAdapter) CallExprQuery() string {
	return "(function_call (identifier) @name) @call"
}

func (a *SqlAdapter) TypeDefQuery() string {
	return "(create_table_statement (identifier) @name) @type_def"
}

// ImportQuery is empty — SQL has no portable import construct.
func (a *SqlAdapter) ImportQuery() string { return "" }

func (a *SqlAdapter) FieldQuery() string {
	return "(table_column (identifier) @name) @field"
}

func (a *SqlAdapter) DocCommentPrefix() string { return "--" }

func (a *SqlAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

// GenField emits a column declaration for insertion into CREATE TABLE (...).
func (a *SqlAdapter) GenField(name, typeName string) string {
	if typeName == "" {
		typeName = "INTEGER"
	}
	return fmt.Sprintf("  %s %s,\n", name, typeName)
}

func (a *SqlAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *SqlAdapter) GenMethod(name, params, returnType, body string) string {
	if returnType == "" {
		returnType = "void"
	}
	if body == "" {
		body = "NULL"
	}
	return fmt.Sprintf("CREATE FUNCTION %s(%s) RETURNS %s AS %s LANGUAGE sql;\n",
		name, params, returnType, quoteSQLBody(body))
}

func (a *SqlAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *SqlAdapter) GenImport(_ string) string { return "" }

func (a *SqlAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("-- const %s = %s\n", name, value)
}

func (a *SqlAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("current_setting(%q)", varName)
}

func quoteSQLBody(body string) string {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "'") || strings.HasPrefix(body, "$$") {
		return body
	}
	return "'" + strings.ReplaceAll(body, "'", "''") + "'"
}
