package compiler

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func isPublic(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return r != utf8.RuneError && unicode.IsUpper(r)
}

func canAccess(name, ownerNamespace, fromNamespace string) bool {
	return isPublic(name) || ownerNamespace == fromNamespace
}

func (p *program) requireAccess(pos position, fromNamespace, ownerNamespace, name, kind string) bool {
	if canAccess(name, ownerNamespace, fromNamespace) {
		return true
	}
	p.add(pos, "SLK330", "%s %s is private to %s; capitalize it to make it public", kind, name, ownerNamespace)
	return false
}

func (p *program) checkVisibility() {
	for _, function := range p.functions {
		p.checkCallableTypeVisibility(function.namespace, function.aliases, function.params, function.result)
	}
	for _, implementation := range p.methodImpls {
		p.checkCallableTypeVisibility(implementation.namespace, implementation.aliases, implementation.params, implementation.result)
	}
	for _, class := range p.classes {
		for _, field := range class.fields {
			p.checkTypeVisibility(class.namespace, class.aliases, field.typ)
		}
		for _, method := range class.methods {
			p.checkCallableTypeVisibility(method.namespace, method.aliases, method.params, method.result)
		}
	}
	for _, iface := range p.interfaces {
		for _, method := range iface.methods {
			p.checkCallableTypeVisibility(method.namespace, method.aliases, method.params, method.result)
		}
	}
}

func (p *program) checkCallableTypeVisibility(namespace string, aliases map[string]aliasDecl, params []paramDecl, result typeRef) {
	for _, param := range params {
		p.checkTypeVisibility(namespace, aliases, param.typ)
	}
	p.checkTypeVisibility(namespace, aliases, result)
}

func (p *program) checkTypeVisibility(namespace string, aliases map[string]aliasDecl, ref typeRef) {
	p.checkTypeName(ref.pos, namespace, p.canonicalType(namespace, aliases, ref))
}

// checkTypeName walks a canonical type and validates every component: the base
// and arity of each generic application, Map key restrictions, and namespace
// access for each named class or interface.
func (p *program) checkTypeName(pos position, namespace, name string) {
	if pos.file != "" && strings.Contains(name, "std.io.") {
		p.usesStdIO = true
	}
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional, typeKindArray:
		p.checkTypeName(pos, namespace, parsed.base)
		return
	case typeKindTuple:
		for _, element := range parsed.args {
			p.checkTypeName(pos, namespace, element)
		}
		return
	case typeKindGeneric:
		declaration, known := coreGenericType(parsed.base)
		if !known {
			p.add(pos, "SLK361", "unknown generic type %s", parsed.base)
		} else if len(parsed.args) != len(declaration.typeParams) {
			p.addTypeArityDiagnostic(pos, parsed.base, len(declaration.typeParams), len(parsed.args))
		} else if parsed.base == mapTypeName && !isMapKeyType(parsed.args[0]) {
			p.add(pos, "SLK361", "Map key type must be string, int, or bool; found %s", displayName(parsed.args[0]))
		}
		for _, arg := range parsed.args {
			p.checkTypeName(pos, namespace, arg)
		}
		return
	}
	if declaration, generic := coreGenericType(name); generic {
		p.addTypeArityDiagnostic(pos, name, len(declaration.typeParams), 0)
		return
	}
	if strings.ContainsRune(name, '<') {
		p.add(pos, "SLK361", "malformed generic type %s", name)
		return
	}
	if class := p.classes[name]; class != nil {
		p.requireAccess(pos, namespace, class.namespace, class.name, "class")
		return
	}
	if iface := p.interfaces[name]; iface != nil {
		p.requireAccess(pos, namespace, iface.namespace, iface.name, "interface")
	}
}

func (p *program) addTypeArityDiagnostic(pos position, name string, expected, actual int) {
	argument := "arguments"
	if expected == 1 {
		argument = "argument"
	}
	p.add(pos, "SLK361", "%s takes %d type %s, found %d", name, expected, argument, actual)
}
