// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package adapters

import "strings"

// LanguageInfo is the agent-facing capability card for one supported language.
// It is the source of truth for what tools can honestly claim about that
// language — not just "is there a grammar", but which operations work and
// where the sharp edges are.
type LanguageInfo struct {
	// ID is the stable language key (e.g. "go", "lua", "bash").
	ID string `json:"id"`
	// Name is a human-readable label.
	Name string `json:"name"`
	// Extensions are file extensions without a leading dot.
	Extensions []string `json:"extensions"`
	// Parse: files of this language enter the forest.
	Parse bool `json:"parse"`
	// FindSymbol: function/type index queries work.
	FindSymbol bool `json:"find_symbol"`
	// Rename: identifier rewrite is available. See Notes for quality caveats
	// (e.g. Bash is best-effort word-level, not binding-aware).
	Rename bool `json:"rename"`
	// AddField: add_field can insert into type-like bodies.
	AddField bool `json:"add_field"`
	// ImportRewrite: rename_file can rewrite import/require paths.
	ImportRewrite bool `json:"import_rewrite"`
	// Formatter is the configured formatter argv, empty if none.
	Formatter []string `json:"formatter,omitempty"`
	// LSP is the configured language-server argv, empty if none.
	LSP []string `json:"lsp,omitempty"`
	// ASTMerge is "full" (declaration algebra + fixtures), "adapter"
	// (algebra via queries, no dedicated fixtures), or "text" (whole-file
	// diff3 fallthrough).
	ASTMerge string `json:"ast_merge"`
	// Notes are agent-facing caveats. Always read these before relying on
	// rename/add_field/import_rewrite for this language.
	Notes []string `json:"notes,omitempty"`
}

// languageCatalog is the ordered list of supported languages. Keep in sync
// with ForExtension — every extension branch must appear here.
var languageCatalog = []LanguageInfo{
	{
		ID: "python", Name: "Python", Extensions: []string{"py", "pyi"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"ruff", "format", "-"},
		LSP:       []string{"pyright-langserver", "--stdio"},
		ASTMerge:  "full",
	},
	{
		ID: "go", Name: "Go", Extensions: []string{"go"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"gofmt"},
		LSP:       []string{"gopls"},
		ASTMerge:  "full",
	},
	{
		ID: "rust", Name: "Rust", Extensions: []string{"rs"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"rustfmt"},
		LSP:       []string{"rust-analyzer"},
		ASTMerge:  "adapter",
	},
	{
		ID: "typescript", Name: "TypeScript", Extensions: []string{"ts", "tsx"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"prettier", "--stdin-filepath", "file.ts"},
		LSP:       []string{"typescript-language-server", "--stdio"},
		ASTMerge:  "adapter",
	},
	{
		ID: "javascript", Name: "JavaScript", Extensions: []string{"js", "jsx", "mjs", "cjs"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"prettier", "--stdin-filepath", "file.js"},
		LSP:       []string{"typescript-language-server", "--stdio"},
		ASTMerge:  "adapter",
	},
	{
		ID: "c", Name: "C", Extensions: []string{"c"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"clang-format"},
		LSP:       []string{"clangd"},
		ASTMerge:  "adapter",
		Notes:     []string{".h headers are routed to the C++ adapter, not C."},
	},
	{
		ID: "cpp", Name: "C++", Extensions: []string{"cpp", "cc", "cxx", "hpp", "hxx", "h"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"clang-format"},
		LSP:       []string{"clangd"},
		ASTMerge:  "adapter",
	},
	{
		ID: "java", Name: "Java", Extensions: []string{"java"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"clang-format"},
		LSP:       []string{"jdtls"},
		ASTMerge:  "adapter",
	},
	{
		ID: "csharp", Name: "C#", Extensions: []string{"cs"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"clang-format"},
		LSP:       []string{"csharp-ls"},
		ASTMerge:  "adapter",
	},
	{
		ID: "ruby", Name: "Ruby", Extensions: []string{"rb", "rake", "gemspec"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		LSP:      []string{"solargraph", "stdio"},
		ASTMerge: "adapter",
		Notes:    []string{"Fields are modeled as instance-variable assignments and attr_accessor codegen."},
	},
	{
		ID: "php", Name: "PHP", Extensions: []string{"php"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: false,
		LSP:      []string{"intelephense", "--stdio"},
		ASTMerge: "adapter",
		Notes:    []string{"Import path rewrite is not implemented (namespace use ≠ filesystem path)."},
	},
	{
		ID: "kotlin", Name: "Kotlin", Extensions: []string{"kt", "kts"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		LSP:      []string{"kotlin-language-server"},
		ASTMerge: "adapter",
	},
	{
		ID: "swift", Name: "Swift", Extensions: []string{"swift"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: false,
		Formatter: []string{"swift-format"},
		LSP:       []string{"sourcekit-lsp"},
		ASTMerge:  "adapter",
	},
	{
		ID: "lua", Name: "Lua", Extensions: []string{"lua"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"stylua", "-"},
		LSP:       []string{"lua-language-server"},
		ASTMerge:  "adapter",
		Notes: []string{
			"Types are table-assignment stand-ins (Widget = { ... }); not real classes.",
			"add_field inserts into table constructors.",
		},
	},
	{
		ID: "protobuf", Name: "Protobuf", Extensions: []string{"proto"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		LSP:      []string{"protols", "--stdio"},
		ASTMerge: "adapter",
		Notes: []string{
			"Functions map to rpc methods; free functions do not exist.",
			"Generated fields use field number 100 by default — adjust numbers after codegen.",
		},
	},
	{
		ID: "zig", Name: "Zig", Extensions: []string{"zig"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: true,
		Formatter: []string{"zig", "fmt", "--stdin"},
		LSP:       []string{"zls"},
		ASTMerge:  "adapter",
	},
	{
		ID: "bash", Name: "Bash / Shell", Extensions: []string{"sh", "bash"},
		Parse: true, FindSymbol: true, Rename: true, AddField: false, ImportRewrite: true,
		Formatter: []string{"shfmt", "-"},
		LSP:       []string{"bash-language-server", "start"},
		ASTMerge:  "text",
		Notes: []string{
			"No types or fields — add_field is a no-op.",
			"Rename is best-effort over word/variable_name tokens, not binding-aware scope. Prefer narrow path= scopes; review diffs carefully.",
			"Imports are source / . paths only.",
		},
	},
	{
		ID: "sql", Name: "SQL", Extensions: []string{"sql"},
		Parse: true, FindSymbol: true, Rename: true, AddField: true, ImportRewrite: false,
		ASTMerge: "text",
		Notes: []string{
			"Dialect-agnostic best-effort grammar; complex PL/pgSQL or vendor DDL may partially fail to parse while still exposing names.",
			"Tables map to types; columns to fields; CREATE FUNCTION to functions.",
			"No portable import model — import rewrite is unavailable.",
		},
	},
}

// AllLanguages returns a copy of the capability catalog in stable order.
func AllLanguages() []LanguageInfo {
	out := make([]LanguageInfo, len(languageCatalog))
	copy(out, languageCatalog)
	return out
}

// LookupLanguage finds a language by id, name, or file extension (with or
// without a leading dot). Matching is case-insensitive. Returns nil if unknown.
func LookupLanguage(key string) *LanguageInfo {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.TrimPrefix(key, ".")
	if key == "" {
		return nil
	}
	// Aliases agents commonly pass.
	switch key {
	case "shell", "sh", "zsh":
		key = "bash"
	case "proto", "protobuf", "protocol-buffers", "protocolbuffers":
		key = "protobuf"
	case "c++", "cplusplus", "cxx":
		key = "cpp"
	case "c#", "cs", "csharp":
		key = "csharp"
	case "js":
		key = "javascript"
	case "ts":
		key = "typescript"
	case "py":
		key = "python"
	case "rs":
		key = "rust"
	case "kt":
		key = "kotlin"
	case "rb":
		key = "ruby"
	}

	for i := range languageCatalog {
		info := &languageCatalog[i]
		if strings.ToLower(info.ID) == key || strings.ToLower(info.Name) == key {
			cp := *info
			return &cp
		}
		for _, ext := range info.Extensions {
			if ext == key {
				cp := *info
				return &cp
			}
		}
	}
	// Secondary: name contains (e.g. "Bash / Shell")
	for i := range languageCatalog {
		info := &languageCatalog[i]
		if strings.Contains(strings.ToLower(info.Name), key) {
			cp := *info
			return &cp
		}
	}
	return nil
}
