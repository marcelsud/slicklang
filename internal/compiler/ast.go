package compiler

import (
	"strconv"
	"strings"
	"text/scanner"
)

type blockNode struct {
	statements []statementNode
	pos        position
}

type statementNode interface {
	statementPos() position
}

type expressionNode interface {
	expressionPos() position
}

// letStatement introduces a local. resolved is the storage type the checker
// inferred for it, which the interpreter needs in order to promote a value
// into an optional; it cannot be recovered from the node alone.
type letStatement struct {
	name     string
	value    expressionNode
	resolved string
	pos      position
}

func (n *letStatement) statementPos() position { return n.pos }

// assignmentStatement writes a local. resolved is the local's declared storage
// type, which branch narrowing never changes.
type assignmentStatement struct {
	name     string
	value    expressionNode
	resolved string
	pos      position
}

func (n *assignmentStatement) statementPos() position { return n.pos }

type forStatement struct {
	bindings []string
	iterable expressionNode
	body     *blockNode
	pos      position
}

func (n *forStatement) statementPos() position { return n.pos }

type breakStatement struct {
	pos position
}

func (n *breakStatement) statementPos() position { return n.pos }

type continueStatement struct {
	pos position
}

func (n *continueStatement) statementPos() position { return n.pos }

type throwStatement struct {
	value expressionNode
	pos   position
}

func (n *throwStatement) statementPos() position { return n.pos }

type returnStatement struct {
	value expressionNode
	pos   position
}

func (n *returnStatement) statementPos() position { return n.pos }

type expressionStatement struct {
	value expressionNode
	pos   position
}

func (n *expressionStatement) statementPos() position { return n.pos }

type literalExpression struct {
	value any
	pos   position
}

func (n *literalExpression) expressionPos() position { return n.pos }

type arrayExpression struct {
	elements []expressionNode
	pos      position
}

func (n *arrayExpression) expressionPos() position { return n.pos }

type rangeExpression struct {
	start expressionNode
	end   expressionNode
	pos   position
}

func (n *rangeExpression) expressionPos() position { return n.pos }

type templateExpression struct {
	text string
	pos  position
}

func (n *templateExpression) expressionPos() position { return n.pos }

type nameExpression struct {
	name string
	pos  position
}

func (n *nameExpression) expressionPos() position { return n.pos }

type objectFieldExpression struct {
	name  string
	value expressionNode
	pos   position
}

type objectExpression struct {
	typeName string
	fields   []objectFieldExpression
	pos      position
}

func (n *objectExpression) expressionPos() position { return n.pos }

type callExpression struct {
	callee expressionNode
	args   []expressionNode
	pos    position
}

func (n *callExpression) expressionPos() position { return n.pos }

type binaryExpression struct {
	left  expressionNode
	op    string
	right expressionNode
	pos   position
}

func (n *binaryExpression) expressionPos() position { return n.pos }

type ifExpression struct {
	condition expressionNode
	thenBlock *blockNode
	elseBlock *blockNode
	pos       position
}

func (n *ifExpression) expressionPos() position { return n.pos }

type catchArm struct {
	errorType typeRef
	value     expressionNode
}

type catchExpression struct {
	value   expressionNode
	binding string
	arms    []catchArm
	pos     position
}

func (n *catchExpression) expressionPos() position { return n.pos }

// resultExpression is an Ok(...) or Err(...) constructor. resolved holds the
// canonical Result type the checker took from the expected-type context; it is
// the one piece of context that cannot be recovered from the node alone.
type resultExpression struct {
	ok       bool
	value    expressionNode
	resolved string
	pos      position
}

func (n *resultExpression) expressionPos() position { return n.pos }

func (n *resultExpression) label() string {
	if n.ok {
		return "Ok"
	}
	return "Err"
}

// propagateExpression is the postfix ? operator.
type propagateExpression struct {
	value expressionNode
	pos   position
}

func (n *propagateExpression) expressionPos() position { return n.pos }

type matchPattern int

const (
	matchPatternOk matchPattern = iota
	matchPatternErr
	matchPatternAny
)

func (pattern matchPattern) String() string {
	switch pattern {
	case matchPatternOk:
		return "Ok"
	case matchPatternErr:
		return "Err"
	default:
		return "_"
	}
}

type matchArm struct {
	pattern matchPattern
	binding string
	value   expressionNode
	pos     position
}

// matchExpression is a Result match.
type matchExpression struct {
	value expressionNode
	arms  []matchArm
	pos   position
}

func (n *matchExpression) expressionPos() position { return n.pos }

type bodyParser struct {
	program           *program
	tokens            []token
	index             int
	stopObjectLiteral bool
}

func (p *program) parseBodies() {
	for _, function := range p.functions {
		if function.native != "" {
			continue
		}
		function.ast = p.parseBody(function.body, function.pos)
	}
	for _, implementation := range p.methodImpls {
		implementation.ast = p.parseBody(implementation.body, implementation.pos)
	}
}

func (p *program) parseBody(tokens []token, pos position) *blockNode {
	parser := &bodyParser{program: p, tokens: tokens}
	return parser.parseBlock(false, pos)
}

func (p *bodyParser) parseBlock(expectClose bool, pos position) *blockNode {
	block := &blockNode{pos: pos}
	for !p.atEnd() && (!expectClose || p.current().text != "}") {
		if p.accept(";") || p.accept(",") {
			continue
		}
		start := p.index
		statement := p.parseStatement()
		if statement != nil {
			block.statements = append(block.statements, statement)
		}
		p.accept(";")
		if p.index == start {
			p.error(p.current().pos, "could not parse statement starting at %q", p.current().text)
			p.index++
		}
	}
	if expectClose && !p.accept("}") {
		p.error(pos, "unterminated block")
	}
	return block
}

func (p *bodyParser) parseStatement() statementNode {
	if p.accept("for") {
		return p.parseForStatement(p.previous().pos)
	}
	if p.accept("break") {
		return &breakStatement{pos: p.previous().pos}
	}
	if p.accept("continue") {
		return &continueStatement{pos: p.previous().pos}
	}
	if p.current().kind == scanner.Ident && p.index+1 < len(p.tokens) && p.tokens[p.index+1].text == "=" &&
		(p.index+2 >= len(p.tokens) || p.tokens[p.index+2].text != "=") {
		name := p.current()
		p.index += 2
		value := p.parseExpression()
		if value == nil {
			p.error(name.pos, "expected assignment value")
			return nil
		}
		return &assignmentStatement{name: name.text, value: value, pos: name.pos}
	}
	if p.accept("let") {
		pos := p.previous().pos
		name, ok := p.expectIdent("binding name")
		if !ok {
			return nil
		}
		if !p.accept("=") {
			p.error(p.current().pos, "expected '=' after binding name")
			return nil
		}
		value := p.parseExpression()
		if value == nil {
			p.error(p.current().pos, "expected binding value")
			return nil
		}
		return &letStatement{name: name.text, value: value, pos: pos}
	}
	if p.accept("throw") {
		pos := p.previous().pos
		value := p.parseExpression()
		if value == nil {
			p.error(pos, "expected thrown error value")
			return nil
		}
		return &throwStatement{value: value, pos: pos}
	}
	if p.accept("return") {
		pos := p.previous().pos
		value := p.parseExpression()
		if value == nil {
			value = &literalExpression{value: nil, pos: pos}
		}
		return &returnStatement{value: value, pos: pos}
	}
	value := p.parseExpression()
	if value == nil {
		return nil
	}
	return &expressionStatement{value: value, pos: value.expressionPos()}
}

func (p *bodyParser) parseForStatement(pos position) statementNode {
	var bindings []string
	for {
		binding, ok := p.expectIdent("loop binding")
		if !ok {
			return nil
		}
		bindings = append(bindings, binding.text)
		if !p.accept(",") {
			break
		}
	}
	if !p.accept("in") {
		p.error(p.current().pos, "expected 'in' after loop bindings")
		return nil
	}
	outerStop := p.stopObjectLiteral
	p.stopObjectLiteral = true
	iterable := p.parseExpression()
	p.stopObjectLiteral = outerStop
	if iterable == nil {
		p.error(p.current().pos, "expected iterable expression")
		return nil
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected loop body")
		return nil
	}
	body := p.parseBlock(true, pos)
	return &forStatement{bindings: bindings, iterable: iterable, body: body, pos: pos}
}

func (p *bodyParser) parseExpression() expressionNode {
	return p.parseRangeExpression()
}

func (p *bodyParser) parseRangeExpression() expressionNode {
	start := p.parseEquality()
	if start == nil || !p.matchPair(".", ".") {
		return start
	}
	end := p.parseEquality()
	if end == nil {
		p.error(p.current().pos, "expected range end")
		return start
	}
	return &rangeExpression{start: start, end: end, pos: start.expressionPos()}
}

func (p *bodyParser) parseEquality() expressionNode {
	left := p.parseAddition()
	if left == nil {
		return nil
	}
	for p.matchPair("=", "=") || p.matchPair("!", "=") {
		operator := p.tokens[p.index-2].text + p.tokens[p.index-1].text
		right := p.parseAddition()
		if right == nil {
			p.error(p.current().pos, "expected expression after %s", operator)
			return left
		}
		left = &binaryExpression{left: left, op: operator, right: right, pos: left.expressionPos()}
	}
	return left
}

func (p *bodyParser) parseAddition() expressionNode {
	left := p.parsePostfix()
	if left == nil {
		return nil
	}
	for p.accept("+") {
		right := p.parsePostfix()
		if right == nil {
			p.error(p.current().pos, "expected expression after '+'")
			return left
		}
		left = &binaryExpression{left: left, op: "+", right: right, pos: left.expressionPos()}
	}
	return left
}

func (p *bodyParser) parsePostfix() expressionNode {
	expression := p.parsePrimary()
	if expression == nil {
		return nil
	}
	for !p.atEnd() {
		if !p.stopObjectLiteral {
			if name, ok := expression.(*nameExpression); ok && p.accept("{") {
				fields := p.parseObjectFields()
				expression = &objectExpression{typeName: name.name, fields: fields, pos: name.pos}
				continue
			}
		}
		if p.accept("(") {
			args := p.parseArguments()
			expression = &callExpression{callee: expression, args: args, pos: expression.expressionPos()}
			continue
		}
		if p.accept("catch") {
			expression = p.parseCatchExpression(expression)
			continue
		}
		if p.accept("?") {
			expression = &propagateExpression{value: expression, pos: expression.expressionPos()}
			continue
		}
		break
	}
	return expression
}

func (p *bodyParser) parsePrimary() expressionNode {
	if p.atEnd() {
		return nil
	}
	tok := p.current()
	if p.accept("if") {
		return p.parseIf(tok.pos)
	}
	if p.accept("match") {
		return p.parseMatch(tok.pos)
	}
	if p.accept("[") {
		return p.parseArray(tok.pos)
	}
	if p.accept("(") {
		expression := p.parseExpression()
		if !p.accept(")") {
			p.error(tok.pos, "unterminated parenthesized expression")
		}
		return expression
	}
	switch tok.kind {
	case scanner.String:
		p.index++
		value, err := strconv.Unquote(tok.text)
		if err != nil {
			p.error(tok.pos, "invalid string literal")
			return nil
		}
		return &literalExpression{value: value, pos: tok.pos}
	case scanner.RawString:
		p.index++
		return &templateExpression{text: strings.TrimSuffix(strings.TrimPrefix(tok.text, "`"), "`"), pos: tok.pos}
	case scanner.Int:
		p.index++
		value, err := strconv.ParseInt(tok.text, 10, 64)
		if err != nil {
			p.error(tok.pos, "invalid integer literal")
			return nil
		}
		return &literalExpression{value: value, pos: tok.pos}
	case scanner.Float:
		p.index++
		value, err := strconv.ParseFloat(tok.text, 64)
		if err != nil {
			p.error(tok.pos, "invalid float literal")
			return nil
		}
		return &literalExpression{value: value, pos: tok.pos}
	case scanner.Ident:
		ref, next, _ := readQualified(p.tokens, p.index)
		p.index = next
		switch ref.name {
		case "true":
			return &literalExpression{value: true, pos: ref.pos}
		case "false":
			return &literalExpression{value: false, pos: ref.pos}
		case "null":
			return &literalExpression{value: nil, pos: ref.pos}
		case "Ok", "Err":
			if p.current().text == "(" {
				return p.parseResultConstructor(ref)
			}
			return &nameExpression{name: ref.name, pos: ref.pos}
		default:
			return &nameExpression{name: ref.name, pos: ref.pos}
		}
	default:
		return nil
	}
}

func (p *bodyParser) parseIf(pos position) expressionNode {
	if !p.accept("(") {
		p.error(p.current().pos, "expected '(' after if")
		return nil
	}
	condition := p.parseExpression()
	if !p.accept(")") {
		p.error(p.current().pos, "expected ')' after if condition")
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected block after if condition")
		return nil
	}
	thenBlock := p.parseBlock(true, pos)
	var elseBlock *blockNode
	if p.accept("else") {
		if !p.accept("{") {
			p.error(p.current().pos, "expected block after else")
		} else {
			elseBlock = p.parseBlock(true, pos)
		}
	}
	return &ifExpression{condition: condition, thenBlock: thenBlock, elseBlock: elseBlock, pos: pos}
}

func (p *bodyParser) parseArray(pos position) expressionNode {
	var elements []expressionNode
	for !p.atEnd() && p.current().text != "]" {
		if p.accept(",") {
			continue
		}
		element := p.parseExpression()
		if element == nil {
			p.error(p.current().pos, "expected array element")
			p.index++
			continue
		}
		elements = append(elements, element)
		if !p.accept(",") {
			break
		}
	}
	if !p.accept("]") {
		p.error(pos, "unterminated array literal")
	}
	return &arrayExpression{elements: elements, pos: pos}
}

func (p *bodyParser) parseObjectFields() []objectFieldExpression {
	var fields []objectFieldExpression
	for !p.atEnd() && p.current().text != "}" {
		if p.accept(",") || p.accept(";") {
			continue
		}
		name, ok := p.expectIdent("object field name")
		if !ok {
			p.index++
			continue
		}
		if !p.accept(":") {
			p.error(p.current().pos, "expected ':' after object field name")
			continue
		}
		value := p.parseExpression()
		if value == nil {
			p.error(name.pos, "expected value for field %s", name.text)
			continue
		}
		fields = append(fields, objectFieldExpression{name: name.text, value: value, pos: name.pos})
	}
	if !p.accept("}") {
		p.error(p.current().pos, "unterminated object construction")
	}
	return fields
}

func (p *bodyParser) parseArguments() []expressionNode {
	var args []expressionNode
	for !p.atEnd() && p.current().text != ")" {
		if p.accept(",") {
			continue
		}
		argument := p.parseExpression()
		if argument == nil {
			p.error(p.current().pos, "expected call argument")
			p.index++
			continue
		}
		args = append(args, argument)
		if !p.accept(",") {
			break
		}
	}
	if !p.accept(")") {
		p.error(p.current().pos, "unterminated call")
	}
	return args
}

func (p *bodyParser) parseCatchExpression(value expressionNode) expressionNode {
	pos := p.previous().pos
	binding := ""
	if p.accept("(") {
		name, ok := p.expectIdent("catch binding")
		if ok {
			binding = name.text
		}
		if !p.accept(")") {
			p.error(p.current().pos, "expected ')' after catch binding")
		}
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected catch arms")
		return value
	}
	var arms []catchArm
	for !p.atEnd() && p.current().text != "}" {
		if p.accept(",") || p.accept(";") {
			continue
		}
		ref, next, ok := readQualified(p.tokens, p.index)
		if !ok {
			p.error(p.current().pos, "expected caught error type")
			p.index++
			continue
		}
		p.index = next
		if !p.matchPair("=", ">") {
			p.error(p.current().pos, "expected '=>' after caught error type")
			continue
		}
		armValue := p.parseExpression()
		if armValue == nil {
			p.error(ref.pos, "expected catch arm value")
			continue
		}
		arms = append(arms, catchArm{errorType: typeRef{name: ref.name, pos: ref.pos}, value: armValue})
	}
	if !p.accept("}") {
		p.error(pos, "unterminated catch expression")
	}
	return &catchExpression{value: value, binding: binding, arms: arms, pos: pos}
}

func (p *bodyParser) parseResultConstructor(ref qualifiedRef) expressionNode {
	p.accept("(")
	args := p.parseArguments()
	node := &resultExpression{ok: ref.name == "Ok", pos: ref.pos}
	if len(args) != 1 {
		p.program.add(ref.pos, "SLK359", "%s expects exactly 1 argument, found %d", ref.name, len(args))
	}
	if len(args) > 0 {
		node.value = args[0]
	}
	return node
}

func (p *bodyParser) parseMatch(pos position) expressionNode {
	outerStop := p.stopObjectLiteral
	p.stopObjectLiteral = true
	value := p.parseExpression()
	p.stopObjectLiteral = outerStop
	if value == nil {
		p.error(pos, "expected match scrutinee")
		return nil
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected match arms")
		return nil
	}
	node := &matchExpression{value: value, pos: pos}
	for !p.atEnd() && p.current().text != "}" {
		if p.accept(",") || p.accept(";") {
			continue
		}
		arm, ok := p.parseMatchArm()
		if !ok {
			p.index++
			continue
		}
		node.arms = append(node.arms, arm)
	}
	if !p.accept("}") {
		p.error(pos, "unterminated match expression")
	}
	return node
}

func (p *bodyParser) parseMatchArm() (matchArm, bool) {
	name, ok := p.expectIdent("match pattern")
	if !ok {
		return matchArm{}, false
	}
	arm := matchArm{pos: name.pos}
	switch {
	case name.text == "_":
		arm.pattern = matchPatternAny
	case isResultConstructor(name.text):
		arm.pattern = matchPatternOk
		if name.text == "Err" {
			arm.pattern = matchPatternErr
		}
		if !p.accept("(") {
			p.error(p.current().pos, "expected '(' after %s pattern", name.text)
			return matchArm{}, false
		}
		binding, ok := p.expectIdent("match binding")
		if !ok {
			return matchArm{}, false
		}
		if binding.text != "_" {
			arm.binding = binding.text
		}
		if !p.accept(")") {
			p.error(p.current().pos, "expected ')' after match binding")
			return matchArm{}, false
		}
	default:
		p.program.add(name.pos, "SLK360", "match supports only Ok(...), Err(...), and _ patterns, found %s", name.text)
		return matchArm{}, false
	}
	if !p.matchPair("=", ">") {
		p.error(p.current().pos, "expected '=>' after match pattern")
		return matchArm{}, false
	}
	value := p.parseExpression()
	if value == nil {
		p.error(arm.pos, "expected match arm value")
		return matchArm{}, false
	}
	arm.value = value
	return arm, true
}

func (p *bodyParser) current() token {
	if p.atEnd() {
		if len(p.tokens) == 0 {
			return token{kind: scanner.EOF}
		}
		return token{kind: scanner.EOF, pos: p.tokens[len(p.tokens)-1].pos}
	}
	return p.tokens[p.index]
}

func (p *bodyParser) previous() token {
	return p.tokens[p.index-1]
}

func (p *bodyParser) accept(text string) bool {
	if p.atEnd() || p.tokens[p.index].text != text {
		return false
	}
	p.index++
	return true
}

func (p *bodyParser) matchPair(first, second string) bool {
	if p.index+1 >= len(p.tokens) || p.tokens[p.index].text != first || p.tokens[p.index+1].text != second {
		return false
	}
	p.index += 2
	return true
}

func (p *bodyParser) expectIdent(label string) (token, bool) {
	if p.atEnd() || p.current().kind != scanner.Ident {
		p.error(p.current().pos, "expected %s", label)
		return token{}, false
	}
	tok := p.current()
	p.index++
	return tok, true
}

func (p *bodyParser) atEnd() bool {
	return p.index >= len(p.tokens)
}

func (p *bodyParser) error(pos position, format string, args ...any) {
	p.program.add(pos, "SLK001", format, args...)
}
