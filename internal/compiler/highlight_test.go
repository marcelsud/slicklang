package compiler_test

import (
	"strings"
	"testing"

	"slick/internal/compiler"
)

// TestHighlightReproducesSource is the invariant a renderer depends on:
// concatenating every token reproduces the file byte for byte, so highlighting
// can never drop, duplicate, or reorder code.
func TestHighlightReproducesSource(t *testing.T) {
	for project := range exampleOutputs {
		t.Run(project, func(t *testing.T) {
			sources, err := compiler.LoadSources(examplePath(project))
			if err != nil {
				t.Fatalf("load example: %v", err)
			}
			for _, source := range sources {
				var rebuilt strings.Builder
				for _, token := range compiler.Highlight(source.Text) {
					rebuilt.WriteString(token.Text)
				}
				if rebuilt.String() != source.Text {
					t.Fatalf("%s did not round-trip through Highlight", source.Name)
				}
			}
		})
	}
}

func TestHighlightClassifiesSlickTokens(t *testing.T) {
	source := "// note\n" +
		"function load(Count: int) -> Result<string, Failure> throws Failure {\n" +
		"    match self.read()? { Ok(Text) => `${Text}` Err(_) => \"x\" }\n" +
		"}\n"

	classes := make(map[string]compiler.TokenClass)
	for _, token := range compiler.Highlight(source) {
		if token.Class != compiler.ClassPlain {
			classes[token.Text] = token.Class
		}
	}

	expected := map[string]compiler.TokenClass{
		"// note":   compiler.ClassComment,
		"function":  compiler.ClassKeyword,
		"match":     compiler.ClassKeyword,
		"throws":    compiler.ClassKeyword,
		"self":      compiler.ClassKeyword,
		"int":       compiler.ClassType,
		"string":    compiler.ClassType,
		"Result":    compiler.ClassType,
		"Ok":        compiler.ClassConstructor,
		"Err":       compiler.ClassConstructor,
		"Failure":   compiler.ClassIdent,
		"Count":     compiler.ClassIdent,
		"`${Text}`": compiler.ClassTemplate,
		`"x"`:       compiler.ClassString,
		"?":         compiler.ClassPunct,
	}
	for text, want := range expected {
		if got := classes[text]; got != want {
			t.Errorf("%q classified as %q, expected %q", text, got, want)
		}
	}
}

// TestHighlightSurvivesUnlexableSource keeps the viewer usable while a file is
// mid-edit: a scanner error must not lose the rest of the text.
func TestHighlightSurvivesUnlexableSource(t *testing.T) {
	source := "function main() -> string { \"unterminated\n}\n"
	var rebuilt strings.Builder
	for _, token := range compiler.Highlight(source) {
		rebuilt.WriteString(token.Text)
	}
	if rebuilt.String() != source {
		t.Fatalf("unlexable source did not round-trip: %q", rebuilt.String())
	}
}
