package compiler

import (
	"context"
	"strings"
)

// functionCallableType is the callable type a named function reads as when it
// is used as a value. Its contracts are sorted so one function has exactly one
// callable spelling.
func (p *program) functionCallableType(function *functionDecl) string {
	params := make([]string, len(function.params))
	for index, param := range function.params {
		params[index] = p.resolveType(function.namespace, function.aliases, param.typ)
	}
	result := p.resolveType(function.namespace, function.aliases, function.result)
	throws := make(map[string]struct{}, len(function.throws))
	for _, thrown := range function.throws {
		throws[p.canonicalTypeName(function.namespace, function.aliases, thrown.name)] = struct{}{}
	}
	return callableType(params, result, sortedSet(throws), sortedOperationEffects(function.operationSet))
}

// checkFunctionValue types a named function used without call parentheses. It
// follows the ordinary namespace, alias, and visibility rules a call follows,
// so a function is reachable as a value exactly where it is reachable as a
// call target.
func (p *program) checkFunctionValue(node *nameExpression, scope *astScope) (expressionInfo, bool) {
	if strings.Contains(node.name, ".") && !isAbsoluteCanonicalName(node.name) {
		if _, aliased := scope.function.aliases[node.name]; !aliased {
			return expressionInfo{}, false
		}
	}
	callee := p.resolveFunction(scope.function, node.name)
	if callee == nil {
		return expressionInfo{}, false
	}
	unknown := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	if !p.requireAccess(node.pos, scope.function.namespace, callee.namespace, callee.name, "function") {
		return unknown, true
	}
	if len(callee.typeParams) > 0 {
		p.add(node.pos, diagnosticCodeTypeArguments,
			"%s is generic and has no single callable type; call it with its type arguments", node.name)
		return unknown, true
	}
	p.markStandardLibraryUse(callee.namespace, callee.native)
	typ := p.functionCallableType(callee)
	// The value carries the declaration's types into this namespace, so the same
	// visibility and runtime-support rules a declared type gets apply here.
	p.checkTypeName(node.pos, scope.function.namespace, typ)
	return expressionInfo{typ: typ, effects: make(effectSet)}, true
}

// checkLambdaExpression types a lambda. The body is checked once, against a
// scope holding exactly the captured bindings plus the declared parameters, and
// the node keeps its resolved type so a second visit is stable.
func (p *program) checkLambdaExpression(node *lambdaExpression, scope *astScope) expressionInfo {
	if node.resolved != "" {
		return expressionInfo{typ: node.resolved, effects: make(effectSet)}
	}
	function := &functionDecl{
		name:       "lambda",
		qualified:  "lambda in " + scope.function.qualified,
		namespace:  scope.function.namespace,
		aliases:    scope.function.aliases,
		params:     node.params,
		result:     node.result,
		throws:     node.throws,
		operations: node.operations,
		ast:        node.body,
		pos:        node.pos,
	}
	function.throwSet = p.resolveThrows(function.namespace, function.aliases, function.throws)
	function.operationSet = p.resolveOperationEffects(function.operations)

	lambdaScope := newASTScope(function, len(node.params)+len(scope.locals))
	lambdaScope.recursive = recursiveNames(scope)
	bound := make(map[string]struct{}, len(node.params))
	for _, param := range node.params {
		bound[param.name] = struct{}{}
	}
	node.captures = p.bindLambdaCaptures(node, scope, lambdaScope, bound)

	paramTypes := make([]string, len(node.params))
	seen := make(map[string]struct{}, len(node.params))
	for index, param := range node.params {
		p.checkTypeRef(param.typ)
		paramTypes[index] = p.resolveType(function.namespace, function.aliases, param.typ)
		p.checkTypeName(param.typ.pos, function.namespace, paramTypes[index])
		if _, duplicate := seen[param.name]; duplicate {
			p.add(node.pos, diagnosticCodeTypeMismatch, "duplicate lambda parameter %s", param.name)
			continue
		}
		seen[param.name] = struct{}{}
		lambdaScope.bind(param.name, paramTypes[index])
	}
	p.checkTypeRef(node.result)
	resultType := p.resolveType(function.namespace, function.aliases, node.result)
	p.checkTypeName(node.result.pos, function.namespace, resultType)

	p.checkCallableBody(function, lambdaScope)

	node.fn = function
	node.resolved = callableType(paramTypes, resultType, sortedSet(function.throwSet), sortedOperationEffects(function.operationSet))
	return expressionInfo{typ: node.resolved, effects: make(effectSet)}
}

// bindLambdaCaptures copies the surrounding bindings the body reads into the
// lambda scope and reports the ones a lambda may not hold: a pending async
// binding has no value yet, and an active using binding is owned by the scope
// that must close it.
func (p *program) bindLambdaCaptures(node *lambdaExpression, scope, lambdaScope *astScope, bound map[string]struct{}) []string {
	referenced := make(map[string]struct{})

	collectReferencedNames(node.body, referenced, bound)
	captures := make([]string, 0, len(referenced))
	for _, name := range sortedKeys(referenced) {
		if _, pending := scope.pending[name]; pending {
			p.add(node.pos, diagnosticCodeLambdaCapture, "lambda cannot capture pending binding %s; await it first", name)
			continue
		}
		if _, active := scope.usingBindings[name]; active {
			p.add(node.pos, diagnosticCodeLambdaCapture, "lambda cannot capture using binding %s; it is closed when its scope exits", name)
			continue
		}
		typ, exists := scope.lookup(name)
		if !exists {
			continue
		}
		captures = append(captures, name)
		lambdaScope.locals[name] = typ
		lambdaScope.captured[name] = struct{}{}
	}
	return captures
}

// recursiveNames carries the let bindings currently being initialized into a
// lambda body, where naming one is the rejected directly recursive lambda.
func recursiveNames(scope *astScope) map[string]struct{} {
	if len(scope.initializing) == 0 && len(scope.recursive) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(scope.initializing)+len(scope.recursive))
	for name := range scope.recursive {
		names[name] = struct{}{}
	}
	for _, name := range scope.initializing {
		names[name] = struct{}{}
	}
	return names
}

func (p *program) reportRecursiveLambda(pos position, scope *astScope, name string) bool {
	if _, recursive := scope.recursive[name]; !recursive {
		return false
	}
	p.add(pos, diagnosticCodeLambdaCapture,
		"%s is not in scope inside its own initializer; use a named function for recursion", name)
	return true
}

func collectReferencedNames(node any, names, bound map[string]struct{}) {
	switch value := node.(type) {
	case nil:
		return
	case *blockNode:
		if value == nil {
			return
		}
		blockBound := cloneBoundNames(bound)
		for _, statement := range value.statements {
			collectReferencedNames(statement, names, blockBound)
		}
	case *letStatement:
		collectReferencedNames(value.value, names, bound)
		for _, name := range value.names {
			bound[name] = struct{}{}
		}
	case *asyncLetStatement:
		collectReferencedNames(value.call, names, bound)
		bound[value.name] = struct{}{}
	case *assignmentStatement:
		collectReferencedName(value.name, names, bound)
		collectReferencedNames(value.value, names, bound)
	case *forStatement:
		collectReferencedNames(value.iterable, names, bound)
		bodyBound := cloneBoundNames(bound)
		for _, name := range value.bindings {
			bodyBound[name] = struct{}{}
		}
		collectReferencedNames(value.body, names, bodyBound)
	case *throwStatement:
		collectReferencedNames(value.value, names, bound)
	case *returnStatement:
		collectReferencedNames(value.value, names, bound)
	case *expressionStatement:
		collectReferencedNames(value.value, names, bound)
	case *nameExpression:
		collectReferencedName(value.name, names, bound)
	case *templateExpression:
		for _, name := range templateNames(value.text) {
			collectReferencedName(name, names, bound)
		}
	case *awaitExpression:
		collectReferencedName(value.name, names, bound)
	case *lambdaExpression:
		lambdaBound := cloneBoundNames(bound)
		for _, param := range value.params {
			lambdaBound[param.name] = struct{}{}
		}
		collectReferencedNames(value.body, names, lambdaBound)
	case *tupleExpression:
		for _, element := range value.elements {
			collectReferencedNames(element, names, bound)
		}
	case *arrayExpression:
		for _, element := range value.elements {
			collectReferencedNames(element, names, bound)
		}
	case *mapExpression:
		for _, entry := range value.entries {
			collectReferencedNames(entry.key, names, bound)
			collectReferencedNames(entry.value, names, bound)
		}
	case *rangeExpression:
		collectReferencedNames(value.start, names, bound)
		collectReferencedNames(value.end, names, bound)
	case *objectExpression:
		for _, field := range value.fields {
			collectReferencedNames(field.value, names, bound)
		}
	case *callExpression:
		collectReferencedNames(value.callee, names, bound)
		for _, argument := range value.args {
			collectReferencedNames(argument, names, bound)
		}
	case *unaryExpression:
		collectReferencedNames(value.value, names, bound)
	case *binaryExpression:
		collectReferencedNames(value.left, names, bound)
		collectReferencedNames(value.right, names, bound)
	case *ifExpression:
		collectReferencedNames(value.condition, names, bound)
		collectReferencedNames(value.thenBlock, names, bound)
		collectReferencedNames(value.elseBlock, names, bound)
	case *catchExpression:
		collectReferencedNames(value.value, names, bound)
		for _, arm := range value.arms {
			armBound := cloneBoundNames(bound)
			binding := arm.binding
			if binding == "" {
				binding = value.binding
			}
			if binding != "" {
				armBound[binding] = struct{}{}
			}
			collectReferencedNames(arm.value, names, armBound)
		}
	case *resultExpression:
		collectReferencedNames(value.value, names, bound)
	case *propagateExpression:
		collectReferencedNames(value.value, names, bound)
	case *usingExpression:
		collectReferencedNames(value.initializer, names, bound)
		bodyBound := cloneBoundNames(bound)
		bodyBound[value.name] = struct{}{}
		collectReferencedNames(value.body, names, bodyBound)
	case *matchExpression:
		collectReferencedNames(value.value, names, bound)
		for _, arm := range value.arms {
			armBound := cloneBoundNames(bound)
			if arm.binding != "" {
				armBound[arm.binding] = struct{}{}
			}
			for _, binding := range arm.bindings {
				armBound[binding] = struct{}{}
			}
			collectReferencedNames(arm.value, names, armBound)
		}
	}
}

func collectReferencedName(name string, names, bound map[string]struct{}) {
	name = rootName(name)
	if _, local := bound[name]; !local {
		names[name] = struct{}{}
	}
}

func cloneBoundNames(bound map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(bound))
	for name := range bound {
		clone[name] = struct{}{}
	}
	return clone
}

func rootName(name string) string {
	if index := strings.IndexByte(name, '.'); index >= 0 {
		return name[:index]
	}
	return name
}

// templateNames lists the interpolations of a template literal, using the same
// scan the checker and both backends use to render one.
func templateNames(template string) []string {
	var names []string
	for {
		start := strings.Index(template, "${")
		if start < 0 {
			return names
		}
		template = template[start+2:]
		end := strings.IndexByte(template, '}')
		if end < 0 {
			return names
		}
		names = append(names, strings.TrimSpace(template[:end]))
		template = template[end+1:]
	}
}

// checkCallableInvocation types a call whose target is a callable value rather
// than a statically named function or method. Arity, argument assignability,
// the result type, and the checked error and operation effects follow the
// callable type alone, so invoking a value agrees with invoking the function
// it came from.
func (p *program) checkCallableInvocation(node *callExpression, scope *astScope, calleeType, label string, includeThrows bool) expressionInfo {
	params, result, throws, operations, callable := callableTypeParts(calleeType)
	if !callable {
		info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
		if base, optional := optionalBase(calleeType); optional && isCallableType(base) {
			p.add(node.pos, diagnosticCodeOptionalReceiver,
				"%s is %s and may be null; compare it with null and call it inside the branch that proved it is not",
				label, displayName(calleeType))
		} else if calleeType != typeUnknown {
			p.add(node.pos, diagnosticCodeNotCallable, "%s is %s and cannot be called", label, displayName(calleeType))
		}
		// The arguments are still checked, so one wrong target does not hide
		// every mistake inside the call.
		for _, argument := range node.args {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
		}
		return info
	}
	node.resolvedCallable = true
	if len(node.typeArgs) > 0 {
		p.add(node.pos, diagnosticCodeTypeArguments, "%s does not take type arguments", label)
	}
	info := expressionInfo{typ: result, effects: make(effectSet)}
	node.resolvedParams = params
	node.resolvedResult = result
	node.resolvedThrows = make(effectSet, len(throws))
	for _, thrown := range throws {
		node.resolvedThrows[thrown] = effectOrigin{pos: node.pos, origin: label}
	}
	p.requireOperationEffects(scope.function, operationEffectNamesSet(operations), node.pos, label)
	if len(node.args) != len(params) {
		p.add(node.pos, diagnosticCodeCallArgument, "%s expects %d arguments, found %d", label, len(params), len(node.args))
	}
	node.resolvedArgumentTypes = make([]string, len(node.args))
	argumentInfos := make([]expressionInfo, len(node.args))
	for index, argument := range node.args {
		expected := ""
		if index < len(params) {
			expected = params[index]
		}
		argumentInfo := p.checkASTExpressionExpecting(argument, scope, expected)
		argumentInfos[index] = argumentInfo
		node.resolvedArgumentTypes[index] = argumentInfo.typ
		mergeEffects(info.effects, argumentInfo.effects)
		info.using = mergeUsingValues(info.using, argumentInfo.using)
		if index < len(params) {
			// The position matches a named call's, so both call forms report an
			// argument mismatch the same way.
			p.checkAssignable(node.pos, argumentInfo.typ, expected, label, index+1)
		}
	}
	p.attachUsingToEffects(node.resolvedThrows, info.using)
	p.retainCallArguments(node, scope, argumentInfos)
	info.using = p.usingForType(info.typ, info.using)
	if includeThrows {
		mergeEffects(info.effects, node.resolvedThrows)
	}
	return info
}

// checkCallableValueTarget types a named call target that resolves to a
// callable value rather than to a declaration. A local shadows a function name
// in ordinary lexical scope, and a field holding a callable is invoked the way
// a method is, but a real method always wins over a field of the same name.
func (p *program) checkCallableValueTarget(node *callExpression, scope *astScope, name *nameExpression, includeThrows bool) (expressionInfo, bool) {
	parts := strings.Split(name.name, ".")
	if len(parts) == 1 {
		typ, exists := scope.lookup(name.name)
		if !exists {
			return expressionInfo{}, false
		}
		return p.checkCallableInvocation(node, scope, typ, name.name, includeThrows), true
	}
	if len(parts) != 2 {
		return expressionInfo{}, false
	}
	receiver, exists := scope.lookup(parts[0])
	if !exists {
		return expressionInfo{}, false
	}
	if _, method := p.methodForType(receiver, parts[1]); method {
		return expressionInfo{}, false
	}
	typ, callable := p.callableFieldType(node.pos, scope, receiver, parts[1])
	if !callable {
		return expressionInfo{}, false
	}
	node.resolvedReceiver = receiver
	return p.checkCallableInvocation(node, scope, typ, name.name, includeThrows), true
}

// callableFieldType reports the callable type of receiver.field, so a field
// holding a callable can be invoked the way a method is.
func (p *program) callableFieldType(pos position, scope *astScope, receiverType, fieldName string) (string, bool) {
	class := p.classes[receiverType]
	if class == nil {
		return "", false
	}
	field, exists := class.fields[fieldName]
	if !exists {
		return "", false
	}
	typ := p.resolveType(class.namespace, class.aliases, field.typ)
	if !isCallableType(typ) {
		return "", false
	}
	if !p.requireAccess(pos, scope.function.namespace, class.namespace, field.name, "field") {
		return "", false
	}
	return typ, true
}

// callableDisplayValue is what a callable prints as. A callable has no
// printable contents, and generated Go cannot recover a Slick type from a
// function value, so one fixed marker keeps both backends identical.
const callableDisplayValue = "<callable>"

// runtimeCallable is an invocable value. function is the declaration the body
// lives in — a synthetic one for a lambda — and captures holds the values that
// were copied when a lambda was created. A named function value captures
// nothing, so it costs no environment.
type runtimeCallable struct {
	function *functionDecl
	captures map[string]runtimeValue
}

func (p *program) evalLambda(node *lambdaExpression, frame *runtimeFrame) (runtimeValue, error) {
	value := runtimeValue{typ: node.resolved, callable: &runtimeCallable{function: node.fn}}
	if len(node.captures) == 0 {
		return value, nil
	}
	captures := make(map[string]runtimeValue, len(node.captures))
	for _, name := range node.captures {
		captured, exists := frame.lookup(name)
		if !exists {
			return runtimeValue{}, runtimeError(node.pos, "lambda cannot capture unknown value %s", name)
		}
		captures[name] = captured
	}
	value.callable.captures = captures
	return value, nil
}

// callRuntimeCallable invokes a callable value. Captured values are placed
// before the parameters, so a parameter of the same name shadows its capture
// exactly as the checker decided.
func (p *program) callRuntimeCallable(ctx context.Context, callable *runtimeCallable, args []runtimeValue, pos position) (runtimeValue, error) {
	if callable == nil {
		return runtimeValue{}, runtimeError(pos, "call target is not callable")
	}
	return p.invokeFunction(ctx, callable.function, args, nil, nil, callable.captures)
}

// functionValue reads a named function as a callable value.
func (p *program) functionValue(function *functionDecl) runtimeValue {
	return runtimeValue{typ: p.functionCallableType(function), callable: &runtimeCallable{function: function}}
}

// callableValueOf reads an expression's runtime value as a callable.
func callableValueOf(value runtimeValue) (*runtimeCallable, bool) {
	if value.optional != nil && value.optional.present {
		value = value.optional.value
	}
	return value.callable, value.callable != nil
}
