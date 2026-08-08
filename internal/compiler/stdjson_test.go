package compiler_test

import (
	"strings"
	"testing"

	"slick/internal/compiler"
)

const stdJSONConfig = `
class Config {
    Name: string
    Port: int
    Debug: bool?
    Tags: string[]
}
`

const jsonFailTemplate = "`${Failure.Operation}|${Failure.Path}|${Failure.Message}`"
const jsonPathTemplate = "`${Failure.Path}:${Failure.Message}`"
const jsonIntTemplate = "`${Low}:${High}`"

func TestStdJsonExactAliasesAndFailureTypeCheck(t *testing.T) {
	diagnostics := checkResult(t, `
	use std.json.Decode as DecodeJson
	use std.json.Encode as EncodeJson
	use std.json.Failure as JsonFailure

	`+stdJSONConfig+`
	function Load(Text: string) -> Result<Config, JsonFailure> {
	    DecodeJson<Config>(Text)
	}

	function Dump(Value: Config) -> Result<string, JsonFailure> {
	    EncodeJson<Config>(Value)
	}

	function NewFailure() -> JsonFailure {
	    JsonFailure {
	        Operation: "Decode"
	        Path: "$"
	        Message: "failure"
	    }
	}
	`)
	assertNoDiagnostics(t, diagnostics)
}

func TestStdJsonGenericCallSyntax(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name: "missing type arguments",
			source: `
class Config {
    Name: string
    Port: int
    Tags: string[]
}
function main() -> Result<Config, std.json.Failure> {
    std.json.Decode("{\"Name\":\"A\",\"Port\":1,\"Tags\":[]}")
}
`,
			code:    "SLK380",
			message: "expects 1 type arguments, found 0",
		},
		{
			name: "extra type arguments",
			source: `
class Config {
    Name: string
    Port: int
    Tags: string[]
}
function main() -> Result<Config, std.json.Failure> {
    std.json.Decode<Config, string>("{\"Name\":\"A\",\"Port\":1,\"Tags\":[]}")
}
`,
			code:    "SLK380",
			message: "expects 1 type arguments, found 2",
		},
		{
			name: "type arguments on ordinary function",
			source: `
function Identity(Value: int) -> int { Value }
function main() -> int { Identity<int>(1) }
`,
			code:    "SLK380",
			message: "does not take type arguments",
		},
		{
			name: "Encode value assignability",
			source: `
class Config {
    Name: string
}
function main() -> Result<string, std.json.Failure> {
    std.json.Encode<Config>("nope")
}
`,
			code:    "SLK320",
			message: "argument 1 to std.json.Encode must be Config, found string",
		},
		{
			name: "unsupported Result type argument",
			source: `
function main() -> Result<Result<int, std.json.Failure>, std.json.Failure> {
    std.json.Decode<Result<int, std.json.Failure>>("1")
}
`,
			code:    "SLK381",
			message: "Result cannot be encoded or decoded as JSON",
		},
		{
			name: "unsupported interface type argument",
			source: `
interface Marker {
    function mark() -> null
}
function main() -> Result<Marker, std.json.Failure> {
    std.json.Decode<Marker>("{}")
}
`,
			code:    "SLK381",
			message: "interfaces cannot be encoded or decoded as JSON",
		},
		{
			name: "class with private field",
			source: `
class Secret {
    Name: string
    hidden: int
}
function main() -> Result<Secret, std.json.Failure> {
    std.json.Decode<Secret>("{\"Name\":\"A\",\"hidden\":1}")
}
`,
			code:    "SLK381",
			message: "has private field hidden",
		},
		{
			name: "non generic source still checks",
			source: `
function main() -> string {
    "ok"
}
`,
			code:    "",
			message: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkResult(t, test.source)
			if test.code == "" {
				assertNoDiagnostics(t, diagnostics)
				return
			}
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestStdJsonQualifiedOptionalAndArrayTypeArguments(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{
			Name:      "models.slk",
			Namespace: "root.models",
			Text: `
class Item {
    Name: string
}
`,
		},
		{
			Name:      "main.slk",
			Namespace: "root",
			Text: `
function DecodeItem(Text: string) -> Result<root.models.Item, std.json.Failure> {
    std.json.Decode<root.models.Item>(Text)
}

function DecodeOptional(Text: string) -> Result<root.models.Item?, std.json.Failure> {
    std.json.Decode<root.models.Item?>(Text)
}

function DecodeArray(Text: string) -> Result<root.models.Item[], std.json.Failure> {
    std.json.Decode<root.models.Item[]>(Text)
}

function main() -> string { "ok" }
`,
		},
	})
	assertNoDiagnostics(t, diagnostics)
}

func TestStdJsonRoundTripAndOptionalBehavior(t *testing.T) {
	source := stdJSONConfig + `
function main() -> string {
    match std.json.Decode<Config>("{\"Name\":\"Slick\",\"Port\":8080,\"Tags\":[\"strict\",\"native\"]}") {
        Ok(Value) => match std.json.Encode<Config>(Value) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`
	output := runResultEverywhere(t, source)
	if output != `{"Name":"Slick","Port":8080,"Tags":["strict","native"]}` {
		t.Fatalf("unexpected round-trip output %q", output)
	}
}

func TestStdJsonPrimitiveSupport(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected string
	}{
		{
			name: "null",
			source: `
function main() -> string {
    match std.json.Decode<null>("null") {
        Ok(_) => "ok"
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "ok",
		},
		{
			name: "bool",
			source: `
function main() -> string {
    match std.json.Decode<bool>("true") {
        Ok(Value) => if (Value) { "yes" } else { "no" }
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "yes",
		},
		{
			name: "float",
			source: `
function main() -> string {
    match std.json.Decode<float>("1.5") {
        Ok(Value) => match std.json.Encode<float>(Value) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "1.5",
		},
		{
			name: "string",
			source: `
function main() -> string {
    match std.json.Decode<string>("\"Ada\"") {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "Ada",
		},
		{
			name: "array order",
			source: `
function main() -> string {
    match std.json.Decode<string[]>("[\"a\",\"b\",\"c\"]") {
        Ok(Value) => match std.json.Encode<string[]>(Value) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`,
			expected: `["a","b","c"]`,
		},
		{
			name: "top-level absent optional encodes null",
			source: `
function main() -> string {
    match std.json.Encode<string?>(null) {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "null",
		},
		{
			name: "int bounds",
			source: `
function main() -> string {
    match std.json.Decode<int>("-9223372036854775808") {
        Ok(Low) => match std.json.Decode<int>("9223372036854775807") {
            Ok(High) => ` + jsonIntTemplate + `
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`,
			expected: "-9223372036854775808:9223372036854775807",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if output := runResultEverywhere(t, test.source); output != test.expected {
				t.Fatalf("expected %q, found %q", test.expected, output)
			}
		})
	}
}

func TestStdJsonNestedClassesAndArrays(t *testing.T) {
	source := `
class Address {
    City: string
    Postcode: string
}

class User {
    Name: string
    Address: Address
    Tags: string[]
}

function main() -> string {
    match std.json.Decode<User[]>("[{\"Name\":\"Ada\",\"Address\":{\"City\":\"London\",\"Postcode\":\"E1\"},\"Tags\":[\"a\",\"b\"]}]") {
        Ok(Users) => match std.json.Encode<User[]>(Users) {
            Ok(Text) => Text
            Err(Failure) => ` + jsonPathTemplate + `
        }
        Err(Failure) => ` + jsonPathTemplate + `
    }
}
`
	output := runResultEverywhere(t, source)
	if !strings.Contains(output, `"Name":"Ada"`) || !strings.Contains(output, `"City":"London"`) || !strings.Contains(output, `"Tags":["a","b"]`) {
		t.Fatalf("unexpected nested output %q", output)
	}
}

func TestStdJsonOptionalFieldAbsence(t *testing.T) {
	source := `
class User {
    Name: string
    Nickname: string?
}

function Describe(Value: User) -> string {
    let Nickname = Value.Nickname
    if (Nickname == null) {
        "absent"
    } else {
        Nickname
    }
}

function Combine(Missing: User, NullField: User, Present: User, EncodedMissing: string, EncodedNull: string) -> string {
    Describe(Missing) + ";" + Describe(NullField) + ";" + Describe(Present) + ";" + EncodedMissing + ";" + EncodedNull
}

function main() -> string {
    match std.json.Decode<User>("{\"Name\":\"Ada\"}") {
        Ok(Missing) => match std.json.Decode<User>("{\"Name\":\"Ada\",\"Nickname\":null}") {
            Ok(NullField) => match std.json.Decode<User>("{\"Name\":\"Ada\",\"Nickname\":\"Addy\"}") {
                Ok(Present) => match std.json.Encode<User>(Missing) {
                    Ok(EncodedMissing) => match std.json.Encode<User>(NullField) {
                        Ok(EncodedNull) => Combine(Missing, NullField, Present, EncodedMissing, EncodedNull)
                        Err(Failure) => Failure.Message
                    }
                    Err(Failure) => Failure.Message
                }
                Err(Failure) => Failure.Message
            }
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`
	output := runResultEverywhere(t, source)
	parts := strings.Split(output, ";")
	if len(parts) != 5 {
		t.Fatalf("unexpected optional behavior %q", output)
	}
	if parts[0] != "absent" || parts[1] != "absent" || parts[2] != "Addy" {
		t.Fatalf("unexpected optional describe %q", output)
	}
	if parts[3] != `{"Name":"Ada"}` || parts[4] != `{"Name":"Ada"}` {
		t.Fatalf("absent optional fields must be omitted, found %q", output)
	}
}

func TestStdJsonQuestionPropagatesFailure(t *testing.T) {
	source := stdJSONConfig + `
function Load(Text: string) -> Result<Config, std.json.Failure> {
    std.json.Decode<Config>(Text)
}

function RoundTrip(Text: string) -> Result<string, std.json.Failure> {
    let Value = Load(Text)?
    std.json.Encode<Config>(Value)
}

function main() -> string {
    match RoundTrip("{") {
        Ok(Text) => Text
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`
	output := runResultEverywhere(t, source)
	if !strings.HasPrefix(output, "Decode|$|") || strings.HasSuffix(output, "|") {
		t.Fatalf("expected populated Decode failure, found %q", output)
	}
}

func TestStdJsonErrIsNotCaughtAsThrow(t *testing.T) {
	output := runResultEverywhere(t, `
function DecodeBad() -> Result<int, std.json.Failure> {
    std.json.Decode<int>("nope")
}

function Recover() -> Result<int, std.json.Failure> {
    DecodeBad() catch (error) {
        std.json.Failure => Ok(0)
    }
}

function main() -> string {
    match Recover() {
        Ok(_) => "caught"
        Err(Failure) => "err"
    }
}
`)
	if output != "err" {
		t.Fatalf("JSON Err must not be caught as a throw, found %q", output)
	}
}

func TestStdJsonDecodeFailures(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		operation string
		path      string
		message   string
	}{
		{
			name: "empty input",
			source: `
function main() -> string {
    match std.json.Decode<int>("") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "unexpected end of JSON input",
		},
		{
			name: "multi value",
			source: `
function main() -> string {
    match std.json.Decode<int>("1 2") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "more than one JSON value",
		},
		{
			name: "duplicate key",
			source: `
class User { Name: string }
function main() -> string {
    match std.json.Decode<User>("{\"Name\":\"A\",\"Name\":\"B\"}") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$.Name",
			message:   "duplicate object key",
		},
		{
			name: "unknown field",
			source: `
class User { Name: string }
function main() -> string {
    match std.json.Decode<User>("{\"Name\":\"A\",\"Age\":1}") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$.Age",
			message:   "unknown field",
		},
		{
			name: "missing required field",
			source: `
class User { Name: string }
function main() -> string {
    match std.json.Decode<User>("{}") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$.Name",
			message:   "missing required field",
		},
		{
			name: "null required field",
			source: `
class User { Name: string }
function main() -> string {
    match std.json.Decode<User>("{\"Name\":null}") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$.Name",
			message:   "expected JSON string",
		},
		{
			name: "wrong primitive",
			source: `
function main() -> string {
    match std.json.Decode<int>("true") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "expected JSON integer",
		},
		{
			name: "fractional int",
			source: `
function main() -> string {
    match std.json.Decode<int>("1.5") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "expected JSON integer without fraction or exponent",
		},
		{
			name: "exponent int",
			source: `
function main() -> string {
    match std.json.Decode<int>("1e2") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "expected JSON integer without fraction or exponent",
		},
		{
			name: "int overflow",
			source: `
function main() -> string {
    match std.json.Decode<int>("9223372036854775808") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$",
			message:   "integer out of int64 range",
		},
		{
			name: "nested array path",
			source: `
class User { Name: string }
function main() -> string {
    match std.json.Decode<User[]>("[{\"Name\":\"A\"},{\"Name\":1}]") {
        Ok(_) => "ok"
        Err(Failure) => ` + jsonFailTemplate + `
    }
}
`,
			operation: "Decode",
			path:      "$[1].Name",
			message:   "expected JSON string",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := runResultEverywhere(t, test.source)
			prefix := test.operation + "|" + test.path + "|"
			if !strings.HasPrefix(output, prefix) || !strings.Contains(output, test.message) {
				t.Fatalf("expected failure %s...%s, found %q", prefix, test.message, output)
			}
		})
	}
}

func TestStdJsonEncodeFiniteFloat(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> string {
    match std.json.Encode<float>(1.25) {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
}
`)
	if output != "1.25" {
		t.Fatalf("expected finite float encoding, found %q", output)
	}
}

func TestStdJsonNativeBinaryNeedsNoInterpreter(t *testing.T) {
	source := stdJSONConfig + `
function main() -> string {
    match std.json.Decode<Config>("{\"Name\":\"Slick\",\"Port\":8080,\"Tags\":[\"strict\",\"native\"]}") {
        Ok(Value) => match std.json.Encode<Config>(Value) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`
	binary := buildStdEnvProgram(t, source)
	output := runStdEnvBinary(t, binary, nil)
	if output != `{"Name":"Slick","Port":8080,"Tags":["strict","native"]}` {
		t.Fatalf("native binary output %q", output)
	}
}

func TestStdJsonExistingStdEnvUnchanged(t *testing.T) {
	diagnostics := checkResult(t, `
function main() -> string? {
    std.env.Get("SLICK_STDLIB_JSON_GUARD")
}
`)
	assertNoDiagnostics(t, diagnostics)
}
