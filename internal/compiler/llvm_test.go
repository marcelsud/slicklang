package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestLLVMBackendMatchesGoOnOperators(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.slk")
	program := `
function main() -> string {
    let Value = 10
    let Difference = Value - 3
    let Product = Difference * 5 - 5
    let Ordered = Difference < Product
    let Logic = !false && (Difference <= Product || false)
    let Negative = -Value
    let Grouped = (Value - 3) * 2
    ` + "`difference=${Difference}; product=${Product}; ordered=${Ordered}; logic=${Logic}; negative=${Negative}; grouped=${Grouped}`" + `
}
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	goBin := filepath.Join(root, "goapp")
	llvmBin := filepath.Join(root, "llvmapp")
	if diags, err := compiler.BuildPathBackend(src, goBin, compiler.BackendGo); err != nil || len(diags) > 0 {
		t.Fatalf("go build: %v %v", err, diags)
	}
	if diags, err := compiler.BuildPathBackend(src, llvmBin, compiler.BackendLLVM); err != nil || len(diags) > 0 {
		t.Fatalf("llvm build: %v %v", err, diags)
	}
	goOut, err := exec.Command(goBin).CombinedOutput()
	if err != nil {
		t.Fatalf("run go: %v: %s", err, goOut)
	}
	llvmOut, err := exec.Command(llvmBin).CombinedOutput()
	if err != nil {
		t.Fatalf("run llvm: %v: %s", err, llvmOut)
	}
	if string(goOut) != string(llvmOut) {
		t.Fatalf("go=%q llvm=%q", goOut, llvmOut)
	}
	if !strings.Contains(string(llvmOut), "difference=7") {
		t.Fatalf("unexpected llvm output %q", llvmOut)
	}
}

func TestLLVMBackendMatchesExampleProjects(t *testing.T) {
	for _, project := range []string{"operators", "hello", "constants", "visibility", "optional", "result"} {
		t.Run(project, func(t *testing.T) {
			goBin := filepath.Join(t.TempDir(), "goapp")
			llvmBin := filepath.Join(t.TempDir(), "llvmapp")
			path := filepath.Join("..", "..", "examples", project)
			if diags, err := compiler.BuildPathBackend(path, goBin, compiler.BackendGo); err != nil || len(diags) > 0 {
				t.Fatalf("go build: %v %v", err, diags)
			}
			if diags, err := compiler.BuildPathBackend(path, llvmBin, compiler.BackendLLVM); err != nil || len(diags) > 0 {
				t.Fatalf("llvm build: %v %v", err, diags)
			}
			goOut, err := exec.Command(goBin).CombinedOutput()
			if err != nil {
				t.Fatalf("run go: %v: %s", err, goOut)
			}
			llvmOut, err := exec.Command(llvmBin).CombinedOutput()
			if err != nil {
				t.Fatalf("run llvm: %v: %s", err, llvmOut)
			}
			if string(goOut) != string(llvmOut) {
				t.Fatalf("go=%q llvm=%q", goOut, llvmOut)
			}
		})
	}
}

func TestLLVMBackendRunsAsyncMethodAndCaughtFailure(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.slk")
	program := `
class Box {
    Value: int
    function Get() -> int { self.Value }
}

class ChildFailure implements Error { Message: string }

function Fail() -> int throws ChildFailure {
    throw ChildFailure { Message: "caught" }
}

function main() -> string {
    let BoxValue = Box { Value: 42 }
    async let MethodJob = BoxValue.Get()
    async let FailureJob = Fail()
    let MethodValue = await MethodJob
    let FailureValue = await FailureJob catch {
        ChildFailure as Failure => 0
    }
    let Total = MethodValue + FailureValue
    ` + "`${Total}`" + `
}
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "app")
	if diags, err := compiler.BuildPathBackend(src, binary, compiler.BackendLLVM); err != nil || len(diags) > 0 {
		t.Fatalf("llvm build: %v %v", err, diags)
	}
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run llvm: %v: %s", err, output)
	}
	if string(output) != "42\n" {
		t.Fatalf("unexpected llvm output %q", output)
	}
}

func TestLLVMSelectionDoesNotChangeGoDefault(t *testing.T) {
	if got, err := compiler.ParseBackend(""); err != nil || got != compiler.BackendGo {
		t.Fatalf("empty backend: %q %v", got, err)
	}
	if _, err := compiler.ParseBackend("tinygo"); err == nil {
		t.Fatal("accepted unknown backend")
	}
}
