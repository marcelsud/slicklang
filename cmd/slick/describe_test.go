package main

import (
	"bytes"
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
  "schema_version": 2,
  "symbol": {
    "canonical_name": "std.env.Get",
    "kind": "function",
    "visibility": "public",
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
  "schema_version": 2,
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
	want := "describe requires a symbol\n" +
		"usage: slick <check|run> [path]\n" +
		"       slick build [path] -o <output>\n" +
		"       slick describe [--json] [--budget <lines>] <symbol> [path]\n"
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

func TestDescribeHumanBudgetContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runDescribe([]string{"--budget", "6", "std"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	want := `Name: std
Kind: namespace
Visibility: public
Children:
  namespace std.bytes (public)
  … 3 more entries (re-run with a higher ` + "`--budget`" + `; use ` + "`--budget 8`" + ` for full output)
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
  "schema_version": 2,
  "budget": {
    "unit": "lines",
    "limit": 39,
    "required": 40,
    "truncated": true,
    "omitted": [
      {
        "section": "children",
        "count": 3
      }
    ]
  },
  "symbol": {
    "canonical_name": "std",
    "kind": "namespace",
    "visibility": "public",
    "type_parameters": [],
    "parameters": [],
    "return_type": "",
    "throws": [],
    "native": false,
    "fields": [],
    "declared_methods": [],
    "implemented_methods": [],
    "interfaces": [],
    "children": [
      {
        "canonical_name": "std.bytes",
        "kind": "namespace",
        "visibility": "public"
      }
    ],
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
