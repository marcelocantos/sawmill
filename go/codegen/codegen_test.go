// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"strings"
	"testing"

	tree_sitter "github.com/marcelocantos/sawmill/tscompat"

	"github.com/marcelocantos/sawmill/adapters"
	"github.com/marcelocantos/sawmill/forest"
)

func makeForest(files []struct{ path, source string }) *forest.Forest {
	adapter := adapters.LanguageAdapter(&adapters.PythonAdapter{})
	var parsed []*forest.ParsedFile

	for _, f := range files {
		sourceBytes := []byte(f.source)
		parser := tree_sitter.NewParser()
		defer parser.Close()
		if err := parser.SetLanguage(adapter.Language()); err != nil {
			panic(err)
		}
		tree := parser.Parse(sourceBytes, nil)
		parsed = append(parsed, &forest.ParsedFile{
			Path:           f.path,
			OriginalSource: sourceBytes,
			Tree:           tree,
			Adapter:        adapter,
		})
	}

	return &forest.Forest{Files: parsed}
}

func makeRustForest(files []struct{ path, source string }) *forest.Forest {
	adapter := adapters.LanguageAdapter(&adapters.RustAdapter{})
	var parsed []*forest.ParsedFile

	for _, f := range files {
		sourceBytes := []byte(f.source)
		parser := tree_sitter.NewParser()
		defer parser.Close()
		if err := parser.SetLanguage(adapter.Language()); err != nil {
			panic(err)
		}
		tree := parser.Parse(sourceBytes, nil)
		parsed = append(parsed, &forest.ParsedFile{
			Path:           f.path,
			OriginalSource: sourceBytes,
			Tree:           tree,
			Adapter:        adapter,
		})
	}

	return &forest.Forest{Files: parsed}
}

func TestCodegenRenameAcrossFiles(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"a.py", "def foo():\n    pass\n"},
		{"b.py", "foo()\n"},
	})

	changes, err := RunCodegen(f, `
		var fns = ctx.findFunction("foo");
		for (var i = 0; i < fns.length; i++) {
			fns[i].replaceName("bar");
		}
		var refs = ctx.references("foo");
		for (var i = 0; i < refs.length; i++) {
			refs[i].replaceName("bar");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}

	var aChange, bChange *forest.FileChange
	for i := range changes {
		switch changes[i].Path {
		case "a.py":
			aChange = &changes[i]
		case "b.py":
			bChange = &changes[i]
		}
	}

	if aChange == nil || string(aChange.NewSource) != "def bar():\n    pass\n" {
		t.Errorf("a.py: got %q", string(aChange.NewSource))
	}
	if bChange == nil || string(bChange.NewSource) != "bar()\n" {
		t.Errorf("b.py: got %q", string(bChange.NewSource))
	}
}

func TestCodegenQueryWithGlob(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"test.py", "def test_a():\n    pass\n\ndef test_b():\n    pass\n\ndef helper():\n    pass\n"},
	})

	changes, err := RunCodegen(f, `
		var tests = ctx.query({kind: "function", name: "test_*"});
		for (var i = 0; i < tests.length; i++) {
			tests[i].remove();
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	result := string(changes[0].NewSource)
	if !strings.Contains(result, "helper") {
		t.Errorf("helper should remain: %s", result)
	}
	if strings.Contains(result, "test_a") {
		t.Errorf("test_a should be removed: %s", result)
	}
}

func TestCodegenAddNewFile(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"main.py", "pass\n"},
	})

	changes, err := RunCodegen(f, `
		ctx.addFile("new_module.py", "# Generated\ndef generated():\n    pass\n");
	`)
	if err != nil {
		t.Fatal(err)
	}

	var newFile *forest.FileChange
	for i := range changes {
		if changes[i].Path == "new_module.py" {
			newFile = &changes[i]
			break
		}
	}
	if newFile == nil {
		t.Fatal("new file should be created")
	}
	if !strings.Contains(string(newFile.NewSource), "Generated") {
		t.Errorf("new file should contain 'Generated': %s", string(newFile.NewSource))
	}
}

func TestCodegenReadFile(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"config.py", "DB_HOST = 'localhost'\n"},
	})

	changes, err := RunCodegen(f, `
		var content = ctx.readFile("config.py");
		if (content && content.includes("localhost")) {
			ctx.addFile("warning.txt", "Config uses localhost!\n");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	var warning *forest.FileChange
	for i := range changes {
		if changes[i].Path == "warning.txt" {
			warning = &changes[i]
			break
		}
	}
	if warning == nil {
		t.Fatal("warning file should be created")
	}
}

func TestCodegenFieldsAndMethods(t *testing.T) {
	f := makeRustForest([]struct{ path, source string }{
		{"lib.rs", "struct User {\n    name: String,\n    age: u32,\n}\n"},
	})

	changes, err := RunCodegen(f, `
		var types = ctx.findType("User");
		if (types.length > 0) {
			var user = types[0];
			var fields = user.fields();
			var fieldNames = fields.map(function(f) { return f.name; }).join(", ");
			user.insertBefore("// Fields: " + fieldNames);
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	result := string(changes[0].NewSource)
	if !strings.Contains(result, "// Fields: name, age") {
		t.Errorf("should list fields: %s", result)
	}
}

func TestCodegenAddField(t *testing.T) {
	f := makeRustForest([]struct{ path, source string }{
		{"lib.rs", "struct User {\n    name: String,\n}\n"},
	})

	changes, err := RunCodegen(f, `
		var types = ctx.findType("User");
		if (types.length > 0) {
			types[0].addField("email", "String");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	result := string(changes[0].NewSource)
	if !strings.Contains(result, "email: String") {
		t.Errorf("should contain new field: %s", result)
	}
}

func TestCodegenGenImport(t *testing.T) {
	f := makeRustForest([]struct{ path, source string }{
		{"lib.rs", "fn main() {}\n"},
	})

	changes, err := RunCodegen(f, `
		ctx.addImport("lib.rs", "std::collections::HashMap");
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	result := string(changes[0].NewSource)
	if !strings.Contains(result, "use std::collections::HashMap;") {
		t.Errorf("should contain import: %s", result)
	}
}

func TestCodegenAddFieldWithDoc(t *testing.T) {
	f := makeRustForest([]struct{ path, source string }{
		{"lib.rs", "struct User {\n    /// The user's name.\n    name: String,\n}\n"},
	})

	changes, err := RunCodegen(f, `
		var types = ctx.findType("User");
		if (types.length > 0) {
			types[0].addField("email", "String", "The user's email address.");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	result := string(changes[0].NewSource)
	if !strings.Contains(result, "/// The user's email address.") {
		t.Errorf("should contain doc comment: %s", result)
	}
	if !strings.Contains(result, "email: String") {
		t.Errorf("should contain field: %s", result)
	}
}

func TestCodegenReadFieldDocs(t *testing.T) {
	f := makeRustForest([]struct{ path, source string }{
		{"lib.rs", "struct User {\n    /// The user's name.\n    name: String,\n    age: u32,\n}\n"},
	})

	changes, err := RunCodegen(f, `
		var types = ctx.findType("User");
		if (types.length > 0) {
			var fields = types[0].fields();
			var docs = [];
			for (var i = 0; i < fields.length; i++) {
				docs.push(fields[i].name + ":" + (fields[i].doc || "none"));
			}
			ctx.addFile("fields.txt", docs.join("\n") + "\n");
		}
	`)
	if err != nil {
		t.Fatal(err)
	}

	var fieldsFile *forest.FileChange
	for i := range changes {
		if changes[i].Path == "fields.txt" {
			fieldsFile = &changes[i]
			break
		}
	}
	if fieldsFile == nil {
		t.Fatal("fields.txt should be created")
	}

	content := string(fieldsFile.NewSource)
	if !strings.Contains(content, "name:The user's name.") {
		t.Errorf("should have name doc: %s", content)
	}
	if !strings.Contains(content, "age:none") {
		t.Errorf("age should have no doc: %s", content)
	}
}

func TestValidateCatchesParseErrors(t *testing.T) {
	changes := []forest.FileChange{{
		Path:      "broken.py",
		Original:  []byte("x = 1\n"),
		NewSource: []byte("def (\n"),
	}}

	errors := ValidateChanges(changes)
	if len(errors) == 0 {
		t.Fatal("should detect parse error")
	}
	if !strings.Contains(errors[0], "broken.py") {
		t.Errorf("error should mention file: %s", errors[0])
	}
}

func TestStructuralCheckRemovedFunction(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"lib.py", "def compute():\n    pass\n"},
		{"main.py", "compute()\n"},
	})

	changes := []forest.FileChange{{
		Path:      "lib.py",
		Original:  []byte("def compute():\n    pass\n"),
		NewSource: []byte("# compute was removed\n"),
	}}

	warnings := StructuralChecks(f, changes)
	if len(warnings) == 0 {
		t.Fatal("should detect removed symbol still referenced")
	}

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "compute") && strings.Contains(w, "main.py") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warning should mention compute and main.py: %v", warnings)
	}
}

func TestStructuralCheckRenamedFunctionMissingCallUpdate(t *testing.T) {
	f := makeForest([]struct{ path, source string }{
		{"defs.py", "def foo():\n    pass\n"},
		{"caller.py", "foo()\n"},
	})

	changes := []forest.FileChange{{
		Path:      "defs.py",
		Original:  []byte("def foo():\n    pass\n"),
		NewSource: []byte("def bar():\n    pass\n"),
	}}

	warnings := StructuralChecks(f, changes)
	if len(warnings) == 0 {
		t.Fatal("should detect that foo was removed but still called")
	}

	foundFoo := false
	foundCaller := false
	for _, w := range warnings {
		if strings.Contains(w, "foo") {
			foundFoo = true
		}
		if strings.Contains(w, "caller.py") {
			foundCaller = true
		}
	}
	if !foundFoo {
		t.Errorf("warning should mention foo: %v", warnings)
	}
	if !foundCaller {
		t.Errorf("warning should mention caller.py: %v", warnings)
	}
}
