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
	name       string
	namespace  string
	aliases    map[string]aliasDecl
	typeParams []string
	params     []paramDecl
	result     typeRef
	throwSet   map[string]struct{}
	native     nativeFunction
	function   *functionDecl
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

type usingBinding struct {
	outerLocals map[string]struct{}
}

type pendingState uint8

const (
	pendingAvailable pendingState = iota
	pendingConsumed
	pendingInvalid
)

type pendingBinding struct {
	typ       string
	effects   effectSet
	state     pendingState
	loopDepth int
	owner     *blockNode
	pos       position
}

type pendingPath struct {
	scope  *astScope
	normal bool
}

// astScope keeps the two type facts a local carries apart. locals holds the
// declared or inferred storage type, which assignment validates against and
// which narrowing never rewrites. narrowed holds the branch-local refinement a
// null comparison proved. Reading a name prefers the refinement; writing one
// discards it.
type astScope struct {
	function      *functionDecl
	locals        map[string]string
	narrowed      map[string]string
	usingBindings map[string]usingBinding
	pending       map[string]pendingBinding
	currentBlock  *blockNode
	loopDepth     int
}

func newASTScope(function *functionDecl, size int) *astScope {
	return &astScope{
		function:      function,
		locals:        make(map[string]string, size),
		narrowed:      make(map[string]string),
		usingBindings: make(map[string]usingBinding),
		pending:       make(map[string]pendingBinding),
	}
}

// lookup returns the type a name reads as at this point: its refinement when a
// branch has proven one, otherwise its storage type.
func (scope *astScope) lookup(name string) (string, bool) {
	if typ, refined := scope.narrowed[name]; refined {
		return typ, true
	}
	typ, exists := scope.locals[name]
	return typ, exists
}

// bind records a local's storage type and drops any refinement it shadows.
func (scope *astScope) bind(name, typ string) {
	scope.locals[name] = typ
	delete(scope.narrowed, name)
	delete(scope.usingBindings, name)
}

func (p *program) checkASTFunction(function *functionDecl) {
	if function.ast == nil {
		return
	}
	scope := newASTScope(function, len(function.params)+1)
	for _, param := range function.params {
		scope.locals[param.name] = p.resolveType(function.namespace, function.aliases, param.typ)
		if strings.Contains(scope.locals[param.name], "std.io.") {
			p.usesStdIO = true
		}
		if usesStdFSDirectoryName(scope.locals[param.name]) {
			p.usesStdFSDirectory = true
		}
	}
	if function.receiverCanonical != "" {
		scope.locals["self"] = function.receiverCanonical
	}
	expected := p.resolveType(function.namespace, function.aliases, function.result)
	if strings.Contains(expected, "std.io.") {
		p.usesStdIO = true
	}
	if usesStdFSDirectoryName(expected) {
		p.usesStdFSDirectory = true
	}
	info := p.checkASTBlock(function.ast, scope, expected)
	if !p.assignable(info.typ, expected) {
		p.reportUnassignable(function.pos, info.typ, expected, diagnosticCodeReturnType,
			"%s returns %s, but its body produces %s", function.qualified, displayName(expected), displayName(info.typ))
	}
	for thrown, origin := range info.effects {
		if !containsError(function.throwSet, thrown) {
			if origin.origin != "" {
				p.add(origin.pos, diagnosticCodeUnhandledError, "unhandled %s from %s; catch it or declare it in %s", displayName(thrown), origin.origin, function.qualified)
			} else {
				p.add(origin.pos, diagnosticCodeUnhandledError, "%s throws %s, but its signature does not declare it", function.qualified, displayName(thrown))
			}
		}
	}
}

// checkASTBlock types every statement in block. expected is the type the block
// as a whole must produce, so it reaches only the final statement.
func (p *program) checkASTBlock(block *blockNode, scope *astScope, expected string) expressionInfo {
	previousBlock := scope.currentBlock
	scope.currentBlock = block
	defer func() { scope.currentBlock = previousBlock }()

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
	if info.typ != typeNever {
		for _, statement := range block.statements {
			node, ok := statement.(*asyncLetStatement)
			if !ok {
				continue
			}
			binding, exists := scope.pending[node.name]
			if exists && binding.owner == block && binding.state == pendingAvailable {
				p.add(node.pos, diagnosticCodePendingMissingAwait, "pending binding %s must be awaited on every normal path leaving this block", node.name)
			}
		}
	}
	return info
}

func (p *program) checkASTStatement(statement statementNode, scope *astScope, expected string) expressionInfo {
	switch node := statement.(type) {
	case *letStatement:
		info := p.checkASTExpression(node.value, scope)
		node.resolved = info.typ
		if len(node.names) == 1 {
			scope.bind(node.names[0], info.typ)
			return expressionInfo{typ: "null", effects: info.effects}
		}
		tupleTypes, tuple := tupleElementTypes(info.typ)
		if !tuple || len(tupleTypes) != len(node.names) {
			if info.typ != typeUnknown {
				p.add(node.pos, diagnosticCodeLoopBindings, "let has %d bindings, but the value produces %s", len(node.names), displayName(info.typ))
			}
			tupleTypes = make([]string, len(node.names))
			for index := range tupleTypes {
				tupleTypes[index] = typeUnknown
			}
		}
		seen := make(map[string]struct{}, len(node.names))
		for index, name := range node.names {
			if name == "_" {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				p.add(node.pos, diagnosticCodeTypeMismatch, "duplicate destructuring binding %s", name)
				continue
			}
			seen[name] = struct{}{}
			scope.bind(name, tupleTypes[index])
		}
		return expressionInfo{typ: "null", effects: info.effects}
	case *asyncLetStatement:
		p.usesAsync = true
		if scope.currentBlock != nil {
			scope.currentBlock.hasAsync = true
		}
		info := p.checkCallExpressionEffects(node.call, scope, false)
		if pending, exists := scope.pending[node.name]; exists && pending.state == pendingAvailable {
			p.add(node.pos, diagnosticCodePendingAssignment, "pending binding %s is immutable and cannot be redeclared before it is awaited", node.name)
			return expressionInfo{typ: "null", effects: info.effects}
		}
		if node.call.resolvedResult == "" {
			if info.typ != typeUnknown {
				p.add(node.pos, diagnosticCodeAsyncInitializer, "async let initializer must resolve to one function or method call")
			}
			return expressionInfo{typ: "null", effects: info.effects}
		}
		unsafe := false
		if node.call.resolvedReceiver != "" && !p.taskSafeType(node.call.resolvedReceiver, make(map[string]bool)) {
			p.add(node.pos, diagnosticCodeAsyncUnsafeValue, "async call receiver has task-unsafe type %s", displayName(node.call.resolvedReceiver))
			unsafe = true
		}
		for index, typ := range node.call.resolvedArgumentTypes {
			if typ != "" && typ != typeUnknown && !p.taskSafeType(typ, make(map[string]bool)) {
				p.add(node.call.args[index].expressionPos(), diagnosticCodeAsyncUnsafeValue, "async argument %d has task-unsafe type %s", index+1, displayName(typ))
				unsafe = true
			}
		}
		if node.call.resolvedResult != "" && !p.taskSafeType(node.call.resolvedResult, make(map[string]bool)) {
			p.add(node.pos, diagnosticCodeAsyncUnsafeValue, "async call result has task-unsafe type %s", displayName(node.call.resolvedResult))
			unsafe = true
		}
		state := pendingAvailable
		if unsafe {
			state = pendingInvalid
		}
		scope.pending[node.name] = pendingBinding{
			typ:       node.call.resolvedResult,
			effects:   cloneEffects(node.call.resolvedThrows),
			state:     state,
			loopDepth: scope.loopDepth,
			owner:     scope.currentBlock,
			pos:       node.pos,
		}
		return expressionInfo{typ: "null", effects: info.effects}
	case *assignmentStatement:
		if _, active := scope.usingBindings[node.name]; active {
			p.add(node.pos, diagnosticCodeUsingAssignment, "using binding %s is immutable", node.name)
		}
		if pending, exists := scope.pending[node.name]; exists && pending.state != pendingInvalid {
			p.add(node.pos, diagnosticCodePendingAssignment, "pending binding %s is immutable and can only be consumed by await", node.name)
			info := p.checkASTExpression(node.value, scope)
			return expressionInfo{typ: "null", effects: info.effects}
		}
		declared, exists := scope.locals[node.name]
		if !exists {
			p.add(node.pos, diagnosticCodeUnknownValue, "cannot assign unknown value %s", node.name)
			return expressionInfo{typ: "null", effects: make(effectSet)}
		}
		node.resolved = declared
		info := p.checkASTExpressionExpecting(node.value, scope, declared)
		if !p.assignable(info.typ, declared) {
			p.reportUnassignable(node.pos, info.typ, declared, diagnosticCodeTypeMismatch,
				"cannot assign %s to %s of type %s", displayName(info.typ), node.name, displayName(declared))
		}
		// The stored value is no longer the one a branch proved non-null.
		delete(scope.narrowed, node.name)
		if resource, binding, ok := directUsingBinding(node.value, scope); ok {
			if _, escapes := binding.outerLocals[node.name]; escapes && node.name != resource {
				p.add(node.pos, diagnosticCodeUsingEscape, "using binding %s cannot be assigned outside its scope", resource)
			}
		}
		return expressionInfo{typ: "null", effects: info.effects}
	case *forStatement:
		iterable := p.checkASTExpression(node.iterable, scope)
		elementType, ok := iterableElementType(iterable.typ)
		if !ok {
			elementType = typeUnknown
			if iterable.typ != typeUnknown {
				p.add(node.pos, diagnosticCodeNotIterable, "for requires an iterable, found %s", displayName(iterable.typ))
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
				p.add(node.pos, diagnosticCodeLoopBindings, "loop has %d bindings, but the iterable produces %s", len(node.bindings), displayName(elementType))
			} else {
				bindingTypes = tupleTypes
			}
		}
		loopScope := scope.clone()
		loopScope.loopDepth++
		for index, binding := range node.bindings {
			if binding != "_" {
				loopScope.bind(binding, bindingTypes[index])
			}
		}
		body := p.checkASTBlock(node.body, loopScope, "")
		clearAssignedNarrowings(scope, node.body)
		mergeEffects(iterable.effects, body.effects)
		return expressionInfo{typ: "null", effects: iterable.effects}
	case *breakStatement:
		if scope.loopDepth == 0 {
			p.add(node.pos, diagnosticCodeLoopControl, "break is only valid inside a loop")
		}
		return expressionInfo{typ: typeNever, effects: make(effectSet)}
	case *continueStatement:
		if scope.loopDepth == 0 {
			p.add(node.pos, diagnosticCodeLoopControl, "continue is only valid inside a loop")
		}
		return expressionInfo{typ: typeNever, effects: make(effectSet)}
	case *throwStatement:
		info := p.checkASTExpression(node.value, scope)
		if class := p.classes[info.typ]; class == nil || !class.isError {
			p.add(node.pos, diagnosticCodeErrorValue, "%s does not produce an Error value", expressionLabel(node.value))
			return expressionInfo{typ: typeNever, effects: info.effects}
		}
		if info.effects == nil {
			info.effects = make(effectSet)
		}
		info.effects[info.typ] = effectOrigin{pos: node.pos}
		info.typ = typeNever
		return info
	case *returnStatement:
		if resource, _, ok := directUsingBinding(node.value, scope); ok {
			p.add(node.pos, diagnosticCodeUsingEscape, "using binding %s cannot be returned outside its scope", resource)
		}
		declared := p.resolveType(scope.function.namespace, scope.function.aliases, scope.function.result)
		info := p.checkASTExpressionExpecting(node.value, scope, declared)
		if !p.assignable(info.typ, declared) {
			p.reportUnassignable(node.pos, info.typ, declared, diagnosticCodeReturnType,
				"%s returns %s, expected %s", scope.function.qualified, displayName(info.typ), displayName(declared))
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
	case *invalidExpression:
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	case *literalExpression:
		return expressionInfo{typ: literalType(node.value), effects: make(effectSet)}
	case *tupleExpression:
		return p.checkTupleExpression(node, scope, expected)
	case *arrayExpression:
		return p.checkArrayExpression(node, scope, expected)

	case *mapExpression:
		return p.checkMapExpression(node, scope, expected)
	case *rangeExpression:
		return p.checkRangeExpression(node, scope)
	case *objectExpression:
		return p.checkObjectExpression(node, scope)
	case *callExpression:
		return p.checkCallExpression(node, scope)
	case *templateExpression:
		return p.checkTemplateExpression(node, scope)
	case *nameExpression:
		return p.checkNameExpression(node, scope)
	case *awaitExpression:
		return p.checkAwaitExpression(node, scope)
	case *unaryExpression:
		return p.checkUnaryExpression(node, scope)
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
	case *usingExpression:
		return p.checkUsingExpression(node, scope, expected)
	case *matchExpression:
		return p.checkMatchExpression(node, scope, expected)
	default:
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
}
func (p *program) checkTupleExpression(node *tupleExpression, scope *astScope, expected string) expressionInfo {
	info := expressionInfo{effects: make(effectSet)}
	expectedTypes, hasExpectedTuple := tupleElementTypes(expected)
	if hasExpectedTuple && len(expectedTypes) != len(node.elements) {
		for _, element := range node.elements {
			elementInfo := p.checkASTExpression(element, scope)
			mergeEffects(info.effects, elementInfo.effects)
		}
		p.add(node.pos, diagnosticCodeLoopBindings, "tuple expects %d elements, found %d", len(expectedTypes), len(node.elements))
		info.typ = typeUnknown
		return info
	}
	types := make([]string, len(node.elements))
	mismatch := false
	for index, element := range node.elements {
		elementExpected := ""
		if hasExpectedTuple {
			elementExpected = expectedTypes[index]
		}
		elementInfo := p.checkASTExpressionExpecting(element, scope, elementExpected)
		mergeEffects(info.effects, elementInfo.effects)
		types[index] = elementInfo.typ
		if hasExpectedTuple && !p.assignable(elementInfo.typ, elementExpected) {
			mismatch = true
			p.reportUnassignable(element.expressionPos(), elementInfo.typ, elementExpected,
				diagnosticCodeTypeMismatch, "tuple element %d must be %s, found %s",
				index+1, displayName(elementExpected), displayName(elementInfo.typ))
		}
	}
	info.typ = "(" + strings.Join(types, ",") + ")"
	if hasExpectedTuple {
		node.resolved = expected
		info.typ = expected
		if mismatch {
			return info
		}
	}
	return info
}

func (p *program) checkTemplateExpression(node *templateExpression, scope *astScope) expressionInfo {
	info := expressionInfo{typ: "string", effects: make(effectSet)}
	text := node.text
	for {
		start := strings.Index(text, "${")
		if start < 0 {
			return info
		}
		text = text[start+2:]
		end := strings.IndexByte(text, '}')
		if end < 0 {
			return info
		}
		name := strings.TrimSpace(text[:end])
		mergeEffects(info.effects, p.checkNameExpression(&nameExpression{name: name, pos: node.pos}, scope).effects)
		text = text[end+1:]
	}
}

func (p *program) checkNameExpression(node *nameExpression, scope *astScope) expressionInfo {
	parts := strings.Split(node.name, ".")
	if len(parts) == 1 {
		if pending, exists := scope.pending[node.name]; exists {
			p.add(node.pos, diagnosticCodePendingUse, "pending binding %s is not a value; use await %s", node.name, node.name)
			return expressionInfo{typ: pending.typ, effects: make(effectSet)}
		}
		if typ, exists := scope.lookup(node.name); exists {
			return expressionInfo{typ: typ, effects: make(effectSet)}
		}
		p.add(node.pos, diagnosticCodeUnknownValue, "unknown value %s", node.name)
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	if pending, exists := scope.pending[parts[0]]; exists {
		p.add(node.pos, diagnosticCodePendingUse, "pending binding %s is not a value or receiver; use await %s", parts[0], parts[0])
		return expressionInfo{typ: pending.typ, effects: make(effectSet)}
	}
	if len(parts) == 2 {
		receiver, exists := scope.lookup(parts[0])
		if exists && isOptionalType(receiver) {
			p.add(node.pos, diagnosticCodeOptionalReceiver, "%s is %s and may be null; compare it with null and read %s inside the branch that proved it is not",
				parts[0], displayName(receiver), parts[1])
			return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
		}
		class := p.classes[receiver]
		if class != nil {
			field, ok := class.fields[parts[1]]
			if !ok {
				p.add(node.pos, diagnosticCodeUnknownField, "%s has no field %s", class.name, parts[1])
				return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
			}
			p.requireAccess(node.pos, scope.function.namespace, class.namespace, field.name, "field")
			return expressionInfo{typ: p.resolveType(class.namespace, class.aliases, field.typ), effects: make(effectSet)}
		}
		if union := p.unions[receiver]; union != nil {
			p.add(node.pos, diagnosticCodeUnionVariant, "%s is union %s; match it to read the payload of one variant", parts[0], displayName(receiver))
			return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
		}
	}
	if _, shadowed := scope.lookup(parts[0]); !shadowed {
		if union, variant, named := p.resolveVariant(scope.function.namespace, scope.function.aliases, node.name); named {
			return p.checkVariantValue(node, union, variant, scope)
		}
	}
	p.add(node.pos, diagnosticCodeUnknownValue, "unknown value %s", node.name)
	return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
}

func (p *program) checkAwaitExpression(node *awaitExpression, scope *astScope) expressionInfo {
	binding, exists := scope.pending[node.name]
	if !exists && node.resolved != "" {
		return expressionInfo{typ: node.resolved, effects: make(effectSet)}
	}
	if !exists {
		if _, ordinary := scope.lookup(node.name); ordinary {
			p.add(node.pos, diagnosticCodeAwaitOrdinary, "await requires a pending binding, but %s is an ordinary value", node.name)
		} else {
			p.add(node.pos, diagnosticCodeAwaitUnknown, "await refers to unknown pending binding %s", node.name)
		}
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	node.resolved = binding.typ
	if scope.loopDepth > binding.loopDepth {
		p.add(node.pos, diagnosticCodeAwaitLoop, "pending binding %s cannot be awaited from a loop that may consume it more than once", node.name)
		binding.state = pendingInvalid
		scope.pending[node.name] = binding
		return expressionInfo{typ: binding.typ, effects: make(effectSet)}
	}
	switch binding.state {
	case pendingConsumed:
		p.add(node.pos, diagnosticCodeAwaitTwice, "pending binding %s has already been awaited", node.name)
		return expressionInfo{typ: binding.typ, effects: make(effectSet)}
	case pendingInvalid:
		return expressionInfo{typ: binding.typ, effects: make(effectSet)}
	}
	binding.state = pendingConsumed
	scope.pending[node.name] = binding
	effects := make(effectSet, len(binding.effects))
	for name, origin := range binding.effects {
		origin.pos = node.pos
		effects[name] = origin
	}
	return expressionInfo{typ: binding.typ, effects: effects}
}

func (p *program) checkArrayExpression(node *arrayExpression, scope *astScope, expected string) expressionInfo {
	info := expressionInfo{typ: typeUnknown + "[]", effects: make(effectSet)}
	if len(node.elements) == 0 {
		if _, isArray := arrayElementType(expected); isArray {
			info.typ = expected
			node.resolved = expected
		}
		return info
	}
	elementType := ""
	for _, element := range node.elements {
		elementInfo := p.checkASTExpressionExpecting(element, scope, elementType)
		mergeEffects(info.effects, elementInfo.effects)
		if elementType == "" {
			elementType = elementInfo.typ
			continue
		}
		joined, ok := joinTypes(elementType, elementInfo.typ)
		if !ok {
			p.add(element.expressionPos(), diagnosticCodeTypeMismatch, "array elements must share one type; found %s and %s", displayName(elementType), displayName(elementInfo.typ))
			joined = typeUnknown
		}
		elementType = joined
	}
	if elementType != "" {
		info.typ = elementType + "[]"
	}
	return info
}
func (p *program) checkMapExpression(node *mapExpression, scope *astScope, expected string) expressionInfo {
	info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	expectedKey, expectedValue, hasExpectedMap := mapTypeArgs(expected)
	if len(node.entries) == 0 {
		if node.resolved != "" {
			info.typ = node.resolved
			return info
		}
		if !hasExpectedMap {
			p.add(node.pos, diagnosticCodeMapTypeUnknown, "empty map literal needs a known Map<K, V> type here")
			return info
		}
		node.resolved = expected
		info.typ = expected
		return info
	}

	keyType, valueType := "", ""
	staticKeys := make(map[any]struct{})
	for _, entry := range node.entries {
		keyExpected, valueExpected := "", ""
		if hasExpectedMap {
			keyExpected, valueExpected = expectedKey, expectedValue
		}
		keyInfo := p.checkASTExpressionExpecting(entry.key, scope, keyExpected)
		valueInfo := p.checkASTExpressionExpecting(entry.value, scope, valueExpected)
		mergeEffects(info.effects, keyInfo.effects)
		mergeEffects(info.effects, valueInfo.effects)

		if keyInfo.typ != typeUnknown && !isMapKeyType(keyInfo.typ) {
			p.add(entry.key.expressionPos(), diagnosticCodeMapKeyType, "Map key type must be string, int, or bool; found %s", displayName(keyInfo.typ))
		}
		if keyType == "" {
			keyType = keyInfo.typ
		} else if joined, ok := joinTypes(keyType, keyInfo.typ); ok {
			keyType = joined
		} else {
			p.add(entry.key.expressionPos(), diagnosticCodeTypeMismatch, "map keys must share one type; found %s and %s", displayName(keyType), displayName(keyInfo.typ))
			keyType = typeUnknown
		}
		if valueType == "" {
			valueType = valueInfo.typ
		} else if joined, ok := joinTypes(valueType, valueInfo.typ); ok {
			valueType = joined
		} else {
			p.add(entry.value.expressionPos(), diagnosticCodeTypeMismatch, "map values must share one type; found %s and %s", displayName(valueType), displayName(valueInfo.typ))
			valueType = typeUnknown
		}

		if literal, ok := entry.key.(*literalExpression); ok {
			switch literal.value.(type) {
			case string, int64, bool:
				if _, duplicate := staticKeys[literal.value]; duplicate {
					p.add(entry.pos, diagnosticCodeDuplicateMapKey, "duplicate static map key %v", literal.value)
				}
				staticKeys[literal.value] = struct{}{}
			}
		}
	}
	info.typ = mapType(keyType, valueType)
	node.resolved = info.typ
	return info
}

func (p *program) checkRangeExpression(node *rangeExpression, scope *astScope) expressionInfo {
	start := p.checkASTExpression(node.start, scope)
	end := p.checkASTExpression(node.end, scope)
	mergeEffects(start.effects, end.effects)
	if start.typ != "int" && start.typ != typeUnknown {
		p.add(node.start.expressionPos(), diagnosticCodeTypeMismatch, "range start must be int, found %s", displayName(start.typ))
	}
	if end.typ != "int" && end.typ != typeUnknown {
		p.add(node.end.expressionPos(), diagnosticCodeTypeMismatch, "range end must be int, found %s", displayName(end.typ))
	}
	return expressionInfo{typ: "Iterable<int>", effects: start.effects}
}

func (p *program) checkObjectExpression(node *objectExpression, scope *astScope) expressionInfo {
	canonical := p.resolveNameIn(scope.function.namespace, scope.function.aliases, node.typeName)
	if strings.HasPrefix(canonical, "std.io.") {
		p.usesStdIO = true
	}
	if strings.HasPrefix(canonical, "std.http.") {
		p.usesStdHTTP = true
	}
	if usesStdFSDirectoryName(canonical) {
		p.usesStdFSDirectory = true
	}
	if strings.HasPrefix(canonical, "std.process.") {
		p.usesStdProcess = true
	}
	class := p.classes[canonical]
	info := expressionInfo{typ: canonical, effects: make(effectSet)}
	if class == nil {
		p.add(node.pos, diagnosticCodeUnknownClass, "unknown class %s", node.typeName)
		info.typ = typeUnknown
		return info
	}
	p.requireAccess(node.pos, scope.function.namespace, class.namespace, class.name, "class")
	seen := make(map[string]struct{}, len(node.fields))
	for _, fieldValue := range node.fields {
		if _, duplicate := seen[fieldValue.name]; duplicate {
			p.add(fieldValue.pos, diagnosticCodeTypeMismatch, "duplicate field %s.%s", class.name, fieldValue.name)
			continue
		}
		seen[fieldValue.name] = struct{}{}
		field, ok := class.fields[fieldValue.name]
		if !ok {
			p.add(fieldValue.pos, diagnosticCodeUnknownField, "%s has no field %s", class.name, fieldValue.name)
			continue
		}
		p.requireAccess(fieldValue.pos, scope.function.namespace, class.namespace, field.name, "field")
		expected := p.resolveType(class.namespace, class.aliases, field.typ)
		valueInfo := p.checkASTExpressionExpecting(fieldValue.value, scope, expected)
		mergeEffects(info.effects, valueInfo.effects)
		if !p.assignable(valueInfo.typ, expected) {
			p.reportUnassignable(fieldValue.pos, valueInfo.typ, expected, diagnosticCodeTypeMismatch,
				"field %s.%s must be %s, found %s", class.name, field.name, displayName(expected), displayName(valueInfo.typ))
		}
	}
	// An optional field may be left out and defaults to null; every other field
	// has no value to fall back on and must be given one.
	for _, fieldName := range sortedKeys(class.fields) {
		if _, provided := seen[fieldName]; provided {
			continue
		}
		expected := p.resolveType(class.namespace, class.aliases, class.fields[fieldName].typ)
		if isOptionalType(expected) {
			continue
		}
		p.add(node.pos, diagnosticCodeRequiredField, "%s requires field %s of type %s; only optional fields may be omitted", class.name, fieldName, displayName(expected))
	}
	return info
}

func (p *program) checkUsingExpression(node *usingExpression, scope *astScope, expected string) expressionInfo {
	info := p.checkASTExpression(node.initializer, scope)
	node.resolved = info.typ

	bodyScope := scope.clone()
	outerLocals := make(map[string]struct{}, len(scope.locals))
	for name := range scope.locals {
		outerLocals[name] = struct{}{}
	}
	bodyScope.bind(node.name, info.typ)
	bodyScope.usingBindings[node.name] = usingBinding{outerLocals: outerLocals}
	body := p.checkASTBlock(node.body, bodyScope, expected)
	mergeEffects(info.effects, body.effects)
	info.typ = body.typ
	if body.typ != typeNever {
		copyPendingStates(scope, bodyScope)
	}

	if resource, _, ok := directUsingBlockValue(node.body, bodyScope); ok {
		p.add(node.pos, diagnosticCodeUsingEscape, "using binding %s cannot escape its scope", resource)
	}
	node.result = body.typ
	if node.resolved == typeUnknown {
		return info
	}
	closeMethod, ok := p.methodForType(node.resolved, "Close")
	if !ok {
		p.add(node.pos, diagnosticCodeCloseMethod, "%s has no accessible Close method", displayName(node.resolved))
		return info
	}
	if !p.requireAccess(node.pos, scope.function.namespace, closeMethod.ownerNamespace, closeMethod.name, "method") {
		return info
	}
	valid := true
	if len(closeMethod.params) != 0 {
		p.add(node.pos, diagnosticCodeCloseParameters, "%s.Close must take no arguments", displayName(node.resolved))
		valid = false
	}
	closeResult := p.resolveType(closeMethod.namespace, closeMethod.aliases, closeMethod.result)
	if closeResult != "null" {
		p.add(node.pos, diagnosticCodeCloseResult, "%s.Close must return null, found %s", displayName(node.resolved), displayName(closeResult))
		valid = false
	}
	if valid {
		for thrown := range closeMethod.throwSet {
			info.effects[thrown] = effectOrigin{pos: node.pos, origin: node.name + ".Close"}
		}
	}
	return info
}

func directUsingBinding(expression expressionNode, scope *astScope) (string, usingBinding, bool) {
	name, ok := expression.(*nameExpression)
	if !ok {
		return "", usingBinding{}, false
	}
	binding, ok := scope.usingBindings[name.name]
	return name.name, binding, ok
}

func directUsingBlockValue(block *blockNode, scope *astScope) (string, usingBinding, bool) {
	if block == nil || len(block.statements) == 0 {
		return "", usingBinding{}, false
	}
	statement, ok := block.statements[len(block.statements)-1].(*expressionStatement)
	if !ok {
		return "", usingBinding{}, false
	}
	return directUsingBinding(statement.value, scope)
}

func (p *program) checkCallExpression(node *callExpression, scope *astScope) expressionInfo {
	return p.checkCallExpressionEffects(node, scope, true)
}

func (p *program) checkCallExpressionEffects(node *callExpression, scope *astScope, includeThrows bool) expressionInfo {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		p.add(node.pos, diagnosticCodeUnknownValue, "call target is not a function or method")
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	parts := strings.Split(name.name, ".")
	if pending, exists := scope.pending[parts[0]]; exists {
		p.add(node.pos, diagnosticCodePendingUse, "pending binding %s is not a call receiver; use await %s", parts[0], parts[0])
		info := expressionInfo{typ: pending.typ, effects: make(effectSet)}
		for _, argument := range node.args {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
		}
		return info
	}
	if len(parts) == 2 && parts[1] == "Close" {
		if _, active := scope.usingBindings[parts[0]]; active {
			p.add(node.pos, diagnosticCodeManualClose, "cannot call Close directly on active using binding %s", parts[0])
		} else if receiverType, exists := scope.lookup(parts[0]); exists &&
			(p.assignable(receiverType, stdIOReaderName) || p.assignable(receiverType, stdIOWriterName)) {
			p.add(node.pos, diagnosticCodeResourceRequiresUsing, "std.io resources must be closed by a using scope")
		}
	}
	if info, builtin := p.checkIterableCall(node, scope, name); builtin {
		if len(node.typeArgs) > 0 {
			p.add(node.pos, diagnosticCodeTypeArguments, "%s does not take type arguments", name.name)
		}
		return info
	}
	if _, shadowed := scope.lookup(parts[0]); !shadowed {
		if union, variant, named := p.resolveVariant(scope.function.namespace, scope.function.aliases, name.name); named {
			return p.checkVariantConstruction(node, name, union, variant, scope)
		}
	}
	if className, isError := p.resolveErrorIn(scope.function.namespace, scope.function.aliases, name.name); isError && p.classes[className] != nil {
		class := p.classes[className]
		p.requireAccess(node.pos, scope.function.namespace, class.namespace, class.name, "error class")
		info := expressionInfo{typ: className, effects: make(effectSet)}
		if len(node.typeArgs) > 0 {
			p.add(node.pos, diagnosticCodeTypeArguments, "%s does not take type arguments", name.name)
		}
		for _, argument := range node.args {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
		}
		return info
	}

	target, reported := p.resolveASTCall(scope.function, name, scope)
	if len(parts) == 2 {
		if receiver, exists := scope.lookup(parts[0]); exists {
			node.resolvedReceiver = receiver
		}
	}
	if target == nil {
		if !reported {
			p.add(node.pos, diagnosticCodeUnknownCallable, "unknown function or method %s", name.name)
		}
		// Avoid cascade diagnostics for type arguments on unknown callables.
		for _, argument := range node.args {
			mergeEffects(make(effectSet), p.checkASTExpression(argument, scope).effects)
		}
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	if target.namespace == "std.io" {
		p.usesStdIO = true
	}
	if target.namespace == "std.http" {
		p.usesStdHTTP = true
	}
	if target.native == nativeStdFSReadDirectory || target.native == nativeStdFSCreateTemporaryDirectory {
		p.usesStdFSDirectory = true
	}
	if target.namespace == "std.process" {
		p.usesStdProcess = true
	}

	info := expressionInfo{
		typ:     p.resolveType(target.namespace, target.aliases, target.result),
		effects: make(effectSet),
	}
	params := target.params
	result := target.result
	typeArgs := make([]string, 0, len(node.typeArgs))

	if len(target.typeParams) == 0 {
		if len(node.typeArgs) > 0 {
			p.add(node.pos, diagnosticCodeTypeArguments, "%s does not take type arguments", target.name)
		}
	} else {
		if len(node.typeArgs) != len(target.typeParams) {
			p.add(node.pos, diagnosticCodeTypeArguments, "%s expects %d type arguments, found %d", target.name, len(target.typeParams), len(node.typeArgs))
			for _, argument := range node.args {
				mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
			}
			return info
		}
		substitutions := make(map[string]string, len(target.typeParams))
		for index, typeArg := range node.typeArgs {
			canonical := p.canonicalType(scope.function.namespace, scope.function.aliases, typeArg)
			p.checkTypeName(typeArg.pos, scope.function.namespace, canonical)
			if reason := p.jsonUnsupportedReason(canonical, map[string]bool{}); reason != "" {
				// Only Decode/Encode currently own JSON type contracts.
				if target.native == nativeStdJsonDecode || target.native == nativeStdJsonEncode {
					p.add(typeArg.pos, diagnosticCodeJSONType, "%s", reason)
				}
			}
			typeArgs = append(typeArgs, canonical)
			substitutions[target.typeParams[index]] = canonical
		}
		params = make([]paramDecl, len(target.params))
		for index, param := range target.params {
			params[index] = paramDecl{
				name: param.name,
				typ:  typeRef{name: substituteTypeParams(p.resolveType(target.namespace, target.aliases, param.typ), substitutions), pos: param.typ.pos},
			}
		}
		result = typeRef{name: substituteTypeParams(p.resolveType(target.namespace, target.aliases, target.result), substitutions), pos: target.result.pos}
		info.typ = result.name
	}

	node.resolvedTypeArgs = typeArgs
	node.resolvedParams = make([]string, len(params))
	for index, param := range params {
		if len(target.typeParams) == 0 {
			node.resolvedParams[index] = p.resolveType(target.namespace, target.aliases, param.typ)
		} else {
			node.resolvedParams[index] = param.typ.name
		}
	}
	node.resolvedResult = info.typ
	node.resolvedNative = target.native
	node.resolvedThrows = make(effectSet, len(target.throwSet))
	for thrown := range target.throwSet {
		node.resolvedThrows[thrown] = effectOrigin{pos: node.pos, origin: target.name}
	}

	if len(node.args) != len(params) {
		p.add(node.pos, diagnosticCodeCallArgument, "%s expects %d arguments, found %d", target.name, len(params), len(node.args))
	}
	node.resolvedArgumentTypes = make([]string, len(node.args))
	for index, argument := range node.args {
		if index >= len(params) {
			argumentInfo := p.checkASTExpression(argument, scope)
			node.resolvedArgumentTypes[index] = argumentInfo.typ
			mergeEffects(info.effects, argumentInfo.effects)
			continue
		}
		expected := node.resolvedParams[index]
		argumentInfo := p.checkASTExpressionExpecting(argument, scope, expected)
		node.resolvedArgumentTypes[index] = argumentInfo.typ
		mergeEffects(info.effects, argumentInfo.effects)
		p.checkAssignable(node.pos, argumentInfo.typ, expected, target.name, index+1)
	}
	if includeThrows {
		mergeEffects(info.effects, node.resolvedThrows)
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
			p.add(node.pos, diagnosticCodeCallArgument, "enumerate expects 1 argument, found %d", len(arguments))
			return info, true
		}
		elementType, ok := iterableElementType(arguments[0].typ)
		if !ok {
			p.add(node.pos, diagnosticCodeNotIterable, "enumerate requires an iterable, found %s", displayName(arguments[0].typ))
			return info, true
		}
		info.typ = iterableType("int", elementType)
		return info, true
	}
	if len(arguments) < 2 {
		p.add(node.pos, diagnosticCodeCallArgument, "zip expects at least 2 arguments, found %d", len(arguments))
		return info, true
	}
	elementTypes := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		elementType, ok := iterableElementType(argument.typ)
		if !ok {
			p.add(node.pos, diagnosticCodeNotIterable, "zip requires iterable arguments, found %s", displayName(argument.typ))
			elementType = typeUnknown
		}
		elementTypes = append(elementTypes, elementType)
	}
	info.typ = iterableType(elementTypes...)
	return info, true
}

// resolveASTCall finds the function or method a call names. reported is true
// when it already explained the failure, so the caller adds no second cascade
// diagnostic for the same call.
func (p *program) resolveASTCall(function *functionDecl, name *nameExpression, scope *astScope) (*callableTarget, bool) {
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 {
		if receiverType, exists := scope.lookup(parts[0]); exists {
			if isOptionalType(receiverType) {
				p.add(name.pos, diagnosticCodeOptionalReceiver, "%s is %s and may be null; compare it with null and call %s inside the branch that proved it is not",
					parts[0], displayName(receiverType), parts[1])
				return nil, true
			}
			method, ok := p.methodForType(receiverType, parts[1])
			if !ok {
				p.add(name.pos, diagnosticCodeUnknownMethod, "%s has no method %s", displayName(receiverType), parts[1])
				return nil, true
			}
			if !p.requireAccess(name.pos, function.namespace, method.ownerNamespace, method.name, "method") {
				return nil, true
			}
			return &callableTarget{name: name.name, namespace: method.namespace, aliases: method.aliases, params: method.params, result: method.result, throwSet: method.throwSet}, false
		}
	}
	if strings.Contains(name.name, ".") && !isAbsoluteCanonicalName(name.name) {
		return nil, false
	}
	callee := p.resolveFunction(function, name.name)
	if callee == nil {
		return nil, false
	}
	if !p.requireAccess(name.pos, function.namespace, callee.namespace, callee.name, "function") {
		return nil, true
	}
	return &callableTarget{
		name:       name.name,
		namespace:  callee.namespace,
		aliases:    callee.aliases,
		typeParams: callee.typeParams,
		params:     callee.params,
		result:     callee.result,
		throwSet:   callee.throwSet,
		native:     callee.native,
		function:   callee,
	}, false
}

// assignable is the single decision point for storing a value of type actual
// where expected is required: T fits T?, null fits any optional, and a class
// fits an interface it satisfies. Optionals are invariant, so A? never fits B?.
func (p *program) assignable(actual, expected string) bool {
	if actual == typeUnknown || actual == typeNever || expected == typeUnknown || expected == "" {
		return true
	}
	if actual == expected {
		return true
	}
	if base, optional := optionalBase(expected); optional {
		if actual == "null" {
			return true
		}
		return p.assignable(actual, base)
	}
	if iface := p.interfaces[expected]; iface != nil {
		if class := p.classes[actual]; class != nil {
			return len(p.classSatisfies(class, iface)) == 0
		}
	}
	return false
}

// reportUnassignable emits exactly one diagnostic for a value that cannot be
// stored where expected is required. An optionality fault gets its own code so
// the message names the real problem instead of reading as a plain mismatch.
func (p *program) reportUnassignable(pos position, actual, expected string, fallbackCode diagnosticCode, fallbackFormat string, args ...any) {
	switch {
	case actual == "null":
		p.add(pos, diagnosticCodeNullTarget, "null needs an optional type here; %s is not optional", displayName(expected))
	case isOptionalType(actual) && !isOptionalType(expected):
		p.add(pos, diagnosticCodeOptionalValue, "%s may be null; compare it with null and use the narrowed value where %s is required",
			displayName(actual), displayName(expected))
	default:
		p.add(pos, fallbackCode, fallbackFormat, args...)
	}
}

func (p *program) checkAssignable(pos position, actual, expected, target string, argument int) {
	if p.assignable(actual, expected) {
		return
	}
	// A failed optional target still reports against its base type, so the
	// interface wording below survives an argument declared as Shape?.
	required := expected
	if base, optional := optionalBase(expected); optional {
		required = base
	}
	if iface := p.interfaces[required]; iface != nil {
		if class := p.classes[actual]; class != nil {
			if reasons := p.classSatisfies(class, iface); len(reasons) > 0 {
				p.add(pos, diagnosticCodeCallArgument, "%s does not implement %s: %s", class.qualified, iface.qualified, strings.Join(reasons, "; "))
				return
			}
		}
		p.reportUnassignable(pos, actual, expected, diagnosticCodeCallArgument,
			"argument %d to %s must implement %s, found %s", argument, target, displayName(expected), displayName(actual))
		return
	}
	p.reportUnassignable(pos, actual, expected, diagnosticCodeCallArgument,
		"argument %d to %s must be %s, found %s", argument, target, displayName(expected), displayName(actual))
}

func (p *program) checkUnaryExpression(node *unaryExpression, scope *astScope) expressionInfo {
	value := p.checkASTExpression(node.value, scope)
	if value.typ == typeUnknown {
		return value
	}
	switch node.op {
	case "-":
		if value.typ != "int" && value.typ != "float" {
			p.reportUnaryOperatorType(node.pos, node.op, value.typ)
			value.typ = typeUnknown
		}
	case "!":
		if value.typ != "bool" {
			p.reportUnaryOperatorType(node.pos, node.op, value.typ)
		}
		value.typ = "bool"
	}
	return value
}

func (p *program) reportUnaryOperatorType(pos position, operator, typ string) {
	if isOptionalType(typ) {
		p.add(pos, diagnosticCodeOptionalValue,
			"unary operator %s does not accept %s; %s may be null; compare it with null and use the narrowed value",
			operator, displayName(typ), displayName(typ))
		return
	}
	p.add(pos, diagnosticCodeTypeMismatch, "unary operator %s does not accept %s", operator, displayName(typ))
}

func (p *program) reportBinaryOperatorType(pos position, operator, left, right string) {
	optional := ""
	if isOptionalType(left) {
		optional = left
	} else if isOptionalType(right) {
		optional = right
	}
	if optional != "" {
		p.add(pos, diagnosticCodeOptionalValue,
			"operator %s does not accept %s and %s; %s may be null; compare it with null and use the narrowed value",
			operator, displayName(left), displayName(right), displayName(optional))
		return
	}
	p.add(pos, diagnosticCodeTypeMismatch, "operator %s does not accept %s and %s",
		operator, displayName(left), displayName(right))
}

func (p *program) checkBinaryExpression(node *binaryExpression, scope *astScope) expressionInfo {
	left := p.checkASTExpression(node.left, scope)
	right := p.checkASTExpression(node.right, scope)
	effects := make(effectSet)
	mergeEffects(effects, left.effects)
	mergeEffects(effects, right.effects)
	switch node.op {
	case "==", "!=":
		if left.typ != typeUnknown && right.typ != typeUnknown && !comparableTypes(left.typ, right.typ) {
			code := diagnosticCodeTypeMismatch
			if isOptionalType(left.typ) || isOptionalType(right.typ) || left.typ == "null" || right.typ == "null" {
				code = diagnosticCodeOptionalComparison
			}
			p.add(node.pos, code, "cannot compare %s with %s", displayName(left.typ), displayName(right.typ))
		}
		return expressionInfo{typ: "bool", effects: effects}
	case "+", "-", "*":
		if left.typ == typeUnknown || right.typ == typeUnknown {
			return expressionInfo{typ: typeUnknown, effects: effects}
		}
		valid := left.typ == right.typ && (left.typ == "int" || left.typ == "float")
		if node.op == "+" {
			valid = valid || left.typ == "string" && right.typ == "string"
		}
		if !valid {
			p.reportBinaryOperatorType(node.pos, node.op, left.typ, right.typ)
			return expressionInfo{typ: typeUnknown, effects: effects}
		}
		return expressionInfo{typ: left.typ, effects: effects}
	case "<", "<=", ">", ">=":
		if left.typ != typeUnknown && right.typ != typeUnknown &&
			(left.typ != right.typ || left.typ != "int" && left.typ != "float") {
			p.reportBinaryOperatorType(node.pos, node.op, left.typ, right.typ)
		}
		return expressionInfo{typ: "bool", effects: effects}
	case "&&", "||":
		if left.typ != typeUnknown && right.typ != typeUnknown && (left.typ != "bool" || right.typ != "bool") {
			p.reportBinaryOperatorType(node.pos, node.op, left.typ, right.typ)
		}
		return expressionInfo{typ: "bool", effects: effects}
	default:
		return expressionInfo{typ: typeUnknown, effects: effects}
	}
}

func (p *program) checkIfExpression(node *ifExpression, scope *astScope, expected string) expressionInfo {
	condition := p.checkASTExpression(node.condition, scope)
	switch {
	case isOptionalType(condition.typ):
		p.add(node.condition.expressionPos(), diagnosticCodeOptionalCondition, "%s may be null and is not a condition; compare it with null instead", displayName(condition.typ))
	case condition.typ != "bool" && condition.typ != typeUnknown:
		p.add(node.condition.expressionPos(), diagnosticCodeTypeMismatch, "if condition must be bool, found %s", displayName(condition.typ))
	}
	thenScope, elseScope := scope.clone(), scope.clone()
	narrowNullTest(node.condition, scope, thenScope, elseScope)
	thenInfo := p.checkASTBlock(node.thenBlock, thenScope, expected)
	info := expressionInfo{typ: "null", effects: make(effectSet)}
	mergeEffects(info.effects, condition.effects)
	mergeEffects(info.effects, thenInfo.effects)
	elseInfo := expressionInfo{typ: "null", effects: make(effectSet)}
	if node.elseBlock != nil {
		elseInfo = p.checkASTBlock(node.elseBlock, elseScope, expected)
		mergeEffects(info.effects, elseInfo.effects)
	}
	p.mergePendingBranches(scope, thenScope, elseScope, thenInfo.typ != typeNever, elseInfo.typ != typeNever, node.pos)
	if node.elseBlock == nil {
		clearAssignedNarrowings(scope, node.thenBlock)
		return info
	}
	clearAssignedNarrowings(scope, node.thenBlock, node.elseBlock)
	joined, ok := joinTypes(thenInfo.typ, elseInfo.typ)
	if !ok {
		p.add(node.pos, diagnosticCodeTypeMismatch, "if branches must produce one type; found %s and %s", displayName(thenInfo.typ), displayName(elseInfo.typ))
		joined = typeUnknown
	}
	info.typ = joined
	return info
}

func (p *program) checkCatchExpression(node *catchExpression, scope *astScope, expected string) expressionInfo {
	valueInfo := p.checkASTExpressionExpecting(node.value, scope, expected)
	remaining := make(effectSet)
	for name, origin := range valueInfo.effects {
		remaining[name] = origin
	}
	result := expressionInfo{typ: valueInfo.typ, effects: make(effectSet)}
	paths := []pendingPath{{scope: scope.clone(), normal: valueInfo.typ != typeNever}}
	catchAll := false
	for _, arm := range node.arms {
		resolved, ok := p.resolveErrorIn(scope.function.namespace, scope.function.aliases, arm.errorType.name)
		if !ok {
			p.add(arm.errorType.pos, diagnosticCodeErrorValue, "%s does not name an Error type", arm.errorType.name)
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
		binding := arm.binding
		if binding == "" {
			binding = node.binding
		}
		if binding != "" {
			armScope.bind(binding, resolved)
		}
		armInfo := p.checkASTExpressionExpecting(arm.value, armScope, expected)
		mergeEffects(result.effects, armInfo.effects)
		paths = append(paths, pendingPath{scope: armScope, normal: armInfo.typ != typeNever})
		clearAssignedNarrowings(scope, arm.value)
		if armInfo.typ != typeNever && result.typ != armInfo.typ {
			p.add(arm.errorType.pos, diagnosticCodeTypeMismatch, "catch success and error paths must produce one type; found %s and %s", displayName(result.typ), displayName(armInfo.typ))
			result.typ = typeUnknown
		}
	}
	if !catchAll && len(remaining) > 0 {
		missing := make([]string, 0, len(remaining))
		for name := range remaining {
			missing = append(missing, displayName(name))
		}
		sort.Strings(missing)
		p.add(node.pos, diagnosticCodeNonExhaustiveCatch, "non-exhaustive catch for %s; missing %s", expressionLabel(node.value), strings.Join(missing, ", "))
	}
	p.mergePendingPaths(scope, node.pos, paths)
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
		p.add(node.pos, diagnosticCodeResultTypeUnknown, "%s needs a known Result type here; give the enclosing return type, argument, or field a Result<T, E> type", node.label())
		return expressionInfo{typ: typeUnknown, effects: p.checkASTExpression(node.value, scope).effects}
	}
	payload := success
	if !node.ok {
		payload = failure
	}
	info := p.checkASTExpressionExpecting(node.value, scope, payload)
	if !p.assignable(info.typ, payload) {
		p.reportUnassignable(node.pos, info.typ, payload, diagnosticCodeResultPayload,
			"%s payload must be %s, found %s", node.label(), displayName(payload), displayName(info.typ))
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
		p.add(node.pos, diagnosticCodePropagateReturn, "? requires %s to return Result, found %s", scope.function.qualified, displayName(declared))
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	success, failure, isResult := resultTypeArgs(info.typ)
	if !isResult {
		if info.typ != typeUnknown {
			p.add(node.pos, diagnosticCodePropagateValue, "? requires a Result value, found %s", displayName(info.typ))
		}
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	if failure != enclosingFailure {
		p.add(node.pos, diagnosticCodePropagateError, "? cannot propagate %s from %s, which fails with %s", displayName(failure), scope.function.qualified, displayName(enclosingFailure))
		return expressionInfo{typ: typeUnknown, effects: info.effects}
	}
	return expressionInfo{typ: success, effects: info.effects}
}

// checkMatchExpression types an exhaustive match. A Result scrutinee must
// reach Ok and Err; a union scrutinee must reach every declared variant. Either
// way one arm is selected and every reachable arm shares one type. An optional
// is neither: null is not an implicit variant.
func (p *program) checkMatchExpression(node *matchExpression, scope *astScope, expected string) expressionInfo {
	valueInfo := p.checkASTExpression(node.value, scope)
	info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	mergeEffects(info.effects, valueInfo.effects)
	if union := p.unions[valueInfo.typ]; union != nil {
		return p.checkUnionMatch(node, union, valueInfo.effects, scope, expected)
	}
	success, failure, isResult := resultTypeArgs(valueInfo.typ)
	if !isResult {
		if valueInfo.typ != typeUnknown {
			p.add(node.pos, diagnosticCodeMatchValue, "match requires a Result or union value, found %s", displayName(valueInfo.typ))
		}
		return info
	}
	handled := make(map[matchPattern]position, len(node.arms))
	armType := ""
	var paths []pendingPath
	for _, arm := range node.arms {
		if arm.pattern == matchPatternVariant {
			p.add(arm.pos, diagnosticCodeUnionVariant, "%s is a union variant pattern, but the matched value is %s", arm.variant, displayName(valueInfo.typ))
			continue
		}
		if previous, duplicate := handled[arm.pattern]; duplicate {
			p.add(arm.pos, diagnosticCodeMatchArm, "duplicate %s arm; already handled at %s:%d:%d", arm.pattern, previous.file, previous.line, previous.column)
			continue
		}
		if catchAll, exists := handled[matchPatternAny]; exists {
			p.add(arm.pos, diagnosticCodeMatchArm, "unreachable %s arm; the _ arm at %s:%d:%d already matches", arm.pattern, catchAll.file, catchAll.line, catchAll.column)
			continue
		}
		_, hasOk := handled[matchPatternOk]
		_, hasErr := handled[matchPatternErr]
		if arm.pattern == matchPatternAny && hasOk && hasErr {
			p.add(arm.pos, diagnosticCodeMatchArm, "unreachable _ arm; Ok and Err are already handled")
			continue
		}
		handled[arm.pattern] = arm.pos
		armScope := scope.clone()
		if arm.binding != "" {
			binding := success
			if arm.pattern == matchPatternErr {
				binding = failure
			}
			armScope.bind(arm.binding, binding)
		}
		armInfo := p.checkASTExpressionExpecting(arm.value, armScope, expected)
		mergeEffects(info.effects, armInfo.effects)
		paths = append(paths, pendingPath{scope: armScope, normal: armInfo.typ != typeNever})
		clearAssignedNarrowings(scope, arm.value)
		if armInfo.typ == typeNever || armInfo.typ == typeUnknown {
			continue
		}
		if armType == "" {
			armType = armInfo.typ
			continue
		}
		if armInfo.typ != armType {
			p.add(arm.pos, diagnosticCodeMatchArmType, "match arms must produce one type; found %s and %s", displayName(armType), displayName(armInfo.typ))
			armType = typeUnknown
		}
	}
	if _, catchAll := handled[matchPatternAny]; !catchAll {
		if _, ok := handled[matchPatternOk]; !ok {
			p.add(node.pos, diagnosticCodeMatchExhaustiveness, "match does not handle Ok; add an Ok(...) or _ arm")
		}
		if _, ok := handled[matchPatternErr]; !ok {
			p.add(node.pos, diagnosticCodeMatchExhaustiveness, "match does not handle Err; add an Err(...) or _ arm")
		}
	}
	p.mergePendingPaths(scope, node.pos, paths)
	if armType != "" {
		info.typ = armType
	}
	return info
}

// narrowNullTest records the refinement a condition of the form Name == null or
// Name != null proves, in either operand order. Only a simple local name with
// an optional storage type narrows; nothing is inferred from calls or field
// paths, and the fact reaches only the branch that proved it.
func narrowNullTest(condition expressionNode, scope, thenScope, elseScope *astScope) {
	name, present, ok := nullTestOf(condition)
	if !ok {
		return
	}
	declared, exists := scope.lookup(name)
	if !exists {
		return
	}
	base, optional := optionalBase(declared)
	if !optional {
		return
	}
	if present {
		thenScope.narrowed[name] = base
		return
	}
	elseScope.narrowed[name] = base
}

// nullTestOf reports the local a condition compares with null and whether the
// then-branch is the one that proves the local is present.
func nullTestOf(condition expressionNode) (name string, present bool, ok bool) {
	binary, isBinary := condition.(*binaryExpression)
	if !isBinary || (binary.op != "==" && binary.op != "!=") {
		return "", false, false
	}
	switch {
	case isNullLiteral(binary.right):
		name, ok = simpleLocalName(binary.left)
	case isNullLiteral(binary.left):
		name, ok = simpleLocalName(binary.right)
	}
	return name, binary.op == "!=", ok
}

func simpleLocalName(expression expressionNode) (string, bool) {
	name, ok := expression.(*nameExpression)
	if !ok || strings.Contains(name.name, ".") {
		return "", false
	}
	return name.name, true
}

func isNullLiteral(expression expressionNode) bool {
	literal, ok := expression.(*literalExpression)
	return ok && literal.value == nil
}

// clearAssignedNarrowings drops every refinement whose local a nested construct
// assigns. A branch or loop body that may or may not run cannot leave a
// refinement proven before it standing.
func clearAssignedNarrowings(scope *astScope, nodes ...any) {
	if len(scope.narrowed) == 0 {
		return
	}
	for _, node := range nodes {
		dropAssignedNarrowings(node, scope.narrowed)
	}
}

func dropAssignedNarrowings(node any, narrowed map[string]string) {
	switch value := node.(type) {
	case *blockNode:
		if value == nil {
			return
		}
		for _, statement := range value.statements {
			dropAssignedNarrowings(statement, narrowed)
		}
	case *assignmentStatement:
		delete(narrowed, value.name)
		dropAssignedNarrowings(value.value, narrowed)
	case *letStatement:
		dropAssignedNarrowings(value.value, narrowed)
	case *forStatement:
		dropAssignedNarrowings(value.iterable, narrowed)
		dropAssignedNarrowings(value.body, narrowed)
	case *throwStatement:
		dropAssignedNarrowings(value.value, narrowed)
	case *returnStatement:
		dropAssignedNarrowings(value.value, narrowed)
	case *expressionStatement:
		dropAssignedNarrowings(value.value, narrowed)
	case *usingExpression:
		dropAssignedNarrowings(value.initializer, narrowed)
		dropAssignedNarrowings(value.body, narrowed)
	case *arrayExpression:
		for _, element := range value.elements {
			dropAssignedNarrowings(element, narrowed)
		}
	case *tupleExpression:
		for _, element := range value.elements {
			dropAssignedNarrowings(element, narrowed)
		}
	case *rangeExpression:
		dropAssignedNarrowings(value.start, narrowed)
		dropAssignedNarrowings(value.end, narrowed)
	case *objectExpression:
		for _, field := range value.fields {
			dropAssignedNarrowings(field.value, narrowed)
		}
	case *callExpression:
		for _, argument := range value.args {
			dropAssignedNarrowings(argument, narrowed)
		}
	case *unaryExpression:
		dropAssignedNarrowings(value.value, narrowed)
	case *binaryExpression:
		dropAssignedNarrowings(value.left, narrowed)
		dropAssignedNarrowings(value.right, narrowed)
	case *ifExpression:
		dropAssignedNarrowings(value.condition, narrowed)
		dropAssignedNarrowings(value.thenBlock, narrowed)
		dropAssignedNarrowings(value.elseBlock, narrowed)
	case *catchExpression:
		dropAssignedNarrowings(value.value, narrowed)
		for _, arm := range value.arms {
			dropAssignedNarrowings(arm.value, narrowed)
		}
	case *resultExpression:
		dropAssignedNarrowings(value.value, narrowed)
	case *propagateExpression:
		dropAssignedNarrowings(value.value, narrowed)
	case *matchExpression:
		dropAssignedNarrowings(value.value, narrowed)
		for _, arm := range value.arms {
			dropAssignedNarrowings(arm.value, narrowed)
		}
	}
}

func (scope *astScope) clone() *astScope {
	locals := make(map[string]string, len(scope.locals))
	for name, typ := range scope.locals {
		locals[name] = typ
	}
	narrowed := make(map[string]string, len(scope.narrowed))
	for name, typ := range scope.narrowed {
		narrowed[name] = typ
	}
	usingBindings := make(map[string]usingBinding, len(scope.usingBindings))
	for name, binding := range scope.usingBindings {
		usingBindings[name] = binding
	}
	pending := make(map[string]pendingBinding, len(scope.pending))
	for name, binding := range scope.pending {
		binding.effects = cloneEffects(binding.effects)
		pending[name] = binding
	}
	return &astScope{
		function:      scope.function,
		locals:        locals,
		narrowed:      narrowed,
		usingBindings: usingBindings,
		pending:       pending,
		currentBlock:  scope.currentBlock,
		loopDepth:     scope.loopDepth,
	}
}

func cloneEffects(source effectSet) effectSet {
	cloned := make(effectSet, len(source))
	for name, origin := range source {
		cloned[name] = origin
	}
	return cloned
}

func copyPendingStates(target, source *astScope) {
	for name, binding := range target.pending {
		updated, exists := source.pending[name]
		if exists && updated.owner == binding.owner {
			binding.state = updated.state
			target.pending[name] = binding
		}
	}
}

func (p *program) mergePendingBranches(target, left, right *astScope, leftNormal, rightNormal bool, pos position) {
	p.mergePendingPaths(target, pos, []pendingPath{{scope: left, normal: leftNormal}, {scope: right, normal: rightNormal}})
}

func (p *program) mergePendingPaths(target *astScope, pos position, paths []pendingPath) {
	for name, binding := range target.pending {
		var state pendingState
		haveState := false
		mismatch := false
		for _, path := range paths {
			if !path.normal {
				continue
			}
			pathBinding, exists := path.scope.pending[name]
			if !exists || pathBinding.owner != binding.owner {
				pathBinding = binding
			}
			if !haveState {
				state = pathBinding.state
				haveState = true
				continue
			}
			if pathBinding.state != state {
				mismatch = true
			}
		}
		if !haveState {
			continue
		}
		if mismatch {
			if state != pendingInvalid {
				p.add(pos, diagnosticCodePendingPath, "pending binding %s is awaited on only some normal branches", name)
			}
			binding.state = pendingInvalid
		} else {
			binding.state = state
		}
		target.pending[name] = binding
	}
}

func (p *program) taskSafeType(name string, visiting map[string]bool) bool {
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional, typeKindArray:
		return p.taskSafeType(parsed.base, visiting)
	case typeKindTuple:
		for _, element := range parsed.args {
			if !p.taskSafeType(element, visiting) {
				return false
			}
		}
		return true
	case typeKindGeneric:
		if parsed.base != "Result" && parsed.base != mapTypeName {
			return false
		}
		for _, argument := range parsed.args {
			if !p.taskSafeType(argument, visiting) {
				return false
			}
		}
		return true
	case typeKindName:
		switch name {
		case "null", "bool", "int", "float", "string", "bytes":
			return true
		}
		if p.interfaces[name] != nil {
			return false
		}
		if union := p.unions[name]; union != nil {
			if visiting[name] {
				return true
			}
			visiting[name] = true
			defer delete(visiting, name)
			for _, variantName := range union.order {
				for _, field := range union.variants[variantName].fields {
					if !p.taskSafeType(p.resolveType(union.namespace, union.aliases, field.typ), visiting) {
						return false
					}
				}
			}
			return true
		}
		class := p.classes[name]
		if class == nil || class.nativeResource != "" {
			return false
		}
		if visiting[name] {
			return true
		}
		visiting[name] = true
		defer delete(visiting, name)
		for _, field := range class.fields {
			if !p.taskSafeType(p.resolveType(class.namespace, class.aliases, field.typ), visiting) {
				return false
			}
		}
		return true
	default:
		return false
	}
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

func iterableElementType(name string) (string, bool) {
	if element, isArray := arrayElementType(name); isArray {
		return element, true
	}
	if key, value, isMap := mapTypeArgs(name); isMap {
		return "(" + key + "," + value + ")", true
	}
	base, args, generic := genericType(name)
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

func tupleElementTypes(name string) ([]string, bool) {
	parsed := parseTypeName(name)
	if parsed.kind != typeKindTuple || len(parsed.args) < 2 {
		return nil, false
	}
	return parsed.args, true
}

func expressionLabel(expression expressionNode) string {
	switch node := expression.(type) {
	case *nameExpression:
		return node.name
	case *callExpression:
		return expressionLabel(node.callee)
	case *objectExpression:
		return node.typeName
	case *mapExpression:
		return "map literal"
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
	if isAbsoluteCanonicalName(name) {
		return name
	}
	if alias, ok := aliases[name]; ok {
		return alias.target
	}
	return qualify(namespace, name)
}
