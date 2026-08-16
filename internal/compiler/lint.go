package compiler

import (
	"sort"
	"strings"
)

// Lint compiles sources once and reports the mechanical dead source a valid
// program can still contain. Compiler errors are returned instead of lint
// findings: claims read off an invalid AST would cascade from one mistake.
func Lint(sources []Source) []Diagnostic {
	prog, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return diagnostics
	}
	return prog.lint()
}

// LintPath lints the .slk file at path, or every .slk file under it.
func LintPath(path string) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	return Lint(sources), nil
}

// lint walks every authored callable once. It is the seam both `slick lint` and
// quality analysis use, so neither compiles the project twice.
func (p *program) lint() []Diagnostic {
	walker := &lintWalker{}
	for _, callable := range p.authoredCallables() {
		walker.walkCallable(callable)
	}
	sortDiagnostics(walker.diags)
	return walker.diags
}

// authoredCallables lists the callables a source actually wrote, in source
// order. Open generic declarations are included and their monomorphized clones
// are not, so one mistake in a generic is reported once however many
// instantiations a program mentions. Native declarations have no body to read.
func (p *program) authoredCallables() []*functionDecl {
	callables := make([]*functionDecl, 0, len(p.functions)+len(p.methodImpls))
	appendAuthored := func(function *functionDecl) {
		if function.ast == nil || function.native != "" || function.instanceOf != "" {
			return
		}
		callables = append(callables, function)
	}
	for _, name := range sortedKeys(p.functions) {
		appendAuthored(p.functions[name])
	}
	for _, name := range sortedKeys(p.genericFunctions) {
		appendAuthored(p.genericFunctions[name])
	}
	for _, implementations := range [][]*functionDecl{p.methodImpls, p.genericMethodImpls} {
		for _, implementation := range implementations {
			appendAuthored(implementation)
		}
	}
	sort.SliceStable(callables, func(first, second int) bool {
		left, right := callables[first], callables[second]
		if left.pos.file != right.pos.file {
			return left.pos.file < right.pos.file
		}
		if left.pos.line != right.pos.line {
			return left.pos.line < right.pos.line
		}
		if left.pos.column != right.pos.column {
			return left.pos.column < right.pos.column
		}
		return left.qualified < right.qualified
	})
	return callables
}

// lintBinding is one let binding's lexical identity. Two bindings that share a
// name are separate identities, so shadowing never transfers a read.
type lintBinding struct {
	name string
	pos  position
	read bool
}

// lintScope is one lexical frame. tracked holds the let bindings this rule
// owns; opaque names every other binding form, whose only lint role is to
// shadow an outer binding of the same name.
type lintScope struct {
	declared []*lintBinding
	byName   map[string]*lintBinding
}

type lintWalker struct {
	diags  []Diagnostic
	scopes []*lintScope
}

func (w *lintWalker) push() {
	w.scopes = append(w.scopes, &lintScope{byName: make(map[string]*lintBinding)})
}

// pop closes a frame and reports the bindings it never read, in declaration
// order.
func (w *lintWalker) pop() {
	scope := w.scopes[len(w.scopes)-1]
	w.scopes = w.scopes[:len(w.scopes)-1]
	for _, binding := range scope.declared {
		if binding.read {
			continue
		}
		w.report(binding.pos, diagnosticCodeUnreadBinding, "binding %s is never read", binding.name)
	}
}

// bind records a let binding this rule reports on. A name bound to _ is an
// explicit discard.
func (w *lintWalker) bind(name string, pos position) {
	if name == "_" {
		return
	}
	scope := w.scopes[len(w.scopes)-1]
	binding := &lintBinding{name: name, pos: pos}
	scope.declared = append(scope.declared, binding)
	scope.byName[name] = binding
}

// shadow records a binding form outside this rule's scope: a parameter, loop
// binding, match or catch payload, using resource, or async let. It exists only
// so a read inside its scope cannot be attributed to an outer let.
func (w *lintWalker) shadow(name string) {
	if name == "" || name == "_" {
		return
	}
	w.scopes[len(w.scopes)-1].byName[name] = nil
}

// read resolves one name to the innermost binding that owns it. A dotted read
// such as Entry.Name reads Entry.
func (w *lintWalker) read(name string) {
	if name == "" {
		return
	}
	if index := strings.IndexByte(name, '.'); index >= 0 {
		name = name[:index]
	}
	for index := len(w.scopes) - 1; index >= 0; index-- {
		binding, declared := w.scopes[index].byName[name]
		if !declared {
			continue
		}
		if binding != nil {
			binding.read = true
		}
		return
	}
}

func (w *lintWalker) report(pos position, code diagnosticCode, format string, args ...any) {
	w.diags = append(w.diags, newDiagnostic(pos, code, format, args...))
}

func (w *lintWalker) walkCallable(function *functionDecl) {
	w.push()
	for _, param := range function.params {
		w.shadow(param.name)
	}
	if function.receiverCanonical != "" {
		w.shadow("self")
	}
	w.walkBlock(function.ast)
	w.pop()
}

func (w *lintWalker) walkBlock(block *blockNode) {
	if block == nil {
		return
	}
	w.push()
	terminated := false
	for index, statement := range block.statements {
		if terminated {
			w.report(statement.statementPos(), diagnosticCodeUnreachableStatement, "statement is unreachable")
		}
		if expression, ok := statement.(*expressionStatement); ok && index < len(block.statements)-1 && pureExpression(expression.value) {
			w.report(expression.pos, diagnosticCodeDiscardedExpression, "pure expression result is discarded")
		}
		w.walkStatement(statement)
		if terminatesBlock(statement) {
			terminated = true
		}
	}
	w.pop()
}

func terminatesBlock(statement statementNode) bool {
	switch statement.(type) {
	case *returnStatement, *throwStatement, *breakStatement, *continueStatement:
		return true
	default:
		return false
	}
}

func (w *lintWalker) walkStatement(statement statementNode) {
	switch node := statement.(type) {
	case *letStatement:
		w.walkExpression(node.value)
		for _, name := range node.names {
			w.bind(name, node.pos)
		}
	case *asyncLetStatement:
		w.walkExpression(node.call)
		w.shadow(node.name)
	case *assignmentStatement:
		// Writing a binding is not reading it.
		w.walkExpression(node.value)
	case *forStatement:
		w.walkExpression(node.iterable)
		w.push()
		for _, name := range node.bindings {
			w.shadow(name)
		}
		w.walkBlock(node.body)
		w.pop()
	case *returnStatement:
		w.walkExpression(node.value)
	case *throwStatement:
		w.walkExpression(node.value)
	case *expressionStatement:
		w.walkExpression(node.value)
	case *breakStatement, *continueStatement:
	}
}

func (w *lintWalker) walkExpression(expression expressionNode) {
	switch node := expression.(type) {
	case nil, *literalExpression, *invalidExpression:
	case *nameExpression:
		w.read(node.name)
	case *templateExpression:
		for _, name := range interpolatedNames(node.text) {
			w.read(name)
		}
	case *awaitExpression:
		w.read(node.name)
	case *tupleExpression:
		w.walkExpressions(node.elements)
	case *arrayExpression:
		w.walkExpressions(node.elements)
	case *mapExpression:
		for _, entry := range node.entries {
			w.walkExpression(entry.key)
			w.walkExpression(entry.value)
		}
	case *rangeExpression:
		w.walkExpression(node.start)
		w.walkExpression(node.end)
	case *objectExpression:
		for _, field := range node.fields {
			w.walkExpression(field.value)
		}
	case *callExpression:
		w.walkExpression(node.callee)
		w.walkExpressions(node.args)
	case *unaryExpression:
		w.walkExpression(node.value)
	case *binaryExpression:
		w.walkExpression(node.left)
		w.walkExpression(node.right)
	case *propagateExpression:
		w.walkExpression(node.value)
	case *resultExpression:
		w.walkExpression(node.value)
	case *lambdaExpression:
		w.push()
		for _, param := range node.params {
			w.shadow(param.name)
		}
		w.walkBlock(node.body)
		w.pop()
	case *usingExpression:
		w.walkExpression(node.initializer)
		w.push()
		w.shadow(node.name)
		w.walkBlock(node.body)
		w.pop()
	case *ifExpression:
		w.walkExpression(node.condition)
		w.walkBlock(node.thenBlock)
		w.walkBlock(node.elseBlock)
	case *matchExpression:
		w.walkExpression(node.value)
		for _, arm := range node.arms {
			w.push()
			w.shadow(arm.binding)
			for _, name := range arm.bindings {
				w.shadow(name)
			}
			w.walkExpression(arm.value)
			w.pop()
		}
	case *catchExpression:
		w.walkExpression(node.value)
		for _, arm := range node.arms {
			w.push()
			w.shadow(node.binding)
			w.shadow(arm.binding)
			w.walkExpression(arm.value)
			w.pop()
		}
	}
}

func (w *lintWalker) walkExpressions(expressions []expressionNode) {
	for _, expression := range expressions {
		w.walkExpression(expression)
	}
}

// interpolatedNames lists the names a template reads. Interpolation accepts a
// name or dotted field access only, so the text between ${ and } is the read.
func interpolatedNames(text string) []string {
	var names []string
	for index := 0; index < len(text); index++ {
		if text[index] != '$' || index+1 >= len(text) || text[index+1] != '{' {
			continue
		}
		close := strings.IndexByte(text[index+2:], '}')
		if close < 0 {
			break
		}
		names = append(names, strings.TrimSpace(text[index+2:index+2+close]))
		index += 2 + close
	}
	return names
}

// pureExpression proves an expression tree free of effects using a closed
// allowlist. Every call, propagation, await, and unrecognized form is treated as
// effectful, so a discarded statement is reported only when doing nothing is
// certain.
func pureExpression(expression expressionNode) bool {
	switch node := expression.(type) {
	case *literalExpression, *templateExpression, *nameExpression, *lambdaExpression:
		return true
	case *tupleExpression:
		return pureExpressions(node.elements)
	case *arrayExpression:
		return pureExpressions(node.elements)
	case *rangeExpression:
		return pureExpression(node.start) && pureExpression(node.end)
	case *mapExpression:
		for _, entry := range node.entries {
			if !pureExpression(entry.key) || !pureExpression(entry.value) {
				return false
			}
		}
		return true
	case *objectExpression:
		for _, field := range node.fields {
			if !pureExpression(field.value) {
				return false
			}
		}
		return true
	case *resultExpression:
		return pureExpression(node.value)
	case *unaryExpression:
		return pureExpression(node.value)
	case *binaryExpression:
		return pureExpression(node.left) && pureExpression(node.right)
	default:
		return false
	}
}

func pureExpressions(expressions []expressionNode) bool {
	for _, expression := range expressions {
		if !pureExpression(expression) {
			return false
		}
	}
	return true
}
