package compiler

import (
	"go/format"
	"net/http"
	"strings"
	"testing"
)

func TestStdHTTPSyntheticDeclarations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.Failure as HTTPFailure
function Load(Request: HTTPRequest) -> Result<std.http.Response, HTTPFailure> { Fetch(Request) }
function main() -> string { "ok" }
`,
	}})
	requireNoDiagnostics(t, diagnostics)

	assertStandardFunction(t, program.functions[string(nativeStdHTTPFetch)], "std.http", "Fetch", []string{stdHTTPRequestName}, "Result<std.http.Response,std.http.Failure>", nativeStdHTTPFetch)
	assertStandardFunction(t, program.functions[string(nativeStdHTTPHeaderValues)], "std.http", "HeaderValues", []string{"Map<string,string[]>", "string"}, "string[]", nativeStdHTTPHeaderValues)
	assertStandardFunction(t, program.functions[string(nativeStdHTTPStatusText)], "std.http", "StatusText", []string{"int"}, "string?", nativeStdHTTPStatusText)

	expected := map[string]map[string]string{
		stdHTTPRequestName: {
			"Method": "string", "URL": "string", "Headers": "Map<string,string[]>?", "Body": "bytes?",
			"TimeoutMilliseconds": "int?", "MaxResponseBytes": "int?", "FollowRedirects": "bool?",
		},
		stdHTTPResponseName: {"Status": "int", "URL": "string", "Headers": "Map<string,string[]>", "Body": "bytes"},
		stdHTTPFailureName:  {"Kind": "string", "URL": "string", "Status": "int?", "Message": "string"},
	}
	for className, fields := range expected {
		class := program.classes[className]
		if class == nil {
			t.Fatalf("%s was not registered", className)
		}
		for fieldName, typ := range fields {
			field, present := class.fields[fieldName]
			if !present || field.typ.name != typ {
				t.Fatalf("%s.%s = %+v, want %s", className, fieldName, field, typ)
			}
		}
	}
	if !program.classes[stdHTTPFailureName].isError {
		t.Fatal("std.http.Failure must implement Error")
	}
}

func TestStdHTTPGeneratedSupportIsDeterministicAndConditional(t *testing.T) {
	withoutHTTP, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> string { "ok" }`}})
	requireNoDiagnostics(t, diagnostics)
	plain, err := withoutHTTP.generateGo()
	if err != nil {
		t.Fatalf("generate plain Go: %v", err)
	}
	for _, forbidden := range []string{`"net/http"`, "slickHTTPTransport", goClassName(stdHTTPRequestName)} {
		if strings.Contains(plain, forbidden) {
			t.Fatalf("plain program contains HTTP support %q", forbidden)
		}
	}

	withHTTP, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: `
function main() -> string {
    let Request = std.http.Request {
        Method: "GET"
        URL: "http://127.0.0.1:1"
        TimeoutMilliseconds: 1
    }
    let Result = std.http.Fetch(Request)
    match Result {
        Ok(Response) => std.convert.IntToString(Response.Status)
        Err(Failure) => Failure.Kind
    }
}
`}})
	requireNoDiagnostics(t, diagnostics)
	first, err := withHTTP.generateGo()
	if err != nil {
		t.Fatalf("generate HTTP Go: %v", err)
	}
	second, err := withHTTP.generateGo()
	if err != nil {
		t.Fatalf("generate HTTP Go again: %v", err)
	}
	if first != second {
		t.Fatal("generated HTTP source is not deterministic")
	}
	if _, err := format.Source([]byte(first)); err != nil {
		t.Fatalf("generated HTTP source is not formattable: %v", err)
	}
	for _, required := range []string{`"net/http"`, "slickHTTPTransport", goClassName(stdHTTPRequestName)} {
		if !strings.Contains(first, required) {
			t.Fatalf("HTTP program is missing support %q", required)
		}
	}
	if strings.Count(first, "var slickHTTPTransport") != 1 {
		t.Fatal("generated HTTP transport support was duplicated")
	}
}

func TestStdHTTPResponseHeadersAreCanonicalAndSorted(t *testing.T) {
	headers := http.Header{
		"x-zed":      {"last"},
		"Set-Cookie": {"a=1", "b=2"},
		"X-alpha":    {"first"},
		"x-ALPHA":    {"second"},
	}
	converted := responseHTTPHeaders(headers)
	if len(converted) != 3 {
		t.Fatalf("converted headers = %+v", converted)
	}
	if converted[0].name != "Set-Cookie" || converted[1].name != "X-Alpha" || converted[2].name != "X-Zed" {
		t.Fatalf("header order = %+v", converted)
	}
	if got := strings.Join(converted[1].values, ","); got != "first,second" {
		t.Fatalf("merged values = %q", got)
	}
	if got := strings.Join(converted[0].values, ","); got != "a=1,b=2" {
		t.Fatalf("Set-Cookie values = %q", got)
	}
}
