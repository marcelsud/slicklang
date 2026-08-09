package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeHumanOutputContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"std.env.Get"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Name: std.env.Get
Kind: function
Visibility: public
Documentation:
Returns the environment value for Name, or null when Name is unset.

Parameters:
  Name: string
Returns: string?
Throws: none
Native: true
`
	if stdout.String() != want {
		t.Fatalf("human output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeJSONOutputContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--json", "std.env.Get"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `{
  "schema_version": 4,
  "symbol": {
    "canonical_name": "std.env.Get",
    "kind": "function",
    "visibility": "public",
    "documentation": "Returns the environment value for Name, or null when Name is unset.",
    "type": "",
    "type_parameters": [],
    "parameters": [
      {
        "name": "Name",
        "type": "string"
      }
    ],
    "return_type": "string?",
    "throws": [],
    "native": true,
    "fields": [],
    "declared_methods": [],
    "implemented_methods": [],
    "interfaces": [],
    "children": [],
    "source": null
  }
}
`
	if stdout.String() != want {
		t.Fatalf("JSON output:\n%s\nwant:\n%s", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatal("JSON output contains an ANSI escape")
	}
}

func TestDescribeUnknownJSONErrorContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--json", "std.env.Missing"}, &stdout, &stderr)
	if status != 1 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `{
  "schema_version": 4,
  "error": {
    "code": "unknown_symbol",
    "message": "unknown symbol \"std.env.Missing\"",
    "symbol": "std.env.Missing"
  }
}
`
	if stdout.String() != want {
		t.Fatalf("unknown JSON output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeUsageErrorsStayOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--json"}, &stdout, &stderr)
	if status != 2 || stdout.Len() != 0 {
		t.Fatalf("status=%d stdout=%q", status, stdout.String())
	}
	want := "describe requires a symbol or diagnostic code\n" +
		"usage: slick <check|run> [path]\n" +
		"       slick build [path] -o <output>\n" +
		"       slick describe [--json] [--budget <lines>] <symbol|diagnostic-code> [path]\n" +
		"       slick fmt [--check] [path]\n"
	if stderr.String() != want {
		t.Fatalf("usage stderr=%q, want %q", stderr.String(), want)
	}
}

func TestDescribeFlagFollowsBuildPositionConvention(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--budget", "30", "std.env.Get"},
		{"std.env.Get", "--budget", "30", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		if status := runDescribe(args, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
			t.Fatalf("args=%q status=%d stderr=%q", args, status, stderr.String())
		}
	}
}

func TestDescribeStdIOReaderContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"std.io.Reader"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Name: std.io.Reader
Kind: interface
Visibility: public
Documentation:
Reads bounded immutable byte chunks and supports deterministic cleanup.

Declared methods:
  public std.io.Reader.Close() -> null throws std.io.Failure — Closes the reader or throws Failure when cleanup fails.
  public std.io.Reader.Read(MaxBytes: int) -> Result<bytes?,std.io.Failure> — Reads at most MaxBytes and returns null only at end-of-stream.
`
	if stdout.String() != want {
		t.Fatalf("std.io.Reader output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeHumanBudgetContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--budget", "6", "std"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Name: std
Kind: namespace
Visibility: public
Documentation:
Provides compiler-owned portable standard-library components.

Children:
  … 14 more entries (re-run with a higher ` + "`--budget`" + `; use ` + "`--budget 21`" + ` for full output)
`
	if stdout.String() != want {
		t.Fatalf("budgeted human output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeJSONBudgetContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--json", "std", "--budget", "39"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `{
  "schema_version": 4,
  "budget": {
    "unit": "lines",
    "limit": 39,
    "required": 106,
    "truncated": true,
    "omitted": [
      {
        "section": "children",
        "count": 14
      }
    ]
  },
  "symbol": {
    "canonical_name": "std",
    "kind": "namespace",
    "visibility": "public",
    "documentation": "Provides compiler-owned portable standard-library components.",
    "type": "",
    "type_parameters": [],
    "parameters": [],
    "return_type": "",
    "throws": [],
    "native": false,
    "fields": [],
    "declared_methods": [],
    "implemented_methods": [],
    "interfaces": [],
    "children": [],
    "source": null
  }
}
`
	if stdout.String() != want {
		t.Fatalf("budgeted JSON output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestDescribeRejectsInvalidBudgets(t *testing.T) {
	for _, args := range [][]string{
		{"--budget", "std.env.Get"},
		{"--budget", "-1", "std.env.Get"},
		{"--budget", "1", "--budget", "2", "std.env.Get"},
	} {

		var stdout, stderr bytes.Buffer
		if status := runDescribe(args, &stdout, &stderr); status != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args=%q status=%d stdout=%q stderr=%q", args, status, stdout.String(), stderr.String())
		}
	}
}
func TestDescribeUserDocumentationAndExplicitNull(t *testing.T) {
	root := t.TempDir()
	source := `/// Complete user documentation.
class Documented {
    /// Public value summary.
    Value: string
    /// Reads the stored value.
    function Read() -> string { self.Value }
}
function Undocumented() -> null { null }
`
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatalf("write documented project: %v", err)
	}

	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"root.Documented", root}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Name: root.Documented
Kind: class
Visibility: public
Documentation:
Complete user documentation.

Fields:
  public Value: string @ main.slk:4:5 — Public value summary.
Declared methods:
  public root.Documented.Read() -> string @ main.slk:6:14 — Reads the stored value.
Implemented methods:
  public root.Documented.Read() -> string @ main.slk:6:14 — Reads the stored value.
Interfaces: none
Source: main.slk:2:7
`
	if stdout.String() != want {
		t.Fatalf("documented human output:\n%s\nwant:\n%s", stdout.String(), want)
	}

	stdout.Reset()
	status = runDescribe([]string{"--json", "root.Undocumented", root}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"documentation": null`) {
		t.Fatalf("undocumented JSON has no explicit null:\n%s", stdout.String())
	}
}
