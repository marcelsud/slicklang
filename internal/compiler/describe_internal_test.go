package compiler

import (
	"os"
	"path/filepath"
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

func TestStabilityRegistriesAreExplicit(t *testing.T) {
	if err := validateStabilityRegistries(); err != nil {
		t.Fatal(err)
	}
	for name, record := range standardSymbolRecords(standardLibraryRegistry) {
		if !record.stability.valid() {
			t.Fatalf("%s has invalid stability %q", name, record.stability)
		}
	}
	for _, backend := range Backends() {
		if !backend.Stability.valid() || (backend.Stability == StabilityStable && !backend.Eligible) {
			t.Fatalf("backend %s has invalid stability or stable coverage: %+v", backend.Name, backend)
		}
		for _, target := range backend.Targets {
			if !target.Stability.valid() || (target.Stability == StabilityStable && !target.Eligible) {
				t.Fatalf("backend %s target has invalid stability or stable coverage: %+v", backend.Name, target)
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
		runtimeABI: runtimeABIVersion,
		operations: runtimeOperationTable{},
	})
	defer func() { backendRegistry = original }()

	if err := validateStabilityRegistries(); err == nil || !strings.Contains(err.Error(), "lacks complete stable-backend coverage") {
		t.Fatalf("stable backend coverage error = %v", err)
	}
}

func TestOmittedStabilityIsInvalid(t *testing.T) {
	original := standardLibraryRegistry
	registry := original
	registry.functions = append([]standardFunctionDecl(nil), original.functions...)
	registry.functions[0].stability = ""
	standardLibraryRegistry = registry
	defer func() { standardLibraryRegistry = original }()

	if err := validateStabilityRegistries(); err == nil || !strings.Contains(err.Error(), "invalid stability") {
		t.Fatalf("omitted stability validation error = %v", err)
	}
}

func TestAlphaUseRequiresOptInAndBackendCoverageBeforeEmission(t *testing.T) {
	originalRegistry := standardLibraryRegistry
	registry := originalRegistry
	registry.functions = append([]standardFunctionDecl(nil), originalRegistry.functions...)
	registry.functions[0].stability = StabilityAlpha
	standardLibraryRegistry = registry
	defer func() { standardLibraryRegistry = originalRegistry }()

	source := Source{Name: "main.slk", Namespace: "root", Text: `function main() -> bytes {
    let Convert = std.bytes.FromUtf8
    Convert("ok")
}`}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source.Text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckPath(root); err == nil || !strings.Contains(err.Error(), "--allow-alpha") {
		t.Fatalf("check without alpha opt-in error = %v", err)
	}
	if diagnostics, err := CheckPathWithOptions(root, CheckOptions{AllowAlpha: true}); err != nil || len(diagnostics) != 0 {
		t.Fatalf("check with alpha opt-in = %v, %v", diagnostics, err)
	}

	output := filepath.Join(root, "existing")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSourcesWithOptions([]Source{source}, output, BuildOptions{Backend: BackendGo}); err == nil || !strings.Contains(err.Error(), "--allow-alpha") {
		t.Fatalf("build without alpha opt-in error = %v", err)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "sentinel" {
		t.Fatalf("pre-emission alpha failure changed output: %q, %v", contents, err)
	}

	originalBackends := backendRegistry
	backendRegistry = append(append([]backendRegistration(nil), originalBackends...), backendRegistration{
		name:       "missing-alpha",
		stability:  StabilityAlpha,
		targets:    []backendTargetRegistration{{name: "linux-x64", stability: StabilityAlpha}},
		runtimeABI: runtimeABIVersion,
		operations: runtimeOperationTable{},
	})
	defer func() { backendRegistry = originalBackends }()
	if _, err := BuildSourcesWithOptions([]Source{source}, output, BuildOptions{Backend: "missing-alpha", AllowAlpha: true}); err == nil ||
		!strings.Contains(err.Error(), "std.bytes.FromUtf8") ||
		!strings.Contains(err.Error(), "backend missing-alpha target linux-x64") {
		t.Fatalf("missing alpha backend coverage error = %v", err)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "sentinel" {
		t.Fatalf("pre-emission backend failure changed output: %q, %v", contents, err)
	}
}

func TestAlphaFieldsInObjectsAndTemplatesRequireOptIn(t *testing.T) {
	original := standardLibraryRegistry
	registry := original
	registry.classes = append([]standardClassDecl(nil), original.classes...)
	for index := range registry.classes {
		if registry.classes[index].canonical != stdProcessStatusName {
			continue
		}
		registry.classes[index].fields = append([]standardFieldDecl(nil), registry.classes[index].fields...)
		for fieldIndex := range registry.classes[index].fields {
			if registry.classes[index].fields[fieldIndex].name == "ExitCode" {
				registry.classes[index].fields[fieldIndex].stability = StabilityAlpha
			}
		}
	}
	standardLibraryRegistry = registry
	defer func() { standardLibraryRegistry = original }()

	root := t.TempDir()
	source := `function main() -> string {
    let Status = std.process.Status {
        ExitCode: 0
        Output: std.bytes.FromUtf8("")
        ErrorOutput: std.bytes.FromUtf8("")
    }
    "${Status.ExitCode}"
}`
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckPath(root); err == nil || !strings.Contains(err.Error(), "std.process.Status.ExitCode is alpha") {
		t.Fatalf("alpha field without opt-in error = %v", err)
	}
	if diagnostics, err := CheckPathWithOptions(root, CheckOptions{AllowAlpha: true}); err != nil || len(diagnostics) != 0 {
		t.Fatalf("alpha field with opt-in = %v, %v", diagnostics, err)
	}
}
