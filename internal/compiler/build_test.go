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
	"async-let":           "set SLICK_ASYNC_LEFT_URL and SLICK_ASYNC_RIGHT_URL",
	"bytes":               "Hello, 世界; 13; 72; bytes[13]",
	"callables":           "42; 42; 44; 42; parsed 7; empty text",
	"checked-errors":      "Ada: woof",
	"constants":           "SLK001; 512; 3; true; -256; Strict",
	"extensions":          "***; Ada: woof; Grace: meow",
	"fieldless-union":     "GET /todos; DELETE /todos/42",
	"generics":            "42;slick;size=3;root;1;no two;42;42;slick",
	"hello":               "Ada: woof",
	"maps":                "37; 2; Ada=37;Linus=55;; map {Ada: 37, Grace: 36}",
	"modular-effects":     "config: MODULAR_EFFECTS_INPUT is required",
	"operators":           "difference=7; product=30; ordered=true; logic=true; negative=-10; grouped=14",
	"optional":            "missing user",
	"optional-throws":     "Ada; Grace has no nickname; missing; Countess",
	"range-loop":          "0:Ada;2:Grace;",
	"result":              "missing user",
	"result-match":        "on is true; bad flag maybe; yes; no; false",
	"result-propagation":  "localhost:8080; empty host; 6; zero is not scorable",
	"result-types":        "42; corrupt payload; no such record; [alpha, beta]; cannot divide by zero; 7",
	"result-vs-throws":    "recovered from a thrown error; disk unavailable; disk unavailable",
	"std-convert":         "42",
	"std-env":             "missing;Ada;missing",
	"std-fs":              "set SLICK_STD_FS_EXAMPLE_PATH to an isolated file path",
	"std-fs-directory":    "a.txt:false,b.txt:false,c:true,é.txt:false; removed=true",
	"std-http":            "set SLICK_HTTP_EXAMPLE_URL",
	"std-http-server":     "set SLICK_HTTP_SERVER_ADDR",
	"std-io":              "hello:5;closed",
	"std-json":            `{"Name":"Slick","Port":8080,"Tags":["strict","native"]}`,
	"std-math":            "20 = 6 * 3 + 2|Divide:DivisionByZero",
	"std-path":            "report.txt:.txt",
	"std-sqlite":          "todo: ship sqlite",
	"std-source-scanning": "café;99:1;true;true;\"café\"",
	"std-text":            "alpha | beta | gamma;(alpha,beta | gamma)",
	"tagged-union":        "(6 + (3 * 7)) = 27; missing node",
	"using":               "value;ABC",
	"visibility":          "Ada:[private]; Ada: 1500 cents",
}

func isolateExampleEnvironment(t *testing.T) {
	t.Helper()
	for _, assignment := range os.Environ() {
		name, _, _ := strings.Cut(assignment, "=")
		isRuntimeConfig := strings.HasPrefix(name, "MODULAR_EFFECTS_") ||
			strings.HasPrefix(name, "TODO_API_") ||
			strings.HasPrefix(name, "SLICK_") &&
				name != "SLICK_LLVM_BIN" &&
				name != "SLICK_JANSSON_ROOT"
		if !isRuntimeConfig {
			continue
		}
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset example environment %s: %v", name, err)
		}
	}
}

func TestExampleMatrixIgnoresAmbientRuntimeConfiguration(t *testing.T) {
	guard := filepath.Join(t.TempDir(), "guard.txt")
	if err := os.WriteFile(guard, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLICK_STD_FS_EXAMPLE_PATH", guard)
	t.Run("interpreter", func(t *testing.T) {
		isolateExampleEnvironment(t)
		output, diagnostics, err := compiler.RunPath(examplePath("std-fs"))
		if err != nil {
			t.Fatalf("run std-fs example: %v", err)
		}
		assertNoDiagnostics(t, diagnostics)
		if output != exampleOutputs["std-fs"] {
			t.Fatalf("std-fs output = %q, want pinned fallback", output)
		}
	})
	contents, err := os.ReadFile(guard)
	if err != nil {
		t.Fatalf("ambient path was removed: %v", err)
	}
	if string(contents) != "do not touch" {
		t.Fatalf("ambient path was changed to %q", contents)
	}
}

func examplePath(project string) string {
	return filepath.Join("..", "..", "examples", project)
}

func TestEveryExampleHasLLVMExecutionContract(t *testing.T) {
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
			t.Errorf("example %s has no LLVM execution contract", entry.Name())
		}
	}
}

func TestBuildPathProducesStandaloneExampleBinaries(t *testing.T) {
	for project, expected := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			isolateExampleEnvironment(t)
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

// TestInterpreterMatchesExampleOutput pins `slick run` to the same output
// shared by the generated-Go and LLVM example matrices.
func TestInterpreterMatchesExampleOutput(t *testing.T) {
	for project, expected := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			isolateExampleEnvironment(t)
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

func TestLLVMMatchesExampleOutput(t *testing.T) {
	for project, expected := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			isolateExampleEnvironment(t)
			binary := filepath.Join(t.TempDir(), "app")
			diagnostics, err := compiler.BuildPathBackend(examplePath(project), binary, compiler.BackendLLVM)
			if err != nil {
				if strings.Contains(err.Error(), "LLVM") && strings.Contains(err.Error(), "not found") {
					t.Skip(err.Error())
				}
				t.Fatalf("build llvm binary: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			output, err := exec.Command(binary).CombinedOutput()
			if err != nil {
				t.Fatalf("run llvm binary: %v: %s", err, output)
			}
			if string(output) != expected+"\n" {
				t.Fatalf("expected %q, found %q", expected+"\n", output)
			}
		})
	}
}

func TestBuildPathStopsBeforeBackendOnSlickDiagnostics(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	if err := os.WriteFile(source, []byte(`function main() -> string { 42 }`), 0o644); err != nil {
		t.Fatalf("write invalid Slick source: %v", err)
	}
	for _, backend := range []compiler.Backend{compiler.BackendGo, compiler.BackendLLVM} {
		t.Run(string(backend), func(t *testing.T) {
			binary := filepath.Join(root, "app-"+string(backend))
			diagnostics, err := compiler.BuildPathBackend(source, binary, backend)
			if err != nil {
				t.Fatalf("check invalid Slick %s build: %v", backend, err)
			}
			assertDiagnostic(t, diagnostics, "SLK340", "body produces int")
			if _, err := os.Stat(binary); !os.IsNotExist(err) {
				t.Fatalf("invalid Slick %s build created a binary", backend)
			}
		})
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
	for _, backend := range []compiler.Backend{compiler.BackendGo, compiler.BackendLLVM} {
		t.Run(string(backend), func(t *testing.T) {
			binary := filepath.Join(root, "app-"+string(backend))
			diagnostics, err := compiler.BuildPathBackend(source, binary, backend)
			if err != nil {
				t.Fatalf("build throwing Slick binary: %v", err)
			}
			assertNoDiagnostics(t, diagnostics)
			output, err := exec.Command(binary).CombinedOutput()
			if err == nil {
				t.Fatalf("uncaught Slick error exited successfully")
			}
			if got, want := string(output), "root.Failure: boom\n"; got != want {
				t.Fatalf("uncaught Slick error = %q, want %q", got, want)
			}
		})
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
	for _, backend := range []compiler.Backend{compiler.BackendGo, compiler.BackendLLVM} {
		t.Run(string(backend), func(t *testing.T) {
			binary := filepath.Join(root, "app-"+string(backend))
			diagnostics, err := compiler.BuildPathBackend(source, binary, backend)
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
		})
	}
}
