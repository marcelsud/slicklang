package compiler

import (
	"sort"
	"strings"
)

const (
	typeUnknown = "<unknown>"
	typeNever   = "<never>"
)

type callableTarget struct {
	name      string
	namespace string
	aliases   map[string]aliasDecl
	params    []paramDecl
	result    typeRef
	throwSet  map[string]struct{}
}

type effectOrigin struct {
	pos    position
	origin string
}

type effectSet map[string]effectOrigin

type expressionInfo struct {
	typ     string
	effects effectSet
}

type astScope struct {
	function  *functionDecl
	locals    map[string]string
	loopDepth int
}

func (p *program) checkASTFunction(function *functionDecl) {
	if function.ast == nil {
		return
	}
	scope := &astScope{function: function, locals: make(map[string]string, len(function.params)+1)}
	for _, param := range function.params {
		scope.locals[param.name] = p.resolveType(function.namespace, function.aliases, param.typ)
	}
	if function.receiverCanonical != "" {
		scope.locals["self"] = function.receiverCanonical
	}
	expected := p.resolveType(function.namespace, function.aliases, function.result)
	info := p.checkASTBlock(function.ast, scope, expected)
	if info.typ != typeNever && info.typ != typeUnknown && info.typ != expected {
		p.add(function.pos, "SLK340", "%s returns %s, but its body produces %s", function.qualified, displayName(expected), displayName(info.typ))
	}
	for thrown, origin := range info.effects {
		if !containsError(function.throwSet, thrown) {
			if origin.origin != "" {
				p.add(origin.pos, "SLK201", "unhandled %s from %s; catch it or declare it in %s", displayName(thrown), origin.origin, function.qualified)
			} else {
				p.add(origin.pos, "SLK201", "%s throws %s, but its signature does not declare it", function.qualified, displayName(thrown))
			}
		}
	}
}

// checkASTBlock types every statement in block. expected is the type the block
// as a whole must produce, so it reaches only the final statement.
func (p *program) checkASTBlock(block *blockNode, scope *astScope, expected string) expressionInfo {
	info := expressionInfo{typ: "null", effects: make(effectSet)}
	for index, statement := range block.statements {
		statementExpected := ""
		if index == len(block.statements)-1 {
			statementExpected = expected
		}
		statementInfo := p.checkASTStatement(statement, scope, statementExpected)
		mergeEffects(info.effects, statementInfo.effects)
		info.typ = statementInfo.typ
	}
	return info
}

func (p *program) checkASTStatement(statement statementNode, scope *astScope, expected string) expressionInfo {
	switch node := statement.(type) {
	case *letStatement:
		info := p.checkASTExpression(node.value, scope)
		scope.locals[node.name] = info.typ
		return expressionInfo{typ: "null", effects: info.effects}
	case *assignmentStatement:
		declared, exists := scope.locals[node.name]
		if !exists {
			p.add(node.pos, "SLK341", "cannot assign unknown value %s", node.name)
			return expressionInfo{typ: "null", effects: make(effectSet)}
		}
		info := p.checkASTExpressionExpecting(node.value, scope, declared)
		if info.typ != typeUnknown && info.typ != declared {
			p.add(node.pos, "SLK342", "cannot assign %s to %s of type %s", displayName(info.typ), node.name, displayName(declared))
		}
		return expressionInfo{typ: "null", effects: info.effects}
	case *forStatement:
		iterable := p.checkASTExpression(node.iterable, scope)
		elementType, ok := iterableElementType(iterable.typ)
		if !ok {
			elementType = typeUnknown
			if iterable.typ != typeUnknown {
				p.add(node.pos, "SLK344", "for requires an iterable, found %s", displayName(iterable.typ))
			}
		}
		bindingTypes := make([]string, len(node.bindings))
		for index := range bindingTypes {
			bindingTypes[index] = typeUnknown
		}
		if len(node.bindings) == 1 {
			bindingTypes[0] = elementType
		} else if elementType != typeUnknown {
			tupleTypes, tuple := tupleElementTypes(elementType)
			if !tuple || len(tupleTypes) != len(node.bindings) {
				p.add(node.pos, "SLK346", "loop has %d bindings, but the iterable produces %s", len(node.bindings), displayName(elementType))
			} else {
				bindingTypes = tupleTypes
			}
		}
		loopScope := scope.clone()
		loopScope.loopDepth++
		for index, binding := range node.bindings {
			if binding != "_" {
				loopScope.locals[binding] = bindingTypes[index]
			}
		}
		body := p.checkASTBlock(node.body, loopScope, "")
		mergeEffects(iterable.effects, body.effects)
		return expressionInfo{typ: "null", effects: iterable.effects}
	case *breakStatement:
		if scope.loopDepth == 0 {
			p.add(node.pos, "SLK345", "break is only valid inside a loop")
		}
		return expressionInfo{typ: typeNever, effects: make(effectSet)}
	case *continueStatement:
		if scope.loopDepth == 0 {
			p.add(node.pos, "SLK345", "continue is only valid inside a loop")
		}
		return expressionInfo{typ: typeNever, effects: make(effectSet)}
	case *throwStatement:
		info := p.checkASTExpression(node.value, scope)
		if class := p.classes[info.typ]; class == nil || !class.isError {
			p.add(node.pos, "SLK200", "%s does not produce an Error value", expressionLabel(node.value))
			return expressionInfo{typ: typeNever, effects: info.effects}
		}
		if info.effects == nil {
			info.effects = make(effectSet)
		}
		info.effects[info.typ] = effectOrigin{pos: node.pos}
		info.typ = typeNever
		return info
	case *returnStatement:
		declared := p.resolveType(scope.function.namespace, scope.function.aliases, scope.function.result)
		info := p.checkASTExpressionExpecting(node.value, scope, declared)
		if info.typ != typeNever && info.typ != typeUnknown && info.typ != declared {
			p.add(node.pos, "SLK340", "%s returns %s, expected %s", scope.function.qualified, displayName(info.typ), displayName(declared))
		}
		info.typ = typeNever
		return info
	case *expressionStatement:
		return p.checkASTExpressionExpecting(node.value, scope, expected)
	default:
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
}

func (p *program) checkASTExpression(expression expressionNode, scope *astScope) expressionInfo {
	return p.checkASTExpressionExpecting(expression, scope, "")
}

// checkASTExpressionExpecting types expression. expected is the type the context
// already knows the expression must produce, or "" when the context knows
// nothing. Only constructs that cannot be typed bottom-up read it.
func (p *program) checkASTExpressionExpecting(expression expressionNode, scope *astScope, expected string) expressionInfo {
	if expression == nil {
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	switch node := expression.(type) {
	case *literalExpression:
		return expressionInfo{typ: literalType(node.value), effects: make(effectSet)}
	case *arrayExpression:
		return p.checkArrayExpression(node, scope)
	case *rangeExpression:
		return p.checkRangeExpression(node, scope)
	case *templateExpression:
		return expressionInfo{typ: "string", effects: make(effectSet)}
	case *nameExpression:
		return p.checkNameExpression(node, scope)
	case *objectExpression:
		return p.checkObjectExpression(node, scope)
	case *callExpression:
		return p.checkCallExpression(node, scope)
	case *binaryExpression:
		return p.checkBinaryExpression(node, scope)
	case *ifExpression:
		return p.checkIfExpression(node, scope, expected)
	case *catchExpression:
		return p.checkCatchExpression(node, scope, expected)
	case *resultExpression:
		return p.checkResultExpression(node, scope, expected)
	case *propagateExpression:
		return p.checkPropagateExpression(node, scope)
	case *matchExpression:
		return p.checkMatchExpression(node, scope, expected)
	default:
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
}

func (p *program) checkNameExpression(node *nameExpression, scope *astScope) expressionInfo {
	parts := strings.Split(node.name, ".")
	if len(parts) == 1 {
		if typ := scope.locals[node.name]; typ != "" {
			return expressionInfo{typ: typ, effects: make(effectSet)}
		}
		p.add(node.pos, "SLK341", "unknown value %s", node.name)
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	if len(parts) == 2 {
		class := p.classes[scope.locals[parts[0]]]
		if class != nil {
			field, ok := class.fields[parts[1]]
			if !ok {
				p.add(node.pos, "SLK322", "%s has no field %s", class.name, parts[1])
				return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
			}
			p.requireAccess(node.pos, scope.function.namespace, class.namespace, field.name, "field")
			return expressionInfo{typ: p.resolveType(class.namespace, class.aliases, field.typ), effects: make(effectSet)}
		}
	}
	p.add(node.pos, "SLK341", "unknown value %s", node.name)
	return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
}

func (p *program) checkArrayExpression(node *arrayExpression, scope *astScope) expressionInfo {
	info := expressionInfo{typ: typeUnknown + "[]", effects: make(effectSet)}
	elementType := ""
	for _, element := range node.elements {
		elementInfo := p.checkASTExpressionExpecting(element, scope, elementType)
		mergeEffects(info.effects, elementInfo.effects)
		if elementType == "" {
			elementType = elementInfo.typ
			continue
		}
		if elementInfo.typ != typeUnknown && elementType != typeUnknown && elementInfo.typ != elementType {
			p.add(element.expressionPos(), "SLK342", "array elements must share one type; found %s and %s", displayName(elementType), displayName(elementInfo.typ))
			elementType = typeUnknown
		}
	}
	if elementType != "" {
		info.typ = elementType + "[]"
	}
	return info
}

func (p *program) checkRangeExpression(node *rangeExpression, scope *astScope) expressionInfo {
	start := p.checkASTExpression(node.start, scope)
	end := p.checkASTExpression(node.end, scope)
	mergeEffects(start.effects, end.effects)
	if start.typ != "int" && start.typ != typeUnknown {
		p.add(node.start.expressionPos(), "SLK342", "range start must be int, found %s", displayName(start.typ))
	}
	if end.typ != "int" && end.typ != typeUnknown {
		p.add(node.end.expressionPos(), "SLK342", "range end must be int, found %s", displayName(end.typ))
	}
	return expressionInfo{typ: "Iterable<int>", effects: start.effects}
}

func (p *program) checkObjectExpression(node *objectExpression, scope *astScope) expressionInfo {
	canonical := p.resolveNameIn(scope.function.namespace, scope.function.aliases, node.typeName)
	class := p.classes[canonical]
	info := expressionInfo{typ: canonical, effects: make(effectSet)}
	if class == nil {
		p.add(node.pos, "SLK205", "unknown class %s", node.typeName)
		info.typ = typeUnknown
		return info
	}
	p.requireAccess(node.pos, scope.function.namespace, class.namespace, class.name, "class")
	seen := make(map[string]struct{}, len(node.fields))
	for _, fieldValue := range node.fields {
		if _, duplicate := seen[fieldValue.name]; duplicate {
			p.add(fieldValue.pos, "SLK342", "duplicate field %s.%s", class.name, fieldValue.name)
			continue
		}
		seen[fieldValue.name] = struct{}{}
		field, ok := class.fields[fieldValue.name]
		if !ok {
			p.add(fieldValue.pos, "SLK322", "%s has no field %s", class.name, fieldValue.name)
			continue
		}
		p.requireAccess(fieldValue.pos, scope.function.namespace, class.namespace, field.name, "field")
		expected := p.resolveType(class.namespace, class.aliases, field.typ)
		valueInfo := p.checkASTExpressionExpecting(fieldValue.value, scope, expected)
		mergeEffects(info.effects, valueInfo.effects)
		if valueInfo.typ != typeUnknown && valueInfo.typ != expected {
			p.add(fieldValue.pos, "SLK342", "field %s.%s must be %s, found %s", class.name, field.name, displayName(expected), displayName(valueInfo.typ))
		}
	}
	return info
}

func (p *program) checkCallExpression(node *callExpression, scope *astScope) expressionInfo {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		p.add(node.pos, "SLK341", "call target is not a function or method")
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	if info, builtin := p.checkIterableCall(node, scope, name); builtin {
		return info
	}
	if className, isError := p.resolveErrorIn(scope.function.namespace, scope.function.aliases, name.name); isError && p.classes[className] != nil {
		class := p.classes[className]
		p.requireAccess(node.pos, scope.function.namespace, class.namespace, class.name, "error class")
		info := expressionInfo{typ: className, effects: make(effectSet)}
		for _, argument := range node.args {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
		}
		return info
	}

	target, found := p.resolveASTCall(scope.function, name, scope.locals)
	if !found {
		p.add(node.pos, "SLK203", "unknown function or method %s", name.name)
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	info := expressionInfo{
		typ:     p.resolveType(target.namespace, target.aliases, target.result),
		effects: make(effectSet),
	}
	if len(node.args) != len(target.params) {
		p.add(node.pos, "SLK320", "%s expects %d arguments, found %d", target.name, len(target.params), len(node.args))
	}
	for index, argument := range node.args {
		if index >= len(target.params) {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
			continue
		}
		expected := p.resolveType(target.namespace, target.aliases, target.params[index].typ)
		argumentInfo := p.checkASTExpressionExpecting(argument, scope, expected)
		mergeEffects(info.effects, argumentInfo.effects)
		p.checkAssignable(node.pos, argumentInfo.typ, expected, target.name, index+1)
	}
	for thrown := range target.throwSet {
		info.effects[thrown] = effectOrigin{pos: node.pos, origin: target.name}
	}
	return info
}

func (p *program) checkIterableCall(node *callExpression, scope *astScope, name *nameExpression) (expressionInfo, bool) {
	if !isIterableBuiltin(name.name) {
		return expressionInfo{}, false
	}
	info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	arguments := make([]expressionInfo, 0, len(node.args))
	for _, argument := range node.args {
		argumentInfo := p.checkASTExpression(argument, scope)
		mergeEffects(info.effects, argumentInfo.effects)
		arguments = append(arguments, argumentInfo)
	}
	if name.name == "enumerate" {
		if len(arguments) != 1 {
			p.add(node.pos, "SLK320", "enumerate expects 1 argument, found %d", len(arguments))
			return info, true
		}
		elementType, ok := iterableElementType(arguments[0].typ)
		if !ok {
			p.add(node.pos, "SLK344", "enumerate requires an iterable, found %s", displayName(arguments[0].typ))
			return info, true
		}
		info.typ = iterableType("int", elementType)
		return info, true
	}
	if len(arguments) < 2 {
		p.add(node.pos, "SLK320", "zip expects at least 2 arguments, found %d", len(arguments))
		return info, true
	}
	elementTypes := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		elementType, ok := iterableElementType(argument.typ)
		if !ok {
			p.add(node.pos, "SLK344", "zip requires iterable arguments, found %s", displayName(argument.typ))
			elementType = typeUnknown
		}
		elementTypes = append(elementTypes, elementType)
	}
	info.typ = iterableType(elementTypes...)
	return info, true
}

func (p *program) resolveASTCall(function *functionDecl, name *nameExpression, locals map[string]string) (*callableTarget, bool) {
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 {
		if receiverType := locals[parts[0]]; receiverType != "" {
			method, ok := p.methodForType(receiverType, parts[1])
			if !ok {
				p.add(name.pos, "SLK321", "%s has no method %s", displayName(receiverType), parts[1])
				return nil, false
			}
			if !p.requireAccess(name.pos, function.namespace, method.ownerNamespace, method.name, "method") {
				return nil, false
			}
			return &callableTarget{name: name.name, namespace: method.namespace, aliases: method.aliases, params: method.params, result: method.result, throwSet: method.throwSet}, true
		}
	}
	if strings.Contains(name.name, ".") && !strings.HasPrefix(name.name, "root.") {
		return nil, false
	}
	callee := p.resolveFunction(function, name.name)
	if callee == nil {
		return nil, false
	}
	if !p.requireAccess(name.pos, function.namespace, callee.namespace, callee.name, "function") {
		return nil, false
	}
	return &callableTarget{name: name.name, namespace: callee.namespace, aliases: callee.aliases, params: callee.params, result: callee.result, throwSet: callee.throwSet}, true
}

func (p *program) checkAssignable(pos position, actual, expected, target string, argument int) {
	if actual == typeUnknown || actual == typeNever {
		return
	}
	if iface := p.interfaces[expected]; iface != nil {
		class := p.classes[actual]
		if class == nil {
			if actual != expected {
				p.add(pos, "SLK320", "argument %d to %s must implement %s, found %s", argument, target, displayName(expected), displayName(actual))
			}
			return
		}
		if reasons := p.classSatisfies(class, iface); len(reasons) > 0 {
			p.add(pos, "SLK320", "%s does not implement %s: %s", class.qualified, iface.qualified, strings.Join(reasons, "; "))
		}
		return
	}
	if actual != expected {
		p.add(pos, "SLK320", "argument %d to %s must be %s, found %s", argument, target, displayName(expected), displayName(actual))
	}
}

func (p *program) checkBinaryExpression(node *binaryExpression, scope *astScope) expressionInfo {
	left := p.checkASTExpression(node.left, scope)
	right := p.checkASTExpression(node.right, scope)
	effects := make(effectSet)
	mergeEffects(effects, left.effects)
	mergeEffects(effects, right.effects)
	switch node.op {
	case "==", "!=":
		if left.typ != typeUnknown && right.typ != typeUnknown && left.typ != right.typ {
			p.add(node.pos, "SLK342", "cannot compare %s with %s", displayName(left.typ), displayName(right.typ))
		}
		return expressionInfo{typ: "bool", effects: effects}
	case "+":
		if left.typ != right.typ || (left.typ != "int" && left.typ != "float" && left.typ != "string") {
			p.add(node.pos, "SLK342", "operator + does not accept %s and %s", displayName(left.typ), displayName(right.typ))
			return expressionInfo{typ: typeUnknown, effects: effects}
		}
		return expressionInfo{typ: left.typ, effects: effects}
	default:
		return expressionInfo{typ: typeUnknown, effects: effects}
	}
}

func (p *program) checkIfExpression(node *ifExpression, scope *astScope, expected string) expressionInfo {
	condition := p.checkASTExpression(node.condition, scope)
	if condition.typ != "bool" && condition.typ != typeUnknown {
		p.add(node.condition.expressionPos(), "SLK342", "if condition must be bool, found %s", displayName(condition.typ))
	}
	thenInfo := p.checkASTBlock(node.thenBlock, scope.clone(), expected)
	info := expressionInfo{typ: "null", effects: make(effectSet)}
	mergeEffects(info.effects, condition.effects)
	mergeEffects(info.effects, thenInfo.effects)
	if node.elseBlock == nil {
		return info
	}
	elseInfo := p.checkASTBlock(node.elseBlock, scope.clone(), expected)
	mergeEffects(info.effects, elseInfo.effects)
	switch {
	case thenInfo.typ == typeNever:
		info.typ = elseInfo.typ
	case elseInfo.typ == typeNever:
		info.typ = thenInfo.typ
	case thenInfo.typ == elseInfo.typ:
		info.typ = thenInfo.typ
	default:
		p.add(node.pos, "SLK342", "if branches must produce one type; found %s and %s", displayName(thenInfo.typ), displayName(elseInfo.typ))
		info.typ = typeUnknown
	}
	return info
}

func (p *program) checkCatchExpression(node *catchExpression, scope *astScope, expected string) expressionInfo {
	valueInfo := p.checkASTExpressionExpecting(node.value, scope, expected)
	remaining := make(effectSet)
	for name, origin := range valueInfo.effects {
		remaining[name] = origin
	}
	result := expressionInfo{typ: valueInfo.typ, effects: make(effectSet)}
	catchAll := false
	for _, arm := range node.arms {
		resolved, ok := p.resolveErrorIn(scope.function.namespace, scope.function.aliases, arm.errorType.name)
		if !ok {
			p.add(arm.errorType.pos, "SLK200", "%s does not name an Error type", arm.errorType.name)
			continue
		}
		if class := p.classes[resolved]; class != nil {
			p.requireAccess(arm.errorType.pos, scope.function.namespace, class.namespace, class.name, "error class")
		}
		if resolved == "Error" {
			catchAll = true
			for name := range remaining {
				delete(remaining, name)
			}
		} else {
			delete(remaining, resolved)
		}
		armScope := scope.clone()
		if node.binding != "" {
			armScope.locals[node.binding] = resolved
		}
		armInfo := p.checkASTExpressionExpecting(arm.value, armScope, expected)
		mergeEffects(result.effects, armInfo.effects)
		if armInfo.typ != typeNever && result.typ != armInfo.typ {
			p.add(arm.errorType.pos, "SLK342", "catch success and error paths must produce one type; found %s and %s", displayName(result.typ), displayName(armInfo.typ))
			result.typ = typeUnknown
		}
	}
	if !catchAll && len(remaining) > 0 {
		missing := make([]string, 0, len(remaining))
		for name := range remaining {
			missing = append(missing, displayName(name))
		}
		sort.Strings(missing)
		p.add(node.pos, "SLK202", "non-exhaustive catch for %s; missing %s", expressionLabel(node.value), strings.Join(missing, ", "))
	}
	return result
}

// checkResultExpression types Ok(value) and Err(error) against the Result type
// the context expects. Once resolved the node keeps that type, so re-checking it
// from a context that knows nothing stays deterministic.
func (p *program) checkResultExpression(node *resultExpression, scope *astScope, expected string) expressionInfo {
	if node.resolved != "" {
		expected = node.resolved
	}
	success, failure, isResult := resultTypeArgs(expected)
	if !isResult {
		p.add(node.pos, "SLK351", "%s needs a known Result type here; give the enclosing return type, argument, or field a Result<T, E> type", node.label())
		return expressionInfo{typ: typeUnknown, effects: p.checkASTExpression(node.value, scope).effects}
	}
	payload := success
	if !node.ok {
		payload = failure
	}
	info := p.checkASTExpressionExpecting(node.value, scope, payload)
	if info.typ != typeUnknown && info.typ != typeNever && info.typ != payload {
		p.add(node.pos, "SLK350", "%s payload must be %s, found %s", node.label(), displayName(payload), displayName(info.typ))
	}
	node.resolved = expected
	return expressionInfo{typ: expected, effects: info.effects}
}

// checkPropagateExpression types postfix ?. It never contributes a throw
// effect: an Err travels as an ordinary early return, not as a checked throw.
func (p *program) checkPropagateExpression(node *propagateExpression, scope *astScope) expressionInfo {
	info := p.checkASTExpression(node.value, scope)
	declared := p.resolveType(scope.function.namespace, scope.function.aliases, scope.function.result)
	_, enclosingFailure, returnsResult := resultTypeArgs(declared)
	if !returnsResult {
		p.add(node.pos, "SLK353", "? requires %s to return Result, found %s", scope.function.qualified, displayName(declared))
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	success, failure, isResult := resultTypeArgs(info.typ)
	if !isResult {
		if info.typ != typeUnknown {
			p.add(node.pos, "SLK352", "? requires a Result value, found %s", displayName(info.typ))
		}
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	if failure != enclosingFailure {
		p.add(node.pos, "SLK354", "? cannot propagate %s from %s, which fails with %s", displayName(failure), scope.function.qualified, displayName(enclosingFailure))
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	return expressionInfo{typ: success, effects: info.effects}
}

// checkMatchExpression types an exhaustive Result match: one arm is selected,
// Ok and Err must both be reachable, and every reachable arm shares one type.
func (p *program) checkMatchExpression(node *matchExpression, scope *astScope, expected string) expressionInfo {
	valueInfo := p.checkASTExpression(node.value, scope)
	info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	mergeEffects(info.effects, valueInfo.effects)
	success, failure, isResult := resultTypeArgs(valueInfo.typ)
	if !isResult {
		if valueInfo.typ != typeUnknown {
			p.add(node.pos, "SLK355", "match requires a Result value, found %s", displayName(valueInfo.typ))
		}
		return info
	}
	handled := make(map[matchPattern]position, len(node.arms))
	armType := ""
	for _, arm := range node.arms {
		if previous, duplicate := handled[arm.pattern]; duplicate {
			p.add(arm.pos, "SLK357", "duplicate %s arm; already handled at %s:%d:%d", arm.pattern, previous.file, previous.line, previous.column)
			continue
		}
		if catchAll, exists := handled[matchPatternAny]; exists {
			p.add(arm.pos, "SLK357", "unreachable %s arm; the _ arm at %s:%d:%d already matches", arm.pattern, catchAll.file, catchAll.line, catchAll.column)
			continue
		}
		_, hasOk := handled[matchPatternOk]
		_, hasErr := handled[matchPatternErr]
		if arm.pattern == matchPatternAny && hasOk && hasErr {
			p.add(arm.pos, "SLK357", "unreachable _ arm; Ok and Err are already handled")
			continue
		}
		handled[arm.pattern] = arm.pos
		armScope := scope.clone()
		if arm.binding != "" {
			binding := success
			if arm.pattern == matchPatternErr {
				binding = failure
			}
			armScope.locals[arm.binding] = binding
		}
		armInfo := p.checkASTExpressionExpecting(arm.value, armScope, expected)
		mergeEffects(info.effects, armInfo.effects)
		if armInfo.typ == typeNever || armInfo.typ == typeUnknown {
			continue
		}
		if armType == "" {
			armType = armInfo.typ
			continue
		}
		if armInfo.typ != armType {
			p.add(arm.pos, "SLK358", "match arms must produce one type; found %s and %s", displayName(armType), displayName(armInfo.typ))
			armType = typeUnknown
		}
	}
	if _, catchAll := handled[matchPatternAny]; !catchAll {
		if _, ok := handled[matchPatternOk]; !ok {
			p.add(node.pos, "SLK356", "match does not handle Ok; add an Ok(...) or _ arm")
		}
		if _, ok := handled[matchPatternErr]; !ok {
			p.add(node.pos, "SLK356", "match does not handle Err; add an Err(...) or _ arm")
		}
	}
	if armType != "" {
		info.typ = armType
	}
	return info
}

func (scope *astScope) clone() *astScope {
	locals := make(map[string]string, len(scope.locals))
	for name, typ := range scope.locals {
		locals[name] = typ
	}
	return &astScope{function: scope.function, locals: locals, loopDepth: scope.loopDepth}
}

func mergeEffects(target, source effectSet) {
	for name, origin := range source {
		if _, exists := target[name]; !exists {
			target[name] = origin
		}
	}
}

func literalType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	default:
		return typeUnknown
	}
}

func isIterableBuiltin(name string) bool {
	return name == "enumerate" || name == "zip"
}

func iterableElementType(iterableType string) (string, bool) {
	if strings.HasSuffix(iterableType, "[]") {
		return strings.TrimSuffix(iterableType, "[]"), true
	}
	base, args, generic := genericType(iterableType)
	if generic && base == "Iterable" && len(args) == 1 {
		return args[0], true
	}
	return "", false
}

func iterableType(elementTypes ...string) string {
	if len(elementTypes) == 1 {
		return "Iterable<" + elementTypes[0] + ">"
	}
	return "Iterable<(" + strings.Join(elementTypes, ",") + ")>"
}

func tupleElementTypes(tupleType string) ([]string, bool) {
	if !strings.HasPrefix(tupleType, "(") || !strings.HasSuffix(tupleType, ")") {
		return nil, false
	}
	return splitTypeList(tupleType[1 : len(tupleType)-1]), true
}

func expressionLabel(expression expressionNode) string {
	switch node := expression.(type) {
	case *nameExpression:
		return node.name
	case *callExpression:
		return expressionLabel(node.callee)
	case *objectExpression:
		return node.typeName
	case *resultExpression:
		return node.label()
	case *propagateExpression:
		return expressionLabel(node.value)
	case *matchExpression:
		return "match"
	default:
		return "expression"
	}
}

func (p *program) resolveNameIn(namespace string, aliases map[string]aliasDecl, name string) string {
	if strings.HasPrefix(name, "root.") {
		return name
	}
	if alias, ok := aliases[name]; ok {
		return alias.target
	}
	return qualify(namespace, name)
}
