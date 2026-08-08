package compiler

import (
	"reflect"
	"strings"
	"testing"
)

func TestEveryPublicStandardLibrarySymbolIsDocumented(t *testing.T) {
	program, diagnostics := compile(nil)
	requireNoDiagnostics(t, diagnostics)
	if missing := program.undocumentedStandardLibrarySymbols(); len(missing) != 0 {
		t.Fatalf("undocumented standard-library symbols: %v", missing)
	}
}

func TestStandardLibraryDocumentationGateReportsCanonicalNames(t *testing.T) {
	program, diagnostics := compile(nil)
	requireNoDiagnostics(t, diagnostics)
	program.namespaceDocumentation["std.env"] = nil
	program.functions["std.env.Set"].documentation = nil
	failure := program.classes["std.env.Failure"]
	field := failure.fields["Message"]
	field.documentation = nil
	failure.fields["Message"] = field

	want := []string{"std.env", "std.env.Failure.Message", "std.env.Set"}
	if got := program.undocumentedStandardLibrarySymbols(); !reflect.DeepEqual(got, want) {
		t.Fatalf("undocumented symbols = %v, want %v", got, want)
	}
}

func TestDocumentationIsNotEmbeddedInGeneratedBinaries(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
/// GENERATED_DOCUMENTATION_SENTINEL
function main() -> string { "ok" }
`,
	}})
	requireNoDiagnostics(t, diagnostics)
	generated, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	if strings.Contains(generated, "GENERATED_DOCUMENTATION_SENTINEL") {
		t.Fatal("generated Go contains source documentation")
	}
}
