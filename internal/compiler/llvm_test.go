package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestMatrixOperatorsMatchGo(t *testing.T) {
	program := `
function main() -> string {
    let Value = 10
    let Difference = Value - 3
    let Product = Difference * 5 - 5
    let Ordered = Difference < Product
    let Logic = !false && (Difference <= Product || false)
    let Negative = -Value
    let Grouped = (Value - 3) * 2
    ` + "`difference=${Difference}; product=${Product}; ordered=${Ordered}; logic=${Logic}; negative=${Negative}; grouped=${Grouped}`" + `
}
`
	if output := runOnEveryEngineSource(t, program); !strings.Contains(output, "difference=7") {
		t.Fatalf("unexpected output %q", output)
	}
}

func TestMatrixAsyncMethodAndCaughtFailure(t *testing.T) {
	program := `
class Box {
    Value: int
    function Get() -> int { self.Value }
}

class ChildFailure implements Error { Message: string }

function Fail() -> int throws ChildFailure {
    throw ChildFailure { Message: "caught" }
}

function main() -> string {
    let BoxValue = Box { Value: 42 }
    async let MethodJob = BoxValue.Get()
    async let FailureJob = Fail()
    let MethodValue = await MethodJob
    let FailureValue = await FailureJob catch {
        ChildFailure as Failure => 0
    }
    let Total = MethodValue + FailureValue
    ` + "`${Total}`" + `
}
`
	if output := runOnEveryEngineSource(t, program); output != "42" {
		t.Fatalf("unexpected output %q", output)
	}
}

func TestLLVMSelectionDoesNotChangeGoDefault(t *testing.T) {
	if got, err := compiler.ParseBackend(""); err != nil || got != compiler.BackendGo {
		t.Fatalf("empty backend: %q %v", got, err)
	}
	if _, err := compiler.ParseBackend("tinygo"); err == nil {
		t.Fatal("accepted unknown backend")
	}
}

func TestMatrixOptionalStorageAndEmptyInterfaceParity(t *testing.T) {
	source := `
interface Marker {}

class Item implements Marker {
    Name: string
}

function Maybe() -> Item? {
    Item { Name: "Ada" }
}

function Promote(Value: Marker?) -> string {
    if (Value == null) { "missing" } else { "present" }
}

function main() -> string {
    let Value = Maybe()
    let First = if (Value == null) { "missing" } else { Value.Name }
    let Second = if (Value == null) { "missing" } else { Value.Name }
    let Promoted = if (Value == null) { "missing" } else { Promote(Value) }
    First + "|" + Second + "|" + Promoted
}
`
	if output := runOnEveryEngineSource(t, source); output != "Ada|Ada|present" {
		t.Fatalf("optional/interface output = %q", output)
	}
}

func TestMatrixWrappingIntegersAndShortestFloatsMatch(t *testing.T) {
	source := `
function main() -> string {
    let Maximum = 9223372036854775807
    let Minimum = Maximum + 1
    let Negated = -Minimum
    let Multiplied = Maximum * 2
    let Fraction = 0.1
    let Converted = std.convert.FloatToString(Fraction)
    ` + "`${Minimum}|${Negated}|${Multiplied}|${Fraction}|${Converted}`" + `
}
`
	if output := runOnEveryEngineSource(t, source); output != "-9223372036854775808|-9223372036854775808|-2|0.1|0.1" {
		t.Fatalf("numeric boundary output = %q", output)
	}
}

func TestMatrixTextOperationsPreserveEmbeddedNULBytes(t *testing.T) {
	source := `
function main() -> string {
    let Value = "a\u0000b\u0000c"
    let Parts = std.text.Split(Value, "\u0000")
    let Joined = std.text.Join(Parts, "\u0000")
    let Cut = std.text.Cut(Value, "\u0000")
    let After = if (Cut == null) {
        "missing"
    } else {
        let (_, Suffix) = Cut
        Suffix
    }
    let Contains = std.text.Contains(Value, "\u0000b")
    let Starts = std.text.StartsWith(Value, "a\u0000")
    let Ends = std.text.EndsWith(Value, "\u0000c")
    let Equal = Joined == Value
    let Length = Parts.Length()
    let SuffixEqual = After == "b\u0000c"
    ` + "`${Contains}|${Starts}|${Ends}|${Equal}|${Length}|${SuffixEqual}`" + `
}
`
	if output := runOnEveryEngineSource(t, source); output != "true|true|true|true|3|true" {
		t.Fatalf("embedded-NUL text output = %q", output)
	}
}

func TestMatrixAsyncArgumentsBeyondEightValuesMatch(t *testing.T) {
	source := `
function AddTen(A: int, B: int, C: int, D: int, E: int, F: int, G: int, H: int, I: int, J: int) -> int {
    A + B + C + D + E + F + G + H + I + J
}

function main() -> string {
    async let Work = AddTen(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
    let Total = await Work
    ` + "`${Total}`" + `
}
`
	if output := runOnEveryEngineSource(t, source); output != "55" {
		t.Fatalf("async argument output = %q", output)
	}
}

func TestLLVMJanssonDependencyFollowsSemanticJSONUse(t *testing.T) {
	engine := mustExecutionEngine(t, "llvm")
	t.Setenv("SLICK_JANSSON_ROOT", filepath.Join(t.TempDir(), "missing"))
	root := t.TempDir()
	plainSource := filepath.Join(root, "plain.slk")
	if err := os.WriteFile(plainSource, []byte(`function main() -> string { "ok" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if diagnostics, err := compiler.BuildPathWithOptions(
		plainSource, filepath.Join(root, "plain"), engineBuildOptions(engine, ""),
	); err != nil || len(diagnostics) != 0 {
		t.Fatalf("non-JSON LLVM build required Jansson: diagnostics=%v err=%v", diagnostics, err)
	}

	jsonSource := filepath.Join(root, "json.slk")
	if err := os.WriteFile(jsonSource, []byte(`
function main() -> string {
    match std.json.Encode<int>(1) {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "json")
	if err := os.WriteFile(output, []byte("existing output"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := compiler.BuildPathWithOptions(jsonSource, output, engineBuildOptions(engine, ""))
	if err == nil || !strings.Contains(err.Error(), "libjansson") {
		t.Fatalf("JSON LLVM build error = %v, want missing libjansson", err)
	}
	unchanged, readErr := os.ReadFile(output)
	if readErr != nil || string(unchanged) != "existing output" {
		t.Fatalf("failed LLVM build changed output: contents=%q err=%v", unchanged, readErr)
	}
}

func TestLLVMIncompatibleToolchainLeavesNoPartialOutput(t *testing.T) {
	engine := mustExecutionEngine(t, "llvm")
	tools := t.TempDir()
	for _, name := range []string{"llvm-as-18", "llc-18"} {
		path := filepath.Join(tools, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'LLVM version 17.0.0'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SLICK_LLVM_BIN", tools)

	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	if err := os.WriteFile(source, []byte(`function main() -> string { "ok" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "app")
	_, err := compiler.BuildPathWithOptions(source, output, engineBuildOptions(engine, ""))
	if err == nil || !strings.Contains(err.Error(), "major version 18 is required") {
		t.Fatalf("incompatible LLVM build error = %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("incompatible LLVM build created output: %v", statErr)
	}
}
