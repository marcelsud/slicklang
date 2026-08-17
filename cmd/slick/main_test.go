package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"slick/internal/compiler"
)

func TestParseBuildArgsAcceptsOutputAfterProject(t *testing.T) {
	path, output, backend, err := parseBuildArgs([]string{"examples/hello", "-o", "bin/hello"})
	if err != nil {
		t.Fatalf("parse build arguments: %v", err)
	}
	if path != "examples/hello" || output != "bin/hello" || backend != compiler.BackendGo {
		t.Fatalf("unexpected build arguments: path=%q output=%q backend=%q", path, output, backend)
	}
}

func TestParseBuildArgsRequiresOutput(t *testing.T) {
	if _, _, _, err := parseBuildArgs([]string{"examples/hello"}); err == nil {
		t.Fatal("build arguments accepted a missing output")
	}
}

func TestParseBuildArgsAcceptsLLVMBackend(t *testing.T) {
	_, _, backend, err := parseBuildArgs([]string{"examples/hello", "-o", "bin/hello", "--backend=llvm"})
	if err != nil {
		t.Fatalf("parse llvm backend: %v", err)
	}
	if backend != compiler.BackendLLVM {
		t.Fatalf("expected llvm backend, found %q", backend)
	}
}

func TestParseBuildOptionsAcceptsTarget(t *testing.T) {
	_, _, options, err := parseBuildOptions([]string{"examples/hello", "-o", "bin/hello", "--target=linux-x64"})
	if err != nil {
		t.Fatalf("parse build target: %v", err)
	}
	if options.Target != "linux-x64" {
		t.Fatalf("expected linux-x64 target, found %q", options.Target)
	}
}

func TestRustBuildStatusReportsPinnedToolchainAndTarget(t *testing.T) {
	message := buildSuccessMessage("bin/app", compiler.BuildOptions{Backend: compiler.BackendRust})
	want := "built bin/app (backend rust, target x86_64-unknown-linux-gnu, rust 1.93.1)"
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestBuildAndCheckParseExplicitAlphaOptIn(t *testing.T) {
	_, _, build, err := parseBuildOptions([]string{"examples/hello", "-o", "bin/hello", "--allow-alpha"})
	if err != nil || !build.AllowAlpha {
		t.Fatalf("build alpha options = %+v, %v", build, err)
	}
	path, check, err := parseCheckArgs([]string{"--allow-alpha", "examples/hello"})
	if err != nil || path != "examples/hello" || !check.AllowAlpha {
		t.Fatalf("check alpha options = %q, %+v, %v", path, check, err)
	}
}

// TestRunProgramForwardsArgumentsAndStatus covers the `slick run` seam: the
// project path is the first argument, everything after it belongs to the Slick
// program, and a std.process.Status result becomes exact bytes plus the exit
// code.
func TestRunProgramForwardsArgumentsAndStatus(t *testing.T) {
	root := t.TempDir()
	source := `
function main(Arguments: string[]) -> std.process.Status {
    std.process.Status {
        ExitCode: 4
        Output: std.bytes.FromUtf8(std.text.Join(Arguments, "|"))
        ErrorOutput: std.bytes.FromUtf8("done")
    }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	var stdout, stderr bytes.Buffer
	status := runProgram([]string{root, "first", "", "sec ond"}, &stdout, &stderr)
	if status != 4 {
		t.Fatalf("expected exit 4, found %d (stderr %q)", status, stderr.String())
	}
	if stdout.String() != "first||sec ond" {
		t.Fatalf("unexpected stdout %q", stdout.String())
	}
	if stderr.String() != "done" {
		t.Fatalf("unexpected stderr %q", stderr.String())
	}
}

// TestRunProgramPrintsDisplayResults holds the existing `slick run` behavior for
// mains that return a displayed value.
func TestRunProgramPrintsDisplayResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(`function main() -> string { "hello" }`), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if status := runProgram([]string{root}, &stdout, &stderr); status != 0 {
		t.Fatalf("expected exit 0, found %d (stderr %q)", status, stderr.String())
	}
	if stdout.String() != "hello\n" || stderr.Len() != 0 {
		t.Fatalf("unexpected output %q / %q", stdout.String(), stderr.String())
	}
}

func TestCheckRejectsExtraArguments(t *testing.T) {
	if status := run([]string{"check", "examples/hello", "extra"}); status != 2 {
		t.Fatalf("expected usage exit, found %d", status)
	}
}
