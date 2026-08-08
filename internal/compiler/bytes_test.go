package compiler_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestBytesContracts(t *testing.T) {
	source := `
function Decode(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Text) => Text
        Err(Failure) => Failure.Message
    }
}
function Empty() -> bytes? { std.bytes.FromUtf8("") }
function Missing() -> bytes? { null }
function main() -> string {
    let Prefix = std.bytes.FromUtf8("A")
    let Value = std.bytes.FromUtf8("é界")
    let Joined = std.bytes.Concat([Prefix, Value])
    let First = std.bytes.At(Joined, 0)
    let Last = std.bytes.At(Joined, 5)
    let Negative = std.bytes.At(Joined, -1)
    let Past = std.bytes.At(Joined, 6)
    let Equal = Value == std.bytes.FromUtf8("é界")
    let PrefixUnchanged = Prefix == std.bytes.FromUtf8("A")
    let EmptyValue = Empty()
    let MissingValue = Missing()
    let EmptyPresent = EmptyValue != null
    let MissingAbsent = MissingValue == null
    let Text = Decode(Joined)
    let Length = std.bytes.Length(Joined)
` + "    `${Text};${Length};${First};${Last};${Negative};${Past};${Equal};${PrefixUnchanged};${EmptyPresent};${MissingAbsent};${Joined}`\n" + `}
`
	output, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	if err != nil {
		t.Fatalf("run bytes contracts: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	const expected = "Aé界;6;65;140;;;true;true;true;true;bytes[6]"
	if output != expected {
		t.Fatalf("expected %q, found %q", expected, output)
	}
	assertNativeOutput(t, source, expected)
}

func TestBytesHaveNoImplicitConversions(t *testing.T) {
	tests := map[string]struct {
		source string
		code   string
	}{
		"string to bytes": {`function Need(Value: bytes) -> null { null } function main() -> null { Need("x") }`, "SLK320"},
		"array to bytes":  {`function Need(Value: bytes) -> null { null } function main() -> null { Need([1]) }`, "SLK320"},
		"bytes to string": {`function main() -> string { std.bytes.FromUtf8("x") }`, "SLK340"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			assertDiagnostic(t, diagnostics, test.code, "")
		})
	}
}

func TestStdBytesExactAliases(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
use std.bytes.FromUtf8 as Encode
use std.bytes.ToUtf8 as Decode
use std.bytes.Utf8Failure as DecodeFailure
function Convert(Text: string) -> Result<string, DecodeFailure> { Decode(Encode(Text)) }
function main() -> string { match Convert("ok") { Ok(Text) => Text Err(Failure) => Failure.Message } }
`,
	}})
	assertNoDiagnostics(t, diagnostics)
}

func assertNativeOutput(t *testing.T, source, expected string) {
	t.Helper()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.slk")
	writeSource(t, sourcePath, source)
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(sourcePath, binary)
	if err != nil {
		t.Fatalf("build native contract: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run native contract: %v: %s", err, output)
	}
	if actual := strings.TrimSuffix(string(output), "\n"); actual != expected {
		t.Fatalf("native output: expected %q, found %q", expected, actual)
	}
}
