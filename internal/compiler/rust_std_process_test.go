package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rustStdProcessProgram exercises every observable path of std.process.Run:
// a successful run with captured stdout, a nonzero exit that is still Ok, a
// missing-program Spawn failure, a bad WorkingDirectory failure, and an
// OutputLimit failure from capture truncation. Report renders each Result into
// a deterministic string so the interpreter and the compiled Rust binary can
// be compared byte for byte, including the os/exec missing-program Message.
const rustStdProcessProgram = `
function Report(R: Result<std.process.Completed, std.process.Failure>) -> string {
    match R {
        Ok(Done) => ` + "`ok exit=${Done.ExitCode} out=${Done.Output} err=${Done.ErrorOutput}`" + `
        Err(Failure) => ` + "`err op=${Failure.Operation} prog=${Failure.Program} msg=${Failure.Message}`" + `
    }
}

function main() -> (string, string, string, string, string) effects { process } {
    let Echo = std.process.Run("echo", ["hello"], null, 65536)
    let Nonzero = std.process.Run("false", [], null, 65536)
    let Missing = std.process.Run("no-such-program-xyz", [], null, 65536)
    let BadDir = std.process.Run("echo", [], "/nonexistent-slick-dir", 65536)
    let Overflow = std.process.Run("echo", ["abcdefghij"], null, 4)
    let Summary = (Report(Echo), Report(Nonzero), Report(Missing), Report(BadDir), Report(Overflow))
    Summary
}
`

func TestRustStdProcessMatchesInterpreter(t *testing.T) {
	t.Run("Contract", func(t *testing.T) {
		assertRustStdProcessMatches(t, rustStdProcessProgram, "(ok exit=0 out=bytes[6] err=bytes[0], ok exit=1 out=bytes[0] err=bytes[0], err op=Spawn prog=no-such-program-xyz msg=exec: \"no-such-program-xyz\": executable file not found in $PATH, err op=WorkingDirectory prog=echo msg=working directory is not an existing directory, err op=OutputLimit prog=echo msg=captured output exceeds 4 bytes)")
	})

	t.Run("OverlayChild", func(t *testing.T) {
		assertRustStdProcessMatches(t, rustStdProcessOverlayProgram, "visible")
	})

	t.Run("NonExecutableBareName", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "slick-not-exec"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLICK_TEST_BARE_DIR", dir)
		assertRustStdProcessMatches(t, rustStdProcessBareNameProgram, `Spawn:exec: "slick-not-exec": executable file not found in $PATH`)
	})

	// Go os/exec refuses an executable found through a relative PATH entry, so a
	// current directory can never decide which program a Slick call starts.
	t.Run("RelativePATHIsRefused", func(t *testing.T) {
		root := t.TempDir()
		binDir := filepath.Join(root, "bin")
		workDir := filepath.Join(root, "work")
		decoyDir := filepath.Join(workDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(decoyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "slick-rel-probe"), []byte("#!/bin/sh\necho rel-ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(decoyDir, "slick-rel-probe"), []byte("#!/bin/sh\necho wrong\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		want := "Spawn:exec: \"slick-rel-probe\": cannot run executable found relative to current directory"
		source := Source{Name: "main.slk", Namespace: "root", Text: rustStdProcessRelativePathProgram}
		binary := buildRustTestProgram(t, source)
		t.Chdir(root)
		t.Setenv("SLICK_TEST_REL_PATH", "bin")
		t.Setenv("SLICK_TEST_REL_WORK", "work")
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoRustDiagnostics(t, diagnostics)
		if interpreted != want {
			t.Fatalf("interpreter output = %q, want %q", interpreted, want)
		}
		command := exec.Command(binary)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil || string(output) != interpreted+"\n" {
			t.Fatalf("Rust relative PATH output=%q error=%v, want %q", output, err, interpreted+"\n")
		}
	})
}

func assertRustStdProcessMatches(t *testing.T, text, want string) {
	t.Helper()
	source := Source{Name: "main.slk", Namespace: "root", Text: text}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil || string(output) != interpreted+"\n" {
		t.Fatalf("Rust process output=%q error=%v, want %q", output, err, interpreted+"\n")
	}
	if strings.Contains(string(output), "panicked") {
		t.Fatalf("Rust panic replaced a Slick process result: %s", output)
	}
}

const rustStdProcessOverlayProgram = `
function main() -> string effects { environment, process } {
    let _ = std.env.Set("SLICK_OVERLAY_CHILD", "visible")
    let Result = match std.process.Run("printenv", ["SLICK_OVERLAY_CHILD"], null, 65536) {
        Ok(Done) => match std.bytes.ToUtf8(Done.Output) {
            Ok(Text) => if (std.text.StartsWith(Text, "visible")) { "visible" } else { "got:" + Text }
            Err(Failure) => Failure.Message
        }
        Err(Failure) => Failure.Message
    }
    let _ = std.env.Unset("SLICK_OVERLAY_CHILD")
    Result
}
`

const rustStdProcessBareNameProgram = `
function RestorePath(Saved: string?) -> null effects { environment } {
    if (Saved == null) {
        let _ = std.env.Unset("PATH")
        null
    } else {
        let _ = std.env.Set("PATH", Saved)
        null
    }
}

function main() -> string effects { environment, process } {
    let Dir = std.env.Get("SLICK_TEST_BARE_DIR")
    if (Dir == null) {
        "missing-dir"
    } else {
        let Saved = std.env.Get("PATH")
        let _ = std.env.Set("PATH", Dir)
        let Result = std.process.Run("slick-not-exec", [], null, 65536)
        RestorePath(Saved)
        match Result {
            Ok(_) => "unexpected-ok"
            Err(Failure) => Failure.Operation + ":" + Failure.Message
        }
    }
}
`

const rustStdProcessRelativePathProgram = `
function RestorePath(Saved: string?) -> null effects { environment } {
    if (Saved == null) {
        let _ = std.env.Unset("PATH")
        null
    } else {
        let _ = std.env.Set("PATH", Saved)
        null
    }
}

function main() -> string effects { environment, process } {
    let Rel = std.env.Get("SLICK_TEST_REL_PATH")
    let Work = std.env.Get("SLICK_TEST_REL_WORK")
    if (Rel == null) {
        "missing-path"
    } else {
        if (Work == null) {
            "missing-work"
        } else {
            let Saved = std.env.Get("PATH")
            let _ = std.env.Set("PATH", Rel)
            let Result = std.process.Run("slick-rel-probe", [], Work, 65536)
            RestorePath(Saved)
            match Result {
                Ok(Done) => match std.bytes.ToUtf8(Done.Output) {
                    Ok(Text) => if (std.text.StartsWith(Text, "rel-ok")) { "rel-ok" } else { "got:" + Text }
                    Err(_) => "utf8"
                }
                Err(Failure) => Failure.Operation + ":" + Failure.Message
            }
        }
    }
}
`
