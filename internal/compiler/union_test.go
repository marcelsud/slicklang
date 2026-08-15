package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// unionShapes declares the union the contract tests share: a fieldless
// variant, a single-field variant, a multi-field variant, and a recursive one.
const unionShapes = `
class Label {
    Text: string
}

class Failure implements Error {
    Message: string
}

union Shape {
    Empty
    Circle(Radius: float)
    Rect(Width: int, Height: int)
    Grouped(Inner: Shape, Tag: Label)
}

function Describe(Value: Shape) -> string {
    match Value {
        Shape.Empty => "empty"
        Shape.Circle(Radius) => "circle"
        Shape.Rect(Width, Height) => "rect"
        Shape.Grouped(Inner, Tag) => DescribeGroup(Inner, Tag)
    }
}

function DescribeGroup(Inner: Shape, Tag: Label) -> string {
    let Nested = Describe(Inner)
    ` + "`${Tag.Text}(${Nested})`" + `
}
`

// TestUnionValuesFlowThroughEveryStorage constructs variants in a local, a
// field, an argument, a return, an array, a map, an optional, and a Result,
// and asserts both backends observe the same values.
func TestUnionValuesFlowThroughEveryStorage(t *testing.T) {
	// runResultEverywhere runs the interpreter and a native binary and fails
	// unless both produce the same output.
	output := runResultEverywhere(t, unionShapes+`
class Holder {
    Value: Shape
    Maybe: Shape?
}

function Wrap(Value: Shape) -> Result<Shape, Failure> {
    Ok(Value)
}

function main() -> string {
    let Local = Shape.Grouped(Shape.Circle(1.5), Label { Text: "outer" })
    let Held = Holder { Value: Shape.Empty, Maybe: Shape.Rect(2, 3) }
    let Items = [Shape.Empty, Shape.Circle(2.0)]
    let Table = map { "one": Shape.Rect(1, 1) }
    let Maybe = Held.Maybe
    let FromMap = Table.Get("one")
    let FromArray = Items.Get(1)
    let Wrapped = Wrap(Held.Value)
    let First = Describe(Local)
    let Second = if (Maybe != null) { Describe(Maybe) } else { "none" }
    let Third = if (FromMap != null) { Describe(FromMap) } else { "none" }
    let Fourth = if (FromArray != null) { Describe(FromArray) } else { "none" }
    let Fifth = match Wrapped {
        Ok(Value) => Describe(Value)
        Err(Reason) => Reason.Message
    }
    `+"`${First};${Second};${Third};${Fourth};${Fifth};${Local}`"+`
}
`)
	want := "outer(circle);rect;rect;circle;empty;Grouped(Circle(1.5), root.Label)"
	if output != want {
		t.Fatalf("union storage output = %q, want %q", output, want)
	}
}

// TestUnionMatchEvaluatesScrutineeOnceAndOnlySelectedArm records every
// evaluation in a buffer, so a second scrutinee evaluation or an unselected arm
// would show up as an extra entry.
func TestUnionMatchEvaluatesScrutineeOnceAndOnlySelectedArm(t *testing.T) {
	output := runResultEverywhere(t, unionShapes+`
function Tap(Log: Buffer<string>, Name: string, Value: Shape) -> Shape effects { state } {
    std.buffer.Push<string>(Log, Name)
    Value
}

function main() -> string effects { state } {
    let Log = std.buffer.New<string>()
    let Selected = match Tap(Log, "scrutinee", Shape.Circle(1.0)) {
        Shape.Empty => Tap(Log, "empty", Shape.Empty)
        Shape.Circle(Radius) => Tap(Log, "circle", Shape.Empty)
        Shape.Rect(Width, Height) => Tap(Log, "rect", Shape.Empty)
        Shape.Grouped(Inner, Tag) => Tap(Log, "grouped", Shape.Empty)
    }
    let Count = std.buffer.Length<string>(Log)
    let Entries = std.buffer.Freeze<string>(Log)
    let Chosen = Describe(Selected)
    `+"`${Count};${Entries};${Chosen}`"+`
}
`)
	want := "2;[scrutinee, circle];empty"
	if output != want {
		t.Fatalf("match evaluation trace = %q, want %q", output, want)
	}
}

// TestUnionMatchWildcardAndIgnoredPayload covers the two shorthands: a _ arm
// closing the remaining variants and _ in a payload position.
func TestUnionMatchWildcardAndIgnoredPayload(t *testing.T) {
	output := runResultEverywhere(t, unionShapes+`
function Kind(Value: Shape) -> int {
    match Value {
        Shape.Rect(Width, _) => Width
        _ => 0
    }
}

function Name(Value: Shape) -> string {
    match Value {
        Shape.Circle(_) => "circle"
        _ => "other"
    }
}

function Anything(Value: Shape) -> string {
    match Value {
        _ => "any"
    }
}

function main() -> string {
    let Sized = Kind(Shape.Rect(7, 9))
    let Fallback = Kind(Shape.Empty)
    let Circle = Name(Shape.Circle(1.0))
    let Other = Name(Shape.Empty)
    let Every = Anything(Shape.Rect(1, 2))
    `+"`${Sized};${Fallback};${Circle};${Other};${Every}`"+`
}
`)
	want := "7;0;circle;other;any"
	if output != want {
		t.Fatalf("wildcard output = %q, want %q", output, want)
	}
}

// TestUnionValuesCompareByVariantAndPayload pins structural equality, which
// both backends must agree on for nested recursive values.
func TestUnionValuesCompareByVariantAndPayload(t *testing.T) {
	output := runResultEverywhere(t, unionShapes+`
function main() -> string {
    let Same = Shape.Rect(1, 2) == Shape.Rect(1, 2)
    let Payload = Shape.Rect(1, 2) == Shape.Rect(1, 3)
    let Variant = Shape.Empty == Shape.Circle(1.0)
    let Nested = Shape.Grouped(Shape.Empty, Label { Text: "a" }) == Shape.Grouped(Shape.Empty, Label { Text: "a" })
    `+"`${Same};${Payload};${Variant};${Nested}`"+`
}
`)
	want := "true;false;false;true"
	if output != want {
		t.Fatalf("union equality output = %q, want %q", output, want)
	}
}

// TestUnionMatchArmEffectsStayChecked holds match arms to the same effect
// contract as every other expression.
func TestUnionMatchArmEffectsStayChecked(t *testing.T) {
	source := unionShapes + `
function Explode() -> string throws Failure {
    throw Failure { Message: "boom" }
}

function Handle(Value: Shape) -> string %s {
    match Value {
        Shape.Empty => Explode()
        _ => "other"
    }
}

function main() -> string {
    Handle(Shape.Circle(1.0)) catch (Reason) {
        Failure => Reason.Message
    }
}
`
	t.Run("undeclared", func(t *testing.T) {
		diagnostics := checkResult(t, strings.Replace(source, "%s", "", 1))
		assertDiagnostic(t, diagnostics, "SLK201", "unhandled Failure")
	})

	t.Run("declared", func(t *testing.T) {
		assertNoDiagnostics(t, checkResult(t, strings.Replace(source, "%s", "throws Failure", 1)))
	})
}

func TestUnionDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		body    string
		code    string
		message string
	}{
		{
			name: "missing variant",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Empty => "empty"
        Shape.Circle(Radius) => "circle"
        Shape.Rect(Width, Height) => "rect"
    }
}`,
			code:    "SLK356",
			message: "match does not handle Shape.Grouped; add that arm or a _ arm",
		},
		{
			name: "duplicate arm",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Empty => "empty"
        Shape.Empty => "again"
        _ => "other"
    }
}`,
			code:    "SLK357",
			message: "duplicate Shape.Empty arm; already handled at",
		},
		{
			name: "unreachable after wildcard",
			body: `function Check(Value: Shape) -> string {
    match Value {
        _ => "other"
        Shape.Empty => "empty"
    }
}`,
			code:    "SLK357",
			message: "unreachable Shape.Empty arm; the _ arm at",
		},
		{
			name: "unreachable wildcard",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Empty => "empty"
        Shape.Circle(Radius) => "circle"
        Shape.Rect(Width, Height) => "rect"
        Shape.Grouped(Inner, Tag) => "grouped"
        _ => "other"
    }
}`,
			code:    "SLK357",
			message: "unreachable _ arm; every variant of Shape is already handled",
		},
		{
			name: "wrong union",
			body: `union Other {
    Thing
}

function Check(Value: Shape) -> string {
    match Value {
        Other.Thing => "thing"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "Other.Thing is a variant of Other, but the matched value is Shape",
		},
		{
			name: "unknown variant pattern",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Blob => "blob"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "union Shape has no variant Blob",
		},
		{
			name: "payload binding count",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Rect(Width) => "rect"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "Shape.Rect binds 1 payload values, but the variant declares 2",
		},
		{
			name: "result pattern on union",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Ok(Inner) => "ok"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "Ok is not a variant of Shape",
		},
		{
			name: "union pattern on result",
			body: `function Check(Value: Result<int, Failure>) -> string {
    match Value {
        Shape.Empty => "empty"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "Shape.Empty is a union variant pattern, but the matched value is Result<int, Failure>",
		},
		{
			name: "optional scrutinee",
			body: `function Check(Value: Shape?) -> string {
    match Value {
        Shape.Empty => "empty"
        _ => "other"
    }
}`,
			code:    "SLK355",
			message: "match requires a Result or union value, found Shape?",
		},
		{
			name: "unknown variant construction",
			body: `function Make() -> Shape {
    Shape.Blob(1)
}`,
			code:    "SLK404",
			message: "union Shape has no variant Blob",
		},
		{
			name: "fieldless variant with parentheses",
			body: `function Make() -> Shape {
    Shape.Empty()
}`,
			code:    "SLK404",
			message: "Shape.Empty has no payload; write it without parentheses",
		},
		{
			name: "payload variant without parentheses",
			body: `function Make() -> Shape {
    Shape.Circle
}`,
			code:    "SLK404",
			message: "Shape.Circle carries a payload; construct it with Shape.Circle(...)",
		},
		{
			name: "construction arity",
			body: `function Make() -> Shape {
    Shape.Rect(1)
}`,
			code:    "SLK320",
			message: "Shape.Rect expects 2 payload values, found 1",
		},
		{
			name: "payload type",
			body: `function Make() -> Shape {
    Shape.Circle("wide")
}`,
			code:    "SLK320",
			message: "argument 1 to Shape.Circle must be float, found string",
		},
		{
			name: "arm types",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Empty => 1
        _ => "other"
    }
}`,
			code:    "SLK358",
			message: "match arms must produce one type; found int and string",
		},
		{
			name: "payload field read",
			body: `function Check(Value: Shape) -> float {
    Value.Radius
}`,
			code:    "SLK404",
			message: "Value is union Shape; match it to read the payload of one variant",
		},
		{
			name: "duplicate payload binding",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Rect(Size, Size) => "rect"
        _ => "other"
    }
}`,
			code:    "SLK404",
			message: "duplicate payload binding Size",
		},
		{
			name: "empty payload pattern",
			body: `function Check(Value: Shape) -> string {
    match Value {
        Shape.Rect() => "rect"
        _ => "other"
    }
}`,
			code:    "SLK360",
			message: "Shape.Rect must bind at least one payload value or omit its parentheses",
		},
		{
			name:    "duplicate variant declaration",
			body:    "union Twice {\n    One\n    One\n}",
			code:    "SLK001",
			message: "duplicate variant root.Twice.One",
		},
		{
			name: "async let on a variant constructor",
			body: `function Start() -> string {
    async let Task = Shape.Circle(1.0)
    let Value = await Task
    "done"
}`,
			code:    "SLK394",
			message: "async let initializer must resolve to one function or method call",
		},
		{
			name:    "union without variants",
			body:    "union Nothing {}",
			code:    "SLK001",
			message: "union root.Nothing must declare at least one variant",
		},
		{
			name:    "union conflicts with class",
			body:    "class Taken {}\n\nunion Taken {\n    One\n}",
			code:    "SLK001",
			message: "union root.Taken conflicts with a class or interface of the same name",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			diagnostics := checkResult(t, unionShapes+"\n"+testCase.body+`

function main() -> null {
    null
}
`)
			assertDiagnostic(t, diagnostics, testCase.code, testCase.message)
		})
	}
}

// TestUnionsResolveThroughNamespacesAndAliases pins how a union is named from
// another namespace: through its absolute path or an exact alias, with private
// unions and private variants still gated by ordinary visibility.
func TestUnionsResolveThroughNamespacesAndAliases(t *testing.T) {
	models := compiler.Source{Name: "models.slk", Namespace: "root.models", Text: `
union Shape {
    Empty
    Circle(Radius: float)
    hidden
}

union secret {
    Thing
}
`}

	t.Run("alias and absolute path", func(t *testing.T) {
		assertNoDiagnostics(t, compiler.Check([]compiler.Source{models, {Name: "app.slk", Namespace: "root", Text: `
use root.models.Shape as Figure

function Describe(Value: Figure) -> string {
    match Value {
        Figure.Empty => "empty"
        Figure.Circle(Radius) => "circle"
        _ => "other"
    }
}

function main() -> string {
    let Absolute = root.models.Shape.Circle(1.0)
    Describe(Absolute)
}
`}}))
	})

	t.Run("private union", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{models, {Name: "app.slk", Namespace: "root", Text: `
function main() -> null {
    let Value = root.models.secret.Thing
    null
}
`}})
		assertDiagnostic(t, diagnostics, "SLK330", "union secret is private to root.models")
	})

	t.Run("private variant", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{models, {Name: "app.slk", Namespace: "root", Text: `
function main() -> null {
    let Value = root.models.Shape.hidden
    null
}
`}})
		assertDiagnostic(t, diagnostics, "SLK330", "variant hidden is private to root.models")
	})

	t.Run("private variant still needs coverage", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{models, {Name: "app.slk", Namespace: "root", Text: `
function main() -> string {
    match root.models.Shape.Empty {
        root.models.Shape.Empty => "empty"
        root.models.Shape.Circle(Radius) => "circle"
    }
}
`}})
		assertDiagnostic(t, diagnostics, "SLK356", "match does not handle every variant of Shape visible in root.models; add a _ arm")
	})
}

// TestDescribeExposesUnionVariantsAndPayloads holds `slick describe` to the
// union surface: documentation, variants in declaration order, and the payload
// fields of one variant.
func TestDescribeExposesUnionVariantsAndPayloads(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "main.slk"), `
/// Shape is a closed set of figures.
union Shape {
    /// Empty draws nothing.
    Empty
    Rect(Width: int, Height: int)
}

function main() -> null {
    null
}
`)

	union, diagnostics, err := compiler.DescribePath("root.Shape", root)
	assertDescriptionResult(t, diagnostics, err)
	if union.Symbol.Kind != "union" || union.Symbol.Documentation == nil || *union.Symbol.Documentation != "Shape is a closed set of figures." {
		t.Fatalf("describe root.Shape = %+v", union.Symbol)
	}
	if len(union.Symbol.Children) != 2 ||
		union.Symbol.Children[0].CanonicalName != "root.Shape.Empty" ||
		union.Symbol.Children[0].Kind != "variant" ||
		union.Symbol.Children[1].CanonicalName != "root.Shape.Rect" {
		t.Fatalf("describe root.Shape children = %+v", union.Symbol.Children)
	}

	variant, diagnostics, err := compiler.DescribePath("root.Shape.Rect", root)
	assertDescriptionResult(t, diagnostics, err)
	if variant.Symbol.Kind != "variant" || variant.Symbol.Type != "root.Shape" {
		t.Fatalf("describe root.Shape.Rect = %+v", variant.Symbol)
	}
	if got, want := fieldNames(variant.Symbol.Fields), []string{"Width", "Height"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("root.Shape.Rect fields = %+v, want %+v", got, want)
	}
}

// TestUnionsAreFormattedCanonically holds the formatter to one spelling for a
// union declaration and its match patterns.
func TestUnionsAreFormattedCanonically(t *testing.T) {
	formatted, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: `
union Shape {
  Empty
  Rect( Width : int , Height : int )
}
function main() -> string {
  match Shape.Rect(1,2) { Shape.Empty => "e"
    Shape.Rect(Width, _) => "r" }
}
`})
	if err != nil {
		t.Fatalf("format union source: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	want := `union Shape {
    Empty
    Rect(Width: int, Height: int)
}

function main() -> string {
    match Shape.Rect(1, 2) {
        Shape.Empty => "e"
        Shape.Rect(Width, _) => "r"
    }
}
`
	if formatted != want {
		t.Fatalf("formatted union source:\n%s\nwant:\n%s", formatted, want)
	}
}

// TestUnionKeywordIsHighlighted keeps the editor surface in step with the
// grammar.
func TestUnionKeywordIsHighlighted(t *testing.T) {
	for _, token := range compiler.Highlight("union Shape { Empty }") {
		if token.Text == "union" {
			if token.Class != compiler.ClassKeyword {
				t.Fatalf("union highlighted as %q", token.Class)
			}
			return
		}
	}
	t.Fatal("union keyword was not highlighted")
}

// TestNewVariantFailsEveryUnhandledPhase is the completion condition: the AST
// example models itself as closed recursive data, and adding a variant stops
// compilation until every phase handles it.
func TestNewVariantFailsEveryUnhandledPhase(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "tagged-union", "main.slk"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	extended := strings.Replace(string(source), "    Missing\n}", "    Missing\n    Negate(Inner: Expression)\n}", 1)
	if extended == string(source) {
		t.Fatal("example union declaration did not match the expected shape")
	}

	diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: extended}})
	phases := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "SLK356" && strings.Contains(diagnostic.Message, "Expression.Negate") {
			phases++
		}
	}
	if phases != 2 {
		t.Fatalf("expected the render and evaluate phases to fail, found %d in %+v", phases, diagnostics)
	}
}
