package compiler

import "sort"

// OperationCoverage reports, for one engine, the registry operations it implements,
// the stable-declaration operations it is missing, and the alpha-declaration gaps.
type OperationCoverage struct {
	Engine        string
	Implemented   []string
	MissingStable []string
	AlphaGaps     []string
}

// EngineOperationCoverage returns coverage for the interpreter plus every
// registered backend, derived from the runtime operation registry and each
// engine's implementation table. Lists are sorted. Registries are not mutated.
func EngineOperationCoverage() []OperationCoverage {
	stabilities := operationDeclarationStabilities()
	engines := coverageEngines()
	coverages := make([]OperationCoverage, 0, len(engines))
	for _, engine := range engines {
		coverages = append(coverages, engine.coverage(stabilities))
	}
	return coverages
}

type coverageEngine struct {
	name  string
	table runtimeOperationTable
}

func coverageEngines() []coverageEngine {
	engines := []coverageEngine{{
		name:  "interpreter",
		table: interpreterRuntimeOperations,
	}}
	for _, backend := range backendRegistry {
		engines = append(engines, coverageEngine{
			name:  string(backend.name),
			table: backend.operations,
		})
	}
	sort.Slice(engines, func(left, right int) bool {
		return engines[left].name < engines[right].name
	})
	return engines
}

func operationDeclarationStabilities() map[runtimeOperationID]Stability {
	stabilities := make(map[runtimeOperationID]Stability)
	for _, record := range standardSymbolRecords(standardLibraryRegistry) {
		if record.native == "" {
			continue
		}
		stabilities[record.native] = record.stability
	}
	return stabilities
}

func (engine coverageEngine) coverage(stabilities map[runtimeOperationID]Stability) OperationCoverage {
	operations := make([]runtimeOperationID, 0, len(runtimeOperationRegistry))
	for operation := range runtimeOperationRegistry {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left] < operations[right]
	})

	coverage := OperationCoverage{
		Engine:        engine.name,
		Implemented:   make([]string, 0, len(operations)),
		MissingStable: make([]string, 0),
		AlphaGaps:     make([]string, 0),
	}
	for _, operation := range operations {
		name := string(operation)
		if engine.table.implements(operation) {
			coverage.Implemented = append(coverage.Implemented, name)
			continue
		}
		if stabilities[operation] == StabilityAlpha {
			coverage.AlphaGaps = append(coverage.AlphaGaps, name)
			continue
		}
		coverage.MissingStable = append(coverage.MissingStable, name)
	}
	return coverage
}
