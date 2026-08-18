package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// bunStdConvertMathProgram exercises every std.convert, std.math, std.unicode,
// and std.utf8 operation the Bun backend owns, including failure paths.
const bunStdConvertMathProgram = `function DescribeInt(Text: string) -> string {
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

function Flag(Value: bool) -> string {
    if (Value) { "true" } else { "false" }
}

function Decode(Value: bytes, Index: int) -> string {
    match std.utf8.DecodeAt(Value, Index) {
        Ok(Rune) => std.convert.IntToString(Rune.Value) + ":" + std.convert.IntToString(Rune.Width)
        Err(Failure) => Failure.Message
    }
}

function DecodeValues(Values: int[], Index: int) -> string {
    match std.bytes.FromValues(Values) {
        Ok(Value) => Decode(Value, Index)
        Err(Failure) => Failure.Message
    }
}

function main() -> string {
    let Low = MinInt()
    let High = 9223372036854775807
    let Encoded = std.bytes.FromUtf8("A¢€𐍈")
    let HexZeros = ""
    for Item in 0..300 {
        HexZeros = HexZeros + "0"
    }
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
        DescribeFloat("1_0.5"),
        DescribeFloat("0x1_0p0"),
        DescribeFloat("1_.5"),
        DescribeFloat("0x1" + HexZeros + "p-1200"),
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
        DescribeRem(Low, 2),
        Decode(Encoded, 0),
        Decode(Encoded, 1),
        Decode(Encoded, 3),
        Decode(Encoded, 6),
        DecodeValues([239, 191, 189], 0),
        DecodeValues([244, 143, 191, 191], 0),
        DecodeValues([194, 162], 1),
        DecodeValues([194], 0),
        DecodeValues([226, 130], 0),
        DecodeValues([240, 144, 141], 0),
        DecodeValues([192, 128], 0),
        DecodeValues([224, 128, 128], 0),
        DecodeValues([237, 160, 128], 0),
        DecodeValues([244, 144, 128, 128], 0),
        DecodeValues([245, 128, 128, 128], 0),
        DecodeValues([255], 0),
        DecodeValues([226, 40, 161], 0),
        DecodeValues([65], -1),
        DecodeValues([65], 1),
        DecodeValues([], 0),
        Flag(std.unicode.IsLetter(937)),
        Flag(std.unicode.IsDigit(1635)),
        Flag(std.unicode.IsWhitespace(8195)),
        Flag(std.unicode.IsUpper(937)),
        Flag(std.unicode.IsDigit(190)),
        Flag(std.unicode.IsDigit(48)),
        Flag(std.unicode.IsUpper(453)),
        Flag(std.unicode.IsUpper(65)),
        Flag(std.unicode.IsLetter(837)),
        Flag(std.unicode.IsLetter(688)),
        Flag(std.unicode.IsUpper(8544)),
        Flag(std.unicode.IsLetter(-1)),
        Flag(std.unicode.IsLetter(55296)),
        Flag(std.unicode.IsLetter(1114112)),
        Flag(std.unicode.IsLetter(4294967361))
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

func TestBunStdConvertMathMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdConvertMathProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	want := strings.Join([]string{
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
		"1.25",
		"-2.5",
		"0",
		"1000",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:invalid floating-point number",
		"err:float:floating-point value out of range",
		"1.2345678901234567",
		"10.5",
		"16",
		"err:float:invalid floating-point number",
		"1",
		"1e+06",
		"1e-05",
		"123456",
		"-0",
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
		"2",
		"-2",
		"2",
		"-2",
		"0",
		"Remainder:DivisionByZero:division by zero",
		"0",
		"0",
		"0",
		"65:1",
		"162:2",
		"8364:3",
		"66376:4",
		"65533:3",
		"1114111:4",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"invalid UTF-8 encoding",
		"byte index out of range",
		"byte index out of range",
		"byte index out of range",
		"true",
		"true",
		"true",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"false",
		"false",
		"false",
		"false",
	}, "|")
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun output = %q, want interpreter output %q", output, interpreted+"\n")
	}
	t.Run("UnsetInvalidName", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdEnvUnsetInvalidProgram}
		binary := buildBunTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Bun binary error = %v, output = %q", err, output)
		}
		if string(output) != "Unset: unsetenv: invalid argument\n" {
			t.Fatalf("Bun output = %q, want %q", output, "Unset: unsetenv: invalid argument\n")
		}
	})
	t.Run("CancelledCustomReader", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdCancelledReaderProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoDiagnostics(t, diagnostics)
		if interpreted != "started" {
			t.Fatalf("interpreter output = %q, want started", interpreted)
		}
		binary := buildBunTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Bun binary error = %v, output = %q", err, output)
		}
		if string(output) != "started\n" {
			t.Fatalf("Bun output = %q, want started", output)
		}
	})
}

const bunStdEnvUnsetInvalidProgram = `function main() -> string effects { environment } {
    match std.env.Unset("BAD=NAME") {
        Ok(_) => "ok"
        Err(F) => F.Operation + ": " + F.Message
    }
}
`

const bunStdCancelledReaderProgram = `class Stop implements Error {
    Message: string
}

class CancelReader {
    function Read(MaxBytes: int) -> Result<bytes?, std.io.Failure> effects { io } {
        for Item in 0..100000000 {
            let _ = Item
        }
        Ok(null)
    }
    function Close() -> null throws std.io.Failure { null }
}

function Mark(Value: string) -> string effects { environment } {
    let _ = std.env.Set("SLICK_CANCEL_READER", Value)
    Value
}

function Consume() -> string effects { io, environment } {
    let _ = Mark("started")
    let Reader = CancelReader {}
    match std.io.ReadAll(Reader, 8) {
        Ok(_) => Mark("ok")
        Err(F) => Mark("err:" + F.Operation + ":" + F.Message)
    }
}

function Fail() -> string throws Stop {
    for Item in 0..1000000 {
        let _ = Item
    }
    throw Stop { Message: "stop" }
}

function Drive() -> string throws Stop effects { io, environment } {
    async let Work = Consume()
    async let Killer = Fail()
    let _ = await Killer
    await Work
}

function Observed() -> string effects { environment } {
    let Got = std.env.Get("SLICK_CANCEL_READER")
    if (Got == null) {
        "missing"
    } else {
        Got
    }
}

function main() -> string effects { io, environment } {
    Drive() catch { Stop as _ => Observed() }
}
`
