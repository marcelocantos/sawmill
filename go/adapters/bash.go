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

// BashAdapter implements LanguageAdapter for shell scripts (.sh / .bash).
//
// Shell has functions but no types or fields. TypeDefQuery / FieldQuery /
// GenField are intentionally empty so add_field is a no-op. Rename targets
// words and variable names matching the from-string — agents should prefer
// scoped renames; whole-file shell rename is best-effort.
type BashAdapter struct{ baseAdapter }

func (a *BashAdapter) Language() *tree_sitter.Language {
	return tree_sitter.BashLanguage()
}

func (a *BashAdapter) Extensions() []string { return []string{"sh", "bash"} }

func (a *BashAdapter) FunctionDefQuery() string {
	return "(function_definition (word) @name) @func"
}

func (a *BashAdapter) IdentifierQuery() string {
	return "[(word) (variable_name)] @name"
}

func (a *BashAdapter) CallExprQuery() string {
	return "(command (command_name (word) @name)) @call"
}

// TypeDefQuery is empty — shell has no type declarations.
func (a *BashAdapter) TypeDefQuery() string { return "" }

func (a *BashAdapter) ImportQuery() string {
	return `(command (command_name (word) @_c) (#eq? @_c "source") (word) @name) @import
(command (command_name (word) @_c) (#eq? @_c ".") (word) @name) @import`
}

func (a *BashAdapter) FormatterCommand() []string { return []string{"shfmt", "-"} }

func (a *BashAdapter) LSPCommand() []string {
	return []string{"bash-language-server", "start"}
}

func (a *BashAdapter) LSPLanguageID() string { return "shellscript" }

func (a *BashAdapter) DocCommentPrefix() string { return "#" }

func (a *BashAdapter) FormatDocComment(doc, indent string) string {
	return FormatDocCommentWith(doc, indent, a.DocCommentPrefix())
}

// GenField is a no-op — shell has no fields.
func (a *BashAdapter) GenField(_, _ string) string { return "" }

func (a *BashAdapter) GenFieldWithDoc(name, typeName, doc string) string {
	return GenFieldWithDoc(a, name, typeName, doc)
}

func (a *BashAdapter) GenMethod(name, params, _, body string) string {
	return fmt.Sprintf("%s() {\n  %s\n}\n", name, body)
}

func (a *BashAdapter) GenMethodWithDoc(name, params, returnType, body, doc string) string {
	return GenMethodWithDoc(a, name, params, returnType, body, doc)
}

func (a *BashAdapter) GenImport(path string) string {
	return fmt.Sprintf("source %s\n", path)
}

func (a *BashAdapter) GenConstDeclaration(name, value string) string {
	return fmt.Sprintf("readonly %s=%s\n", name, value)
}

func (a *BashAdapter) GenEnvRead(varName string) string {
	return fmt.Sprintf("${%s}", varName)
}

// ResolveImportPath resolves `source ./file.sh` relative to the importing file.
func (a *BashAdapter) ResolveImportPath(importText, importingFile, root string) string {
	importText = strings.TrimSpace(importText)
	if importText == "" {
		return ""
	}
	var abs string
	if filepath.IsAbs(importText) {
		abs = importText
	} else {
		abs = filepath.Join(filepath.Dir(importingFile), importText)
	}
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return rel
}

func (a *BashAdapter) BuildImportPath(targetFile, importingFile, _ string) string {
	rel, err := filepath.Rel(filepath.Dir(importingFile), targetFile)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return filepath.ToSlash(rel)
}
