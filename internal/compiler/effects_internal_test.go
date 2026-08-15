package compiler

import (
	"slices"
	"strings"
	"testing"
)

func compileEffectsTest(t *testing.T, text string) (*program, []Diagnostic) {
	t.Helper()
	return compile([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
}

func requireEffectDiagnostic(t *testing.T, diagnostics []Diagnostic, code diagnosticCode, message string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == string(code) && strings.Contains(diagnostic.Message, message) {
			return
		}
	}
	t.Fatalf("missing %s containing %q in %+v", code, message, diagnostics)
}

func TestEffectsRequireExplicitDirectAndTransitiveContracts(t *testing.T) {
	_, diagnostics := compileEffectsTest(t, `
function Read(Name: string) -> string? {
    std.env.Get(Name)
}

function Declared(Name: string) -> string? effects { environment } {
    std.env.Get(Name)
}

function Wrapper(Name: string) -> string? {
    Declared(Name)
}
`)
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeUndeclaredEffect, "root.Read uses effect environment through std.env.Get")
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeUndeclaredEffect, "root.Wrapper uses effect environment through Declared")
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestEffectsFlowThroughLambdasAndCallableValues(t *testing.T) {
	_, diagnostics := compileEffectsTest(t, `
function Apply(Action: (string) -> string? effects { environment }, Name: string) -> string? effects { environment } {
    Action(Name)
}

function main() -> string? effects { environment } {
    let Read = (Name: string) -> string? effects { environment } {
        std.env.Get(Name)
    }
    Apply(Read, "SLICK_EFFECT_TEST")
}
`)
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
}

func TestEffectsRejectUnknownDuplicatesAndBroaderImplementations(t *testing.T) {
	_, diagnostics := compileEffectsTest(t, `
class Source {
    function Read() -> string?
}

function Source.Read() -> string? effects { environment } {
    std.env.Get("SLICK_EFFECT_TEST")
}

function Invalid() -> null effects { missing, state, state } {
    null
}
`)
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeEffectDeclaration, "unknown effect missing")
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeEffectDeclaration, "duplicate effect state")
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeMethodSignature, "undeclared operation effect environment")
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestEffectFormattingAndDescriptionAreCanonical(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
function Run(Action: () -> null effects { state, environment }) -> null effects { state, environment } {
    Action()
}
`}
	formatted, diagnostics, err := Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format: diagnostics=%+v err=%v", diagnostics, err)
	}
	wantClause := "effects { environment, state }"
	if strings.Count(formatted, wantClause) != 2 {
		t.Fatalf("formatted effects are not canonical:\n%s", formatted)
	}
	program, diagnostics := compile([]Source{{Name: source.Name, Namespace: source.Namespace, Text: formatted}})
	if len(diagnostics) != 0 {
		t.Fatalf("compile formatted source: %+v", diagnostics)
	}
	description, ok := program.describeSymbol("root.Run")
	if !ok {
		t.Fatal("root.Run is not describable")
	}
	if !slices.Equal(description.Effects, []string{"environment", "state"}) {
		t.Fatalf("declaration effects = %v", description.Effects)
	}
	if description.Parameters[0].Callable == nil || !slices.Equal(description.Parameters[0].Callable.Effects, []string{"environment", "state"}) {
		t.Fatalf("callable effects = %+v", description.Parameters[0].Callable)
	}
}

func TestStandardLibraryEffectRegistryMatchesAuthoritySeams(t *testing.T) {
	program, diagnostics := compile(nil)
	if len(diagnostics) != 0 {
		t.Fatalf("compile standard library: %+v", diagnostics)
	}
	want := map[string][]string{
		"std.buffer.Push":                 {effectState},
		"std.env.Get":                     {effectEnvironment},
		"std.fs.ReadText":                 {effectFilesystem},
		"std.http.Fetch":                  {effectNetwork},
		"std.io.ReadAll":                  {effectIO},
		"std.process.Run":                 {effectProcess},
		"std.sqlite.Open":                 {effectDatabase},
		"std.sqlite.Database.Execute":     {effectDatabase},
		"std.fs.TemporaryDirectory.Close": {effectFilesystem},
		"std.io.BytesWriter.Write":        {effectIO},
	}
	for name, effects := range want {
		description, ok := program.describeSymbol(name)
		if !ok {
			t.Fatalf("%s is not describable", name)
		}
		if !slices.Equal(description.Effects, effects) {
			t.Errorf("%s effects = %v, want %v", name, description.Effects, effects)
		}
	}
	pure, ok := program.describeSymbol("std.json.Encode")
	if !ok || len(pure.Effects) != 0 {
		t.Fatalf("std.json.Encode effects = %v", pure.Effects)
	}
}

func TestEffectsUseSubsetSubtypingAndCannotBeErased(t *testing.T) {
	_, diagnostics := compileEffectsTest(t, `
interface Reader {
    function Read() -> string? effects { environment, state }
}

class EnvironmentReader implements Reader {
    function Read() -> string? effects { environment } {
        std.env.Get("SLICK_EFFECT_TEST")
    }
}

function ReadWith(Source: Reader) -> string? effects { environment, state } {
    Source.Read()
}

function main() -> string? effects { environment, state } {
    ReadWith(EnvironmentReader {})
}
`)
	if len(diagnostics) != 0 {
		t.Fatalf("narrower implementation rejected: %+v", diagnostics)
	}

	_, diagnostics = compileEffectsTest(t, `
function Read() -> string? effects { environment } {
    std.env.Get("SLICK_EFFECT_TEST")
}

function Invoke(Action: () -> string?) -> string? {
    Action()
}

function main() -> string? effects { environment } {
    Invoke(Read)
}
`)
	requireEffectDiagnostic(t, diagnostics, diagnosticCodeCallArgument, "argument 1 to Invoke must be () -> string?, found () -> string? effects { environment }")
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}
