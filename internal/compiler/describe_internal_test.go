package compiler

import (
	"slices"
	"strings"
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

func TestStabilityRegistriesAreExplicitAndEligible(t *testing.T) {
	if err := validateStabilityRegistries(); err != nil {
		t.Fatal(err)
	}
	for name, record := range standardSymbolRecords(standardLibraryRegistry) {
		if !record.stability.valid() {
			t.Fatalf("%s has invalid stability %q", name, record.stability)
		}
	}
	for _, backend := range Backends() {
		if backend.Stability != StabilityStable || !backend.Eligible {
			t.Fatalf("backend %s = %+v, want stable and eligible", backend.Name, backend)
		}
		for _, target := range backend.Targets {
			if target.Stability != StabilityStable || !target.Eligible {
				t.Fatalf("backend %s target = %+v, want stable and eligible", backend.Name, target)
			}
		}
	}
}

func TestEligibleAlphaSymbolIsNotPromoted(t *testing.T) {
	original := standardLibraryRegistry
	registry := original
	registry.functions = append([]standardFunctionDecl(nil), original.functions...)
	registry.functions[0].stability = StabilityAlpha
	standardLibraryRegistry = registry
	defer func() { standardLibraryRegistry = original }()

	record := standardSymbolRecords(standardLibraryRegistry)[registry.functions[0].canonical]
	if record.stability != StabilityAlpha || !standardSymbolEligible(record) {
		t.Fatalf("alpha symbol metadata = %+v, want eligible alpha", record)
	}
	if err := validateStandardSymbolAvailability(registry.functions[0].canonical, BackendGo, "linux-x64", false); err == nil || !strings.Contains(err.Error(), "--allow-alpha") {
		t.Fatalf("alpha symbol without opt-in error = %v", err)
	}
	if err := validateStandardSymbolAvailability(registry.functions[0].canonical, BackendGo, "linux-x64", true); err != nil {
		t.Fatalf("eligible alpha symbol with opt-in: %v", err)
	}
}

func TestStableRegistryRejectsAlphaSignatureDependency(t *testing.T) {
	original := standardLibraryRegistry
	registry := original
	registry.functions = append([]standardFunctionDecl(nil), original.functions...)
	registry.classes = append([]standardClassDecl(nil), original.classes...)
	registry.classes[0].stability = StabilityAlpha
	registry.functions[0].result = typeRef{name: registry.classes[0].canonical}
	standardLibraryRegistry = registry
	defer func() { standardLibraryRegistry = original }()

	if err := validateStabilityRegistries(); err == nil || !strings.Contains(err.Error(), "depends on alpha symbol") {
		t.Fatalf("stable signature validation error = %v", err)
	}
}

func TestStableBackendRequiresEveryStableOperation(t *testing.T) {
	original := backendRegistry
	backendRegistry = append(append([]backendRegistration(nil), original...), backendRegistration{
		name:       "missing",
		stability:  StabilityStable,
		targets:    []backendTargetRegistration{{name: "linux-x64", stability: StabilityStable}},
		implements: func(nativeFunction) bool { return false },
	})
	defer func() { backendRegistry = original }()

	if err := validateStabilityRegistries(); err == nil || !strings.Contains(err.Error(), "lacks complete stable-backend coverage") {
		t.Fatalf("stable backend coverage error = %v", err)
	}
}
