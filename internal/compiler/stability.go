package compiler

import (
	"fmt"
	"sort"
)

// Stability is maintainer-declared release status. Zero is invalid so a new
// registry entry cannot become stable by omission.
type Stability string

const (
	StabilityAlpha  Stability = "alpha"
	StabilityStable Stability = "stable"
)

func (stability Stability) valid() bool {
	return stability == StabilityAlpha || stability == StabilityStable
}

type standardLibraryRegistryDecl struct {
	namespaces []standardNamespaceDecl
	functions  []standardFunctionDecl
	classes    []standardClassDecl
	interfaces []standardInterfaceDecl
}

// declareStandardLibrary makes the stability of this registry block explicit.
// An entry may opt into a different status, but zero never means stable.
func declareStandardLibrary(stability Stability, registry standardLibraryRegistryDecl) standardLibraryRegistryDecl {
	declared := func(entry Stability) Stability {
		if entry == "" {
			return stability
		}
		return entry
	}
	for index := range registry.namespaces {
		registry.namespaces[index].stability = declared(registry.namespaces[index].stability)
	}
	for index := range registry.functions {
		registry.functions[index].stability = declared(registry.functions[index].stability)
	}
	for index := range registry.classes {
		class := &registry.classes[index]
		class.stability = declared(class.stability)
		for fieldIndex := range class.fields {
			class.fields[fieldIndex].stability = declared(class.fields[fieldIndex].stability)
		}
		for methodIndex := range class.methods {
			class.methods[methodIndex].stability = declared(class.methods[methodIndex].stability)
		}
	}
	for index := range registry.interfaces {
		iface := &registry.interfaces[index]
		iface.stability = declared(iface.stability)
		for methodIndex := range iface.methods {
			iface.methods[methodIndex].stability = declared(iface.methods[methodIndex].stability)
		}
	}
	return registry
}

type standardSymbolRecord struct {
	stability Stability
	native    nativeFunction
	types     []string
}

func standardSymbolRecords(registry standardLibraryRegistryDecl) map[string]standardSymbolRecord {
	records := make(map[string]standardSymbolRecord)
	for _, namespace := range registry.namespaces {
		records[namespace.canonical] = standardSymbolRecord{stability: namespace.stability}
	}
	for _, function := range registry.functions {
		records[function.canonical] = standardSymbolRecord{
			stability: function.stability,
			native:    function.native,
			types:     standardSignatureTypes(function.params, function.result, nil),
		}
	}
	for _, class := range registry.classes {
		records[class.canonical] = standardSymbolRecord{stability: class.stability}
		for _, field := range class.fields {
			records[class.canonical+"."+field.name] = standardSymbolRecord{
				stability: field.stability,
				types:     []string{field.typ.name},
			}
		}
		for _, method := range class.methods {
			records[class.canonical+"."+method.name] = standardSymbolRecord{
				stability: method.stability,
				native:    method.native,
				types:     standardSignatureTypes(method.params, method.result, method.throws),
			}
		}
	}
	for _, iface := range registry.interfaces {
		records[iface.canonical] = standardSymbolRecord{stability: iface.stability}
		for _, method := range iface.methods {
			records[iface.canonical+"."+method.name] = standardSymbolRecord{
				stability: method.stability,
				native:    method.native,
				types:     standardSignatureTypes(method.params, method.result, method.throws),
			}
		}
	}
	return records
}

func standardSignatureTypes(params []paramDecl, result typeRef, throws []typeRef) []string {
	types := make([]string, 0, len(params)+len(throws)+1)
	for _, param := range params {
		types = append(types, param.typ.name)
	}
	if result.name != "" {
		types = append(types, result.name)
	}
	for _, thrown := range throws {
		types = append(types, thrown.name)
	}
	return types
}

func standardTypeNames(name string, names map[string]struct{}) {
	parsed := parseTypeName(name)
	if parsed.kind == typeKindName {
		names[parsed.base] = struct{}{}
		return
	}
	if parsed.kind == typeKindGeneric {
		names[parsed.base] = struct{}{}
	}
	if parsed.base != "" && parsed.kind != typeKindTuple {
		standardTypeNames(parsed.base, names)
	}
	for _, arg := range parsed.args {
		standardTypeNames(arg, names)
	}
	for _, thrown := range parsed.throws {
		standardTypeNames(thrown, names)
	}
}

func standardSymbolMetadata(name string) (Stability, bool, bool) {
	record, ok := standardSymbolRecords(standardLibraryRegistry)[name]
	if !ok {
		return "", false, false
	}
	return record.stability, standardSymbolEligible(record), true
}

func standardDescriptionMetadata(name string) (Stability, *bool) {
	stability, eligible, ok := standardSymbolMetadata(name)
	if !ok {
		return "", nil
	}
	return stability, &eligible
}

func standardSymbolEligible(record standardSymbolRecord) bool {
	for _, backend := range backendRegistry {
		if backend.stability != StabilityStable || record.native == "" {
			continue
		}
		if !backend.implements(record.native) {
			return false
		}
	}
	return true
}

func validateStandardSymbolAvailability(name string, backend Backend, target string, allowAlpha bool) error {
	record, ok := standardSymbolRecords(standardLibraryRegistry)[name]
	if !ok {
		return fmt.Errorf("unknown standard-library symbol %s", name)
	}
	if record.stability == StabilityAlpha && !allowAlpha {
		return fmt.Errorf("standard-library symbol %s is alpha; pass --allow-alpha to use it", name)
	}
	declaration, ok := backendRegistrationFor(backend)
	if !ok {
		return fmt.Errorf("unknown backend %q", backend)
	}
	if record.native != "" && !declaration.implements(record.native) {
		if target == "" && len(declaration.targets) > 0 {
			target = declaration.targets[0].name
		}
		return fmt.Errorf("standard-library symbol %s is unavailable for backend %s target %s", name, backend, target)
	}
	return nil
}

func validateStabilityRegistries() error {
	for _, backend := range backendRegistry {
		if !backend.stability.valid() {
			return fmt.Errorf("backend %s has invalid stability %q", backend.name, backend.stability)
		}
		for _, target := range backend.targets {
			if !target.stability.valid() {
				return fmt.Errorf("backend %s target %s has invalid stability %q", backend.name, target.name, target.stability)
			}
		}
	}

	records := standardSymbolRecords(standardLibraryRegistry)
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		record := records[name]
		if !record.stability.valid() {
			return fmt.Errorf("standard-library symbol %s has invalid stability %q", name, record.stability)
		}
		if record.stability == StabilityStable && !standardSymbolEligible(record) {
			return fmt.Errorf("stable standard-library symbol %s lacks complete stable-backend coverage", name)
		}
		if record.stability != StabilityStable {
			continue
		}
		for _, typ := range record.types {
			dependencies := make(map[string]struct{})
			standardTypeNames(typ, dependencies)
			for dependency := range dependencies {
				if depended, exists := records[dependency]; exists && depended.stability != StabilityStable {
					return fmt.Errorf("stable standard-library symbol %s depends on alpha symbol %s", name, dependency)
				}
			}
		}
	}
	for _, backend := range backendRegistry {
		if backend.stability != StabilityStable {
			continue
		}
		for _, name := range names {
			record := records[name]
			if record.stability == StabilityStable && record.native != "" && !backend.implements(record.native) {
				return fmt.Errorf("stable backend %s lacks standard-library symbol %s", backend.name, name)
			}
		}
	}
	return nil
}

func init() {
	if err := validateStabilityRegistries(); err != nil {
		panic(err)
	}
}
