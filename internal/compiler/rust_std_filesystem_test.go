package compiler

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRustStdFilesystemMatchesInterpreter exercises every std.env and std.fs
// operation the Rust backend owns against the interpreter. Filesystem work runs
// inside a fixed root the test publishes through SLICK_TEST_FS_ROOT so the
// interpreter and the compiled Rust binary address the identical path and the
// missing-file failure message normalizes to the same text in both. The
// temporary-directory path is random, so only deterministic facts about it
// (absolute, Close removes the tree, idempotent second Close, Close after the
// directory is already gone, literal Close throws, TMPDIR overlay) reach the
// compared output.
func TestRustStdFilesystemMatchesInterpreter(t *testing.T) {
	t.Setenv("SLICK_TEST_FS_ROOT", t.TempDir())
	source := Source{Name: "main.slk", Namespace: "root", Text: `
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
`}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatalf("interpreter run failed: %v", err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Rust binary failed: %v\noutput=%q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust output=%q\nwant %q", output, interpreted+"\n")
	}
	if strings.Contains(string(output), "panicked") {
		t.Fatalf("Rust panicked instead of producing a Slick result: %s", output)
	}

	hostTmp := t.TempDir()
	overlayTmp := t.TempDir()
	t.Setenv("TMPDIR", hostTmp)
	t.Setenv("SLICK_TEST_TMPDIR_OVERLAY", overlayTmp)
	overlaySource := Source{Name: "main.slk", Namespace: "root", Text: `
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


`}
	overlayBinary := buildRustTestProgram(t, overlaySource)
	overlayCmd := exec.Command(overlayBinary)
	overlayCmd.Env = append(os.Environ(), "TMPDIR="+hostTmp, "SLICK_TEST_TMPDIR_OVERLAY="+overlayTmp)
	overlayOutput, err := overlayCmd.CombinedOutput()
	if err != nil || string(overlayOutput) != "true\n" {
		t.Fatalf("TMPDIR overlay Rust output=%q error=%v, want true", overlayOutput, err)
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
