package compiler

import (
	"os/exec"
	"testing"
)

func TestBunStdCollectionsMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdCollectionsProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	const want = "0,255|1:-1|1:256|Aé界|0|0|é|-1:1:6|slice bounds out of range|slice bounds out of range|65,,|Hello, 世界|13|13|invalid UTF-8|bytes[6]|A:BC:2:ok:bounds:missing:X" +
		"@@.|.|..|/|/|a/b/c|a/b|a/b|b|/a|a|a|/b|a/b|/a/b|../..|../..|/|/|.|/c|.|.|/|.|a/b/c/..." +
		"##.|.|..|/|/|foo|bar|foo|bar|.|.|x|x" +
		"##.|.|.|/|/|/foo|/foo|.|foo|.|/|a/b|/a/b|.|/|a|/|x" +
		"##null|null|.bar|.baz|.bashrc|.|.|.c|null|." +
		"##false|true|true|false|false|true|false|false" +
		"##a/b/c||a/b|/a/b|a/b|a/b/d|b|a|a/b/d|/a/b|a/b|"
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Bun collections output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

const bunStdCollectionsProgram = `
use std.bytes.Slice as Slice
use std.bytes.FromValues as FromValues
use std.bytes.BoundsFailure as BoundsFailure
use std.bytes.ValueFailure as ValueFailure

function Make(Values: int[]) -> Result<bytes, ValueFailure> { FromValues(Values) }
function Take(Value: bytes, Start: int, End: int) -> Result<bytes, BoundsFailure> { Slice(Value, Start, End) }
function DecodeBytes(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) { Ok(Text) => Text Err(Failure) => Failure.Message }
}
function DescribeBytes(Value: bytes) -> string {
    let First = std.bytes.At(Value, 0)
    let Last = std.bytes.At(Value, 2)
    ` + "`" + `${First},${Last}` + "`" + `
}
function SetStatus(Value: Result<null, std.collections.BoundsFailure>) -> string {
    match Value { Ok(_) => "ok" Err(_) => "bounds" }
}
function ExtOr(Path: string, Fallback: string) -> string {
    let Extension = std.path.Extension(Path)
    if (Extension == null) {
        Fallback
    } else {
        Extension
    }
}
function BoolText(Value: bool) -> string {
    if (Value) {
        "true"
    } else {
        "false"
    }
}

function main() -> string effects { state } {
    let Value = std.bytes.FromUtf8("Aé界")
    let Encoded = std.bytes.FromUtf8("Hello, 世界")
    let Hello = std.bytes.FromUtf8("Hello, ")
    let World = std.bytes.FromUtf8("世界")
    let Constructed = match Make([0, 65, 255]) {
        Ok(Bytes) => DescribeBytes(Bytes)
        Err(Failure) => Failure.Message
    }
    let InvalidLow = match Make([1, -1, 256]) {
        Ok(_) => "unexpected"
        Err(Failure) => ` + "`" + `${Failure.Index}:${Failure.Value}` + "`" + `
    }
    let InvalidHigh = match Make([1, 256, -1]) {
        Ok(_) => "unexpected"
        Err(Failure) => ` + "`" + `${Failure.Index}:${Failure.Value}` + "`" + `
    }
    let Full = match Take(Value, 0, 6) { Ok(Bytes) => DecodeBytes(Bytes) Err(Failure) => Failure.Message }
    let EmptyStart = match Take(Value, 0, 0) { Ok(Bytes) => std.bytes.Length(Bytes) Err(_) => -1 }
    let EmptyEnd = match Take(Value, 6, 6) { Ok(Bytes) => std.bytes.Length(Bytes) Err(_) => -1 }
    let Middle = match Take(Value, 1, 3) { Ok(Bytes) => DecodeBytes(Bytes) Err(Failure) => Failure.Message }
    let Negative = match Take(Value, -1, 1) { Ok(_) => "unexpected" Err(Failure) => ` + "`" + `${Failure.Start}:${Failure.End}:${Failure.Length}` + "`" + ` }
    let Reversed = match Take(Value, 3, 2) { Ok(_) => "unexpected" Err(Failure) => Failure.Message }
    let PastEnd = match Take(Value, 0, 7) { Ok(_) => "unexpected" Err(Failure) => Failure.Message }
    let AtFirst = std.bytes.At(Value, 0)
    let AtNegative = std.bytes.At(Value, -1)
    let AtPast = std.bytes.At(Value, 6)
    let Concat = std.bytes.Concat([Hello, World])
    let ConcatText = DecodeBytes(Concat)
    let ConcatLen = std.bytes.Length(Concat)
    let EncodedLen = std.bytes.Length(Encoded)
    let InvalidBytes = match Make([255]) { Ok(Bytes) => Bytes Err(_) => std.bytes.FromUtf8("x") }
    let Utf8Fail = match std.bytes.ToUtf8(InvalidBytes) { Ok(_) => "unexpected" Err(Failure) => Failure.Message }
    let Buf = std.buffer.New<string>()
    std.buffer.Push<string>(Buf, "A")
    let FirstSnapshot = std.buffer.Freeze<string>(Buf)
    let Replaced = std.buffer.Set<string>(Buf, 0, "B")
    std.buffer.Push<string>(Buf, "C")
    let Rejected = std.buffer.Set<string>(Buf, 2, "D")
    let SecondSnapshot = std.buffer.Freeze<string>(Buf)
    let BufFirst = FirstSnapshot.Get(0)
    let BufSecondFirst = SecondSnapshot.Get(0)
    let BufSecondLast = SecondSnapshot.Get(1)
    let BufLen = std.buffer.Length<string>(Buf)
    let BufMissingNeg = std.buffer.Get<string>(Buf, -1)
    let BufMissingPast = std.buffer.Get<string>(Buf, 3)
    let ReplacedStatus = SetStatus(Replaced)
    let RejectedStatus = SetStatus(Rejected)
    let BufMissing = if (BufMissingNeg == null && BufMissingPast == null) { "missing" } else { "bad" }
    let Shared = std.buffer.New<string>()
    let Alias = Shared
    std.buffer.Push<string>(Shared, "X")
    let SharedSeen = std.buffer.Get<string>(Alias, 0)
    let SharedText = if (SharedSeen == null) { "missing" } else { SharedSeen }
    let BytesPart = ` + "`" + `${Constructed}|${InvalidLow}|${InvalidHigh}|${Full}|${EmptyStart}|${EmptyEnd}|${Middle}|${Negative}|${Reversed}|${PastEnd}|${AtFirst},${AtNegative},${AtPast}|${ConcatText}|${ConcatLen}|${EncodedLen}|${Utf8Fail}|${Value}|${BufFirst}:${BufSecondFirst}${BufSecondLast}:${BufLen}:${ReplacedStatus}:${RejectedStatus}:${BufMissing}:${SharedText}` + "`" + `
    let Cleans = std.text.Join([
        std.path.Clean(""),
        std.path.Clean("."),
        std.path.Clean(".."),
        std.path.Clean("/"),
        std.path.Clean("//"),
        std.path.Clean("a/b/c"),
        std.path.Clean("a//b"),
        std.path.Clean("a/./b"),
        std.path.Clean("a/../b"),
        std.path.Clean("/../a"),
        std.path.Clean("a/b/.."),
        std.path.Clean("./a"),
        std.path.Clean("/a/../b"),
        std.path.Clean("a/./b/."),
        std.path.Clean("//a//b//"),
        std.path.Clean("../.."),
        std.path.Clean("./../.."),
        std.path.Clean("/.."),
        std.path.Clean("/../.."),
        std.path.Clean("a/.."),
        std.path.Clean("/a/b/../../c"),
        std.path.Clean("./."),
        std.path.Clean("./"),
        std.path.Clean("/."),
        std.path.Clean("x/y//../.."),
        std.path.Clean("a/b/c/...")
    ], "|")
    let Bases = std.text.Join([
        std.path.Base(""),
        std.path.Base("."),
        std.path.Base(".."),
        std.path.Base("/"),
        std.path.Base("//"),
        std.path.Base("/foo/"),
        std.path.Base("/foo/bar"),
        std.path.Base("foo"),
        std.path.Base("foo/bar"),
        std.path.Base("./"),
        std.path.Base("/."),
        std.path.Base("x/"),
        std.path.Base("/x")
    ], "|")
    let Dirs = std.text.Join([
        std.path.Directory(""),
        std.path.Directory("."),
        std.path.Directory(".."),
        std.path.Directory("/"),
        std.path.Directory("//"),
        std.path.Directory("/foo/"),
        std.path.Directory("/foo/bar"),
        std.path.Directory("foo"),
        std.path.Directory("foo/bar"),
        std.path.Directory("./"),
        std.path.Directory("/."),
        std.path.Directory("a/b/c"),
        std.path.Directory("/a/b/c"),
        std.path.Directory("a"),
        std.path.Directory("/a"),
        std.path.Directory("a/.."),
        std.path.Directory("/.."),
        std.path.Directory("x/y")
    ], "|")
    let Exts = std.text.Join([
        ExtOr("", "null"),
        ExtOr("foo", "null"),
        ExtOr("foo.bar", "null"),
        ExtOr("foo.bar.baz", "null"),
        ExtOr(".bashrc", "null"),
        ExtOr("foo.", "null"),
        ExtOr("foo..", "null"),
        ExtOr("a/b.c", "null"),
        ExtOr("/tmp/", "null"),
        ExtOr("a.", "null")
    ], "|")
    let Absolutes = std.text.Join([
        BoolText(std.path.IsAbsolute("")),
        BoolText(std.path.IsAbsolute("/")),
        BoolText(std.path.IsAbsolute("/a")),
        BoolText(std.path.IsAbsolute("a")),
        BoolText(std.path.IsAbsolute("a/b")),
        BoolText(std.path.IsAbsolute("//a")),
        BoolText(std.path.IsAbsolute("./a")),
        BoolText(std.path.IsAbsolute("../a"))
    ], "|")
    let Joins = std.text.Join([
        std.path.Join(["a", "b", "c"]),
        std.path.Join(["", ""]),
        std.path.Join(["a", "", "b"]),
        std.path.Join(["/a", "b"]),
        std.path.Join(["a", "/b"]),
        std.path.Join(["a/b", "c/../d"]),
        std.path.Join(["a", "..", "b"]),
        std.path.Join(["", "a"]),
        std.path.Join(["a", "b/c", "..", "d"]),
        std.path.Join(["//a", "b"]),
        std.path.Join(["a", "./b"]),
        std.path.Join([])
    ], "|")
    let PathPart = std.text.Join([Cleans, Bases, Dirs, Exts, Absolutes, Joins], "##")
    let Combined = ` + "`" + `${BytesPart}@@${PathPart}` + "`" + `
    Combined
}
`
