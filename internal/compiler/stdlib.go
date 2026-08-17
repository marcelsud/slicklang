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

const (
	nativeStdBytesFromUtf8        runtimeOperationID = "std.bytes.FromUtf8"
	nativeStdBytesToUtf8          runtimeOperationID = "std.bytes.ToUtf8"
	nativeStdBytesLength          runtimeOperationID = "std.bytes.Length"
	nativeStdBytesAt              runtimeOperationID = "std.bytes.At"
	nativeStdBytesConcat          runtimeOperationID = "std.bytes.Concat"
	nativeStdBufferNew            runtimeOperationID = "std.buffer.New"
	nativeStdBufferPush           runtimeOperationID = "std.buffer.Push"
	nativeStdBufferGet            runtimeOperationID = "std.buffer.Get"
	nativeStdBufferSet            runtimeOperationID = "std.buffer.Set"
	nativeStdBufferLength         runtimeOperationID = "std.buffer.Length"
	nativeStdBufferFreeze         runtimeOperationID = "std.buffer.Freeze"
	nativeStdBytesSlice           runtimeOperationID = "std.bytes.Slice"
	nativeStdBytesFromValues      runtimeOperationID = "std.bytes.FromValues"
	nativeStdUTF8DecodeAt         runtimeOperationID = "std.utf8.DecodeAt"
	nativeStdUnicodeIsLetter      runtimeOperationID = "std.unicode.IsLetter"
	nativeStdUnicodeIsDigit       runtimeOperationID = "std.unicode.IsDigit"
	nativeStdUnicodeIsWhitespace  runtimeOperationID = "std.unicode.IsWhitespace"
	nativeStdUnicodeIsUpper       runtimeOperationID = "std.unicode.IsUpper"
	nativeStdConvertParseInt      runtimeOperationID = "std.convert.ParseInt"
	nativeStdConvertParseFloat    runtimeOperationID = "std.convert.ParseFloat"
	nativeStdConvertIntToString   runtimeOperationID = "std.convert.IntToString"
	nativeStdConvertFloatToString runtimeOperationID = "std.convert.FloatToString"
	nativeStdMathDivide           runtimeOperationID = "std.math.Divide"
	nativeStdMathRemainder        runtimeOperationID = "std.math.Remainder"
	nativeStdEnvGet               runtimeOperationID = "std.env.Get"
	nativeStdEnvSet               runtimeOperationID = "std.env.Set"
	nativeStdEnvUnset             runtimeOperationID = "std.env.Unset"
	nativeStdFSReadText           runtimeOperationID = "std.fs.ReadText"
	nativeStdFSWriteText          runtimeOperationID = "std.fs.WriteText"
	nativeStdFSExists             runtimeOperationID = "std.fs.Exists"
	nativeStdFSCreateDirectoryAll runtimeOperationID = "std.fs.CreateDirectoryAll"
	nativeStdFSRemove             runtimeOperationID = "std.fs.Remove"
	nativeStdFSReadDirectory      runtimeOperationID = "std.fs.ReadDirectory"

	nativeStdFSCreateTemporaryDirectory runtimeOperationID = "std.fs.CreateTemporaryDirectory"
	nativeStdFSTemporaryDirectoryClose  runtimeOperationID = "std.fs.TemporaryDirectory.Close"

	nativeStdJsonDecode        runtimeOperationID = "std.json.Decode"
	nativeStdJsonEncode        runtimeOperationID = "std.json.Encode"
	nativeStdPathJoin          runtimeOperationID = "std.path.Join"
	nativeStdPathClean         runtimeOperationID = "std.path.Clean"
	nativeStdPathBase          runtimeOperationID = "std.path.Base"
	nativeStdPathDirectory     runtimeOperationID = "std.path.Directory"
	nativeStdPathExtension     runtimeOperationID = "std.path.Extension"
	nativeStdPathIsAbsolute    runtimeOperationID = "std.path.IsAbsolute"
	nativeStdTextTrim          runtimeOperationID = "std.text.Trim"
	nativeStdTextContains      runtimeOperationID = "std.text.Contains"
	nativeStdTextStartsWith    runtimeOperationID = "std.text.StartsWith"
	nativeStdTextEndsWith      runtimeOperationID = "std.text.EndsWith"
	nativeStdTextSplit         runtimeOperationID = "std.text.Split"
	nativeStdTextJoin          runtimeOperationID = "std.text.Join"
	nativeStdTextReplaceAll    runtimeOperationID = "std.text.ReplaceAll"
	nativeStdTextCut           runtimeOperationID = "std.text.Cut"
	nativeStdTextQuote         runtimeOperationID = "std.text.Quote"
	nativeStdIOReaderFromBytes runtimeOperationID = "std.io.ReaderFromBytes"
	nativeStdIOWriterToBytes   runtimeOperationID = "std.io.WriterToBytes"
	nativeStdIOReadAll         runtimeOperationID = "std.io.ReadAll"
	nativeStdIOCopy            runtimeOperationID = "std.io.Copy"
	nativeStdIOReaderRead      runtimeOperationID = "std.io.bytesReader.Read"
	nativeStdIOReaderClose     runtimeOperationID = "std.io.bytesReader.Close"
	nativeStdIOWriterWrite     runtimeOperationID = "std.io.BytesWriter.Write"
	nativeStdIOWriterBytes     runtimeOperationID = "std.io.BytesWriter.Bytes"
	nativeStdIOWriterClose     runtimeOperationID = "std.io.BytesWriter.Close"
	nativeStdHTTPServerServe   runtimeOperationID = "std.http.server.Serve"

	stdBytesUtf8FailureName         = "std.bytes.Utf8Failure"
	stdCollectionsBoundsFailureName = "std.collections.BoundsFailure"
	stdConvertFailureName           = "std.convert.Failure"
	stdMathArithmeticFailureName    = "std.math.ArithmeticFailure"
	stdEnvFailureName               = "std.env.Failure"
	stdFSFailureName                = "std.fs.Failure"
	stdFSEntryName                  = "std.fs.Entry"
	stdFSTemporaryDirectoryName     = "std.fs.TemporaryDirectory"
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
	stdHTTPServerHandlerName        = "std.http.server.Handler"
	stdHTTPServerConfigName         = "std.http.server.Config"
	stdHTTPServerRequestName        = "std.http.server.Request"
	stdHTTPServerResponseName       = "std.http.server.Response"
	stdHTTPServerFailureName        = "std.http.server.Failure"
)

func isNativeStdBuffer(operation runtimeOperationID) bool {
	declaration, ok := runtimeOperationRegistry[operation]
	return ok && declaration.family == runtimeFamilyBuffer
}

type standardNamespaceDecl struct {
	stability     Stability
	canonical     string
	documentation string
}

type standardFunctionDecl struct {
	stability     Stability
	canonical     string
	namespace     string
	name          string
	documentation string
	typeParams    []string
	params        []paramDecl
	result        typeRef
	effects       []string
	native        runtimeOperationID
}

type standardMethodDecl struct {
	stability     Stability
	name          string
	documentation string
	params        []paramDecl
	result        typeRef
	throws        []typeRef
	effects       []string
	native        runtimeOperationID
}

type standardFieldDecl struct {
	stability     Stability
	name          string
	typ           typeRef
	documentation string
}

type standardClassDecl struct {
	stability      Stability
	canonical      string
	namespace      string
	name           string
	documentation  string
	isError        bool
	nativeResource string
	fields         []standardFieldDecl
	methods        []standardMethodDecl
}

type standardInterfaceDecl struct {
	stability     Stability
	canonical     string
	namespace     string
	name          string
	documentation string
	methods       []standardMethodDecl
}

// standardLibraryRegistry is the authoritative public Slick surface. The
// compiler, interpreter, and every backend consume declarations from this
// table; runtime providers are private implementation details.
var standardLibraryRegistry = standardLibraryRegistryDecl{
	namespaces: []standardNamespaceDecl{
		{stability: StabilityStable, canonical: "std", documentation: "Provides compiler-owned portable components whose blocking calls inherit the active handler or task cancellation scope and return their module's typed Failure."},
		{stability: StabilityStable, canonical: "std.bytes", documentation: "Converts and inspects immutable binary byte values."},
		{stability: StabilityStable, canonical: "std.buffer", documentation: "Builds mutable sequences that freeze into immutable array snapshots."},
		{stability: StabilityStable, canonical: "std.collections", documentation: "Defines failures shared by compiler-owned collection operations."},
		{stability: StabilityStable, canonical: "std.convert", documentation: "Converts primitive values with explicit parse failures."},
		{stability: StabilityStable, canonical: "std.math", documentation: "Provides checked integer arithmetic that returns typed Result failures."},
		{stability: StabilityStable, canonical: "std.env", documentation: "Reads and updates the process environment without exposing values in failures."},
		{stability: StabilityStable, canonical: "std.fs", documentation: "Performs cancellation-aware bounded whole-file and directory operations on platform paths; whole-file calls accept regular files and named pipes."},
		{stability: StabilityStable, canonical: "std.json", documentation: "Encodes and decodes supported Slick values as JSON."},
		{stability: StabilityStable, canonical: "std.http", documentation: "Performs cancellation-aware synchronous fully buffered HTTP requests with typed failures."},
		{stability: StabilityStable, canonical: "std.http.server", documentation: "Serves bounded inbound HTTP requests through one typed Handler with graceful shutdown."},
		{stability: StabilityStable, canonical: "std.path", documentation: "Manipulates platform-dependent filesystem path strings without accessing the filesystem."},
		{stability: StabilityStable, canonical: "std.process", documentation: "Runs cancellation-aware child programs directly without a shell and describes command-line results."},
		{stability: StabilityStable, canonical: "std.text", documentation: "Provides deterministic Unicode-aware and substring text operations."},
		{stability: StabilityStable, canonical: "std.io", documentation: "Provides bounded resource-safe byte readers, writers, and transfer helpers."},
		{stability: StabilityStable, canonical: "std.utf8", documentation: "Decodes Unicode scalar values from immutable UTF-8 bytes."},
		{stability: StabilityStable, canonical: "std.unicode", documentation: "Classifies Unicode scalar values using the toolchain's pinned tables."},
		{stability: StabilityStable, canonical: "std.sqlite", documentation: "Provides cancellation-aware resource-safe access to persistent and in-memory SQLite databases."},
	},
	functions: []standardFunctionDecl{
		{stability: StabilityStable, canonical: string(nativeStdBytesFromUtf8),
			namespace:     "std.bytes",
			name:          "FromUtf8",
			documentation: "Encodes Text as immutable UTF-8 bytes.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "bytes"},
			native:        nativeStdBytesFromUtf8,
		},
		{stability: StabilityStable, canonical: string(nativeStdBytesToUtf8),
			namespace:     "std.bytes",
			name:          "ToUtf8",
			documentation: "Decodes Value as UTF-8 or returns Utf8Failure for invalid data.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: "Result<string,std.bytes.Utf8Failure>"},
			native:        nativeStdBytesToUtf8,
		},
		{stability: StabilityStable, canonical: string(nativeStdBytesLength),
			namespace:     "std.bytes",
			name:          "Length",
			documentation: "Returns the number of bytes in Value.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: "int"},
			native:        nativeStdBytesLength,
		},
		{stability: StabilityStable, canonical: string(nativeStdBytesAt),
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
		{stability: StabilityStable, canonical: string(nativeStdBytesConcat),
			namespace:     "std.bytes",
			name:          "Concat",
			documentation: "Concatenates Values in order into a new immutable byte value.",
			params:        []paramDecl{{name: "Values", typ: typeRef{name: "bytes[]"}}},
			result:        typeRef{name: "bytes"},
			native:        nativeStdBytesConcat,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferNew),
			namespace:     "std.buffer",
			name:          "New",
			documentation: "Creates an empty growable buffer for T values.",
			typeParams:    []string{"T"},
			result:        typeRef{name: "Buffer<T>"},
			native:        nativeStdBufferNew,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferPush),
			namespace:     "std.buffer",
			name:          "Push",
			documentation: "Appends Value to Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Value", typ: typeRef{name: "T"}},
			},
			result:  typeRef{name: "null"},
			effects: []string{effectState},
			native:  nativeStdBufferPush,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferGet),
			namespace:     "std.buffer",
			name:          "Get",
			documentation: "Returns the value at Index, or null when Index is outside Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Index", typ: typeRef{name: "int"}},
			},
			result:  typeRef{name: "T?"},
			effects: []string{effectState},
			native:  nativeStdBufferGet,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferSet),
			namespace:     "std.buffer",
			name:          "Set",
			documentation: "Replaces the value at Index or returns BoundsFailure without growing Buffer.",
			typeParams:    []string{"T"},
			params: []paramDecl{
				{name: "Buffer", typ: typeRef{name: "Buffer<T>"}},
				{name: "Index", typ: typeRef{name: "int"}},
				{name: "Value", typ: typeRef{name: "T"}},
			},
			result:  typeRef{name: "Result<null," + stdCollectionsBoundsFailureName + ">"},
			effects: []string{effectState},
			native:  nativeStdBufferSet,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferLength),
			namespace:     "std.buffer",
			name:          "Length",
			documentation: "Returns the number of values in Buffer.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Buffer", typ: typeRef{name: "Buffer<T>"}}},
			result:        typeRef{name: "int"},
			effects:       []string{effectState},
			native:        nativeStdBufferLength,
		},
		{stability: StabilityStable, canonical: string(nativeStdBufferFreeze),
			namespace:     "std.buffer",
			name:          "Freeze",
			documentation: "Copies Buffer into an immutable array snapshot.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Buffer", typ: typeRef{name: "Buffer<T>"}}},
			result:        typeRef{name: "T[]"},
			effects:       []string{effectState},
			native:        nativeStdBufferFreeze,
		},
		{stability: StabilityStable, canonical: string(nativeStdBytesSlice),
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
		{stability: StabilityStable, canonical: string(nativeStdBytesFromValues),
			namespace:     "std.bytes",
			name:          "FromValues",
			documentation: "Constructs immutable bytes from integer values or reports the first value outside 0 through 255.",
			params:        []paramDecl{{name: "Values", typ: typeRef{name: "int[]"}}},
			result:        typeRef{name: "Result<bytes," + stdBytesValueFailureName + ">"},
			native:        nativeStdBytesFromValues,
		},
		{stability: StabilityStable, canonical: string(nativeStdUTF8DecodeAt),
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
		{stability: StabilityStable, canonical: string(nativeStdUnicodeIsLetter),
			namespace:     "std.unicode",
			name:          "IsLetter",
			documentation: "Reports whether Value is a Unicode scalar classified as a letter.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsLetter,
		},
		{stability: StabilityStable, canonical: string(nativeStdUnicodeIsDigit),
			namespace:     "std.unicode",
			name:          "IsDigit",
			documentation: "Reports whether Value is a Unicode decimal digit.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsDigit,
		},
		{stability: StabilityStable, canonical: string(nativeStdUnicodeIsWhitespace),
			namespace:     "std.unicode",
			name:          "IsWhitespace",
			documentation: "Reports whether Value is a Unicode white-space scalar.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsWhitespace,
		},
		{stability: StabilityStable, canonical: string(nativeStdUnicodeIsUpper),
			namespace:     "std.unicode",
			name:          "IsUpper",
			documentation: "Reports whether Value is an uppercase Unicode scalar.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdUnicodeIsUpper,
		},
		{stability: StabilityStable, canonical: string(nativeStdConvertParseInt),
			namespace:     "std.convert",
			name:          "ParseInt",
			documentation: "Parses a base-10 integer or returns Failure when Text is invalid or out of range.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<int,std.convert.Failure>"},
			native:        nativeStdConvertParseInt,
		},
		{stability: StabilityStable, canonical: string(nativeStdConvertParseFloat),
			namespace:     "std.convert",
			name:          "ParseFloat",
			documentation: "Parses a finite floating-point value or returns Failure when Text is invalid or out of range.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<float,std.convert.Failure>"},
			native:        nativeStdConvertParseFloat,
		},
		{stability: StabilityStable, canonical: string(nativeStdConvertIntToString),
			namespace:     "std.convert",
			name:          "IntToString",
			documentation: "Formats Value as a base-10 integer string.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdConvertIntToString,
		},
		{stability: StabilityStable, canonical: string(nativeStdConvertFloatToString),
			namespace:     "std.convert",
			name:          "FloatToString",
			documentation: "Formats Value as a deterministic floating-point string.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "float"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdConvertFloatToString,
		},
		{stability: StabilityStable, canonical: string(nativeStdMathDivide),
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
		{stability: StabilityStable, canonical: string(nativeStdMathRemainder),
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
		{stability: StabilityStable, canonical: string(nativeStdProcessRun),
			namespace:     "std.process",
			name:          "Run",
			documentation: "Runs Program directly with Arguments, never through a shell, captures at most MaxOutputBytes of combined output, and signals and reaps the child on cancellation.",
			params: []paramDecl{
				{name: "Program", typ: typeRef{name: "string"}},
				{name: "Arguments", typ: typeRef{name: "string[]"}},
				{name: "WorkingDirectory", typ: typeRef{name: "string?"}},
				{name: "MaxOutputBytes", typ: typeRef{name: "int"}},
			},
			result:  typeRef{name: "Result<std.process.Completed,std.process.Failure>"},
			effects: []string{effectProcess},
			native:  nativeStdProcessRun,
		},
		{stability: StabilityStable, canonical: string(nativeStdEnvGet),
			namespace:     "std.env",
			name:          "Get",
			documentation: "Returns the environment value for Name, or null when Name is unset.",
			params:        []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string?"},
			effects:       []string{effectEnvironment},
			native:        nativeStdEnvGet,
		},
		{stability: StabilityStable, canonical: string(nativeStdEnvSet),
			namespace:     "std.env",
			name:          "Set",
			documentation: "Sets Name to Value or returns Failure without including Value in the error.",
			params: []paramDecl{
				{name: "Name", typ: typeRef{name: "string"}},
				{name: "Value", typ: typeRef{name: "string"}},
			},
			result:  typeRef{name: "Result<null,std.env.Failure>"},
			effects: []string{effectEnvironment},
			native:  nativeStdEnvSet,
		},
		{stability: StabilityStable, canonical: string(nativeStdEnvUnset),
			namespace:     "std.env",
			name:          "Unset",
			documentation: "Removes Name from the environment or returns Failure.",
			params:        []paramDecl{{name: "Name", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.env.Failure>"},
			effects:       []string{effectEnvironment},
			native:        nativeStdEnvUnset,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSReadText),
			namespace:     "std.fs",
			name:          "ReadText",
			documentation: "Reads Path completely as UTF-8 text, returns typed cancellation or I/O Failure, and rejects non-regular inputs other than named pipes.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<string,std.fs.Failure>"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSReadText,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSWriteText),
			namespace:     "std.fs",
			name:          "WriteText",
			documentation: "Writes Contents to Path or returns typed cancellation or I/O Failure; cancellation may leave Path truncated or partially written, and non-regular inputs other than named pipes are rejected.",
			params: []paramDecl{
				{name: "Path", typ: typeRef{name: "string"}},
				{name: "Contents", typ: typeRef{name: "string"}},
			},
			result:  typeRef{name: "Result<null,std.fs.Failure>"},
			effects: []string{effectFilesystem},
			native:  nativeStdFSWriteText,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSExists),
			namespace:     "std.fs",
			name:          "Exists",
			documentation: "Reports whether Path exists or returns Failure when its status cannot be read.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<bool,std.fs.Failure>"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSExists,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSCreateDirectoryAll),
			namespace:     "std.fs",
			name:          "CreateDirectoryAll",
			documentation: "Creates Path and every missing parent directory or returns Failure.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.fs.Failure>"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSCreateDirectoryAll,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSRemove),
			namespace:     "std.fs",
			name:          "Remove",
			documentation: "Removes the file or empty directory at Path or returns Failure.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<null,std.fs.Failure>"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSRemove,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSReadDirectory),
			namespace:     "std.fs",
			name:          "ReadDirectory",
			documentation: "Lists the direct children of Path sorted by Name or returns Failure.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<" + stdFSEntryName + "[]," + stdFSFailureName + ">"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSReadDirectory,
		},
		{stability: StabilityStable, canonical: string(nativeStdFSCreateTemporaryDirectory),
			namespace:     "std.fs",
			name:          "CreateTemporaryDirectory",
			documentation: "Creates a unique directory under the platform temporary root named after Prefix.",
			params:        []paramDecl{{name: "Prefix", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<" + stdFSTemporaryDirectoryName + "," + stdFSFailureName + ">"},
			effects:       []string{effectFilesystem},
			native:        nativeStdFSCreateTemporaryDirectory,
		},
		{stability: StabilityStable, canonical: string(nativeStdJsonDecode),
			namespace:     "std.json",
			name:          "Decode",
			documentation: "Decodes JSON Text into T or returns Failure with a structural path.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<T,std.json.Failure>"},
			native:        nativeStdJsonDecode,
		},
		{stability: StabilityStable, canonical: string(nativeStdJsonEncode),
			namespace:     "std.json",
			name:          "Encode",
			documentation: "Encodes Value as JSON or returns Failure for unsupported data.",
			typeParams:    []string{"T"},
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "T"}}},
			result:        typeRef{name: "Result<string,std.json.Failure>"},
			native:        nativeStdJsonEncode,
		},
		{stability: StabilityStable, canonical: string(nativeStdHTTPFetch),
			namespace:     "std.http",
			name:          "Fetch",
			documentation: "Executes Request and returns a fully buffered Response or typed Failure with Kind Cancelled when its inherited scope is cancelled.",
			params:        []paramDecl{{name: "Request", typ: typeRef{name: stdHTTPRequestName}}},
			result:        typeRef{name: "Result<" + stdHTTPResponseName + "," + stdHTTPFailureName + ">"},
			effects:       []string{effectNetwork},
			native:        nativeStdHTTPFetch,
		},
		{stability: StabilityStable, canonical: string(nativeStdHTTPHeaderValues),
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
		{stability: StabilityStable, canonical: string(nativeStdHTTPStatusText),
			namespace:     "std.http",
			name:          "StatusText",
			documentation: "Returns the standard text for Status, or null when Status is unknown.",
			params:        []paramDecl{{name: "Status", typ: typeRef{name: "int"}}},
			result:        typeRef{name: "string?"},
			native:        nativeStdHTTPStatusText,
		},
		{stability: StabilityStable, canonical: string(nativeStdHTTPServerServe),
			namespace:     "std.http.server",
			name:          "Serve",
			documentation: "Binds Config.Address, invokes a task-safe Application.Handle concurrently under documented limits and timeouts, and blocks until SIGINT, SIGTERM, or a fatal listener failure.",
			params: []paramDecl{
				{name: "Config", typ: typeRef{name: stdHTTPServerConfigName}},
				{name: "Application", typ: typeRef{name: stdHTTPServerHandlerName}},
			},
			result:  typeRef{name: "Result<null," + stdHTTPServerFailureName + ">"},
			effects: sortedOperationEffects(allOperationEffects()),
			native:  nativeStdHTTPServerServe,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathJoin),
			namespace:     "std.path",
			name:          "Join",
			documentation: "Joins Parts using platform path rules and cleans the result.",
			params:        []paramDecl{{name: "Parts", typ: typeRef{name: "string[]"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathJoin,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathClean),
			namespace:     "std.path",
			name:          "Clean",
			documentation: "Returns the shortest platform-equivalent spelling of Path.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathClean,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathBase),
			namespace:     "std.path",
			name:          "Base",
			documentation: "Returns the final element of Path after platform cleaning.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathBase,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathDirectory),
			namespace:     "std.path",
			name:          "Directory",
			documentation: "Returns every element of Path except the final one.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdPathDirectory,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathExtension),
			namespace:     "std.path",
			name:          "Extension",
			documentation: "Returns Path's final extension without the dot, or null when absent.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string?"},
			native:        nativeStdPathExtension,
		},
		{stability: StabilityStable, canonical: string(nativeStdPathIsAbsolute),
			namespace:     "std.path",
			name:          "IsAbsolute",
			documentation: "Reports whether Path is absolute under the current platform rules.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "bool"},
			native:        nativeStdPathIsAbsolute,
		},
		{stability: StabilityStable, canonical: string(nativeStdTextTrim),
			namespace:     "std.text",
			name:          "Trim",
			documentation: "Removes leading and trailing Unicode whitespace from Text.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdTextTrim,
		},
		{stability: StabilityStable, canonical: string(nativeStdTextContains),
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
		{stability: StabilityStable, canonical: string(nativeStdTextStartsWith),
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
		{stability: StabilityStable, canonical: string(nativeStdTextEndsWith),
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
		{stability: StabilityStable, canonical: string(nativeStdTextSplit),
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
		{stability: StabilityStable, canonical: string(nativeStdTextJoin),
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
		{stability: StabilityStable, canonical: string(nativeStdTextReplaceAll),
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
		{stability: StabilityStable, canonical: string(nativeStdTextCut),
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
		{stability: StabilityStable, canonical: string(nativeStdTextQuote),
			namespace:     "std.text",
			name:          "Quote",
			documentation: "Returns Text as a deterministic double-quoted Slick string literal.",
			params:        []paramDecl{{name: "Text", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "string"},
			native:        nativeStdTextQuote,
		},
		{stability: StabilityStable, canonical: string(nativeStdIOReaderFromBytes),
			namespace:     "std.io",
			name:          "ReaderFromBytes",
			documentation: "Creates a closeable Reader over an immutable snapshot of Value.",
			params:        []paramDecl{{name: "Value", typ: typeRef{name: "bytes"}}},
			result:        typeRef{name: stdIOReaderName},
			native:        nativeStdIOReaderFromBytes,
		},
		{stability: StabilityStable, canonical: string(nativeStdIOWriterToBytes),
			namespace:     "std.io",
			name:          "WriterToBytes",
			documentation: "Creates a closeable in-memory BytesWriter.",
			result:        typeRef{name: stdIOBytesWriterName},
			native:        nativeStdIOWriterToBytes,
		},
		{stability: StabilityStable, canonical: string(nativeStdIOReadAll),
			namespace:     "std.io",
			name:          "ReadAll",
			documentation: "Reads through Reader until end-of-stream without accepting more than MaxBytes.",
			params: []paramDecl{
				{name: "Reader", typ: typeRef{name: stdIOReaderName}},
				{name: "MaxBytes", typ: typeRef{name: "int"}},
			},
			result:  typeRef{name: "Result<bytes," + stdIOFailureName + ">"},
			effects: []string{effectIO},
			native:  nativeStdIOReadAll,
		},
		{stability: StabilityStable, canonical: string(nativeStdIOCopy),
			namespace:     "std.io",
			name:          "Copy",
			documentation: "Copies bytes from Reader to Writer without writing beyond MaxBytes.",
			params: []paramDecl{
				{name: "Reader", typ: typeRef{name: stdIOReaderName}},
				{name: "Writer", typ: typeRef{name: stdIOWriterName}},
				{name: "MaxBytes", typ: typeRef{name: "int"}},
			},
			result:  typeRef{name: "Result<int," + stdIOFailureName + ">"},
			effects: []string{effectIO},
			native:  nativeStdIOCopy,
		},
		{stability: StabilityStable, canonical: string(nativeStdSQLiteOpen),
			namespace:     "std.sqlite",
			name:          "Open",
			documentation: "Opens a file-backed or :memory: SQLite database handle.",
			params:        []paramDecl{{name: "Path", typ: typeRef{name: "string"}}},
			result:        typeRef{name: "Result<" + stdSQLiteDatabaseName + "," + stdSQLiteFailureName + ">"},
			effects:       []string{effectDatabase},
			native:        nativeStdSQLiteOpen,
		},
	},
	classes: []standardClassDecl{
		{stability: StabilityStable, canonical: stdCollectionsBoundsFailureName,
			namespace:     "std.collections",
			name:          "BoundsFailure",
			documentation: "Reports an invalid half-open collection range or indexed write.",
			isError:       true,
		},
		{stability: StabilityStable, canonical: stdBytesBoundsFailureName,
			namespace:     "std.bytes",
			name:          "BoundsFailure",
			documentation: "Describes invalid bounds for a half-open immutable byte slice.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Start", typ: typeRef{name: "int"}, documentation: "Provides the requested inclusive start byte offset."},
				{stability: StabilityStable, name: "End", typ: typeRef{name: "int"}, documentation: "Provides the requested exclusive end byte offset."},
				{stability: StabilityStable, name: "Length", typ: typeRef{name: "int"}, documentation: "Provides the source byte length."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why the requested bounds are invalid."},
			},
		},
		{stability: StabilityStable, canonical: stdBytesValueFailureName,
			namespace:     "std.bytes",
			name:          "ValueFailure",
			documentation: "Describes the first integer that cannot represent a byte.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Index", typ: typeRef{name: "int"}, documentation: "Provides the index of the first invalid value."},
				{stability: StabilityStable, name: "Value", typ: typeRef{name: "int"}, documentation: "Provides the invalid integer value."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the valid byte value range."},
			},
		},
		{stability: StabilityStable, canonical: stdUTF8DecodedRuneName,
			namespace:     "std.utf8",
			name:          "DecodedRune",
			documentation: "Contains one decoded Unicode scalar value and its UTF-8 byte width.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Value", typ: typeRef{name: "int"}, documentation: "Provides the Unicode scalar value."},
				{stability: StabilityStable, name: "Width", typ: typeRef{name: "int"}, documentation: "Provides the scalar's UTF-8 width in bytes."},
			},
		},
		{stability: StabilityStable, canonical: stdUTF8FailureName,
			namespace:     "std.utf8",
			name:          "Failure",
			documentation: "Describes an invalid byte offset or UTF-8 encoding.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Index", typ: typeRef{name: "int"}, documentation: "Provides the requested byte offset."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why decoding failed."},
			},
		},
		{stability: StabilityStable, canonical: stdBytesUtf8FailureName,
			namespace:     "std.bytes",
			name:          "Utf8Failure",
			documentation: "Describes a failure to decode immutable bytes as UTF-8 text.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why the byte value is not valid UTF-8."},
			},
		},
		{stability: StabilityStable, canonical: stdConvertFailureName,
			namespace:     "std.convert",
			name:          "Failure",
			documentation: "Describes a failed primitive conversion.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Target", typ: typeRef{name: "string"}, documentation: "Names the requested destination type."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains why conversion failed without exposing unrelated data."},
			},
		},
		{stability: StabilityStable, canonical: stdMathArithmeticFailureName,
			namespace:     "std.math",
			name:          "ArithmeticFailure",
			documentation: "Describes a failed checked integer division or remainder operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the failing operation: Divide or Remainder."},
				{stability: StabilityStable, name: "Kind", typ: typeRef{name: "string"}, documentation: "Names the failure kind: DivisionByZero or Overflow."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the arithmetic failure."},
			},
		},
		{stability: StabilityStable, canonical: stdProcessStatusName,
			namespace:     "std.process",
			name:          "Status",
			documentation: "Describes the explicit result of a command-line main: output bytes, error bytes, and the process exit code.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "ExitCode", typ: typeRef{name: "int"}, documentation: "Provides the process exit code, which must be 0 through 255."},
				{stability: StabilityStable, name: "Output", typ: typeRef{name: "bytes"}, documentation: "Provides the exact bytes written to standard output without added formatting."},
				{stability: StabilityStable, name: "ErrorOutput", typ: typeRef{name: "bytes"}, documentation: "Provides the exact bytes written to standard error without added formatting."},
			},
		},
		{stability: StabilityStable, canonical: stdProcessCompletedName,
			namespace:     "std.process",
			name:          "Completed",
			documentation: "Contains the complete captured result of a child process that ran and reported an exit code.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "ExitCode", typ: typeRef{name: "int"}, documentation: "Provides the exit code the child process reported, which may be nonzero."},
				{stability: StabilityStable, name: "Output", typ: typeRef{name: "bytes"}, documentation: "Provides the immutable bytes the child wrote to standard output."},
				{stability: StabilityStable, name: "ErrorOutput", typ: typeRef{name: "bytes"}, documentation: "Provides the immutable bytes the child wrote to standard error."},
			},
		},
		{stability: StabilityStable, canonical: stdProcessFailureName,
			namespace:     "std.process",
			name:          "Failure",
			documentation: "Describes a child process that never produced an exit code.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the failing step: Spawn, WorkingDirectory, Wait, Signal, OutputLimit, or Cancelled."},
				{stability: StabilityStable, name: "Program", typ: typeRef{name: "string"}, documentation: "Identifies the program that was asked to run."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without exposing environment values or captured output."},
			},
		},
		{stability: StabilityStable, canonical: stdEnvFailureName,
			namespace:     "std.env",
			name:          "Failure",
			documentation: "Describes a failed process-environment operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the environment operation that failed."},
				{stability: StabilityStable, name: "Name", typ: typeRef{name: "string"}, documentation: "Names the environment entry involved in the failure."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without including the environment value."},
			},
		},
		{stability: StabilityStable, canonical: stdFSFailureName,
			namespace:     "std.fs",
			name:          "Failure",
			documentation: "Describes a failed filesystem operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the filesystem operation that failed."},
				{stability: StabilityStable, name: "Path", typ: typeRef{name: "string"}, documentation: "Identifies the path involved in the failure."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the platform filesystem failure."},
			},
		},
		{stability: StabilityStable, canonical: stdFSEntryName,
			namespace:     "std.fs",
			name:          "Entry",
			documentation: "Describes one direct child of a listed directory.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Name", typ: typeRef{name: "string"}, documentation: "Provides the child name without any directory component."},
				{stability: StabilityStable, name: "Path", typ: typeRef{name: "string"}, documentation: "Provides the listed directory joined with Name."},
				{stability: StabilityStable, name: "IsDirectory", typ: typeRef{name: "bool"}, documentation: "Reports whether the entry itself is a directory without walking it."},
			},
		},
		{stability: StabilityStable, canonical: stdFSTemporaryDirectoryName,
			namespace:      "std.fs",
			name:           "TemporaryDirectory",
			documentation:  "Owns one temporary directory that Close removes recursively.",
			nativeResource: "*slickFSTemporary",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Path", typ: typeRef{name: "string"}, documentation: "Provides the absolute created path, which Close invalidates."},
			},
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Close",
					documentation: "Removes the owned directory tree, does nothing when already closed, and throws Failure otherwise.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdFSFailureName}},
					effects:       []string{effectFilesystem},
					native:        nativeStdFSTemporaryDirectoryClose,
				},
			},
		},
		{stability: StabilityStable, canonical: stdJsonFailureName,
			namespace:     "std.json",
			name:          "Failure",
			documentation: "Describes a failed JSON encoding or decoding operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the JSON operation that failed."},
				{stability: StabilityStable, name: "Path", typ: typeRef{name: "string"}, documentation: "Identifies the structural location of the failure."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the JSON failure without including complete source data."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPRequestName,
			namespace:     "std.http",
			name:          "Request",
			documentation: "Describes one fully buffered HTTP request.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Method", typ: typeRef{name: "string"}, documentation: "Provides the exact HTTP method token."},
				{stability: StabilityStable, name: "URL", typ: typeRef{name: "string"}, documentation: "Provides an absolute HTTP or HTTPS URL."},
				{stability: StabilityStable, name: "Headers", typ: typeRef{name: "Map<string,string[]>?"}, documentation: "Provides optional request headers."},
				{stability: StabilityStable, name: "Body", typ: typeRef{name: "bytes?"}, documentation: "Provides an optional immutable request body."},
				{stability: StabilityStable, name: "TimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Provides a positive whole-request timeout in milliseconds."},
				{stability: StabilityStable, name: "MaxResponseBytes", typ: typeRef{name: "int?"}, documentation: "Provides a positive buffered response limit."},
				{stability: StabilityStable, name: "FollowRedirects", typ: typeRef{name: "bool?"}, documentation: "Controls whether up to ten redirects are followed."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPResponseName,
			namespace:     "std.http",
			name:          "Response",
			documentation: "Contains a complete buffered HTTP response.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Status", typ: typeRef{name: "int"}, documentation: "Provides the HTTP status code."},
				{stability: StabilityStable, name: "URL", typ: typeRef{name: "string"}, documentation: "Provides the effective final URL."},
				{stability: StabilityStable, name: "Headers", typ: typeRef{name: "Map<string,string[]>"}, documentation: "Provides deterministic canonical response headers."},
				{stability: StabilityStable, name: "Body", typ: typeRef{name: "bytes"}, documentation: "Provides the immutable complete response body."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPFailureName,
			namespace:     "std.http",
			name:          "Failure",
			documentation: "Describes a sanitized HTTP request failure.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Kind", typ: typeRef{name: "string"}, documentation: "Provides the stable failure classification."},
				{stability: StabilityStable, name: "URL", typ: typeRef{name: "string"}, documentation: "Provides the sanitized URL without userinfo, query, or fragment."},
				{stability: StabilityStable, name: "Status", typ: typeRef{name: "int?"}, documentation: "Provides the response status when one was received."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without exposing request or response secrets."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPServerConfigName,
			namespace:     "std.http.server",
			name:          "Config",
			documentation: "Configures one bounded inbound HTTP listener. Omitted limits and timeouts use fixed defaults; zero or negative values are errors.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Address", typ: typeRef{name: "string"}, documentation: "Provides the listen address, such as 127.0.0.1:8080 or :0."},
				{stability: StabilityStable, name: "MaxHeaderBytes", typ: typeRef{name: "int?"}, documentation: "Limits request header size; default 1048576."},
				{stability: StabilityStable, name: "MaxBodyBytes", typ: typeRef{name: "int?"}, documentation: "Limits buffered request body size; default 8388608."},
				{stability: StabilityStable, name: "ReadHeaderTimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Limits time to read request headers; default 10000."},
				{stability: StabilityStable, name: "ReadTimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Limits time to read the full request; default 30000."},
				{stability: StabilityStable, name: "WriteTimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Limits time to write the response; default 30000."},
				{stability: StabilityStable, name: "IdleTimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Limits keep-alive idle time; default 120000."},
				{stability: StabilityStable, name: "ShutdownTimeoutMilliseconds", typ: typeRef{name: "int?"}, documentation: "Limits graceful shutdown after SIGINT or SIGTERM; default 30000."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPServerRequestName,
			namespace:     "std.http.server",
			name:          "Request",
			documentation: "Describes one fully buffered inbound HTTP request accepted by Serve.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Method", typ: typeRef{name: "string"}, documentation: "Provides the exact inbound HTTP method token."},
				{stability: StabilityStable, name: "Path", typ: typeRef{name: "string"}, documentation: "Provides the decoded URL path without query or fragment."},
				{stability: StabilityStable, name: "Query", typ: typeRef{name: "Map<string,string[]>"}, documentation: "Provides query values preserving repeated-value order."},
				{stability: StabilityStable, name: "Headers", typ: typeRef{name: "Map<string,string[]>"}, documentation: "Provides canonical request headers preserving repeated-value order."},
				{stability: StabilityStable, name: "Body", typ: typeRef{name: "bytes"}, documentation: "Provides an immutable snapshot of the buffered request body."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPServerResponseName,
			namespace:     "std.http.server",
			name:          "Response",
			documentation: "Describes one fully buffered HTTP response returned by a Handler.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Status", typ: typeRef{name: "int"}, documentation: "Provides the HTTP status code."},
				{stability: StabilityStable, name: "Headers", typ: typeRef{name: "Map<string,string[]>?"}, documentation: "Provides optional response headers."},
				{stability: StabilityStable, name: "Body", typ: typeRef{name: "bytes"}, documentation: "Provides the immutable response body; suppressed for HEAD and no-content statuses."},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPServerFailureName,
			namespace:     "std.http.server",
			name:          "Failure",
			documentation: "Describes a sanitized listener, configuration, or shutdown failure from Serve.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the failing step: Config, Bind, Serve, or Shutdown."},
				{stability: StabilityStable, name: "Address", typ: typeRef{name: "string"}, documentation: "Provides the configured listen address."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without exposing request contents or secrets."},
			},
		},
		{stability: StabilityStable, canonical: stdIOFailureName,
			namespace:     "std.io",
			name:          "Failure",
			documentation: "Describes a failed byte-stream operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the byte-stream operation that failed."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without including transferred byte contents."},
			},
		},
		{stability: StabilityStable, canonical: stdIOBytesReaderName,
			namespace:      "std.io",
			name:           "bytesReader",
			documentation:  "Implements Reader over an immutable byte snapshot.",
			nativeResource: "*slickIOResource",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Read",
					documentation: "Reads at most MaxBytes and returns null only at end-of-stream.",
					params:        []paramDecl{{name: "MaxBytes", typ: typeRef{name: "int"}}},
					result:        typeRef{name: "Result<bytes?," + stdIOFailureName + ">"},
					effects:       []string{effectIO},
					native:        nativeStdIOReaderRead,
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the reader or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					effects:       []string{effectIO},
					native:        nativeStdIOReaderClose,
				},
			},
		},
		{stability: StabilityStable, canonical: stdIOBytesWriterName,
			namespace:      "std.io",
			name:           "BytesWriter",
			documentation:  "Collects written bytes in memory and exposes immutable snapshots.",
			nativeResource: "*slickIOResource",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Write",
					documentation: "Writes the complete immutable Data chunk or returns Failure.",
					params:        []paramDecl{{name: "Data", typ: typeRef{name: "bytes"}}},
					result:        typeRef{name: "Result<null," + stdIOFailureName + ">"},
					effects:       []string{effectIO},
					native:        nativeStdIOWriterWrite,
				},
				{stability: StabilityStable, name: "Bytes",
					documentation: "Returns an immutable snapshot of all bytes written so far.",
					result:        typeRef{name: "bytes"},
					effects:       []string{effectState},
					native:        nativeStdIOWriterBytes,
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the writer or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					effects:       []string{effectIO},
					native:        nativeStdIOWriterClose,
				},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteStatementName,
			namespace:     "std.sqlite",
			name:          "Statement",
			documentation: "Describes one parameter-bound SQL statement for execution.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "SQL", typ: typeRef{name: "string"}, documentation: "Provides the single SQL statement text."},
				{stability: StabilityStable, name: "Parameters", typ: typeRef{name: stdSQLiteValueName + "[]"}, documentation: "Provides positional parameters to bind."},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteQueryName,
			namespace:     "std.sqlite",
			name:          "Query",
			documentation: "Describes one bounded parameter-bound SQL query.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "SQL", typ: typeRef{name: "string"}, documentation: "Provides the single SQL query text."},
				{stability: StabilityStable, name: "Parameters", typ: typeRef{name: stdSQLiteValueName + "[]"}, documentation: "Provides positional parameters to bind."},
				{stability: StabilityStable, name: "MaxRows", typ: typeRef{name: "int"}, documentation: "Limits the maximum number of rows returned."},
				{stability: StabilityStable, name: "MaxBytes", typ: typeRef{name: "int"}, documentation: "Limits cumulative byte size of returned data."},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteRowName,
			namespace:     "std.sqlite",
			name:          "Row",
			documentation: "Contains one returned database row mapped by column name.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Values", typ: typeRef{name: "Map<string," + stdSQLiteValueName + ">"}, documentation: "Maps column names to their typed values."},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteExecutionName,
			namespace:     "std.sqlite",
			name:          "Execution",
			documentation: "Describes the outcome of an executed SQL statement.",
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "RowsAffected", typ: typeRef{name: "int"}, documentation: "Reports the number of rows inserted, updated, or deleted."},
				{stability: StabilityStable, name: "LastInsertId", typ: typeRef{name: "int?"}, documentation: "Provides the optional rowid of the most recently inserted row."},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteFailureName,
			namespace:     "std.sqlite",
			name:          "Failure",
			documentation: "Describes a failed SQLite operation.",
			isError:       true,
			fields: []standardFieldDecl{
				{stability: StabilityStable, name: "Operation", typ: typeRef{name: "string"}, documentation: "Names the database operation that failed."},
				{stability: StabilityStable, name: "Code", typ: typeRef{name: "int?"}, documentation: "Provides the optional SQLite numeric error code."},
				{stability: StabilityStable, name: "Message", typ: typeRef{name: "string"}, documentation: "Explains the failure without exposing parameter or row data."},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteDatabaseName,
			namespace:      "std.sqlite",
			name:           "Database",
			documentation:  "Represents an open SQLite database connection handle safe for concurrent use.",
			nativeResource: "*slickSQLiteDatabase",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Execute",
					documentation: "Executes one parameter-bound statement and returns execution metadata.",
					params:        []paramDecl{{name: "Statement", typ: typeRef{name: stdSQLiteStatementName}}},
					result:        typeRef{name: "Result<" + stdSQLiteExecutionName + "," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteDatabaseExecute,
				},
				{stability: StabilityStable, name: "Query",
					documentation: "Executes one query and returns bounded rows mapped by column name.",
					params:        []paramDecl{{name: "Query", typ: typeRef{name: stdSQLiteQueryName}}},
					result:        typeRef{name: "Result<" + stdSQLiteRowName + "[]," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteDatabaseQuery,
				},
				{stability: StabilityStable, name: "Begin",
					documentation: "Begins a new explicit transaction.",
					result:        typeRef{name: "Result<" + stdSQLiteTransactionName + "," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteDatabaseBegin,
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the database handle and releases all connections.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdSQLiteFailureName}},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteDatabaseClose,
				},
			},
		},
		{stability: StabilityStable, canonical: stdSQLiteTransactionName,
			namespace:      "std.sqlite",
			name:           "Transaction",
			documentation:  "Represents an active SQLite transaction.",
			nativeResource: "*slickSQLiteTransaction",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Execute",
					documentation: "Executes one parameter-bound statement inside this transaction.",
					params:        []paramDecl{{name: "Statement", typ: typeRef{name: stdSQLiteStatementName}}},
					result:        typeRef{name: "Result<" + stdSQLiteExecutionName + "," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteTransactionExecute,
				},
				{stability: StabilityStable, name: "Query",
					documentation: "Executes one query inside this transaction.",
					params:        []paramDecl{{name: "Query", typ: typeRef{name: stdSQLiteQueryName}}},
					result:        typeRef{name: "Result<" + stdSQLiteRowName + "[]," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteTransactionQuery,
				},
				{stability: StabilityStable, name: "Commit",
					documentation: "Commits this transaction.",
					result:        typeRef{name: "Result<null," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteTransactionCommit,
				},
				{stability: StabilityStable, name: "Rollback",
					documentation: "Rolls back this transaction.",
					result:        typeRef{name: "Result<null," + stdSQLiteFailureName + ">"},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteTransactionRollback,
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the transaction, rolling it back if still active.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdSQLiteFailureName}},
					effects:       []string{effectDatabase},
					native:        nativeStdSQLiteTransactionClose,
				},
			},
		},
	},
	interfaces: []standardInterfaceDecl{
		{stability: StabilityStable, canonical: stdIOReaderName,
			namespace:     "std.io",
			name:          "Reader",
			documentation: "Reads bounded immutable byte chunks and supports deterministic cleanup.",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Read",
					documentation: "Reads at most MaxBytes and returns null only at end-of-stream.",
					params:        []paramDecl{{name: "MaxBytes", typ: typeRef{name: "int"}}},
					result:        typeRef{name: "Result<bytes?," + stdIOFailureName + ">"},
					effects:       []string{effectIO},
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the reader or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					effects:       []string{effectIO},
				},
			},
		},
		{stability: StabilityStable, canonical: stdIOWriterName,
			namespace:     "std.io",
			name:          "Writer",
			documentation: "Writes complete immutable byte chunks and supports deterministic cleanup.",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Write",
					documentation: "Writes the complete immutable Data chunk or returns Failure.",
					params:        []paramDecl{{name: "Data", typ: typeRef{name: "bytes"}}},
					result:        typeRef{name: "Result<null," + stdIOFailureName + ">"},
					effects:       []string{effectIO},
				},
				{stability: StabilityStable, name: "Close",
					documentation: "Closes the writer or throws Failure when cleanup fails.",
					result:        typeRef{name: "null"},
					throws:        []typeRef{{name: stdIOFailureName}},
					effects:       []string{effectIO},
				},
			},
		},
		{stability: StabilityStable, canonical: stdHTTPServerHandlerName,
			namespace:     "std.http.server",
			name:          "Handler",
			documentation: "Handles one accepted inbound HTTP request and returns a fully buffered Response.",
			methods: []standardMethodDecl{
				{stability: StabilityStable, name: "Handle",
					documentation: "Produces the Response for Request. Domain failures must become explicit Response values; the method does not throw.",
					params:        []paramDecl{{name: "Request", typ: typeRef{name: stdHTTPServerRequestName}}},
					result:        typeRef{name: stdHTTPServerResponseName},
					effects:       sortedOperationEffects(allOperationEffects()),
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
			operations:    operationEffectRefs(declaration.effects...),
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
				operations:        operationEffectRefs(method.effects...),
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
	valDoc := "Represents one SQLite value alternative: Null, Integer, Float, Text, or Blob."
	nullDoc := "Represents a SQL NULL value."
	intDoc := "Represents a 64-bit signed integer value."
	floatDoc := "Represents a 64-bit IEEE-754 floating-point value."
	textDoc := "Represents a UTF-8 text value."
	blobDoc := "Represents an arbitrary immutable binary byte sequence."
	valFieldDoc := "Provides the payload value."

	valueUnion := &unionDecl{
		name:          "Value",
		qualified:     stdSQLiteValueName,
		namespace:     "std.sqlite",
		aliases:       make(map[string]aliasDecl),
		variants:      make(map[string]*unionVariantDecl),
		order:         []string{"Null", "Integer", "Float", "Text", "Blob"},
		documentation: &valDoc,
	}
	valueUnion.variants["Null"] = &unionVariantDecl{name: "Null", tag: 1, documentation: &nullDoc}
	valueUnion.variants["Integer"] = &unionVariantDecl{
		name: "Integer", tag: 2, documentation: &intDoc,
		fields: []fieldDecl{{name: "Value", typ: typeRef{name: "int"}, documentation: &valFieldDoc}},
	}
	valueUnion.variants["Float"] = &unionVariantDecl{
		name: "Float", tag: 3, documentation: &floatDoc,
		fields: []fieldDecl{{name: "Value", typ: typeRef{name: "float"}, documentation: &valFieldDoc}},
	}
	valueUnion.variants["Text"] = &unionVariantDecl{
		name: "Text", tag: 4, documentation: &textDoc,
		fields: []fieldDecl{{name: "Value", typ: typeRef{name: "string"}, documentation: &valFieldDoc}},
	}
	valueUnion.variants["Blob"] = &unionVariantDecl{
		name: "Blob", tag: 5, documentation: &blobDoc,
		fields: []fieldDecl{{name: "Value", typ: typeRef{name: "bytes"}, documentation: &valFieldDoc}},
	}
	p.unions[stdSQLiteValueName] = valueUnion
	p.registerTerminalAnnotation(&terminalAnnotationDecl{
		canonical:     "std.json.Name",
		params:        []string{"string"},
		targets:       []annotationTarget{annotationTargetField},
		documentation: "Sets the JSON object key for a public class field.",
		apply:         applyStdJSONName,
	})
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
		operations:    operationEffectRefs(declaration.effects...),
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
	for name, annotation := range p.annotations {
		if strings.HasPrefix(name, "std.") && isPublic(annotation.name) {
			requireNamespaces(name)
			require(name, annotation.documentation)
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
	if !interpreterRuntimeOperations.implements(function.native) {
		return runtimeValue{}, fmt.Errorf("interpreter does not implement runtime operation %s", function.native)
	}
	if value, err, ok := p.callNativeStdIO(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdHTTP(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdHTTPServer(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdFS(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdProcess(function, frame); ok {
		return value, err
	}
	if value, err, ok := p.callNativeStdSQLite(function, frame); ok {
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
		contents, err := readTextFileContext(frame.ctx, path)
		if err != nil {
			return runtimeFSFailure(resultType, "ReadText", path, err), nil
		}
		if !utf8.Valid(contents) {
			return runtimeFSFailure(resultType, "ReadText", path, fmt.Errorf("invalid UTF-8")), nil
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "string", scalar: string(contents)}), nil
	case nativeStdFSWriteText:
		path := frame.locals["Path"].scalar.(string)
		err := writeTextFileContext(frame.ctx, path, frame.locals["Contents"].scalar.(string))
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

// runtimePresentValue unwraps an optional runtime value, reporting whether a
// payload is present.
func runtimePresentValue(value runtimeValue) (runtimeValue, bool) {
	if value.optional == nil || !value.optional.present {
		return runtimeValue{}, false
	}
	return value.optional.value, true
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
	return runtimeResultValue(resultType, false, runtimeFSFailureValue(operation, path, err.Error()))
}

func runtimeFSFailureValue(operation, path, message string) runtimeValue {
	return runtimeValue{
		typ: stdFSFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Path":      {typ: "string", scalar: path},
			"Message":   {typ: "string", scalar: message},
		},
	}
}

func (g *goGenerator) emitNativeFunction(function *functionDecl) error {
	if !goRuntimeOperations.implements(function.native) {
		return fmt.Errorf("unknown native Slick function %s", function.native)
	}
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
	if g.program.usesContext {
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
	if g.program.usesContext {
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
		if g.program.usesContext {
			g.line("contents, err := slickFSReadText(slickContext, %s)", arguments[0])
		} else {
			g.line("contents, err := os.ReadFile(%s)", arguments[0])
		}
		g.line("if err != nil {")
		g.emitNativeFSFailure(resultType, "ReadText", arguments[0], "err")
		g.line("}")
		g.line("if !utf8.Valid(contents) {")
		g.emitNativeFSFailure(resultType, "ReadText", arguments[0], `errors.New("invalid UTF-8")`)
		g.line("}")
		g.line("return %s{ok: true, value: string(contents)}, nil", result)
	case nativeStdFSWriteText:
		if g.program.usesContext {
			g.emitNativeFSResult(resultType, "WriteText", arguments[0], fmt.Sprintf("slickFSWriteText(slickContext, %s, %s)", arguments[0], arguments[1]))
		} else {
			g.emitNativeFSResult(resultType, "WriteText", arguments[0], fmt.Sprintf("os.WriteFile(%s, []byte(%s), 0o666)", arguments[0], arguments[1]))
		}
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
	case nativeStdFSReadDirectory, nativeStdFSCreateTemporaryDirectory:
		g.emitNativeStdFSFunction(function, resultType, arguments)
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
		if g.program.usesContext {
			g.line("return slickIOReadAll(slickContext, %s, %s)", arguments[0], arguments[1])
		} else {
			g.line("return slickIOReadAll(%s, %s)", arguments[0], arguments[1])
		}
	case nativeStdIOCopy:
		if g.program.usesContext {
			g.line("return slickIOCopy(slickContext, %s, %s, %s)", arguments[0], arguments[1], arguments[2])
		} else {
			g.line("return slickIOCopy(%s, %s, %s)", arguments[0], arguments[1], arguments[2])
		}
	case nativeStdHTTPFetch:
		callContext := "context.Background()"
		if g.program.usesContext {
			callContext = "slickContext"
		}
		g.line("return slickHTTPFetch(%s, %s)", callContext, arguments[0])
	case nativeStdHTTPServerServe:
		if g.program.usesContext {
			g.line("return slickHTTPServerServe(slickContext, %s, %s)", arguments[0], arguments[1])
		} else {
			g.line("return slickHTTPServerServe(%s, %s)", arguments[0], arguments[1])
		}
	case nativeStdProcessRun:
		callContext := "context.Background()"
		if g.program.usesContext {
			callContext = "slickContext"
		}
		g.line("return slickProcessRun(%s, %s, %s, %s, %s)", callContext, arguments[0], arguments[1], arguments[2], arguments[3])
	case nativeStdHTTPHeaderValues:
		g.line("return slickHTTPHeaderValues(%s, %s), nil", arguments[0], arguments[1])
	case nativeStdHTTPStatusText:
		g.line("return slickHTTPStatusText(%s), nil", arguments[0])
	case nativeStdSQLiteOpen:
		callContext := "context.Background()"
		if g.program.usesContext {
			callContext = "slickContext"
		}
		g.line("return slickSQLiteOpen(%s, %s)", callContext, arguments[0])
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
