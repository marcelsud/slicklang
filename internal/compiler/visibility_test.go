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

func TestVisibilityExamplePrivateSurfaceContracts(t *testing.T) {
	models := []compiler.Source{
		{
			Name:      "user.slk",
			Namespace: "root.models",
			Text: `
class User {
    Name: string
    secret: string
    function secret_label() -> string
}
function User.secret_label() -> string { format_secret(self.secret) }
function NewUser(Name: string) -> User { User { Name: Name secret: "private" } }
`,
		},
		{
			Name:      "private_helpers.slk",
			Namespace: "root.models",
			Text:      `function format_secret(Value: string) -> string { Value }`,
		},
	}

	t.Run("private declarations remain visible across files in their namespace", func(t *testing.T) {
		sources := append([]compiler.Source{}, models...)
		sources = append(sources, compiler.Source{
			Name:      "user_behavior.slk",
			Namespace: "root.models",
			Text:      `function reveal(User: User) -> string { format_secret(User.secret) + User.secret_label() }`,
		})
		assertNoDiagnostics(t, compiler.Check(sources))
	})

	tests := []struct {
		name    string
		source  string
		message string
	}{
		{
			name:    "construct private field",
			source:  `function main() -> null { let User = root.models.User { Name: "Ada" secret: "x" } }`,
			message: "field secret is private to root.models",
		},
		{
			name:    "read private field",
			source:  `function main() -> string { let User = root.models.NewUser("Ada") User.secret }`,
			message: "field secret is private to root.models",
		},
		{
			name:    "call private method",
			source:  `function main() -> string { let User = root.models.NewUser("Ada") User.secret_label() }`,
			message: "method secret_label is private to root.models",
		},
		{
			name:    "call private function",
			source:  `function main() -> string { root.models.format_secret("x") }`,
			message: "function format_secret is private to root.models",
		},
		{
			name:    "alias private function",
			source:  "use root.models.format_secret as FormatSecret\nfunction main() -> null {}",
			message: "function format_secret is private to root.models",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := append([]compiler.Source{}, models...)
			sources = append(sources, compiler.Source{Name: "app.slk", Namespace: "root", Text: test.source})
			diagnostics := compiler.Check(sources)
			if len(diagnostics) != 1 {
				t.Fatalf("expected one visibility diagnostic, got %+v", diagnostics)
			}
			assertDiagnostic(t, diagnostics, "SLK330", test.message)
		})
	}
}

func TestPrivateInterfaceMethodsAreNamespaceSpecific(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "barker.slk", Namespace: "root.contracts", Text: "interface Barker { function bark() -> string }"},
		{Name: "dog.slk", Namespace: "root.models", Text: "class Dog { function bark() -> string } function Dog.bark() -> string { \"woof\" }"},
		{Name: "app.slk", Namespace: "root", Text: "function Use(value: root.contracts.Barker) -> null {} function main() -> null { let dog = root.models.Dog {} Use(dog) }"},
	})
	assertDiagnostic(t, diagnostics, "SLK320", "private method bark belongs to root.contracts")
}
