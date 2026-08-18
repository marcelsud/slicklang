package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestMatrixExecutionEnginesMatchRegistry(t *testing.T) {
	engines := compiler.ExecutionEngines()
	backends := compiler.Backends()

	wantNames := make([]string, 0, 1+len(backends))
	wantNames = append(wantNames, "interpreter")
	for _, backend := range backends {
		wantNames = append(wantNames, string(backend.Name))
	}
	sort.Strings(wantNames)

	gotNames := make([]string, len(engines))
	for index, engine := range engines {
		gotNames[index] = engine.Name
		if index > 0 && engines[index-1].Name >= engine.Name {
			t.Fatalf("ExecutionEngines is not sorted by Name: %v", gotNames)
		}
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("ExecutionEngines names = %v, want %v", gotNames, wantNames)
	}

	var interpreter compiler.ExecutionEngine
	backendEngines := make(map[compiler.Backend]compiler.ExecutionEngine, len(backends))
	for _, engine := range engines {
		if engine.Interpreted {
			interpreter = engine
			continue
		}
		if engine.Backend == "" || engine.Name != string(engine.Backend) {
			t.Fatalf("backend engine %+v has mismatched name/backend", engine)
		}
		backendEngines[engine.Backend] = engine
	}

	if !interpreter.Interpreted || interpreter.Name != "interpreter" {
		t.Fatal("ExecutionEngines is missing the interpreter")
	}
	if interpreter.Backend != "" {
		t.Fatalf("interpreter Backend = %q, want empty", interpreter.Backend)
	}
	if interpreter.Stability != compiler.StabilityStable || !interpreter.Eligible {
		t.Fatalf("interpreter stability/eligibility = %s/%v, want stable/true", interpreter.Stability, interpreter.Eligible)
	}
	if len(interpreter.Targets) != 0 {
		t.Fatalf("interpreter Targets = %#v, want empty", interpreter.Targets)
	}

	if len(backendEngines) != len(backends) {
		t.Fatalf("backend engines = %d, registry = %d", len(backendEngines), len(backends))
	}
	for _, backend := range backends {
		engine, ok := backendEngines[backend.Name]
		if !ok {
			t.Fatalf("registry backend %s is missing from ExecutionEngines", backend.Name)
		}
		if engine.Stability != backend.Stability {
			t.Fatalf("%s stability = %s, want %s", engine.Name, engine.Stability, backend.Stability)
		}
		if engine.Eligible != backend.Eligible {
			t.Fatalf("%s eligible = %v, want %v", engine.Name, engine.Eligible, backend.Eligible)
		}
		if !reflect.DeepEqual(engine.Targets, backend.Targets) {
			t.Fatalf("%s targets = %#v, want %#v", engine.Name, engine.Targets, backend.Targets)
		}
	}
}

func TestMatrixEveryExampleHasContract(t *testing.T) {
	entries, err := os.ReadDir(examplePath(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := exampleOutputs[entry.Name()]; ok {
			continue
		}
		switch entry.Name() {
		case "std-process":
			// TestStdProcessExampleRunsEverywhere pins its nonzero Status.
		case "todo-api":
			// TestTodoAPIExampleServesAndCleansUpUnderLLVM exercises its server lifecycle.
		default:
			t.Errorf("example %s has no matrix execution contract", entry.Name())
		}
	}
}

func TestMatrixExamples(t *testing.T) {
	for _, engine := range compiler.ExecutionEngines() {
		t.Run(engine.Name, func(t *testing.T) {
			forEachEngineTarget(t, engine, func(t *testing.T, target string) {
				for project, expected := range exampleOutputs {
					t.Run(project, func(t *testing.T) {
						isolateExampleEnvironment(t)
						stdout, exitCode, err := engine.Run(examplePath(project), target)
						if err != nil {
							t.Fatalf("run: %v", err)
						}
						if exitCode != 0 {
							t.Fatalf("exit %d: %s", exitCode, stdout)
						}
						if want := expected + "\n"; stdout != want {
							t.Fatalf("stdout = %q, want %q", stdout, want)
						}
					})
				}
			})
		})
	}
}

func TestMatrixLanguageContracts(t *testing.T) {
	contracts := []struct {
		name   string
		source string
		want   string
	}{
		{"primitives", matrixPrimitiveProgram, "(230, false, -9223372036854775808, ok😀, -0, 1e+20, 1e-07, 1e+06, 7, true, 5, 10)"},
		{"language-runtime", matrixLanguageRuntimeProgram, "(constant/Empty/<callable>/number 41, 43, 0, Ok(1), [1, 2, 3], map {a: 2, b: 3}, true, body, close, 42, 6, 9, 22, 9, true, true)"},
		{"control-flow", matrixControlFlowProgram, "B2caughtearly"},
		{"optional-interface", matrixOptionalInterfaceProgram, "Ada|Ada|present"},
		{"wrapping-numerics", matrixWrappingNumericsProgram, "-9223372036854775808|-9223372036854775808|-2|0.1|0.1"},
		{"embedded-nul", matrixEmbeddedNULProgram, "true|true|true|true|3|true"},
		{"async-arity", matrixAsyncArityProgram, "55"},
	}
	paths := make(map[string]string, len(contracts))
	for _, contract := range contracts {
		paths[contract.name] = writeSlickMain(t, contract.source)
	}
	for _, engine := range compiler.ExecutionEngines() {
		t.Run(engine.Name, func(t *testing.T) {
			forEachEngineTarget(t, engine, func(t *testing.T, target string) {
				for _, contract := range contracts {
					t.Run(contract.name, func(t *testing.T) {
						stdout, exitCode, err := engine.Run(paths[contract.name], target)
						if err != nil {
							t.Fatalf("run: %v", err)
						}
						if exitCode != 0 {
							t.Fatalf("exit %d: %s", exitCode, stdout)
						}
						if got := strings.TrimSuffix(stdout, "\n"); got != contract.want {
							t.Fatalf("stdout = %q, want %q", got, contract.want)
						}
					})
				}
			})
		})
	}
}

func forEachEngineTarget(t *testing.T, engine compiler.ExecutionEngine, run func(*testing.T, string)) {
	t.Helper()
	for _, target := range engineRunTargets(engine) {
		if target == "" {
			run(t, target)
			continue
		}
		t.Run(target, func(t *testing.T) {
			run(t, target)
		})
	}
}

func writeSlickMain(t *testing.T, source string) string {
	t.Helper()
	return writeSlickSources(t, compiler.Source{Name: "main.slk", Namespace: "root", Text: source})
}

func writeSlickSources(t *testing.T, sources ...compiler.Source) string {
	t.Helper()
	root := t.TempDir()
	for _, source := range sources {
		path := filepath.Join(root, filepath.FromSlash(source.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create source directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(source.Text), 0o644); err != nil {
			t.Fatalf("write Slick source: %v", err)
		}
	}
	return root
}

func runOnEveryEngineSource(t *testing.T, source string) string {
	t.Helper()
	return runOnEveryEngine(t, writeSlickMain(t, source))
}

func runOnEveryEngine(t *testing.T, path string) string {
	t.Helper()
	var interpreted string
	var haveInterpreter bool
	for _, engine := range compiler.ExecutionEngines() {
		if !engine.Interpreted {
			continue
		}
		stdout, exitCode, err := engine.Run(path, "")
		if err != nil {
			t.Fatalf("interpreter: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("interpreter exit %d: %s", exitCode, stdout)
		}
		interpreted = strings.TrimSuffix(stdout, "\n")
		haveInterpreter = true
	}
	if !haveInterpreter {
		t.Fatal("interpreter engine is missing")
	}
	for _, engine := range compiler.ExecutionEngines() {
		if engine.Interpreted {
			continue
		}
		t.Run(engine.Name, func(t *testing.T) {
			stdout, exitCode, err := engine.Run(path, "")
			if err != nil {
				t.Fatal(err)
			}
			if exitCode != 0 {
				t.Fatalf("exit %d: %s", exitCode, stdout)
			}
			if got := strings.TrimSuffix(stdout, "\n"); got != interpreted {
				t.Fatalf("interpreter produced %q, %s produced %q", interpreted, engine.Name, got)
			}
		})
	}
	return interpreted
}

func mustExecutionEngine(t *testing.T, name string) compiler.ExecutionEngine {
	t.Helper()
	for _, engine := range compiler.ExecutionEngines() {
		if engine.Name == name {
			return engine
		}
	}
	t.Fatalf("execution engine %q is not registered", name)
	return compiler.ExecutionEngine{}
}

func stableExecutionEngines() []compiler.ExecutionEngine {
	engines := compiler.ExecutionEngines()
	stable := make([]compiler.ExecutionEngine, 0, len(engines))
	for _, engine := range engines {
		if engine.Stability == compiler.StabilityStable {
			stable = append(stable, engine)
		}
	}
	return stable
}

func engineRunTargets(engine compiler.ExecutionEngine) []string {
	if engine.Interpreted || len(engine.Targets) == 0 {
		return []string{""}
	}
	targets := make([]string, len(engine.Targets))
	for index, target := range engine.Targets {
		targets[index] = target.Name
	}
	return targets
}

func engineBuildOptions(engine compiler.ExecutionEngine, target string) compiler.BuildOptions {
	return compiler.BuildOptions{
		Backend:    engine.Backend,
		Target:     target,
		AllowAlpha: engine.Stability == compiler.StabilityAlpha,
	}
}

const matrixPrimitiveProgram = `const Maximum: int = 9223372036854775807

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

const matrixLanguageRuntimeProgram = `
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

const matrixControlFlowProgram = `
class Failure implements Error {}

function fail() -> string throws Failure {
    throw Failure("boom")
}

function stop_early() -> string {
    if (true) { return "early" }
    "late"
}

function main() -> string {
    let Output = ""
    for Name, Number in zip(["A", "B"], 1..3) {
        if (Name == "A") { continue }
        Output = Output + Name + ` + "`${Number}`" + `
    }
    let Recovered = fail() catch (error) {
        Failure => "caught"
    }
    Output + Recovered + stop_early()
}
`

const matrixOptionalInterfaceProgram = `
interface Marker {}

class Item implements Marker {
    Name: string
}

function Maybe() -> Item? {
    Item { Name: "Ada" }
}

function Promote(Value: Marker?) -> string {
    if (Value == null) { "missing" } else { "present" }
}

function main() -> string {
    let Value = Maybe()
    let First = if (Value == null) { "missing" } else { Value.Name }
    let Second = if (Value == null) { "missing" } else { Value.Name }
    let Promoted = if (Value == null) { "missing" } else { Promote(Value) }
    First + "|" + Second + "|" + Promoted
}
`

const matrixWrappingNumericsProgram = `
function main() -> string {
    let Maximum = 9223372036854775807
    let Minimum = Maximum + 1
    let Negated = -Minimum
    let Multiplied = Maximum * 2
    let Fraction = 0.1
    let Converted = std.convert.FloatToString(Fraction)
    ` + "`${Minimum}|${Negated}|${Multiplied}|${Fraction}|${Converted}`" + `
}
`

const matrixEmbeddedNULProgram = `
function main() -> string {
    let Value = "a\u0000b\u0000c"
    let Parts = std.text.Split(Value, "\u0000")
    let Joined = std.text.Join(Parts, "\u0000")
    let Cut = std.text.Cut(Value, "\u0000")
    let After = if (Cut == null) {
        "missing"
    } else {
        let (_, Suffix) = Cut
        Suffix
    }
    let Contains = std.text.Contains(Value, "\u0000b")
    let Starts = std.text.StartsWith(Value, "a\u0000")
    let Ends = std.text.EndsWith(Value, "\u0000c")
    let Equal = Joined == Value
    let Length = Parts.Length()
    let SuffixEqual = After == "b\u0000c"
    ` + "`${Contains}|${Starts}|${Ends}|${Equal}|${Length}|${SuffixEqual}`" + `
}
`

const matrixAsyncArityProgram = `
function AddTen(A: int, B: int, C: int, D: int, E: int, F: int, G: int, H: int, I: int, J: int) -> int {
    A + B + C + D + E + F + G + H + I + J
}

function main() -> string {
    async let Work = AddTen(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
    let Total = await Work
    ` + "`${Total}`" + `
}
`
