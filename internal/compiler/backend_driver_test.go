package compiler

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRegisteredBackendDriversDeclareBuildContracts(t *testing.T) {
	if names := BackendNames(); !reflect.DeepEqual(names, []string{"bun", "go", "llvm", "rust"}) {
		t.Fatalf("backend names = %v", names)
	}
	for _, registration := range backendRegistry {
		if registration.driver == "" || registration.runtimeABI == 0 || registration.operations == nil {
			t.Fatalf("backend %s lacks driver or operation implementation metadata", registration.name)
		}
		if len(registration.runtimeCapabilities) == 0 {
			t.Fatalf("backend %s lacks runtime capabilities", registration.name)
		}
		driver, ok := registeredBackendDriver(registration.driver)
		if !ok || driver.validate == nil || driver.emit == nil || driver.build == nil || driver.verify == nil {
			t.Fatalf("backend %s lacks an explicit build phase", registration.name)
		}
		for _, target := range registration.targets {
			if target.platform.operatingSystem == "" || target.platform.architecture == "" ||
				target.artifactKind == "" || target.toolchain.name == "" || target.toolchain.version == "" {
				t.Fatalf("backend %s target %s has incomplete metadata: %+v", registration.name, target.name, target)
			}
		}
	}
	if ArtifactNativeExecutable == ArtifactWasmComponent ||
		ArtifactNativeExecutable == ArtifactRuntimeProgram ||
		ArtifactWasmComponent == ArtifactRuntimeProgram {
		t.Fatal("backend artifact kinds are not distinct")
	}
}

func TestNativeExecutableVerificationUsesTargetPlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "program")
	if err := os.WriteFile(path, []byte("MZpayload"), 0o644); err != nil {
		t.Fatal(err)
	}
	windows := backendDriverInput{
		target:       backendTargetRegistration{platform: backendPlatform{operatingSystem: "windows"}},
		artifactKind: ArtifactNativeExecutable,
	}
	if err := verifyNativeExecutable(windows, path); err != nil {
		t.Fatalf("verify Windows executable: %v", err)
	}
	unix := windows
	unix.target.platform.operatingSystem = "linux"
	if err := verifyNativeExecutable(unix, path); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("non-executable Unix candidate error = %v", err)
	}
}

func TestBackendDriverPhasesInstallAtomically(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "program")
	if err := os.WriteFile(output, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var phases []string
	driver := backendDriver{
		validate: func(input backendDriverInput) (backendToolchain, error) {
			phases = append(phases, "validate")
			if input.target.name != "test-x64" {
				t.Fatalf("driver target = %q", input.target.name)
			}
			return backendToolchain{registration: input.target.toolchain}, nil
		},
		emit: func(_ backendDriverInput, workspace string) (backendEmission, error) {
			phases = append(phases, "emit")
			path := filepath.Join(workspace, "source")
			return backendEmission{primary: path}, os.WriteFile(path, []byte("source"), 0o644)
		},
		build: func(_ backendDriverInput, emission backendEmission, candidate string) error {
			phases = append(phases, "build")
			if _, err := os.Stat(emission.primary); err != nil {
				return err
			}
			return os.WriteFile(candidate, []byte("new"), 0o755)
		},
		verify: func(input backendDriverInput, candidate string) error {
			phases = append(phases, "verify")
			return verifyNativeExecutable(input, candidate)
		},
	}
	plan := backendBuildPlan{
		input: backendDriverInput{
			core: coreProgram{},
			target: backendTargetRegistration{
				name:         "test-x64",
				artifactKind: ArtifactNativeExecutable,
				toolchain:    backendToolchainRegistration{name: "test", version: "1"},
			},
			artifactKind: ArtifactNativeExecutable,
		},
		output: output,
	}
	if err := executeBuildPlan(driver, plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(phases, []string{"validate", "emit", "build", "verify"}) {
		t.Fatalf("driver phases = %v", phases)
	}
	contents, err := os.ReadFile(output)
	if err != nil || string(contents) != "new" {
		t.Fatalf("installed artifact = %q, %v", contents, err)
	}
	assertNoBuildWorkspace(t, root)
}

func TestBackendDriverFailurePreservesExistingOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "program")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	driver := backendDriver{
		validate: func(input backendDriverInput) (backendToolchain, error) {
			return backendToolchain{registration: input.target.toolchain}, nil
		},
		emit: func(_ backendDriverInput, workspace string) (backendEmission, error) {
			path := filepath.Join(workspace, "source")
			return backendEmission{primary: path}, os.WriteFile(path, []byte("source"), 0o644)
		},
		build: func(_ backendDriverInput, _ backendEmission, candidate string) error {
			if err := os.WriteFile(candidate, []byte("partial"), 0o755); err != nil {
				return err
			}
			return errors.New("toolchain failed")
		},
		verify: verifyNativeExecutable,
	}
	plan := backendBuildPlan{
		input: backendDriverInput{
			target:       backendTargetRegistration{name: "test-x64"},
			artifactKind: ArtifactNativeExecutable,
		},
		output: output,
	}
	if err := executeBuildPlan(driver, plan); err == nil || !strings.Contains(err.Error(), "toolchain failed") {
		t.Fatalf("build failure = %v", err)
	}
	contents, err := os.ReadFile(output)
	if err != nil || string(contents) != "sentinel" {
		t.Fatalf("failed build changed output to %q, %v", contents, err)
	}
	assertNoBuildWorkspace(t, root)
}

func TestUnsupportedTargetAndMissingToolchainPreserveOutput(t *testing.T) {
	source := []Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> string { "ok" }`}}
	root := t.TempDir()
	output := filepath.Join(root, "program")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSourcesWithOptions(source, output, BuildOptions{Backend: BackendGo, Target: "unsupported"}); err == nil || !strings.Contains(err.Error(), "does not support target") {
		t.Fatalf("unsupported target error = %v", err)
	}
	assertOutputContents(t, output, "sentinel")

	t.Setenv("PATH", t.TempDir())
	if _, err := BuildSourcesWithOptions(source, output, BuildOptions{Backend: BackendGo}); err == nil || !strings.Contains(err.Error(), "Go toolchain not found") {
		t.Fatalf("missing toolchain error = %v", err)
	}
	assertOutputContents(t, output, "sentinel")
	assertNoBuildWorkspace(t, root)
}

func assertOutputContents(t *testing.T, path, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != expected {
		t.Fatalf("output = %q, %v; want %q", contents, err, expected)
	}
}

func assertNoBuildWorkspace(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".slick-build-") {
			t.Fatalf("temporary build workspace remains: %s", entry.Name())
		}
	}
}
