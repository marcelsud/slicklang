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
}

type runtimeIterable struct {
	kind    string
	sources []runtimeValue
	start   int64
	end     int64
}

type runtimeFrame struct {
	function *functionDecl
	locals   map[string]runtimeValue
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
		frame.locals[param.name] = args[index]
	}
	if self != nil {
		frame.locals["self"] = *self
	}
	value, err := p.evalBlock(function.ast, frame)
	var returned *returnSignal
	if errors.As(err, &returned) {
		return returned.value, nil
	}
	return value, err
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
		frame.locals[node.name] = value
		return nullRuntimeValue(), nil
	case *assignmentStatement:
		value, err := p.evalExpression(node.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		if !frame.assign(node.name, value) {
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
		for _, element := range node.elements {
			value, err := p.evalExpression(element, frame)
			if err != nil {
				return runtimeValue{}, err
			}
			elements = append(elements, value)
		}
		typ := typeUnknown + "[]"
		if len(elements) > 0 {
			typ = elements[0].typ + "[]"
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
	for name := range class.fields {
		value.fields[name] = nullRuntimeValue()
	}
	for _, field := range node.fields {
		fieldValue, err := p.evalExpression(field.value, frame)
		if err != nil {
			return runtimeValue{}, err
		}
		value.fields[field.name] = fieldValue
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
		return p.evalBlock(node.thenBlock, frame.clone())
	}
	if node.elseBlock != nil {
		return p.evalBlock(node.elseBlock, frame.clone())
	}
	return nullRuntimeValue(), nil
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

func (frame *runtimeFrame) lookup(name string) (runtimeValue, bool) {
	for current := frame; current != nil; current = current.parent {
		if value, exists := current.locals[name]; exists {
			return value, true
		}
	}
	return runtimeValue{}, false
}

func (frame *runtimeFrame) assign(name string, value runtimeValue) bool {
	for current := frame; current != nil; current = current.parent {
		if _, exists := current.locals[name]; exists {
			current.locals[name] = value
			return true
		}
	}
	return false
}

func nullRuntimeValue() runtimeValue {
	return runtimeValue{typ: "null"}
}

func runtimeEqual(left, right runtimeValue) bool {
	if left.typ != right.typ {
		return false
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
		return value.typ
	}
}

func runtimeError(pos position, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if pos.file == "" {
		return errors.New(message)
	}
	return fmt.Errorf("%s:%d:%d: %s", pos.file, pos.line, pos.column, message)
}
