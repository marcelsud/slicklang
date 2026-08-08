package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestBuildPathProducesStandaloneExampleBinaries(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	tests := map[string]struct {
		project string
		output  string
	}{
		"hello":          {project: "hello", output: "Ada: woof\n"},
		"range loop":     {project: "range-loop", output: "0:Ada;2:Grace;\n"},
		"checked errors": {project: "checked-errors", output: "Ada: woof\n"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "app")
			diagnostics, err := compiler.BuildPath(filepath.Join(root, test.project), binary)
			if err != nil {
				t.Fatalf("build native binary: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			output, err := exec.Command(binary).CombinedOutput()
			if err != nil {
				t.Fatalf("run native binary: %v: %s", err, output)
			}
			if string(output) != test.output {
				t.Fatalf("expected %q, found %q", test.output, output)
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
