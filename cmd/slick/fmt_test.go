package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFmtCheckAndWriteContracts(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a.slk")
	second := filepath.Join(root, "b.slk")
	firstInput := `function first()->string{"first"}`
	if err := os.WriteFile(first, []byte(firstInput), 0o751); err != nil {
		t.Fatalf("write first source: %v", err)
	}
	if err := os.WriteFile(second, []byte(`function second()->string{"second"}`), 0o644); err != nil {
		t.Fatalf("write second source: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if status := runFmt([]string{root, "--check"}, &stdout, &stderr); status != 1 || stderr.Len() != 0 {
		t.Fatalf("initial check status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	wantPaths := first + "\n" + second + "\n"
	if stdout.String() != wantPaths {
		t.Fatalf("check paths=%q, want %q", stdout.String(), wantPaths)
	}
	if got := readTestFile(t, first); got != firstInput {
		t.Fatalf("check changed first source to %q", got)
	}

	stdout.Reset()
	if status := runFmt([]string{root}, &stdout, &stderr); status != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("format status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	const firstWant = "function first() -> string {\n    \"first\"\n}\n"
	if got := readTestFile(t, first); got != firstWant {
		t.Fatalf("formatted first source=%q, want %q", got, firstWant)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat formatted source: %v", err)
	}
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("formatted source mode=%#o, want 0751", info.Mode().Perm())
	}

	stdout.Reset()
	if status := runFmt([]string{"--check", root}, &stdout, &stderr); status != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("final check status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestFmtDirectoryParseFailureMakesNoChanges(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "a.slk")
	invalid := filepath.Join(root, "b.slk")
	validInput := `function first()->string{"first"}`
	invalidInput := "function broken() -> string { \"unterminated\n"
	if err := os.WriteFile(valid, []byte(validInput), 0o644); err != nil {
		t.Fatalf("write valid source: %v", err)
	}
	if err := os.WriteFile(invalid, []byte(invalidInput), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if status := runFmt([]string{root}, &stdout, &stderr); status != 1 {
		t.Fatalf("invalid directory status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "b.slk:1:") || !strings.Contains(stderr.String(), "literal not terminated") {
		t.Fatalf("invalid directory stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if got := readTestFile(t, valid); got != validInput {
		t.Fatalf("valid source changed despite directory failure: %q", got)
	}
	if got := readTestFile(t, invalid); got != invalidInput {
		t.Fatalf("invalid source changed: %q", got)
	}
}

func TestFmtUsageErrorsStayOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if status := runFmt([]string{"--unknown"}, &stdout, &stderr); status != 2 || stdout.Len() != 0 {
		t.Fatalf("usage status=%d stdout=%q", status, stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "unknown fmt flag") || !strings.Contains(stderr.String(), "slick fmt [--check] [path]") {
		t.Fatalf("usage stderr=%q", stderr.String())
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
