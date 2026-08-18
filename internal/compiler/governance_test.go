package compiler

import (
	"bytes"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGovernanceBuildKnobsAreBackendTargetAndAlphaOnly(t *testing.T) {
	typ := reflect.TypeOf(BuildOptions{})
	want := []struct {
		name string
		typ  reflect.Type
	}{
		{"Backend", reflect.TypeOf(Backend(""))},
		{"Target", reflect.TypeOf("")},
		{"AllowAlpha", reflect.TypeOf(false)},
	}
	if typ.NumField() != len(want) {
		t.Fatalf("BuildOptions has %d fields, want exactly Backend, Target, AllowAlpha", typ.NumField())
	}
	for index, field := range want {
		got := typ.Field(index)
		if got.Name != field.name || got.Type != field.typ || !got.IsExported() {
			t.Fatalf("BuildOptions field %d = %s %s, want %s %s", index, got.Name, got.Type, field.name, field.typ)
		}
	}

	fset := gotoken.NewFileSet()
	pkgs, err := goparser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for filename, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				switch decl := node.(type) {
				case *ast.FuncDecl:
					if decl.Name.IsExported() {
						assertNoSelectorParams(t, filename, decl.Name.Name, decl.Type.Params)
					}
				case *ast.GenDecl:
					for _, spec := range decl.Specs {
						typeSpec, ok := spec.(*ast.TypeSpec)
						if !ok || !typeSpec.Name.IsExported() {
							continue
						}
						structType, ok := typeSpec.Type.(*ast.StructType)
						if !ok || structType.Fields == nil {
							continue
						}
						for _, field := range structType.Fields.List {
							for _, name := range field.Names {
								if name.IsExported() && governanceSelectorName(name.Name) {
									t.Fatalf("%s: exported field %s.%s selects a provider/adapter/implementation", filename, typeSpec.Name.Name, name.Name)
								}
							}
						}
					}
				}
				return true
			})
		}
	}

	manifest, err := os.ReadFile("packages.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`json:"provider"`, `json:"provider,omitempty"`, `json:"implementation"`, `json:"implementation,omitempty"`} {
		if bytes.Contains(manifest, []byte(key)) {
			t.Fatalf("packages.go manifest literal selects a std provider via %s", key)
		}
	}

	records := standardSymbolRecords(standardLibraryRegistry)
	if _, ok := records["std"]; !ok {
		t.Fatal("standardLibraryRegistry has no std namespace root")
	}
	for name := range records {
		if name != "std" && !strings.HasPrefix(name, "std.") {
			t.Fatalf("public declaration %s is not under the std namespace root", name)
		}
	}
}

func TestGovernanceNoEngineFallback(t *testing.T) {
	t.Run("missingAdapter", func(t *testing.T) {
		root := t.TempDir()
		dependency := writePackageFixture(t, filepath.Join(root, "pkg"), "acme.onlygo", "1.0.0",
			`function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		app := filepath.Join(root, "app")
		writeProjectFixture(t, app, "use acme.onlygo.Value\nfunction main() -> int { Value() }\n", []packageDependency{dependency})
		options := BuildOptions{Backend: BackendRust, AllowAlpha: true}
		assertGovernanceFailure(t, app, filepath.Join(t.TempDir(), "missing"), options,
			"backend rust", "target "+rustTargetTriple, "acme.onlygo@1.0.0")
		assertGovernancePreservesOutput(t, app, options,
			"backend rust", "target "+rustTargetTriple, "acme.onlygo@1.0.0")
	})

	t.Run("unregisteredTarget", func(t *testing.T) {
		dir := t.TempDir()
		source := filepath.Join(dir, "main.slk")
		if err := os.WriteFile(source, []byte("function main() -> int { 1 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		options := BuildOptions{Backend: BackendGo, Target: "unregistered-target"}
		assertGovernanceFailure(t, source, filepath.Join(t.TempDir(), "missing"), options,
			"backend go", `target "unregistered-target"`)
		assertGovernancePreservesOutput(t, source, options,
			"backend go", `target "unregistered-target"`)
	})
}

func TestGovernanceArtifactsCarryNoHostManifests(t *testing.T) {
	examples := filepath.Join("..", "..", "examples")
	err := filepath.WalkDir(examples, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if governanceHostManifest(entry.Name()) {
			t.Fatalf("committed example host manifest %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	example := filepath.Join(examples, "hello")
	for _, backend := range Backends() {
		if backend.Stability != StabilityStable {
			continue
		}
		if backend.Name == BackendLLVM && !governanceLLVMAvailable() {
			continue
		}
		t.Run(string(backend.Name), func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "hello")
			diagnostics, err := BuildPathWithOptions(example, output, BuildOptions{Backend: backend.Name})
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("build %s: diagnostics=%v error=%v", backend.Name, diagnostics, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "hello" || entries[0].IsDir() {
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				t.Fatalf("%s output dir = %v, want only hello", backend.Name, names)
			}
			info, err := os.Stat(output)
			if err != nil || info.IsDir() || info.Size() == 0 {
				t.Fatalf("%s artifact = %v, %v", backend.Name, info, err)
			}
			for _, entry := range entries {
				if governanceHostManifest(entry.Name()) || governanceGeneratedSource(entry.Name()) {
					t.Fatalf("%s left host or generated file %s", backend.Name, entry.Name())
				}
			}
			assertNoBuildWorkspace(t, dir)
			assertNoBuildWorkspace(t, example)
		})
	}
}

func TestGovernanceStableBackendsCoverStableOperationsOnStableTargets(t *testing.T) {
	coverages := map[string]OperationCoverage{}
	for _, coverage := range EngineOperationCoverage() {
		coverages[coverage.Engine] = coverage
	}
	for _, backend := range Backends() {
		if backend.Stability != StabilityStable {
			continue
		}
		var stableTargets []string
		for _, target := range backend.Targets {
			if target.Stability == StabilityStable {
				stableTargets = append(stableTargets, target.Name)
			}
		}
		if len(stableTargets) == 0 {
			t.Fatalf("stable backend %s advertises no stable targets", backend.Name)
		}
		coverage, ok := coverages[string(backend.Name)]
		if !ok {
			t.Fatalf("stable backend %s has no operation coverage", backend.Name)
		}
		if len(coverage.MissingStable) > 0 {
			t.Fatalf("stable backend %s missing stable operations on stable targets %s:\n%s",
				backend.Name, strings.Join(stableTargets, ", "), strings.Join(coverage.MissingStable, "\n"))
		}
	}
}

func TestGovernanceEligibilityNeverPromotes(t *testing.T) {
	before := snapshotGovernanceStabilities()

	_ = Backends()
	_ = EngineOperationCoverage()
	if _, err := ProjectAdapterAvailability(filepath.Join("testdata", "redis-app")); err != nil {
		t.Fatalf("ProjectAdapterAvailability: %v", err)
	}
	if _, diagnostics, err := DescribePath("std", filepath.Join("..", "..", "examples", "hello")); err != nil || len(diagnostics) != 0 {
		t.Fatalf("DescribePath: diagnostics=%v error=%v", diagnostics, err)
	}

	after := snapshotGovernanceStabilities()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("declared stability mutated: before %#v after %#v", before, after)
	}

	var eligibleAlpha []string
	for _, backend := range Backends() {
		if backend.Eligible && backend.Stability == StabilityAlpha {
			eligibleAlpha = append(eligibleAlpha, string(backend.Name))
		}
	}
	if len(eligibleAlpha) == 0 {
		t.Fatal("no engine is eligible while still declared alpha")
	}
	for _, name := range []Backend{BackendRust, BackendBun} {
		found := false
		for _, backend := range Backends() {
			if backend.Name != name {
				continue
			}
			found = true
			if backend.Stability != StabilityAlpha {
				t.Fatalf("%s declared stability = %s, want alpha", name, backend.Stability)
			}
			if !backend.Eligible {
				t.Fatalf("%s is declared alpha but not eligible", name)
			}
		}
		if !found {
			t.Fatalf("backend %s missing from Backends()", name)
		}
	}
}

func assertNoSelectorParams(t *testing.T, file, fn string, fields *ast.FieldList) {
	t.Helper()
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if governanceSelectorName(name.Name) {
				t.Fatalf("%s: exported API %s takes selector parameter %s", file, fn, name.Name)
			}
		}
		if name := governanceExportedTypeName(field.Type); governanceSelectorName(name) {
			t.Fatalf("%s: exported API %s takes selector type %s", file, fn, name)
		}
	}
}

func governanceSelectorName(name string) bool {
	switch strings.ToLower(name) {
	case "provider", "implementation", "adapter":
		return true
	default:
		return false
	}
}

func governanceExportedTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return governanceExportedTypeName(typed.X)
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func governanceHostManifest(name string) bool {
	switch name {
	case "Cargo.toml", "Cargo.lock", "package.json", "bunfig.toml", "tsconfig.json":
		return true
	default:
		return false
	}
}

func governanceGeneratedSource(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".rs", ".js", ".ll":
		return true
	default:
		return false
	}
}

func governanceLLVMAvailable() bool {
	if bin := os.Getenv("SLICK_LLVM_BIN"); bin != "" {
		if _, err := os.Stat(filepath.Join(bin, "llc-18")); err == nil {
			if _, err := os.Stat(filepath.Join(bin, "llvm-as-18")); err == nil {
				return true
			}
		}
		if _, err := os.Stat(filepath.Join(bin, "llc")); err == nil {
			if _, err := os.Stat(filepath.Join(bin, "llvm-as")); err == nil {
				return true
			}
		}
		return false
	}
	for _, dir := range []string{"/usr/lib/llvm-18/bin", "/usr/bin"} {
		if _, err := os.Stat(filepath.Join(dir, "llc-18")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "llvm-as-18")); err == nil {
				return true
			}
		}
	}
	return false
}

func assertGovernanceFailure(t *testing.T, path, output string, options BuildOptions, want ...string) {
	t.Helper()
	diagnostics, err := BuildPathWithOptions(path, output, options)
	if len(diagnostics) != 0 || err == nil {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	message := err.Error()
	for _, fragment := range want {
		if !strings.Contains(message, fragment) {
			t.Fatalf("missing %q in error:\n%s", fragment, message)
		}
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed build left artifact %s: %v", output, statErr)
	}
	assertNoBuildWorkspace(t, filepath.Dir(output))
}

func assertGovernancePreservesOutput(t *testing.T, path string, options BuildOptions, want ...string) {
	t.Helper()
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	original := []byte("governance-sentinel")
	if err := os.WriteFile(output, original, 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := BuildPathWithOptions(path, output, options)
	if len(diagnostics) != 0 || err == nil {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	message := err.Error()
	for _, fragment := range want {
		if !strings.Contains(message, fragment) {
			t.Fatalf("missing %q in error:\n%s", fragment, message)
		}
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("pre-existing output changed: %q, %v", got, err)
	}
	assertNoBuildWorkspace(t, dir)
}

type governanceStabilitySnapshot struct {
	backends map[Backend]Stability
	targets  map[string]Stability
	symbols  map[string]Stability
}

func snapshotGovernanceStabilities() governanceStabilitySnapshot {
	snapshot := governanceStabilitySnapshot{
		backends: make(map[Backend]Stability),
		targets:  make(map[string]Stability),
		symbols:  make(map[string]Stability),
	}
	for _, backend := range backendRegistry {
		snapshot.backends[backend.name] = backend.stability
		for _, target := range backend.targets {
			snapshot.targets[string(backend.name)+"/"+target.name] = target.stability
		}
	}
	for name, record := range standardSymbolRecords(standardLibraryRegistry) {
		snapshot.symbols[name] = record.stability
	}
	return snapshot
}
