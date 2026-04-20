// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/rewrite"
)

// handleExtractToEnv implements the extract_to_env MCP tool (🎯T8.2):
// replace every occurrence of a literal with an env-var read and scaffold
// the .env.example + .gitignore contract.
func (h *Handler) handleExtractToEnv(args map[string]any) (string, bool, error) {
	literal, err := requireString(args, "literal")
	if err != nil {
		return err.Error(), true, nil
	}
	varName, err := requireString(args, "var_name")
	if err != nil {
		return err.Error(), true, nil
	}
	pathFilter := optString(args, "path")
	format := optBool(args, "format")

	if literal == "" {
		return "literal must not be empty", true, nil
	}
	if !looksLikeEnvVarName(varName) {
		return fmt.Sprintf("var_name %q is not a valid POSIX env-var name (expected uppercase/digits/underscores, must not start with a digit)", varName), true, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	m, err := h.requireModel()
	if err != nil {
		return err.Error(), true, nil
	}

	accessors, err := m.FileAccessors(pathFilter)
	if err != nil {
		return fmt.Sprintf("listing files: %v", err), true, nil
	}

	var changes []forest.FileChange
	var diffs []string
	totalReplacements := 0

	for _, acc := range accessors {
		err := acc.WithTree(func(source []byte, tree *tree_sitter.Tree) error {
			adapter := acc.Adapter()
			replacement := adapter.GenEnvRead(varName)

			occurrences := findLiteralOccurrences(tree.RootNode(), source, literal)
			if len(occurrences) == 0 {
				return nil
			}

			edits := make([]rewrite.Edit, 0, len(occurrences))
			for _, occ := range occurrences {
				edits = append(edits, rewrite.Edit{
					Start:       occ.start,
					End:         occ.end,
					Replacement: replacement,
				})
			}

			newSource := rewrite.ApplyEdits(source, edits)
			if string(newSource) == string(source) {
				return nil
			}
			if format {
				newSource, _ = rewrite.FormatSource(adapter, newSource)
			}
			diff := rewrite.UnifiedDiff(acc.Path(), source, newSource)
			diffs = append(diffs, diff)
			changes = append(changes, forest.FileChange{
				Path:      acc.Path(),
				Original:  source,
				NewSource: newSource,
			})
			totalReplacements += len(occurrences)
			return nil
		})
		if err != nil {
			return fmt.Sprintf("processing %s: %v", acc.Path(), err), true, nil
		}
	}

	if len(changes) == 0 {
		return fmt.Sprintf("Literal %s not found in scope.", literal), false, nil
	}

	// Scaffold .env.example and .gitignore at the project root. These are
	// staged as FileChanges so they flow through the standard apply/undo.
	envExampleValue := unquoteLiteral(literal)
	if envChange, err := stageEnvExample(m.Root, varName, envExampleValue); err != nil {
		return fmt.Sprintf("staging .env.example: %v", err), true, nil
	} else if envChange != nil {
		changes = append(changes, *envChange)
		diffs = append(diffs, rewrite.UnifiedDiff(envChange.Path, envChange.Original, envChange.NewSource))
	}
	if giChange, err := stageGitignore(m.Root); err != nil {
		return fmt.Sprintf("staging .gitignore: %v", err), true, nil
	} else if giChange != nil {
		changes = append(changes, *giChange)
		diffs = append(diffs, rewrite.UnifiedDiff(giChange.Path, giChange.Original, giChange.NewSource))
	}

	h.pending = &PendingChanges{Changes: changes, Diffs: diffs}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Extracted %s → env var %s in %d file(s) (%d replacement(s)). Call apply to write.\n\n",
		literal, varName, len(changes), totalReplacements)
	for _, d := range diffs {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	// Nudge the user to ensure the relevant module is imported.
	sb.WriteString("Note: ensure your code imports the module providing the env read (os in Go/Python, std::env in Rust, <cstdlib> in C++).\n")
	return sb.String(), false, nil
}

// stageEnvExample builds a FileChange for <root>/.env.example that adds or
// updates the given key=value pair. Returns nil if the file already has the
// exact line and no update is needed.
func stageEnvExample(root, key, value string) (*forest.FileChange, error) {
	path := filepath.Join(root, ".env.example")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	line := fmt.Sprintf("%s=%s", key, value)
	existingStr := string(existing)

	// If the exact line (or an existing key=... line that already matches
	// the new value) is present, nothing to do.
	for _, l := range strings.Split(existingStr, "\n") {
		if strings.TrimSpace(l) == line {
			return nil, nil
		}
	}

	// If the key already exists with a different value, replace that line.
	// Otherwise append.
	var newContent string
	if idx := findEnvKeyLine(existingStr, key); idx >= 0 {
		newContent = replaceLineAt(existingStr, idx, line)
	} else {
		newContent = existingStr
		if len(newContent) > 0 && !strings.HasSuffix(newContent, "\n") {
			newContent += "\n"
		}
		newContent += line + "\n"
	}

	return &forest.FileChange{
		Path:      path,
		Original:  existing,
		NewSource: []byte(newContent),
	}, nil
}

// stageGitignore builds a FileChange for <root>/.gitignore that ensures
// `.env` is ignored. Returns nil if .env is already covered.
func stageGitignore(root string) (*forest.FileChange, error) {
	path := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	existingStr := string(existing)
	for _, l := range strings.Split(existingStr, "\n") {
		s := strings.TrimSpace(l)
		if s == ".env" || s == ".env/*" || s == ".env.*" || s == "*.env" {
			return nil, nil
		}
	}

	newContent := existingStr
	if len(newContent) > 0 && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += ".env\n"

	return &forest.FileChange{
		Path:      path,
		Original:  existing,
		NewSource: []byte(newContent),
	}, nil
}

// findEnvKeyLine returns the line index (0-based) of a line starting with
// "KEY=" in content, or -1 if absent.
func findEnvKeyLine(content, key string) int {
	prefix := key + "="
	for i, l := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), prefix) {
			return i
		}
	}
	return -1
}

// replaceLineAt replaces the i-th line (0-based) of content with newLine,
// preserving the surrounding newlines. If i is out of range, content is
// returned unchanged.
func replaceLineAt(content string, i int, newLine string) string {
	lines := strings.Split(content, "\n")
	if i < 0 || i >= len(lines) {
		return content
	}
	lines[i] = newLine
	return strings.Join(lines, "\n")
}

// unquoteLiteral strips enclosing quotes from a literal source text. Used to
// produce the .env.example right-hand side from a quoted string literal.
// Numeric literals pass through unchanged.
func unquoteLiteral(literal string) string {
	if len(literal) < 2 {
		return literal
	}
	first, last := literal[0], literal[len(literal)-1]
	if (first == '"' && last == '"') ||
		(first == '\'' && last == '\'') ||
		(first == '`' && last == '`') {
		return literal[1 : len(literal)-1]
	}
	return literal
}

// looksLikeEnvVarName enforces POSIX-ish env var naming: letters, digits,
// underscores, not starting with a digit. Convention is uppercase but we
// don't enforce that here.
func looksLikeEnvVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
