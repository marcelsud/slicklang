package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestArrayLengthAndGetEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
function Empty() -> string[] { [] }
function main() -> string {
    let EmptyValues = Empty()
    let Values = ["first", "middle", "last"]
    let First = Values.Get(0)
    let Last = Values.Get(2)
    let Negative = Values.Get(-1)
    let PastEnd = Values.Get(3)
    if (First == null) {
        "bad"
    } else {
        if (Last == null) {
            "bad"
        } else {
            if (Negative != null || PastEnd != null) {
                "bad"
            } else {
                std.convert.IntToString(EmptyValues.Length()) + ":" +
                    std.convert.IntToString(Values.Length()) + ":" + First + ":" + Last
            }
        }
    }
}
`)
	if output != "0:3:first:last" {
		t.Fatalf("array operations output = %q", output)
	}
}

func TestArrayGetFlattensOptionalElements(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> string {
    let Values = [null, "present"]
    let StoredNull = Values.Get(0)
    let Present = Values.Get(1)
    let OutOfBounds = Values.Get(2)
    if (StoredNull != null || OutOfBounds != null) {
        "bad"
    } else {
        if (Present == null) { "bad" } else { Present }
    }
}
`)
	if output != "present" {
		t.Fatalf("optional array output = %q", output)
	}
}

func TestArraySliceEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
function RenderItems(Items: int[]) -> string {
    let Output = ""
    for Item in Items {
        Output = Output + std.convert.IntToString(Item)
    }
    Output
}
function Render(Value: Result<int[], std.collections.BoundsFailure>) -> string {
    match Value {
        Ok(Items) => RenderItems(Items)
        Err(_) => "bounds"
    }
}
function Nested(Values: int[]) -> Result<int[], std.collections.BoundsFailure> {
    match Values.Slice(1, 4) {
        Ok(Items) => Items.Slice(1, 2)
        Err(Failure) => Err(Failure)
    }
}
function main() -> string {
    let Values = [1, 2, 3, 4]
    Render(Values.Slice(1, 1)) + ";" +
        Render(Values.Slice(0, 4)) + ";" +
        Render(Nested(Values)) + ";" +
        Render(Values.Slice(-1, 1)) + ";" +
        Render(Values.Slice(3, 2)) + ";" +
        Render(Values.Slice(0, 5))
}
`)
	if output != ";1234;3;bounds;bounds;bounds" {
		t.Fatalf("array slices output = %q", output)
	}
}

func TestBufferMutationAndFreezeSnapshotsEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
use std.buffer.New as NewBuffer
use std.buffer.Push as Push
use std.buffer.Get as Get
use std.buffer.Set as Set
use std.buffer.Length as BufferLength
use std.buffer.Freeze as Freeze

function SetStatus(Value: Result<null, std.collections.BoundsFailure>) -> string {
    match Value { Ok(_) => "ok" Err(_) => "bounds" }
}
function main() -> string effects { state } {
    let Values = NewBuffer<string>()
    Push<string>(Values, "A")
    let FirstSnapshot = Freeze<string>(Values)
    let Replaced = Set<string>(Values, 0, "B")
    Push<string>(Values, "C")
    let Rejected = Set<string>(Values, 2, "D")
    let SecondSnapshot = Freeze<string>(Values)
    let First = FirstSnapshot.Get(0)
    let SecondFirst = SecondSnapshot.Get(0)
    let SecondLast = SecondSnapshot.Get(1)
    if (First == null) {
        "bad"
    } else {
        if (SecondFirst == null) {
            "bad"
        } else {
            if (SecondLast == null) {
                "bad"
            } else {
                First + ":" + SecondFirst + SecondLast + ":" +
                    std.convert.IntToString(BufferLength<string>(Values)) + ":" +
                    SetStatus(Replaced) + ":" + SetStatus(Rejected) + ":" +
                    if (Get<string>(Values, -1) == null && Get<string>(Values, 2) == null) { "missing" } else { "bad" }
            }
        }
    }
}
`)
	if output != "A:BC:2:ok:bounds:missing" {
		t.Fatalf("buffer snapshot output = %q", output)
	}
}

func TestBufferAcceptsEverySupportedElementShape(t *testing.T) {
	output := runResultEverywhere(t, `
interface Named { function Label() -> string }
class User implements Named {
    Name: string
    function Label() -> string { self.Name }
}
class Failure implements Error {}
function main() -> string effects { state } {
    let Ints = std.buffer.New<int>()
    std.buffer.Push<int>(Ints, 1)
    let Bools = std.buffer.New<bool>()
    std.buffer.Push<bool>(Bools, true)
    let Floats = std.buffer.New<float>()
    std.buffer.Push<float>(Floats, 1.5)
    let Strings = std.buffer.New<string>()
    std.buffer.Push<string>(Strings, "value")
    let Nulls = std.buffer.New<null>()
    std.buffer.Push<null>(Nulls, null)
    let Bytes = std.buffer.New<bytes>()
    std.buffer.Push<bytes>(Bytes, std.bytes.FromUtf8("x"))
    let Users = std.buffer.New<User>()
    std.buffer.Push<User>(Users, User { Name: "Ada" })
    let NamedValues = std.buffer.New<Named>()
    std.buffer.Push<Named>(NamedValues, User { Name: "Grace" })
    let OptionalValues = std.buffer.New<string?>()
    std.buffer.Push<string?>(OptionalValues, null)
    let Results = std.buffer.New<Result<int, Failure>>()
    std.buffer.Push<Result<int, Failure>>(Results, Ok(1))
    let Arrays = std.buffer.New<int[]>()
    std.buffer.Push<int[]>(Arrays, [1, 2])
    let Maps = std.buffer.New<Map<string, int>>()
    std.buffer.Push<Map<string, int>>(Maps, map { "one": 1 })
    let Pairs = std.buffer.New<(int, string)>()
    for Pair in enumerate(["value"]) {
        std.buffer.Push<(int, string)>(Pairs, Pair)
    }
    "ok"
}
`)
	if output != "ok" {
		t.Fatalf("element-shape program output = %q", output)
	}
}

func TestBufferGetFlattensOptionalElements(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> string effects { state } {
    let Values = std.buffer.New<string?>()
    std.buffer.Push<string?>(Values, null)
    std.buffer.Push<string?>(Values, "present")
    let MissingValue = std.buffer.Get<string?>(Values, 0)
    let PresentValue = std.buffer.Get<string?>(Values, 1)
    let OutOfBounds = std.buffer.Get<string?>(Values, 2)
    if (MissingValue != null || OutOfBounds != null || std.buffer.Length<string?>(Values) != 2) {
        "bad"
    } else {
        if (PresentValue == null) { "bad" } else { PresentValue }
    }
}
`)
	if output != "present" {
		t.Fatalf("optional buffer output = %q", output)
	}
}

func TestCollectionCallsReportFocusedDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"bare buffer type": {
			source:  `function Use(Value: Buffer) -> null { null } function main() -> null { null }`,
			code:    "SLK361",
			message: "Buffer takes 1 type argument, found 0",
		},
		"buffer type arity": {
			source:  `function Use(Value: Buffer<int, string>) -> null { null } function main() -> null { null }`,
			code:    "SLK361",
			message: "Buffer takes 1 type argument, found 2",
		},
		"missing function type argument": {
			source:  `function main() -> null { std.buffer.New() null }`,
			code:    "SLK380",
			message: "std.buffer.New expects 1 type arguments, found 0",
		},
		"extra function type argument": {
			source:  `function main() -> null { std.buffer.New<int, string>() null }`,
			code:    "SLK380",
			message: "std.buffer.New expects 1 type arguments, found 2",
		},
		"push arity": {
			source:  `function main() -> null effects { state } { let Values = std.buffer.New<int>() std.buffer.Push<int>(Values) null }`,
			code:    "SLK320",
			message: "expects 2 arguments, found 1",
		},
		"push value type": {
			source:  `function main() -> null effects { state } { let Values = std.buffer.New<int>() std.buffer.Push<int>(Values, "x") null }`,
			code:    "SLK320",
			message: "argument 2 to std.buffer.Push must be int, found string",
		},
		"array get arity": {
			source:  `function main() -> null { let Values = [1] Values.Get() null }`,
			code:    "SLK320",
			message: "Values.Get expects 1 arguments, found 0",
		},
		"array slice type": {
			source:  `function main() -> null { let Values = [1] Values.Slice("x", 1) null }`,
			code:    "SLK320",
			message: "argument 1 to Values.Slice must be int, found string",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestFormatCollectionSyntax(t *testing.T) {
	source := compiler.Source{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main()->null effects { state } {let Values=std.buffer.New<int>();std.buffer.Push<int>(Values,1);let Frozen=std.buffer.Freeze<int>(Values);Frozen.Slice(0,Frozen.Length());null}`,
	}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format collections: diagnostics=%+v err=%v", diagnostics, err)
	}
	const want = `function main() -> null effects { state } {
    let Values = std.buffer.New<int>()
    std.buffer.Push<int>(Values, 1)
    let Frozen = std.buffer.Freeze<int>(Values)
    Frozen.Slice(0, Frozen.Length())
    null
}
`
	if formatted != want {
		t.Fatalf("formatted collections:\n%s\nwant:\n%s", formatted, want)
	}
}

func TestBufferRemainsOpaque(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> Buffer<int> effects { state } {
    let Values = std.buffer.New<int>()
    std.buffer.Push<int>(Values, 1)
    Values
}
`)
	if output != "Buffer" {
		t.Fatalf("opaque buffer output = %q", output)
	}
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> bool { std.buffer.New<int>() == std.buffer.New<int>() }`,
	}})
	assertDiagnostic(t, diagnostics, "SLK342", "cannot compare Buffer<int> with Buffer<int>")
}
