package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestFormatExpandsCompactSyntaxCanonically(t *testing.T) {
	source := compiler.Source{Name: "main.slk", Namespace: "root", Text: `use root.models.User as User;class Box{Value:string,function Get()->string{self.Value}}function main()->string{let Values=["a","b"];for Index,Value in enumerate(Values){if(Index==0){continue}else{Value}}return "done"}`}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format compact source: diagnostics=%+v err=%v", diagnostics, err)
	}
	const want = `use root.models.User as User

class Box {
    Value: string
    function Get() -> string {
        self.Value
    }
}

function main() -> string {
    let Values = ["a", "b"]
    for Index, Value in enumerate(Values) {
        if (Index == 0) {
            continue
        } else {
            Value
        }
    }
    return "done"
}
`
	if formatted != want {
		t.Fatalf("formatted source:\n%s\nwant:\n%s", formatted, want)
	}

	source.Text = formatted
	second, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 || second != formatted {
		t.Fatalf("second format changed output: diagnostics=%+v err=%v\n%s", diagnostics, err, second)
	}
}

func TestFormatDistinguishesArrayTypesFromArrayValues(t *testing.T) {
	source := compiler.Source{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "// file comment\r\n\r\nfunction values()->string [][]{return[[\"x\"]]}",
	}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format arrays: diagnostics=%+v err=%v", diagnostics, err)
	}
	const want = `// file comment

function values() -> string[][] {
    return [["x"]]
}
`
	if formatted != want {
		t.Fatalf("formatted arrays=%q, want %q", formatted, want)
	}
}

func TestFormatPreservesNestedAndEndOfLineCommentsExactlyOnce(t *testing.T) {
	const input = `// declaration
function main()->string{/* block */let Values=[// array
"a", // first
"b"] // binding
if(Values==Values){ // then
// standalone
Values.At(0)}else{"none"}} // function
`
	source := compiler.Source{Name: "main.slk", Namespace: "root", Text: input}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("format comments: diagnostics=%+v err=%v", diagnostics, err)
	}
	comments := []string{
		"// declaration",
		"/* block */",
		"// array",
		"// first",
		"// binding",
		"// then",
		"// standalone",
		"// function",
	}
	last := -1
	for _, comment := range comments {
		if count := strings.Count(formatted, comment); count != 1 {
			t.Fatalf("comment %q appears %d times in:\n%s", comment, count, formatted)
		}
		index := strings.Index(formatted, comment)
		if index <= last {
			t.Fatalf("comment %q was reordered in:\n%s", comment, formatted)
		}
		last = index
	}
	if strings.Contains(formatted, "\r") || !strings.HasSuffix(formatted, "\n") || strings.HasSuffix(formatted, "\n\n") {
		t.Fatalf("formatter did not produce one LF final newline: %q", formatted)
	}
}

func TestFormatCanonicalizesEmptyFilesAndBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "file", input: "", want: "\n"},
		{
			name:  "blocks",
			input: `class Empty{}function noop()->null{}`,
			want:  "class Empty {}\n\nfunction noop() -> null {}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			formatted, diagnostics, err := compiler.Format(compiler.Source{
				Name:      "main.slk",
				Namespace: "root",
				Text:      test.input,
			})
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("format empty syntax: diagnostics=%+v err=%v", diagnostics, err)
			}
			if formatted != test.want {
				t.Fatalf("formatted empty syntax=%q, want %q", formatted, test.want)
			}
		})
	}
}

func TestFormatRejectsInvalidSource(t *testing.T) {
	formatted, diagnostics, err := compiler.Format(compiler.Source{
		Name:      "broken.slk",
		Namespace: "root",
		Text:      `function main() -> string {`,
	})
	if err != nil {
		t.Fatalf("format invalid source: %v", err)
	}
	if formatted != "" || len(diagnostics) == 0 || diagnostics[0].File != "broken.slk" {
		t.Fatalf("invalid source result: formatted=%q diagnostics=%+v", formatted, diagnostics)
	}
}

func TestEveryExampleFormatsToAValidFixedPoint(t *testing.T) {
	projects, err := os.ReadDir("../../examples")
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		t.Run(project.Name(), func(t *testing.T) {
			sources, loadErr := compiler.LoadSources(filepath.Join("../../examples", project.Name()))
			if loadErr != nil {
				t.Fatalf("load example: %v", loadErr)
			}
			formattedSources := make([]compiler.Source, 0, len(sources))
			for _, source := range sources {
				formatted, diagnostics, formatErr := compiler.Format(source)
				if formatErr != nil || len(diagnostics) != 0 {
					t.Fatalf("format %s: diagnostics=%+v err=%v", source.Name, diagnostics, formatErr)
				}
				source.Text = formatted
				formattedSources = append(formattedSources, source)
			}
			if diagnostics := compiler.Check(formattedSources); len(diagnostics) != 0 {
				t.Fatalf("formatted example no longer checks: %+v", diagnostics)
			}
		})
	}
}
