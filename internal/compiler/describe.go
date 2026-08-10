package compiler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DescriptionSchemaVersion is 6 because annotations expose authored and
// terminal-resolved metadata on declarations and parameters.
const DescriptionSchemaVersion = 6

var ErrUnknownSymbol = errors.New("unknown symbol")

type Description struct {
	SchemaVersion int               `json:"schema_version"`
	Symbol        SymbolDescription `json:"symbol"`
}

type SymbolDescription struct {
	CanonicalName      string                   `json:"canonical_name"`
	Kind               string                   `json:"kind"`
	Visibility         string                   `json:"visibility"`
	Documentation      *string                  `json:"documentation"`
	Annotations        []AnnotationDescription  `json:"annotations"`
	Type               string                   `json:"type"`
	TypeCallable       *CallableTypeDescription `json:"type_callable"`
	TypeParameters     []string                 `json:"type_parameters"`
	Parameters         []ParameterDescription   `json:"parameters"`
	ReturnType         string                   `json:"return_type"`
	ReturnCallable     *CallableTypeDescription `json:"return_callable"`
	Throws             []string                 `json:"throws"`
	Native             bool                     `json:"native"`
	Fields             []FieldDescription       `json:"fields"`
	DeclaredMethods    []MethodDescription      `json:"declared_methods"`
	ImplementedMethods []MethodDescription      `json:"implemented_methods"`
	Interfaces         []string                 `json:"interfaces"`
	Children           []ChildDescription       `json:"children"`
	Source             *SourceDescription       `json:"source"`
}
type AnnotationDescription struct {
	Name              string   `json:"name"`
	Arguments         []string `json:"arguments"`
	ResolvedName      string   `json:"resolved_name"`
	ResolvedArguments []string `json:"resolved_arguments"`
}

// CallableTypeDescription is the structure of a callable type, so a consumer
// reads its parameter types, result, and checked effects directly instead of
// parsing the display spelling.
type CallableTypeDescription struct {
	ParameterTypes []string `json:"parameter_types"`
	ReturnType     string   `json:"return_type"`
	Throws         []string `json:"throws"`
}

type ParameterDescription struct {
	Name        string                   `json:"name"`
	Type        string                   `json:"type"`
	Callable    *CallableTypeDescription `json:"callable"`
	Annotations []AnnotationDescription  `json:"annotations"`
}

type FieldDescription struct {
	Name          string                   `json:"name"`
	Type          string                   `json:"type"`
	Callable      *CallableTypeDescription `json:"callable"`
	Annotations   []AnnotationDescription  `json:"annotations"`
	Visibility    string                   `json:"visibility"`
	Documentation *string                  `json:"documentation"`
	Source        *SourceDescription       `json:"source"`
}

type MethodDescription struct {
	CanonicalName  string                   `json:"canonical_name"`
	Visibility     string                   `json:"visibility"`
	Documentation  *string                  `json:"documentation"`
	Annotations    []AnnotationDescription  `json:"annotations"`
	Parameters     []ParameterDescription   `json:"parameters"`
	ReturnType     string                   `json:"return_type"`
	ReturnCallable *CallableTypeDescription `json:"return_callable"`
	Throws         []string                 `json:"throws"`
	Source         *SourceDescription       `json:"source"`
}

// describeCallable reports the structure of name when it is a callable type,
// and nothing otherwise. Nested types keep their canonical spelling.
func describeCallable(name string) *CallableTypeDescription {
	params, result, throws, callable := callableTypeParts(name)
	if !callable {
		return nil
	}
	description := &CallableTypeDescription{
		ParameterTypes: make([]string, 0, len(params)),
		ReturnType:     result,
		Throws:         make([]string, 0, len(throws)),
	}
	description.ParameterTypes = append(description.ParameterTypes, params...)
	description.Throws = append(description.Throws, throws...)
	return description
}

type ChildDescription struct {
	CanonicalName string  `json:"canonical_name"`
	Kind          string  `json:"kind"`
	Documentation *string `json:"documentation"`
	Visibility    string  `json:"visibility"`
}

type SourceDescription struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// DescribePath compiles an ordinary project and derives a read-only semantic
// description from the resulting declaration graph. Compiler-owned symbols do
// not read the current directory unless a path is explicitly supplied.
func DescribePath(name, path string) (Description, []Diagnostic, error) {
	var sources []Source
	var err error
	if path != "" || name == "root" || strings.HasPrefix(name, "root.") {
		if path == "" {
			path = "."
		}
		sources, err = loadSources(path)
		if err != nil {
			return Description{}, nil, err
		}
	}

	prog, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return Description{}, diagnostics, nil
	}
	symbol, ok := prog.describeSymbol(name)
	if !ok {
		return Description{}, nil, fmt.Errorf("%w %q", ErrUnknownSymbol, name)
	}
	return Description{SchemaVersion: DescriptionSchemaVersion, Symbol: symbol}, nil, nil
}

func (p *program) describeSymbol(name string) (SymbolDescription, bool) {
	if declaration, ok := coreType(name); ok {
		description := emptySymbol(name, declaration.kind, "public")
		description.TypeParameters = append(description.TypeParameters, declaration.typeParams...)
		return description, true
	}
	// A monomorphized instantiation is compiler-owned and is not a declaration
	// anyone wrote, so describe reports the open declaration instead.
	if function := p.functionDeclaration(name); function != nil && function.instanceOf == "" {
		return p.describeFunction(function), true
	}
	if class := p.classDeclaration(name); class != nil && class.instanceOf == "" {
		return p.describeClass(class), true
	}
	if iface := p.interfaceDeclaration(name); iface != nil && iface.instanceOf == "" {
		return p.describeInterface(iface), true
	}
	if union := p.unions[name]; union != nil {
		return p.describeUnion(union), true
	}
	if constant := p.constants[name]; constant != nil {
		return p.describeConstant(constant), true
	}
	if annotation := p.annotations[name]; annotation != nil {
		return p.describeAnnotation(annotation), true
	}
	if member, ok := p.describeMember(name); ok {
		return member, true
	}
	if p.namespaceExists(name) {
		description := emptySymbol(name, "namespace", "public")
		description.Documentation = p.namespaceDocumentation[name]
		description.Children = p.describeChildren(name)
		return description, true
	}
	return SymbolDescription{}, false
}

func emptySymbol(name, kind, visibility string) SymbolDescription {
	return SymbolDescription{
		CanonicalName:      name,
		Kind:               kind,
		Visibility:         visibility,
		Annotations:        []AnnotationDescription{},
		TypeParameters:     []string{},
		Parameters:         []ParameterDescription{},
		Throws:             []string{},
		Fields:             []FieldDescription{},
		DeclaredMethods:    []MethodDescription{},
		ImplementedMethods: []MethodDescription{},
		Interfaces:         []string{},
		Children:           []ChildDescription{},
	}
}

func (p *program) describeFunction(function *functionDecl) SymbolDescription {
	description := emptySymbol(function.qualified, "function", visibility(function.name))
	description.Annotations = p.describeAnnotations(function.annotations)
	description.Documentation = function.documentation
	description.TypeParameters = append(description.TypeParameters, function.typeParams...)
	description.Parameters = p.describeParameters(function.namespace, function.aliases, function.params)
	description.ReturnType = p.canonicalType(function.namespace, function.aliases, function.result)
	description.ReturnCallable = describeCallable(description.ReturnType)
	description.Throws = sortedSet(function.throwSet)
	description.Native = function.native != ""
	description.Source = describeSource(function.pos)
	return description
}

func (p *program) describeClass(class *classDecl) SymbolDescription {
	description := emptySymbol(class.qualified, "class", visibility(class.name))
	description.Annotations = p.describeAnnotations(class.annotations)
	description.Documentation = class.documentation
	description.TypeParameters = append(description.TypeParameters, class.typeParams...)
	description.Source = describeSource(class.pos)
	for _, name := range sortedKeys(class.fields) {
		field := class.fields[name]
		fieldType := p.canonicalType(class.namespace, class.aliases, field.typ)
		description.Fields = append(description.Fields, FieldDescription{
			Name:          field.name,
			Type:          fieldType,
			Callable:      describeCallable(fieldType),
			Annotations:   p.describeAnnotations(field.annotations),
			Visibility:    visibility(field.name),
			Documentation: field.documentation,
			Source:        describeSource(field.pos),
		})
	}
	for _, name := range sortedKeys(class.methods) {
		description.DeclaredMethods = append(description.DeclaredMethods, p.describeMethod(class.qualified, class.methods[name]))
	}
	for _, name := range sortedKeys(class.implementations) {
		implementation := class.implementations[name]
		implementationResult := p.canonicalType(implementation.namespace, implementation.aliases, implementation.result)
		description.ImplementedMethods = append(description.ImplementedMethods, MethodDescription{
			CanonicalName:  class.qualified + "." + implementation.name,
			Documentation:  implementation.documentation,
			Annotations:    p.describeAnnotations(implementation.annotations),
			Visibility:     visibility(implementation.name),
			Parameters:     p.describeParameters(implementation.namespace, implementation.aliases, implementation.params),
			ReturnType:     implementationResult,
			ReturnCallable: describeCallable(implementationResult),
			Throws:         sortedSet(implementation.throwSet),
			Source:         describeSource(implementation.pos),
		})
	}
	if class.isError {
		description.Interfaces = append(description.Interfaces, errorTypeName)
	}
	for _, name := range sortedKeys(p.interfaces) {
		if p.interfaces[name].instanceOf != "" && class.instanceOf == "" {
			continue
		}
		if len(p.classSatisfies(class, p.interfaces[name])) == 0 {
			description.Interfaces = append(description.Interfaces, name)
		}
	}
	sort.Strings(description.Interfaces)
	return description
}

func (p *program) describeInterface(iface *interfaceDecl) SymbolDescription {
	description := emptySymbol(iface.qualified, "interface", visibility(iface.name))
	description.Annotations = p.describeAnnotations(iface.annotations)
	description.Documentation = iface.documentation
	description.TypeParameters = append(description.TypeParameters, iface.typeParams...)
	description.Source = describeSource(iface.pos)
	for _, name := range sortedKeys(iface.methods) {
		description.DeclaredMethods = append(description.DeclaredMethods, p.describeMethod(iface.qualified, iface.methods[name]))
	}
	return description
}

// describeUnion reports a union as its documentation plus its variants in
// declaration order. Each variant is a child symbol whose own description
// carries the payload fields.
func (p *program) describeUnion(union *unionDecl) SymbolDescription {
	description := emptySymbol(union.qualified, "union", visibility(union.name))
	description.Documentation = union.documentation
	description.Source = describeSource(union.pos)
	for _, name := range union.order {
		variant := union.variants[name]
		description.Children = append(description.Children, ChildDescription{
			CanonicalName: union.qualified + "." + variant.name,
			Kind:          "variant",
			Visibility:    visibility(variant.name),
			Documentation: variant.documentation,
		})
	}
	return description
}

// describeConstant reports a constant as its declared type, the same shape a
// field description uses. The value itself stays out of the description: it is
// a compile-time detail every backend inlines, not part of the public surface.
func (p *program) describeConstant(constant *constDecl) SymbolDescription {
	description := emptySymbol(constant.qualified, "constant", visibility(constant.name))
	description.Documentation = constant.documentation
	description.Type = p.canonicalType(constant.namespace, constant.aliases, constant.typ)
	description.TypeCallable = describeCallable(description.Type)
	description.Source = describeSource(constant.pos)
	return description
}

func (p *program) describeVariant(name string, union *unionDecl, variant *unionVariantDecl) SymbolDescription {
	description := emptySymbol(name, "variant", visibility(variant.name))
	description.Documentation = variant.documentation
	description.Type = union.qualified
	description.Source = describeSource(variant.pos)
	for _, field := range variant.fields {
		fieldType := p.canonicalType(union.namespace, union.aliases, field.typ)
		description.Fields = append(description.Fields, FieldDescription{
			Name:       field.name,
			Type:       fieldType,
			Callable:   describeCallable(fieldType),
			Visibility: visibility(field.name),
		})
	}
	return description
}

func (p *program) describeMethod(owner string, method *methodSignature) MethodDescription {
	result := p.canonicalType(method.namespace, method.aliases, method.result)
	return MethodDescription{
		Annotations:    p.describeAnnotations(method.annotations),
		CanonicalName:  owner + "." + method.name,
		Documentation:  method.documentation,
		Visibility:     visibility(method.name),
		Parameters:     p.describeParameters(method.namespace, method.aliases, method.params),
		ReturnType:     result,
		ReturnCallable: describeCallable(result),
		Throws:         sortedSet(method.throwSet),
		Source:         describeSource(method.pos),
	}
}

func (p *program) describeMember(name string) (SymbolDescription, bool) {
	separator := strings.LastIndexByte(name, '.')
	if separator < 0 {
		return SymbolDescription{}, false
	}
	owner, member := name[:separator], name[separator+1:]
	if class := p.classDeclaration(owner); class != nil {
		if field, ok := class.fields[member]; ok {
			description := emptySymbol(name, "field", visibility(field.name))
			description.Documentation = field.documentation
			description.Annotations = p.describeAnnotations(field.annotations)
			description.Type = p.canonicalType(class.namespace, class.aliases, field.typ)
			description.TypeCallable = describeCallable(description.Type)
			description.Source = describeSource(field.pos)
			return description, true
		}
		if implementation := class.implementations[member]; implementation != nil {
			return p.describeFunctionMember(name, implementation), true
		}
		if method := class.methods[member]; method != nil {
			return p.describeMethodMember(name, method), true
		}
	}
	if iface := p.interfaceDeclaration(owner); iface != nil {
		if method := iface.methods[member]; method != nil {
			return p.describeMethodMember(name, method), true
		}
	}
	if union := p.unions[owner]; union != nil {
		if variant := union.variants[member]; variant != nil {
			return p.describeVariant(name, union, variant), true
		}
	}
	return SymbolDescription{}, false
}

func (p *program) describeFunctionMember(name string, function *functionDecl) SymbolDescription {
	description := emptySymbol(name, "method", visibility(function.name))
	description.Documentation = function.documentation
	description.Annotations = p.describeAnnotations(function.annotations)
	description.Parameters = p.describeParameters(function.namespace, function.aliases, function.params)
	description.ReturnType = p.canonicalType(function.namespace, function.aliases, function.result)
	description.ReturnCallable = describeCallable(description.ReturnType)
	description.Throws = sortedSet(function.throwSet)
	description.Source = describeSource(function.pos)
	return description
}

func (p *program) describeMethodMember(name string, method *methodSignature) SymbolDescription {
	description := emptySymbol(name, "method", visibility(method.name))
	description.Annotations = p.describeAnnotations(method.annotations)
	description.Documentation = method.documentation
	description.Parameters = p.describeParameters(method.namespace, method.aliases, method.params)
	description.ReturnType = p.canonicalType(method.namespace, method.aliases, method.result)
	description.ReturnCallable = describeCallable(description.ReturnType)
	description.Throws = sortedSet(method.throwSet)
	description.Source = describeSource(method.pos)
	return description
}

func (p *program) describeParameters(namespace string, aliases map[string]aliasDecl, params []paramDecl) []ParameterDescription {
	descriptions := make([]ParameterDescription, 0, len(params))
	for _, param := range params {
		typ := p.canonicalType(namespace, aliases, param.typ)
		descriptions = append(descriptions, ParameterDescription{
			Name:        param.name,
			Annotations: p.describeAnnotations(param.annotations),
			Type:        typ,
			Callable:    describeCallable(typ),
		})
	}
	return descriptions
}

func (p *program) namespaceExists(namespace string) bool {
	prefix := namespace + "."
	for _, name := range p.declaredSymbolNames() {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range p.unions {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range p.constants {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range p.annotations {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// declaredSymbolNames lists every symbol a source declared, which excludes the
// instantiations the compiler monomorphized for it.
func (p *program) declaredSymbolNames() []string {
	names := make([]string, 0, len(p.functions)+len(p.classes)+len(p.interfaces))
	for name, function := range p.functions {
		if function.instanceOf == "" {
			names = append(names, name)
		}
	}
	for name, class := range p.classes {
		if class.instanceOf == "" {
			names = append(names, name)
		}
	}
	for name, iface := range p.interfaces {
		if iface.instanceOf == "" {
			names = append(names, name)
		}
	}
	for name := range p.genericFunctions {
		names = append(names, name)
	}
	for name := range p.genericClasses {
		names = append(names, name)
	}
	for name := range p.genericInterfaces {
		names = append(names, name)
	}
	for name, annotation := range p.annotations {
		if annotation.terminal == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (p *program) describeChildren(namespace string) []ChildDescription {
	children := make(map[string]ChildDescription)
	add := func(name, kind, declarationName string, documentation *string) {
		prefix := namespace + "."
		if !strings.HasPrefix(name, prefix) {
			return
		}
		remainder := strings.TrimPrefix(name, prefix)
		if separator := strings.IndexByte(remainder, '.'); separator >= 0 {
			childName := prefix + remainder[:separator]
			children[childName] = ChildDescription{
				CanonicalName: childName,
				Kind:          "namespace",
				Visibility:    "public",
				Documentation: p.namespaceDocumentation[childName],
			}
			return
		}
		children[name] = ChildDescription{
			CanonicalName: name,
			Kind:          kind,
			Visibility:    visibility(declarationName),
			Documentation: documentation,
		}
	}
	for _, name := range p.declaredSymbolNames() {
		switch {
		case p.functionDeclaration(name) != nil:
			declaration := p.functionDeclaration(name)
			add(name, "function", declaration.name, declaration.documentation)
		case p.classDeclaration(name) != nil:
			declaration := p.classDeclaration(name)
			add(name, "class", declaration.name, declaration.documentation)
		case p.interfaceDeclaration(name) != nil:
			declaration := p.interfaceDeclaration(name)
			add(name, "interface", declaration.name, declaration.documentation)
		}
	}
	for name, union := range p.unions {
		add(name, "union", union.name, union.documentation)
	}
	for name, constant := range p.constants {
		add(name, "constant", constant.name, constant.documentation)
	}
	for name, annotation := range p.annotations {
		if annotation.terminal == nil {
			add(name, "annotation", annotation.name, annotation.documentation)
		}
	}
	names := sortedKeys(children)
	descriptions := make([]ChildDescription, 0, len(names))
	for _, name := range names {
		descriptions = append(descriptions, children[name])
	}
	return descriptions
}

func visibility(name string) string {
	if isPublic(name) {
		return "public"
	}
	return "private"
}

func describeSource(pos position) *SourceDescription {
	if pos.file == "" {
		return nil
	}
	return &SourceDescription{File: pos.file, Line: pos.line, Column: pos.column}
}

func sortedSet(values map[string]struct{}) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *program) describeAnnotation(annotation *annotationDecl) SymbolDescription {
	description := emptySymbol(annotation.qualified, "annotation", visibility(annotation.name))
	description.Documentation = annotation.documentation
	description.Parameters = p.describeParameters(annotation.namespace, annotation.aliases, annotation.params)
	description.Source = describeSource(annotation.pos)
	if annotation.target != nil {
		description.Annotations = p.describeAnnotations([]*annotationUse{annotation.target})
		if terminal := p.annotationTerminalName(annotation, nil); terminal != "" && len(description.Annotations) > 0 {
			description.Annotations[0].ResolvedName = terminal
		}
	}
	return description
}

func (p *program) annotationTerminalName(annotation *annotationDecl, seen map[string]struct{}) string {
	if annotation == nil {
		return ""
	}
	if annotation.terminal != nil {
		return annotation.terminal.canonical
	}
	if seen == nil {
		seen = make(map[string]struct{})
	}
	if _, duplicate := seen[annotation.qualified]; duplicate {
		return ""
	}
	seen[annotation.qualified] = struct{}{}
	if annotation.target == nil {
		return ""
	}
	return p.annotationTerminalName(p.annotationFor(annotation.namespace, annotation.aliases, annotation.target.name), seen)
}

func (p *program) describeAnnotations(annotations []*annotationUse) []AnnotationDescription {
	descriptions := make([]AnnotationDescription, 0, len(annotations))
	for _, annotation := range annotations {
		description := AnnotationDescription{
			Name:              annotation.name,
			Arguments:         make([]string, len(annotation.args)),
			ResolvedArguments: []string{},
		}
		for index, argument := range annotation.args {
			description.Arguments[index] = annotationExpressionDisplay(argument)
		}
		if len(annotation.resolved) > 0 {
			resolved := annotation.resolved[0]
			description.ResolvedName = resolved.terminal.canonical
			description.ResolvedArguments = make([]string, len(resolved.values))
			for index, value := range resolved.values {
				description.ResolvedArguments[index] = value.display
			}
		}
		descriptions = append(descriptions, description)
	}
	return descriptions
}

func annotationExpressionDisplay(expression expressionNode) string {
	switch node := expression.(type) {
	case *literalExpression:
		return literalAnnotationValue(node.value).display
	case *nameExpression:
		return node.name
	case *unaryExpression:
		return node.op + annotationExpressionDisplay(node.value)
	case *lambdaExpression:
		return "<lambda>"
	default:
		return expressionLabel(expression)
	}
}
