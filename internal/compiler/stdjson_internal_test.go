package compiler

import (
	"go/format"
	"strings"
	"testing"
)

func TestStdJsonSyntheticDeclarations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root.app",
		Text: `
use std.json.Decode as DecodeJson
use std.json.Failure as JsonFailure

class Config {
    Name: string
}

function Load(Text: string) -> Result<Config, JsonFailure> {
    DecodeJson<Config>(Text)
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	decode := program.functions["std.json.Decode"]
	assertStandardFunction(t, decode, "std.json", "Decode", []string{"string"}, "Result<T,std.json.Failure>", nativeStdJsonDecode)
	if len(decode.typeParams) != 1 || decode.typeParams[0] != "T" {
		t.Fatalf("Decode type params = %+v", decode.typeParams)
	}
	encode := program.functions["std.json.Encode"]
	assertStandardFunction(t, encode, "std.json", "Encode", []string{"T"}, "Result<string,std.json.Failure>", nativeStdJsonEncode)
	if len(encode.typeParams) != 1 || encode.typeParams[0] != "T" {
		t.Fatalf("Encode type params = %+v", encode.typeParams)
	}

	failure := program.classes[stdJsonFailureName]
	if failure == nil {
		t.Fatal("std.json.Failure was not registered")
	}
	if failure.namespace != "std.json" || failure.name != "Failure" || !failure.isError {
		t.Fatalf("unexpected Failure declaration: %+v", failure)
	}
	for _, name := range []string{"Operation", "Path", "Message"} {
		if _, ok := failure.fields[name]; !ok {
			t.Fatalf("Failure missing field %s", name)
		}
	}

	load := program.functions["root.app.Load"]
	if load == nil {
		t.Fatal("Load was not registered")
	}
	if resolved := program.resolveFunction(load, "DecodeJson"); resolved != decode {
		t.Fatalf("DecodeJson resolved to %+v", resolved)
	}
}

func TestStdJsonGeneratedSourceIsDeterministicAndScoped(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
class Config {
    Name: string
    Port: int
    Tags: string[]
}

function once(Text: string) -> Result<Config, std.json.Failure> {
    std.json.Decode<Config>(Text)
}

function twice(Text: string) -> Result<Config, std.json.Failure> {
    std.json.Decode<Config>(Text)
}

function dump(Value: Config) -> Result<string, std.json.Failure> {
    std.json.Encode<Config>(Value)
}

function main() -> string {
    match once("{\"Name\":\"A\",\"Port\":1,\"Tags\":[]}") {
        Ok(Value) => match dump(Value) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	first, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	second, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go again: %v", err)
	}
	if first != second {
		t.Fatal("generated Go source is not deterministic")
	}
	if _, err := format.Source([]byte(first)); err != nil {
		t.Fatalf("generated Go is not formattable: %v\n%s", err, first)
	}
	if !strings.Contains(first, "encoding/json") {
		t.Fatal("JSON program must import encoding/json")
	}
	if strings.Count(first, "func slickJSONDecode_") != 1 {
		t.Fatalf("Decode helper should be emitted once\n%s", first)
	}
	if strings.Count(first, "func slickJSONEncode_") != 1 {
		t.Fatalf("Encode helper should be emitted once\n%s", first)
	}

	hello, helloDiags := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { "hello" }`,
	}})
	requireNoDiagnostics(t, helloDiags)
	generated, err := hello.generateGo()
	if err != nil {
		t.Fatalf("generate hello Go: %v", err)
	}
	for _, banned := range []string{"encoding/json", "slickJSON"} {
		if strings.Contains(generated, banned) {
			t.Fatalf("non-JSON program emitted %q", banned)
		}
	}
}

func TestStdJsonInvalidUTF8Decode(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { "ok" }`,
	}})
	requireNoDiagnostics(t, diagnostics)
	invalid := string([]byte{0xff, 0xfe})
	result := program.runtimeJSONDecode("string", invalid)
	if result.result == nil || result.result.ok {
		t.Fatalf("expected Decode Err for invalid UTF-8, got %+v", result)
	}
	failure := result.result.payload
	if failure.fields["Operation"].scalar != "Decode" || failure.fields["Path"].scalar != "$" {
		t.Fatalf("unexpected failure %+v", failure.fields)
	}
	message, _ := failure.fields["Message"].scalar.(string)
	if message == "" || !strings.Contains(message, "UTF-8") {
		t.Fatalf("unexpected message %q", message)
	}
}
