package compiler

import (
	"fmt"
	"sort"
	"strings"
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

func (p *program) usedStandardSymbols() []string {
	records := standardSymbolRecords(standardLibraryRegistry)
	used := make(map[string]struct{})
	natives := make(map[nativeFunction]string)
	for name, record := range records {
		if record.native != "" {
			natives[record.native] = name
		}
	}
	addType := func(namespace string, aliases map[string]aliasDecl, name string) {
		names := make(map[string]struct{})
		standardTypeNames(p.canonicalTypeName(namespace, aliases, name), names)
		for candidate := range names {
			if _, ok := records[candidate]; ok {
				used[candidate] = struct{}{}
			}
		}
	}
	var collectBlock func(*blockNode, *functionDecl)
	var collectExpression func(expressionNode, *functionDecl)
	collectExpression = func(expression expressionNode, function *functionDecl) {
		switch node := expression.(type) {
		case nil, *invalidExpression, *literalExpression, *templateExpression, *nameExpression, *awaitExpression:
		case *tupleExpression:
			for _, element := range node.elements {
				collectExpression(element, function)
			}
		case *arrayExpression:
			for _, element := range node.elements {
				collectExpression(element, function)
			}
		case *mapExpression:
			for _, entry := range node.entries {
				collectExpression(entry.key, function)
				collectExpression(entry.value, function)
			}
		case *rangeExpression:
			collectExpression(node.start, function)
			collectExpression(node.end, function)
		case *lambdaExpression:
			for _, param := range node.params {
				addType(function.namespace, function.aliases, param.typ.name)
			}
			addType(function.namespace, function.aliases, node.result.name)
			for _, thrown := range node.throws {
				addType(function.namespace, function.aliases, thrown.name)
			}
			collectBlock(node.body, node.fn)
		case *objectExpression:
			addType(function.namespace, function.aliases, node.typeName)
			for _, field := range node.fields {
				collectExpression(field.value, function)
			}
		case *callExpression:
			if name, ok := natives[node.resolvedNative]; ok {
				used[name] = struct{}{}
			}
			if callee, ok := node.callee.(*nameExpression); ok {
				if _, exists := records[callee.name]; exists {
					used[callee.name] = struct{}{}
				}
				if node.resolvedReceiver != "" {
					parts := strings.Split(callee.name, ".")
					name := node.resolvedReceiver + "." + parts[len(parts)-1]
					if _, exists := records[name]; exists {
						used[name] = struct{}{}
					}
				}
			}
			for _, typeArg := range node.resolvedTypeArgs {
				addType(function.namespace, function.aliases, typeArg)
			}
			collectExpression(node.callee, function)
			for _, argument := range node.args {
				collectExpression(argument, function)
			}
		case *unaryExpression:
			collectExpression(node.value, function)
		case *binaryExpression:
			collectExpression(node.left, function)
			collectExpression(node.right, function)
		case *ifExpression:
			collectExpression(node.condition, function)
			collectBlock(node.thenBlock, function)
			collectBlock(node.elseBlock, function)
		case *catchExpression:
			collectExpression(node.value, function)
			for _, arm := range node.arms {
				addType(function.namespace, function.aliases, arm.errorType.name)
				collectExpression(arm.value, function)
			}
		case *resultExpression:
			collectExpression(node.value, function)
		case *propagateExpression:
			collectExpression(node.value, function)
		case *usingExpression:
			collectExpression(node.initializer, function)
			collectBlock(node.body, function)
		case *matchExpression:
			collectExpression(node.value, function)
			for _, arm := range node.arms {
				collectExpression(arm.value, function)
			}
		}
	}
	collectBlock = func(block *blockNode, function *functionDecl) {
		if block == nil || function == nil {
			return
		}
		for _, statement := range block.statements {
			switch node := statement.(type) {
			case *letStatement:
				collectExpression(node.value, function)
			case *asyncLetStatement:
				collectExpression(node.call, function)
			case *assignmentStatement:
				collectExpression(node.value, function)
			case *forStatement:
				collectExpression(node.iterable, function)
				collectBlock(node.body, function)
			case *throwStatement:
				collectExpression(node.value, function)
			case *returnStatement:
				collectExpression(node.value, function)
			case *expressionStatement:
				collectExpression(node.value, function)
			case *breakStatement, *continueStatement:
			}
		}
	}
	for _, function := range p.authoredCallables() {
		for _, param := range function.params {
			addType(function.namespace, function.aliases, param.typ.name)
		}
		addType(function.namespace, function.aliases, function.result.name)
		for _, thrown := range function.throws {
			addType(function.namespace, function.aliases, thrown.name)
		}
		collectBlock(function.ast, function)
	}
	for _, class := range p.classes {
		if class.pos.file == "" || class.instanceOf != "" {
			continue
		}
		for _, field := range class.fields {
			addType(class.namespace, class.aliases, field.typ.name)
		}
	}
	for _, iface := range p.interfaces {
		if iface.pos.file == "" || iface.instanceOf != "" {
			continue
		}
		for _, method := range iface.methods {
			for _, param := range method.params {
				addType(method.namespace, method.aliases, param.typ.name)
			}
			addType(method.namespace, method.aliases, method.result.name)
			for _, thrown := range method.throws {
				addType(method.namespace, method.aliases, thrown.name)
			}
		}
	}
	for _, union := range p.unions {
		for _, variant := range union.variants {
			for _, field := range variant.fields {
				addType(union.namespace, union.aliases, field.typ.name)
			}
		}
	}
	for _, constant := range p.constants {
		addType(constant.namespace, constant.aliases, constant.typ.name)
		collectExpression(constant.ast, &functionDecl{namespace: constant.namespace, aliases: constant.aliases})
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *program) validateStandardUsage(backend Backend, target string, allowAlpha, validateBackend bool) error {
	records := standardSymbolRecords(standardLibraryRegistry)
	for _, name := range p.usedStandardSymbols() {
		record := records[name]
		if record.stability == StabilityAlpha && !allowAlpha {
			return fmt.Errorf("standard-library symbol %s is alpha; pass --allow-alpha to use it", name)
		}
		if validateBackend {
			if err := validateStandardSymbolAvailability(name, backend, target, allowAlpha); err != nil {
				return err
			}
		}
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
