package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBunGenerationIsDeterministicAndEvaluatesArgumentsLeftToRight(t *testing.T) {
	core := rustCoreForTest(t, `function Pair(Left: int, Right: int) -> (int,int) { (Left, Right) }
function First() -> int { 1 }
function Second() -> int { 2 }
function main() -> (int,int) { Pair(First(), Second()) }`)
	first, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("identical Core IR produced different Bun module source")
	}
	mainStart := strings.Index(first, "function "+bunFunctionName("root.main"))
	entrypoint := strings.Index(first[mainStart:], "\n}\n\nconst slick_main_value")
	if mainStart < 0 || entrypoint < 0 {
		t.Fatal("generated root.main function is missing")
	}
	body := first[mainStart : mainStart+entrypoint]
	left := strings.Index(body, bunFunctionName("root.First")+"(")
	right := strings.Index(body, bunFunctionName("root.Second")+"(")
	call := strings.LastIndex(body, bunFunctionName("root.Pair")+"(")
	if left < 0 || right <= left || call <= right {
		t.Fatalf("call evaluation order was not materialized left-to-right:\n%s", body)
	}
}

func TestBunWorkspaceOwnsDeterministicRuntimeAndLockfile(t *testing.T) {
	core := rustCoreForTest(t, `function main() -> int { 42 }`)
	workspace := t.TempDir()
	emission, err := emitBunWorkspace(core, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if emission.primary != filepath.Join(workspace, "main.js") {
		t.Fatalf("primary emission = %q", emission.primary)
	}
	for path, want := range map[string]string{
		filepath.Join(workspace, "package.json"):            bunPackageManifest,
		filepath.Join(workspace, "bun.lock"):                bunLockfile,
		filepath.Join(workspace, "bunfig.toml"):             bunConfig,
		filepath.Join(workspace, "tsconfig.json"):           bunTypeScriptConfig,
		filepath.Join(workspace, "main.js"):                 mustGenerateBun(t, core),
		filepath.Join(workspace, "runtime", "package.json"): bunRuntimeManifest,
		filepath.Join(workspace, "runtime", "index.js"):     bunRuntimeModule,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s is not deterministic", path)
		}
	}
}

func TestBunBackendRegistrationReportsPinnedTargets(t *testing.T) {
	for _, backend := range Backends() {
		if backend.Name != BackendBun {
			continue
		}
		if backend.Stability != StabilityAlpha || backend.Eligible || len(backend.Targets) != 2 {
			t.Fatalf("Bun registration = %+v", backend)
		}
		want := []BackendTargetDescription{
			{Name: bunTargetLinuxX64Modern, Stability: StabilityAlpha, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
			{Name: bunTargetLinuxX64Baseline, Stability: StabilityAlpha, Toolchain: "bun", ToolchainVersion: bunToolchainVersion},
		}
		if !reflect.DeepEqual(backend.Targets, want) {
			t.Fatalf("Bun targets = %+v, want %+v", backend.Targets, want)
		}
		return
	}
	t.Fatal("Bun backend is not registered")
}

func TestBunBackendRequiresAlphaOptInBeforeTouchingOutput(t *testing.T) {
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "backend bun is alpha") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendDiagnosesSourceBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> string { 42 }`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		t.Fatalf("invalid Slick reached Bun toolchain: %v", err)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != "SLK340" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendLocatesUnsupportedStandardOperationsBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `function main() -> bool {
    std.text.Contains("slick", "ick")
}`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if len(diagnostics) != 0 || err == nil ||
		!strings.Contains(err.Error(), "Bun lowering main.slk:2:") ||
		!strings.Contains(err.Error(), "standard-library operation std.text.Contains is not supported") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("unsupported operation reached Bun toolchain: %v", err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendRejectsUnsupportedCoreBeforeToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output := rustSentinelOutput(t)
	diagnostics, err := BuildSourcesWithOptions([]Source{{
		Name: "main.slk", Namespace: "root", Text: `class Value { Number: int }
function main() -> int { let Item = Value { Number: 1 } Item.Number }`,
	}}, output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if len(diagnostics) != 0 || err == nil || !strings.Contains(err.Error(), "Bun lowering main.slk:") ||
		!strings.Contains(err.Error(), "classes are not supported (root.Value)") {
		t.Fatalf("diagnostics=%v error=%v", diagnostics, err)
	}
	if strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("unsupported Core reached Bun toolchain: %v", err)
	}
	requireRustSentinel(t, output)
}

func TestBunBackendReportsMissingIncompatibleAndUnavailableTargets(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), "need Bun "+bunToolchainVersion) ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Modern) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})

	t.Run("incompatible", func(t *testing.T) {
		tools := t.TempDir()
		writeRustTool(t, tools, "bun", "#!/bin/sh\necho '1.2.0'\n")
		t.Setenv("PATH", tools)
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{Backend: BackendBun, AllowAlpha: true})
		if err == nil || !strings.Contains(err.Error(), `unsupported Bun toolchain "1.2.0"`) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})

	t.Run("target", func(t *testing.T) {
		output := rustSentinelOutput(t)
		_, err := BuildSourcesWithOptions(rustValidSources(), output, BuildOptions{
			Backend: BackendBun, Target: "bun-plan9-x64", AllowAlpha: true,
		})
		if err == nil || !strings.Contains(err.Error(), "backend bun does not support target") ||
			!strings.Contains(err.Error(), bunTargetLinuxX64Baseline) {
			t.Fatalf("error = %v", err)
		}
		requireRustSentinel(t, output)
	})
}

func TestBunExecutableIsStandaloneAndLeavesNoWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "output with spaces")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	parentConfig := `{"compilerOptions":{"baseUrl":".","paths":{"@slick/runtime":["./hijack.js"]}}}`
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(parentConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hijack.js"), []byte(`export function slickWrite() { process.stdout.write("hijacked"); }`), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := BuildSourcesWithOptions(rustValidSources(), binary, BuildOptions{Backend: BackendBun, AllowAlpha: true})
	if err != nil {
		if strings.Contains(err.Error(), "Bun toolchain not found") {
			t.Skip(err.Error())
		}
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	command := exec.Command(binary)
	command.Env = []string{"PATH="}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "42\n" {
		t.Fatalf("standalone output=%q error=%v", output, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	wantEntries := []string{"app", "hijack.js", "tsconfig.json"}
	gotEntries := make([]string, len(entries))
	for index, entry := range entries {
		gotEntries[index] = entry.Name()
	}
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Fatalf("installed output directory contains generated workspace: %v", gotEntries)
	}
}

func TestBunBuildEnvironmentRemovesHostOverrides(t *testing.T) {
	got := bunBuildEnvironment([]string{
		"PATH=/bin", "HOME=/home/user", "BUN_INSTALL=/user/bun", "BUNFIG=/user/bunfig", "NODE_OPTIONS=--require=x", "SOURCE_DATE_EPOCH=host", "XDG_CONFIG_HOME=/host/config",
	}, map[string]string{"BUN_INSTALL": "/owned", "HOME": "/owned/home", "NO_COLOR": "1", "SOURCE_DATE_EPOCH": "0", "XDG_CONFIG_HOME": "/owned/config"})
	want := []string{"PATH=/bin", "BUN_INSTALL=/owned", "HOME=/owned/home", "NO_COLOR=1", "SOURCE_DATE_EPOCH=0", "XDG_CONFIG_HOME=/owned/config"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %v, want %v", got, want)
	}
}

func TestBunStringLiteralUsesUTF16Escapes(t *testing.T) {
	got := bunStringLiteral("a😀\u202e\n")
	if want := `"a\ud83d\ude00\u202e\n"`; got != want {
		t.Fatalf("literal = %q, want %q", got, want)
	}
}

func mustGenerateBun(t *testing.T, core coreProgram) string {
	t.Helper()
	source, err := generateBun(core)
	if err != nil {
		t.Fatal(err)
	}
	return source
}
