package compiler

import "fmt"

// Quality limits are fixed for the first version. There is no configuration,
// profile, baseline, budget, or suppression: a repository can be made clean, so
// zero findings is a simpler and stronger invariant than "no new issues".
const (
	// QualityCyclomaticLimit bounds the independent decisions one callable makes.
	QualityCyclomaticLimit = 10
	// QualityCognitiveLimit bounds the nested context one callable demands.
	QualityCognitiveLimit = 15
)

// callableSubject is one authored callable the analyzer measures: a named
// function, a method implementation, or a lambda written inside either. A lambda
// is a separate subject, so its decisions never accrue to the callable that
// contains it.
type callableSubject struct {
	symbol string
	pos    position
	end    position
	body   *blockNode
}

// complexityScore is the pair of metrics one subject earns.
type complexityScore struct {
	cyclomatic int
	cognitive  int
}

// complexityWalker measures one callable body. Every statement and expression
// form is classified explicitly:
//
//	if, for, match           branch or loop: cyclomatic and cognitive
//	catch arm                one handled alternative each
//	&& and ||                 one cyclomatic per operator, one cognitive per run
//	postfix ?                 an implicit early exit
//	else if                   a chain link, not another nesting level
//	using                     transparent: neither score nor nesting
//	lambda                    a separate subject at nesting zero
//	everything else           no contribution, children still walked
//
// A form the walker does not recognize is recorded in unclassified, which makes
// the analysis fail rather than silently under-report new control-flow syntax.
type complexityWalker struct {
	score        complexityScore
	lambdas      []*lambdaExpression
	unclassified []string
}

// measureCallable scores one body and collects the lambdas written directly
// inside it.
func measureCallable(body *blockNode) *complexityWalker {
	walker := &complexityWalker{score: complexityScore{cyclomatic: 1}}
	walker.walkBlock(body, 0)
	return walker
}

func (w *complexityWalker) unknown(node any) {
	w.unclassified = append(w.unclassified, fmt.Sprintf("%T", node))
}

// branch charges one decision at the current nesting depth.
func (w *complexityWalker) branch(nesting int) {
	w.score.cyclomatic++
	w.score.cognitive += 1 + nesting
}

func (w *complexityWalker) walkBlock(block *blockNode, nesting int) {
	if block == nil {
		return
	}
	for _, statement := range block.statements {
		w.walkStatement(statement, nesting)
	}
}

func (w *complexityWalker) walkStatement(statement statementNode, nesting int) {
	switch node := statement.(type) {
	case *letStatement:
		w.walkExpression(node.value, nesting)
	case *asyncLetStatement:
		w.walkExpression(node.call, nesting)
	case *assignmentStatement:
		w.walkExpression(node.value, nesting)
	case *forStatement:
		w.branch(nesting)
		w.walkExpression(node.iterable, nesting)
		w.walkBlock(node.body, nesting+1)
	case *returnStatement:
		w.walkExpression(node.value, nesting)
	case *throwStatement:
		w.walkExpression(node.value, nesting)
	case *expressionStatement:
		w.walkExpression(node.value, nesting)
	case *breakStatement, *continueStatement:
		// Leaving a loop is not another decision; the loop already counted.
	default:
		w.unknown(node)
	}
}

func (w *complexityWalker) walkExpression(expression expressionNode, nesting int) {
	w.walkExpressionIn(expression, nesting, "")
}

// walkExpressionIn carries the short-circuit operator of the enclosing boolean
// node, so a maximal run of one operator costs one cognitive point and changing
// operator starts another run.
func (w *complexityWalker) walkExpressionIn(expression expressionNode, nesting int, boolean string) {
	switch node := expression.(type) {
	case nil:
	case *literalExpression, *templateExpression, *nameExpression, *awaitExpression, *invalidExpression:
		// Reading a value, interpolating one, or awaiting a pending call adds
		// no decision.
	case *tupleExpression:
		w.walkEach(node.elements, nesting)
	case *arrayExpression:
		w.walkEach(node.elements, nesting)
	case *mapExpression:
		for _, entry := range node.entries {
			w.walkExpression(entry.key, nesting)
			w.walkExpression(entry.value, nesting)
		}
	case *rangeExpression:
		w.walkExpression(node.start, nesting)
		w.walkExpression(node.end, nesting)
	case *objectExpression:
		for _, field := range node.fields {
			w.walkExpression(field.value, nesting)
		}
	case *callExpression:
		w.walkExpression(node.callee, nesting)
		w.walkEach(node.args, nesting)
	case *unaryExpression:
		w.walkExpression(node.value, nesting)
	case *binaryExpression:
		w.walkBinary(node, nesting, boolean)
	case *propagateExpression:
		// Propagation is an implicit early exit from the enclosing callable.
		w.score.cyclomatic++
		w.score.cognitive++
		w.walkExpression(node.value, nesting)
	case *resultExpression:
		w.walkExpression(node.value, nesting)
	case *lambdaExpression:
		// A lambda is measured as its own subject, at nesting zero.
		w.lambdas = append(w.lambdas, node)
	case *usingExpression:
		// using is transparent: penalizing Slick's required safe-resource
		// construct would reward unsafe structure.
		w.walkExpression(node.initializer, nesting)
		w.walkBlock(node.body, nesting)
	case *ifExpression:
		w.walkIf(node, nesting)
	case *matchExpression:
		// N alternatives are N-1 additional independent paths.
		w.score.cyclomatic += max(len(node.arms)-1, 0)
		w.score.cognitive += 1 + nesting
		w.walkExpression(node.value, nesting)
		for _, arm := range node.arms {
			w.walkExpression(arm.value, nesting+1)
		}
	case *catchExpression:
		// Each arm is one handled error alternative beside the success path.
		w.score.cyclomatic += len(node.arms)
		w.walkExpression(node.value, nesting)
		for _, arm := range node.arms {
			w.score.cognitive += 1 + nesting
			w.walkExpression(arm.value, nesting+1)
		}
	default:
		w.unknown(node)
	}
}

// walkIf charges one branch. A source-level else if continues its chain instead
// of nesting: the reader follows one ladder, not a tower.
func (w *complexityWalker) walkIf(node *ifExpression, nesting int) {
	w.score.cyclomatic++
	if node.compact {
		w.score.cognitive++
	} else {
		w.score.cognitive += 1 + nesting
	}
	w.walkExpression(node.condition, nesting)
	w.walkBlock(node.thenBlock, nesting+1)
	if chained := compactIfChain(node.elseBlock); chained != nil {
		w.walkIf(chained, nesting)
		return
	}
	w.walkBlock(node.elseBlock, nesting+1)
}

// compactIfChain returns the if an else-if tail holds, which the parser wraps in
// a synthesized single-statement block.
func compactIfChain(block *blockNode) *ifExpression {
	if block == nil || len(block.statements) != 1 {
		return nil
	}
	statement, ok := block.statements[0].(*expressionStatement)
	if !ok {
		return nil
	}
	conditional, ok := statement.value.(*ifExpression)
	if !ok || !conditional.compact {
		return nil
	}
	return conditional
}

func (w *complexityWalker) walkBinary(node *binaryExpression, nesting int, boolean string) {
	if !shortCircuitOperator(node.op) {
		w.walkExpression(node.left, nesting)
		w.walkExpression(node.right, nesting)
		return
	}
	w.score.cyclomatic++
	if node.op != boolean {
		w.score.cognitive++
	}
	w.walkExpressionIn(node.left, nesting, node.op)
	w.walkExpressionIn(node.right, nesting, node.op)
}

func shortCircuitOperator(op string) bool {
	return op == "&&" || op == "||"
}

func (w *complexityWalker) walkEach(expressions []expressionNode, nesting int) {
	for _, expression := range expressions {
		w.walkExpression(expression, nesting)
	}
}

// callableSubjects lists every authored callable of a program, named callables
// in source order and each one's lambdas in the order they are written.
func (p *program) callableSubjects() []callableSubject {
	var subjects []callableSubject
	for _, function := range p.authoredCallables() {
		subjects = append(subjects, callableSubject{
			symbol: callableSymbol(function),
			pos:    function.pos,
			end:    function.end,
			body:   function.ast,
		})
	}
	return subjects
}

// callableSymbol is the canonical name a report uses. A method is named through
// the class it implements, whatever namespace wrote it.
func callableSymbol(function *functionDecl) string {
	if function.receiverCanonical != "" {
		return function.receiverCanonical + "." + function.name
	}
	return function.qualified
}

// lambdaSubject labels a lambda by the callable that contains it and its own
// expression position, which is stable across edits elsewhere in the file.
func lambdaSubject(owner callableSubject, lambda *lambdaExpression) callableSubject {
	return callableSubject{
		symbol: fmt.Sprintf("%s.lambda@%d:%d", owner.symbol, lambda.pos.line, lambda.pos.column),
		pos:    lambda.pos,
		end:    lambda.body.end,
		body:   lambda.body,
	}
}
