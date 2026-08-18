package compiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	projectManifestName         = "slick.project.json"
	packageManifestName         = "slick.package.json"
	packageLockName             = "slick.lock"
	packageSchemaVersion        = 1
	packageConformanceStepLimit = 10_000
)

type projectManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Name          string              `json:"name"`
	Source        string              `json:"source"`
	Dependencies  []packageDependency `json:"dependencies"`
}

type packageDependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

type packageManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Name          string              `json:"name"`
	Version       string              `json:"version"`
	Stability     Stability           `json:"stability"`
	Interface     packageInterface    `json:"interface"`
	Dependencies  []packageDependency `json:"dependencies"`
	Adapters      []packageAdapter    `json:"adapters"`
}

type packageInterface struct {
	Path              string   `json:"path"`
	SHA256            string   `json:"sha256"`
	Effects           []string `json:"effects"`
	Resources         []string `json:"resources"`
	ConformancePath   string   `json:"conformance_path"`
	ConformanceSHA256 string   `json:"conformance_sha256"`
}

type packageAdapter struct {
	ID                string         `json:"id"`
	Backend           Backend        `json:"backend"`
	Targets           []string       `json:"targets"`
	Stability         Stability      `json:"stability"`
	Kind              string         `json:"kind"`
	Entry             string         `json:"entry"`
	Dependencies      []string       `json:"dependencies"`
	Checksum          string         `json:"checksum"`
	Assets            []packageAsset `json:"assets"`
	InterfaceSHA256   string         `json:"interface_sha256"`
	ConformanceSHA256 string         `json:"conformance_sha256"`
	ABI               string         `json:"abi"`
	Protocol          string         `json:"protocol,omitempty"`
}

type packageAsset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type packageBuildSelection struct {
	backend    Backend
	target     string
	allowAlpha bool
}

type packageLoadResult struct {
	sources  []Source
	lockPath string
	lockData []byte
	lockBase []byte
}
type resolvedPackage struct {
	root               string
	manifest           packageManifest
	interfaceHash      string
	conformanceHash    string
	interfaceSources   []Source
	conformanceSources []Source
	adapter            *packageAdapter
	adapterSources     []Source
	adapterSourceSets  map[string][]Source
}

type packageResolver struct {
	selection  *packageBuildSelection
	allowAlpha bool
	packages   map[string]*resolvedPackage
	visiting   map[string]bool
	order      []*resolvedPackage
}

type packageLock struct {
	SchemaVersion int                `json:"schema_version"`
	Packages      []packageLockEntry `json:"packages"`
}

type packageLockEntry struct {
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	InterfaceHash string                 `json:"interface_sha256"`
	Selections    []packageLockSelection `json:"selections"`
}

type packageLockSelection struct {
	Backend  Backend `json:"backend"`
	Target   string  `json:"target"`
	Adapter  string  `json:"adapter"`
	Checksum string  `json:"checksum"`
}

func loadPackageProject(root string, selection *packageBuildSelection, allowAlpha bool) (packageLoadResult, error) {
	manifestPath := filepath.Join(root, projectManifestName)
	var project projectManifest
	if err := readStrictJSON(manifestPath, &project); err != nil {
		return packageLoadResult{}, err
	}
	if err := validateProjectManifest(project); err != nil {
		return packageLoadResult{}, fmt.Errorf("project manifest %s: %w", manifestPath, err)
	}
	sourceRoot, err := packageRelativePath(root, project.Source, "project source")
	if err != nil {
		return packageLoadResult{}, err
	}
	sources, err := loadNamespacedSources(sourceRoot, "root", "")
	if err != nil {
		return packageLoadResult{}, err
	}
	projectImports := packageDependencySet(project.Dependencies)
	for index := range sources {
		sources[index].packageImports = projectImports
	}
	resolver := packageResolver{
		selection:  selection,
		allowAlpha: allowAlpha,
		packages:   make(map[string]*resolvedPackage),
		visiting:   make(map[string]bool),
	}
	for _, dependency := range project.Dependencies {
		if err := resolver.resolveDependency(root, dependency, []string{project.Name}); err != nil {
			return packageLoadResult{}, err
		}
	}
	var interfaceSources []Source
	for _, resolved := range resolver.order {
		interfacePackageSources := append([]Source(nil), resolved.interfaceSources...)
		interfaceImports := packageDependencySet(resolved.manifest.Dependencies)
		for index := range interfacePackageSources {
			interfacePackageSources[index].packageImports = interfaceImports
		}
		packageSources := append([]Source(nil), interfacePackageSources...)
		if resolved.adapter != nil {
			packageSources = append([]Source(nil), resolved.adapterSources...)
		}
		if resolved.adapter != nil {
			adapterImports := packageNameSet(resolved.adapter.Dependencies)
			for index := range packageSources {
				packageSources[index].packageBackend = selection.backend
				packageSources[index].packageTarget = selection.target
				packageSources[index].packageImports = adapterImports
			}
		}
		interfaceSources = append(interfaceSources, interfacePackageSources...)
		sources = append(sources, packageSources...)
	}
	if len(resolver.packages) > 0 {
		if err := validatePackageContracts(resolver.order, interfaceSources); err != nil {
			return packageLoadResult{}, err
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	result := packageLoadResult{sources: sources}
	if selection == nil || len(resolver.packages) == 0 {
		return result, nil
	}
	lockPath := filepath.Join(root, packageLockName)
	lockBase, err := os.ReadFile(lockPath)
	if errors.Is(err, fs.ErrNotExist) {
		lockBase, err = nil, nil
	}
	if err != nil {
		return packageLoadResult{}, err
	}
	lockData, err := resolver.prepareLock(lockPath, lockBase)
	if err != nil {
		return packageLoadResult{}, err
	}
	result.lockPath, result.lockData, result.lockBase = lockPath, lockData, lockBase
	return result, nil
}

func (r *packageResolver) resolveDependency(base string, dependency packageDependency, chain []string) error {
	if !validPackageName(dependency.Name) || !validSemanticVersion(dependency.Version) || dependency.Path == "" {
		return fmt.Errorf("invalid package dependency %+v", dependency)
	}
	root, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(dependency.Path)))
	if err != nil {
		return err
	}
	if r.visiting[root] {
		return fmt.Errorf("package dependency cycle: %s", strings.Join(append(chain, dependency.Name+"@"+dependency.Version), " -> "))
	}
	if existing := r.packages[dependency.Name]; existing != nil {
		if existing.manifest.Version != dependency.Version || existing.root != root {
			return fmt.Errorf("package %s resolves inconsistently: %s at %s and %s at %s",
				dependency.Name, existing.manifest.Version, existing.root, dependency.Version, root)
		}
		return nil
	}
	r.visiting[root] = true
	defer delete(r.visiting, root)
	manifestPath := filepath.Join(root, packageManifestName)
	var manifest packageManifest
	if err := readStrictJSON(manifestPath, &manifest); err != nil {
		return err
	}
	if err := validatePackageManifest(manifest); err != nil {
		return fmt.Errorf("package manifest %s: %w", manifestPath, err)
	}
	if manifest.Stability == StabilityAlpha && !r.allowAlpha {
		return fmt.Errorf("package interface %s@%s is alpha; pass --allow-alpha to use it", manifest.Name, manifest.Version)
	}
	if manifest.Name != dependency.Name || manifest.Version != dependency.Version {
		return fmt.Errorf("dependency requires %s@%s but %s declares %s@%s",
			dependency.Name, dependency.Version, manifestPath, manifest.Name, manifest.Version)
	}
	for name := range r.packages {
		if strings.HasPrefix(name, manifest.Name+".") || strings.HasPrefix(manifest.Name, name+".") {
			return fmt.Errorf("package namespaces %s and %s overlap", name, manifest.Name)
		}
	}
	interfaceRoot, err := packageRelativePath(root, manifest.Interface.Path, "package interface")
	if err != nil {
		return err
	}
	interfacePrefix := "packages/" + manifest.Name + "/interface/"
	interfaceSources, err := loadNamespacedSources(interfaceRoot, manifest.Name, interfacePrefix)
	if err != nil {
		return fmt.Errorf("load package %s@%s interface: %w", manifest.Name, manifest.Version, err)
	}
	interfaceHash := hashPackageSources(interfaceSources, interfacePrefix)
	if interfaceHash != manifest.Interface.SHA256 {
		return fmt.Errorf("package %s@%s interface hash mismatch: manifest %s, found %s",
			manifest.Name, manifest.Version, manifest.Interface.SHA256, interfaceHash)
	}
	conformanceRoot, err := packageRelativePath(root, manifest.Interface.ConformancePath, "package conformance suite")
	if err != nil {
		return err
	}
	conformancePrefix := "conformance/" + manifest.Name + "/"
	conformanceSources, err := loadNamespacedSources(conformanceRoot, "root", conformancePrefix)
	if err != nil {
		return fmt.Errorf("load package %s@%s conformance suite: %w", manifest.Name, manifest.Version, err)
	}
	conformanceHash := hashPackageSources(conformanceSources, conformancePrefix)
	if conformanceHash != manifest.Interface.ConformanceSHA256 {
		return fmt.Errorf("package %s@%s conformance hash mismatch: manifest %s, found %s",
			manifest.Name, manifest.Version, manifest.Interface.ConformanceSHA256, conformanceHash)
	}
	resolved := &resolvedPackage{
		root: root, manifest: manifest, interfaceHash: interfaceHash, conformanceHash: conformanceHash,
		interfaceSources: interfaceSources, conformanceSources: conformanceSources,
		adapterSourceSets: make(map[string][]Source),
	}
	for index := range resolved.manifest.Adapters {
		if err := validatePackageAdapterArtifact(resolved, &resolved.manifest.Adapters[index]); err != nil {
			return err
		}
	}
	r.packages[manifest.Name] = resolved
	packageChain := append(append([]string(nil), chain...), manifest.Name+"@"+manifest.Version)
	for _, child := range manifest.Dependencies {
		if err := r.resolveDependency(root, child, packageChain); err != nil {
			return err
		}
	}
	if r.selection != nil {
		adapter, err := r.selectAdapter(resolved, packageChain)
		if err != nil {
			return err
		}
		resolved.adapter = adapter
	}
	r.order = append(r.order, resolved)
	return nil
}

func validatePackageAdapterArtifact(resolved *resolvedPackage, adapter *packageAdapter) error {
	entry, err := packageRelativePath(resolved.root, adapter.Entry, "package adapter entry")
	if err != nil {
		return err
	}
	var checksum string
	if adapter.Kind == "slick" {
		if filepath.Clean(filepath.FromSlash(adapter.Entry)) ==
			filepath.Clean(filepath.FromSlash(resolved.manifest.Interface.Path)) {
			resolved.adapterSourceSets[adapter.ID] = append([]Source(nil), resolved.interfaceSources...)
			checksum = resolved.interfaceHash
		} else {
			prefix := "packages/" + resolved.manifest.Name + "/adapter/" + adapter.ID + "/"
			sources, err := loadNamespacedSources(entry, resolved.manifest.Name, prefix)
			if err != nil {
				return fmt.Errorf("load package adapter %s for %s@%s: %w",
					adapter.ID, resolved.manifest.Name, resolved.manifest.Version, err)
			}
			resolved.adapterSourceSets[adapter.ID] = sources
			checksum = hashPackageSources(sources, prefix)
		}
	} else {
		checksum, err = hashPath(entry, false)
		if err != nil {
			return fmt.Errorf("hash package adapter %s for %s@%s: %w",
				adapter.ID, resolved.manifest.Name, resolved.manifest.Version, err)
		}
	}
	if checksum != adapter.Checksum {
		return fmt.Errorf("package adapter %s for %s@%s checksum mismatch: manifest %s, found %s",
			adapter.ID, resolved.manifest.Name, resolved.manifest.Version, adapter.Checksum, checksum)
	}
	for _, asset := range adapter.Assets {
		path, err := packageRelativePath(resolved.root, asset.Path, "package adapter asset")
		if err != nil {
			return err
		}
		found, err := hashPath(path, false)
		if err != nil {
			return fmt.Errorf("hash package adapter asset %s: %w", asset.Path, err)
		}
		if found != asset.SHA256 {
			return fmt.Errorf("package adapter %s asset %s checksum mismatch: manifest %s, found %s",
				adapter.ID, asset.Path, asset.SHA256, found)
		}
	}
	return nil
}

func (r *packageResolver) selectAdapter(resolved *resolvedPackage, chain []string) (*packageAdapter, error) {
	manifest := resolved.manifest
	var matches []*packageAdapter
	for index := range manifest.Adapters {
		adapter := &manifest.Adapters[index]
		if adapter.Backend == r.selection.backend && containsPackageString(adapter.Targets, r.selection.target) {
			matches = append(matches, adapter)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("package %s@%s has no adapter for backend %s target %s\nrequired by: %s\navailable adapters: %s",
			manifest.Name, manifest.Version, r.selection.backend, r.selection.target,
			strings.Join(chain, " -> "), availablePackageAdapters(manifest.Adapters))
	}
	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for index, adapter := range matches {
			ids[index] = adapter.ID
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("package %s@%s has ambiguous adapters for backend %s target %s: %s",
			manifest.Name, manifest.Version, r.selection.backend, r.selection.target, strings.Join(ids, ", "))
	}
	adapter := matches[0]
	if adapter.Stability == StabilityAlpha && !r.selection.allowAlpha {
		return nil, fmt.Errorf("package adapter %s for %s@%s backend %s target %s is alpha; pass --allow-alpha to use it",
			adapter.ID, manifest.Name, manifest.Version, r.selection.backend, r.selection.target)
	}
	if adapter.Kind != "slick" {
		return nil, fmt.Errorf("package adapter %s for %s@%s uses implementation kind %s, which this compiler cannot link",
			adapter.ID, manifest.Name, manifest.Version, adapter.Kind)
	}
	resolved.adapterSources = append([]Source(nil), resolved.adapterSourceSets[adapter.ID]...)
	return adapter, nil
}

func validateProjectManifest(manifest projectManifest) error {
	if manifest.SchemaVersion != packageSchemaVersion {
		return fmt.Errorf("schema_version %d, want %d", manifest.SchemaVersion, packageSchemaVersion)
	}
	if !validProjectName(manifest.Name) || manifest.Source == "" {
		return fmt.Errorf("invalid project name or source path: %q", manifest.Name)
	}
	previous := ""
	seen := make(map[string]bool, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		if !validPackageName(dependency.Name) || !validSemanticVersion(dependency.Version) ||
			dependency.Path == "" || filepath.IsAbs(dependency.Path) {

			return fmt.Errorf("invalid package dependency %+v", dependency)
		}
		key := dependency.Name + "@" + dependency.Version
		if key <= previous || seen[dependency.Name] {
			return errors.New("project dependencies must be sorted by canonical name and unique")
		}
		previous = key
		seen[dependency.Name] = true
	}
	return nil
}
func validatePackageContracts(packages []*resolvedPackage, interfaceSources []Source) error {
	interfaceProgram, diagnostics := compile(interfaceSources)
	if len(diagnostics) > 0 {
		return packageContractDiagnostic("canonical package interfaces", diagnostics[0])
	}
	for _, resolved := range packages {
		want, err := publicPackageSurface(interfaceProgram, resolved.manifest.Name)
		if err != nil {
			return err
		}
		if err := validatePackageSurfaceMetadata(interfaceProgram, resolved.manifest, want); err != nil {
			return err
		}
		for index := range resolved.manifest.Adapters {
			adapter := &resolved.manifest.Adapters[index]
			if adapter.Kind != "slick" {
				continue
			}
			adapterSources := append([]Source(nil), resolved.adapterSourceSets[adapter.ID]...)
			adapterImports := packageNameSet(adapter.Dependencies)
			for sourceIndex := range adapterSources {
				adapterSources[sourceIndex].packageImports = adapterImports
			}
			candidateSources := make([]Source, 0, len(interfaceSources)+len(adapterSources))
			for _, source := range interfaceSources {
				if source.Package != resolved.manifest.Name {
					candidateSources = append(candidateSources, source)
				}
			}
			candidateSources = append(candidateSources, adapterSources...)
			adapterProgram, diagnostics := compile(candidateSources)
			if len(diagnostics) > 0 {
				return packageContractDiagnostic("package adapter "+adapter.ID+" for "+resolved.manifest.Name+"@"+resolved.manifest.Version, diagnostics[0])
			}
			got, err := publicPackageSurface(adapterProgram, resolved.manifest.Name)
			if err != nil {
				return err
			}
			if !bytes.Equal(got, want) {
				return fmt.Errorf("package adapter %s for %s@%s redefines the canonical public interface",
					adapter.ID, resolved.manifest.Name, resolved.manifest.Version)
			}
			conformanceSources := append([]Source(nil), resolved.conformanceSources...)
			contractImports := packageNameSet(adapter.Dependencies)
			contractImports[resolved.manifest.Name] = true
			for sourceIndex := range conformanceSources {
				conformanceSources[sourceIndex].packageImports = contractImports
			}
			contractSources := make([]Source, 0, len(candidateSources)+len(conformanceSources))
			contractSources = append(contractSources, candidateSources...)
			contractSources = append(contractSources, conformanceSources...)
			contractProgram, diagnostics := compile(contractSources)
			if len(diagnostics) > 0 {
				return packageContractDiagnostic("package "+resolved.manifest.Name+"@"+resolved.manifest.Version+
					" adapter "+adapter.ID+" conformance contract", diagnostics[0])
			}
			main := contractProgram.functions["root.main"]
			if main == nil || len(main.params) != 0 ||
				contractProgram.canonicalType(main.namespace, main.aliases, main.result) != "bool" ||
				len(main.throws) != 0 || len(main.operations) != 0 {
				return fmt.Errorf("package %s@%s adapter %s conformance contract must define pure root.main() -> bool",
					resolved.manifest.Name, resolved.manifest.Version, adapter.ID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ctx = withRuntimeStepBudget(ctx, packageConformanceStepLimit)
			result, err := contractProgram.callFunctionContext(ctx, main, nil, nil, nil)
			cancel()
			if err != nil {
				return fmt.Errorf("package %s@%s adapter %s conformance contract failed: %w",
					resolved.manifest.Name, resolved.manifest.Version, adapter.ID, err)
			}
			passed, ok := result.scalar.(bool)
			if result.typ != "bool" || !ok || !passed {
				return fmt.Errorf("package %s@%s adapter %s conformance contract returned %s, want true",
					resolved.manifest.Name, resolved.manifest.Version, adapter.ID, formatRuntimeValue(result))
			}
		}
	}
	return nil
}
func validatePackageSurfaceMetadata(p *program, manifest packageManifest, encoded []byte) error {
	var symbols []SymbolDescription
	if err := json.Unmarshal(encoded, &symbols); err != nil {
		return err
	}
	effects := make(map[string]struct{})
	addCallable := func(callable *CallableTypeDescription) {
		if callable == nil {
			return
		}
		for _, effect := range callable.Effects {
			effects[effect] = struct{}{}
		}
	}
	for _, symbol := range symbols {
		for _, effect := range symbol.Effects {
			effects[effect] = struct{}{}
		}
		addCallable(symbol.TypeCallable)
		addCallable(symbol.ReturnCallable)
		for _, parameter := range symbol.Parameters {
			addCallable(parameter.Callable)
		}
		for _, field := range symbol.Fields {
			addCallable(field.Callable)
		}
		for _, method := range symbol.DeclaredMethods {
			for _, effect := range method.Effects {
				effects[effect] = struct{}{}
			}
			addCallable(method.ReturnCallable)
			for _, parameter := range method.Parameters {
				addCallable(parameter.Callable)
			}
		}
	}
	foundEffects := sortedKeys(effects)
	if strings.Join(foundEffects, "\x00") != strings.Join(manifest.Interface.Effects, "\x00") {
		return fmt.Errorf("package %s@%s interface effects are %v, manifest declares %v",
			manifest.Name, manifest.Version, foundEffects, manifest.Interface.Effects)
	}
	for _, resource := range manifest.Interface.Resources {
		name := resource
		if !strings.ContainsRune(name, '.') {
			name = manifest.Name + "." + name
		}
		declaration := p.classDeclaration(name)
		if declaration == nil || !isPublic(declaration.name) {
			return fmt.Errorf("package %s@%s manifest resource %s is not a public class",
				manifest.Name, manifest.Version, resource)
		}
	}
	return nil
}

func publicPackageSurface(p *program, namespace string) ([]byte, error) {
	names := make(map[string]bool)
	add := func(name, declarationName, instanceOf string) {
		if instanceOf == "" && isPublic(declarationName) &&
			strings.HasPrefix(name, namespace+".") {
			names[name] = true
		}
	}
	for name, declaration := range p.classes {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.genericClasses {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.interfaces {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.genericInterfaces {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.functions {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.genericFunctions {
		add(name, declaration.name, declaration.instanceOf)
	}
	for name, declaration := range p.unions {
		add(name, declaration.name, "")
	}
	for name, declaration := range p.constants {
		add(name, declaration.name, "")
	}
	for name, declaration := range p.annotations {
		add(name, declaration.name, "")
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	surface := make([]SymbolDescription, 0, len(ordered))
	for _, name := range ordered {
		description, ok := p.describeSymbol(name)
		if !ok {
			return nil, fmt.Errorf("describe package interface symbol %s", name)
		}
		normalizePackageSymbol(&description)
		surface = append(surface, description)
	}
	return json.Marshal(surface)
}

func normalizePackageSymbol(description *SymbolDescription) {
	description.Documentation = nil
	description.Source = nil
	description.Stability = ""
	description.Eligible = nil
	description.ImplementedMethods = nil
	for index := range description.Fields {
		description.Fields[index].Documentation = nil
		description.Fields[index].Source = nil
		description.Fields[index].Stability = ""
		description.Fields[index].Eligible = nil
	}
	publicMethods := description.DeclaredMethods[:0]
	for _, method := range description.DeclaredMethods {
		if method.Visibility != "public" {
			continue
		}
		method.Documentation = nil
		method.Source = nil
		method.Stability = ""
		method.Eligible = nil
		publicMethods = append(publicMethods, method)
	}
	description.DeclaredMethods = publicMethods
	publicChildren := description.Children[:0]
	for _, child := range description.Children {
		if child.Visibility != "public" {
			continue
		}
		child.Documentation = nil
		child.Stability = ""
		child.Eligible = nil
		publicChildren = append(publicChildren, child)
	}
	description.Children = publicChildren
}

func packageContractDiagnostic(label string, diagnostic Diagnostic) error {
	return fmt.Errorf("%s do not compile: %s:%d:%d %s %s",
		label, diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
}

func validatePackageManifest(manifest packageManifest) error {
	if manifest.SchemaVersion != packageSchemaVersion {
		return fmt.Errorf("schema_version %d, want %d", manifest.SchemaVersion, packageSchemaVersion)
	}
	if !validPackageName(manifest.Name) || !validSemanticVersion(manifest.Version) {
		return fmt.Errorf("invalid canonical identity %s@%s", manifest.Name, manifest.Version)
	}
	if !manifest.Stability.valid() {
		return fmt.Errorf("invalid interface stability %q", manifest.Stability)
	}
	if manifest.Interface.Path == "" || manifest.Interface.ConformancePath == "" ||
		!validSHA256(manifest.Interface.SHA256) || !validSHA256(manifest.Interface.ConformanceSHA256) {
		return errors.New("interface requires relative source/conformance paths and SHA-256 hashes")
	}
	if !sortedUniqueStrings(manifest.Interface.Effects) || !sortedUniqueStrings(manifest.Interface.Resources) {
		return errors.New("interface effects and resources must be sorted and unique")
	}
	dependencyNames := make(map[string]bool, len(manifest.Dependencies))
	previousDependency := ""
	for _, dependency := range manifest.Dependencies {
		if !validPackageName(dependency.Name) || !validSemanticVersion(dependency.Version) ||
			dependency.Path == "" || filepath.IsAbs(dependency.Path) {
			return fmt.Errorf("invalid dependency %+v", dependency)
		}
		key := dependency.Name + "@" + dependency.Version
		if key <= previousDependency || dependencyNames[dependency.Name] {
			return errors.New("package dependencies must be sorted by canonical name and unique")
		}
		previousDependency = key
		dependencyNames[dependency.Name] = true
	}
	seenAdapters := make(map[string]bool)
	for _, adapter := range manifest.Adapters {
		if adapter.ID == "" || seenAdapters[adapter.ID] {
			return fmt.Errorf("adapter IDs must be non-empty and unique: %q", adapter.ID)
		}
		seenAdapters[adapter.ID] = true
		registration, ok := backendRegistrationFor(adapter.Backend)
		if !ok {
			return fmt.Errorf("adapter %s names unknown backend %q", adapter.ID, adapter.Backend)
		}
		if !adapter.Stability.valid() || len(adapter.Targets) == 0 || !sortedUniqueStrings(adapter.Targets) {
			return fmt.Errorf("adapter %s requires explicit stability and sorted unique targets", adapter.ID)
		}
		for _, target := range adapter.Targets {
			if _, err := backendTargetFor(registration, target); err != nil {
				return fmt.Errorf("adapter %s: %w", adapter.ID, err)
			}
		}
		switch adapter.Kind {
		case "slick", "go", "c", "bun", "wasm", "sidecar":
		default:
			return fmt.Errorf("adapter %s has unsupported implementation kind %q", adapter.ID, adapter.Kind)
		}
		if adapter.Entry == "" || !validSHA256(adapter.Checksum) || !validSHA256(adapter.InterfaceSHA256) ||
			!validSHA256(adapter.ConformanceSHA256) || adapter.ABI == "" {
			return fmt.Errorf("adapter %s lacks entry, checksums, or ABI", adapter.ID)
		}
		if adapter.InterfaceSHA256 != manifest.Interface.SHA256 {
			return fmt.Errorf("adapter %s declares interface hash %s, want canonical hash %s",
				adapter.ID, adapter.InterfaceSHA256, manifest.Interface.SHA256)
		}
		if adapter.ConformanceSHA256 != manifest.Interface.ConformanceSHA256 {
			return fmt.Errorf("adapter %s declares conformance hash %s, want canonical hash %s",
				adapter.ID, adapter.ConformanceSHA256, manifest.Interface.ConformanceSHA256)
		}
		if adapter.Kind == "sidecar" && adapter.Protocol == "" {
			return fmt.Errorf("sidecar adapter %s lacks a protocol version", adapter.ID)
		}
		if adapter.Kind == "slick" && adapter.ABI != fmt.Sprintf("slick-core-%d", runtimeABIVersion) {
			return fmt.Errorf("portable Slick adapter %s has ABI %q, want slick-core-%d", adapter.ID, adapter.ABI, runtimeABIVersion)
		}
		if !sortedUniqueStrings(adapter.Dependencies) {
			return fmt.Errorf("adapter %s dependencies must be sorted and unique", adapter.ID)
		}
		for _, dependency := range adapter.Dependencies {
			if !dependencyNames[dependency] {
				return fmt.Errorf("adapter %s names undeclared dependency %q", adapter.ID, dependency)
			}
		}
		seenAssets := make(map[string]bool)
		for _, asset := range adapter.Assets {
			if asset.Path == "" || !validSHA256(asset.SHA256) || seenAssets[asset.Path] {
				return fmt.Errorf("adapter %s has an invalid or duplicate asset %q", adapter.ID, asset.Path)
			}
			seenAssets[asset.Path] = true
		}
	}
	return nil
}

func (r *packageResolver) prepareLock(path string, data []byte) ([]byte, error) {
	locked := packageLock{SchemaVersion: packageSchemaVersion}
	if data != nil {
		if err := decodeStrictJSON(data, &locked); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if locked.SchemaVersion != packageSchemaVersion {
			return nil, fmt.Errorf("%s has schema_version %d, want %d", path, locked.SchemaVersion, packageSchemaVersion)
		}
	}
	entries := make(map[string]int, len(locked.Packages))
	for index := range locked.Packages {
		entry := &locked.Packages[index]
		if _, exists := entries[entry.Name]; exists {
			return nil, fmt.Errorf("%s contains duplicate package %s", path, entry.Name)
		}
		entries[entry.Name] = index
	}
	for name := range entries {
		if r.packages[name] == nil {
			return nil, fmt.Errorf("%s contains unreferenced package %s", path, name)
		}
	}
	for name, resolved := range r.packages {
		index, exists := entries[name]
		if !exists {
			locked.Packages = append(locked.Packages, packageLockEntry{
				Name: name, Version: resolved.manifest.Version, InterfaceHash: resolved.interfaceHash,
			})
			index = len(locked.Packages) - 1
			entries[name] = index
		}
		entry := &locked.Packages[index]
		if entry.Version != resolved.manifest.Version || entry.InterfaceHash != resolved.interfaceHash {
			return nil, fmt.Errorf("package %s lock drift: locked %s interface %s, resolved %s interface %s",
				name, entry.Version, entry.InterfaceHash, resolved.manifest.Version, resolved.interfaceHash)
		}
		seenSelections := make(map[string]bool, len(entry.Selections))
		for _, lockedSelection := range entry.Selections {
			key := string(lockedSelection.Backend) + "\x00" + lockedSelection.Target
			if seenSelections[key] {
				return nil, fmt.Errorf("%s contains duplicate selection for package %s backend %s target %s",
					path, name, lockedSelection.Backend, lockedSelection.Target)
			}
			seenSelections[key] = true
			matchesManifest := false
			for _, adapter := range resolved.manifest.Adapters {
				if adapter.ID == lockedSelection.Adapter && adapter.Backend == lockedSelection.Backend &&
					containsPackageString(adapter.Targets, lockedSelection.Target) && adapter.Checksum == lockedSelection.Checksum {
					matchesManifest = true
					break
				}
			}
			if !matchesManifest {
				return nil, fmt.Errorf("package %s adapter lock drift for backend %s target %s: locked %s/%s is not declared",
					name, lockedSelection.Backend, lockedSelection.Target, lockedSelection.Adapter, lockedSelection.Checksum)
			}
		}
		selection := packageLockSelection{
			Backend: r.selection.backend, Target: r.selection.target,
			Adapter: resolved.adapter.ID, Checksum: resolved.adapter.Checksum,
		}
		found := false
		for _, existing := range entry.Selections {
			if existing.Backend != selection.Backend || existing.Target != selection.Target {
				continue
			}
			found = true
			if existing.Adapter != selection.Adapter || existing.Checksum != selection.Checksum {
				return nil, fmt.Errorf("package %s adapter lock drift for backend %s target %s: locked %s/%s, resolved %s/%s",
					name, selection.Backend, selection.Target, existing.Adapter, existing.Checksum, selection.Adapter, selection.Checksum)
			}
		}
		if !found {
			entry.Selections = append(entry.Selections, selection)
		}
	}
	sort.Slice(locked.Packages, func(i, j int) bool { return locked.Packages[i].Name < locked.Packages[j].Name })
	for index := range locked.Packages {
		sort.Slice(locked.Packages[index].Selections, func(i, j int) bool {
			left, right := locked.Packages[index].Selections[i], locked.Packages[index].Selections[j]
			if left.Backend != right.Backend {
				return left.Backend < right.Backend
			}
			return left.Target < right.Target
		})
	}
	encoded, err := json.MarshalIndent(locked, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if bytes.Equal(data, encoded) {
		return nil, nil
	}
	return encoded, nil
}

type packageLockGuard struct {
	path string
	file *os.File
}

func acquirePackageLock(path string, expected []byte) (*packageLockGuard, error) {
	file, err := os.OpenFile(path+".guard", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	locked, err := tryPackageFileLock(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		file.Close()
		return nil, fmt.Errorf("another build is updating %s", path)
	}
	guard := &packageLockGuard{path: path, file: file}
	current, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		current, err = nil, nil
	}
	if err != nil {
		guard.release()
		return nil, err
	}
	if !bytes.Equal(current, expected) {
		guard.release()
		return nil, fmt.Errorf("%s changed during build; refusing to overwrite it", path)
	}
	return guard, nil
}

func (g *packageLockGuard) release() {
	if g == nil {
		return
	}
	unlockPackageFile(g.file)
	g.file.Close()
}

func (g *packageLockGuard) write(data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(g.path), ".slick-lock-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, g.path)
}

func isPackageProject(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	_, err = os.Stat(filepath.Join(path, projectManifestName))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func readStrictJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decodeStrictJSON(data, target); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON document has trailing value")
		}
		return fmt.Errorf("JSON document has trailing data: %w", err)
	}
	return nil
}

func packageRelativePath(root, relative, label string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%s path %q must be relative", label, relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes its package", label, relative)
	}
	return filepath.Join(root, clean), nil
}

func hashPath(path string, slickOnly bool) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	files := []string{path}
	root := filepath.Dir(path)
	if info.IsDir() {
		root = path
		files = nil
		err = filepath.WalkDir(path, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || (slickOnly && filepath.Ext(entry.Name()) != ".slk") {
				return nil
			}
			files = append(files, file)
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if len(files) == 0 {
		return "", ErrNoSources
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%d:%s:%d:", len(filepath.ToSlash(rel)), filepath.ToSlash(rel), len(data))
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func hashPackageSources(sources []Source, prefix string) string {
	hash := sha256.New()
	for _, source := range sources {
		relative := strings.TrimPrefix(source.Name, prefix)
		data := []byte(source.Text)
		fmt.Fprintf(hash, "%d:%s:%d:", len(relative), relative, len(data))
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func availablePackageAdapters(adapters []packageAdapter) string {
	var available []string
	for _, adapter := range adapters {
		for _, target := range adapter.Targets {
			available = append(available, fmt.Sprintf("%s/%s (%s)", adapter.Backend, target, adapter.Stability))
		}
	}
	if len(available) == 0 {
		return "none"
	}
	sort.Strings(available)
	return strings.Join(available, ", ")
}

func validProjectName(name string) bool {
	parts := strings.Split(name, ".")
	return len(parts) >= 2 && parts[0] != "std" && validPackageNameParts(parts)
}

func validPackageName(name string) bool {
	parts := strings.Split(name, ".")
	return len(parts) >= 2 && parts[0] != "root" && parts[0] != "std" && validPackageNameParts(parts)
}

func validPackageNameParts(parts []string) bool {
	for _, part := range parts {
		if part == "" || part[0] < 'a' || part[0] > 'z' {
			return false
		}
		for _, character := range part[1:] {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
				return false
			}
		}
	}
	return true
}

func validSemanticVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sortedUniqueStrings(values []string) bool {
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}

	return true
}
func packageDependencySet(dependencies []packageDependency) map[string]bool {
	names := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		names[index] = dependency.Name
	}
	return packageNameSet(names)
}

func packageNameSet(names []string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

func containsPackageString(values []string, wanted string) bool {
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}
