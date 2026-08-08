package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestDetachedMethodCompletesClassRequirement(t *testing.T) {
	assertNoDiagnostics(t, checkRoot(`
class Dog {
    name: string
    function bark() -> string
}

function Dog.bark() -> string {
    "woof"
}

function main() -> string {
    let dog = Dog { name: "Ada" }
    dog.bark()
}
`))
}

func TestMissingClassMethodImplementationIsRejected(t *testing.T) {
	diagnostics := checkRoot(`
class Dog {
    function bark() -> string
}
`)
	assertDiagnostic(t, diagnostics, "SLK310", "root.Dog.bark has no implementation")
}

func TestInterfaceMethodsRemainBodyless(t *testing.T) {
	assertNoDiagnostics(t, checkRoot(`
interface Barker {
    function bark() -> string
}
`))
}

func TestInlineMethodWorksWhenDetachedMethodsAreDisabled(t *testing.T) {
	assertNoDiagnostics(t, checkRoot(`
class Credential extension(none) {
    function redact() -> string {
        "***"
    }
}
`))
}

func TestExtensionPoliciesControlDetachedImplementations(t *testing.T) {
	t.Run("namespace allows owning namespace", func(t *testing.T) {
		assertNoDiagnostics(t, compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { function bark() -> string }"},
			{Name: "bark.slk", Namespace: "root.models", Text: "function Dog.bark() -> string { \"woof\" }"},
		}))
	})

	t.Run("namespace rejects another namespace", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { function Bark() -> string }"},
			{Name: "bark.slk", Namespace: "root.behaviors", Text: "function root.models.Dog.Bark() -> string { \"woof\" }"},
		})
		assertDiagnostic(t, diagnostics, "SLK313", "allows method implementations only from root.models")
	})

	t.Run("global allows another namespace", func(t *testing.T) {
		assertNoDiagnostics(t, compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog extension(global) { function Bark() -> string }"},
			{Name: "bark.slk", Namespace: "root.behaviors", Text: "function root.models.Dog.Bark() -> string { \"woof\" }"},
		}))
	})

	t.Run("global still rejects private methods from another namespace", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: `class Dog extension(global) {
    function bark() -> string { "woof" }
}`},
			{Name: "bark.slk", Namespace: "root.behaviors", Text: `function root.models.Dog.bark() -> string { "again" }`},
		})
		assertDiagnostic(t, diagnostics, "SLK330", "method bark is private to root.models")
	})

	t.Run("none rejects detached implementation", func(t *testing.T) {
		diagnostics := checkRoot(`
class Dog extension(none) {
    function bark() -> string
}
function Dog.bark() -> string { "woof" }
`)
		assertDiagnostic(t, diagnostics, "SLK313", "does not allow detached method implementations")
	})
}

func TestDuplicateAndMismatchedImplementationsAreRejected(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		diagnostics := checkRoot(`
class Dog { function bark() -> string }
function Dog.bark() -> string { "woof" }
function Dog.bark() -> string { "again" }
`)
		assertDiagnostic(t, diagnostics, "SLK311", "duplicate implementation of root.Dog.bark")
	})

	t.Run("signature", func(t *testing.T) {
		diagnostics := checkRoot(`
class Dog { function bark(volume: int) -> string }
function Dog.bark(volume: string) -> string { volume }
`)
		assertDiagnostic(t, diagnostics, "SLK312", "parameter 1 must be int, found string")
	})

	t.Run("result", func(t *testing.T) {
		diagnostics := checkRoot(`
class Dog { function bark() -> string }
function Dog.bark() -> int { 1 }
`)
		assertDiagnostic(t, diagnostics, "SLK312", "result must be string, found int")
	})

	t.Run("undeclared method", func(t *testing.T) {
		diagnostics := checkRoot(`
class Dog extension(global) {}
function Dog.bark() -> string { "woof" }
`)
		assertDiagnostic(t, diagnostics, "SLK314", "root.Dog.bark is not declared by root.Dog")
	})
}

func TestMethodReceiverCannotUseAlias(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "dog.slk", Namespace: "root.models", Text: "class Dog extension(global) { function Bark() -> string }"},
		{Name: "bark.slk", Namespace: "root.behaviors", Text: `use root.models.Dog as Hound
function Hound.Bark() -> string { "woof" }`},
	})
	assertDiagnostic(t, diagnostics, "SLK315", "not alias Hound")
}

func TestImplicitInterfaceConformanceIsCheckedAtUse(t *testing.T) {
	program := `
interface Barker {
    function bark() -> string
}

class Dog {
    function bark() -> string
}

function Dog.bark() -> string { "woof" }

function speak(animal: Barker) -> string {
    animal.bark()
}

function main() -> string {
    let dog = Dog {}
    speak(dog)
}
`
	assertNoDiagnostics(t, checkRoot(program))
}

func TestMissingInterfaceMethodIsRejectedAtUse(t *testing.T) {
	diagnostics := checkRoot(`
interface Barker {
    function bark() -> string
}

class Cat {}

function speak(animal: Barker) -> string {
    animal.bark()
}

function main() -> string {
    let cat = Cat {}
    speak(cat)
}
`)
	assertDiagnostic(t, diagnostics, "SLK320", "root.Cat does not implement root.Barker: missing bark")
}

func TestMethodImplementationCannotAddErrorEffects(t *testing.T) {
	diagnostics := checkRoot(`
class BarkError implements Error {}
class NetworkError implements Error {}

class Dog {
    function bark() -> string throws BarkError
}

function Dog.bark() -> string throws BarkError | NetworkError {
    throw NetworkError("offline")
}
`)
	assertDiagnostic(t, diagnostics, "SLK312", "undeclared error effect NetworkError")
}

func TestInterfaceMethodErrorsMustBeHandled(t *testing.T) {
	diagnostics := checkRoot(`
class BarkError implements Error {}

interface Barker {
    function bark() -> string throws BarkError
}

function speak(animal: Barker) -> string {
    animal.bark()
}
`)
	assertDiagnostic(t, diagnostics, "SLK201", "unhandled BarkError from animal.bark")
}

func checkRoot(source string) []compiler.Diagnostic {
	return compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
}
