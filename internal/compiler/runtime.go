package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type runtimeValue struct {
	typ      string
	scalar   any
	fields   map[string]runtimeValue
	elements []runtimeValue
	iterable *runtimeIterable
	mapping  *runtimeMap
	buffer   *runtimeBuffer
	result   *runtimeResult
	optional *runtimeOptional
	native   *nativeIOResource
}

// runtimeOptional is the tagged representation of a T? value. The complete
// static optional type stays on the owning runtimeValue's typ, so an absent
// int? never collapses into an absent User?, and present never depends on the
// payload being non-zero: 0, false, and "" are ordinary present values.
type runtimeOptional struct {
	present bool
	value   runtimeValue
}

// runtimeResult is a tagged Result payload. The complete static Result type
// stays on the owning runtimeValue's typ; ok selects which side payload holds.
// An Err is an ordinary value and never a slickThrow.
type runtimeResult struct {
	ok      bool
	payload runtimeValue
}
type runtimeMapKey struct {
	kind byte
	text string
}

type runtimeMapEntry struct {
	key       runtimeValue
	value     runtimeValue
	canonical runtimeMapKey
}

type runtimeMap struct {
	entries []runtimeMapEntry
	index   map[runtimeMapKey]int
}

type runtimeBuffer struct {
	elementType string
	values      []runtimeValue
}

func canonicalRuntimeMapKey(value runtimeValue) (runtimeMapKey, error) {
	switch value.typ {
	case "string":
		return runtimeMapKey{kind: 's', text: value.scalar.(string)}, nil
	case "int":
		return runtimeMapKey{kind: 'i', text: strconv.FormatInt(value.scalar.(int64), 10)}, nil
	case "bool":
		return runtimeMapKey{kind: 'b', text: strconv.FormatBool(value.scalar.(bool))}, nil
	default:
		return runtimeMapKey{}, fmt.Errorf("%s cannot be a Map key", displayName(value.typ))
	}
}

func newRuntimeMap(entries []runtimeMapEntry) (*runtimeMap, error) {
	result := &runtimeMap{
		entries: make([]runtimeMapEntry, 0, len(entries)),
		index:   make(map[runtimeMapKey]int, len(entries)),
	}
	for _, entry := range entries {
		key, err := canonicalRuntimeMapKey(entry.key)
		if err != nil {
			return nil, err
		}
		if index, exists := result.index[key]; exists {
			result.entries[index].value = entry.value
			continue
		}
		entry.canonical = key
		result.index[key] = len(result.entries)
		result.entries = append(result.entries, entry)
	}
	return result, nil
}

func runtimeMapWith(source *runtimeMap, key, value runtimeValue) (*runtimeMap, error) {
	canonical, err := canonicalRuntimeMapKey(key)
	if err != nil {
		return nil, err
	}
	if index, exists := source.index[canonical]; exists && runtimeEqual(source.entries[index].value, value) {
		return source, nil
	}
	result := &runtimeMap{
		entries: append([]runtimeMapEntry(nil), source.entries...),
		index:   make(map[runtimeMapKey]int, len(source.index)+1),
	}
	for stored, index := range source.index {
		result.index[stored] = index
	}
	if index, exists := result.index[canonical]; exists {
		result.entries[index].value = value
		return result, nil
	}
	result.index[canonical] = len(result.entries)
	result.entries = append(result.entries, runtimeMapEntry{key: key, value: value, canonical: canonical})
	return result, nil
}

func runtimeMapWithout(source *runtimeMap, key runtimeValue) (*runtimeMap, error) {
	canonical, err := canonicalRuntimeMapKey(key)
	if err != nil {
		return nil, err
	}
	removed, exists := source.index[canonical]
	if !exists {
		return source, nil
	}
	result := &runtimeMap{
		entries: make([]runtimeMapEntry, 0, len(source.entries)-1),
		index:   make(map[runtimeMapKey]int, len(source.index)-1),
	}
	for index, entry := range source.entries {
		if index == removed {
			continue
		}
		result.index[entry.canonical] = len(result.entries)
		result.entries = append(result.entries, entry)
	}
	return result, nil
}

func (r *runtimeResult) label() string {
	if r.ok {
		return "Ok"
	}
	return "Err"
}

// coerceRuntimeValue promotes value into target's shape: T becomes a present
// T?, null becomes an absent T?, and arrays convert element by element. Every
// place a value enters typed storage — a parameter, a return, a local, a
// field, an array element — goes through it, so promotion lives in one place.
func coerceRuntimeValue(value runtimeValue, target string) runtimeValue {
	if target == "" || value.typ == target {
		return value
	}
	if base, optional := optionalBase(target); optional {
		switch {
		case value.typ == "null":
			return runtimeValue{typ: target, optional: &runtimeOptional{}}
		case value.optional != nil:
			return runtimeValue{typ: target, optional: value.optional}
		default:
			return runtimeValue{typ: target, optional: &runtimeOptional{present: true, value: coerceRuntimeValue(value, base)}}
		}
	}
	if element, isArray := arrayElementType(target); isArray && value.elements != nil {
		elements := make([]runtimeValue, len(value.elements))
		for index, item := range value.elements {
			elements[index] = coerceRuntimeValue(item, element)
		}
		return runtimeValue{typ: target, elements: elements}
	}
	return value
}

// runtimeOptionalParts reads a value as an optional: an absent optional and a
// null literal are both absent, and any other value is its own payload. It lets
// equality compare T? with null, with T, and with T? uniformly.
func runtimeOptionalParts(value runtimeValue) (bool, runtimeValue) {
	if value.optional != nil {
		return value.optional.present, value.optional.value
	}
	if value.typ == "null" {
		return false, runtimeValue{}
	}
	return true, value
}

type runtimeIterable struct {
	kind    string
	sources []runtimeValue
	start   int64
	end     int64
}

// runtimeFrame separates declared storage from branch refinements the same way
// the checker's scope does: locals holds what a name was bound to, narrowed
// holds the payload a null test proved present. Assignment writes storage and
// retires the refinement, so a narrowed branch never hides a write.
type runtimeFrame struct {
	function  *functionDecl
	locals    map[string]runtimeValue
	narrowed  map[string]runtimeValue
	pending   map[string]*runtimePending
	taskScope *runtimeTaskScope
	ctx       context.Context
	parent    *runtimeFrame
}

type taskCancelled struct{}

func (*taskCancelled) Error() string { return "task cancelled" }

func checkTaskCancellation(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return &taskCancelled{}
	}
	return nil
}

func isTaskCancellation(err error) bool {
	var cancelled *taskCancelled
	return errors.As(err, &cancelled)
}

type runtimeTaskResult struct {
	value runtimeValue
	err   error
}

type runtimePending struct {
	result   chan runtimeTaskResult
	cancel   context.CancelFunc
	consumed bool
}

func (pending *runtimePending) await() (runtimeValue, error) {
	if pending.consumed {
		return runtimeValue{}, errors.New("pending binding already awaited")
	}
	pending.consumed = true
	result := <-pending.result
	pending.cancel()
	return result.value, result.err
}

type runtimeTaskScope struct {
	ctx      context.Context
	cancel   context.CancelFunc
	children []*runtimePending
}

func newRuntimeTaskScope(parent context.Context) *runtimeTaskScope {
	ctx, cancel := context.WithCancel(parent)
	return &runtimeTaskScope{ctx: ctx, cancel: cancel}
}

func (scope *runtimeTaskScope) start(call func(context.Context) (runtimeValue, error)) *runtimePending {
	ctx, cancel := context.WithCancel(scope.ctx)
	pending := &runtimePending{result: make(chan runtimeTaskResult, 1), cancel: cancel}
	scope.children = append(scope.children, pending)
	go func() {
		result := runtimeTaskResult{}
		defer func() {
			if failure := recover(); failure != nil {
				result = runtimeTaskResult{err: &panicFailure{value: failure}}
			}
			pending.result <- result
		}()
		result.value, result.err = call(ctx)
	}()
	return pending
}

func (scope *runtimeTaskScope) finish(primary error) error {
	outstanding := false
	for _, child := range scope.children {
		if !child.consumed {
			outstanding = true
			break
		}
	}
	if outstanding {
		scope.cancel()
	}
	for _, child := range scope.children {
		if child.consumed {
			continue
		}
		_, childError := child.await()
		if childError == nil || isTaskCancellation(childError) {
			continue
		}
		if primary == nil {
			primary = childError
		} else {
			primary = suppressFailure(primary, childError)
		}
	}
	scope.cancel()
	return primary
}

type slickThrow struct {
	typ     string
	message string
	value   runtimeValue
}

func (e *slickThrow) Error() string {
	if e.message == "" {
		return e.typ
	}
	return e.typ + ": " + e.message
}

type returnSignal struct {
	value runtimeValue
}

func (e *returnSignal) Error() string { return "return" }

type breakSignal struct{}

func (e *breakSignal) Error() string { return "break" }

type continueSignal struct{}

func (e *continueSignal) Error() string { return "continue" }

type panicFailure struct {
	value any
}

func (e *panicFailure) Error() string { return fmt.Sprintf("panic: %v", e.value) }

type suppressedFailure struct {
	primary    error
	suppressed []error
}

func (e *suppressedFailure) Error() string {
	items := make([]string, len(e.suppressed))
	for index, err := range e.suppressed {
		items[index] = err.Error()
	}
	return e.primary.Error() + " (suppressed: " + strings.Join(items, "; ") + ")"
}

func (e *suppressedFailure) Unwrap() error { return e.primary }

func suppressFailure(primary, suppressed error) error {
	if combined, ok := primary.(*suppressedFailure); ok {
		failures := make([]error, len(combined.suppressed), len(combined.suppressed)+1)
		copy(failures, combined.suppressed)
		failures = append(failures, suppressed)
		return &suppressedFailure{primary: combined.primary, suppressed: failures}
	}
	return &suppressedFailure{primary: primary, suppressed: []error{suppressed}}
}

func isControlSignal(err error) bool {
	var returned *returnSignal
	var shouldBreak *breakSignal
	var shouldContinue *continueSignal
	return errors.As(err, &returned) || errors.As(err, &shouldBreak) || errors.As(err, &shouldContinue)
}

func RunPath(path string) (string, []Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return "", nil, err
	}
	return Run(sources)
}

func Run(sources []Source) (string, []Diagnostic, error) {
	program, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return "", diagnostics, nil
	}
	value, err := program.runMain()
	if err != nil {
		return "", nil, err
	}
	return formatRuntimeValue(value), nil, nil
}

func (p *program) runMain() (runtimeValue, error) {
	main := p.functions["root.main"]
	if main == nil {
		return runtimeValue{}, errors.New("root.main is not defined")
	}
	if len(main.params) != 0 {
		return runtimeValue{}, errors.New("root.main must not accept parameters")
	}
	return p.callFunctionContext(context.Background(), main, nil, nil, nil)
}

func (p *program) callFunction(function *functionDecl, args []runtimeValue, self *runtimeValue, typeArgs []string) (runtimeValue, error) {
	return p.callFunctionContext(context.Background(), function, args, self, typeArgs)
}

func (p *program) callFunctionContext(ctx context.Context, function *functionDecl, args []runtimeValue, self *runtimeValue, typeArgs []string) (runtimeValue, error) {
	if err := checkTaskCancellation(ctx); err != nil {
		return runtimeValue{}, err
	}
	if len(args) != len(function.params) {
		return runtimeValue{}, fmt.Errorf("%s expects %d arguments, found %d", function.qualified, len(function.params), len(args))
	}
	frame := &runtimeFrame{function: function, locals: make(map[string]runtimeValue, len(args)+1), ctx: ctx}
	paramTypes := make([]string, len(function.params))
	if len(function.typeParams) > 0 {
		if len(typeArgs) != len(function.typeParams) {
			return runtimeValue{}, fmt.Errorf("%s expects %d type arguments, found %d", function.qualified, len(function.typeParams), len(typeArgs))
		}
		substitutions := make(map[string]string, len(function.typeParams))
		for index, name := range function.typeParams {
			substitutions[name] = typeArgs[index]
		}
		for index, param := range function.params {
			paramTypes[index] = substituteTypeParams(p.resolveType(function.namespace, function.aliases, param.typ), substitutions)
		}
	} else {
		for index, param := range function.params {
			paramTypes[index] = p.resolveType(function.namespace, function.aliases, param.typ)
		}
	}
	for index, param := range function.params {
		frame.locals[param.name] = coerceRuntimeValue(args[index], paramTypes[index])
	}
	if self != nil {
		frame.locals["self"] = *self
	}
	if function.native != "" {
		return p.callNativeFunction(function, frame, typeArgs)
	}
	result := p.resolveType(function.namespace, function.aliases, function.result)
	if len(function.typeParams) > 0 {
		substitutions := make(map[string]string, len(function.typeParams))
		for index, name := range function.typeParams {
			substitutions[name] = typeArgs[index]
		}
		result = substituteTypeParams(result, substitutions)
	}
	value, err := p.evalBlock(function.ast, frame)
	var returned *returnSignal
	if errors.As(err, &returned) {
		return coerceRuntimeValue(returned.value, result), nil
	}
	if err != nil {
		return runtimeValue{}, err
	}
	return coerceRuntimeValue(value, result), nil
}

func (p *program) evalBlock(block *blockNode, frame *runtimeFrame) (last runtimeValue, err error) {
	last = nullRuntimeValue()
	if block == nil {
		return last, nil
	}
	if block.hasAsync {
		scope := newRuntimeTaskScope(frame.ctx)
		blockFrame := frame.clone()
		blockFrame.ctx = scope.ctx
		blockFrame.taskScope = scope
		blockFrame.pending = make(map[string]*runtimePending)
		frame = blockFrame
		defer func() {
			if failure := recover(); failure != nil {
				last = runtimeValue{}
				err = &panicFailure{value: failure}
			}
			err = scope.finish(err)
		}()
	}
	for _, statement := range block.statements {
		var value runtimeValue
		value, err = p.evalStatement(statement, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		last = value
	}
	return last, nil
}

func (p *program) evalStatement(statement statementNode, frame *runtimeFrame) (runtimeValue, error) {
	switch node := statement.(type) {
	case *letStatement:
		value, err := p.evalExpression(node.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		// A fresh binding replaces any refinement of the same name, exactly as
		// the checker's scope does.
		delete(frame.narrowed, node.name)
		frame.locals[node.name] = coerceRuntimeValue(value, node.resolved)
		return nullRuntimeValue(), nil
	case *asyncLetStatement:
		if err := checkTaskCancellation(frame.ctx); err != nil {
			return runtimeValue{}, err
		}
		call, err := p.prepareAsyncCall(node.call, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		if frame.taskScope == nil {
			return runtimeValue{}, runtimeError(node.pos, "async let has no owning task scope")
		}
		if frame.pending == nil {
			frame.pending = make(map[string]*runtimePending)
		}
		frame.pending[node.name] = frame.taskScope.start(call)
		return nullRuntimeValue(), nil
	case *assignmentStatement:
		value, err := p.evalExpression(node.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		if !frame.assign(node.name, coerceRuntimeValue(value, node.resolved)) {
			return runtimeValue{}, runtimeError(node.pos, "unknown value %s", node.name)
		}
		return nullRuntimeValue(), nil
	case *forStatement:
		return p.evalFor(node, frame)
	case *breakStatement:
		return runtimeValue{}, &breakSignal{}
	case *continueStatement:
		return runtimeValue{}, &continueSignal{}
	case *throwStatement:
		value, err := p.evalExpression(node.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		message := ""
		if text, ok := value.scalar.(string); ok {
			message = text
		} else if field, ok := value.fields["Message"]; ok {
			message = formatRuntimeValue(field)
		} else if field, ok := value.fields["message"]; ok {
			message = formatRuntimeValue(field)
		}
		return runtimeValue{}, &slickThrow{typ: value.typ, message: message, value: value}
	case *returnStatement:
		value, err := p.evalExpression(node.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		return runtimeValue{}, &returnSignal{value: value}
	case *expressionStatement:
		return p.evalExpression(node.value, frame)
	default:
		return runtimeValue{}, fmt.Errorf("unsupported statement at %s:%d:%d", statement.statementPos().file, statement.statementPos().line, statement.statementPos().column)
	}
}

func (p *program) evalExpression(expression expressionNode, frame *runtimeFrame) (runtimeValue, error) {
	switch node := expression.(type) {
	case *literalExpression:
		return runtimeValue{typ: literalType(node.value), scalar: node.value}, nil
	case *arrayExpression:
		elements := make([]runtimeValue, 0, len(node.elements))
		elementType := ""
		for _, element := range node.elements {
			value, err := p.evalExpression(element, frame)
			if err != nil {
				return runtimeValue{}, err
			}
			elements = append(elements, value)
			if elementType == "" {
				elementType = value.typ
				continue
			}
			// The same join the checker applied, so a literal mixing values and
			// null materializes as one homogeneous optional array.
			if joined, ok := joinTypes(elementType, value.typ); ok {
				elementType = joined
			}
		}
		typ := node.resolved
		if typ == "" {
			typ = typeUnknown + "[]"
		}
		if elementType != "" {
			typ = elementType + "[]"
			for index := range elements {
				elements[index] = coerceRuntimeValue(elements[index], elementType)
			}
		}
		return runtimeValue{typ: typ, elements: elements}, nil
	case *mapExpression:
		keyType, valueType, _ := mapTypeArgs(node.resolved)
		entries := make([]runtimeMapEntry, 0, len(node.entries))
		for _, entry := range node.entries {
			key, err := p.evalExpression(entry.key, frame)
			if err != nil {
				return runtimeValue{}, err
			}
			value, err := p.evalExpression(entry.value, frame)
			if err != nil {
				return runtimeValue{}, err
			}
			entries = append(entries, runtimeMapEntry{
				key:   coerceRuntimeValue(key, keyType),
				value: coerceRuntimeValue(value, valueType),
			})
		}
		mapping, err := newRuntimeMap(entries)
		if err != nil {
			return runtimeValue{}, runtimeError(node.pos, "%v", err)
		}
		return runtimeValue{typ: node.resolved, mapping: mapping}, nil
	case *rangeExpression:
		start, err := p.evalExpression(node.start, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		end, err := p.evalExpression(node.end, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		return runtimeValue{
			typ:      "Iterable<int>",
			iterable: &runtimeIterable{kind: "range", start: start.scalar.(int64), end: end.scalar.(int64)},
		}, nil
	case *templateExpression:
		text, err := p.renderTemplate(node.text, frame)
		return runtimeValue{typ: "string", scalar: text}, err
	case *nameExpression:
		return p.evalName(node, frame)
	case *objectExpression:
		return p.evalObject(node, frame)
	case *callExpression:
		return p.evalCall(node, frame)
	case *awaitExpression:
		pending, ok := frame.lookupPending(node.name)
		if !ok {
			return runtimeValue{}, runtimeError(node.pos, "unknown pending binding %s", node.name)
		}
		return pending.await()
	case *unaryExpression:
		return p.evalUnary(node, frame)
	case *binaryExpression:
		return p.evalBinary(node, frame)
	case *ifExpression:
		return p.evalIf(node, frame)
	case *catchExpression:
		return p.evalCatch(node, frame)
	case *resultExpression:
		return p.evalResult(node, frame)
	case *propagateExpression:
		return p.evalPropagate(node, frame)
	case *usingExpression:
		return p.evalUsing(node, frame)
	case *matchExpression:
		return p.evalMatch(node, frame)
	default:
		return runtimeValue{}, fmt.Errorf("unsupported expression at %s:%d:%d", expression.expressionPos().file, expression.expressionPos().line, expression.expressionPos().column)
	}
}
func (p *program) evalUsing(node *usingExpression, frame *runtimeFrame) (runtimeValue, error) {
	resource, err := p.evalExpression(node.initializer, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	bodyFrame := frame.clone()
	bodyFrame.locals[node.name] = resource
	value, bodyError := p.evalUsingBody(node.body, bodyFrame)
	closeError := p.closeUsingResource(frame.ctx, resource, node)
	if closeError == nil {
		return value, bodyError
	}
	if bodyError == nil || isControlSignal(bodyError) {
		return runtimeValue{}, closeError
	}
	return runtimeValue{}, suppressFailure(bodyError, closeError)
}

func (p *program) evalUsingBody(body *blockNode, frame *runtimeFrame) (value runtimeValue, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			value = runtimeValue{}
			err = &panicFailure{value: failure}
		}
	}()
	return p.evalBlock(body, frame)
}

func (p *program) closeUsingResource(ctx context.Context, resource runtimeValue, node *usingExpression) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = &panicFailure{value: failure}
		}
	}()
	class := p.classes[resource.typ]
	if class == nil || class.implementations["Close"] == nil {
		return runtimeError(node.pos, "%s has no implemented Close method", displayName(resource.typ))
	}
	_, err = p.callFunctionContext(context.WithoutCancel(ctx), class.implementations["Close"], nil, &resource, nil)
	return err
}

func (p *program) evalName(node *nameExpression, frame *runtimeFrame) (runtimeValue, error) {
	parts := strings.Split(node.name, ".")
	value, ok := frame.lookup(parts[0])
	if !ok {
		return runtimeValue{}, runtimeError(node.pos, "unknown value %s", node.name)
	}
	for _, fieldName := range parts[1:] {
		// The checker only lets a field read past an optional inside a branch
		// that proved the value present, so the payload is there; an absent one
		// is a compiler bug and is reported rather than silently zeroed.
		if value.optional != nil {
			if !value.optional.present {
				return runtimeValue{}, runtimeError(node.pos, "%s is null and has no field %s", displayName(value.typ), fieldName)
			}
			value = value.optional.value
		}
		field, ok := value.fields[fieldName]
		if !ok {
			return runtimeValue{}, runtimeError(node.pos, "%s has no field %s", displayName(value.typ), fieldName)
		}
		value = field
	}
	return value, nil
}

func (p *program) evalObject(node *objectExpression, frame *runtimeFrame) (runtimeValue, error) {
	canonical := p.resolveNameIn(frame.function.namespace, frame.function.aliases, node.typeName)
	class := p.classes[canonical]
	if class == nil {
		return runtimeValue{}, runtimeError(node.pos, "unknown class %s", node.typeName)
	}
	value := runtimeValue{typ: canonical, fields: make(map[string]runtimeValue, len(class.fields))}
	declared := make(map[string]string, len(class.fields))
	for name, field := range class.fields {
		// An omitted optional field defaults to a typed absent value, not to a
		// bare null, so it stays distinguishable from every other optional.
		declared[name] = p.resolveType(class.namespace, class.aliases, field.typ)
		value.fields[name] = coerceRuntimeValue(nullRuntimeValue(), declared[name])
	}
	for _, field := range node.fields {
		fieldValue, err := p.evalExpression(field.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		value.fields[field.name] = coerceRuntimeValue(fieldValue, declared[field.name])
	}
	return value, nil
}

func (p *program) prepareAsyncCall(node *callExpression, frame *runtimeFrame) (func(context.Context) (runtimeValue, error), error) {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		return nil, runtimeError(node.pos, "async call target is not callable")
	}

	var function *functionDecl
	var receiver *runtimeValue
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 && node.resolvedReceiver != "" {
		class := p.classes[node.resolvedReceiver]
		if class == nil || class.implementations[parts[1]] == nil {
			return nil, runtimeError(node.pos, "unknown async method %s", name.name)
		}
		function = class.implementations[parts[1]]
		value, exists := frame.lookup(parts[0])
		if !exists {
			return nil, runtimeError(node.pos, "unknown async receiver %s", parts[0])
		}
		if value.optional != nil {
			if !value.optional.present {
				return nil, runtimeError(node.pos, "%s is null and has no method %s", displayName(value.typ), parts[1])
			}
			value = value.optional.value
		}
		receiver = &value
	} else {
		function = p.resolveFunction(frame.function, name.name)
		if function == nil {
			return nil, runtimeError(node.pos, "unknown async function %s", name.name)
		}
	}

	args := make([]runtimeValue, len(node.args))
	for index, argument := range node.args {
		value, err := p.evalExpression(argument, frame)
		if err != nil {
			return nil, err
		}
		if index < len(node.resolvedParams) {
			value = coerceRuntimeValue(value, node.resolvedParams[index])
		}
		args[index] = value
	}
	typeArgs := append([]string(nil), node.resolvedTypeArgs...)
	return func(ctx context.Context) (runtimeValue, error) {
		return p.callFunctionContext(ctx, function, args, receiver, typeArgs)
	}, nil
}

func (p *program) evalCall(node *callExpression, frame *runtimeFrame) (runtimeValue, error) {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		return runtimeValue{}, runtimeError(node.pos, "call target is not callable")
	}
	args := make([]runtimeValue, 0, len(node.args))
	for _, argument := range node.args {
		value, err := p.evalExpression(argument, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		args = append(args, value)
	}
	if name.name == "enumerate" {
		elementType, _ := iterableElementType(args[0].typ)
		return runtimeValue{
			typ:      iterableType("int", elementType),
			iterable: &runtimeIterable{kind: "enumerate", sources: args},
		}, nil
	}
	if name.name == "zip" {
		elementTypes := make([]string, 0, len(args))
		for _, argument := range args {
			elementType, _ := iterableElementType(argument.typ)
			elementTypes = append(elementTypes, elementType)
		}
		return runtimeValue{
			typ:      iterableType(elementTypes...),
			iterable: &runtimeIterable{kind: "zip", sources: args},
		}, nil
	}
	if errorType, isError := p.resolveErrorIn(frame.function.namespace, frame.function.aliases, name.name); isError && p.classes[errorType] != nil {
		value := runtimeValue{typ: errorType, fields: make(map[string]runtimeValue)}
		if len(args) > 0 {
			value.scalar = args[0].scalar
		}
		return value, nil
	}

	parts := strings.Split(name.name, ".")
	if len(parts) == 2 {
		if receiver, exists := frame.lookup(parts[0]); exists {
			// Narrowing proved the receiver present before the checker allowed
			// this call, so the payload is the real receiver.
			if receiver.optional != nil {
				if !receiver.optional.present {
					return runtimeValue{}, runtimeError(node.pos, "%s is null and has no method %s", displayName(receiver.typ), parts[1])
				}
				receiver = receiver.optional.value
			}
			if _, isArray := arrayElementType(receiver.typ); isArray {
				return evalRuntimeArrayCall(node, parts[1], receiver, args)
			}
			if receiver.mapping != nil {
				return evalRuntimeMapCall(node, parts[1], receiver, args)
			}
			class := p.classes[receiver.typ]
			if class == nil {
				return runtimeValue{}, runtimeError(node.pos, "%s is not a class value", parts[0])
			}
			implementation := class.implementations[parts[1]]
			if implementation == nil {
				return runtimeValue{}, runtimeError(node.pos, "%s has no implemented method %s", class.name, parts[1])
			}
			return p.callFunctionContext(frame.ctx, implementation, args, &receiver, nil)
		}
	}
	canonical := p.resolveNameIn(frame.function.namespace, frame.function.aliases, name.name)
	function := p.functions[canonical]
	if function == nil {
		return runtimeValue{}, runtimeError(node.pos, "unknown function %s", name.name)
	}
	return p.callFunctionContext(frame.ctx, function, args, nil, append([]string(nil), node.resolvedTypeArgs...))
}

func evalRuntimeArrayCall(node *callExpression, method string, receiver runtimeValue, args []runtimeValue) (runtimeValue, error) {
	elementType, _ := arrayElementType(receiver.typ)
	switch method {
	case "Length":
		return runtimeValue{typ: "int", scalar: int64(len(receiver.elements))}, nil
	case "Get":
		return runtimeIndexedValue(elementType, receiver.elements, args[0].scalar.(int64)), nil
	case "Slice":
		start, end := args[0].scalar.(int64), args[1].scalar.(int64)
		if start < 0 || end < start || end > int64(len(receiver.elements)) {
			failure := runtimeValue{typ: stdCollectionsBoundsFailureName}
			return runtimeResultValue(node.resolvedResult, false, failure), nil
		}
		values := append([]runtimeValue(nil), receiver.elements[start:end]...)
		return runtimeResultValue(node.resolvedResult, true, runtimeValue{typ: receiver.typ, elements: values}), nil
	default:
		return runtimeValue{}, runtimeError(node.pos, "%s has no method %s", displayName(receiver.typ), method)
	}
}

func runtimeIndexedValue(elementType string, values []runtimeValue, index int64) runtimeValue {
	resultType := optionalOf(elementType)
	if index < 0 || index >= int64(len(values)) {
		return coerceRuntimeValue(nullRuntimeValue(), resultType)
	}
	value := coerceRuntimeValue(values[index], elementType)
	if isOptionalType(elementType) {
		return value
	}
	return runtimeValue{typ: resultType, optional: &runtimeOptional{present: true, value: value}}
}
func evalRuntimeMapCall(node *callExpression, method string, receiver runtimeValue, args []runtimeValue) (runtimeValue, error) {
	keyType, valueType, _ := mapTypeArgs(receiver.typ)
	switch method {
	case "Get":
		key := coerceRuntimeValue(args[0], keyType)
		canonical, err := canonicalRuntimeMapKey(key)
		if err != nil {
			return runtimeValue{}, err
		}
		index, present := receiver.mapping.index[canonical]
		if isOptionalType(valueType) {
			if !present {
				return coerceRuntimeValue(nullRuntimeValue(), valueType), nil
			}
			return receiver.mapping.entries[index].value, nil
		}
		result := &runtimeOptional{}
		if present {
			result.present = true
			result.value = receiver.mapping.entries[index].value
		}
		return runtimeValue{typ: optionalOf(valueType), optional: result}, nil
	case "Contains":
		key := coerceRuntimeValue(args[0], keyType)
		canonical, err := canonicalRuntimeMapKey(key)
		if err != nil {
			return runtimeValue{}, err
		}
		_, present := receiver.mapping.index[canonical]
		return runtimeValue{typ: "bool", scalar: present}, nil
	case "With":
		key := coerceRuntimeValue(args[0], keyType)
		value := coerceRuntimeValue(args[1], valueType)
		mapping, err := runtimeMapWith(receiver.mapping, key, value)
		return runtimeValue{typ: receiver.typ, mapping: mapping}, err
	case "Without":
		key := coerceRuntimeValue(args[0], keyType)
		mapping, err := runtimeMapWithout(receiver.mapping, key)
		return runtimeValue{typ: receiver.typ, mapping: mapping}, err
	case "Length":
		return runtimeValue{typ: "int", scalar: int64(len(receiver.mapping.entries))}, nil
	default:
		return runtimeValue{}, runtimeError(node.pos, "%s has no method %s", displayName(receiver.typ), method)
	}
}

func (p *program) evalUnary(node *unaryExpression, frame *runtimeFrame) (runtimeValue, error) {
	value, err := p.evalExpression(node.value, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	switch node.op {
	case "-":
		if value.typ == "int" {
			return runtimeValue{typ: "int", scalar: -value.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "float", scalar: -value.scalar.(float64)}, nil
	case "!":
		return runtimeValue{typ: "bool", scalar: !value.scalar.(bool)}, nil
	default:
		return runtimeValue{}, runtimeError(node.pos, "unsupported unary operation %s", node.op)
	}
}

func (p *program) evalBinary(node *binaryExpression, frame *runtimeFrame) (runtimeValue, error) {
	left, err := p.evalExpression(node.left, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	if node.op == "&&" && !left.scalar.(bool) {
		return runtimeValue{typ: "bool", scalar: false}, nil
	}
	if node.op == "||" && left.scalar.(bool) {
		return runtimeValue{typ: "bool", scalar: true}, nil
	}
	right, err := p.evalExpression(node.right, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	switch node.op {
	case "==":
		return runtimeValue{typ: "bool", scalar: runtimeEqual(left, right)}, nil
	case "!=":
		return runtimeValue{typ: "bool", scalar: !runtimeEqual(left, right)}, nil
	case "+":
		switch left.typ {
		case "string":
			return runtimeValue{typ: "string", scalar: left.scalar.(string) + right.scalar.(string)}, nil
		case "int":
			return runtimeValue{typ: "int", scalar: left.scalar.(int64) + right.scalar.(int64)}, nil
		case "float":
			return runtimeValue{typ: "float", scalar: left.scalar.(float64) + right.scalar.(float64)}, nil
		}
	case "-":
		if left.typ == "int" {
			return runtimeValue{typ: "int", scalar: left.scalar.(int64) - right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "float", scalar: left.scalar.(float64) - right.scalar.(float64)}, nil
	case "*":
		if left.typ == "int" {
			return runtimeValue{typ: "int", scalar: left.scalar.(int64) * right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "float", scalar: left.scalar.(float64) * right.scalar.(float64)}, nil
	case "<":
		if left.typ == "int" {
			return runtimeValue{typ: "bool", scalar: left.scalar.(int64) < right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "bool", scalar: left.scalar.(float64) < right.scalar.(float64)}, nil
	case "<=":
		if left.typ == "int" {
			return runtimeValue{typ: "bool", scalar: left.scalar.(int64) <= right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "bool", scalar: left.scalar.(float64) <= right.scalar.(float64)}, nil
	case ">":
		if left.typ == "int" {
			return runtimeValue{typ: "bool", scalar: left.scalar.(int64) > right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "bool", scalar: left.scalar.(float64) > right.scalar.(float64)}, nil
	case ">=":
		if left.typ == "int" {
			return runtimeValue{typ: "bool", scalar: left.scalar.(int64) >= right.scalar.(int64)}, nil
		}
		return runtimeValue{typ: "bool", scalar: left.scalar.(float64) >= right.scalar.(float64)}, nil
	case "&&", "||":
		return runtimeValue{typ: "bool", scalar: right.scalar.(bool)}, nil
	}
	return runtimeValue{}, runtimeError(node.pos, "unsupported binary operation %s", node.op)
}

func (p *program) evalFor(node *forStatement, frame *runtimeFrame) (runtimeValue, error) {
	iterable, err := p.evalExpression(node.iterable, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	length, err := runtimeIterableLength(iterable)
	if err != nil {
		return runtimeValue{}, runtimeError(node.pos, "%v", err)
	}
	for index := 0; index < length; index++ {
		if err := checkTaskCancellation(frame.ctx); err != nil {
			return runtimeValue{}, err
		}
		values, err := runtimeIterableValues(iterable, index)
		if err != nil {
			return runtimeValue{}, runtimeError(node.pos, "%v", err)
		}
		if len(node.bindings) == 1 {
			values = []runtimeValue{packRuntimeValues(values)}
		}
		iteration := frame.clone()
		for bindingIndex, binding := range node.bindings {
			if binding != "_" {
				iteration.locals[binding] = values[bindingIndex]
			}
		}
		_, err = p.evalBlock(node.body, iteration)
		if err == nil {
			continue
		}
		var shouldBreak *breakSignal
		if errors.As(err, &shouldBreak) {
			break
		}
		var shouldContinue *continueSignal
		if errors.As(err, &shouldContinue) {
			continue
		}
		return runtimeValue{}, err
	}
	return nullRuntimeValue(), nil
}

func runtimeIterableLength(value runtimeValue) (int, error) {
	if value.iterable == nil {
		if strings.HasSuffix(value.typ, "[]") {
			return len(value.elements), nil
		}
		if value.mapping != nil {
			return len(value.mapping.entries), nil
		}
		return 0, fmt.Errorf("%s is not iterable", displayName(value.typ))
	}
	switch value.iterable.kind {
	case "range":
		if value.iterable.end <= value.iterable.start {
			return 0, nil
		}
		length := value.iterable.end - value.iterable.start
		if int64(int(length)) != length {
			return 0, errors.New("range is too large")
		}
		return int(length), nil
	case "enumerate":
		return runtimeIterableLength(value.iterable.sources[0])
	case "zip":
		length, err := runtimeIterableLength(value.iterable.sources[0])
		if err != nil {
			return 0, err
		}
		for _, source := range value.iterable.sources[1:] {
			sourceLength, err := runtimeIterableLength(source)
			if err != nil {
				return 0, err
			}
			if sourceLength < length {
				length = sourceLength
			}
		}
		return length, nil
	default:
		return 0, fmt.Errorf("unsupported iterable %s", value.iterable.kind)
	}
}

func runtimeIterableValues(value runtimeValue, index int) ([]runtimeValue, error) {
	if value.iterable == nil {
		if value.mapping != nil {
			if index >= len(value.mapping.entries) {
				return nil, fmt.Errorf("%s has no iterable value at %d", displayName(value.typ), index)
			}
			entry := value.mapping.entries[index]
			return []runtimeValue{entry.key, entry.value}, nil
		}
		if !strings.HasSuffix(value.typ, "[]") || index >= len(value.elements) {
			return nil, fmt.Errorf("%s has no iterable value at %d", displayName(value.typ), index)
		}
		return []runtimeValue{value.elements[index]}, nil
	}
	switch value.iterable.kind {
	case "range":
		return []runtimeValue{{typ: "int", scalar: value.iterable.start + int64(index)}}, nil
	case "enumerate":
		source, err := runtimeIterableValues(value.iterable.sources[0], index)
		if err != nil {
			return nil, err
		}
		return []runtimeValue{{typ: "int", scalar: int64(index)}, packRuntimeValues(source)}, nil
	case "zip":
		values := make([]runtimeValue, 0, len(value.iterable.sources))
		for _, source := range value.iterable.sources {
			sourceValues, err := runtimeIterableValues(source, index)
			if err != nil {
				return nil, err
			}
			values = append(values, packRuntimeValues(sourceValues))
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported iterable %s", value.iterable.kind)
	}
}

func packRuntimeValues(values []runtimeValue) runtimeValue {
	if len(values) == 1 {
		return values[0]
	}
	types := make([]string, 0, len(values))
	for _, value := range values {
		types = append(types, value.typ)
	}
	return runtimeValue{typ: "(" + strings.Join(types, ",") + ")", elements: values}
}

func (p *program) evalIf(node *ifExpression, frame *runtimeFrame) (runtimeValue, error) {
	condition, err := p.evalExpression(node.condition, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	truth, ok := condition.scalar.(bool)
	if !ok {
		return runtimeValue{}, runtimeError(node.pos, "if condition is not bool")
	}
	if truth {
		return p.evalBlock(node.thenBlock, narrowedFrame(node.condition, frame, true))
	}
	if node.elseBlock != nil {
		return p.evalBlock(node.elseBlock, narrowedFrame(node.condition, frame, false))
	}
	return nullRuntimeValue(), nil
}

// narrowedFrame mirrors the checker's branch narrowing: inside the branch a
// null test proved present, the local reads as its payload while its declared
// storage stays where it was.
func narrowedFrame(condition expressionNode, frame *runtimeFrame, thenBranch bool) *runtimeFrame {
	branch := frame.clone()
	name, present, ok := nullTestOf(condition)
	if !ok || present != thenBranch {
		return branch
	}
	value, exists := frame.lookup(name)
	if !exists || value.optional == nil || !value.optional.present {
		return branch
	}
	branch.narrowed = map[string]runtimeValue{name: value.optional.value}
	return branch
}

func (p *program) evalCatch(node *catchExpression, frame *runtimeFrame) (runtimeValue, error) {
	value, err := p.evalExpression(node.value, frame)
	if err == nil {
		return value, nil
	}
	var thrown *slickThrow
	if !errors.As(err, &thrown) {
		return runtimeValue{}, err
	}
	for _, arm := range node.arms {
		errorType, ok := p.resolveErrorIn(frame.function.namespace, frame.function.aliases, arm.errorType.name)
		if !ok || (errorType != "Error" && errorType != thrown.typ) {
			continue
		}
		armFrame := frame.clone()
		binding := arm.binding
		if binding == "" {
			binding = node.binding
		}
		if binding != "" {
			caught := thrown.value
			if caught.typ == "" {
				caught = runtimeValue{typ: thrown.typ, scalar: thrown.message, fields: make(map[string]runtimeValue)}
			}
			armFrame.locals[binding] = caught
		}
		return p.evalExpression(arm.value, armFrame)
	}
	return runtimeValue{}, err
}

func (p *program) evalResult(node *resultExpression, frame *runtimeFrame) (runtimeValue, error) {
	payload, err := p.evalExpression(node.value, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	return runtimeValue{typ: node.resolved, result: &runtimeResult{ok: node.ok, payload: payload}}, nil
}

// evalPropagate evaluates its operand exactly once. An Err leaves the enclosing
// function through the ordinary early-return signal carrying an Err Result, so
// it never reaches a catch arm.
func (p *program) evalPropagate(node *propagateExpression, frame *runtimeFrame) (runtimeValue, error) {
	value, err := p.evalExpression(node.value, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	if value.result == nil {
		return runtimeValue{}, runtimeError(node.pos, "? requires a Result value, found %s", displayName(value.typ))
	}
	if value.result.ok {
		return value.result.payload, nil
	}
	enclosing := p.resolveType(frame.function.namespace, frame.function.aliases, frame.function.result)
	return runtimeValue{}, &returnSignal{value: runtimeValue{
		typ:    enclosing,
		result: &runtimeResult{payload: value.result.payload},
	}}
}

func (p *program) evalMatch(node *matchExpression, frame *runtimeFrame) (runtimeValue, error) {
	value, err := p.evalExpression(node.value, frame)
	if err != nil {
		return runtimeValue{}, err
	}
	if value.result == nil {
		return runtimeValue{}, runtimeError(node.pos, "match requires a Result value, found %s", displayName(value.typ))
	}
	for _, arm := range node.arms {
		if arm.pattern == matchPatternOk && !value.result.ok {
			continue
		}
		if arm.pattern == matchPatternErr && value.result.ok {
			continue
		}
		armFrame := frame.clone()
		if arm.binding != "" {
			armFrame.locals[arm.binding] = value.result.payload
		}
		return p.evalExpression(arm.value, armFrame)
	}
	return runtimeValue{}, runtimeError(node.pos, "match has no arm for %s", displayName(value.typ))
}

func (p *program) renderTemplate(template string, frame *runtimeFrame) (string, error) {
	var output strings.Builder
	for {
		start := strings.Index(template, "${")
		if start < 0 {
			output.WriteString(template)
			return output.String(), nil
		}
		output.WriteString(template[:start])
		template = template[start+2:]
		end := strings.IndexByte(template, '}')
		if end < 0 {
			return "", errors.New("unterminated template interpolation")
		}
		name := strings.TrimSpace(template[:end])
		value, err := p.evalName(&nameExpression{name: name}, frame)
		if err != nil {
			return "", err
		}
		output.WriteString(formatRuntimeValue(value))
		template = template[end+1:]
	}
}

func (frame *runtimeFrame) clone() *runtimeFrame {
	return &runtimeFrame{function: frame.function, locals: make(map[string]runtimeValue), ctx: frame.ctx, parent: frame}
}

// lookup reads a name as the nearest frame sees it: a refinement proven on that
// frame wins over the storage it refines, and both win over any outer frame.
func (frame *runtimeFrame) lookup(name string) (runtimeValue, bool) {
	for current := frame; current != nil; current = current.parent {
		if value, exists := current.narrowed[name]; exists {
			return value, true
		}
		if value, exists := current.locals[name]; exists {
			return value, true
		}
	}
	return runtimeValue{}, false
}

func (frame *runtimeFrame) lookupPending(name string) (*runtimePending, bool) {
	for current := frame; current != nil; current = current.parent {
		if pending, exists := current.pending[name]; exists {
			return pending, true
		}
	}
	return nil, false
}

// assign writes the declared storage and retires every refinement of the name
// along the way, so a narrowed branch cannot swallow the write or keep reading
// the value the test proved.
func (frame *runtimeFrame) assign(name string, value runtimeValue) bool {
	assigned := false
	for current := frame; current != nil; current = current.parent {
		delete(current.narrowed, name)
		if _, exists := current.locals[name]; exists && !assigned {
			current.locals[name] = value
			assigned = true
		}
	}
	return assigned
}

func nullRuntimeValue() runtimeValue {
	return runtimeValue{typ: "null"}
}

func runtimeEqual(left, right runtimeValue) bool {
	// Optional comparison runs before the type check: T? compares with null,
	// with T, and with another T?, and only absence-versus-presence decides
	// the result before the payloads are looked at.
	if left.optional != nil || right.optional != nil {
		leftPresent, leftValue := runtimeOptionalParts(left)
		rightPresent, rightValue := runtimeOptionalParts(right)
		if leftPresent != rightPresent {
			return false
		}
		return !leftPresent || runtimeEqual(leftValue, rightValue)
	}
	if left.typ == "bytes" && right.typ == "bytes" {
		return bytes.Equal(left.scalar.([]byte), right.scalar.([]byte))
	}
	if left.typ != right.typ {
		return false
	}
	if left.result != nil || right.result != nil {
		if left.result == nil || right.result == nil {
			return false
		}
		return left.result.ok == right.result.ok && runtimeEqual(left.result.payload, right.result.payload)
	}
	if left.mapping != nil || right.mapping != nil {
		if left.mapping == nil || right.mapping == nil || len(left.mapping.entries) != len(right.mapping.entries) {
			return false
		}
		for index, leftEntry := range left.mapping.entries {
			rightEntry := right.mapping.entries[index]
			if leftEntry.canonical != rightEntry.canonical || !runtimeEqual(leftEntry.value, rightEntry.value) {
				return false
			}
		}
		return true
	}
	if strings.HasSuffix(left.typ, "[]") || strings.HasPrefix(left.typ, "(") {
		if len(left.elements) != len(right.elements) {
			return false
		}
		for index := range left.elements {
			if !runtimeEqual(left.elements[index], right.elements[index]) {
				return false
			}
		}
		return true
	}
	if left.fields != nil || right.fields != nil {
		if len(left.fields) != len(right.fields) {
			return false
		}
		for name, leftField := range left.fields {
			rightField, exists := right.fields[name]
			if !exists || !runtimeEqual(leftField, rightField) {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(left.scalar) == fmt.Sprint(right.scalar)
}

func formatRuntimeValue(value runtimeValue) string {
	// A present optional reads as its payload and an absent one as nothing, so
	// an absent optional main result prints no line at all.
	if value.optional != nil {
		if !value.optional.present {
			return ""
		}
		return formatRuntimeValue(value.optional.value)
	}
	switch value.typ {
	case "null":
		return ""
	case "bytes":
		return fmt.Sprintf("bytes[%d]", len(value.scalar.([]byte)))
	case "string":
		text, _ := value.scalar.(string)
		return text
	case "int":
		return strconv.FormatInt(value.scalar.(int64), 10)
	case "float":
		return strconv.FormatFloat(value.scalar.(float64), 'g', -1, 64)
	case "bool":
		return strconv.FormatBool(value.scalar.(bool))
	default:
		if value.buffer != nil {
			return "Buffer"
		}
		if value.mapping != nil {
			items := make([]string, len(value.mapping.entries))
			for index, entry := range value.mapping.entries {
				items[index] = formatRuntimeValue(entry.key) + ": " + formatRuntimeValue(entry.value)
			}
			return "map {" + strings.Join(items, ", ") + "}"
		}
		if strings.HasSuffix(value.typ, "[]") || strings.HasPrefix(value.typ, "(") {
			items := make([]string, 0, len(value.elements))
			for _, element := range value.elements {
				items = append(items, formatRuntimeValue(element))
			}
			open, close := "[", "]"
			if strings.HasPrefix(value.typ, "(") {
				open, close = "(", ")"
			}
			return open + strings.Join(items, ", ") + close
		}
		if value.result != nil {
			return value.result.label() + "(" + formatRuntimeValue(value.result.payload) + ")"
		}
		if value.iterable != nil {
			return formatRuntimeIterable(value)
		}
		return value.typ
	}
}

// formatRuntimeIterable renders a lazy sequence as its materialized elements,
// so `run` matches what the Go backend prints for the same value.
func formatRuntimeIterable(value runtimeValue) string {
	length, err := runtimeIterableLength(value)
	if err != nil {
		return value.typ
	}
	items := make([]string, 0, length)
	for index := range length {
		values, err := runtimeIterableValues(value, index)
		if err != nil {
			return value.typ
		}
		items = append(items, formatRuntimeValue(packRuntimeValues(values)))
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func runtimeError(pos position, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if pos.file == "" {
		return errors.New(message)
	}
	return fmt.Errorf("%s:%d:%d: %s", pos.file, pos.line, pos.column, message)
}
