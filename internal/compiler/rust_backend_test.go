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

const rustPrimitiveProgram = `const Maximum: int = 9223372036854775807

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

func TestPrimitiveBackendsMatchInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustPrimitiveProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatalf("run interpreter: %v", err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	if want := "(230, false, -9223372036854775808, ok😀, -0, 1e+20, 1e-07, 1e+06, 7, true, 5, 10)"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}

	for _, backend := range []Backend{BackendGo, BackendLLVM, BackendRust, BackendBun} {
		t.Run(string(backend), func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "app")
			options := BuildOptions{Backend: backend, AllowAlpha: backend == BackendRust || backend == BackendBun}
			diagnostics, err := BuildSourcesWithOptions([]Source{source}, binary, options)
			if err != nil {
				if backend == BackendLLVM && strings.Contains(err.Error(), "LLVM") && strings.Contains(err.Error(), "not found") {
					t.Skip(err.Error())
				}
				t.Fatalf("build %s: %v", backend, err)
			}
			requireNoRustDiagnostics(t, diagnostics)
			output, err := exec.Command(binary).CombinedOutput()
			if err != nil {
				t.Fatalf("run %s binary: %v: %s", backend, err, output)
			}
			if want := interpreted + "\n"; string(output) != want {
				t.Fatalf("%s output = %q, want %q", backend, output, want)
			}
		})
	}
}

func TestRustGenerationIsDeterministicAndEvaluatesArgumentsLeftToRight(t *testing.T) {
	core := rustCoreForTest(t, `function Pair(Left: int, Right: int) -> (int,int) { (Left, Right) }
function First() -> int { 1 }
function Second() -> int { 2 }
function main() -> (int,int) { Pair(First(), Second()) }`)
	first, err := generateRust(core)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateRust(core)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identical Core IR produced different Rust source")
	}
	mainStart := strings.Index(first, "fn "+rustFunctionName("root.main"))
	entrypoint := strings.Index(first[mainStart:], "\n}\n\nfn main()")
	if mainStart < 0 || entrypoint < 0 {
		t.Fatal("generated root.main function is missing")
	}
	body := first[mainStart : mainStart+entrypoint]
	left := strings.Index(body, rustFunctionName("root.First")+"(")
	right := strings.Index(body, rustFunctionName("root.Second")+"(")
	call := strings.LastIndex(body, rustFunctionName("root.Pair")+"(")
	if left < 0 || right <= left || call <= right {
		t.Fatalf("call evaluation order was not materialized left-to-right:\n%s", body)
	}
}

func TestRustWorkspaceOwnsExactManifestAndLockfile(t *testing.T) {
	core := rustCoreForTest(t, `function main() -> int { 42 }`)
	workspace := t.TempDir()
	emission, err := emitRustWorkspace(core, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if emission.primary != filepath.Join(workspace, "Cargo.toml") {
		t.Fatalf("primary emission = %q", emission.primary)
	}
	for path, want := range map[string]string{
		filepath.Join(workspace, "Cargo.toml"):     rustCargoManifest,
		filepath.Join(workspace, "Cargo.lock"):     rustCargoLock,
		filepath.Join(workspace, "src", "main.rs"): mustGenerateRust(t, core),
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s is not deterministic", filepath.Base(path))
		}
	}
}

func TestRustBackendRegistrationReportsPinnedToolchainAndTarget(t *testing.T) {
	for _, backend := range Backends() {
		if backend.Name != BackendRust {
			continue
		}
		if backend.Stability != StabilityAlpha || len(backend.Targets) != 1 {
			t.Fatalf("Rust registration = %+v", backend)
		}
		target := backend.Targets[0]
		if target.Name != rustTargetTriple || target.Stability != StabilityAlpha ||
			target.Toolchain != "rust" || target.ToolchainVersion != rustToolchainVersion {
			t.Fatalf("Rust target = %+v", target)
		}
		return
	}
	t.Fatal("Rust backend is not registered")
}

func TestRustBackendRequiresAlphaOptInBeforeTouchingOutput(t *testing.T) {
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendRust})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "backend rust is alpha") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	requireRustSentinel(t, output)
}

func TestRustBackendDiagnosesSourceBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> string { 42 }`,
	}}, output, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if err != nil {
		t.Fatalf("invalid Slick reached Rust toolchain: %v", err)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != "SLK340" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	requireRustSentinel(t, output)
}
func TestRustLoweringLocatesUnsupportedStandardOperations(t *testing.T) {
	core := rustCoreForTest(t, `function main() -> bool {
    std.text.Contains("slick", "ick")
}`)
	runtime, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	err = validateRustCore(core, runtime)
	if err == nil ||
		!strings.Contains(err.Error(), "Rust lowering main.slk:2:") ||
		!strings.Contains(err.Error(), "standard-library operation std.text.Contains is not supported") {
		t.Fatalf("error=%v", err)
	}
}

func TestRustBackendRejectsUnsupportedCoreBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> bool {
    std.text.Contains("slick", "ick")
}`,
	}}, output, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "standard-library operation std.text.Contains is not supported") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("unsupported Core reached toolchain validation: %v", err)
	}
	requireRustSentinel(t, output)
}

func TestRustBackendReportsMissingAndIncompatibleToolchains(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendRust, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), "need rustc "+rustToolchainVersion+" and cargo "+rustToolchainVersion) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})

	t.Run("incompatible", func(t *testing.T) {
		tools := t.TempDir()
		writeRustTool(t, tools, "rustc", "#!/bin/sh\necho 'rustc 1.92.0'\n")
		writeRustTool(t, tools, "cargo", "#!/bin/sh\necho 'cargo 1.93.1'\n")
		t.Setenv("PATH", tools)
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendRust, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), `unsupported rustc toolchain "rustc 1.92.0"`) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})
}

func TestRustExecutableHasNoRustDeploymentDependency(t *testing.T) {
	outputDirectory := filepath.Join(t.TempDir(), "output with spaces")
	if err := os.Mkdir(outputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(outputDirectory, "app")
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), binary, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Rust toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	command := exec.Command(binary)
	command.Env = []string{"PATH="}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run without Rust or Cargo on PATH: %v: %s", err, output)
	}
	if string(output) != "42\n" {
		t.Fatalf("output = %q", output)
	}
}
func TestRustBackendIgnoresProjectCargoConfiguration(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".cargo")
	if err := os.Mkdir(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"),
		[]byte("[build]\nrustc-wrapper = \"/missing/slick-user-wrapper\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), binary, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Rust toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatalf("compiler-owned Cargo build read project configuration: %v", err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != "42\n" {
		t.Fatalf("built binary output=%q error=%v", output, err)
	}
}

func TestRustStringLiteralEscapesDirectionControls(t *testing.T) {
	got := rustStringLiteral("a\u202eb\u2066")
	if want := `"a\u{202e}b\u{2066}"`; got != want {
		t.Fatalf("literal = %q, want %q", got, want)
	}
}

func TestRustToolCommandsIgnoreCallerToolchainFiles(t *testing.T) {
	command := rustToolCommand("/bin/echo", "ok")
	if command.Dir != string(filepath.Separator) {
		t.Fatalf("tool directory = %q", command.Dir)
	}
	found := false
	for _, entry := range command.Env {
		if entry == "RUSTUP_TOOLCHAIN="+rustToolchainName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool environment does not pin RUSTUP_TOOLCHAIN: %v", command.Env)
	}
}

func TestRustBuildEnvironmentRemovesHostOverrides(t *testing.T) {
	got := rustBuildEnvironment([]string{
		"PATH=/bin", "RUSTFLAGS=host", "RUSTUP_TOOLCHAIN=caller", "CARGO_BUILD_TARGET=other",
		"CARGO_PROFILE_RELEASE_LTO=false", "RUSTC_WRAPPER=wrapper",
	}, map[string]string{
		"CARGO_ENCODED_RUSTFLAGS": "one\x1ftwo",
		"CARGO_HOME":              "/owned",
		"RUSTUP_TOOLCHAIN":        rustToolchainName,
	})
	want := []string{
		"PATH=/bin",
		"CARGO_ENCODED_RUSTFLAGS=one\x1ftwo",
		"CARGO_HOME=/owned",
		"RUSTUP_TOOLCHAIN=" + rustToolchainName,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %v, want %v", got, want)
	}
}

const rustLanguageRuntimeProgram = `
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
function MaybeMap() -> Map<string, int?> { map { "none": null, "one": 1 } }
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
    let OptionalMapping = MaybeMap()
    let Result = (Labelled, Apply((Value: int) -> int { Value + Offset }, Count), PresentValue, Propagate(), Values, Mapping, Values == [1, 2, 3], Recover(true, false), Recover(false, true), TaskValue(), Sum(), Held.Get(), Sequences(), MapValue, Updated.Contains("a") && Updated.Length() == 1, Present == 0 && OptionalValues.Get(0) == null && OptionalMapping.Get("none") == null)
    Result
}
`

func TestRustLanguageRuntimeMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustLanguageRuntimeProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	if want := "(constant/Empty/<callable>/number 41, 43, 0, Ok(1), [1, 2, 3], map {a: 2, b: 3}, true, body, close, 42, 6, 9, 22, 9, true, true)"; interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Rust language output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}

func TestRustCleanupPreservesPrimaryAndSuppressedFailures(t *testing.T) {
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
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil || string(output) != interpretedErr.Error()+"\n" {
		t.Fatalf("Rust cleanup output=%q error=%v, want %q", output, err, interpretedErr.Error()+"\n")
	}
	if strings.Contains(string(output), "panicked") {
		t.Fatalf("Rust panic replaced Slick cleanup failure: %s", output)
	}
}

func TestRustTaskFailureCancelsAndJoinsSibling(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
class Failure implements Error { Message: string }
function Spin() -> null { Spin() }
function Fail() -> null throws Failure { throw Failure { Message: "failed" } }
function Run() -> null throws Failure {
    async let Spinner = Spin()
    async let Broken = Fail()
    let Value = await Broken
    await Spinner
}
function main() -> null throws Failure { Run() }
`}
	binary := buildRustTestProgram(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("Rust task scope did not cancel and join its recursive sibling")
	}
	if err == nil || string(output) != "root.Failure: failed\n" {
		t.Fatalf("Rust task output=%q error=%v", output, err)
	}
}

func buildRustTestProgram(t *testing.T, source Source) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "app")
	diagnostics, err := BuildSourcesWithOptions([]Source{source}, binary, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Rust toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	return binary
}

func rustCoreForTest(t *testing.T, text string) coreProgram {
	t.Helper()
	program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
	requireNoRustDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func mustGenerateRust(t *testing.T, core coreProgram) string {
	t.Helper()
	source, err := generateRust(core)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func rustValidSources() []Source {
	return []Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> int { 42 }`}}
}

func rustSentinelOutput(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	return output
}

func requireRustSentinel(t *testing.T, output string) {
	t.Helper()
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "sentinel" {
		t.Fatalf("output changed to %q", contents)
	}
}

func requireNoRustDiagnostics(t *testing.T, diagnostics []Diagnostic) {
	t.Helper()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
}

func writeRustTool(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
