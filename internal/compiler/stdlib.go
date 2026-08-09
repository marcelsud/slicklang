package compiler

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type nativeFunction string

const (
	nativeStdBytesFromUtf8        nativeFunction = "std.bytes.FromUtf8"
	nativeStdBytesToUtf8          nativeFunction = "std.bytes.ToUtf8"
	nativeStdBytesLength          nativeFunction = "std.bytes.Length"
	nativeStdBytesAt              nativeFunction = "std.bytes.At"
	nativeStdBytesConcat          nativeFunction = "std.bytes.Concat"
	nativeStdBufferNew            nativeFunction = "std.buffer.New"
	nativeStdBufferPush           nativeFunction = "std.buffer.Push"
	nativeStdBufferGet            nativeFunction = "std.buffer.Get"
	nativeStdBufferSet            nativeFunction = "std.buffer.Set"
	nativeStdBufferLength         nativeFunction = "std.buffer.Length"
	nativeStdBufferFreeze         nativeFunction = "std.buffer.Freeze"
	nativeStdBytesSlice           nativeFunction = "std.bytes.Slice"
	nativeStdBytesFromValues      nativeFunction = "std.bytes.FromValues"
	nativeStdUTF8DecodeAt         nativeFunction = "std.utf8.DecodeAt"
	nativeStdUnicodeIsLetter      nativeFunction = "std.unicode.IsLetter"
	nativeStdUnicodeIsDigit       nativeFunction = "std.unicode.IsDigit"
	nativeStdUnicodeIsWhitespace  nativeFunction = "std.unicode.IsWhitespace"
	nativeStdUnicodeIsUpper       nativeFunction = "std.unicode.IsUpper"
	nativeStdConvertParseInt      nativeFunction = "std.convert.ParseInt"
	nativeStdConvertParseFloat    nativeFunction = "std.convert.ParseFloat"
	nativeStdConvertIntToString   nativeFunction = "std.convert.IntToString"
	nativeStdConvertFloatToString nativeFunction = "std.convert.FloatToString"
	nativeStdMathDivide           nativeFunction = "std.math.Divide"
	nativeStdMathRemainder        nativeFunction = "std.math.Remainder"
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
	nativeStdTextQuote            nativeFunction = "std.text.Quote"
	nativeStdIOReaderFromBytes    nativeFunction = "std.io.ReaderFromBytes"
	nativeStdIOWriterToBytes      nativeFunction = "std.io.WriterToBytes"
	nativeStdIOReadAll            nativeFunction = "std.io.ReadAll"
	nativeStdIOCopy               nativeFunction = "std.io.Copy"
	nativeStdIOReaderRead         nativeFunction = "std.io.bytesReader.Read"
	nativeStdIOReaderClose        nativeFunction = "std.io.bytesReader.Close"
	nativeStdIOWriterWrite        nativeFunction = "std.io.BytesWriter.Write"
	nativeStdIOWriterBytes        nativeFunction = "std.io.BytesWriter.Bytes"
	nativeStdIOWriterClose        nativeFunction = "std.io.BytesWriter.Close"

	stdBytesUtf8FailureName         = "std.bytes.Utf8Failure"
	stdCollectionsBoundsFailureName = "std.collections.BoundsFailure"
	stdConvertFailureName           = "std.convert.Failure"
	stdMathArithmeticFailureName    = "std.math.ArithmeticFailure"
	stdEnvFailureName               = "std.env.Failure"
	stdFSFailureName                = "std.fs.Failure"
	stdJsonFailureName              = "std.json.Failure"
	stdIOFailureName                = "std.io.Failure"
	stdIOReaderName                 = "std.io.Reader"
	stdIOWriterName                 = "std.io.Writer"
	stdIOBytesReaderName            = "std.io.bytesReader"
	stdIOBytesWriterName            = "std.io.BytesWriter"
	stdBytesBoundsFailureName       = "std.bytes.BoundsFailure"
	stdBytesValueFailureName        = "std.bytes.ValueFailure"
	stdUTF8DecodedRuneName          = "std.utf8.DecodedRune"
	stdUTF8FailureName              = "std.utf8.Failure"
)

func isNativeStdBuffer(native nativeFunction) bool {
	switch native {
	case nativeStdBufferNew, nativeStdBufferPush, nativeStdBufferGet,
		nativeStdBufferSet, nativeStdBufferLength, nativeStdBufferFreeze:
		return true
	default:
		return false
	}
}

type standardNamespaceDecl struct {
	canonical     string
	documentation string
}

type standardFunctionDecl struct {
	canonical     string
	namespace     string
	name          string
	documentation string
	typeParams    []string
	params        []paramDecl
	result        typeRef
	native        nativeFunction
}

type standardMethodDecl struct {
	name          string
	documentation string
	params        []paramDecl
	result        typeRef
	throws        []typeRef
	native        nativeFunction
}

type standardFieldDecl struct {
	name          string
	typ           typeRef
	documentation string
}

type standardClassDecl struct {
	canonical      string
	namespace      string
	name           string
	documentation  string
	isError        bool
	nativeResource bool
	fields         []standardFieldDecl
	methods        []standardMethodDecl
}

type standardInterfaceDecl struct {
	canonical     string
	namespace     string
	name          string
	documentation string
	methods       []standardMethodDecl
}

// standardLibraryRegistry is the authoritative public Slick surface backed by
// the Go standard library. The compiler, interpreter, and Go backend all use
// the synthetic declarations registered from this table.
var standardLibraryRegistry = struct {
	namespaces []standardNamespaceDecl
	functions  []standardFunctionDecl
	classes    []standardClassDecl
	interfaces []standardInterfaceDecl
}{
	namespaces: []standardNamespaceDecl{
		{canonical: "std", documentation: "Provides compiler-owned portable standard-library components."},
		{canonical: "std.bytes", documentation: "Converts and inspects immutable binary byte values."},
		{canonical: "std.buffer", documentation: "Builds mutable sequences that freeze into immutable array snapshots."},
		{canonical: "std.collections", documentation: "Defines failures shared by compiler-owned collection operations."},
		{canonical: "std.convert", documentation: "Converts primitive values with explicit parse failures."},
		{canonical: "std.math", documentation: "Provides checked integer arithmetic that returns typed Result failures."},
		{canonical: "std.env", documentation: "Reads and updates the process environment without exposing values in failures."},
		{canonical: "std.fs", documentation: "Performs bounded whole-file and directory operations on platform paths."},
		{canonical: "std.json", documentation: "Encodes and decodes supported Slick values as JSON."},
		{canonical: "std.http", documentation: "Performs synchronous fully buffered HTTP requests with typed failures."},
		{canonical: "std.path", documentation: "Manipulates platform-dependent filesystem path strings without accessing the filesystem."},
		{canonical: "std.text", documentation: "Provides deterministic Unicode-aware and substring text operations."},
		{canonical: "std.io", documentation: "Provides bounded resource-safe byte readers, writers, and transfer helpers."},
		{canonical: "std.utf8", documentation: "Decodes Unicode scalar values from immutable UTF-8 bytes."},
		{canonical: "std.unicode", documentation: "Classifies Unicode scalar values using the toolchain's pinned tables."},
	},
	functions: []standardFunctionDecl{
		{
			canonical:     string(nativeStdBytesFromUtf8),
			namespace:     "std.bytes",
			name:          "FromUtf8",
			documentation: "Encodes Text as immutable UTF-8 bytes.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "bytes"},
			native:        nativeStdBytesFromUtf8,
		},
		{
			canonical:     string(nativeStdBytesToUtf8),
			namespace:     "std.bytes",
			name:          "ToUtf8",
			documentation: "Decodes Value as UTF-8 or returns Utf8Failure for invalid data.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: "Result<string,std.bytes.Utf8Failure>"},
			native:        nativeStdBytesToUtf8,
		},
		{
			canonical:     string(nativeStdBytesLength),
			namespace:     "std.bytes",
			name:          "Length",
			documentation: "Returns the number of bytes in Value.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: "int"},
			native:        nativeStdBytesLength,
		},
		{
			canonical:     string(nativeStdBytesAt),
			namespace:     "std.bytes",
			name:          "At",
			documentation: "Returns the byte at Index, or null when Index is outside Value.",
			params: []paramDecl{
				{name: "Value", typ: typeRef{name: "bytes"}},
				{name: "Index", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "int?"},
			native: nativeStdBytesAt,
		},
		{
			canonical:     string(nativeStdBytesConcat),
			namespace:     "std.bytes",
			name:          "Concat",
			documentation: "Concatenates Values in order into a new immutable byte value.",
			params:        []paramDecl{{name: "Values", typ: typeRef{name: "bytes[]"}}},
			result:        typeRef{name: "bytes"},
			native:        nativeStdBytesConcat,
		},
		{
			canonical:     string(nativeStdBufferNew),
			namespace:     "std.buffer",
			name:          "New",
			documentation: "Creates an empty growable buffer for T values.",
			typeParams:    []string{"T"},
			result:        typeRef{name: "Buffer<T>"},
			native:        nativeStdBufferNew,
		},
		{
			canonical:     string(nativeStdBufferPush),
			namespace:     "std.buffer",
			name:          "Push",
			documentation: "Appends Value to Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Value", typ: typeRef{name: "T"}},
			},
			result: typeRef{name: "null"},
			native: nativeStdBufferPush,
		},
		{
			canonical:     string(nativeStdBufferGet),
			namespace:     "std.buffer",
			name:          "Get",
			documentation: "Returns the value at Index, or null when Index is outside Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Index", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "T?"},
			native: nativeStdBufferGet,
		},
		{
			canonical:     string(nativeStdBufferSet),
			namespace:     "std.buffer",
			name:          "Set",
			documentation: "Replaces the value at Index or returns BoundsFailure without growing Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Index", typ: typeRef{name: "int"}},
				{name: "Value", typ: typeRef{name: "T"}},
			},
			result: typeRef{name: "Result<null," + stdCollectionsBoundsFailureName + ">"},
			native: nativeStdBufferSet,
		},
		{
			canonical:     string(nativeStdBufferLength),
			namespace:     "std.buffer",
			name:          "Length",
			documentation: "Returns the number of values in Buffer.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Buffer", typ: typeRef{name: "Buffer<T>"}}},
			result:        typeRef{name: "int"},
			native:        nativeStdBufferLength,
		},
		{
			canonical:     string(nativeStdBufferFreeze),
			namespace:     "std.buffer",
			name:          "Freeze",
			documentation: "Copies Buffer into an immutable array snapshot.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Buffer", typ: typeRef{name: "Buffer<T>"}}},
			result:        typeRef{name: "T[]"},
			native:        nativeStdBufferFreeze,
		},
		{
			canonical:     string(nativeStdBytesSlice),
			namespace:     "std.bytes",
			name:          "Slice",
			documentation: "Copies the half-open byte range from Start through End or returns BoundsFailure.",
			params: []paramDecl{
				{name: "Value", typ: typeRef{name: "bytes"}},
				{name: "Start", typ: typeRef{name: "int"}},
				{name: "End", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<bytes," + stdBytesBoundsFailureName + ">"},
			native: nativeStdBytesSlice,
		},
		{
			canonical:     string(nativeStdBytesFromValues),
			namespace:     "std.bytes",
			name:          "FromValues",
			documentation: "Constructs immutable bytes from integer values or reports the first value outside 0 through 255.",
			params:        []paramDecl{{name: "Values", typ: typeRef{name: "int[]"}}},
			result:        typeRef{name: "Result<bytes," + stdBytesValueFailureName + ">"},
			native:        nativeStdBytesFromValues,
		},
		{
			canonical:     string(nativeStdUTF8DecodeAt),
			namespace:     "std.utf8",
			name:          "DecodeAt",
			documentation: "Decodes the Unicode scalar value beginning at byte Index or returns Failure.",
			params: []paramDecl{
				{name: "Value", typ: typeRef{name: "bytes"}},
				{name: "Index", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<" + stdUTF8DecodedRuneName + "," + stdUTF8FailureName + ">"},
			native: nativeStdUTF8DecodeAt,
		},
		{
			canonical:     string(nativeStdUnicodeIsLetter),
			namespace:     "std.unicode",
			name:          "IsLetter",
			documentation: "Reports whether Value is a Unicode scalar classified as a letter.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsLetter,
		},
		{
			canonical:     string(nativeStdUnicodeIsDigit),
			namespace:     "std.unicode",
			name:          "IsDigit",
			documentation: "Reports whether Value is a Unicode decimal digit.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsDigit,
		},
		{
			canonical:     string(nativeStdUnicodeIsWhitespace),
			namespace:     "std.unicode",
			name:          "IsWhitespace",
			documentation: "Reports whether Value is a Unicode white-space scalar.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsWhitespace,
		},
		{
			canonical:     string(nativeStdUnicodeIsUpper),
			namespace:     "std.unicode",
			name:          "IsUpper",
			documentation: "Reports whether Value is an uppercase Unicode scalar.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsUpper,
		},
		{
			canonical:     string(nativeStdConvertParseInt),
			namespace:     "std.convert",
			name:          "ParseInt",
			documentation: "Parses a base-10 integer or returns Failure when Text is invalid or out of range.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<int,std.convert.Failure>"},
			native:        nativeStdConvertParseInt,
		},
		{
			canonical:     string(nativeStdConvertParseFloat),
			namespace:     "std.convert",
			name:          "ParseFloat",
			documentation: "Parses a finite floating-point value or returns Failure when Text is invalid or out of range.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<float,std.convert.Failure>"},
			native:        nativeStdConvertParseFloat,
		},
		{
			canonical:     string(nativeStdConvertIntToString),
			namespace:     "std.convert",
			name:          "IntToString",
			documentation: "Formats Value as a base-10 integer string.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdConvertIntToString,
		},
		{
			canonical:     string(nativeStdConvertFloatToString),
			namespace:     "std.convert",
			name:          "FloatToString",
			documentation: "Formats Value as a deterministic floating-point string.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "float"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdConvertFloatToString,
		},
		{
			canonical:     string(nativeStdMathDivide),
			namespace:     "std.math",
			name:          "Divide",
			documentation: "Returns Dividend / Divisor truncated toward zero, or ArithmeticFailure for a zero divisor or the non-representable minimum-int / -1 case.",
			params: []paramDecl{
				{name: "Dividend", typ: typeRef{name: "int"}},
				{name: "Divisor", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<int,std.math.ArithmeticFailure>"},
			native: nativeStdMathDivide,
		},
		{
			canonical:     string(nativeStdMathRemainder),
			namespace:     "std.math",
			name:          "Remainder",
			documentation: "Returns the remainder of Dividend / Divisor with the dividend's sign, or ArithmeticFailure when Divisor is zero.",
			params: []paramDecl{
				{name: "Dividend", typ: typeRef{name: "int"}},
				{name: "Divisor", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<int,std.math.ArithmeticFailure>"},
			native: nativeStdMathRemainder,
		},
		{
			canonical:     string(nativeStdEnvGet),
			namespace:     "std.env",
			name:          "Get",
			documentation: "Returns the environment value for Name, or null when Name is unset.",
			params:        []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string?"},
			native:        nativeStdEnvGet,
		},
		{
			canonical:     string(nativeStdEnvSet),
			namespace:     "std.env",
			name:          "Set",
			documentation: "Sets Name to Value or returns Failure without including Value in the error.",
			params: []paramDecl{
				{name: "Name", typ: typeRef{name: "string"}},
				{name: "Value", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "Result<null,std.env.Failure>"},
			native: nativeStdEnvSet,
		},
		{
			canonical:     string(nativeStdEnvUnset),
			namespace:     "std.env",
			name:          "Unset",
			documentation: "Removes Name from the environment or returns Failure.",
			params:        []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.env.Failure>"},
			native:        nativeStdEnvUnset,
		},
		{
			canonical:     string(nativeStdFSReadText),
			namespace:     "std.fs",
			name:          "ReadText",
			documentation: "Reads Path completely as UTF-8 text or returns Failure for I/O or invalid UTF-8.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<string,std.fs.Failure>"},
			native:        nativeStdFSReadText,
		},
		{
			canonical:     string(nativeStdFSWriteText),
			namespace:     "std.fs",
			name:          "WriteText",
			documentation: "Writes Contents to Path, replacing the file, or returns Failure.",
			params: []paramDecl{
				{name: "Path", typ: typeRef{name: "string"}},
				{name: "Contents", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "Result<null,std.fs.Failure>"},
			native: nativeStdFSWriteText,
		},
		{
			canonical:     string(nativeStdFSExists),
			namespace:     "std.fs",
			name:          "Exists",
			documentation: "Reports whether Path exists or returns Failure when its status cannot be read.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<bool,std.fs.Failure>"},
			native:        nativeStdFSExists,
		},
		{
			canonical:     string(nativeStdFSCreateDirectoryAll),
			namespace:     "std.fs",
			name:          "CreateDirectoryAll",
			documentation: "Creates Path and every missing parent directory or returns Failure.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.fs.Failure>"},
			native:        nativeStdFSCreateDirectoryAll,
		},
		{
			canonical:     string(nativeStdFSRemove),
			namespace:     "std.fs",
			name:          "Remove",
			documentation: "Removes the file or empty directory at Path or returns Failure.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.fs.Failure>"},
			native:        nativeStdFSRemove,
		},
		{
			canonical:     string(nativeStdJsonDecode),
			namespace:     "std.json",
			name:          "Decode",
			documentation: "Decodes JSON Text into T or returns Failure with a structural path.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<T,std.json.Failure>"},
			native:        nativeStdJsonDecode,
		},
		{
			canonical:     string(nativeStdJsonEncode),
			namespace:     "std.json",
			name:          "Encode",
			documentation: "Encodes Value as JSON or returns Failure for unsupported data.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "T"}}},
			result:        typeRef{name: "Result<string,std.json.Failure>"},
			native:        nativeStdJsonEncode,
		},
		{
			canonical:     string(nativeStdHTTPFetch),
			namespace:     "std.http",
			name:          "Fetch",
			documentation: "Executes Request and returns a fully buffered Response or typed Failure.",
			params:        []paramDecl{{name: "Request", typ: typeRef{name: stdHTTPRequestName}}},
			result:        typeRef{name: "Result<" + stdHTTPResponseName + "," + stdHTTPFailureName + ">"},
			native:        nativeStdHTTPFetch,
		},
		{
			canonical:     string(nativeStdHTTPHeaderValues),
			namespace:     "std.http",
			name:          "HeaderValues",
			documentation: "Returns all values for Name using case-insensitive header lookup.",
			params: []paramDecl{
				{name: "Headers", typ: typeRef{name: "Map<string,string[]>"}},
				{name: "Name", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string[]"},
			native: nativeStdHTTPHeaderValues,
		},
		{
			canonical:     string(nativeStdHTTPStatusText),
			namespace:     "std.http",
			name:          "StatusText",
			documentation: "Returns the standard text for Status, or null when Status is unknown.",
			params:        []paramDecl{{name: "Status", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "string?"},
			native:        nativeStdHTTPStatusText,
		},
		{
			canonical:     string(nativeStdPathJoin),
			namespace:     "std.path",
			name:          "Join",
			documentation: "Joins Parts using platform path rules and cleans the result.",
			params:        []paramDecl{{name: "Parts", typ: typeRef{name: "string[]"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathJoin,
		},
		{
			canonical:     string(nativeStdPathClean),
			namespace:     "std.path",
			name:          "Clean",
			documentation: "Returns the shortest platform-equivalent spelling of Path.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathClean,
		},
		{
			canonical:     string(nativeStdPathBase),
			namespace:     "std.path",
			name:          "Base",
			documentation: "Returns the final element of Path after platform cleaning.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathBase,
		},
		{
			canonical:     string(nativeStdPathDirectory),
			namespace:     "std.path",
			name:          "Directory",
			documentation: "Returns every element of Path except the final one.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathDirectory,
		},
		{
			canonical:     string(nativeStdPathExtension),
			namespace:     "std.path",
			name:          "Extension",
			documentation: "Returns Path's final extension without the dot, or null when absent.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string?"},
			native:        nativeStdPathExtension,
		},
		{
			canonical:     string(nativeStdPathIsAbsolute),
			namespace:     "std.path",
			name:          "IsAbsolute",
			documentation: "Reports whether Path is absolute under the current platform rules.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdPathIsAbsolute,
		},
		{
			canonical:     string(nativeStdTextTrim),
			namespace:     "std.text",
			name:          "Trim",
			documentation: "Removes leading and trailing Unicode whitespace from Text.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdTextTrim,
		},
		{
			canonical:     string(nativeStdTextContains),
			namespace:     "std.text",
			name:          "Contains",
			documentation: "Reports whether Text contains Search as an exact substring.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Search", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextContains,
		},
		{
			canonical:     string(nativeStdTextStartsWith),
			namespace:     "std.text",
			name:          "StartsWith",
			documentation: "Reports whether Text begins with Prefix.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Prefix", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextStartsWith,
		},
		{
			canonical:     string(nativeStdTextEndsWith),
			namespace:     "std.text",
			name:          "EndsWith",
			documentation: "Reports whether Text ends with Suffix.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Suffix", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "bool"},
			native: nativeStdTextEndsWith,
		},
		{
			canonical:     string(nativeStdTextSplit),
			namespace:     "std.text",
			name:          "Split",
			documentation: "Splits Text at every Separator occurrence while preserving order.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string[]"},
			native: nativeStdTextSplit,
		},
		{
			canonical:     string(nativeStdTextJoin),
			namespace:     "std.text",
			name:          "Join",
			documentation: "Joins Parts in order with Separator between adjacent values.",
			params: []paramDecl{
				{name: "Parts", typ: typeRef{name: "string[]"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string"},
			native: nativeStdTextJoin,
		},
		{
			canonical:     string(nativeStdTextReplaceAll),
			namespace:     "std.text",
			name:          "ReplaceAll",
			documentation: "Returns Text with every non-overlapping Old substring replaced by New.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Old", typ: typeRef{name: "string"}},
				{name: "New", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "string"},
			native: nativeStdTextReplaceAll,
		},
		{
			canonical:     string(nativeStdTextCut),
			namespace:     "std.text",
			name:          "Cut",
			documentation: "Splits Text at the first Separator, or returns null when Separator is absent.",
			params: []paramDecl{
				{name: "Text", typ: typeRef{name: "string"}},
				{name: "Separator", typ: typeRef{name: "string"}},
			},
			result: typeRef{name: "(string,string)?"},
			native: nativeStdTextCut,
		},
		{
			canonical:     string(nativeStdTextQuote),
			namespace:     "std.text",
			name:          "Quote",
			documentation: "Returns Text as a deterministic double-quoted Slick string literal.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdTextQuote,
		},
		{
			canonical:     string(nativeStdIOReaderFromBytes),
			namespace:     "std.io",
			name:          "ReaderFromBytes",
			documentation: "Creates a closeable Reader over an immutable snapshot of Value.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: stdIOReaderName},
			native:        nativeStdIOReaderFromBytes,
		},
		{
			canonical:     string(nativeStdIOWriterToBytes),
			namespace:     "std.io",
			name:          "WriterToBytes",
			documentation: "Creates a closeable in-memory BytesWriter.",
			result:        typeRef{name: stdIOBytesWriterName},
			native:        nativeStdIOWriterToBytes,
		},
		{
			canonical:     string(nativeStdIOReadAll),
			namespace:     "std.io",
			name:          "ReadAll",
			documentation: "Reads through Reader until end-of-stream without accepting more than MaxBytes.",
			params: []paramDecl{
				{name: "Reader", typ: typeRef{name: stdIOReaderName}},
				{name: "MaxBytes", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<bytes," + stdIOFailureName + ">"},
			native: nativeStdIOReadAll,
		},
		{
			canonical:     string(nativeStdIOCopy),
			namespace:     "std.io",
			name:          "Copy",
			documentation: "Copies bytes from Reader to Writer without writing beyond MaxBytes.",
			params: []paramDecl{
				{name: "Reader", typ: typeRef{name: stdIOReaderName}},
				{name: "Writer", typ: typeRef{name: stdIOWriterName}},
				{name: "MaxBytes", typ: typeRef{name: "int"}},
			},
			result: typeRef{name: "Result<int," + stdIOFailureName + ">"},
			native: nativeStdIOCopy,
		},
	},
	classes: []standardClassDecl{
		{
			canonical:     stdCollectionsBoundsFailureName,
			namespace:     "std.collections",
			name:          "BoundsFailure",
			documentation: "Reports an invalid half-open collection range or indexed write.",
			isError:       true,
		},
		{
			canonical:     stdBytesBoundsFailureName,
			namespace:     "std.bytes",
			name:          "BoundsFailure",
			documentation: "Describes invalid bounds for a half-open immutable byte slice.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Start", typ: typeRef{name: "int"}, documentation: "Provides the requested inclusive start byte offset."},
				{name: "End", typ: typeRef{name: "int"}, documentation: "Provides the requested exclusive end byte offset."},
				{name: "Length", typ: typeRef{name: "int"}, documentation: "Provides the source byte length."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why the requested bounds are invalid."},
			},
		},
		{
			canonical:     stdBytesValueFailureName,
			namespace:     "std.bytes",
			name:          "ValueFailure",
			documentation: "Describes the first integer that cannot represent a byte.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Index", typ: typeRef{name: "int"}, documentation: "Provides the index of the first invalid value."},
				{name: "Value", typ: typeRef{name: "int"}, documentation: "Provides the invalid integer value."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the valid byte value range."},
			},
		},
		{
			canonical:     stdUTF8DecodedRuneName,
			namespace:     "std.utf8",
			name:          "DecodedRune",
			documentation: "Contains one decoded Unicode scalar value and its UTF-8 byte width.",
			fields: []standardFieldDecl{
				{name: "Value", typ: typeRef{name: "int"}, documentation: "Provides the Unicode scalar value."},
				{name: "Width", typ: typeRef{name: "int"}, documentation: "Provides the scalar's UTF-8 width in bytes."},
			},
		},
		{
			canonical:     stdUTF8FailureName,
			namespace:     "std.utf8",
			name:          "Failure",
			documentation: "Describes an invalid byte offset or UTF-8 encoding.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Index", typ: typeRef{name: "int"}, documentation: "Provides the requested byte offset."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why decoding failed."},
			},
		},
		{
			canonical:     stdBytesUtf8FailureName,
			namespace:     "std.bytes",
			name:          "Utf8Failure",
			documentation: "Describes a failure to decode immutable bytes as UTF-8 text.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why the byte value is not valid UTF-8."},
			},
		},
		{
			canonical:     stdConvertFailureName,
			namespace:     "std.convert",
			name:          "Failure",
			documentation: "Describes a failed primitive conversion.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Target", typ: typeRef{name: "string"}, documentation: "Names the requested destination type."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why conversion failed without exposing unrelated data."},
			},
		},
		{
			canonical:     stdMathArithmeticFailureName,
			namespace:     "std.math",
			name:          "ArithmeticFailure",
			documentation: "Describes a failed checked integer division or remainder operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the failing operation: Divide or Remainder."},
				{name: "Kind", typ: typeRef{name: "string"}, documentation: "Names the failure kind: DivisionByZero or Overflow."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the arithmetic failure."},
			},
		},
		{
			canonical:     stdEnvFailureName,
			namespace:     "std.env",
			name:          "Failure",
			documentation: "Describes a failed process-environment operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the environment operation that failed."},
				{name: "Name", typ: typeRef{name: "string"}, documentation: "Names the environment entry involved in the failure."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without including the environment value."},
			},
		},
		{
			canonical:     stdFSFailureName,
			namespace:     "std.fs",
			name:          "Failure",
			documentation: "Describes a failed filesystem operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the filesystem operation that failed."},
				{name: "Path", typ: typeRef{name: "string"}, documentation: "Identifies the path involved in the failure."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the platform filesystem failure."},
			},
		},
		{
			canonical:     stdJsonFailureName,
			namespace:     "std.json",
			name:          "Failure",
			documentation: "Describes a failed JSON encoding or decoding operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the JSON operation that failed."},
				{name: "Path", typ: typeRef{name: "string"}, documentation: "Identifies the structural location of the failure."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the JSON failure without including complete source data."},
			},
		},
		{
			canonical:     stdHTTPRequestName,
			namespace:     "std.http",
			name:          "Request",
			documentation: "Describes one fully buffered HTTP request.",
			fields: []standardFieldDecl{
				{name: "Method", typ: typeRef{name: "string"}, documentation: "Provides the exact HTTP method token."},
				{name: "URL", typ: typeRef{name: "string"}, documentation: "Provides an absolute HTTP or HTTPS URL."},
				{name: "Headers", typ: typeRef{name: "Map<string,string[]>?"}, documentation: "Provides optional request headers."},
				{name: "Body", typ: typeRef{name: "bytes?"}, documentation: "Provides an optional immutable request body."},
				{name: "TimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Provides a positive whole-request timeout in milliseconds."},
				{name: "MaxResponseBytes", typ: typeRef{name: "int?"}, documentation: "Provides a positive buffered response limit."},
				{name: "FollowRedirects", typ: typeRef{name: "bool?"}, documentation: "Controls whether up to ten redirects are followed."},
			},
		},
		{
			canonical:     stdHTTPResponseName,
			namespace:     "std.http",
			name:          "Response",
			documentation: "Contains a complete buffered HTTP response.",
			fields: []standardFieldDecl{
				{name: "Status", typ: typeRef{name: "int"}, documentation: "Provides the HTTP status code."},
				{name: "URL", typ: typeRef{name: "string"}, documentation: "Provides the effective final URL."},
				{name: "Headers", typ: typeRef{name: "Map<string,string[]>"}, documentation: "Provides deterministic canonical response headers."},
				{name: "Body", typ: typeRef{name: "bytes"}, documentation: "Provides the immutable complete response body."},
			},
		},
		{
			canonical:     stdHTTPFailureName,
			namespace:     "std.http",
			name:          "Failure",
			documentation: "Describes a sanitized HTTP request failure.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Kind", typ: typeRef{name: "string"}, documentation: "Provides the stable failure classification."},
				{name: "URL", typ: typeRef{name: "string"}, documentation: "Provides the sanitized URL without userinfo, query, or fragment."},
				{name: "Status", typ: typeRef{name: "int?"}, documentation: "Provides the response status when one was received."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without exposing request or response secrets."},
			},
		},
		{
			canonical:     stdIOFailureName,
			namespace:     "std.io",
			name:          "Failure",
			documentation: "Describes a failed byte-stream operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the byte-stream operation that failed."},
				{name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without including transferred byte contents."},
			},
		},
		{
			canonical:      stdIOBytesReaderName,
			namespace:      "std.io",
			name:           "bytesReader",
			documentation:  "Implements Reader over an immutable byte snapshot.",
			nativeResource: true,
			methods: []standardMethodDecl{
				{
					name:          "Read",
					documentation: "Reads at most MaxBytes and returns null only at end-of-stream.",
					params:        []paramDecl{{name: "MaxBytes", typ: typeRef{name: "int"}}},
					result:        typeRef{name: "Result<bytes?," + stdIOFailureName + ">"},
					native:        nativeStdIOReaderRead,
				},
				{
					name:          "Close",
					documentation: "Closes the reader or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					native:        nativeStdIOReaderClose,
				},
			},
		},
		{
			canonical:      stdIOBytesWriterName,
			namespace:      "std.io",
			name:           "BytesWriter",
			documentation:  "Collects written bytes in memory and exposes immutable snapshots.",
			nativeResource: true,
			methods: []standardMethodDecl{
				{
					name:          "Write",
					documentation: "Writes the complete immutable Data chunk or returns Failure.",
					params:        []paramDecl{{name: "Data", typ: typeRef{name: "bytes"}}},
					result:        typeRef{name: "Result<null," + stdIOFailureName + ">"},
					native:        nativeStdIOWriterWrite,
				},
				{
					name:          "Bytes",
					documentation: "Returns an immutable snapshot of all bytes written so far.",
					result:        typeRef{name: "bytes"},
					native:        nativeStdIOWriterBytes,
				},
				{
					name:          "Close",
					documentation: "Closes the writer or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					native:        nativeStdIOWriterClose,
				},
			},
		},
	},
	interfaces: []standardInterfaceDecl{
		{
			canonical:     stdIOReaderName,
			namespace:     "std.io",
			name:          "Reader",
			documentation: "Reads bounded immutable byte chunks and supports deterministic cleanup.",
			methods: []standardMethodDecl{
				{
					name:          "Read",
					documentation: "Reads at most MaxBytes and returns null only at end-of-stream.",
					params:        []paramDecl{{name: "MaxBytes", typ: typeRef{name: "int"}}},
					result:        typeRef{name: "Result<bytes?," + stdIOFailureName + ">"},
				},
				{
					name:          "Close",
					documentation: "Closes the reader or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
				},
			},
		},
		{
			canonical:     stdIOWriterName,
			namespace:     "std.io",
			name:          "Writer",
			documentation: "Writes complete immutable byte chunks and supports deterministic cleanup.",
			methods: []standardMethodDecl{
				{
					name:          "Write",
					documentation: "Writes the complete immutable Data chunk or returns Failure.",
					params:        []paramDecl{{name: "Data", typ: typeRef{name: "bytes"}}},
					result:        typeRef{name: "Result<null," + stdIOFailureName + ">"},
				},
				{
					name:          "Close",
					documentation: "Closes the writer or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
				},
			},
		},
	},
}

func registerStandardLibrary(p *program) {
	for _, declaration := range standardLibraryRegistry.namespaces {
		documentation := declaration.documentation
		p.namespaceDocumentation[declaration.canonical] = &documentation
	}
	for _, declaration := range standardLibraryRegistry.functions {
		documentation := declaration.documentation
		p.functions[declaration.canonical] = &functionDecl{
			name:          declaration.name,
			qualified:     declaration.canonical,
			namespace:     declaration.namespace,
			aliases:       make(map[string]aliasDecl),
			typeParams:    append([]string(nil), declaration.typeParams...),
			params:        declaration.params,
			result:        declaration.result,
			native:        declaration.native,
			documentation: &documentation,
		}
	}
	for _, declaration := range standardLibraryRegistry.classes {
		fields := make(map[string]fieldDecl, len(declaration.fields))
		for _, field := range declaration.fields {
			documentation := field.documentation
			fields[field.name] = fieldDecl{
				name:          field.name,
				typ:           field.typ,
				documentation: &documentation,
			}
		}
		methods := make(map[string]*methodSignature, len(declaration.methods))
		for _, method := range declaration.methods {
			methods[method.name] = standardMethodSignature(declaration.namespace, method)
			p.methodImpls = append(p.methodImpls, &functionDecl{
				name:              method.name,
				namespace:         declaration.namespace,
				aliases:           make(map[string]aliasDecl),
				params:            method.params,
				result:            method.result,
				throws:            method.throws,
				receiver:          typeRef{name: declaration.canonical},
				receiverCanonical: declaration.canonical,
				inline:            true,
				native:            method.native,
			})
		}
		documentation := declaration.documentation
		p.classes[declaration.canonical] = &classDecl{
			name:            declaration.name,
			qualified:       declaration.canonical,
			namespace:       declaration.namespace,
			aliases:         make(map[string]aliasDecl),
			isError:         declaration.isError,
			nativeResource:  declaration.nativeResource,
			extension:       extensionNone,
			fields:          fields,
			methods:         methods,
			effective:       make(map[string]*methodSignature),
			implementations: make(map[string]*functionDecl),
			documentation:   &documentation,
		}
	}
	for _, declaration := range standardLibraryRegistry.interfaces {
		methods := make(map[string]*methodSignature, len(declaration.methods))
		for _, method := range declaration.methods {
			methods[method.name] = standardMethodSignature(declaration.namespace, method)
		}
		documentation := declaration.documentation
		p.interfaces[declaration.canonical] = &interfaceDecl{
			name:          declaration.name,
			qualified:     declaration.canonical,
			namespace:     declaration.namespace,
			methods:       methods,
			documentation: &documentation,
		}
	}
}

func standardMethodSignature(namespace string, declaration standardMethodDecl) *methodSignature {
	documentation := declaration.documentation
	return &methodSignature{
		name:          declaration.name,
		namespace:     namespace,
		aliases:       make(map[string]aliasDecl),
		params:        declaration.params,
		result:        declaration.result,
		throws:        declaration.throws,
		documentation: &documentation,
	}
}
func (p *program) undocumentedStandardLibrarySymbols() []string {
	undocumented := make(map[string]struct{})
	require := func(name string, documentation *string) {
		if documentation == nil || strings.TrimSpace(*documentation) == "" {
			undocumented[name] = struct{}{}
		}
	}
	requireNamespaces := func(name string) {
		parts := strings.Split(name, ".")
		for length := 1; length < len(parts); length++ {
			namespace := strings.Join(parts[:length], ".")
			if strings.HasPrefix(namespace, "std") {
				require(namespace, p.namespaceDocumentation[namespace])
			}
		}
	}
	for name, function := range p.functions {
		if !strings.HasPrefix(name, "std.") || !isPublic(function.name) {
			continue
		}
		requireNamespaces(name)
		require(name, function.documentation)
	}
	for name, class := range p.classes {
		if !strings.HasPrefix(name, "std.") || !isPublic(class.name) {
			continue
		}
		requireNamespaces(name)
		require(name, class.documentation)
		for fieldName, field := range class.fields {
			if isPublic(fieldName) {
				require(name+"."+fieldName, field.documentation)
			}
		}
		for methodName, method := range class.methods {
			if isPublic(methodName) {
				require(name+"."+methodName, method.documentation)
			}
		}
	}
	for name, iface := range p.interfaces {
		if !strings.HasPrefix(name, "std.") || !isPublic(iface.name) {
			continue
		}
		requireNamespaces(name)
		require(name, iface.documentation)
		for methodName, method := range iface.methods {
			if isPublic(methodName) {
				require(name+"."+methodName, method.documentation)
			}
		}
	}
	return sortedKeys(undocumented)
}

// isAbsoluteCanonicalName is the single namespace boundary shared by user
// project declarations and compiler-owned standard-library declarations.
func isAbsoluteCanonicalName(name string) bool {
	return strings.HasPrefix(name, "root.") || strings.HasPrefix(name, "std.")
}

func (p *program) callNativeFunction(function *functionDecl, frame *runtimeFrame, typeArgs []string) (runtimeValue, error) {
	if value, err, ok := p.callNativeStdIO(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdHTTP(function, frame); ok {
		return value, err
	}
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdBufferNew:
		elementType := typeArgs[0]
		return runtimeValue{
			typ:    bufferType(elementType),
			buffer: &runtimeBuffer{elementType: elementType},
		}, nil
	case nativeStdBufferPush:
		buffer := frame.locals["Buffer"].buffer
		buffer.values = append(buffer.values, frame.locals["Value"])
		return nullRuntimeValue(), nil
	case nativeStdBufferGet:
		buffer := frame.locals["Buffer"].buffer
		index := frame.locals["Index"].scalar.(int64)
		return runtimeIndexedValue(buffer.elementType, buffer.values, index), nil
	case nativeStdBufferSet:
		buffer := frame.locals["Buffer"].buffer
		index := frame.locals["Index"].scalar.(int64)
		if index < 0 || index >= int64(len(buffer.values)) {
			return runtimeResultValue(resultType, false, runtimeValue{typ: stdCollectionsBoundsFailureName}), nil
		}
		buffer.values[index] = frame.locals["Value"]
		return runtimeResultValue(resultType, true, nullRuntimeValue()), nil
	case nativeStdBufferLength:
		buffer := frame.locals["Buffer"].buffer
		return runtimeValue{typ: "int", scalar: int64(len(buffer.values))}, nil
	case nativeStdBufferFreeze:
		buffer := frame.locals["Buffer"].buffer
		values := append([]runtimeValue(nil), buffer.values...)
		return runtimeValue{typ: buffer.elementType + "[]", elements: values}, nil
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
	case nativeStdBytesSlice:
		value := frame.locals["Value"].scalar.([]byte)
		start := frame.locals["Start"].scalar.(int64)
		end := frame.locals["End"].scalar.(int64)
		if start < 0 || end < start || end > int64(len(value)) {
			failure := runtimeValue{
				typ: stdBytesBoundsFailureName,
				fields: map[string]runtimeValue{
					"Start":   {typ: "int", scalar: start},
					"End":     {typ: "int", scalar: end},
					"Length":  {typ: "int", scalar: int64(len(value))},
					"Message": {typ: "string", scalar: "slice bounds out of range"},
				},
			}
			return runtimeResultValue(resultType, false, failure), nil
		}
		sliced := make([]byte, end-start)
		copy(sliced, value[start:end])
		return runtimeResultValue(resultType, true, runtimeValue{typ: "bytes", scalar: sliced}), nil
	case nativeStdBytesFromValues:
		values := frame.locals["Values"].elements
		for index, value := range values {
			number := value.scalar.(int64)
			if number < 0 || number > 255 {
				failure := runtimeValue{
					typ: stdBytesValueFailureName,
					fields: map[string]runtimeValue{
						"Index":   {typ: "int", scalar: int64(index)},
						"Value":   {typ: "int", scalar: number},
						"Message": {typ: "string", scalar: "byte value must be between 0 and 255"},
					},
				}
				return runtimeResultValue(resultType, false, failure), nil
			}
		}
		bytes := make([]byte, len(values))
		for index, value := range values {
			bytes[index] = byte(value.scalar.(int64))
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "bytes", scalar: bytes}), nil
	case nativeStdUTF8DecodeAt:
		value := frame.locals["Value"].scalar.([]byte)
		index := frame.locals["Index"].scalar.(int64)
		if index < 0 || index >= int64(len(value)) {
			failure := runtimeValue{
				typ: stdUTF8FailureName,
				fields: map[string]runtimeValue{
					"Index":   {typ: "int", scalar: index},
					"Message": {typ: "string", scalar: "byte index out of range"},
				},
			}
			return runtimeResultValue(resultType, false, failure), nil
		}
		decoded, width := utf8.DecodeRune(value[index:])
		if decoded == utf8.RuneError && width == 1 {
			failure := runtimeValue{
				typ: stdUTF8FailureName,
				fields: map[string]runtimeValue{
					"Index":   {typ: "int", scalar: index},
					"Message": {typ: "string", scalar: "invalid UTF-8 encoding"},
				},
			}
			return runtimeResultValue(resultType, false, failure), nil
		}
		payload := runtimeValue{
			typ: stdUTF8DecodedRuneName,
			fields: map[string]runtimeValue{
				"Value": {typ: "int", scalar: int64(decoded)},
				"Width": {typ: "int", scalar: int64(width)},
			},
		}
		return runtimeResultValue(resultType, true, payload), nil
	case nativeStdUnicodeIsLetter:
		value := frame.locals["Value"].scalar.(int64)
		return runtimeValue{typ: "bool", scalar: isUnicodeScalar(value) && unicode.IsLetter(rune(value))}, nil
	case nativeStdUnicodeIsDigit:
		value := frame.locals["Value"].scalar.(int64)
		return runtimeValue{typ: "bool", scalar: isUnicodeScalar(value) && unicode.IsDigit(rune(value))}, nil
	case nativeStdUnicodeIsWhitespace:
		value := frame.locals["Value"].scalar.(int64)
		return runtimeValue{typ: "bool", scalar: isUnicodeScalar(value) && unicode.IsSpace(rune(value))}, nil
	case nativeStdUnicodeIsUpper:
		value := frame.locals["Value"].scalar.(int64)
		return runtimeValue{typ: "bool", scalar: isUnicodeScalar(value) && unicode.IsUpper(rune(value))}, nil
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
	case nativeStdMathDivide:
		return runtimeMathDivide(
			resultType,
			frame.locals["Dividend"].scalar.(int64),
			frame.locals["Divisor"].scalar.(int64),
		), nil
	case nativeStdMathRemainder:
		return runtimeMathRemainder(
			resultType,
			frame.locals["Dividend"].scalar.(int64),
			frame.locals["Divisor"].scalar.(int64),
		), nil
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
	case nativeStdTextQuote:
		return runtimeValue{typ: "string", scalar: strconv.Quote(frame.locals["Text"].scalar.(string))}, nil
	default:
		return runtimeValue{}, fmt.Errorf("unknown native Slick function %s", function.native)
	}
}

func isUnicodeScalar(value int64) bool {
	return value >= 0 && value <= utf8.MaxRune && (value < 0xd800 || value > 0xdfff)
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

func runtimeMathArithmeticFailure(resultType, operation, kind, message string) runtimeValue {
	failure := runtimeValue{
		typ: stdMathArithmeticFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Kind":      {typ: "string", scalar: kind},
			"Message":   {typ: "string", scalar: message},
		},
	}
	return runtimeResultValue(resultType, false, failure)
}

// runtimeMathDivide implements truncation-toward-zero integer division with
// explicit checks so host integer division never panics on zero or min/-1.
func runtimeMathDivide(resultType string, dividend, divisor int64) runtimeValue {
	if divisor == 0 {
		return runtimeMathArithmeticFailure(resultType, "Divide", "DivisionByZero", "division by zero")
	}
	if dividend == math.MinInt64 && divisor == -1 {
		return runtimeMathArithmeticFailure(resultType, "Divide", "Overflow", "integer division overflow")
	}
	return runtimeResultValue(resultType, true, runtimeValue{typ: "int", scalar: dividend / divisor})
}

// runtimeMathRemainder implements remainder with the dividend's sign. The
// representable minimum-int % -1 case returns 0 without host evaluation.
func runtimeMathRemainder(resultType string, dividend, divisor int64) runtimeValue {
	if divisor == 0 {
		return runtimeMathArithmeticFailure(resultType, "Remainder", "DivisionByZero", "division by zero")
	}
	if dividend == math.MinInt64 && divisor == -1 {
		return runtimeResultValue(resultType, true, runtimeValue{typ: "int", scalar: int64(0)})
	}
	return runtimeResultValue(resultType, true, runtimeValue{typ: "int", scalar: dividend % divisor})
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
	if function.native == nativeStdJsonDecode || function.native == nativeStdJsonEncode || isNativeStdBuffer(function.native) {
		// Generic operations are emitted per call site type argument.
		return nil
	}
	resultType, err := g.declaredType(function.namespace, function.aliases, function.result)
	if err != nil {
		return err
	}
	arguments := make([]string, 0, len(function.params))
	parameters := make([]string, 0, len(function.params)+1)
	if g.program.usesAsync {
		parameters = append(parameters, "slickContext context.Context")
	}
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
	if g.program.usesAsync {
		g.line("if err := slickCheckCancellation(slickContext); err != nil { return %s, err }", g.zero(resultType))
	}
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
	case nativeStdBytesSlice:
		result := g.goType(resultType)
		failure := goClassName(stdBytesBoundsFailureName)
		g.line("if %s < 0 || %s < %s || %s > int64(len(%s)) {", arguments[1], arguments[2], arguments[1], arguments[2], arguments[0])
		g.line("return %s{failure: &%s{%s: %s, %s: %s, %s: int64(len(%s)), %s: %q}}, nil",
			result, failure,
			goFieldName("Start"), arguments[1],
			goFieldName("End"), arguments[2],
			goFieldName("Length"), arguments[0],
			goFieldName("Message"), "slice bounds out of range",
		)
		g.line("}")
		g.line("value := make(slickBytes, %s-%s)", arguments[2], arguments[1])
		g.line("copy(value, %s[%s:%s])", arguments[0], arguments[1], arguments[2])
		g.line("return %s{ok: true, value: value}, nil", result)
	case nativeStdBytesFromValues:
		result := g.goType(resultType)
		failure := goClassName(stdBytesValueFailureName)
		g.line("for index, value := range %s {", arguments[0])
		g.line("if value < 0 || value > 255 {")
		g.line("return %s{failure: &%s{%s: int64(index), %s: value, %s: %q}}, nil",
			result, failure,
			goFieldName("Index"),
			goFieldName("Value"),
			goFieldName("Message"), "byte value must be between 0 and 255",
		)
		g.line("}")
		g.line("}")
		g.line("value := make(slickBytes, len(%s))", arguments[0])
		g.line("for index, number := range %s { value[index] = byte(number) }", arguments[0])
		g.line("return %s{ok: true, value: value}, nil", result)
	case nativeStdUTF8DecodeAt:
		result := g.goType(resultType)
		failure := goClassName(stdUTF8FailureName)
		decodedRune := goClassName(stdUTF8DecodedRuneName)
		g.line("if %s < 0 || %s >= int64(len(%s)) {", arguments[1], arguments[1], arguments[0])
		g.line("return %s{failure: &%s{%s: %s, %s: %q}}, nil",
			result, failure,
			goFieldName("Index"), arguments[1],
			goFieldName("Message"), "byte index out of range",
		)
		g.line("}")
		g.line("value, width := utf8.DecodeRune(%s[%s:])", arguments[0], arguments[1])
		g.line("if value == utf8.RuneError && width == 1 {")
		g.line("return %s{failure: &%s{%s: %s, %s: %q}}, nil",
			result, failure,
			goFieldName("Index"), arguments[1],
			goFieldName("Message"), "invalid UTF-8 encoding",
		)
		g.line("}")
		g.line("return %s{ok: true, value: %s{%s: int64(value), %s: int64(width)}}, nil",
			result, decodedRune, goFieldName("Value"), goFieldName("Width"),
		)
	case nativeStdUnicodeIsLetter:
		g.line("return %s >= 0 && %s <= utf8.MaxRune && (%s < 0xd800 || %s > 0xdfff) && unicode.IsLetter(rune(%s)), nil",
			arguments[0], arguments[0], arguments[0], arguments[0], arguments[0])
	case nativeStdUnicodeIsDigit:
		g.line("return %s >= 0 && %s <= utf8.MaxRune && (%s < 0xd800 || %s > 0xdfff) && unicode.IsDigit(rune(%s)), nil",
			arguments[0], arguments[0], arguments[0], arguments[0], arguments[0])
	case nativeStdUnicodeIsWhitespace:
		g.line("return %s >= 0 && %s <= utf8.MaxRune && (%s < 0xd800 || %s > 0xdfff) && unicode.IsSpace(rune(%s)), nil",
			arguments[0], arguments[0], arguments[0], arguments[0], arguments[0])
	case nativeStdUnicodeIsUpper:
		g.line("return %s >= 0 && %s <= utf8.MaxRune && (%s < 0xd800 || %s > 0xdfff) && unicode.IsUpper(rune(%s)), nil",
			arguments[0], arguments[0], arguments[0], arguments[0], arguments[0])
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
	case nativeStdMathDivide:
		result := g.goType(resultType)
		failure := goClassName(stdMathArithmeticFailureName)
		g.line("if %s == 0 {", arguments[1])
		g.line("return %s{failure: &%s{%s: %q, %s: %q, %s: %q}}, nil",
			result, failure,
			goFieldName("Operation"), "Divide",
			goFieldName("Kind"), "DivisionByZero",
			goFieldName("Message"), "division by zero",
		)
		g.line("}")
		g.line("if %s == math.MinInt64 && %s == -1 {", arguments[0], arguments[1])
		g.line("return %s{failure: &%s{%s: %q, %s: %q, %s: %q}}, nil",
			result, failure,
			goFieldName("Operation"), "Divide",
			goFieldName("Kind"), "Overflow",
			goFieldName("Message"), "integer division overflow",
		)
		g.line("}")
		g.line("return %s{ok: true, value: %s / %s}, nil", result, arguments[0], arguments[1])
	case nativeStdMathRemainder:
		result := g.goType(resultType)
		failure := goClassName(stdMathArithmeticFailureName)
		g.line("if %s == 0 {", arguments[1])
		g.line("return %s{failure: &%s{%s: %q, %s: %q, %s: %q}}, nil",
			result, failure,
			goFieldName("Operation"), "Remainder",
			goFieldName("Kind"), "DivisionByZero",
			goFieldName("Message"), "division by zero",
		)
		g.line("}")
		// Avoid host remainder of MinInt64 % -1, which can panic on platforms
		// that implement rem via the same trapping integer-divide instruction.
		g.line("if %s == math.MinInt64 && %s == -1 {", arguments[0], arguments[1])
		g.line("return %s{ok: true, value: int64(0)}, nil", result)
		g.line("}")
		g.line("return %s{ok: true, value: %s %% %s}, nil", result, arguments[0], arguments[1])
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
	case nativeStdTextQuote:
		g.line("return strconv.Quote(%s), nil", arguments[0])
	case nativeStdIOReaderFromBytes:
		g.line("return %s{slickResource: slickIONewReader(%s)}, nil", goClassName(stdIOBytesReaderName), arguments[0])
	case nativeStdIOWriterToBytes:
		g.line("return %s{slickResource: slickIONewWriter()}, nil", goClassName(stdIOBytesWriterName))
	case nativeStdIOReadAll:
		if g.program.usesAsync {
			g.line("return slickIOReadAll(slickContext, %s, %s)", arguments[0], arguments[1])
		} else {
			g.line("return slickIOReadAll(%s, %s)", arguments[0], arguments[1])
		}
	case nativeStdIOCopy:
		if g.program.usesAsync {
			g.line("return slickIOCopy(slickContext, %s, %s, %s)", arguments[0], arguments[1], arguments[2])
		} else {
			g.line("return slickIOCopy(%s, %s, %s)", arguments[0], arguments[1], arguments[2])
		}
	case nativeStdHTTPFetch:
		if g.program.usesAsync {
			g.line("return slickHTTPFetch(slickContext, %s)", arguments[0])
		} else {
			g.line("return slickHTTPFetch(%s)", arguments[0])
		}
	case nativeStdHTTPHeaderValues:
		g.line("return slickHTTPHeaderValues(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdHTTPStatusText:
		g.line("return slickHTTPStatusText(%s), nil", arguments[0])
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
