package compiler

import (
	"os/exec"
	"testing"
)

func TestRustStdBytesBufferMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdBytesBufferProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	if want := "0,255|1:-1|1:256|Aé界|0|0|é|-1:1:6|slice bounds out of range|slice bounds out of range|65,,|Hello, 世界|13|13|invalid UTF-8|bytes[6]|A:BC:2:ok:bounds:missing"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Rust bytes/buffer output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

const rustStdBytesBufferProgram = `
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
    ` + "`" + `${Constructed}|${InvalidLow}|${InvalidHigh}|${Full}|${EmptyStart}|${EmptyEnd}|${Middle}|${Negative}|${Reversed}|${PastEnd}|${AtFirst},${AtNegative},${AtPast}|${ConcatText}|${ConcatLen}|${EncodedLen}|${Utf8Fail}|${Value}|${BufFirst}:${BufSecondFirst}${BufSecondLast}:${BufLen}:${ReplacedStatus}:${RejectedStatus}:${BufMissing}` + "`" + `
}
`
