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

type docBlock struct {
	text       string
	pos        position
	targetLine int
	claimed    bool
}

type typeRef struct {
	name string
	pos  position
}

type paramDecl struct {
	name        string
	typ         typeRef
	annotations []*annotationUse
}

type fieldDecl struct {
	name          string
	typ           typeRef
	jsonName      string
	annotations   []*annotationUse
	documentation *string
	pos           position
}

type aliasDecl struct {
	name          string
	target        string
	namespace     string
	documentation *string
	pos           position
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
	operations     []operationEffectRef
	operationSet   operationEffectSet
	annotations    []*annotationUse
	documentation  *string
	pos            position
}

type classDecl struct {
	name           string
	qualified      string
	namespace      string
	aliases        map[string]aliasDecl
	isError        bool
	nativeResource string
	extension      extensionPolicy
	typeParams     []string
	// instanceOf names the open generic declaration this class was
	// monomorphized from; it is empty for ordinary declarations.
	instanceOf      string
	fields          map[string]fieldDecl
	methods         map[string]*methodSignature
	effective       map[string]*methodSignature
	implementations map[string]*functionDecl
	annotations     []*annotationUse
	documentation   *string
	pos             position
}

type interfaceDecl struct {
	name          string
	qualified     string
	namespace     string
	typeParams    []string
	instanceOf    string
	methods       map[string]*methodSignature
	annotations   []*annotationUse
	documentation *string
	pos           position
}

type functionDecl struct {
	name              string
	qualified         string
	namespace         string
	aliases           map[string]aliasDecl
	typeParams        []string
	params            []paramDecl
	result            typeRef
	throws            []typeRef
	throwSet          map[string]struct{}
	operations        []operationEffectRef
	operationSet      operationEffectSet
	body              []token
	ast               *blockNode
	receiver          typeRef
	receiverCanonical string
	inline            bool
	instanceOf        string
	native            nativeFunction
	annotations       []*annotationUse
	documentation     *string
	pos               position
}

type program struct {
	aliases                []aliasDecl
	classes                map[string]*classDecl
	interfaces             map[string]*interfaceDecl
	unions                 map[string]*unionDecl
	functions              map[string]*functionDecl
	constants              map[string]*constDecl
	genericClasses         map[string]*classDecl
	genericInterfaces      map[string]*interfaceDecl
	genericFunctions       map[string]*functionDecl
	genericMethodImpls     []*functionDecl
	annotations            map[string]*annotationDecl
	namespaceDocumentation map[string]*string
	methodImpls            []*functionDecl
	diags                  []Diagnostic
	// emitted deduplicates diagnostics while dedupe is on, so one mistake in a
	// generic declaration is reported once instead of once per instantiation.
	emitted            map[Diagnostic]struct{}
	dedupe             bool
	usesStdIO          bool
	usesStdHTTP        bool
	usesStdHTTPServer  bool
	usesStdFSDirectory bool
	usesStdProcess     bool
	usesStdSQLite      bool
	usesUsing          bool
	usesAsync          bool
}

func newProgram(terminals ...terminalAnnotationDecl) *program {
	program := &program{
		classes:                make(map[string]*classDecl),
		interfaces:             make(map[string]*interfaceDecl),
		unions:                 make(map[string]*unionDecl),
		functions:              make(map[string]*functionDecl),
		constants:              make(map[string]*constDecl),
		genericClasses:         make(map[string]*classDecl),
		genericInterfaces:      make(map[string]*interfaceDecl),
		genericFunctions:       make(map[string]*functionDecl),
		namespaceDocumentation: make(map[string]*string),
		annotations:            make(map[string]*annotationDecl),
		emitted:                make(map[Diagnostic]struct{}),
	}
	for index := range terminals {
		program.registerTerminalAnnotation(&terminals[index])
	}
	return program
}

func CheckPath(path string) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	return Check(sources), nil
}

// LoadSources reads the .slk file at path, or every .slk file under it when
// path is a directory, in stable name order.
func LoadSources(path string) ([]Source, error) {
	return loadSources(path)
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
	return compileWithTerminals(sources, nil)
}

func compileWithTerminals(sources []Source, terminals []terminalAnnotationDecl) (*program, []Diagnostic) {
	prog := newProgram(terminals...)
	registerStandardLibrary(prog)
	for _, source := range sources {
		if !validNamespace(source.Namespace) {
			prog.add(position{file: source.Name, line: 1, column: 1}, diagnosticCodeNamespace, "invalid namespace %q", source.Namespace)
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
	tokens, diagnostics := scanTokens(source, false)
	prog.diags = append(prog.diags, diagnostics...)
	if len(diagnostics) > 0 {
		return
	}
	parseSourceTokens(prog, source, tokens)
}

func parseSourceTokens(prog *program, source Source, tokens []token) {
	blocks := collectDocBlocks(source)
	p := parser{
		prog:      prog,
		source:    source,
		tokens:    tokens,
		aliases:   make(map[string]aliasDecl),
		docByLine: make(map[int]*docBlock, len(blocks)),
	}
	for _, block := range blocks {
		p.docByLine[block.targetLine] = block
	}
	for !p.atEnd() {
		switch {
		case p.current().text == "@":
			p.parseAnnotatedTopLevel()
		case p.acceptDocumented("annotation"):
			p.parseAnnotationDeclaration()
		case p.acceptDocumented("class"):
			p.parseClass()
		case p.acceptDocumented("interface"):
			p.parseInterface()
		case p.acceptDocumented("union"):
			p.parseUnion()
		case p.acceptDocumented("function"):
			p.parseFunction()
		case p.acceptDocumented("const"):
			p.parseConst()
		case p.accept("use"):
			p.parseUse()
		default:
			tok := p.current()
			p.error(tok.pos, "expected 'use', 'const', 'annotation', 'class', 'interface', 'union', or 'function', found %q", tok.text)
			p.advance()
		}
	}
	for _, block := range blocks {
		if !block.claimed {
			prog.add(block.pos, diagnosticCodeOrphanDocumentation, "documentation comment is not attached to a describable declaration")
		}
	}
}

func collectDocBlocks(source Source) []*docBlock {
	text := strings.ReplaceAll(source.Text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	var blocks []*docBlock
	for index := 0; index < len(lines); {
		trimmed := strings.TrimLeft(lines[index], " \t")
		if !strings.HasPrefix(trimmed, "///") {
			index++
			continue
		}
		start := index
		var content []string
		for index < len(lines) {
			line := strings.TrimLeft(lines[index], " \t")
			if !strings.HasPrefix(line, "///") {
				break
			}
			line = strings.TrimPrefix(line, "///")
			line = strings.TrimPrefix(line, " ")
			content = append(content, line)
			index++
		}
		block := &docBlock{
			text: strings.Join(content, "\n"),
			pos:  position{file: source.Name, line: start + 1, column: strings.Index(lines[start], "///") + 1},
		}
		if index < len(lines) && strings.TrimSpace(lines[index]) != "" {
			block.targetLine = index + 1
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func scanTokens(source Source, comments bool) ([]token, []Diagnostic) {
	var s scanner.Scanner
	s.Init(strings.NewReader(source.Text))
	s.Filename = source.Name
	s.Mode = scanner.GoTokens
	if comments {
		s.Mode &^= scanner.SkipComments
	}
	var diagnostics []Diagnostic
	s.Error = func(scanner *scanner.Scanner, message string) {
		diagnostics = append(diagnostics, newDiagnostic(
			position{file: source.Name, line: scanner.Position.Line, column: scanner.Position.Column},
			diagnosticCodeSyntax,
			"%s", message,
		))
	}

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
	return append(tokens, token{kind: scanner.EOF, pos: position{file: source.Name, line: line, column: 1}}), diagnostics
}

type parser struct {
	prog                 *program
	source               Source
	tokens               []token
	aliases              map[string]aliasDecl
	docByLine            map[int]*docBlock
	pendingDocumentation *string
	pendingAnnotations   []*annotationUse
	index                int
}

func (p *parser) acceptDocumented(text string) bool {
	if p.current().text != text {
		return false
	}
	p.pendingDocumentation = p.takeDocumentation(p.current().pos.line)
	p.advance()
	return true
}

func (p *parser) takeDocumentation(line int) *string {
	block := p.docByLine[line]
	if block == nil {
		return nil
	}
	block.claimed = true
	text := block.text
	return &text
}

func (p *parser) consumeDocumentation() *string {
	documentation := p.pendingDocumentation
	p.pendingDocumentation = nil
	return documentation
}

func (p *parser) parseUse() {
	start := p.index
	target, next, ok := readQualified(p.tokens, start)
	if !ok || !isAbsoluteCanonicalName(target.name) {
		p.error(p.current().pos, "use target must be an absolute root or std namespace path")
		return
	}
	for index := start + 2; index < next; index += 2 {
		if p.tokens[index-1].pos.line != p.tokens[index].pos.line {
			p.index = index
			p.error(p.tokens[index-1].pos, "use target must end in an identifier")
			return
		}
	}
	p.index = next
	if p.current().text == "." {
		p.error(p.current().pos, "use target must end in an identifier")
		return
	}
	name := p.tokens[next-1]
	if p.accept("as") {
		name, ok = p.expectIdent("use alias")
		if !ok {
			return
		}
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

// parseTypeParameters reads a <T, U> declaration list. Names are unique and
// may not shadow a compiler-owned type, so a parameter never silently hides
// int or Result inside the declaration that binds it.
func (p *parser) parseTypeParameters(owner string) ([]string, bool) {
	if p.current().text != "<" {
		return nil, true
	}
	p.advance()
	var params []string
	for {
		name, ok := p.expectIdent("type parameter name")
		if !ok {
			return nil, false
		}
		switch {
		case isReservedTypeName(name.text):
			p.prog.add(name.pos, diagnosticCodeTypeParameter, "type parameter %s shadows the built-in type %s", name.text, name.text)
		case containsName(params, name.text):
			p.prog.add(name.pos, diagnosticCodeTypeParameter, "duplicate type parameter %s on %s", name.text, owner)
		default:
			params = append(params, name.text)
		}
		if !p.accept(",") {
			break
		}
	}
	if !p.accept(">") {
		p.error(p.current().pos, "expected '>' after type parameters")
		return nil, false
	}
	return params, true
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (p *parser) parseClass() {
	name, ok := p.expectIdent("class name")
	if !ok {
		return
	}
	if isReservedTypeName(name.text) {
		p.error(name.pos, "class name %s is reserved by the compiler", name.text)
	}
	typeParams, ok := p.parseTypeParameters(name.text)
	if !ok {
		return
	}
	class := &classDecl{
		name:            name.text,
		qualified:       qualify(p.source.Namespace, name.text),
		namespace:       p.source.Namespace,
		extension:       extensionNamespace,
		typeParams:      typeParams,
		aliases:         p.aliases,
		fields:          make(map[string]fieldDecl),
		methods:         make(map[string]*methodSignature),
		effective:       make(map[string]*methodSignature),
		implementations: make(map[string]*functionDecl),
		annotations:     p.consumeAnnotations(),
		documentation:   p.consumeDocumentation(),
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
				// An implemented generic carries type arguments; conformance is
				// checked structurally at each concrete instantiation.
				if p.current().text == "<" {
					close := matchingAngle(p.tokens, p.index)
					if close < 0 {
						p.error(p.current().pos, "unterminated generic type")
						return
					}
					p.index = close + 1
				}
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
	if previous := p.prog.classDeclaration(class.qualified); previous != nil {
		p.reportDocumentationConflict(name.pos, class.qualified, previous.documentation, class.documentation)
		p.error(name.pos, "duplicate class %s; first declared at %s:%d:%d", class.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		registered = false
	} else if len(class.typeParams) > 0 {
		p.prog.genericClasses[class.qualified] = class
	} else {
		p.prog.classes[class.qualified] = class
	}

	for !p.atEnd() && p.current().text != "}" {
		p.pendingDocumentation = p.takeDocumentation(p.current().pos.line)
		if p.current().text == "@" {
			p.pendingAnnotations = p.parseAnnotationUses()
		}
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
	params, result, throws, operations, body, hasBody, ok := p.parseCallableTail()
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
		operations:     operations,
		annotations:    p.consumeAnnotations(),
		documentation:  p.consumeDocumentation(),
		pos:            name.pos,
	}
	if previous, exists := class.methods[name.text]; exists {
		p.error(name.pos, "duplicate method declaration %s.%s; first declared at %s:%d:%d", class.qualified, name.text, previous.pos.file, previous.pos.line, previous.pos.column)
		p.reportDocumentationConflict(name.pos, class.qualified+"."+name.text, previous.documentation, signature.documentation)
		return
	}
	class.methods[name.text] = signature
	if registered && hasBody {
		implementation := &functionDecl{
			name:              name.text,
			qualified:         class.qualified + "." + name.text,
			namespace:         class.namespace,
			aliases:           p.aliases,
			typeParams:        class.typeParams,
			params:            params,
			result:            result,
			throws:            throws,
			operations:        operations,
			body:              body,
			receiver:          typeRef{name: class.qualified, pos: name.pos},
			receiverCanonical: class.qualified,
			inline:            true,
			annotations:       signature.annotations,
			documentation:     signature.documentation,
			pos:               name.pos,
		}
		if len(class.typeParams) > 0 {
			p.prog.genericMethodImpls = append(p.prog.genericMethodImpls, implementation)
			return
		}
		p.prog.methodImpls = append(p.prog.methodImpls, implementation)
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
	documentation := p.consumeDocumentation()
	annotations := p.consumeAnnotations()
	if previous, exists := class.fields[name.text]; exists {
		p.reportDocumentationConflict(name.pos, class.qualified+"."+name.text, previous.documentation, documentation)
		p.error(name.pos, "duplicate field %s.%s; first declared at %s:%d:%d", class.qualified, name.text, previous.pos.file, previous.pos.line, previous.pos.column)
	} else {
		class.fields[name.text] = fieldDecl{name: name.text, typ: typ, annotations: annotations, documentation: documentation, pos: name.pos}
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
		p.error(name.pos, "interface name %s is reserved by the compiler", name.text)
	}
	typeParams, ok := p.parseTypeParameters(name.text)
	if !ok {
		return
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected interface body")
		return
	}
	decl := &interfaceDecl{
		name:          name.text,
		qualified:     qualify(p.source.Namespace, name.text),
		namespace:     p.source.Namespace,
		typeParams:    typeParams,
		methods:       make(map[string]*methodSignature),
		annotations:   p.consumeAnnotations(),
		documentation: p.consumeDocumentation(),
		pos:           name.pos,
	}
	registered := true
	if previous := p.prog.interfaceDeclaration(decl.qualified); previous != nil {
		p.reportDocumentationConflict(name.pos, decl.qualified, previous.documentation, decl.documentation)
		p.error(name.pos, "duplicate interface %s; first declared at %s:%d:%d", decl.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		registered = false
	} else if len(decl.typeParams) > 0 {
		p.prog.genericInterfaces[decl.qualified] = decl
	} else {
		p.prog.interfaces[decl.qualified] = decl
	}
	for !p.atEnd() && p.current().text != "}" {
		p.pendingDocumentation = p.takeDocumentation(p.current().pos.line)
		if p.current().text == "@" {
			p.pendingAnnotations = p.parseAnnotationUses()
		}
		if !p.accept("function") {
			p.error(p.current().pos, "interfaces may contain only method declarations")
			p.advance()
			continue
		}
		methodName, ok := p.expectIdent("interface method name")
		if !ok {
			continue
		}
		params, result, throws, operations, _, hasBody, ok := p.parseCallableTail()
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
			operations:     operations,
			annotations:    p.consumeAnnotations(),
			documentation:  p.consumeDocumentation(),
			pos:            methodName.pos,
		}
		if previous, exists := decl.methods[methodName.text]; exists {
			p.reportDocumentationConflict(methodName.pos, decl.qualified+"."+methodName.text, previous.documentation, signature.documentation)
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
	// A generic receiver interrupts the dotted name, so Box<T>.Get arrives as
	// the base name, its parameters, and the method name in three steps.
	typeParams, ok := p.parseTypeParameters(ref.name)
	if !ok {
		return
	}
	genericReceiver := ""
	if len(typeParams) > 0 && p.current().text == "." {
		p.advance()
		method, methodOK := p.expectIdent("method name")
		if !methodOK {
			return
		}
		if p.current().text == "<" {
			p.error(p.current().pos, "methods do not declare their own type parameters; %s uses the parameters of %s", method.text, ref.name)
			return
		}
		genericReceiver = ref.name
		ref = qualifiedRef{name: ref.name + "." + method.text, pos: ref.pos}
	}
	params, result, throws, operations, body, hasBody, ok := p.parseCallableTail()
	if !ok {
		return
	}
	if !hasBody {
		p.error(ref.pos, "function %s must have a body", ref.name)
		return
	}
	parts := strings.Split(ref.name, ".")
	for _, part := range parts {
		if isReservedKeyword(part) {
			p.error(ref.pos, "%s is a reserved language keyword", part)
			return
		}
	}
	if len(parts) == 1 && isIterableBuiltin(ref.name) {
		p.error(ref.pos, "function name %s is reserved by the iterable standard library", ref.name)
		return
	}
	if len(parts) == 1 && isResultConstructor(ref.name) {
		p.error(ref.pos, "function name %s is reserved by the compiler", ref.name)
		return
	}
	if len(parts) == 1 {
		qualified := qualify(p.source.Namespace, ref.name)
		if previous := p.prog.functionDeclaration(qualified); previous != nil {
			p.reportDocumentationConflict(ref.pos, qualified, previous.documentation, p.pendingDocumentation)
			p.error(ref.pos, "duplicate function %s; first declared at %s:%d:%d", qualified, previous.pos.file, previous.pos.line, previous.pos.column)
			return
		}
		declaration := &functionDecl{
			name:          ref.name,
			qualified:     qualified,
			namespace:     p.source.Namespace,
			aliases:       p.aliases,
			typeParams:    typeParams,
			params:        params,
			result:        result,
			throws:        throws,
			operations:    operations,
			body:          body,
			annotations:   p.consumeAnnotations(),
			documentation: p.consumeDocumentation(),
			pos:           ref.pos,
		}
		if len(typeParams) > 0 {
			p.prog.genericFunctions[qualified] = declaration
			return
		}
		p.prog.functions[qualified] = declaration
		return
	}

	methodName := parts[len(parts)-1]
	receiverName := strings.Join(parts[:len(parts)-1], ".")
	if genericReceiver != "" {
		receiverName = genericReceiver
	}
	implementation := &functionDecl{
		name:          methodName,
		qualified:     receiverName + "." + methodName,
		namespace:     p.source.Namespace,
		aliases:       p.aliases,
		typeParams:    typeParams,
		params:        params,
		result:        result,
		throws:        throws,
		operations:    operations,
		body:          body,
		annotations:   p.consumeAnnotations(),
		receiver:      typeRef{name: receiverName, pos: ref.pos},
		documentation: p.consumeDocumentation(),
		pos:           ref.pos,
	}
	if len(typeParams) > 0 {
		p.prog.genericMethodImpls = append(p.prog.genericMethodImpls, implementation)
		return
	}
	p.prog.methodImpls = append(p.prog.methodImpls, implementation)
}

func (p *parser) parseCallableTail() ([]paramDecl, typeRef, []typeRef, []operationEffectRef, []token, bool, bool) {
	params, ok := p.parseParams()
	if !ok {
		return nil, typeRef{}, nil, nil, nil, false, false
	}
	if !p.accept("-") || !p.accept(">") {
		p.error(p.current().pos, "expected '->' and a return type")
		return nil, typeRef{}, nil, nil, nil, false, false
	}
	// The declaration owns the throws and effects clauses, so a returned
	// callable that declares either is parenthesized.
	result, ok := p.parseTypeAllowing(false)
	if !ok {
		return nil, typeRef{}, nil, nil, nil, false, false
	}
	throws := p.parseThrows()
	operations := p.parseEffects()
	if !p.accept("{") {
		p.accept(";")
		return params, result, throws, operations, nil, false, true
	}
	bodyStart := p.index
	p.skipBlock()
	bodyEnd := p.index - 1
	if bodyEnd < bodyStart {
		bodyEnd = bodyStart
	}
	return params, result, throws, operations, p.tokens[bodyStart:bodyEnd], true, true
}

func (p *parser) parseParams() ([]paramDecl, bool) {
	if !p.accept("(") {
		p.error(p.current().pos, "expected parameter list")
		return nil, false
	}
	var params []paramDecl
	for !p.atEnd() && p.current().text != ")" {
		annotations := p.parseAnnotationUses()
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
		params = append(params, paramDecl{name: name.text, typ: typ, annotations: annotations})
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
	return p.parseTypeAllowing(true)
}

// parseTypeAllowing reads one type. allowContracts is false where throws and
// effects clauses belong to the enclosing declaration rather than to the type
// being read, so a returned callable declaring either is parenthesized.
func (p *parser) parseTypeAllowing(allowContracts bool) (typeRef, bool) {
	pos := p.current().pos
	text, next, message, errorPos := parseTypeTokensAllowing(p.tokens, p.index, allowContracts)
	if message != "" {
		p.error(errorPos, "%s", message)
		return typeRef{}, false
	}
	p.index = next
	return typeRef{name: text, pos: pos}, true
}

// parseTypeTokens reads one type starting at index and returns its canonical
// spelling. It is shared by every parser so a type has one grammar, and it
// builds the text structurally rather than by joining tokens: a callable's ->
// and throws clause need spacing that token text alone cannot supply.
func parseTypeTokens(tokens []token, index int) (string, int, string, position) {
	return parseTypeTokensAllowing(tokens, index, true)
}

func parseTypeTokensAllowing(tokens []token, index int, allowContracts bool) (string, int, string, position) {
	text, next, message, errorPos := parseTypePrimary(tokens, index, allowContracts)
	if message != "" {
		return "", next, message, errorPos
	}
	for next < len(tokens) {
		if tokens[next].text == "?" {
			text = groupType(text) + "?"
			next++
			continue
		}
		if tokens[next].text == "[" && next+1 < len(tokens) && tokens[next+1].text == "]" {
			text = arrayOf(text)
			next += 2
			continue
		}
		break
	}
	for next < len(tokens) && tokens[next].text == "|" {
		right, after, message, errorPos := parseTypeTokensAllowing(tokens, next+1, allowContracts)
		if message != "" {
			return "", after, message, errorPos
		}
		text += "|" + right
		next = after
	}
	return text, next, "", position{}
}

func parseTypePrimary(tokens []token, index int, allowContracts bool) (string, int, string, position) {
	if index >= len(tokens) {
		return "", index, "expected type", typeTokenPos(tokens, index)
	}
	if tokens[index].text == "(" {
		close := matching(tokens, index, "(", ")")
		if close < 0 {
			return "", index, "unterminated tuple type", tokens[index].pos
		}
		elements, wellFormed := parseTypeTokenList(tokens, index+1, close)
		if close+2 < len(tokens) && tokens[close+1].text == "-" && tokens[close+2].text == ">" {
			if !wellFormed {
				return "", index, "expected type", tokens[index].pos
			}
			return parseCallableTypeTail(tokens, elements, close+3, allowContracts)
		}
		// A malformed element keeps its written spelling, so the type checker
		// reports one structural diagnostic instead of a parse cascade.
		if !wellFormed {
			return joinTokenText(tokens, index, close+1), close + 1, "", position{}
		}
		return "(" + strings.Join(elements, ",") + ")", close + 1, "", position{}
	}
	ref, next, ok := readQualified(tokens, index)
	if !ok {
		return "", index, "expected type", tokens[index].pos
	}
	for _, part := range strings.Split(ref.name, ".") {
		if isReservedKeyword(part) {
			return "", index, part + " is a reserved language keyword", ref.pos
		}
	}
	text := ref.name
	if next < len(tokens) && tokens[next].text == "<" {
		close := matchingAngle(tokens, next)
		if close < 0 {
			return "", next, "unterminated generic type", tokens[next].pos
		}
		args, wellFormed := parseTypeTokenList(tokens, next+1, close)
		if !wellFormed {
			return joinTokenText(tokens, index, close+1), close + 1, "", position{}
		}
		text += "<" + strings.Join(args, ",") + ">"
		next = close + 1
	}
	return text, next, "", position{}
}

// parseCallableTypeTail reads the result type and optional throws and effects
// clauses that follow a callable type's parameter list.
func parseCallableTypeTail(tokens []token, params []string, index int, allowContracts bool) (string, int, string, position) {
	// The result is read without contracts of its own, so both suffixes bind to
	// the outermost callable and never become ambiguous.
	result, next, message, errorPos := parseTypeTokensAllowing(tokens, index, false)
	if message != "" {
		return "", next, message, errorPos
	}
	var throws []string
	if allowContracts && next < len(tokens) && tokens[next].text == "throws" {
		next++
		for {
			thrown, after, message, errorPos := parseThrownTypeTokens(tokens, next)
			if message != "" {
				return "", next, message, errorPos
			}
			throws = append(throws, thrown.name)
			next = after
			if next >= len(tokens) || tokens[next].text != "|" {
				break
			}
			next++
		}
	}
	var operations []string
	if allowContracts && next < len(tokens) && tokens[next].text == "effects" {
		refs, after, message, errorPos := parseOperationEffects(tokens, next+1)
		if message != "" {
			return "", next, message, errorPos
		}
		operations = make([]string, len(refs))
		for index, ref := range refs {
			operations[index] = ref.name
		}
		next = after
	}
	return callableType(params, result, throws, operations), next, "", position{}
}

// parseTypeTokenList reads the comma-separated types between start and end. It
// reports whether the whole span is a well-formed list, so a caller can keep a
// malformed spelling intact rather than inventing a shorter type from it.
func parseTypeTokenList(tokens []token, start, end int) ([]string, bool) {
	var elements []string
	index := start
	for index < end {
		text, next, message, _ := parseTypeTokens(tokens, index)
		if message != "" {
			return nil, false
		}
		elements = append(elements, text)
		index = next
		if index >= end {
			break
		}
		if tokens[index].text != "," {
			return nil, false
		}
		index++
		if index >= end {
			return nil, false
		}
	}
	return elements, index == end
}

func joinTokenText(tokens []token, start, end int) string {
	var text strings.Builder
	for _, tok := range tokens[start:end] {
		text.WriteString(tok.text)
	}
	return text.String()
}

func typeTokenPos(tokens []token, index int) position {
	if index < len(tokens) {
		return tokens[index].pos
	}
	if len(tokens) > 0 {
		return tokens[len(tokens)-1].pos
	}
	return position{}
}

func (p *parser) parseThrows() []typeRef {
	if !p.accept("throws") {
		return nil
	}
	var throws []typeRef
	for {
		thrown, next, message, errorPos := parseThrownTypeTokens(p.tokens, p.index)
		if message != "" {
			p.error(errorPos, "%s", message)
			return throws
		}
		p.index = next
		throws = append(throws, thrown)
		if !p.accept("|") {
			return throws
		}
	}
}

func parseThrownTypeTokens(tokens []token, index int) (typeRef, int, string, position) {
	if index >= len(tokens) || tokens[index].kind != scanner.Ident {
		return typeRef{}, index, "expected error type after 'throws'", typeTokenPos(tokens, index)
	}
	name, next, message, errorPos := parseTypePrimary(tokens, index, false)
	if message != "" {
		return typeRef{}, next, message, errorPos
	}
	return typeRef{name: name, pos: tokens[index].pos}, next, "", position{}
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
	if isReservedKeyword(tok.text) {
		p.error(tok.pos, "%s is a reserved language keyword", tok.text)
		p.advance()
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

func (p *parser) reportDocumentationConflict(pos position, name string, first, second *string) {
	if first != nil && second != nil {
		p.prog.add(pos, diagnosticCodeConflictingDocumentation, "competing documentation for %s", name)
	}
}

func (p *parser) error(pos position, format string, args ...any) {
	p.prog.add(pos, diagnosticCodeSyntax, format, args...)
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

// classDeclaration finds a class by canonical name whether it is an ordinary
// declaration or an open generic one. Only name collision and documentation
// care about both at once; everything downstream works on concrete classes.
func (p *program) classDeclaration(name string) *classDecl {
	if class := p.classes[name]; class != nil {
		return class
	}
	return p.genericClasses[name]
}

func (p *program) interfaceDeclaration(name string) *interfaceDecl {
	if iface := p.interfaces[name]; iface != nil {
		return iface
	}
	return p.genericInterfaces[name]
}

func (p *program) functionDeclaration(name string) *functionDecl {
	if function := p.functions[name]; function != nil {
		return function
	}
	return p.genericFunctions[name]
}

func (p *program) check() {
	p.parseBodies()
	p.checkAliases()
	p.checkConstants()
	p.checkAnnotations()
	p.checkGenericDeclarations()
	p.instantiateGenerics()
	p.checkJSONFieldNames()
	p.checkDeclaredTypes()
	p.checkVisibility()
	p.resolveThrowSets()
	p.resolveOperationEffectSets()
	p.linkMethods()
	for _, name := range sortedKeys(p.functions) {
		function := p.functions[name]
		p.checkingInstance(function.instanceOf != "", func() { p.checkFunction(function) })
	}
	for _, implementation := range p.methodImpls {
		p.checkingInstance(implementation.instanceOf != "", func() { p.checkFunction(implementation) })
	}
}

func (p *program) checkAliases() {
	for _, alias := range p.aliases {
		class := p.classDeclaration(alias.target)
		iface := p.interfaceDeclaration(alias.target)
		union := p.unions[alias.target]
		function := p.functionDeclaration(alias.target)
		constant := p.constants[alias.target]
		annotation := p.annotations[alias.target]
		if class == nil && iface == nil && union == nil && function == nil && constant == nil && annotation == nil {
			p.add(alias.pos, diagnosticCodeAlias, "alias target %s does not exist", alias.target)
		} else if class != nil {
			p.requireAccess(alias.pos, alias.namespace, class.namespace, class.name, "class")
		} else if iface != nil {
			p.requireAccess(alias.pos, alias.namespace, iface.namespace, iface.name, "interface")
		} else if union != nil {
			p.requireAccess(alias.pos, alias.namespace, union.namespace, union.name, "union")
		} else if constant != nil {
			p.requireAccess(alias.pos, alias.namespace, constant.namespace, constant.name, "constant")
		} else if annotation != nil {
			p.requireAccess(alias.pos, alias.namespace, annotation.namespace, annotation.name, "annotation")
		} else {
			p.requireAccess(alias.pos, alias.namespace, function.namespace, function.name, "function")
		}
		local := qualify(alias.namespace, alias.name)
		localClassExists := p.classDeclaration(local) != nil
		localInterfaceExists := p.interfaceDeclaration(local) != nil
		localUnionExists := p.unions[local] != nil
		localFunctionExists := p.functionDeclaration(local) != nil
		localConstantExists := p.constants[local] != nil
		localAnnotationExists := p.annotations[local] != nil
		if alias.target != local && (localClassExists || localInterfaceExists || localUnionExists || localFunctionExists || localConstantExists || localAnnotationExists) {
			p.add(alias.pos, diagnosticCodeAlias, "alias %s conflicts with a declaration in %s", alias.name, alias.namespace)
		}
	}
}

// checkDeclaredTypes rejects type spellings that are structurally wrong before
// any body is typed, so one type has exactly one canonical spelling. Inline
// class methods are skipped in methodImpls: the class already carries their
// declaration, and reporting both would duplicate the diagnostic.
func (p *program) checkDeclaredTypes() {
	for _, name := range sortedKeys(p.functions) {
		function := p.functions[name]
		p.checkingInstance(function.instanceOf != "", func() {
			p.checkCallableTypes(function.params, function.result)
		})
	}
	for _, implementation := range p.methodImpls {
		if implementation.inline {
			continue
		}
		p.checkingInstance(implementation.instanceOf != "", func() {
			p.checkCallableTypes(implementation.params, implementation.result)
		})
	}
	for _, name := range sortedKeys(p.classes) {
		class := p.classes[name]
		p.checkingInstance(class.instanceOf != "", func() {
			for _, fieldName := range sortedKeys(class.fields) {
				p.checkTypeRef(class.fields[fieldName].typ)
			}
			for _, methodName := range sortedKeys(class.methods) {
				method := class.methods[methodName]
				p.checkCallableTypes(method.params, method.result)
			}
		})
	}
	for _, name := range sortedKeys(p.interfaces) {
		iface := p.interfaces[name]
		p.checkingInstance(iface.instanceOf != "", func() {
			for _, methodName := range sortedKeys(iface.methods) {
				method := iface.methods[methodName]
				p.checkCallableTypes(method.params, method.result)
			}
		})
	}
	for _, name := range sortedKeys(p.unions) {
		union := p.unions[name]
		for _, variantName := range union.order {
			for _, field := range union.variants[variantName].fields {
				p.checkTypeRef(field.typ)
			}
		}
	}
}

func (p *program) checkCallableTypes(params []paramDecl, result typeRef) {
	for _, param := range params {
		p.checkTypeRef(param.typ)
	}
	p.checkTypeRef(result)
}

func (p *program) checkTypeRef(ref typeRef) {
	if redundantOptional(ref.name) {
		p.add(ref.pos, diagnosticCodeRedundantOptional, "%s is redundant; a type is optional at most once", ref.name)
	}
}

func (p *program) checkFunction(function *functionDecl) {
	p.checkASTFunction(function)
}

// matchingAngle finds the > that closes the < at start. The > of a callable
// type's -> is skipped, so a generic argument may itself be a callable.
func matchingAngle(tokens []token, start int) int {
	if start >= len(tokens) || tokens[start].text != "<" {
		return -1
	}
	depth := 0
	for index := start; index < len(tokens); index++ {
		switch tokens[index].text {
		case "<":
			depth++
		case ">":
			if index > start && tokens[index-1].text == "-" {
				continue
			}
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
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

// callTarget is the function a checked call reaches. A generic call already
// selected one instantiation, so both backends follow that decision instead of
// resolving the open declaration a second time.
func (p *program) callTarget(function *functionDecl, node *callExpression, name string) *functionDecl {
	if node.resolvedCallee != "" {
		return p.functions[node.resolvedCallee]
	}
	return p.resolveFunction(function, name)
}

func (p *program) resolveName(function *functionDecl, name string) string {
	if isAbsoluteCanonicalName(name) {
		return name
	}
	if alias, ok := function.aliases[name]; ok {
		return alias.target
	}
	return qualify(function.namespace, name)
}

// add records one diagnostic. While dedupe is on the caller is re-checking a
// declaration the compiler synthesized, so a diagnostic identical to one
// already reported describes the same source mistake and is dropped.
func (p *program) add(pos position, code diagnosticCode, format string, args ...any) {
	diagnostic := newDiagnostic(pos, code, format, args...)
	if p.emitted == nil {
		p.emitted = make(map[Diagnostic]struct{})
	}
	if _, seen := p.emitted[diagnostic]; seen && p.dedupe {
		return
	}
	p.emitted[diagnostic] = struct{}{}
	p.diags = append(p.diags, diagnostic)
}

// checkingInstance runs check while diagnostics from a monomorphized
// declaration are deduplicated against everything already reported.
func (p *program) checkingInstance(instance bool, check func()) {
	previous := p.dedupe
	p.dedupe = instance
	check()
	p.dedupe = previous
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

// displayName shortens a canonical type for diagnostics, descending through
// every structural layer so Result<root.User,root.Missing> reads as
// Result<User, Missing> and root.User?[] as User?[] instead of losing their
// shape to the last dot.
func displayName(name string) string {
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional:
		return groupDisplayName(parsed.base) + "?"
	case typeKindArray:
		return groupDisplayName(parsed.base) + "[]"
	case typeKindCallable:
		short := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			short[index] = displayName(arg)
		}
		display := "(" + strings.Join(short, ", ") + ") -> " + displayName(parsed.base)
		if len(parsed.throws) > 0 {
			thrown := make([]string, len(parsed.throws))
			for index, name := range parsed.throws {
				thrown[index] = displayName(name)
			}
			display += " throws " + strings.Join(thrown, " | ")
		}
		if len(parsed.operations) > 0 {
			operations := append([]string(nil), parsed.operations...)
			sort.Strings(operations)
			display += " effects { " + strings.Join(operations, ", ") + " }"
		}
		return display
	case typeKindTuple:
		short := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			short[index] = displayName(arg)
		}
		return "(" + strings.Join(short, ", ") + ")"
	case typeKindGeneric:
		short := make([]string, len(parsed.args))
		for index, arg := range parsed.args {
			short[index] = displayName(arg)
		}
		return displayName(parsed.base) + "<" + strings.Join(short, ", ") + ">"
	}
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		return name[index+1:]
	}
	return name
}

// groupDisplayName keeps the parentheses a callable needs when a ? or []
// suffix follows it, matching the canonical spelling.
func groupDisplayName(name string) string {
	if isCallableType(name) {
		return "(" + displayName(name) + ")"
	}
	return displayName(name)
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
	if isReservedKeyword(value) {
		return false
	}
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

func isReservedKeyword(value string) bool {
	return value == "async" || value == "await" || value == "effects"
}
