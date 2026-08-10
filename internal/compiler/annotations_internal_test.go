package compiler

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testBeforeTerminal() terminalAnnotationDecl {
	return terminalAnnotationDecl{
		canonical:  "std.test.Before",
		params:     []string{"(std.io.BytesWriter)->null throws std.io.Failure"},
		targets:    []annotationTarget{annotationTargetMethod},
		repeatable: false,
		apply: func(program *program, target annotationTargetRef, annotation resolvedAnnotation) {
			if target.function == nil || len(target.function.params) == 0 {
				program.add(annotation.authored.pos, diagnosticCodeAnnotationTarget, "annotation std.test.Before requires an implemented method with at least one parameter")
				return
			}
			hook := annotation.values[0].function
			call := &callExpression{
				callee: &nameExpression{name: hook.qualified, pos: annotation.authored.pos},
				args: []expressionNode{&nameExpression{
					name: target.function.params[0].name,
					pos:  annotation.authored.pos,
				}},
				pos: annotation.authored.pos,
			}
			target.function.ast.statements = append([]statementNode{&expressionStatement{value: call, pos: annotation.authored.pos}}, target.function.ast.statements...)
		},
	}
}

func testAnnotationTerminals() []terminalAnnotationDecl {
	return []terminalAnnotationDecl{
		testBeforeTerminal(),
		{canonical: "std.test.Marker", targets: []annotationTarget{annotationTargetClass, annotationTargetMethod, annotationTargetParameter}},
		{canonical: "std.test.Tag", params: []string{"string"}, targets: []annotationTarget{annotationTargetClass}, repeatable: true},
		{canonical: "std.test.Choice", params: []string{"root.Method"}, targets: []annotationTarget{annotationTargetClass}},
	}
}

func compileAnnotationProgram(t *testing.T, sources ...Source) *program {
	t.Helper()
	program, diagnostics := compileWithTerminals(sources, testAnnotationTerminals())
	if len(diagnostics) > 0 {
		t.Fatalf("compile annotations: %+v", diagnostics)
	}
	return program
}

func checkAnnotationProgram(sources ...Source) []Diagnostic {
	_, diagnostics := compileWithTerminals(sources, testAnnotationTerminals())
	return diagnostics
}

func requireAnnotationDiagnostic(t *testing.T, diagnostics []Diagnostic, code, message string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, message) {
			return
		}
	}
	t.Fatalf("missing %s containing %q in %+v", code, message, diagnostics)
}

func runGeneratedAnnotationProgram(t *testing.T, program *program) string {
	t.Helper()
	generated, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	formatted, err := format.Source([]byte(generated))
	if err != nil {
		t.Fatalf("format generated Go: %v", err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	binary := filepath.Join(root, "app")
	if err := os.WriteFile(source, formatted, 0o644); err != nil {
		t.Fatalf("write generated Go: %v", err)
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build generated Go: %v: %s", err, output)
	}
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run generated binary: %v: %s", err, output)
	}
	return string(output)
}

func TestAnnotationAliasRunsTerminalOnceOnBothBackends(t *testing.T) {
	program := compileAnnotationProgram(t, Source{Name: "main.slk", Namespace: "root", Text: `
use std.test.Before

annotation Around(Hook: (std.io.BytesWriter) -> null throws std.io.Failure) =
    @Before(Hook)

function Raise(Failure: std.io.Failure) -> null throws std.io.Failure {
    throw Failure
}

function WriteText(Writer: std.io.BytesWriter, Text: string) -> null throws std.io.Failure {
    match Writer.Write(std.bytes.FromUtf8(Text)) {
        Ok(Count) => null
        Err(Failure) => Raise(Failure)
    }
}

function Prefix(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
    WriteText(Writer, "A")
}

class Service<T> {
    @Around(Prefix)
    function Run(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        WriteText(Writer, "B")
    }
}

function main() -> string throws std.io.Failure {
    using Writer = std.io.WriterToBytes() {
        let App = Service<int> {}
        App.Run(Writer)
        match std.bytes.ToUtf8(Writer.Bytes()) {
            Ok(Text) => Text
            Err(Failure) => Failure.Message
        }
    }
}
`})
	value, err := program.runMain(nil)
	if err != nil {
		t.Fatalf("run interpreter: %v", err)
	}
	if output := formatRuntimeValue(value); output != "AB" {
		t.Fatalf("interpreter output %q, want AB", output)
	}
	if output := runGeneratedAnnotationProgram(t, program); output != "AB\n" {
		t.Fatalf("native output %q, want AB\\n", output)
	}
}

func TestAnnotationHookRunsBeforeGenericInstantiation(t *testing.T) {
	applied := 0
	terminals := append(testAnnotationTerminals(), terminalAnnotationDecl{
		canonical: "std.test.MakeBox",
		targets:   []annotationTarget{annotationTargetMethod},
		apply: func(_ *program, target annotationTargetRef, _ resolvedAnnotation) {
			applied++
			result := typeRef{name: "Box<int>", pos: target.pos}
			target.method.result = result
			target.function.result = result
		},
	})
	program, diagnostics := compileWithTerminals([]Source{{Name: "main.slk", Namespace: "root", Text: `
class Box<T> {
    Value: T
}

class Factory<T> {
    @std.test.MakeBox
    function Make() -> null {
        Box<int> { Value: 42 }
    }
}

function main() -> int {
    let Factory = Factory<string> {}
    let Box = Factory.Make()
    Box.Value
}
`}}, terminals)
	if len(diagnostics) > 0 {
		t.Fatalf("compile annotations: %+v", diagnostics)
	}
	if applied != 1 {
		t.Fatalf("terminal applied %d times, want 1", applied)
	}
	value, err := program.runMain(nil)
	if err != nil || formatRuntimeValue(value) != "42" {
		t.Fatalf("interpreter value=%v err=%v", value, err)
	}
	if output := runGeneratedAnnotationProgram(t, program); output != "42\n" {
		t.Fatalf("native output %q, want 42\\n", output)
	}
}

func TestDetachedMethodAnnotationsShareOneTarget(t *testing.T) {
	applied := 0
	terminals := append(testAnnotationTerminals(), terminalAnnotationDecl{
		canonical: "std.test.Once",
		targets:   []annotationTarget{annotationTargetMethod},
		apply: func(_ *program, _ annotationTargetRef, _ resolvedAnnotation) {
			applied++
		},
	})
	_, diagnostics := compileWithTerminals([]Source{{Name: "main.slk", Namespace: "root", Text: `
class Service {
    @std.test.Once
    function Run() -> int
}

@std.test.Once
function Service.Run() -> int {
    1
}

function main() -> int {
    let App = Service {}
    App.Run()
}
`}}, terminals)
	requireAnnotationDiagnostic(t, diagnostics, "SLK416", "cannot repeat")
	if applied != 1 {
		t.Fatalf("terminal applied %d times, want 1", applied)
	}
}

func TestAnnotationApplicationsResolveAcrossTargetsAndAliases(t *testing.T) {
	program := compileAnnotationProgram(t, Source{Name: "main.slk", Namespace: "root", Text: `
use std.test.Marker

annotation Label(Name: string) =
    @std.test.Tag(Name)

annotation Nested(Name: string) =
    @Label(Name)

/// Service documentation survives its annotation prefix.
@Marker
@Nested("one")
@std.test.Tag("two")
class Service {
    /// Run documentation survives its annotation prefix.
    @Marker
    function Run(@Marker Value: int) -> int {
        Value
    }
}

function main() -> int {
    let App = Service {}
    App.Run(42)
}
`})
	class, ok := program.describeSymbol("root.Service")
	if !ok {
		t.Fatal("describe root.Service")
	}
	if class.Documentation == nil || !strings.Contains(*class.Documentation, "survives") {
		t.Fatalf("class documentation: %+v", class.Documentation)
	}
	if len(class.Annotations) != 3 ||
		class.Annotations[0].ResolvedName != "std.test.Marker" ||
		class.Annotations[1].ResolvedName != "std.test.Tag" ||
		class.Annotations[1].ResolvedArguments[0] != `"one"` ||
		class.Annotations[2].ResolvedArguments[0] != `"two"` {
		t.Fatalf("class annotations: %+v", class.Annotations)
	}
	if len(class.DeclaredMethods) != 1 || len(class.DeclaredMethods[0].Annotations) != 1 {
		t.Fatalf("method annotations: %+v", class.DeclaredMethods)
	}
	params := class.DeclaredMethods[0].Parameters
	if len(params) != 1 || len(params[0].Annotations) != 1 || params[0].Annotations[0].ResolvedName != "std.test.Marker" {
		t.Fatalf("parameter annotations: %+v", params)
	}
	alias, ok := program.describeSymbol("root.Label")
	if !ok || alias.Kind != "annotation" || len(alias.Parameters) != 1 || alias.Annotations[0].ResolvedName != "std.test.Tag" || alias.Annotations[0].ResolvedArguments[0] != "Name" {
		t.Fatalf("annotation alias description: %+v, found=%t", alias, ok)
	}
	nested, ok := program.describeSymbol("root.Nested")
	if !ok || nested.Annotations[0].ResolvedName != "std.test.Tag" || nested.Annotations[0].ResolvedArguments[0] != "Name" {
		t.Fatalf("nested annotation alias description: %+v, found=%t", nested, ok)
	}
}

func TestAnnotationAliasesFollowVisibilityAndExactImports(t *testing.T) {
	public := Source{Name: "meta.slk", Namespace: "root.meta", Text: `
annotation Public =
    @std.test.Marker

annotation hidden =
    @std.test.Marker
`}
	t.Run("public exact import", func(t *testing.T) {
		compileAnnotationProgram(t, public, Source{Name: "main.slk", Namespace: "root", Text: `
use root.meta.Public

@Public
class Service {}

function main() -> null {
    null
}
`})
	})
	t.Run("private alias", func(t *testing.T) {
		diagnostics := checkAnnotationProgram(public, Source{Name: "main.slk", Namespace: "root", Text: `
use root.meta.hidden

@hidden
class Service {}

function main() -> null {
    null
}
`})
		requireAnnotationDiagnostic(t, diagnostics, "SLK330", "private")
	})
}

func TestAnnotationAliasCyclesAndUnresolvedTargetsAreRejectedUnused(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		diagnostics := checkAnnotationProgram(Source{Name: "main.slk", Namespace: "root", Text: `
annotation First =
    @Second

annotation Second =
    @First

function main() -> null {
    null
}
`})
		requireAnnotationDiagnostic(t, diagnostics, "SLK418", "root.First -> root.Second -> root.First")
	})
	t.Run("unresolved terminal", func(t *testing.T) {
		diagnostics := checkAnnotationProgram(Source{Name: "main.slk", Namespace: "root", Text: `
annotation Missing =
    @std.test.DoesNotExist

function main() -> null {
    null
}
`})
		requireAnnotationDiagnostic(t, diagnostics, "SLK415", "does not resolve to a compiler-owned terminal")
	})
}

func TestAnnotationCompileTimeValues(t *testing.T) {
	compileAnnotationProgram(t, Source{Name: "main.slk", Namespace: "root", Text: `
union Method {
    Get
    Post
    Payload(Value: int)
}

const Label: string = "api"

@std.test.Choice(Method.Get)
@std.test.Tag(Label)
class Service {}

function main() -> null {
    null
}
`})
}

func TestAnnotationCompileTimeValuesFollowVisibility(t *testing.T) {
	meta := Source{Name: "meta.slk", Namespace: "root.meta", Text: `
const hidden: string = "private"

union Method {
    hidden
}
`}
	t.Run("constant", func(t *testing.T) {
		diagnostics := checkAnnotationProgram(meta, Source{Name: "main.slk", Namespace: "root", Text: `
@std.test.Tag(root.meta.hidden)
class Service {}

function main() -> null { null }
`})
		requireAnnotationDiagnostic(t, diagnostics, "SLK330", "constant hidden is private")
	})
	t.Run("variant", func(t *testing.T) {
		terminals := append(testAnnotationTerminals(), terminalAnnotationDecl{
			canonical: "std.test.MetaChoice",
			params:    []string{"root.meta.Method"},
			targets:   []annotationTarget{annotationTargetClass},
		})
		_, diagnostics := compileWithTerminals([]Source{meta, {
			Name: "main.slk", Namespace: "root", Text: `
@std.test.MetaChoice(root.meta.Method.hidden)
class Service {}

function main() -> null { null }
`,
		}}, terminals)
		requireAnnotationDiagnostic(t, diagnostics, "SLK330", "variant hidden is private")
	})
}

func TestAnnotationDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name: "zero argument parentheses",
			source: `
@std.test.Marker()
class Service {}
function main() -> null { null }
`,
			code: "SLK001", message: "without parentheses",
		},
		{
			name: "wrong target",
			source: `
@std.test.Tag("x")
function main() -> null { null }
`,
			code: "SLK416", message: "cannot target a function",
		},
		{
			name: "interface target",
			source: `
@std.test.Marker
interface Service {}
function main() -> null { null }
`,
			code: "SLK416", message: "cannot target an interface",
		},
		{
			name: "wrong arity",
			source: `
@std.test.Tag
class Service {}
function main() -> null { null }
`,
			code: "SLK417", message: "expects 1 arguments, found 0",
		},
		{
			name: "wrong type",
			source: `
@std.test.Tag(1)
class Service {}
function main() -> null { null }
`,
			code: "SLK417", message: "must be string, found int",
		},
		{
			name: "illegal repeat",
			source: `
@std.test.Marker
@std.test.Marker
class Service {}
function main() -> null { null }
`,
			code: "SLK416", message: "cannot repeat",
		},
		{
			name: "inline lambda",
			source: `
class Service {
    @std.test.Before((Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        null
    })
    function Run(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        null
    }
}
function main() -> null { null }
`,
			code: "SLK417", message: "inline lambdas are not allowed",
		},
		{
			name: "generic function",
			source: `
function Prefix<T>(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
    null
}
class Service {
    @std.test.Before(Prefix)
    function Run(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        null
    }
}
function main() -> null { null }
`,
			code: "SLK417", message: "generic function Prefix",
		},
		{
			name: "method value",
			source: `
class Service {
    function Prefix(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        null
    }
    @std.test.Before(Service.Prefix)
    function Run(Writer: std.io.BytesWriter) -> null throws std.io.Failure {
        null
    }
}
function main() -> null { null }
`,
			code: "SLK417", message: "method Service.Prefix is not an annotation value",
		},
		{
			name: "payload variant",
			source: `
union Method {
    Get
    Payload(Value: int)
}
@std.test.Choice(Method.Payload)
class Service {}
function main() -> null { null }
`,
			code: "SLK417", message: "payload variant",
		},
		{
			name: "wrong union",
			source: `
union Method { Get }
union Other { Get }
@std.test.Choice(Other.Get)
class Service {}
function main() -> null { null }
`,
			code: "SLK417", message: "must be Method, found Other",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkAnnotationProgram(Source{Name: "main.slk", Namespace: "root", Text: test.source})
			requireAnnotationDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestGenericAnnotationDiagnosticsAreDeduplicated(t *testing.T) {
	diagnostics := checkAnnotationProgram(Source{Name: "main.slk", Namespace: "root", Text: `
@std.test.Marker
@std.test.Marker
class Box<T> {
    Value: T
}

function main() -> null {
    let Number = Box<int> { Value: 1 }
    let Text = Box<string> { Value: "value" }
    null
}
`})
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "SLK416" && strings.Contains(diagnostic.Message, "cannot repeat") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("repeat diagnostics = %d, want 1: %+v", count, diagnostics)
	}
}

func TestAnnotationFormattingAndHighlighting(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `annotation Label ( Name : string ) = @std.test.Tag( Name )
/// Service docs.
@std.test.Marker @Label( "one" ) class Service {
/// Run docs.
@std.test.Marker function Run( @std.test.Marker Value : int ) -> int { Value }
}
function main ( ) -> int { let App = Service { } App.Run( 1 ) }
@std.test.Marker class Later {}`}
	formatted, diagnostics, err := Format(source)
	if err != nil {
		parsed, _ := parseFormatSource(source)
		t.Fatalf("format annotations: %v\nfirst pass:\n%s", err, newSourceFormatter(parsed).format())
	}
	if len(diagnostics) > 0 {
		t.Fatalf("format diagnostics: %+v", diagnostics)
	}
	want := `annotation Label(Name: string) =
    @std.test.Tag(Name)

/// Service docs.
@std.test.Marker
@Label("one")
class Service {
    /// Run docs.
    @std.test.Marker
    function Run(@std.test.Marker Value: int) -> int {
        Value
    }
}

function main() -> int {
    let App = Service {}
    App.Run(1)
}

@std.test.Marker
class Later {}
`
	if formatted != want {
		t.Fatalf("formatted annotations:\n%s\nwant:\n%s", formatted, want)
	}
	var keyword, punct bool
	for _, token := range Highlight(formatted) {
		if token.Text == "annotation" && token.Class == ClassKeyword {
			keyword = true
		}
		if token.Text == "@" && token.Class == ClassPunct {
			punct = true
		}
	}
	if !keyword || !punct {
		t.Fatalf("annotation highlighting keyword=%t punct=%t", keyword, punct)
	}
}
