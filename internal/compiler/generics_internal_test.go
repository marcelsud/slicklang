package compiler

import (
	"go/format"
	"strings"
	"testing"
)

const genericInternalSource = `
class Box<T> {
    Value: T

    function Get() -> T {
        self.Value
    }
}

function Identity<T>(Value: T) -> T {
    Value
}

function main() -> string {
    let Number = Box<int> { Value: 1 }
    let Word = Box<string> { Value: "one" }
    let Same = Identity<int>(Number.Get())
    let Text = Word.Get()
    ` + "`${Same};${Text}`" + `
}
`

func compileGenericProgram(t *testing.T, text string) *program {
	t.Helper()
	program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
	if len(diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
	return program
}

// TestGenericGeneratedSourceIsDeterministic pins that one program generates one
// byte-identical Go source, so the names an instantiation produces never depend
// on map iteration order.
func TestGenericGeneratedSourceIsDeterministic(t *testing.T) {
	first, err := compileGenericProgram(t, genericInternalSource).generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	for range 3 {
		next, err := compileGenericProgram(t, genericInternalSource).generateGo()
		if err != nil {
			t.Fatalf("generate Go again: %v", err)
		}
		if next != first {
			t.Fatal("generated Go source is not deterministic across compilations")
		}
	}
	if _, err := format.Source([]byte(first)); err != nil {
		t.Fatalf("generated Go is not formattable: %v", err)
	}
}

// TestUnusedGenericEmitsNoSupport pins that a declaration nobody instantiates
// costs nothing in the generated program, and that each instantiation is
// emitted exactly once however many times a program names it.
func TestUnusedGenericEmitsNoSupport(t *testing.T) {
	generated, err := compileGenericProgram(t, `
class Used<T> {
    Value: T
}

class Unused<T> {
    Value: T
}

function main() -> int {
    let First = Used<int> { Value: 1 }
    let Second = Used<int> { Value: 2 }
    First.Value + Second.Value
}
`).generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	if strings.Contains(generated, goClassName("root.Unused<int>")) || strings.Contains(generated, goClassName("root.Unused")) {
		t.Fatal("generated Go carries support for an uninstantiated generic")
	}
	declaration := "type " + goClassName("root.Used<int>") + " struct"
	if count := strings.Count(generated, declaration); count != 1 {
		t.Fatalf("expected one declaration of Used<int>, found %d", count)
	}
}
