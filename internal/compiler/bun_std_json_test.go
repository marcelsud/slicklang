package compiler

import (
	"os/exec"
	"testing"
)

// bunStdJSONProgram exercises the std.json contract the Bun backend must
// match: encoding a class with renamed fields, a nested optional field, an
// array field, and a nested class; decoding the same class and re-encoding it;
// declaring a union so the descriptor table carries a union entry; and a
// malformed-input failure with the structural path the interpreter reports.
const bunStdJSONProgram = `union Shape {
    Circle(Radius: float)
    Square(Side: float)
}

class Account {
    @std.json.Name("login")
    Name: string
    Active: bool
}

class Config {
    @std.json.Name("full_name")
    Name: string
    Port: int
    Debug: bool?
    Tags: string[]
    Owner: Account
    Backup: Account?
    Ratio: float
}

function EncodeConfig(C: Config) -> string {
    match std.json.Encode<Config>(C) {
        Ok(Text) => Text
        Err(Failure) => ` + "`" + `enc:${Failure.Message}` + "`" + `
    }
}

function RoundTrip(Text: string) -> string {
    match std.json.Decode<Config>(Text) {
        Ok(C) => EncodeConfig(C)
        Err(Failure) => ` + "`" + `dec:${Failure.Path}:${Failure.Message}` + "`" + `
    }
}

function BadDecode() -> string {
    match std.json.Decode<Config>("") {
        Ok(C) => "ok"
        Err(Failure) => ` + "`" + `${Failure.Path}: ${Failure.Message}` + "`" + `
    }
}

function main() -> (string, string, string) {
    let Literal = Config { Name: "Slick", Port: 8080, Tags: ["strict", "native"], Owner: Account { Name: "ada", Active: true }, Ratio: 1.5 }
    let Enc = EncodeConfig(Literal)
    let RT = RoundTrip(` + "`" + `{"full_name":"Slick","Port":8080,"Debug":true,"Tags":["strict","native"],"Owner":{"login":"ada","Active":true},"Backup":{"login":"grace","Active":false},"Ratio":1.5}` + "`" + `)
    let Bad = BadDecode()
    let Outcome = (Enc, RT, Bad)
    Outcome
}
`

// bunStdJSONEdgeProgram covers the encoding/json mismatches: number grammar,
// raw control bytes in strings, unpaired high-surrogate then non-low escape,
// and negative zero.
const bunStdJSONEdgeProgram = `function ReportInt(Text: string) -> string {
    match std.json.Decode<int>(Text) {
        Ok(Value) => ` + "`" + `ok:${Value}` + "`" + `
        Err(Failure) => ` + "`" + `${Failure.Path}:${Failure.Message}` + "`" + `
    }
}

function ReportStr(Text: string) -> string {
    match std.json.Decode<string>(Text) {
        Ok(Value) => Value
        Err(Failure) => ` + "`" + `${Failure.Path}:${Failure.Message}` + "`" + `
    }
}

function DeepJSON() -> string {
    let Text = "0"
    for Index in 0..10001 {
        Text = "[" + Text + "]"
        if (Index < 0) {
            Text = Text
        }
    }
    Text
}

function main() -> (string, string, string, string, string, string, string) {
    let LeadZero = ReportInt("01")
    let TrailDot = ReportInt("1.")
    let BadExp = ReportInt("1e")
    let Control = ReportStr("\"\u0001\"")
    let Surrogate = ReportStr("\"\\uD800\\u0041\"")
    let NegZero = match std.json.Encode<float>(-0.0) {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
    let Deep = match std.json.Decode<int>(DeepJSON()) {
        Ok(_) => "ok"
        Err(_) => "fail"
    }
    let Outcome = (LeadZero, TrailDot, BadExp, Control, Surrogate, NegZero, Deep)
    Outcome
}
`

// bunStdJSONDepthProgram checks that decode accepts nesting well past 256,
// that a document past encoding/json's 10000 limit is a typed Failure, and
// that encoding a deep value does not abort.
const bunStdJSONDepthProgram = `class Nest {
    Child: Nest?
    Value: int?
}

function NestedJSON() -> string {
    let Text = "{\"Value\":1}"
    for Index in 0..1024 {
        Text = "{\"Child\":" + Text + "}"
        if (Index < 0) {
            Text = Text
        }
    }
    Text
}

function NestedArrays() -> string {
    let Text = "0"
    for Index in 0..10001 {
        Text = "[" + Text + "]"
        if (Index < 0) {
            Text = Text
        }
    }
    Text
}

function ReportNest(Text: string) -> string {
    match std.json.Decode<Nest>(Text) {
        Ok(_) => "ok"
        Err(Failure) => ` + "`" + `${Failure.Path}:${Failure.Message}` + "`" + `
    }
}

function ReportOver(Text: string) -> string {
    match std.json.Decode<int>(Text) {
        Ok(_) => "ok"
        Err(_) => "fail"
    }
}

function RoundTripNest(Text: string) -> string {
    match std.json.Decode<Nest>(Text) {
        Ok(Value) => match std.json.Encode<Nest>(Value) {
            Ok(Encoded) => Encoded
            Err(Failure) => ` + "`" + `enc:${Failure.Message}` + "`" + `
        }
        Err(Failure) => ` + "`" + `dec:${Failure.Path}:${Failure.Message}` + "`" + `
    }
}

function main() -> (string, string, string) {
    let Deep = ReportNest(NestedJSON())
    let Over = ReportOver(NestedArrays())
    let Encoded = RoundTripNest(NestedJSON())
    let Outcome = (Deep, Over, Encoded)
    Outcome
}
`

func assertBunJSONMatchesInterpreter(t *testing.T, text string) {
	t.Helper()
	source := Source{Name: "main.slk", Namespace: "root", Text: text}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatalf("run interpreter: %v", err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run bun binary: %v: %s", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun std.json output=%q\nerror=%v\nwant interpreter=%q", output, err, interpreted+"\n")
	}
}

func TestBunStdJSONMatchesInterpreter(t *testing.T) {
	t.Run("Contract", func(t *testing.T) {
		assertBunJSONMatchesInterpreter(t, bunStdJSONProgram)
	})
	t.Run("Edges", func(t *testing.T) {
		assertBunJSONMatchesInterpreter(t, bunStdJSONEdgeProgram)
	})
	t.Run("Depth", func(t *testing.T) {
		assertBunJSONMatchesInterpreter(t, bunStdJSONDepthProgram)
	})
}
