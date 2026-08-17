package compiler

import (
	"fmt"
	"strings"
)

func validateNativeCore(core coreProgram, runtime backendRuntimeInputs) error {
	if core.EvaluationOrder != "left_to_right_once" {
		return fmt.Errorf("unsupported Core evaluation order %q", core.EvaluationOrder)
	}
	if core.CleanupSuppression != "immutable_primary_then_cleanup" {
		return fmt.Errorf("unsupported Core cleanup contract %q", core.CleanupSuppression)
	}
	if runtime.abiVersion != runtimeABIVersion {
		return fmt.Errorf("runtime ABI %d does not match compiler ABI %d", runtime.abiVersion, runtimeABIVersion)
	}
	for _, function := range core.Functions {
		if function.ID == "root.main" {
			return nil
		}
	}
	return fmt.Errorf("Core IR has no root.main")
}

// coreEmitterProgram projects backend-neutral Core IR into the mature native
// rendering model. Every declaration and body node is rebuilt from Core; native
// emitters never retain or receive the checked source program.
func coreEmitterProgram(core coreProgram) (*program, error) {
	projection := &coreEmitterProjection{program: newProgram()}
	registerStandardLibrary(projection.program)
	projection.aliases = coreEmitterAliases(core)
	projection.installRuntimeFamilies(core.RuntimeFamilies)

	for _, class := range core.Classes {
		projection.installClass(class)
	}
	for _, iface := range core.Interfaces {
		projection.installInterface(iface)
	}
	for _, union := range core.Unions {
		projection.installUnion(union)
	}
	for _, constant := range core.Constants {
		if err := projection.installConstant(constant); err != nil {
			return nil, err
		}
	}
	projection.installNativeMethods()
	for _, function := range core.Functions {
		if err := projection.installFunction(function); err != nil {
			return nil, err
		}
	}
	if projection.program.functions["root.main"] == nil {
		return nil, fmt.Errorf("Core IR has no root.main")
	}
	return projection.program, nil
}

type coreEmitterProjection struct {
	program *program
	aliases map[string]aliasDecl
}

func (p *coreEmitterProjection) installRuntimeFamilies(families []string) {
	for _, family := range families {
		switch runtimeFamily(family) {
		case runtimeFamilyIO:
			p.program.usesStdIO = true
		case runtimeFamilyHTTP:
			p.program.usesStdHTTP = true
		case runtimeFamilyHTTPServer:
			p.program.usesStdHTTPServer = true
			p.program.usesContext = true
		case runtimeFamilyFilesystem:
			p.program.usesStdFSDirectory = true
		case runtimeFamilyProcess:
			p.program.usesStdProcess = true
		case runtimeFamilySQLite:
			p.program.usesStdSQLite = true
		}
	}
}

func (p *coreEmitterProjection) installClass(core coreClass) {
	name, namespace := coreDeclarationName(core.ID)
	class := p.program.classes[core.ID]
	if class == nil {
		class = &classDecl{
			name: name, qualified: core.ID, namespace: namespace, aliases: p.aliases,
			fields: make(map[string]fieldDecl), methods: make(map[string]*methodSignature),
			effective: make(map[string]*methodSignature), implementations: make(map[string]*functionDecl),
		}
		p.program.classes[core.ID] = class
	}
	class.name, class.qualified, class.namespace, class.aliases = name, core.ID, namespace, p.aliases
	class.isError = core.Error
	class.pos = corePosition(core.Location)
	class.fields = make(map[string]fieldDecl, len(core.Fields))
	for _, field := range core.Fields {
		class.fields[field.Name] = fieldDecl{name: field.Name, typ: coreTypeRef(field.Type), jsonName: field.JSONName, pos: corePosition(field.Location)}
	}
	class.methods = make(map[string]*methodSignature, len(core.Methods))
	class.effective = make(map[string]*methodSignature, len(core.Methods))
	if class.implementations == nil {
		class.implementations = make(map[string]*functionDecl)
	}
	for _, method := range core.Methods {
		signature := coreMethodSignature(method, namespace, p.aliases)
		class.methods[signature.name] = signature
		class.effective[signature.name] = signature
	}
}

func (p *coreEmitterProjection) installInterface(core coreInterface) {
	name, namespace := coreDeclarationName(core.ID)
	iface := &interfaceDecl{name: name, qualified: core.ID, namespace: namespace, methods: make(map[string]*methodSignature, len(core.Methods)), pos: corePosition(core.Location)}
	for _, method := range core.Methods {
		signature := coreMethodSignature(method, namespace, p.aliases)
		iface.methods[signature.name] = signature
	}
	p.program.interfaces[core.ID] = iface
}

func (p *coreEmitterProjection) installUnion(core coreUnion) {
	name, namespace := coreDeclarationName(core.ID)
	union := &unionDecl{name: name, qualified: core.ID, namespace: namespace, aliases: p.aliases, variants: make(map[string]*unionVariantDecl, len(core.Variants)), pos: corePosition(core.Location)}
	for _, variant := range core.Variants {
		variantName, _ := coreDeclarationName(variant.ID)
		fields := make([]fieldDecl, 0, len(variant.Fields))
		for _, field := range variant.Fields {
			fields = append(fields, fieldDecl{name: field.Name, typ: coreTypeRef(field.Type), jsonName: field.JSONName, pos: corePosition(field.Location)})
		}
		union.variants[variantName] = &unionVariantDecl{name: variantName, fields: fields, tag: variant.Tag, pos: corePosition(variant.Location)}
		union.order = append(union.order, variantName)
	}
	p.program.unions[core.ID] = union
}

func (p *coreEmitterProjection) installConstant(core coreConstant) error {
	name, namespace := coreDeclarationName(core.ID)
	value, err := p.literal(core.Value)
	if err != nil {
		return fmt.Errorf("project Core constant %s: %w", core.ID, err)
	}
	literal := &literalExpression{value: value, pos: corePosition(core.Location)}
	p.program.expressionTypes[literal] = core.Type
	p.program.constants[core.ID] = &constDecl{
		name: name, qualified: core.ID, namespace: namespace, aliases: p.aliases,
		typ: coreTypeRef(core.Type), resolved: core.Type, ast: literal, value: value,
		evaluated: true, state: constDone, pos: corePosition(core.Location),
	}
	return nil
}

func (p *coreEmitterProjection) installNativeMethods() {
	for _, implementation := range p.program.methodImpls {
		class := p.program.classes[implementation.receiverCanonical]
		if class == nil {
			continue
		}
		implementation.qualified = implementation.receiverCanonical + "." + implementation.name
		implementation.aliases = p.aliases
		implementation.throwSet = coreTypeSetFromRefs(implementation.throws)
		implementation.operationSet = coreOperationSetFromRefs(implementation.operations)
		class.implementations[implementation.name] = implementation
	}
	for _, function := range p.program.functions {
		function.aliases = p.aliases
		function.throwSet = coreTypeSetFromRefs(function.throws)
		function.operationSet = coreOperationSetFromRefs(function.operations)
	}
}

func (p *coreEmitterProjection) installFunction(core coreFunction) error {
	name, namespace := coreDeclarationName(core.ID)
	function := &functionDecl{
		name: name, qualified: core.ID, namespace: namespace, aliases: p.aliases,
		params: coreParams(core.Parameters), result: coreTypeRef(core.Result),
		throws: coreTypeRefs(core.Throws), throwSet: coreStringSet(core.Throws),
		operations: coreEffectRefs(core.Effects), operationSet: coreStringSet(core.Effects),
		receiverCanonical: core.Receiver, pos: corePosition(core.Location),
	}
	if core.Receiver != "" {
		function.receiver = coreTypeRef(core.Receiver)
		function.namespace = coreNamespace(core.Receiver)
	}
	block, err := p.block(core.Body, function)
	if err != nil {
		return fmt.Errorf("project Core function %s: %w", core.ID, err)
	}
	function.ast = block
	if core.Receiver == "" {
		p.program.functions[core.ID] = function
		return nil
	}
	class := p.program.classes[core.Receiver]
	if class == nil {
		return fmt.Errorf("Core method %s has unknown receiver %s", core.ID, core.Receiver)
	}
	class.implementations[name] = function
	p.program.methodImpls = append(p.program.methodImpls, function)
	return nil
}

func (p *coreEmitterProjection) block(core coreBlock, function *functionDecl) (*blockNode, error) {
	block := &blockNode{pos: corePosition(core.Location), hasAsync: core.StructuredTasks}
	if core.StructuredTasks {
		p.program.usesContext = true
	}
	for _, statement := range core.Statements {
		node, err := p.statement(statement, function)
		if err != nil {
			return nil, err
		}
		block.statements = append(block.statements, node)
	}
	return block, nil
}

func (p *coreEmitterProjection) statement(core coreStatement, function *functionDecl) (statementNode, error) {
	pos := corePosition(core.Location)
	value := func() (expressionNode, error) {
		if core.Value == nil {
			return nil, fmt.Errorf("%s statement has no value", core.Kind)
		}
		return p.expression(*core.Value, function)
	}
	switch core.Kind {
	case "bind":
		expression, err := value()
		if err != nil {
			return nil, err
		}
		names := make([]string, len(core.Bindings))
		for index, binding := range core.Bindings {
			names[index] = binding.Name
		}
		return &letStatement{names: names, value: expression, resolved: coreStorageType(*core.Value), pos: pos}, nil
	case "task_launch":
		expression, err := value()
		if err != nil {
			return nil, err
		}
		call, ok := expression.(*callExpression)
		if !ok || len(core.Bindings) != 1 {
			return nil, fmt.Errorf("invalid Core task launch")
		}
		p.program.usesContext = true
		return &asyncLetStatement{name: core.Bindings[0].Name, call: call, pos: pos}, nil
	case "assign":
		expression, err := value()
		if err != nil {
			return nil, err
		}
		return &assignmentStatement{name: core.Target, value: expression, resolved: coreStorageType(*core.Value), pos: pos}, nil
	case "loop":
		expression, err := value()
		if err != nil {
			return nil, err
		}
		if core.Body == nil {
			return nil, fmt.Errorf("Core loop has no body")
		}
		body, err := p.block(*core.Body, function)
		if err != nil {
			return nil, err
		}
		bindings := make([]string, len(core.Bindings))
		for index, binding := range core.Bindings {
			bindings[index] = binding.Name
		}
		return &forStatement{bindings: bindings, iterable: expression, body: body, pos: pos}, nil
	case "break":
		return &breakStatement{pos: pos}, nil
	case "continue":
		return &continueStatement{pos: pos}, nil
	case "throw":
		expression, err := value()
		return &throwStatement{value: expression, pos: pos}, err
	case "return":
		expression, err := value()
		return &returnStatement{value: expression, pos: pos}, err
	case "expression":
		expression, err := value()
		return &expressionStatement{value: expression, pos: pos}, err
	default:
		return nil, fmt.Errorf("unsupported Core statement %q", core.Kind)
	}
}

func (p *coreEmitterProjection) expression(core coreExpression, function *functionDecl) (expressionNode, error) {
	pos := corePosition(core.Location)
	child := func(value *coreExpression) (expressionNode, error) {
		if value == nil {
			return nil, fmt.Errorf("Core %s expression has no child", core.Kind)
		}
		return p.expression(*value, function)
	}
	children := func(values []coreExpression) ([]expressionNode, error) {
		result := make([]expressionNode, 0, len(values))
		for _, value := range values {
			node, err := p.expression(value, function)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		}
		return result, nil
	}
	var node expressionNode
	switch core.Kind {
	case "literal":
		if core.Literal == nil {
			return nil, fmt.Errorf("Core literal has no value")
		}
		value, err := p.literal(*core.Literal)
		if err != nil {
			return nil, err
		}
		node = &literalExpression{value: value, pos: pos}
	case "tuple":
		values, err := children(core.Elements)
		if err != nil {
			return nil, err
		}
		node = &tupleExpression{elements: values, resolved: core.Type, pos: pos}
	case "array":
		values, err := children(core.Elements)
		if err != nil {
			return nil, err
		}
		node = &arrayExpression{elements: values, resolved: core.Type, pos: pos}
	case "map":
		entries := make([]mapEntryExpression, 0, len(core.Entries))
		for _, entry := range core.Entries {
			key, err := p.expression(entry.Key, function)
			if err != nil {
				return nil, err
			}
			value, err := p.expression(entry.Value, function)
			if err != nil {
				return nil, err
			}
			entries = append(entries, mapEntryExpression{key: key, value: value, pos: pos})
		}
		node = &mapExpression{entries: entries, resolved: core.Type, pos: pos}
	case "range":
		left, err := child(core.Left)
		if err != nil {
			return nil, err
		}
		right, err := child(core.Right)
		if err != nil {
			return nil, err
		}
		node = &rangeExpression{start: left, end: right, pos: pos}
	case "template":
		template := &templateExpression{pos: pos}
		for _, part := range core.Template {
			if part.Name == "" {
				template.text += part.Text
				continue
			}
			template.text += "${" + part.Name + "}"
			template.resolvedNames = append(template.resolvedNames, part.Name)
			template.resolvedTypes = append(template.resolvedTypes, part.Type)
			template.resolvedTargets = append(template.resolvedTargets, part.Declaration)
			template.resolvedStorageTypes = append(template.resolvedStorageTypes, part.ReadStorageType)
			template.resolvedConversions = append(template.resolvedConversions, part.ReadConversion)
		}
		node = template
	case "name":
		node = &nameExpression{name: core.Name, resolvedStandard: coreOperationDeclaration(core), resolvedDeclaration: core.Declaration, storageType: core.ReadStorageType, pos: pos}
	case "lambda":
		body, err := p.blockValue(core.Body, function)
		if err != nil {
			return nil, err
		}
		params := coreParams(core.Parameters)
		lambdaFunction := &functionDecl{name: "lambda", namespace: function.namespace, aliases: p.aliases, params: params, result: coreTypeRef(coreCallableResult(core.Type)), throws: coreTypeRefs(core.Throws), throwSet: coreStringSet(core.Throws), operations: coreEffectRefs(core.Effects), operationSet: coreStringSet(core.Effects), ast: body, pos: pos}
		captures := make([]string, len(core.Captures))
		captureTypes := make([]string, len(core.Captures))
		for index, capture := range core.Captures {
			captures[index], captureTypes[index] = capture.Name, capture.Type
		}
		node = &lambdaExpression{params: params, result: lambdaFunction.result, throws: lambdaFunction.throws, operations: lambdaFunction.operations, body: body, fn: lambdaFunction, captures: captures, captureTypes: captureTypes, resolved: core.Type, pos: pos}
	case "object":
		fields := make([]objectFieldExpression, 0, len(core.Fields))
		for _, field := range core.Fields {
			value, err := p.expression(field.Value, function)
			if err != nil {
				return nil, err
			}
			fields = append(fields, objectFieldExpression{name: field.Name, resolvedStandard: field.Declaration, value: value, pos: pos})
		}
		node = &objectExpression{typeName: core.Declaration, fields: fields, pos: pos}
	case "call":
		call, err := p.call(core, function)
		if err != nil {
			return nil, err
		}
		node = call
	case "task_await":
		node = &awaitExpression{name: core.Name, resolved: core.Type, pos: pos}
	case "unary":
		value, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		node = &unaryExpression{op: core.Operator, value: value, pos: pos}
	case "binary":
		left, err := child(core.Left)
		if err != nil {
			return nil, err
		}
		right, err := child(core.Right)
		if err != nil {
			return nil, err
		}
		node = &binaryExpression{left: left, op: core.Operator, right: right, pos: pos, opPos: pos}
	case "branch":
		condition, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		body, err := p.blockValue(core.Body, function)
		if err != nil {
			return nil, err
		}
		alternate, err := p.blockValue(core.Alternate, function)
		if err != nil {
			return nil, err
		}
		node = &ifExpression{condition: condition, thenBlock: body, elseBlock: alternate, pos: pos}
	case "catch":
		value, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		arms := make([]catchArm, 0, len(core.Arms))
		for _, arm := range core.Arms {
			armValue, err := p.expression(arm.Value, function)
			if err != nil {
				return nil, err
			}
			binding := ""
			if len(arm.Bindings) > 0 {
				binding = arm.Bindings[0].Name
			}
			arms = append(arms, catchArm{errorType: coreTypeRef(arm.Pattern), binding: binding, value: armValue})
		}
		node = &catchExpression{value: value, arms: arms, pos: pos}
	case "result":
		value, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		node = &resultExpression{ok: core.ResultVariant == "ok", value: value, resolved: core.Type, pos: pos}
	case "propagate":
		value, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		node = &propagateExpression{value: value, pos: pos}
	case "using":
		initializer, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		body, err := p.blockValue(core.Body, function)
		if err != nil {
			return nil, err
		}
		if len(core.Bindings) != 1 {
			return nil, fmt.Errorf("Core using expression has %d bindings", len(core.Bindings))
		}
		p.program.usesUsing = true
		node = &usingExpression{name: core.Bindings[0].Name, initializer: initializer, body: body, resolved: core.Bindings[0].Type, result: core.Type, pos: pos}
	case "match":
		value, err := child(core.Value)
		if err != nil {
			return nil, err
		}
		arms := make([]matchArm, 0, len(core.Arms))
		for _, arm := range core.Arms {
			armValue, err := p.expression(arm.Value, function)
			if err != nil {
				return nil, err
			}
			pattern := coreMatchPattern(arm.Pattern)
			projected := matchArm{pattern: pattern, value: armValue, pos: corePosition(arm.Location)}
			if pattern == matchPatternVariant {
				projected.variant = arm.Variant
				projected.resolvedVariant, _ = coreDeclarationName(arm.Variant)
				for _, binding := range arm.Bindings {
					projected.bindings = append(projected.bindings, binding.Name)
				}
			} else if len(arm.Bindings) > 0 {
				projected.binding = arm.Bindings[0].Name
			}
			arms = append(arms, projected)
		}
		node = &matchExpression{value: value, arms: arms, pos: pos}
	default:
		return nil, fmt.Errorf("unsupported Core expression %q", core.Kind)
	}
	p.program.expressionTypes[node] = core.Type
	return node, nil
}

func (p *coreEmitterProjection) call(core coreExpression, function *functionDecl) (*callExpression, error) {
	arguments := make([]expressionNode, 0, len(core.Arguments))
	argumentTypes := make([]string, 0, len(core.Arguments))
	parameterTypes := make([]string, 0, len(core.Arguments))
	for _, argument := range core.Arguments {
		node, err := p.expression(argument, function)
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, node)
		argumentTypes = append(argumentTypes, argument.Type)
		parameterTypes = append(parameterTypes, coreStorageType(argument))
	}
	var callee expressionNode
	callable := core.Declaration == ""
	if callable {
		var err error
		callee, err = p.expression(*core.Value, function)
		if err != nil {
			return nil, err
		}
	} else {
		name := core.Declaration
		if strings.HasPrefix(core.Declaration, "core.") {
			name, _ = coreDeclarationName(core.Declaration)
		}
		if core.Receiver != nil {
			method, _ := coreDeclarationName(core.Declaration)
			name = core.Receiver.Name + "." + method
		}
		callee = &nameExpression{name: name, resolvedStandard: coreOperationDeclaration(core), resolvedDeclaration: core.Declaration, pos: corePosition(core.Location)}
		p.program.expressionTypes[callee] = callableType(parameterTypes, core.Type, core.Throws, core.Effects)
	}
	return &callExpression{
		callee: callee, args: arguments, resolvedCallee: core.Declaration, resolvedDeclaration: core.Declaration,
		resolvedTypeArgs: append([]string(nil), core.TypeArguments...), resolvedParams: parameterTypes,
		resolvedArgumentTypes: argumentTypes, resolvedResult: core.Type,
		resolvedReceiverStorage: coreReceiverStorage(core), resolvedReceiver: core.ReceiverType,
		resolvedThrows: coreThrowSet(core.Throws), resolvedNative: core.Operation,
		resolvedCallable: callable, pos: corePosition(core.Location),
	}, nil
}

func (p *coreEmitterProjection) blockValue(core *coreBlock, function *functionDecl) (*blockNode, error) {
	if core == nil {
		return nil, fmt.Errorf("Core expression has no block")
	}
	return p.block(*core, function)
}

func (p *coreEmitterProjection) literal(core coreLiteral) (any, error) {
	switch core.Kind {
	case "null":
		return nil, nil
	case "bool":
		return core.Boolean, nil
	case "int":
		return core.Integer, nil
	case "float":
		return core.Float, nil
	case "string":
		return core.Text, nil
	case "union":
		unionName, variantName := coreUnionVariant(core.Variant)
		union := p.program.unions[unionName]
		if union == nil || union.variants[variantName] == nil {
			return nil, fmt.Errorf("unknown Core union literal %s", core.Variant)
		}
		return constantVariant{union: union, variant: union.variants[variantName]}, nil
	default:
		return nil, fmt.Errorf("unsupported Core literal %q", core.Kind)
	}
}

func coreEmitterAliases(core coreProgram) map[string]aliasDecl {
	aliases := make(map[string]aliasDecl)
	add := func(name string) {
		if strings.Contains(name, ".") && !isAbsoluteCanonicalName(name) {
			aliases[name] = aliasDecl{name: name, target: name}
		}
		parsed := parseTypeName(name)
		if parsed.kind == typeKindGeneric && strings.Contains(parsed.base, ".") && !isAbsoluteCanonicalName(parsed.base) {
			aliases[parsed.base] = aliasDecl{name: parsed.base, target: parsed.base}
		}
	}
	for _, class := range core.Classes {
		add(class.ID)
		for _, field := range class.Fields {
			add(field.Type)
		}
	}
	for _, iface := range core.Interfaces {
		add(iface.ID)
	}
	for _, union := range core.Unions {
		add(union.ID)
	}
	for _, function := range core.Functions {
		add(function.ID)
		add(function.Receiver)
		add(function.Result)
		for _, parameter := range function.Parameters {
			add(parameter.Type)
		}
	}
	return aliases
}

func coreMethodSignature(core coreSignature, namespace string, aliases map[string]aliasDecl) *methodSignature {
	name, _ := coreDeclarationName(core.ID)
	return &methodSignature{
		name: name, namespace: namespace, ownerNamespace: namespace, aliases: aliases,
		params: coreParams(core.Parameters), result: coreTypeRef(core.Result),
		throws: coreTypeRefs(core.Throws), throwSet: coreStringSet(core.Throws),
		operations: coreEffectRefs(core.Effects), operationSet: coreStringSet(core.Effects), pos: corePosition(core.Location),
	}
}

func corePosition(location coreLocation) position {
	return position{file: location.File, line: location.Line, column: location.Column}
}

func coreTypeRef(name string) typeRef { return typeRef{name: name} }

func coreTypeRefs(names []string) []typeRef {
	result := make([]typeRef, len(names))
	for index, name := range names {
		result[index] = coreTypeRef(name)
	}
	return result
}

func coreParams(bindings []coreBinding) []paramDecl {
	result := make([]paramDecl, len(bindings))
	for index, binding := range bindings {
		result[index] = paramDecl{name: binding.Name, typ: coreTypeRef(binding.Type)}
	}
	return result
}

func coreEffectRefs(names []string) []operationEffectRef {
	result := make([]operationEffectRef, len(names))
	for index, name := range names {
		result[index] = operationEffectRef{name: name}
	}
	return result
}

func coreStringSet(names []string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func coreTypeSetFromRefs(refs []typeRef) map[string]struct{} {
	result := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		result[ref.name] = struct{}{}
	}
	return result
}

func coreThrowSet(names []string) effectSet {
	result := make(effectSet, len(names))
	for _, name := range names {
		result[name] = effectOrigin{}
	}
	return result
}

func coreCallableResult(name string) string {
	_, result, _, _, ok := callableTypeParts(name)
	if ok {
		return result
	}
	return typeUnknown
}

func coreOperationSetFromRefs(refs []operationEffectRef) operationEffectSet {
	result := make(operationEffectSet, len(refs))
	for _, ref := range refs {
		result[ref.name] = struct{}{}
	}
	return result
}

func coreDeclarationName(canonical string) (string, string) {
	index := strings.LastIndexByte(canonical, '.')
	if index < 0 {
		return canonical, "root"
	}
	return canonical[index+1:], canonical[:index]
}

func coreNamespace(canonical string) string {
	_, namespace := coreDeclarationName(canonical)
	return namespace
}

func coreUnionVariant(canonical string) (string, string) {
	variant, union := coreDeclarationName(canonical)
	return union, variant
}

func coreStorageType(value coreExpression) string {
	if value.StorageType != "" {
		return value.StorageType
	}
	return value.Type
}

func coreReceiverStorage(value coreExpression) string {
	if value.Receiver == nil {
		return ""
	}
	if value.Receiver.ReadStorageType != "" {
		return value.Receiver.ReadStorageType
	}
	return value.Receiver.Type
}

func coreOperationDeclaration(value coreExpression) string {
	if value.Operation != "" {
		return value.Declaration
	}
	return ""
}

func coreMatchPattern(name string) matchPattern {
	switch name {
	case "Ok":
		return matchPatternOk
	case "Err":
		return matchPatternErr
	case "variant":
		return matchPatternVariant
	default:
		return matchPatternAny
	}
}
