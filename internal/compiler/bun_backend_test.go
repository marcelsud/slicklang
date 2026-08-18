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
	core := rustCoreForTest(t, `function Pair(Left: int, Right: int) -> (int,int) { (Left, Right) }
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
	core := rustCoreForTest(t, `function main() -> int { 42 }`)
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

func TestBunBackendRegistrationReportsPinnedTargets(t *testing.T) {
	for _, backend := range Backends() {
		if backend.Name != BackendBun {
			continue
		}
		if backend.Stability != StabilityAlpha || backend.Eligible || len(backend.Targets) != 2 {
			t.Fatalf("Bun registration = %+v", backend)
		}
		want := []BackendTargetDescription{
			{Name: bunTargetLinuxX64Modern, Stability: StabilityAlpha, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
			{Name: bunTargetLinuxX64Baseline, Stability: StabilityAlpha, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
		}
		if !reflect.DeepEqual(backend.Targets, want) {
			t.Fatalf("Bun targets = %+v, want %+v", backend.Targets, want)
		}
		return
	}
	t.Fatal("Bun backend is not registered")
}

func TestBunBackendRequiresAlphaOptInBeforeTouchingOutput(t *testing.T) {
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "backend bun is alpha") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendDiagnosesSourceBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> string { 42 }`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		t.Fatalf("invalid Slick reached Bun toolchain: %v", err)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != "SLK340" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendLocatesUnsupportedStandardOperationsBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> bool {
    std.text.Contains("slick", "ick")
}`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if len(diagnostics) != 0 || err == nil ||
		!strings.Contains(err.Error(), "Bun lowering main.slk:2:") ||
		!strings.Contains(err.Error(), "standard-library operation std.text.Contains is not supported") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("unsupported operation reached Bun toolchain: %v", err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendRejectsUnsupportedRuntimeFamilyBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> string throws std.io.Failure effects { io, state } {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("slick")) {
        using Writer = std.io.WriterToBytes() {
            match std.io.Copy(Reader, Writer, 64) {
                Ok(Count) => "copied"
                Err(Failure) => Failure.Message
            }
        }
    }
}`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "Bun lowering") ||
		!strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("unsupported Core reached Bun toolchain: %v", err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendReportsMissingIncompatibleAndUnavailableTargets(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), "need Bun "+bunToolchainVersion) ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Modern) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})

	t.Run("incompatible", func(t *testing.T) {
		tools := t.TempDir()
		writeRustTool(t, tools, "bun", "#!/bin/sh\necho '1.2.0'\n")
		t.Setenv("PATH", tools)
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), `unsupported Bun toolchain "1.2.0"`) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})

	t.Run("target", func(t *testing.T) {
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{
			Backend: BackendBun, Target: "bun-plan9-x64", AllowAlpha: true,
		})
		if err == nil || !strings.Contains(err.Error(), "backend bun does not support target") ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Baseline) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
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
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), binary, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Bun toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
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
	requireNoRustDiagnostics(t, diagnostics)
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
	requireNoRustDiagnostics(t, diagnostics)
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
	requireNoRustDiagnostics(t, diagnostics)
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
	requireNoRustDiagnostics(t, diagnostics)
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
	requireNoRustDiagnostics(t, diagnostics)
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
	core := rustCoreForTest(t, `function main() -> int { 42 }`)
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
	requireNoRustDiagnostics(t, diagnostics)
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
