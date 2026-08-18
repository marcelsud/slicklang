package compiler

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ArtifactKind states what a backend produces. Artifact identity is separate
// from a file suffix: every current backend emits a native executable, while
// future drivers may emit Wasm components or runtime-dependent programs.
type ArtifactKind string

const (
	ArtifactNativeExecutable ArtifactKind = "native-executable"
	ArtifactWasmComponent    ArtifactKind = "wasm-component"
	ArtifactRuntimeProgram   ArtifactKind = "runtime-dependent"
)

type backendPlatform struct {
	operatingSystem string
	architecture    string
}

type backendToolchainRegistration struct {
	name    string
	version string
}

type backendToolchain struct {
	registration backendToolchainRegistration
	executables  map[string]string
	version      string
	llvm         llvmToolchain
}

type backendRuntimeCapability string

const (
	backendCapabilityEmbeddedRuntime backendRuntimeCapability = "embedded-runtime"
	backendCapabilityStructuredTasks backendRuntimeCapability = "structured-tasks"
)

type backendRuntimeInputs struct {
	abiVersion int
	operations []runtimeOperationID
	families   map[runtimeFamily]bool
	usesJSON   bool
	usesSQLite bool
	usesHTTP   bool
}

// backendDriverInput is the complete input visible to one driver invocation.
// It deliberately excludes the requested output path; the coordinator owns it.
type backendDriverInput struct {
	core         coreProgram
	target       backendTargetRegistration
	toolchain    backendToolchain
	runtime      backendRuntimeInputs
	artifactKind ArtifactKind
}

type backendBuildPlan struct {
	input  backendDriverInput
	output string
}

type backendEmission struct {
	primary string
}

// backendDriver makes every externally observable build phase explicit. The
// coordinator owns the workspace and installation, so a driver can only create
// a candidate artifact and cannot partially replace the requested output.
type backendDriver struct {
	checkCore func(backendDriverInput) error
	validate  func(backendDriverInput) (backendToolchain, error)
	emit      func(backendDriverInput, string) (backendEmission, error)
	build     func(backendDriverInput, backendEmission, string) error
	verify    func(backendDriverInput, string) error
}

type backendDriverID string

const (
	backendDriverGo   backendDriverID = "go"
	backendDriverLLVM backendDriverID = "llvm"
	backendDriverRust backendDriverID = "rust"
	backendDriverBun  backendDriverID = "bun"
)

var backendDriverRegistry = map[backendDriverID]func() backendDriver{
	backendDriverGo:   goBackendDriver,
	backendDriverLLVM: llvmBackendDriver,
	backendDriverRust: rustBackendDriver,
	backendDriverBun:  bunBackendDriver,
}

func registeredBackendDriver(id backendDriverID) (backendDriver, bool) {
	factory, ok := backendDriverRegistry[id]
	if !ok {
		return backendDriver{}, false
	}
	return factory(), true
}

func executeBuildPlan(driver backendDriver, plan backendBuildPlan) error {
	if driver.validate == nil || driver.emit == nil || driver.build == nil || driver.verify == nil {
		return errorsForBackendPlan("driver does not implement every build phase")
	}
	input := plan.input
	toolchain, err := driver.validate(input)
	if err != nil {
		return fmt.Errorf("validate backend target %s: %w", input.target.name, err)
	}
	input.toolchain = toolchain

	output, err := filepath.Abs(plan.output)
	if err != nil {
		return err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp(parent, ".slick-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)

	emission, err := driver.emit(input, workspace)
	if err != nil {
		return fmt.Errorf("emit %s: %w", input.target.name, err)
	}
	candidate := filepath.Join(workspace, "artifact")
	if err := driver.build(input, emission, candidate); err != nil {
		return fmt.Errorf("build %s: %w", input.target.name, err)
	}
	if err := driver.verify(input, candidate); err != nil {
		return fmt.Errorf("verify %s artifact: %w", input.target.name, err)
	}
	if err := os.Rename(candidate, output); err != nil {
		return fmt.Errorf("install %s artifact: %w", input.target.name, err)
	}
	return nil
}

func errorsForBackendPlan(message string) error {
	return fmt.Errorf("invalid backend build plan: %s", message)
}

func verifyNativeExecutable(input backendDriverInput, path string) error {
	if input.artifactKind != ArtifactNativeExecutable {
		return fmt.Errorf("driver produced a native executable for artifact kind %s", input.artifactKind)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("candidate %s is not a regular file", path)
	}
	if input.target.platform.operatingSystem == "windows" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		var magic [2]byte
		if _, err := io.ReadFull(file, magic[:]); err != nil {
			return fmt.Errorf("read Windows executable header: %w", err)
		}
		if string(magic[:]) != "MZ" {
			return fmt.Errorf("candidate %s is not a Windows executable", path)
		}
	} else if info.Mode()&0o111 == 0 {
		return fmt.Errorf("candidate %s is not executable", path)
	}
	return nil
}

func goBackendDriver() backendDriver {
	return backendDriver{
		checkCore: func(input backendDriverInput) error {
			return validateNativeCore(input.core, input.runtime)
		},
		validate: func(input backendDriverInput) (backendToolchain, error) {
			if input.target.name != hostTargetName() {
				return backendToolchain{}, fmt.Errorf("Go target %s requires host %s", input.target.name, hostTargetName())
			}
			tool, err := exec.LookPath("go")
			if err != nil {
				return backendToolchain{}, fmt.Errorf("Go toolchain not found: need go on PATH")
			}
			return backendToolchain{
				registration: input.target.toolchain,
				executables:  map[string]string{"go": tool},
			}, nil
		},
		emit: func(input backendDriverInput, workspace string) (backendEmission, error) {
			return emitGoSource(input.core, input.runtime, workspace)
		},
		build: func(input backendDriverInput, emission backendEmission, candidate string) error {
			command := exec.Command(input.toolchain.executables["go"], "build", "-buildvcs=false", "-trimpath", "-o", candidate, emission.primary)
			command.Dir = filepath.Dir(emission.primary)
			output, err := command.CombinedOutput()
			if err != nil {
				return fmt.Errorf("go build: %w: %s", err, strings.TrimSpace(string(output)))
			}
			return nil
		},
		verify: verifyNativeExecutable,
	}
}

func llvmBackendDriver() backendDriver {
	return backendDriver{
		checkCore: func(input backendDriverInput) error {
			return validateNativeCore(input.core, input.runtime)
		},
		validate: func(input backendDriverInput) (backendToolchain, error) {
			tool, err := locateLLVMToolchain()
			if err != nil {
				return backendToolchain{}, err
			}
			if input.runtime.usesSQLite && !sqliteDevPresent() {
				return backendToolchain{}, fmt.Errorf("LLVM backend requires libsqlite3 (sqlite3.h and -lsqlite3)")
			}
			if input.runtime.usesHTTP && !curlDevPresent() {
				return backendToolchain{}, fmt.Errorf("LLVM backend requires libcurl development files (curl/curl.h and -lcurl)")
			}
			if _, _, err := llvmJSONFlags(input.runtime.usesJSON); err != nil {
				return backendToolchain{}, err
			}
			return backendToolchain{registration: input.target.toolchain, version: tool.version, llvm: tool}, nil
		},
		emit: func(input backendDriverInput, workspace string) (backendEmission, error) {
			return emitLLVMSource(input.core, workspace)
		},
		build: func(input backendDriverInput, emission backendEmission, candidate string) error {
			return buildLLVMEmission(input.toolchain.llvm, input.runtime, emission, candidate)
		},
		verify: verifyNativeExecutable,
	}
}

func backendTargetFor(registration backendRegistration, requested string) (backendTargetRegistration, error) {
	if requested == "" && len(registration.targets) > 0 {
		return registration.targets[0], nil
	}
	for _, target := range registration.targets {
		if target.name == requested {
			return target, nil
		}
	}
	names := make([]string, 0, len(registration.targets))
	for _, target := range registration.targets {
		names = append(names, target.name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return backendTargetRegistration{}, fmt.Errorf("backend %s has no supported targets", registration.name)
	}
	return backendTargetRegistration{}, fmt.Errorf("backend %s does not support target %q (want %s)", registration.name, requested, strings.Join(names, " or "))
}
