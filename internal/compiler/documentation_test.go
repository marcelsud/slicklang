package compiler_test

import (
	"errors"
	"reflect"
	"testing"

	"slick/internal/compiler"
)

func TestDescribePreservesDeclarationDocumentation(t *testing.T) {
	path := writeProject(t, map[string]string{
		"models.slk": `
/// A documented application failure.
class Failure implements Error {
    /// Explains the failure.
    Message: string
}

/// Greets a person.
interface Greeter {
    /// Produces a greeting for Name.
    function Greet(Name: string) -> string
}

/// Stores a display name.
class User {
    /// The public display name.
    Name: string
    /// Declares the greeting contract.
    function Greet(Name: string) -> string
}

/// Implements the greeting contract.
///
/// The implementation keeps the supplied spelling.
function User.Greet(Name: string) -> string { Name }

/// Loads a user by name.
///
/// Returns a new immutable value.
function Load(Name: string) -> User { User { Name: Name } }

// Ordinary comments are not documentation.
function Undocumented() -> null { null }
`,
	})

	load, diagnostics, err := compiler.DescribePath("root.Load", path)
	assertDescriptionResult(t, diagnostics, err)
	assertDocumentation(t, load.Symbol.Documentation, "Loads a user by name.\n\nReturns a new immutable value.")

	user, diagnostics, err := compiler.DescribePath("root.User", path)
	assertDescriptionResult(t, diagnostics, err)
	assertDocumentation(t, user.Symbol.Documentation, "Stores a display name.")
	assertDocumentation(t, user.Symbol.Fields[0].Documentation, "The public display name.")
	assertDocumentation(t, user.Symbol.DeclaredMethods[0].Documentation, "Declares the greeting contract.")
	assertDocumentation(t, user.Symbol.ImplementedMethods[0].Documentation, "Implements the greeting contract.\n\nThe implementation keeps the supplied spelling.")

	method, diagnostics, err := compiler.DescribePath("root.User.Greet", path)
	assertDescriptionResult(t, diagnostics, err)
	if method.Symbol.Kind != "method" {
		t.Fatalf("root.User.Greet kind = %q, want method", method.Symbol.Kind)
	}
	assertDocumentation(t, method.Symbol.Documentation, "Implements the greeting contract.\n\nThe implementation keeps the supplied spelling.")

	field, diagnostics, err := compiler.DescribePath("root.User.Name", path)
	assertDescriptionResult(t, diagnostics, err)
	if field.Symbol.Kind != "field" || field.Symbol.Type != "string" {
		t.Fatalf("root.User.Name = %+v", field.Symbol)
	}
	assertDocumentation(t, field.Symbol.Documentation, "The public display name.")

	failure, diagnostics, err := compiler.DescribePath("root.Failure", path)
	assertDescriptionResult(t, diagnostics, err)
	assertDocumentation(t, failure.Symbol.Documentation, "A documented application failure.")
	assertDocumentation(t, failure.Symbol.Fields[0].Documentation, "Explains the failure.")

	greeter, diagnostics, err := compiler.DescribePath("root.Greeter", path)
	assertDescriptionResult(t, diagnostics, err)
	assertDocumentation(t, greeter.Symbol.Documentation, "Greets a person.")
	assertDocumentation(t, greeter.Symbol.DeclaredMethods[0].Documentation, "Produces a greeting for Name.")

	undocumented, diagnostics, err := compiler.DescribePath("root.Undocumented", path)
	assertDescriptionResult(t, diagnostics, err)
	if undocumented.Symbol.Documentation != nil {
		t.Fatalf("ordinary comment became documentation: %q", *undocumented.Symbol.Documentation)
	}
}

func TestNamespaceChildrenCarryDocumentationDeterministically(t *testing.T) {
	path := writeProject(t, map[string]string{
		"a.slk": "/// Alpha summary.\nfunction Alpha() -> null { null }\n",
		"b.slk": "/// Beta summary.\nclass Beta {}\n",
	})
	description, diagnostics, err := compiler.DescribePath("root", path)
	assertDescriptionResult(t, diagnostics, err)
	got := make([]string, 0, len(description.Symbol.Children))
	for _, child := range description.Symbol.Children {
		documentation := ""
		if child.Documentation != nil {
			documentation = *child.Documentation
		}
		got = append(got, child.CanonicalName+":"+documentation)
	}
	want := []string{"root.Alpha:Alpha summary.", "root.Beta:Beta summary."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("documented children = %v, want %v", got, want)
	}
}

func TestDocumentationDiagnosticsAreFocused(t *testing.T) {
	tests := map[string]string{
		"blank line breaks attachment": `/// Orphaned.

function main() -> null { null }
`,
		"non declaration target": `function main() -> null {
    /// Not a declaration.
    let Value = null
    Value
}
`,
		"orphan at end": `function main() -> null { null }
/// Orphaned.
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
			if len(diagnostics) != 1 {
				t.Fatalf("diagnostics = %+v, want one", diagnostics)
			}
			assertDiagnostic(t, diagnostics, "SLK391", "not attached to a describable declaration")
		})
	}
}

func TestCompetingDocumentationIsRejected(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "a.slk", Namespace: "root", Text: "/// First.\nfunction Load() -> null { null }"},
		{Name: "b.slk", Namespace: "root", Text: "/// Second.\nfunction Load() -> null { null }"},
	})
	assertDiagnostic(t, diagnostics, "SLK392", "competing documentation for root.Load")
}

func TestAliasDoesNotCopyDocumentation(t *testing.T) {
	path := writeProject(t, map[string]string{
		"models/user.slk": "/// Canonical user documentation.\nclass User {}\n",
		"main.slk":        "use root.models.User as Person\nfunction Load() -> Person { Person {} }\n",
	})
	description, diagnostics, err := compiler.DescribePath("root.models.User", path)
	assertDescriptionResult(t, diagnostics, err)
	assertDocumentation(t, description.Symbol.Documentation, "Canonical user documentation.")
	if _, diagnostics, err := compiler.DescribePath("root.Person", path); !errors.Is(err, compiler.ErrUnknownSymbol) || len(diagnostics) != 0 {
		t.Fatalf("alias unexpectedly became describable: diagnostics=%+v err=%v", diagnostics, err)
	}
}

func assertDocumentation(t *testing.T, documentation *string, want string) {
	t.Helper()
	if documentation == nil || *documentation != want {
		t.Fatalf("documentation = %v, want %q", documentation, want)
	}
}
