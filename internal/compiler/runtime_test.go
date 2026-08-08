package compiler_test

import (
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestRunExecutesNamespacedDetachedMethod(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{
		{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { Name: string function Bark() -> string }"},
		{Name: "behavior.slk", Namespace: "root.models", Text: "function Dog.Bark() -> string { `${self.Name}: woof` }"},
		{Name: "main.slk", Namespace: "root", Text: "function main() -> string { let dog = root.models.Dog { Name: \"Ada\" } dog.Bark() }"},
	})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "Ada: woof" {
		t.Fatalf("expected method output, found %q", output)
	}
}

func TestRunDispatchesThroughInterface(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
interface Barker { function Bark() -> string }
class Dog { function Bark() -> string }
function Dog.Bark() -> string { "woof" }
function Speak(value: Barker) -> string { value.Bark() }
function main() -> string { let dog = Dog {} Speak(dog) }
`,
	}})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "woof" {
		t.Fatalf("expected interface dispatch output, found %q", output)
	}
}

func TestRunExecutesThrowAndExhaustiveCatch(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
class EmptyName implements Error {}
function RequireName(name: string) -> string throws EmptyName {
    if (name == "") { throw EmptyName("name is empty") }
    name
}
function main() -> string {
    RequireName("") catch (error) {
        EmptyName => "caught"
    }
}
`,
	}})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "caught" {
		t.Fatalf("expected caught error output, found %q", output)
	}
}

func TestRunPropagatesEarlyReturnThroughConditional(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> string { if (true) { return \"yes\" } \"no\" }",
	}})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "yes" {
		t.Fatalf("expected early return output, found %q", output)
	}
}

func TestRunStopsOnCompileDiagnostics(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> string { 42 }",
	}})
	if err != nil {
		t.Fatalf("compile invalid Slick: %v", err)
	}
	if output != "" {
		t.Fatalf("invalid program produced output %q", output)
	}
	assertDiagnostic(t, diagnostics, "SLK340", "body produces int")
}

func TestRunReturnsUncaughtSlickError(t *testing.T) {
	_, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "class Failure implements Error {} function main() -> string throws Failure { throw Failure(\"boom\") }",
	}})
	assertNoDiagnostics(t, diagnostics)
	if err == nil || !strings.Contains(err.Error(), "root.Failure: boom") {
		t.Fatalf("expected uncaught Slick error, found %v", err)
	}
}

func TestRunEnumeratesArrayWithBreakAndContinue(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function Collect(Names: string[]) -> string {
    let Output = ""
    for Index, Name in enumerate(Names) {
        if (Name == "skip") { continue }
        Output = Output + ` + "`${Index}:${Name};`" + `
        if (Name == "Grace") { break }
    }
    Output
}
function main() -> string { Collect(["Ada", "skip", "Grace", "Linus"]) }
`,
	}})
	if err != nil {
		t.Fatalf("run Slick enumerate loop: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "0:Ada;2:Grace;" {
		t.Fatalf("expected ranged output, found %q", output)
	}
}

func TestRunIteratesHalfOpenIntegerRange(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> int {
    let Sum = 0
    for Value in 1..4 {
        Sum = Sum + Value
    }
    Sum
}
`,
	}})
	if err != nil {
		t.Fatalf("run Slick integer range: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "6" {
		t.Fatalf("expected half-open range sum, found %q", output)
	}
}

func TestForRejectsNonIterableValue(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> null { for Value in 42 {} null }",
	}})
	assertDiagnostic(t, diagnostics, "SLK344", "for requires an iterable, found int")
}

func TestLoopControlRejectsUseOutsideLoop(t *testing.T) {
	for _, keyword := range []string{"break", "continue"} {
		t.Run(keyword, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{
				Name:      "main.slk",
				Namespace: "root",
				Text:      "function main() -> null { " + keyword + " }",
			}})
			assertDiagnostic(t, diagnostics, "SLK345", keyword+" is only valid inside a loop")
		})
	}
}

func TestRunZipSupportsBlankBinding(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> string {
    let Output = ""
    for _, Name in zip([1, 2], ["Ada", "Grace"]) {
        Output = Output + Name
    }
    Output
}
`,
	}})
	if err != nil {
		t.Fatalf("run Slick zip loop: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "AdaGrace" {
		t.Fatalf("expected zipped values output, found %q", output)
	}
}

func TestArraysAndLoopAssignmentsAreTypeChecked(t *testing.T) {
	t.Run("array elements", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text:      `function main() -> null { let Values = [1, "two"] null }`,
		}})
		assertDiagnostic(t, diagnostics, "SLK342", "array elements must share one type")
	})
	t.Run("assignment", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text:      `function main() -> null { let Count = 0 Count = "one" null }`,
		}})
		assertDiagnostic(t, diagnostics, "SLK342", "cannot assign string to Count of type int")
	})
}

func TestZipStopsAtShortestIterable(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> string {
    let Output = ""
    for Name, Number in zip(["Ada", "Grace"], 1..2) {
        Output = Output + Name + ` + "`${Number}`" + `
    }
    Output
}
`,
	}})
	if err != nil {
		t.Fatalf("run Slick uneven zip: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "Ada1" {
		t.Fatalf("expected shortest zip output, found %q", output)
	}
}

func TestLoopBindingArityIsChecked(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> null { for Left, Right in [1, 2] {} null }`,
	}})
	assertDiagnostic(t, diagnostics, "SLK346", "loop has 2 bindings, but the iterable produces int")
}

func TestRangeEndpointsMustBeIntegers(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> null { for Value in "a".."z" {} null }`,
	}})
	assertDiagnostic(t, diagnostics, "SLK342", "range start must be int")
	assertDiagnostic(t, diagnostics, "SLK342", "range end must be int")
}

func TestGoStyleRangeSyntaxIsRejected(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> null { for Index := range [1, 2] {} null }`,
	}})
	assertDiagnostic(t, diagnostics, "SLK001", "expected 'in' after loop bindings")
}

func TestRunForBindsArrayValues(t *testing.T) {
	output, diagnostics, err := compiler.Run([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
function main() -> string {
    let Output = ""
    for Name in ["Ada", "Grace"] {
        Output = Output + Name
    }
    Output
}
`,
	}})
	if err != nil {
		t.Fatalf("run Slick value loop: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "AdaGrace" {
		t.Fatalf("expected array values, found %q", output)
	}
}

func TestIterableBuiltinsRejectInvalidDeclarationsAndArguments(t *testing.T) {
	t.Run("reserved declaration", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text:      `function enumerate(Value: int) -> int { Value }`,
		}})
		assertDiagnostic(t, diagnostics, "SLK001", "function name enumerate is reserved")
	})
	t.Run("non-iterable argument", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text:      `function main() -> null { for Index, Value in enumerate(42) {} null }`,
		}})
		assertDiagnostic(t, diagnostics, "SLK344", "enumerate requires an iterable, found int")
	})
}
