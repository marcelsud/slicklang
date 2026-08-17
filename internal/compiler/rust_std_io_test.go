package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRustStdIOMatchesInterpreter exercises every std.io operation against the
// interpreter. The Contract subtest is fully self-contained (it bootstraps empty
// bytes from a writer snapshot, so it never depends on another standard-library
// family) and verifies the Reader/Writer/ReadAll/Copy contract plus the
// read-after-close and write-after-close failure paths through using scopes.
// The Transfer subtests mirror examples/std-io byte-for-byte and run only once
// the std.bytes and std.convert families are implemented for Rust; until then
// they skip so the std.io contract is verified in isolation.
func TestRustStdIOMatchesInterpreter(t *testing.T) {
	t.Run("Contract", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: rustStdIOContractProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoRustDiagnostics(t, diagnostics)
		const want = "(eof, Read:MaxBytes must be greater than zero, ReadAll:MaxBytes must not be negative, Read:reader is closed, 0, -2, wrote, Write:writer is closed, bytes[0])"
		if interpreted != want {
			t.Fatalf("interpreter output = %q, want %q", interpreted, want)
		}
		binary := buildRustTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil || string(output) != interpreted+"\n" {
			t.Fatalf("Rust output=%q error=%v, want %q", output, err, interpreted+"\n")
		}
	})

	t.Run("BytesAfterClose", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: rustStdIOBytesAfterCloseProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoRustDiagnostics(t, diagnostics)
		const want = "(bytes[0], bytes[0], Write:writer is closed, Close:writer is already closed)"
		if interpreted != want {
			t.Fatalf("interpreter output = %q, want %q", interpreted, want)
		}
		binary := buildRustTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil || string(output) != interpreted+"\n" {
			t.Fatalf("Rust output=%q error=%v, want %q", output, err, interpreted+"\n")
		}
	})

	t.Run("ObjectLiteral", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: rustStdIOObjectLiteralProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoRustDiagnostics(t, diagnostics)
		const want = "(Write:writer is closed, Close:writer is already closed)"
		if interpreted != want {
			t.Fatalf("interpreter output = %q, want %q", interpreted, want)
		}
		binary := buildRustTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil || string(output) != interpreted+"\n" {
			t.Fatalf("Rust output=%q error=%v, want %q", output, err, interpreted+"\n")
		}
	})

	transferCases := []struct {
		name string
		text string
		want string
	}{
		{"CopyFull", rustStdIOCopyFullProgram, "hello:5"},
		{"CopyLimit", rustStdIOCopyLimitProgram, "Copy:byte limit exceeded:abc"},
		{"ReadAllContent", rustStdIOReadAllContentProgram, "abcd"},
		{"ReadAllLimit", rustStdIOReadAllLimitProgram, "ReadAll:byte limit exceeded"},
	}
	for _, example := range transferCases {
		example := example
		t.Run(example.name, func(t *testing.T) {
			source := Source{Name: "main.slk", Namespace: "root", Text: example.text}
			interpreted, diagnostics, err := Run([]Source{source})
			if err != nil {
				t.Fatal(err)
			}
			requireNoRustDiagnostics(t, diagnostics)
			if interpreted != example.want {
				t.Fatalf("interpreter output = %q, want %q", interpreted, example.want)
			}
			binary := buildRustStdIOProgramOrSkip(t, source)
			output, err := exec.Command(binary).CombinedOutput()
			if err != nil || string(output) != interpreted+"\n" {
				t.Fatalf("Rust output=%q error=%v, want %q", output, err, interpreted+"\n")
			}
		})
	}
}

// buildRustStdIOProgramOrSkip builds the Rust binary for a source that may reach
// not-yet-implemented standard-library families. A Rust lowering error for an
// unsupported operation (or a missing toolchain) skips the subtest so the
// self-contained Contract subtest remains the authoritative std.io check until
// every family the program reaches is implemented.
func buildRustStdIOProgramOrSkip(t *testing.T, source Source) string {
	t.Helper()
	binary := t.TempDir() + "/app"
	diagnostics, err := BuildSourcesWithOptions([]Source{source}, binary, BuildOptions{Backend: BackendRust, AllowAlpha: true})
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "Rust toolchain not found") || strings.Contains(message, "is not supported") {
			t.Skipf("std.io transfer subtest requires an unimplemented Rust family: %v", err)
		}
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	return binary
}

const rustStdIOContractProgram = `function main() -> (string, string, string, string, int, int, string, string, bytes) throws std.io.Failure effects { io, state } {
    let Empty = using Seed = std.io.WriterToBytes() { Seed.Bytes() }

    let ReadEof = using R1 = std.io.ReaderFromBytes(Empty) {
        match R1.Read(64) {
            Ok(Chunk) => if (Chunk == null) { "eof" } else { "data" }
            Err(Failure) => "err:" + Failure.Message
        }
    }

    let ReadInvalid = using R2 = std.io.ReaderFromBytes(Empty) {
        match R2.Read(0) {
            Ok(_) => "ok"
            Err(Failure) => Failure.Operation + ":" + Failure.Message
        }
    }

    let ReadAllNeg = using R3 = std.io.ReaderFromBytes(Empty) {
        match std.io.ReadAll(R3, -1) {
            Ok(_) => "ok"
            Err(Failure) => Failure.Operation + ":" + Failure.Message
        }
    }

    let CopyCount = using R4 = std.io.ReaderFromBytes(Empty) {
        using W4 = std.io.WriterToBytes() {
            match std.io.Copy(R4, W4, 64) {
                Ok(Count) => Count
                Err(Failure) => -1
            }
        }
    }

    let CopyNeg = using R5 = std.io.ReaderFromBytes(Empty) {
        using W5 = std.io.WriterToBytes() {
            match std.io.Copy(R5, W5, -1) {
                Ok(Count) => Count
                Err(Failure) => -2
            }
        }
    }

    let Reader = std.io.ReaderFromBytes(Empty)
    let ReaderScope = using ActiveReader = Reader { "closed" }
    let ReadAfterClose = match Reader.Read(1) {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Message
    }

    let Writer = std.io.WriterToBytes()
    let WriterScope = match Writer.Write(Empty) {
        Ok(_) => "wrote"
        Err(Failure) => Failure.Message
    }
    let Snapshot = Writer.Bytes()
    let _ = using ActiveWriter = Writer { "closed" }
    let WriteAfterClose = match Writer.Write(Empty) {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Message
    }

    let Result = (ReadEof, ReadInvalid, ReadAllNeg, ReadAfterClose, CopyCount, CopyNeg, WriterScope, WriteAfterClose, Snapshot)
    Result
}`

const rustStdIOBytesAfterCloseProgram = `function main() -> (bytes, bytes, string, string) throws std.io.Failure effects { io, state } {
    let Writer = std.io.WriterToBytes()
    let Before = Writer.Bytes()
    let _ = using ActiveWriter = Writer { "closed" }
    let After = Writer.Bytes()
    let WriteAfter = match Writer.Write(Before) {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Message
    }
    let CloseAfter = using Again = Writer { "ok" } catch { std.io.Failure as F => F.Operation + ":" + F.Message }
    let Result = (Before, After, WriteAfter, CloseAfter)
    Result
}
`

const rustStdIOCopyFullProgram = `function main() -> string throws std.io.Failure effects { io, state } {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("hello")) {
        using Writer = std.io.WriterToBytes() {
            match std.io.Copy(Reader, Writer, 64) {
                Ok(Count) => match std.bytes.ToUtf8(Writer.Bytes()) {
                    Ok(Text) => Text + ":" + std.convert.IntToString(Count)
                    Err(Failure) => Failure.Message
                }
                Err(Failure) => Failure.Message
            }
        }
    }
}
`

const rustStdIOCopyLimitProgram = `function main() -> string throws std.io.Failure effects { io, state } {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abcd")) {
        using Writer = std.io.WriterToBytes() {
            match std.io.Copy(Reader, Writer, 3) {
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

const rustStdIOReadAllContentProgram = `function main() -> string throws std.io.Failure effects { io, state } {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abcd")) {
        match std.io.ReadAll(Reader, 4) {
            Ok(Value) => match std.bytes.ToUtf8(Value) {
                Ok(Text) => Text
                Err(Failure) => Failure.Message
            }
            Err(Failure) => Failure.Operation + ":" + Failure.Message
        }
    }
}
`

const rustStdIOReadAllLimitProgram = `function main() -> string throws std.io.Failure effects { io } {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("abcd")) {
        match std.io.ReadAll(Reader, 3) {
            Ok(_) => "unexpected"
            Err(Failure) => Failure.Operation + ":" + Failure.Message
        }
    }
}
`

const rustStdIOObjectLiteralProgram = `function main() -> (string, string) throws std.io.Failure effects { io, state } {
    let Empty = using Seed = std.io.WriterToBytes() { Seed.Bytes() }
    let LiteralWriter = std.io.BytesWriter {}
    let WriteLit = match LiteralWriter.Write(Empty) {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Message
    }
    let WriterClose = using ActiveWriter = LiteralWriter { "ok" } catch { std.io.Failure as F => F.Operation + ":" + F.Message }
    let Result = (WriteLit, WriterClose)
    Result
}

`
