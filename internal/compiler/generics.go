package compiler

import (
	"fmt"
	"strings"
)

// User-defined generics are monomorphized. Every concrete instantiation a
// program mentions becomes an ordinary declaration keyed by its canonical name,
// so root.Box<int> is a class exactly like root.User is: the checker, both
// backends, JSON, and describe reach it through the maps they already use, and
// no runtime decision depends on reflection or on parsing a type name back
// apart. Open declarations stay out of those maps entirely and live in
// genericClasses, genericInterfaces, and genericFunctions.

// maxInstantiationDepth bounds how deeply an instantiation may nest. A
// recursive declaration such as Node<T> holding Node<T>? repeats one name and
// converges; a declaration that wraps its own parameter grows a new name every
// round and is rejected here instead of expanding forever.
const maxInstantiationDepth = 8

// typeParamScope is the set of type parameters a declaration binds. It is nil
// outside a generic declaration, where an unknown plain type name is an
// ordinary forward reference rather than a mistyped parameter.
type typeParamScope struct {
	owner  string
	params map[string]struct{}
}

func newTypeParamScope(owner string, params []string) *typeParamScope {
	if len(params) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(params))
	for _, param := range params {
		set[param] = struct{}{}
	}
	return &typeParamScope{owner: owner, params: set}
}

func (scope *typeParamScope) binds(name string) bool {
	if scope == nil {
		return false
	}
	_, ok := scope.params[name]
	return ok
}

// instantiation is one concrete use of a generic declaration.
type instantiation struct {
	name      string
	base      string
	args      []string
	pos       position
	ancestors []string
}

func genericInstanceName(base string, args []string) string {
	return base + "<" + strings.Join(args, ",") + ">"
}

func substitutionsFor(params, args []string) map[string]string {
	substitutions := make(map[string]string, len(params))
	for index, param := range params {
		substitutions[param] = args[index]
	}
	return substitutions
}

// instantiationDepth is how deeply angle brackets nest in a canonical name.
func instantiationDepth(name string) int {
	depth, deepest := 0, 0
	for index := 0; index < len(name); index++ {
		if isTypeArrow(name, index) {
			index++
			continue
		}
		switch name[index] {
		case '<':
			depth++
			if depth > deepest {
				deepest = depth
			}
		case '>':
			depth--
		}
	}
	return deepest
}

// checkGenericDeclarations validates open declarations once, with their own
// type parameters in scope, so a mistake in a generic is reported against the
// generic rather than against every instantiation of it.
func (p *program) checkGenericDeclarations() {
	p.linkGenericMethodReceivers()
	for _, implementation := range p.genericMethodImpls {
		if implementation.receiverCanonical == "" {
			continue
		}
		p.checkGenericCallable(implementation.namespace, implementation.aliases,
			newTypeParamScope(implementation.qualified, implementation.typeParams), implementation.params, implementation.result)
	}
	for _, name := range sortedKeys(p.genericFunctions) {
		function := p.genericFunctions[name]
		p.checkGenericCallable(function.namespace, function.aliases,
			newTypeParamScope(function.qualified, function.typeParams), function.params, function.result)
	}
	for _, name := range sortedKeys(p.genericClasses) {
		class := p.genericClasses[name]
		scope := newTypeParamScope(class.qualified, class.typeParams)
		for _, fieldName := range sortedKeys(class.fields) {
			field := class.fields[fieldName]
			p.checkTypeRef(field.typ)
			p.checkTypeNameScoped(field.typ.pos, class.namespace, scope, false,
				p.canonicalType(class.namespace, class.aliases, field.typ))
		}
		for _, methodName := range sortedKeys(class.methods) {
			method := class.methods[methodName]
			p.checkGenericCallable(method.namespace, method.aliases, scope, method.params, method.result)
		}
	}
	for _, name := range sortedKeys(p.genericInterfaces) {
		iface := p.genericInterfaces[name]
		scope := newTypeParamScope(iface.qualified, iface.typeParams)
		for _, methodName := range sortedKeys(iface.methods) {
			method := iface.methods[methodName]
			p.checkGenericCallable(method.namespace, method.aliases, scope, method.params, method.result)
		}
	}
}

func (p *program) checkGenericCallable(namespace string, aliases map[string]aliasDecl, scope *typeParamScope, params []paramDecl, result typeRef) {
	for _, param := range params {
		p.checkTypeRef(param.typ)
		p.checkTypeNameScoped(param.typ.pos, namespace, scope, false, p.canonicalType(namespace, aliases, param.typ))
	}
	p.checkTypeRef(result)
	p.checkTypeNameScoped(result.pos, namespace, scope, false, p.canonicalType(namespace, aliases, result))
}

// linkGenericMethodReceivers resolves each generic method implementation to the
// open declaration it extends. Receiver mistakes are reported once here; the
// clones an instantiation produces carry a resolved receiver and flow through
// ordinary method linking.
func (p *program) linkGenericMethodReceivers() {
	for _, implementation := range p.genericMethodImpls {
		if implementation.receiverCanonical != "" {
			p.recordGenericImplementation(implementation)
			continue
		}
		if _, alias := implementation.aliases[implementation.receiver.name]; alias {
			p.add(implementation.receiver.pos, diagnosticCodeAliasedMethodReceiver,
				"method receivers must use a local or absolute class name, not alias %s", implementation.receiver.name)
			continue
		}
		receiver := implementation.receiver.name
		if !isAbsoluteCanonicalName(receiver) {
			receiver = qualify(implementation.namespace, receiver)
		}
		generic := p.genericClasses[receiver]
		if generic == nil {
			if p.classes[receiver] != nil {
				p.add(implementation.receiver.pos, diagnosticCodeGenericType,
					"%s takes no type arguments", receiver)
				continue
			}
			p.add(implementation.receiver.pos, diagnosticCodeMethodReceiver,
				"method receiver %s is not a class", implementation.receiver.name)
			continue
		}
		if len(implementation.typeParams) != len(generic.typeParams) {
			p.addTypeArityDiagnostic(implementation.receiver.pos, receiver, len(generic.typeParams), len(implementation.typeParams))
			continue
		}
		implementation.receiverCanonical = receiver
		implementation.qualified = receiver + "." + implementation.name
		p.recordGenericImplementation(implementation)
	}
}

// recordGenericImplementation attaches an implementation to the open
// declaration it extends. Instantiation clones from genericMethodImpls; this
// exists so documentation describes the declaration a source actually wrote.
func (p *program) recordGenericImplementation(implementation *functionDecl) {
	if generic := p.genericClasses[implementation.receiverCanonical]; generic != nil {
		if _, exists := generic.implementations[implementation.name]; !exists {
			generic.implementations[implementation.name] = implementation
		}
	}
}

// instantiateGenerics materializes every concrete instantiation the program
// mentions, following the ones each new declaration introduces until the set is
// closed. Nothing else is generated, so an unused generic costs nothing.
func (p *program) instantiateGenerics() {
	if len(p.genericClasses)+len(p.genericInterfaces)+len(p.genericFunctions) == 0 {
		return
	}
	pending := p.collectInstantiationRoots()
	created := make(map[string]struct{}, len(pending))
	for len(pending) > 0 {
		request := pending[0]
		pending = pending[1:]
		if _, exists := created[request.name]; exists {
			continue
		}
		created[request.name] = struct{}{}
		recursiveDepth := 1
		for _, ancestor := range request.ancestors {
			if ancestor == request.base {
				recursiveDepth++
			}
		}
		if recursiveDepth > maxInstantiationDepth || instantiationDepth(request.name) > maxInstantiationDepth {
			p.add(request.pos, diagnosticCodeGenericExpansion,
				"%s expands without limit; a generic declaration may contain itself only at the same type arguments", displayName(request.name))
			continue
		}
		next := p.instantiate(request)
		ancestors := append(append([]string(nil), request.ancestors...), request.base)
		for index := range next {
			next[index].ancestors = ancestors
		}
		pending = append(pending, next...)
	}
}

func (p *program) collectInstantiationRoots() []instantiation {
	var found []instantiation
	for _, name := range sortedKeys(p.functions) {
		p.collectFromCallable(p.functions[name], &found)
	}
	for _, implementation := range p.methodImpls {
		p.collectFromCallable(implementation, &found)
	}
	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		for _, fieldName := range sortedKeys(class.fields) {
			field := class.fields[fieldName]
			p.collectFromType(field.typ.pos, class.namespace, class.aliases, field.typ.name, &found)
		}
		for _, methodName := range sortedKeys(class.methods) {
			p.collectFromSignature(class.methods[methodName], &found)
		}
	}
	for _, name := range sortedKeys(p.interfaces) {
		iface := p.interfaces[name]
		for _, methodName := range sortedKeys(iface.methods) {
			p.collectFromSignature(iface.methods[methodName], &found)
		}
	}
	for _, name := range sortedKeys(p.unions) {
		union := p.unions[name]
		for _, variantName := range union.order {
			for _, field := range union.variants[variantName].fields {
				p.collectFromType(field.typ.pos, union.namespace, union.aliases, field.typ.name, &found)
			}
		}
	}
	return found
}

func (p *program) collectFromSignature(method *methodSignature, found *[]instantiation) {
	for _, param := range method.params {
		p.collectFromType(param.typ.pos, method.namespace, method.aliases, param.typ.name, found)
	}
	p.collectFromType(method.result.pos, method.namespace, method.aliases, method.result.name, found)
	for _, thrown := range method.throws {
		p.collectFromType(thrown.pos, method.namespace, method.aliases, thrown.name, found)
	}
}

func (p *program) collectFromCallable(function *functionDecl, found *[]instantiation) {
	for _, param := range function.params {
		p.collectFromType(param.typ.pos, function.namespace, function.aliases, param.typ.name, found)
	}
	p.collectFromType(function.result.pos, function.namespace, function.aliases, function.result.name, found)
	for _, thrown := range function.throws {
		p.collectFromType(thrown.pos, function.namespace, function.aliases, thrown.name, found)
	}
	p.collectFromBody(function, found)
}

// collectFromBody finds the instantiations a function body names.
func (p *program) collectFromBody(function *functionDecl, found *[]instantiation) {
	walkAST(function.ast, func(node any) {
		switch value := node.(type) {
		case *lambdaExpression:
			for _, param := range value.params {
				p.collectFromType(param.typ.pos, function.namespace, function.aliases, param.typ.name, found)
			}
			p.collectFromType(value.result.pos, function.namespace, function.aliases, value.result.name, found)
			for _, thrown := range value.throws {
				p.collectFromType(thrown.pos, function.namespace, function.aliases, thrown.name, found)
			}
		case *callExpression:
			for _, typeArg := range value.typeArgs {
				p.collectFromType(typeArg.pos, function.namespace, function.aliases, typeArg.name, found)
			}
			p.collectFromCall(function, value, found)
		case *objectExpression:
			p.collectFromType(value.pos, function.namespace, function.aliases, value.typeName, found)
		case *catchExpression:
			for _, arm := range value.arms {
				p.collectFromType(arm.errorType.pos, function.namespace, function.aliases, arm.errorType.name, found)
			}
		}
	})
}

func (p *program) collectFromCall(function *functionDecl, node *callExpression, found *[]instantiation) {
	name, ok := node.callee.(*nameExpression)
	if !ok || (strings.Contains(name.name, ".") && !isAbsoluteCanonicalName(name.name)) {
		return
	}
	generic := p.genericFunctions[p.resolveName(function, name.name)]
	if generic == nil || len(node.typeArgs) != len(generic.typeParams) {
		return
	}
	args := make([]string, len(node.typeArgs))
	for index, typeArg := range node.typeArgs {
		args[index] = p.canonicalType(function.namespace, function.aliases, typeArg)
	}
	*found = append(*found, instantiation{
		name: genericInstanceName(generic.qualified, args),
		base: generic.qualified,
		args: args,
		pos:  node.pos,
	})
}

func (p *program) collectFromType(pos position, namespace string, aliases map[string]aliasDecl, name string, found *[]instantiation) {
	p.collectFromCanonicalType(pos, p.canonicalTypeName(namespace, aliases, name), found)
}

func (p *program) collectFromCanonicalType(pos position, canonical string, found *[]instantiation) {
	parsed := parseTypeName(canonical)
	switch parsed.kind {
	case typeKindOptional, typeKindArray:
		p.collectFromCanonicalType(pos, parsed.base, found)
	case typeKindTuple:
		for _, element := range parsed.args {
			p.collectFromCanonicalType(pos, element, found)
		}
	case typeKindCallable:
		for _, param := range parsed.args {
			p.collectFromCanonicalType(pos, param, found)
		}
		p.collectFromCanonicalType(pos, parsed.base, found)
		for _, thrown := range parsed.throws {
			p.collectFromCanonicalType(pos, thrown, found)
		}
	case typeKindGeneric:
		for _, arg := range parsed.args {
			p.collectFromCanonicalType(pos, arg, found)
		}
		if p.genericClasses[parsed.base] != nil || p.genericInterfaces[parsed.base] != nil {
			*found = append(*found, instantiation{name: canonical, base: parsed.base, args: parsed.args, pos: pos})
		}
	}
}

func (p *program) instantiate(request instantiation) []instantiation {
	switch {
	case p.genericClasses[request.base] != nil:
		return p.instantiateClass(request)
	case p.genericInterfaces[request.base] != nil:
		return p.instantiateInterface(request)
	case p.genericFunctions[request.base] != nil:
		return p.instantiateFunction(request)
	}
	return nil
}

func (p *program) instantiateClass(request instantiation) []instantiation {
	generic := p.genericClasses[request.base]
	if len(request.args) != len(generic.typeParams) {
		// The arity diagnostic belongs to the type that named it, which
		// checkTypeName reports against the referring declaration.
		return nil
	}
	substitutions := substitutionsFor(generic.typeParams, request.args)
	instance := &classDecl{
		name:            generic.name,
		qualified:       request.name,
		namespace:       generic.namespace,
		aliases:         generic.aliases,
		isError:         generic.isError,
		nativeResource:  generic.nativeResource,
		extension:       generic.extension,
		instanceOf:      generic.qualified,
		fields:          make(map[string]fieldDecl, len(generic.fields)),
		methods:         make(map[string]*methodSignature, len(generic.methods)),
		effective:       make(map[string]*methodSignature, len(generic.methods)),
		implementations: make(map[string]*functionDecl, len(generic.methods)),
		documentation:   generic.documentation,
		pos:             generic.pos,
		annotations:     generic.annotations,
	}
	p.classes[request.name] = instance

	var found []instantiation
	for name, field := range generic.fields {
		typ := p.substitutedRef(generic.namespace, generic.aliases, substitutions, field.typ)
		instance.fields[name] = fieldDecl{name: field.name, typ: typ, jsonName: field.jsonName, annotations: field.annotations, documentation: field.documentation, pos: field.pos}
		p.collectFromCanonicalType(field.typ.pos, typ.name, &found)
	}
	for name, method := range generic.methods {
		instance.methods[name] = p.substitutedSignature(method, substitutions, &found)
	}
	for _, implementation := range p.genericMethodImpls {
		if implementation.receiverCanonical != generic.qualified {
			continue
		}
		clone := p.substitutedFunction(implementation, substitutionsFor(implementation.typeParams, request.args), request.name+"."+implementation.name, &found)
		clone.receiver = typeRef{name: request.name, pos: implementation.receiver.pos}
		clone.receiverCanonical = request.name
		clone.inline = implementation.inline
		p.methodImpls = append(p.methodImpls, clone)
	}
	return found
}

func (p *program) instantiateInterface(request instantiation) []instantiation {
	generic := p.genericInterfaces[request.base]
	if len(request.args) != len(generic.typeParams) {
		return nil
	}
	substitutions := substitutionsFor(generic.typeParams, request.args)
	instance := &interfaceDecl{
		name:          generic.name,
		qualified:     request.name,
		namespace:     generic.namespace,
		instanceOf:    generic.qualified,
		methods:       make(map[string]*methodSignature, len(generic.methods)),
		documentation: generic.documentation,
		annotations:   generic.annotations,
		pos:           generic.pos,
	}
	p.interfaces[request.name] = instance
	var found []instantiation
	for name, method := range generic.methods {
		instance.methods[name] = p.substitutedSignature(method, substitutions, &found)
	}
	return found
}

func (p *program) instantiateFunction(request instantiation) []instantiation {
	generic := p.genericFunctions[request.base]
	if len(request.args) != len(generic.typeParams) {
		return nil
	}
	var found []instantiation
	clone := p.substitutedFunction(generic, substitutionsFor(generic.typeParams, request.args), request.name, &found)
	p.functions[request.name] = clone
	return found
}

// substitutedFunction clones one generic callable at concrete type arguments.
func (p *program) substitutedFunction(generic *functionDecl, substitutions map[string]string, qualified string, found *[]instantiation) *functionDecl {
	clone := &functionDecl{
		name:          generic.name,
		qualified:     qualified,
		namespace:     generic.namespace,
		aliases:       generic.aliases,
		params:        p.substitutedParams(generic.namespace, generic.aliases, substitutions, generic.params, found),
		result:        p.substitutedRef(generic.namespace, generic.aliases, substitutions, generic.result),
		throws:        p.substitutedRefs(generic.namespace, generic.aliases, substitutions, generic.throws, found),
		body:          generic.body,
		instanceOf:    generic.qualified,
		documentation: generic.documentation,
		pos:           generic.pos,
		annotations:   generic.annotations,
	}
	clone.ast = cloneBlock(generic.ast)
	substituteASTTypes(clone.ast, substitutions)
	p.collectFromCanonicalType(generic.result.pos, clone.result.name, found)
	p.collectFromBody(clone, found)
	return clone
}

func cloneBlock(block *blockNode) *blockNode {
	if block == nil {
		return nil
	}
	clone := *block
	clone.statements = make([]statementNode, len(block.statements))
	for index, statement := range block.statements {
		clone.statements[index] = cloneStatement(statement)
	}
	return &clone
}

func cloneStatement(statement statementNode) statementNode {
	switch node := statement.(type) {
	case *letStatement:
		clone := *node
		clone.names = append([]string(nil), node.names...)
		clone.value = cloneExpression(node.value)
		return &clone
	case *asyncLetStatement:
		clone := *node
		clone.call = cloneExpression(node.call).(*callExpression)
		return &clone
	case *assignmentStatement:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *forStatement:
		clone := *node
		clone.bindings = append([]string(nil), node.bindings...)
		clone.iterable = cloneExpression(node.iterable)
		clone.body = cloneBlock(node.body)
		return &clone
	case *breakStatement:
		clone := *node
		return &clone
	case *continueStatement:
		clone := *node
		return &clone
	case *throwStatement:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *returnStatement:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *expressionStatement:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	default:
		panic(fmt.Sprintf("cannot clone statement %T", statement))
	}
}

func cloneExpression(expression expressionNode) expressionNode {
	if expression == nil {
		return nil
	}
	switch node := expression.(type) {
	case *invalidExpression:
		clone := *node
		return &clone
	case *literalExpression:
		clone := *node
		return &clone
	case *tupleExpression:
		clone := *node
		clone.elements = cloneExpressions(node.elements)
		return &clone
	case *arrayExpression:
		clone := *node
		clone.elements = cloneExpressions(node.elements)
		return &clone
	case *mapExpression:
		clone := *node
		clone.entries = make([]mapEntryExpression, len(node.entries))
		for index, entry := range node.entries {
			clone.entries[index] = entry
			clone.entries[index].key = cloneExpression(entry.key)
			clone.entries[index].value = cloneExpression(entry.value)
		}
		return &clone
	case *rangeExpression:
		clone := *node
		clone.start = cloneExpression(node.start)
		clone.end = cloneExpression(node.end)
		return &clone
	case *templateExpression:
		clone := *node
		return &clone
	case *nameExpression:
		clone := *node
		return &clone
	case *lambdaExpression:
		clone := *node
		clone.params = append([]paramDecl(nil), node.params...)
		clone.throws = append([]typeRef(nil), node.throws...)
		clone.body = cloneBlock(node.body)
		clone.fn = nil
		clone.captures = nil
		clone.resolved = ""
		return &clone
	case *objectExpression:
		clone := *node
		clone.fields = make([]objectFieldExpression, len(node.fields))
		for index, field := range node.fields {
			clone.fields[index] = field
			clone.fields[index].value = cloneExpression(field.value)
		}
		return &clone
	case *callExpression:
		clone := *node
		clone.callee = cloneExpression(node.callee)
		clone.typeArgs = append([]typeRef(nil), node.typeArgs...)
		clone.args = cloneExpressions(node.args)
		clone.resolvedCallee = ""
		clone.resolvedTypeArgs = nil
		clone.resolvedParams = nil
		clone.resolvedArgumentTypes = nil
		clone.resolvedResult = ""
		clone.resolvedReceiver = ""
		clone.resolvedThrows = nil
		clone.resolvedNative = ""
		clone.resolvedCallable = false
		return &clone
	case *awaitExpression:
		clone := *node
		return &clone
	case *unaryExpression:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *binaryExpression:
		clone := *node
		clone.left = cloneExpression(node.left)
		clone.right = cloneExpression(node.right)
		return &clone
	case *ifExpression:
		clone := *node
		clone.condition = cloneExpression(node.condition)
		clone.thenBlock = cloneBlock(node.thenBlock)
		clone.elseBlock = cloneBlock(node.elseBlock)
		return &clone
	case *catchExpression:
		clone := *node
		clone.value = cloneExpression(node.value)
		clone.arms = make([]catchArm, len(node.arms))
		for index, arm := range node.arms {
			clone.arms[index] = arm
			clone.arms[index].value = cloneExpression(arm.value)
		}
		return &clone
	case *resultExpression:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *propagateExpression:
		clone := *node
		clone.value = cloneExpression(node.value)
		return &clone
	case *usingExpression:
		clone := *node
		clone.initializer = cloneExpression(node.initializer)
		clone.body = cloneBlock(node.body)
		return &clone
	case *matchExpression:
		clone := *node
		clone.value = cloneExpression(node.value)
		clone.arms = make([]matchArm, len(node.arms))
		for index, arm := range node.arms {
			clone.arms[index] = arm
			clone.arms[index].bindings = append([]string(nil), arm.bindings...)
			clone.arms[index].value = cloneExpression(arm.value)
		}
		return &clone
	default:
		panic(fmt.Sprintf("cannot clone expression %T", expression))
	}
}

func cloneExpressions(expressions []expressionNode) []expressionNode {
	clones := make([]expressionNode, len(expressions))
	for index, expression := range expressions {
		clones[index] = cloneExpression(expression)
	}
	return clones
}
func substituteASTTypes(block *blockNode, substitutions map[string]string) {
	walkAST(block, func(node any) {
		switch value := node.(type) {
		case *lambdaExpression:
			for index := range value.params {
				value.params[index].typ.name = substituteTypeParams(value.params[index].typ.name, substitutions)
			}
			value.result.name = substituteTypeParams(value.result.name, substitutions)
			for index := range value.throws {
				value.throws[index].name = substituteTypeParams(value.throws[index].name, substitutions)
			}
		case *objectExpression:
			value.typeName = substituteTypeParams(value.typeName, substitutions)
		case *callExpression:
			for index := range value.typeArgs {
				value.typeArgs[index].name = substituteTypeParams(value.typeArgs[index].name, substitutions)
			}
		case *catchExpression:
			for index := range value.arms {
				value.arms[index].errorType.name = substituteTypeParams(value.arms[index].errorType.name, substitutions)
			}
		}
	})
}

func (p *program) substitutedSignature(method *methodSignature, substitutions map[string]string, found *[]instantiation) *methodSignature {
	return &methodSignature{
		name:           method.name,
		namespace:      method.namespace,
		ownerNamespace: method.ownerNamespace,
		aliases:        method.aliases,
		params:         p.substitutedParams(method.namespace, method.aliases, substitutions, method.params, found),
		result:         p.substitutedRef(method.namespace, method.aliases, substitutions, method.result),
		throws:         p.substitutedRefs(method.namespace, method.aliases, substitutions, method.throws, found),
		annotations:    method.annotations,
		documentation:  method.documentation,
		pos:            method.pos,
	}
}

func (p *program) substitutedParams(namespace string, aliases map[string]aliasDecl, substitutions map[string]string, params []paramDecl, found *[]instantiation) []paramDecl {
	substituted := make([]paramDecl, len(params))
	for index, param := range params {
		substituted[index] = paramDecl{name: param.name, typ: p.substitutedRef(namespace, aliases, substitutions, param.typ), annotations: param.annotations}
		p.collectFromCanonicalType(param.typ.pos, substituted[index].typ.name, found)
	}
	return substituted
}

func (p *program) substitutedRefs(namespace string, aliases map[string]aliasDecl, substitutions map[string]string, refs []typeRef, found *[]instantiation) []typeRef {
	substituted := make([]typeRef, len(refs))
	for index, ref := range refs {
		substituted[index] = p.substitutedRef(namespace, aliases, substitutions, ref)
		p.collectFromCanonicalType(ref.pos, substituted[index].name, found)
	}
	return substituted
}

// substitutedRef replaces type parameters before canonicalizing, so a
// parameter named like a declaration in the same namespace still resolves to
// the type argument rather than to that declaration.
func (p *program) substitutedRef(namespace string, aliases map[string]aliasDecl, substitutions map[string]string, ref typeRef) typeRef {
	return typeRef{
		name: p.canonicalTypeName(namespace, aliases, substituteTypeParams(ref.name, substitutions)),
		pos:  ref.pos,
	}
}

// walkAST visits every statement and expression reachable from node.
func walkAST(node any, visit func(any)) {
	if node == nil {
		return
	}
	switch value := node.(type) {
	case *blockNode:
		if value == nil {
			return
		}
		visit(value)
		for _, statement := range value.statements {
			walkAST(statement, visit)
		}
		return
	case *letStatement:
		visit(value)
		walkAST(value.value, visit)
	case *asyncLetStatement:
		visit(value)
		walkAST(value.call, visit)
	case *assignmentStatement:
		visit(value)
		walkAST(value.value, visit)
	case *forStatement:
		visit(value)
		walkAST(value.iterable, visit)
		walkAST(value.body, visit)
	case *throwStatement:
		visit(value)
		walkAST(value.value, visit)
	case *returnStatement:
		visit(value)
		walkAST(value.value, visit)
	case *expressionStatement:
		visit(value)
		walkAST(value.value, visit)
	case *tupleExpression:
		visit(value)
		for _, element := range value.elements {
			walkAST(element, visit)
		}
	case *arrayExpression:
		visit(value)
		for _, element := range value.elements {
			walkAST(element, visit)
		}
	case *mapExpression:
		visit(value)
		for _, entry := range value.entries {
			walkAST(entry.key, visit)
			walkAST(entry.value, visit)
		}
	case *rangeExpression:
		visit(value)
		walkAST(value.start, visit)
		walkAST(value.end, visit)
	case *objectExpression:
		visit(value)
		for _, field := range value.fields {
			walkAST(field.value, visit)
		}
	case *lambdaExpression:
		visit(value)
		walkAST(value.body, visit)
	case *callExpression:
		visit(value)
		walkAST(value.callee, visit)
		for _, argument := range value.args {
			walkAST(argument, visit)
		}
	case *unaryExpression:
		visit(value)
		walkAST(value.value, visit)
	case *binaryExpression:
		visit(value)
		walkAST(value.left, visit)
		walkAST(value.right, visit)
	case *ifExpression:
		visit(value)
		walkAST(value.condition, visit)
		walkAST(value.thenBlock, visit)
		walkAST(value.elseBlock, visit)
	case *catchExpression:
		visit(value)
		walkAST(value.value, visit)
		for _, arm := range value.arms {
			walkAST(arm.value, visit)
		}
	case *resultExpression:
		visit(value)
		walkAST(value.value, visit)
	case *propagateExpression:
		visit(value)
		walkAST(value.value, visit)
	case *usingExpression:
		visit(value)
		walkAST(value.initializer, visit)
		walkAST(value.body, visit)
	case *matchExpression:
		visit(value)
		walkAST(value.value, visit)
		for _, arm := range value.arms {
			walkAST(arm.value, visit)
		}
	default:
		visit(node)
	}
}
