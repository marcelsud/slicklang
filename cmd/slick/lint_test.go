package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintPrintsWarningsAndExitsOne(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": `function main() -> int {
    let NeverRead = 1
    "discarded"
    2
}
`})
	var stdout, stderr bytes.Buffer
	if status := runLint([]string{root}, &stdout, &stderr); status != 1 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	const want = `main.slk:2:5: warning[SLK500]: binding NeverRead is never read
main.slk:3:5: warning[SLK501]: pure expression result is discarded
`
	if stdout.String() != want {
		t.Fatalf("lint stdout:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestLintPrintsOkForCleanProject(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> int {\n    1\n}\n"})
	var stdout, stderr bytes.Buffer
	if status := runLint([]string{root}, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("lint stdout=%q", stdout.String())
	}
}

// TestLintPrintsOnlyCompilerErrors proves an invalid program never produces lint
// output beside its errors.
func TestLintPrintsOnlyCompilerErrors(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> string {\n    let NeverRead = 1\n    42\n}\n"})
	var stdout, stderr bytes.Buffer
	if status := runLint([]string{root}, &stdout, &stderr); status != 1 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	const want = "main.slk:1:10: error[SLK340]: root.main returns string, but its body produces int\n"
	if stdout.String() != want {
		t.Fatalf("lint stdout:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestLintArgumentContracts(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": "function main() -> int {\n    1\n}\n"})
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	for name, args := range map[string][]string{
		"default path":  {},
		"explicit path": {"."},
		"file path":     {"main.slk"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := runLint(args, &stdout, &stderr); status != 0 || stdout.String() != "ok\n" || stderr.Len() != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}

	for name, test := range map[string]struct {
		args    []string
		message string
	}{
		"duplicate path": {[]string{".", "."}, `unexpected lint argument "."`},
		"unknown flag":   {[]string{"--check"}, `unknown lint flag "--check"`},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := runLint(test.args, &stdout, &stderr); status != 2 || stdout.Len() != 0 {
				t.Fatalf("status=%d stdout=%q", status, stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), test.message) || !strings.Contains(stderr.String(), "slick lint [path]") {
				t.Fatalf("stderr=%q, want %q and usage", stderr.String(), test.message)
			}
		})
	}
}

func TestLintReportsLoadFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if status := runLint([]string{t.TempDir()}, &stdout, &stderr); status != 2 || stdout.Len() != 0 {
		t.Fatalf("empty directory status=%d stdout=%q", status, stdout.String())
	}
	if !strings.Contains(stderr.String(), "no .slk files found") {
		t.Fatalf("stderr=%q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if status := runLint([]string{filepath.Join(t.TempDir(), "absent")}, &stdout, &stderr); status != 2 {
		t.Fatalf("missing path status=%d", status)
	}
	if !strings.HasPrefix(stderr.String(), "lint: ") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
