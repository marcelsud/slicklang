package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type coreLocation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type coreProgram struct {
	EvaluationOrder    string          `json:"evaluation_order"`
	CleanupSuppression string          `json:"cleanup_suppression"`
	RuntimeFamilies    []string        `json:"runtime_families"`
	Classes            []coreClass     `json:"classes"`
	Interfaces         []coreInterface `json:"interfaces"`
	Unions             []coreUnion     `json:"unions"`
	Constants          []coreConstant  `json:"constants"`
	Functions          []coreFunction  `json:"functions"`
}

type coreBinding struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type coreField struct {
	Name     string       `json:"name"`
	JSONName string       `json:"json_name,omitempty"`
	Type     string       `json:"type"`
	Location coreLocation `json:"location"`
}

type coreSignature struct {
	ID             string             `json:"id"`
	Parameters     []coreBinding      `json:"parameters"`
	Result         string             `json:"result"`
	Throws         []string           `json:"throws"`
	Effects        []string           `json:"effects"`
	Operation      runtimeOperationID `json:"operation,omitempty"`
	Implementation string             `json:"implementation,omitempty"`
	Location       coreLocation       `json:"location"`
}

type coreClass struct {
	ID         string          `json:"id"`
	Error      bool            `json:"error"`
	Resource   bool            `json:"resource"`
	Fields     []coreField     `json:"fields"`
	Methods    []coreSignature `json:"methods"`
	Interfaces []string        `json:"interfaces"`
	Location   coreLocation    `json:"location"`
}

type coreInterface struct {
	ID       string          `json:"id"`
	Methods  []coreSignature `json:"methods"`
	Location coreLocation    `json:"location"`
}

type coreVariant struct {
	ID       string       `json:"id"`
	Tag      int          `json:"tag"`
	Fields   []coreField  `json:"fields"`
	Location coreLocation `json:"location"`
}

type coreUnion struct {
	ID       string        `json:"id"`
	Variants []coreVariant `json:"variants"`
	Location coreLocation  `json:"location"`
}

type coreLiteral struct {
	Kind    string  `json:"kind"`
	Boolean bool    `json:"boolean"`
	Integer int64   `json:"integer"`
	Float   float64 `json:"float"`
	Text    string  `json:"text"`
	Variant string  `json:"variant"`
}

type coreConstant struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Value    coreLiteral  `json:"value"`
	Location coreLocation `json:"location"`
}

type coreFunction struct {
	ID         string        `json:"id"`
	Receiver   string        `json:"receiver,omitempty"`
	Parameters []coreBinding `json:"parameters"`
	Result     string        `json:"result"`
	Throws     []string      `json:"throws"`
	Effects    []string      `json:"effects"`
	Body       coreBlock     `json:"body"`
	Location   coreLocation  `json:"location"`
}

type coreBlock struct {
	Statements       []coreStatement `json:"statements"`
	Result           string          `json:"result"`
	StorageResult    string          `json:"storage_result,omitempty"`
	ResultConversion string          `json:"result_conversion,omitempty"`
	StructuredTasks  bool            `json:"structured_tasks"`
	TaskExitPolicy   string          `json:"task_exit_policy,omitempty"`
	Location         coreLocation    `json:"location"`
}

type coreStatement struct {
	Kind     string          `json:"kind"`
	Bindings []coreBinding   `json:"bindings,omitempty"`
	Target   string          `json:"target,omitempty"`
	Value    *coreExpression `json:"value,omitempty"`
	Body     *coreBlock      `json:"body,omitempty"`
	Location coreLocation    `json:"location"`
}

type coreMapEntry struct {
	Key   coreExpression `json:"key"`
	Value coreExpression `json:"value"`
}

type coreFieldValue struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Declaration string         `json:"declaration"`
	Value       coreExpression `json:"value"`
	Conversion  string         `json:"conversion,omitempty"`
}

type coreCapture struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type coreArm struct {
	Pattern     string         `json:"pattern"`
	Variant     string         `json:"variant,omitempty"`
	Bindings    []coreBinding  `json:"bindings,omitempty"`
	Value       coreExpression `json:"value"`
	StorageType string         `json:"storage_type,omitempty"`
	Conversion  string         `json:"conversion,omitempty"`
	Location    coreLocation   `json:"location"`
}

type coreTemplatePart struct {
	Text            string `json:"text,omitempty"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"`
	Declaration     string `json:"declaration,omitempty"`
	ReadStorageType string `json:"read_storage_type,omitempty"`
	ReadConversion  string `json:"read_conversion,omitempty"`
}
type coreCleanup struct {
	Operation   runtimeOperationID `json:"operation"`
	Suppression string             `json:"suppression"`
}

type coreExpression struct {
	Kind            string             `json:"kind"`
	Type            string             `json:"type"`
	Name            string             `json:"name,omitempty"`
	Operator        string             `json:"operator,omitempty"`
	Declaration     string             `json:"declaration,omitempty"`
	Operation       runtimeOperationID `json:"operation,omitempty"`
	ReceiverType    string             `json:"receiver_type,omitempty"`
	Receiver        *coreExpression    `json:"receiver,omitempty"`
	StorageType     string             `json:"storage_type,omitempty"`
	Conversion      string             `json:"conversion,omitempty"`
	ReadStorageType string             `json:"read_storage_type,omitempty"`
	ReadConversion  string             `json:"read_conversion,omitempty"`
	Literal         *coreLiteral       `json:"literal,omitempty"`
	Elements        []coreExpression   `json:"elements,omitempty"`
	Entries         []coreMapEntry     `json:"entries,omitempty"`
	Fields          []coreFieldValue   `json:"fields,omitempty"`
	Arguments       []coreExpression   `json:"arguments,omitempty"`
	Template        []coreTemplatePart `json:"template,omitempty"`
	Captures        []coreCapture      `json:"captures,omitempty"`
	Parameters      []coreBinding      `json:"parameters,omitempty"`
	Throws          []string           `json:"throws,omitempty"`
	Effects         []string           `json:"effects,omitempty"`
	Bindings        []coreBinding      `json:"bindings,omitempty"`
	Value           *coreExpression    `json:"value,omitempty"`
	Left            *coreExpression    `json:"left,omitempty"`
	Right           *coreExpression    `json:"right,omitempty"`
	Body            *coreBlock         `json:"body,omitempty"`
	Alternate       *coreBlock         `json:"alternate,omitempty"`
	Arms            []coreArm          `json:"arms,omitempty"`
	ShortCircuit    bool               `json:"short_circuit,omitempty"`
	ResultVariant   string             `json:"result_variant,omitempty"`
	Cleanup         *coreCleanup       `json:"cleanup,omitempty"`
	Location        coreLocation       `json:"location"`
}

type coreLowerer struct {
	program  *program
	function *functionDecl
	records  map[string]standardSymbolRecord
}

func (p *program) coreRuntimeFamilies() []string {
	families := make(map[string]struct{})
	include := func(used bool, family runtimeFamily) {
		if used {
			families[string(family)] = struct{}{}
		}
	}
	include(p.usesStdIO, runtimeFamilyIO)
	include(p.usesStdHTTP, runtimeFamilyHTTP)
	include(p.usesStdHTTPServer, runtimeFamilyHTTPServer)
	include(p.usesStdFSDirectory, runtimeFamilyFilesystem)
	include(p.usesStdProcess, runtimeFamilyProcess)
	include(p.usesStdSQLite, runtimeFamilySQLite)
	return sortedKeys(families)
}

func (p *program) lowerCore() (coreProgram, error) {
	lowerer := &coreLowerer{program: p, records: standardSymbolRecords(standardLibraryRegistry)}
	core := coreProgram{
		EvaluationOrder:    "left_to_right_once",
		CleanupSuppression: "immutable_primary_then_cleanup",
		RuntimeFamilies:    p.coreRuntimeFamilies(),
		Classes:            []coreClass{},
		Interfaces:         []coreInterface{},
		Unions:             []coreUnion{},
		Constants:          []coreConstant{},
		Functions:          []coreFunction{},
	}
	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		fields := make([]coreField, 0, len(class.fields))
		for _, fieldName := range sortedKeys(class.fields) {
			field := class.fields[fieldName]
			fields = append(fields, coreField{
				Name: field.name, JSONName: field.jsonName,
				Type: p.resolveType(class.namespace, class.aliases, field.typ), Location: coreLocationOf(field.pos),
			})
		}
		methods := make([]coreSignature, 0, len(class.methods))
		for _, methodName := range sortedKeys(class.methods) {
			method := class.methods[methodName]
			id := class.qualified + "." + method.name
			implementation := ""
			if class.implementations[methodName] != nil && class.implementations[methodName].native == "" {
				implementation = id
			}
			methods = append(methods, lowerer.signature(id, method.params, method.result, method.throws, method.operationSet, class.namespace, class.aliases, implementation, method.pos))
		}
		interfaces := make([]string, 0)
		for _, interfaceName := range sortedKeys(p.interfaces) {
			if len(p.classSatisfies(class, p.interfaces[interfaceName])) == 0 {
				interfaces = append(interfaces, interfaceName)
			}
		}
		core.Classes = append(core.Classes, coreClass{
			ID: class.qualified, Error: class.isError, Resource: class.nativeResource != "",
			Fields: fields, Methods: methods, Interfaces: interfaces, Location: coreLocationOf(class.pos),
		})
	}
	for _, name := range sortedKeys(p.interfaces) {
		iface := p.interfaces[name]
		methods := make([]coreSignature, 0, len(iface.methods))
		for _, methodName := range sortedKeys(iface.methods) {
			method := iface.methods[methodName]
			id := iface.qualified + "." + method.name
			methods = append(methods, lowerer.signature(id, method.params, method.result, method.throws, method.operationSet, method.namespace, method.aliases, "", method.pos))
		}
		core.Interfaces = append(core.Interfaces, coreInterface{ID: iface.qualified, Methods: methods, Location: coreLocationOf(iface.pos)})
	}
	for _, name := range sortedKeys(p.unions) {
		union := p.unions[name]
		variants := make([]coreVariant, 0, len(union.order))
		for _, variantName := range union.order {
			variant := union.variants[variantName]
			fields := make([]coreField, 0, len(variant.fields))
			for _, field := range variant.fields {
				fields = append(fields, coreField{Name: field.name, JSONName: field.jsonName, Type: p.resolveType(union.namespace, union.aliases, field.typ), Location: coreLocationOf(field.pos)})
			}
			variants = append(variants, coreVariant{ID: union.qualified + "." + variant.name, Tag: variant.tag, Fields: fields, Location: coreLocationOf(variant.pos)})
		}
		core.Unions = append(core.Unions, coreUnion{ID: union.qualified, Variants: variants, Location: coreLocationOf(union.pos)})
	}
	for _, name := range sortedKeys(p.constants) {
		constant := p.constants[name]
		value, err := coreLiteralValue(constant.value)
		if err != nil {
			return coreProgram{}, fmt.Errorf("lower constant %s: %w", constant.qualified, err)
		}
		core.Constants = append(core.Constants, coreConstant{ID: constant.qualified, Type: constant.resolved, Value: value, Location: coreLocationOf(constant.pos)})
	}
	for _, name := range sortedKeys(p.functions) {
		function := p.functions[name]
		if function.native != "" || function.ast == nil {
			continue
		}
		lowered, err := lowerer.functionDeclaration(function.qualified, function)
		if err != nil {
			return coreProgram{}, err
		}
		core.Functions = append(core.Functions, lowered)
	}
	for _, className := range sortedKeys(p.classes) {
		class := p.classes[className]
		for _, methodName := range sortedKeys(class.implementations) {
			implementation := class.implementations[methodName]
			if implementation.native != "" || implementation.ast == nil {
				continue
			}
			lowered, err := lowerer.functionDeclaration(class.qualified+"."+implementation.name, implementation)
			if err != nil {
				return coreProgram{}, err
			}
			core.Functions = append(core.Functions, lowered)
		}
	}
	sort.Slice(core.Functions, func(left, right int) bool { return core.Functions[left].ID < core.Functions[right].ID })
	return core, nil
}

func (l *coreLowerer) signature(id string, params []paramDecl, result typeRef, throws []typeRef, effects operationEffectSet, namespace string, aliases map[string]aliasDecl, implementation string, pos position) coreSignature {
	operation := runtimeOperationID("")
	if record, ok := l.records[id]; ok {
		operation = runtimeOperationID(record.native)
	}
	return coreSignature{
		ID: id, Parameters: l.bindings(namespace, aliases, params), Result: l.program.resolveType(namespace, aliases, result),
		Throws: l.types(namespace, aliases, throws), Effects: sortedOperationEffects(effects), Operation: operation,
		Implementation: implementation, Location: coreLocationOf(pos),
	}
}

func (l *coreLowerer) functionDeclaration(id string, function *functionDecl) (coreFunction, error) {
	previous := l.function
	l.function = function
	body, err := l.block(function.ast)
	l.function = previous
	if err != nil {
		return coreFunction{}, fmt.Errorf("lower %s: %w", id, err)
	}
	result := l.program.resolveType(function.namespace, function.aliases, function.result)
	l.setBlockStorage(&body, result)
	return coreFunction{
		ID: id, Receiver: function.receiverCanonical,
		Parameters: l.bindings(function.namespace, function.aliases, function.params),
		Result:     result,
		Throws:     l.types(function.namespace, function.aliases, function.throws),
		Effects:    sortedOperationEffects(function.operationSet), Body: body, Location: coreLocationOf(function.pos),
	}, nil
}

func (l *coreLowerer) bindings(namespace string, aliases map[string]aliasDecl, params []paramDecl) []coreBinding {
	bindings := make([]coreBinding, 0, len(params))
	for _, param := range params {
		bindings = append(bindings, coreBinding{Name: param.name, Type: l.program.resolveType(namespace, aliases, param.typ)})
	}
	return bindings
}

func (l *coreLowerer) types(namespace string, aliases map[string]aliasDecl, refs []typeRef) []string {
	types := make([]string, 0, len(refs))
	for _, ref := range refs {
		types = append(types, l.program.resolveType(namespace, aliases, ref))
	}
	sort.Strings(types)
	return types
}

func (l *coreLowerer) block(node *blockNode) (coreBlock, error) {
	if node == nil {
		return coreBlock{Statements: []coreStatement{}, Result: "null"}, nil
	}
	block := coreBlock{Statements: make([]coreStatement, 0, len(node.statements)), Result: "null", StructuredTasks: node.hasAsync, Location: coreLocationOf(node.pos)}
	if node.hasAsync {
		block.TaskExitPolicy = "cancel_then_join"
	}
	for _, statement := range node.statements {
		lowered, err := l.statement(statement)
		if err != nil {
			return coreBlock{}, err
		}
		block.Statements = append(block.Statements, lowered)
		block.Result = "null"
		switch node := statement.(type) {
		case *expressionStatement:
			block.Result = l.program.expressionTypes[node.value]
		case *returnStatement, *throwStatement, *breakStatement, *continueStatement:
			block.Result = typeNever
		}
	}
	return block, nil
}

func (l *coreLowerer) setBlockStorage(block *coreBlock, expected string) {
	block.StorageResult = expected
	block.ResultConversion = l.storageConversion(block.Result, expected)
	if len(block.Statements) > 0 {
		last := &block.Statements[len(block.Statements)-1]
		if last.Kind == "expression" {
			l.setStorage(last.Value, expected)
		}
	}
}

func (l *coreLowerer) setStorage(value *coreExpression, expected string) {
	if value == nil || expected == "" {
		return
	}
	value.StorageType = expected
	value.Conversion = l.storageConversion(value.Type, expected)
}

func (l *coreLowerer) storageConversion(actual, expected string) string {
	if actual == "" || actual == typeNever || actual == expected {
		return ""
	}
	if base, optional := optionalBase(expected); optional &&
		(actual == "null" || l.program.assignable(actual, base)) {
		return "optional_inject"
	}
	return ""
}

func (l *coreLowerer) statement(statement statementNode) (coreStatement, error) {
	core := coreStatement{Location: coreLocationOf(statement.statementPos())}
	value := func(expression expressionNode) error {
		lowered, err := l.expression(expression)
		if err == nil {
			core.Value = &lowered
		}
		return err
	}
	valueAs := func(expression expressionNode, expected string) error {
		if err := value(expression); err != nil {
			return err
		}
		l.setStorage(core.Value, expected)
		return nil
	}
	switch node := statement.(type) {
	case *letStatement:
		core.Kind = "bind"
		types := []string{node.resolved}
		if elements, ok := tupleElementTypes(node.resolved); ok && len(elements) == len(node.names) {
			types = elements
		}
		for index, name := range node.names {
			typ := node.resolved
			if index < len(types) {
				typ = types[index]
			}
			core.Bindings = append(core.Bindings, coreBinding{Name: name, Type: typ})
		}
		return core, valueAs(node.value, node.resolved)
	case *asyncLetStatement:
		core.Kind = "task_launch"
		core.Bindings = []coreBinding{{Name: node.name, Type: node.call.resolvedResult}}
		return core, value(node.call)
	case *assignmentStatement:
		core.Kind, core.Target = "assign", node.name
		return core, valueAs(node.value, node.resolved)
	case *forStatement:
		core.Kind = "loop"
		elementType, _ := iterableElementType(l.program.expressionTypes[node.iterable])
		bindingTypes := []string{elementType}
		if elements, ok := tupleElementTypes(elementType); ok && len(elements) == len(node.bindings) {
			bindingTypes = elements
		}
		for index, name := range node.bindings {
			core.Bindings = append(core.Bindings, coreBinding{Name: name, Type: bindingTypes[index]})
		}
		if err := value(node.iterable); err != nil {
			return coreStatement{}, err
		}
		body, err := l.block(node.body)
		l.setBlockStorage(&body, "null")
		core.Body = &body
		return core, err
	case *breakStatement:
		core.Kind = "break"
		return core, nil
	case *continueStatement:
		core.Kind = "continue"
		return core, nil
	case *throwStatement:
		core.Kind = "throw"
		return core, value(node.value)
	case *returnStatement:
		core.Kind = "return"
		result := l.program.resolveType(l.function.namespace, l.function.aliases, l.function.result)
		return core, valueAs(node.value, result)
	case *expressionStatement:
		core.Kind = "expression"
		return core, value(node.value)
	default:
		return coreStatement{}, fmt.Errorf("unclassified Core IR statement %T", statement)
	}
}

func (l *coreLowerer) expression(expression expressionNode) (coreExpression, error) {
	if expression == nil {
		return coreExpression{}, fmt.Errorf("nil Core IR expression")
	}
	typ, typed := l.program.expressionTypes[expression]
	if !typed || typ == typeUnknown || typ == "" {
		if name, ok := expression.(*nameExpression); ok {
			return coreExpression{}, fmt.Errorf("untyped Core IR name %q", name.name)
		}
		return coreExpression{}, fmt.Errorf("untyped Core IR expression %T", expression)
	}
	core := coreExpression{Type: typ, Location: coreLocationOf(expression.expressionPos())}
	lower := func(child expressionNode) (*coreExpression, error) {
		value, err := l.expression(child)
		return &value, err
	}
	lowerAll := func(expressions []expressionNode) ([]coreExpression, error) {
		values := make([]coreExpression, 0, len(expressions))
		for _, expression := range expressions {
			value, err := l.expression(expression)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	}
	switch node := expression.(type) {
	case *invalidExpression:
		return coreExpression{}, fmt.Errorf("invalid expression reached Core IR")
	case *literalExpression:
		literal, err := coreLiteralValue(node.value)
		if err != nil {
			return coreExpression{}, err
		}
		core.Kind, core.Literal = "literal", &literal
	case *tupleExpression:
		core.Kind = "tuple"
		var err error
		core.Elements, err = lowerAll(node.elements)
		if err != nil {
			return coreExpression{}, err
		}
		if elementTypes, tuple := tupleElementTypes(core.Type); tuple {
			for index := range core.Elements {
				if index < len(elementTypes) {
					l.setStorage(&core.Elements[index], elementTypes[index])
				}
			}
		}
	case *arrayExpression:
		core.Kind = "array"
		var err error
		core.Elements, err = lowerAll(node.elements)
		if err != nil {
			return coreExpression{}, err
		}
		if elementType, array := arrayElementType(core.Type); array {
			for index := range core.Elements {
				l.setStorage(&core.Elements[index], elementType)
			}
		}
	case *mapExpression:
		core.Kind = "map"
		keyType, valueType, _ := mapTypeArgs(core.Type)
		for _, entry := range node.entries {
			key, err := l.expression(entry.key)
			if err != nil {
				return coreExpression{}, err
			}
			value, err := l.expression(entry.value)
			if err != nil {
				return coreExpression{}, err
			}
			l.setStorage(&key, keyType)
			l.setStorage(&value, valueType)
			core.Entries = append(core.Entries, coreMapEntry{Key: key, Value: value})
		}
	case *rangeExpression:
		core.Kind = "range"
		var err error
		if core.Left, err = lower(node.start); err != nil {
			return coreExpression{}, err
		}
		if core.Right, err = lower(node.end); err != nil {
			return coreExpression{}, err
		}
	case *templateExpression:
		core.Kind = "template"
		core.Template = coreTemplateParts(node)
	case *nameExpression:
		core.Kind, core.Name = "name", node.name
		core.Declaration = l.nameDeclaration(node)
		if record, ok := l.records[core.Declaration]; ok {
			core.Operation = record.native
		}
		if node.storageType != "" {
			core.ReadStorageType = node.storageType
			if base, optional := optionalBase(node.storageType); optional && base == core.Type {
				core.ReadConversion = "optional_unwrap_proven"
			}
		}
	case *lambdaExpression:
		core.Kind = "lambda"
		core.Parameters = l.bindings(l.function.namespace, l.function.aliases, node.params)
		core.Throws = l.types(l.function.namespace, l.function.aliases, node.throws)
		core.Effects = sortedOperationEffects(node.fn.operationSet)
		for index, name := range node.captures {
			typ := ""
			if index < len(node.captureTypes) {
				typ = node.captureTypes[index]
			}
			core.Captures = append(core.Captures, coreCapture{Name: name, Type: typ})
		}
		body, err := l.block(node.body)
		if err != nil {
			return coreExpression{}, err
		}
		l.setBlockStorage(&body, l.program.resolveType(node.fn.namespace, node.fn.aliases, node.fn.result))
		core.Body = &body
	case *objectExpression:
		core.Kind, core.Declaration = "object", l.program.canonicalTypeName(l.function.namespace, l.function.aliases, node.typeName)
		class := l.program.classes[core.Declaration]
		for _, field := range node.fields {
			value, err := l.expression(field.value)
			if err != nil {
				return coreExpression{}, err
			}
			fieldType := ""
			declaration := ""
			if class != nil {
				fieldType = l.program.resolveType(class.namespace, class.aliases, class.fields[field.name].typ)
				declaration = class.qualified + "." + field.name
			}
			l.setStorage(&value, fieldType)
			core.Fields = append(core.Fields, coreFieldValue{Name: field.name, Declaration: declaration, Type: fieldType, Value: value, Conversion: value.Conversion})
		}
	case *callExpression:
		core.Kind = "call"
		core.Declaration = l.callDeclaration(node)
		core.ReceiverType = node.resolvedReceiver
		if node.resolvedReceiver != "" {
			name := node.callee.(*nameExpression)
			receiverName := strings.Split(name.name, ".")[0]
			core.Receiver = &coreExpression{
				Kind: "name", Type: node.resolvedReceiver, Name: receiverName,
				ReadStorageType: node.resolvedReceiverStorage, Location: coreLocationOf(name.pos),
			}
			if base, optional := optionalBase(node.resolvedReceiverStorage); optional && base == node.resolvedReceiver {
				core.Receiver.ReadConversion = "optional_unwrap_proven"
			}
		}
		core.Throws = coreSortedThrows(node.resolvedThrows)
		if record, ok := l.records[core.Declaration]; ok {
			core.Operation = runtimeOperationID(record.native)
		} else {
			core.Operation = runtimeOperationID(node.resolvedNative)
		}
		if core.Operation == "" && strings.HasPrefix(core.Declaration, "core.") {
			core.Operation = runtimeOperationID(core.Declaration)
		}
		var err error
		core.Arguments, err = lowerAll(node.args)
		if err != nil {
			return coreExpression{}, err
		}
		for index := range core.Arguments {
			if index < len(node.resolvedParams) {
				l.setStorage(&core.Arguments[index], node.resolvedParams[index])
			}
		}
		if core.Declaration == "" {
			core.Value, err = lower(node.callee)
			if err != nil {
				return coreExpression{}, err
			}
		}
	case *awaitExpression:
		core.Kind, core.Name = "task_await", node.name
	case *unaryExpression:
		core.Kind, core.Operator = "unary", node.op
		var err error
		core.Value, err = lower(node.value)
		if err != nil {
			return coreExpression{}, err
		}
	case *binaryExpression:
		core.Kind, core.Operator = "binary", node.op
		core.ShortCircuit = node.op == "&&" || node.op == "||"
		var err error
		if core.Left, err = lower(node.left); err != nil {
			return coreExpression{}, err
		}
		if core.Right, err = lower(node.right); err != nil {
			return coreExpression{}, err
		}
	case *ifExpression:
		core.Kind = "branch"
		var err error
		if core.Value, err = lower(node.condition); err != nil {
			return coreExpression{}, err
		}
		body, err := l.block(node.thenBlock)
		if err != nil {
			return coreExpression{}, err
		}
		alternate, err := l.block(node.elseBlock)
		if err != nil {
			return coreExpression{}, err
		}
		l.setBlockStorage(&body, core.Type)
		l.setBlockStorage(&alternate, core.Type)
		core.Body, core.Alternate = &body, &alternate
	case *catchExpression:
		core.Kind = "catch"
		var err error
		if core.Value, err = lower(node.value); err != nil {
			return coreExpression{}, err
		}
		for _, arm := range node.arms {
			value, err := l.expression(arm.value)
			if err != nil {
				return coreExpression{}, err
			}
			l.setStorage(&value, core.Type)
			errorType := l.program.canonicalTypeName(l.function.namespace, l.function.aliases, arm.errorType.name)
			bindingName := arm.binding
			if bindingName == "" {
				bindingName = node.binding
			}
			var bindings []coreBinding
			if bindingName != "" {
				bindings = []coreBinding{{Name: bindingName, Type: errorType}}
			}
			core.Arms = append(core.Arms, coreArm{Pattern: errorType, Bindings: bindings, Value: value, StorageType: core.Type, Conversion: value.Conversion, Location: coreLocationOf(arm.errorType.pos)})
		}
	case *resultExpression:
		core.Kind = "result"
		if node.ok {
			core.ResultVariant = "ok"
		} else {
			core.ResultVariant = "error"
		}
		var err error
		core.Value, err = lower(node.value)
		if err != nil {
			return coreExpression{}, err
		}
		success, failure, _ := resultTypeArgs(core.Type)
		payloadType := success
		if !node.ok {
			payloadType = failure
		}
		l.setStorage(core.Value, payloadType)
	case *propagateExpression:
		core.Kind = "propagate"
		var err error
		core.Value, err = lower(node.value)
		if err != nil {
			return coreExpression{}, err
		}
	case *usingExpression:
		core.Kind = "using"
		core.Bindings = []coreBinding{{Name: node.name, Type: node.resolved}}
		cleanupOperation := runtimeOperationID("core.resource.Close")
		if record, ok := l.records[node.resolved+".Close"]; ok && record.native != "" {
			cleanupOperation = record.native
		}
		core.Cleanup = &coreCleanup{Operation: cleanupOperation, Suppression: "immutable_primary_then_cleanup"}
		var err error
		if core.Value, err = lower(node.initializer); err != nil {
			return coreExpression{}, err
		}
		body, err := l.block(node.body)
		if err != nil {
			return coreExpression{}, err
		}
		l.setBlockStorage(&body, core.Type)
		core.Body = &body
	case *matchExpression:
		core.Kind = "match"
		var err error
		if core.Value, err = lower(node.value); err != nil {
			return coreExpression{}, err
		}
		scrutineeType := l.program.expressionTypes[node.value]
		success, failure, result := resultTypeArgs(scrutineeType)
		union := l.program.unions[scrutineeType]
		for _, arm := range node.arms {
			value, err := l.expression(arm.value)
			if err != nil {
				return coreExpression{}, err
			}
			l.setStorage(&value, core.Type)
			bindings := make([]coreBinding, 0, len(arm.bindings)+1)
			if arm.binding != "" {
				bindingType := success
				if arm.pattern == matchPatternErr {
					bindingType = failure
				}
				if !result {
					bindingType = scrutineeType
				}
				bindings = append(bindings, coreBinding{Name: arm.binding, Type: bindingType})
			}
			variantID := ""
			var bindingTypes []string
			if union != nil && arm.resolvedVariant != "" {
				variantID = union.qualified + "." + arm.resolvedVariant
				bindingTypes = l.program.variantFieldTypes(union, union.variants[arm.resolvedVariant])
			}
			for index, name := range arm.bindings {
				bindingType := scrutineeType
				if index < len(bindingTypes) {
					bindingType = bindingTypes[index]
				}
				bindings = append(bindings, coreBinding{Name: name, Type: bindingType})
			}
			core.Arms = append(core.Arms, coreArm{Pattern: arm.pattern.String(), Variant: variantID, Bindings: bindings, Value: value, StorageType: core.Type, Conversion: value.Conversion, Location: coreLocationOf(arm.pos)})
		}
	default:
		return coreExpression{}, fmt.Errorf("unclassified Core IR expression %T", expression)
	}
	return core, nil
}

func (l *coreLowerer) nameDeclaration(node *nameExpression) string {
	return node.resolvedDeclaration
}

func (l *coreLowerer) callDeclaration(node *callExpression) string {
	return node.resolvedDeclaration
}

func (p *program) debugCore() ([]byte, error) {
	core, err := p.lowerCore()
	if err != nil {
		return nil, err
	}
	return json.Marshal(core)
}

func coreLocationOf(pos position) coreLocation {
	return coreLocation{File: pos.file, Line: pos.line, Column: pos.column}
}

func coreLiteralValue(value any) (coreLiteral, error) {
	switch value := value.(type) {
	case nil:
		return coreLiteral{Kind: "null"}, nil
	case bool:
		return coreLiteral{Kind: "bool", Boolean: value}, nil
	case int64:
		return coreLiteral{Kind: "int", Integer: value}, nil
	case float64:
		return coreLiteral{Kind: "float", Float: value}, nil
	case string:
		return coreLiteral{Kind: "string", Text: value}, nil
	case constantVariant:
		return coreLiteral{Kind: "union", Variant: value.union.qualified + "." + value.variant.name}, nil
	case *constantVariant:
		return coreLiteral{Kind: "union", Variant: value.union.qualified + "." + value.variant.name}, nil
	default:
		return coreLiteral{}, fmt.Errorf("unsupported Core IR literal %T", value)
	}
}

func coreSortedThrows(throws effectSet) []string {
	names := make([]string, 0, len(throws))
	for name := range throws {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func coreTemplateParts(node *templateExpression) []coreTemplatePart {
	parts := make([]coreTemplatePart, 0, len(node.resolvedNames)*2+1)
	text := node.text
	index := 0
	for {
		start := strings.Index(text, "${")
		if start < 0 {
			if text != "" {
				parts = append(parts, coreTemplatePart{Text: text})
			}
			return parts
		}
		if start > 0 {
			parts = append(parts, coreTemplatePart{Text: text[:start]})
		}
		remaining := text[start+2:]
		end := strings.IndexByte(remaining, '}')
		if end < 0 {
			parts = append(parts, coreTemplatePart{Text: text})
			return parts
		}
		part := coreTemplatePart{Name: strings.TrimSpace(remaining[:end])}
		if index < len(node.resolvedNames) {
			part.Name = node.resolvedNames[index]
			part.Type = node.resolvedTypes[index]
			part.Declaration = node.resolvedTargets[index]
			part.ReadStorageType = node.resolvedStorageTypes[index]
			part.ReadConversion = node.resolvedConversions[index]
		}
		parts = append(parts, part)
		index++
		text = remaining[end+1:]
	}
}
