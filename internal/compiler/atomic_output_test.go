package compiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicOutput(t *testing.T) {
	t.Run("missing toolchain", testAtomicOutputMissingToolchain)
	t.Run("missing dependency", testAtomicOutputMissingDependency)
}

func testAtomicOutputMissingToolchain(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("SLICK_LLVM_BIN", empty)

	for _, test := range atomicOutputBackends() {
		t.Run(test.name, func(t *testing.T) {
			root, output, original := atomicOutputSentinel(t)
			_, err := BuildSourcesWithOptions(atomicSimpleSources(), output, test.options)
			if err == nil {
				t.Fatal("build succeeded with no toolchain")
			}
			// An empty PATH does not hide a host compiler the driver reaches by
			// absolute path, so a backend may fail later than validation with a
			// host tool's own message. The contract under test is atomicity;
			// toolchain validation messages are pinned where they are stable.
			if test.toolchainErr != "" && !strings.Contains(err.Error(), test.toolchainErr) {
				t.Fatalf("error = %v, want %q", err, test.toolchainErr)
			}
			assertAtomicOutputUntouched(t, root, output, original)
		})
	}
}

func testAtomicOutputMissingDependency(t *testing.T) {
	for _, test := range atomicOutputBackends() {
		t.Run(test.name, func(t *testing.T) {
			test.hideDependency(t)
			root, output, original := atomicOutputSentinel(t)
			_, err := BuildSourcesWithOptions(test.dependentSources, output, test.options)
			if err == nil {
				t.Fatal("build succeeded with a missing dependency")
			}
			assertAtomicOutputUntouched(t, root, output, original)
		})
	}
}

type atomicOutputBackend struct {
	name             string
	options          BuildOptions
	toolchainErr     string
	dependentSources []Source
	hideDependency   func(*testing.T)
}

func atomicOutputBackends() []atomicOutputBackend {
	return []atomicOutputBackend{
		{
			name:             "go",
			options:          BuildOptions{Backend: BackendGo},
			toolchainErr:     "Go toolchain not found",
			dependentSources: atomicSQLiteSources(),
			hideDependency:   hideGoModules,
		},
		{
			name:    "llvm",
			options: BuildOptions{Backend: BackendLLVM},
			// A host cc found by absolute path can fail while assembling instead
			// of at toolchain validation, so only atomicity is asserted here;
			// TestLLVMIncompatibleToolchainLeavesNoPartialOutput pins the message.
			toolchainErr:     "",
			dependentSources: atomicJSONSources(),
			hideDependency:   hideLLVMJansson,
		},
		{
			name:             "bun",
			options:          BuildOptions{Backend: BackendBun, AllowAlpha: true},
			toolchainErr:     "Bun toolchain not found",
			dependentSources: atomicSimpleSources(),
			hideDependency:   hideBunInstall,
		},
	}
}

func atomicSimpleSources() []Source {
	return []Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> int { 42 }`}}
}

func atomicJSONSources() []Source {
	return []Source{{Name: "main.slk", Namespace: "root", Text: `
function main() -> Result<string, std.json.Failure> {
    std.json.Encode<int>(1)
}
`}}
}

func atomicSQLiteSources() []Source {
	return []Source{{Name: "main.slk", Namespace: "root", Text: `
function main() -> Result<std.sqlite.Database, std.sqlite.Failure> effects { database } {
    std.sqlite.Open(":memory:")
}
`}}
}

func atomicOutputSentinel(t *testing.T) (root, output string, original []byte) {
	t.Helper()
	root = t.TempDir()
	output = filepath.Join(root, "program")
	original = []byte("preexisting-output-must-not-change")
	if err := os.WriteFile(output, original, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, output, original
}

func assertAtomicOutputUntouched(t *testing.T, root, output string, original []byte) {
	t.Helper()
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("output changed: %q, %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "program" {
			continue
		}
		t.Fatalf("partial leftover %s", name)
	}
}

func hideGoModules(t *testing.T) {
	t.Helper()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOMODCACHE", t.TempDir())
}

func hideLLVMJansson(t *testing.T) {
	t.Helper()
	t.Setenv("SLICK_JANSSON_ROOT", filepath.Join(t.TempDir(), "missing"))
}

func hideBunInstall(t *testing.T) {
	t.Helper()
	shadowOrHide(t, "bun", "install")
}

func shadowOrHide(t *testing.T, name, denyArg string) {
	t.Helper()
	real, err := exec.LookPath(name)
	if err != nil {
		t.Setenv("PATH", t.TempDir())
		return
	}
	dir := t.TempDir()
	key := "ATOMIC_REAL_" + strings.ToUpper(name)
	t.Setenv(key, real)
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = %s ]; then echo 'missing dependency' >&2; exit 1; fi\nexec \"$%s\" \"$@\"\n", denyArg, key)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
