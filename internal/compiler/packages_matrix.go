package compiler

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// PackageAdapterAvailability reports one canonical interface and one adapter claim,
// keyed by canonical interface hash, backend, target, and adapter stability, with the
// computed eligibility of both the interface and that adapter.
type PackageAdapterAvailability struct {
	Package           string
	Interface         string
	InterfaceHash     string
	InterfaceEligible bool
	Backend           string
	Target            string
	AdapterStability  Stability
	AdapterEligible   bool
	Conforms          bool
}

// ProjectAdapterAvailability reports every claimed adapter in the project's
// resolved package closure. Eligibility is computed from conformance and
// declared coverage; declared stability is never changed.
func ProjectAdapterAvailability(projectPath string) ([]PackageAdapterAvailability, error) {
	resolver, interfaceSources, err := resolveProjectPackages(projectPath)
	if err != nil {
		return nil, err
	}
	interfaceProgram, diagnostics := compile(interfaceSources)
	interfacesCompile := len(diagnostics) == 0

	var rows []PackageAdapterAvailability
	for _, resolved := range resolver.order {
		interfaceEligible := interfacesCompile && packageInterfaceEligible(resolved, interfaceProgram)
		want, surfaceErr := publicPackageSurface(interfaceProgram, resolved.manifest.Name)
		if !interfacesCompile || surfaceErr != nil {
			want = nil
		}
		for index := range resolved.manifest.Adapters {
			adapter := &resolved.manifest.Adapters[index]
			conforms := adapterConforms(resolved, adapter, interfaceSources, want)
			adapterEligible := interfaceEligible && conforms && adapterTechnicallyReady(resolved, adapter)
			for _, target := range adapter.Targets {
				rows = append(rows, PackageAdapterAvailability{
					Package:           resolved.manifest.Name,
					Interface:         resolved.manifest.Name,
					InterfaceHash:     resolved.interfaceHash,
					InterfaceEligible: interfaceEligible,
					Backend:           string(adapter.Backend),
					Target:            target,
					AdapterStability:  adapter.Stability,
					AdapterEligible:   adapterEligible,
					Conforms:          conforms,
				})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.Backend != right.Backend {
			return left.Backend < right.Backend
		}
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.AdapterStability < right.AdapterStability
	})
	return rows, nil
}

func resolveProjectPackages(projectPath string) (*packageResolver, []Source, error) {
	var project projectManifest
	manifestPath := filepath.Join(projectPath, projectManifestName)
	if err := readStrictJSON(manifestPath, &project); err != nil {
		return nil, nil, err
	}
	if err := validateProjectManifest(project); err != nil {
		return nil, nil, fmt.Errorf("project manifest %s: %w", manifestPath, err)
	}
	resolver := &packageResolver{
		allowAlpha: true,
		packages:   make(map[string]*resolvedPackage),
		visiting:   make(map[string]bool),
	}
	for _, dependency := range project.Dependencies {
		if err := resolver.resolveDependency(projectPath, dependency, []string{project.Name}); err != nil {
			return nil, nil, err
		}
	}
	var interfaceSources []Source
	for _, resolved := range resolver.order {
		packageSources := append([]Source(nil), resolved.interfaceSources...)
		imports := packageDependencySet(resolved.manifest.Dependencies)
		for index := range packageSources {
			packageSources[index].packageImports = imports
		}
		interfaceSources = append(interfaceSources, packageSources...)
	}
	return resolver, interfaceSources, nil
}

func packageInterfaceEligible(resolved *resolvedPackage, interfaceProgram *program) bool {
	if interfaceProgram == nil {
		return false
	}
	encoded, err := publicPackageSurface(interfaceProgram, resolved.manifest.Name)
	if err != nil {
		return false
	}
	if err := validatePackageSurfaceMetadata(interfaceProgram, resolved.manifest, encoded); err != nil {
		return false
	}
	conformanceSources := append([]Source(nil), resolved.conformanceSources...)
	imports := packageNameSet(nil)
	imports[resolved.manifest.Name] = true
	for index := range conformanceSources {
		conformanceSources[index].packageImports = imports
	}
	sources := append(append([]Source(nil), resolved.interfaceSources...), conformanceSources...)
	program, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return false
	}
	main := program.functions["root.main"]
	return main != nil && len(main.params) == 0 &&
		program.canonicalType(main.namespace, main.aliases, main.result) == "bool" &&
		len(main.throws) == 0 && len(main.operations) == 0
}

func adapterTechnicallyReady(resolved *resolvedPackage, adapter *packageAdapter) bool {
	return adapter.Kind == "slick" &&
		adapter.InterfaceSHA256 == resolved.interfaceHash &&
		adapter.ConformanceSHA256 == resolved.conformanceHash
}

func adapterConforms(resolved *resolvedPackage, adapter *packageAdapter, interfaceSources []Source, want []byte) bool {
	if adapter.Kind != "slick" || len(want) == 0 {
		return false
	}
	adapterSources := append([]Source(nil), resolved.adapterSourceSets[adapter.ID]...)
	imports := packageNameSet(adapter.Dependencies)
	for index := range adapterSources {
		adapterSources[index].packageImports = imports
	}
	candidates := make([]Source, 0, len(interfaceSources)+len(adapterSources))
	for _, source := range interfaceSources {
		if source.Package != resolved.manifest.Name {
			candidates = append(candidates, source)
		}
	}
	candidates = append(candidates, adapterSources...)
	adapterProgram, diagnostics := compile(candidates)
	if len(diagnostics) > 0 {
		return false
	}
	got, err := publicPackageSurface(adapterProgram, resolved.manifest.Name)
	if err != nil || !bytes.Equal(got, want) {
		return false
	}
	conformanceSources := append([]Source(nil), resolved.conformanceSources...)
	contractImports := packageNameSet(adapter.Dependencies)
	contractImports[resolved.manifest.Name] = true
	for index := range conformanceSources {
		conformanceSources[index].packageImports = contractImports
	}
	contractSources := append(append([]Source(nil), candidates...), conformanceSources...)
	contractProgram, diagnostics := compile(contractSources)
	if len(diagnostics) > 0 {
		return false
	}
	main := contractProgram.functions["root.main"]
	if main == nil || len(main.params) != 0 ||
		contractProgram.canonicalType(main.namespace, main.aliases, main.result) != "bool" ||
		len(main.throws) != 0 || len(main.operations) != 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ctx = withRuntimeStepBudget(ctx, packageConformanceStepLimit)
	result, err := contractProgram.callFunctionContext(ctx, main, nil, nil, nil)
	cancel()
	if err != nil {
		return false
	}
	passed, ok := result.scalar.(bool)
	return result.typ == "bool" && ok && passed
}
