package compiler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ExecutionEngine is one runnable implementation of a Slick program: the
// interpreter or a registered backend. There is no two-state host/compiled
// boolean; the interpreter is a first-class engine with an empty Backend.
type ExecutionEngine struct {
	Name        string  // "interpreter", "go", "llvm", "rust", "bun"
	Backend     Backend // zero value "" for the interpreter
	Interpreted bool    // true only for the interpreter
	Stability   Stability
	Eligible    bool
	Targets     []BackendTargetDescription // empty for the interpreter
}

// ExecutionEngines returns the interpreter plus every registered backend,
// derived from the backend registry, sorted by Name.
func ExecutionEngines() []ExecutionEngine {
	backends := Backends()
	engines := make([]ExecutionEngine, 0, 1+len(backends))
	engines = append(engines, ExecutionEngine{
		Name:        "interpreter",
		Interpreted: true,
		Stability:   StabilityStable,
		Eligible:    true,
	})
	for _, backend := range backends {
		engines = append(engines, ExecutionEngine{
			Name:      string(backend.Name),
			Backend:   backend.Name,
			Stability: backend.Stability,
			Eligible:  backend.Eligible,
			Targets:   append([]BackendTargetDescription(nil), backend.Targets...),
		})
	}
	sort.Slice(engines, func(left, right int) bool {
		return engines[left].Name < engines[right].Name
	})
	return engines
}

// Run builds (or interprets) the project at path and returns its stdout and
// exit status. It uses a compiler-owned temporary output, requires AllowAlpha
// for alpha engines, and never falls back to another engine.
func (e ExecutionEngine) Run(path string, target string) (stdout string, exitCode int, err error) {
	if e.Interpreted {
		if target != "" {
			return "", 0, fmt.Errorf("interpreter has no targets")
		}
		return runInterpreted(path)
	}
	if e.Backend == "" {
		return "", 0, fmt.Errorf("execution engine %q has no backend", e.Name)
	}
	dir, err := os.MkdirTemp("", "slick-engine-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "app")
	diagnostics, err := BuildPathWithOptions(path, output, BuildOptions{
		Backend:    e.Backend,
		Target:     target,
		AllowAlpha: e.Stability == StabilityAlpha,
	})
	if err != nil {
		return "", 0, err
	}
	if len(diagnostics) > 0 {
		return "", 0, engineDiagnosticsError(diagnostics)
	}
	return runExecutable(output)
}

func runInterpreted(path string) (string, int, error) {
	outcome, diagnostics, err := RunPathArguments(path, nil)
	if len(diagnostics) > 0 {
		return "", 0, engineDiagnosticsError(diagnostics)
	}
	if outcome.Status != nil {
		stdout := string(outcome.Status.Output) + string(outcome.Status.ErrorOutput)
		if err != nil {
			return stdout + err.Error() + "\n", 1, nil
		}
		return stdout, outcome.Status.ExitCode, nil
	}
	if err != nil {
		return err.Error() + "\n", 1, nil
	}
	if outcome.Text == "" {
		return "", 0, nil
	}
	return outcome.Text + "\n", 0, nil
}

func runExecutable(binary string) (string, int, error) {
	output, err := exec.Command(binary).CombinedOutput()
	if err == nil {
		return string(output), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode(), nil
	}
	return string(output), 0, err
}

func engineDiagnosticsError(diagnostics []Diagnostic) error {
	parts := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		parts[index] = fmt.Sprintf("%s:%d:%d: %s %s", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}
