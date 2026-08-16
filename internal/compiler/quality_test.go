package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// qualityOf analyzes one source and fails the test when the analysis itself
// fails, which is never a passing report.
func qualityOf(t *testing.T, text string) compiler.QualityReport {
	t.Helper()
	report, err := compiler.Quality([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: text}})
	if err != nil {
		t.Fatalf("quality analysis failed: %v", err)
	}
	return report
}

// callableOf finds one measured callable by canonical symbol.
func callableOf(t *testing.T, report compiler.QualityReport, symbol string) compiler.CallableQuality {
	t.Helper()
	for _, callable := range report.Callables {
		if callable.Symbol == symbol {
			return callable
		}
	}
	t.Fatalf("no callable %s in %+v", symbol, report.Callables)
	return compiler.CallableQuality{}
}

func assertScores(t *testing.T, report compiler.QualityReport, symbol string, cyclomatic, cognitive int) {
	t.Helper()
	callable := callableOf(t, report, symbol)
	if callable.CyclomaticComplexity != cyclomatic || callable.CognitiveComplexity != cognitive {
		t.Fatalf("%s scored cyclomatic %d cognitive %d, want %d and %d",
			symbol, callable.CyclomaticComplexity, callable.CognitiveComplexity, cyclomatic, cognitive)
	}
}

// TestCyclomaticComplexityCountsEveryDecisionForm pins the published formula:
// one for each if, for, short circuit, and propagation, N-1 for a match with N
// arms, and N for a catch with N arms.
func TestCyclomaticComplexityCountsEveryDecisionForm(t *testing.T) {
	report := qualityOf(t, `class Failure implements Error {}

function Plain() -> int {
    1
}

function OneIf(Flag: bool) -> int {
    if (Flag) {
        1
    } else {
        0
    }
}

function OneLoop(Values: int[]) -> int {
    for Value in Values {
        let Ignored = Value
        Ignored
    }
    0
}

function ShortCircuits(A: bool, B: bool, C: bool) -> bool {
    A && B || C
}

function ThreeArmMatch(Value: Result<int, Failure>) -> int {
    match Value {
        Ok(Number) => Number
        Err(_) => 0
    }
}

function Work() -> Result<int, Failure> {
    Ok(2)
}

function OneCatchArm() -> int {
    Unwrapped()
    catch (error) {
        Failure => 0
    }
}

function Unwrapped() -> int throws Failure {
    unwrap(Work())
}

function Propagation() -> Result<int, Failure> {
    let Value = Work()?
    Ok(Value)
}
`)
	assertScores(t, report, "root.Plain", 1, 0)
	assertScores(t, report, "root.OneIf", 2, 1)
	assertScores(t, report, "root.OneLoop", 2, 1)
	// Two operators, and two runs because the operator changes.
	assertScores(t, report, "root.ShortCircuits", 3, 2)
	// Two arms are one additional path.
	assertScores(t, report, "root.ThreeArmMatch", 2, 1)
	// One handled alternative beside the success path.
	assertScores(t, report, "root.OneCatchArm", 2, 1)
	assertScores(t, report, "root.Propagation", 2, 1)
}

// TestCognitiveComplexityCountsNestedContext pins nesting, else-if chains, mixed
// boolean runs, transparent using, and separately scored lambdas.
func TestCognitiveComplexityCountsNestedContext(t *testing.T) {
	report := qualityOf(t, `class Handle {
    function Close() -> null {
        null
    }
}

function Open() -> Handle {
    Handle {}
}

function FlatBranches(A: bool, B: bool) -> int {
    let First = if (A) {
        1
    } else {
        0
    }
    let Second = if (B) {
        1
    } else {
        0
    }
    First + Second
}

function NestedBranches(A: bool, B: bool) -> int {
    if (A) {
        if (B) {
            2
        } else {
            1
        }
    } else {
        0
    }
}

function ElseIfChain(Value: int) -> int {
    if (Value == 0) {
        0
    } else if (Value == 1) {
        1
    } else if (Value == 2) {
        2
    } else {
        3
    }
}

function MixedRuns(A: bool, B: bool, C: bool, D: bool) -> bool {
    A && B && C || D
}

function TransparentUsing(Flag: bool) -> int {
    using Resource = Open() {
        if (Flag) {
            1
        } else {
            0
        }
    }
}

function SeparateLambda(Flag: bool) -> int {
    let Choose = (Value: int) -> int {
        if (Value > 0) {
            Value
        } else {
            0
        }
    }
    if (Flag) {
        Choose(1)
    } else {
        0
    }
}
`)
	// Two branches at nesting zero.
	assertScores(t, report, "root.FlatBranches", 3, 2)
	// The inner branch pays for the context it is read inside.
	assertScores(t, report, "root.NestedBranches", 3, 3)
	// A chain is three links, not a three-deep tower.
	assertScores(t, report, "root.ElseIfChain", 4, 3)
	// Two runs of && then ||: three operators, two runs.
	assertScores(t, report, "root.MixedRuns", 4, 2)
	// using adds neither score nor nesting.
	assertScores(t, report, "root.TransparentUsing", 2, 1)
	// The lambda's decision belongs to the lambda alone.
	assertScores(t, report, "root.SeparateLambda", 2, 1)
	assertScores(t, report, "root.SeparateLambda.lambda@64:18", 2, 1)
}

// TestComplexityNestsInsideMatchAndCatchArms holds arm bodies to one additional
// nesting level.
func TestComplexityNestsInsideMatchAndCatchArms(t *testing.T) {
	report := qualityOf(t, `class Failure implements Error {}

function Work() -> Result<int, Failure> {
    Ok(2)
}

function InsideMatchArm(Flag: bool) -> int {
    match Work() {
        Ok(Number) => if (Flag) {
            Number
        } else {
            0
        }
        Err(_) => 0
    }
}

function Failing() -> int throws Failure {
    unwrap(Work())
}

function InsideCatchArm(Flag: bool) -> int {
    Failing()
    catch (error) {
        Failure => if (Flag) {
            1
        } else {
            0
        }
    }
}
`)
	// match 1 + branch at nesting 1 = 3.
	assertScores(t, report, "root.InsideMatchArm", 3, 3)
	// catch arm 1 + branch at nesting 1 = 3.
	assertScores(t, report, "root.InsideCatchArm", 3, 3)
}

func TestQualityReportsGenericSourceOnceRegardlessOfInstantiations(t *testing.T) {
	report := qualityOf(t, `function Pick<T>(Value: T, Flag: bool) -> T {
    if (Flag) {
        Value
    } else {
        Value
    }
}

function main() -> string {
    let Number = Pick<int>(1, true)
    let Text = Pick<string>("two", false)
    let Flag = Pick<bool>(true, true)
    if (Flag && Number == 1) {
        Text
    } else {
        ""
    }
}
`)
	measured := 0
	for _, callable := range report.Callables {
		if callable.Symbol == "root.Pick" {
			measured++
		}
	}
	if measured != 1 {
		t.Fatalf("generic declaration measured %d times: %+v", measured, report.Callables)
	}
	assertScores(t, report, "root.Pick", 2, 1)
}

func TestQualityFlagsBothComplexityLimits(t *testing.T) {
	report := qualityOf(t, `function Decisions(A: bool, B: bool, C: bool, D: bool, E: bool, F: bool) -> int {
    if (A && B && C && D && E && F) {
        if (A) {
            if (B) {
                if (C) {
                    if (D) {
                        if (E) {
                            if (F) {
                                7
                            } else {
                                6
                            }
                        } else {
                            5
                        }
                    } else {
                        4
                    }
                } else {
                    3
                }
            } else {
                2
            }
        } else {
            1
        }
    } else {
        0
    }
}
`)
	const want = `main.slk:1:10: warning[SLK503]: cyclomatic complexity 13 exceeds limit 10 in root.Decisions
main.slk:1:10: warning[SLK504]: cognitive complexity 29 exceeds limit 15 in root.Decisions
`
	if got := formatDiagnostics(report.Diagnostics); got != want {
		t.Fatalf("complexity diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if report.ComplexityViolations() != 2 || report.Warnings() != 2 || report.Errors() != 0 {
		t.Fatalf("counts: violations=%d warnings=%d errors=%d", report.ComplexityViolations(), report.Warnings(), report.Errors())
	}
	if report.Passed() {
		t.Fatal("complexity violations passed the gate")
	}
}

// TestQualityStopsAtCompilerErrors proves an invalid AST produces no formatting,
// lint, or complexity claim, and names the skipped sections.
func TestQualityStopsAtCompilerErrors(t *testing.T) {
	report := qualityOf(t, `function main()->string{
    let NeverRead = 1
    42
}
`)
	if report.Compiled || report.Passed() {
		t.Fatalf("invalid program compiled=%v passed=%v", report.Compiled, report.Passed())
	}
	if len(report.Callables) != 0 || len(report.Unformatted) != 0 {
		t.Fatalf("invalid program analyzed: callables=%+v unformatted=%+v", report.Callables, report.Unformatted)
	}
	if report.Errors() != 1 || report.Warnings() != 0 {
		t.Fatalf("invalid program errors=%d warnings=%d: %+v", report.Errors(), report.Warnings(), report.Diagnostics)
	}
	want := []compiler.QualitySection{
		{Name: "FORMAT", Status: compiler.QualityStatusSkip},
		{Name: "CHECK", Status: compiler.QualityStatusFail},
		{Name: "LINT", Status: compiler.QualityStatusSkip},
		{Name: "COMPLEXITY", Status: compiler.QualityStatusSkip},
	}
	if got := report.Sections(); !equalSections(got, want) {
		t.Fatalf("sections=%+v, want %+v", got, want)
	}
}

func equalSections(got, want []compiler.QualitySection) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestQualityReportsUnformattedSourcesWithoutWriting(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	const text = "function main() -> int {\n        1\n}\n"
	if err := os.WriteFile(source, []byte(text), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	report, err := compiler.QualityPath(root)
	if err != nil {
		t.Fatalf("quality path: %v", err)
	}
	if len(report.Unformatted) != 1 || report.Unformatted[0] != "main.slk" {
		t.Fatalf("unformatted=%+v", report.Unformatted)
	}
	if report.Passed() {
		t.Fatal("unformatted source passed the gate")
	}
	if got := readTestSource(t, source); got != text {
		t.Fatalf("quality rewrote the source: %q", got)
	}
}

// TestQualityMeasuresCodeLinesWithoutGatingOnThem holds LOC to evidence: a long
// but simple callable still passes.
func TestQualityMeasuresCodeLinesWithoutGatingOnThem(t *testing.T) {
	var body strings.Builder
	body.WriteString("// a comment line\nfunction main() -> int {\n")
	for index := range 60 {
		body.WriteString("    let Step")
		body.WriteString(string(rune('A' + index%26)))
		body.WriteString(string(rune('a' + index/26)))
		body.WriteString(" = 1\n")
		body.WriteString("\n")
	}
	body.WriteString("    1\n}\n")
	report := qualityOf(t, body.String())
	main := callableOf(t, report, "root.main")
	if main.CodeLines != 63 {
		t.Fatalf("main measured %d code lines", main.CodeLines)
	}
	if report.CodeLines != 63 {
		t.Fatalf("project measured %d code lines", report.CodeLines)
	}
	if report.ComplexityViolations() != 0 {
		t.Fatalf("length produced complexity violations: %+v", report.Diagnostics)
	}
	// Every binding is unread, so the gate fails for lint alone; length never
	// contributes.
	if report.Warnings() != 60 {
		t.Fatalf("warnings=%d", report.Warnings())
	}
	largest, measured := report.LargestCallable()
	if !measured || largest.Symbol != "root.main" || largest.CodeLines != 63 {
		t.Fatalf("largest callable=%+v measured=%v", largest, measured)
	}
}

func TestQualityCountsCodeLinesExcludingBlanksAndComments(t *testing.T) {
	report := qualityOf(t, `// leading comment

/* block
   comment */
function main() -> string {

    // inner comment
    "ok"
}
`)
	if report.CodeLines != 3 {
		t.Fatalf("code lines=%d, want 3", report.CodeLines)
	}
	if main := callableOf(t, report, "root.main"); main.CodeLines != 3 {
		t.Fatalf("main code lines=%d, want 3", main.CodeLines)
	}
}

func TestQualityIsByteIdenticalAcrossRepeatedRuns(t *testing.T) {
	first, err := compiler.QualityPath(filepath.Join("../../examples", "todo-api"))
	if err != nil {
		t.Fatalf("quality path: %v", err)
	}
	baseline := renderQuality(first)
	for range 20 {
		repeated, repeatErr := compiler.QualityPath(filepath.Join("../../examples", "todo-api"))
		if repeatErr != nil {
			t.Fatalf("repeated quality path: %v", repeatErr)
		}
		if got := renderQuality(repeated); got != baseline {
			t.Fatalf("repeated report:\n%s\nwant:\n%s", got, baseline)
		}
	}
}

// renderQuality flattens a report into the total order a printed report follows.
func renderQuality(report compiler.QualityReport) string {
	var out strings.Builder
	for _, section := range report.Sections() {
		out.WriteString(string(section.Status))
		out.WriteString(" ")
	}
	out.WriteString("\n")
	for _, file := range report.Unformatted {
		out.WriteString(file + "\n")
	}
	out.WriteString(formatDiagnostics(report.Diagnostics))
	for _, callable := range report.Callables {
		out.WriteString(callable.Symbol)
		out.WriteString(" ")
		out.WriteString(callable.File)
		out.WriteString("\n")
	}
	return out.String()
}

func TestEveryExamplePassesTheQualityGate(t *testing.T) {
	projects, err := os.ReadDir("../../examples")
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		t.Run(project.Name(), func(t *testing.T) {
			report, qualityErr := compiler.QualityPath(filepath.Join("../../examples", project.Name()))
			if qualityErr != nil {
				t.Fatalf("quality example: %v", qualityErr)
			}
			if !report.Passed() {
				t.Fatalf("example failed the gate: unformatted=%+v diagnostics:\n%s", report.Unformatted, formatDiagnostics(report.Diagnostics))
			}
		})
	}
}

func TestQualityPathReportsFilesystemFailures(t *testing.T) {
	if _, err := compiler.QualityPath(t.TempDir()); err == nil {
		t.Fatal("empty directory analyzed without error")
	}
	if _, err := compiler.QualityPath(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing path analyzed without error")
	}
}

func readTestSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
