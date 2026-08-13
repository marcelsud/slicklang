package compiler_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"slick/internal/compiler"
)

const genericBox = `
class Box<T> {
    Value: T

    function Get() -> T {
        self.Value
    }
}
`

func runGenerics(t *testing.T, sources ...compiler.Source) string {
	t.Helper()
	output, diagnostics, err := compiler.Run(sources)
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	return output
}

func buildAndRunGenerics(t *testing.T, sources ...compiler.Source) string {
	t.Helper()
	root := t.TempDir()
	for _, source := range sources {
		path := filepath.Join(root, filepath.FromSlash(source.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create source directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(source.Text), 0o644); err != nil {
			t.Fatalf("write Slick source: %v", err)
		}
	}
	binary := filepath.Join(t.TempDir(), "app")
	diagnostics, err := compiler.BuildPath(root, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run native binary: %v: %s", err, output)
	}
	return strings.TrimSuffix(string(output), "\n")
}

// runGenericsEverywhere holds the interpreter and the native binary to one
// observable result, so a generic declaration cannot mean different things on
// the two backends.
func runGenericsEverywhere(t *testing.T, source string) string {
	t.Helper()
	sources := []compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}}
	interpreted := runGenerics(t, sources...)
	native := buildAndRunGenerics(t, sources...)
	if interpreted != native {
		t.Fatalf("interpreter produced %q, native binary produced %q", interpreted, native)
	}
	return interpreted
}

func checkGenerics(t *testing.T, source string) []compiler.Diagnostic {
	t.Helper()
	return compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
}

func TestGenericDeclarationsRunOnBothBackends(t *testing.T) {
	tests := map[string]struct {
		source   string
		expected string
	}{
		"one type parameter": {
			source: genericBox + `
function main() -> int {
    let Number = Box<int> { Value: 42 }
    Number.Get()
}
`,
			expected: "42",
		},
		"multiple type parameters": {
			source: `
class Pair<K, V> {
    Key: K
    Value: V

    function Describe() -> string {
        let Name = self.Key
        let Held = self.Value
        ` + "`${Name}=${Held}`" + `
    }
}

function main() -> string {
    let Entry = Pair<string, int> { Key: "size", Value: 3 }
    Entry.Describe()
}
`,
			expected: "size=3",
		},
		"one declaration serves every instantiation": {
			source: genericBox + `
function main() -> string {
    let Number = Box<int> { Value: 42 }
    let Word = Box<string> { Value: "slick" }
    let Held = Number.Get()
    let Text = Word.Get()
    ` + "`${Held};${Text}`" + `
}
`,
			expected: "42;slick",
		},
		"generic function": {
			source: `
function Identity<T>(Value: T) -> T {
    Value
}

function main() -> string {
    let Number = Identity<int>(7)
    let Text = Identity<string>("seven")
    ` + "`${Number};${Text}`" + `
}
`,
			expected: "7;seven",
		},
		"generic nested only in callable signature": {
			source: `
class Box<T> {
    Value: T
}

function Accept(Operation: (Box<int>) -> Box<int>) -> null {
    null
}

function main() -> int {
    42
}
`,
			expected: "42",
		},
		"generic constructed only inside lambda": {
			source: genericBox + `
function main() -> int {
    let Make = () -> Box<int> {
        Box<int> { Value: 42 }
    }
    let Value = Make()
    Value.Get()
}
`,
			expected: "42",
		},
		"enclosing generic substitutes lambda signature": {
			source: `
function Make<T>(Value: T) -> (() -> T) {
    () -> T {
        Value
    }
}

function main() -> int {
    let Read = Make<int>(42)
    Read()
}
`,
			expected: "42",
		},
		"detached method keeps the receiver parameters": {
			source: `
class Box<T> {
    Value: T

    function Get() -> T
}

function Box<T>.Get() -> T {
    self.Value
}

function main() -> int {
    let Number = Box<int> { Value: 5 }
    Number.Get()
}
`,
			expected: "5",
		},
		"nested instantiation": {
			source: genericBox + `
function main() -> int {
    let Outer = Box<Box<int>> { Value: Box<int> { Value: 9 } }
    let Inner = Outer.Get()
    Inner.Get()
}
`,
			expected: "9",
		},
		"recursive instantiation": {
			source: `
class Tree<T> {
    Item: T
    Children: Tree<T>[]
}

function main() -> string {
    let Leaf = Tree<string> { Item: "leaf", Children: [] }
    let Root = Tree<string> { Item: "root", Children: [Leaf] }
    let Nested = Root.Children
    let First = Nested.Get(0)
    let Name = if (First == null) { "none" } else { First.Item }
    ` + "`${Root.Item};${Name}`" + `
}
`,
			expected: "root;leaf",
		},
		"optional array tuple map and result carry the parameter": {
			source: `
class Failure implements Error {
    Message: string
}

class Holder<T> {
    Maybe: T?
    Items: T[]
    Both: (T, int)
    Lookup: Map<string, T>
    Outcome: Result<T, Failure>

    function First() -> T? {
        let Items = self.Items
        Items.Get(0)
    }
}

function main() -> string {
    let Held = Holder<string> {
        Maybe: "m",
        Items: ["i"],
        Both: ("b", 1),
        Lookup: map { "k": "v" },
        Outcome: Ok("o")
    }
    let Found = Held.First()
    let Item = if (Found == null) { "none" } else { Found }
    let Table = Held.Lookup
    let Value = Table.Get("k")
    let Stored = if (Value == null) { "none" } else { Value }
    let Outcome = match Held.Outcome {
        Ok(Text) => Text
        Err(Error) => "failed"
    }
    ` + "`${Item};${Stored};${Outcome}`" + `
}
`,
			expected: "i;v;o",
		},
		"checked effect carries the parameter": {
			source: genericBox + `
class Empty<T> implements Error {
    Message: string
    Wanted: Box<T>
}

function Demand<T>(From: Box<T>, Ready: bool) -> T throws Empty<T> {
    if (!Ready) {
        throw Empty<T> { Message: "empty", Wanted: From }
    }
    From.Get()
}

function main() -> string {
    let Number = Box<int> { Value: 3 }
    let Ready = Demand<int>(Number, true) catch {
        Empty<int> => 0
    }
    let Absent = Demand<int>(Number, false) catch {
        Empty<int> as Failure => 0 - 1
    }
    ` + "`${Ready};${Absent}`" + `
}
`,
			expected: "3;-1",
		},
		"a parameter resolves before a namespace declaration of the same name": {
			source: `
class T {
    Marker: string
}

class Box<T> {
    Value: T
}

function main() -> int {
    let Held = Box<int> { Value: 3 }
    Held.Value
}
`,
			expected: "3",
		},
		"generic resource in a using scope": {
			source: `
class Held<T> {
    Value: T

    function Close() -> null {
        null
    }
}

function main() -> int {
    using Resource = Held<int> { Value: 5 } {
        Resource.Value
    }
}
`,
			expected: "5",
		},
		"async call to a generic function": {
			source: genericBox + `
function Load<T>(From: Box<T>) -> T {
    From.Get()
}

function main() -> int {
    let Held = Box<int> { Value: 4 }
    async let Pending = Load<int>(Held)
    await Pending
}
`,
			expected: "4",
		},
		"generic interface conformance at the instantiation": {
			source: `
interface Source<T> {
    function Read() -> T?
}

class Box<T> {
    Value: T

    function Read() -> T? {
        self.Value
    }
}

function First<T>(From: Source<T>) -> T? {
    From.Read()
}

function main() -> string {
    let Found = First<string>(Box<string> { Value: "here" })
    if (Found == null) { "none" } else { Found }
}
`,
			expected: "here",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if output := runGenericsEverywhere(t, test.source); output != test.expected {
				t.Fatalf("expected %q, found %q", test.expected, output)
			}
		})
	}
}

// TestGenericDeclarationsCrossNamespaces pins visibility and exact aliases for
// generic declarations: an alias names one declaration, and its instantiations
// obey the same access rules the declaration does.
func TestGenericDeclarationsCrossNamespaces(t *testing.T) {
	library := compiler.Source{Name: "deep/lib.slk", Namespace: "root.deep", Text: `
class Parsed<T> {
    Value: T

    function Get() -> T {
        self.Value
    }
}

function Wrap<T>(Value: T) -> Parsed<T> {
    Parsed<T> { Value: Value }
}
`}
	main := compiler.Source{Name: "main.slk", Namespace: "root", Text: `
use root.deep.Parsed as Held
use root.deep.Wrap

function main() -> string {
    let Direct = Held<int> { Value: 1 }
    let Wrapped = Wrap<string>("two")
    let Number = Direct.Get()
    let Text = Wrapped.Get()
    ` + "`${Number};${Text}`" + `
}
`}
	interpreted := runGenerics(t, library, main)
	native := buildAndRunGenerics(t, library, main)
	if interpreted != "1;two" || native != interpreted {
		t.Fatalf("expected 1;two from both backends, found %q and %q", interpreted, native)
	}
}

func TestPrivateGenericDeclarationStaysInsideItsNamespace(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "deep/lib.slk", Namespace: "root.deep", Text: "class box<T> {\n    Value: T\n}\n"},
		{Name: "main.slk", Namespace: "root", Text: "function main() -> string {\n    let B = root.deep.box<int> { Value: 1 }\n    \"x\"\n}\n"},
	})
	assertDiagnostic(t, diagnostics, "SLK330", "class box is private to root.deep")
}

func TestGenericCallableTypesKeepGenericEffectsAndScope(t *testing.T) {
	diagnostics := checkGenerics(t, `
class Box<T> {
    Value: T
}

class Empty<T> implements Error {}

class Holder<T> {
    Transform: (Box<T>) -> Box<T> throws Empty<T>
}

function main() -> int {
    let Held = Holder<int> {
        Transform: (Value: Box<int>) -> Box<int> throws Empty<int> {
            Value
        }
    }
    let Output = Held.Transform(Box<int> { Value: 42 }) catch {
        Empty<int> => Box<int> { Value: 0 }
    }
    Output.Value
}
`)
	assertNoDiagnostics(t, diagnostics)
}

// TestRecursiveGenericConverges pins that a declaration containing itself at
// the same type arguments is a finite, ordinary program, while one that wraps
// its own parameter is rejected instead of expanding forever.
//
// The same-arguments case is checked and interpreted but deliberately not
// built: a class that reaches itself by value through an optional is a Go
// recursive value type, which the native backend already rejects for the
// non-generic class Node { Next: Node? }. Recursion through an array is
// covered by TestGenericDeclarationsRunOnBothBackends on both backends.
func TestRecursiveGenericConverges(t *testing.T) {
	t.Run("same type arguments", func(t *testing.T) {
		source := `
class Node<T> {
    Item: T
    Next: Node<T>?
}

function main() -> string {
    let Tail = Node<int> { Item: 2 }
    let Head = Node<int> { Item: 1, Next: Tail }
    let Following = Head.Next
    let Second = if (Following == null) { 0 } else { Following.Item }
    ` + "`${Head.Item};${Second}`" + `
}
`
		assertNoDiagnostics(t, checkGenerics(t, source))
		if output := runGenerics(t, compiler.Source{Name: "main.slk", Namespace: "root", Text: source}); output != "1;2" {
			t.Fatalf("expected 1;2, found %q", output)
		}
	})
	t.Run("expanding type arguments", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Bad<T> {
    Next: Bad<Bad<T>>?
}

function main() -> string {
    let B = Bad<int> { }
    "x"
}
`)
		assertDiagnostic(t, diagnostics, "SLK411", "expands without limit")
	})
	t.Run("callable arrows do not hide expansion depth", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Box<T> {}

class Loop<T> {
    Next: Loop<() -> Box<T>>?
}

function main() -> null {
    let Value = Loop<int> {}
    null
}
`)
		assertDiagnostic(t, diagnostics, "SLK411", "expands without limit")
	})
	t.Run("callable-growing type arguments", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Loop<T> {
    Next: Loop<() -> T>?
}

function main() -> null {
    let Value = Loop<int> {}
    null
}
`)
		assertDiagnostic(t, diagnostics, "SLK411", "expands without limit")
	})
	t.Run("array-growing type arguments", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Loop<T> {
    Next: Loop<T[]>?
}

function main() -> null {
    let Value = Loop<int> {}
    null
}
`)
		assertDiagnostic(t, diagnostics, "SLK411", "expands without limit")
	})
	t.Run("long acyclic expansion", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class A<T> { Next: B<T>? }
class B<T> { Next: C<T>? }
class C<T> { Next: D<T>? }
class D<T> { Next: E<T>? }
class E<T> { Next: F<T>? }
class F<T> { Next: G<T>? }
class G<T> { Next: H<T>? }
class H<T> { Next: I<T>? }
class I<T> { Next: J<T>? }
class J<T> {}

function main() -> null {
    let Value = A<int> {}
    null
}
`)
		assertNoDiagnostics(t, diagnostics)
	})
}

func TestCallableTypeArgumentsAtGenericExpressionBoundaries(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"object construction": {
			source: `
class Holder<T> {
    Value: T
}

function main() -> int {
    let Identity = (Value: int) -> int {
        Value
    }
    let Held = Holder<(int) -> int> { Value: Identity }
    Held.Value(42)
}
`,
			want: "42",
		},
		"caught error": {
			source: `
class Failure<T> implements Error {}

function Work() -> int throws Failure<() -> int> {
    42
}

function main() -> int {
    Work() catch {
        Failure<() -> int> => 0
    }
}
`,
			want: "42",
		},
		"implements clause": {
			source: `
interface Contract<T> {}

class Service implements Contract<() -> int> {}

function main() -> int {
    let App = Service {}
    42
}
`,
			want: "42",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if output := runGenericsEverywhere(t, test.source); output != test.want {
				t.Fatalf("output = %q, want %q", output, test.want)
			}
		})
	}
}

func TestGenericDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"type argument arity": {
			source:  genericBox + "function Use(Held: Box<int, string>) -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK361",
			message: "root.Box takes 1 type argument, found 2",
		},
		"bare generic name": {
			source:  genericBox + "function Use(Held: Box) -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK361",
			message: "root.Box takes 1 type argument, found 0",
		},
		"bare generic construction": {
			source:  genericBox + "function main() -> int {\n    let Held = Box { Value: 1 }\n    1\n}\n",
			code:    "SLK361",
			message: "root.Box takes 1 type argument, found 0",
		},
		"type arguments on a non-generic class": {
			source:  "class Point {\n    X: int\n}\nfunction Use(P: Point<int>) -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK361",
			message: "root.Point takes no type arguments",
		},
		"unbound type parameter": {
			source:  "class Box<T> {\n    Value: U\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK410",
			message: "U is not a known type or a type parameter of root.Box",
		},
		"unbound type parameter in a detached method": {
			source:  genericBox + "function Box<T>.Missing() -> U {\n    self.Value\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK410",
			message: "U is not a known type or a type parameter of root.Box.Missing",
		},
		"type parameter shadows a built-in type": {
			source:  "class Box<int> {\n    Value: int\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK409",
			message: "type parameter int shadows the built-in type int",
		},
		"type parameter shadows a compiler generic": {
			source:  "class Box<Result> {\n    Value: int\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK409",
			message: "type parameter Result shadows the built-in type Result",
		},
		"duplicate type parameter": {
			source:  "class Box<T, T> {\n    Value: T\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK409",
			message: "duplicate type parameter T on Box",
		},
		"generic call without type arguments": {
			source:  "function Identity<T>(Value: T) -> T {\n    Value\n}\nfunction main() -> int {\n    Identity(1)\n}\n",
			code:    "SLK380",
			message: "Identity expects 1 type arguments, found 0",
		},
		"generic call with the wrong arity": {
			source:  "function Identity<T>(Value: T) -> T {\n    Value\n}\nfunction main() -> int {\n    Identity<int, string>(1)\n}\n",
			code:    "SLK380",
			message: "Identity expects 1 type arguments, found 2",
		},
		"type arguments on a plain function": {
			source:  "function Plain(Value: int) -> int {\n    Value\n}\nfunction main() -> int {\n    Plain<int>(1)\n}\n",
			code:    "SLK380",
			message: "Plain does not take type arguments",
		},
		"unknown type argument": {
			source:  genericBox + "function main() -> int {\n    let Held = Box<Nope> { Value: 1 }\n    1\n}\n",
			code:    "SLK410",
			message: "Nope is not a known type",
		},
		"detached method on a generic receiver without parameters": {
			source:  genericBox + "function Box.Extra() -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK361",
			message: "root.Box takes 1 type argument, found 0",
		},
		"type parameters on a non-generic receiver": {
			source:  "class Point {\n    X: int\n}\nfunction Point<T>.Extra() -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK361",
			message: "root.Point takes no type arguments",
		},
		"method-local type parameters": {
			source:  genericBox + "function Box<T>.Extra<U>() -> int {\n    1\n}\nfunction main() -> int {\n    1\n}\n",
			code:    "SLK001",
			message: "methods do not declare their own type parameters",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assertDiagnostic(t, checkGenerics(t, test.source), test.code, test.message)
		})
	}
}

// TestGenericTypesAreInvariant pins that one declaration at two type arguments
// produces two unrelated types.
func TestGenericTypesAreInvariant(t *testing.T) {
	diagnostics := checkGenerics(t, genericBox+`
function Take(Held: Box<int>) -> int {
    Held.Get()
}

function main() -> int {
    Take(Box<string> { Value: "one" })
}
`)
	assertDiagnostic(t, diagnostics, "SLK320", "argument 1 to Take must be Box<int>, found Box<string>")
}

func TestGenericInterfaceConformanceIsCheckedAfterSubstitution(t *testing.T) {
	diagnostics := checkGenerics(t, `
interface Source<T> {
    function Read() -> T?
}

class Box<T> {
    Value: T

    function Read() -> T? {
        self.Value
    }
}

function Take(From: Source<string>) -> string {
    "x"
}

function main() -> string {
    Take(Box<int> { Value: 1 })
}
`)
	assertDiagnostic(t, diagnostics, "SLK320", "root.Box<int> does not implement root.Source<string>")
}

// TestGenericMistakeIsReportedOnce pins that a mistake in a generic body is
// reported against the declaration, not once per instantiation.
func TestGenericMistakeIsReportedOnce(t *testing.T) {
	diagnostics := checkGenerics(t, `
class Box<T> {
    Value: T

    function Get() -> T {
        Absent(self.Value)
    }
}

function main() -> string {
    let Number = Box<int> { Value: 1 }
    let Word = Box<string> { Value: "one" }
    let Held = Number.Get()
    Word.Get()
}
`)
	reported := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "SLK203" && strings.Contains(diagnostic.Message, "Absent") {
			reported++
		}
	}
	if reported != 1 {
		t.Fatalf("expected one unknown-callable diagnostic, found %d in %+v", reported, diagnostics)
	}
}

func TestGenericJSONAcceptsConcreteInstantiationsOnly(t *testing.T) {
	t.Run("concrete instantiation", func(t *testing.T) {
		source := `
class Payload<T> {
    Value: T
    Label: string
}

function encode() -> Result<string, std.json.Failure> {
    std.json.Encode<Payload<int>>(Payload<int> { Value: 7, Label: "n" })
}

function main() -> string {
    match encode() {
        Ok(Text) => Text
        Err(Failure) => "failed"
    }
}
`
		if output := runGenericsEverywhere(t, source); output != `{"Label":"n","Value":7}` {
			t.Fatalf("unexpected JSON output %q", output)
		}
	})
	t.Run("open declaration", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Payload<T> {
    Value: T
}

function main() -> Result<string, std.json.Failure> {
    std.json.Encode<Payload>(Payload<int> { Value: 1 })
}
`)
		assertDiagnostic(t, diagnostics, "SLK381", "JSON encodes one concrete instantiation, not an open generic declaration")
	})
	t.Run("unsupported instantiated field", func(t *testing.T) {
		diagnostics := checkGenerics(t, `
class Payload<T> {
    Value: T
}

function main() -> Result<string, std.json.Failure> {
    std.json.Encode<Payload<bytes>>(Payload<bytes> { Value: "x" })
}
`)
		assertDiagnostic(t, diagnostics, "SLK381", "Payload<bytes> field Value: bytes cannot be encoded")
	})
}

// TestCompilerOwnedGenericsAreUnchanged pins that declaring user generics does
// not disturb Map, Result, Buffer, or the native JSON generics.
func TestCompilerOwnedGenericsAreUnchanged(t *testing.T) {
	source := genericBox + `
function collect() -> Result<int, std.json.Failure> {
    let Numbers = std.buffer.New<int>()
    let Pushed = std.buffer.Push<int>(Numbers, 4)
    let Length = std.buffer.Length<int>(Numbers)
    let Table = map { "k": Length }
    let Found = Table.Get("k")
    if (Found == null) { Ok(0) } else { Ok(Found) }
}

function main() -> string {
    let Held = Box<int> { Value: 1 }
    let Value = Held.Get()
    let Total = match collect() {
        Ok(Count) => Count
        Err(Failure) => 0
    }
    ` + "`${Value};${Total}`" + `
}
`
	if output := runGenericsEverywhere(t, source); output != "1;1" {
		t.Fatalf("expected 1;1, found %q", output)
	}
}

// TestUnionVariantCarriesGenericInstantiation pins that a union variant payload
// typed as a concrete instantiation of a user generic seeds monomorphization, so
// the native backend can emit the payload field. Box<int> appears only in the
// variant declaration; the Full arm is never constructed, so nothing else
// registers the instance. Without seeding from p.unions the native build fails.
func TestUnionVariantCarriesGenericInstantiation(t *testing.T) {
	source := genericBox + `
union Holder {
    Full(Content: Box<int>)
    Empty
}

function describe(Value: Holder) -> int {
    match Value {
        Holder.Full(Held) => Held.Get()
        Holder.Empty => 0
    }
}

function main() -> int {
    describe(Holder.Empty)
}
`
	if output := runGenericsEverywhere(t, source); output != "0" {
		t.Fatalf("expected 0, found %q", output)
	}
}

// TestGenericSourceFormatting pins that generic declaration and use syntax is
// already canonical. The angle brackets sit next to every token that could
// absorb them: a catch arm whose error type ends in > directly before the
// arrow, a throws list continued with |, and a >= operator in the same file.
func TestGenericSourceFormatting(t *testing.T) {
	source := `class Box<T> {
    Value: T
    function Get() -> T {
        self.Value
    }
}

class Missing implements Error {
    Message: string
}

class Empty<T> implements Error {
    Message: string
}

function Box<T>.Describe() -> string {
    "box"
}

function Demand<T>(From: Box<T>) -> T throws Empty<T> | Missing {
    From.Get()
}

function Wide(Table: Map<string, int>) -> bool {
    Table.Length() >= 2
}

function main() -> int {
    let Held = Box<int> {
        Value: 1
    }
    let Value = Demand<int>(Held) catch {
        Empty<int> => 0
        Missing => 0
    }
    Value
}
`
	formatted, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: source})
	if err != nil {
		t.Fatalf("format Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if formatted != source {
		t.Fatalf("generic source is not canonical:\n%s", formatted)
	}
}

func TestGenericDeclarationHighlighting(t *testing.T) {
	tokens := compiler.Highlight("class Box<T> { Value: T }")
	var classes []string
	var texts []string
	for _, token := range tokens {
		if token.Class == compiler.ClassPlain && strings.TrimSpace(token.Text) == "" {
			continue
		}
		classes = append(classes, string(token.Class))
		texts = append(texts, token.Text)
	}
	expected := []string{"keyword", "ident", "punct", "ident", "punct", "punct", "ident", "punct", "ident", "punct"}
	if strings.Join(classes, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected %v, found %v for %v", expected, classes, texts)
	}
}

// TestDescribeGenericDeclarations pins that documentation names a declaration's
// type parameters and never reports the instantiations the compiler
// monomorphized for a program.
func TestDescribeGenericDeclarations(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "main.slk"), `
/// Box holds one value.
class Box<T> {
    /// Value is the held value.
    Value: T

    /// Get returns the held value.
    function Get() -> T {
        self.Value
    }
}

/// Source produces a value.
interface Source<T> {
    /// Read returns the next value.
    function Read() -> T?
}

/// Identity returns its argument.
function Identity<T>(Value: T) -> T {
    Value
}

function main() -> int {
    let Held = Box<int> { Value: 1 }
    Identity<int>(Held.Get())
}
`)

	for name, want := range map[string][]string{
		"root.Box":      {"T"},
		"root.Source":   {"T"},
		"root.Identity": {"T"},
	} {
		description, diagnostics, err := compiler.DescribePath(name, root)
		assertDescriptionResult(t, diagnostics, err)
		if !reflect.DeepEqual(description.Symbol.TypeParameters, want) {
			t.Fatalf("describe %s type parameters = %+v, want %+v", name, description.Symbol.TypeParameters, want)
		}
	}

	description, diagnostics, err := compiler.DescribePath("root.Box", root)
	assertDescriptionResult(t, diagnostics, err)
	if len(description.Symbol.Fields) != 1 || description.Symbol.Fields[0].Type != "T" {
		t.Fatalf("describe root.Box fields = %+v", description.Symbol.Fields)
	}
	if len(description.Symbol.ImplementedMethods) != 1 || description.Symbol.ImplementedMethods[0].ReturnType != "T" {
		t.Fatalf("describe root.Box implemented methods = %+v", description.Symbol.ImplementedMethods)
	}

	description, diagnostics, err = compiler.DescribePath("root", root)
	assertDescriptionResult(t, diagnostics, err)
	for _, child := range description.Symbol.Children {
		if strings.ContainsRune(child.CanonicalName, '<') {
			t.Fatalf("describe root reports the instantiation %s", child.CanonicalName)
		}
	}

	if _, _, err := compiler.DescribePath("root.Box<int>", root); !errors.Is(err, compiler.ErrUnknownSymbol) {
		t.Fatalf("describe root.Box<int> = %v, want ErrUnknownSymbol", err)
	}
}
