package compiler

import (
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
	name := p.canonicalType(namespace, aliases, ref)
	if class := p.classes[name]; class != nil {
		p.requireAccess(ref.pos, namespace, class.namespace, class.name, "class")
		return
	}
	if iface := p.interfaces[name]; iface != nil {
		p.requireAccess(ref.pos, namespace, iface.namespace, iface.name, "interface")
	}
}
