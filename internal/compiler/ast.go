package compiler

import (
	"strconv"
	"strings"
	"text/scanner"
)

type blockNode struct {
	statements []statementNode
	pos        position
	hasAsync   bool
}

type statementNode interface {
	statementPos() position
}

type expressionNode interface {
	expressionPos() position
}

// letStatement introduces one or more locals. resolved is the storage type the
// checker inferred for the value, which the interpreter needs in order to
// promote a value into an optional; it cannot be recovered from the node alone.
type letStatement struct {
	names    []string
	value    expressionNode
	resolved string
	pos      position
}

func (n *letStatement) statementPos() position { return n.pos }

type tupleExpression struct {
	elements []expressionNode
	resolved string
	pos      position
}

func (n *tupleExpression) expressionPos() position { return n.pos }

type asyncLetStatement struct {
	name string
	call *callExpression
	pos  position
}

func (n *asyncLetStatement) statementPos() position { return n.pos }

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

type invalidExpression struct {
	pos position
}

func (n *invalidExpression) expressionPos() position { return n.pos }

type literalExpression struct {
	value any
	pos   position
}

func (n *literalExpression) expressionPos() position { return n.pos }

type arrayExpression struct {
	elements []expressionNode
	resolved string
	pos      position
}

func (n *arrayExpression) expressionPos() position { return n.pos }

type mapEntryExpression struct {
	key   expressionNode
	value expressionNode
	pos   position
}

// mapExpression keeps its resolved type because an empty literal is typed by
// its enclosing context and cannot recover that context during generation.
type mapExpression struct {
	entries  []mapEntryExpression
	resolved string
	pos      position
}

func (n *mapExpression) expressionPos() position { return n.pos }

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

// lambdaExpression is an anonymous callable written with explicit parameter and
// result types. fn is the synthetic declaration the checker builds so both
// backends run the body exactly the way they run a named function, and captures
// names the surrounding bindings the body reads, copied by value when the
// callable is created.
type lambdaExpression struct {
	params   []paramDecl
	result   typeRef
	throws   []typeRef
	body     *blockNode
	fn       *functionDecl
	captures []string
	resolved string
	pos      position
}

func (n *lambdaExpression) expressionPos() position { return n.pos }

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
	callee   expressionNode
	typeArgs []typeRef
	args     []expressionNode
	// resolvedCallee names the concrete function a call reaches when the source
	// named a generic one, so both backends emit the instantiation the checker
	// selected instead of resolving the open declaration again.
	resolvedCallee        string
	resolvedTypeArgs      []string
	resolvedParams        []string
	resolvedArgumentTypes []string
	resolvedResult        string
	resolvedReceiver      string
	resolvedThrows        effectSet
	resolvedNative        nativeFunction
	// resolvedCallable marks a call that goes through a callable value rather
	// than a statically resolved function or method.
	resolvedCallable bool
	pos              position
}

func (n *callExpression) expressionPos() position { return n.pos }

type awaitExpression struct {
	name     string
	resolved string
	pos      position
}

func (n *awaitExpression) expressionPos() position { return n.pos }

type unaryExpression struct {
	op    string
	value expressionNode
	pos   position
}

func (n *unaryExpression) expressionPos() position { return n.pos }

type binaryExpression struct {
	left  expressionNode
	op    string
	right expressionNode
	pos   position
	opPos position
}

func (n *binaryExpression) expressionPos() position { return n.pos }

type ifExpression struct {
	condition expressionNode
	thenBlock *blockNode
	elseBlock *blockNode
	// compact is true only when this node came from source-level "else if".
	// Execution still uses the ordinary nested-if representation.
	compact bool
	pos     position
}

func (n *ifExpression) expressionPos() position { return n.pos }

type catchArm struct {
	errorType typeRef
	binding   string
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

// usingExpression owns the initializer value until body exits. resolved is the
// static resource type and result is the block type selected by the checker.
type usingExpression struct {
	name        string
	initializer expressionNode
	body        *blockNode
	resolved    string
	result      string
	pos         position
}

func (n *usingExpression) expressionPos() position { return n.pos }

type matchPattern int

const (
	matchPatternOk matchPattern = iota
	matchPatternErr
	matchPatternAny
	matchPatternVariant
)

func (pattern matchPattern) String() string {
	switch pattern {
	case matchPatternOk:
		return "Ok"
	case matchPatternErr:
		return "Err"
	case matchPatternVariant:
		return "variant"
	default:
		return "_"
	}
}

// matchArm is one pattern and its value. binding carries the single Result
// payload name; variant, bindings, and resolvedVariant carry a union pattern
// such as Expression.Binary(Left, Operator, Right).
type matchArm struct {
	pattern matchPattern
	binding string
	// variant is the pattern as written; resolvedVariant is the variant name
	// the checker matched it to, which every backend dispatches on.
	variant         string
	bindings        []string
	resolvedVariant string
	value           expressionNode
	pos             position
}

// matchExpression is a Result or union match.
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

// parseBodies parses every declared body once. An open generic body is parsed
// too: nothing checks it directly, but the formatter reads its statement
// positions, and each instantiation parses its own copy to own the resolved
// types the checker writes back.
func (p *program) parseBodies() {
	for _, function := range p.functions {
		if function.native != "" {
			continue
		}
		function.ast = p.parseBody(function.body, function.pos)
	}
	for _, function := range p.genericFunctions {
		function.ast = p.parseBody(function.body, function.pos)
	}
	for _, implementation := range p.methodImpls {
		if implementation.native != "" {
			continue
		}
		implementation.ast = p.parseBody(implementation.body, implementation.pos)
	}
	for _, implementation := range p.genericMethodImpls {
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
	if p.accept("async") {
		pos := p.previous().pos
		if !p.accept("let") {
			p.error(pos, "expected 'let' after 'async'")
			return nil
		}
		name, ok := p.expectIdent("binding name")
		if !ok {
			return nil
		}
		if !p.accept("=") {
			p.error(p.current().pos, "expected '=' after binding name")
			return nil
		}
		value := p.parseExpression()
		call, ok := value.(*callExpression)
		if !ok {
			p.error(pos, "async let initializer must be one function or method call")
			return nil
		}
		return &asyncLetStatement{name: name.text, call: call, pos: pos}
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
		var names []string
		if p.accept("(") {
			for {
				name, ok := p.expectIdent("destructuring binding")
				if !ok {
					return nil
				}
				names = append(names, name.text)
				if !p.accept(",") {
					break
				}
				if p.current().text == ")" {
					p.error(p.current().pos, "let destructuring does not allow a trailing comma")
					p.index++
					return nil
				}
			}
			if len(names) < 2 {
				p.error(pos, "let destructuring requires at least two bindings")
				if p.accept(")") {
					return nil
				}
			} else if !p.accept(")") {
				p.error(p.current().pos, "expected ')' after destructuring bindings")
				return nil
			}
		} else {
			name, ok := p.expectIdent("binding name")
			if !ok {
				return nil
			}
			names = []string{name.text}
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
		return &letStatement{names: names, value: value, pos: pos}
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
	start := p.parseBooleanOr()
	if start == nil || !p.matchPair(".", ".") {
		return start
	}
	end := p.parseBooleanOr()
	if end == nil {
		p.error(p.current().pos, "expected range end")
		return start
	}
	return &rangeExpression{start: start, end: end, pos: start.expressionPos()}
}

func (p *bodyParser) parseBooleanOr() expressionNode {
	left := p.parseBooleanAnd()
	if left == nil {
		return nil
	}
	for p.matchPair("|", "|") {
		opPos := p.tokens[p.index-2].pos
		right := p.parseBooleanAnd()
		if right == nil {
			p.error(p.current().pos, "expected expression after ||")
			return left
		}
		left = &binaryExpression{left: left, op: "||", right: right, pos: left.expressionPos(), opPos: opPos}
	}
	return left
}

func (p *bodyParser) parseBooleanAnd() expressionNode {
	left := p.parseEquality()
	if left == nil {
		return nil
	}
	for p.matchPair("&", "&") {
		opPos := p.tokens[p.index-2].pos
		right := p.parseEquality()
		if right == nil {
			p.error(p.current().pos, "expected expression after &&")
			return left
		}
		left = &binaryExpression{left: left, op: "&&", right: right, pos: left.expressionPos(), opPos: opPos}
	}
	return left
}

func (p *bodyParser) parseEquality() expressionNode {
	left := p.parseOrdering()
	if left == nil {
		return nil
	}
	for p.matchPair("=", "=") || p.matchPair("!", "=") {
		operator := p.tokens[p.index-2].text + p.tokens[p.index-1].text
		opPos := p.tokens[p.index-2].pos
		right := p.parseOrdering()
		if right == nil {
			p.error(p.current().pos, "expected expression after %s", operator)
			return left
		}
		left = &binaryExpression{left: left, op: operator, right: right, pos: left.expressionPos(), opPos: opPos}
	}
	return left
}

func (p *bodyParser) parseOrdering() expressionNode {
	left := p.parseAddition()
	if left == nil {
		return nil
	}
	for {
		operator := ""
		opPos := p.current().pos
		switch {
		case p.matchPair("<", "="):
			operator = "<="
		case p.matchPair(">", "="):
			operator = ">="
		case p.accept("<"):
			operator = "<"
		case p.accept(">"):
			operator = ">"
		default:
			return left
		}
		right := p.parseAddition()
		if right == nil {
			p.error(p.current().pos, "expected expression after %s", operator)
			return left
		}
		left = &binaryExpression{left: left, op: operator, right: right, pos: left.expressionPos(), opPos: opPos}
	}
}

func (p *bodyParser) parseAddition() expressionNode {
	left := p.parseMultiplication()
	if left == nil {
		return nil
	}
	for p.current().text == "+" || p.current().text == "-" {
		operator := p.current()
		p.index++
		right := p.parseMultiplication()
		if right == nil {
			p.error(p.current().pos, "expected expression after %q", operator.text)
			return left
		}
		left = &binaryExpression{left: left, op: operator.text, right: right, pos: left.expressionPos(), opPos: operator.pos}
	}
	return left
}

func (p *bodyParser) parseMultiplication() expressionNode {
	left := p.parseUnary()
	if left == nil {
		return nil
	}
	for p.accept("*") {
		opPos := p.previous().pos
		right := p.parseUnary()
		if right == nil {
			p.error(p.current().pos, "expected expression after '*'")
			return left
		}
		left = &binaryExpression{left: left, op: "*", right: right, pos: left.expressionPos(), opPos: opPos}
	}
	return left
}

func (p *bodyParser) parseUnary() expressionNode {
	if p.current().text == "-" || p.current().text == "!" {
		operator := p.current()
		p.index++
		value := p.parseUnary()
		if value == nil {
			p.error(operator.pos, "expected expression after unary %s", operator.text)
			return &invalidExpression{pos: operator.pos}
		}
		return &unaryExpression{op: operator.text, value: value, pos: operator.pos}
	}
	return p.parsePostfix()
}

func (p *bodyParser) parsePostfix() expressionNode {
	expression := p.parsePrimary()
	if expression == nil {
		return nil
	}
	for !p.atEnd() {
		if !p.stopObjectLiteral {
			if name, ok := expression.(*nameExpression); ok {
				if typeName, generic := p.tryParseObjectTypeArguments(name.name); generic {
					fields := p.parseObjectFields()
					expression = &objectExpression{typeName: typeName, fields: fields, pos: name.pos}
					continue
				}
				if p.accept("{") {
					fields := p.parseObjectFields()
					expression = &objectExpression{typeName: name.name, fields: fields, pos: name.pos}
					continue
				}
			}
		}
		if typeArgs, ok := p.tryParseCallTypeArguments(); ok {
			if !p.accept("(") {
				p.error(p.current().pos, "expected '(' after type arguments")
				return expression
			}
			args := p.parseArguments()
			expression = &callExpression{
				callee:   expression,
				typeArgs: typeArgs,
				args:     args,
				pos:      expression.expressionPos(),
			}
			continue
		}
		// A '(' that opens a lambda starts a new expression rather than an
		// argument list. The scanner drops newlines, so without this a statement
		// whose tail is an expression would swallow a following lambda; every
		// other '(' keeps its existing meaning.
		if p.current().text == "(" && !p.lambdaAhead() {
			p.index++
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
	if p.accept("await") {
		name, ok := p.expectIdent("pending binding after await")
		if !ok {
			return &invalidExpression{pos: tok.pos}
		}
		return &awaitExpression{name: name.text, pos: tok.pos}
	}
	if p.accept("using") {
		return p.parseUsing(tok.pos)
	}
	if p.accept("if") {
		return p.parseIf(tok.pos)
	}
	if p.accept("match") {
		return p.parseMatch(tok.pos)
	}
	if p.accept("map") {
		if !p.accept("{") {
			p.error(tok.pos, "expected '{' after map")
			return nil
		}
		return p.parseMap(tok.pos)
	}
	if p.accept("[") {
		return p.parseArray(tok.pos)
	}
	// A parenthesized expression is grouping or a tuple; only a parameter list
	// followed by -> opens a lambda, and -> appears nowhere else in an
	// expression, so the shape is unambiguous before anything is consumed.
	if p.current().text == "(" && p.lambdaAhead() {
		return p.parseLambda(tok.pos)
	}
	if p.accept("(") {
		first := p.parseExpression()
		if first == nil {
			p.error(tok.pos, "expected expression after '('")
			if p.accept(")") {
				return &invalidExpression{pos: tok.pos}
			}
			return nil
		}
		if !p.accept(",") {
			if !p.accept(")") {
				p.error(tok.pos, "unterminated parenthesized expression")
			}
			return first
		}
		elements := []expressionNode{first}
		for {
			if p.current().text == ")" {
				p.error(p.current().pos, "tuple expression requires at least two elements and does not allow a trailing comma")
				p.index++
				return &invalidExpression{pos: tok.pos}
			}
			element := p.parseExpression()
			if element == nil {
				p.error(p.current().pos, "expected tuple element")
				return &invalidExpression{pos: tok.pos}
			}
			elements = append(elements, element)
			if !p.accept(",") {
				break
			}
		}
		if !p.accept(")") {
			p.error(tok.pos, "unterminated tuple expression")
			return &invalidExpression{pos: tok.pos}
		}
		return &tupleExpression{elements: elements, pos: tok.pos}
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

func (p *bodyParser) lambdaAhead() bool {
	close := matching(p.tokens, p.index, "(", ")")
	return close >= 0 && close+2 < len(p.tokens) && p.tokens[close+1].text == "-" && p.tokens[close+2].text == ">"
}

func (p *bodyParser) parseLambda(pos position) expressionNode {
	params, ok := p.parseLambdaParams()
	if !ok {
		return &invalidExpression{pos: pos}
	}
	if !p.matchPair("-", ">") {
		p.error(p.current().pos, "expected '->' and a lambda result type")
		return &invalidExpression{pos: pos}
	}
	// The lambda owns the throws clause that follows its result, so a returned
	// callable declaring effects of its own is parenthesized.
	result, ok := p.parseTypeAllowing(false)
	if !ok {
		return &invalidExpression{pos: pos}
	}
	throws := p.parseLambdaThrows()
	if !p.accept("{") {
		p.error(p.current().pos, "expected lambda body")
		return &invalidExpression{pos: pos}
	}
	body := p.parseBlock(true, pos)
	return &lambdaExpression{params: params, result: result, throws: throws, body: body, pos: pos}
}

func (p *bodyParser) parseLambdaParams() ([]paramDecl, bool) {
	if !p.accept("(") {
		p.error(p.current().pos, "expected lambda parameter list")
		return nil, false
	}
	var params []paramDecl
	for !p.atEnd() && p.current().text != ")" {
		name, ok := p.expectIdent("lambda parameter name")
		if !ok {
			return nil, false
		}
		if !p.accept(":") {
			p.error(p.current().pos, "expected ':' and a type after lambda parameter name")
			return nil, false
		}
		typ, ok := p.parseTypeAllowing(true)
		if !ok {
			return nil, false
		}
		params = append(params, paramDecl{name: name.text, typ: typ})
		if !p.accept(",") {
			break
		}
	}
	if !p.accept(")") {
		p.error(p.current().pos, "expected ')' after lambda parameters")
		return nil, false
	}
	return params, true
}

func (p *bodyParser) parseLambdaThrows() []typeRef {
	if !p.accept("throws") {
		return nil
	}
	var throws []typeRef
	for {
		ref, next, ok := readQualified(p.tokens, p.index)
		if !ok {
			p.error(p.current().pos, "expected error type after 'throws'")
			return throws
		}
		throws = append(throws, typeRef{name: ref.name, pos: ref.pos})
		p.index = next
		if !p.accept("|") {
			return throws
		}
	}
}

func (p *bodyParser) parseUsing(pos position) expressionNode {
	p.program.usesUsing = true
	name, ok := p.expectIdent("using binding")
	if !ok {
		return nil
	}
	if !p.accept("=") {
		p.error(p.current().pos, "expected '=' after using binding")
		return nil
	}
	outerStop := p.stopObjectLiteral
	p.stopObjectLiteral = outerStop || p.usingInitializerIsBareName()
	initializer := p.parseExpression()
	p.stopObjectLiteral = outerStop
	if initializer == nil {
		p.error(p.current().pos, "expected resource initializer")
		return nil
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected using body")
		return nil
	}
	body := p.parseBlock(true, pos)
	return &usingExpression{name: name.text, initializer: initializer, body: body, pos: pos}
}
func (p *bodyParser) usingInitializerIsBareName() bool {
	_, next, ok := readQualified(p.tokens, p.index)
	if !ok || next >= len(p.tokens) || p.tokens[next].text != "{" {
		return false
	}
	depth := 0
	for index := next; index < len(p.tokens); index++ {
		switch p.tokens[index].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return index+1 >= len(p.tokens) || p.tokens[index+1].text != "{"
			}
		}
	}
	return true
}

func (p *bodyParser) parseIf(pos position) expressionNode {
	if !p.accept("(") {
		p.error(p.current().pos, "expected '(' after if")
		p.skipMalformedIfBranch()
		return &invalidExpression{pos: pos}
	}
	condition := p.parseExpression()
	if condition == nil {
		p.error(p.current().pos, "expected if condition")
		condition = &invalidExpression{pos: p.current().pos}
	}
	if !p.accept(")") {
		p.error(p.current().pos, "expected ')' after if condition")
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected block after if condition")
		p.skipMalformedIfBranch()
		return &invalidExpression{pos: pos}
	}
	thenBlock := p.parseBlock(true, pos)
	var elseBlock *blockNode
	if p.accept("else") {
		switch {
		case p.accept("{"):
			elseBlock = p.parseBlock(true, pos)
		case p.accept("if"):
			nestedPos := p.previous().pos
			nested := p.parseIf(nestedPos)
			if conditional, ok := nested.(*ifExpression); ok {
				conditional.compact = true
			}
			elseBlock = &blockNode{
				statements: []statementNode{&expressionStatement{value: nested, pos: nestedPos}},
				pos:        nestedPos,
			}
		default:
			p.error(p.current().pos, "expected 'if' or block after else")
			p.skipMalformedIfBranch()
			return &invalidExpression{pos: pos}
		}
	}
	return &ifExpression{condition: condition, thenBlock: thenBlock, elseBlock: elseBlock, pos: pos}
}

// skipMalformedIfBranch consumes the malformed branch and any following
// else-if tail so the enclosing block reports the syntax error only once.
func (p *bodyParser) skipMalformedIfBranch() {
	for {
		for !p.atEnd() && p.current().text != "{" && p.current().text != "}" && p.current().text != "else" {
			p.index++
		}
		if p.accept("{") {
			p.skipBalancedBlock()
		}
		if !p.accept("else") {
			return
		}
		if p.accept("{") {
			p.skipBalancedBlock()
			return
		}
		if !p.accept("if") {
			return
		}
	}
}

func (p *bodyParser) skipBalancedBlock() {
	depth := 1
	for !p.atEnd() && depth > 0 {
		switch p.current().text {
		case "{":
			depth++
		case "}":
			depth--
		}
		p.index++
	}
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
func (p *bodyParser) parseMap(pos position) expressionNode {
	var entries []mapEntryExpression
	for !p.atEnd() && p.current().text != "}" {
		if p.accept(",") || p.accept(";") {
			continue
		}
		key := p.parseExpression()
		if key == nil {
			p.error(p.current().pos, "expected map key")
			p.index++
			continue
		}
		if !p.accept(":") {
			p.error(p.current().pos, "expected ':' after map key")
			continue
		}
		value := p.parseExpression()
		if value == nil {
			p.error(key.expressionPos(), "expected map value")
			continue
		}
		entries = append(entries, mapEntryExpression{key: key, value: value, pos: key.expressionPos()})
		p.accept(",")
		p.accept(";")
	}
	if !p.accept("}") {
		p.error(pos, "unterminated map literal")
	}
	return &mapExpression{entries: entries, pos: pos}
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

// tryParseObjectTypeArguments consumes <...>{ only when the angle brackets
// carry the type arguments of a generic object construction. The closing > must
// be followed by the opening brace, so an ordinary comparison keeps its meaning.
func (p *bodyParser) tryParseObjectTypeArguments(name string) (string, bool) {
	if p.atEnd() || p.current().text != "<" {
		return "", false
	}
	close := matching(p.tokens, p.index, "<", ">")
	if close < 0 || close+1 >= len(p.tokens) || p.tokens[close+1].text != "{" {
		return "", false
	}
	var text strings.Builder
	text.WriteString(name)
	for _, tok := range p.tokens[p.index : close+1] {
		text.WriteString(tok.text)
	}
	p.index = close + 2
	return text.String(), true
}

// readTypeArgumentSuffix appends a <...> type argument list to base when one
// follows, so a caught error type may name a generic instantiation.
func (p *bodyParser) readTypeArgumentSuffix(base string) (string, bool) {
	if p.atEnd() || p.current().text != "<" {
		return base, true
	}
	close := matching(p.tokens, p.index, "<", ">")
	if close < 0 {
		p.error(p.current().pos, "unterminated generic type")
		return base, false
	}
	var text strings.Builder
	text.WriteString(base)
	for _, tok := range p.tokens[p.index : close+1] {
		text.WriteString(tok.text)
	}
	p.index = close + 1
	return text.String(), true
}

// tryParseCallTypeArguments consumes <...> only when the angle brackets form
// type arguments for a call: the closing > must be followed by (. Ordinary
// comparison expressions that use < or > are left untouched.
func (p *bodyParser) tryParseCallTypeArguments() ([]typeRef, bool) {
	if p.atEnd() || p.current().text != "<" {
		return nil, false
	}
	close := matchingAngle(p.tokens, p.index)
	if close < 0 || close+1 >= len(p.tokens) || p.tokens[close+1].text != "(" {
		return nil, false
	}
	start := p.index
	p.index++
	var typeArgs []typeRef
	for p.index < close {
		if p.accept(",") {
			continue
		}
		typeArg, ok := p.parseTypeArgument()
		if !ok {
			p.index = close + 1
			return nil, true
		}
		typeArgs = append(typeArgs, typeArg)
		if p.index >= close {
			break
		}
		if !p.accept(",") {
			p.error(p.current().pos, "expected ',' between type arguments")
			p.index = close + 1
			return typeArgs, true
		}
	}
	if p.index != close {
		p.error(p.tokens[start].pos, "malformed type arguments")
	}
	p.index = close + 1
	if len(typeArgs) == 0 {
		p.error(p.tokens[start].pos, "expected at least one type argument")
	}
	return typeArgs, true
}

func (p *bodyParser) parseTypeArgument() (typeRef, bool) {
	return p.parseTypeAllowing(true)
}

func (p *bodyParser) parseTypeAllowing(allowThrows bool) (typeRef, bool) {
	pos := p.current().pos
	text, next, message, errorPos := parseTypeTokensAllowing(p.tokens, p.index, allowThrows)
	if message != "" {
		p.error(errorPos, "%s", message)
		return typeRef{}, false
	}
	p.index = next
	return typeRef{name: text, pos: pos}, true
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
		caught, ok := p.readTypeArgumentSuffix(ref.name)
		if !ok {
			continue
		}
		armBinding := ""
		if p.accept("as") {
			name, bindingOK := p.expectIdent("catch binding")
			if !bindingOK {
				continue
			}
			armBinding = name.text
		}
		if !p.matchPair("=", ">") {
			p.error(p.current().pos, "expected '=>' after caught error type")
			continue
		}
		armValue := p.parseExpression()
		if armValue == nil {
			p.error(ref.pos, "expected catch arm value")
			continue
		}
		arms = append(arms, catchArm{errorType: typeRef{name: caught, pos: ref.pos}, binding: armBinding, value: armValue})
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
		p.program.add(ref.pos, diagnosticCodeResultConstructorArity, "%s expects exactly 1 argument, found %d", ref.name, len(args))
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
	if ref, next, ok := readQualified(p.tokens, p.index); ok && strings.Contains(ref.name, ".") {
		p.index = next
		return p.parseVariantMatchArm(ref)
	}
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
		p.program.add(name.pos, diagnosticCodeMatchPattern, "match supports only Ok(...), Err(...), Union.Variant patterns, and _, found %s", name.text)
		return matchArm{}, false
	}
	return p.parseMatchArmValue(arm)
}

// parseVariantMatchArm reads a qualified union pattern. A variant with payload
// fields binds one name per field, where _ ignores that field; a fieldless
// variant takes no binding list at all.
func (p *bodyParser) parseVariantMatchArm(ref qualifiedRef) (matchArm, bool) {
	arm := matchArm{pattern: matchPatternVariant, variant: ref.name, pos: ref.pos}
	if p.accept("(") {
		for !p.atEnd() && p.current().text != ")" {
			binding, ok := p.expectIdent("payload binding")
			if !ok {
				return matchArm{}, false
			}
			arm.bindings = append(arm.bindings, binding.text)
			if !p.accept(",") {
				break
			}
		}
		if !p.accept(")") {
			p.error(p.current().pos, "expected ')' after payload bindings")
			return matchArm{}, false
		}
		if len(arm.bindings) == 0 {
			p.program.add(ref.pos, diagnosticCodeMatchPattern, "%s must bind at least one payload value or omit its parentheses", ref.name)
			return matchArm{}, false
		}
	}
	return p.parseMatchArmValue(arm)
}

func (p *bodyParser) parseMatchArmValue(arm matchArm) (matchArm, bool) {
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
	if isAsyncKeyword(tok.text) {
		p.error(tok.pos, "%s is a reserved language keyword", tok.text)
		p.index++
		return token{}, false
	}
	p.index++
	return tok, true
}

func (p *bodyParser) atEnd() bool {
	return p.index >= len(p.tokens)
}

func (p *bodyParser) error(pos position, format string, args ...any) {
	p.program.add(pos, diagnosticCodeSyntax, format, args...)
}
