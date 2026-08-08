package compiler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/scanner"
	"unicode"
)

var ErrNoSources = errors.New("no .slk files found")

type Source struct {
	Name      string
	Namespace string
	Text      string
}

type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Code    string
	Message string
}

type position struct {
	file   string
	line   int
	column int
}

type token struct {
	kind rune
	text string
	pos  position
}

type typeRef struct {
	name string
	pos  position
}

type paramDecl struct {
	name string
	typ  typeRef
}

type fieldDecl struct {
	name string
	typ  typeRef
	pos  position
}

type aliasDecl struct {
	name      string
	target    string
	namespace string
	pos       position
}

type extensionPolicy string

const (
	extensionNone      extensionPolicy = "none"
	extensionNamespace extensionPolicy = "namespace"
	extensionGlobal    extensionPolicy = "global"
)

type methodSignature struct {
	name           string
	namespace      string
	ownerNamespace string
	aliases        map[string]aliasDecl
	params         []paramDecl
	result         typeRef
	throws         []typeRef
	throwSet       map[string]struct{}
	pos            position
}

type classDecl struct {
	name            string
	qualified       string
	namespace       string
	aliases         map[string]aliasDecl
	isError         bool
	extension       extensionPolicy
	fields          map[string]fieldDecl
	methods         map[string]*methodSignature
	effective       map[string]*methodSignature
	implementations map[string]*functionDecl
	pos             position
}

type interfaceDecl struct {
	name      string
	qualified string
	namespace string
	methods   map[string]*methodSignature
	pos       position
}

type functionDecl struct {
	name              string
	qualified         string
	namespace         string
	aliases           map[string]aliasDecl
	params            []paramDecl
	result            typeRef
	throws            []typeRef
	throwSet          map[string]struct{}
	body              []token
	ast               *blockNode
	receiver          typeRef
	receiverCanonical string
	inline            bool
	pos               position
}

type program struct {
	aliases     []aliasDecl
	classes     map[string]*classDecl
	interfaces  map[string]*interfaceDecl
	functions   map[string]*functionDecl
	methodImpls []*functionDecl
	diags       []Diagnostic
}

func CheckPath(path string) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	return Check(sources), nil
}

func loadSources(path string) ([]Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		if filepath.Ext(path) != ".slk" {
			return nil, fmt.Errorf("%s is not a .slk file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return []Source{{Name: filepath.Base(path), Namespace: "root", Text: string(data)}}, nil
	}

	var sources []Source
	err = filepath.WalkDir(path, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".slk" {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, file)
		if err != nil {
			return err
		}
		dir := filepath.Dir(rel)
		namespace := "root"
		if dir != "." {
			namespace += "." + strings.ReplaceAll(filepath.ToSlash(dir), "/", ".")
		}
		sources = append(sources, Source{Name: filepath.ToSlash(rel), Namespace: namespace, Text: string(data)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, ErrNoSources
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources, nil
}

func Check(sources []Source) []Diagnostic {
	_, diagnostics := compile(sources)
	return diagnostics
}

func compile(sources []Source) (*program, []Diagnostic) {
	prog := &program{
		classes:    make(map[string]*classDecl),
		interfaces: make(map[string]*interfaceDecl),
		functions:  make(map[string]*functionDecl),
	}
	for _, source := range sources {
		if !validNamespace(source.Namespace) {
			prog.add(position{file: source.Name, line: 1, column: 1}, "SLK100", "invalid namespace %q", source.Namespace)
			continue
		}
		parseSource(prog, source)
	}
	prog.check()
	sort.SliceStable(prog.diags, func(i, j int) bool {
		a, b := prog.diags[i], prog.diags[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return prog, prog.diags
}

func parseSource(prog *program, source Source) {
	p := parser{
		prog:    prog,
		source:  source,
		tokens:  lex(source),
		aliases: make(map[string]aliasDecl),
	}
	for !p.atEnd() {
		switch {
		case p.accept("use"):
			p.parseUse()
		case p.accept("class"):
			p.parseClass()
		case p.accept("interface"):
			p.parseInterface()
		case p.accept("function"):
			p.parseFunction()
		default:
			tok := p.current()
			p.error(tok.pos, "expected 'use', 'class', 'interface', or 'function', found %q", tok.text)
			p.advance()
		}
	}
}

func lex(source Source) []token {
	var s scanner.Scanner
	s.Init(strings.NewReader(source.Text))
	s.Filename = source.Name
	s.Mode = scanner.GoTokens

	var tokens []token
	for kind := s.Scan(); kind != scanner.EOF; kind = s.Scan() {
		scanned := token{
			kind: kind,
			text: s.TokenText(),
			pos:  position{file: source.Name, line: s.Position.Line, column: s.Position.Column},
		}
		if len(tokens) > 0 && kind == scanner.Float && strings.HasPrefix(scanned.text, ".") {
			previous := &tokens[len(tokens)-1]
			if previous.kind == scanner.Float && strings.HasSuffix(previous.text, ".") &&
				previous.pos.line == scanned.pos.line &&
				previous.pos.column+len(previous.text) == scanned.pos.column {
				previous.text = strings.TrimSuffix(previous.text, ".")
				previous.kind = scanner.Int
				tokens = append(tokens,
					token{kind: '.', text: ".", pos: position{file: source.Name, line: previous.pos.line, column: previous.pos.column + len(previous.text)}},
					token{kind: '.', text: ".", pos: scanned.pos},
					token{kind: scanner.Int, text: strings.TrimPrefix(scanned.text, "."), pos: position{file: source.Name, line: scanned.pos.line, column: scanned.pos.column + 1}},
				)
				continue
			}
		}
		tokens = append(tokens, scanned)
	}
	line := 1
	if len(tokens) > 0 {
		line = tokens[len(tokens)-1].pos.line
	}
	return append(tokens, token{kind: scanner.EOF, pos: position{file: source.Name, line: line, column: 1}})
}

type parser struct {
	prog    *program
	source  Source
	tokens  []token
	aliases map[string]aliasDecl
	index   int
}

func (p *parser) parseUse() {
	target, next, ok := readQualified(p.tokens, p.index)
	if !ok || !strings.HasPrefix(target.name, "root.") {
		p.error(p.current().pos, "use target must be an absolute root namespace path")
		return
	}
	p.index = next
	if !p.accept("as") {
		p.error(p.current().pos, "expected 'as' in use declaration")
		return
	}
	name, ok := p.expectIdent("use alias")
	if !ok {
		return
	}
	p.accept(";")
	if previous, exists := p.aliases[name.text]; exists {
		p.error(name.pos, "duplicate alias %s; first declared at %s:%d:%d", name.text, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	alias := aliasDecl{name: name.text, target: target.name, namespace: p.source.Namespace, pos: name.pos}
	p.aliases[name.text] = alias
	p.prog.aliases = append(p.prog.aliases, alias)
}

func (p *parser) parseClass() {
	name, ok := p.expectIdent("class name")
	if !ok {
		return
	}
	if isReservedTypeName(name.text) {
		p.error(name.pos, "class name %s is reserved by Result", name.text)
	}
	class := &classDecl{
		name:            name.text,
		qualified:       qualify(p.source.Namespace, name.text),
		namespace:       p.source.Namespace,
		extension:       extensionNamespace,
		aliases:         p.aliases,
		fields:          make(map[string]fieldDecl),
		methods:         make(map[string]*methodSignature),
		effective:       make(map[string]*methodSignature),
		implementations: make(map[string]*functionDecl),
		pos:             name.pos,
	}

	for !p.atEnd() && p.current().text != "{" {
		switch {
		case p.accept("extension"):
			if !p.accept("(") {
				p.error(p.current().pos, "expected '(' after extension")
				continue
			}
			mode, ok := p.expectIdent("extension policy")
			if ok {
				switch extensionPolicy(mode.text) {
				case extensionNone, extensionNamespace, extensionGlobal:
					class.extension = extensionPolicy(mode.text)
				default:
					p.error(mode.pos, "extension policy must be none, namespace, or global")
				}
			}
			if !p.accept(")") {
				p.error(p.current().pos, "expected ')' after extension policy")
			}
		case p.accept("implements"):
			for {
				implemented, next, ok := readQualified(p.tokens, p.index)
				if !ok {
					p.error(p.current().pos, "expected implemented type")
					break
				}
				p.index = next
				if implemented.name == "Error" {
					class.isError = true
				}
				if !p.accept(",") {
					break
				}
			}
		default:
			p.error(p.current().pos, "expected extension policy, implements clause, or class body")
			p.advance()
		}
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected class body")
		return
	}

	registered := true
	if previous, exists := p.prog.classes[class.qualified]; exists {
		p.error(name.pos, "duplicate class %s; first declared at %s:%d:%d", class.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		registered = false
	} else {
		p.prog.classes[class.qualified] = class
	}

	for !p.atEnd() && p.current().text != "}" {
		if p.accept("function") {
			p.parseClassMethod(class, registered)
		} else {
			p.parseClassField(class)
		}
	}
	if !p.accept("}") {
		p.error(p.current().pos, "unterminated class body")
	}
}

func (p *parser) parseClassMethod(class *classDecl, registered bool) {
	name, ok := p.expectIdent("method name")
	if !ok {
		return
	}
	params, result, throws, body, hasBody, ok := p.parseCallableTail()
	if !ok {
		return
	}
	signature := &methodSignature{
		name:           name.text,
		namespace:      class.namespace,
		ownerNamespace: class.namespace,
		aliases:        p.aliases,
		params:         params,
		result:         result,
		throws:         throws,
		pos:            name.pos,
	}
	if previous, exists := class.methods[name.text]; exists {
		p.error(name.pos, "duplicate method declaration %s.%s; first declared at %s:%d:%d", class.qualified, name.text, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	class.methods[name.text] = signature
	if registered && hasBody {
		p.prog.methodImpls = append(p.prog.methodImpls, &functionDecl{
			name:              name.text,
			qualified:         class.qualified + "." + name.text,
			namespace:         class.namespace,
			aliases:           p.aliases,
			params:            params,
			result:            result,
			throws:            throws,
			body:              body,
			receiver:          typeRef{name: class.qualified, pos: name.pos},
			receiverCanonical: class.qualified,
			inline:            true,
			pos:               name.pos,
		})
	}
}

func (p *parser) parseClassField(class *classDecl) {
	name, ok := p.expectIdent("field or method declaration")
	if !ok {
		p.advance()
		return
	}
	if !p.accept(":") {
		p.error(p.current().pos, "expected ':' after field name")
		return
	}
	typ, ok := p.parseType()
	if !ok {
		return
	}
	if previous, exists := class.fields[name.text]; exists {
		p.error(name.pos, "duplicate field %s.%s; first declared at %s:%d:%d", class.qualified, name.text, previous.pos.file, previous.pos.line, previous.pos.column)
	} else {
		class.fields[name.text] = fieldDecl{name: name.text, typ: typ, pos: name.pos}
	}
	p.accept(",")
	p.accept(";")
}

func (p *parser) parseInterface() {
	name, ok := p.expectIdent("interface name")
	if !ok {
		return
	}
	if isReservedTypeName(name.text) {
		p.error(name.pos, "interface name %s is reserved by Result", name.text)
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected interface body")
		return
	}
	decl := &interfaceDecl{
		name:      name.text,
		qualified: qualify(p.source.Namespace, name.text),
		namespace: p.source.Namespace,
		methods:   make(map[string]*methodSignature),
		pos:       name.pos,
	}
	registered := true
	if previous, exists := p.prog.interfaces[decl.qualified]; exists {
		p.error(name.pos, "duplicate interface %s; first declared at %s:%d:%d", decl.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		registered = false
	} else {
		p.prog.interfaces[decl.qualified] = decl
	}
	for !p.atEnd() && p.current().text != "}" {
		if !p.accept("function") {
			p.error(p.current().pos, "interfaces may contain only method declarations")
			p.advance()
			continue
		}
		methodName, ok := p.expectIdent("interface method name")
		if !ok {
			continue
		}
		params, result, throws, _, hasBody, ok := p.parseCallableTail()
		if !ok {
			continue
		}
		if hasBody {
			p.error(methodName.pos, "interface method %s must not have a body", methodName.text)
		}
		signature := &methodSignature{
			name:           methodName.text,
			namespace:      p.source.Namespace,
			ownerNamespace: p.source.Namespace,
			aliases:        p.aliases,
			params:         params,
			result:         result,
			throws:         throws,
			pos:            methodName.pos,
		}
		if previous, exists := decl.methods[methodName.text]; exists {
			p.error(methodName.pos, "duplicate interface method %s.%s; first declared at %s:%d:%d", decl.qualified, methodName.text, previous.pos.file, previous.pos.line, previous.pos.column)
		} else if registered {
			decl.methods[methodName.text] = signature
		}
	}
	if !p.accept("}") {
		p.error(p.current().pos, "unterminated interface body")
	}
}

func (p *parser) parseFunction() {
	ref, next, ok := readQualified(p.tokens, p.index)
	if !ok {
		p.error(p.current().pos, "expected function or receiver method name")
		return
	}
	p.index = next
	params, result, throws, body, hasBody, ok := p.parseCallableTail()
	if !ok {
		return
	}
	if !hasBody {
		p.error(ref.pos, "function %s must have a body", ref.name)
		return
	}
	parts := strings.Split(ref.name, ".")
	if len(parts) == 1 && isIterableBuiltin(ref.name) {
		p.error(ref.pos, "function name %s is reserved by the iterable standard library", ref.name)
		return
	}
	if len(parts) == 1 && isResultConstructor(ref.name) {
		p.error(ref.pos, "function name %s is reserved by Result", ref.name)
		return
	}
	if len(parts) == 1 {
		qualified := qualify(p.source.Namespace, ref.name)
		if previous, exists := p.prog.functions[qualified]; exists {
			p.error(ref.pos, "duplicate function %s; first declared at %s:%d:%d", qualified, previous.pos.file, previous.pos.line, previous.pos.column)
			return
		}
		p.prog.functions[qualified] = &functionDecl{
			name:      ref.name,
			qualified: qualified,
			namespace: p.source.Namespace,
			aliases:   p.aliases,
			params:    params,
			result:    result,
			throws:    throws,
			body:      body,
			pos:       ref.pos,
		}
		return
	}

	methodName := parts[len(parts)-1]
	receiverName := strings.Join(parts[:len(parts)-1], ".")
	p.prog.methodImpls = append(p.prog.methodImpls, &functionDecl{
		name:      methodName,
		qualified: receiverName + "." + methodName,
		namespace: p.source.Namespace,
		aliases:   p.aliases,
		params:    params,
		result:    result,
		throws:    throws,
		body:      body,
		receiver:  typeRef{name: receiverName, pos: ref.pos},
		pos:       ref.pos,
	})
}

func (p *parser) parseCallableTail() ([]paramDecl, typeRef, []typeRef, []token, bool, bool) {
	params, ok := p.parseParams()
	if !ok {
		return nil, typeRef{}, nil, nil, false, false
	}
	if !p.accept("-") || !p.accept(">") {
		p.error(p.current().pos, "expected '->' and a return type")
		return nil, typeRef{}, nil, nil, false, false
	}
	result, ok := p.parseType()
	if !ok {
		return nil, typeRef{}, nil, nil, false, false
	}
	throws := p.parseThrows()
	if !p.accept("{") {
		p.accept(";")
		return params, result, throws, nil, false, true
	}
	bodyStart := p.index
	p.skipBlock()
	bodyEnd := p.index - 1
	if bodyEnd < bodyStart {
		bodyEnd = bodyStart
	}
	return params, result, throws, p.tokens[bodyStart:bodyEnd], true, true
}

func (p *parser) parseParams() ([]paramDecl, bool) {
	if !p.accept("(") {
		p.error(p.current().pos, "expected parameter list")
		return nil, false
	}
	var params []paramDecl
	for !p.atEnd() && p.current().text != ")" {
		name, ok := p.expectIdent("parameter name")
		if !ok {
			return nil, false
		}
		if !p.accept(":") {
			p.error(p.current().pos, "expected ':' after parameter name")
			return nil, false
		}
		typ, ok := p.parseType()
		if !ok {
			return nil, false
		}
		params = append(params, paramDecl{name: name.text, typ: typ})
		if !p.accept(",") {
			break
		}
	}
	if !p.accept(")") {
		p.error(p.current().pos, "expected ')' after parameters")
		return nil, false
	}
	return params, true
}

func (p *parser) parseType() (typeRef, bool) {
	start := p.index
	pos := p.current().pos
	if p.current().text == "(" {
		close := matching(p.tokens, p.index, "(", ")")
		if close < 0 {
			p.error(pos, "unterminated tuple type")
			return typeRef{}, false
		}
		p.index = close + 1
	} else {
		_, next, ok := readQualified(p.tokens, p.index)
		if !ok {
			p.error(pos, "expected type")
			return typeRef{}, false
		}
		p.index = next
	}
	if p.current().text == "<" {
		close := matching(p.tokens, p.index, "<", ">")
		if close < 0 {
			p.error(p.current().pos, "unterminated generic type")
			return typeRef{}, false
		}
		p.index = close + 1
	}
	for p.current().text == "?" || (p.current().text == "[" && p.index+1 < len(p.tokens) && p.tokens[p.index+1].text == "]") {
		if p.accept("?") {
			continue
		}
		p.index += 2
	}
	for p.accept("|") {
		if _, ok := p.parseType(); !ok {
			return typeRef{}, false
		}
	}
	var text strings.Builder
	for _, tok := range p.tokens[start:p.index] {
		text.WriteString(tok.text)
	}
	return typeRef{name: text.String(), pos: pos}, true
}

func (p *parser) parseThrows() []typeRef {
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

func (p *parser) skipBlock() {
	p.skipDelimited("{", "}")
}

func (p *parser) skipDelimited(open, close string) {
	depth := 1
	for !p.atEnd() && depth > 0 {
		switch p.current().text {
		case open:
			depth++
		case close:
			depth--
		}
		p.advance()
	}
	if depth != 0 {
		p.error(p.current().pos, "unterminated %s block", open)
	}
}

func (p *parser) expectIdent(label string) (token, bool) {
	tok := p.current()
	if tok.kind != scanner.Ident {
		p.error(tok.pos, "expected %s", label)
		return token{}, false
	}
	p.advance()
	return tok, true
}

func (p *parser) accept(text string) bool {
	if p.current().text != text {
		return false
	}
	p.advance()
	return true
}

func (p *parser) current() token {
	return p.tokens[p.index]
}

func (p *parser) advance() {
	if !p.atEnd() {
		p.index++
	}
}

func (p *parser) atEnd() bool {
	return p.current().kind == scanner.EOF
}

func (p *parser) error(pos position, format string, args ...any) {
	p.prog.add(pos, "SLK001", format, args...)
}

type qualifiedRef struct {
	name string
	pos  position
}

func readQualified(tokens []token, start int) (qualifiedRef, int, bool) {
	if start >= len(tokens) || tokens[start].kind != scanner.Ident {
		return qualifiedRef{}, start, false
	}
	parts := []string{tokens[start].text}
	end := start + 1
	for end+1 < len(tokens) && tokens[end].text == "." && tokens[end+1].kind == scanner.Ident {
		parts = append(parts, tokens[end+1].text)
		end += 2
	}
	return qualifiedRef{name: strings.Join(parts, "."), pos: tokens[start].pos}, end, true
}

func (p *program) check() {
	p.parseBodies()
	p.checkAliases()
	p.checkVisibility()
	p.resolveThrowSets()
	p.linkMethods()
	for _, function := range p.functions {
		p.checkFunction(function)
	}
	for _, implementation := range p.methodImpls {
		p.checkFunction(implementation)
	}
}

func (p *program) checkAliases() {
	for _, alias := range p.aliases {
		class := p.classes[alias.target]
		iface := p.interfaces[alias.target]
		function := p.functions[alias.target]
		if class == nil && iface == nil && function == nil {
			p.add(alias.pos, "SLK204", "alias target %s does not exist", alias.target)
		} else if class != nil {
			p.requireAccess(alias.pos, alias.namespace, class.namespace, class.name, "class")
		} else if iface != nil {
			p.requireAccess(alias.pos, alias.namespace, iface.namespace, iface.name, "interface")
		} else {
			p.requireAccess(alias.pos, alias.namespace, function.namespace, function.name, "function")
		}
		local := qualify(alias.namespace, alias.name)
		_, localClassExists := p.classes[local]
		_, localInterfaceExists := p.interfaces[local]
		_, localFunctionExists := p.functions[local]
		if alias.target != local && (localClassExists || localInterfaceExists || localFunctionExists) {
			p.add(alias.pos, "SLK204", "alias %s conflicts with a declaration in %s", alias.name, alias.namespace)
		}
	}
}

func (p *program) checkFunction(function *functionDecl) {
	p.checkASTFunction(function)
}

func matching(tokens []token, start int, open, close string) int {
	if start >= len(tokens) || tokens[start].text != open {
		return -1
	}
	depth := 1
	for i := start + 1; i < len(tokens); i++ {
		switch tokens[i].text {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (p *program) resolveFunction(function *functionDecl, name string) *functionDecl {
	return p.functions[p.resolveName(function, name)]
}

func (p *program) resolveName(function *functionDecl, name string) string {
	if strings.HasPrefix(name, "root.") {
		return name
	}
	if alias, ok := function.aliases[name]; ok {
		return alias.target
	}
	return qualify(function.namespace, name)
}

func (p *program) add(pos position, code, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{
		File:    pos.file,
		Line:    pos.line,
		Column:  pos.column,
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	})
}

func qualify(namespace, name string) string {
	return namespace + "." + name
}

func containsError(set map[string]struct{}, name string) bool {
	if _, ok := set["Error"]; ok {
		return true
	}
	_, ok := set[name]
	return ok
}

// displayName shortens a canonical type for diagnostics, descending into array
// elements and generic arguments so Result<root.User,root.Missing> reads as
// Result<User, Missing> instead of losing its shape to the last dot.
func displayName(name string) string {
	if element := strings.TrimSuffix(name, "[]"); element != name {
		return displayName(element) + "[]"
	}
	if base, args, generic := genericType(name); generic {
		short := make([]string, len(args))
		for index, arg := range args {
			short[index] = displayName(arg)
		}
		return displayName(base) + "<" + strings.Join(short, ", ") + ">"
	}
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func validNamespace(namespace string) bool {
	parts := strings.Split(namespace, ".")
	if len(parts) == 0 || parts[0] != "root" {
		return false
	}
	for _, part := range parts {
		if !validIdentifier(part) {
			return false
		}
	}
	return true
}

func validIdentifier(value string) bool {
	for index, r := range value {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
		} else if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return value != ""
}
