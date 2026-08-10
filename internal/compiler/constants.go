package compiler

import (
	"strconv"
	"strings"
)

// constState tracks how far a constant has travelled through cycle detection.
// A constant that fails any check is parked in constFailed, so a dependant
// stops there instead of inheriting a placeholder value.
type constState uint8

const (
	constPending constState = iota
	constVisiting
	constDone
	constFailed
)

// constDecl is one namespace-scoped compile-time constant. value holds the
// single evaluated value both backends inline, so a constant needs no runtime
// storage and introduces no initialization order.
type constDecl struct {
	name          string
	qualified     string
	namespace     string
	aliases       map[string]aliasDecl
	typ           typeRef
	resolved      string
	ast           expressionNode
	value         any
	evaluated     bool
	state         constState
	documentation *string
	pos           position
}

// constantVariant is the evaluated form of a fieldless union variant. The two
// pointers identify one declaration, so comparing two of them with == answers
// variant equality without walking any payload: a fieldless variant has none.
type constantVariant struct {
	union   *unionDecl
	variant *unionVariantDecl
}

// callable presents a constant as the enclosing declaration the shared
// expression checker expects, so an initializer resolves names through the
// constant's own namespace and aliases.
func (decl *constDecl) callable() *functionDecl {
	return &functionDecl{
		name:      decl.name,
		qualified: decl.qualified,
		namespace: decl.namespace,
		aliases:   decl.aliases,
		result:    typeRef{name: decl.resolved, pos: decl.pos},
		pos:       decl.pos,
	}
}

func (p *parser) parseConst() {
	keyword := p.tokens[p.index-1]
	p.parseConstDeclaration(keyword)
	// One declaration owns one line, so whatever the declaration did not
	// consume is skipped instead of being read as the next declaration.
	for !p.atEnd() && p.current().pos.line == keyword.pos.line {
		p.advance()
	}
}

func (p *parser) parseConstDeclaration(keyword token) {
	documentation := p.consumeDocumentation()
	name, ok := p.expectIdent("constant name")
	if !ok {
		return
	}
	if !p.accept(":") {
		p.error(p.current().pos, "expected ':' after constant name")
		return
	}
	typ, ok := p.parseType()
	if !ok {
		return
	}
	if !p.accept("=") {
		p.error(p.current().pos, "expected '=' and a constant value")
		return
	}
	start := p.index
	for !p.atEnd() && p.current().pos.line == keyword.pos.line {
		p.advance()
	}
	initializer := p.tokens[start:p.index]
	for len(initializer) > 0 && initializer[len(initializer)-1].text == ";" {
		initializer = initializer[:len(initializer)-1]
	}
	if len(initializer) == 0 {
		p.error(keyword.pos, "constant %s needs its value on the same line as its declaration", name.text)
		return
	}
	body := &bodyParser{program: p.prog, tokens: initializer}
	value := body.parseExpression()
	if value == nil {
		p.error(initializer[0].pos, "expected a constant value")
		return
	}
	if !body.atEnd() {
		p.error(body.current().pos, "constant %s must be one expression", name.text)
		return
	}
	decl := &constDecl{
		name:          name.text,
		qualified:     qualify(p.source.Namespace, name.text),
		namespace:     p.source.Namespace,
		aliases:       p.aliases,
		typ:           typ,
		ast:           value,
		documentation: documentation,
		pos:           name.pos,
	}
	if previous, exists := p.prog.constants[decl.qualified]; exists {
		p.reportDocumentationConflict(name.pos, decl.qualified, previous.documentation, decl.documentation)
		p.error(name.pos, "duplicate constant %s; first declared at %s:%d:%d",
			decl.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	p.prog.constants[decl.qualified] = decl
}

// constantFor resolves name the way every other declaration reference resolves
// it: an absolute canonical path, an exact alias, or a member of namespace.
func (p *program) constantFor(namespace string, aliases map[string]aliasDecl, name string) *constDecl {
	return p.constants[p.resolveNameIn(namespace, aliases, name)]
}

// checkConstantReference types a name that denotes a constant. Access follows
// the same capitalization rule every other declaration uses.
func (p *program) checkConstantReference(node *nameExpression, scope *astScope) (expressionInfo, bool) {
	decl := p.constantFor(scope.function.namespace, scope.function.aliases, node.name)
	if decl == nil {
		return expressionInfo{}, false
	}
	p.requireAccess(node.pos, scope.function.namespace, decl.namespace, decl.name, "constant")
	return expressionInfo{typ: decl.resolved, effects: make(effectSet)}, true
}

// isConstantType reports whether a declared constant type is one a constant
// expression can produce: a language primitive or a union, whose fieldless
// variants are the only compound constant values.
func (p *program) isConstantType(name string) bool {
	switch name {
	case "null", "bool", "int", "float", "string":
		return true
	default:
		return p.unions[name] != nil
	}
}

// checkConstants runs the constant pipeline in the order its stages depend on
// one another: declared types first so references can be typed, then the closed
// expression grammar, then types, then cycles, then one evaluation each.
func (p *program) checkConstants() {
	names := sortedKeys(p.constants)
	for _, name := range names {
		decl := p.constants[name]
		decl.resolved = p.resolveType(decl.namespace, decl.aliases, decl.typ)
		if !p.isConstantType(decl.resolved) {
			p.add(decl.typ.pos, diagnosticCodeConstantType,
				"constant %s must be declared bool, int, float, string, null, or a union, found %s",
				decl.name, displayName(decl.resolved))
			decl.state = constFailed
		}
		p.checkConstantCollision(decl)
	}
	for _, name := range names {
		decl := p.constants[name]
		if !p.checkConstantExpression(decl, decl.ast) {
			decl.state = constFailed
		}
	}
	for _, name := range names {
		decl := p.constants[name]
		if decl.state == constFailed {
			continue
		}
		p.checkTypeName(decl.typ.pos, decl.namespace, decl.resolved)
		info := p.checkASTExpression(decl.ast, newASTScope(decl.callable(), 0))
		if !p.assignable(info.typ, decl.resolved) {
			p.reportUnassignable(decl.pos, info.typ, decl.resolved, diagnosticCodeTypeMismatch,
				"constant %s is %s, but its value produces %s",
				decl.name, displayName(decl.resolved), displayName(info.typ))
			decl.state = constFailed
		}
	}
	for _, name := range names {
		p.visitConstant(p.constants[name], nil)
	}
	for _, name := range names {
		p.evaluateConstant(p.constants[name])
	}
}

// checkConstantCollision rejects a constant occupying a name another
// declaration in the same namespace already owns.
func (p *program) checkConstantCollision(decl *constDecl) {
	kind, previous := "", position{}
	switch {
	case p.classes[decl.qualified] != nil:
		kind, previous = "class", p.classes[decl.qualified].pos
	case p.interfaces[decl.qualified] != nil:
		kind, previous = "interface", p.interfaces[decl.qualified].pos
	case p.unions[decl.qualified] != nil:
		kind, previous = "union", p.unions[decl.qualified].pos
	case p.functions[decl.qualified] != nil:
		kind, previous = "function", p.functions[decl.qualified].pos
	default:
		return
	}
	p.add(decl.pos, diagnosticCodeSyntax, "constant %s conflicts with the %s of the same name; first declared at %s:%d:%d",
		decl.qualified, kind, previous.file, previous.line, previous.column)
	decl.state = constFailed
}

// checkConstantExpression enforces the closed constant grammar: literals,
// references to other constants and to fieldless union variants, grouping,
// unary - and !, and the non-failing binary operators. Everything else is
// rejected here so no later stage has to invent a compile-time meaning for it.
func (p *program) checkConstantExpression(decl *constDecl, expression expressionNode) bool {
	switch node := expression.(type) {
	case *literalExpression, *nameExpression:
		return true
	case *unaryExpression:
		return p.checkConstantExpression(decl, node.value)
	case *binaryExpression:
		left := p.checkConstantExpression(decl, node.left)
		return p.checkConstantExpression(decl, node.right) && left
	case nil:
		return false
	default:
		p.add(expression.expressionPos(), diagnosticCodeConstantExpression,
			"constant %s cannot use %s; a constant expression is a literal, another constant, a fieldless union variant, grouping, unary - or !, or a non-failing operator",
			decl.name, p.constantExpressionLabel(decl, expression))
		return false
	}
}

func (p *program) constantExpressionLabel(decl *constDecl, expression expressionNode) string {
	switch node := expression.(type) {
	case *callExpression:
		if name, ok := node.callee.(*nameExpression); ok {
			if union, variant, named := p.resolveVariant(decl.namespace, decl.aliases, name.name); named && variant != nil {
				return "the payload variant " + union.name + "." + variant.name
			}
		}
		return "the call " + expressionLabel(node.callee)
	case *objectExpression:
		return "object construction"
	case *arrayExpression:
		return "an array literal"
	case *mapExpression:
		return "a map literal"
	case *tupleExpression:
		return "a tuple expression"
	case *templateExpression:
		return "a template"
	case *rangeExpression:
		return "a range"
	case *resultExpression:
		return "the Result constructor " + node.label()
	case *propagateExpression:
		return "the ? operator"
	case *catchExpression:
		return "catch"
	case *usingExpression:
		return "using"
	case *matchExpression:
		return "match"
	case *ifExpression:
		return "if"
	case *awaitExpression:
		return "await"
	default:
		return "this expression"
	}
}

// visitConstant walks the reference graph, so a cycle is reported from the
// declarations that form it rather than from whichever value happened to be
// needed first. Short circuiting never hides a cycle: every reference is an
// edge here, whether or not evaluation would reach it.
func (p *program) visitConstant(decl *constDecl, chain []string) {
	switch decl.state {
	case constDone, constFailed:
		return
	case constVisiting:
		p.reportConstantCycle(decl, chain)
		return
	}
	decl.state = constVisiting
	for _, target := range p.constantReferences(decl, decl.ast, nil) {
		p.visitConstant(target, append(chain, decl.qualified))
		if target.state == constFailed {
			decl.state = constFailed
		}
	}
	if decl.state == constVisiting {
		decl.state = constDone
	}
}

func (p *program) constantReferences(decl *constDecl, expression expressionNode, found []*constDecl) []*constDecl {
	switch node := expression.(type) {
	case *nameExpression:
		if target := p.constantFor(decl.namespace, decl.aliases, node.name); target != nil {
			return append(found, target)
		}
	case *unaryExpression:
		return p.constantReferences(decl, node.value, found)
	case *binaryExpression:
		return p.constantReferences(decl, node.right, p.constantReferences(decl, node.left, found))
	}
	return found
}

// reportConstantCycle names every declaration on the cycle once, then parks all
// of them so the remaining roots do not repeat the same chain.
func (p *program) reportConstantCycle(decl *constDecl, chain []string) {
	start := 0
	for index, name := range chain {
		if name == decl.qualified {
			start = index
			break
		}
	}
	cycle := append(append([]string(nil), chain[start:]...), decl.qualified)
	p.add(p.constants[chain[start]].pos, diagnosticCodeConstantCycle,
		"constant %s depends on itself: %s", chain[start], strings.Join(cycle, " -> "))
	for _, name := range chain[start:] {
		p.constants[name].state = constFailed
	}
}

// evaluateConstant produces the constant's single compile-time value. The
// reference graph is known acyclic by the time it runs, and memoisation keeps
// a constant reached from several places evaluated exactly once.
func (p *program) evaluateConstant(decl *constDecl) (any, bool) {
	if decl.evaluated {
		return decl.value, true
	}
	if decl.state != constDone {
		return nil, false
	}
	value, ok := p.evalConstantExpression(decl, decl.ast)
	if !ok {
		decl.state = constFailed
		return nil, false
	}
	decl.value = value
	decl.evaluated = true
	return value, true
}

// evalConstantExpression folds one constant expression with Slick's own
// semantics: int arithmetic wraps exactly as it does at runtime, and the
// boolean operators short circuit.
func (p *program) evalConstantExpression(decl *constDecl, expression expressionNode) (any, bool) {
	switch node := expression.(type) {
	case *literalExpression:
		return node.value, true
	case *nameExpression:
		if union, variant, named := p.resolveVariant(decl.namespace, decl.aliases, node.name); named && variant != nil {
			return constantVariant{union: union, variant: variant}, true
		}
		target := p.constantFor(decl.namespace, decl.aliases, node.name)
		if target == nil {
			return nil, false
		}
		return p.evaluateConstant(target)
	case *unaryExpression:
		value, ok := p.evalConstantExpression(decl, node.value)
		if !ok {
			return nil, false
		}
		return evalConstantUnary(node.op, value)
	case *binaryExpression:
		left, ok := p.evalConstantExpression(decl, node.left)
		if !ok {
			return nil, false
		}
		if short, decided := shortCircuitConstant(node.op, left); decided {
			return short, true
		}
		right, ok := p.evalConstantExpression(decl, node.right)
		if !ok {
			return nil, false
		}
		return evalConstantBinary(node.op, left, right)
	default:
		return nil, false
	}
}

func shortCircuitConstant(operator string, left any) (any, bool) {
	value, ok := left.(bool)
	if !ok {
		return nil, false
	}
	if operator == "&&" && !value {
		return false, true
	}
	if operator == "||" && value {
		return true, true
	}
	return nil, false
}

func evalConstantUnary(operator string, value any) (any, bool) {
	switch operator {
	case "-":
		switch number := value.(type) {
		case int64:
			return -number, true
		case float64:
			return -number, true
		}
	case "!":
		if flag, ok := value.(bool); ok {
			return !flag, true
		}
	}
	return nil, false
}

func evalConstantBinary(operator string, left, right any) (any, bool) {
	switch operator {
	case "==":
		return left == right, true
	case "!=":
		return left != right, true
	case "&&", "||":
		flag, ok := right.(bool)
		return flag, ok
	}
	switch leftValue := left.(type) {
	case int64:
		rightValue, ok := right.(int64)
		if !ok {
			return nil, false
		}
		return evalConstantArithmetic(operator, leftValue, rightValue)
	case float64:
		rightValue, ok := right.(float64)
		if !ok {
			return nil, false
		}
		return evalConstantArithmetic(operator, leftValue, rightValue)
	case string:
		rightValue, ok := right.(string)
		if ok && operator == "+" {
			return leftValue + rightValue, true
		}
	}
	return nil, false
}

// evalConstantArithmetic folds the arithmetic and ordering operators shared by
// int and float. Integer results wrap on overflow, which is what the same
// expression does at runtime; nothing here widens to another representation.
func evalConstantArithmetic[T int64 | float64](operator string, left, right T) (any, bool) {
	switch operator {
	case "+":
		return left + right, true
	case "-":
		return left - right, true
	case "*":
		return left * right, true
	case "<":
		return left < right, true
	case "<=":
		return left <= right, true
	case ">":
		return left > right, true
	case ">=":
		return left >= right, true
	}
	return nil, false
}

// constantRuntimeValue converts an evaluated constant into the interpreter's
// value representation.
func (p *program) constantRuntimeValue(decl *constDecl) (runtimeValue, bool) {
	if !decl.evaluated {
		return runtimeValue{}, false
	}
	if variant, ok := decl.value.(constantVariant); ok {
		return p.runtimeVariantValue(variant.union, variant.variant, nil), true
	}
	return runtimeValue{typ: decl.resolved, scalar: decl.value}, true
}

// constantGoValue inlines an evaluated constant into generated Go. A negative
// number is parenthesised because a Slick unary minus emits its operand
// directly after the operator, and "--5" is not Go.
func constantGoValue(decl *constDecl) (string, bool) {
	if !decl.evaluated {
		return "", false
	}
	if variant, ok := decl.value.(constantVariant); ok {
		return "&" + goUnionName(variant.union.qualified) + "{slickTag: " + strconv.Itoa(variant.variant.tag) + "}", true
	}
	literal := goLiteral(decl.value)
	if strings.HasPrefix(literal, "-") {
		return "(" + literal + ")", true
	}
	return literal, true
}
