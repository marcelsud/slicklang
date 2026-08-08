package compiler

import (
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
	result   *runtimeResult
	optional *runtimeOptional
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
	function *functionDecl
	locals   map[string]runtimeValue
	narrowed map[string]runtimeValue
	parent   *runtimeFrame
}

type slickThrow struct {
	typ     string
	message string
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
	return p.callFunction(main, nil, nil)
}

func (p *program) callFunction(function *functionDecl, args []runtimeValue, self *runtimeValue) (runtimeValue, error) {
	if len(args) != len(function.params) {
		return runtimeValue{}, fmt.Errorf("%s expects %d arguments, found %d", function.qualified, len(function.params), len(args))
	}
	frame := &runtimeFrame{function: function, locals: make(map[string]runtimeValue, len(args)+1)}
	for index, param := range function.params {
		declared := p.resolveType(function.namespace, function.aliases, param.typ)
		frame.locals[param.name] = coerceRuntimeValue(args[index], declared)
	}
	if self != nil {
		frame.locals["self"] = *self
	}
	result := p.resolveType(function.namespace, function.aliases, function.result)
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

func (p *program) evalBlock(block *blockNode, frame *runtimeFrame) (runtimeValue, error) {
	last := nullRuntimeValue()
	if block == nil {
		return last, nil
	}
	for _, statement := range block.statements {
		value, err := p.evalStatement(statement, frame)
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
		return runtimeValue{}, &slickThrow{typ: value.typ, message: message}
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
		typ := typeUnknown + "[]"
		if elementType != "" {
			typ = elementType + "[]"
			for index := range elements {
				elements[index] = coerceRuntimeValue(elements[index], elementType)
			}
		}
		return runtimeValue{typ: typ, elements: elements}, nil
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
	case *matchExpression:
		return p.evalMatch(node, frame)
	default:
		return runtimeValue{}, fmt.Errorf("unsupported expression at %s:%d:%d", expression.expressionPos().file, expression.expressionPos().line, expression.expressionPos().column)
	}
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
			class := p.classes[receiver.typ]
			if class == nil {
				return runtimeValue{}, runtimeError(node.pos, "%s is not a class value", parts[0])
			}
			implementation := class.implementations[parts[1]]
			if implementation == nil {
				return runtimeValue{}, runtimeError(node.pos, "%s has no implemented method %s", class.name, parts[1])
			}
			return p.callFunction(implementation, args, &receiver)
		}
	}
	canonical := p.resolveNameIn(frame.function.namespace, frame.function.aliases, name.name)
	function := p.functions[canonical]
	if function == nil {
		return runtimeValue{}, runtimeError(node.pos, "unknown function %s", name.name)
	}
	return p.callFunction(function, args, nil)
}

func (p *program) evalBinary(node *binaryExpression, frame *runtimeFrame) (runtimeValue, error) {
	left, err := p.evalExpression(node.left, frame)
	if err != nil {
		return runtimeValue{}, err
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
		if node.binding != "" {
			armFrame.locals[node.binding] = runtimeValue{typ: thrown.typ, scalar: thrown.message, fields: make(map[string]runtimeValue)}
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
	return &runtimeFrame{function: frame.function, locals: make(map[string]runtimeValue), parent: frame}
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
	if left.typ != right.typ {
		return false
	}
	if left.result != nil || right.result != nil {
		if left.result == nil || right.result == nil {
			return false
		}
		return left.result.ok == right.result.ok && runtimeEqual(left.result.payload, right.result.payload)
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
