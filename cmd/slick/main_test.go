package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseBuildArgsAcceptsOutputAfterProject(t *testing.T) {
	path, output, err := parseBuildArgs([]string{"examples/hello", "-o", "bin/hello"})
	if err != nil {
		t.Fatalf("parse build arguments: %v", err)
	}
	if path != "examples/hello" || output != "bin/hello" {
		t.Fatalf("unexpected build arguments: path=%q output=%q", path, output)
	}
}

func TestParseBuildArgsRequiresOutput(t *testing.T) {
	if _, _, err := parseBuildArgs([]string{"examples/hello"}); err == nil {
		t.Fatal("build arguments accepted a missing output")
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
