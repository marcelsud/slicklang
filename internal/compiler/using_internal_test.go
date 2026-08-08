package compiler

import (
	"strings"
	"testing"
)

func TestUsingRuntimeSupportIsEmittedOnlyWhenNeeded(t *testing.T) {
	withoutUsing, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { "plain" }`,
	}})
	if len(diagnostics) != 0 {
		t.Fatalf("compile plain program: %+v", diagnostics)
	}
	plainGo, err := withoutUsing.generateGo()
	if err != nil {
		t.Fatalf("generate plain program: %v", err)
	}
	if strings.Contains(plainGo, "slickUsing") {
		t.Fatal("non-using program contains cleanup runtime support")
	}

	withUsing, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `
class Resource { function Close() -> null { null } }
function Open() -> Resource { Resource {} }
function main() -> string { using Handle = Open() { "used" } }
`,
	}})
	if len(diagnostics) != 0 {
		t.Fatalf("compile using program: %+v", diagnostics)
	}
	usingGo, err := withUsing.generateGo()
	if err != nil {
		t.Fatalf("generate using program: %v", err)
	}
	if !strings.Contains(usingGo, "func slickUsing[") {
		t.Fatal("using program does not contain cleanup runtime support")
	}
}
