package compiler

import (
	"go/format"
	"strings"
	"testing"
)

func TestStdPathSyntheticDeclarations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root.app",
		Text: `
use std.path.Join as JoinPath

function Combine(Parts: string[]) -> string { JoinPath(Parts) }
function Suffix(Path: string) -> string? { std.path.Extension(Path) }
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	tests := []struct {
		canonical string
		name      string
		params    []string
		result    string
		native    nativeFunction
	}{
		{canonical: "std.path.Join", name: "Join", params: []string{"string[]"}, result: "string", native: nativeStdPathJoin},
		{canonical: "std.path.Clean", name: "Clean", params: []string{"string"}, result: "string", native: nativeStdPathClean},
		{canonical: "std.path.Base", name: "Base", params: []string{"string"}, result: "string", native: nativeStdPathBase},
		{canonical: "std.path.Directory", name: "Directory", params: []string{"string"}, result: "string", native: nativeStdPathDirectory},
		{canonical: "std.path.Extension", name: "Extension", params: []string{"string"}, result: "string?", native: nativeStdPathExtension},
		{canonical: "std.path.IsAbsolute", name: "IsAbsolute", params: []string{"string"}, result: "bool", native: nativeStdPathIsAbsolute},
	}
	for _, test := range tests {
		assertStandardFunction(t, program.functions[test.canonical], "std.path", test.name, test.params, test.result, test.native)
	}

	combine := program.functions["root.app.Combine"]
	join := program.functions["std.path.Join"]
	if resolved := program.resolveFunction(combine, "JoinPath"); resolved != join {
		t.Fatalf("JoinPath resolved to %+v, want std.path.Join", resolved)
	}
	if resolved := program.resolveFunction(combine, "std.path.Join"); resolved != join {
		t.Fatalf("absolute std.path.Join resolved to %+v", resolved)
	}
	if program.functions["root.app.std.path.Join"] != nil {
		t.Fatal("std.path.Join was also registered relative to root.app")
	}
}

func TestStdPathGeneratedSourceIsDeterministicAndFilepathBacked(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { std.path.Clean("alpha/../beta") }`,
	}})
	requireNoDiagnostics(t, diagnostics)

	first, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	second, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go again: %v", err)
	}
	if first != second {
		t.Fatal("generated Go source is not deterministic")
	}
	if _, err := format.Source([]byte(first)); err != nil {
		t.Fatalf("generated Go is not formattable: %v", err)
	}
	for _, call := range []string{"filepath.Join", "filepath.Clean", "filepath.Base", "filepath.Dir", "filepath.Ext", "filepath.IsAbs"} {
		if !strings.Contains(first, call) {
			t.Fatalf("generated Go does not call %s", call)
		}
	}
	if !strings.Contains(first, `"path/filepath"`) {
		t.Fatal("generated Go does not import path/filepath")
	}
	if !strings.Contains(first, "slickNone[string]()") || !strings.Contains(first, "slickSome(value)") {
		t.Fatal("generated Extension does not use the shared Optional representation")
	}
}
