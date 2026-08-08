package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

const errorsAndLoader = `
class IoError implements Error {}
class ParseError implements Error {}

function read_file() -> string throws IoError {
    throw IoError {}
}

function parse() -> string throws ParseError {
    throw ParseError {}
}

function load() -> string throws IoError | ParseError {
    read_file()
    parse()
}
`

func TestDeclaredErrorsPropagate(t *testing.T) {
	source := errorsAndLoader + `
function main() -> string throws IoError | ParseError {
    load()
}
`
	assertNoDiagnostics(t, compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}}))
}

func TestCatchMustBeExhaustive(t *testing.T) {
	source := errorsAndLoader + `
function main() -> null {
    load() catch (error) {
        IoError => null
    }
}
`
	diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	assertDiagnostic(t, diagnostics, "SLK202", "missing ParseError")
}

func TestConcreteAndBaseErrorCatchesAreExhaustive(t *testing.T) {
	tests := map[string]string{
		"concrete arms": `
function main() -> string {
    load() catch (error) {
        IoError => ""
        ParseError => ""
    }
}
`,
		"base Error": `
function main() -> string {
    load() catch (error) {
        Error => ""
    }
}
`,
	}
	for name, main := range tests {
		t.Run(name, func(t *testing.T) {
			assertNoDiagnostics(t, compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: errorsAndLoader + main}}))
		})
	}
}

func TestUnhandledErrorIsRejected(t *testing.T) {
	source := errorsAndLoader + `
function main() -> null {
    load()
}
`
	diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	assertDiagnostic(t, diagnostics, "SLK201", "unhandled IoError")
	assertDiagnostic(t, diagnostics, "SLK201", "unhandled ParseError")
}

func TestFolderNamespacesResolveInlineAndLocally(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "animals", "errors.slk"), `
class BarkError implements Error {}
`)
	writeSource(t, filepath.Join(root, "animals", "dog.slk"), `
class Dog {}

function Bark() -> null throws BarkError {
    let dog = Dog {}
    throw BarkError("test")
}
`)
	writeSource(t, filepath.Join(root, "app.slk"), `
function main() -> null {
    let dog = root.animals.Dog {}
    root.animals.Bark() catch (error) {
        root.animals.BarkError => null
    }
}
`)

	diagnostics, err := compiler.CheckPath(root)
	if err != nil {
		t.Fatalf("check project: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
}

func TestUseCreatesOptionalAlias(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{
		{Name: "dog.slk", Namespace: "root.animals", Text: "class Dog {}"},
		{
			Name:      "app.slk",
			Namespace: "root",
			Text: `
use root.animals.Dog as MyDoggie

function main() -> null {
    let dog = MyDoggie {}
}
`,
		},
	})
	assertNoDiagnostics(t, diagnostics)
}

func TestUnknownInlineClassIsRejected(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> null { let dog = root.models.Cat {} }",
	}})
	assertDiagnostic(t, diagnostics, "SLK205", "unknown class root.models.Cat")
}

func TestUnknownAliasTargetIsRejected(t *testing.T) {
	diagnostics := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "use root.animals.Cat as Cat\nfunction main() -> null {}",
	}})
	assertDiagnostic(t, diagnostics, "SLK204", "root.animals.Cat does not exist")
}

func TestProjectDiscoveryUsesSlkFilesOnly(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "legacy.tst"), "function main() -> null {}")

	_, err := compiler.CheckPath(root)
	if !errors.Is(err, compiler.ErrNoSources) {
		t.Fatalf("expected ErrNoSources for legacy extension, found %v", err)
	}

	writeSource(t, filepath.Join(root, "main.slk"), "function main() -> null {}")
	diagnostics, err := compiler.CheckPath(root)
	if err != nil {
		t.Fatalf("check Slick project: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
}

func TestBranchResultsMustShareOneType(t *testing.T) {
	t.Run("if", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text:      `function main() -> string { if (true) { "yes" } else { 0 } }`,
		}})
		assertDiagnostic(t, diagnostics, "SLK342", "if branches must produce one type")
	})
	t.Run("catch", func(t *testing.T) {
		diagnostics := compiler.Check([]compiler.Source{{
			Name:      "main.slk",
			Namespace: "root",
			Text: `
class Failure implements Error {}
function load() -> string throws Failure { throw Failure {} }
function main() -> string {
    load() catch (error) {
        Failure => null
    }
}
`,
		}})
		assertDiagnostic(t, diagnostics, "SLK342", "catch success and error paths must produce one type")
	})
}

func assertNoDiagnostics(t *testing.T, diagnostics []compiler.Diagnostic) {
	t.Helper()
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
}

func assertDiagnostic(t *testing.T, diagnostics []compiler.Diagnostic, code, message string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, message) {
			return
		}
	}
	t.Fatalf("missing %s containing %q in %+v", code, message, diagnostics)
}

func writeSource(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
}
