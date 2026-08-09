package compiler_test

import (
	"strings"
	"testing"

	"slick/internal/compiler"
)

const operatorTraceSupport = `
function ResetTrace() -> null {
    match std.env.Unset("SLICK_OPERATOR_TEST") {
        Ok(_) => null
        Err(_) => null
    }
}

function StoreTrace(Value: string) -> null {
    match std.env.Set("SLICK_OPERATOR_TEST", Value) {
        Ok(_) => null
        Err(_) => null
    }
}

function AppendTrace(Value: string) -> null {
    let Current = std.env.Get("SLICK_OPERATOR_TEST")
    if (Current == null) {
        StoreTrace(Value)
    } else {
        StoreTrace(Current + Value)
    }
}

function Trace() -> string {
    let Current = std.env.Get("SLICK_OPERATOR_TEST")
    if (Current == null) { "" } else { Current }
}

function MarkInt(Value: int, Mark: string) -> int {
    AppendTrace(Mark)
    Value
}

function MarkBool(Value: bool, Mark: string) -> bool {
    AppendTrace(Mark)
    Value
}
`

func TestOperatorsMatchAcrossBackendsAndPreserveEvaluationOrder(t *testing.T) {
	source := operatorTraceSupport + `
function main() -> string {
    ResetTrace()
    let Arithmetic = MarkInt(20, "A") - MarkInt(3, "B") * MarkInt(4, "C") - MarkInt(1, "D")
    let Grouped = (20 - 3) * 4
    let GroupedDifference = 10 - (3 - 1)
    let GroupedBoolean = (true || false) && false
    let Float = -(1.5 * 2.0)
    let Value = 3
    let VariableNegative = -Value
    let CallNegative = -MarkInt(2, "E")
    let Nested = !!MarkBool(true, "F")
    let Ordered = 1 + 2 * 3 < 8 == true
    let AllOrderings = 4 <= 4 && 5 > 4 && 5 >= 5
    let Boolean = false || true && !false
    let SkippedAnd = false && MarkBool(true, "X")
    let SkippedOr = true || MarkBool(false, "Y")
    if (Arithmetic == 7 && Grouped == 68 && GroupedDifference == 8 && !GroupedBoolean && Float == -3.0 && VariableNegative == -3 && CallNegative == -2 && Nested && Ordered && AllOrderings && Boolean && !SkippedAnd && SkippedOr) {
        "7|68|" + Trace()
    } else {
        "wrong"
    }
}
`
	if output := runResultEverywhere(t, source); output != "7|68|ABCDEF" {
		t.Fatalf("operator output %q", output)
	}
}

func TestOperatorTypeDiagnosticsAreFocused(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		code    string
		message string
	}{
		{name: "unary minus", expr: `-true`, code: "SLK342", message: "unary operator - does not accept bool"},
		{name: "boolean negation", expr: `!1`, code: "SLK342", message: "unary operator ! does not accept int"},
		{name: "subtraction", expr: `"a" - "b"`, code: "SLK342", message: "operator - does not accept string and string"},
		{name: "multiplication", expr: `1 * 2.0`, code: "SLK342", message: "operator * does not accept int and float"},
		{name: "ordering", expr: `"a" < "b"`, code: "SLK342", message: "operator < does not accept string and string"},
		{name: "boolean", expr: `1 && true`, code: "SLK342", message: "operator && does not accept int and bool"},
		{name: "chained ordering", expr: `1 < 2 < 3`, code: "SLK342", message: "operator < does not accept bool and int"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkResult(t, "function main() -> null { let Invalid = "+test.expr+" null }")
			if len(diagnostics) != 1 {
				t.Fatalf("diagnostics=%+v", diagnostics)
			}
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestOptionalOperatorsRequireNarrowing(t *testing.T) {
	declarations := `function Maybe() -> int? { 2 }
`
	diagnostics := checkResult(t, declarations+`function main() -> null { let Value = Maybe() let Invalid = Value * 2 null }`)
	if len(diagnostics) != 1 {
		t.Fatalf("optional diagnostics=%+v", diagnostics)
	}
	assertDiagnostic(t, diagnostics, "SLK372", "operator * does not accept int? and int")
	assertDiagnostic(t, diagnostics, "SLK372", "may be null; compare it with null")

	assertNoDiagnostics(t, checkResult(t, declarations+`
function main() -> int {
    let Value = Maybe()
    if (Value != null) { Value * 2 } else { 0 }
}
`))
}

func TestBooleanRightOperandEffectsRemainChecked(t *testing.T) {
	diagnostics := checkResult(t, `
class Failure implements Error {}
function Risky() -> bool throws Failure { throw Failure {} }
function main() -> bool { false && Risky() }
`)
	assertDiagnostic(t, diagnostics, "SLK201", "unhandled Failure")
}

func TestBooleanRightOperandPreservesCheckedErrorFlow(t *testing.T) {
	source := `
class Failure implements Error {}
function Risky() -> bool throws Failure { throw Failure {} }
function main() -> string {
    if ((false || Risky()) catch (error) { Failure => false }) {
        "wrong"
    } else {
        "caught"
    }
}
`
	if output := runResultEverywhere(t, source); output != "caught" {
		t.Fatalf("checked right operand output %q", output)
	}
}

func TestMissingOperatorOperandProducesOneDiagnostic(t *testing.T) {
	tests := map[string]struct {
		source  string
		message string
	}{
		"binary":       {source: `function main() -> bool { true && }`, message: "expected expression after &&"},
		"nested unary": {source: `function main() -> bool { true && ! }`, message: "expected expression after unary !"},
		"unary":        {source: `function main() -> int { - }`, message: "expected expression after unary -"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := checkResult(t, test.source)
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, test.message) {
				t.Fatalf("diagnostics=%+v", diagnostics)
			}
		})
	}
}

func TestFormatOperatorsCanonicallyAndIdempotently(t *testing.T) {
	source := compiler.Source{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main()->bool{let A=-(1+2)*3;let Grouped=(1+2);let Generic=std.json.Decode<int>("1");1+2<4==true&&!!false||A>0}`,
	}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format operators: diagnostics=%+v err=%v", diagnostics, err)
	}
	const want = `function main() -> bool {
    let A = -(1 + 2) * 3
    let Grouped = (1 + 2)
    let Generic = std.json.Decode<int>("1")
    1 + 2 < 4 == true && !!false || A > 0
}
`
	if formatted != want {
		t.Fatalf("formatted operators:\n%s\nwant:\n%s", formatted, want)
	}
	source.Text = formatted
	second, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 || second != formatted {
		t.Fatalf("second format changed output: diagnostics=%+v err=%v\n%s", diagnostics, err, second)
	}
}

func TestHighlightClassifiesWholeOperatorsWithoutChangingSource(t *testing.T) {
	source := `-Value * 2 <= 4 && !Ready || Value >= 1`
	operators := map[string]bool{"-": false, "*": false, "<=": false, "&&": false, "!": false, "||": false, ">=": false}
	var rebuilt strings.Builder
	for _, token := range compiler.Highlight(source) {
		rebuilt.WriteString(token.Text)
		if _, ok := operators[token.Text]; ok {
			if token.Class != compiler.ClassPunct {
				t.Fatalf("operator %q classified as %q", token.Text, token.Class)
			}
			operators[token.Text] = true
		}
	}
	if rebuilt.String() != source {
		t.Fatalf("highlight rebuilt %q", rebuilt.String())
	}
	for operator, found := range operators {
		if !found {
			t.Errorf("operator %q was not highlighted as one token", operator)
		}
	}
}
