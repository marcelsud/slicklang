package compiler

import (
	"fmt"
	"os"
	"strings"
)

type nativeFunction string

const (
	nativeStdEnvGet   nativeFunction = "std.env.Get"
	nativeStdEnvSet   nativeFunction = "std.env.Set"
	nativeStdEnvUnset nativeFunction = "std.env.Unset"

	stdEnvFailureName = "std.env.Failure"
)

type standardFunctionDecl struct {
	canonical string
	namespace string
	name      string
	params    []paramDecl
	result    typeRef
	native    nativeFunction
}

type standardClassDecl struct {
	canonical string
	namespace string
	name      string
	isError   bool
	fields    []fieldDecl
}

// standardLibraryRegistry is the authoritative public Slick surface backed by
// the Go standard library. The compiler, interpreter, and Go backend all use
// the synthetic declarations registered from this table.
var standardLibraryRegistry = struct {
	functions []standardFunctionDecl
	classes   []standardClassDecl
}{
	functions: []standardFunctionDecl{
		{
			canonical: string(nativeStdEnvGet),
			namespace: "std.env",
			name:      "Get",
			params:    []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string?"},
			native:    nativeStdEnvGet,
		},
		{
			canonical: string(nativeStdEnvSet),
			namespace: "std.env",
			name:      "Set",
			params: []paramDecl{
				{name: "Name", typ: typeRef{name: "string"}},
				{name: "Value", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "Result<null,std.env.Failure>"},
			native: nativeStdEnvSet,
		},
		{
			canonical: string(nativeStdEnvUnset),
			namespace: "std.env",
			name:      "Unset",
			params:    []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<null,std.env.Failure>"},
			native:    nativeStdEnvUnset,
		},
	},
	classes: []standardClassDecl{
		{
			canonical: stdEnvFailureName,
			namespace: "std.env",
			name:      "Failure",
			isError:   true,
			fields: []fieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}},
				{name: "Name", typ: typeRef{name: "string"}},
				{name: "Message", typ: typeRef{name: "string"}},
			},
		},
	},
}

func registerStandardLibrary(p *program) {
	for _, declaration := range standardLibraryRegistry.functions {
		p.functions[declaration.canonical] = &functionDecl{
			name:      declaration.name,
			qualified: declaration.canonical,
			namespace: declaration.namespace,
			aliases:   make(map[string]aliasDecl),
			params:    declaration.params,
			result:    declaration.result,
			native:    declaration.native,
		}
	}
	for _, declaration := range standardLibraryRegistry.classes {
		fields := make(map[string]fieldDecl, len(declaration.fields))
		for _, field := range declaration.fields {
			fields[field.name] = field
		}
		p.classes[declaration.canonical] = &classDecl{
			name:            declaration.name,
			qualified:       declaration.canonical,
			namespace:       declaration.namespace,
			aliases:         make(map[string]aliasDecl),
			isError:         declaration.isError,
			extension:       extensionNone,
			fields:          fields,
			methods:         make(map[string]*methodSignature),
			effective:       make(map[string]*methodSignature),
			implementations: make(map[string]*functionDecl),
		}
	}
}

// isAbsoluteCanonicalName is the single namespace boundary shared by user
// project declarations and compiler-owned standard-library declarations.
func isAbsoluteCanonicalName(name string) bool {
	return strings.HasPrefix(name, "root.") || strings.HasPrefix(name, "std.")
}

func (p *program) callNativeFunction(function *functionDecl, frame *runtimeFrame) (runtimeValue, error) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	name := frame.locals["Name"].scalar.(string)
	switch function.native {
	case nativeStdEnvGet:
		value, present := os.LookupEnv(name)
		optional := &runtimeOptional{present: present}
		if present {
			optional.value = runtimeValue{typ: "string", scalar: value}
		}
		return runtimeValue{typ: resultType, optional: optional}, nil
	case nativeStdEnvSet:
		value := frame.locals["Value"].scalar.(string)
		return runtimeEnvMutationResult(resultType, "Set", name, os.Setenv(name, value)), nil
	case nativeStdEnvUnset:
		return runtimeEnvMutationResult(resultType, "Unset", name, os.Unsetenv(name)), nil
	default:
		return runtimeValue{}, fmt.Errorf("unknown native Slick function %s", function.native)
	}
}

func runtimeEnvMutationResult(resultType, operation, name string, err error) runtimeValue {
	if err == nil {
		return runtimeValue{
			typ:    resultType,
			result: &runtimeResult{ok: true, payload: nullRuntimeValue()},
		}
	}
	failure := runtimeValue{
		typ: stdEnvFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Name":      {typ: "string", scalar: name},
			"Message":   {typ: "string", scalar: err.Error()},
		},
	}
	return runtimeValue{
		typ:    resultType,
		result: &runtimeResult{payload: failure},
	}
}

func (g *goGenerator) emitNativeFunction(function *functionDecl) error {
	resultType, err := g.declaredType(function.namespace, function.aliases, function.result)
	if err != nil {
		return err
	}
	arguments := make([]string, 0, len(function.params))
	parameters := make([]string, 0, len(function.params))
	for _, parameter := range function.params {
		typ, err := g.declaredType(function.namespace, function.aliases, parameter.typ)
		if err != nil {
			return err
		}
		argument := g.unique("argument")
		arguments = append(arguments, argument)
		parameters = append(parameters, argument+" "+g.goType(typ))
	}
	g.line("func %s(%s) (%s, error) {", goFunctionName(function.qualified), strings.Join(parameters, ", "), g.goType(resultType))
	switch function.native {
	case nativeStdEnvGet:
		base, _ := optionalBase(resultType)
		g.line("value, present := os.LookupEnv(%s)", arguments[0])
		g.line("if !present { return slickNone[%s](), nil }", g.goType(base))
		g.line("return slickSome(value), nil")
	case nativeStdEnvSet:
		g.emitNativeEnvMutation(resultType, "Set", arguments[0], fmt.Sprintf("os.Setenv(%s, %s)", arguments[0], arguments[1]))
	case nativeStdEnvUnset:
		g.emitNativeEnvMutation(resultType, "Unset", arguments[0], fmt.Sprintf("os.Unsetenv(%s)", arguments[0]))
	default:
		return fmt.Errorf("unknown native Slick function %s", function.native)
	}
	g.line("}")
	g.line("")
	return nil
}

func (g *goGenerator) emitNativeEnvMutation(resultType, operation, name, call string) {
	result := g.goType(resultType)
	g.line("if err := %s; err != nil {", call)
	g.line("return %s{failure: &%s{%s: %q, %s: %s, %s: err.Error()}}, nil",
		result,
		goClassName(stdEnvFailureName),
		goFieldName("Operation"), operation,
		goFieldName("Name"), name,
		goFieldName("Message"),
	)
	g.line("}")
	g.line("return %s{ok: true, value: struct{}{}}, nil", result)
}
