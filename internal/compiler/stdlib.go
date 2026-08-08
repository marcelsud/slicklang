package compiler

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

type nativeFunction string

const (
	nativeStdBytesFromUtf8        nativeFunction = "std.bytes.FromUtf8"
	nativeStdBytesToUtf8          nativeFunction = "std.bytes.ToUtf8"
	nativeStdBytesLength          nativeFunction = "std.bytes.Length"
	nativeStdBytesAt              nativeFunction = "std.bytes.At"
	nativeStdBytesConcat          nativeFunction = "std.bytes.Concat"
	nativeStdConvertParseInt      nativeFunction = "std.convert.ParseInt"
	nativeStdConvertParseFloat    nativeFunction = "std.convert.ParseFloat"
	nativeStdConvertIntToString   nativeFunction = "std.convert.IntToString"
	nativeStdConvertFloatToString nativeFunction = "std.convert.FloatToString"
	nativeStdEnvGet               nativeFunction = "std.env.Get"
	nativeStdEnvSet               nativeFunction = "std.env.Set"
	nativeStdEnvUnset             nativeFunction = "std.env.Unset"
	nativeStdFSReadText           nativeFunction = "std.fs.ReadText"
	nativeStdFSWriteText          nativeFunction = "std.fs.WriteText"
	nativeStdFSExists             nativeFunction = "std.fs.Exists"
	nativeStdFSCreateDirectoryAll nativeFunction = "std.fs.CreateDirectoryAll"
	nativeStdFSRemove             nativeFunction = "std.fs.Remove"
	nativeStdJsonDecode           nativeFunction = "std.json.Decode"
	nativeStdJsonEncode           nativeFunction = "std.json.Encode"
	nativeStdPathJoin             nativeFunction = "std.path.Join"
	nativeStdPathClean            nativeFunction = "std.path.Clean"
	nativeStdPathBase             nativeFunction = "std.path.Base"
	nativeStdPathDirectory        nativeFunction = "std.path.Directory"
	nativeStdPathExtension        nativeFunction = "std.path.Extension"
	nativeStdPathIsAbsolute       nativeFunction = "std.path.IsAbsolute"
	nativeStdTextTrim             nativeFunction = "std.text.Trim"
	nativeStdTextContains         nativeFunction = "std.text.Contains"
	nativeStdTextStartsWith       nativeFunction = "std.text.StartsWith"
	nativeStdTextEndsWith         nativeFunction = "std.text.EndsWith"
	nativeStdTextSplit            nativeFunction = "std.text.Split"
	nativeStdTextJoin             nativeFunction = "std.text.Join"
	nativeStdTextReplaceAll       nativeFunction = "std.text.ReplaceAll"
	nativeStdTextCut              nativeFunction = "std.text.Cut"

	stdBytesUtf8FailureName = "std.bytes.Utf8Failure"
	stdConvertFailureName   = "std.convert.Failure"
	stdEnvFailureName       = "std.env.Failure"
	stdFSFailureName        = "std.fs.Failure"
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
			canonical: string(nativeStdConvertParseInt),
			namespace: "std.convert",
			name:      "ParseInt",
			params:    []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<int,std.convert.Failure>"},
			native:    nativeStdConvertParseInt,
		},
		{
			canonical: string(nativeStdConvertParseFloat),
			namespace: "std.convert",
			name:      "ParseFloat",
			params:    []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<float,std.convert.Failure>"},
			native:    nativeStdConvertParseFloat,
		},
		{
			canonical: string(nativeStdConvertIntToString),
			namespace: "std.convert",
			name:      "IntToString",
			params:    []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdConvertIntToString,
		},
		{
			canonical: string(nativeStdConvertFloatToString),
			namespace: "std.convert",
			name:      "FloatToString",
			params:    []paramDecl{{name: "Value", typ: typeRef{name: "float"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdConvertFloatToString,
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
			canonical: string(nativeStdFSReadText),
			namespace: "std.fs",
			name:      "ReadText",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<string,std.fs.Failure>"},
			native:    nativeStdFSReadText,
		},
		{
			canonical: string(nativeStdFSWriteText),
			namespace: "std.fs",
			name:      "WriteText",
			params: []paramDecl{
				{name: "Path", typ: typeRef{name: "string"}},
				{name: "Contents", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "Result<null,std.fs.Failure>"},
			native: nativeStdFSWriteText,
		},
		{
			canonical: string(nativeStdFSExists),
			namespace: "std.fs",
			name:      "Exists",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<bool,std.fs.Failure>"},
			native:    nativeStdFSExists,
		},
		{
			canonical: string(nativeStdFSCreateDirectoryAll),
			namespace: "std.fs",
			name:      "CreateDirectoryAll",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<null,std.fs.Failure>"},
			native:    nativeStdFSCreateDirectoryAll,
		},
		{
			canonical: string(nativeStdFSRemove),
			namespace: "std.fs",
			name:      "Remove",
			params:    []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "Result<null,std.fs.Failure>"},
			native:    nativeStdFSRemove,
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
		{
			canonical: string(nativeStdTextTrim),
			namespace: "std.text",
			name:      "Trim",
			params:    []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:    typeRef{name: "string"},
			native:    nativeStdTextTrim,
		},
		{
			canonical: string(nativeStdTextContains),
			namespace: "std.text",
			name:      "Contains",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Search", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextContains,
		},
		{
			canonical: string(nativeStdTextStartsWith),
			namespace: "std.text",
			name:      "StartsWith",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Prefix", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextStartsWith,
		},
		{
			canonical: string(nativeStdTextEndsWith),
			namespace: "std.text",
			name:      "EndsWith",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Suffix", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextEndsWith,
		},
		{
			canonical: string(nativeStdTextSplit),
			namespace: "std.text",
			name:      "Split",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string[]"},
			native: nativeStdTextSplit,
		},
		{
			canonical: string(nativeStdTextJoin),
			namespace: "std.text",
			name:      "Join",
			params: []paramDecl{
				{name: "Parts", typ: typeRef{name: "string[]"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string"},
			native: nativeStdTextJoin,
		},
		{
			canonical: string(nativeStdTextReplaceAll),
			namespace: "std.text",
			name:      "ReplaceAll",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Old", typ: typeRef{name: "string"}},
				{name: "New", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string"},
			native: nativeStdTextReplaceAll,
		},
		{
			canonical: string(nativeStdTextCut),
			namespace: "std.text",
			name:      "Cut",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "(string,string)?"},
			native: nativeStdTextCut,
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
			canonical: stdConvertFailureName,
			namespace: "std.convert",
			name:      "Failure",
			isError:   true,
			fields: []fieldDecl{
				{name: "Target", typ: typeRef{name: "string"}},
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
			canonical: stdFSFailureName,
			namespace: "std.fs",
			name:      "Failure",
			isError:   true,
			fields: []fieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}},
				{name: "Path", typ: typeRef{name: "string"}},
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
	case nativeStdConvertParseInt:
		text := frame.locals["Text"].scalar.(string)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return runtimeConvertFailure(resultType, "int", parseIntFailureMessage(err)), nil
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "int", scalar: value}), nil
	case nativeStdConvertParseFloat:
		text := frame.locals["Text"].scalar.(string)
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return runtimeConvertFailure(resultType, "float", parseFloatFailureMessage(err)), nil
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "float", scalar: value}), nil
	case nativeStdConvertIntToString:
		return runtimeValue{typ: "string", scalar: strconv.FormatInt(frame.locals["Value"].scalar.(int64), 10)}, nil
	case nativeStdConvertFloatToString:
		value := frame.locals["Value"].scalar.(float64)
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return runtimeValue{}, fmt.Errorf("std.convert.FloatToString cannot format non-finite float")
		}
		return runtimeValue{typ: "string", scalar: strconv.FormatFloat(value, 'g', -1, 64)}, nil
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
	case nativeStdFSReadText:
		path := frame.locals["Path"].scalar.(string)
		contents, err := os.ReadFile(path)
		if err != nil {
			return runtimeFSFailure(resultType, "ReadText", path, err), nil
		}
		if !utf8.Valid(contents) {
			return runtimeFSFailure(resultType, "ReadText", path, fmt.Errorf("invalid UTF-8")), nil
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "string", scalar: string(contents)}), nil
	case nativeStdFSWriteText:
		path := frame.locals["Path"].scalar.(string)
		err := os.WriteFile(path, []byte(frame.locals["Contents"].scalar.(string)), 0o666)
		return runtimeFSResult(resultType, "WriteText", path, err), nil
	case nativeStdFSExists:
		path := frame.locals["Path"].scalar.(string)
		_, err := os.Stat(path)
		switch {
		case err == nil:
			return runtimeResultValue(resultType, true, runtimeValue{typ: "bool", scalar: true}), nil
		case os.IsNotExist(err):
			return runtimeResultValue(resultType, true, runtimeValue{typ: "bool", scalar: false}), nil
		default:
			return runtimeFSFailure(resultType, "Exists", path, err), nil
		}
	case nativeStdFSCreateDirectoryAll:
		path := frame.locals["Path"].scalar.(string)
		return runtimeFSResult(resultType, "CreateDirectoryAll", path, os.MkdirAll(path, 0o777)), nil
	case nativeStdFSRemove:
		path := frame.locals["Path"].scalar.(string)
		return runtimeFSResult(resultType, "Remove", path, os.Remove(path)), nil
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
	case nativeStdTextTrim:
		return runtimeValue{typ: "string", scalar: strings.TrimSpace(frame.locals["Text"].scalar.(string))}, nil
	case nativeStdTextContains:
		return runtimeValue{typ: "bool", scalar: strings.Contains(frame.locals["Text"].scalar.(string), frame.locals["Search"].scalar.(string))}, nil
	case nativeStdTextStartsWith:
		return runtimeValue{typ: "bool", scalar: strings.HasPrefix(frame.locals["Text"].scalar.(string), frame.locals["Prefix"].scalar.(string))}, nil
	case nativeStdTextEndsWith:
		return runtimeValue{typ: "bool", scalar: strings.HasSuffix(frame.locals["Text"].scalar.(string), frame.locals["Suffix"].scalar.(string))}, nil
	case nativeStdTextSplit:
		parts := strings.Split(frame.locals["Text"].scalar.(string), frame.locals["Separator"].scalar.(string))
		values := make([]runtimeValue, len(parts))
		for index, part := range parts {
			values[index] = runtimeValue{typ: "string", scalar: part}
		}
		return runtimeValue{typ: resultType, elements: values}, nil
	case nativeStdTextJoin:
		values := frame.locals["Parts"].elements
		parts := make([]string, len(values))
		for index, value := range values {
			parts[index] = value.scalar.(string)
		}
		return runtimeValue{typ: "string", scalar: strings.Join(parts, frame.locals["Separator"].scalar.(string))}, nil
	case nativeStdTextReplaceAll:
		return runtimeValue{typ: "string", scalar: strings.ReplaceAll(
			frame.locals["Text"].scalar.(string),
			frame.locals["Old"].scalar.(string),
			frame.locals["New"].scalar.(string),
		)}, nil
	case nativeStdTextCut:
		before, after, found := strings.Cut(frame.locals["Text"].scalar.(string), frame.locals["Separator"].scalar.(string))
		optional := &runtimeOptional{present: found}
		if found {
			optional.value = runtimeValue{
				typ: "(string,string)",
				elements: []runtimeValue{
					{typ: "string", scalar: before},
					{typ: "string", scalar: after},
				},
			}
		}
		return runtimeValue{typ: resultType, optional: optional}, nil
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
func runtimeResultValue(resultType string, ok bool, payload runtimeValue) runtimeValue {
	return runtimeValue{typ: resultType, result: &runtimeResult{ok: ok, payload: payload}}
}

func runtimeConvertFailure(resultType, target, message string) runtimeValue {
	failure := runtimeValue{
		typ: stdConvertFailureName,
		fields: map[string]runtimeValue{
			"Target":  {typ: "string", scalar: target},
			"Message": {typ: "string", scalar: message},
		},
	}
	return runtimeResultValue(resultType, false, failure)
}

func parseIntFailureMessage(err error) string {
	if errorsIsRange(err) {
		return "integer out of range"
	}
	return "invalid base-10 integer"
}

func parseFloatFailureMessage(err error) string {
	if errorsIsRange(err) {
		return "floating-point value out of range"
	}
	return "invalid floating-point number"
}

func errorsIsRange(err error) bool {
	numberError, ok := err.(*strconv.NumError)
	return ok && numberError.Err == strconv.ErrRange
}

func runtimeFSResult(resultType, operation, path string, err error) runtimeValue {
	if err != nil {
		return runtimeFSFailure(resultType, operation, path, err)
	}
	return runtimeResultValue(resultType, true, nullRuntimeValue())
}

func runtimeFSFailure(resultType, operation, path string, err error) runtimeValue {
	failure := runtimeValue{
		typ: stdFSFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Path":      {typ: "string", scalar: path},
			"Message":   {typ: "string", scalar: err.Error()},
		},
	}
	return runtimeResultValue(resultType, false, failure)
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
	case nativeStdConvertParseInt:
		result := g.goType(resultType)
		failure := goClassName(stdConvertFailureName)
		g.line("value, err := strconv.ParseInt(%s, 10, 64)", arguments[0])
		g.line("if err != nil {")
		g.line("message := %q", "invalid base-10 integer")
		g.line("if numberError, ok := err.(*strconv.NumError); ok && numberError.Err == strconv.ErrRange { message = %q }", "integer out of range")
		g.line("return %s{failure: &%s{%s: %q, %s: message}}, nil", result, failure, goFieldName("Target"), "int", goFieldName("Message"))
		g.line("}")
		g.line("return %s{ok: true, value: value}, nil", result)
	case nativeStdConvertParseFloat:
		result := g.goType(resultType)
		failure := goClassName(stdConvertFailureName)
		g.line("value, err := strconv.ParseFloat(%s, 64)", arguments[0])
		g.line("if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {")
		g.line("message := %q", "invalid floating-point number")
		g.line("if numberError, ok := err.(*strconv.NumError); ok && numberError.Err == strconv.ErrRange { message = %q }", "floating-point value out of range")
		g.line("return %s{failure: &%s{%s: %q, %s: message}}, nil", result, failure, goFieldName("Target"), "float", goFieldName("Message"))
		g.line("}")
		g.line("return %s{ok: true, value: value}, nil", result)
	case nativeStdConvertIntToString:
		g.line("return strconv.FormatInt(%s, 10), nil", arguments[0])
	case nativeStdConvertFloatToString:
		g.line("if math.IsInf(%s, 0) || math.IsNaN(%s) { return \"\", errors.New(%q) }", arguments[0], arguments[0], "std.convert.FloatToString cannot format non-finite float")
		g.line("return strconv.FormatFloat(%s, 'g', -1, 64), nil", arguments[0])
	case nativeStdEnvGet:
		base, _ := optionalBase(resultType)
		g.line("value, present := os.LookupEnv(%s)", arguments[0])
		g.line("if !present { return slickNone[%s](), nil }", g.goType(base))
		g.line("return slickSome(value), nil")
	case nativeStdEnvSet:
		g.emitNativeEnvMutation(resultType, "Set", arguments[0], fmt.Sprintf("os.Setenv(%s, %s)", arguments[0], arguments[1]))
	case nativeStdEnvUnset:
		g.emitNativeEnvMutation(resultType, "Unset", arguments[0], fmt.Sprintf("os.Unsetenv(%s)", arguments[0]))
	case nativeStdFSReadText:
		result := g.goType(resultType)
		g.line("contents, err := os.ReadFile(%s)", arguments[0])
		g.line("if err != nil {")
		g.emitNativeFSFailure(resultType, "ReadText", arguments[0], "err")
		g.line("}")
		g.line("if !utf8.Valid(contents) {")
		g.emitNativeFSFailure(resultType, "ReadText", arguments[0], `errors.New("invalid UTF-8")`)
		g.line("}")
		g.line("return %s{ok: true, value: string(contents)}, nil", result)
	case nativeStdFSWriteText:
		g.emitNativeFSResult(resultType, "WriteText", arguments[0], fmt.Sprintf("os.WriteFile(%s, []byte(%s), 0o666)", arguments[0], arguments[1]))
	case nativeStdFSExists:
		result := g.goType(resultType)
		g.line("_, err := os.Stat(%s)", arguments[0])
		g.line("if err == nil { return %s{ok: true, value: true}, nil }", result)
		g.line("if os.IsNotExist(err) { return %s{ok: true, value: false}, nil }", result)
		g.emitNativeFSFailure(resultType, "Exists", arguments[0], "err")
	case nativeStdFSCreateDirectoryAll:
		g.emitNativeFSResult(resultType, "CreateDirectoryAll", arguments[0], fmt.Sprintf("os.MkdirAll(%s, 0o777)", arguments[0]))
	case nativeStdFSRemove:
		g.emitNativeFSResult(resultType, "Remove", arguments[0], fmt.Sprintf("os.Remove(%s)", arguments[0]))
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
	case nativeStdTextTrim:
		g.line("return strings.TrimSpace(%s), nil", arguments[0])
	case nativeStdTextContains:
		g.line("return strings.Contains(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdTextStartsWith:
		g.line("return strings.HasPrefix(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdTextEndsWith:
		g.line("return strings.HasSuffix(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdTextSplit:
		g.line("return strings.Split(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdTextJoin:
		g.line("return strings.Join(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdTextReplaceAll:
		g.line("return strings.ReplaceAll(%s, %s, %s), nil", arguments[0], arguments[1], arguments[2])
	case nativeStdTextCut:
		g.line("before, after, found := strings.Cut(%s, %s)", arguments[0], arguments[1])
		g.line("if !found { return slickNone[[]any](), nil }")
		g.line("return slickSome([]any{before, after}), nil")
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

func (g *goGenerator) emitNativeFSResult(resultType, operation, path, call string) {
	g.line("if err := %s; err != nil {", call)
	g.emitNativeFSFailure(resultType, operation, path, "err")
	g.line("}")
	g.line("return %s{ok: true, value: struct{}{}}, nil", g.goType(resultType))
}

func (g *goGenerator) emitNativeFSFailure(resultType, operation, path, err string) {
	g.line("return %s{failure: &%s{%s: %q, %s: %s, %s: %s.Error()}}, nil",
		g.goType(resultType),
		goClassName(stdFSFailureName),
		goFieldName("Operation"), operation,
		goFieldName("Path"), path,
		goFieldName("Message"), err,
	)
}
