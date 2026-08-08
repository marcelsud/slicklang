package compiler

import (
	"strings"
	"testing"
)

func TestDiagnosticRegistryIsCompleteAndDescribable(t *testing.T) {
	registry, err := buildDiagnosticRegistry(diagnosticDefinitions)
	if err != nil {
		t.Fatalf("validate diagnostic registry: %v", err)
	}
	if len(registry) != len(diagnosticDefinitions) {
		t.Fatalf("registry has %d definitions, want %d", len(registry), len(diagnosticDefinitions))
	}
	for _, definition := range diagnosticDefinitions {
		description, err := DescribeDiagnostic(string(definition.Code))
		if err != nil {
			t.Fatalf("describe %s: %v", definition.Code, err)
		}
		if description.Code != string(definition.Code) || description.Fixes == nil || description.Related == nil {
			t.Fatalf("incomplete exported description for %s: %+v", definition.Code, description)
		}
	}
}

func TestDiagnosticRegistryRejectsInvalidDefinitions(t *testing.T) {
	first := defineDiagnostic(diagnosticCodeSyntax, DiagnosticPhaseParse, "Title", "Explanation", "Trigger", "Fix")
	second := defineDiagnostic(diagnosticCodeNamespace, DiagnosticPhaseParse, "Title", "Explanation", "Trigger", "Fix")

	malformed := first
	malformed.Code = "SLK1"
	incomplete := first
	incomplete.Title = ""
	withoutFix := first
	withoutFix.Fixes = nil
	ansi := first
	ansi.Explanation = "bad\x1b[31mtext"
	missingRelated := first.withRelated("SLK999")
	duplicateRelated := first.withRelated(diagnosticCodeNamespace, diagnosticCodeNamespace)
	selfRelated := first.withRelated(diagnosticCodeSyntax)

	tests := map[string]struct {
		definitions []diagnosticDefinition
		message     string
	}{
		"duplicate":         {[]diagnosticDefinition{first, first}, "duplicate diagnostic code"},
		"malformed":         {[]diagnosticDefinition{malformed}, "invalid diagnostic code"},
		"out of order":      {[]diagnosticDefinition{second, first}, "not ordered"},
		"incomplete":        {[]diagnosticDefinition{incomplete}, "incomplete description"},
		"missing fix":       {[]diagnosticDefinition{withoutFix}, "no repair strategy"},
		"ANSI":              {[]diagnosticDefinition{ansi}, "ANSI escape"},
		"missing related":   {[]diagnosticDefinition{missingRelated}, "unknown code"},
		"duplicate related": {[]diagnosticDefinition{duplicateRelated, second}, "repeats related code"},
		"self related":      {[]diagnosticDefinition{selfRelated}, "relates to itself"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := buildDiagnosticRegistry(test.definitions)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("build registry error = %v, want containing %q", err, test.message)
			}
		})
	}
}

func TestDiagnosticConstructorRejectsUnregisteredCodes(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unregistered diagnostic code did not panic")
		}
	}()
	newDiagnostic(position{}, "SLK999", "message")
}

func TestOptionalReceiverDescriptionExamples(t *testing.T) {
	description, err := DescribeDiagnostic(string(diagnosticCodeOptionalReceiver))
	if err != nil {
		t.Fatal(err)
	}
	if description.InvalidExample == nil || description.ValidExample == nil {
		t.Fatalf("SLK370 examples are absent: %+v", description)
	}
	declaration := "class User { Name: string }\n"
	invalid := declaration + "function main(User: User?) -> string { " + *description.InvalidExample + " }"
	requireDiagnostic(t, Check([]Source{{Name: "invalid.slk", Namespace: "root", Text: invalid}}), string(diagnosticCodeOptionalReceiver), "may be null")

	valid := declaration + "function main(User: User?) -> null {\n" + *description.ValidExample + "\n  null\n}"
	requireNoDiagnostics(t, Check([]Source{{Name: "valid.slk", Namespace: "root", Text: valid}}))
}

func TestDiagnosticDescriptionsAreNotGenerated(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { "ok" }`,
	}})
	requireNoDiagnostics(t, diagnostics)
	generated, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	description, err := DescribeDiagnostic(string(diagnosticCodeOptionalReceiver))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(generated, description.Title) || strings.Contains(generated, description.Explanation) {
		t.Fatal("generated Go contains diagnostic documentation")
	}
}
