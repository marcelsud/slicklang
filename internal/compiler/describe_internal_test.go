package compiler

import (
	"slices"
	"testing"
)

func TestEveryCoreTypeRegistryEntryIsDescribable(t *testing.T) {
	program, diagnostics := compile(nil)
	if len(diagnostics) != 0 {
		t.Fatalf("compile core registry: %+v", diagnostics)
	}
	for name, declaration := range coreTypeRegistry {
		t.Run(name, func(t *testing.T) {
			description, ok := program.describeSymbol(name)
			if !ok {
				t.Fatalf("registered core type %s is not describable", name)
			}
			if description.CanonicalName != name || description.Kind != declaration.kind {
				t.Fatalf("describe %s = %+v", name, description)
			}
			if !slices.Equal(description.TypeParameters, declaration.typeParams) {
				t.Fatalf("describe %s type parameters = %+v, want %+v", name, description.TypeParameters, declaration.typeParams)
			}
		})
	}
}

func TestEveryStandardLibraryRegistryEntryIsDescribable(t *testing.T) {
	program, diagnostics := compile(nil)
	if len(diagnostics) != 0 {
		t.Fatalf("compile standard library registry: %+v", diagnostics)
	}
	for _, declaration := range standardLibraryRegistry.functions {
		description, ok := program.describeSymbol(declaration.canonical)
		if !ok || description.Kind != "function" {
			t.Fatalf("registered function %s is not describable: %+v", declaration.canonical, description)
		}
	}
	for _, declaration := range standardLibraryRegistry.classes {
		description, ok := program.describeSymbol(declaration.canonical)
		if !ok || description.Kind != "class" {
			t.Fatalf("registered class %s is not describable: %+v", declaration.canonical, description)
		}
	}
}
