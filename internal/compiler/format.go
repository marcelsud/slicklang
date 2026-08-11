package compiler

import (
	"fmt"
	"sort"
	"strings"
	"text/scanner"
)

// Format parses source and returns its canonical, comment-preserving form.
// Diagnostics describe invalid source; err is reserved for formatter invariant
// failures. Formatting never evaluates the program.
func Format(source Source) (string, []Diagnostic, error) {
	parsed, diagnostics := parseFormatSource(source)
	if len(diagnostics) > 0 {
		return "", diagnostics, nil
	}

	formatted := newSourceFormatter(parsed).format()
	verification := source
	verification.Text = formatted
	verified, diagnostics := parseFormatSource(verification)
	if len(diagnostics) > 0 {
		return "", nil, fmt.Errorf("formatter produced invalid source for %s", source.Name)
	}
	if second := newSourceFormatter(verified).format(); second != formatted {
		return "", nil, fmt.Errorf("formatter is not idempotent for %s", source.Name)
	}
	return formatted, nil, nil
}

type formatSource struct {
	source Source
	prog   *program
	tokens []token
}

func parseFormatSource(source Source) (*formatSource, []Diagnostic) {
	tokens, diagnostics := scanTokens(source, true)
	prog := newProgram()
	parsed := &formatSource{source: source, prog: prog, tokens: tokens}
	if len(diagnostics) > 0 {
		return parsed, diagnostics
	}
	code := make([]token, 0, len(tokens))
	for _, token := range tokens {
		if token.kind != scanner.Comment {
			code = append(code, token)
		}
	}
	parseSourceTokens(prog, source, code)
	prog.parseBodies()
	sort.SliceStable(prog.diags, func(i, j int) bool {
		if prog.diags[i].Line != prog.diags[j].Line {
			return prog.diags[i].Line < prog.diags[j].Line
		}
		return prog.diags[i].Column < prog.diags[j].Column
	})
	return parsed, prog.diags
}

type sourcePoint struct {
	line   int
	column int
}

type formatToken struct {
	kind     rune
	text     string
	pos      position
	unary    bool
	operator bool
}

type sourceFormatter struct {
	source                *formatSource
	tokens                []formatToken
	breakBefore           map[sourcePoint]struct{}
	unaryAt               map[sourcePoint]struct{}
	operatorAt            map[sourcePoint]struct{}
	annotatedTargets      map[sourcePoint]struct{}
	annotationStarts      map[sourcePoint]string
	aliasTargets          map[sourcePoint]struct{}
	out                   strings.Builder
	indent                int
	braceDepth            int
	delimiters            []string
	lineStart             bool
	pendingSpace          bool
	trailingNewlines      int
	braceComment          bool
	previous              *formatToken
	previousTop           string
	topComment            bool
	forHeader             bool
	annotationAliasIndent bool
}

func newSourceFormatter(source *formatSource) *sourceFormatter {
	formatter := &sourceFormatter{
		source:           source,
		tokens:           canonicalFormatTokens(mergeFormatTokens(source.tokens)),
		breakBefore:      make(map[sourcePoint]struct{}),
		unaryAt:          make(map[sourcePoint]struct{}),
		annotatedTargets: make(map[sourcePoint]struct{}),
		annotationStarts: make(map[sourcePoint]string),
		aliasTargets:     make(map[sourcePoint]struct{}),
		operatorAt:       make(map[sourcePoint]struct{}),
		lineStart:        true,
	}
	formatter.collectBreaks()
	for index := range formatter.tokens {
		point := sourcePoint{line: formatter.tokens[index].pos.line, column: formatter.tokens[index].pos.column}
		_, formatter.tokens[index].unary = formatter.unaryAt[point]
		_, formatter.tokens[index].operator = formatter.operatorAt[point]
	}
	return formatter
}

func mergeFormatTokens(tokens []token) []formatToken {
	merged := make([]formatToken, 0, len(tokens))
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		if current.kind == scanner.EOF {
			continue
		}
		text := current.text
		if index+1 < len(tokens) {
			pair := text + tokens[index+1].text
			switch pair {
			// A > that closes a type argument list stands alone: in
			// "Broken<string> => ..." the arrow owns the = and the >, so
			// merging >= first would split the arrow apart.
			case ">=":
				if index+2 >= len(tokens) || tokens[index+2].text != ">" {
					text = pair
					index++
				}
			case "->", "=>", "==", "!=", "<=", "&&", "||", "..":
				text = pair
				index++
			}
		}
		merged = append(merged, formatToken{kind: current.kind, text: text, pos: current.pos})
	}
	return merged
}

func canonicalFormatTokens(tokens []formatToken) []formatToken {
	for index := range tokens {
		if tokens[index].text != "use" {
			continue
		}
		current := nextFormatCodeToken(tokens, index+1)
		if current >= len(tokens) || tokens[current].kind != scanner.Ident {
			continue
		}
		finalName := tokens[current].text
		current = nextFormatCodeToken(tokens, current+1)
		for current < len(tokens) && tokens[current].text == "." {
			current = nextFormatCodeToken(tokens, current+1)
			if current >= len(tokens) || tokens[current].kind != scanner.Ident {
				break
			}
			finalName = tokens[current].text
			current = nextFormatCodeToken(tokens, current+1)
		}
		if current >= len(tokens) || tokens[current].text != "as" {
			continue
		}
		alias := nextFormatCodeToken(tokens, current+1)
		if alias < len(tokens) && tokens[alias].text == finalName {
			tokens[current].text = ""
			tokens[alias].text = ""
		}
	}
	canonical := tokens[:0]
	for _, token := range tokens {
		if token.text != "" {
			canonical = append(canonical, token)
		}
	}
	return canonical
}

func nextFormatCodeToken(tokens []formatToken, index int) int {
	for index < len(tokens) && tokens[index].kind == scanner.Comment {
		index++
	}
	return index
}

func (f *sourceFormatter) collectBreaks() {
	for _, classes := range []map[string]*classDecl{f.source.prog.classes, f.source.prog.genericClasses} {
		for _, class := range classes {
			for _, field := range class.fields {
				f.addBreak(field.pos)
			}
		}
	}
	for _, union := range f.source.prog.unions {
		for _, name := range union.order {
			f.addBreak(union.variants[name].pos)
		}
	}
	for _, constant := range f.source.prog.constants {
		f.collectExpression(constant.ast)
	}
	for _, functions := range []map[string]*functionDecl{f.source.prog.functions, f.source.prog.genericFunctions} {
		for _, function := range functions {
			f.collectBlock(function.ast)
		}
	}
	for _, implementations := range [][]*functionDecl{f.source.prog.methodImpls, f.source.prog.genericMethodImpls} {
		for _, implementation := range implementations {
			f.collectBlock(implementation.ast)
		}
	}
	for _, target := range f.source.prog.annotationTargets() {
		if len(target.annotations) == 0 {
			continue
		}
		if target.kind != annotationTargetParameter {
			for _, annotation := range target.annotations {
				f.addBreak(annotation.pos)
			}
			first := target.annotations[0].pos
			kind := "class"
			if target.kind == annotationTargetFunction || target.kind == annotationTargetMethod {
				kind = "function"
			}
			f.annotationStarts[sourcePoint{line: first.line, column: first.column}] = kind
		}
		point, found := f.annotationTargetPoint(target)
		if found {
			f.breakBefore[point] = struct{}{}
			f.annotatedTargets[point] = struct{}{}
		}
	}
	for _, annotation := range f.source.prog.annotations {
		if annotation.terminal != nil || annotation.target == nil {
			continue
		}
		f.addBreak(annotation.target.pos)
		f.aliasTargets[sourcePoint{line: annotation.target.pos.line, column: annotation.target.pos.column}] = struct{}{}
	}
}

func (f *sourceFormatter) addBreak(pos position) {
	f.breakBefore[sourcePoint{line: pos.line, column: pos.column}] = struct{}{}
}

func (f *sourceFormatter) annotationTargetPoint(target annotationTargetRef) (sourcePoint, bool) {
	if target.kind == annotationTargetParameter {
		return sourcePoint{}, false
	}
	for index, token := range f.tokens {
		if token.pos.line != target.pos.line || token.pos.column != target.pos.column {
			continue
		}
		for previous := index - 1; previous >= 0; previous-- {
			if f.tokens[previous].kind == scanner.Comment {
				continue
			}
			if f.tokens[previous].text == "class" || f.tokens[previous].text == "interface" || f.tokens[previous].text == "function" {
				position := f.tokens[previous].pos
				return sourcePoint{line: position.line, column: position.column}, true
			}
			break
		}
	}
	return sourcePoint{}, false
}

func (f *sourceFormatter) collectBlock(block *blockNode) {
	if block == nil {
		return
	}
	for _, statement := range block.statements {
		compactElseIf := false
		if expression, ok := statement.(*expressionStatement); ok {
			if conditional, ok := expression.value.(*ifExpression); ok {
				compactElseIf = conditional.compact
			}
		}
		if !compactElseIf {
			f.addBreak(statement.statementPos())
		}
		switch node := statement.(type) {
		case *letStatement:
			f.collectExpression(node.value)
		case *asyncLetStatement:
			f.collectExpression(node.call)
		case *assignmentStatement:
			f.collectExpression(node.value)
		case *forStatement:
			f.collectExpression(node.iterable)
			f.collectBlock(node.body)
		case *throwStatement:
			f.collectExpression(node.value)
		case *returnStatement:
			f.collectExpression(node.value)
		case *expressionStatement:
			f.collectExpression(node.value)
		}
	}
}

func (f *sourceFormatter) collectExpression(expression expressionNode) {
	if expression == nil {
		return
	}
	switch node := expression.(type) {
	case *tupleExpression:
		for _, element := range node.elements {
			f.collectExpression(element)
		}
	case *arrayExpression:
		for _, element := range node.elements {
			f.collectExpression(element)
		}
	case *mapExpression:
		for _, entry := range node.entries {
			f.addBreak(entry.pos)
			f.collectExpression(entry.key)
			f.collectExpression(entry.value)
		}
	case *rangeExpression:
		f.collectExpression(node.start)
		f.collectExpression(node.end)
	case *objectExpression:
		for _, field := range node.fields {
			f.addBreak(field.pos)
			f.collectExpression(field.value)
		}
	case *lambdaExpression:
		f.collectBlock(node.body)
	case *callExpression:
		f.collectExpression(node.callee)
		for _, argument := range node.args {
			f.collectExpression(argument)
		}
	case *unaryExpression:
		f.unaryAt[sourcePoint{line: node.pos.line, column: node.pos.column}] = struct{}{}
		f.collectExpression(node.value)
	case *binaryExpression:
		f.collectExpression(node.left)
		f.collectExpression(node.right)
		f.operatorAt[sourcePoint{line: node.opPos.line, column: node.opPos.column}] = struct{}{}
	case *ifExpression:
		f.collectExpression(node.condition)
		f.collectBlock(node.thenBlock)
		f.collectBlock(node.elseBlock)
	case *catchExpression:
		f.collectExpression(node.value)
		for _, arm := range node.arms {
			f.addBreak(arm.errorType.pos)
			f.collectExpression(arm.value)
		}
	case *resultExpression:
		f.collectExpression(node.value)
	case *propagateExpression:
		f.collectExpression(node.value)
	case *usingExpression:
		f.collectExpression(node.initializer)
		f.collectBlock(node.body)
	case *matchExpression:
		f.collectExpression(node.value)
		for _, arm := range node.arms {
			f.addBreak(arm.pos)
			f.collectExpression(arm.value)
		}
	}
}

func (f *sourceFormatter) format() string {
	for index := 0; index < len(f.tokens); index++ {
		token := f.tokens[index]
		if token.kind == scanner.Comment {
			f.writeComment(index)
			continue
		}
		if token.text == ";" {
			continue
		}
		if token.text == "," && f.inBrace() && !f.forHeader {
			continue
		}
		if token.text == "{" && index+1 < len(f.tokens) && f.tokens[index+1].text == "}" {
			f.writeOpeningBrace()
			f.writeText("}")
			f.previous = &f.tokens[index+1]
			index++
			continue
		}
		f.braceComment = token.text == "{" && index+1 < len(f.tokens) &&
			f.tokens[index+1].kind == scanner.Comment && f.tokens[index+1].pos.line == token.pos.line
		f.writeCode(token)
	}
	f.newline()
	return f.out.String()
}

func (f *sourceFormatter) writeCode(token formatToken) {
	point := sourcePoint{line: token.pos.line, column: token.pos.column}
	if _, aliasTarget := f.aliasTargets[point]; aliasTarget && !f.lineStart {
		f.newline()
		f.indent++
		f.annotationAliasIndent = true
	}
	if token.text == "@" && f.braceDepth == 0 && f.annotationAliasIndent {
		if _, aliasTarget := f.aliasTargets[point]; !aliasTarget {
			f.indent--
			f.annotationAliasIndent = false
			f.blankline()
		}
	}
	if _, ok := f.breakBefore[point]; ok && !f.lineStart {
		f.newline()
	}
	if kind, ok := f.annotationStarts[point]; ok && f.braceDepth == 0 {
		f.startTopDeclaration(kind)
	}
	if f.braceDepth == 0 && isTopDeclaration(token.text) {
		if f.annotationAliasIndent {
			f.indent--
			f.annotationAliasIndent = false
		}
		if _, annotated := f.annotatedTargets[point]; !annotated {
			f.startTopDeclaration(token.text)
		} else {
			f.previousTop = token.text
			f.topComment = false
		}
	}
	if token.text == "function" && f.braceDepth > 0 && !f.lineStart {
		f.newline()
	}
	switch token.text {
	case "{":
		f.writeOpeningBrace()
		f.braceDepth++
		f.delimiters = append(f.delimiters, "{")
		f.indent++
		if !f.braceComment {
			f.newline()
		}
	case "}":
		if f.indent > 0 {
			f.indent--
		}
		if f.braceDepth > 0 {
			f.braceDepth--
		}
		f.popDelimiter("{")
		if !f.lineStart {
			f.newline()
		}
		f.writeText("}")
	case "(", "[":
		f.writeToken(token)
		f.delimiters = append(f.delimiters, token.text)
	case "<":
		f.writeToken(token)
		if !token.operator {
			f.delimiters = append(f.delimiters, token.text)
		}
	case ")":
		f.popDelimiter("(")
		f.writeToken(token)
	case "]":
		f.popDelimiter("[")
		f.writeToken(token)
	case ">":
		if !token.operator {
			f.popDelimiter("<")
		}
		f.writeToken(token)
	default:
		f.writeToken(token)
	}

	if token.text == "for" {
		f.forHeader = true
	} else if token.text == "in" && f.forHeader {
		f.forHeader = false
	}
	f.previous = &token
}

func (f *sourceFormatter) writeOpeningBrace() {
	if !f.lineStart {
		f.space()
	}
	f.writeText("{")
}

func (f *sourceFormatter) writeToken(token formatToken) {
	if f.needsSpace(token) {
		f.space()
	}
	f.writeText(token.text)
}

func (f *sourceFormatter) needsSpace(current formatToken) bool {
	if f.lineStart || f.previous == nil {
		return false
	}
	previous := f.previous.text
	if current.unary {
		return !f.previous.unary && previous != "(" && previous != "["
	}
	if f.previous.unary {
		return false
	}
	if current.operator || f.previous.operator {
		return true
	}
	switch current.text {
	case ")", "]", ">", ",", ":", ".", "?":
		return false
	case "(":
		return previous != "(" && previous != "[" && previous != "<" && !isSuffixTarget(*f.previous)
	case "[":
		return previous != "(" && previous != "[" && previous != "<" && !isSuffixTarget(*f.previous)
	case "<":
		return false
	}
	switch previous {
	case "(", "[", "<", ".", "@":
		return false
	}
	return true
}

func isSuffixTarget(token formatToken) bool {
	if token.kind == scanner.Ident {
		switch token.text {
		case "return", "throw", "in", "let", "if", "else", "catch", "match", "for", "using":
			return false
		default:
			return true
		}
	}
	return token.text == ")" || token.text == "]" || token.text == ">" || token.text == "?"
}

func (f *sourceFormatter) writeComment(index int) {
	token := f.tokens[index]
	if f.braceDepth == 0 && f.annotationAliasIndent {
		f.indent--
		f.annotationAliasIndent = false
		f.blankline()
	}
	inline := f.previous != nil && f.previous.pos.line == token.pos.line && !f.lineStart
	if inline {
		f.space()
	} else {
		if !f.lineStart {
			f.newline()
		}
		if f.braceDepth == 0 && f.previousTop != "" && !f.topComment {
			f.blankline()
			f.topComment = true
		}
	}
	f.writeText(normalizeLineEndings(token.text))
	if f.braceComment {
		f.braceComment = false
		if f.blankLineAfterComment(index) {
			f.blankline()
		} else {
			f.newline()
		}
	} else if strings.HasPrefix(token.text, "//") || f.nextTokenStartsLaterLine(index) {
		if f.blankLineAfterComment(index) {
			f.blankline()
		} else {
			f.newline()
		}
	} else {
		f.space()
	}
}

func (f *sourceFormatter) nextTokenStartsLaterLine(index int) bool {
	return index+1 >= len(f.tokens) || f.tokens[index+1].pos.line > f.commentEndLine(index)
}

func (f *sourceFormatter) blankLineAfterComment(index int) bool {
	return index+1 < len(f.tokens) && f.tokens[index+1].pos.line > f.commentEndLine(index)+1
}

func (f *sourceFormatter) commentEndLine(index int) int {
	text := normalizeLineEndings(f.tokens[index].text)
	if strings.HasPrefix(text, "//") {
		text = strings.TrimSuffix(text, "\n")
	}
	return f.tokens[index].pos.line + strings.Count(text, "\n")
}

func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func (f *sourceFormatter) startTopDeclaration(kind string) {
	if f.previousTop != "" && !f.topComment {
		if kind == "use" && f.previousTop == "use" {
			f.newline()
		} else {
			f.blankline()
		}
	}
	f.previousTop = kind
	f.topComment = false
}

func isTopDeclaration(text string) bool {
	switch text {
	case "use", "const", "annotation", "class", "interface", "union", "function":
		return true
	default:
		return false
	}
}

func (f *sourceFormatter) inBrace() bool {
	return len(f.delimiters) > 0 && f.delimiters[len(f.delimiters)-1] == "{"
}

func (f *sourceFormatter) popDelimiter(want string) {
	if len(f.delimiters) == 0 {
		return
	}
	if f.delimiters[len(f.delimiters)-1] == want {
		f.delimiters = f.delimiters[:len(f.delimiters)-1]
	}
}

func (f *sourceFormatter) writeText(text string) {
	if text == "" {
		return
	}
	if f.lineStart {
		f.out.WriteString(strings.Repeat("    ", f.indent))
		f.lineStart = false
	}
	if f.pendingSpace {
		f.out.WriteByte(' ')
		f.pendingSpace = false
	}
	f.out.WriteString(text)
	f.trailingNewlines = 0
	for index := len(text) - 1; index >= 0 && text[index] == '\n'; index-- {
		f.trailingNewlines++
	}
	f.lineStart = f.trailingNewlines > 0
}

func (f *sourceFormatter) space() {
	if !f.lineStart && f.out.Len() > 0 {
		f.pendingSpace = true
	}
}

func (f *sourceFormatter) newline() {
	f.pendingSpace = false
	if f.out.Len() == 0 || !f.lineStart {
		f.out.WriteByte('\n')
		f.trailingNewlines = 1
	}
	f.lineStart = true
}

func (f *sourceFormatter) blankline() {
	f.newline()
	if f.trailingNewlines < 2 {
		f.out.WriteByte('\n')
		f.trailingNewlines++
	}
	f.lineStart = true
}
