package compiler_test

import (
	"fmt"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestElseIfInterpreterNativeSemantics(t *testing.T) {
	source := `
class FlowFailure implements Error { Message: string }
class User { Name: string }

function First() -> Result<bool, FlowFailure> { Err(FlowFailure { Message: "first" }) }
function Second() -> Result<bool, FlowFailure> { Err(FlowFailure { Message: "second" }) }

function Ordered() -> Result<string, FlowFailure> {
    if (First()?) {
        Ok("first true")
    } else if (Second()?) {
        Ok("second true")
    } else {
        Ok("fallback")
    }
}

function SkipLaterCondition() -> Result<string, FlowFailure> {
    if (true) {
        Ok("selected")
    } else if (true) {
        let FailedBranch = Second()?
        Ok("wrong")
    } else if (Second()?) {
        Ok("wrong")
    } else {
        Ok("wrong")
    }
}

function ReturnBranch(Value: int) -> string {
    if (Value == 1) {
        return "one"
    } else if (Value == 2) {
        return "two"
    } else {
        "other"
    }
}

function ThrowBranch(Value: int) -> string throws FlowFailure {
    if (Value == 1) {
        "one"
    } else if (Value == 2) {
        throw FlowFailure { Message: "throw-two" }
    } else {
        "other"
    }
}

function LoopControl() -> string {
    let Output = ""
    for Value in [1, 2, 3] {
        if (Value == 1) {
            continue
        } else if (Value == 2) {
            Output = Output + "2"
            break
        } else {
            Output = Output + "wrong"
        }
    }
    Output
}

function Label(Value: User?) -> string {
    if (Value == null) {
        "missing"
    } else if (Value.Name == "admin") {
        "admin"
    } else {
        Value.Name
    }
}

function Compact(Value: int) -> string {
    if (Value > 90) {
        "high"
    } else if (Value > 50) {
        "medium"
    } else {
        "low"
    }
}

function Nested(Value: int) -> string {
    if (Value > 90) {
        "high"
    } else {
        if (Value > 50) {
            "medium"
        } else {
            "low"
        }
    }
}

function NoFinal(Value: int) -> null {
    if (Value == 1) {
        let FirstValue = Value
    } else if (Value == 2) {
        let SecondValue = Value
    }
}

function main() -> string {
    let OrderedMessage = match Ordered() {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
    let Skipped = match SkipLaterCondition() {
        Ok(Value) => Value
        Err(Failure) => Failure.Message
    }
    let Thrown = ThrowBranch(2) catch (Failure) {
        FlowFailure => Failure.Message
    }
    let Done = NoFinal(2)
    OrderedMessage + "|" + Skipped + "|" + ReturnBranch(2) + "|" + LoopControl() + "|" + Label(User { Name: "admin" }) + "|" + Label(null) + "|" + Thrown + "|" + Compact(60) + ":" + Nested(60)
}
`
	if output := runResultEverywhere(t, source); output != "first|selected|two|2|admin|missing|throw-two|medium:medium" {
		t.Fatalf("unexpected else-if output %q", output)
	}
}

func TestElseIfTypeDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name:    "later condition must be bool",
			source:  `function main() -> int { if (false) { 1 } else if (2) { 2 } else { 3 } }`,
			code:    "SLK342",
			message: "if condition must be bool",
		},
		{
			name:    "later condition cannot be optional",
			source:  `function main(Value: bool?) -> int { if (false) { 1 } else if (Value) { 2 } else { 3 } }`,
			code:    "SLK375",
			message: "may be null and is not a condition",
		},
		{
			name:    "later branch joins",
			source:  `function main() -> string { if (false) { "first" } else if (true) { 2 } else { "last" } }`,
			code:    "SLK342",
			message: "if branches must produce one type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, test.source), test.code, test.message)
		})
	}
}

func TestElseIfFormattingAndHighlighting(t *testing.T) {
	input := `function Compact(First:bool,Second:bool,Third:bool)->string{if(First){"first"}else if(Second){"second"}else if(Third){"third"}else{"fallback"}}function Nested(First:bool,Second:bool)->string{if(First){"first"}else{if(Second){"second"}else{"fallback"}}}`
	expected := `function Compact(First: bool, Second: bool, Third: bool) -> string {
    if (First) {
        "first"
    } else if (Second) {
        "second"
    } else if (Third) {
        "third"
    } else {
        "fallback"
    }
}

function Nested(First: bool, Second: bool) -> string {
    if (First) {
        "first"
    } else {
        if (Second) {
            "second"
        } else {
            "fallback"
        }
    }
}
`
	formatted := formatElseIf(t, input)
	if formatted != expected {
		t.Fatalf("formatted else-if source:\n%s", formatted)
	}
	if again := formatElseIf(t, formatted); again != formatted {
		t.Fatal("else-if formatting is not idempotent")
	}

	withComments := `function main(First:bool,Second:bool)->string{if(First){"first"}else /* later branch */ if(/* later condition */Second){"second"}else{"fallback"}}`
	commented := formatElseIf(t, withComments)
	for _, comment := range []string{"/* later branch */", "/* later condition */"} {
		if !strings.Contains(commented, comment) {
			t.Fatalf("formatted source lost %s:\n%s", comment, commented)
		}
	}
	if again := formatElseIf(t, commented); again != commented {
		t.Fatal("commented else-if formatting is not idempotent")
	}

	var reproduced strings.Builder
	keywords := map[string]int{}
	for _, token := range compiler.Highlight(input) {
		reproduced.WriteString(token.Text)
		if token.Class == compiler.ClassKeyword {
			keywords[token.Text]++
		}
	}
	if reproduced.String() != input {
		t.Fatal("else-if highlighting did not preserve source bytes")
	}
	if keywords["else"] != 5 || keywords["if"] != 5 {
		t.Fatalf("conditional keyword classes = %v", keywords)
	}
}

func TestMalformedElseIfReportsOneSyntaxDiagnostic(t *testing.T) {
	tests := map[string]string{
		"missing condition":    `function main() -> string { if (true) { "a" } else if () { "b" } else { "c" } }`,
		"missing parentheses":  `function main() -> string { if (true) { "a" } else if true { "b" } else { "c" } }`,
		"missing branch block": `function main() -> string { if (true) { "a" } else if (true) "b" }`,
		"invalid else target":  `function main() -> string { if (true) { "a" } else "b" }`,
		"malformed later branch": `function main() -> string {
            if (false) { "a" } else if (false) { "b" } else if (true) "c"
        }`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := checkResult(t, source)
			if len(diagnostics) != 1 || diagnostics[0].Code != "SLK001" {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
		})
	}
}

func formatElseIf(t *testing.T, text string) string {
	t.Helper()
	formatted, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: text})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format else-if: diagnostics=%+v err=%v", diagnostics, err)
	}
	return formatted
}

func TestElseIfCheckedEffectsMatchNestedForm(t *testing.T) {
	template := `
class Failure implements Error { Message: string }
function Check() -> bool throws Failure { true }
function main() -> string {
    %s
}
`
	compact := fmt.Sprintf(template, `if (false) { "first" } else if (Check()) { "second" } else { "last" }`)
	nested := fmt.Sprintf(template, `if (false) { "first" } else { if (Check()) { "second" } else { "last" } }`)
	for name, source := range map[string]string{"compact": compact, "nested": nested} {
		t.Run(name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, source), "SLK201", "unhandled Failure")
		})
	}
}
