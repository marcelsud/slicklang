package compiler

import (
	"strings"
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

func TestStdMathGeneratedSourceChecksBeforeHostDivision(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> string {
    match std.math.Divide(1, 0) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Kind
    }
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)
	source, err := program.generateGo()
	if err != nil {
		t.Fatalf("generateGo: %v", err)
	}
	for _, needle := range []string{
		"math.MinInt64",
		"DivisionByZero",
		"Overflow",
		"integer division overflow",
		"division by zero",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("generated Go missing %q:\n%s", needle, source)
		}
	}
	// Host division must only appear after the zero and overflow guards inside
	// the generated std.math.Divide native body.
	divideFunc := extractGeneratedNativeByMessage(t, source, `"Divide"`, "integer division overflow")
	zeroIdx := strings.Index(divideFunc, "== 0")
	overflowIdx := strings.Index(divideFunc, "math.MinInt64")
	hostIdx := strings.LastIndex(divideFunc, "/")
	if zeroIdx < 0 || overflowIdx < 0 || hostIdx < 0 {
		t.Fatalf("expected zero, overflow, and host division in generated Divide:\n%s", divideFunc)
	}
	if !(zeroIdx < hostIdx && overflowIdx < hostIdx) {
		t.Fatalf("host division is not guarded:\n%s", divideFunc)
	}

	remainderProgram, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> string {
    match std.math.Remainder(1, 0) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Kind
    }
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)
	remainderSource, err := remainderProgram.generateGo()
	if err != nil {
		t.Fatalf("generateGo remainder: %v", err)
	}
	remainderFunc := extractGeneratedNativeByMessage(t, remainderSource, `"Remainder"`, "math.MinInt64")
	if !strings.Contains(remainderFunc, "%") {
		t.Fatalf("generated Remainder missing host rem:\n%s", remainderFunc)
	}
	remHost := strings.LastIndex(remainderFunc, "%")
	remGuard := strings.Index(remainderFunc, "math.MinInt64")
	if remGuard < 0 || remHost < 0 || remGuard > remHost {
		t.Fatalf("host remainder is not guarded by min-int case:\n%s", remainderFunc)
	}
}

// extractGeneratedNativeByMessage returns the func body that contains both markers.
func extractGeneratedNativeByMessage(t *testing.T, source, markerA, markerB string) string {
	t.Helper()
	offset := 0
	for {
		idx := strings.Index(source[offset:], markerA)
		if idx < 0 {
			t.Fatalf("generated source missing marker %q", markerA)
		}
		idx += offset
		start := strings.LastIndex(source[:idx], "func ")
		if start < 0 {
			t.Fatalf("could not find func start near %q", markerA)
		}
		depth := 0
		end := -1
		for i := start; i < len(source); i++ {
			switch source[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			t.Fatalf("unclosed function near %q", markerA)
		}
		body := source[start:end]
		if strings.Contains(body, markerB) {
			return body
		}
		offset = end
	}
}
