package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// resultLookup declares the shared error and payload classes plus a Result
// producer used by the Result contract tests.
const resultLookup = `
class LookupError implements Error {
    Message: string
}

class User {
    Name: string
}

function find_user(Found: bool) -> Result<User, LookupError> {
    if (Found) {
        Ok(User { Name: "Ada" })
    } else {
        Err(LookupError { Message: "missing user" })
    }
}
`

func runResult(t *testing.T, source string) string {
	t.Helper()
	output, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	return output
}

func checkResult(t *testing.T, source string) []compiler.Diagnostic {
	t.Helper()
	return compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
}

// buildAndRunResult compiles source to a standalone native binary and returns
// its stdout with the trailing newline removed, so it is directly comparable
// with the interpreter's return value.
func buildAndRunResult(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(path, binary)
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

// runResultEverywhere asserts the interpreter and the native binary agree, and
// returns the single observable output.
func runResultEverywhere(t *testing.T, source string) string {
	t.Helper()
	interpreted := runResult(t, source)
	native := buildAndRunResult(t, source)
	if interpreted != native {
		t.Fatalf("interpreter produced %q, native binary produced %q", interpreted, native)
	}
	return interpreted
}

func TestResultMatchesOkAndErrConstruction(t *testing.T) {
	tests := map[string]struct {
		found  bool
		output string
	}{
		"ok":  {found: true, output: "Ada"},
		"err": {found: false, output: "missing user"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			found := "false"
			if test.found {
				found = "true"
			}
			output := runResult(t, resultLookup+`
function main() -> string {
    match find_user(`+found+`) {
        Ok(User) => User.Name
        Err(Error) => Error.Message
    }
}
`)
			if output != test.output {
				t.Fatalf("expected %q, found %q", test.output, output)
			}
		})
	}
}

func TestResultPropagationExtractsOkValue(t *testing.T) {
	output := runResult(t, resultLookup+`
function user_name(Found: bool) -> Result<string, LookupError> {
    let User = find_user(Found)?
    Ok(User.Name)
}

function main() -> string {
    match user_name(true) {
        Ok(Name) => Name
        Err(Error) => Error.Message
    }
}
`)
	if output != "Ada" {
		t.Fatalf("expected propagated success value, found %q", output)
	}
}

func TestResultPropagationReturnsErrThroughTwoFunctions(t *testing.T) {
	output := runResultEverywhere(t, resultLookup+`
function user_name(Found: bool) -> Result<string, LookupError> {
    let User = find_user(Found)?
    Ok(User.Name)
}

function greeting(Found: bool) -> Result<string, LookupError> {
    let Name = user_name(Found)?
    Ok("hello " + Name)
}

function main() -> string {
    match greeting(false) {
        Ok(Text) => Text
        Err(Error) => Error.Message
    }
}
`)
	if output != "missing user" {
		t.Fatalf("expected Err propagated through two functions, found %q", output)
	}
}

func TestResultMatchBindingsCarryTheirTypes(t *testing.T) {
	t.Run("bindings are usable", func(t *testing.T) {
		output := runResult(t, `
class Failure implements Error {
    Message: string
}

function tally(Ready: bool) -> Result<int, Failure> {
    if (Ready) { Ok(41) } else { Err(Failure { Message: "not ready" }) }
}

function main() -> string {
    let Value = match tally(true) {
        Ok(Count) => Count + 1
        Err(Error) => 0
    }
    let Reason = match tally(false) {
        Ok(Count) => ""
        Err(Error) => Error.Message
    }
    `+"`${Value}:${Reason}`"+`
}
`)
		if output != "42:not ready" {
			t.Fatalf("expected typed bindings, found %q", output)
		}
	})
	t.Run("ok binding is not the error type", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {
    Message: string
}
function tally() -> Result<int, Failure> { Ok(1) }
function main() -> string {
    match tally() {
        Ok(Count) => Count.Message
        Err(Error) => Error.Message
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK341", "unknown value Count.Message")
	})
}

// countingScrutinee increments Count while producing the Result it matches, so a
// second evaluation of the scrutinee would be observable in the reported count.
const countingScrutinee = `
class Failure implements Error {
    Message: string
}

function tally(Count: int) -> Result<int, Failure> {
    Ok(Count)
}
`

func TestResultMatchEvaluatesScrutineeOnce(t *testing.T) {
	output := runResultEverywhere(t, countingScrutinee+`
function main() -> int {
    let Count = 0
    let Value = match (if (true) { Count = Count + 1 tally(Count) } else { tally(0) }) {
        Ok(Total) => Total
        Err(Error) => 0
    }
    Count
}
`)
	if output != "1" {
		t.Fatalf("expected the scrutinee to run once, found %q", output)
	}
}

func TestResultPropagationEvaluatesOperandOnce(t *testing.T) {
	output := runResultEverywhere(t, countingScrutinee+`
function collect() -> Result<int, Failure> {
    let Count = 0
    let Value = (if (true) { Count = Count + 1 tally(Count) } else { tally(0) })?
    Ok(Count)
}

function main() -> int {
    match collect() {
        Ok(Count) => Count
        Err(Error) => 0
    }
}
`)
	if output != "1" {
		t.Fatalf("expected the ? operand to run once, found %q", output)
	}
}

func TestResultStaysSeparateFromCheckedThrows(t *testing.T) {
	t.Run("Result needs no throws clause", func(t *testing.T) {
		assertNoDiagnostics(t, checkResult(t, resultLookup+`
function main() -> string {
    match find_user(false) {
        Ok(User) => User.Name
        Err(Error) => Error.Message
    }
}
`))
	})
	t.Run("throws still demands handling", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function fail() -> string throws Failure { throw Failure {} }
function main() -> string { fail() }
`)
		assertDiagnostic(t, diagnostics, "SLK201", "unhandled Failure")
	})

	t.Run("catch does not intercept an Err", func(t *testing.T) {
		output := runResultEverywhere(t, `
class Failure implements Error {
    Message: string
}

function load() -> Result<string, Failure> {
    Err(Failure { Message: "boom" })
}

function recover() -> Result<string, Failure> {
    load() catch (error) {
        Failure => Ok("caught")
    }
}

function main() -> string {
    match recover() {
        Ok(Text) => Text
        Err(Problem) => Problem.Message
    }
}
`)
		if output != "boom" {
			t.Fatalf("catch intercepted an Err value: %q", output)
		}
	})
}

func TestResultPropagatesInsideMethods(t *testing.T) {
	output := runResultEverywhere(t, `
class Failure implements Error {
    Message: string
}

class Chain {
    Tag: string
    function Head(Ready: bool) -> Result<string, Failure>
    function Resolve(Ready: bool) -> Result<string, Failure>
}

function Chain.Head(Ready: bool) -> Result<string, Failure> {
    if (Ready) { Ok(self.Tag) } else { Err(Failure { Message: "no head" }) }
}

function Chain.Resolve(Ready: bool) -> Result<string, Failure> {
    let Text = self.Head(Ready)?
    Ok("chain " + Text)
}

function main() -> string {
    let Instance = Chain { Tag: "root" }
    let Good = match Instance.Resolve(true) { Ok(Text) => Text Err(Problem) => Problem.Message }
    let Bad = match Instance.Resolve(false) { Ok(Text) => Text Err(Problem) => Problem.Message }
    Good + ";" + Bad
}
`)
	if output != "chain root;no head" {
		t.Fatalf("unexpected method propagation output %q", output)
	}
}

func TestResultTypesNestAndCarryArrays(t *testing.T) {
	t.Run("nested Result", func(t *testing.T) {
		output := runResultEverywhere(t, `
class Inner implements Error {
    Message: string
}
class Outer implements Error {
    Message: string
}

function nested(Deep: bool) -> Result<Result<int, Inner>, Outer> {
    if (Deep) {
        Ok(Err(Inner { Message: "inner" }))
    } else {
        Err(Outer { Message: "outer" })
    }
}

function main() -> string {
    match nested(true) {
        Ok(Value) => match Value {
            Ok(Number) => "number"
            Err(Error) => Error.Message
        }
        Err(Error) => Error.Message
    }
}
`)
		if output != "inner" {
			t.Fatalf("expected nested Result payload, found %q", output)
		}
	})
	t.Run("array payload", func(t *testing.T) {
		output := runResultEverywhere(t, `
class Failure implements Error {
    Message: string
}

function join(Values: string[]) -> string {
    let Output = ""
    for Value in Values {
        Output = Output + Value
    }
    Output
}

function names(Found: bool) -> Result<string[], Failure> {
    if (Found) { Ok(["Ada", "Grace"]) } else { Err(Failure { Message: "none" }) }
}

function main() -> string {
    match names(true) {
        Ok(Values) => join(Values)
        Err(Error) => Error.Message
    }
}
`)
		if output != "AdaGrace" {
			t.Fatalf("expected array payload, found %q", output)
		}
	})
	t.Run("class array payload type-checks", func(t *testing.T) {
		assertNoDiagnostics(t, checkResult(t, resultLookup+`
function all_users() -> Result<User[], LookupError> {
    Ok([User { Name: "Ada" }])
}

function main() -> string {
    match all_users() {
        Ok(Users) => "ok"
        Err(Error) => Error.Message
    }
}
`))
	})
}

// TestResultFlowsThroughEveryValuePosition exercises Result as an ordinary
// value: explicit return, a class field, a parameter, a method result, ? inside
// a loop, and a failure type that is not an Error class.
func TestResultFlowsThroughEveryValuePosition(t *testing.T) {
	output := runResultEverywhere(t, `
class LoadError implements Error {
    Message: string
}

class Box {
    Slot: Result<int, LoadError>
}

class Loader {
    Tag: string
    function Load(Ready: bool) -> Result<string, LoadError>
}

function Loader.Load(Ready: bool) -> Result<string, LoadError> {
    if (Ready) {
        return Ok(self.Tag)
    }
    return Err(LoadError { Message: "not ready" })
}

function step(Value: int) -> Result<int, LoadError> {
    if (Value == 3) { Err(LoadError { Message: "hit three" }) } else { Ok(Value) }
}

function total(Values: int[]) -> Result<int, LoadError> {
    let Sum = 0
    for Value in Values {
        let Step = step(Value)?
        Sum = Sum + Step
    }
    Ok(Sum)
}

function unwrap_box(Container: Box) -> string {
    match Container.Slot {
        Ok(Number) => `+"`${Number}`"+`
        Err(Problem) => Problem.Message
    }
}

function describe(Value: Result<int, string>) -> string {
    match Value {
        _ => "any"
    }
}

function main() -> string {
    let Instance = Loader { Tag: "loader" }
    let Ready = match Instance.Load(true) { Ok(Text) => Text Err(Problem) => Problem.Message }
    let Stalled = match Instance.Load(false) { Ok(Text) => Text Err(Problem) => Problem.Message }
    let Summed = match total([1, 2]) { Ok(Sum) => `+"`${Sum}`"+` Err(Problem) => Problem.Message }
    let Stopped = match total([1, 2, 3, 4]) { Ok(Sum) => `+"`${Sum}`"+` Err(Problem) => Problem.Message }
    let Boxed = unwrap_box(Box { Slot: Ok(7) })
    let Plain = describe(Err("plain string"))
    `+"`${Ready};${Stalled};${Summed};${Stopped};${Boxed};${Plain}`"+`
}
`)
	if output != "loader;not ready;3;hit three;7;any" {
		t.Fatalf("unexpected Result value-position output %q", output)
	}
}

// TestResultIsFormattedWhenMainReturnsIt pins the tagged rendering of a Result
// returned from main. runResultEverywhere is the point of the test: the
// interpreter and the generated binary must render the payload identically,
// including class and error payloads.
func TestResultIsFormattedWhenMainReturnsIt(t *testing.T) {
	const declarations = `
class LoadError implements Error {
    Message: string
}

class User {
    Name: string
}
`
	tests := map[string]struct {
		declared string
		body     string
		output   string
	}{
		"string payload":   {declared: "Result<string, LoadError>", body: `Ok("shipped")`, output: "Ok(shipped)"},
		"class payload":    {declared: "Result<User, LoadError>", body: `Ok(User { Name: "Ada" })`, output: "Ok(root.User)"},
		"error payload":    {declared: "Result<string, LoadError>", body: `Err(LoadError { Message: "boom" })`, output: "Err(root.LoadError)"},
		"array payload":    {declared: "Result<string[], LoadError>", body: `Ok(["Ada", "Grace"])`, output: "Ok([Ada, Grace])"},
		"iterable payload": {declared: "Result<Iterable<int>, LoadError>", body: "Ok(1..3)", output: "Ok([1, 2])"},
		"tuple sequence":   {declared: "Result<Iterable<(int,string)>, LoadError>", body: `Ok(enumerate(["Ada"]))`, output: "Ok([(0, Ada)])"},
		"nested Result":    {declared: "Result<Result<int, LoadError>, LoadError>", body: "Ok(Ok(7))", output: "Ok(Ok(7))"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			output := runResultEverywhere(t, declarations+`
function main() -> `+test.declared+` {
    `+test.body+`
}
`)
			if output != test.output {
				t.Fatalf("expected %q, found %q", test.output, output)
			}
		})
	}
}

func TestResultCatchAllArmSatisfiesExhaustiveness(t *testing.T) {
	output := runResultEverywhere(t, resultLookup+`
function main() -> string {
    match find_user(false) {
        Ok(User) => User.Name
        _ => "fallback"
    }
}
`)
	if output != "fallback" {
		t.Fatalf("expected catch-all arm, found %q", output)
	}
}

func TestResultBlankBindingsAreAccepted(t *testing.T) {
	output := runResultEverywhere(t, resultLookup+`
function main() -> string {
    match find_user(true) {
        Ok(_) => "found"
        Err(_) => "missing"
    }
}
`)
	if output != "found" {
		t.Fatalf("expected blank Result bindings, found %q", output)
	}
}

const resultBinaryProgram = resultLookup + `
function user_name(Found: bool) -> Result<string, LookupError> {
    let User = find_user(Found)?
    Ok(User.Name)
}

function main() -> string {
    match user_name(true) {
        Ok(Name) => "found " + Name
        Err(Error) => Error.Message
    }
}
`

func TestResultNativeBinaryReturnsSuccessOutput(t *testing.T) {
	if output := runResultEverywhere(t, resultBinaryProgram); output != "found Ada" {
		t.Fatalf("expected success output, found %q", output)
	}
}

func TestResultNativeBinaryPropagatesErr(t *testing.T) {
	program := resultLookup + `
function user_name(Found: bool) -> Result<string, LookupError> {
    let User = find_user(Found)?
    Ok(User.Name)
}

function main() -> string {
    match user_name(false) {
        Ok(Name) => Name
        Err(Error) => Error.Message
    }
}
`
	if output := buildAndRunResult(t, program); output != "missing user" {
		t.Fatalf("expected propagated Err output, found %q", output)
	}
}

func TestResultConstructorPayloadsAreChecked(t *testing.T) {
	t.Run("Ok payload", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> Result<int, Failure> { Ok("text") }
function main() -> null { null }
`)
		assertDiagnostic(t, diagnostics, "SLK350", "Ok payload must be int, found string")
	})
	t.Run("Err payload", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> Result<int, Failure> { Err(42) }
function main() -> null { null }
`)
		assertDiagnostic(t, diagnostics, "SLK350", "Err payload must be Failure, found int")
	})
	t.Run("wrong argument count", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> Result<int, Failure> { Ok(1, 2) }
function main() -> null { null }
`)
		assertDiagnostic(t, diagnostics, "SLK359", "Ok expects exactly 1 argument, found 2")
	})
}

func TestResultConstructorRequiresExpectedType(t *testing.T) {
	for _, constructor := range []string{"Ok(1)", "Err(1)"} {
		t.Run(constructor, func(t *testing.T) {
			diagnostics := checkResult(t, `
function main() -> null {
    let Value = `+constructor+`
    null
}
`)
			assertDiagnostic(t, diagnostics, "SLK351", "needs a known Result type here")
		})
	}
}

func TestResultPropagationIsRejectedOnInvalidUse(t *testing.T) {
	t.Run("non-Result operand", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> Result<int, Failure> {
    let Value = 42?
    Ok(Value)
}
function main() -> null { null }
`)
		assertDiagnostic(t, diagnostics, "SLK352", "? requires a Result value, found int")
	})
	t.Run("non-Result function", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> Result<int, Failure> { Ok(1) }
function main() -> int {
    let Value = load()?
    Value
}
`)
		assertDiagnostic(t, diagnostics, "SLK353", "? requires root.main to return Result, found int")
	})
	t.Run("incompatible error type", func(t *testing.T) {
		diagnostics := checkResult(t, `
class Alpha implements Error {}
class Beta implements Error {}
function load() -> Result<int, Alpha> { Ok(1) }
function relay() -> Result<int, Beta> {
    let Value = load()?
    Ok(Value)
}
function main() -> null { null }
`)
		assertDiagnostic(t, diagnostics, "SLK354", "? cannot propagate Alpha from root.relay, which fails with Beta")
	})
}

func TestResultMatchIsRejectedOnInvalidUse(t *testing.T) {
	const failure = `
class Failure implements Error {
    Message: string
}
function load() -> Result<int, Failure> { Ok(1) }
`
	t.Run("non-Result scrutinee", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match 42 {
        Ok(Value) => Value
        Err(Error) => 0
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK355", "match requires a Result value, found int")
	})
	t.Run("missing Ok", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> string {
    match load() {
        Err(Error) => Error.Message
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK356", "match does not handle Ok")
	})
	t.Run("missing Err", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Ok(Value) => Value
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK356", "match does not handle Err")
	})
	t.Run("duplicate Ok", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Ok(Value) => Value
        Ok(Other) => Other
        Err(Error) => 0
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK357", "duplicate Ok arm")
	})
	t.Run("duplicate Err", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Ok(Value) => Value
        Err(Error) => 0
        Err(Other) => 1
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK357", "duplicate Err arm")
	})
	t.Run("unreachable arm after catch-all", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        _ => 0
        Ok(Value) => Value
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK357", "unreachable Ok arm")
	})
	t.Run("unreachable catch-all", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Ok(Value) => Value
        Err(Error) => 0
        _ => 1
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK357", "unreachable _ arm")
	})
	t.Run("incompatible arm types", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Ok(Value) => Value
        Err(Error) => Error.Message
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK358", "match arms must produce one type; found int and string")
	})
	t.Run("unsupported pattern", func(t *testing.T) {
		diagnostics := checkResult(t, failure+`
function main() -> int {
    match load() {
        Some(Value) => Value
        Err(Error) => 0
    }
}
`)
		assertDiagnostic(t, diagnostics, "SLK360", "match supports only Ok(...), Err(...), and _ patterns")
	})
}

func TestResultFailureIsNotAThrownError(t *testing.T) {
	diagnostics := checkResult(t, `
class Failure implements Error {
    Message: string
}
function load() -> Result<int, Failure> { Err(Failure { Message: "boom" }) }
function main() -> null throws Failure {
    let Value = load()
    throw Value
}
`)
	assertDiagnostic(t, diagnostics, "SLK200", "Value does not produce an Error value")
}

func TestResultTypeArgumentsAreInvariant(t *testing.T) {
	diagnostics := checkResult(t, `
class Alpha implements Error {}
class Beta implements Error {}
function load() -> Result<int, Alpha> { Ok(1) }
function accept(Value: Result<int, Beta>) -> int { 0 }
function main() -> int { accept(load()) }
`)
	assertDiagnostic(t, diagnostics, "SLK320", "argument 1 to accept must be Result<int, Beta>, found Result<int, Alpha>")
}

// TestGenericTypeApplicationsAreValidated pins the check-time contract for
// generic type syntax: a declared type must name a generic Slick understands
// with the right arity, and a malformed argument list must not slip past Check
// only to fail in the Go backend.
func TestGenericTypeApplicationsAreValidated(t *testing.T) {
	tests := map[string]struct {
		declared string
		message  string
	}{
		"missing argument":    {declared: "Result<int>", message: "Result takes 2 type arguments, found 1"},
		"extra argument":      {declared: "Result<int, Failure, bool>", message: "Result takes 2 type arguments, found 3"},
		"blank argument":      {declared: "Result<int,>", message: "malformed generic type Result<int,>"},
		"unknown generic":     {declared: "Maybe<int>", message: "unknown generic type Maybe"},
		"iterable arity":      {declared: "Iterable<int, string>", message: "Iterable takes 1 type argument, found 2"},
		"unbalanced argument": {declared: "Result<int,(Failure>", message: "malformed generic type Result<int,(Failure>"},
		"mismatched bracket":  {declared: "Result<int,Failure[>", message: "malformed generic type Result<int,Failure[>"},
		"nested malformed":    {declared: "Result<Result<int,>, Failure>", message: "malformed generic type Result<int,>"},
		"nested unknown":      {declared: "Result<Maybe<int>, Failure>", message: "unknown generic type Maybe"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := checkResult(t, `
class Failure implements Error {}
function load() -> `+test.declared+` { Ok(1) }
function main() -> null { null }
`)
			assertDiagnostic(t, diagnostics, "SLK361", test.message)
		})
	}
}

// TestDeclaredGenericArgumentsResolveInBothBackends guards the canonical form of
// a declared generic argument: the checker and the Go backend must agree, or a
// program that passes Check fails to build.
func TestDeclaredGenericArgumentsResolveInBothBackends(t *testing.T) {
	output := runResultEverywhere(t, `
class Dog {
    Name: string
}

function first(Values: Iterable<Dog>) -> string {
    let Output = ""
    for Value in Values {
        Output = Output + Value.Name
    }
    Output
}

function main() -> string {
    "ok"
}
`)
	if output != "ok" {
		t.Fatalf("unexpected output %q", output)
	}
}

func TestResultNamesAreReserved(t *testing.T) {
	tests := map[string]string{
		"class Result":  "class Result {}\nfunction main() -> null { null }",
		"class Ok":      "class Ok {}\nfunction main() -> null { null }",
		"interface Err": "interface Err {}\nfunction main() -> null { null }",
		"function Ok":   "function Ok(Value: int) -> int { Value }\nfunction main() -> null { null }",
		"function Err":  "function Err(Value: int) -> int { Value }\nfunction main() -> null { null }",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, source), "SLK001", "is reserved by the compiler")
		})
	}
}
