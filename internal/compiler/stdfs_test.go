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

// stdFSDirectorySupport lists a directory into a deterministic ordered summary
// so both backends can be compared on a single line of output.
const stdFSDirectorySupport = `
function Describe(Entries: std.fs.Entry[]) -> string {
    let Buffer = std.buffer.New<string>()
    for Entry in Entries {
        std.buffer.Push<string>(Buffer, ` + "`" + `${Entry.Name}|${Entry.IsDirectory}|${Entry.Path}` + "`" + `)
    }
    std.text.Join(std.buffer.Freeze<string>(Buffer), ",")
}

function List(Path: string) -> string {
    match std.fs.ReadDirectory(Path) {
        Ok(Entries) => Describe(Entries)
        Err(Failure) => ` + "`" + `${Failure.Operation}|${Failure.Path}|${Failure.Message}` + "`" + `
    }
}
`

func stdFSEntry(directory, name string, isDirectory bool) string {
	return fmt.Sprintf("%s|%t|%s", name, isDirectory, filepath.Join(directory, name))
}

// stdFSListingProgram joins one List call per path with a separator that cannot
// appear in a path, so each listing stays individually addressable.
func stdFSListingProgram(paths []string) string {
	lets := make([]string, 0, len(paths))
	names := make([]string, 0, len(paths))
	for index, path := range paths {
		name := fmt.Sprintf("Listing%d", index)
		lets = append(lets, fmt.Sprintf("    let %s = List(%s)", name, strconv.Quote(path)))
		names = append(names, "${"+name+"}")
	}
	return stdFSDirectorySupport + "\nfunction main() -> string {\n" +
		strings.Join(lets, "\n") + "\n    `" + strings.Join(names, "§") + "`\n}\n"
}

func TestStdFSReadDirectoryListsSortedDirectChildrenEverywhere(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	populated := filepath.Join(root, "populated")
	nested := filepath.Join(populated, "nested")
	for _, directory := range []string{empty, populated, nested} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	// The creation order is deliberately not the expected order, so a backend
	// that returns host enumeration order cannot pass.
	for _, name := range []string{"b.txt", "é.txt", "a.txt", "Z.txt"} {
		if err := os.WriteFile(filepath.Join(populated, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatalf("write nested child: %v", err)
	}

	// Ordering is byte order, not locale collation: "Z" precedes "a", and the
	// multi-byte "é" sorts last. The nested child never appears under the
	// populated listing, which stays one level deep.
	want := strings.Join([]string{
		"",
		strings.Join([]string{
			stdFSEntry(populated, "Z.txt", false),
			stdFSEntry(populated, "a.txt", false),
			stdFSEntry(populated, "b.txt", false),
			stdFSEntry(populated, "nested", true),
			stdFSEntry(populated, "é.txt", false),
		}, ","),
		stdFSEntry(nested, "deep.txt", false),
	}, "§")
	source := stdFSListingProgram([]string{empty, populated, nested})
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("std.fs.ReadDirectory produced %q, want %q", output, want)
	}
}

func TestStdFSReadDirectoryFailuresEverywhere(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("file"), 0o644); err != nil {
		t.Fatalf("write non-directory fixture: %v", err)
	}
	paths := []string{filepath.Join(root, "missing"), file, root + "\x00invalid"}
	if os.Geteuid() != 0 {
		// Root ignores the mode bits, so the permission case only proves
		// anything for an unprivileged run.
		unreadable := filepath.Join(root, "unreadable")
		if err := os.Mkdir(unreadable, 0o000); err != nil {
			t.Fatalf("create unreadable fixture: %v", err)
		}
		// Restore the mode so TempDir cleanup can remove the tree.
		t.Cleanup(func() { os.Chmod(unreadable, 0o755) })
		paths = append(paths, unreadable)
	}

	output := runResultEverywhere(t, stdFSListingProgram(paths))
	lines := strings.Split(output, "§")
	if len(lines) != len(paths) {
		t.Fatalf("std.fs.ReadDirectory failures produced %d lines: %q", len(lines), output)
	}
	for index, line := range lines {
		if !strings.HasPrefix(line, "ReadDirectory|"+paths[index]+"|") {
			t.Fatalf("failure %d = %q, want a ReadDirectory failure for %q", index, line, paths[index])
		}
		if strings.HasSuffix(line, "|") {
			t.Fatalf("failure %d = %q, want a non-empty message", index, line)
		}
	}
}

// stdFSWorkspaceSupport records the created workspace path in the environment
// so a test can prove the directory is gone once the using scope exits, even
// when the body never produced a value.
const stdFSWorkspaceSupport = `
function Reset() -> null {
    match std.env.Unset("SLICK_FS_WORKSPACE") {
        Ok(_) => null
        Err(_) => null
    }
}

function Remember(Path: string) -> null {
    match std.env.Set("SLICK_FS_WORKSPACE", Path) {
        Ok(_) => null
        Err(_) => null
    }
}

function Absent(Present: bool) -> string {
    let Value = !Present
    ` + "`" + `${Value}` + "`" + `
}

function Gone(Path: string) -> string {
    match std.fs.Exists(Path) {
        Ok(Present) => Absent(Present)
        Err(Failure) => Failure.Message
    }
}

function Removed() -> string {
    let Path = std.env.Get("SLICK_FS_WORKSPACE")
    if (Path == null) { "unrecorded" } else { Gone(Path) }
}
`

func TestStdFSTemporaryDirectoryIsUniqueAbsoluteAndRemovedEverywhere(t *testing.T) {
	source := stdFSWorkspaceSupport + `
function Reserve() -> Result<string, std.fs.Failure> throws std.fs.Failure {
    using Workspace = std.fs.CreateTemporaryDirectory("slick-unique-")? {
        let Present = std.fs.Exists(Workspace.Path)?
        if (Present) { Ok(Workspace.Path) } else { Ok("missing during block") }
    }
}

function Compare(First: string, Second: string) -> string {
    let Unique = First != Second
    let Absolute = std.path.IsAbsolute(First)
    let FirstGone = Gone(First)
    let SecondGone = Gone(Second)
    ` + "`" + `${Unique}|${Absolute}|${FirstGone}|${SecondGone}` + "`" + `
}

function main() -> string throws std.fs.Failure {
    match Reserve() {
        Ok(First) => match Reserve() {
            Ok(Second) => Compare(First, Second)
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`
	if output := runResultEverywhere(t, source); output != "true|true|true|true" {
		t.Fatalf("temporary workspace contract produced %q", output)
	}
}

func TestStdFSTemporaryDirectoryClosesOnEveryExitPathEverywhere(t *testing.T) {
	const report = `
function Report(Value: string) -> string {
    let Cleaned = Removed()
    ` + "`" + `${Value};${Cleaned}` + "`" + `
}
`
	tests := map[string]struct {
		program string
		want    string
	}{
		"normal completion": {
			program: `
function Exercise() -> Result<string, std.fs.Failure> throws std.fs.Failure {
    using Workspace = std.fs.CreateTemporaryDirectory("slick-exit-")? {
        Remember(Workspace.Path)
        Ok("normal")
    }
}
function main() -> string throws std.fs.Failure {
    Reset()
    let Value = match Exercise() {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
    Report(Value)
}
`,
			want: "normal;true",
		},
		"early return": {
			program: `
function Exercise() -> Result<string, std.fs.Failure> throws std.fs.Failure {
    using Workspace = std.fs.CreateTemporaryDirectory("slick-exit-")? {
        Remember(Workspace.Path)
        return Ok("returned")
    }
}
function main() -> string throws std.fs.Failure {
    Reset()
    let Value = match Exercise() {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
    Report(Value)
}
`,
			want: "returned;true",
		},
		"result propagation": {
			program: `
function Exercise() -> Result<string, std.fs.Failure> throws std.fs.Failure {
    using Workspace = std.fs.CreateTemporaryDirectory("slick-exit-")? {
        Remember(Workspace.Path)
        let Text = std.fs.ReadText(std.path.Join([Workspace.Path, "absent.txt"]))?
        Ok(Text)
    }
}
function main() -> string throws std.fs.Failure {
    Reset()
    let Value = match Exercise() {
        Ok(Text) => Text
        Err(Failure) => Failure.Operation
    }
    Report(Value)
}
`,
			want: "ReadText;true",
		},
		"checked throw": {
			program: `
class BodyFailure implements Error {}
function Exercise(Directory: std.fs.TemporaryDirectory) -> string throws BodyFailure | std.fs.Failure {
    using Workspace = Directory {
        Remember(Workspace.Path)
        throw BodyFailure("body")
    }
}
function Guarded(Directory: std.fs.TemporaryDirectory) -> string {
    Exercise(Directory) catch (Caught) {
        BodyFailure => "caught"
        std.fs.Failure => "cleanup"
    }
}
function main() -> string {
    Reset()
    let Value = match std.fs.CreateTemporaryDirectory("slick-exit-") {
        Ok(Directory) => Guarded(Directory)
        Err(Failure) => Failure.Message
    }
    Report(Value)
}
`,
			want: "caught;true",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if output := runResultEverywhere(t, stdFSWorkspaceSupport+report+test.program); output != test.want {
				t.Fatalf("cleanup output = %q, want %q", output, test.want)
			}
		})
	}
}

func TestStdFSTemporaryDirectoryCloseIsIdempotentEverywhere(t *testing.T) {
	// Both scopes close the same owned resource, so the second close must be a
	// silent no-op rather than a failure.
	source := stdFSWorkspaceSupport + `
function CloseTwice(Directory: std.fs.TemporaryDirectory) -> string throws std.fs.Failure {
    let First = using Opened = Directory { "first" }
    let Second = using Reopened = Directory { "second" }
    let Cleaned = Gone(Directory.Path)
    ` + "`" + `${First}|${Second}|${Cleaned}` + "`" + `
}

function main() -> string throws std.fs.Failure {
    match std.fs.CreateTemporaryDirectory("slick-idempotent-") {
        Ok(Directory) => CloseTwice(Directory)
        Err(Failure) => Failure.Message
    }
}
`
	if output := runResultEverywhere(t, source); output != "first|second|true" {
		t.Fatalf("idempotent close produced %q", output)
	}
}

func TestStdFSCreateTemporaryDirectoryRejectsParentSelectionEverywhere(t *testing.T) {
	source := `
function main() -> string {
    match std.fs.CreateTemporaryDirectory("../escape-") {
        Ok(Workspace) => "unexpected success"
        Err(Failure) => ` + "`" + `${Failure.Operation}|${Failure.Path}` + "`" + `
    }
}
`
	if output := runResultEverywhere(t, source); output != "CreateTemporaryDirectory|../escape-" {
		t.Fatalf("prefix rejection produced %q", output)
	}
}

func TestStdFSTemporaryDirectoryCloseRefusesUnownedTargetsEverywhere(t *testing.T) {
	guarded := filepath.Join(t.TempDir(), "guarded")
	keep := filepath.Join(guarded, "keep.txt")
	if err := os.MkdirAll(guarded, 0o755); err != nil {
		t.Fatalf("create guarded fixture: %v", err)
	}
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write guarded child: %v", err)
	}

	// A TemporaryDirectory built from an object literal owns nothing, so Close
	// reports a deterministic failure instead of removing a host path.
	source := fmt.Sprintf(`
function main() -> string {
    using Workspace = std.fs.TemporaryDirectory { Path: %s } {
        "body"
    } catch (Caught) {
        std.fs.Failure => `+"`"+`${Caught.Operation}|${Caught.Path}|${Caught.Message}`+"`"+`
    }
}
`, strconv.Quote(guarded))
	want := "Close|" + guarded + "|temporary directory is not owned by this resource"
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("unowned close produced %q, want %q", output, want)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unowned close removed a host path: %v", err)
	}
}

func TestStdFSTemporaryDirectoryCleanupFailureFollowsUsingPrecedence(t *testing.T) {
	source := `
class BodyFailure implements Error {}
function main() -> string throws BodyFailure | std.fs.Failure {
    using Workspace = std.fs.TemporaryDirectory { Path: "/slick-unowned" } {
        throw BodyFailure("body")
    }
}
`
	want := "root.BodyFailure: body (suppressed: std.fs.Failure: temporary directory is not owned by this resource)"
	if got := runUsingFailureEverywhere(t, source); got != want {
		t.Fatalf("cleanup precedence failure = %q, want %q", got, want)
	}
}

func TestStdFSDirectoryExactAliasesAndSignatures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "only.txt"), []byte("only"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	source := fmt.Sprintf(`
use std.fs.ReadDirectory as ReadDirectory
use std.fs.Entry as Entry
use std.fs.CreateTemporaryDirectory as CreateWorkspace
use std.fs.TemporaryDirectory as Workspace
use std.fs.Failure as FSFailure

function List(Path: string) -> Result<Entry[], FSFailure> { ReadDirectory(Path) }
function Open(Prefix: string) -> Result<Workspace, FSFailure> { CreateWorkspace(Prefix) }

function Head(Entries: Entry[]) -> string {
    let Value = Entries.Get(0)
    if (Value == null) { "empty" } else { `+"`"+`${Value.Name}|${Value.IsDirectory}`+"`"+` }
}

function First(Path: string) -> string {
    match List(Path) {
        Ok(Entries) => Head(Entries)
        Err(Failure) => Failure.Message
    }
}

function Absolute() -> Result<string, FSFailure> throws FSFailure {
    using Handle = Open("slick-alias-")? {
        let Value = std.path.IsAbsolute(Handle.Path)
        Ok(`+"`"+`${Value}`+"`"+`)
    }
}

function main() -> string throws FSFailure {
    let Listed = First(%s)
    let Opened = match Absolute() {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
    `+"`"+`${Listed}|${Opened}`+"`"+`
}
`, strconv.Quote(root))
	assertNoDiagnostics(t, checkResult(t, source))
	if output := runResultEverywhere(t, source); output != "only.txt|false|true" {
		t.Fatalf("std.fs directory aliases produced %q", output)
	}
}

func TestStdFSDirectoryCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{
			name:       "ReadDirectory type",
			resultType: "Result<std.fs.Entry[],std.fs.Failure>",
			call:       "std.fs.ReadDirectory(1)",
			message:    "argument 1 to std.fs.ReadDirectory must be string, found int",
		},
		{
			name:       "ReadDirectory arity",
			resultType: "Result<std.fs.Entry[],std.fs.Failure>",
			call:       "std.fs.ReadDirectory()",
			message:    "std.fs.ReadDirectory expects 1 arguments, found 0",
		},
		{
			name:       "CreateTemporaryDirectory type",
			resultType: "Result<std.fs.TemporaryDirectory,std.fs.Failure>",
			call:       "std.fs.CreateTemporaryDirectory(false)",
			message:    "argument 1 to std.fs.CreateTemporaryDirectory must be string, found bool",
		},
		{
			name:       "CreateTemporaryDirectory arity",
			resultType: "Result<std.fs.TemporaryDirectory,std.fs.Failure>",
			call:       `std.fs.CreateTemporaryDirectory("a", "b")`,
			message:    "std.fs.CreateTemporaryDirectory expects 1 arguments, found 2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
