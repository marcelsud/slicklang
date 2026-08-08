package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStdFSExactAliasesAndSignatures(t *testing.T) {
	source := `
use std.fs.ReadText as ReadText
use std.fs.WriteText as WriteText
use std.fs.Exists as Exists
use std.fs.CreateDirectoryAll as CreateDirectoryAll
use std.fs.Remove as Remove
use std.fs.Failure as FSFailure

function Read(Path: string) -> Result<string, FSFailure> { ReadText(Path) }
function Write(Path: string, Contents: string) -> Result<null, FSFailure> { WriteText(Path, Contents) }
function Inspect(Path: string) -> Result<bool, FSFailure> { Exists(Path) }
function Create(Path: string) -> Result<null, FSFailure> { CreateDirectoryAll(Path) }
function Delete(Path: string) -> Result<null, FSFailure> { Remove(Path) }

function main() -> string {
    match Inspect(".") {
        Ok(Value) => ` + "`" + `${Value}` + "`" + `
        Err(Failure) => Failure.Message
    }
}
`
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != "true" {
		t.Fatalf("std.fs aliases produced %q", output)
	}
}

func TestStdFSWholeFileFlowEverywhere(t *testing.T) {
	base := filepath.Join(t.TempDir(), "workspace")
	parent := filepath.Join(base, "nested")
	directory := filepath.Join(parent, "deep")
	path := filepath.Join(directory, "note.txt")
	source := fmt.Sprintf(`
function Exercise() -> Result<string, std.fs.Failure> {
    let Before = std.fs.Exists(%s)?
    std.fs.CreateDirectoryAll(%s)?
    std.fs.CreateDirectoryAll(%s)?
    let During = std.fs.Exists(%s)?
    std.fs.WriteText(%s, "first\nsecond")?
    let Multiline = std.fs.ReadText(%s)?
    std.fs.WriteText(%s, "")?
    let Empty = std.fs.ReadText(%s)?
    std.fs.WriteText(%s, "short")?
    let Truncated = std.fs.ReadText(%s)?
    std.fs.Remove(%s)?
    let After = std.fs.Exists(%s)?
    std.fs.Remove(%s)?
    std.fs.Remove(%s)?
    std.fs.Remove(%s)?
    Ok(`+"`"+`${Before}|${During}|${Multiline}|${Empty}|${Truncated}|${After}`+"`"+`)
}

function main() -> string {
    match Exercise() {
        Ok(Value) => Value
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
`, strconv.Quote(base), strconv.Quote(directory), strconv.Quote(directory), strconv.Quote(base),
		strconv.Quote(path), strconv.Quote(path), strconv.Quote(path), strconv.Quote(path),
		strconv.Quote(path), strconv.Quote(path), strconv.Quote(path), strconv.Quote(path),
		strconv.Quote(directory), strconv.Quote(parent), strconv.Quote(base))
	if output := runResultEverywhere(t, source); output != "false|true|first\nsecond||short|false" {
		t.Fatalf("std.fs flow produced %q", output)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("flow left workspace behind: %v", err)
	}
}

func TestStdFSFailuresEverywhere(t *testing.T) {
	root := t.TempDir()
	invalidUTF8 := filepath.Join(root, "invalid.txt")
	if err := os.WriteFile(invalidUTF8, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatalf("write invalid UTF-8 fixture: %v", err)
	}
	conflict := filepath.Join(root, "conflict")
	if err := os.WriteFile(conflict, []byte("file"), 0o644); err != nil {
		t.Fatalf("write directory conflict fixture: %v", err)
	}
	nonempty := filepath.Join(root, "nonempty")
	if err := os.Mkdir(nonempty, 0o755); err != nil {
		t.Fatalf("create nonempty directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "child"), []byte("child"), 0o644); err != nil {
		t.Fatalf("write nonempty child: %v", err)
	}
	missing := filepath.Join(root, "missing")
	missingChild := filepath.Join(missing, "child.txt")
	invalidPath := root + "\x00invalid"

	source := fmt.Sprintf(`
function ReadFailure(Path: string) -> string {
    match std.fs.ReadText(Path) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
function WriteFailure(Path: string) -> string {
    match std.fs.WriteText(Path, "TOP_SECRET_CONTENTS") {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
function ExistsFailure(Path: string) -> string {
    match std.fs.Exists(Path) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
function CreateFailure(Path: string) -> string {
    match std.fs.CreateDirectoryAll(Path) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
function RemoveFailure(Path: string) -> string {
    match std.fs.Remove(Path) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Path}|${Failure.Message}`+"`"+`
    }
}
function main() -> string {
    let InvalidUTF8 = ReadFailure(%s)
    let MissingRead = ReadFailure(%s)
    let Write = WriteFailure(%s)
    let Exists = ExistsFailure(%s)
    let Create = CreateFailure(%s)
    let MissingRemove = RemoveFailure(%s)
    let NonemptyRemove = RemoveFailure(%s)
    `+"`"+`${InvalidUTF8}§${MissingRead}§${Write}§${Exists}§${Create}§${MissingRemove}§${NonemptyRemove}`+"`"+`
}
`, strconv.Quote(invalidUTF8), strconv.Quote(missing), strconv.Quote(missingChild),
		strconv.Quote(invalidPath), strconv.Quote(filepath.Join(conflict, "child")),
		strconv.Quote(missing), strconv.Quote(nonempty))
	output := runResultEverywhere(t, source)
	operations := []string{"ReadText", "ReadText", "WriteText", "Exists", "CreateDirectoryAll", "Remove", "Remove"}
	lines := strings.Split(output, "§")
	if len(lines) != len(operations) {
		t.Fatalf("std.fs failures produced %d lines: %q", len(lines), output)
	}
	for index, operation := range operations {
		if !strings.HasPrefix(lines[index], operation+"|") || strings.Count(lines[index], "|") < 2 {
			t.Fatalf("failure %d = %q, want populated %s fields", index, lines[index], operation)
		}
	}
	if strings.Contains(output, "TOP_SECRET_CONTENTS") || strings.Contains(output, string([]byte{0xff, 0xfe})) {
		t.Fatalf("std.fs Failure exposed file contents: %q", output)
	}
}

func TestStdFSQuestionPropagationAndErrCatchBoundary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	source := fmt.Sprintf(`
function Through(Path: string) -> Result<string, std.fs.Failure> {
    let Contents = std.fs.ReadText(Path)?
    Ok(Contents)
}
function Recovery() -> Result<string, std.fs.Failure> { Ok("caught") }
function main() -> string {
    let Propagated = match Through(%s) {
        Ok(_) => "unexpected success"
        Err(Failure) => Failure.Operation
    }
    let NotCaught = match (std.fs.ReadText(%s) catch (error) {
        std.fs.Failure => Recovery()
    }) {
        Ok(_) => "caught"
        Err(Failure) => Failure.Operation
    }
    `+"`"+`${Propagated}|${NotCaught}`+"`"+`
}
`, strconv.Quote(missing), strconv.Quote(missing))
	if output := runResultEverywhere(t, source); output != "ReadText|ReadText" {
		t.Fatalf("std.fs Result boundary produced %q", output)
	}
}

func TestStdFSCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{name: "ReadText type", resultType: "Result<string,std.fs.Failure>", call: "std.fs.ReadText(1)", message: "argument 1 to std.fs.ReadText must be string, found int"},
		{name: "WriteText arity", resultType: "Result<null,std.fs.Failure>", call: "std.fs.WriteText(\"path\")", message: "std.fs.WriteText expects 2 arguments, found 1"},
		{name: "Exists type", resultType: "Result<bool,std.fs.Failure>", call: "std.fs.Exists(false)", message: "argument 1 to std.fs.Exists must be string, found bool"},
		{name: "CreateDirectoryAll arity", resultType: "Result<null,std.fs.Failure>", call: "std.fs.CreateDirectoryAll()", message: "std.fs.CreateDirectoryAll expects 1 arguments, found 0"},
		{name: "Remove type", resultType: "Result<null,std.fs.Failure>", call: "std.fs.Remove(1)", message: "argument 1 to std.fs.Remove must be string, found int"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
