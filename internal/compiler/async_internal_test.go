package compiler

import (
	"strings"
	"testing"
)

func TestGeneratedTaskRuntimeIsConditional(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantTask bool
	}{
		{
			name:     "ordinary program",
			source:   `function main() -> int { 1 }`,
			wantTask: false,
		},
		{
			name:     "async program",
			source:   `function Load() -> int { 1 } function main() -> int { async let Work = Load() await Work }`,
			wantTask: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			if len(diagnostics) != 0 {
				t.Fatalf("compile diagnostics: %v", diagnostics)
			}
			generated, err := program.generateGo()
			if err != nil {
				t.Fatalf("generate Go: %v", err)
			}
			if got := strings.Contains(generated, "type slickTask["); got != test.wantTask {
				t.Fatalf("generated task runtime = %v, want %v", got, test.wantTask)
			}
		})
	}
}
