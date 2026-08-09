package compiler_test

import (
	"testing"

	"slick/internal/compiler"
)

func TestExactNameUseResolvesDeclarations(t *testing.T) {
	tests := map[string][]compiler.Source{
		"class": {
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog {}"},
			{Name: "main.slk", Namespace: "root", Text: "use root.models.Dog\nfunction main() -> Dog { Dog {} }"},
		},
		"interface": {
			{Name: "greeter.slk", Namespace: "root.contracts", Text: "interface Greeter { function Greet() -> string }"},
			{Name: "main.slk", Namespace: "root", Text: "use root.contracts.Greeter\nfunction Identity(Value: Greeter) -> Greeter { Value }"},
		},
		"function": {
			{Name: "greet.slk", Namespace: "root.services", Text: `function Greet() -> string { "hello" }`},
			{Name: "main.slk", Namespace: "root", Text: "use root.services.Greet\nfunction main() -> string { Greet() }"},
		},
		"same namespace": {
			{Name: "main.slk", Namespace: "root", Text: "use root.Dog\nclass Dog {}\nfunction main() -> Dog { Dog {} }"},
		},
		"standard library": {
			{Name: "main.slk", Namespace: "root", Text: "use std.convert.IntToString\nfunction main() -> string { IntToString(42) }"},
		},
		"explicit rename": {
			{Name: "dog.slk", Namespace: "root.models", Text: "class Dog {}"},
			{Name: "main.slk", Namespace: "root", Text: "use root.models.Dog as Animal\nfunction main() -> Animal { Animal {} }"},
		},
	}
	for name, sources := range tests {
		t.Run(name, func(t *testing.T) {
			assertNoDiagnostics(t, compiler.Check(sources))
		})
	}
}

func TestExactNameUseExecutesImportedCallableEverywhere(t *testing.T) {
	output := runResultEverywhere(t, `
use std.convert.IntToString
function main() -> string { IntToString(42) }
`)
	if output != "42" {
		t.Fatalf("imported callable output = %q", output)
	}
}

func TestExactNameUseRejectsInvalidImports(t *testing.T) {
	tests := map[string]struct {
		sources []compiler.Source
		code    string
		message string
	}{
		"missing target": {
			sources: []compiler.Source{{Name: "main.slk", Namespace: "root", Text: "use root.models.Dog\nfunction main() -> null {}"}},
			code:    "SLK204",
			message: "root.models.Dog does not exist",
		},
		"private target": {
			sources: []compiler.Source{
				{Name: "dog.slk", Namespace: "root.models", Text: "class dog {}"},
				{Name: "main.slk", Namespace: "root", Text: "use root.models.dog\nfunction main() -> null {}"},
			},
			code:    "SLK330",
			message: "class dog is private to root.models",
		},
		"exact-name collision": {
			sources: []compiler.Source{
				{Name: "one.slk", Namespace: "root.one", Text: "class Dog {}"},
				{Name: "two.slk", Namespace: "root.two", Text: "class Dog {}"},
				{Name: "main.slk", Namespace: "root", Text: "use root.one.Dog\nuse root.two.Dog\nfunction main() -> null {}"},
			},
			code:    "SLK001",
			message: "duplicate alias Dog",
		},
		"explicit alias collision": {
			sources: []compiler.Source{
				{Name: "dog.slk", Namespace: "root.one", Text: "class Dog {}"},
				{Name: "cat.slk", Namespace: "root.two", Text: "class Cat {}"},
				{Name: "main.slk", Namespace: "root", Text: "use root.one.Dog\nuse root.two.Cat as Dog\nfunction main() -> null {}"},
			},
			code:    "SLK001",
			message: "duplicate alias Dog",
		},
		"local declaration collision": {
			sources: []compiler.Source{
				{Name: "remote.slk", Namespace: "root.models", Text: "class Dog {}"},
				{Name: "main.slk", Namespace: "root", Text: "use root.models.Dog\nclass Dog {}\nfunction main() -> null {}"},
			},
			code:    "SLK204",
			message: "alias Dog conflicts with a declaration in root",
		},
		"missing final identifier": {
			sources: []compiler.Source{{Name: "main.slk", Namespace: "root", Text: "use root.models.\nfunction main() -> null {}"}},
			code:    "SLK001",
			message: "use target must end in an identifier",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assertDiagnostic(t, compiler.Check(test.sources), test.code, test.message)
		})
	}
}

func TestSameNamedImportsAllowExplicitDisambiguation(t *testing.T) {
	assertNoDiagnostics(t, compiler.Check([]compiler.Source{
		{Name: "one.slk", Namespace: "root.one", Text: "class Dog {}"},
		{Name: "two.slk", Namespace: "root.two", Text: "class Dog {}"},
		{Name: "main.slk", Namespace: "root", Text: "use root.one.Dog\nuse root.two.Dog as OtherDog\nfunction main() -> null { let First = Dog {} let Second = OtherDog {} }"},
	}))
}
