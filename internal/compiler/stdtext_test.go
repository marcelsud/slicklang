package compiler_test

import (
	"strings"
	"testing"
)

func TestStdTextExactAliasesAndSignatures(t *testing.T) {
	source := `
use std.text.Trim as Trim
use std.text.Contains as Contains
use std.text.StartsWith as StartsWith
use std.text.EndsWith as EndsWith
use std.text.Split as Split
use std.text.Join as Join
use std.text.ReplaceAll as ReplaceAll
use std.text.Cut as Cut

function main() -> string {
    let Trimmed = Trim(" value ")
    let Contained = Contains(Trimmed, "alu")
    let Started = StartsWith(Trimmed, "val")
    let Ended = EndsWith(Trimmed, "lue")
    let Parts = Split(Trimmed, "l")
    let Joined = Join(Parts, "l")
    let Replaced = ReplaceAll(Joined, "value", "done")
    let CutValue = Cut(Replaced, "o")
    ` + "`" + `${Contained}|${Started}|${Ended}|${Replaced}|${CutValue}` + "`" + `
}
`
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != "true|true|true|done|(d, ne)" {
		t.Fatalf("std.text aliases produced %q", output)
	}
}

func TestStdTextGoSemanticsEverywhere(t *testing.T) {
	source := `
function main() -> string {
    let Trimmed = std.text.Trim("\u00a0\u2003 value \u2003\u00a0")
    let Contains = std.text.Contains("Slick", "lick")
    let CaseSensitive = std.text.Contains("Slick", "slick")
    let Starts = std.text.StartsWith("Slick", "Sli")
    let Ends = std.text.EndsWith("Slick", "ick")
    let MissingSplit = std.text.Split("abc", "|")
    let EdgeSplit = std.text.Split("|a||", "|")
    let EmptySplit = std.text.Split("Go😊", "")
    let EmptyJoin = std.text.Join([], ",")
    let Join = std.text.Join(["a", "", "b"], "|")
    let MissingReplace = std.text.ReplaceAll("abc", "z", "x")
    let EmptyReplace = std.text.ReplaceAll("ab", "", "-")
    let FirstCut = std.text.Cut("a=b=c", "=")
    let MissingCut = std.text.Cut("abc", "=")
    let EmptyCut = std.text.Cut("=", "=")
    ` + "`" + `${Trimmed}|${Contains}|${CaseSensitive}|${Starts}|${Ends}|${MissingSplit}|${EdgeSplit}|${EmptySplit}|${EmptyJoin}|${Join}|${MissingReplace}|${EmptyReplace}|${FirstCut}|${MissingCut}|${EmptyCut}` + "`" + `
}
`
	want := strings.Join([]string{
		"value", "true", "false", "true", "true",
		"[abc]", "[, a, , ]", "[G, o, 😊]", "", "a||b",
		"abc", "-a-b-", "(a, b=c)", "", "(, )",
	}, "|")
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("std.text output %q, want %q", output, want)
	}
}

func TestStdTextCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{name: "Trim type", resultType: "string", call: "std.text.Trim(1)", message: "argument 1 to std.text.Trim must be string, found int"},
		{name: "Contains arity", resultType: "bool", call: "std.text.Contains(\"text\")", message: "std.text.Contains expects 2 arguments, found 1"},
		{name: "Join parts type", resultType: "string", call: "std.text.Join(\"text\", \",\")", message: "argument 1 to std.text.Join must be string[], found string"},
		{name: "ReplaceAll arity", resultType: "string", call: "std.text.ReplaceAll(\"text\", \"old\")", message: "std.text.ReplaceAll expects 3 arguments, found 2"},
		{name: "Cut separator type", resultType: "(string,string)?", call: "std.text.Cut(\"text\", 1)", message: "argument 2 to std.text.Cut must be string, found int"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
