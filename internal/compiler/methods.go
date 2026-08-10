package compiler

import (
	"fmt"
	"sort"
	"strings"
)

func (p *program) resolveThrowSets() {
	for _, name := range sortedKeys(p.functions) {
		function := p.functions[name]
		p.checkingInstance(function.instanceOf != "", func() {
			function.throwSet = p.resolveThrows(function.namespace, function.aliases, function.throws)
		})
	}
	for _, implementation := range p.methodImpls {
		p.checkingInstance(implementation.instanceOf != "", func() {
			implementation.throwSet = p.resolveThrows(implementation.namespace, implementation.aliases, implementation.throws)
		})
	}
	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		p.checkingInstance(class.instanceOf != "", func() {
			for _, methodName := range sortedKeys(class.methods) {
				method := class.methods[methodName]
				method.throwSet = p.resolveThrows(method.namespace, method.aliases, method.throws)
			}
		})
	}
	for _, name := range sortedKeys(p.interfaces) {
		iface := p.interfaces[name]
		p.checkingInstance(iface.instanceOf != "", func() {
			for _, methodName := range sortedKeys(iface.methods) {
				method := iface.methods[methodName]
				method.throwSet = p.resolveThrows(method.namespace, method.aliases, method.throws)
			}
		})
	}
}

func (p *program) resolveThrows(namespace string, aliases map[string]aliasDecl, refs []typeRef) map[string]struct{} {
	resolved := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		name, ok := p.resolveErrorIn(namespace, aliases, ref.name)
		if !ok {
			p.add(ref.pos, diagnosticCodeErrorValue, "%s does not name an Error type", ref.name)
			continue
		}
		if name != "Error" {
			decl := p.classes[name]
			p.requireAccess(ref.pos, namespace, decl.namespace, decl.name, "error class")
		}
		resolved[name] = struct{}{}
	}
	return resolved
}

func (p *program) linkMethods() {
	for _, implementation := range p.methodImpls {
		p.checkingInstance(implementation.instanceOf != "", func() { p.linkMethod(implementation) })
	}

	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		p.checkingInstance(class.instanceOf != "", func() {
			for _, methodName := range sortedKeys(class.methods) {
				contract := class.methods[methodName]
				class.effective[methodName] = contract
				if class.implementations[methodName] == nil {
					p.add(contract.pos, diagnosticCodeMissingMethod, "%s.%s has no implementation; implement it or remove its declaration", class.qualified, methodName)
				}
			}
		})
	}
}

func (p *program) linkMethod(implementation *functionDecl) {
	receiver := implementation.receiverCanonical
	if receiver == "" {
		if _, alias := implementation.aliases[implementation.receiver.name]; alias {
			p.add(implementation.receiver.pos, diagnosticCodeAliasedMethodReceiver, "method receivers must use a local or absolute class name, not alias %s", implementation.receiver.name)
			return
		}
		receiver = implementation.receiver.name
		if !isAbsoluteCanonicalName(receiver) {
			receiver = qualify(implementation.namespace, receiver)
		}
	}
	class := p.classes[receiver]
	if class == nil {
		// A generic class is only ever extended at its declared parameters, so
		// naming it bare is a missing type argument list, not a missing class.
		if p.reportGenericMisuse(implementation.receiver.pos, receiver) {
			return
		}
		p.add(implementation.receiver.pos, diagnosticCodeMethodReceiver, "method receiver %s is not a class", implementation.receiver.name)
		return
	}
	if !p.requireAccess(implementation.receiver.pos, implementation.namespace, class.namespace, class.name, "class") {
		return
	}
	implementation.receiverCanonical = receiver
	implementation.qualified = receiver + "." + implementation.name

	if !isPublic(implementation.name) && implementation.namespace != class.namespace {
		p.add(implementation.pos, diagnosticCodePrivateAccess, "method %s is private to %s; capitalize it to implement it from %s", implementation.name, class.namespace, implementation.namespace)
		return
	}
	if !implementation.inline {
		switch class.extension {
		case extensionNone:
			p.add(implementation.pos, diagnosticCodeDetachedMethod, "%s does not allow detached method implementations", class.qualified)
			return
		case extensionNamespace:
			if implementation.namespace != class.namespace {
				p.add(implementation.pos, diagnosticCodeDetachedMethod, "%s allows method implementations only from %s, not %s", class.qualified, class.namespace, implementation.namespace)
				return
			}
		}
	}

	contract := class.methods[implementation.name]
	if contract == nil {
		p.add(implementation.pos, diagnosticCodeMethodReceiver, "%s.%s is not declared by %s", class.qualified, implementation.name, class.qualified)
		return
	}

	if previous := class.implementations[implementation.name]; previous != nil {
		if previous.documentation != nil && implementation.documentation != nil {
			p.add(implementation.pos, diagnosticCodeConflictingDocumentation, "competing documentation for %s.%s", class.qualified, implementation.name)
		}
		p.add(implementation.pos, diagnosticCodeDuplicateMethod, "duplicate implementation of %s.%s; first implemented at %s:%d:%d", class.qualified, implementation.name, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	class.implementations[implementation.name] = implementation

	class.effective[implementation.name] = contract
	if mismatch := p.signatureMismatch(contract, implementation); mismatch != "" {
		p.add(implementation.pos, diagnosticCodeMethodSignature, "implementation of %s.%s does not match its declaration: %s", class.qualified, implementation.name, mismatch)
	}
}

func signatureFromImplementation(implementation *functionDecl, ownerNamespace string) *methodSignature {
	return &methodSignature{
		name:           implementation.name,
		namespace:      implementation.namespace,
		ownerNamespace: ownerNamespace,
		aliases:        implementation.aliases,
		params:         implementation.params,
		result:         implementation.result,
		throws:         implementation.throws,
		throwSet:       implementation.throwSet,
		pos:            implementation.pos,
	}
}

func (p *program) signatureMismatch(contract *methodSignature, implementation *functionDecl) string {
	if len(contract.params) != len(implementation.params) {
		return fmt.Sprintf("expected %d parameters, found %d", len(contract.params), len(implementation.params))
	}
	for index := range contract.params {
		expected := p.canonicalType(contract.namespace, contract.aliases, contract.params[index].typ)
		actual := p.canonicalType(implementation.namespace, implementation.aliases, implementation.params[index].typ)
		if expected != actual {
			return fmt.Sprintf("parameter %d must be %s, found %s", index+1, expected, actual)
		}
	}
	expectedResult := p.canonicalType(contract.namespace, contract.aliases, contract.result)
	actualResult := p.canonicalType(implementation.namespace, implementation.aliases, implementation.result)
	if expectedResult != actualResult {
		return fmt.Sprintf("result must be %s, found %s", expectedResult, actualResult)
	}
	for thrown := range implementation.throwSet {
		if !containsError(contract.throwSet, thrown) {
			return fmt.Sprintf("undeclared error effect %s", displayName(thrown))
		}
	}
	return ""
}

func (p *program) classSatisfies(class *classDecl, iface *interfaceDecl) []string {
	names := make([]string, 0, len(iface.methods))
	for name := range iface.methods {
		names = append(names, name)
	}
	sort.Strings(names)
	var reasons []string
	for _, name := range names {
		required := iface.methods[name]
		provided := class.effective[name]
		if provided == nil {
			reasons = append(reasons, "missing "+name)
			continue
		}
		if !isPublic(required.name) && required.ownerNamespace != provided.ownerNamespace {
			reasons = append(reasons, "private method "+name+" belongs to "+required.ownerNamespace)
			continue
		}
		implementation := &functionDecl{
			params:    provided.params,
			result:    provided.result,
			throwSet:  provided.throwSet,
			namespace: provided.namespace,
			aliases:   provided.aliases,
		}
		if mismatch := p.signatureMismatch(required, implementation); mismatch != "" {
			reasons = append(reasons, name+": "+mismatch)
		}
	}
	return reasons
}

func (p *program) canonicalType(namespace string, aliases map[string]aliasDecl, ref typeRef) string {
	return p.canonicalTypeName(namespace, aliases, ref.name)
}

// canonicalTypeName resolves name to its fully qualified form, descending
// through the structural layers parseTypeName reports so an optional, array,
// or generic argument canonicalizes the same way a bare type does.
func (p *program) canonicalTypeName(namespace string, aliases map[string]aliasDecl, name string) string {
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional:
		return optionalOf(p.canonicalTypeName(namespace, aliases, parsed.base))
	case typeKindArray:
		return p.canonicalTypeName(namespace, aliases, parsed.base) + "[]"
	case typeKindTuple:
		elements := make([]string, len(parsed.args))
		for index, element := range parsed.args {
			elements[index] = p.canonicalTypeName(namespace, aliases, element)
		}
		return "(" + strings.Join(elements, ",") + ")"
	case typeKindGeneric:
		canonical := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			canonical[index] = p.canonicalTypeName(namespace, aliases, arg)
		}
		return p.canonicalGenericBase(namespace, aliases, parsed.base) + "<" + strings.Join(canonical, ",") + ">"
	}
	if isBuiltinType(name) || strings.ContainsAny(name, "<>()[]?|,") {
		return name
	}
	if isAbsoluteCanonicalName(name) {
		return name
	}
	if alias, ok := aliases[name]; ok {
		return alias.target
	}
	qualified := qualify(namespace, name)
	if p.classDeclaration(qualified) != nil || p.interfaceDeclaration(qualified) != nil || p.unions[qualified] != nil {
		return qualified
	}
	return name
}

// canonicalGenericBase resolves the declaration a generic application names.
// Compiler-owned generics keep their bare names; a user-declared one is
// qualified or resolved through an alias exactly as a plain type is, so
// Box<int> and root.Box<int> are one type.
func (p *program) canonicalGenericBase(namespace string, aliases map[string]aliasDecl, base string) string {
	if _, core := coreGenericType(base); core {
		return base
	}
	if isAbsoluteCanonicalName(base) {
		return base
	}
	if alias, ok := aliases[base]; ok {
		return alias.target
	}
	qualified := qualify(namespace, base)
	if p.classDeclaration(qualified) != nil || p.interfaceDeclaration(qualified) != nil || p.unions[qualified] != nil {
		return qualified
	}
	return base
}

func (p *program) resolveType(namespace string, aliases map[string]aliasDecl, ref typeRef) string {
	return p.canonicalType(namespace, aliases, ref)
}

func (p *program) methodForType(typeName, methodName string) (*methodSignature, bool) {
	if elementType, ok := arrayElementType(typeName); ok {
		method := &methodSignature{
			name:     methodName,
			aliases:  make(map[string]aliasDecl),
			throwSet: make(map[string]struct{}),
		}
		switch methodName {
		case "Length":
			method.result = typeRef{name: "int"}
		case "Get":
			method.params = []paramDecl{{name: "Index", typ: typeRef{name: "int"}}}
			method.result = typeRef{name: optionalOf(elementType)}
		case "Slice":
			method.params = []paramDecl{
				{name: "Start", typ: typeRef{name: "int"}},
				{name: "End", typ: typeRef{name: "int"}},
			}
			method.result = typeRef{name: "Result<" + typeName + "," + stdCollectionsBoundsFailureName + ">"}
		default:
			return nil, false
		}
		return method, true
	}
	if keyType, valueType, ok := mapTypeArgs(typeName); ok {
		method := &methodSignature{
			name:     methodName,
			aliases:  make(map[string]aliasDecl),
			throwSet: make(map[string]struct{}),
		}
		switch methodName {
		case "Get":
			method.params = []paramDecl{{name: "Key", typ: typeRef{name: keyType}}}
			method.result = typeRef{name: optionalOf(valueType)}
		case "Contains", "Without":
			method.params = []paramDecl{{name: "Key", typ: typeRef{name: keyType}}}
			if methodName == "Contains" {
				method.result = typeRef{name: "bool"}
			} else {
				method.result = typeRef{name: typeName}
			}
		case "With":
			method.params = []paramDecl{
				{name: "Key", typ: typeRef{name: keyType}},
				{name: "Value", typ: typeRef{name: valueType}},
			}
			method.result = typeRef{name: typeName}
		case "Length":
			method.result = typeRef{name: "int"}
		default:
			return nil, false
		}
		return method, true
	}
	if class := p.classes[typeName]; class != nil {
		method := class.effective[methodName]
		return method, method != nil
	}
	if iface := p.interfaces[typeName]; iface != nil {
		method := iface.methods[methodName]
		return method, method != nil
	}
	return nil, false
}

func (p *program) resolveErrorIn(namespace string, aliases map[string]aliasDecl, name string) (string, bool) {
	if name == errorTypeName {
		return errorTypeName, true
	}
	// A generic error type names one concrete instantiation, which is an
	// ordinary class once the program mentions it.
	if strings.ContainsRune(name, '<') {
		canonical := p.canonicalTypeName(namespace, aliases, name)
		decl := p.classes[canonical]
		return canonical, decl != nil && decl.isError
	}
	if !isAbsoluteCanonicalName(name) {
		if alias, ok := aliases[name]; ok {
			name = alias.target
		} else {
			name = qualify(namespace, name)
		}
	}
	decl := p.classes[name]
	return name, decl != nil && decl.isError
}
