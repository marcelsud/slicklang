package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// TestQualityReportsPassingProjectExactly pins the whole layout of a passing
// report: fixed section order, the gate line, one summary line, and the three
// navigation maxima.
func TestQualityReportsPassingProjectExactly(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> int {\n    1\n}\n"})
	var stdout, stderr bytes.Buffer
	if status := runQuality([]string{root}, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	const want = `FORMAT      PASS
CHECK       PASS
LINT        PASS
COMPLEXITY  PASS

QUALITY GATE: PASS
Files: 1  Code lines: 3  Errors: 0  Warnings: 0  Complexity violations: 0
Max cyclomatic: 1 root.main
Max cognitive: 0 root.main
Largest callable: 3 lines root.main
`
	if stdout.String() != want {
		t.Fatalf("report:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

// TestQualityReportsFailuresInTotalOrder pins the failing layout: unformatted
// paths before diagnostics, and diagnostics ordered by file, line, column, then
// code.
func TestQualityReportsFailuresInTotalOrder(t *testing.T) {
	root := writeProject(t, map[string]string{
		"a.slk": `function Deep(A: bool, B: bool, C: bool, D: bool, E: bool, F: bool) -> int {
    if (A) {
        if (B) {
            if (C) {
                if (D) {
                    if (E) {
                        if (F) {
                            6
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
`,
		"b.slk": "function main() -> int {\n        let NeverRead = 1\n        2\n}\n",
	})
	var stdout, stderr bytes.Buffer
	if status := runQuality([]string{"--check", root}, &stdout, &stderr); status != 1 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	const want = `FORMAT      FAIL
CHECK       PASS
LINT        FAIL
COMPLEXITY  FAIL

b.slk: source is not canonically formatted
a.slk:1:10: warning[SLK504]: cognitive complexity 21 exceeds limit 15 in root.Deep
b.slk:2:9: warning[SLK500]: binding NeverRead is never read

QUALITY GATE: FAIL
Files: 2  Code lines: 31  Errors: 0  Warnings: 2  Complexity violations: 1
Max cyclomatic: 7 root.Deep
Max cognitive: 21 root.Deep
Largest callable: 27 lines root.Deep
`
	if stdout.String() != want {
		t.Fatalf("report:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

// TestQualityReportModeAlwaysExitsZero holds the report mode to a local report:
// a failed gate is still exit 0, so only --check enforces it.
func TestQualityReportModeAlwaysExitsZero(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> int {\n    let NeverRead = 1\n    2\n}\n"})
	var stdout, stderr bytes.Buffer
	if status := runQuality([]string{root}, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("report mode status=%d stderr=%q", status, stderr.String())
	}
	if !strings.Contains(stdout.String(), "QUALITY GATE: FAIL") {
		t.Fatalf("report:\n%s", stdout.String())
	}

	stdout.Reset()
	if status := runQuality([]string{root, "--check"}, &stdout, &stderr); status != 1 {
		t.Fatalf("check mode status=%d", status)
	}
}

// TestQualityCompilerFailureSkipsSemanticSections proves compiler errors are
// never converted into formatting, lint, or complexity claims.
func TestQualityCompilerFailureSkipsSemanticSections(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> string {\n    42\n}\n"})
	var stdout, stderr bytes.Buffer
	if status := runQuality([]string{"--check", root}, &stdout, &stderr); status != 1 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	const want = `FORMAT      SKIP
CHECK       FAIL
LINT        SKIP
COMPLEXITY  SKIP

main.slk:1:10: error[SLK340]: root.main returns string, but its body produces int

QUALITY GATE: FAIL
Files: 1  Code lines: 3  Errors: 1  Warnings: 0  Complexity violations: 0
`
	if stdout.String() != want {
		t.Fatalf("report:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

// TestQualityFailsAnalysisWithoutPassingReport covers the exit-2 branch every
// analyzer, filesystem, and load failure shares: nothing is printed to stdout
// and the gate never reads PASS.
func TestQualityFailsAnalysisWithoutPassingReport(t *testing.T) {
	t.Run("no sources", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if status := runQuality([]string{t.TempDir()}, &stdout, &stderr); status != 2 || stdout.Len() != 0 {
			t.Fatalf("status=%d stdout=%q", status, stdout.String())
		}
		if !strings.Contains(stderr.String(), "no .slk files found") {
			t.Fatalf("stderr=%q", stderr.String())
		}
	})
	t.Run("missing path", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		status := runQuality([]string{filepath.Join(t.TempDir(), "absent"), "--check"}, &stdout, &stderr)
		if status != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "quality: ") {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
	})
}

func TestQualityArgumentContracts(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> int {\n    1\n}\n"})
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	accepted := map[string][]string{
		"default path":   {},
		"explicit path":  {"."},
		"check first":    {"--check", "."},
		"check last":     {".", "--check"},
		"check only":     {"--check"},
		"relative file":  {"main.slk"},
		"path then flag": {"main.slk", "--check"},
	}
	for name, args := range accepted {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := runQuality(args, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
			if !strings.Contains(stdout.String(), "QUALITY GATE: PASS") {
				t.Fatalf("stdout=%q", stdout.String())
			}
		})
	}

	rejected := map[string]struct {
		args    []string
		message string
	}{
		"duplicate flag": {[]string{"--check", "--check"}, "quality --check may only be specified once"},
		"duplicate path": {[]string{".", "."}, `unexpected quality argument "."`},
		"unknown flag":   {[]string{"--json"}, `unknown quality flag "--json"`},
		"short flag":     {[]string{"-c"}, `unknown quality flag "-c"`},
	}
	for name, test := range rejected {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := runQuality(test.args, &stdout, &stderr); status != 2 || stdout.Len() != 0 {
				t.Fatalf("status=%d stdout=%q", status, stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), test.message) || !strings.Contains(stderr.String(), "slick quality [--check] [path]") {
				t.Fatalf("stderr=%q, want %q and usage", stderr.String(), test.message)
			}
		})
	}
}

func TestQualityRepeatedRunsAreByteIdentical(t *testing.T) {
	var first bytes.Buffer
	var stderr bytes.Buffer
	if status := runQuality([]string{filepath.Join("..", "..", "examples", "todo-api")}, &first, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	for range 20 {
		var repeated bytes.Buffer
		if status := runQuality([]string{filepath.Join("..", "..", "examples", "todo-api")}, &repeated, &stderr); status != 0 {
			t.Fatalf("repeated status=%d", status)
		}
		if repeated.String() != first.String() {
			t.Fatalf("repeated report:\n%s\nwant:\n%s", repeated.String(), first.String())
		}
	}
	if !strings.Contains(first.String(), "QUALITY GATE: PASS") {
		t.Fatalf("example report:\n%s", first.String())
	}
}
