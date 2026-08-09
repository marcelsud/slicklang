package compiler_test

import (
	"fmt"
	"strconv"
	"testing"
)

func TestStdSourceScanningBytesEverywhere(t *testing.T) {
	source := `
use std.bytes.Slice as Slice
use std.bytes.FromValues as FromValues
use std.bytes.BoundsFailure as BoundsFailure
use std.bytes.ValueFailure as ValueFailure

function Make(Values: int[]) -> Result<bytes, ValueFailure> { FromValues(Values) }
function Take(Value: bytes, Start: int, End: int) -> Result<bytes, BoundsFailure> { Slice(Value, Start, End) }
function Describe(Value: bytes) -> string {
    let First = std.bytes.At(Value, 0)
    let Last = std.bytes.At(Value, 2)
    ` + "`" + `${First},${Last}` + "`" + `
}
function DecodeBytes(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) { Ok(Text) => Text Err(Failure) => Failure.Message }
}

function main() -> string {
    let Constructed = match Make([0, 65, 255]) {
        Ok(Value) => Describe(Value)
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
    let Value = std.bytes.FromUtf8("Aé界")
    let Full = match Take(Value, 0, 6) { Ok(Bytes) => DecodeBytes(Bytes) Err(Failure) => Failure.Message }
    let EmptyStart = match Take(Value, 0, 0) { Ok(Bytes) => std.bytes.Length(Bytes) Err(_) => -1 }
    let EmptyEnd = match Take(Value, 6, 6) { Ok(Bytes) => std.bytes.Length(Bytes) Err(_) => -1 }
    let Middle = match Take(Value, 1, 3) { Ok(Bytes) => DecodeBytes(Bytes) Err(Failure) => Failure.Message }
    let Negative = match Take(Value, -1, 1) { Ok(_) => "unexpected" Err(Failure) => ` + "`" + `${Failure.Start}:${Failure.End}:${Failure.Length}` + "`" + ` }
    let Reversed = match Take(Value, 3, 2) { Ok(_) => "unexpected" Err(Failure) => Failure.Message }
    let PastEnd = match Take(Value, 0, 7) { Ok(_) => "unexpected" Err(Failure) => Failure.Message }
    ` + "`" + `${Constructed}|${InvalidLow}|${InvalidHigh}|${Full}|${EmptyStart}|${EmptyEnd}|${Middle}|${Negative}|${Reversed}|${PastEnd}` + "`" + `
}
`
	const want = "0,255|1:-1|1:256|Aé界|0|0|é|-1:1:6|slice bounds out of range|slice bounds out of range"
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("source-scanning byte output %q, want %q", output, want)
	}
}

func TestStdUTF8AndUnicodeEverywhere(t *testing.T) {
	source := `
use std.utf8.DecodeAt as DecodeAt
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
    let InvalidNegative = IsLetter(-1)
    let InvalidSurrogate = IsLetter(55296)
    let InvalidMaximum = IsLetter(1114112)
    let InvalidWrapped = IsLetter(4294967361)
    let Unicode = ` + "`" + `${Letter},${Digit},${Whitespace},${Upper}` + "`" + `
    let InvalidScalars = ` + "`" + `${InvalidNegative},${InvalidSurrogate},${InvalidMaximum},${InvalidWrapped}` + "`" + `
    ` + "`" + `${ASCII}|${Two}|${Three}|${Four}|${Replacement}|${Maximum}|${Continuation},${TruncatedTwo},${TruncatedThree},${TruncatedFour},${OverlongTwo},${OverlongThree},${Surrogate},${AboveMaximum},${InvalidLead},${InvalidContinuation},${Negative},${PastEnd},${Empty}|${Unicode}|${InvalidScalars}` + "`" + `
}
`
	const want = "65:1|162:2|8364:3|66376:4|65533:3|1114111:4|true,true,true,true,true,true,true,true,true,true,true,true,true|true,true,true,true|false,false,false,false"
	if output := runResultEverywhere(t, source); output != want {
		t.Fatalf("UTF-8 and Unicode output %q, want %q", output, want)
	}
}

func TestStdTextQuoteRoundTripsEverywhere(t *testing.T) {
	values := []string{
		"",
		"line\n\t\x00",
		`"quoted" and \\`,
		"`${Name}`",
		"Olá, 世界",
		string([]byte{0xff}),
	}
	for _, value := range values {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			literal := strconv.Quote(value)
			quoteSource := fmt.Sprintf(`function main() -> string { std.text.Quote(%s) }`, literal)
			if quoted := runResultEverywhere(t, quoteSource); quoted != literal {
				t.Fatalf("Quote(%q) = %q, want %q", value, quoted, literal)
			}
			roundTripSource := fmt.Sprintf(`function main() -> string { %s }`, literal)
			if roundTrip := runResultEverywhere(t, roundTripSource); roundTrip != value {
				t.Fatalf("parsed %q = %q, want %q", literal, roundTrip, value)
			}
		})
	}
}

func TestStdSourceScanningCallableDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		resultType string
		call       string
		message    string
	}{
		{name: "Slice arity", resultType: "Result<bytes,std.bytes.BoundsFailure>", call: `std.bytes.Slice(std.bytes.FromUtf8("x"), 0)`, message: "std.bytes.Slice expects 3 arguments, found 2"},
		{name: "FromValues type", resultType: "Result<bytes,std.bytes.ValueFailure>", call: `std.bytes.FromValues(["x"])`, message: "argument 1 to std.bytes.FromValues must be int[], found string[]"},
		{name: "DecodeAt index", resultType: "Result<std.utf8.DecodedRune,std.utf8.Failure>", call: `std.utf8.DecodeAt(std.bytes.FromUtf8("x"), "0")`, message: "argument 2 to std.utf8.DecodeAt must be int, found string"},
		{name: "IsLetter arity", resultType: "bool", call: `std.unicode.IsLetter()`, message: "std.unicode.IsLetter expects 1 arguments, found 0"},
		{name: "Quote type", resultType: "string", call: `std.text.Quote(1)`, message: "argument 1 to std.text.Quote must be string, found int"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function main() -> " + test.resultType + " { " + test.call + " }"
			assertDiagnostic(t, checkResult(t, source), "SLK320", test.message)
		})
	}
}
