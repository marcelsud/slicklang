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
	p.add(pos, diagnosticCodePrivateAccess, "%s %s is private to %s; capitalize it to make it public", kind, name, ownerNamespace)
	return false
}

func (p *program) checkVisibility() {
	for _, name := range sortedKeys(p.functions) {
		function := p.functions[name]
		p.checkingInstance(function.instanceOf != "", func() {
			// A native declaration may keep private type parameters, which are
			// bound for its own signature exactly as a source generic's are.
			p.checkCallableTypeVisibilityScoped(function.namespace, function.aliases,
				newTypeParamScope(function.qualified, function.typeParams), function.params, function.result)
		})
	}
	for _, implementation := range p.methodImpls {
		p.checkingInstance(implementation.instanceOf != "", func() {
			p.checkCallableTypeVisibility(implementation.namespace, implementation.aliases, implementation.params, implementation.result)
		})
	}
	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		p.checkingInstance(class.instanceOf != "", func() {
			for _, fieldName := range sortedKeys(class.fields) {
				p.checkTypeVisibility(class.namespace, class.aliases, class.fields[fieldName].typ)
			}
			for _, methodName := range sortedKeys(class.methods) {
				method := class.methods[methodName]
				p.checkCallableTypeVisibility(method.namespace, method.aliases, method.params, method.result)
			}
		})
	}
	for _, name := range sortedKeys(p.interfaces) {
		iface := p.interfaces[name]
		p.checkingInstance(iface.instanceOf != "", func() {
			for _, methodName := range sortedKeys(iface.methods) {
				method := iface.methods[methodName]
				p.checkCallableTypeVisibility(method.namespace, method.aliases, method.params, method.result)
			}
		})
	}
	for _, union := range p.unions {
		for _, variantName := range union.order {
			for _, field := range union.variants[variantName].fields {
				p.checkTypeVisibility(union.namespace, union.aliases, field.typ)
			}
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

func (p *program) checkCallableTypeVisibilityScoped(namespace string, aliases map[string]aliasDecl, scope *typeParamScope, params []paramDecl, result typeRef) {
	for _, param := range params {
		p.checkTypeNameScoped(param.typ.pos, namespace, scope, false, p.canonicalType(namespace, aliases, param.typ))
	}
	p.checkTypeNameScoped(result.pos, namespace, scope, false, p.canonicalType(namespace, aliases, result))
}

// checkTypeName walks a canonical type and validates every component: the base
// and arity of each generic application, Map key restrictions, and namespace
// access for each named class or interface.
func (p *program) checkTypeName(pos position, namespace, name string) {
	p.checkTypeNameScoped(pos, namespace, nil, false, name)
}

// checkTypeNameScoped is checkTypeName with the type parameters a generic
// declaration binds. scope is nil outside such a declaration. named reports an
// unresolvable plain name, which is only meaningful where a type is required
// to already exist: inside a generic declaration, and as the argument of a
// generic application.
func (p *program) checkTypeNameScoped(pos position, namespace string, scope *typeParamScope, named bool, name string) {
	if pos.file != "" && strings.Contains(name, "std.io.") {
		p.usesStdIO = true
	}
	if pos.file != "" {
		markUsesStdHTTP(p, name)
	}
	if pos.file != "" && usesStdFSDirectoryName(name) {
		p.usesStdFSDirectory = true
	}
	if pos.file != "" && strings.Contains(name, "std.process.") {
		p.usesStdProcess = true
	}
	if pos.file != "" && strings.Contains(name, "std.sqlite.") {
		p.usesStdSQLite = true
	}
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional, typeKindArray:
		p.checkTypeNameScoped(pos, namespace, scope, named, parsed.base)
		return
	case typeKindCallable:
		for _, param := range parsed.args {
			p.checkTypeNameScoped(pos, namespace, scope, named, param)
		}
		p.checkTypeNameScoped(pos, namespace, scope, named, parsed.base)
		for _, thrown := range parsed.throws {
			p.checkTypeNameScoped(pos, namespace, scope, named, thrown)
			if thrown == errorTypeName {
				continue
			}
			p.checkTypeNameScoped(pos, namespace, scope, named, thrown)
			thrownType := parseTypeName(thrown)
			class := p.classes[thrown]
			if thrownType.kind == typeKindGeneric {
				class = p.genericClasses[thrownType.base]
			}
			if class == nil || !class.isError {
				p.add(pos, diagnosticCodeErrorValue, "%s does not name an Error type", displayName(thrown))
			}
		}
		p.resolveOperationEffects(operationEffectRefsAt(pos, parsed.operations...))
		return
	case typeKindTuple:
		if len(parsed.args) < 2 {
			p.add(pos, diagnosticCodeTypeMismatch, "tuple types require at least two elements")
			return
		}
		for _, element := range parsed.args {
			p.checkTypeNameScoped(pos, namespace, scope, named, element)
		}
		return
	case typeKindGeneric:
		p.checkGenericApplication(pos, namespace, parsed)
		for _, arg := range parsed.args {
			// A type argument names a type that must already exist, so an
			// unresolvable one is reported rather than read as a forward use.
			p.checkTypeNameScoped(pos, namespace, scope, true, arg)
		}
		return
	}
	if declaration, generic := coreGenericType(name); generic {
		p.addTypeArityDiagnostic(pos, name, len(declaration.typeParams), 0)
		return
	}
	if params, generic := p.genericTypeParams(name); generic {
		p.addTypeArityDiagnostic(pos, name, len(params), 0)
		return
	}
	if strings.ContainsRune(name, '<') {
		p.add(pos, diagnosticCodeGenericType, "malformed generic type %s", name)
		return
	}
	if class := p.classes[name]; class != nil {
		p.requireAccess(pos, namespace, class.namespace, class.name, "class")
		return
	}
	if iface := p.interfaces[name]; iface != nil {
		p.requireAccess(pos, namespace, iface.namespace, iface.name, "interface")
		return
	}
	if union := p.unions[name]; union != nil {
		p.requireAccess(pos, namespace, union.namespace, union.name, "union")
		return
	}
	p.reportUnboundTypeName(pos, scope, named, name)
}

// checkGenericApplication validates the base and arity of one generic
// application. A declaration that takes no parameters and an unknown base each
// get their own message so the mistake is named rather than described.
func (p *program) checkGenericApplication(pos position, namespace string, parsed parsedType) {
	if declaration, known := coreGenericType(parsed.base); known {
		switch {
		case len(parsed.args) != len(declaration.typeParams):
			p.addTypeArityDiagnostic(pos, parsed.base, len(declaration.typeParams), len(parsed.args))
		case parsed.base == mapTypeName && !isMapKeyType(parsed.args[0]):
			p.add(pos, diagnosticCodeGenericType, "Map key type must be string, int, or bool; found %s", displayName(parsed.args[0]))
		}
		return
	}
	if params, generic := p.genericTypeParams(parsed.base); generic {
		if len(parsed.args) != len(params) {
			p.addTypeArityDiagnostic(pos, parsed.base, len(params), len(parsed.args))
			return
		}
		if class := p.genericClasses[parsed.base]; class != nil {
			p.requireAccess(pos, namespace, class.namespace, class.name, "class")
			return
		}
		iface := p.genericInterfaces[parsed.base]
		p.requireAccess(pos, namespace, iface.namespace, iface.name, "interface")
		return
	}
	if p.classes[parsed.base] != nil || p.interfaces[parsed.base] != nil {
		p.add(pos, diagnosticCodeGenericType, "%s takes no type arguments", parsed.base)
		return
	}
	p.add(pos, diagnosticCodeGenericType, "unknown generic type %s", parsed.base)
}

// reportUnboundTypeName names the two ways a plain type can fail to resolve
// where one is required: a missing declaration, and a type parameter the
// enclosing generic never declared.
func (p *program) reportUnboundTypeName(pos position, scope *typeParamScope, named bool, name string) {
	if scope.binds(name) || isBuiltinType(name) || name == errorTypeName {
		return
	}
	switch {
	case scope != nil:
		p.add(pos, diagnosticCodeUnboundTypeParameter, "%s is not a known type or a type parameter of %s", name, scope.owner)
	case named:
		p.add(pos, diagnosticCodeUnboundTypeParameter, "%s is not a known type", name)
	}
}

// reportGenericMisuse names a type that uses a declaration's type parameters
// wrongly: a bare generic name, the wrong arity, or type arguments on a
// declaration that takes none. It reports whether it named a mistake.
func (p *program) reportGenericMisuse(pos position, name string) bool {
	parsed := parseTypeName(name)
	if parsed.kind == typeKindGeneric {
		if params, generic := p.genericTypeParams(parsed.base); generic {
			if len(parsed.args) == len(params) {
				return false
			}
			p.addTypeArityDiagnostic(pos, parsed.base, len(params), len(parsed.args))
			return true
		}
		if p.classes[parsed.base] != nil || p.interfaces[parsed.base] != nil {
			p.add(pos, diagnosticCodeGenericType, "%s takes no type arguments", parsed.base)
			return true
		}
		return false
	}
	if params, generic := p.genericTypeParams(name); generic {
		p.addTypeArityDiagnostic(pos, name, len(params), 0)
		return true
	}
	return false
}

// genericTypeParams reports the parameters of an open generic declaration.
func (p *program) genericTypeParams(name string) ([]string, bool) {
	if class := p.genericClasses[name]; class != nil {
		return class.typeParams, true
	}
	if iface := p.genericInterfaces[name]; iface != nil {
		return iface.typeParams, true
	}
	return nil, false
}

func (p *program) addTypeArityDiagnostic(pos position, name string, expected, actual int) {
	argument := "arguments"
	if expected == 1 {
		argument = "argument"
	}
	p.add(pos, diagnosticCodeGenericType, "%s takes %d type %s, found %d", name, expected, argument, actual)
}
