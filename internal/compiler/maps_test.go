package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestMapContracts(t *testing.T) {
	source := `
function DynamicKey() -> string { "Ada" }
function Empty() -> Map<string, int> { map {} }
function MaybeValues() -> Map<string, string?> { map { "Ada": null, "Grace": "ok" } }
function main() -> string {
    let Base = map { "Ada": 1, DynamicKey(): 2, "Grace": 3 }
    let Updated = Base.With("Linus", 4)
    let Replaced = Updated.With("Ada", 5)
    let Removed = Replaced.Without("Grace")
    let MissingRemoved = Base.Without("Nobody")
    let Ada = Base.Get("Ada")
    let HasAda = Base.Contains("Ada")
    let HasNobody = Base.Contains("Nobody")
    let OriginalLength = Base.Length()
    let UpdatedLength = Updated.Length()
    let RemovedLength = Removed.Length()
    let Maybe = MaybeValues()
    let StoredAbsent = Maybe.Get("Ada")
    let Missing = Maybe.Get("Nobody")
    let ContainsStored = Maybe.Contains("Ada")
    let ContainsMissing = Maybe.Contains("Nobody")
    let Zero = map { "zero": 0 }
    let ZeroValue = Zero.Get("zero")
    let Flags = map { "off": false }
    let FalseValue = Flags.Get("off")
    let Texts = map { "empty": "" }
    let EmptyText = Texts.Get("empty")
    let EmptyMap = Empty()
    let EmptyLength = EmptyMap.Length()
    let Pairs = ""
    for Name, Age in Removed { Pairs = Pairs + ` + "`${Name}=${Age};`" + ` }
    let Tuples = ""
    for Entry in Base { Tuples = Tuples + ` + "`${Entry}|`" + ` }
` + "    `${Ada};${HasAda};${HasNobody};${OriginalLength};${UpdatedLength};${RemovedLength};${ContainsStored};${ContainsMissing};${StoredAbsent};${Missing};${ZeroValue};${FalseValue};${EmptyText};${EmptyLength};${Pairs};${Tuples};${Base};${Removed};${MissingRemoved}`\n" + `}
`
	output, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	if err != nil {
		t.Fatalf("run map contracts: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	const expected = "2;true;false;2;3;2;true;false;;;0;false;;0;Ada=5;Linus=4;;(Ada, 2)|(Grace, 3)|;map {Ada: 2, Grace: 3};map {Ada: 5, Linus: 4};map {Ada: 2, Grace: 3}"
	if output != expected {
		t.Fatalf("expected %q, found %q", expected, output)
	}
	assertNativeOutput(t, source, expected)
}

func TestMapsWorkInStructuralStorage(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
class Failure implements Error { Message: string }
class Holder { Values: Map<string, int> }
function Identity(Value: Map<string, int>) -> Map<string, int> { Value }
function Maybe(Value: Map<string, int>) -> Map<string, int>? { Value }
function Outcome() -> Result<Map<string, int>, Failure> { Ok(map { "Ada": 37 }) }
function main() -> string {
    let Values = Identity(map { "Ada": 37 })
    let Record = Holder { Values: Values }
    let Array = [Values, Record.Values]
    let Optional = Maybe(Values)
    let Result = Outcome()
    "ok"
}
`,
	}})
	assertNoDiagnostics(t, diagnostics)
}

func TestMapDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"bare map type":      {`function main(Value: Map) -> null { null }`, "SLK361", "Map takes 2 type arguments"},
		"map arity":          {`function main(Value: Map<string>) -> null { null }`, "SLK361", "Map takes 2 type arguments"},
		"float key":          {`function main(Value: Map<float, int>) -> null { null }`, "SLK361", "Map key type must be string, int, or bool"},
		"bytes key":          {`function main(Value: Map<bytes, int>) -> null { null }`, "SLK361", "Map key type must be string, int, or bool"},
		"optional key":       {`function main(Value: Map<string?, int>) -> null { null }`, "SLK361", "Map key type must be string, int, or bool"},
		"nested map key":     {`function main(Value: Map<Map<string, int>, int>) -> null { null }`, "SLK361", "Map key type must be string, int, or bool"},
		"ambiguous empty":    {`function main() -> null { let Values = map {} null }`, "SLK382", "empty map literal needs a known"},
		"literal float key":  {`function main() -> null { let Values = map { 1.5: 1 } null }`, "SLK383", "Map key type must be string, int, or bool"},
		"mixed keys":         {`function main() -> null { let Values = map { "a": 1, 2: 2 } null }`, "SLK342", "map keys must share one type"},
		"mixed values":       {`function main() -> null { let Values = map { "a": 1, "b": "two" } null }`, "SLK342", "map values must share one type"},
		"static duplicate":   {`function main() -> null { let Values = map { "a": 1, "a": 2 } null }`, "SLK384", "duplicate static map key"},
		"comparison":         {`function main() -> bool { let Values = map { "a": 1 } Values == Values }`, "SLK342", "cannot compare Map"},
		"loop binding arity": {`function main() -> null { let Values = map { "a": 1 } for A, B, C in Values {} null }`, "SLK346", "loop has 3 bindings"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}
