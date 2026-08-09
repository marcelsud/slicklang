package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestTupleConstructionAndDestructuringEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
function Advance(Index: int) -> (string, int) {
    ("token", Index + 1)
}
function main() -> string {
    let (Token, Next) = Advance(4)
    Token + ":" + std.convert.IntToString(Next)
}
`)
	if output != "token:5" {
		t.Fatalf("tuple output = %q", output)
	}
}

func TestTupleGroupingNestingAndBlankDestructuringEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> string {
    let Pair = ("Ada", (37, true))
    let (Name, Inner) = Pair
    let (Age, _) = Inner
    Name + ":" + std.convert.IntToString(Age)
}
`)
	if output != "Ada:37" {
		t.Fatalf("nested tuple output = %q", output)
	}
}

func TestTupleDestructuringAfterOptionalHandlingEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
function main() -> string {
    let Entry = std.text.Cut("Ada:37", ":")
    if (Entry == null) {
        "missing"
    } else {
        let (Name, Age) = Entry
        Name + Age
    }
}
`)
	if output != "Ada37" {
		t.Fatalf("optional tuple output = %q", output)
	}
}

func TestTupleDiagnosticsAreFocused(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name:    "destructuring arity",
			source:  `function main() -> null { let (A, B) = (1, 2, 3) null }`,
			code:    "SLK346",
			message: "let has 2 bindings",
		},
		{
			name:    "duplicate binding",
			source:  `function main() -> null { let (A, A) = (1, 2) null }`,
			code:    "SLK342",
			message: "duplicate destructuring binding A",
		},
		{
			name:    "element type mismatch",
			source:  `function main() -> (int, string) { ("wrong", 1) }`,
			code:    "SLK342",
			message: "tuple element 1 must be int",
		},
		{
			name:    "trailing comma",
			source:  `function main() -> null { (1, 2,) }`,
			code:    "SLK001",
			message: "does not allow a trailing comma",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestTupleFormatterIsExactAndIdempotent(t *testing.T) {
	source := compiler.Source{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main()->(string,int){let(A,B)=("Ada",37) A+std.convert.IntToString(B)}`,
	}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format tuple: diagnostics=%+v err=%v", diagnostics, err)
	}
	const want = `function main() -> (string, int) {
    let (A, B) = ("Ada", 37)
    A + std.convert.IntToString(B)
}
`
	if formatted != want {
		t.Fatalf("formatted tuple:\n%s\nwant:\n%s", formatted, want)
	}
	second, diagnostics, err := compiler.Format(compiler.Source{Name: source.Name, Namespace: source.Namespace, Text: formatted})
	if err != nil || len(diagnostics) != 0 || second != formatted {
		t.Fatalf("second tuple format changed output: diagnostics=%+v err=%v\n%s", diagnostics, err, second)
	}
}
func TestTupleTypesFlowThroughStorageShapesEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
class Box {
    Pair: (string, int)
}
class Failure implements Error {}
function Show(Value: (string, int)) -> string {
    let (Name, Number) = Value
    Name + std.convert.IntToString(Number)
}
function Maybe() -> (string, int)? {
    ("optional", 2)
}
function MakeResult() -> Result<(string, int), Failure> {
    Ok(("result", 3))
}
function main() -> string {
    let Value = Box { Pair: ("field", 1) }
    let Values = [("array", 4)]
    let ArrayText = ""
    for Name, Number in Values {
        ArrayText = Name + std.convert.IntToString(Number)
    }
    let Entries = map { "key": ("map", 5) }
    let Found = Entries.Get("key")
    let MapText = if (Found == null) {
        "missing"
    } else {
        let (Name, Number) = Found
        Name + std.convert.IntToString(Number)
    }
    let Optional = Maybe()
    let OptionalText = if (Optional == null) {
        "missing"
    } else {
        let (Name, Number) = Optional
        Name + std.convert.IntToString(Number)
    }
    let ResultText = match MakeResult() {
        Ok(Pair) => Show(Pair)
        Err(_) => "error"
    }
    Show(Value.Pair) + "|" + ArrayText + "|" + MapText + "|" + OptionalText + "|" + ResultText
}
`)
	if output != "field1|array4|map5|optional2|result3" {
		t.Fatalf("tuple storage output = %q", output)
	}
}
