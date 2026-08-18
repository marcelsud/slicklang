package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// constantProgram wraps one constant declaration and prints it, so every
// assertion below is about the value a running program observes.
func constantProgram(declarations string) string {
	return declarations + `
function main() -> string {
    ` + "`${Value}`" + `
}
`
}

// TestConstantsProduceOneValueInEveryBackend covers the constant expression
// grammar end to end: all backends must agree on the value a program prints.
func TestConstantsProduceOneValueInEveryBackend(t *testing.T) {
	for _, test := range []struct {
		name         string
		declarations string
		want         string
	}{
		{"string literal", `const Value: string = "SLK001"`, "SLK001"},
		{"int literal", `const Value: int = 256`, "256"},
		{"float literal", `const Value: float = 1.25`, "1.25"},
		{"bool literal", `const Value: bool = true`, "true"},
		{"null literal", `const Value: null = null`, ""},
		{"reference", "const Depth: int = 256\nconst Value: int = Depth", "256"},
		{"forward reference", "const Value: int = Depth\nconst Depth: int = 256", "256"},
		{"grouping", `const Value: int = (2 + 3) * 4`, "20"},
		{"unary minus", "const Depth: int = 256\nconst Value: int = -Depth", "-256"},
		{"nested unary minus", "const Depth: int = -256\nconst Value: int = -Depth", "256"},
		{"unary not", "const Enabled: bool = true\nconst Value: bool = !Enabled", "false"},
		{"string concatenation", `const Value: string = "SLK" + "001"`, "SLK001"},
		{"int arithmetic", `const Value: int = 8 * 32 - 6`, "250"},
		{"float arithmetic", `const Value: float = 0.1 + 0.2`, "0.30000000000000004"},
		{"int overflow wraps", `const Value: int = 9223372036854775807 + 1`, "-9223372036854775808"},
		{"ordering", `const Value: bool = 8 < 256`, "true"},
		{"equality", `const Value: bool = "a" == "a"`, "true"},
		{"inequality", `const Value: bool = 1 != 2`, "true"},
		{"and with a false left operand", `const Value: bool = false && true`, "false"},
		{"or with a true left operand", `const Value: bool = true || false`, "true"},
		{"boolean chain", `const Value: bool = !false && (256 > 8 || false)`, "true"},
		{"fieldless variant", "union Mode {\n    Strict\n    Lenient\n}\nconst Value: Mode = Mode.Strict", "Strict"},
		{"variant equality", "union Mode {\n    Strict\n    Lenient\n}\nconst Chosen: Mode = Mode.Strict\nconst Value: bool = Chosen == Mode.Lenient", "false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runOnEveryEngineSource(t, constantProgram(test.declarations)); got != test.want {
				t.Fatalf("constant produced %q, want %q", got, test.want)
			}
		})
	}
}

// TestConstantsResolveAcrossFilesAndNamespaces holds the three ways a constant
// is named from another file: an exact alias, a renamed alias, and its absolute
// canonical path.
func TestConstantsResolveAcrossFilesAndNamespaces(t *testing.T) {
	sources := []compiler.Source{
		{Name: "protocol/limits.slk", Namespace: "root.protocol", Text: `
const budget: int = 8

/// The deepest nesting the parser accepts.
const MaximumDepth: int = budget * 32

const Header: string = "slick/1"
`},
		{Name: "main.slk", Namespace: "root", Text: `
use root.protocol.MaximumDepth as MaximumDepth

use root.protocol.Header as Protocol

function main() -> string {
    ` + "`${MaximumDepth}|${Protocol}|${Absolute}`" + `
}

const Absolute: int = root.protocol.MaximumDepth * 2
`},
	}
	output, diagnostics, err := compiler.Run(sources)
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "256|slick/1|512" {
		t.Fatalf("cross-file constants produced %q", output)
	}
}

func TestPrivateConstantIsNotVisibleToAnotherNamespace(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "protocol/limits.slk", Namespace: "root.protocol", Text: "const budget: int = 8\n"},
		{Name: "main.slk", Namespace: "root", Text: `
function main() -> int {
    root.protocol.budget
}
`},
	})
	assertDiagnostic(t, diagnostics, "SLK330", "constant budget is private to root.protocol")
}

func TestConstantDiagnosticsAreFocused(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			"type mismatch",
			`const Value: int = "SLK001"`,
			"SLK342", "constant Value is int, but its value produces string",
		},
		{
			"unassignable operand",
			`const Value: int = 1 + "a"`,
			"SLK342", `operator + does not accept int and string`,
		},
		{
			"optional type",
			`const Value: string? = "a"`,
			"SLK405", "constant Value must be declared bool, int, float, string, null, or a union",
		},
		{
			"array type",
			`const Value: int[] = 1`,
			"SLK405", "found int[]",
		},
		{
			"class type",
			"class Box {\n    Width: int\n}\nconst Value: Box = 1",
			"SLK405", "found Box",
		},
		{
			"call",
			"function width() -> int {\n    1\n}\nconst Value: int = width()",
			"SLK406", "cannot use the call width",
		},
		{
			"object construction",
			"class Box {\n    Width: int\n}\nconst Value: int = Box { Width: 1 }",
			"SLK406", "cannot use object construction",
		},
		{
			"array literal",
			`const Value: int = [1, 2]`,
			"SLK406", "cannot use an array literal",
		},
		{
			"map literal",
			`const Value: int = map { "a": 1 }`,
			"SLK406", "cannot use a map literal",
		},
		{
			"template",
			"const Value: string = `text`",
			"SLK406", "cannot use a template",
		},
		{
			"field access",
			"class Box {\n    Width: int\n}\nconst Value: int = Box.Width",
			"SLK341", "unknown value Box.Width",
		},
		{
			"result constructor",
			`const Value: int = Ok(1)`,
			"SLK406", "cannot use the Result constructor Ok",
		},
		{
			"propagation",
			"function load() -> int {\n    1\n}\nconst Value: int = load()?",
			"SLK406", "cannot use the ? operator",
		},
		{
			"if expression",
			`const Value: int = if (true) { 1 } else { 2 }`,
			"SLK406", "cannot use if",
		},
		{
			"payload variant",
			"union Shape {\n    Rect(Width: int)\n}\nconst Value: Shape = Shape.Rect(1)",
			"SLK406", "cannot use the payload variant Shape.Rect",
		},
		{
			"duplicate declaration",
			"const Value: int = 1\nconst Value: int = 2",
			"SLK001", "duplicate constant root.Value",
		},
		{
			"conflict with a function",
			"function Value() -> int {\n    1\n}\nconst Value: int = 1",
			"SLK001", "constant root.Value conflicts with the function of the same name",
		},
		{
			"method receiver",
			"const Depth: int = 1\nfunction main() -> string {\n    Depth.Describe()\n}",
			"SLK203", "constant Depth is a value and cannot be a method receiver",
		},
		{
			"assignment",
			"const Depth: int = 1\nfunction main() -> null {\n    Depth = 2\n}",
			"SLK407", "constant Depth is immutable and cannot be assigned",
		},
		{
			"value on the next line",
			"const Value: int =\n    1",
			"SLK001", "constant Value needs its value on the same line",
		},
		{
			"two expressions",
			"const Value: int = 1 2",
			"SLK001", "constant Value must be one expression",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, test.source+"\n"), test.code, test.message)
		})
	}
}

// TestConstantCycleReportsTheCompleteChain is the contract for a cycle: the
// diagnostic names every declaration on it, once, however many roots reach it.
func TestConstantCycleReportsTheCompleteChain(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			"direct",
			"const A: int = A",
			"constant root.A depends on itself: root.A -> root.A",
		},
		{
			"indirect",
			"const A: int = B\nconst B: int = C\nconst C: int = A",
			"constant root.A depends on itself: root.A -> root.B -> root.C -> root.A",
		},
		{
			"behind a short circuit",
			"const A: bool = false && A",
			"constant root.A depends on itself: root.A -> root.A",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkResult(t, test.source+"\n")
			assertDiagnostic(t, diagnostics, "SLK408", test.want)
			cycles := 0
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == "SLK408" {
					cycles++
				}
			}
			if cycles != 1 {
				t.Fatalf("expected one cycle diagnostic, found %d in %+v", cycles, diagnostics)
			}
		})
	}
}

// TestConstantsAreFormattedCanonically holds the formatter to one declaration
// per block, and to the spacing the operators inside an initializer require.
func TestConstantsAreFormattedCanonically(t *testing.T) {
	formatted, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: `
const  Depth : int=8*32;
const Deepest:int= -Depth
const Enabled : bool = !false&&( Depth>8||false )
function main()->int{Deepest}
`})
	if err != nil {
		t.Fatalf("format constant source: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	want := `const Depth: int = 8 * 32

const Deepest: int = -Depth

const Enabled: bool = !false && (Depth > 8 || false)

function main() -> int {
    Deepest
}
`
	if formatted != want {
		t.Fatalf("formatted constants:\n%s\nwant:\n%s", formatted, want)
	}
	second, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: formatted})
	if err != nil || len(diagnostics) != 0 || second != formatted {
		t.Fatalf("second format changed output: diagnostics=%+v err=%v\n%s", diagnostics, err, second)
	}
}

func TestHighlightClassifiesConstKeyword(t *testing.T) {
	source := "const Depth: int = 8\n"
	var rebuilt strings.Builder
	keyword := false
	for _, token := range compiler.Highlight(source) {
		rebuilt.WriteString(token.Text)
		keyword = keyword || token.Text == "const" && token.Class == compiler.ClassKeyword
	}
	if !keyword {
		t.Fatalf("const was not highlighted as a keyword in %+v", compiler.Highlight(source))
	}
	if rebuilt.String() != source {
		t.Fatalf("highlighting did not round-trip %q", source)
	}
}

func TestDescribeReportsConstants(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "protocol", "limits.slk"), `
/// The deepest nesting the parser accepts.
const MaximumDepth: int = 256

const budget: int = 8
`)
	writeSource(t, filepath.Join(root, "main.slk"), `
function main() -> int {
    root.protocol.MaximumDepth
}
`)

	constant, diagnostics, err := compiler.DescribePath("root.protocol.MaximumDepth", root)
	assertDescriptionResult(t, diagnostics, err)
	if constant.Symbol.Kind != "constant" || constant.Symbol.Type != "int" || constant.Symbol.Visibility != "public" {
		t.Fatalf("describe root.protocol.MaximumDepth = %+v", constant.Symbol)
	}
	if constant.Symbol.Documentation == nil || *constant.Symbol.Documentation != "The deepest nesting the parser accepts." {
		t.Fatalf("constant documentation = %+v", constant.Symbol.Documentation)
	}

	namespace, diagnostics, err := compiler.DescribePath("root.protocol", root)
	assertDescriptionResult(t, diagnostics, err)
	found := map[string]compiler.ChildDescription{}
	for _, child := range namespace.Symbol.Children {
		found[child.CanonicalName] = child
	}
	public, hasPublic := found["root.protocol.MaximumDepth"]
	private, hasPrivate := found["root.protocol.budget"]
	if !hasPublic || public.Kind != "constant" || public.Visibility != "public" {
		t.Fatalf("namespace children missing the public constant: %+v", namespace.Symbol.Children)
	}
	if !hasPrivate || private.Visibility != "private" {
		t.Fatalf("namespace children missing the private constant: %+v", namespace.Symbol.Children)
	}
}
