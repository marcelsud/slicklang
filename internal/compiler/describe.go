package compiler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const DescriptionSchemaVersion = 4

var ErrUnknownSymbol = errors.New("unknown symbol")

type Description struct {
	SchemaVersion int               `json:"schema_version"`
	Symbol        SymbolDescription `json:"symbol"`
}

type SymbolDescription struct {
	CanonicalName      string                 `json:"canonical_name"`
	Kind               string                 `json:"kind"`
	Visibility         string                 `json:"visibility"`
	Documentation      *string                `json:"documentation"`
	Type               string                 `json:"type"`
	TypeParameters     []string               `json:"type_parameters"`
	Parameters         []ParameterDescription `json:"parameters"`
	ReturnType         string                 `json:"return_type"`
	Throws             []string               `json:"throws"`
	Native             bool                   `json:"native"`
	Fields             []FieldDescription     `json:"fields"`
	DeclaredMethods    []MethodDescription    `json:"declared_methods"`
	ImplementedMethods []MethodDescription    `json:"implemented_methods"`
	Interfaces         []string               `json:"interfaces"`
	Children           []ChildDescription     `json:"children"`
	Source             *SourceDescription     `json:"source"`
}

type ParameterDescription struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type FieldDescription struct {
	Name          string             `json:"name"`
	Type          string             `json:"type"`
	Visibility    string             `json:"visibility"`
	Documentation *string            `json:"documentation"`
	Source        *SourceDescription `json:"source"`
}

type MethodDescription struct {
	CanonicalName string                 `json:"canonical_name"`
	Visibility    string                 `json:"visibility"`
	Documentation *string                `json:"documentation"`
	Parameters    []ParameterDescription `json:"parameters"`
	ReturnType    string                 `json:"return_type"`
	Throws        []string               `json:"throws"`
	Source        *SourceDescription     `json:"source"`
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
	if function := p.functions[name]; function != nil {
		return p.describeFunction(function), true
	}
	if class := p.classes[name]; class != nil {
		return p.describeClass(class), true
	}
	if iface := p.interfaces[name]; iface != nil {
		return p.describeInterface(iface), true
	}
	if union := p.unions[name]; union != nil {
		return p.describeUnion(union), true
	}
	if constant := p.constants[name]; constant != nil {
		return p.describeConstant(constant), true
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
	description.Documentation = function.documentation
	description.TypeParameters = append(description.TypeParameters, function.typeParams...)
	description.Parameters = p.describeParameters(function.namespace, function.aliases, function.params)
	description.ReturnType = p.canonicalType(function.namespace, function.aliases, function.result)
	description.Throws = sortedSet(function.throwSet)
	description.Native = function.native != ""
	description.Source = describeSource(function.pos)
	return description
}

func (p *program) describeClass(class *classDecl) SymbolDescription {
	description := emptySymbol(class.qualified, "class", visibility(class.name))
	description.Documentation = class.documentation
	description.Source = describeSource(class.pos)
	for _, name := range sortedKeys(class.fields) {
		field := class.fields[name]
		description.Fields = append(description.Fields, FieldDescription{
			Name:          field.name,
			Type:          p.canonicalType(class.namespace, class.aliases, field.typ),
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
		description.ImplementedMethods = append(description.ImplementedMethods, MethodDescription{
			CanonicalName: class.qualified + "." + implementation.name,
			Documentation: implementation.documentation,
			Visibility:    visibility(implementation.name),
			Parameters:    p.describeParameters(implementation.namespace, implementation.aliases, implementation.params),
			ReturnType:    p.canonicalType(implementation.namespace, implementation.aliases, implementation.result),
			Throws:        sortedSet(implementation.throwSet),
			Source:        describeSource(implementation.pos),
		})
	}
	if class.isError {
		description.Interfaces = append(description.Interfaces, errorTypeName)
	}
	for _, name := range sortedKeys(p.interfaces) {
		if len(p.classSatisfies(class, p.interfaces[name])) == 0 {
			description.Interfaces = append(description.Interfaces, name)
		}
	}
	sort.Strings(description.Interfaces)
	return description
}

func (p *program) describeInterface(iface *interfaceDecl) SymbolDescription {
	description := emptySymbol(iface.qualified, "interface", visibility(iface.name))
	description.Documentation = iface.documentation
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
	description.Source = describeSource(constant.pos)
	return description
}

func (p *program) describeVariant(name string, union *unionDecl, variant *unionVariantDecl) SymbolDescription {
	description := emptySymbol(name, "variant", visibility(variant.name))
	description.Documentation = variant.documentation
	description.Type = union.qualified
	description.Source = describeSource(variant.pos)
	for _, field := range variant.fields {
		description.Fields = append(description.Fields, FieldDescription{
			Name:       field.name,
			Type:       p.canonicalType(union.namespace, union.aliases, field.typ),
			Visibility: visibility(field.name),
		})
	}
	return description
}

func (p *program) describeMethod(owner string, method *methodSignature) MethodDescription {
	return MethodDescription{
		CanonicalName: owner + "." + method.name,
		Documentation: method.documentation,
		Visibility:    visibility(method.name),
		Parameters:    p.describeParameters(method.namespace, method.aliases, method.params),
		ReturnType:    p.canonicalType(method.namespace, method.aliases, method.result),
		Throws:        sortedSet(method.throwSet),
		Source:        describeSource(method.pos),
	}
}

func (p *program) describeMember(name string) (SymbolDescription, bool) {
	separator := strings.LastIndexByte(name, '.')
	if separator < 0 {
		return SymbolDescription{}, false
	}
	owner, member := name[:separator], name[separator+1:]
	if class := p.classes[owner]; class != nil {
		if field, ok := class.fields[member]; ok {
			description := emptySymbol(name, "field", visibility(field.name))
			description.Documentation = field.documentation
			description.Type = p.canonicalType(class.namespace, class.aliases, field.typ)
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
	if iface := p.interfaces[owner]; iface != nil {
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
	description.Parameters = p.describeParameters(function.namespace, function.aliases, function.params)
	description.ReturnType = p.canonicalType(function.namespace, function.aliases, function.result)
	description.Throws = sortedSet(function.throwSet)
	description.Source = describeSource(function.pos)
	return description
}

func (p *program) describeMethodMember(name string, method *methodSignature) SymbolDescription {
	description := emptySymbol(name, "method", visibility(method.name))
	description.Documentation = method.documentation
	description.Parameters = p.describeParameters(method.namespace, method.aliases, method.params)
	description.ReturnType = p.canonicalType(method.namespace, method.aliases, method.result)
	description.Throws = sortedSet(method.throwSet)
	description.Source = describeSource(method.pos)
	return description
}

func (p *program) describeParameters(namespace string, aliases map[string]aliasDecl, params []paramDecl) []ParameterDescription {
	descriptions := make([]ParameterDescription, 0, len(params))
	for _, param := range params {
		descriptions = append(descriptions, ParameterDescription{
			Name: param.name,
			Type: p.canonicalType(namespace, aliases, param.typ),
		})
	}
	return descriptions
}

func (p *program) namespaceExists(namespace string) bool {
	prefix := namespace + "."
	for name := range p.functions {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range p.classes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for name := range p.interfaces {
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
	return false
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
	for name, function := range p.functions {
		add(name, "function", function.name, function.documentation)
	}
	for name, class := range p.classes {
		add(name, "class", class.name, class.documentation)
	}
	for name, iface := range p.interfaces {
		add(name, "interface", iface.name, iface.documentation)
	}
	for name, union := range p.unions {
		add(name, "union", union.name, union.documentation)
	}
	for name, constant := range p.constants {
		add(name, "constant", constant.name, constant.documentation)
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
