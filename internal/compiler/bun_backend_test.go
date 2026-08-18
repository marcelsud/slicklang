package compiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBunGenerationIsDeterministicAndEvaluatesArgumentsLeftToRight(t *testing.T) {
	core := coreForTest(t, `function Pair(Left: int, Right: int) -> (int,int) { (Left, Right) }
function First() -> int { 1 }
function Second() -> int { 2 }
function main() -> (int,int) { Pair(First(), Second()) }`)
	first, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identical Core IR produced different Bun module source")
	}
	mainStart := strings.Index(first, "async function "+bunFunctionName("root.main"))
	if mainStart < 0 {
		t.Fatal("generated root.main function is missing")
	}
	body := first[mainStart:]
	left := strings.Index(body, bunFunctionName("root.First")+"(")
	right := strings.Index(body, bunFunctionName("root.Second")+"(")
	call := strings.Index(body, bunFunctionName("root.Pair")+"(")
	if left < 0 || right <= left || call <= right {
		t.Fatalf("call evaluation order was not materialized left-to-right:\n%s", body)
	}
}

func TestBunWorkspaceOwnsDeterministicRuntimeAndLockfile(t *testing.T) {
	core := coreForTest(t, `function main() -> int { 42 }`)
	workspace := t.TempDir()
	emission, err := emitBunWorkspace(core, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if emission.primary != filepath.Join(workspace, "main.js") {
		t.Fatalf("primary emission = %q", emission.primary)
	}
	for path, want := range map[string]string{
		filepath.Join(workspace, "package.json"):            bunPackageManifest,
		filepath.Join(workspace, "bun.lock"):                bunLockfile,
		filepath.Join(workspace, "bunfig.toml"):             bunConfig,
		filepath.Join(workspace, "tsconfig.json"):           bunTypeScriptConfig,
		filepath.Join(workspace, "main.js"):                 mustGenerateBun(t, core),
		filepath.Join(workspace, "runtime", "package.json"): bunRuntimeManifest,
		filepath.Join(workspace, "runtime", "index.js"):     bunRuntimeModule,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s is not deterministic", path)
		}
	}
}

// TestBunBackendRegistrationReportsPinnedTargets pins the stability contract:
// complete standard-library coverage makes Bun eligible for consideration, and
// it stays alpha until a maintainer commits a registry change.
func TestBunBackendRegistrationReportsPinnedTargets(t *testing.T) {
	for _, backend := range Backends() {
		if backend.Name != BackendBun {
			continue
		}
		if backend.Stability != StabilityAlpha || !backend.Eligible || len(backend.Targets) != 2 {
			t.Fatalf("Bun registration = %+v", backend)
		}
		want := []BackendTargetDescription{
			{Name: bunTargetLinuxX64Modern, Stability: StabilityAlpha, Eligible: true, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
			{Name: bunTargetLinuxX64Baseline, Stability: StabilityAlpha, Eligible: true, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
		}
		if !reflect.DeepEqual(backend.Targets, want) {
			t.Fatalf("Bun targets = %+v, want %+v", backend.Targets, want)
		}
		return
	}
	t.Fatal("Bun backend is not registered")
}

func TestBunBackendRequiresAlphaOptInBeforeTouchingOutput(t *testing.T) {
	output := sentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions(validSources(), output, BuildOptions{Backend: BackendBun})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "backend bun is alpha") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	requireSentinel(t, output)
}

func TestBunBackendDiagnosesSourceBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := sentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> string { 42 }`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		t.Fatalf("invalid Slick reached Bun toolchain: %v", err)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != "SLK340" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	requireSentinel(t, output)
}

// TestBunImplementsEveryRuntimeOperation is the coverage gate for issue #108:
// every registry operation has exactly one Bun entry point, and the backend
// advertises exactly that set.
func TestBunImplementsEveryRuntimeOperation(t *testing.T) {
	seen := make(map[string]runtimeOperationID, len(bunStdOperations))
	for operation := range runtimeOperationRegistry {
		function, ok := bunStdFunction(operation)
		if !ok {
			t.Fatalf("runtime operation %s has no Bun implementation", operation)
		}
		if previous, exists := seen[function]; exists {
			t.Fatalf("Bun entry point %s implements both %s and %s", function, previous, operation)
		}
		seen[function] = operation
		if !bunRuntimeOperations.implements(operation) {
			t.Fatalf("Bun backend does not advertise runtime operation %s", operation)
		}
	}
	if len(bunRuntimeOperations) != len(runtimeOperationRegistry) {
		t.Fatalf("Bun advertises %d operations, registry declares %d",
			len(bunRuntimeOperations), len(runtimeOperationRegistry))
	}
}

// TestBunLoweringLocatesUnsupportedStandardOperations pins the pre-toolchain
// gate: an operation the backend does not implement is reported with its source
// location before any toolchain work.
func TestBunLoweringLocatesUnsupportedStandardOperations(t *testing.T) {
	core := coreForTest(t, `function main() -> bool {
    std.text.Contains("slick", "ick")
}`)
	runtime, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBunCore(core, runtime); err != nil {
		t.Fatalf("implemented operation rejected: %v", err)
	}
	err = validateLanguageCore(core, runtime, "Bun", func(operation runtimeOperationID) bool {
		return operation != nativeStdTextContains
	})
	if err == nil ||
		!strings.Contains(err.Error(), "Bun lowering main.slk:2:") ||
		!strings.Contains(err.Error(), "standard-library operation std.text.Contains is not supported") {
		t.Fatalf("error=%v", err)
	}
}

func TestBunBackendReportsMissingIncompatibleAndUnavailableTargets(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		output := sentinelOutput(t)
		_, err := BuildSourcesWithOptions(validSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), "need Bun "+bunToolchainVersion) ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Modern) {
			t.Fatalf("error = %v", err)
		}
		requireSentinel(t, output)
	})

	t.Run("incompatible", func(t *testing.T) {
		tools := t.TempDir()
		writeTool(t, tools, "bun", "#!/bin/sh\necho '1.2.0'\n")
		t.Setenv("PATH", tools)
		output := sentinelOutput(t)
		_, err := BuildSourcesWithOptions(validSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), `unsupported Bun toolchain "1.2.0"`) {
			t.Fatalf("error = %v", err)
		}
		requireSentinel(t, output)
	})

	t.Run("target", func(t *testing.T) {
		output := sentinelOutput(t)
		_, err := BuildSourcesWithOptions(validSources(), output, BuildOptions{
			Backend: BackendBun, Target: "bun-plan9-x64", AllowAlpha: true,
		})
		if err == nil || !strings.Contains(err.Error(), "backend bun does not support target") ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Baseline) {
			t.Fatalf("error = %v", err)
		}
		requireSentinel(t, output)
	})
}

func TestBunExecutableIsStandaloneAndLeavesNoWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "output with spaces")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	parentConfig := `{"compilerOptions":{"baseUrl":".","paths":{"@slick/runtime":["./hijack.js"]}}}`
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(parentConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hijack.js"), []byte(`export function slickWrite() { process.stdout.write("hijacked"); }`), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := BuildSourcesWithOptions(validSources(), binary, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Bun toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	command := exec.Command(binary)
	command.Env = []string{"PATH="}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "42\n" {
		t.Fatalf("standalone output=%q error=%v", output, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	wantEntries := []string{"app", "hijack.js", "tsconfig.json"}
	gotEntries := make([]string, len(entries))
	for index, entry := range entries {
		gotEntries[index] = entry.Name()
	}
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Fatalf("installed output directory contains generated workspace: %v", gotEntries)
	}
}

func TestBunBuildEnvironmentRemovesHostOverrides(t *testing.T) {
	got := bunBuildEnvironment([]string{
		"PATH=/bin", "HOME=/home/user", "BUN_INSTALL=/user/bun", "BUNFIG=/user/bunfig", "NODE_OPTIONS=--require=x", "SOURCE_DATE_EPOCH=host", "XDG_CONFIG_HOME=/host/config",
	}, map[string]string{"BUN_INSTALL": "/owned", "HOME": "/owned/home", "NO_COLOR": "1", "SOURCE_DATE_EPOCH": "0", "XDG_CONFIG_HOME": "/owned/config"})
	want := []string{"PATH=/bin", "BUN_INSTALL=/owned", "HOME=/owned/home", "NO_COLOR=1", "SOURCE_DATE_EPOCH=0", "XDG_CONFIG_HOME=/owned/config"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %v, want %v", got, want)
	}
}

func TestBunStringLiteralUsesUTF16Escapes(t *testing.T) {
	got := bunStringLiteral("a😀\u202e\n")
	if want := `"a\ud83d\ude00\u202e\n"`; got != want {
		t.Fatalf("literal = %q, want %q", got, want)
	}
}

const bunLanguageRuntimeProgram = `
const Label: string = "constant"
class Failure implements Error { Message: string }
interface Counter { function Read() -> int }
class Number {
    Value: int
    function Read() -> int { self.Value }
}
class Box<T> {
    Value: T
    function Get() -> T { self.Value }
}
class Resource {
    FailClose: bool
    function Close() -> null throws Failure {
        if (self.FailClose) { throw Failure { Message: "close" } } else { null }
    }
}
union Choice { Number(Value: int) Empty }
function Apply(Operation: (int) -> int, Value: int) -> int { Operation(Value) }
function Maybe(Present: bool) -> int? { if (Present) { 0 } else { null } }
function Outcome(Ready: bool) -> Result<int, Failure> {
    if (Ready) { Ok(0) } else { Err(Failure { Message: "result" }) }
}
function Propagate() -> Result<int, Failure> {
    let Value = Outcome(true)?
    Ok(Value + 1)
}
function Read(Value: Counter) -> int { Value.Read() }
function Render(Value: Choice) -> string {
    match Value {
        Choice.Number(Number) => ` + "`number ${Number}`" + `
        Choice.Empty => "empty"
    }
}
function Use(FailBody: bool, FailClose: bool) -> string throws Failure {
    using Handle = Resource { FailClose: FailClose } {
        if (FailBody) { throw Failure { Message: "body" } } else { "ok" }
    }
}
function Recover(FailBody: bool, FailClose: bool) -> string {
    Use(FailBody, FailClose) catch { Failure as Found => Found.Message }
}
function Work(Value: int) -> int { Value + 1 }
function TaskValue() -> int {
    async let Job = Work(41)
    await Job
}
function Sum() -> int {
    let Total = 0
    for Value in [1, 2, 3] { Total = Total + Value }
    Total
}
function Sequences() -> int {
    let Total = 0
    for Index, Value in enumerate([5, 6]) { Total = Total + Index + Value }
    for Left, Right in zip([1, 2], [3, 4]) { Total = Total + Left + Right }
    Total
}
function Key() -> string { "a" }
function MaybeValues() -> int?[] { [null, 1] }
function main() -> (string,int,int,Result<int,Failure>,int[],Map<string,int>,bool,string,string,int,int,int,int,int,bool,bool) {
    let Offset = 1
    let Text = Render(Choice.Number(41))
    let Count = Read(Number { Value: 42 })
    let Labelled = ` + "`${Label}/${Choice.Empty}/${Work}/${Text}`" + `
    let Present = Maybe(true)
    let PresentValue = if (Present == null) { -1 } else { Present }
    let Values = [1, 2, 3]
    let Mapping = map { "a": 1, Key(): 2, "b": 3 }
    let Held = Box<int> { Value: 9 }
    let Replaced = Mapping.With("a", 9)
    let Updated = Replaced.Without("b")
    let UpdatedValue = Updated.Get("a")
    let MapValue = if (UpdatedValue == null) { -1 } else { UpdatedValue }
    let OptionalValues = MaybeValues()
    let Result = (Labelled, Apply((Value: int) -> int { Value + Offset }, Count), PresentValue, Propagate(), Values, Mapping, Values == [1, 2, 3], Recover(true, false), Recover(false, true), TaskValue(), Sum(), Held.Get(), Sequences(), MapValue, Updated.Contains("a") && Updated.Length() == 1, Present == 0 && OptionalValues.Get(0) == null)
    Result
}
`

func TestBunLanguageRuntimeMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunLanguageRuntimeProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	if want := "(constant/Empty/<callable>/number 41, 43, 0, Ok(1), [1, 2, 3], map {a: 2, b: 3}, true, body, close, 42, 6, 9, 22, 9, true, true)"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Bun language output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

// TestBunSelectsLaterArmsAndNestsTaskScopes defends the arms no earlier pattern
// claims and a task scope launched from inside a worker child.
func TestBunSelectsLaterArmsAndNestsTaskScopes(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Failure implements Error { Message: string }
class Other implements Error { Message: string }
union Choice { First(Value: int) Second(Value: int) Empty }
function Outcome(Ready: bool) -> Result<int, Failure> {
    if (Ready) { Ok(1) } else { Err(Failure { Message: "no" }) }
}
function Pick(Ready: bool) -> string {
    match Outcome(Ready) {
        Ok(Value) => "ok"
        Err(Reason) => Reason.Message
    }
}
function Render(Value: Choice) -> string {
    match Value {
        Choice.First(Value) => "first"
        Choice.Second(Value) => ` + "`second ${Value}`" + `
        Choice.Empty => "empty"
    }
}
function Raise() -> string throws Other { throw Other { Message: "other" } }
function Caught() -> string {
    Raise() catch {
        Failure as Found => Found.Message
        Other as Reason => Reason.Message
    }
}
function Leaf() -> int { 7 }
function Inner() -> int {
    async let Job = Leaf()
    await Job
}
function Nested() -> int {
    async let Outer = Inner()
    await Outer
}
function main() -> string {
    let Failed = Pick(false)
    let Tagged = Render(Choice.Second(3))
    let Fieldless = Render(Choice.Empty)
    let Recovered = Caught()
    let Deep = Nested()
    ` + "`${Failed};${Tagged};${Fieldless};${Recovered};${Deep}`" + `
}
`}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	if want := "no;second 3;empty;other;7"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Bun output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

func TestBunErrorShorthandCarriesFailureMessage(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Bad implements Error { Message: string }
function Fail() -> string throws Bad { throw Bad("boom") }
function main() -> string throws Bad { Fail() }
`}
	_, diagnostics, interpretedErr := Run([]Source{source})
	requireNoDiagnostics(t, diagnostics)
	if interpretedErr == nil {
		t.Fatal("interpreter accepted an uncaught shorthand failure")
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil || string(output) != interpretedErr.Error()+"\n" {
		t.Fatalf("Bun shorthand output=%q error=%v, want %q", output, err, interpretedErr.Error()+"\n")
	}
}

func TestBunOptionalFieldsCatchAllAndErrorValuesMatchInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Bad implements Error { Message: string }
class User {
    Name: string
    Nickname: string?
}
function Find(Present: bool) -> User? {
    if (Present) { User { Name: "Ada" } } else { null }
}
function Fail() -> string throws Bad { throw Bad("boom") }
function Blank() -> int? { let Ignored = 1 }
function main() -> (bool,bool,string,string,bool,bool) {
    let Omitted = User { Name: "Ada" }
    let Explicit = User { Name: "Ada", Nickname: null }
    let Nickname = Omitted.Nickname
    let Found = Find(true)
    let Name = if (Found == null) { "none" } else { Found.Name }
    let Caught = Fail() catch { Error as Reason => "caught" }
    let Blanks = [Blank()]
    let Head = Blanks.Get(0)
    let SameShape = Omitted == Explicit
    let NoNickname = Nickname == null
    let SameError = Bad("x") == Bad("y")
    let BlankAbsent = Head == null
    let Result = (SameShape, NoNickname, Name, Caught, BlankAbsent, SameError)
    Result
}
`}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	if want := "(true, true, Ada, caught, true, true)"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Bun output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

func TestBunCleanupPreservesPrimaryAndSuppressedFailures(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Failure implements Error { Message: string }
class Resource {
    function Close() -> null throws Failure { throw Failure { Message: "close" } }
}
function main() -> null throws Failure {
    using Handle = Resource {} {
        throw Failure { Message: "body" }
    }
}
`}
	_, diagnostics, interpretedErr := Run([]Source{source})
	requireNoDiagnostics(t, diagnostics)
	if interpretedErr == nil {
		t.Fatal("interpreter accepted uncaught cleanup failures")
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil || string(output) != interpretedErr.Error()+"\n" {
		t.Fatalf("Bun cleanup output=%q error=%v, want %q", output, err, interpretedErr.Error()+"\n")
	}
}

func TestBunTaskFailureCancelsAndJoinsWorkerSibling(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Failure implements Error { Message: string }
function Spin() -> null {
    let Seen = 0
    for Index in 0..9223372036854775807 { Seen = Seen + 1 }
}
function Fail() -> null throws Failure { throw Failure { Message: "failed" } }
function Run() -> null throws Failure {
    async let Spinner = Spin()
    async let Broken = Fail()
    let Value = await Broken
    await Spinner
}
function main() -> null throws Failure { Run() }
`}
	binary := buildBunTestProgram(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("Bun task scope did not cancel and join its spinning worker sibling")
	}
	if err == nil || string(output) != "root.Failure: failed\n" {
		t.Fatalf("Bun task output=%q error=%v", output, err)
	}
}

// TestBunRuntimeGuardsTaskSafetyHandlesAndStructuralMaps exercises the
// compiler-owned runtime contracts a pure-language Slick program cannot reach
// yet: worker payload safety, resource-handle generations, and structural map
// identity.
func TestBunRuntimeGuardsTaskSafetyHandlesAndStructuralMaps(t *testing.T) {
	workspace := t.TempDir()
	core := coreForTest(t, `function main() -> int { 42 }`)
	if _, err := emitBunWorkspace(core, workspace); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(workspace, "harness.js")
	if err := os.WriteFile(harness, []byte(bunRuntimeHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun toolchain not found")
	}
	output, err := exec.Command(bun, "run", harness).CombinedOutput()
	if err != nil || string(output) != "ok\n" {
		t.Fatalf("runtime harness output=%q error=%v", output, err)
	}
}

const bunRuntimeHarness = `import * as slick from "./runtime/index.js";

function check(condition, label) {
  if (!condition) throw new Error("failed: " + label);
}

function rejects(work, label) {
  try {
    work();
  } catch (error) {
    check(error instanceof slick.SlickFailure, label + " is not a Slick failure");
    return error;
  }
  throw new Error("failed: " + label + " was accepted");
}

rejects(() => slick.slickEncode(new slick.SlickBuffer([])), "buffer payload");
const handle = slick.slickCreateHandle({ open: true });
rejects(() => slick.slickEncode(new slick.SlickObject("root.R", new Map(), handle)), "resource payload");
check(slick.slickResolveHandle(handle).open === true, "handle resolves in its generation");
rejects(() => slick.slickResolveHandle({ generation: 0, id: handle.id }), "foreign generation handle");
slick.slickReleaseHandle(handle);
rejects(() => slick.slickResolveHandle(handle), "released handle");

const scope = new slick.SlickTaskScope(new slick.SlickContext([]));
rejects(() => scope.launch({ kind: "function", target: "root.main", arguments: [new slick.SlickBuffer([])] }), "launch payload");
check(scope.children.length === 0, "rejected launch left an orphan worker");
check((await scope.finish()) === null, "empty scope reported a failure");

const structural = slick.slickMap([
  [new slick.SlickTuple([1n]), "first"],
  [new slick.SlickTuple([1n]), "second"],
  ["a", 1n],
]);
check(structural.entries.length === 2, "structural map keys collapsed on host identity");
check(slick.slickFormat(structural) === "map {(1): second, a: 1}", "map iteration is not insertion ordered");

check(slick.slickEqual(slick.slickAbsent, null), "absent Optional differs from null");
check(!slick.slickEqual(new slick.SlickOptional(true, 0n), slick.slickAbsent), "present zero collapsed into absence");
check(!slick.slickEqual(new slick.SlickOptional(true, false), slick.slickAbsent), "present false collapsed into absence");
check(!slick.slickEqual(new slick.SlickOptional(true, ""), slick.slickAbsent), "present empty text collapsed into absence");
check(!slick.slickEqual(new slick.SlickResult(false, 0n), new slick.SlickResult(true, 0n)), "Result failure collapsed into success");

process.stdout.write("ok\n");
`

func buildBunTestProgram(t *testing.T, source Source) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "app")
	diagnostics, err := BuildSourcesWithOptions([]Source{source}, binary, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Bun toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	return binary
}

func mustGenerateBun(t *testing.T, core coreProgram) string {
	t.Helper()
	source, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestMatrixPrimitiveBackendsMatchInterpreter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.slk")
	if err := os.WriteFile(path, []byte(primitiveProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	var interpreted string
	for _, engine := range ExecutionEngines() {
		if !engine.Interpreted {
			continue
		}
		stdout, exitCode, err := engine.Run(path, "")
		if err != nil {
			t.Fatalf("run interpreter: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("interpreter exit %d: %s", exitCode, stdout)
		}
		interpreted = strings.TrimSuffix(stdout, "\n")
	}
	if want := "(230, false, -9223372036854775808, ok😀, -0, 1e+20, 1e-07, 1e+06, 7, true, 5, 10)"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	for _, engine := range ExecutionEngines() {
		if engine.Interpreted {
			continue
		}
		t.Run(engine.Name, func(t *testing.T) {
			stdout, exitCode, err := engine.Run(path, "")
			if err != nil {
				t.Fatalf("run %s: %v", engine.Name, err)
			}
			if exitCode != 0 {
				t.Fatalf("%s exit %d: %s", engine.Name, exitCode, stdout)
			}
			if want := interpreted + "\n"; stdout != want {
				t.Fatalf("%s output = %q, want %q", engine.Name, stdout, want)
			}
		})
	}
}

const primitiveProgram = `const Maximum: int = 9223372036854775807

function Explode() -> bool {
    Explode()
}

function Score(Limit: int) -> int {
    let Total = 0
    for Outer in 0 .. Limit {
        for Inner in 0 .. 4 {
            if (Inner == 1) {
                continue
            } else {
                null
            }
            if (Outer == 3) {
                break
            } else {
                null
            }
            Total = Total + Outer * 10 + Inner
        }
    }
    Total
}

function Choose(Value: int) -> int {
    if (Value > 10) {
        return Value
    } else {
        Value + 1
    }
}

function Pick(Flag: bool) -> int {
    if (Flag) {
        return 7
    } else {
        return 9
    }
}

function WideEqual() -> bool {
    (1,2,3,4,5,6,7,8,9,10,11,12,13) == (1,2,3,4,5,6,7,8,9,10,11,12,13)
}

function Rebind(Value: int) -> int {
    let Value = 5
    Value
}

function BranchShadow(Value: int) -> int {
    let Inner = if (true) {
        let Value = 7
        Value
    } else {
        0
    }
    Value + Inner
}

function main() -> (int,bool,int,string,float,float,float,float,int,bool,int,int) {
    (Choose(Score(5)), false && Explode(), Maximum + 1, "ok😀", -0.0, 1e20, 1e-7, 1e6, Pick(true), WideEqual(), Rebind(1), BranchShadow(3))
}`

func coreForTest(t *testing.T, text string) coreProgram {
	t.Helper()
	program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func validSources() []Source {
	return []Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> int { 42 }`}}
}

func sentinelOutput(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	return output
}

func requireSentinel(t *testing.T, output string) {
	t.Helper()
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "sentinel" {
		t.Fatalf("output changed to %q", contents)
	}
}

func writeTool(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
