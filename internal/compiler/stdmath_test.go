package compiler_test

import (
	"strings"
	"testing"
)

func TestStdMathExactAliasesAndResultPropagation(t *testing.T) {
	source := `
use std.math.Divide as Divide
use std.math.Remainder as Remainder
use std.math.ArithmeticFailure as ArithmeticFailure

function Quotient(Dividend: int, Divisor: int) -> Result<int, ArithmeticFailure> {
    Divide(Dividend, Divisor)
}

function Through(Dividend: int, Divisor: int) -> Result<string, ArithmeticFailure> {
    let Value = Divide(Dividend, Divisor)?
    Ok(std.convert.IntToString(Value))
}

function main() -> string {
    let OkPath = match Through(20, 3) {
        Ok(Value) => Value
        Err(Failure) => Failure.Kind
    }
    let Rem = match Remainder(-20, 3) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Kind
    }
    let Propagated = match Quotient(1, 0) {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Kind
    }
    ` + "`" + `${OkPath}|${Rem}|${Propagated}` + "`" + `
}
`
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != "6|-2|Divide:DivisionByZero" {
		t.Fatalf("std.math aliases produced %q", output)
	}
}

func TestStdMathSignTableAndIdentityEverywhere(t *testing.T) {
	source := `
function Format(Value: int) -> string {
    std.convert.IntToString(Value)
}

function DescribeDivide(Dividend: int, Divisor: int) -> string {
    match std.math.Divide(Dividend, Divisor) {
        Ok(Value) => Format(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind
    }
}

function DescribeRemainder(Dividend: int, Divisor: int) -> string {
    match std.math.Remainder(Dividend, Divisor) {
        Ok(Value) => Format(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind
    }
}

function Identity(Dividend: int, Divisor: int) -> string {
    match std.math.Divide(Dividend, Divisor) {
        Ok(Quotient) => match std.math.Remainder(Dividend, Divisor) {
            Ok(Rem) => if (Dividend == Quotient * Divisor + Rem) {
                "ok"
            } else {
                "bad"
            }
            Err(Failure) => Failure.Kind
        }
        Err(Failure) => Failure.Kind
    }
}

function main() -> string {
    let Cases = [
        DescribeDivide(20, 3),
        DescribeRemainder(20, 3),
        DescribeDivide(-20, 3),
        DescribeRemainder(-20, 3),
        DescribeDivide(20, -3),
        DescribeRemainder(20, -3),
        DescribeDivide(-20, -3),
        DescribeRemainder(-20, -3),
        DescribeDivide(0, 5),
        DescribeRemainder(0, -5),
        DescribeDivide(7, 0),
        DescribeRemainder(-7, 0),
        Identity(20, 3),
        Identity(-20, 3),
        Identity(20, -3),
        Identity(-20, -3),
        Identity(0, 5),
        Identity(1, 1),
        Identity(-1, 1)
    ]
    let Output = ""
    for Item in Cases {
        if (Output == "") {
            Output = Item
        } else {
            Output = Output + "|" + Item
        }
    }
    Output
}
`
	want := strings.Join([]string{
		"6", "2",
		"-6", "-2",
		"-6", "2",
		"6", "-2",
		"0", "0",
		"Divide:DivisionByZero",
		"Remainder:DivisionByZero",
		"ok", "ok", "ok", "ok", "ok", "ok", "ok",
	}, "|")
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("std.math sign/identity output %q, want %q", output, want)
	}
}

func TestStdMathBoundariesAndOverflowEverywhere(t *testing.T) {
	source := `
function MinInt() -> int {
    match std.convert.ParseInt("-9223372036854775808") {
        Ok(Value) => Value
        Err(_) => 0
    }
}

function MaxInt() -> int {
    9223372036854775807
}

function DescribeDivide(Dividend: int, Divisor: int) -> string {
    match std.math.Divide(Dividend, Divisor) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind + ":" + Failure.Message
    }
}

function DescribeRemainder(Dividend: int, Divisor: int) -> string {
    match std.math.Remainder(Dividend, Divisor) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind + ":" + Failure.Message
    }
}

function main() -> string {
    let Low = MinInt()
    let High = MaxInt()
    let Parts = [
        DescribeDivide(High, 1),
        DescribeDivide(Low, 1),
        DescribeDivide(High, -1),
        DescribeDivide(Low, -1),
        DescribeRemainder(Low, -1),
        DescribeRemainder(High, -1),
        DescribeDivide(Low, 2),
        DescribeRemainder(Low, 2),
        DescribeDivide(1, 0),
        DescribeRemainder(0, 0)
    ]
    let Output = ""
    for Item in Parts {
        if (Output == "") {
            Output = Item
        } else {
            Output = Output + "|" + Item
        }
    }
    Output
}
`
	want := strings.Join([]string{
		"9223372036854775807",
		"-9223372036854775808",
		"-9223372036854775807",
		"Divide:Overflow:integer division overflow",
		"0",
		"0",
		"-4611686018427387904",
		"0",
		"Divide:DivisionByZero:division by zero",
		"Remainder:DivisionByZero:division by zero",
	}, "|")
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("std.math boundary output %q, want %q", output, want)
	}
}

func TestStdMathErrIsNotCaughtAsThrow(t *testing.T) {
	source := `
function Recovery() -> Result<int, std.math.ArithmeticFailure> { Ok(0) }

function main() -> string {
    match (std.math.Divide(1, 0) catch (error) {
        std.math.ArithmeticFailure => Recovery()
    }) {
        Ok(_) => "caught"
        Err(Failure) => Failure.Kind
    }
}
`
	if output := runResultEverywhere(t, source); output != "DivisionByZero" {
		t.Fatalf("Divide Err was caught as a throw: %q", output)
	}
}

func TestStdMathCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{
			name:       "Divide arity",
			resultType: "Result<int,std.math.ArithmeticFailure>",
			call:       "std.math.Divide(1)",
			message:    "std.math.Divide expects 2 arguments, found 1",
		},
		{
			name:       "Divide type",
			resultType: "Result<int,std.math.ArithmeticFailure>",
			call:       `std.math.Divide(1, "2")`,
			message:    "argument 2 to std.math.Divide must be int, found string",
		},
		{
			name:       "Remainder arity",
			resultType: "Result<int,std.math.ArithmeticFailure>",
			call:       "std.math.Remainder()",
			message:    "std.math.Remainder expects 2 arguments, found 0",
		},
		{
			name:       "Remainder type",
			resultType: "Result<int,std.math.ArithmeticFailure>",
			call:       "std.math.Remainder(1.0, 2)",
			message:    "argument 1 to std.math.Remainder must be int, found float",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
