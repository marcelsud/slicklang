package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type jsonFailure struct {
	Operation string
	Path      string
	Message   string
}

func (f jsonFailure) Error() string {
	return f.Message
}

func jsonPathRoot() string { return "$" }

func jsonPathField(path, field string) string {
	return path + "." + field
}

func jsonPathIndex(path string, index int) string {
	return fmt.Sprintf("%s[%d]", path, index)
}

func (field fieldDecl) jsonWireName() string {
	if field.jsonName != "" {
		return field.jsonName
	}
	return field.name
}

func jsonFieldForWire(class *classDecl, wire string) (fieldDecl, bool) {
	for _, name := range sortedKeys(class.fields) {
		field := class.fields[name]
		if field.jsonWireName() == wire {
			return field, true
		}
	}
	return fieldDecl{}, false
}

func applyStdJSONName(p *program, target annotationTargetRef, annotation resolvedAnnotation) {
	if target.class == nil || target.fieldName == "" {
		p.add(annotation.authored.pos, diagnosticCodeAnnotationTarget, "annotation std.json.Name requires a class field")
		return
	}
	field, ok := target.class.fields[target.fieldName]
	if !ok {
		return
	}
	name, _ := annotation.values[0].value.(string)
	if name == "" {
		p.add(annotation.authored.pos, diagnosticCodeAnnotationArgument, "annotation std.json.Name requires a non-empty string")
		return
	}
	if !isPublic(field.name) {
		p.add(annotation.authored.pos, diagnosticCodeAnnotationTarget, "annotation std.json.Name can only target a public field")
		return
	}
	field.jsonName = name
	target.class.fields[target.fieldName] = field
}

func (p *program) checkJSONFieldNames() {
	for _, classes := range []map[string]*classDecl{p.genericClasses, p.classes} {
		for _, name := range sortedKeys(classes) {
			class := classes[name]
			p.checkingInstance(class.instanceOf != "", func() {
				p.checkClassJSONFieldNames(class)
			})
		}
	}
}

func (p *program) checkClassJSONFieldNames(class *classDecl) {
	named := false
	for _, fieldName := range sortedKeys(class.fields) {
		if class.fields[fieldName].jsonName != "" {
			named = true
			break
		}
	}
	if !named {
		return
	}
	supported := false
	if len(class.typeParams) > 0 {
		supported = p.jsonShapeSupported(class, make(map[string]bool))
	} else {
		supported = p.jsonTypeSupported(class.qualified, make(map[string]bool))
	}
	if !supported {
		for _, fieldName := range sortedKeys(class.fields) {
			field := class.fields[fieldName]
			if field.jsonName == "" {
				continue
			}
			p.reportJSONNameUnsupported(class, field)
		}
		return
	}
	owners := map[string][]string{}
	for _, fieldName := range sortedKeys(class.fields) {
		wire := class.fields[fieldName].jsonWireName()
		owners[wire] = append(owners[wire], fieldName)
	}
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		if field.jsonName == "" {
			continue
		}
		names := owners[field.jsonName]
		if len(names) < 2 {
			continue
		}
		other := names[0]
		if other == field.name {
			other = names[1]
		}
		p.add(field.pos, diagnosticCodeAnnotation, "JSON wire name %q is already used by field %s", field.jsonName, other)
	}
}

func (p *program) reportJSONNameUnsupported(class *classDecl, field fieldDecl) {
	for _, authored := range field.annotations {
		if len(authored.resolved) == 0 || authored.resolved[0].terminal == nil {
			continue
		}
		if authored.resolved[0].terminal.canonical != "std.json.Name" {
			continue
		}
		p.add(authored.pos, diagnosticCodeAnnotationTarget, "annotation std.json.Name requires a class accepted by std.json")
		return
	}
	p.add(field.pos, diagnosticCodeAnnotationTarget, "annotation std.json.Name requires a class accepted by std.json")
}

func substituteTypeParams(name string, params map[string]string) string {
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional:
		return optionalOf(substituteTypeParams(parsed.base, params))
	case typeKindArray:
		return arrayOf(substituteTypeParams(parsed.base, params))
	case typeKindCallable:
		parameters := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			parameters[index] = substituteTypeParams(arg, params)
		}
		throws := make([]string, len(parsed.throws))
		for index, thrown := range parsed.throws {
			throws[index] = substituteTypeParams(thrown, params)
		}
		return callableType(parameters, substituteTypeParams(parsed.base, params), throws, parsed.operations)
	case typeKindTuple:
		parts := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			parts[index] = substituteTypeParams(arg, params)
		}
		return "(" + strings.Join(parts, ",") + ")"
	case typeKindGeneric:
		parts := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			parts[index] = substituteTypeParams(arg, params)
		}
		return parsed.base + "<" + strings.Join(parts, ",") + ">"
	default:
		if replacement, ok := params[name]; ok {
			return replacement
		}
		return name
	}
}

func (p *program) jsonTypeSupported(name string, visiting map[string]bool) bool {
	if name == "" || name == typeUnknown || name == typeNever {
		return false
	}
	if name == "bytes" || isMapType(name) {
		return false
	}
	if isBuiltinType(name) {
		return true
	}
	if _, generic := p.genericTypeParams(name); generic {
		return false
	}
	if base, optional := optionalBase(name); optional {
		return p.jsonTypeSupported(base, visiting)
	}
	if element, isArray := arrayElementType(name); isArray {
		return p.jsonTypeSupported(element, visiting)
	}
	if _, _, ok := resultTypeArgs(name); ok {
		return false
	}
	if strings.HasPrefix(name, "Iterable<") {
		return false
	}
	if strings.HasPrefix(name, "(") {
		return false
	}
	// A concrete instantiation of a user-declared generic is an ordinary class
	// whose fields are already substituted; only an unresolved generic shape is
	// unsupported.
	if _, _, generic := genericType(name); generic && p.classes[name] == nil {
		return false
	}
	if p.interfaces[name] != nil {
		return false
	}
	class := p.classes[name]
	if class == nil {
		return false
	}
	if visiting[name] {
		return true
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		if !isPublic(field.name) {
			return false
		}
		fieldType := p.resolveType(class.namespace, class.aliases, field.typ)
		if !p.jsonTypeSupported(fieldType, visiting) {
			return false
		}
	}
	return true
}

func (p *program) jsonShapeSupported(class *classDecl, visiting map[string]bool) bool {
	if class == nil {
		return false
	}
	if visiting[class.qualified] {
		return true
	}
	visiting[class.qualified] = true
	defer delete(visiting, class.qualified)
	params := map[string]bool{}
	for _, param := range class.typeParams {
		params[param] = true
	}
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		if !isPublic(field.name) {
			return false
		}
		fieldType := p.resolveType(class.namespace, class.aliases, field.typ)
		if !p.jsonShapeTypeSupported(fieldType, params, visiting) {
			return false
		}
	}
	return true
}

func (p *program) jsonShapeTypeSupported(name string, params map[string]bool, visiting map[string]bool) bool {
	if params[name] {
		return true
	}
	if base, optional := optionalBase(name); optional {
		return p.jsonShapeTypeSupported(base, params, visiting)
	}
	if element, isArray := arrayElementType(name); isArray {
		return p.jsonShapeTypeSupported(element, params, visiting)
	}
	if root, args, generic := genericType(name); generic {
		if open := p.genericClasses[root]; open != nil {
			for _, arg := range args {
				if !p.jsonShapeTypeSupported(arg, params, visiting) {
					return false
				}
			}
			return p.jsonShapeSupported(open, visiting)
		}
	}
	return p.jsonTypeSupported(name, visiting)
}

func (p *program) jsonUnsupportedReason(name string, visiting map[string]bool) string {
	if name == "" || name == typeUnknown || name == typeNever {
		return "unknown type"
	}
	if name == "bytes" {
		return "bytes cannot be encoded or decoded as JSON"
	}
	if isMapType(name) {
		return "Map cannot be encoded or decoded as JSON"
	}
	if isBuiltinType(name) {
		return ""
	}
	if base, optional := optionalBase(name); optional {
		if reason := p.jsonUnsupportedReason(base, visiting); reason != "" {
			return reason
		}
		return ""
	}
	if element, isArray := arrayElementType(name); isArray {
		if reason := p.jsonUnsupportedReason(element, visiting); reason != "" {
			return reason
		}
		return ""
	}
	if _, _, ok := resultTypeArgs(name); ok {
		return "Result cannot be encoded or decoded as JSON"
	}
	if strings.HasPrefix(name, "Iterable<") {
		return "Iterable cannot be encoded or decoded as JSON"
	}
	if isCallableType(name) {
		return "callables cannot be encoded or decoded as JSON"
	}
	if strings.HasPrefix(name, "(") {
		return "tuples cannot be encoded or decoded as JSON"
	}
	if base, _, generic := genericType(name); generic && p.classes[name] == nil {
		if p.genericInterfaces[base] != nil {
			return "interfaces cannot be encoded or decoded as JSON"
		}
		return fmt.Sprintf("unknown generic type %s cannot be encoded or decoded as JSON", base)
	}
	if p.interfaces[name] != nil {
		return "interfaces cannot be encoded or decoded as JSON"
	}
	if params, generic := p.genericTypeParams(name); generic {
		return fmt.Sprintf("%s takes %d type arguments; JSON encodes one concrete instantiation, not an open generic declaration",
			displayName(name), len(params))
	}
	class := p.classes[name]
	if class == nil {
		return fmt.Sprintf("%s cannot be encoded or decoded as JSON", displayName(name))
	}
	if visiting[name] {
		return ""
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		if !isPublic(field.name) {
			return fmt.Sprintf("%s has private field %s", displayName(name), field.name)
		}
		fieldType := p.resolveType(class.namespace, class.aliases, field.typ)
		if reason := p.jsonUnsupportedReason(fieldType, visiting); reason != "" {
			return fmt.Sprintf("%s field %s: %s", displayName(name), field.name, reason)
		}
	}
	return ""
}

func runtimeJSONFailure(operation, path, message string) runtimeValue {
	return runtimeValue{
		typ: stdJsonFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Path":      {typ: "string", scalar: path},
			"Message":   {typ: "string", scalar: message},
		},
	}
}

func runtimeJSONResult(resultType string, ok bool, payload runtimeValue) runtimeValue {
	return runtimeValue{
		typ: resultType,
		result: &runtimeResult{
			ok:      ok,
			payload: payload,
		},
	}
}

func (p *program) runtimeJSONDecode(target, text string) runtimeValue {
	resultType := "Result<" + target + "," + stdJsonFailureName + ">"
	if !utf8.ValidString(text) {
		return runtimeJSONResult(resultType, false, runtimeJSONFailure("Decode", jsonPathRoot(), "input is not valid UTF-8"))
	}
	value, err := decodeJSONValue([]byte(text))
	if err != nil {
		failure, ok := err.(jsonFailure)
		if !ok {
			failure = jsonFailure{Operation: "Decode", Path: jsonPathRoot(), Message: err.Error()}
		}
		if failure.Operation == "" {
			failure.Operation = "Decode"
		}
		if failure.Path == "" {
			failure.Path = jsonPathRoot()
		}
		return runtimeJSONResult(resultType, false, runtimeJSONFailure(failure.Operation, failure.Path, failure.Message))
	}
	converted, err := p.convertJSONToRuntime(value, target, jsonPathRoot())
	if err != nil {
		failure := err.(jsonFailure)
		return runtimeJSONResult(resultType, false, runtimeJSONFailure(failure.Operation, failure.Path, failure.Message))
	}
	return runtimeJSONResult(resultType, true, converted)
}

func (p *program) runtimeJSONEncode(target string, value runtimeValue) runtimeValue {
	resultType := "Result<string," + stdJsonFailureName + ">"
	tree, err := p.convertRuntimeToJSON(value, target, jsonPathRoot())
	if err != nil {
		failure := err.(jsonFailure)
		return runtimeJSONResult(resultType, false, runtimeJSONFailure(failure.Operation, failure.Path, failure.Message))
	}
	encoded, err := json.Marshal(tree)
	if err != nil {
		return runtimeJSONResult(resultType, false, runtimeJSONFailure("Encode", jsonPathRoot(), "failed to encode JSON"))
	}
	return runtimeJSONResult(resultType, true, runtimeValue{typ: "string", scalar: string(encoded)})
}

type jsonValue struct {
	kind     jsonValueKind
	boolVal  bool
	number   json.Number
	string   string
	array    []jsonValue
	object   []jsonObjectField
	objectIX map[string]int
}

type jsonObjectField struct {
	key   string
	value jsonValue
}

type jsonValueKind int

const (
	jsonKindNull jsonValueKind = iota
	jsonKindBool
	jsonKindNumber
	jsonKindString
	jsonKindArray
	jsonKindObject
)

func decodeJSONValue(input []byte) (jsonValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, jsonPathRoot())
	if err != nil {
		return jsonValue{}, err
	}
	var extra json.Token
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return jsonValue{}, jsonFailure{Operation: "Decode", Path: jsonPathRoot(), Message: "input contains more than one JSON value"}
		}
		return jsonValue{}, jsonDecodeError(err, jsonPathRoot())
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder, path string) (jsonValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonValue{}, jsonDecodeError(err, path)
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '[':
			var elements []jsonValue
			for decoder.More() {
				element, err := readJSONValue(decoder, jsonPathIndex(path, len(elements)))
				if err != nil {
					return jsonValue{}, err
				}
				elements = append(elements, element)
			}
			closing, err := decoder.Token()
			if err != nil {
				return jsonValue{}, jsonDecodeError(err, path)
			}
			if delim, ok := closing.(json.Delim); !ok || delim != ']' {
				return jsonValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected end of array"}
			}
			return jsonValue{kind: jsonKindArray, array: elements}, nil
		case '{':
			fields := make([]jsonObjectField, 0)
			index := make(map[string]int)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return jsonValue{}, jsonDecodeError(err, path)
				}
				key, ok := keyToken.(string)
				if !ok {
					return jsonValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "object key must be a string"}
				}
				if _, exists := index[key]; exists {
					return jsonValue{}, jsonFailure{Operation: "Decode", Path: jsonPathField(path, key), Message: "duplicate object key"}
				}
				fieldPath := jsonPathField(path, key)
				value, err := readJSONValue(decoder, fieldPath)
				if err != nil {
					return jsonValue{}, err
				}
				index[key] = len(fields)
				fields = append(fields, jsonObjectField{key: key, value: value})
			}
			closing, err := decoder.Token()
			if err != nil {
				return jsonValue{}, jsonDecodeError(err, path)
			}
			if delim, ok := closing.(json.Delim); !ok || delim != '}' {
				return jsonValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected end of object"}
			}
			return jsonValue{kind: jsonKindObject, object: fields, objectIX: index}, nil
		default:
			return jsonValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "unexpected delimiter"}
		}
	case bool:
		return jsonValue{kind: jsonKindBool, boolVal: typed}, nil
	case json.Number:
		return jsonValue{kind: jsonKindNumber, number: typed}, nil
	case string:
		return jsonValue{kind: jsonKindString, string: typed}, nil
	case nil:
		return jsonValue{kind: jsonKindNull}, nil
	default:
		return jsonValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "unsupported JSON token"}
	}
}

func jsonDecodeError(err error, path string) error {
	if err == io.EOF {
		return jsonFailure{Operation: "Decode", Path: path, Message: "unexpected end of JSON input"}
	}
	message := err.Error()
	message = strings.TrimPrefix(message, "json: ")
	if syntax, ok := err.(*json.SyntaxError); ok {
		message = syntax.Error()
	}
	if message == "" {
		message = "invalid JSON"
	}
	return jsonFailure{Operation: "Decode", Path: path, Message: message}
}

func (p *program) convertJSONToRuntime(value jsonValue, target, path string) (runtimeValue, error) {
	if base, optional := optionalBase(target); optional {
		if value.kind == jsonKindNull {
			return runtimeValue{typ: target, optional: &runtimeOptional{}}, nil
		}
		inner, err := p.convertJSONToRuntime(value, base, path)
		if err != nil {
			return runtimeValue{}, err
		}
		return runtimeValue{typ: target, optional: &runtimeOptional{present: true, value: inner}}, nil
	}
	if element, isArray := arrayElementType(target); isArray {
		if value.kind != jsonKindArray {
			return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON array"}
		}
		elements := make([]runtimeValue, len(value.array))
		for index, item := range value.array {
			converted, err := p.convertJSONToRuntime(item, element, jsonPathIndex(path, index))
			if err != nil {
				return runtimeValue{}, err
			}
			elements[index] = converted
		}
		return runtimeValue{typ: target, elements: elements}, nil
	}
	switch target {
	case "null":
		if value.kind != jsonKindNull {
			return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON null"}
		}
		return nullRuntimeValue(), nil
	case "bool":
		if value.kind != jsonKindBool {
			return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON boolean"}
		}
		return runtimeValue{typ: "bool", scalar: value.boolVal}, nil
	case "string":
		if value.kind != jsonKindString {
			return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON string"}
		}
		return runtimeValue{typ: "string", scalar: value.string}, nil
	case "int":
		number, err := parseJSONInt(value, path)
		if err != nil {
			return runtimeValue{}, err
		}
		return runtimeValue{typ: "int", scalar: number}, nil
	case "float":
		number, err := parseJSONFloat(value, path)
		if err != nil {
			return runtimeValue{}, err
		}
		return runtimeValue{typ: "float", scalar: number}, nil
	}
	class := p.classes[target]
	if class == nil {
		return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "unsupported JSON target type"}
	}
	if value.kind != jsonKindObject {
		return runtimeValue{}, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON object"}
	}
	result := runtimeValue{typ: target, fields: make(map[string]runtimeValue, len(class.fields))}
	seen := make(map[string]bool, len(value.object))
	for _, field := range value.object {
		decl, ok := jsonFieldForWire(class, field.key)
		if !ok {
			return runtimeValue{}, jsonFailure{Operation: "Decode", Path: jsonPathField(path, field.key), Message: "unknown field"}
		}
		fieldType := p.resolveType(class.namespace, class.aliases, decl.typ)
		converted, err := p.convertJSONToRuntime(field.value, fieldType, jsonPathField(path, field.key))
		if err != nil {
			return runtimeValue{}, err
		}
		result.fields[decl.name] = converted
		seen[decl.name] = true
	}
	for _, fieldName := range sortedKeys(class.fields) {
		if seen[fieldName] {
			continue
		}
		field := class.fields[fieldName]
		fieldType := p.resolveType(class.namespace, class.aliases, field.typ)
		if _, optional := optionalBase(fieldType); optional {
			result.fields[fieldName] = runtimeValue{typ: fieldType, optional: &runtimeOptional{}}
			continue
		}
		return runtimeValue{}, jsonFailure{Operation: "Decode", Path: jsonPathField(path, field.jsonWireName()), Message: "missing required field"}
	}
	return result, nil
}

func parseJSONInt(value jsonValue, path string) (int64, error) {
	if value.kind != jsonKindNumber {
		return 0, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON integer"}
	}
	text := string(value.number)
	if strings.ContainsAny(text, ".eE") {
		return 0, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON integer without fraction or exponent"}
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, jsonFailure{Operation: "Decode", Path: path, Message: "integer out of int64 range"}
	}
	return number, nil
}

func parseJSONFloat(value jsonValue, path string) (float64, error) {
	if value.kind != jsonKindNumber {
		return 0, jsonFailure{Operation: "Decode", Path: path, Message: "expected JSON number"}
	}
	number, err := value.number.Float64()
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return 0, jsonFailure{Operation: "Decode", Path: path, Message: "number out of float64 range"}
	}
	return number, nil
}

func (p *program) convertRuntimeToJSON(value runtimeValue, target, path string) (any, error) {
	value = coerceRuntimeValue(value, target)
	if base, optional := optionalBase(target); optional {
		if value.optional == nil || !value.optional.present {
			return nil, nil
		}
		return p.convertRuntimeToJSON(value.optional.value, base, path)
	}
	if element, isArray := arrayElementType(target); isArray {
		items := make([]any, len(value.elements))
		for index, item := range value.elements {
			converted, err := p.convertRuntimeToJSON(item, element, jsonPathIndex(path, index))
			if err != nil {
				return nil, err
			}
			items[index] = converted
		}
		return items, nil
	}
	switch target {
	case "null":
		return nil, nil
	case "bool":
		return value.scalar.(bool), nil
	case "string":
		return value.scalar.(string), nil
	case "int":
		return value.scalar.(int64), nil
	case "float":
		number := value.scalar.(float64)
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, jsonFailure{Operation: "Encode", Path: path, Message: "non-finite float cannot be encoded as JSON"}
		}
		return number, nil
	}
	class := p.classes[target]
	if class == nil {
		return nil, jsonFailure{Operation: "Encode", Path: path, Message: "unsupported JSON source type"}
	}
	names := sortedKeys(class.fields)
	object := make(map[string]any, len(names))
	for _, fieldName := range names {
		field := class.fields[fieldName]
		fieldType := p.resolveType(class.namespace, class.aliases, field.typ)
		wire := field.jsonWireName()
		fieldValue, ok := value.fields[fieldName]
		if !ok {
			fieldValue = coerceRuntimeValue(nullRuntimeValue(), fieldType)
		}
		if base, optional := optionalBase(fieldType); optional {
			fieldValue = coerceRuntimeValue(fieldValue, fieldType)
			if fieldValue.optional == nil || !fieldValue.optional.present {
				continue
			}
			converted, err := p.convertRuntimeToJSON(fieldValue.optional.value, base, jsonPathField(path, wire))
			if err != nil {
				return nil, err
			}
			object[wire] = converted
			continue
		}
		converted, err := p.convertRuntimeToJSON(fieldValue, fieldType, jsonPathField(path, wire))
		if err != nil {
			return nil, err
		}
		object[wire] = converted
	}
	return object, nil
}

func goJSONHelperName(operation, typ string) string {
	return fmt.Sprintf("slickJSON%s_%x", operation, []byte(typ))
}

func (g *goGenerator) collectJSONCodecs() []jsonCodecNeed {
	needs := map[string]jsonCodecNeed{}
	var walk func(expression expressionNode)
	var walkBlock func(block *blockNode)
	walk = func(expression expressionNode) {
		if expression == nil {
			return
		}
		switch node := expression.(type) {
		case *callExpression:
			if node.resolvedNative == nativeStdJsonDecode || node.resolvedNative == nativeStdJsonEncode {
				if len(node.resolvedTypeArgs) == 1 {
					key := string(node.resolvedNative) + "\x00" + node.resolvedTypeArgs[0]
					needs[key] = jsonCodecNeed{
						operation: string(node.resolvedNative),
						typ:       node.resolvedTypeArgs[0],
					}
				}
			}
			for _, arg := range node.args {
				walk(arg)
			}
		case *tupleExpression:
			for _, element := range node.elements {
				walk(element)
			}
		case *arrayExpression:
			for _, element := range node.elements {
				walk(element)
			}
		case *rangeExpression:
			walk(node.start)
			walk(node.end)
		case *objectExpression:
			for _, field := range node.fields {
				walk(field.value)
			}
		case *unaryExpression:
			walk(node.value)
		case *binaryExpression:
			walk(node.left)
			walk(node.right)
		case *ifExpression:
			walk(node.condition)
			walkBlock(node.thenBlock)
			walkBlock(node.elseBlock)
		case *catchExpression:
			walk(node.value)
			for _, arm := range node.arms {
				walk(arm.value)
			}
		case *resultExpression:
			walk(node.value)
		case *propagateExpression:
			walk(node.value)
		case *usingExpression:
			walk(node.initializer)
			walkBlock(node.body)
		case *matchExpression:
			walk(node.value)
			for _, arm := range node.arms {
				walk(arm.value)
			}
		}
	}
	walkBlock = func(block *blockNode) {
		if block == nil {
			return
		}
		for _, statement := range block.statements {
			switch node := statement.(type) {
			case *letStatement:
				walk(node.value)
			case *asyncLetStatement:
				walk(node.call)
			case *assignmentStatement:
				walk(node.value)
			case *forStatement:
				walk(node.iterable)
				walkBlock(node.body)
			case *throwStatement:
				walk(node.value)
			case *returnStatement:
				walk(node.value)
			case *expressionStatement:
				walk(node.value)
			}
		}
	}
	for _, name := range sortedKeys(g.program.functions) {
		function := g.program.functions[name]
		if function.native != "" || function.ast == nil {
			continue
		}
		walkBlock(function.ast)
	}
	for _, className := range sortedKeys(g.program.classes) {
		class := g.program.classes[className]
		for _, methodName := range sortedKeys(class.implementations) {
			implementation := class.implementations[methodName]
			if implementation.ast == nil {
				continue
			}
			walkBlock(implementation.ast)
		}
	}
	keys := make([]string, 0, len(needs))
	for key := range needs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]jsonCodecNeed, 0, len(keys))
	for _, key := range keys {
		result = append(result, needs[key])
	}
	return result
}

type jsonCodecNeed struct {
	operation string
	typ       string
}

func (g *goGenerator) usesJSON() bool {
	return len(g.collectJSONCodecs()) > 0
}

func (g *goGenerator) emitJSONSupport() error {
	needs := g.collectJSONCodecs()
	if len(needs) == 0 {
		return nil
	}
	g.emitJSONRuntimeSupport()
	emitted := map[string]bool{}
	var ensure func(typ string) error
	ensure = func(typ string) error {
		if emitted[typ] {
			return nil
		}
		emitted[typ] = true
		if base, optional := optionalBase(typ); optional {
			return ensure(base)
		}
		if element, isArray := arrayElementType(typ); isArray {
			return ensure(element)
		}
		if isBuiltinType(typ) {
			return nil
		}
		class := g.program.classes[typ]
		if class == nil {
			return fmt.Errorf("JSON codec requested for unsupported type %s", typ)
		}
		for _, fieldName := range sortedKeys(class.fields) {
			field := class.fields[fieldName]
			fieldType, err := g.declaredType(class.namespace, class.aliases, field.typ)
			if err != nil {
				return err
			}
			if err := ensure(fieldType); err != nil {
				return err
			}
		}
		return nil
	}
	for _, need := range needs {
		if err := ensure(need.typ); err != nil {
			return err
		}
	}
	// Emit helpers in sorted canonical type order, decode then encode.
	types := make([]string, 0, len(emitted))
	for typ := range emitted {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		if err := g.emitJSONTypeHelpers(typ); err != nil {
			return err
		}
	}
	for _, need := range needs {
		if err := g.emitJSONOperationHelper(need); err != nil {
			return err
		}
	}
	return nil
}

func (g *goGenerator) emitJSONRuntimeSupport() {
	g.line("type slickJSONValue struct {")
	g.line("kind int")
	g.line("boolVal bool")
	g.line("number json.Number")
	g.line("text string")
	g.line("array []slickJSONValue")
	g.line("object []slickJSONField")
	g.line("objectIX map[string]int")
	g.line("}")
	g.line("type slickJSONField struct { key string; value slickJSONValue }")
	g.line("func slickJSONFail(operation, path, message string) *%s {", goClassName(stdJsonFailureName))
	g.line("return &%s{%s: operation, %s: path, %s: message}",
		goClassName(stdJsonFailureName),
		goFieldName("Operation"),
		goFieldName("Path"),
		goFieldName("Message"),
	)
	g.line("}")
	g.line("func slickJSONDecodeValue(input []byte) (slickJSONValue, *%s) {", goClassName(stdJsonFailureName))
	g.line("decoder := json.NewDecoder(bytes.NewReader(input))")
	g.line("decoder.UseNumber()")
	g.line("value, failure := slickJSONReadValue(decoder, \"$\")")
	g.line("if failure != nil { return slickJSONValue{}, failure }")
	g.line("var extra json.Token")
	g.line("if err := decoder.Decode(&extra); err != io.EOF {")
	g.line("if err == nil { return slickJSONValue{}, slickJSONFail(\"Decode\", \"$\", \"input contains more than one JSON value\") }")
	g.line("return slickJSONValue{}, slickJSONDecodeError(err, \"$\")")
	g.line("}")
	g.line("return value, nil")
	g.line("}")
	g.line("func slickJSONReadValue(decoder *json.Decoder, path string) (slickJSONValue, *%s) {", goClassName(stdJsonFailureName))
	g.line("token, err := decoder.Token()")
	g.line("if err != nil { return slickJSONValue{}, slickJSONDecodeError(err, path) }")
	g.line("switch typed := token.(type) {")
	g.line("case json.Delim:")
	g.line("switch typed {")
	g.line("case '[':")
	g.line("var elements []slickJSONValue")
	g.line("for decoder.More() {")
	g.line("element, failure := slickJSONReadValue(decoder, fmt.Sprintf(\"%%s[%%d]\", path, len(elements)))")
	g.line("if failure != nil { return slickJSONValue{}, failure }")
	g.line("elements = append(elements, element)")
	g.line("}")
	g.line("closing, err := decoder.Token()")
	g.line("if err != nil { return slickJSONValue{}, slickJSONDecodeError(err, path) }")
	g.line("if delim, ok := closing.(json.Delim); !ok || delim != ']' {")
	g.line("return slickJSONValue{}, slickJSONFail(\"Decode\", path, \"expected end of array\")")
	g.line("}")
	g.line("return slickJSONValue{kind: 4, array: elements}, nil")
	g.line("case '{':")
	g.line("fields := make([]slickJSONField, 0)")
	g.line("index := make(map[string]int)")
	g.line("for decoder.More() {")
	g.line("keyToken, err := decoder.Token()")
	g.line("if err != nil { return slickJSONValue{}, slickJSONDecodeError(err, path) }")
	g.line("key, ok := keyToken.(string)")
	g.line("if !ok { return slickJSONValue{}, slickJSONFail(\"Decode\", path, \"object key must be a string\") }")
	g.line("if _, exists := index[key]; exists {")
	g.line("return slickJSONValue{}, slickJSONFail(\"Decode\", path+\".\"+key, \"duplicate object key\")")
	g.line("}")
	g.line("value, failure := slickJSONReadValue(decoder, path+\".\"+key)")
	g.line("if failure != nil { return slickJSONValue{}, failure }")
	g.line("index[key] = len(fields)")
	g.line("fields = append(fields, slickJSONField{key: key, value: value})")
	g.line("}")
	g.line("closing, err := decoder.Token()")
	g.line("if err != nil { return slickJSONValue{}, slickJSONDecodeError(err, path) }")
	g.line("if delim, ok := closing.(json.Delim); !ok || delim != '}' {")
	g.line("return slickJSONValue{}, slickJSONFail(\"Decode\", path, \"expected end of object\")")
	g.line("}")
	g.line("return slickJSONValue{kind: 5, object: fields, objectIX: index}, nil")
	g.line("default:")
	g.line("return slickJSONValue{}, slickJSONFail(\"Decode\", path, \"unexpected delimiter\")")
	g.line("}")
	g.line("case bool:")
	g.line("return slickJSONValue{kind: 1, boolVal: typed}, nil")
	g.line("case json.Number:")
	g.line("return slickJSONValue{kind: 2, number: typed}, nil")
	g.line("case string:")
	g.line("return slickJSONValue{kind: 3, text: typed}, nil")
	g.line("case nil:")
	g.line("return slickJSONValue{kind: 0}, nil")
	g.line("default:")
	g.line("return slickJSONValue{}, slickJSONFail(\"Decode\", path, \"unsupported JSON token\")")
	g.line("}")
	g.line("}")
	g.line("func slickJSONDecodeError(err error, path string) *%s {", goClassName(stdJsonFailureName))
	g.line("if err == io.EOF { return slickJSONFail(\"Decode\", path, \"unexpected end of JSON input\") }")
	g.line("message := err.Error()")
	g.line("if strings.HasPrefix(message, \"json: \") { message = strings.TrimPrefix(message, \"json: \") }")
	g.line("if message == \"\" { message = \"invalid JSON\" }")
	g.line("return slickJSONFail(\"Decode\", path, message)")
	g.line("}")
	g.line("func slickJSONParseInt(value slickJSONValue, path string) (int64, *%s) {", goClassName(stdJsonFailureName))
	g.line("if value.kind != 2 { return 0, slickJSONFail(\"Decode\", path, \"expected JSON integer\") }")
	g.line("text := string(value.number)")
	g.line("if strings.ContainsAny(text, \".eE\") { return 0, slickJSONFail(\"Decode\", path, \"expected JSON integer without fraction or exponent\") }")
	g.line("number, err := strconv.ParseInt(text, 10, 64)")
	g.line("if err != nil { return 0, slickJSONFail(\"Decode\", path, \"integer out of int64 range\") }")
	g.line("return number, nil")
	g.line("}")
	g.line("func slickJSONParseFloat(value slickJSONValue, path string) (float64, *%s) {", goClassName(stdJsonFailureName))
	g.line("if value.kind != 2 { return 0, slickJSONFail(\"Decode\", path, \"expected JSON number\") }")
	g.line("number, err := value.number.Float64()")
	g.line("if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {")
	g.line("return 0, slickJSONFail(\"Decode\", path, \"number out of float64 range\")")
	g.line("}")
	g.line("return number, nil")
	g.line("}")
	g.line("")
}

func (g *goGenerator) emitJSONTypeHelpers(typ string) error {
	decodeName := goJSONHelperName("DecodeInto", typ)
	encodeName := goJSONHelperName("EncodeFrom", typ)
	goTyp := g.goType(typ)
	g.line("func %s(value slickJSONValue, path string) (%s, *%s) {", decodeName, goTyp, goClassName(stdJsonFailureName))
	if err := g.emitJSONDecodeBody(typ); err != nil {
		return err
	}
	g.line("}")
	g.line("")
	g.line("func %s(value %s, path string) (any, *%s) {", encodeName, goTyp, goClassName(stdJsonFailureName))
	if err := g.emitJSONEncodeBody(typ); err != nil {
		return err
	}
	g.line("}")
	g.line("")
	return nil
}

func (g *goGenerator) emitJSONDecodeBody(typ string) error {
	if base, optional := optionalBase(typ); optional {
		g.line("if value.kind == 0 { return slickNone[%s](), nil }", g.goType(base))
		g.line("inner, failure := %s(value, path)", goJSONHelperName("DecodeInto", base))
		g.line("if failure != nil { return slickNone[%s](), failure }", g.goType(base))
		g.line("return slickSome(inner), nil")
		return nil
	}
	if element, isArray := arrayElementType(typ); isArray {
		g.line("if value.kind != 4 { return nil, slickJSONFail(\"Decode\", path, \"expected JSON array\") }")
		g.line("elements := make([]%s, len(value.array))", g.goType(element))
		g.line("for index, item := range value.array {")
		g.line("converted, failure := %s(item, fmt.Sprintf(\"%%s[%%d]\", path, index))", goJSONHelperName("DecodeInto", element))
		g.line("if failure != nil { return nil, failure }")
		g.line("elements[index] = converted")
		g.line("}")
		g.line("return elements, nil")
		return nil
	}
	switch typ {
	case "null":
		g.line("if value.kind != 0 { return struct{}{}, slickJSONFail(\"Decode\", path, \"expected JSON null\") }")
		g.line("return struct{}{}, nil")
		return nil
	case "bool":
		g.line("if value.kind != 1 { return false, slickJSONFail(\"Decode\", path, \"expected JSON boolean\") }")
		g.line("return value.boolVal, nil")
		return nil
	case "string":
		g.line("if value.kind != 3 { return \"\", slickJSONFail(\"Decode\", path, \"expected JSON string\") }")
		g.line("return value.text, nil")
		return nil
	case "int":
		g.line("return slickJSONParseInt(value, path)")
		return nil
	case "float":
		g.line("return slickJSONParseFloat(value, path)")
		return nil
	}
	class := g.program.classes[typ]
	if class == nil {
		return fmt.Errorf("cannot emit JSON decode helper for %s", typ)
	}
	receiver := g.goType(typ)
	g.line("if value.kind != 5 { return %s, slickJSONFail(\"Decode\", path, \"expected JSON object\") }", g.zero(typ))
	g.line("result := %s{}", strings.TrimPrefix(receiver, "*"))
	if strings.HasPrefix(receiver, "*") {
		g.line("out := &result")
	} else {
		g.line("out := result")
	}
	g.line("seen := map[string]bool{}")
	g.line("for _, field := range value.object {")
	g.line("switch field.key {")
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		fieldType, err := g.declaredType(class.namespace, class.aliases, field.typ)
		if err != nil {
			return err
		}
		wire := field.jsonWireName()
		g.line("case %q:", wire)
		g.line("converted, failure := %s(field.value, path+\".\"+%q)", goJSONHelperName("DecodeInto", fieldType), wire)
		g.line("if failure != nil { return %s, failure }", g.zero(typ))
		g.line("out.%s = converted", goFieldName(fieldName))
		g.line("seen[%q] = true", fieldName)
	}
	g.line("default:")
	g.line("return %s, slickJSONFail(\"Decode\", path+\".\"+field.key, \"unknown field\")", g.zero(typ))
	g.line("}")
	g.line("}")
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		fieldType, err := g.declaredType(class.namespace, class.aliases, field.typ)
		if err != nil {
			return err
		}
		if _, optional := optionalBase(fieldType); optional {
			continue
		}
		g.line("if !seen[%q] { return %s, slickJSONFail(\"Decode\", path+\".\"+%q, \"missing required field\") }", fieldName, g.zero(typ), field.jsonWireName())
	}
	if strings.HasPrefix(receiver, "*") {
		g.line("return out, nil")
	} else {
		g.line("return out, nil")
	}
	return nil
}

func (g *goGenerator) emitJSONEncodeBody(typ string) error {
	if base, optional := optionalBase(typ); optional {
		g.line("if !value.present { return nil, nil }")
		g.line("return %s(value.value, path)", goJSONHelperName("EncodeFrom", base))
		return nil
	}
	if element, isArray := arrayElementType(typ); isArray {
		g.line("items := make([]any, len(value))")
		g.line("for index, item := range value {")
		g.line("converted, failure := %s(item, fmt.Sprintf(\"%%s[%%d]\", path, index))", goJSONHelperName("EncodeFrom", element))
		g.line("if failure != nil { return nil, failure }")
		g.line("items[index] = converted")
		g.line("}")
		g.line("return items, nil")
		return nil
	}
	switch typ {
	case "null":
		g.line("return nil, nil")
		return nil
	case "bool", "string", "int":
		g.line("return value, nil")
		return nil
	case "float":
		g.line("if math.IsInf(value, 0) || math.IsNaN(value) {")
		g.line("return nil, slickJSONFail(\"Encode\", path, \"non-finite float cannot be encoded as JSON\")")
		g.line("}")
		g.line("return value, nil")
		return nil
	}
	class := g.program.classes[typ]
	if class == nil {
		return fmt.Errorf("cannot emit JSON encode helper for %s", typ)
	}
	receiver := "value"
	if class.isError {
		// error classes are pointers in Go.
		g.line("if value == nil { return map[string]any{}, nil }")
	}
	g.line("object := map[string]any{}")
	for _, fieldName := range sortedKeys(class.fields) {
		field := class.fields[fieldName]
		fieldType, err := g.declaredType(class.namespace, class.aliases, field.typ)
		if err != nil {
			return err
		}
		wire := field.jsonWireName()
		fieldExpr := receiver + "." + goFieldName(fieldName)
		if _, optional := optionalBase(fieldType); optional {
			base, _ := optionalBase(fieldType)
			g.line("if %s.present {", fieldExpr)
			g.line("converted, failure := %s(%s.value, path+\".\"+%q)", goJSONHelperName("EncodeFrom", base), fieldExpr, wire)
			g.line("if failure != nil { return nil, failure }")
			g.line("object[%q] = converted", wire)
			g.line("}")
			continue
		}
		g.line("{")
		g.line("converted, failure := %s(%s, path+\".\"+%q)", goJSONHelperName("EncodeFrom", fieldType), fieldExpr, wire)
		g.line("if failure != nil { return nil, failure }")
		g.line("object[%q] = converted", wire)
		g.line("}")
	}
	g.line("return object, nil")
	return nil
}

func (g *goGenerator) emitJSONOperationHelper(need jsonCodecNeed) error {
	switch need.operation {
	case string(nativeStdJsonDecode):
		resultType := "Result<" + need.typ + "," + stdJsonFailureName + ">"
		g.line("func %s(text string) (%s, error) {", goJSONHelperName("Decode", need.typ), g.goType(resultType))
		g.line("if !utf8.ValidString(text) {")
		g.line("return %s{failure: slickJSONFail(\"Decode\", \"$\", \"input is not valid UTF-8\")}, nil", g.goType(resultType))
		g.line("}")
		g.line("value, failure := slickJSONDecodeValue([]byte(text))")
		g.line("if failure != nil { return %s{failure: failure}, nil }", g.goType(resultType))
		g.line("converted, failure := %s(value, \"$\")", goJSONHelperName("DecodeInto", need.typ))
		g.line("if failure != nil { return %s{failure: failure}, nil }", g.goType(resultType))
		g.line("return %s{ok: true, value: converted}, nil", g.goType(resultType))
		g.line("}")
		g.line("")
		return nil
	case string(nativeStdJsonEncode):
		resultType := "Result<string," + stdJsonFailureName + ">"
		g.line("func %s(value %s) (%s, error) {", goJSONHelperName("Encode", need.typ), g.goType(need.typ), g.goType(resultType))
		g.line("tree, failure := %s(value, \"$\")", goJSONHelperName("EncodeFrom", need.typ))
		g.line("if failure != nil { return %s{failure: failure}, nil }", g.goType(resultType))
		g.line("encoded, err := json.Marshal(tree)")
		g.line("if err != nil { return %s{failure: slickJSONFail(\"Encode\", \"$\", \"failed to encode JSON\")}, nil }", g.goType(resultType))
		g.line("return %s{ok: true, value: string(encoded)}, nil", g.goType(resultType))
		g.line("}")
		g.line("")
		return nil
	default:
		return fmt.Errorf("unknown JSON operation %s", need.operation)
	}
}
