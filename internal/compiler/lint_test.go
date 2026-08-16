package compiler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// formatDiagnostics renders diagnostics the way the toolchain prints them, so a
// test pins the exact text a user reads.
func formatDiagnostics(diagnostics []compiler.Diagnostic) string {
	var out strings.Builder
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(&out, "%s:%d:%d: %s[%s]: %s\n",
			diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Severity, diagnostic.Code, diagnostic.Message)
	}
	return out.String()
}

func lintSource(text string) []compiler.Diagnostic {
	return compiler.Lint([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: text}})
}

func TestLintReportsThreeRulesInTotalOrder(t *testing.T) {
	diagnostics := lintSource(`function unused(Value: int) -> int {
    let NeverRead = Value + 1
    42
}

function deadExpression() -> int {
    "discarded"
    42
}

function unreachable() -> int {
    return 1
    2
}

function main() -> int {
    deadExpression() + unreachable()
}
`)
	const want = `main.slk:2:5: warning[SLK500]: binding NeverRead is never read
main.slk:7:5: warning[SLK501]: pure expression result is discarded
main.slk:13:5: warning[SLK502]: statement is unreachable
`
	if got := formatDiagnostics(diagnostics); got != want {
		t.Fatalf("lint output:\n%s\nwant:\n%s", got, want)
	}
}

func TestLintIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	source := `function main() -> int {
    let First = 1
    let Second = 2
    Second
}
`
	first := formatDiagnostics(lintSource(source))
	for range 20 {
		if got := formatDiagnostics(lintSource(source)); got != first {
			t.Fatalf("repeated lint produced %q, want %q", got, first)
		}
	}
	if first != "main.slk:2:5: warning[SLK500]: binding First is never read\n" {
		t.Fatalf("lint output %q", first)
	}
}

func TestLintResolvesBindingsByLexicalIdentity(t *testing.T) {
	diagnostics := lintSource(`function main() -> int {
    let Value = 1
    let Inner = if (true) {
        let Value = 2
        Value
    } else {
        0
    }
    Inner
}
`)
	if got := formatDiagnostics(diagnostics); got != "main.slk:2:5: warning[SLK500]: binding Value is never read\n" {
		t.Fatalf("shadowed lint output:\n%s", got)
	}
}

func TestLintCountsEveryReadForm(t *testing.T) {
	diagnostics := lintSource(`class Record {
    Name: string
    function Label() -> string {
        self.Name
    }
}

function main() -> string {
    let Entry = Record {
        Name: "Ada"
    }
    let Field = Entry.Name
    let Method = Entry.Label()
    let Interpolated = ` + "`" + `${Field}-${Entry.Name}` + "`" + `
    let Branched = if (true) {
        Method
    } else {
        ""
    }
    let Captured = Interpolated
    let Reader = () -> string {
        Captured
    }
    Branched + Reader()
}
`)
	if got := formatDiagnostics(diagnostics); got != "" {
		t.Fatalf("read forms reported:\n%s", got)
	}
}

func TestLintKeepsExplicitDiscardsAndEffectfulStatements(t *testing.T) {
	diagnostics := lintSource(`class Failure implements Error {}

function Work() -> Result<int, Failure> {
    Ok(2)
}

function main() -> Result<int, Failure> {
    let (_, Kept) = (1, 2)
    Work()?
    Ok(Kept)
}
`)
	if got := formatDiagnostics(diagnostics); got != "" {
		t.Fatalf("explicit discard or effectful statement reported:\n%s", got)
	}
}

func TestLintReportsGenericSourceOnce(t *testing.T) {
	diagnostics := lintSource(`function Identity<T>(Value: T) -> T {
    let Unused = Value
    Value
}

function main() -> string {
    let Number = Identity<int>(1)
    let Text = Identity<string>("two")
    let Flag = Identity<bool>(true)
    if (Flag && Number == 1) {
        Text
    } else {
        ""
    }
}
`)
	if got := formatDiagnostics(diagnostics); got != "main.slk:2:5: warning[SLK500]: binding Unused is never read\n" {
		t.Fatalf("generic lint output:\n%s", got)
	}
}

// TestLintReportsOnlyDirectlyUnreachableStatements holds the first version's
// scope: a statement after a terminator in the same block reports, while an
// all-path proof through if/else stays silent.
func TestLintReportsOnlyDirectlyUnreachableStatements(t *testing.T) {
	diagnostics := lintSource(`function everyBranchReturns(Flag: bool) -> int {
    if (Flag) {
        return 1
    } else {
        return 2
    }
    3
}

function loopControl(Values: int[]) -> int {
    for Value in Values {
        continue
        Value
    }
    0
}
`)
	const want = "main.slk:13:9: warning[SLK502]: statement is unreachable\n"
	if got := formatDiagnostics(diagnostics); got != want {
		t.Fatalf("unreachable lint output:\n%s\nwant:\n%s", got, want)
	}
}

func TestLintReportsCompilerErrorsInsteadOfWarnings(t *testing.T) {
	diagnostics := lintSource(`function main() -> string {
    let NeverRead = 1
    42
}
`)
	assertDiagnostic(t, diagnostics, "SLK340", "body produces int")
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != compiler.DiagnosticSeverityError {
			t.Fatalf("invalid program produced %s: %+v", diagnostic.Severity, diagnostic)
		}
	}
}

func TestLintPathReportsMissingSources(t *testing.T) {
	if _, err := compiler.LintPath(t.TempDir()); err == nil {
		t.Fatal("empty directory linted without error")
	}
	if _, err := compiler.LintPath(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing path linted without error")
	}
}

func TestEveryExamplePassesLint(t *testing.T) {
	projects, err := os.ReadDir("../../examples")
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		t.Run(project.Name(), func(t *testing.T) {
			diagnostics, lintErr := compiler.LintPath(filepath.Join("../../examples", project.Name()))
			if lintErr != nil {
				t.Fatalf("lint example: %v", lintErr)
			}
			if got := formatDiagnostics(diagnostics); got != "" {
				t.Fatalf("example reports:\n%s", got)
			}
		})
	}
}
