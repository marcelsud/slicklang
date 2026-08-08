package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

const optionalUser = `
class User {
    Name: string
    Nickname: string?
}

function find_user(Found: bool) -> User? {
    if (Found) {
        User { Name: "Ada" }
    } else {
        null
    }
}
`

// TestOptionalPrograms pins the observable behaviour of every Optional feature
// against both backends at once. Interpreter and native output must agree, so
// each case doubles as the cross-backend contract.
func TestOptionalPrograms(t *testing.T) {
	tests := map[string]struct {
		source   string
		expected string
	}{
		"present class optional": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(true)
    if (Found == null) { "none" } else { Found.Name }
}
`,
			expected: "Ada",
		},
		"absent class optional": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(false)
    if (Found == null) { "none" } else { Found.Name }
}
`,
			expected: "none",
		},
		// 0, false, and "" are ordinary present values; only the tag decides
		// absence, so none of them may read back as null.
		"optional primitives keep zero values": {
			source: `
function zero() -> int? { 0 }
function untrue() -> bool? { false }
function blank() -> string? { "" }

function main() -> string {
    let Number = zero()
    let Flag = untrue()
    let Text = blank()
    let A = if (Number == null) { "absent" } else { "present" }
    let B = if (Flag == null) { "absent" } else { "present" }
    let C = if (Text == null) { "absent" } else { "present" }
    A + B + C + ` + "`;${Number};${Flag};${Text}`" + `
}
`,
			expected: "presentpresentpresent;0;false;",
		},
		"value and null both reach an optional parameter": {
			source: optionalUser + `
function describe(Value: User?) -> string {
    if (Value == null) { "none" } else { Value.Name }
}

function main() -> string {
    describe(User { Name: "Ada" }) + ";" + describe(null)
}
`,
			expected: "Ada;none",
		},
		"value and null both return from an optional function": {
			source: `
function pick(Found: bool) -> string? {
    if (Found) { return "yes" }
    return null
}

function main() -> string {
    let First = pick(true)
    let Second = pick(false)
    let A = if (First == null) { "none" } else { First }
    let B = if (Second == null) { "none" } else { Second }
    A + ";" + B
}
`,
			expected: "yes;none",
		},
		"optional local accepts a value and null": {
			source: optionalUser + `
function main() -> string {
    let Current = find_user(true)
    Current = null
    let A = if (Current == null) { "none" } else { Current.Name }
    Current = User { Name: "Grace" }
    let B = if (Current == null) { "none" } else { Current.Name }
    A + ";" + B
}
`,
			expected: "none;Grace",
		},
		"narrowing through not null": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(true)
    if (Found != null) { Found.Name } else { "none" }
}
`,
			expected: "Ada",
		},
		"symmetric null comparisons narrow the same way": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(true)
    let A = if (null != Found) { Found.Name } else { "none" }
    let B = if (null == Found) { "none" } else { Found.Name }
    A + ";" + B
}
`,
			expected: "Ada;Ada",
		},
		"omitted optional field defaults to absent": {
			source: optionalUser + `
function main() -> string {
    let Person = User { Name: "Ada" }
    let Nickname = Person.Nickname
    if (Nickname == null) { Person.Name + " has no nickname" } else { Nickname }
}
`,
			expected: "Ada has no nickname",
		},
		"explicit null optional field": {
			source: optionalUser + `
function main() -> string {
    let Absent = User { Name: "Ada", Nickname: null }
    let Present = User { Name: "Grace", Nickname: "Amazing" }
    let A = Absent.Nickname
    let B = Present.Nickname
    let First = if (A == null) { "none" } else { A }
    let Second = if (B == null) { "none" } else { B }
    First + ";" + Second
}
`,
			expected: "none;Amazing",
		},
		"branches join into an optional": {
			source: optionalUser + `
function main() -> string {
    let Found = if (true) { User { Name: "Ada" } } else { null }
    if (Found == null) { "none" } else { Found.Name }
}
`,
			expected: "Ada",
		},
		"array of values and null infers an array of optionals": {
			source: optionalUser + `
function main() -> string {
    let People = [User { Name: "Ada" }, null]
    let Output = ""
    for Person in People {
        if (Person == null) {
            Output = Output + ";-"
        } else {
            Output = Output + ";" + Person.Name
        }
    }
    Output
}
`,
			expected: ";Ada;-",
		},
		// User[]? is an array that may be missing; User?[] is an array whose
		// elements may be missing. The two must stay distinct end to end.
		"optional array differs from array of optionals": {
			source: optionalUser + `
function whole(Present: bool) -> User[]? {
    if (Present) { [User { Name: "Ada" }] } else { null }
}

function parts() -> User?[] {
    [User { Name: "Grace" }, null]
}

function main() -> string {
    let Whole = whole(false)
    let Output = if (Whole == null) { "no array" } else { "array" }
    for Person in parts() {
        if (Person == null) {
            Output = Output + ";-"
        } else {
            Output = Output + ";" + Person.Name
        }
    }
    Output
}
`,
			expected: "no array;Grace;-",
		},
		"optional main prints a present value": {
			source: `
function main() -> string? { "hello" }
`,
			expected: "hello",
		},
		"optional main prints nothing when absent": {
			source: `
function main() -> string? { null }
`,
			expected: "",
		},
		// Absence is not an error effect: a function returning T? declares no
		// throws clause for the null it may produce, and a thrown Failure is
		// still recovered by catch. Catch arms keep their existing rule that
		// every path produces exactly the same type, so an arm recovering an
		// optional produces that optional rather than a bare null.
		"optional coexists with checked errors": {
			source: `
class Failure implements Error {}

function recovered() -> string? { "recovered" }

function lookup(Found: bool) -> string? throws Failure {
    if (Found) { throw Failure("boom") }
    null
}

function main() -> string {
    let Caught = lookup(true) catch (error) {
        Failure => recovered()
    }
    let Direct = lookup(false) catch (error) {
        Failure => recovered()
    }
    let A = if (Caught == null) { "absent" } else { Caught }
    let B = if (Direct == null) { "absent" } else { Direct }
    A + ";" + B
}
`,
			expected: "recovered;absent",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assertOptionalProgram(t, test.source, test.expected)
		})
	}
}

func TestOptionalDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"null returned from a required type": {
			source:  `function main() -> string { null }`,
			code:    "SLK371",
			message: "string is not optional",
		},
		"optional passed where a value is required": {
			source: `
function need(Value: string) -> string { Value }
function maybe() -> string? { null }
function main() -> string { need(maybe()) }
`,
			code:    "SLK372",
			message: "string? may be null",
		},
		"field read through an optional": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(false)
    Found.Name
}
`,
			code:    "SLK370",
			message: "Found is User? and may be null",
		},
		"method called through an optional": {
			source: `
class User {
    Name: string
    function Greet() -> string
}

function User.Greet() -> string { self.Name }
function find_user() -> User? { null }

function main() -> string {
    let Found = find_user()
    Found.Greet()
}
`,
			code:    "SLK370",
			message: "may be null",
		},
		"optional used as a condition": {
			source: `
function maybe() -> bool? { true }
function main() -> string {
    let Flag = maybe()
    if (Flag) { "yes" } else { "no" }
}
`,
			code:    "SLK375",
			message: "bool? may be null and is not a condition",
		},
		"incompatible value assigned to an optional": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(false)
    Found = 42
    "done"
}
`,
			code:    "SLK342",
			message: "cannot assign int to Found of type User?",
		},
		"narrowed value used after assigning null": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(true)
    if (Found != null) {
        Found = null
        Found.Name
    } else {
        "none"
    }
}
`,
			code:    "SLK370",
			message: "Found is User? and may be null",
		},
		"narrowed value used after leaving the branch": {
			source: optionalUser + `
function main() -> string {
    let Found = find_user(true)
    if (Found != null) { Found.Name } else { "none" }
    Found.Name
}
`,
			code:    "SLK370",
			message: "Found is User? and may be null",
		},
		"required field omitted": {
			source: optionalUser + `
function main() -> string {
    let Person = User { Nickname: "Amazing" }
    Person.Name
}
`,
			code:    "SLK376",
			message: "User requires field Name of type string",
		},
		"redundant optional suffix": {
			source:  `function main() -> string?? { null }`,
			code:    "SLK373",
			message: "string?? is redundant",
		},
		"unrelated optional types compared": {
			source: `
class User { Name: string }
class Dog { Name: string }
function user() -> User? { null }
function dog() -> Dog? { null }
function main() -> string {
    let Person = user()
    let Pet = dog()
    if (Person == Pet) { "same" } else { "different" }
}
`,
			code:    "SLK374",
			message: "cannot compare User? with Dog?",
		},
		"non-optional value compared with null": {
			source: `
function main() -> string {
    let Name = "Ada"
    if (Name == null) { "none" } else { Name }
}
`,
			code:    "SLK374",
			message: "cannot compare string with null",
		},
		"branches that cannot form an optional join": {
			source: `
class User { Name: string }
class Dog { Name: string }
function user() -> User? { null }
function dog() -> Dog? { null }
function main() -> string {
    let Either = if (true) { user() } else { dog() }
    "done"
}
`,
			code:    "SLK342",
			message: "if branches must produce one type",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

// TestOptionalAccessReportsOneFailure holds the unsafe-access diagnostics to a
// single cause each. A second "unknown function" or "no method" diagnostic for
// the same expression would send the reader chasing a symptom.
func TestOptionalAccessReportsOneFailure(t *testing.T) {
	source := `
class User {
    Name: string
    function Greet() -> string
}

function User.Greet() -> string { self.Name }
function find_user() -> User? { null }

function main() -> string {
    let Found = find_user()
    Found.Greet()
}
`
	diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	optionalFailures := 0
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case "SLK370":
			optionalFailures++
		case "SLK203", "SLK321":
			t.Fatalf("cascade diagnostic after unsafe optional access: %+v", diagnostic)
		}
	}
	if optionalFailures != 1 {
		t.Fatalf("expected one SLK370, found %d in %+v", optionalFailures, diagnostics)
	}
}

// TestOptionalTypeStructure pins the structural distinctions the type model has
// to preserve, through the observable behaviour of a declared signature.
func TestOptionalTypeStructure(t *testing.T) {
	source := `
class User { Name: string }
class LookupError implements Error { Message: string }

function optional_array(Values: User[]?) -> string {
    if (Values == null) { "no array" } else { "array" }
}

function array_of_optional(Values: User?[]) -> string {
    let Output = ""
    for Value in Values {
        if (Value == null) { Output = Output + "-" } else { Output = Output + "x" }
    }
    Output
}

function nested(Value: Result<User?, LookupError>) -> string {
    match Value {
        Ok(Found) => if (Found == null) { "ok none" } else { Found.Name }
        Err(Problem) => Problem.Message
    }
}

function main() -> string {
    optional_array(null) + ";" +
        array_of_optional([User { Name: "Ada" }, null]) + ";" +
        nested(Ok(null)) + ";" +
        nested(Ok(User { Name: "Grace" }))
}
`
	assertOptionalProgram(t, source, "no array;x-;ok none;Grace")
}

// assertOptionalProgram runs source through the interpreter and through a
// native binary built from the same source, requiring both to produce expected.
// Optional values are only useful if the two backends agree, so every positive
// contract checks both rather than trusting one.
func assertOptionalProgram(t *testing.T, source, expected string) {
	t.Helper()
	output, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != expected {
		t.Fatalf("interpreter produced %q, expected %q", output, expected)
	}

	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.slk")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err = compiler.BuildPath(sourcePath, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	native, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run native binary: %v: %s", err, native)
	}
	if nativeOutput := strings.TrimSuffix(string(native), "\n"); nativeOutput != expected {
		t.Fatalf("native binary produced %q, expected %q", nativeOutput, expected)
	}
}
