package compiler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type annotationTarget string

const (
	annotationTargetClass     annotationTarget = "class"
	annotationTargetMethod    annotationTarget = "method"
	annotationTargetParameter annotationTarget = "parameter"
	annotationTargetFunction  annotationTarget = "function"
)

// annotationUse is one authored @Name application. resolved records the
// compiler-owned terminal reached after alias expansion so describe consumers
// do not have to reproduce name resolution or substitution.
type annotationUse struct {
	name      string
	args      []expressionNode
	resolved  []resolvedAnnotation
	namespace string
	aliases   map[string]aliasDecl
	pos       position
}

// annotationDecl is either a source alias or a compiler-owned terminal.
type annotationDecl struct {
	name          string
	qualified     string
	namespace     string
	aliases       map[string]aliasDecl
	params        []paramDecl
	target        *annotationUse
	terminal      *terminalAnnotationDecl
	invalid       bool
	documentation *string
	pos           position
}

type annotationValue struct {
	typ      string
	display  string
	value    any
	function *functionDecl
}

type resolvedAnnotation struct {
	authored *annotationUse
	terminal *terminalAnnotationDecl
	values   []annotationValue
}

// terminalAnnotationDecl is the private compiler seam a framework owns. The
// language resolves and checks metadata; a terminal may then rewrite the
// already-parsed declaration once, before either backend checks or emits it.
type terminalAnnotationDecl struct {
	canonical     string
	params        []string
	targets       []annotationTarget
	repeatable    bool
	documentation string
	apply         func(*program, annotationTargetRef, resolvedAnnotation)
}

type annotationTargetRef struct {
	kind           annotationTarget
	name           string
	pos            position
	namespace      string
	aliases        map[string]aliasDecl
	annotations    []*annotationUse
	instance       bool
	class          *classDecl
	method         *methodSignature
	function       *functionDecl
	parameter      *paramDecl
	parameterIndex int
}

type pendingTerminalApplication struct {
	target     annotationTargetRef
	annotation resolvedAnnotation
}

func (p *program) registerTerminalAnnotation(terminal *terminalAnnotationDecl) {
	separator := strings.LastIndexByte(terminal.canonical, '.')
	if separator < 1 || separator == len(terminal.canonical)-1 {
		panic("terminal annotation name must be canonical: " + terminal.canonical)
	}
	decl := &annotationDecl{
		name:          terminal.canonical[separator+1:],
		qualified:     terminal.canonical,
		namespace:     terminal.canonical[:separator],
		terminal:      terminal,
		documentation: &terminal.documentation,
	}
	p.annotations[decl.qualified] = decl
}

func (p *parser) consumeAnnotations() []*annotationUse {
	annotations := p.pendingAnnotations
	p.pendingAnnotations = nil
	return annotations
}

func (p *parser) parseAnnotationUses() []*annotationUse {
	var annotations []*annotationUse
	for p.current().text == "@" {
		annotation := p.parseAnnotationUse()
		if annotation == nil {
			break
		}
		annotations = append(annotations, annotation)
	}
	return annotations
}

func (p *parser) parseAnnotationUse() *annotationUse {
	at := p.current()
	if !p.accept("@") {
		return nil
	}
	ref, next, ok := readQualified(p.tokens, p.index)
	if !ok {
		p.error(at.pos, "expected annotation name after '@'")
		return nil
	}
	p.index = next
	application := &annotationUse{name: ref.name, namespace: p.source.Namespace, aliases: p.aliases, pos: at.pos}
	if !p.accept("(") {
		return application
	}
	if p.accept(")") {
		p.error(at.pos, "zero-argument annotation %s is written @%s without parentheses", ref.name, ref.name)
		return application
	}
	for !p.atEnd() {
		body := &bodyParser{program: p.prog, tokens: p.tokens, index: p.index}
		argument := body.parseExpression()
		p.index = body.index
		if argument == nil {
			p.error(p.current().pos, "expected annotation argument")
			return application
		}
		application.args = append(application.args, argument)
		if !p.accept(",") {
			break
		}
		if p.current().text == ")" {
			break
		}
	}
	if !p.accept(")") {
		p.error(at.pos, "unterminated annotation application")
	}
	return application
}

func (p *parser) parseAnnotationDeclaration() {
	name, ok := p.expectIdent("annotation name")
	if !ok {
		return
	}
	var params []paramDecl
	if p.current().text == "(" {
		params, ok = p.parseParams()
		if !ok {
			return
		}
		for _, param := range params {
			if len(param.annotations) > 0 {
				p.prog.add(param.annotations[0].pos, diagnosticCodeAnnotationTarget, "annotation alias parameters cannot be annotated")
			}
		}
	}
	if !p.accept("=") {
		p.error(p.current().pos, "expected '=' and an annotation target")
		return
	}
	target := p.parseAnnotationUse()
	if target == nil {
		p.error(p.current().pos, "annotation alias must target one annotation")
		return
	}
	p.accept(";")
	qualified := qualify(p.source.Namespace, name.text)
	decl := &annotationDecl{
		name:          name.text,
		qualified:     qualified,
		namespace:     p.source.Namespace,
		aliases:       p.aliases,
		params:        params,
		target:        target,
		documentation: p.consumeDocumentation(),
		pos:           name.pos,
	}
	if previous := p.prog.annotations[qualified]; previous != nil {
		p.reportDocumentationConflict(name.pos, qualified, previous.documentation, decl.documentation)
		p.error(name.pos, "duplicate annotation %s; first declared at %s:%d:%d", qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	p.prog.annotations[qualified] = decl
}

func (p *program) annotationFor(namespace string, aliases map[string]aliasDecl, name string) *annotationDecl {
	return p.annotations[p.resolveNameIn(namespace, aliases, name)]
}

func (p *program) checkAnnotations() {
	p.checkAnnotationDeclarations()
	targets := p.annotationTargets()
	sort.SliceStable(targets, func(i, j int) bool { return positionLess(targets[i].pos, targets[j].pos) })
	var pending []pendingTerminalApplication
	for _, target := range targets {
		p.checkingInstance(target.instance, func() {
			p.checkAnnotationTarget(target, &pending)
		})
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return positionLess(pending[i].annotation.authored.pos, pending[j].annotation.authored.pos)
	})
	for _, application := range pending {
		if application.annotation.terminal.apply != nil {
			p.checkingInstance(application.target.instance, func() {
				application.annotation.terminal.apply(p, application.target, application.annotation)
			})
		}
	}
}

func (p *program) checkAnnotationTarget(target annotationTargetRef, pending *[]pendingTerminalApplication) {
	seen := make(map[string]position)
	for _, authored := range target.annotations {
		namespace, aliases := authored.namespace, authored.aliases
		if namespace == "" {
			namespace, aliases = target.namespace, target.aliases
		}
		resolved, ok := p.expandAnnotation(target, authored, namespace, aliases, nil, nil)
		if !ok {
			continue
		}
		authored.resolved = []resolvedAnnotation{resolved}
		if first, duplicate := seen[resolved.terminal.canonical]; duplicate && !resolved.terminal.repeatable {
			p.add(authored.pos, diagnosticCodeAnnotationTarget, "annotation %s cannot repeat on a %s; first applied at %s:%d:%d", resolved.terminal.canonical, target.kind, first.file, first.line, first.column)
			continue
		}
		seen[resolved.terminal.canonical] = authored.pos
		if !resolved.terminal.accepts(target.kind) {
			p.add(authored.pos, diagnosticCodeAnnotationTarget, "annotation %s cannot target a %s", resolved.terminal.canonical, target.kind)
			continue
		}
		*pending = append(*pending, pendingTerminalApplication{target: target, annotation: resolved})
	}
}

func positionLess(left, right position) bool {
	if left.file != right.file {
		return left.file < right.file
	}
	if left.line != right.line {
		return left.line < right.line
	}
	return left.column < right.column
}

func mergeAnnotationUses(first, second []*annotationUse) []*annotationUse {
	if len(first) == 0 {
		return second
	}
	if len(second) == 0 {
		return first
	}
	merged := append(append([]*annotationUse(nil), first...), second...)
	sort.SliceStable(merged, func(i, j int) bool { return positionLess(merged[i].pos, merged[j].pos) })
	return merged
}

func methodAnnotationUses(method *methodSignature, function *functionDecl) []*annotationUse {
	if method == nil {
		if function == nil {
			return nil
		}
		return function.annotations
	}
	if function == nil || function.inline {
		return method.annotations
	}
	return mergeAnnotationUses(method.annotations, function.annotations)
}

func (terminal *terminalAnnotationDecl) accepts(target annotationTarget) bool {
	for _, allowed := range terminal.targets {
		if target == allowed {
			return true
		}
	}
	return false
}

func (p *program) checkAnnotationDeclarations() {
	for _, name := range sortedKeys(p.annotations) {
		decl := p.annotations[name]
		if decl.terminal != nil {
			continue
		}
		if p.annotationNameCollides(decl.qualified) {
			p.add(decl.pos, diagnosticCodeAnnotation, "annotation %s conflicts with another declaration", decl.qualified)
		}
		seen := make(map[string]struct{}, len(decl.params))
		for _, param := range decl.params {
			if _, duplicate := seen[param.name]; duplicate {
				p.add(param.typ.pos, diagnosticCodeAnnotationArgument, "duplicate annotation parameter %s", param.name)
			}
			seen[param.name] = struct{}{}
			p.checkTypeRef(param.typ)
			resolved := p.resolveType(decl.namespace, decl.aliases, param.typ)
			p.checkTypeName(param.typ.pos, decl.namespace, resolved)
		}
	}
	states := make(map[string]uint8)
	for _, name := range sortedKeys(p.annotations) {
		p.validateAnnotationAlias(p.annotations[name], states, nil)
	}
}

func (p *program) validateAnnotationAlias(decl *annotationDecl, states map[string]uint8, chain []string) bool {
	if decl == nil {
		return false
	}
	if decl.terminal != nil {
		return true
	}
	switch states[decl.qualified] {
	case 2:
		return !decl.invalid
	case 1:
		index := 0
		for index < len(chain) && chain[index] != decl.qualified {
			index++
		}
		cycle := append(append([]string(nil), chain[index:]...), decl.qualified)
		p.add(decl.target.pos, diagnosticCodeAnnotationCycle, "annotation expansion cycle: %s", strings.Join(cycle, " -> "))
		for _, name := range cycle {
			if member := p.annotations[name]; member != nil {
				member.invalid = true
			}
		}
		return false
	}
	states[decl.qualified] = 1
	target := p.annotationFor(decl.namespace, decl.aliases, decl.target.name)
	if target == nil {
		p.add(decl.target.pos, diagnosticCodeAnnotation, "annotation alias %s does not resolve to a compiler-owned terminal; unknown target %s", decl.qualified, decl.target.name)
		decl.invalid = true
		states[decl.qualified] = 2
		return false
	}
	if !p.requireAccess(decl.target.pos, decl.namespace, target.namespace, target.name, "annotation") {
		decl.invalid = true
		states[decl.qualified] = 2
		return false
	}
	substitutions := make(map[string]annotationValue, len(decl.params))
	for _, param := range decl.params {
		substitutions[param.name] = annotationValue{typ: p.resolveType(decl.namespace, decl.aliases, param.typ), display: param.name}
	}
	values := make([]annotationValue, len(decl.target.args))
	valid := true
	for index, argument := range decl.target.args {
		value, ok := p.annotationArgument(decl.namespace, decl.aliases, argument, substitutions)
		if !ok {
			valid = false
			continue
		}
		values[index] = value
	}
	expected := targetAnnotationParameterTypes(p, target)
	if valid && !p.checkAnnotationArguments(decl.target.pos, target.qualified, values, expected, decl.namespace) {
		valid = false
	}
	if !p.validateAnnotationAlias(target, states, append(chain, decl.qualified)) {
		valid = false
	}
	if valid {
		resolved, ok := p.expandAnnotation(annotationTargetRef{}, decl.target, decl.namespace, decl.aliases, substitutions, nil)
		if ok {
			decl.target.resolved = []resolvedAnnotation{resolved}
		} else {
			valid = false
		}
	}
	decl.invalid = !valid
	states[decl.qualified] = 2
	return valid
}

func targetAnnotationParameterTypes(p *program, decl *annotationDecl) []string {
	if decl.terminal != nil {
		return decl.terminal.params
	}
	expected := make([]string, len(decl.params))
	for index, param := range decl.params {
		expected[index] = p.resolveType(decl.namespace, decl.aliases, param.typ)
	}
	return expected
}

func (p *program) annotationNameCollides(name string) bool {
	return p.classDeclaration(name) != nil || p.interfaceDeclaration(name) != nil || p.unions[name] != nil || p.functionDeclaration(name) != nil || p.constants[name] != nil
}

func (p *program) expandAnnotation(target annotationTargetRef, use *annotationUse, namespace string, aliases map[string]aliasDecl, substitutions map[string]annotationValue, chain []string) (resolvedAnnotation, bool) {
	decl := p.annotationFor(namespace, aliases, use.name)
	if decl == nil {
		p.add(use.pos, diagnosticCodeAnnotation, "unknown annotation %s", use.name)
		return resolvedAnnotation{}, false
	}
	if decl.invalid {
		return resolvedAnnotation{}, false
	}
	if !p.requireAccess(use.pos, namespace, decl.namespace, decl.name, "annotation") {
		return resolvedAnnotation{}, false
	}
	for index, name := range chain {
		if name == decl.qualified {
			cycle := append(append([]string(nil), chain[index:]...), decl.qualified)
			p.add(use.pos, diagnosticCodeAnnotationCycle, "annotation expansion cycle: %s", strings.Join(cycle, " -> "))
			return resolvedAnnotation{}, false
		}
	}
	values := make([]annotationValue, len(use.args))
	for index, argument := range use.args {
		value, ok := p.annotationArgument(namespace, aliases, argument, substitutions)
		if !ok {
			return resolvedAnnotation{}, false
		}
		values[index] = value
	}
	if decl.terminal != nil {
		if !p.checkAnnotationArguments(use.pos, decl.qualified, values, decl.terminal.params, namespace) {
			return resolvedAnnotation{}, false
		}
		return resolvedAnnotation{authored: use, terminal: decl.terminal, values: values}, true
	}
	expected := make([]string, len(decl.params))
	for index, param := range decl.params {
		expected[index] = p.resolveType(decl.namespace, decl.aliases, param.typ)
	}
	if !p.checkAnnotationArguments(use.pos, decl.qualified, values, expected, namespace) {
		return resolvedAnnotation{}, false
	}
	bound := make(map[string]annotationValue, len(decl.params))
	for index, param := range decl.params {
		bound[param.name] = values[index]
	}
	resolved, ok := p.expandAnnotation(target, decl.target, decl.namespace, decl.aliases, bound, append(chain, decl.qualified))
	if ok {
		resolved.authored = use
	}
	return resolved, ok
}

func (p *program) checkAnnotationArguments(pos position, name string, values []annotationValue, expected []string, namespace string) bool {
	if len(values) != len(expected) {
		p.add(pos, diagnosticCodeAnnotationArgument, "annotation %s expects %d arguments, found %d", name, len(expected), len(values))
		return false
	}
	valid := true
	for index, value := range values {
		if p.assignable(value.typ, expected[index]) {
			continue
		}
		p.add(pos, diagnosticCodeAnnotationArgument, "argument %d to annotation %s must be %s, found %s", index+1, name, displayName(expected[index]), displayName(value.typ))
		valid = false
	}
	return valid
}

func (p *program) annotationArgument(namespace string, aliases map[string]aliasDecl, expression expressionNode, substitutions map[string]annotationValue) (annotationValue, bool) {
	switch node := expression.(type) {
	case *literalExpression:
		return literalAnnotationValue(node.value), true
	case *unaryExpression:
		value, ok := p.annotationArgument(namespace, aliases, node.value, substitutions)
		if !ok {
			return annotationValue{}, false
		}
		folded, ok := evalConstantUnary(node.op, value.value)
		if !ok {
			p.add(node.pos, diagnosticCodeAnnotationArgument, "annotation argument is not a compile-time literal")
			return annotationValue{}, false
		}
		return literalAnnotationValue(folded), true
	case *lambdaExpression:
		p.add(node.pos, diagnosticCodeAnnotationArgument, "inline lambdas are not allowed in annotation arguments; use a named function")
		return annotationValue{}, false
	case *nameExpression:
		if value, exists := substitutions[node.name]; exists {
			return value, true
		}
		if constant := p.constantFor(namespace, aliases, node.name); constant != nil {
			if !p.requireAccess(node.pos, namespace, constant.namespace, constant.name, "constant") {
				return annotationValue{}, false
			}
			value, ok := p.evaluateConstant(constant)
			if !ok {
				p.add(node.pos, diagnosticCodeAnnotationArgument, "constant %s has no valid compile-time value", node.name)
				return annotationValue{}, false
			}
			return annotationValue{typ: constant.resolved, display: annotationConstantDisplay(value), value: value}, true
		}
		if union, variant, named := p.resolveVariant(namespace, aliases, node.name); named {
			variant = p.requireVariant(node.pos, namespace, node.name, union, variant)
			if variant == nil {
				return annotationValue{}, false
			}
			if len(variant.fields) != 0 {
				p.add(node.pos, diagnosticCodeAnnotationArgument, "payload variant %s is not a compile-time annotation value", node.name)
				return annotationValue{}, false
			}
			return annotationValue{typ: union.qualified, display: union.qualified + "." + variant.name, value: constantVariant{union: union, variant: variant}}, true
		}
		function := p.functionDeclaration(p.resolveNameIn(namespace, aliases, node.name))
		if function != nil {
			if !p.requireAccess(node.pos, namespace, function.namespace, function.name, "function") {
				return annotationValue{}, false
			}
			if len(function.typeParams) > 0 {
				p.add(node.pos, diagnosticCodeAnnotationArgument, "generic function %s has no single callable annotation value", node.name)
				return annotationValue{}, false
			}
			return annotationValue{typ: p.functionCallableType(function), display: function.qualified, function: function}, true
		}
		if p.annotationMethodExists(namespace, aliases, node.name) {
			p.add(node.pos, diagnosticCodeAnnotationArgument, "method %s is not an annotation value; use a named function", node.name)
			return annotationValue{}, false
		}
		p.add(node.pos, diagnosticCodeAnnotationArgument, "annotation argument %s is not a literal, constant, fieldless variant, alias parameter, or named function", node.name)
		return annotationValue{}, false
	default:
		p.add(expression.expressionPos(), diagnosticCodeAnnotationArgument, "annotation arguments must be literals, constants, fieldless variants, alias parameters, or named functions")
		return annotationValue{}, false
	}
}

func literalAnnotationValue(value any) annotationValue {
	switch value := value.(type) {
	case nil:
		return annotationValue{typ: "null", display: "null"}
	case bool:
		return annotationValue{typ: "bool", display: strconv.FormatBool(value), value: value}
	case int64:
		return annotationValue{typ: "int", display: strconv.FormatInt(value, 10), value: value}
	case float64:
		return annotationValue{typ: "float", display: strconv.FormatFloat(value, 'g', -1, 64), value: value}
	case string:
		return annotationValue{typ: "string", display: strconv.Quote(value), value: value}
	default:
		return annotationValue{typ: typeUnknown, display: fmt.Sprint(value), value: value}
	}
}

func annotationConstantDisplay(value any) string {
	if variant, ok := value.(constantVariant); ok {
		return variant.union.qualified + "." + variant.variant.name
	}
	return literalAnnotationValue(value).display
}

func (p *program) annotationMethodExists(namespace string, aliases map[string]aliasDecl, name string) bool {
	separator := strings.LastIndexByte(name, '.')
	if separator < 1 {
		return false
	}
	owner := p.resolveNameIn(namespace, aliases, name[:separator])
	method := name[separator+1:]
	if class := p.classDeclaration(owner); class != nil {
		return class.methods[method] != nil
	}
	if iface := p.interfaceDeclaration(owner); iface != nil {
		return iface.methods[method] != nil
	}
	return false
}

func (p *program) annotationTargets() []annotationTargetRef {
	var targets []annotationTargetRef
	for _, classes := range []map[string]*classDecl{p.genericClasses, p.classes} {
		for _, name := range sortedKeys(classes) {
			class := classes[name]
			instance := class.instanceOf != ""
			targets = append(targets, annotationTargetRef{kind: annotationTargetClass, name: class.qualified, pos: class.pos, namespace: class.namespace, aliases: class.aliases, annotations: class.annotations, instance: instance, class: class})
			for _, methodName := range sortedKeys(class.methods) {
				method := class.methods[methodName]
				function := class.implementations[method.name]
				if function == nil {
					function = p.methodImplementation(class.qualified, method.name)
				}
				annotations := methodAnnotationUses(method, function)
				methodTarget := annotationTargetRef{kind: annotationTargetMethod, name: class.qualified + "." + method.name, pos: method.pos, namespace: method.namespace, aliases: method.aliases, annotations: annotations, instance: instance, class: class, method: method, function: function}
				targets = append(targets, methodTarget)
				var implementationParams []paramDecl
				if function != nil && !function.inline {
					implementationParams = function.params
				}
				targets = append(targets, parameterAnnotationTargets(methodTarget, method.params, implementationParams)...)
			}
		}
	}
	for _, interfaces := range []map[string]*interfaceDecl{p.genericInterfaces, p.interfaces} {
		for _, name := range sortedKeys(interfaces) {
			iface := interfaces[name]
			instance := iface.instanceOf != ""
			for _, methodName := range sortedKeys(iface.methods) {
				method := iface.methods[methodName]
				methodTarget := annotationTargetRef{kind: annotationTargetMethod, name: iface.qualified + "." + method.name, pos: method.pos, namespace: method.namespace, aliases: method.aliases, annotations: method.annotations, instance: instance, method: method}
				targets = append(targets, methodTarget)
				targets = append(targets, parameterAnnotationTargets(methodTarget, method.params, nil)...)
			}
		}
	}
	for _, functions := range []map[string]*functionDecl{p.genericFunctions, p.functions} {
		for _, name := range sortedKeys(functions) {
			function := functions[name]
			kind := annotationTargetFunction
			if function.receiver.name != "" {
				kind = annotationTargetMethod
			}
			instance := function.instanceOf != ""
			functionTarget := annotationTargetRef{kind: kind, name: function.qualified, pos: function.pos, namespace: function.namespace, aliases: function.aliases, annotations: function.annotations, instance: instance, function: function}
			targets = append(targets, functionTarget)
			targets = append(targets, parameterAnnotationTargets(functionTarget, function.params, nil)...)
		}
	}
	for _, implementations := range [][]*functionDecl{p.genericMethodImpls, p.methodImpls} {
		for _, function := range implementations {
			if function.inline || p.methodDeclaration(function) != nil {
				continue
			}
			instance := function.instanceOf != ""
			methodTarget := annotationTargetRef{kind: annotationTargetMethod, name: function.qualified, pos: function.pos, namespace: function.namespace, aliases: function.aliases, annotations: function.annotations, instance: instance, function: function}
			if len(function.annotations) > 0 {
				targets = append(targets, methodTarget)
			}
			targets = append(targets, parameterAnnotationTargets(methodTarget, function.params, nil)...)
		}
	}
	return targets
}

func (p *program) methodImplementation(owner, method string) *functionDecl {
	for _, implementations := range [][]*functionDecl{p.genericMethodImpls, p.methodImpls} {
		for _, implementation := range implementations {
			receiver := implementation.receiverCanonical
			if receiver == "" {
				if _, alias := implementation.aliases[implementation.receiver.name]; alias {
					continue
				}
				receiver = implementation.receiver.name
				if !isAbsoluteCanonicalName(receiver) {
					receiver = qualify(implementation.namespace, receiver)
				}
			}
			if receiver == owner && implementation.name == method {
				return implementation
			}
		}
	}
	return nil
}

func (p *program) methodDeclaration(implementation *functionDecl) *methodSignature {
	receiver := implementation.receiverCanonical
	if receiver == "" {
		if _, alias := implementation.aliases[implementation.receiver.name]; alias {
			return nil
		}
		receiver = implementation.receiver.name
		if !isAbsoluteCanonicalName(receiver) {
			receiver = qualify(implementation.namespace, receiver)
		}
	}
	if class := p.classDeclaration(receiver); class != nil {
		return class.methods[implementation.name]
	}
	return nil
}

func parameterAnnotationTargets(owner annotationTargetRef, params, implementationParams []paramDecl) []annotationTargetRef {
	targets := make([]annotationTargetRef, 0, len(params))
	for index := range params {
		param := &params[index]
		annotations := param.annotations
		if index < len(implementationParams) && len(implementationParams[index].annotations) > 0 {
			annotations = mergeAnnotationUses(annotations, implementationParams[index].annotations)
		}
		target := owner
		target.kind = annotationTargetParameter
		target.name += "." + param.name
		target.pos = param.typ.pos
		target.annotations = annotations
		target.parameter = param
		target.parameterIndex = index
		targets = append(targets, target)
	}
	return targets
}

func (p *parser) parseAnnotatedTopLevel() {
	p.pendingDocumentation = p.takeDocumentation(p.current().pos.line)
	p.pendingAnnotations = p.parseAnnotationUses()
	switch {
	case p.accept("class"):
		p.parseClass()
	case p.accept("interface"):
		p.rejectPendingAnnotations("interface")
		p.parseInterface()
	case p.accept("function"):
		p.parseFunction()
	case p.accept("union"):
		p.rejectPendingAnnotations("union")
		p.parseUnion()
	case p.accept("const"):
		p.rejectPendingAnnotations("constant")
		p.parseConst()
	case p.accept("annotation"):
		p.rejectPendingAnnotations("annotation alias")
		p.parseAnnotationDeclaration()
	default:
		p.error(p.current().pos, "annotations may prefix only class, method, or parameter declarations")
		p.pendingAnnotations = nil
	}
}

func (p *parser) rejectPendingAnnotations(target string) {
	article := "a"
	if strings.HasPrefix(target, "a") || strings.HasPrefix(target, "i") {
		article = "an"
	}
	for _, annotation := range p.consumeAnnotations() {
		p.prog.add(annotation.pos, diagnosticCodeAnnotationTarget, "annotations cannot target %s %s", article, target)
	}
}
