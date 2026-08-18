package compiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type packageAdapterSpec struct {
	id           string
	backend      Backend
	target       string
	stability    Stability
	dependencies []string
}

func TestCanonicalPackageBuildSelectsTargetAdapterAndWritesLock(t *testing.T) {
	root := t.TempDir()
	redisRoot := filepath.Join(root, "packages", "redis")
	dependency := writePackageFixture(t, redisRoot, "acme.redis", "2.1.0", `function Value() -> int { 42 }`, nil, []packageAdapterSpec{
		{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
		{id: "bun", backend: BackendBun, target: bunTargetLinuxX64Modern, stability: StabilityAlpha},
	})
	writeProjectFixture(t, root, `use acme.redis.Value
function main() -> int { Value() }`, []packageDependency{dependency})

	binary := filepath.Join(root, "app")
	diagnostics, err := BuildPathWithOptions(root, binary, BuildOptions{Backend: BackendGo})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("build package application: diagnostics=%v error=%v", diagnostics, err)
	}
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != "42\n" {
		t.Fatalf("package output=%q error=%v", output, err)
	}
	var lock packageLock
	if err := readStrictJSON(filepath.Join(root, packageLockName), &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.Packages) != 1 || lock.Packages[0].Name != "acme.redis" ||
		len(lock.Packages[0].Selections) != 1 || lock.Packages[0].Selections[0].Adapter != "go" {
		t.Fatalf("package lock = %+v", lock)
	}
	if diagnostics, err := CheckPath(root); err != nil || len(diagnostics) != 0 {
		t.Fatalf("check canonical package source: diagnostics=%v error=%v", diagnostics, err)
	}
	sources, err := LoadSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSourcesWithOptions(sources, filepath.Join(root, "bypass"), BuildOptions{Backend: BackendGo}); err == nil ||
		!strings.Contains(err.Error(), "use BuildPathWithOptions") {
		t.Fatalf("unresolved package source build error = %v", err)
	}
}

func TestMissingAdapterFailsWithDependencyPathBeforeEmission(t *testing.T) {
	root := t.TempDir()
	redisRoot := filepath.Join(root, "packages", "redis")
	redis := writePackageFixture(t, redisRoot, "acme.redis", "2.1.0", `function Value() -> int { 42 }`, nil, []packageAdapterSpec{
		{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
		{id: "bun", backend: BackendBun, target: bunTargetLinuxX64Modern, stability: StabilityAlpha},
	})
	cacheRoot := filepath.Join(root, "packages", "cache")
	cache := writePackageFixture(t, cacheRoot, "acme.cache", "1.4.0", `use acme.redis.Value
function Cached() -> int { Value() }`, []packageDependency{{Name: redis.Name, Version: redis.Version, Path: "../redis"}}, []packageAdapterSpec{
		{id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable, dependencies: []string{"acme.redis"}},
	})
	writeProjectFixture(t, root, `use acme.cache.Cached
function main() -> int { Cached() }`, []packageDependency{cache})
	output := filepath.Join(root, "app")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendLLVM})
	if len(diagnostics) != 0 || err == nil {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	message := err.Error()
	for _, want := range []string{
		"package acme.redis@2.1.0 has no adapter for backend llvm target linux-x64",
		"required by: root.application -> acme.cache@1.4.0 -> acme.redis@2.1.0",
		"go/" + hostTargetName() + " (stable)",
		"bun/" + bunTargetLinuxX64Modern + " (alpha)",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing %q in error:\n%s", want, message)
		}
	}
	assertOutputContents(t, output, "sentinel")
	if _, statErr := os.Stat(filepath.Join(root, packageLockName)); !os.IsNotExist(statErr) {
		t.Fatalf("failed resolution wrote lock: %v", statErr)
	}
}

func TestAddingLLVMAdapterBuildsUnchangedPackageApplication(t *testing.T) {
	root := t.TempDir()
	redisRoot := filepath.Join(root, "packages", "redis")
	goOnly := []packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}}
	dependency := writePackageFixture(t, redisRoot, "acme.redis", "2.1.0", `function Value() -> int { 42 }`, nil, goOnly)
	application := `use acme.redis.Value
function main() -> int { Value() }`
	writeProjectFixture(t, root, application, []packageDependency{dependency})
	output := filepath.Join(root, "app")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendLLVM}); err == nil {
		t.Fatal("Go-only package unexpectedly built under LLVM")
	}
	writePackageFixture(t, redisRoot, "acme.redis", "2.1.0", `function Value() -> int { 42 }`, nil, append(goOnly,
		packageAdapterSpec{id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable}))
	llvmEntry := filepath.Join(redisRoot, "llvm")
	if err := os.MkdirAll(llvmEntry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(llvmEntry, "adapter.slk"), []byte(`function helper() -> int { 42 }
function Value() -> int { helper() }`), 0o644); err != nil {
		t.Fatal(err)
	}
	llvmChecksum, err := hashPath(llvmEntry, true)
	if err != nil {
		t.Fatal(err)
	}
	manifest := readPackageManifestFixture(t, redisRoot)
	manifest.Adapters[1].Entry = "llvm"
	manifest.Adapters[1].Checksum = llvmChecksum
	writeJSONFixture(t, filepath.Join(redisRoot, packageManifestName), manifest)
	if got := readTestFile(t, filepath.Join(root, "src", "main.slk")); got != application {
		t.Fatalf("application source changed: %q", got)
	}
	diagnostics, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendLLVM})
	if err != nil {
		if strings.Contains(err.Error(), "LLVM toolchain not found") || strings.Contains(err.Error(), "llc") {
			t.Skip(err.Error())
		}
		t.Fatalf("build with added LLVM adapter: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	built, err := exec.Command(output).CombinedOutput()
	if err != nil || string(built) != "42\n" {
		t.Fatalf("LLVM package output=%q error=%v", built, err)
	}
}

func TestPackageMatrixResolvesEveryExplicitBackendTarget(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "packages", "portable")
	specs := []packageAdapterSpec{
		{id: "bun", backend: BackendBun, target: bunTargetLinuxX64Modern, stability: StabilityAlpha},
		{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
		{id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable},
	}
	dependency := writePackageFixture(t, packageRoot, "acme.portable", "1.0.0", `function Value() -> int { 1 }`, nil, specs)
	writeProjectFixture(t, root, `use acme.portable.Value
function main() -> int { Value() }`, []packageDependency{dependency})
	for _, spec := range specs {
		loaded, err := loadPackageProject(root, &packageBuildSelection{backend: spec.backend, target: spec.target, allowAlpha: true}, true)
		if err != nil {
			t.Fatalf("resolve %s/%s: %v", spec.backend, spec.target, err)
		}
		var lock packageLock
		if err := decodeStrictJSON(loaded.lockData, &lock); err != nil {
			t.Fatal(err)
		}
		if len(lock.Packages) != 1 || len(lock.Packages[0].Selections) != 1 || lock.Packages[0].Selections[0].Adapter != spec.id {
			t.Fatalf("%s/%s lock = %+v", spec.backend, spec.target, lock)
		}
	}
}

func TestPackageAlphaAmbiguityAndConformancePolicies(t *testing.T) {
	t.Run("interface-alpha", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.preview", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Stability = StabilityAlpha
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `use acme.preview.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		if _, err := CheckPath(root); err == nil || !strings.Contains(err.Error(), "package interface acme.preview@1.0.0 is alpha") {
			t.Fatalf("check alpha interface error = %v", err)
		}
		if diagnostics, err := CheckPathWithOptions(root, CheckOptions{AllowAlpha: true}); err != nil || len(diagnostics) != 0 {
			t.Fatalf("check opted-in alpha interface: diagnostics=%v error=%v", diagnostics, err)
		}
	})
	t.Run("alpha", func(t *testing.T) {
		root := t.TempDir()
		dependency := writePackageFixture(t, filepath.Join(root, "package"), "acme.alpha", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityAlpha}})
		writeProjectFixture(t, root, `use acme.alpha.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		output := filepath.Join(root, "app")
		if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err == nil || !strings.Contains(err.Error(), "package adapter go") || !strings.Contains(err.Error(), "--allow-alpha") {
			t.Fatalf("alpha error = %v", err)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		root := t.TempDir()
		dependency := writePackageFixture(t, filepath.Join(root, "package"), "acme.ambiguous", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{
				{id: "first", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
				{id: "second", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
			})
		writeProjectFixture(t, root, `use acme.ambiguous.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "ambiguous adapters") || !strings.Contains(err.Error(), "first, second") {
			t.Fatalf("ambiguity error = %v", err)
		}
	})

	t.Run("conformance", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.invalid", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Adapters[0].ConformanceSHA256 = strings.Repeat("0", 64)
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `use acme.invalid.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "declares conformance hash") {
			t.Fatalf("conformance error = %v", err)
		}
	})
}

func TestPackageLockRejectsInterfaceAndAdapterDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*packageManifest)
		source string
		want   string
	}{
		{name: "interface", source: `function Value() -> int { 2 }`, want: "interface"},
		{name: "adapter", source: `function Value() -> int { 1 }`, want: "adapter lock drift", mutate: func(manifest *packageManifest) {
			manifest.Adapters[0].ID = "replacement"
		}},
	} {

		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			packageRoot := filepath.Join(root, "package")
			dependency := writePackageFixture(t, packageRoot, "acme.locked", "1.0.0", `function Value() -> int { 1 }`, nil,
				[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
			writeProjectFixture(t, root, `use acme.locked.Value
function main() -> int { Value() }`, []packageDependency{dependency})
			output := filepath.Join(root, "app")
			if diagnostics, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err != nil || len(diagnostics) != 0 {
				t.Fatalf("initial build: diagnostics=%v error=%v", diagnostics, err)
			}
			manifest := readPackageManifestFixture(t, packageRoot)
			if test.source != `function Value() -> int { 1 }` {
				if err := os.WriteFile(filepath.Join(packageRoot, "src", "api.slk"), []byte(test.source), 0o644); err != nil {
					t.Fatal(err)
				}
				hash, err := hashPath(filepath.Join(packageRoot, "src"), true)
				if err != nil {
					t.Fatal(err)
				}
				manifest.Interface.SHA256 = hash
				manifest.Adapters[0].InterfaceSHA256 = hash
				manifest.Adapters[0].Checksum = hash
			}
			if test.mutate != nil {
				test.mutate(&manifest)
			}
			writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
			if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("drift error = %v", err)
			}
			assertOutputContents(t, output, "sentinel")
		})
	}
}
func TestPackageLockRejectsStaleSelectionForAnotherTarget(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "package")
	dependency := writePackageFixture(t, packageRoot, "acme.locked", "1.0.0", `function Value() -> int { 1 }`, nil,
		[]packageAdapterSpec{
			{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
			{id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable},
		})
	writeProjectFixture(t, root, `use acme.locked.Value
function main() -> int { Value() }`, []packageDependency{dependency})
	output := filepath.Join(root, "app")
	if diagnostics, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err != nil || len(diagnostics) != 0 {
		t.Fatalf("initial build: diagnostics=%v error=%v", diagnostics, err)
	}
	var lock packageLock
	lockPath := filepath.Join(root, packageLockName)
	if err := readStrictJSON(lockPath, &lock); err != nil {
		t.Fatal(err)
	}
	lock.Packages[0].Selections = append(lock.Packages[0].Selections, packageLockSelection{
		Backend: BackendLLVM, Target: "linux-x64", Adapter: "removed", Checksum: strings.Repeat("0", 64),
	})
	writeJSONFixture(t, lockPath, lock)
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err == nil ||
		!strings.Contains(err.Error(), "locked removed/") || !strings.Contains(err.Error(), "is not declared") {
		t.Fatalf("stale selection error = %v", err)
	}
	assertOutputContents(t, output, "sentinel")
}

func TestPackageResolutionRejectsCyclesAndAssetDrift(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		root := t.TempDir()
		packages := filepath.Join(root, "packages")
		commonAdapter := []packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}}
		writePackageFixture(t, filepath.Join(packages, "b"), "acme.b", "1.0.0", `function B() -> int { 2 }`,
			[]packageDependency{{Name: "acme.a", Version: "1.0.0", Path: "../a"}}, commonAdapter)
		a := writePackageFixture(t, filepath.Join(packages, "a"), "acme.a", "1.0.0", `function A() -> int { 1 }`,
			[]packageDependency{{Name: "acme.b", Version: "1.0.0", Path: "../b"}}, commonAdapter)
		writeProjectFixture(t, root, `use acme.a.A
function main() -> int { A() }`, []packageDependency{a})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "root.application -> acme.a@1.0.0 -> acme.b@1.0.0 -> acme.a@1.0.0") {
			t.Fatalf("cycle error = %v", err)
		}
	})

	t.Run("asset", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.asset", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		if err := os.WriteFile(filepath.Join(packageRoot, "runtime.bin"), []byte("artifact"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Adapters[0].Assets = []packageAsset{{Path: "runtime.bin", SHA256: strings.Repeat("0", 64)}}
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `use acme.asset.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "asset runtime.bin checksum mismatch") {
			t.Fatalf("asset error = %v", err)
		}
	})
}

func TestPackageAdapterContractEnforcement(t *testing.T) {
	t.Run("surface", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.surface", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		adapterRoot := filepath.Join(packageRoot, "adapter")
		if err := os.MkdirAll(adapterRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adapterRoot, "adapter.slk"), []byte(`function Value() -> string { "wrong" }`), 0o644); err != nil {
			t.Fatal(err)
		}
		checksum, err := hashPath(adapterRoot, true)
		if err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Adapters[0].Entry = "adapter"
		manifest.Adapters[0].Checksum = checksum
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "redefines the canonical public interface") {
			t.Fatalf("surface error = %v", err)
		}
	})

	t.Run("private-field-shape", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.shape", "1.0.0", `class Item { Value: int }
function Make() -> Item { Item { Value: 1 } }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		adapterRoot := filepath.Join(packageRoot, "adapter")
		if err := os.MkdirAll(adapterRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adapterRoot, "adapter.slk"), []byte(`class Item {
    Value: int
    secret: int
}
function Make() -> Item { Item { Value: 1, secret: 0 } }`), 0o644); err != nil {
			t.Fatal(err)
		}
		checksum, err := hashPath(adapterRoot, true)
		if err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Adapters[0].Entry = "adapter"
		manifest.Adapters[0].Checksum = checksum
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "redefines the canonical public interface") {
			t.Fatalf("private field shape error = %v", err)
		}
	})

	t.Run("every-adapter-conformance", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.contract", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{
				{id: "bun", backend: BackendBun, target: bunTargetLinuxX64Modern, stability: StabilityAlpha},
				{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
			})
		bunRoot := filepath.Join(packageRoot, "bun")
		if err := os.MkdirAll(bunRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bunRoot, "adapter.slk"), []byte(`function Value() -> int { 2 }`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, "conformance", "contract.slk"), []byte(`use acme.contract.Value
function main() -> bool { Value() == 1 }`), 0o644); err != nil {
			t.Fatal(err)
		}
		bunChecksum, err := hashPath(bunRoot, true)
		if err != nil {
			t.Fatal(err)
		}
		conformanceHash, err := hashPath(filepath.Join(packageRoot, "conformance"), true)
		if err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Interface.ConformanceSHA256 = conformanceHash
		for index := range manifest.Adapters {
			manifest.Adapters[index].ConformanceSHA256 = conformanceHash
			if manifest.Adapters[index].ID == "bun" {
				manifest.Adapters[index].Entry = "bun"
				manifest.Adapters[index].Checksum = bunChecksum
			}
		}
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `use acme.contract.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "adapter bun conformance contract returned false") {
			t.Fatalf("conformance error = %v", err)
		}
	})

	t.Run("conformance-step-limit", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.fuel", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		if err := os.WriteFile(filepath.Join(packageRoot, "conformance", "contract.slk"), []byte(`function recurse() -> bool { recurse() }
function main() -> bool { recurse() }`), 0o644); err != nil {
			t.Fatal(err)
		}
		conformanceHash, err := hashPath(filepath.Join(packageRoot, "conformance"), true)
		if err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Interface.ConformanceSHA256 = conformanceHash
		manifest.Adapters[0].ConformanceSHA256 = conformanceHash
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "execution step limit exceeded") {
			t.Fatalf("conformance limit error = %v", err)
		}
	})

	t.Run("unselected-hash-binding", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "package")
		dependency := writePackageFixture(t, packageRoot, "acme.binding", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{
				{id: "bun", backend: BackendBun, target: bunTargetLinuxX64Modern, stability: StabilityAlpha},
				{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable},
			})
		manifest := readPackageManifestFixture(t, packageRoot)
		manifest.Adapters[0].InterfaceSHA256 = strings.Repeat("0", 64)
		writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{dependency})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "adapter bun declares interface hash") {
			t.Fatalf("binding error = %v", err)
		}
	})
}

func TestPackageImportsRequireDeclaredDirectDependencies(t *testing.T) {
	t.Run("application", func(t *testing.T) {
		root := t.TempDir()
		redis := writePackageFixture(t, filepath.Join(root, "packages", "redis"), "acme.redis", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		cache := writePackageFixture(t, filepath.Join(root, "packages", "cache"), "acme.cache", "1.0.0", `use acme.redis.Value
function Cached() -> int { Value() }`, []packageDependency{{Name: redis.Name, Version: redis.Version, Path: "../redis"}},
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable, dependencies: []string{"acme.redis"}}})
		writeProjectFixture(t, root, `use acme.redis.Value
function main() -> int { Value() }`, []packageDependency{cache})
		diagnostics, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo})
		if err != nil || len(diagnostics) == 0 || !strings.Contains(diagnostics[0].Message, "not a declared dependency of the application") {
			t.Fatalf("application dependency diagnostics=%v error=%v", diagnostics, err)
		}
	})

	t.Run("qualified-access-without-use", func(t *testing.T) {
		for name, application := range map[string]string{
			"call":   `function main() -> int { acme.redis.Value() }`,
			"object": `function main() -> int { let Item = acme.redis.Item { Number: 1 } Item.Number }`,
		} {
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				redis := writePackageFixture(t, filepath.Join(root, "packages", "redis"), "acme.redis", "1.0.0", `class Item { Number: int }
function Value() -> int { 1 }`, nil,
					[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
				cache := writePackageFixture(t, filepath.Join(root, "packages", "cache"), "acme.cache", "1.0.0", `use acme.redis.Value
function Cached() -> int { Value() }`, []packageDependency{{Name: redis.Name, Version: redis.Version, Path: "../redis"}},
					[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable, dependencies: []string{"acme.redis"}}})
				writeProjectFixture(t, root, application, []packageDependency{cache})
				diagnostics, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo})
				if err != nil || len(diagnostics) == 0 {
					t.Fatalf("qualified transitive access diagnostics=%v error=%v", diagnostics, err)
				}
			})
		}
	})

	t.Run("adapter", func(t *testing.T) {
		root := t.TempDir()
		redis := writePackageFixture(t, filepath.Join(root, "packages", "redis"), "acme.redis", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		cache := writePackageFixture(t, filepath.Join(root, "packages", "cache"), "acme.cache", "1.0.0", `use acme.redis.Value
function Cached() -> int { Value() }`, []packageDependency{{Name: redis.Name, Version: redis.Version, Path: "../redis"}},
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{cache})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "not a declared dependency of acme.cache") {
			t.Fatalf("adapter dependency error = %v", err)
		}
	})
}

func TestPackageNamespacesAndLockUpdatesAreIsolated(t *testing.T) {
	if validPackageName("std.evil") || validPackageName("root.evil") {
		t.Fatal("reserved namespace accepted as a package")
	}
	t.Run("prefix", func(t *testing.T) {
		root := t.TempDir()
		short := writePackageFixture(t, filepath.Join(root, "packages", "short"), "acme.redis", "1.0.0", `function One() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		nested := writePackageFixture(t, filepath.Join(root, "packages", "nested"), "acme.redis.cache", "1.0.0", `function Two() -> int { 2 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		writeProjectFixture(t, root, `function main() -> int { 0 }`, []packageDependency{nested, short})
		if _, err := BuildPathWithOptions(root, filepath.Join(root, "app"), BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "package namespaces acme.redis.cache and acme.redis overlap") {
			t.Fatalf("namespace overlap error = %v", err)
		}
	})

	t.Run("guard-before-output", func(t *testing.T) {
		root := t.TempDir()
		dependency := writePackageFixture(t, filepath.Join(root, "package"), "acme.guard", "1.0.0", `function Value() -> int { 1 }`, nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		writeProjectFixture(t, root, `use acme.guard.Value
function main() -> int { Value() }`, []packageDependency{dependency})
		guard, err := acquirePackageLock(filepath.Join(root, packageLockName), nil)
		if err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "app")
		if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err == nil ||
			!strings.Contains(err.Error(), "another build is updating") {
			t.Fatalf("guard error = %v", err)
		}
		assertOutputContents(t, output, "sentinel")
		guard.release()
		if _, err := BuildPathWithOptions(root, output, BuildOptions{Backend: BackendGo}); err != nil {
			t.Fatalf("build after guard release: %v", err)
		}
		built, err := exec.Command(output).CombinedOutput()
		if err != nil || string(built) != "1\n" {
			t.Fatalf("build after released guard output=%q error=%v", built, err)
		}
	})
}

func TestPackageBuildUsesChecksummedSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "package")
	dependency := writePackageFixture(t, packageRoot, "acme.snapshot", "1.0.0", `function Value() -> int { 1 }`, nil,
		[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
	writeProjectFixture(t, root, `use acme.snapshot.Value
function main() -> int { Value() }`, []packageDependency{dependency})
	loaded, err := loadPackageProject(root, &packageBuildSelection{backend: BackendGo, target: hostTargetName()}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "src", "api.slk"), []byte(`function Value() -> int { 2 }`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "app")
	diagnostics, err := BuildSourcesWithOptions(loaded.sources, output, BuildOptions{Backend: BackendGo})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("snapshot build: diagnostics=%v error=%v", diagnostics, err)
	}
	built, err := exec.Command(output).CombinedOutput()
	if err != nil || string(built) != "1\n" {
		t.Fatalf("snapshot output=%q error=%v", built, err)
	}
}

func writePackageFixture(t *testing.T, root, name, version, source string, dependencies []packageDependency, specs []packageAdapterSpec) packageDependency {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "conformance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "api.slk"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "conformance", "contract.slk"), []byte("function main() -> bool { true }"), 0o644); err != nil {
		t.Fatal(err)
	}
	interfaceHash, err := hashPath(filepath.Join(root, "src"), true)
	if err != nil {
		t.Fatal(err)
	}
	conformanceHash, err := hashPath(filepath.Join(root, "conformance"), true)
	if err != nil {
		t.Fatal(err)
	}
	manifest := packageManifest{
		SchemaVersion: packageSchemaVersion,
		Name:          name,
		Version:       version,
		Stability:     StabilityStable,
		Interface: packageInterface{
			Path: "src", SHA256: interfaceHash,
			Effects: []string{}, Resources: []string{},
			ConformancePath: "conformance", ConformanceSHA256: conformanceHash,
		},
		Dependencies: dependencies,
	}
	for _, spec := range specs {
		manifest.Adapters = append(manifest.Adapters, packageAdapter{
			ID: spec.id, Backend: spec.backend, Targets: []string{spec.target}, Stability: spec.stability,
			Kind: "slick", Entry: "src", Dependencies: spec.dependencies, Checksum: interfaceHash,
			Assets: []packageAsset{}, InterfaceSHA256: interfaceHash, ConformanceSHA256: conformanceHash,
			ABI: "slick-core-1",
		})
	}
	writeJSONFixture(t, filepath.Join(root, packageManifestName), manifest)
	return packageDependency{Name: name, Version: version, Path: filepath.ToSlash(root)}
}

func writeProjectFixture(t *testing.T, root, source string, dependencies []packageDependency) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for index := range dependencies {
		relative, err := filepath.Rel(root, dependencies[index].Path)
		if err != nil {
			t.Fatal(err)
		}
		dependencies[index].Path = filepath.ToSlash(relative)
	}
	writeJSONFixture(t, filepath.Join(root, projectManifestName), projectManifest{
		SchemaVersion: packageSchemaVersion, Name: "root.application", Source: "src", Dependencies: dependencies,
	})
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readPackageManifestFixture(t *testing.T, root string) packageManifest {
	t.Helper()
	var manifest packageManifest
	if err := readStrictJSON(filepath.Join(root, packageManifestName), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
