package compiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestBunStdFilesystemMatchesInterpreter exercises every std.env, std.fs, and
// std.io operation the Bun backend owns against the interpreter. Filesystem
// work runs inside a fixed root the test publishes through SLICK_TEST_FS_ROOT
// so the interpreter and the compiled Bun binary address the identical path
// and the missing-file failure message normalizes to the same text in both.
// The temporary-directory path is random, so only deterministic facts about it
// (absolute, Close removes the tree, idempotent second Close, Close after the
// directory is already gone, literal Close throws, TMPDIR overlay, rejected
// traversal Prefix) reach the compared output. IO programs cover copy limits,
// Bytes-after-Close.
func TestBunStdFilesystemMatchesInterpreter(t *testing.T) {
	t.Setenv("SLICK_TEST_FS_ROOT", t.TempDir())
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemProgram}
	compareBunWithInterpreter(t, source)

	t.Run("IOContract", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOContractProgram})
	})
	t.Run("BytesAfterClose", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOBytesAfterCloseProgram})
	})
	t.Run("CopyLimit", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOCopyLimitProgram})
	})
	t.Run("CopyFull", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOCopyFullProgram})
	})
	t.Run("ReadAfterClose", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOContractProgram})
	})
	t.Run("TraversalPrefix", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemTraversalProgram})
	})
	t.Run("DevNull", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemDevNullProgram})
	})
	t.Run("DirectoryByteOrder", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, string(rune(0xE000))), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, string(rune(0x10000))), []byte("b"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLICK_TEST_SORT_DIR", dir)
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemSortProgram}
		compareBunWithInterpreter(t, source)
		interpreted, _, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		want := string(rune(0xE000)) + "," + string(rune(0x10000))
		if interpreted != want {
			t.Fatalf("directory order = %q, want %q", interpreted, want)
		}
	})
	t.Run("NulPath", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemNulProgram})
	})
	t.Run("BOMPrefixed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bom.txt")
		if err := os.WriteFile(path, []byte{0xEF, 0xBB, 0xBF, 'h', 'i'}, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLICK_TEST_BOM", path)
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemBOMProgram})
	})
	t.Run("ManyChunks", func(t *testing.T) {
		compareBunWithInterpreter(t, Source{Name: "main.slk", Namespace: "root", Text: bunStdIOManyChunksProgram})
	})
	t.Run("TempMode", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemTempModeProgram}
		binary := buildBunTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Bun binary failed: %v\noutput=%q", err, output)
		}
		path := strings.TrimSpace(string(output))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat temp dir %q: %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("temp dir mode = %04o, want 0700", perm)
		}
		_ = os.RemoveAll(path)
	})
	t.Run("CancelledFIFO", func(t *testing.T) {
		fifo := filepath.Join(t.TempDir(), "block.fifo")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatalf("create FIFO: %v", err)
		}
		t.Setenv("SLICK_TEST_FIFO", fifo)
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemCancelledFIFOProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatalf("interpreter run failed: %v", err)
		}
		requireNoRustDiagnostics(t, diagnostics)
		if interpreted != "started" {
			t.Fatalf("interpreter output = %q, want started", interpreted)
		}
		binary := buildBunTestProgram(t, source)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = append(os.Environ(), "SLICK_TEST_FIFO="+fifo)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Bun binary failed: %v\noutput=%q", err, output)
		}
		if string(output) != interpreted+"\n" {
			t.Fatalf("Bun output=%q\nwant %q", output, interpreted+"\n")
		}
	})

	hostTmp := t.TempDir()
	overlayTmp := t.TempDir()
	t.Setenv("TMPDIR", hostTmp)
	t.Setenv("SLICK_TEST_TMPDIR_OVERLAY", overlayTmp)
	overlaySource := Source{Name: "main.slk", Namespace: "root", Text: bunStdFilesystemOverlayProgram}
	overlayBinary := buildBunTestProgram(t, overlaySource)
	overlayCmd := exec.Command(overlayBinary)
	overlayCmd.Env = append(os.Environ(), "TMPDIR="+hostTmp, "SLICK_TEST_TMPDIR_OVERLAY="+overlayTmp)
	overlayOutput, err := overlayCmd.CombinedOutput()
	if err != nil || string(overlayOutput) != "true\n" {
		t.Fatalf("TMPDIR overlay Bun output=%q error=%v, want true", overlayOutput, err)
	}
	interpretedOverlay, overlayDiagnostics, err := Run([]Source{overlaySource})
	if err != nil {
		t.Fatalf("TMPDIR overlay interpreter run failed: %v", err)
	}
	requireNoRustDiagnostics(t, overlayDiagnostics)
	if interpretedOverlay != "true" {
		t.Fatalf("TMPDIR overlay interpreter output=%q, want true", interpretedOverlay)
	}
}

func compareBunWithInterpreter(t *testing.T, source Source) {
	t.Helper()
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatalf("interpreter run failed: %v", err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun binary failed: %v\noutput=%q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun output=%q\nwant %q", output, interpreted+"\n")
	}
	if strings.Contains(string(output), "host error") {
		t.Fatalf("Bun leaked a host error instead of a Slick result: %s", output)
	}
}

const bunStdFilesystemProgram = `
function DirHead(Entries: std.fs.Entry[]) -> (int, string, bool) {
    let Length = Entries.Length()
    let First = Entries.Get(0)
    if (First == null) {
        (Length, "none", false)
    } else {
        (Length, First.Name, First.IsDirectory)
    }
}

function ListDir(Path: string) -> (int, string, bool) effects { filesystem } {
    match std.fs.ReadDirectory(Path) {
        Ok(Entries) => DirHead(Entries)
        Err(_) => (0, "err", false)
    }
}

function CloseCheck(Dir: std.fs.TemporaryDirectory) -> string throws std.fs.Failure effects { filesystem } {
    let _ = Dir.Close()
    "ok"
}

function TmpBody(Dir: std.fs.TemporaryDirectory) -> (bool, string, bool, string) effects { filesystem } {
    let Abs = std.text.StartsWith(Dir.Path, "/")
    let C1 = CloseCheck(Dir) catch { std.fs.Failure as F => "fail:" + F.Operation }
    let Ex = match std.fs.Exists(Dir.Path) {
        Ok(B) => B
        Err(_) => true
    }
    let C2 = CloseCheck(Dir) catch { std.fs.Failure as F => "fail:" + F.Operation }
    let Out = (Abs, C1, Ex, C2)
    Out
}

function TmpCheck() -> (bool, string, bool, string) effects { filesystem } {
    match std.fs.CreateTemporaryDirectory("slickstd") {
        Ok(Dir) => TmpBody(Dir)
        Err(_) => (false, "err", true, "err")
    }
}

function LiteralClose() -> string throws std.fs.Failure effects { filesystem } {
    let Literal = std.fs.TemporaryDirectory { Path: "/tmp/slick_literal_absent" }
    let _ = Literal.Close()
    "ok"
}

function GoneBody(Dir: std.fs.TemporaryDirectory) -> string effects { filesystem } {
    match std.fs.Remove(Dir.Path) {
        Ok(_) => CloseCheck(Dir) catch { std.fs.Failure as F => "fail:" + F.Operation }
        Err(F) => "fail:" + F.Operation
    }
}

function AlreadyGone() -> string effects { filesystem } {
    match std.fs.CreateTemporaryDirectory("slickgone") {
        Ok(Dir) => GoneBody(Dir)
        Err(_) => "err"
    }
}

function main() -> (string, string, string, bool, string, bool, bool, bool, int, string, bool, string, bool, string, bool, string, string, string) effects { environment, filesystem } {
    let EnvName = "SLICK_TEST_ENV_42"
    let _ = std.env.Unset(EnvName)
    let _ = std.env.Set(EnvName, "Ada")
    let Got = std.env.Get(EnvName)
    let Present = if (Got == null) { "missing" } else { Got }
    let _ = std.env.Unset(EnvName)
    let Got2 = std.env.Get(EnvName)
    let Absent = if (Got2 == null) { "missing" } else { Got2 }
    let BadSet = std.env.Set("BAD=NAME", "v")
    let BadMsg = match BadSet {
        Ok(_) => "ok"
        Err(F) => F.Operation + ": " + F.Message
    }

    let RootOpt = std.env.Get("SLICK_TEST_FS_ROOT")
    let Root = if (RootOpt == null) { "/tmp/slick_no_root" } else { RootOpt }
    let File = Root + "/hello.txt"
    let WriteR = std.fs.WriteText(File, "hello")
    let WriteOk = match WriteR {
        Ok(_) => true
        Err(_) => false
    }
    let ReadR = std.fs.ReadText(File)
    let Contents = match ReadR {
        Ok(S) => S
        Err(F) => F.Operation
    }
    let ExistsBefore = match std.fs.Exists(File) {
        Ok(B) => B
        Err(_) => false
    }
    let _ = std.fs.Remove(File)
    let ExistsAfter = match std.fs.Exists(File) {
        Ok(B) => B
        Err(_) => false
    }
    let Sub = Root + "/sub"
    let MkdirR = std.fs.CreateDirectoryAll(Sub)
    let MkdirOk = match MkdirR {
        Ok(_) => true
        Err(_) => false
    }
    let DirInfo = ListDir(Root)
    let (DirLen, FirstName, FirstIsDir) = DirInfo
    let Missing = Root + "/missing"
    let MissingR = std.fs.ReadText(Missing)
    let MissingMsg = match MissingR {
        Ok(S) => "ok:" + S
        Err(F) => F.Operation + ": " + F.Message
    }

    let TmpInfo = TmpCheck()
    let (TmpAbs, TmpClose1, TmpExists, TmpClose2) = TmpInfo
    let LitTag = LiteralClose() catch { std.fs.Failure as F => "fail:" + F.Operation }
    let GoneTag = AlreadyGone()
    let Out = (Present, Absent, BadMsg, WriteOk, Contents, ExistsBefore, ExistsAfter, MkdirOk, DirLen, FirstName, FirstIsDir, MissingMsg, TmpAbs, TmpClose1, TmpExists, TmpClose2, LitTag, GoneTag)
    Out
}
`

const bunStdFilesystemOverlayProgram = `
function OverlayBody(Dir: std.fs.TemporaryDirectory, Overlay: string) -> bool throws std.fs.Failure effects { filesystem } {
    let Hit = std.text.StartsWith(Dir.Path, Overlay)
    let _ = Dir.Close()
    Hit
}

function main() -> bool throws std.fs.Failure effects { environment, filesystem } {
    let OverlayOpt = std.env.Get("SLICK_TEST_TMPDIR_OVERLAY")
    let Overlay = if (OverlayOpt == null) { "/tmp/slick_no_overlay" } else { OverlayOpt }
    let _ = std.env.Set("TMPDIR", Overlay)
    match std.fs.CreateTemporaryDirectory("slickovl") {
        Ok(Dir) => OverlayBody(Dir, Overlay)
        Err(_) => false
    }
}
`

const bunStdFilesystemTraversalProgram = `
function main() -> string effects { filesystem } {
    match std.fs.CreateTemporaryDirectory("../escape-") {
        Ok(_) => "unexpected success"
        Err(Failure) => Failure.Operation + "|" + Failure.Path + "|" + Failure.Message
    }
}
`

const bunStdIOContractProgram = `function main() -> (string, string, string, string, int, int, string, string, bytes) throws std.io.Failure effects { io, state } {
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

const bunStdIOBytesAfterCloseProgram = `function main() -> (bytes, bytes, string, string) throws std.io.Failure effects { io, state } {
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

const bunStdIOCopyLimitProgram = `function main() -> string throws std.io.Failure effects { io, state } {
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

const bunStdIOCopyFullProgram = `function main() -> string throws std.io.Failure effects { io, state } {
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

const bunStdFilesystemDevNullProgram = `function ReadDevNull() -> string effects { filesystem } {
    match std.fs.ReadText("/dev/null") {
        Ok(_) => "ok"
        Err(F) => F.Message
    }
}

function main() -> string effects { filesystem } {
    async let Work = ReadDevNull()
    await Work
}
`

const bunStdFilesystemSortProgram = `function Names(Entries: std.fs.Entry[]) -> string {
    let First = Entries.Get(0)
    if (First == null) {
        "short"
    } else {
        let Second = Entries.Get(1)
        if (Second == null) {
            "short"
        } else {
            First.Name + "," + Second.Name
        }
    }
}

function main() -> string effects { environment, filesystem } {
    let Path = std.env.Get("SLICK_TEST_SORT_DIR")
    if (Path == null) {
        "missing"
    } else {
        match std.fs.ReadDirectory(Path) {
            Ok(Entries) => Names(Entries)
            Err(F) => F.Message
        }
    }
}
`

const bunStdFilesystemNulProgram = "function main() -> string effects { filesystem } {\n    match std.fs.ReadText(\"foo\\u0000bar\") {\n        Ok(_) => \"ok\"\n        Err(F) => F.Message\n    }\n}\n"

const bunStdFilesystemBOMProgram = `function main() -> string effects { environment, filesystem } {
    let Path = std.env.Get("SLICK_TEST_BOM")
    if (Path == null) {
        "missing"
    } else {
        match std.fs.ReadText(Path) {
            Ok(S) => S
            Err(F) => F.Message
        }
    }
}
`

const bunStdFilesystemTempModeProgram = `function main() -> string effects { filesystem } {
    match std.fs.CreateTemporaryDirectory("slickmode") {
        Ok(Dir) => Dir.Path
        Err(F) => "err:" + F.Message
    }
}
`

const bunStdFilesystemCancelledFIFOProgram = `class Stop implements Error {
    Message: string
}

function MarkFifo(Value: string) -> string effects { environment } {
    let _ = std.env.Set("SLICK_FIFO_RESULT", Value)
    Value
}

function ReadFifo(Path: string) -> string effects { filesystem, environment } {
    let _ = MarkFifo("started")
    match std.fs.ReadText(Path) {
        Ok(S) => MarkFifo("ok:" + S)
        Err(F) => MarkFifo(F.Message)
    }
}

function Fail() -> string throws Stop {
    for Item in 0..1000000 {
        let _ = Item
    }
    throw Stop { Message: "stop" }
}

function Drive(Path: string) -> string throws Stop effects { filesystem, environment } {
    async let Work = ReadFifo(Path)
    async let Killer = Fail()
    let _ = await Killer
    await Work
}

function Observed() -> string effects { environment } {
    let Got = std.env.Get("SLICK_FIFO_RESULT")
    if (Got == null) {
        "missing"
    } else {
        Got
    }
}

function main() -> string effects { environment, filesystem } {
    let Path = std.env.Get("SLICK_TEST_FIFO")
    if (Path == null) {
        "no-fifo"
    } else {
        Drive(Path) catch { Stop as _ => Observed() }
    }
}
`

const bunStdIOManyChunksProgram = `function main() -> string throws std.io.Failure effects { io, state } {
    using Writer = std.io.WriterToBytes() {
        let Want = ""
        for Item in 0..256 {
            Want = Want + "a"
            let _ = Writer.Write(std.bytes.FromUtf8("a"))
        }
        match std.bytes.ToUtf8(Writer.Bytes()) {
            Ok(Text) => if (Text == Want) { "ok" } else { "mismatch" }
            Err(F) => F.Message
        }
    }
}
`
