package compiler

import (
	"testing"
)

func TestStdMathSyntheticDeclarations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root.app",
		Text: `
use std.math.Divide as Divide
use std.math.Remainder as Remainder
use std.math.ArithmeticFailure as ArithmeticFailure

function Quotient(Dividend: int, Divisor: int) -> Result<int, ArithmeticFailure> {
    Divide(Dividend, Divisor)
}

function Mod(Dividend: int, Divisor: int) -> Result<int, ArithmeticFailure> {
    Remainder(Dividend, Divisor)
}

function Raise() -> null throws ArithmeticFailure {
    throw ArithmeticFailure {
        Operation: "Divide"
        Kind: "DivisionByZero"
        Message: "division by zero"
    }
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	divide := program.functions["std.math.Divide"]
	assertStandardFunction(t, divide, "std.math", "Divide", []string{"int", "int"}, "Result<int,std.math.ArithmeticFailure>", nativeStdMathDivide)
	remainder := program.functions["std.math.Remainder"]
	assertStandardFunction(t, remainder, "std.math", "Remainder", []string{"int", "int"}, "Result<int,std.math.ArithmeticFailure>", nativeStdMathRemainder)

	failure := program.classes[stdMathArithmeticFailureName]
	if failure == nil {
		t.Fatal("std.math.ArithmeticFailure was not registered")
	}
	if failure.namespace != "std.math" || failure.name != "ArithmeticFailure" || !failure.isError {
		t.Fatalf("unexpected ArithmeticFailure declaration: %+v", failure)
	}
	if len(failure.fields) != 3 {
		t.Fatalf("ArithmeticFailure must expose exactly three fields, found %+v", failure.fields)
	}
	for _, name := range []string{"Operation", "Kind", "Message"} {
		field, ok := failure.fields[name]
		if !ok || field.typ.name != "string" {
			t.Fatalf("ArithmeticFailure.%s must be string, found %+v", name, field)
		}
	}

	quotient := program.functions["root.app.Quotient"]
	if quotient == nil {
		t.Fatal("test Quotient function was not registered")
	}
	if resolved := program.resolveFunction(quotient, "Divide"); resolved != divide {
		t.Fatalf("Divide resolved to %+v, want std.math.Divide", resolved)
	}
	if resolved := program.resolveType("root.app", quotient.aliases, typeRef{name: "Result<int,ArithmeticFailure>"}); resolved != "Result<int,std.math.ArithmeticFailure>" {
		t.Fatalf("ArithmeticFailure in Result resolved to %q", resolved)
	}
	if resolved, ok := program.resolveErrorIn("root.app", quotient.aliases, "ArithmeticFailure"); !ok || resolved != stdMathArithmeticFailureName {
		t.Fatalf("ArithmeticFailure error resolved to %q, %t", resolved, ok)
	}
}
