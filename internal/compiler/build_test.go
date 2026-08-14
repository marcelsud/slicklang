package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// exampleOutputs pins the observable output of every example project. The
// result-* and optional examples double as the documentation for those
// features, so the output each one documents is verified here instead of being
// left to rot.
var exampleOutputs = map[string]string{
	"hello":              "Ada: woof",
	"bytes":              "Hello, 世界; 13; 72; bytes[13]",
	"callables":          "42; 42; 44; 42; parsed 7; empty text",
	"range-loop":         "0:Ada;2:Grace;",
	"checked-errors":     "Ada: woof",
	"constants":          "SLK001; 512; 3; true; -256; Strict",
	"fieldless-union":    "GET /todos; DELETE /todos/42",
	"generics":           "42;slick;size=3;root;1;no two;42;42;slick",
	"maps":               "37; 2; Ada=37;Linus=55;; map {Ada: 37, Grace: 36}",
	"optional":           "missing user",
	"optional-throws":    "Ada; Grace has no nickname; missing; Countess",
	"operators":          "difference=7; product=30; ordered=true; logic=true; negative=-10; grouped=14",
	"result":             "missing user",
	"result-match":       "on is true; bad flag maybe; yes; no; false",
	"result-propagation": "localhost:8080; empty host; 6; zero is not scorable",
	"result-types":       "42; corrupt payload; no such record; [alpha, beta]; cannot divide by zero; 7",
	"result-vs-throws":   "recovered from a thrown error; disk unavailable; disk unavailable",
	"std-env":            "missing;Ada;missing",
	"tagged-union":       "(6 + (3 * 7)) = 27; missing node",
	"std-fs-directory":   "a.txt:false,b.txt:false,c:true,\u00e9.txt:false; removed=true",
	"std-io":             "hello:5;closed",
	"using":              "value;ABC",
	"visibility":         "Ada:[private]; Ada: 1500 cents",
	"std-sqlite":         "todo: ship sqlite",
}

func examplePath(project string) string {
	return filepath.Join("..", "..", "examples", project)
}

func TestBuildPathProducesStandaloneExampleBinaries(t *testing.T) {
	for project, expected := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "app")
			diagnostics, err := compiler.BuildPath(examplePath(project), binary)
			if err != nil {
				t.Fatalf("build native binary: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			output, err := exec.Command(binary).CombinedOutput()
			if err != nil {
				t.Fatalf("run native binary: %v: %s", err, output)
			}
			if string(output) != expected+"\n" {
				t.Fatalf("expected %q, found %q", expected+"\n", output)
			}
		})
	}
}

// TestInterpreterMatchesExampleOutput holds `slick run` to the same output the
// native binary produces, so the two backends cannot drift apart on any
// documented example.
func TestInterpreterMatchesExampleOutput(t *testing.T) {
	for project, expected := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			output, diagnostics, err := compiler.RunPath(examplePath(project))
			if err != nil {
				t.Fatalf("run example: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			if output != expected {
				t.Fatalf("expected %q, found %q", expected, output)
			}
		})
	}
}

func TestBuildPathStopsBeforeGoBuildOnSlickDiagnostics(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	if err := os.WriteFile(source, []byte(`function main() -> string { 42 }`), 0o644); err != nil {
		t.Fatalf("write invalid Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(source, binary)
	if err != nil {
		t.Fatalf("check invalid Slick build: %v", err)
	}
	assertDiagnostic(t, diagnostics, "SLK340", "body produces int")
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("invalid Slick build created a binary")
	}
}

func TestBuiltBinaryReportsUncaughtSlickError(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	program := `
class Failure implements Error {}
function main() -> string throws Failure {
    throw Failure("boom")
}
`
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatalf("write throwing Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(source, binary)
	if err != nil {
		t.Fatalf("build throwing Slick binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil {
		t.Fatalf("uncaught Slick error exited successfully")
	}
	if !strings.Contains(string(output), "root.Failure: boom") {
		t.Fatalf("expected uncaught Slick error, found %q", output)
	}
}

func TestBuiltBinaryExecutesZipCatchAndEarlyReturn(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	program := `
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
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatalf("write Slick control-flow source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(source, binary)
	if err != nil {
		t.Fatalf("build Slick control-flow binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run Slick control-flow binary: %v: %s", err, output)
	}
	if string(output) != "B2caughtearly\n" {
		t.Fatalf("unexpected control-flow output %q", output)
	}
}
