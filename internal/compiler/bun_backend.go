package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	bunToolchainVersion       = "1.3.14"
	bunTargetLinuxX64Modern   = "bun-linux-x64-modern"
	bunTargetLinuxX64Baseline = "bun-linux-x64-baseline"
)
const bunConfig = `# Compiler-owned; prevents parent or user project configuration.
`

const bunTypeScriptConfig = `{
  "compilerOptions": {
    "module": "ESNext",
    "moduleResolution": "bundler"
  }
}
`

const bunPackageManifest = `{
  "name": "slick-program",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "workspaces": ["runtime"],
  "dependencies": {
    "@slick/runtime": "workspace:*"
  }
}
`

const bunRuntimeManifest = `{
  "name": "@slick/runtime",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "exports": "./index.js"
}
`

const bunLockfile = `{
  "lockfileVersion": 1,
  "configVersion": 1,
  "workspaces": {
    "": {
      "name": "slick-program",
      "dependencies": {
        "@slick/runtime": "workspace:*"
      }
    },
    "runtime": {
      "name": "@slick/runtime",
      "version": "1.0.0"
    }
  },
  "packages": {
    "@slick/runtime": ["@slick/runtime@workspace:runtime"]
  }
}
`

const bunRuntimeModule = `export function slickWrapInt(value) {
  return BigInt.asIntN(64, value);
}

export function slickFormatFloat(value) {
  if (Number.isNaN(value)) return "NaN";
  if (value === Infinity) return "+Inf";
  if (value === -Infinity) return "-Inf";
  if (Object.is(value, -0)) return "-0";
  const magnitude = Math.abs(value);
  if (value === 0 || (magnitude >= 1e-4 && magnitude < 1e6)) return String(value);
  const [mantissa, rawExponent] = value.toExponential().split("e");
  const exponent = Number(rawExponent);
  const sign = exponent < 0 ? "-" : "+";
  const digits = String(Math.abs(exponent));
  return mantissa + "e" + sign + (digits.length < 2 ? "0" + digits : digits);
}

export function slickWrite(value) {
  process.stdout.write(value);
}
`

func bunBackendDriver() backendDriver {
	return backendDriver{
		checkCore: func(input backendDriverInput) error {
			return validatePrimitiveCore(input.core, input.runtime, "Bun")
		},
		validate: validateBunToolchain,
		emit: func(input backendDriverInput, workspace string) (backendEmission, error) {
			emission, err := emitBunWorkspace(input.core, workspace)
			if err != nil {
				return backendEmission{}, bunBackendBuildError(input, err)
			}
			return emission, nil
		},
		build: func(input backendDriverInput, emission backendEmission, candidate string) error {
			if err := buildBunEmission(input, emission, candidate); err != nil {
				return bunBackendBuildError(input, err)
			}
			return nil
		},
		verify: verifyNativeExecutable,
	}
}

func bunBackendBuildError(input backendDriverInput, err error) error {
	return fmt.Errorf("Bun backend (%s %s, target %s): %w",
		input.toolchain.registration.name, input.toolchain.version, input.target.name, err)
}

func validateBunToolchain(input backendDriverInput) (backendToolchain, error) {
	switch input.target.name {
	case bunTargetLinuxX64Modern, bunTargetLinuxX64Baseline:
	default:
		return backendToolchain{}, fmt.Errorf("Bun target %s is unsupported; need %s or %s",
			input.target.name, bunTargetLinuxX64Modern, bunTargetLinuxX64Baseline)
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		return backendToolchain{}, fmt.Errorf("Bun toolchain not found: need Bun %s for target %s or %s",
			bunToolchainVersion, bunTargetLinuxX64Modern, bunTargetLinuxX64Baseline)
	}
	command := exec.Command(bun, "--version")
	command.Dir = string(filepath.Separator)
	command.Env = bunBuildEnvironment(os.Environ(), map[string]string{"HOME": string(filepath.Separator), "NO_COLOR": "1"})
	output, err := command.CombinedOutput()
	if err != nil {
		return backendToolchain{}, fmt.Errorf("inspect Bun version: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if found := strings.TrimSpace(string(output)); found != bunToolchainVersion {
		return backendToolchain{}, fmt.Errorf("unsupported Bun toolchain %q; need Bun %s for target %s or %s",
			found, bunToolchainVersion, bunTargetLinuxX64Modern, bunTargetLinuxX64Baseline)
	}
	return backendToolchain{
		registration: input.target.toolchain,
		executables:  map[string]string{"bun": bun},
		version:      bunToolchainVersion,
	}, nil
}

func emitBunWorkspace(core coreProgram, workspace string) (backendEmission, error) {
	entry, err := generateBun(core)
	if err != nil {
		return backendEmission{}, err
	}
	runtimeDirectory := filepath.Join(workspace, "runtime")
	if err := os.MkdirAll(runtimeDirectory, 0o755); err != nil {
		return backendEmission{}, fmt.Errorf("create compiler-owned Bun runtime: %w", err)
	}
	files := []struct {
		path string
		data string
	}{
		{path: filepath.Join(workspace, "package.json"), data: bunPackageManifest},
		{path: filepath.Join(workspace, "bun.lock"), data: bunLockfile},
		{path: filepath.Join(workspace, "main.js"), data: entry},
		{path: filepath.Join(runtimeDirectory, "package.json"), data: bunRuntimeManifest},
		{path: filepath.Join(runtimeDirectory, "index.js"), data: bunRuntimeModule},
		{path: filepath.Join(workspace, "bunfig.toml"), data: bunConfig},
		{path: filepath.Join(workspace, "tsconfig.json"), data: bunTypeScriptConfig},
	}
	for _, file := range files {
		if err := os.WriteFile(file.path, []byte(file.data), 0o644); err != nil {
			return backendEmission{}, fmt.Errorf("write compiler-owned Bun workspace: %w", err)
		}
	}
	return backendEmission{primary: filepath.Join(workspace, "main.js")}, nil
}

func buildBunEmission(input backendDriverInput, emission backendEmission, candidate string) error {
	workspace, err := filepath.Abs(filepath.Dir(emission.primary))
	if err != nil {
		return fmt.Errorf("resolve compiler-owned Bun workspace: %w", err)
	}
	home := filepath.Join(workspace, "home")
	install := filepath.Join(workspace, "bun-install")
	cache := filepath.Join(workspace, "cache")
	config := filepath.Join(workspace, "config")
	for _, directory := range []string{home, install, cache, config} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create compiler-owned Bun directory: %w", err)
		}
	}
	environment := bunBuildEnvironment(os.Environ(), map[string]string{
		"BUN_INSTALL":           install,
		"BUN_INSTALL_CACHE_DIR": cache,
		"HOME":                  home,
		"NO_COLOR":              "1",
		"SOURCE_DATE_EPOCH":     "0",
		"XDG_CONFIG_HOME":       config,
	})
	bun := input.toolchain.executables["bun"]
	installCommand := exec.Command(bun, "install", "--frozen-lockfile", "--offline", "--ignore-scripts")
	installCommand.Dir = workspace
	installCommand.Env = environment
	if output, err := installCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("bun install: %w: %s", err, strings.TrimSpace(string(output)))
	}
	buildCommand := exec.Command(bun,
		"build", emission.primary,
		"--compile", "--target="+input.target.name, "--outfile="+candidate,
		"--no-compile-autoload-dotenv", "--no-compile-autoload-bunfig",
		"--no-compile-autoload-tsconfig", "--no-compile-autoload-package-json",
		"--reject-unresolved")
	buildCommand.Dir = workspace
	buildCommand.Env = environment
	if output, err := buildCommand.CombinedOutput(); err != nil {
		return fmt.Errorf("bun build --compile: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func bunBuildEnvironment(environment []string, controlled map[string]string) []string {
	result := make([]string, 0, len(environment)+len(controlled))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		_, controlledName := controlled[name]
		if controlledName || strings.HasPrefix(name, "BUN_") || name == "BUNFIG" || name == "HOME" || name == "NODE_OPTIONS" || name == "NO_COLOR" {
			continue
		}
		result = append(result, entry)
	}
	names := make([]string, 0, len(controlled))
	for name := range controlled {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result = append(result, name+"="+controlled[name])
	}
	return result
}
