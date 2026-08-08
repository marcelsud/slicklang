package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type nativeFunction string

const (
	nativeStdBytesFromUtf8  nativeFunction = "std.bytes.FromUtf8"
	nativeStdBytesToUtf8    nativeFunction = "std.bytes.ToUtf8"
	nativeStdBytesLength    nativeFunction = "std.bytes.Length"
	nativeStdBytesAt        nativeFunction = "std.bytes.At"
	nativeStdBytesConcat    nativeFunction = "std.bytes.Concat"
	nativeStdEnvGet         nativeFunction = "std.env.Get"
	nativeStdEnvSet         nativeFunction = "std.env.Set"
	nativeStdEnvUnset       nativeFunction = "std.env.Unset"
	nativeStdJsonDecode     nativeFunction = "std.json.Decode"
	nativeStdJsonEncode     nativeFunction = "std.json.Encode"
	nativeStdPathJoin       nativeFunction = "std.path.Join"
	nativeStdPathClean      nativeFunction = "std.path.Clean"
	nativeStdPathBase       nativeFunction = "std.path.Base"
	nativeStdPathDirectory  nativeFunction = "std.path.Directory"
	nativeStdPathExtension  nativeFunction = "std.path.Extension"
	nativeStdPathIsAbsolute nativeFunction = "std.path.IsAbsolute"

	stdBytesUtf8FailureName = "std.bytes.Utf8Failure"
	stdEnvFailureName       = "std.env.Failure"
	stdJsonFailureName      = "std.json.Failure"
)

type standardFunctionDecl struct {
	canonical  string
	namespace  string
	name       string
	typeParams []string
	params     []paramDecl
	result     typeRef
	native     nativeFunction
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
			canonical: string(nativeStdBytesFromUtf8),
			namespace: "std.bytes",
			name:      "FromUtf8",
			params:    []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "bytes"},
			native:    nativeStdBytesFromUtf8,
		},
		{
			canonical: string(nativeStdBytesToUtf8),
			namespace: "std.bytes",
			name:      "ToUtf8",
			params:    []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:    typeRef{name: "Result<string,std.bytes.Utf8Failure>"},
			native:    nativeStdBytesToUtf8,
		},
		{
			canonical: string(nativeStdBytesLength),
			namespace: "std.bytes",
			name:      "Length",
			params:    []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:    typeRef{name: "int"},
			native:    nativeStdBytesLength,
		},
		{
			canonical: string(nativeStdBytesAt),
			namespace: "std.bytes",
			name:      "At",
			params: []paramDecl{
				{name: "Value", typ: typeRef{name: "bytes"}},
				{name: "Index", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "int?"},
			native: nativeStdBytesAt,
		},
		{
			canonical: string(nativeStdBytesConcat),
			namespace: "std.bytes",
			name:      "Concat",
			params:    []paramDecl{{name: "Values", typ: typeRef{name: "bytes[]"}}},
			result:    typeRef{name: "bytes"},
			native:    nativeStdBytesConcat,
		},
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
		{
			canonical:  string(nativeStdJsonDecode),
			namespace:  "std.json",
			name:       "Decode",
			typeParams: []string{"T"},
			params:     []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:     typeRef{name: "Result<T,std.json.Failure>"},
			native:     nativeStdJsonDecode,
		},
		{
			canonical:  string(nativeStdJsonEncode),
			namespace:  "std.json",
			name:       "Encode",
			typeParams: []string{"T"},
			params:     []paramDecl{{name: "Value", typ: typeRef{name: "T"}}},
			result:     typeRef{name: "Result<string,std.json.Failure>"},
			native:     nativeStdJsonEncode,
		},
		{
			canonical: string(nativeStdPathJoin),
			namespace: "std.path",
			name:      "Join",
			params:    []paramDecl{{name: "Parts", typ: typeRef{name: "string[]"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdPathJoin,
		},
		{
			canonical: string(nativeStdPathClean),
			namespace: "std.path",
			name:      "Clean",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdPathClean,
		},
		{
			canonical: string(nativeStdPathBase),
			namespace: "std.path",
			name:      "Base",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdPathBase,
		},
		{
			canonical: string(nativeStdPathDirectory),
			namespace: "std.path",
			name:      "Directory",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdPathDirectory,
		},
		{
			canonical: string(nativeStdPathExtension),
			namespace: "std.path",
			name:      "Extension",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string?"},
			native:    nativeStdPathExtension,
		},
		{
			canonical: string(nativeStdPathIsAbsolute),
			namespace: "std.path",
			name:      "IsAbsolute",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "bool"},
			native:    nativeStdPathIsAbsolute,
		},
	},
	classes: []standardClassDecl{
		{
			canonical: stdBytesUtf8FailureName,
			namespace: "std.bytes",
			name:      "Utf8Failure",
			isError:   true,
			fields: []fieldDecl{
				{name: "Message", typ: typeRef{name: "string"}},
			},
		},
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
		{
			canonical: stdJsonFailureName,
			namespace: "std.json",
			name:      "Failure",
			isError:   true,
			fields: []fieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}},
				{name: "Path", typ: typeRef{name: "string"}},
				{name: "Message", typ: typeRef{name: "string"}},
			},
		},
	},
}

func registerStandardLibrary(p *program) {
	for _, declaration := range standardLibraryRegistry.functions {
		p.functions[declaration.canonical] = &functionDecl{
			name:       declaration.name,
			qualified:  declaration.canonical,
			namespace:  declaration.namespace,
			aliases:    make(map[string]aliasDecl),
			typeParams: append([]string(nil), declaration.typeParams...),
			params:     declaration.params,
			result:     declaration.result,
			native:     declaration.native,
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

func (p *program) callNativeFunction(function *functionDecl, frame *runtimeFrame, typeArgs []string) (runtimeValue, error) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdBytesFromUtf8:
		text := frame.locals["Text"].scalar.(string)
		return runtimeValue{typ: "bytes", scalar: []byte(text)}, nil
	case nativeStdBytesToUtf8:
		value := frame.locals["Value"].scalar.([]byte)
		if utf8.Valid(value) {
			return runtimeValue{
				typ:    resultType,
				result: &runtimeResult{ok: true, payload: runtimeValue{typ: "string", scalar: string(value)}},
			}, nil
		}
		failure := runtimeValue{
			typ: stdBytesUtf8FailureName,
			fields: map[string]runtimeValue{
				"Message": {typ: "string", scalar: "invalid UTF-8"},
			},
		}
		return runtimeValue{typ: resultType, result: &runtimeResult{payload: failure}}, nil
	case nativeStdBytesLength:
		value := frame.locals["Value"].scalar.([]byte)
		return runtimeValue{typ: "int", scalar: int64(len(value))}, nil
	case nativeStdBytesAt:
		value := frame.locals["Value"].scalar.([]byte)
		index := frame.locals["Index"].scalar.(int64)
		optional := &runtimeOptional{}
		if index >= 0 && index < int64(len(value)) {
			optional.present = true
			optional.value = runtimeValue{typ: "int", scalar: int64(value[index])}
		}
		return runtimeValue{typ: "int?", optional: optional}, nil
	case nativeStdBytesConcat:
		values := frame.locals["Values"].elements
		total := 0
		for _, value := range values {
			total += len(value.scalar.([]byte))
		}
		joined := make([]byte, total)
		offset := 0
		for _, value := range values {
			offset += copy(joined[offset:], value.scalar.([]byte))
		}
		return runtimeValue{typ: "bytes", scalar: joined}, nil
	case nativeStdEnvGet:
		name := frame.locals["Name"].scalar.(string)
		value, present := os.LookupEnv(name)
		optional := &runtimeOptional{present: present}
		if present {
			optional.value = runtimeValue{typ: "string", scalar: value}
		}
		return runtimeValue{typ: resultType, optional: optional}, nil
	case nativeStdEnvSet:
		name := frame.locals["Name"].scalar.(string)
		value := frame.locals["Value"].scalar.(string)
		return runtimeEnvMutationResult(resultType, "Set", name, os.Setenv(name, value)), nil
	case nativeStdEnvUnset:
		name := frame.locals["Name"].scalar.(string)
		return runtimeEnvMutationResult(resultType, "Unset", name, os.Unsetenv(name)), nil
	case nativeStdJsonDecode:
		if len(typeArgs) != 1 {
			return runtimeValue{}, fmt.Errorf("std.json.Decode requires one type argument")
		}
		text := frame.locals["Text"].scalar.(string)
		return p.runtimeJSONDecode(typeArgs[0], text), nil
	case nativeStdJsonEncode:
		if len(typeArgs) != 1 {
			return runtimeValue{}, fmt.Errorf("std.json.Encode requires one type argument")
		}
		return p.runtimeJSONEncode(typeArgs[0], frame.locals["Value"]), nil
	case nativeStdPathJoin:
		values := frame.locals["Parts"].elements
		parts := make([]string, len(values))
		for index, value := range values {
			parts[index] = value.scalar.(string)
		}
		return runtimeValue{typ: resultType, scalar: filepath.Join(parts...)}, nil
	case nativeStdPathClean:
		return runtimeValue{typ: resultType, scalar: filepath.Clean(frame.locals["Path"].scalar.(string))}, nil
	case nativeStdPathBase:
		return runtimeValue{typ: resultType, scalar: filepath.Base(frame.locals["Path"].scalar.(string))}, nil
	case nativeStdPathDirectory:
		return runtimeValue{typ: resultType, scalar: filepath.Dir(frame.locals["Path"].scalar.(string))}, nil
	case nativeStdPathExtension:
		extension := filepath.Ext(frame.locals["Path"].scalar.(string))
		optional := &runtimeOptional{present: extension != ""}
		if optional.present {
			optional.value = runtimeValue{typ: "string", scalar: extension}
		}
		return runtimeValue{typ: resultType, optional: optional}, nil
	case nativeStdPathIsAbsolute:
		return runtimeValue{typ: resultType, scalar: filepath.IsAbs(frame.locals["Path"].scalar.(string))}, nil
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
	if function.native == nativeStdJsonDecode || function.native == nativeStdJsonEncode {
		// Concrete codecs are emitted per call site type argument.
		return nil
	}
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
	case nativeStdBytesFromUtf8:
		g.line("return slickBytes([]byte(%s)), nil", arguments[0])
	case nativeStdBytesToUtf8:
		result := g.goType(resultType)
		failure := goClassName(stdBytesUtf8FailureName)
		g.line("if !utf8.Valid(%s) {", arguments[0])
		g.line("return %s{failure: &%s{%s: %q}}, nil", result, failure, goFieldName("Message"), "invalid UTF-8")
		g.line("}")
		g.line("return %s{ok: true, value: string(%s)}, nil", result, arguments[0])
	case nativeStdBytesLength:
		g.line("return int64(len(%s)), nil", arguments[0])
	case nativeStdBytesAt:
		base, _ := optionalBase(resultType)
		g.line("if %s < 0 || %s >= int64(len(%s)) { return slickNone[%s](), nil }", arguments[1], arguments[1], arguments[0], g.goType(base))
		g.line("return slickSome(int64(%s[%s])), nil", arguments[0], arguments[1])
	case nativeStdBytesConcat:
		total := g.unique("total")
		value := g.unique("value")
		result := g.unique("bytes")
		offset := g.unique("offset")
		g.line("%s := 0", total)
		g.line("for _, %s := range %s { %s += len(%s) }", value, arguments[0], total, value)
		g.line("%s := make(slickBytes, %s)", result, total)
		g.line("%s := 0", offset)
		g.line("for _, %s := range %s { %s += copy(%s[%s:], %s) }", value, arguments[0], offset, result, offset, value)
		g.line("return %s, nil", result)
	case nativeStdEnvGet:
		base, _ := optionalBase(resultType)
		g.line("value, present := os.LookupEnv(%s)", arguments[0])
		g.line("if !present { return slickNone[%s](), nil }", g.goType(base))
		g.line("return slickSome(value), nil")
	case nativeStdEnvSet:
		g.emitNativeEnvMutation(resultType, "Set", arguments[0], fmt.Sprintf("os.Setenv(%s, %s)", arguments[0], arguments[1]))
	case nativeStdEnvUnset:
		g.emitNativeEnvMutation(resultType, "Unset", arguments[0], fmt.Sprintf("os.Unsetenv(%s)", arguments[0]))
	case nativeStdPathJoin:
		g.line("return filepath.Join(%s...), nil", arguments[0])
	case nativeStdPathClean:
		g.line("return filepath.Clean(%s), nil", arguments[0])
	case nativeStdPathBase:
		g.line("return filepath.Base(%s), nil", arguments[0])
	case nativeStdPathDirectory:
		g.line("return filepath.Dir(%s), nil", arguments[0])
	case nativeStdPathExtension:
		g.line("value := filepath.Ext(%s)", arguments[0])
		g.line("if value == \"\" { return slickNone[string](), nil }")
		g.line("return slickSome(value), nil")
	case nativeStdPathIsAbsolute:
		g.line("return filepath.IsAbs(%s), nil", arguments[0])
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
