package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// rustStdConvertMathProgram exercises every std.convert and std.math operation
// the Rust backend owns, including the failure paths: ParseInt syntax and
// out-of-range errors, ParseFloat syntax, non-finite, and overflow errors, and
// std.math division/remainder by zero and the minimum-int / -1 overflow and
// remainder cases. It uses fully-qualified standard-library calls and string
// concatenation (no interpolation) so the program needs no use imports and the
// Go raw string below never contains a backtick.
const rustStdConvertMathProgram = `function DescribeInt(Text: string) -> string {
    match std.convert.ParseInt(Text) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => "err:" + Failure.Target + ":" + Failure.Message
    }
}

function DescribeFloat(Text: string) -> string {
    match std.convert.ParseFloat(Text) {
        Ok(Value) => std.convert.FloatToString(Value)
        Err(Failure) => "err:" + Failure.Target + ":" + Failure.Message
    }
}

function DescribeDiv(Dividend: int, Divisor: int) -> string {
    match std.math.Divide(Dividend, Divisor) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind + ":" + Failure.Message
    }
}

function DescribeRem(Dividend: int, Divisor: int) -> string {
    match std.math.Remainder(Dividend, Divisor) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => Failure.Operation + ":" + Failure.Kind + ":" + Failure.Message
    }
}

function MinInt() -> int {
    match std.convert.ParseInt("-9223372036854775808") {
        Ok(Value) => Value
        Err(_) => 0
    }
}

function main() -> string {
    let Low = MinInt()
    let High = 9223372036854775807
    let Parts = [
        DescribeInt("-9223372036854775808"),
        DescribeInt("+9223372036854775807"),
        DescribeInt("000"),
        DescribeInt("9223372036854775808"),
        DescribeInt("1.0"),
        DescribeInt("1e2"),
        DescribeInt("0x10"),
        DescribeInt(" 1"),
        DescribeInt("42"),
        DescribeInt("-42"),
        DescribeInt(""),
        DescribeInt("-9223372036854775809"),
        DescribeFloat("1.25"),
        DescribeFloat("-2.5"),
        DescribeFloat("0"),
        DescribeFloat("1e3"),
        DescribeFloat("1 "),
        DescribeFloat("NaN"),
        DescribeFloat("Infinity"),
        DescribeFloat("1e309"),
        DescribeFloat("1.2345678901234567"),
        std.convert.FloatToString(1000000.0),
        std.convert.FloatToString(0.00001),
        std.convert.FloatToString(123456.0),
        std.convert.FloatToString(-0.0),
        DescribeDiv(20, 3),
        DescribeDiv(-20, 3),
        DescribeDiv(20, -3),
        DescribeDiv(-20, -3),
        DescribeDiv(0, 5),
        DescribeDiv(7, 1),
        DescribeDiv(1, 0),
        DescribeDiv(-1, 0),
        DescribeDiv(High, 1),
        DescribeDiv(Low, 1),
        DescribeDiv(High, -1),
        DescribeDiv(Low, -1),
        DescribeDiv(Low, 2),
        DescribeRem(20, 3),
        DescribeRem(-20, 3),
        DescribeRem(20, -3),
        DescribeRem(-20, -3),
        DescribeRem(0, 5),
        DescribeRem(1, 0),
        DescribeRem(Low, -1),
        DescribeRem(High, -1),
        DescribeRem(Low, 2)
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

func TestRustStdConvertMathMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdConvertMathProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	want := strings.Join([]string{
		// std.convert.IntToString success and std.convert.ParseInt success/failure.
		"-9223372036854775808",
		"9223372036854775807",
		"0",
		"err:int:integer out of range",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"42",
		"-42",
		"err:int:invalid base-10 integer",
		"err:int:integer out of range",
		// std.convert.FloatToString success and std.convert.ParseFloat success/failure.
		"1.25",
		"-2.5",
		"0",
		"1000",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:floating-point value out of range",
		"1.2345678901234567",
		"1e+06",
		"1e-05",
		"123456",
		"-0",
		// std.math.Divide success and failure (zero divisor and min / -1 overflow).
		"6",
		"-6",
		"-6",
		"6",
		"0",
		"7",
		"Divide:DivisionByZero:division by zero",
		"Divide:DivisionByZero:division by zero",
		"9223372036854775807",
		"-9223372036854775808",
		"-9223372036854775807",
		"Divide:Overflow:integer division overflow",
		"-4611686018427387904",
		// std.math.Remainder success and failure (zero divisor; min % -1 yields 0).
		"2",
		"-2",
		"2",
		"-2",
		"0",
		"Remainder:DivisionByZero:division by zero",
		"0",
		"0",
		"0",
	}, "|")
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Rust binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust output = %q, want interpreter output %q", output, interpreted+"\n")
	}
}
