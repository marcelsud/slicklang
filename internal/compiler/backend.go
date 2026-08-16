package compiler

import (
	"fmt"
	"os"
	"path/filepath"
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

// ParseBackend accepts the documented CLI names. Empty means Go.
func ParseBackend(name string) (Backend, error) {
	switch name {
	case "", string(BackendGo):
		return BackendGo, nil
	case string(BackendLLVM):
		return BackendLLVM, nil
	default:
		return "", fmt.Errorf("unknown backend %q (want go or llvm)", name)
	}
}

// BuildPath compiles a Slick file or project into a standalone native binary
// using the Go backend.
func BuildPath(path, output string) ([]Diagnostic, error) {
	return BuildPathBackend(path, output, BackendGo)
}

// BuildPathBackend compiles a checked program with the selected native backend.
// Checking, generic instantiation, and standard-library registration stay
// backend-neutral. A missing or incompatible LLVM toolchain fails before any
// output binary is created.
func BuildPathBackend(path, output string, backend Backend) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	return BuildSourcesBackend(sources, output, backend)
}

// BuildSourcesBackend compiles already-loaded sources with the selected backend.
func BuildSourcesBackend(sources []Source, output string, backend Backend) ([]Diagnostic, error) {
	program, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return diagnostics, nil
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	switch backend {
	case BackendGo, "":
		return nil, buildGoBinary(program, output)
	case BackendLLVM:
		return nil, buildLLVMBinary(program, output)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
}
