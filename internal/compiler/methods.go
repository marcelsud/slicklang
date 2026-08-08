package compiler

import (
	"fmt"
	"sort"
	"strings"
)

func (p *program) resolveThrowSets() {
	for _, function := range p.functions {
		function.throwSet = p.resolveThrows(function.namespace, function.aliases, function.throws)
	}
	for _, implementation := range p.methodImpls {
		implementation.throwSet = p.resolveThrows(implementation.namespace, implementation.aliases, implementation.throws)
	}
	for _, class := range p.classes {
		for _, method := range class.methods {
			method.throwSet = p.resolveThrows(method.namespace, method.aliases, method.throws)
		}
	}
	for _, iface := range p.interfaces {
		for _, method := range iface.methods {
			method.throwSet = p.resolveThrows(method.namespace, method.aliases, method.throws)
		}
	}
}

func (p *program) resolveThrows(namespace string, aliases map[string]aliasDecl, refs []typeRef) map[string]struct{} {
	resolved := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		name, ok := p.resolveErrorIn(namespace, aliases, ref.name)
		if !ok {
			p.add(ref.pos, "SLK200", "%s does not name an Error type", ref.name)
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
		receiver := implementation.receiverCanonical
		if receiver == "" {
			if _, alias := implementation.aliases[implementation.receiver.name]; alias {
				p.add(implementation.receiver.pos, "SLK315", "method receivers must use a local or absolute class name, not alias %s", implementation.receiver.name)
				continue
			}
			receiver = implementation.receiver.name
			if !strings.HasPrefix(receiver, "root.") {
				receiver = qualify(implementation.namespace, receiver)
			}
		}
		class := p.classes[receiver]
		if class == nil {
			p.add(implementation.receiver.pos, "SLK314", "method receiver %s is not a class", implementation.receiver.name)
			continue
		}
		if !p.requireAccess(implementation.receiver.pos, implementation.namespace, class.namespace, class.name, "class") {
			continue
		}
		implementation.receiverCanonical = receiver
		implementation.qualified = receiver + "." + implementation.name

		if !isPublic(implementation.name) && implementation.namespace != class.namespace {
			p.add(implementation.pos, "SLK330", "method %s is private to %s; capitalize it to implement it from %s", implementation.name, class.namespace, implementation.namespace)
			continue
		}
		if !implementation.inline {
			switch class.extension {
			case extensionNone:
				p.add(implementation.pos, "SLK313", "%s does not allow detached method implementations", class.qualified)
				continue
			case extensionNamespace:
				if implementation.namespace != class.namespace {
					p.add(implementation.pos, "SLK313", "%s allows method implementations only from %s, not %s", class.qualified, class.namespace, implementation.namespace)
					continue
				}
			}
		}

		if previous := class.implementations[implementation.name]; previous != nil {
			p.add(implementation.pos, "SLK311", "duplicate implementation of %s.%s; first implemented at %s:%d:%d", class.qualified, implementation.name, previous.pos.file, previous.pos.line, previous.pos.column)
			continue
		}
		class.implementations[implementation.name] = implementation

		contract := class.methods[implementation.name]
		if contract == nil {
			contract = signatureFromImplementation(implementation, class.namespace)
			class.effective[implementation.name] = contract
			continue
		}
		class.effective[implementation.name] = contract
		if mismatch := p.signatureMismatch(contract, implementation); mismatch != "" {
			p.add(implementation.pos, "SLK312", "implementation of %s.%s does not match its declaration: %s", class.qualified, implementation.name, mismatch)
		}
	}

	for _, class := range p.classes {
		for name, contract := range class.methods {
			class.effective[name] = contract
			if class.implementations[name] == nil {
				p.add(contract.pos, "SLK310", "%s.%s has no implementation; implement it or remove its declaration", class.qualified, name)
			}
		}
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
	name := ref.name
	if isBuiltinType(name) || strings.ContainsAny(name, "<>()[]?|,") {
		return name
	}
	if strings.HasPrefix(name, "root.") {
		return name
	}
	if alias, ok := aliases[name]; ok {
		return alias.target
	}
	qualified := qualify(namespace, name)
	if p.classes[qualified] != nil || p.interfaces[qualified] != nil {
		return qualified
	}
	return name
}

func (p *program) resolveType(namespace string, aliases map[string]aliasDecl, ref typeRef) string {
	return p.canonicalType(namespace, aliases, ref)
}

func (p *program) methodForType(typeName, methodName string) (*methodSignature, bool) {
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
	if name == "Error" {
		return "Error", true
	}
	if !strings.HasPrefix(name, "root.") {
		if alias, ok := aliases[name]; ok {
			name = alias.target
		} else {
			name = qualify(namespace, name)
		}
	}
	decl := p.classes[name]
	return name, decl != nil && decl.isError
}

func isBuiltinType(name string) bool {
	switch name {
	case "bool", "float", "int", "null", "string":
		return true
	default:
		return false
	}
}
