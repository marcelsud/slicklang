package compiler

import (
	"slices"
	"strings"
	"testing"
)

func TestEngineOperationCoverage(t *testing.T) {
	declared := snapshotDeclaredStabilities()
	registry := snapshotRegistryOperations()
	public := publicStdOperations()
	coverages := EngineOperationCoverage()

	if got := coverageEngineNames(coverages); !slices.Equal(got, []string{"bun", "go", "interpreter", "llvm"}) {
		t.Fatalf("engines = %v, want bun, go, interpreter, llvm", got)
	}

	for _, coverage := range coverages {
		if !slices.IsSorted(coverage.Implemented) || !slices.IsSorted(coverage.MissingStable) || !slices.IsSorted(coverage.AlphaGaps) {
			t.Fatalf("%s coverage lists are not sorted: %+v", coverage.Engine, coverage)
		}
		accounted := accountOperations(t, coverage)
		for _, operation := range registry {
			if _, ok := accounted[operation]; !ok {
				t.Fatalf("%s left %s unaccounted", coverage.Engine, operation)
			}
		}
		if extra := extraOperations(accounted, registry); len(extra) > 0 {
			t.Fatalf("%s reported unknown operations:\n%s", coverage.Engine, strings.Join(extra, "\n"))
		}
		for _, operation := range public {
			if _, ok := accounted[operation]; !ok {
				t.Fatalf("%s left public std operation %s unaccounted", coverage.Engine, operation)
			}
		}

		stability := declaredEngineStability(coverage.Engine)
		if stability == StabilityStable && len(coverage.MissingStable) > 0 {
			t.Fatalf("stable engine %s missing required operations:\n%s", coverage.Engine, strings.Join(coverage.MissingStable, "\n"))
		}
		if stability == StabilityAlpha {
			for _, operation := range claimedEngineOperations(coverage.Engine) {
				name := string(operation)
				if _, ok := runtimeOperationRegistry[operation]; !ok {
					t.Fatalf("alpha engine %s claims unknown operation %s", coverage.Engine, name)
				}
				if !slices.Contains(coverage.Implemented, name) {
					t.Fatalf("alpha engine %s claims %s but does not implement it", coverage.Engine, name)
				}
			}
		}
		if stability == StabilityStable {
			for _, operation := range public {
				switch {
				case slices.Contains(coverage.Implemented, operation),
					slices.Contains(coverage.AlphaGaps, operation):
				default:
					t.Fatalf("stable engine %s left public std operation %s unaccounted (not implemented and not an alpha gap):\nmissing stable: %s",
						coverage.Engine, operation, strings.Join(coverage.MissingStable, "\n"))
				}
			}
		}
	}

	assertDeclaredStabilitiesUnchanged(t, declared)
}

func TestMatrixCoverageEligibility(t *testing.T) {
	declared := snapshotDeclaredStabilities()
	coverages := EngineOperationCoverage()
	backends := make(map[string]BackendDescription)
	for _, backend := range Backends() {
		backends[string(backend.Name)] = backend
	}

	for _, coverage := range coverages {
		eligible := len(coverage.MissingStable) == 0
		stability := declaredEngineStability(coverage.Engine)
		if coverage.Engine == "interpreter" {
			if !eligible {
				t.Fatalf("interpreter has incomplete required coverage:\n%s", strings.Join(coverage.MissingStable, "\n"))
			}
			continue
		}
		backend, ok := backends[coverage.Engine]
		if !ok {
			t.Fatalf("engine %s is missing from the backend registry", coverage.Engine)
		}
		if backend.Eligible != eligible {
			t.Fatalf("%s coverage eligibility = %v (missing stable: %s), backend eligible = %v",
				coverage.Engine, eligible, strings.Join(coverage.MissingStable, "\n"), backend.Eligible)
		}
		if backend.Stability != stability {
			t.Fatalf("%s declared stability flipped from %s to %s", coverage.Engine, stability, backend.Stability)
		}
		if eligible && stability == StabilityAlpha && backend.Stability != StabilityAlpha {
			t.Fatalf("eligible alpha engine %s was promoted to %s", coverage.Engine, backend.Stability)
		}
	}

	assertDeclaredStabilitiesUnchanged(t, declared)
}

func accountOperations(t *testing.T, coverage OperationCoverage) map[string]string {
	t.Helper()
	accounted := make(map[string]string, len(coverage.Implemented)+len(coverage.MissingStable)+len(coverage.AlphaGaps))
	add := func(operation, bucket string) {
		if previous, exists := accounted[operation]; exists {
			t.Fatalf("%s lists %s as both %s and %s", coverage.Engine, operation, previous, bucket)
		}
		accounted[operation] = bucket
	}
	for _, operation := range coverage.Implemented {
		add(operation, "implemented")
	}
	for _, operation := range coverage.MissingStable {
		add(operation, "missing-stable")
	}
	for _, operation := range coverage.AlphaGaps {
		add(operation, "alpha-gap")
	}
	return accounted
}

func extraOperations(accounted map[string]string, registry []string) []string {
	known := make(map[string]struct{}, len(registry))
	for _, operation := range registry {
		known[operation] = struct{}{}
	}
	var extra []string
	for operation := range accounted {
		if _, ok := known[operation]; !ok {
			extra = append(extra, operation)
		}
	}
	slices.Sort(extra)
	return extra
}

func coverageEngineNames(coverages []OperationCoverage) []string {
	names := make([]string, len(coverages))
	for index, coverage := range coverages {
		names[index] = coverage.Engine
	}
	return names
}

func snapshotRegistryOperations() []string {
	operations := make([]string, 0, len(runtimeOperationRegistry))
	for operation := range runtimeOperationRegistry {
		operations = append(operations, string(operation))
	}
	slices.Sort(operations)
	return operations
}

func publicStdOperations() []string {
	operations := make([]string, 0)
	seen := make(map[string]struct{})
	for _, record := range standardSymbolRecords(standardLibraryRegistry) {
		if record.native == "" {
			continue
		}
		name := string(record.native)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		operations = append(operations, name)
	}
	slices.Sort(operations)
	return operations
}

func claimedEngineOperations(engine string) []runtimeOperationID {
	var claimed []runtimeOperationID
	switch engine {
	case string(BackendBun):
		for operation := range bunStdOperations {
			claimed = append(claimed, operation)
		}
	default:
		for operation := range advertisedEngineOperations(engine) {
			claimed = append(claimed, operation)
		}
	}
	slices.Sort(claimed)
	return claimed
}

func advertisedEngineOperations(engine string) runtimeOperationTable {
	switch engine {
	case "interpreter":
		return interpreterRuntimeOperations
	case string(BackendGo):
		return goRuntimeOperations
	case string(BackendLLVM):
		return llvmRuntimeOperations
	case string(BackendBun):
		return bunRuntimeOperations
	default:
		return nil
	}
}

func declaredEngineStability(engine string) Stability {
	if engine == "interpreter" {
		return StabilityStable
	}
	for _, backend := range backendRegistry {
		if string(backend.name) == engine {
			return backend.stability
		}
	}
	return ""
}

type declaredStabilitySnapshot struct {
	backends map[Backend]Stability
	targets  map[string]Stability
}

func snapshotDeclaredStabilities() declaredStabilitySnapshot {
	snapshot := declaredStabilitySnapshot{
		backends: make(map[Backend]Stability, len(backendRegistry)),
		targets:  make(map[string]Stability),
	}
	for _, backend := range backendRegistry {
		snapshot.backends[backend.name] = backend.stability
		for _, target := range backend.targets {
			snapshot.targets[string(backend.name)+"/"+target.name] = target.stability
		}
	}
	return snapshot
}

func assertDeclaredStabilitiesUnchanged(t *testing.T, before declaredStabilitySnapshot) {
	t.Helper()
	after := snapshotDeclaredStabilities()
	for name, stability := range before.backends {
		if after.backends[name] != stability {
			t.Fatalf("declared stability for backend %s flipped from %s to %s", name, stability, after.backends[name])
		}
	}
	for name, stability := range before.targets {
		if after.targets[name] != stability {
			t.Fatalf("declared stability for target %s flipped from %s to %s", name, stability, after.targets[name])
		}
	}
	if len(after.backends) != len(before.backends) || len(after.targets) != len(before.targets) {
		t.Fatalf("declared stability registry size changed: backends %d→%d targets %d→%d",
			len(before.backends), len(after.backends), len(before.targets), len(after.targets))
	}
}
