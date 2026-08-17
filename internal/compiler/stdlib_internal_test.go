package compiler

import (
	"go/format"
	"slices"
	"strings"
	"testing"
)

func TestStdEnvSyntheticDeclarations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root.app",
		Text: `
use std.env.Get as GetEnv
use std.env.Failure as EnvFailure

function Read(Name: string) -> string? effects { environment } {
    GetEnv(Name)
}

function Write(Name: string, Value: string) -> Result<null, EnvFailure> effects { environment } {
    std.env.Set(Name, Value)
}

function Remove(Name: string) -> Result<null, std.env.Failure> effects { environment } {
    std.env.Unset(Name)
}

function Raise(Name: string) -> null throws EnvFailure {
    throw EnvFailure {
        Operation: "Test"
        Name: Name
        Message: "failure"
    }
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	get := program.functions["std.env.Get"]
	assertStandardFunction(t, get, "std.env", "Get", []string{"string"}, "string?", nativeStdEnvGet)
	set := program.functions["std.env.Set"]
	assertStandardFunction(t, set, "std.env", "Set", []string{"string", "string"}, "Result<null,std.env.Failure>", nativeStdEnvSet)
	unset := program.functions["std.env.Unset"]
	assertStandardFunction(t, unset, "std.env", "Unset", []string{"string"}, "Result<null,std.env.Failure>", nativeStdEnvUnset)

	failure := program.classes[stdEnvFailureName]
	if failure == nil {
		t.Fatal("std.env.Failure was not registered")
	}
	if failure.namespace != "std.env" || failure.name != "Failure" || !failure.isError {
		t.Fatalf("unexpected Failure declaration: %+v", failure)
	}
	if len(failure.fields) != 3 {
		t.Fatalf("Failure must expose exactly three fields, found %+v", failure.fields)
	}
	for _, name := range []string{"Operation", "Name", "Message"} {
		field, ok := failure.fields[name]
		if !ok || field.typ.name != "string" {
			t.Fatalf("Failure.%s must be string, found %+v", name, field)
		}
	}

	read := program.functions["root.app.Read"]
	if read == nil {
		t.Fatal("test Read function was not registered")
	}
	if resolved := program.resolveFunction(read, "GetEnv"); resolved != get {
		t.Fatalf("GetEnv resolved to %+v, want std.env.Get", resolved)
	}
	if resolved := program.resolveFunction(read, "std.env.Get"); resolved != get {
		t.Fatalf("absolute std.env.Get resolved to %+v", resolved)
	}
	if resolved := program.resolveType("root.app", read.aliases, typeRef{name: "Result<null,EnvFailure>"}); resolved != "Result<null,std.env.Failure>" {
		t.Fatalf("EnvFailure in Result resolved to %q", resolved)
	}
	if resolved, ok := program.resolveErrorIn("root.app", read.aliases, "EnvFailure"); !ok || resolved != stdEnvFailureName {
		t.Fatalf("EnvFailure error resolved to %q, %t", resolved, ok)
	}
	if program.functions["root.app.std.env.Get"] != nil {
		t.Fatal("std.env.Get was also registered relative to root.app")
	}
}

func TestStdNamespaceCannotBeDefinedByUserSources(t *testing.T) {
	diagnostics := Check([]Source{{
		Name:      "user.slk",
		Namespace: "std.env",
		Text:      `function Get(Name: string) -> string { Name }`,
	}})
	requireDiagnostic(t, diagnostics, "SLK100", `invalid namespace "std.env"`)
}

func TestStdEnvGeneratedSourceIsDeterministicAndRuntimeBacked(t *testing.T) {
	t.Setenv("SLICK_STDLIB_BUILD_TIME_SENTINEL", "must-not-be-generated")
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string? effects { environment } { std.env.Get("SLICK_STDLIB_BUILD_TIME_SENTINEL") }`,
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
	for _, call := range []string{"os.LookupEnv", "os.Setenv", "os.Unsetenv"} {
		if !strings.Contains(first, call) {
			t.Fatalf("generated Go does not call %s at runtime", call)
		}
	}
	if strings.Contains(first, "must-not-be-generated") {
		t.Fatal("generated Go captured a build-time environment value")
	}
	if !strings.Contains(first, "slickNone[string]()") || !strings.Contains(first, "slickSome(value)") {
		t.Fatal("generated Get does not use the shared Optional representation")
	}
	if strings.Count(first, "type slickResult[") != 1 {
		t.Fatal("generated Go contains more than one Result representation")
	}
}

func TestEveryStandardLibraryNativeHasLLVMLowering(t *testing.T) {
	symbols := nativeSymbols()
	check := func(native nativeFunction) {
		t.Helper()
		if native == "" || isNativeStdBuffer(native) ||
			native == nativeStdJsonDecode || native == nativeStdJsonEncode {
			return
		}
		symbol := nativeSymbol(native)
		if symbol == "" {
			t.Fatalf("%s has no LLVM lowering", native)
		}
		if !slices.Contains(symbols, symbol) {
			t.Fatalf("%s lowers to undeclared LLVM native %s", native, symbol)
		}
	}
	for _, function := range standardLibraryRegistry.functions {
		check(function.native)
	}
	for _, class := range standardLibraryRegistry.classes {
		for _, method := range class.methods {
			check(method.native)
		}
	}
	for _, iface := range standardLibraryRegistry.interfaces {
		for _, method := range iface.methods {
			check(method.native)
		}
	}

	if err := (&llvmGen{}).emitNativeWrapper(
		&functionDecl{native: nativeFunction("std.missing.Native")},
		"",
		"",
	); err == nil || !strings.Contains(err.Error(), "unknown native Slick function") {
		t.Fatalf("unknown LLVM native error = %v", err)
	}
}

func assertStandardFunction(t *testing.T, function *functionDecl, namespace, name string, parameters []string, result string, native nativeFunction) {
	t.Helper()
	if function == nil {
		t.Fatalf("std function %s.%s was not registered", namespace, name)
	}
	if function.namespace != namespace || function.name != name || function.native != native {
		t.Fatalf("unexpected std function declaration: %+v", function)
	}
	if len(function.params) != len(parameters) {
		t.Fatalf("%s must have %d parameters, found %+v", function.qualified, len(parameters), function.params)
	}
	for index, parameter := range parameters {
		if function.params[index].typ.name != parameter {
			t.Fatalf("%s parameter %d must be %s, found %s", function.qualified, index+1, parameter, function.params[index].typ.name)
		}
	}
	if function.result.name != result {
		t.Fatalf("%s must return %s, found %s", function.qualified, result, function.result.name)
	}
}

func requireNoDiagnostics(t *testing.T, diagnostics []Diagnostic) {
	t.Helper()
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
}

func requireDiagnostic(t *testing.T, diagnostics []Diagnostic, code, message string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, message) {
			return
		}
	}
	t.Fatalf("missing %s containing %q in %+v", code, message, diagnostics)
}
