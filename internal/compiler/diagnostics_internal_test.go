package compiler

import (
	"fmt"
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

// TestWarningDescriptionsAreCompleteAndSeparate holds the two warning phases to
// their contract: every lint and quality code is a documented warning with focused
// examples, and no compiler code is one.
func TestWarningDescriptionsAreCompleteAndSeparate(t *testing.T) {
	warnings := map[diagnosticCode]DiagnosticPhase{
		diagnosticCodeUnreadBinding:        DiagnosticPhaseLint,
		diagnosticCodeDiscardedExpression:  DiagnosticPhaseLint,
		diagnosticCodeUnreachableStatement: DiagnosticPhaseLint,
		diagnosticCodeCyclomaticComplexity: DiagnosticPhaseQuality,
		diagnosticCodeCognitiveComplexity:  DiagnosticPhaseQuality,
	}
	for _, definition := range diagnosticDefinitions {
		phase, isWarning := warnings[definition.Code]
		if !isWarning {
			if definition.Severity != DiagnosticSeverityError {
				t.Fatalf("%s is %s, want error", definition.Code, definition.Severity)
			}
			continue
		}
		description, err := DescribeDiagnostic(string(definition.Code))
		if err != nil {
			t.Fatalf("describe %s: %v", definition.Code, err)
		}
		if description.Severity != DiagnosticSeverityWarning || description.Phase != phase {
			t.Fatalf("%s is %s in phase %s, want warning in %s", definition.Code, description.Severity, description.Phase, phase)
		}
		if description.InvalidExample == nil || description.ValidExample == nil || len(description.Fixes) == 0 || description.Trigger == "" {
			t.Fatalf("%s description is incomplete: %+v", definition.Code, description)
		}
	}
}

// TestLintDescriptionExamplesTriggerTheirRule proves each documented invalid
// example reports its own rule and the documented repair clears it.
func TestLintDescriptionExamplesTriggerTheirRule(t *testing.T) {
	tests := map[diagnosticCode]string{
		diagnosticCodeUnreadBinding:        "function Check(Value: int) -> int {\n%s\n}\n",
		diagnosticCodeDiscardedExpression:  "function Check() -> string {\n%s\n}\n",
		diagnosticCodeUnreachableStatement: "function Check() -> int {\n%s\n}\n",
	}
	for code, wrapper := range tests {
		t.Run(string(code), func(t *testing.T) {
			description, err := DescribeDiagnostic(string(code))
			if err != nil {
				t.Fatal(err)
			}
			invalid := Lint([]Source{{Name: "main.slk", Namespace: "root", Text: fmt.Sprintf(wrapper, *description.InvalidExample)}})
			requireDiagnostic(t, invalid, string(code), "")
			valid := Lint([]Source{{Name: "main.slk", Namespace: "root", Text: fmt.Sprintf(wrapper, *description.ValidExample)}})
			requireNoDiagnostics(t, valid)
		})
	}
}

// TestComplexityDescriptionExamplesReduceTheirMetric proves the documented
// repairs are valid Slick that actually scores lower, rather than advice to write
// less.
func TestComplexityDescriptionExamplesReduceTheirMetric(t *testing.T) {
	tests := map[diagnosticCode]string{
		diagnosticCodeCyclomaticComplexity: "function Accepted(Ready: bool, Allowed: bool, Expired: bool, Fresh: bool) -> bool {\n" +
			"    Ready && Allowed && !Expired && Fresh\n}\n" +
			"function Check(Ready: bool, Allowed: bool, Expired: bool, Fresh: bool) -> int {\n%s\n}\n",
		diagnosticCodeCognitiveComplexity: "function Check(A: bool, B: bool) -> int {\n%s\n}\n",
	}
	for code, wrapper := range tests {
		t.Run(string(code), func(t *testing.T) {
			description, err := DescribeDiagnostic(string(code))
			if err != nil {
				t.Fatal(err)
			}
			invalid := describedCheckScore(t, fmt.Sprintf(wrapper, *description.InvalidExample))
			valid := describedCheckScore(t, fmt.Sprintf(wrapper, *description.ValidExample))
			if code == diagnosticCodeCyclomaticComplexity && valid.cyclomatic >= invalid.cyclomatic {
				t.Fatalf("documented repair scored cyclomatic %d, want below %d", valid.cyclomatic, invalid.cyclomatic)
			}
			if code == diagnosticCodeCognitiveComplexity && valid.cognitive >= invalid.cognitive {
				t.Fatalf("documented repair scored cognitive %d, want below %d", valid.cognitive, invalid.cognitive)
			}
		})
	}
}

// describedCheckScore measures root.Check in one documented example.
func describedCheckScore(t *testing.T, text string) complexityScore {
	t.Helper()
	report, err := Quality([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
	if err != nil {
		t.Fatalf("quality analysis failed: %v", err)
	}
	if !report.Compiled {
		t.Fatalf("documented example does not compile: %+v", report.Diagnostics)
	}
	for _, callable := range report.Callables {
		if callable.Symbol == "root.Check" {
			return complexityScore{cyclomatic: callable.CyclomaticComplexity, cognitive: callable.CognitiveComplexity}
		}
	}
	t.Fatalf("no root.Check in %+v", report.Callables)
	return complexityScore{}
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
