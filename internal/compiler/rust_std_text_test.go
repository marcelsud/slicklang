package compiler

import (
	"os/exec"
	"testing"
)

// TestRustStdTextMatchesInterpreter pins the std.text family, including the
// deterministic quoting table that must escape exactly the scalars Go escapes.
func TestRustStdTextMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `
function main() -> string {
    let Parts = std.text.Split("a,b,,c", ",")
    let Joined = std.text.Join(Parts, "-")
    let Characters = std.text.Split("a` + "\u00f1" + `b", "")
    let CharacterCount = Characters.Length()
    let Trimmed = std.text.Trim("  padded` + "\t" + `")
    let Replaced = std.text.ReplaceAll("banana", "an", "AN")
    let Inserted = std.text.ReplaceAll("ab", "", ".")
    let Contains = std.text.Contains("slick", "ick")
    let Starts = std.text.StartsWith("slick", "sl")
    let Ends = std.text.EndsWith("slick", "ck")
    let Cut = std.text.Cut("key=value", "=")
    let Missing = std.text.Cut("key", "=")
    let Found = if (Cut == null) { "none" } else { "found" }
    let Absent = if (Missing == null) { "none" } else { "found" }
    let QuotedTab = std.text.Quote("t` + "\t" + `z")
    let QuotedPrivate = std.text.Quote("a` + "\ue000" + `b")
    let QuotedUnassigned = std.text.Quote("x` + "\u0378" + `y")
    let QuotedEmoji = std.text.Quote("ok ` + "\U0001f600" + `")
    let QuotedQuote = std.text.Quote("say \"hi\"")
    let Text = ` + "`${Joined};${CharacterCount};${Trimmed};${Replaced};${Inserted};${Contains};${Starts};${Ends};${Found};${Absent};${QuotedTab};${QuotedPrivate};${QuotedUnassigned};${QuotedEmoji};${QuotedQuote}`" + `
    Text
}
`}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	want := "a-b--c;3;padded;bANANa;.a.b.;true;true;true;found;none;" +
		`"t\tz";"a\ue000b";"x\u0378y";"ok 😀";"say \"hi\""`
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Rust text output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
}
