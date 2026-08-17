package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

const usingTraceSupport = `
class Resource {
    Marker: string
    function Close() -> null effects { environment } {
        Append(self.Marker)
    }
}

function Reset() -> null effects { environment } {
    match std.env.Unset("SLICK_USING_TRACE") {
        Ok(_) => null
        Err(_) => null
    }
}

function Store(Value: string) -> null effects { environment } {
    match std.env.Set("SLICK_USING_TRACE", Value) {
        Ok(_) => null
        Err(_) => null
    }
}

function Append(Value: string) -> null effects { environment } {
    let Current = std.env.Get("SLICK_USING_TRACE")
    if (Current == null) {
        Store(Value)
    } else {
        Store(Current + Value)
    }
}

function Trace() -> string effects { environment } {
    let Current = std.env.Get("SLICK_USING_TRACE")
    if (Current == null) { "" } else { Current }
}

function Acquire(Marker: string) -> Resource effects { environment } {
    Append("A")
    Resource { Marker: Marker }
}
`

func TestUsingRunsCleanupExactlyOnceOnEveryControlExit(t *testing.T) {
	tests := map[string]struct {
		program string
		want    string
	}{
		"normal block value": {
			program: `
function main() -> string effects { environment } {
    Reset()
    let Value = using Handle = Acquire("C") {
        Append("B")
        "value"
    }
    Value + ";" + Trace()
}
`,
			want: "value;ABC",
		},
		"return": {
			program: `
function Finish() -> string effects { environment } {
    using Handle = Acquire("C") {
        Append("B")
        return "returned"
    }
}
function main() -> string effects { environment } {
    Reset()
    let Value = Finish()
    Value + ";" + Trace()
}
`,
			want: "returned;ABC",
		},
		"result propagation": {
			program: `
class LookupFailure implements Error {}
function Fail() -> Result<string, LookupFailure> { Err(LookupFailure("missing")) }
function Relay() -> Result<string, LookupFailure> effects { environment } {
    using Handle = Acquire("C") {
        Append("B")
        let Value = Fail()?
        Ok(Value)
    }
}
function main() -> string effects { environment } {
    Reset()
    let Value = match Relay() {
        Ok(Text) => Text
        Err(_) => "propagated"
    }
    Value + ";" + Trace()
}
`,
			want: "propagated;ABC",
		},
		"checked throw": {
			program: `
class BodyFailure implements Error {}
function Fail() -> string throws BodyFailure effects { environment } {
    using Handle = Acquire("C") {
        Append("B")
        throw BodyFailure("body")
    }
}
function main() -> string effects { environment } {
    Reset()
    let Value = Fail() catch (Failure) {
        BodyFailure => "caught"
    }
    Value + ";" + Trace()
}
`,
			want: "caught;ABC",
		},
		"break and continue": {
			program: `
function main() -> string effects { environment } {
    Reset()
    for Index in 0..3 {
        using Handle = Acquire("C") {
            if (Index == 0) { continue }
            if (Index == 1) { break }
        }
    }
    Trace()
}
`,
			want: "ACAC",
		},
		"nested reverse order": {
			program: `
function main() -> string effects { environment } {
    Reset()
    using Outer = Acquire("O") {
        using Inner = Acquire("I") {
            Append("B")
        }
    }
    Trace()
}
`,
			want: "AABIO",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := runResultEverywhere(t, usingTraceSupport+test.program); got != test.want {
				t.Fatalf("using output = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUsingCleanupErrorsFollowCheckedEffectRules(t *testing.T) {
	const failures = `
class CloseFailure implements Error {}
class FailingResource {
    function Close() -> null throws CloseFailure {
        throw CloseFailure("close")
    }
}
function AcquireFailing() -> FailingResource { FailingResource {} }
`

	t.Run("catchable after normal completion", func(t *testing.T) {
		source := failures + `
function main() -> string {
    using Handle = AcquireFailing() { "body" } catch (Failure) {
        CloseFailure => "closed"
    }
}
`
		if got := runResultEverywhere(t, source); got != "closed" {
			t.Fatalf("cleanup catch output = %q, want closed", got)
		}
	})

	t.Run("replaces return", func(t *testing.T) {
		source := failures + `
function Finish() -> string throws CloseFailure {
    using Handle = AcquireFailing() { return "body" }
}
function main() -> string {
    Finish() catch (Failure) { CloseFailure => "cleanup" }
}
`
		if got := runResultEverywhere(t, source); got != "cleanup" {
			t.Fatalf("cleanup precedence output = %q, want cleanup", got)
		}
	})

	t.Run("replaces result propagation", func(t *testing.T) {
		source := failures + `
class LookupFailure implements Error {}
function Fail() -> Result<string, LookupFailure> { Err(LookupFailure("body")) }
function Relay() -> Result<string, LookupFailure> throws CloseFailure {
    using Handle = AcquireFailing() {
        let Value = Fail()?
        Ok(Value)
    }
}
function Recover() -> Result<string, LookupFailure> {
    Relay() catch (Failure) { CloseFailure => Err(LookupFailure("cleanup")) }
}
function main() -> string {
    match Recover() {
        Ok(_) => "ok"
        Err(_) => "cleanup"
    }
}
`
		if got := runResultEverywhere(t, source); got != "cleanup" {
			t.Fatalf("cleanup propagation output = %q, want cleanup", got)
		}
	})

	t.Run("unhandled cleanup effect", func(t *testing.T) {
		diagnostics := checkResult(t, failures+`
function main() -> string { using Handle = AcquireFailing() { "body" } }
`)
		assertDiagnostic(t, diagnostics, "SLK201", "unhandled CloseFailure from Handle.Close")
	})
}

func TestUsingPreservesPrimaryFailureAndSuppressedCleanupOrder(t *testing.T) {
	source := `
class BodyFailure implements Error {}
class InnerFailure implements Error {}
class OuterFailure implements Error {}
class Inner {
    function Close() -> null throws InnerFailure { throw InnerFailure("inner") }
}
class Outer {
    function Close() -> null throws OuterFailure { throw OuterFailure("outer") }
}
function OpenInner() -> Inner { Inner {} }
function OpenOuter() -> Outer { Outer {} }
function main() -> string throws BodyFailure | InnerFailure | OuterFailure {
    using OuterResource = OpenOuter() {
        using InnerResource = OpenInner() {
            throw BodyFailure("body")
        }
    }
}
`
	want := "root.BodyFailure: body (suppressed: root.InnerFailure: inner; root.OuterFailure: outer)"
	if got := runFailureEverywhere(t, source); got != want {
		t.Fatalf("using failure = %q, want %q", got, want)
	}
}

func TestUsingSuppressionDoesNotMutateOrSelfReferenceErrors(t *testing.T) {
	source := `
class SharedFailure implements Error {}
class Resource {
    Failure: SharedFailure
    function Close() -> null throws SharedFailure { throw self.Failure }
}
function main() -> string throws SharedFailure {
    let Failure = SharedFailure("same")
    using Handle = Resource { Failure: Failure } {
        throw Failure
    }
}
`
	want := "root.SharedFailure: same (suppressed: root.SharedFailure: same)"
	if got := runFailureEverywhere(t, source); got != want {
		t.Fatalf("shared using failure = %q, want %q", got, want)
	}
}

func TestUsingDoesNotCloseWhenAcquisitionFails(t *testing.T) {
	source := usingTraceSupport + `
class AcquireFailure implements Error {}
function BrokenAcquire() -> Result<Resource, AcquireFailure> effects { environment } {
    Append("A")
    Err(AcquireFailure("acquire"))
}
function Broken() -> Result<null, AcquireFailure> effects { environment } {
    using Handle = BrokenAcquire()? {
        Append("B")
        Ok(null)
    }
}
function main() -> string effects { environment } {
    Reset()
    match Broken() { Ok(_) => null Err(_) => null }
    Trace()
}
`
	if got := runResultEverywhere(t, source); got != "A" {
		t.Fatalf("failed acquisition trace = %q, want A", got)
	}
}

func TestUsingAcceptsInterfaceCloseProtocol(t *testing.T) {
	source := `
interface Closable { function Close() -> null }
class Resource { function Close() -> null { null } }

function Open() -> Closable { Resource {} }
function main() -> string { using Handle = Open() { "ok" } }
`
	if got := runResultEverywhere(t, source); got != "ok" {
		t.Fatalf("interface using output = %q, want ok", got)
	}
}
func TestUsingParsesObjectAndExistingValueInitializers(t *testing.T) {
	source := `
class Resource { function Close() -> null { null } }
function Open() -> Resource { Resource {} }
function main() -> string {
    let Existing = Open()
    let First = using Direct = Resource {} { "object" }
    let Second = using Bound = Existing { "existing" }
    First + ";" + Second
}
`
	if got := runResultEverywhere(t, source); got != "object;existing" {
		t.Fatalf("using initializer output = %q, want object;existing", got)
	}
}

func TestUsingDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"missing Close": {
			source:  `class Resource {} function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { null } }`,
			code:    "SLK385",
			message: "Resource has no accessible Close method",
		},
		"lowercase close is not the protocol": {
			source:  `class Resource { function close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { null } }`,
			code:    "SLK385",
			message: "Resource has no accessible Close method",
		},
		"Close parameters": {
			source:  `class Resource { function Close(Force: bool) -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { null } }`,
			code:    "SLK386",
			message: "Close must take no arguments",
		},
		"Close non-null result": {
			source:  `class Failure implements Error {} class Resource { function Close() -> Result<null, Failure> { Ok(null) } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { null } }`,
			code:    "SLK387",
			message: "Close must return null",
		},
		"Close non-Error effect": {
			source:  `class NotError {} class Resource { function Close() -> null throws NotError { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { null } }`,
			code:    "SLK200",
			message: "NotError does not name an Error type",
		},
		"direct Close": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null effects { environment } { using Handle = Open() { Handle.Close() } }`,
			code:    "SLK388",
			message: "cannot call Close directly on active using binding Handle",
		},
		"return escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource { using Handle = Open() { return Handle } }`,
			code:    "SLK389",
			message: "cannot be returned outside its scope",
		},
		"assignment escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { let Saved = Open() using Handle = Open() { Saved = Handle } }`,
			code:    "SLK389",
			message: "cannot be assigned outside its scope",
		},
		"buffer push retention escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null effects { state } { let Saved = std.buffer.New<Resource>() using Handle = Open() { std.buffer.Push<Resource>(Saved, Handle) } }`,
			code:    "SLK389",
			message: "cannot be retained outside its scope",
		},
		"buffer set retention escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null effects { state } { let Saved = std.buffer.New<Resource>() std.buffer.Push<Resource>(Saved, Open()) using Handle = Open() { let Updated = std.buffer.Set<Resource>(Saved, 0, Handle) null } }`,
			code:    "SLK389",
			message: "cannot be retained outside its scope",
		},
		"buffer wrapper retention escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Save(Target: Buffer<Resource>, Value: Resource) -> null effects { state } { std.buffer.Push<Resource>(Target, Value) } function main() -> null effects { state } { let Saved = std.buffer.New<Resource>() using Handle = Open() { Save(Saved, Handle) } }`,
			code:    "SLK389",
			message: "cannot be retained outside its scope",
		},
		"checked effect escape": {
			source:  `class Resource implements Error { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Raise(Value: Resource) -> null throws Resource { throw Value } function main() -> null throws Resource { using Handle = Open() { Raise(Handle) } }`,
			code:    "SLK389",
			message: "cannot escape its scope through Resource",
		},
		"block value escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource { using Handle = Open() { Handle } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"conditional block value escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource { using Handle = Open() { if (true) { Handle } else { Open() } } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"match block value escape": {
			source:  `class Failure implements Error {} class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Choice() -> Result<int, Failure> { Ok(1) } function main() -> Resource { using Handle = Open() { match Choice() { Ok(_) => Handle Err(_) => Open() } } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"catch block value escape": {
			source:  `class Failure implements Error {} class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Choice() -> Resource throws Failure { throw Failure {} } function main() -> Resource { using Handle = Open() { Choice() catch (Caught) { Failure => Handle } } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"object value escape": {
			source:  `class Resource { function Close() -> null { null } } class Box { Value: Resource } function Open() -> Resource { Resource {} } function main() -> Box { using Handle = Open() { Box { Value: Handle } } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"tuple value escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> (Resource,int) { using Handle = Open() { (Handle, 1) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"array value escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource[] { using Handle = Open() { [Handle] } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"map value escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Map<string,Resource> { using Handle = Open() { map { "handle": Handle } } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"result value escape": {
			source:  `class Failure implements Error {} class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Result<Resource,Failure> { using Handle = Open() { Ok(Handle) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"named call result escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Identity(Value: Resource) -> Resource { Value } function main() -> Resource { using Handle = Open() { Identity(Handle) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"callable result escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource { let Identity = (Value: Resource) -> Resource { Value } using Handle = Open() { Identity(Handle) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"receiver result escape": {
			source:  `class Resource { function Close() -> null { null } function Identity() -> Resource { self } } function Open() -> Resource { Resource {} } function main() -> Resource { using Handle = Open() { Handle.Identity() } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"union construction escape": {
			source:  `class Resource { function Close() -> null { null } } union Choice { Held(Value: Resource) Empty } function Open() -> Resource { Resource {} } function main() -> Choice { using Handle = Open() { Choice.Held(Handle) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"result propagation escape": {
			source:  `class Failure implements Error {} class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Wrap(Value: Resource) -> Result<Resource,Failure> { Ok(Value) } function main() -> Result<Resource,Failure> { using Handle = Open() { Ok(Wrap(Handle)?) } }`,
			code:    "SLK389",
			message: "using binding Handle cannot escape its scope",
		},
		"result failure propagation escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Wrap(Value: Resource) -> Result<int,Resource> { Err(Value) } function main() -> Result<int,Resource> { using Handle = Open() { let Value = Wrap(Handle)? Ok(Value) } }`,
			code:    "SLK389",
			message: "cannot escape through Result propagation",
		},
		"result payload escape": {
			source:  `class Failure implements Error {} class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function Wrap(Value: Resource) -> Result<Resource,Failure> { Ok(Value) } function main() -> Resource { using Handle = Open() { match Wrap(Handle) { Ok(Value) => Value Err(_) => Open() } } }`,
			code:    "SLK389",
			message: "cannot escape its scope",
		},
		"union payload escape": {
			source:  `class Resource { function Close() -> null { null } } union Choice { Held(Value: Resource) Empty } function Open() -> Resource { Resource {} } function Wrap(Value: Resource) -> Choice { Choice.Held(Value) } function main() -> Resource { using Handle = Open() { match Wrap(Handle) { Choice.Held(Value) => Value Choice.Empty => Open() } } }`,
			code:    "SLK389",
			message: "cannot escape its scope",
		},
		"loop binding escape": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> Resource { using Handle = Open() { for Item in [Handle] { return Item } Open() } }`,
			code:    "SLK389",
			message: "cannot be returned outside its scope",
		},
		"immutable binding": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { Handle = Open() } }`,
			code:    "SLK390",
			message: "using binding Handle is immutable",
		},
		"malformed header": {
			source:  `function main() -> null { using Handle null }`,
			code:    "SLK001",
			message: "expected '=' after using binding",
		},
		"incompatible branch result": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> int { if (true) { using Handle = Open() { 1 } } else { "bad" } }`,
			code:    "SLK342",
			message: "if branches must produce one type",
		},
		"break target": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { break } }`,
			code:    "SLK345",
			message: "break is only valid inside a loop",
		},
		"continue target": {
			source:  `class Resource { function Close() -> null { null } } function Open() -> Resource { Resource {} } function main() -> null { using Handle = Open() { continue } }`,
			code:    "SLK345",
			message: "continue is only valid inside a loop",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, test.source), test.code, test.message)
		})
	}
}

func TestUsingShadowedAssignmentStaysInsideScope(t *testing.T) {
	diagnostics := checkResult(t, `
class Resource {
    function Close() -> null { null }
}
function Open() -> Resource { Resource {} }
function main() -> null {
    let Alias = Open()
    using Handle = Open() {
        let Alias = Open()
        Alias = Handle
        null
    }
}
`)
	assertNoDiagnostics(t, diagnostics)
}

func TestUsingTupleDestructureKeepsSafeElementClean(t *testing.T) {
	diagnostics := checkResult(t, `
class Resource {
    function Close() -> null { null }
}
function Open() -> Resource { Resource {} }
function main() -> int {
    using Handle = Open() {
        let Pair = (1, Handle)
        let (Number, _) = Pair
        Number
    }
}
`)
	assertNoDiagnostics(t, diagnostics)

	diagnostics = checkResult(t, `
class Resource {
    function Close() -> null { null }
}
function Open() -> Resource { Resource {} }
function main() -> Resource {
    using Handle = Open() {
        let Pair = (Open(), Handle)
        let (Fresh, _) = Pair
        Fresh
    }
}
`)
	assertNoDiagnostics(t, diagnostics)
}

func runFailureEverywhere(t *testing.T, source string) string {
	t.Helper()
	var interpretedError error
	t.Run("interpreter", func(t *testing.T) {
		_, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
		assertNoDiagnostics(t, diagnostics)
		if err == nil {
			t.Fatal("interpreter succeeded, want runtime failure")
		}
		interpretedError = err
	})

	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	for _, backend := range []compiler.Backend{compiler.BackendGo, compiler.BackendLLVM} {
		t.Run(string(backend), func(t *testing.T) {
			binary := filepath.Join(root, "app-"+string(backend))
			diagnostics, err := compiler.BuildPathBackend(path, binary, backend)
			if err != nil {
				t.Fatalf("build %s binary: %v", backend, err)
			}
			assertNoDiagnostics(t, diagnostics)
			output, nativeError := exec.Command(binary).CombinedOutput()
			if nativeError == nil {
				t.Fatalf("%s binary succeeded, want runtime failure", backend)
			}
			native := strings.TrimSpace(string(output))
			if interpretedError.Error() != native {
				t.Fatalf("interpreter failed with %q, %s failed with %q", interpretedError, backend, native)
			}
		})
	}
	return interpretedError.Error()
}
