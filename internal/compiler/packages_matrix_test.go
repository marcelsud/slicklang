package compiler

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const redisInterfaceHash = "17b8d9041ee3bc1a927f9d7cd2063599db87134908ecd62ae3b21def4eeb7956"

func TestPackageAdapterAvailability(t *testing.T) {
	formatted, diagnostics, err := Format(Source{
		Name: "api.slk", Namespace: "acme.redis",
		Text: readTestFile(t, redisTestdata("redis-package", "src", "api.slk")),
	})
	if err != nil || len(diagnostics) != 0 || formatted != readTestFile(t, redisTestdata("redis-package", "src", "api.slk")) {
		t.Fatalf("redis interface format: %q diagnostics=%v error=%v", formatted, diagnostics, err)
	}

	rows, err := ProjectAdapterAvailability(redisTestdata("redis-app"))
	if err != nil {
		t.Fatal(err)
	}
	want := []PackageAdapterAvailability{
		{
			Package: "acme.redis", Interface: "acme.redis", InterfaceHash: redisInterfaceHash,
			InterfaceEligible: true, Backend: string(BackendBun), Target: bunTargetLinuxX64Modern,
			AdapterStability: StabilityAlpha, AdapterEligible: true, Conforms: true,
		},
		{
			Package: "acme.redis", Interface: "acme.redis", InterfaceHash: redisInterfaceHash,
			InterfaceEligible: true, Backend: string(BackendGo), Target: "linux-x64",
			AdapterStability: StabilityStable, AdapterEligible: true, Conforms: true,
		},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("availability = %#v, want %#v", rows, want)
	}
	for _, row := range rows {
		if row.AdapterStability == StabilityAlpha && !row.AdapterEligible {
			t.Fatalf("alpha adapter should stay eligible without promotion: %#v", row)
		}
		if row.AdapterStability == StabilityStable && !row.AdapterEligible {
			t.Fatalf("stable adapter is not eligible: %#v", row)
		}
	}
	if _, err := os.Stat(filepath.Join(redisTestdata("redis-app"), packageLockName)); !os.IsNotExist(err) {
		t.Fatalf("availability survey wrote a lock: %v", err)
	}
	if got, want := backendStabilities(), map[Backend]Stability{
		BackendBun: StabilityAlpha, BackendGo: StabilityStable, BackendLLVM: StabilityStable,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backend stability = %#v, want %#v", got, want)
	}

	t.Run("nonconforming-adapter-is-ineligible", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		pointRedisAdapterAtSource(t, pkg, "go", "adapter", "function Value() -> int {\n    0\n}\n")
		rows, err := ProjectAdapterAvailability(app)
		if err != nil {
			t.Fatal(err)
		}
		var goRow, bunRow PackageAdapterAvailability
		for _, row := range rows {
			switch row.Backend {
			case string(BackendGo):
				goRow = row
			case string(BackendBun):
				bunRow = row
			}
		}
		if !goRow.InterfaceEligible || goRow.Conforms || goRow.AdapterEligible || goRow.AdapterStability != StabilityStable {
			t.Fatalf("nonconforming go adapter = %#v", goRow)
		}
		if !bunRow.InterfaceEligible || !bunRow.Conforms || !bunRow.AdapterEligible || bunRow.AdapterStability != StabilityAlpha {
			t.Fatalf("untouched bun adapter = %#v", bunRow)
		}
	})
}

func TestRedisPackageMatrix(t *testing.T) {
	t.Run("GoBuild", func(t *testing.T) {
		app, _ := copyRedisMatrix(t)
		output := filepath.Join(app, "app")
		if got := buildAndRun(t, app, output, BuildOptions{Backend: BackendGo}); got != "42\n" {
			t.Fatalf("go output = %q", got)
		}
		assertUnchangedApplication(t, app)
	})

	t.Run("BunAlphaOptIn", func(t *testing.T) {
		app, _ := copyRedisMatrix(t)
		output := filepath.Join(app, "app")
		buildMustFailBeforeEmission(t, app, output, BuildOptions{Backend: BackendBun},
			"backend bun is alpha", "--allow-alpha")
		if got := buildAndRun(t, app, output, BuildOptions{Backend: BackendBun, AllowAlpha: true}); got != "42\n" {
			t.Fatalf("bun output = %q", got)
		}
		assertUnchangedApplication(t, app)
	})

	t.Run("LLVMMissingAdapter", func(t *testing.T) {
		app, _ := copyRedisMatrix(t)
		message := buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendLLVM},
			"package acme.redis@2.1.0 has no adapter for backend llvm target linux-x64",
			"required by: root.application -> acme.redis@2.1.0",
			"go/linux-x64 (stable)",
			"bun/"+bunTargetLinuxX64Modern+" (alpha)",
		)
		if strings.Contains(strings.ToLower(message), "fallback") {
			t.Fatalf("missing llvm adapter invoked a fallback:\n%s", message)
		}
		assertUnchangedApplication(t, app)
	})

	t.Run("LLVMAdapterAdded", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		addRedisAdapter(t, pkg, packageAdapterSpec{
			id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable,
		})
		output := filepath.Join(app, "app")
		if got := buildAndRun(t, app, output, BuildOptions{Backend: BackendLLVM}); got != "42\n" {
			t.Fatalf("llvm output = %q", got)
		}
		var lock packageLock
		if err := readStrictJSON(filepath.Join(app, packageLockName), &lock); err != nil {
			t.Fatal(err)
		}
		if len(lock.Packages) != 1 || lock.Packages[0].Name != "acme.redis" ||
			len(lock.Packages[0].Selections) != 1 || lock.Packages[0].Selections[0].Adapter != "llvm" {
			t.Fatalf("llvm lock = %+v", lock)
		}
		assertUnchangedApplication(t, app)
	})

	t.Run("LLVMTransitiveMissing", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		codecRoot := filepath.Join(filepath.Dir(pkg), "codec")
		writePackageFixture(t, codecRoot, "acme.codec", "1.0.0", "function Encode() -> int { 1 }\n", nil,
			[]packageAdapterSpec{{id: "go", backend: BackendGo, target: hostTargetName(), stability: StabilityStable}})
		addRedisDependency(t, pkg, packageDependency{Name: "acme.codec", Version: "1.0.0", Path: "../codec"})
		addRedisAdapter(t, pkg, packageAdapterSpec{
			id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable,
			dependencies: []string{"acme.codec"},
		})
		buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendLLVM},
			"package acme.codec@1.0.0 has no adapter for backend llvm target linux-x64",
			"required by: root.application -> acme.redis@2.1.0 -> acme.codec@1.0.0",
		)
		assertUnchangedApplication(t, app)
	})

	t.Run("HashMismatch", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		manifest := readPackageManifestFixture(t, pkg)
		manifest.Adapters[0].InterfaceSHA256 = strings.Repeat("0", 64)
		writeJSONFixture(t, filepath.Join(pkg, packageManifestName), manifest)
		buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendGo},
			"adapter go declares interface hash",
			"want canonical hash "+redisInterfaceHash,
		)
	})

	t.Run("NonconformingAdapter", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		pointRedisAdapterAtSource(t, pkg, "go", "adapter", "function Value() -> int {\n    0\n}\n")
		buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendGo},
			"package acme.redis@2.1.0 adapter go conformance contract returned false, want true",
		)
	})

	t.Run("TransitiveClosure", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		cacheRoot := filepath.Join(filepath.Dir(pkg), "cache")
		writePackageFixture(t, cacheRoot, "acme.cache", "1.4.0",
			"use acme.redis.Value\nfunction Cached() -> int { Value() }\n",
			[]packageDependency{{Name: "acme.redis", Version: "2.1.0", Path: "../redis-package"}},
			[]packageAdapterSpec{{
				id: "llvm", backend: BackendLLVM, target: "linux-x64", stability: StabilityStable,
				dependencies: []string{"acme.redis"},
			}})
		writeProjectFixture(t, app, "use acme.cache.Cached\nfunction main() -> int { Cached() }\n",
			[]packageDependency{{Name: "acme.cache", Version: "1.4.0", Path: cacheRoot}})
		buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendLLVM},
			"package acme.redis@2.1.0 has no adapter for backend llvm target linux-x64",
			"required by: root.application -> acme.cache@1.4.0 -> acme.redis@2.1.0",
		)
	})

	t.Run("BackendStabilityUnchanged", func(t *testing.T) {
		app, _ := copyRedisMatrix(t)
		before := backendStabilities()
		buildMustFailBeforeEmission(t, app, filepath.Join(app, "app"),
			BuildOptions{Backend: BackendLLVM},
			"package acme.redis@2.1.0 has no adapter for backend llvm target linux-x64",
		)
		if got := backendStabilities(); !reflect.DeepEqual(got, before) || got[BackendLLVM] != StabilityStable {
			t.Fatalf("missing redis adapter changed backend stability: %#v", got)
		}
	})

	t.Run("AtomicOutput", func(t *testing.T) {
		app, pkg := copyRedisMatrix(t)
		output := filepath.Join(app, "app")
		if err := os.WriteFile(output, []byte("preexisting"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := readPackageManifestFixture(t, pkg)
		manifest.Adapters[0].InterfaceSHA256 = strings.Repeat("0", 64)
		writeJSONFixture(t, filepath.Join(pkg, packageManifestName), manifest)
		if _, err := BuildPathWithOptions(app, output, BuildOptions{Backend: BackendGo}); err == nil {
			t.Fatal("mismatched adapter unexpectedly built")
		}
		assertOutputContents(t, output, "preexisting")
		assertNoBuildWorkspace(t, app)
	})
}

func redisTestdata(elem ...string) string {
	return filepath.Join(append([]string{"testdata"}, elem...)...)
}

func copyRedisMatrix(t *testing.T) (app, pkg string) {
	t.Helper()
	root := t.TempDir()
	pkg = filepath.Join(root, "redis-package")
	app = filepath.Join(root, "redis-app")
	copyTree(t, redisTestdata("redis-package"), pkg)
	copyTree(t, redisTestdata("redis-app"), app)
	return app, pkg
}

func copyTree(t *testing.T, source, dest string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func addRedisAdapter(t *testing.T, packageRoot string, spec packageAdapterSpec) {
	t.Helper()
	manifest := readPackageManifestFixture(t, packageRoot)
	manifest.Adapters = append(manifest.Adapters, packageAdapter{
		ID: spec.id, Backend: spec.backend, Targets: []string{spec.target}, Stability: spec.stability,
		Kind: "slick", Entry: "src", Dependencies: spec.dependencies, Checksum: manifest.Interface.SHA256,
		Assets: []packageAsset{}, InterfaceSHA256: manifest.Interface.SHA256,
		ConformanceSHA256: manifest.Interface.ConformanceSHA256, ABI: "slick-core-1",
	})
	writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
}

func addRedisDependency(t *testing.T, packageRoot string, dependency packageDependency) {
	t.Helper()
	manifest := readPackageManifestFixture(t, packageRoot)
	manifest.Dependencies = append(manifest.Dependencies, dependency)
	writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
}

func pointRedisAdapterAtSource(t *testing.T, packageRoot, adapterID, entry, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(packageRoot, entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, entry, "api.slk"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	checksum, err := hashPath(filepath.Join(packageRoot, entry), true)
	if err != nil {
		t.Fatal(err)
	}
	manifest := readPackageManifestFixture(t, packageRoot)
	for index := range manifest.Adapters {
		if manifest.Adapters[index].ID != adapterID {
			continue
		}
		manifest.Adapters[index].Entry = entry
		manifest.Adapters[index].Checksum = checksum
	}
	writeJSONFixture(t, filepath.Join(packageRoot, packageManifestName), manifest)
}

func assertUnchangedApplication(t *testing.T, app string) {
	t.Helper()
	got := readTestFile(t, filepath.Join(app, "src", "main.slk"))
	want := readTestFile(t, redisTestdata("redis-app", "src", "main.slk"))
	if got != want {
		t.Fatalf("application source changed: %q", got)
	}
}

func backendStabilities() map[Backend]Stability {
	stabilities := make(map[Backend]Stability)
	for _, backend := range Backends() {
		stabilities[backend.Name] = backend.Stability
	}
	return stabilities
}

func buildAndRun(t *testing.T, app, output string, options BuildOptions) string {
	t.Helper()
	diagnostics, err := BuildPathWithOptions(app, output, options)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("build: diagnostics=%v error=%v", diagnostics, err)
	}
	built, err := exec.Command(output).CombinedOutput()
	if err != nil {
		t.Fatalf("run: output=%q error=%v", built, err)
	}
	return string(built)
}

func buildMustFailBeforeEmission(t *testing.T, app, output string, options BuildOptions, want ...string) string {
	t.Helper()
	before := backendStabilities()
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostics, err := BuildPathWithOptions(app, output, options)
	if len(diagnostics) != 0 || err == nil {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	message := err.Error()
	for _, fragment := range want {
		if !strings.Contains(message, fragment) {
			t.Fatalf("missing %q in error:\n%s", fragment, message)
		}
	}
	assertOutputContents(t, output, "sentinel")
	assertNoBuildWorkspace(t, app)
	if _, statErr := os.Stat(filepath.Join(app, packageLockName)); !os.IsNotExist(statErr) {
		t.Fatalf("failed resolution wrote lock: %v", statErr)
	}
	if got := backendStabilities(); !reflect.DeepEqual(got, before) {
		t.Fatalf("backend stability changed: got %#v want %#v", got, before)
	}
	return message
}
