package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDescribeDiagnosticHumanOutputContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"SLK370"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Code: SLK370
Kind: diagnostic
Severity: error
Title: Optional value may be null

A member cannot be accessed through an optional value until the value has been proved present.

Phase:
  type-check

Triggered when:
  A field or method is accessed through T?.

Fixes:
  - Compare the value with null and use it inside the present branch.
  - Propagate the absence or provide an explicit fallback.

Invalid:
  User.Name

Valid:
  if (User != null) {
    User.Name
  }

Related:
  - SLK371
  - SLK372
  - SLK374
  - SLK375
`
	if stdout.String() != want {
		t.Fatalf("human output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeDiagnosticJSONOutputContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--json", "SLK370"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `{
  "schema_version": 6,
  "kind": "diagnostic",
  "code": "SLK370",
  "severity": "error",
  "phase": "type-check",
  "title": "Optional value may be null",
  "explanation": "A member cannot be accessed through an optional value until the value has been proved present.",
  "trigger": "A field or method is accessed through T?.",
  "fixes": [
    "Compare the value with null and use it inside the present branch.",
    "Propagate the absence or provide an explicit fallback."
  ],
  "invalid_example": "User.Name",
  "valid_example": "if (User != null) {\n  User.Name\n}",
  "related": [
    "SLK371",
    "SLK372",
    "SLK374",
    "SLK375"
  ]
}
`
	if stdout.String() != want {
		t.Fatalf("JSON output:\n%s\nwant:\n%s", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatal("JSON output contains an ANSI escape")
	}
}

func TestDescribeRepresentativeDiagnosticPhases(t *testing.T) {
	for _, code := range []string{"SLK001", "SLK201", "SLK203", "SLK310", "SLK330", "SLK370"} {
		t.Run(code, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := runDescribe([]string{"--json", code}, &stdout, &stderr)
			if status != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"code": "`+code+`"`) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDescribeUnknownDiagnosticContracts(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		status := runDescribe([]string{"SLK999"}, &stdout, &stderr)
		if status != 1 || stdout.Len() != 0 || stderr.String() != "unknown diagnostic \"SLK999\"\n" {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
	})
	t.Run("JSON", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		status := runDescribe([]string{"--json", "SLK999"}, &stdout, &stderr)
		if status != 1 || stderr.Len() != 0 {
			t.Fatalf("status=%d stderr=%q", status, stderr.String())
		}
		want := `{
  "schema_version": 6,
  "error": {
    "code": "unknown_diagnostic",
    "message": "unknown diagnostic \"SLK999\"",
    "diagnostic": "SLK999"
  }
}
`
		if stdout.String() != want {
			t.Fatalf("JSON output:\n%s\nwant:\n%s", stdout.String(), want)
		}
	})
}

func TestMalformedDiagnosticIdentifiersUseSymbolResolution(t *testing.T) {
	for _, value := range []string{"slk370", "SLK37", "SLK37A"} {
		t.Run(value, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := runDescribe([]string{value}, &stdout, &stderr)
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown symbol") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDescribeDiagnosticRejectsProjectPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"SLK370", "examples/hello"}, &stdout, &stderr)
	if status != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "diagnostic code SLK370 does not accept a project path") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestDescribeDiagnosticNeedsNoProject(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"SLK370"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "Code: SLK370\n") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}
