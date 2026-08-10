package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"slick/internal/compiler"
)

func TestDescribeCompilerOwnedSymbols(t *testing.T) {
	for _, name := range []string{"bool", "bytes", "float", "int", "null", "string"} {
		description, diagnostics, err := compiler.DescribePath(name, "")
		assertDescriptionResult(t, diagnostics, err)
		if description.Symbol.CanonicalName != name || description.Symbol.Kind != "language primitive" {
			t.Fatalf("describe %s = %+v", name, description.Symbol)
		}
	}

	for name, want := range map[string][]string{
		"Iterable": {"T"},
		"Map":      {"K", "V"},
		"Result":   {"T", "E"},
	} {
		description, diagnostics, err := compiler.DescribePath(name, "")
		assertDescriptionResult(t, diagnostics, err)
		if description.Symbol.Kind != "generic type" || !reflect.DeepEqual(description.Symbol.TypeParameters, want) {
			t.Fatalf("describe %s = %+v", name, description.Symbol)
		}
	}

	description, diagnostics, err := compiler.DescribePath("Error", "")
	assertDescriptionResult(t, diagnostics, err)
	if description.Symbol.Kind != "interface" {
		t.Fatalf("describe Error = %+v", description.Symbol)
	}
}

func TestDescribeStandardLibraryFromRegistry(t *testing.T) {
	function, diagnostics, err := compiler.DescribePath("std.env.Set", "")
	assertDescriptionResult(t, diagnostics, err)
	if function.Symbol.Kind != "function" || !function.Symbol.Native {
		t.Fatalf("describe std.env.Set = %+v", function.Symbol)
	}
	if got, want := function.Symbol.Parameters, []compiler.ParameterDescription{{Name: "Name", Type: "string", Annotations: []compiler.AnnotationDescription{}}, {Name: "Value", Type: "string", Annotations: []compiler.AnnotationDescription{}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("std.env.Set parameters = %+v, want %+v", got, want)
	}
	if function.Symbol.ReturnType != "Result<null,std.env.Failure>" || len(function.Symbol.Throws) != 0 {
		t.Fatalf("std.env.Set return/effects = %s / %+v", function.Symbol.ReturnType, function.Symbol.Throws)
	}

	failure, diagnostics, err := compiler.DescribePath("std.env.Failure", "")
	assertDescriptionResult(t, diagnostics, err)
	if got, want := fieldNames(failure.Symbol.Fields), []string{"Message", "Name", "Operation"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("std.env.Failure fields = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(failure.Symbol.Interfaces, []string{"Error"}) {
		t.Fatalf("std.env.Failure interfaces = %+v", failure.Symbol.Interfaces)
	}

	namespace, diagnostics, err := compiler.DescribePath("std.env", "")
	assertDescriptionResult(t, diagnostics, err)
	got := make([]string, 0, len(namespace.Symbol.Children))
	for _, child := range namespace.Symbol.Children {
		got = append(got, child.Kind+" "+child.CanonicalName)
	}
	want := []string{
		"class std.env.Failure",
		"function std.env.Get",
		"function std.env.Set",
		"function std.env.Unset",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("std.env children = %+v, want %+v", got, want)
	}
}

func TestDescribeUserDeclarationsAfterSemanticChecks(t *testing.T) {
	path := writeProject(t, map[string]string{
		"models/user.slk": `class Failure implements Error {
    Message: string
}
interface Greeter {
    function Greet(Name: string) -> string throws Failure
}
class User implements Greeter {
    Name: string
    secret: string
    function Greet(Name: string) -> string throws Failure { Name }
}
function Load(Name: string) -> User throws Failure {
    User { Name: Name secret: "hidden" }
}
`,
	})

	class, diagnostics, err := compiler.DescribePath("root.models.User", path)
	assertDescriptionResult(t, diagnostics, err)
	if class.Symbol.Visibility != "public" || class.Symbol.Source == nil || class.Symbol.Source.File != "models/user.slk" || class.Symbol.Source.Line != 7 {
		t.Fatalf("root.models.User source/visibility = %+v", class.Symbol)
	}
	if got, want := fieldNames(class.Symbol.Fields), []string{"Name", "secret"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root.models.User fields = %+v, want %+v", got, want)
	}
	if class.Symbol.Fields[0].Visibility != "public" || class.Symbol.Fields[1].Visibility != "private" {
		t.Fatalf("root.models.User field visibility = %+v", class.Symbol.Fields)
	}
	if len(class.Symbol.DeclaredMethods) != 1 || len(class.Symbol.ImplementedMethods) != 1 {
		t.Fatalf("root.models.User methods = declared %+v, implemented %+v", class.Symbol.DeclaredMethods, class.Symbol.ImplementedMethods)
	}
	if got, want := class.Symbol.DeclaredMethods[0].Throws, []string{"root.models.Failure"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root.models.User.Greet throws = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(class.Symbol.Interfaces, []string{"root.models.Greeter"}) {
		t.Fatalf("root.models.User interfaces = %+v", class.Symbol.Interfaces)
	}

	function, diagnostics, err := compiler.DescribePath("root.models.Load", path)
	assertDescriptionResult(t, diagnostics, err)
	if function.Symbol.ReturnType != "root.models.User" || !reflect.DeepEqual(function.Symbol.Throws, []string{"root.models.Failure"}) {
		t.Fatalf("root.models.Load = %+v", function.Symbol)
	}
}

func TestDescribeRejectsUnknownSymbolsAndInvalidProjects(t *testing.T) {
	if _, diagnostics, err := compiler.DescribePath("std.env.Missing", ""); !errors.Is(err, compiler.ErrUnknownSymbol) || len(diagnostics) != 0 {
		t.Fatalf("unknown symbol result: diagnostics=%+v err=%v", diagnostics, err)
	}

	path := writeProject(t, map[string]string{"main.slk": `function main() -> Missing {}`})
	description, diagnostics, err := compiler.DescribePath("root.main", path)
	if err != nil || len(diagnostics) == 0 || description.SchemaVersion != 0 {
		t.Fatalf("invalid project result: description=%+v diagnostics=%+v err=%v", description, diagnostics, err)
	}
}

func assertDescriptionResult(t *testing.T, diagnostics []compiler.Diagnostic, err error) {
	t.Helper()
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("describe failed: diagnostics=%+v err=%v", diagnostics, err)
	}
}

func fieldNames(fields []compiler.FieldDescription) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	path := t.TempDir()
	for name, text := range files {
		file := filepath.Join(path, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("create project directory: %v", err)
		}
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatalf("write project source: %v", err)
		}
	}
	return path
}
