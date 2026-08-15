package compiler_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestStdIOExactSurfaceAndStructuralConformance(t *testing.T) {
	source := `
class EmptyReader {
 function Read(MaxBytes: int) -> Result<bytes?, std.io.Failure> { Ok(null) }
 function Close() -> null throws std.io.Failure { null }
}
class EmptyWriter {
 function Write(Data: bytes) -> Result<null, std.io.Failure> { Ok(null) }
 function Close() -> null throws std.io.Failure { null }
}
function Transfer(Reader: std.io.Reader, Writer: std.io.Writer) -> Result<int, std.io.Failure> effects { io } {
 std.io.Copy(Reader, Writer, 4)
}
function main() -> string effects { io } {
 let Reader = EmptyReader {}
 let Writer = EmptyWriter {}
 match Transfer(Reader, Writer) {
  Ok(Count) => std.convert.IntToString(Count)
  Err(Failure) => Failure.Operation + ":" + Failure.Message
 }
}
`
	if got := runResultEverywhere(t, source); got != "0" {
		t.Fatalf("structural std.io transfer = %q, want 0", got)
	}
}

func TestStdIOInMemoryCopyAndSnapshotsEverywhere(t *testing.T) {
	source := `
function main() -> string throws std.io.Failure effects { io, state } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("hello")) {
  using Writer = std.io.WriterToBytes() {
   let Before = Writer.Bytes()
   let Result = std.io.Copy(Reader, Writer, 5)
   let First = Writer.Bytes()
   let Extra = Writer.Write(std.bytes.FromUtf8("!"))
   let Current = Writer.Bytes()
   match Result {
    Ok(Count) => match Extra {
     Ok(_) => match std.bytes.ToUtf8(Before) {
      Ok(BeforeText) => match std.bytes.ToUtf8(First) {
       Ok(FirstText) => match std.bytes.ToUtf8(Current) {
        Ok(CurrentText) => BeforeText + "|" + FirstText + "|" + CurrentText + "|" + std.convert.IntToString(Count)
        Err(Failure) => Failure.Message
       }
       Err(Failure) => Failure.Message
      }
      Err(Failure) => Failure.Message
     }
     Err(Failure) => Failure.Message
    }
    Err(Failure) => Failure.Operation + ":" + Failure.Message
   }
  }
 }
}
`
	if got := runResultEverywhere(t, source); got != "|hello|hello!|5" {
		t.Fatalf("in-memory std.io output = %q", got)
	}
}
func TestStdIOUsingAcceptsReaderWriterAndBytesWriter(t *testing.T) {
	source := `
function AsWriter(Value: std.io.BytesWriter) -> std.io.Writer { Value }
function main() -> string throws std.io.Failure effects { io } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("")) {
  using Writer = AsWriter(std.io.WriterToBytes()) {
   using BytesWriter = std.io.WriterToBytes() { "ok" }
  }
 }
}
`
	if got := runResultEverywhere(t, source); got != "ok" {
		t.Fatalf("using std.io output = %q", got)
	}
}

func TestStdIOLimitsNeverWritePastBoundaryEverywhere(t *testing.T) {
	source := `
function main() -> string throws std.io.Failure effects { io, state } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abcd")) {
  using Writer = std.io.WriterToBytes() {
   let Copied = std.io.Copy(Reader, Writer, 3)
   match Copied {
    Ok(Count) => "unexpected:" + std.convert.IntToString(Count)
    Err(Failure) => match std.bytes.ToUtf8(Writer.Bytes()) {
     Ok(Text) => Failure.Operation + ":" + Failure.Message + ":" + Text
     Err(Utf8Failure) => Utf8Failure.Message
    }
   }
  }
 }
}
`
	if got := runResultEverywhere(t, source); got != "Copy:byte limit exceeded:abc" {
		t.Fatalf("bounded copy output = %q", got)
	}

	exact := strings.Replace(source, `"abcd"`, `"abc"`, 1)
	if got := runResultEverywhere(t, exact); got != "unexpected:3" {
		t.Fatalf("exact-limit copy output = %q", got)
	}
}

func TestStdIOReadAllLimitAndEmptyInputEverywhere(t *testing.T) {
	limited := `
function main() -> string throws std.io.Failure effects { io } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abcd")) {
  match std.io.ReadAll(Reader, 3) {
   Ok(Value) => "unexpected"
   Err(Failure) => Failure.Operation + ":" + Failure.Message
  }
 }
}
`
	if got := runResultEverywhere(t, limited); got != "ReadAll:byte limit exceeded" {
		t.Fatalf("bounded ReadAll output = %q", got)
	}

	empty := strings.Replace(limited, `"abcd"`, `""`, 1)
	empty = strings.Replace(empty, "3)", "0)", 1)
	empty = strings.Replace(empty, `Ok(Value) => "unexpected"`, `Ok(Value) => std.convert.IntToString(std.bytes.Length(Value))`, 1)
	if got := runResultEverywhere(t, empty); got != "0" {
		t.Fatalf("empty ReadAll output = %q", got)
	}
	exact := strings.Replace(limited, `Reader, 3)`, `Reader, 4)`, 1)
	exact = strings.Replace(exact, `Ok(Value) => "unexpected"`, `Ok(Value) => std.convert.IntToString(std.bytes.Length(Value))`, 1)
	if got := runResultEverywhere(t, exact); got != "4" {
		t.Fatalf("exact-limit ReadAll output = %q", got)
	}

}

func TestStdIOInvalidReadFailureEverywhere(t *testing.T) {
	invalid := `
function main() -> string throws std.io.Failure effects { io } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("x")) {
  match Reader.Read(0) {
   Ok(_) => "unexpected"
   Err(Failure) => Failure.Operation + ":" + Failure.Message
  }
 }
}
`
	if got := runResultEverywhere(t, invalid); got != "Read:MaxBytes must be greater than zero" {
		t.Fatalf("invalid Read output = %q", got)
	}
}

func TestStdIOCleanupFailurePreservesTypedFieldsEverywhere(t *testing.T) {
	source := `
class FailingResource {
 function Close() -> null throws std.io.Failure {
  throw std.io.Failure { Operation: "Close", Message: "adapter failure" }
 }
}
function main() -> string {
 using Active = FailingResource {} { "body" } catch (Failure) {
  std.io.Failure => Failure.Operation + ":" + Failure.Message
 }
}
`
	if got := runResultEverywhere(t, source); got != "Close:adapter failure" {
		t.Fatalf("typed cleanup failure = %q", got)
	}
}

func TestStdIOOperationsAfterUsingCloseFailWithoutPanicEverywhere(t *testing.T) {
	source := `
function main() -> string throws std.io.Failure effects { io, state } {
 let Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("x"))
 let ReaderScope = using ActiveReader = Reader { "closed" }
 let ReadFailure = match Reader.Read(1) {
  Ok(_) => "unexpected"
  Err(Failure) => Failure.Operation + ":" + Failure.Message
 }
 let Writer = std.io.WriterToBytes()
 let WriterScope = using ActiveWriter = Writer {
  match ActiveWriter.Write(std.bytes.FromUtf8("A")) {
   Ok(_) => "closed"
   Err(Failure) => Failure.Message
  }
 }
 let Snapshot = match std.bytes.ToUtf8(Writer.Bytes()) {
  Ok(Text) => Text
  Err(Failure) => Failure.Message
 }
 match Writer.Write(std.bytes.FromUtf8("B")) {
  Ok(_) => "unexpected"
  Err(Failure) => ReadFailure + ";" + Snapshot + ";" + Failure.Operation + ":" + Failure.Message
 }
}
`
	want := "Read:reader is closed;A;Write:writer is closed"
	if got := runResultEverywhere(t, source); got != want {
		t.Fatalf("post-close output = %q, want %q", got, want)
	}
}

func TestStdIONoProgressThroughStructuralReaderEverywhere(t *testing.T) {
	source := `
class EmptyChunkReader {
 function Read(MaxBytes: int) -> Result<bytes?, std.io.Failure> { Ok(std.bytes.FromUtf8("")) }
 function Close() -> null throws std.io.Failure { null }
}
function main() -> string effects { io } {
 let Reader = EmptyChunkReader {}
 match std.io.ReadAll(Reader, 10) {
  Ok(_) => "unexpected"
  Err(Failure) => Failure.Operation + ":" + Failure.Message
 }
}
`
	if got := runResultEverywhere(t, source); got != "ReadAll:reader made no progress" {
		t.Fatalf("no-progress output = %q", got)
	}
}

func TestStdIOAliasesAndNegativeLimitsEverywhere(t *testing.T) {
	source := `
use std.io.Reader as ByteReader
use std.io.Failure as IOFailure
use std.io.ReaderFromBytes as OpenReader
use std.io.WriterToBytes as OpenWriter
use std.io.ReadAll as ReadAll
function Consume(Reader: ByteReader) -> Result<bytes, IOFailure> effects { io } { ReadAll(Reader, -1) }
function main() -> string throws IOFailure effects { io } {
 using Reader = OpenReader(std.bytes.FromUtf8("")) {
  using Writer = OpenWriter() {
   match Writer.Write(std.bytes.FromUtf8("")) {
    Ok(_) => match Consume(Reader) {
     Ok(_) => "unexpected"
     Err(Failure) => Failure.Operation + ":" + Failure.Message
    }
    Err(Failure) => Failure.Operation + ":" + Failure.Message
   }
  }
 }
}
`
	if got := runResultEverywhere(t, source); got != "ReadAll:MaxBytes must not be negative" {
		t.Fatalf("aliased negative-limit output = %q", got)
	}

	copySource := strings.Replace(source, "match Consume(Reader)", "match std.io.Copy(Reader, Writer, -1)", 1)
	if got := runResultEverywhere(t, copySource); got != "Copy:MaxBytes must not be negative" {
		t.Fatalf("negative Copy output = %q", got)
	}
}

func TestStdIOLargePayloadIncludingNULEverywhere(t *testing.T) {
	payload := strings.Repeat("ab", 20_000) + "\x00tail"
	source := fmt.Sprintf(`
function main() -> string throws std.io.Failure effects { io } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8(%s)) {
  match std.io.ReadAll(Reader, 50000) {
   Ok(Value) => match std.bytes.ToUtf8(Value) {
    Ok(Text) => Text
    Err(Failure) => Failure.Message
   }
   Err(Failure) => Failure.Operation + ":" + Failure.Message
  }
 }
}
`, strconv.Quote(payload))
	if got := runResultEverywhere(t, source); got != payload {
		t.Fatalf("large binary-safe output length = %d, want %d", len(got), len(payload))
	}
}

func TestStdIOCopyWrapsStructuralWriterFailuresEverywhere(t *testing.T) {
	source := `
class FailingWriter {
 function Write(Data: bytes) -> Result<null, std.io.Failure> {
  Err(std.io.Failure { Operation: "Write", Message: "blocked" })
 }
 function Close() -> null throws std.io.Failure { null }
}
function main() -> string throws std.io.Failure effects { io } {
 using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abc")) {
  using Writer = FailingWriter {} {
   match std.io.Copy(Reader, Writer, 8) {
    Ok(_) => "unexpected"
    Err(Failure) => Failure.Operation + ":" + Failure.Message
   }
  }
 }
}
`
	if got := runResultEverywhere(t, source); got != "Copy:blocked" {
		t.Fatalf("structural writer failure = %q", got)
	}
}

func TestStdIORejectsMalformedImplementationsAndCalls(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name: "reader method result",
			source: `class BadReader { function Read(MaxBytes: int) -> bytes? { null } function Close() -> null throws std.io.Failure { null } }
function Use(Reader: std.io.Reader) -> null { null }
function main() -> null { Use(BadReader {}) }`,
			code:    "SLK",
			message: "does not implement std.io.Reader",
		},
		{
			name:    "read argument",
			source:  `function main() -> null throws std.io.Failure effects { io } { using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("")) { Reader.Read("many") null } }`,
			code:    "SLK",
			message: "argument 1",
		},
		{
			name:    "direct resource close",
			source:  `function main() -> null effects { io } { let Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("")) Reader.Close() }`,
			code:    "SLK393",
			message: "must be closed by a using scope",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := compiler.Check([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: test.source}})
			found := false
			for _, diagnostic := range diagnostics {
				if strings.Contains(diagnostic.Code, test.code) && strings.Contains(diagnostic.Message, test.message) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing diagnostic containing %q: %+v", test.message, diagnostics)
			}
		})
	}
}
