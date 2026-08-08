package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestStdConvertExactAliasesAndResultPropagation(t *testing.T) {
	source := `
use std.convert.ParseInt as ParseInt
use std.convert.ParseFloat as ParseFloat
use std.convert.IntToString as IntToString
use std.convert.FloatToString as FloatToString
use std.convert.Failure as ConvertFailure

function Through(Text: string) -> Result<string, ConvertFailure> {
    let Value = ParseInt(Text)?
    Ok(IntToString(Value))
}

function main() -> string {
    let Integer = match Through("+42") {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
    let Float = match ParseFloat("1.5") {
        Ok(Value) => FloatToString(Value)
        Err(Failure) => Failure.Message
    }
    ` + "`" + `${Integer}|${Float}` + "`" + `
}
`
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != "42|1.5" {
		t.Fatalf("std.convert aliases produced %q", output)
	}
}

func TestStdConvertStrictParsingEverywhere(t *testing.T) {
	source := `
function DescribeInt(Text: string) -> string {
    match std.convert.ParseInt(Text) {
        Ok(Value) => std.convert.IntToString(Value)
        Err(Failure) => ` + "`" + `err:${Failure.Target}:${Failure.Message}` + "`" + `
    }
}

function DescribeFloat(Text: string) -> string {
    match std.convert.ParseFloat(Text) {
        Ok(Value) => std.convert.FloatToString(Value)
        Err(Failure) => ` + "`" + `err:${Failure.Target}:${Failure.Message}` + "`" + `
    }
}

function main() -> string {
    let IntMin = DescribeInt("-9223372036854775808")
    let IntMax = DescribeInt("+9223372036854775807")
    let IntZero = DescribeInt("000")
    let IntOverflow = DescribeInt("9223372036854775808")
    let IntDecimal = DescribeInt("1.0")
    let IntExponent = DescribeInt("1e2")
    let IntPrefix = DescribeInt("0x10")
    let IntSpace = DescribeInt(" 1")
    let FloatPositive = DescribeFloat("1.25")
    let FloatNegative = DescribeFloat("-2.5")
    let FloatZero = DescribeFloat("0")
    let FloatExponent = DescribeFloat("1e3")
    let FloatSpace = DescribeFloat("1 ")
    let FloatNaN = DescribeFloat("NaN")
    let FloatInfinity = DescribeFloat("Infinity")
    let FloatOverflow = DescribeFloat("1e309")
    let Formatted = std.convert.FloatToString(1.2345678901234567)
    let RoundTrip = DescribeFloat(Formatted)
    ` + "`" + `${IntMin}|${IntMax}|${IntZero}|${IntOverflow}|${IntDecimal}|${IntExponent}|${IntPrefix}|${IntSpace}|${FloatPositive}|${FloatNegative}|${FloatZero}|${FloatExponent}|${FloatSpace}|${FloatNaN}|${FloatInfinity}|${FloatOverflow}|${Formatted}|${RoundTrip}` + "`" + `
}
`
	want := strings.Join([]string{
		"-9223372036854775808",
		"9223372036854775807",
		"0",
		"err:int:integer out of range",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"err:int:invalid base-10 integer",
		"1.25", "-2.5", "0", "1000",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:floating-point value out of range",
		"1.2345678901234567", "1.2345678901234567",
	}, "|")
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("std.convert output %q, want %q", output, want)
	}
}

func TestStdConvertErrIsNotCaughtAsThrow(t *testing.T) {
	source := `
function Recovery() -> Result<int, std.convert.Failure> { Ok(0) }

function main() -> string {
    match (std.convert.ParseInt("bad") catch (error) {
        std.convert.Failure => Recovery()
    }) {
        Ok(_) => "caught"
        Err(Failure) => Failure.Target
    }
}
`
	if output := runResultEverywhere(t, source); output != "int" {
		t.Fatalf("parse Err was caught as a throw: %q", output)
	}
}

func TestStdConvertRejectsNonFiniteFormattingWithoutPanic(t *testing.T) {
	source := `function main() -> string { std.convert.FloatToString(1e308 + 1e308) }`
	_, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	assertNoDiagnostics(t, diagnostics)
	if err == nil || !strings.Contains(err.Error(), "cannot format non-finite float") {
		t.Fatalf("interpreted non-finite FloatToString error = %v", err)
	}

	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err = compiler.BuildPath(path, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot format non-finite float") {
		t.Fatalf("native non-finite FloatToString: err=%v output=%q", err, output)
	}
}

func TestStdConvertCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{name: "ParseInt type", resultType: "Result<int,std.convert.Failure>", call: "std.convert.ParseInt(1)", message: "argument 1 to std.convert.ParseInt must be string, found int"},
		{name: "ParseFloat arity", resultType: "Result<float,std.convert.Failure>", call: "std.convert.ParseFloat()", message: "std.convert.ParseFloat expects 1 arguments, found 0"},
		{name: "IntToString type", resultType: "string", call: "std.convert.IntToString(1.0)", message: "argument 1 to std.convert.IntToString must be int, found float"},
		{name: "FloatToString arity", resultType: "string", call: "std.convert.FloatToString(1.0, 2.0)", message: "std.convert.FloatToString expects 1 arguments, found 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
