package compiler

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Backend selects a native code generator after a diagnostic-free checked
// program exists. The interpreter is not a Backend.
type Backend string

const (
	// BackendGo is the default native path: generate Go and invoke go build.
	BackendGo Backend = "go"
	// BackendLLVM emits LLVM IR and links a standalone native executable.
	BackendLLVM Backend = "llvm"
)

type backendTargetRegistration struct {
	name         string
	stability    Stability
	platform     backendPlatform
	artifactKind ArtifactKind
	toolchain    backendToolchainRegistration
}

type backendRegistration struct {
	name                Backend
	stability           Stability
	targets             []backendTargetRegistration
	runtimeCapabilities []backendRuntimeCapability
	implements          func(nativeFunction) bool
	driver              backendDriverID
}

var backendRegistry = []backendRegistration{
	{
		name:      BackendGo,
		stability: StabilityStable,
		targets: []backendTargetRegistration{{
			name:         hostTargetName(),
			stability:    StabilityStable,
			platform:     backendPlatform{operatingSystem: runtime.GOOS, architecture: hostArchitecture()},
			artifactKind: ArtifactNativeExecutable,
			toolchain:    backendToolchainRegistration{name: "go", version: "system"},
		}},
		runtimeCapabilities: []backendRuntimeCapability{backendCapabilityEmbeddedRuntime, backendCapabilityStructuredTasks},
		implements:          goNativeOperationImplemented,
		driver:              backendDriverGo,
	},
	{
		name:      BackendLLVM,
		stability: StabilityStable,
		targets: []backendTargetRegistration{{
			name:         "linux-x64",
			stability:    StabilityStable,
			platform:     backendPlatform{operatingSystem: "linux", architecture: "x64"},
			artifactKind: ArtifactNativeExecutable,
			toolchain:    backendToolchainRegistration{name: "llvm", version: "18"},
		}},
		runtimeCapabilities: []backendRuntimeCapability{backendCapabilityEmbeddedRuntime, backendCapabilityStructuredTasks},
		implements: func(native nativeFunction) bool {
			return isNativeStdBuffer(native) ||
				native == nativeStdJsonDecode ||
				native == nativeStdJsonEncode ||
				nativeSymbol(native) != ""
		},
		driver: backendDriverLLVM,
	},
}

func goNativeOperationImplemented(native nativeFunction) bool {
	return goNativeFunctionImplemented(native) || goNativeMethodImplemented(native)
}

func goNativeFunctionImplemented(native nativeFunction) bool {
	switch native {
	case nativeStdJsonDecode, nativeStdJsonEncode,
		nativeStdBufferNew, nativeStdBufferPush, nativeStdBufferGet,
		nativeStdBufferSet, nativeStdBufferLength, nativeStdBufferFreeze,
		nativeStdBytesFromUtf8, nativeStdBytesToUtf8, nativeStdBytesLength,
		nativeStdBytesAt, nativeStdBytesConcat, nativeStdBytesSlice, nativeStdBytesFromValues,
		nativeStdUTF8DecodeAt,
		nativeStdUnicodeIsLetter, nativeStdUnicodeIsDigit,
		nativeStdUnicodeIsWhitespace, nativeStdUnicodeIsUpper,
		nativeStdConvertParseInt, nativeStdConvertParseFloat,
		nativeStdConvertIntToString, nativeStdConvertFloatToString,
		nativeStdMathDivide, nativeStdMathRemainder,
		nativeStdEnvGet, nativeStdEnvSet, nativeStdEnvUnset,
		nativeStdFSReadText, nativeStdFSWriteText, nativeStdFSExists,
		nativeStdFSCreateDirectoryAll, nativeStdFSRemove,
		nativeStdFSReadDirectory, nativeStdFSCreateTemporaryDirectory,
		nativeStdPathJoin, nativeStdPathClean, nativeStdPathBase,
		nativeStdPathDirectory, nativeStdPathExtension, nativeStdPathIsAbsolute,
		nativeStdTextTrim, nativeStdTextContains, nativeStdTextStartsWith,
		nativeStdTextEndsWith, nativeStdTextSplit, nativeStdTextJoin,
		nativeStdTextReplaceAll, nativeStdTextCut, nativeStdTextQuote,
		nativeStdIOReaderFromBytes, nativeStdIOWriterToBytes,
		nativeStdIOReadAll, nativeStdIOCopy,
		nativeStdHTTPFetch, nativeStdHTTPServerServe,
		nativeStdHTTPHeaderValues, nativeStdHTTPStatusText,
		nativeStdProcessRun, nativeStdSQLiteOpen:
		return true
	default:
		return false
	}
}

func goNativeMethodImplemented(native nativeFunction) bool {
	switch native {
	case nativeStdIOReaderRead, nativeStdIOReaderClose,
		nativeStdIOWriterWrite, nativeStdIOWriterBytes, nativeStdIOWriterClose,
		nativeStdFSTemporaryDirectoryClose,
		nativeStdSQLiteDatabaseExecute, nativeStdSQLiteDatabaseQuery,
		nativeStdSQLiteDatabaseBegin, nativeStdSQLiteDatabaseClose,
		nativeStdSQLiteTransactionExecute, nativeStdSQLiteTransactionQuery,
		nativeStdSQLiteTransactionCommit, nativeStdSQLiteTransactionRollback,
		nativeStdSQLiteTransactionClose:
		return true
	default:
		return false
	}
}

// BackendTargetDescription exposes one maintainer-declared backend target.
type BackendTargetDescription struct {
	Name      string    `json:"name"`
	Stability Stability `json:"stability"`
	Eligible  bool      `json:"eligible"`
}

// BackendDescription separates maintainer-declared stability from computed
// technical eligibility.
type BackendDescription struct {
	Name      Backend                    `json:"name"`
	Stability Stability                  `json:"stability"`
	Eligible  bool                       `json:"eligible"`
	Targets   []BackendTargetDescription `json:"targets"`
}

// Backends returns the registry in deterministic name order.
func Backends() []BackendDescription {
	descriptions := make([]BackendDescription, 0, len(backendRegistry))
	for _, declaration := range backendRegistry {
		eligible := backendEligible(declaration)
		targets := make([]BackendTargetDescription, 0, len(declaration.targets))
		for _, target := range declaration.targets {
			targets = append(targets, BackendTargetDescription{
				Name: target.name, Stability: target.stability, Eligible: eligible,
			})
		}
		descriptions = append(descriptions, BackendDescription{
			Name: declaration.name, Stability: declaration.stability, Eligible: eligible, Targets: targets,
		})
	}
	sort.Slice(descriptions, func(left, right int) bool {
		return descriptions[left].Name < descriptions[right].Name
	})
	return descriptions
}

func backendEligible(backend backendRegistration) bool {
	for _, record := range standardSymbolRecords(standardLibraryRegistry) {
		if record.stability == StabilityStable && record.native != "" && !backend.implements(record.native) {
			return false
		}
	}
	return true
}

func hostArchitecture() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

func hostTargetName() string {
	return runtime.GOOS + "-" + hostArchitecture()
}

func backendRegistrationFor(name Backend) (backendRegistration, bool) {
	for _, backend := range backendRegistry {
		if backend.name == name {
			return backend, true
		}
	}
	return backendRegistration{}, false
}

// BackendNames returns the authoritative driver names in CLI order.
func BackendNames() []string {
	names := make([]string, 0, len(backendRegistry))
	for _, backend := range backendRegistry {
		names = append(names, string(backend.name))
	}
	sort.Strings(names)
	return names
}

// ParseBackend accepts the names in the authoritative registry. Empty means Go.
func ParseBackend(name string) (Backend, error) {
	if name == "" {
		return BackendGo, nil
	}
	if backend, ok := backendRegistrationFor(Backend(name)); ok {
		return backend.name, nil
	}
	return "", fmt.Errorf("unknown backend %q (want %s)", name, strings.Join(BackendNames(), " or "))
}

// BuildOptions contains choices that can change target availability without
// changing Slick source.
type BuildOptions struct {
	Backend    Backend
	Target     string
	AllowAlpha bool
}

// BuildPath compiles a Slick file or project into a standalone native binary
// using the stable Go backend.
func BuildPath(path, output string) ([]Diagnostic, error) {
	return BuildPathWithOptions(path, output, BuildOptions{Backend: BackendGo})
}

// BuildPathBackend preserves the existing API and rejects alpha backends.
func BuildPathBackend(path, output string, backend Backend) ([]Diagnostic, error) {
	return BuildPathWithOptions(path, output, BuildOptions{Backend: backend})
}

// BuildPathWithOptions compiles with an explicit alpha policy.
func BuildPathWithOptions(path, output string, options BuildOptions) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	return BuildSourcesWithOptions(sources, output, options)
}

// BuildSourcesBackend compiles loaded sources and rejects alpha backends.
func BuildSourcesBackend(sources []Source, output string, backend Backend) ([]Diagnostic, error) {
	return BuildSourcesWithOptions(sources, output, BuildOptions{Backend: backend})
}

// BuildSourcesWithOptions validates governance and the selected driver before
// creating a compiler-owned workspace or touching the requested output.
func BuildSourcesWithOptions(sources []Source, output string, options BuildOptions) ([]Diagnostic, error) {
	if err := validateStabilityRegistries(); err != nil {
		return nil, err
	}
	backend := options.Backend
	if backend == "" {
		backend = BackendGo
	}
	declaration, ok := backendRegistrationFor(backend)
	if !ok {
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if declaration.stability == StabilityAlpha && !options.AllowAlpha {
		return nil, fmt.Errorf("backend %s is alpha; pass --allow-alpha to use it", backend)
	}
	target, err := backendTargetFor(declaration, options.Target)
	if err != nil {
		return nil, err
	}
	if target.stability == StabilityAlpha && !options.AllowAlpha {
		return nil, fmt.Errorf("backend %s target %s is alpha; pass --allow-alpha to use it", backend, target.name)
	}

	program, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return diagnostics, nil
	}
	if err := program.validateStandardUsage(backend, target.name, options.AllowAlpha, true); err != nil {
		return nil, err
	}
	core, err := program.lowerCore()
	if err != nil {
		return nil, fmt.Errorf("lower Core IR: %w", err)
	}
	driver, ok := registeredBackendDriver(declaration.driver, program)
	if !ok {
		return nil, fmt.Errorf("backend %s has no build driver", backend)
	}
	plan := backendBuildPlan{
		input: backendDriverInput{
			core:         core,
			target:       target,
			runtime:      backendRuntimeInputs{usesJSON: program.usesStdJSON(), usesSQLite: program.usesStdSQLite, usesHTTP: program.usesStdHTTP},
			artifactKind: target.artifactKind,
		},
		output: output,
	}
	return nil, executeBuildPlan(driver, plan)
}
