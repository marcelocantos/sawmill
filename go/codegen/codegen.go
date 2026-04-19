// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package codegen provides a code generator runtime that executes JavaScript
// programs against the entire codebase model. The program receives a ctx object
// with methods for querying symbols, navigating code structure, and making
// coordinated edits across files.
package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"modernc.org/quickjs"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
	"github.com/marcelocantos/sawmill/index"
	"github.com/marcelocantos/sawmill/lspclient"
	"github.com/marcelocantos/sawmill/rewrite"
)

// editCollector accumulates edits across multiple files.
type editCollector struct {
	edits    map[string][]rewrite.Edit
	newFiles map[string]string
}

func newEditCollector() *editCollector {
	return &editCollector{
		edits:    make(map[string][]rewrite.Edit),
		newFiles: make(map[string]string),
	}
}

func (c *editCollector) addEdit(file string, edit rewrite.Edit) {
	c.edits[file] = append(c.edits[file], edit)
}

func (c *editCollector) addNewFile(path, content string) {
	c.newFiles[path] = content
}

// codegenHelpers defines the node constructor for the codegen context.
// Mutation methods call __editFile directly rather than returning markers.
const codegenHelpers = `
globalThis.__makeNode = function(props) {
    var n = Object.assign({}, props);

    n.replaceText = function(text) {
        __editFile(n.file, n.startByte, n.endByte, text);
        return n;
    };
    n.replaceBody = function(body) {
        if (n.bodyStartByte !== null && n.bodyStartByte !== undefined) {
            __editFile(n.file, n.bodyStartByte, n.bodyEndByte, body);
        }
        return n;
    };
    n.replaceName = function(name) {
        if (n.nameStartByte !== null && n.nameStartByte !== undefined) {
            __editFile(n.file, n.nameStartByte, n.nameEndByte, name);
        }
        return n;
    };
    n.remove = function() {
        __editFile(n.file, n.startByte, n.endByte, "");
        return n;
    };
    n.insertBefore = function(code) {
        __editFile(n.file, n.startByte, n.startByte, code + "\n");
        return n;
    };
    n.insertAfter = function(code) {
        __editFile(n.file, n.endByte, n.endByte, "\n" + code);
        return n;
    };

    n.fields = function() {
        return n._fields || [];
    };
    n.methods = function() {
        return (n._methods || []).map(function(m) { return __makeNode(m); });
    };
    n.method = function(name) {
        var ms = n.methods();
        for (var i = 0; i < ms.length; i++) {
            if (ms[i].name === name) return ms[i];
        }
        return null;
    };
    n.returnType = function() {
        return n._returnType || null;
    };

    n.addField = function(name, type, doc) {
        var code = __genField(n._langId || "", name, type, doc || "");
        if (n.bodyEndByte !== null && n.bodyEndByte !== undefined) {
            __editFile(n.file, n.bodyEndByte, n.bodyEndByte, code);
        } else if (n.endByte) {
            __editFile(n.file, n.endByte - 1, n.endByte - 1, code);
        }
        return n;
    };
    n.addMethod = function(name, params, returnType, body, doc) {
        var code = __genMethod(n._langId || "", name, params, returnType, body, doc || "");
        if (n.bodyEndByte !== null && n.bodyEndByte !== undefined) {
            __editFile(n.file, n.bodyEndByte, n.bodyEndByte, "\n" + code);
        } else if (n.endByte) {
            __editFile(n.file, n.endByte, n.endByte, "\n" + code);
        }
        return n;
    };

    return n;
};
`

// ctxSetupTemplate is the JS code that builds the ctx object's query methods.
// It receives __symbols and __files as injected JSON data.
const ctxSetupTemplate = `
(function(ctx) {
    var __symbols = %s;
    var __files = %s;

    ctx.findFunction = function(name) {
        return __symbols.filter(function(s) {
            return s.kind === "function" && s.name === name;
        }).map(function(s) { return __makeNode(s); });
    };

    ctx.findType = function(name) {
        return __symbols.filter(function(s) {
            return s.kind === "type" && s.name === name;
        }).map(function(s) { return __makeNode(s); });
    };

    ctx.query = function(opts) {
        return __symbols.filter(function(s) {
            if (opts.kind && s.kind !== opts.kind) return false;
            if (opts.name) {
                if (opts.name.includes("*")) {
                    var regex = new RegExp("^" + opts.name.replace(/\*/g, ".*") + "$");
                    if (!regex.test(s.name)) return false;
                } else {
                    if (s.name !== opts.name) return false;
                }
            }
            if (opts.file && !s.file.includes(opts.file)) return false;
            return true;
        }).map(function(s) { return __makeNode(s); });
    };

    ctx.references = function(name) {
        return __symbols.filter(function(s) {
            return s.kind === "call" && s.name === name;
        }).map(function(s) { return __makeNode(s); });
    };

    ctx.readFile = function(path) {
        var f = __files[path];
        return f !== undefined ? f : null;
    };

    ctx.addFile = function(path, content) {
        __addFile(path, content);
    };

    ctx.editFile = function(path, startByte, endByte, replacement) {
        __editFile(path, startByte, endByte, replacement);
    };

    ctx.addImport = function(filePath, importPath) {
        var langId = "";
        var ext = filePath.split(".").pop();
        if (ext === "py") langId = "python";
        else if (ext === "rs") langId = "rust";
        else if (ext === "ts" || ext === "tsx") langId = "typescript";
        else if (ext === "go") langId = "go";
        else if (ext === "cpp" || ext === "cc" || ext === "h") langId = "cpp";
        var code = __genImport(langId, importPath);
        if (code) {
            __editFile(filePath, 0, 0, code);
        }
    };

    ctx.genField = function(langId, name, type) {
        return __genField(langId, name, type, "");
    };

    ctx.genMethod = function(langId, name, params, returnType, body) {
        return __genMethod(langId, name, params, returnType, body, "");
    };

    ctx.hasLsp = false;
})(ctx);
`

// RunCodegen executes a codegen program against the forest (without LSP).
func RunCodegen(f *forest.Forest, program string) ([]forest.FileChange, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("creating QuickJS VM: %w", err)
	}
	defer vm.Close()

	collector := newEditCollector()

	// Register __editFile callback.
	vm.RegisterFunc("__editFile", func(file string, start, end int, replacement string) {
		collector.addEdit(file, rewrite.Edit{
			Start:       uint(start),
			End:         uint(end),
			Replacement: replacement,
		})
	}, false)

	// Register __addFile callback.
	vm.RegisterFunc("__addFile", func(path, content string) {
		collector.addNewFile(path, content)
	}, false)

	// Register code generation callbacks.
	vm.RegisterFunc("__genField", func(langID, name, typeName, doc string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenFieldWithDoc(name, typeName, doc)
		})
	}, false)

	vm.RegisterFunc("__genMethod", func(langID, name, params, returnType, body, doc string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenMethodWithDoc(name, params, returnType, body, doc)
		})
	}, false)

	vm.RegisterFunc("__genImport", func(langID, path string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenImport(path)
		})
	}, false)

	// Inject helpers.
	if _, err := vm.Eval(codegenHelpers, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("injecting codegen helpers: %w", err)
	}

	// Build data.
	allSymbols := buildAllSymbolJSON(f)
	allFiles := buildAllFilesJSON(f)
	filePaths := buildFilePathsJSON(f)

	// Create ctx object.
	ctxInit := fmt.Sprintf("var ctx = {files: %s};", filePaths)
	if _, err := vm.Eval(ctxInit, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("creating ctx: %w", err)
	}

	// Set up ctx methods.
	setupCode := fmt.Sprintf(ctxSetupTemplate, allSymbols, allFiles)
	if _, err := vm.Eval(setupCode, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("setting up ctx: %w", err)
	}

	// Execute the user's program.
	wrapped := fmt.Sprintf("(function(ctx) { %s })(ctx)", program)
	if _, err := vm.Eval(wrapped, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("executing codegen program: %w", err)
	}

	// Collect results.
	var changes []forest.FileChange

	for _, file := range f.Files {
		fileKey := file.Path
		edits, ok := collector.edits[fileKey]
		if !ok {
			continue
		}

		sort.Slice(edits, func(i, j int) bool {
			return edits[i].Start < edits[j].Start
		})

		newSource := rewrite.ApplyEdits(file.OriginalSource, edits)
		if string(newSource) != string(file.OriginalSource) {
			changes = append(changes, forest.FileChange{
				Path:      file.Path,
				Original:  file.OriginalSource,
				NewSource: newSource,
			})
		}
	}

	for path, content := range collector.newFiles {
		changes = append(changes, forest.FileChange{
			Path:      path,
			Original:  nil,
			NewSource: []byte(content),
		})
	}

	return changes, nil
}

// lspCtxSetup is the JS code that wires LSP methods onto the ctx object.
// It requires __lspHover, __lspDefinition, __lspReferences, __lspDiagnostics
// to be registered as host callbacks.
const lspCtxSetup = `
(function(ctx) {
    ctx.typeOf = function(file, line, col) {
        return __lspHover(file, line, col);
    };
    ctx.definition = function(file, line, col) {
        return JSON.parse(__lspDefinition(file, line, col));
    };
    ctx.lspReferences = function(file, line, col) {
        return JSON.parse(__lspReferences(file, line, col));
    };
    ctx.diagnostics = function(file) {
        return JSON.parse(__lspDiagnostics(file));
    };
    ctx.hasLsp = true;
})(ctx);
`

// RunCodegenWithLSP is like RunCodegen but also registers LSP callbacks
// and sets ctx.hasLsp = true when a pool is available.
func RunCodegenWithLSP(f *forest.Forest, program string, lspPool *lspclient.Pool, root string) ([]forest.FileChange, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("creating QuickJS VM: %w", err)
	}
	defer vm.Close()

	collector := newEditCollector()

	// Register standard callbacks.
	vm.RegisterFunc("__editFile", func(file string, start, end int, replacement string) {
		collector.addEdit(file, rewrite.Edit{
			Start:       uint(start),
			End:         uint(end),
			Replacement: replacement,
		})
	}, false)
	vm.RegisterFunc("__addFile", func(path, content string) {
		collector.addNewFile(path, content)
	}, false)
	vm.RegisterFunc("__genField", func(langID, name, typeName, doc string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenFieldWithDoc(name, typeName, doc)
		})
	}, false)
	vm.RegisterFunc("__genMethod", func(langID, name, params, returnType, body, doc string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenMethodWithDoc(name, params, returnType, body, doc)
		})
	}, false)
	vm.RegisterFunc("__genImport", func(langID, path string) string {
		return genForLang(langID, func(a adapters.LanguageAdapter) string {
			return a.GenImport(path)
		})
	}, false)

	// Register LSP callbacks.
	vm.RegisterFunc("__lspHover", func(file string, line, col int) string {
		adapter := adapterForFile(file)
		if adapter == nil || lspPool == nil {
			return ""
		}
		client := lspPool.Get(adapter, root)
		if client == nil {
			return ""
		}
		text, err := client.Hover(context.Background(), file, uint32(line), uint32(col))
		if err != nil {
			return ""
		}
		return text
	}, false)
	vm.RegisterFunc("__lspDefinition", func(file string, line, col int) string {
		adapter := adapterForFile(file)
		if adapter == nil || lspPool == nil {
			return "[]"
		}
		client := lspPool.Get(adapter, root)
		if client == nil {
			return "[]"
		}
		locs, err := client.Definition(context.Background(), file, uint32(line), uint32(col))
		if err != nil {
			return "[]"
		}
		data, _ := json.Marshal(locs)
		return string(data)
	}, false)
	vm.RegisterFunc("__lspReferences", func(file string, line, col int) string {
		adapter := adapterForFile(file)
		if adapter == nil || lspPool == nil {
			return "[]"
		}
		client := lspPool.Get(adapter, root)
		if client == nil {
			return "[]"
		}
		locs, err := client.References(context.Background(), file, uint32(line), uint32(col))
		if err != nil {
			return "[]"
		}
		data, _ := json.Marshal(locs)
		return string(data)
	}, false)
	vm.RegisterFunc("__lspDiagnostics", func(file string) string {
		adapter := adapterForFile(file)
		if adapter == nil || lspPool == nil {
			return "[]"
		}
		client := lspPool.Get(adapter, root)
		if client == nil {
			return "[]"
		}
		diags, err := client.Diagnostics(context.Background(), file)
		if err != nil {
			return "[]"
		}
		data, _ := json.Marshal(diags)
		return string(data)
	}, false)

	// Inject helpers.
	if _, err := vm.Eval(codegenHelpers, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("injecting codegen helpers: %w", err)
	}

	allSymbols := buildAllSymbolJSON(f)
	allFiles := buildAllFilesJSON(f)
	filePaths := buildFilePathsJSON(f)

	ctxInit := fmt.Sprintf("var ctx = {files: %s};", filePaths)
	if _, err := vm.Eval(ctxInit, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("creating ctx: %w", err)
	}

	setupCode := fmt.Sprintf(ctxSetupTemplate, allSymbols, allFiles)
	if _, err := vm.Eval(setupCode, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("setting up ctx: %w", err)
	}

	// Wire LSP onto ctx.
	if _, err := vm.Eval(lspCtxSetup, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("setting up LSP ctx: %w", err)
	}

	wrapped := fmt.Sprintf("(function(ctx) { %s })(ctx)", program)
	if _, err := vm.Eval(wrapped, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("executing codegen program: %w", err)
	}

	var changes []forest.FileChange
	for _, file := range f.Files {
		edits, ok := collector.edits[file.Path]
		if !ok {
			continue
		}
		sort.Slice(edits, func(i, j int) bool {
			return edits[i].Start < edits[j].Start
		})
		newSource := rewrite.ApplyEdits(file.OriginalSource, edits)
		if string(newSource) != string(file.OriginalSource) {
			changes = append(changes, forest.FileChange{
				Path:      file.Path,
				Original:  file.OriginalSource,
				NewSource: newSource,
			})
		}
	}
	for path, content := range collector.newFiles {
		changes = append(changes, forest.FileChange{
			Path:      path,
			Original:  nil,
			NewSource: []byte(content),
		})
	}
	return changes, nil
}

// adapterForFile returns the language adapter for a file based on its extension.
func adapterForFile(file string) adapters.LanguageAdapter {
	ext := fileExt(file)
	return adapters.ForExtension(ext)
}

// ConventionViolation is a structured convention violation as returned by a
// convention check program. The program may return either an array of plain
// strings (legacy contract) or an array of these objects (new contract);
// RunConventionCheck normalises both into ConventionViolation.
//
// Field names mirror sawmill's mcp.Violation shape one-for-one. Kept in the
// codegen package to avoid an import cycle with mcp.
type ConventionViolation struct {
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Rule         string `json:"rule,omitempty"`
	Message      string `json:"message,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

// RunConventionCheck executes a convention check program against the forest.
// The program should return an array of violations. Each element may be
// either a plain string (treated as Message in the resulting struct) or an
// object with file/line/column/severity/rule/message/snippet/suggested_fix
// fields. An empty array means "no violations".
func RunConventionCheck(f *forest.Forest, checkProgram string) ([]ConventionViolation, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, fmt.Errorf("creating QuickJS VM: %w", err)
	}
	defer vm.Close()

	// Register dummy callbacks (convention checks don't edit).
	vm.RegisterFunc("__editFile", func(string, int, int, string) {}, false)
	vm.RegisterFunc("__addFile", func(string, string) {}, false)
	vm.RegisterFunc("__genField", func(string, string, string, string) string { return "" }, false)
	vm.RegisterFunc("__genMethod", func(string, string, string, string, string, string) string { return "" }, false)
	vm.RegisterFunc("__genImport", func(string, string) string { return "" }, false)

	// Inject helpers.
	if _, err := vm.Eval(codegenHelpers, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("injecting helpers: %w", err)
	}

	allSymbols := buildAllSymbolJSON(f)
	allFiles := buildAllFilesJSON(f)
	filePaths := buildFilePathsJSON(f)

	ctxInit := fmt.Sprintf("var ctx = {files: %s};", filePaths)
	if _, err := vm.Eval(ctxInit, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("creating ctx: %w", err)
	}

	setupCode := fmt.Sprintf(ctxSetupTemplate, allSymbols, allFiles)
	if _, err := vm.Eval(setupCode, quickjs.EvalGlobal); err != nil {
		return nil, fmt.Errorf("setting up ctx: %w", err)
	}

	wrapped := fmt.Sprintf("JSON.stringify((function(ctx) { %s })(ctx))", checkProgram)
	result, err := vm.Eval(wrapped, quickjs.EvalGlobal)
	if err != nil {
		return nil, fmt.Errorf("executing convention check: %w", err)
	}

	resultStr, ok := result.(string)
	if !ok || resultStr == "" || resultStr == "null" || resultStr == "undefined" {
		return nil, nil
	}

	// Try parsing as array of structured ConventionViolation objects first.
	// Strings still parse as ConventionViolation with all fields zero, which
	// we then disambiguate from the string fallback.
	var rawArr []json.RawMessage
	if err := json.Unmarshal([]byte(resultStr), &rawArr); err == nil {
		out := make([]ConventionViolation, 0, len(rawArr))
		for _, item := range rawArr {
			out = append(out, parseConventionViolation(item))
		}
		return out, nil
	}

	// Try as a single string.
	var single string
	if err := json.Unmarshal([]byte(resultStr), &single); err == nil && single != "" {
		return []ConventionViolation{{Message: single}}, nil
	}

	return nil, nil
}

// parseConventionViolation handles either a JSON string or a JSON object,
// returning a ConventionViolation. Strings populate Message; objects map
// field-for-field.
func parseConventionViolation(raw json.RawMessage) ConventionViolation {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return ConventionViolation{Message: s}
	}
	var v ConventionViolation
	_ = json.Unmarshal(raw, &v)
	return v
}

// ValidateChanges re-parses modified files and checks for parse errors.
func ValidateChanges(changes []forest.FileChange) []string {
	var errors []string

	for _, change := range changes {
		ext := fileExt(change.Path)
		adapter := adapters.ForExtension(ext)
		if adapter == nil {
			continue
		}

		parser := tree_sitter.NewParser()
		defer parser.Close()

		if err := parser.SetLanguage(adapter.Language()); err != nil {
			continue
		}

		tree := parser.Parse(change.NewSource, nil)
		if tree == nil {
			errors = append(errors, fmt.Sprintf("%s: failed to parse after transformation", change.Path))
			continue
		}
		defer tree.Close()

		if tree.RootNode().HasError() {
			errors = append(errors, fmt.Sprintf("%s: parse error after transformation", change.Path))
		}
	}

	return errors
}

// StructuralChecks detects removed symbols still referenced after changes.
func StructuralChecks(f *forest.Forest, changes []forest.FileChange) []string {
	changedPaths := make(map[string]*forest.FileChange, len(changes))
	for i := range changes {
		changedPaths[changes[i].Path] = &changes[i]
	}

	// Pre-change: collect function/type definition names.
	preFunctions := make(map[string]bool)
	for _, file := range f.Files {
		for _, sym := range index.ExtractSymbols(file) {
			if sym.Kind == "function" || sym.Kind == "type" {
				preFunctions[sym.Name] = true
			}
		}
	}

	// Post-change: build symbol lists by combining re-parsed changed files
	// with unchanged forest files.
	postFunctions := make(map[string]bool)
	var postCalls []index.Symbol

	for _, file := range f.Files {
		var syms []index.Symbol
		if change, ok := changedPaths[file.Path]; ok {
			tmp := parseChange(change)
			if tmp == nil {
				continue
			}
			syms = index.ExtractSymbols(tmp)
		} else {
			syms = index.ExtractSymbols(file)
		}
		for _, sym := range syms {
			switch sym.Kind {
			case "function", "type":
				postFunctions[sym.Name] = true
			case "call":
				postCalls = append(postCalls, sym)
			}
		}
	}

	// Handle brand-new files not yet in the forest.
	forestPaths := make(map[string]bool, len(f.Files))
	for _, file := range f.Files {
		forestPaths[file.Path] = true
	}
	for i := range changes {
		if forestPaths[changes[i].Path] {
			continue
		}
		tmp := parseChange(&changes[i])
		if tmp == nil {
			continue
		}
		for _, sym := range index.ExtractSymbols(tmp) {
			switch sym.Kind {
			case "function", "type":
				postFunctions[sym.Name] = true
			case "call":
				postCalls = append(postCalls, sym)
			}
		}
	}

	// Removed symbols: existed pre-change, missing post-change.
	removed := make(map[string]bool)
	for name := range preFunctions {
		if !postFunctions[name] {
			removed[name] = true
		}
	}

	var warnings []string
	for _, call := range postCalls {
		if removed[call.Name] {
			warnings = append(warnings, fmt.Sprintf(
				"Removed symbol `%s` still referenced at %s:%d",
				call.Name, call.FilePath, call.StartLine,
			))
		}
	}

	return warnings
}

// genForLang dispatches a code generation call to the appropriate adapter.
func genForLang(langID string, f func(adapters.LanguageAdapter) string) string {
	var adapter adapters.LanguageAdapter
	switch langID {
	case "python":
		adapter = &adapters.PythonAdapter{}
	case "rust":
		adapter = &adapters.RustAdapter{}
	case "typescript":
		adapter = &adapters.TypeScriptAdapter{}
	case "cpp":
		adapter = &adapters.CppAdapter{}
	case "go":
		adapter = &adapters.GoAdapter{}
	default:
		return ""
	}
	return f(adapter)
}

// parseChange parses the new source from a FileChange into a temporary
// ParsedFile. Returns nil if the extension is unrecognised or parsing fails.
func parseChange(change *forest.FileChange) *forest.ParsedFile {
	ext := fileExt(change.Path)
	adapter := adapters.ForExtension(ext)
	if adapter == nil {
		return nil
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(adapter.Language()); err != nil {
		return nil
	}

	tree := parser.Parse(change.NewSource, nil)
	if tree == nil {
		return nil
	}

	return &forest.ParsedFile{
		Path:           change.Path,
		OriginalSource: change.NewSource,
		Tree:           tree,
		Adapter:        adapter,
	}
}

// fileExt extracts the file extension without the leading dot.
func fileExt(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
		if path[i] == '/' {
			break
		}
	}
	return ""
}

// nodeInfo holds body and parameter byte ranges for a tree-sitter node.
type nodeInfo struct {
	BodyStart  *uint
	BodyEnd    *uint
	BodyText   *string
	ParamsText *string
}

// findNodeInfo extracts body/parameter info from a node at the given byte range.
func findNodeInfo(file *forest.ParsedFile, start, end uint) *nodeInfo {
	node := file.Tree.RootNode().DescendantForByteRange(start, end)
	if node == nil {
		return nil
	}

	bodyNode := node.ChildByFieldName("body")
	paramsNode := node.ChildByFieldName("parameters")

	if bodyNode == nil && paramsNode == nil {
		return nil
	}

	info := &nodeInfo{}
	if bodyNode != nil {
		bs := bodyNode.StartByte()
		be := bodyNode.EndByte()
		info.BodyStart = &bs
		info.BodyEnd = &be
		text := string(file.OriginalSource[bs:be])
		info.BodyText = &text
	}
	if paramsNode != nil {
		text := string(file.OriginalSource[paramsNode.StartByte():paramsNode.EndByte()])
		info.ParamsText = &text
	}
	return info
}

// extractPrecedingComment extracts the doc comment from the preceding sibling.
func extractPrecedingComment(node *tree_sitter.Node, source []byte) string {
	prev := node.PrevSibling()
	if prev == nil {
		return ""
	}

	var commentLines []string
	for prev != nil {
		kind := prev.Kind()
		if kind == "comment" || kind == "line_comment" || kind == "block_comment" {
			text := string(source[prev.StartByte():prev.EndByte()])
			stripped := text
			for _, prefix := range []string{"///", "//!", "//", "#"} {
				if strings.HasPrefix(stripped, prefix) {
					stripped = stripped[len(prefix):]
					break
				}
			}
			stripped = strings.TrimLeft(stripped, " ")
			commentLines = append(commentLines, stripped)
			prev = prev.PrevSibling()
		} else {
			break
		}
	}

	if len(commentLines) == 0 {
		return ""
	}

	// Reverse (they were collected backwards).
	for i, j := 0, len(commentLines)-1; i < j; i, j = i+1, j-1 {
		commentLines[i], commentLines[j] = commentLines[j], commentLines[i]
	}
	return strings.Join(commentLines, "\n")
}

// extractFields extracts field information from a type node.
func extractFields(file *forest.ParsedFile, nodeStart, nodeEnd uint) []map[string]any {
	fieldQueryStr := file.Adapter.FieldQuery()
	if fieldQueryStr == "" {
		return nil
	}

	query, qErr := tree_sitter.NewQuery(file.Adapter.Language(), fieldQueryStr)
	if qErr != nil {
		return nil
	}
	defer query.Close()

	captureNames := query.CaptureNames()
	indexOf := func(name string) int {
		for i, n := range captureNames {
			if n == name {
				return i
			}
		}
		return -1
	}

	nameIdx := indexOf("name")
	typeIdx := indexOf("type")
	fieldIdx := indexOf("field")

	node := file.Tree.RootNode().DescendantForByteRange(nodeStart, nodeEnd)
	if node == nil {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.SetByteRange(nodeStart, nodeEnd)

	matches := cursor.Matches(query, node, file.OriginalSource)

	var fields []map[string]any
	for m := matches.Next(); m != nil; m = matches.Next() {
		var nameText, typeText string

		if nameIdx >= 0 {
			for _, c := range m.Captures {
				if c.Index == uint32(nameIdx) {
					nameText = string(file.OriginalSource[c.Node.StartByte():c.Node.EndByte()])
					break
				}
			}
		}
		if nameText == "" {
			continue
		}

		if typeIdx >= 0 {
			for _, c := range m.Captures {
				if c.Index == uint32(typeIdx) {
					typeText = string(file.OriginalSource[c.Node.StartByte():c.Node.EndByte()])
					break
				}
			}
		}

		entry := map[string]any{
			"name": nameText,
			"type": typeText,
		}

		// Look for doc comment on the field node.
		if fieldIdx >= 0 {
			for _, c := range m.Captures {
				if c.Index == uint32(fieldIdx) {
					doc := extractPrecedingComment(&c.Node, file.OriginalSource)
					if doc != "" {
						entry["doc"] = doc
					}
					break
				}
			}
		}

		fields = append(fields, entry)
	}

	return fields
}

// extractMethods extracts method information from a type node.
func extractMethods(file *forest.ParsedFile, nodeStart, nodeEnd uint) []map[string]any {
	methodQueryStr := file.Adapter.MethodQuery()
	if methodQueryStr == "" {
		return nil
	}

	query, qErr := tree_sitter.NewQuery(file.Adapter.Language(), methodQueryStr)
	if qErr != nil {
		return nil
	}
	defer query.Close()

	captureNames := query.CaptureNames()
	indexOf := func(name string) int {
		for i, n := range captureNames {
			if n == name {
				return i
			}
		}
		return -1
	}

	nameIdx := indexOf("name")
	methodIdx := indexOf("method")

	node := file.Tree.RootNode().DescendantForByteRange(nodeStart, nodeEnd)
	if node == nil {
		return nil
	}

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.SetByteRange(nodeStart, nodeEnd)

	matches := cursor.Matches(query, node, file.OriginalSource)

	var methods []map[string]any
	for m := matches.Next(); m != nil; m = matches.Next() {
		var nameText string
		var methodNode *tree_sitter.Node

		if nameIdx >= 0 {
			for i := range m.Captures {
				if m.Captures[i].Index == uint32(nameIdx) {
					nameText = string(file.OriginalSource[m.Captures[i].Node.StartByte():m.Captures[i].Node.EndByte()])
					break
				}
			}
		}

		if methodIdx >= 0 {
			for i := range m.Captures {
				if m.Captures[i].Index == uint32(methodIdx) {
					methodNode = &m.Captures[i].Node
					break
				}
			}
		}

		if nameText != "" && methodNode != nil {
			text := string(file.OriginalSource[methodNode.StartByte():methodNode.EndByte()])
			methods = append(methods, map[string]any{
				"name":      nameText,
				"startByte": methodNode.StartByte(),
				"endByte":   methodNode.EndByte(),
				"startLine": methodNode.StartPosition().Row + 1,
				"text":      text,
			})
		}
	}

	return methods
}

// buildAllSymbolJSON builds a JSON array of all symbols from the forest.
func buildAllSymbolJSON(f *forest.Forest) string {
	var allSymbols []map[string]any

	for _, file := range f.Files {
		symbols := index.ExtractSymbols(file)

		for _, sym := range symbols {
			end := sym.EndByte
			if end > uint(len(file.OriginalSource)) {
				end = uint(len(file.OriginalSource))
			}
			text := string(file.OriginalSource[sym.StartByte:end])

			entry := map[string]any{
				"name":          sym.Name,
				"kind":          sym.Kind,
				"file":          file.Path,
				"startLine":     sym.StartLine,
				"endLine":       sym.EndLine,
				"startByte":     sym.StartByte,
				"endByte":       sym.EndByte,
				"text":          text,
				"nameStartByte": sym.NameStartByte,
				"nameEndByte":   sym.NameEndByte,
			}

			// Try to find body and parameters.
			if info := findNodeInfo(file, sym.StartByte, sym.EndByte); info != nil {
				entry["bodyStartByte"] = info.BodyStart
				entry["bodyEndByte"] = info.BodyEnd
				if info.BodyText != nil {
					entry["body"] = *info.BodyText
				}
				if info.ParamsText != nil {
					entry["parameters"] = *info.ParamsText
				}
			}

			// For type symbols, extract fields and methods.
			if sym.Kind == "type" {
				if fields := extractFields(file, sym.StartByte, sym.EndByte); len(fields) > 0 {
					entry["_fields"] = fields
				}
				if methods := extractMethods(file, sym.StartByte, sym.EndByte); len(methods) > 0 {
					entry["_methods"] = methods
				}
			}

			// Store the language ID for code generation.
			entry["_langId"] = file.Adapter.LSPLanguageID()

			allSymbols = append(allSymbols, entry)
		}
	}

	data, err := json.Marshal(allSymbols)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// buildAllFilesJSON builds a JSON object mapping file paths to source text.
func buildAllFilesJSON(f *forest.Forest) string {
	files := make(map[string]string, len(f.Files))
	for _, file := range f.Files {
		files[file.Path] = string(file.OriginalSource)
	}
	data, err := json.Marshal(files)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// buildFilePathsJSON builds a JSON array of all file paths.
func buildFilePathsJSON(f *forest.Forest) string {
	paths := make([]string, len(f.Files))
	for i, file := range f.Files {
		paths[i] = file.Path
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return "[]"
	}
	return string(data)
}
