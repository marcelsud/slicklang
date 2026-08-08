package compiler_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStdPathExactAliasAndSignatures(t *testing.T) {
	source := `
use std.path.Join as JoinPath

function JoinParts(Parts: string[]) -> string { JoinPath(Parts) }
function CleanPath(Path: string) -> string { std.path.Clean(Path) }
function BaseName(Path: string) -> string { std.path.Base(Path) }
function DirectoryName(Path: string) -> string { std.path.Directory(Path) }
function PathExtension(Path: string) -> string? { std.path.Extension(Path) }
function PathIsAbsolute(Path: string) -> bool { std.path.IsAbsolute(Path) }

function main() -> string { JoinParts(["alpha", "beta"]) }
`
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != filepath.Join("alpha", "beta") {
		t.Fatalf("JoinPath produced %q", output)
	}
}

func TestStdPathCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		argument   string
		wantType   string
	}{
		{name: "Join", resultType: "string", argument: `"part"`, wantType: "string[]"},
		{name: "Clean", resultType: "string", argument: "1", wantType: "string"},
		{name: "Base", resultType: "string", argument: "1", wantType: "string"},
		{name: "Directory", resultType: "string", argument: "1", wantType: "string"},
		{name: "Extension", resultType: "string?", argument: "1", wantType: "string"},
		{name: "IsAbsolute", resultType: "bool", argument: "1", wantType: "string"},
	}
	for _, test := range tests {
		t.Run(test.name+" argument type", func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { std.path." + test.name + "(" + test.argument + ") }"
			message := "argument 1 to std.path." + test.name + " must be " + test.wantType
			assertDiagnostic(t, checkResult(t, source), "SLK320", message)
		})
		t.Run(test.name+" arity", func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { std.path." + test.name + "() }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", "std.path."+test.name+" expects 1 arguments, found 0")
		})
	}

	assertDiagnostic(t, checkResult(t, `
function Need(Value: string) -> string { Value }
function main() -> string { Need(std.path.Extension("name.txt")) }
`), "SLK372", "string? may be null")
}

func TestStdPathHostSemanticsEverywhere(t *testing.T) {
	source := `

function ExtensionOr(Path: string) -> string {
    let Value = std.path.Extension(Path)
    if (Value == null) {
        "<none>"
    } else {
        Value
    }
}

function main() -> string {
    let Zero = std.path.Join([])
    let One = std.path.Join(["leaf"])
    let Many = std.path.Join(["alpha", "beta", "..", "gamma"])
    let Empty = std.path.Clean("")
    let Current = std.path.Clean(".")
    let Parent = std.path.Clean("alpha/..")
    let Repeated = std.path.Clean("alpha//beta")
    let Leaf = std.path.Join(["alpha", "beta.txt"])
    let Base = std.path.Base(Leaf)
    let Directory = std.path.Directory(Leaf)
    let Extension = ExtensionOr("archive.tar.gz")
    let Missing = ExtensionOr("archive")
    let Dot = ExtensionOr("name.")
    let RootAbsolute = std.path.IsAbsolute("/")
    let RelativeAbsolute = std.path.IsAbsolute("relative")
` + "    `${Zero}|${One}|${Many}|${Empty}|${Current}|${Parent}|${Repeated}|${Base}|${Directory}|${Extension}|${Missing}|${Dot}|${RootAbsolute}|${RelativeAbsolute}`\n" + `}
`
	leaf := filepath.Join("alpha", "beta.txt")
	expected := strings.Join([]string{
		filepath.Join(),
		filepath.Join("leaf"),
		filepath.Join("alpha", "beta", "..", "gamma"),
		filepath.Clean(""),
		filepath.Clean("."),
		filepath.Clean("alpha/.."),
		filepath.Clean("alpha//beta"),
		filepath.Base(leaf),
		filepath.Dir(leaf),
		filepath.Ext("archive.tar.gz"),
		"<none>",
		filepath.Ext("name."),
		strconv.FormatBool(filepath.IsAbs("/")),
		strconv.FormatBool(filepath.IsAbs("relative")),
	}, "|")
	if output := runResultEverywhere(t, source); output != expected {
		t.Fatalf("std.path output %q, want %q", output, expected)
	}
}
