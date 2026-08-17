package compiler

import (
	"os/exec"
	"testing"
)

// TestRustStdUnicodeUtf8MatchesInterpreter exercises every std.unicode
// predicate and std.utf8.DecodeAt against the interpreter and the compiled
// Rust binary, covering ASCII, multi-byte, replacement, maximum, truncated,
// overlong, surrogate, above-maximum, invalid-lead, invalid-continuation,
// and out-of-range byte indexes, plus valid, non-scalar, and Nd/Lu/L*
// boundary predicate inputs.
func TestRustStdUnicodeUtf8MatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdUnicodeUtf8Program}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Rust binary failed: %v: %s", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust output=%q, want interpreter %q", output, interpreted+"\n")
	}
}

const rustStdUnicodeUtf8Program = `use std.utf8.DecodeAt as DecodeAt
use std.utf8.DecodedRune as DecodedRune
use std.utf8.Failure as DecodeFailure
use std.bytes.FromValues as FromValues
use std.unicode.IsLetter as IsLetter
use std.unicode.IsDigit as IsDigit
use std.unicode.IsWhitespace as IsWhitespace
use std.unicode.IsUpper as IsUpper

function Decode(Value: bytes, Index: int) -> Result<DecodedRune, DecodeFailure> { DecodeAt(Value, Index) }
function DecodeFails(Values: int[], Index: int) -> bool {
    match FromValues(Values) {
        Ok(Value) => match Decode(Value, Index) { Ok(_) => false Err(_) => true }
        Err(_) => false
    }
}
function DecodeValues(Values: int[]) -> string {
    match FromValues(Values) {
        Ok(Value) => match Decode(Value, 0) { Ok(Rune) => ` + "`" + `${Rune.Value}:${Rune.Width}` + "`" + ` Err(Failure) => Failure.Message }
        Err(Failure) => Failure.Message
    }
}

function main() -> string {
    let Value = match FromValues([65, 194, 162, 226, 130, 172, 240, 144, 141, 136]) {
        Ok(Bytes) => Bytes
        Err(_) => std.bytes.FromUtf8("")
    }
    let ASCII = match Decode(Value, 0) { Ok(Rune) => ` + "`" + `${Rune.Value}:${Rune.Width}` + "`" + ` Err(Failure) => Failure.Message }
    let Two = match Decode(Value, 1) { Ok(Rune) => ` + "`" + `${Rune.Value}:${Rune.Width}` + "`" + ` Err(Failure) => Failure.Message }
    let Three = match Decode(Value, 3) { Ok(Rune) => ` + "`" + `${Rune.Value}:${Rune.Width}` + "`" + ` Err(Failure) => Failure.Message }
    let Four = match Decode(Value, 6) { Ok(Rune) => ` + "`" + `${Rune.Value}:${Rune.Width}` + "`" + ` Err(Failure) => Failure.Message }
    let Replacement = DecodeValues([239, 191, 189])
    let Maximum = DecodeValues([244, 143, 191, 191])
    let Continuation = DecodeFails([194, 162], 1)
    let TruncatedTwo = DecodeFails([194], 0)
    let TruncatedThree = DecodeFails([226, 130], 0)
    let TruncatedFour = DecodeFails([240, 144, 141], 0)
    let OverlongTwo = DecodeFails([192, 128], 0)
    let OverlongThree = DecodeFails([224, 128, 128], 0)
    let Surrogate = DecodeFails([237, 160, 128], 0)
    let AboveMaximum = DecodeFails([244, 144, 128, 128], 0)
    let InvalidLead = DecodeFails([255], 0)
    let InvalidContinuation = DecodeFails([226, 40, 161], 0)
    let Negative = DecodeFails([65], -1)
    let PastEnd = DecodeFails([65], 1)
    let Empty = DecodeFails([], 0)
    let Letter = IsLetter(937)
    let Digit = IsDigit(1635)
    let Whitespace = IsWhitespace(8195)
    let Upper = IsUpper(937)
    let FractionDigit = IsDigit(190)
    let AsciiDigit = IsDigit(48)
    let TitleUpper = IsUpper(453)
    let AsciiUpper = IsUpper(65)
    let CombiningLetter = IsLetter(837)
    let ModifierLetter = IsLetter(688)
    let RomanUpper = IsUpper(8544)
    let InvalidNegative = IsLetter(-1)
    let InvalidSurrogate = IsLetter(55296)
    let InvalidMaximum = IsLetter(1114112)
    let InvalidWrapped = IsLetter(4294967361)
    let Unicode = ` + "`" + `${Letter},${Digit},${Whitespace},${Upper}` + "`" + `
    let Categories = ` + "`" + `${FractionDigit},${AsciiDigit},${TitleUpper},${AsciiUpper},${CombiningLetter},${ModifierLetter},${RomanUpper}` + "`" + `
    let InvalidScalars = ` + "`" + `${InvalidNegative},${InvalidSurrogate},${InvalidMaximum},${InvalidWrapped}` + "`" + `
    ` + "`" + `${ASCII}|${Two}|${Three}|${Four}|${Replacement}|${Maximum}|${Continuation},${TruncatedTwo},${TruncatedThree},${TruncatedFour},${OverlongTwo},${OverlongThree},${Surrogate},${AboveMaximum},${InvalidLead},${InvalidContinuation},${Negative},${PastEnd},${Empty}|${Unicode}|${Categories}|${InvalidScalars}` + "`" + `
}
`
