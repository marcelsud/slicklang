package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestLowercaseDeclarationsArePrivateToTheirNamespace(t *testing.T) {
	t.Run("same namespace", func(t *testing.T) {
		assertNoDiagnostics(t, compiler.Check([]compiler.Source{{
			Name:      "private.slk",
			Namespace: "root.models",
			Text: `
class dog {
    name: string
    function bark() -> string
}
function dog.bark() -> string { "woof" }
function hidden() -> null {}
function main() -> null {
    let value = dog { name: "Ada" }
    value.bark()
    hidden()
}
`,
		}}))
	})

	t.Run("class across namespace", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class dog {}"},
			{Name: "app.slk", Namespace: "root", Text: "function main() -> null { let value = root.models.dog {} }"},
		})
		assertDiagnostic(t, diagnostics, "SLK330", "class dog is private to root.models")
	})

	t.Run("function across namespace", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "helpers.slk", Namespace: "root.models", Text: "function hidden() -> null {}"},
			{Name: "app.slk", Namespace: "root", Text: "function main() -> null { root.models.hidden() }"},
		})
		assertDiagnostic(t, diagnostics, "SLK330", "function hidden is private to root.models")
	})
}

func TestUppercaseDeclarationsArePublic(t *testing.T) {
	assertNoDiagnostics(t, compiler.Check([]compiler.Source{
		{
			Name:      "dog.slk",
			Namespace: "root.models",
			Text: `
class Dog {
    Name: string
    function Bark() -> string
}
function Dog.Bark() -> string { "woof" }
function Visible() -> null {}
`,
		},
		{
			Name:      "app.slk",
			Namespace: "root",
			Text: `
function main() -> null {
    let dog = root.models.Dog { Name: "Ada" }
    dog.Bark()
    root.models.Visible()
}
`,
		},
	}))
}

func TestLowercaseFieldsAndMethodsArePrivate(t *testing.T) {
	t.Run("field", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { name: string }"},
			{Name: "app.slk", Namespace: "root", Text: "function main() -> null { let dog = root.models.Dog { name: \"Ada\" } }"},
		})
		assertDiagnostic(t, diagnostics, "SLK330", "field name is private to root.models")
	})

	t.Run("method", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { function bark() -> string } function Dog.bark() -> string { \"woof\" }"},
			{Name: "app.slk", Namespace: "root", Text: "function main() -> null { let dog = root.models.Dog {} dog.bark() }"},
		})
		assertDiagnostic(t, diagnostics, "SLK330", "method bark is private to root.models")
	})
}

func TestUseCannotAliasPrivateDeclarationAcrossNamespaces(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "dog.slk", Namespace: "root.models", Text: "class dog {}"},
		{Name: "app.slk", Namespace: "root", Text: "use root.models.dog as Dog\nfunction main() -> null {}"},
	})
	assertDiagnostic(t, diagnostics, "SLK330", "class dog is private to root.models")
}

func TestPrivateInterfaceMethodsAreNamespaceSpecific(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "barker.slk", Namespace: "root.contracts", Text: "interface Barker { function bark() -> string }"},
		{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { function bark() -> string } function Dog.bark() -> string { \"woof\" }"},
		{Name: "app.slk", Namespace: "root", Text: "function Use(value: root.contracts.Barker) -> null {} function main() -> null { let dog = root.models.Dog {} Use(dog) }"},
	})
	assertDiagnostic(t, diagnostics, "SLK320", "private method bark belongs to root.contracts")
}
