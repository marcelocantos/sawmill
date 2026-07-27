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

// ProtoAdapter implements LanguageAdapter for Protocol Buffer (.proto) files.
// Messages map to types; fields to fields; rpc methods to functions.
type ProtoAdapter struct{ baseAdapter }

func (a *ProtoAdapter) Language() *tree_sitter.Language {
	return tree_sitter.ProtoLanguage()
}

func (a *ProtoAdapter) Extensions() []string { return []string{"proto"} }

// FunctionDefQuery captures rpc method names (the closest proto analogue to
// functions). Free functions do not exist in protobuf.
func (a *ProtoAdapter) FunctionDefQuery() string {
	return "(rpc (rpc_name (identifier) @name)) @func"
}

func (a *ProtoAdapter) IdentifierQuery() string {
	return "(identifier) @name"
}

// CallExprQuery is empty — protobuf has no call expressions.
func (a *ProtoAdapter) CallExprQuery() string { return "" }

func (a *ProtoAdapter) TypeDefQuery() string {
	return "(message (message_name (identifier) @name)) @type_def"
}

func (a *ProtoAdapter) ImportQuery() string {
	return `(import (string) @name) @import`
}

func (a *ProtoAdapter) LSPCommand() []string {
	return []string{"protols", "--stdio"}
}

func (a *ProtoAdapter) LSPLanguageID() string { return "protobuf" }

func (a *ProtoAdapter) FieldQuery() string {
	return "(field (identifier) @name) @field"
}

func (a *ProtoAdapter) MethodQuery() string {
	return "(rpc (rpc_name (identifier) @name)) @method"
}

func (a *ProtoAdapter) DocCommentPrefix() string { return "//" }

// GenField emits a proto3 scalar field. Field numbers for generated fields
// default to 100 so they sit clear of hand-written low numbers; callers that
// care about numbering should edit after generation.
func (a *ProtoAdapter) GenField(name, typeName string) string {
	if typeName == "" {
		typeName = "string"
	}
	return fmt.Sprintf("  %s %s = 100;\n", typeName, name)
}

func (a *ProtoAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *ProtoAdapter) GenMethod(name, params, returnType, body string) string {
	// RPC stubs: params/returnType are message type names; body is unused.
	if returnType == "" {
		returnType = "Empty"
	}
	if params == "" {
		params = "Empty"
	}
	return fmt.Sprintf("  rpc %s(%s) returns (%s);\n", name, params, returnType)
}

func (a *ProtoAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *ProtoAdapter) GenImport(path string) string {
	path = strings.Trim(path, `"`)
	return fmt.Sprintf("import %q;\n", path)
}

// GenConstDeclaration is not idiomatic in proto — emit a comment placeholder.
func (a *ProtoAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("// const %s = %s\n", name, value)
}

func (a *ProtoAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("/* env %s */", varName)
}

// ResolveImportPath resolves import "path/to/file.proto" relative to root.
func (a *ProtoAdapter) ResolveImportPath(importText, _, root string) string {
	importText = strings.TrimSpace(strings.Trim(importText, `"`))
	if importText == "" {
		return ""
	}
	abs := filepath.Join(root, importText)
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return rel
}

func (a *ProtoAdapter) BuildImportPath(targetFile, _, root string) string {
	rel, err := filepath.Rel(root, targetFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}
