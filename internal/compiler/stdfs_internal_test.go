package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func generateStdFSProgram(t *testing.T, text string) string {
	t.Helper()
	program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: text}})
	requireNoDiagnostics(t, diagnostics)
	generated, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate program: %v", err)
	}
	return generated
}

func TestStdFSDirectoryEmissionIsConditional(t *testing.T) {
	traversal := []string{
		"slickFSTemporary",
		"os.ReadDir",
		goClassName(stdFSEntryName),
		goClassName(stdFSTemporaryDirectoryName),
	}

	plain := generateStdFSProgram(t, `function main() -> string { "plain" }`)
	for _, fragment := range traversal {
		if strings.Contains(plain, fragment) {
			t.Fatalf("plain generated program contains %q", fragment)
		}
	}

	// The whole-file operations from issue #5 stay unconditional and must not
	// drag the traversal or temporary-workspace helpers in with them.
	wholeFile := generateStdFSProgram(t, `function main() -> Result<bool, std.fs.Failure> effects { filesystem } { std.fs.Exists(".") }`)
	for _, fragment := range traversal {
		if strings.Contains(wholeFile, fragment) {
			t.Fatalf("std.fs whole-file program contains %q", fragment)
		}
	}
	if !strings.Contains(wholeFile, "os.Stat") {
		t.Fatal("std.fs whole-file program lost its Exists implementation")
	}

	listing := generateStdFSProgram(t, `
function main() -> string effects { filesystem } {
    match std.fs.ReadDirectory(".") {
        Ok(Entries) => "listed"
        Err(Failure) => Failure.Message
    }
}
`)
	for _, fragment := range traversal {
		if !strings.Contains(listing, fragment) {
			t.Fatalf("std.fs.ReadDirectory program missing %q", fragment)
		}
	}

	// Naming the gated types without ever calling the gated functions still has
	// to emit them, or the generated Go references an undeclared type.
	typed := generateStdFSProgram(t, `
class Holder { Item: std.fs.Entry }
class Boxed { Items: std.fs.Entry[]? }
interface Lister { function Latest() -> std.fs.Entry }
function main() -> string { "typed" }
`)
	for _, fragment := range traversal {
		if !strings.Contains(typed, fragment) {
			t.Fatalf("std.fs.Entry type program missing %q", fragment)
		}
	}
}

func TestCreateTemporaryDirectoryUsesPrefixUnderTheTemporaryRoot(t *testing.T) {
	first, err := createTemporaryDirectory("slick-prefix-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	defer os.RemoveAll(first)
	second, err := createTemporaryDirectory("slick-prefix-")
	if err != nil {
		t.Fatalf("create second temporary directory: %v", err)
	}
	defer os.RemoveAll(second)

	if first == second {
		t.Fatalf("temporary directories collide at %q", first)
	}
	for _, path := range []string{first, second} {
		if !filepath.IsAbs(path) {
			t.Fatalf("temporary directory %q is not absolute", path)
		}
		if !strings.HasPrefix(filepath.Base(path), "slick-prefix-") {
			t.Fatalf("temporary directory %q does not use the requested prefix", path)
		}
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("temporary directory %q is not a directory: %v", path, statErr)
		}
	}

	// A star in the prefix stays literal, because the appended star is the one
	// that names the generated suffix.
	starred, err := createTemporaryDirectory("slick-*-star-")
	if err != nil {
		t.Fatalf("create starred temporary directory: %v", err)
	}
	defer os.RemoveAll(starred)
	if !strings.HasPrefix(filepath.Base(starred), "slick-*-star-") {
		t.Fatalf("starred prefix was expanded into %q", filepath.Base(starred))
	}

	for _, prefix := range []string{"../escape-", "nested/prefix-"} {
		if path, err := createTemporaryDirectory(prefix); err == nil {
			os.RemoveAll(path)
			t.Fatalf("prefix %q selected a parent directory at %q", prefix, path)
		}
	}
}

func TestUnsafeCleanupTargetRejectsEmptyAndRootEquivalentPaths(t *testing.T) {
	rejected := []string{"", ".", "./", string(filepath.Separator), filepath.VolumeName(os.TempDir()) + string(filepath.Separator)}
	for _, path := range rejected {
		if !unsafeCleanupTarget(path) {
			t.Fatalf("cleanup target %q was accepted", path)
		}
	}
	if unsafeCleanupTarget(filepath.Join(os.TempDir(), "slick-workspace")) {
		t.Fatal("an ordinary temporary workspace was rejected")
	}
}

func TestNativeTemporaryDirectoryCloseRemovesOnlyItsOwnTreeOnce(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "owned")
	sibling := filepath.Join(root, "sibling")
	for _, directory := range []string{owned, sibling} {
		if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
		if err := os.WriteFile(filepath.Join(directory, "nested", "child.txt"), []byte("child"), 0o644); err != nil {
			t.Fatalf("write child of %s: %v", directory, err)
		}
	}

	resource := &nativeTemporaryDirectory{path: owned}
	if err := resource.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("close left the owned tree behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sibling, "nested", "child.txt")); err != nil {
		t.Fatalf("close removed a sibling tree: %v", err)
	}
	if err := resource.close(); err != nil {
		t.Fatalf("second close reported %v, want a silent no-op", err)
	}

	var unowned *nativeTemporaryDirectory
	if err := unowned.close(); err == nil {
		t.Fatal("closing an unowned temporary directory succeeded")
	}
	dangerous := &nativeTemporaryDirectory{path: string(filepath.Separator)}
	if err := dangerous.close(); err == nil {
		t.Fatal("closing a root-equivalent target succeeded")
	}
}

func TestWholeFileCallsReturnTypedCancellationForNamedPipes(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(context.Context, string) error
	}{
		{name: "read", call: func(ctx context.Context, path string) error {
			_, err := readTextFileContext(ctx, path)
			return err
		}},
		{name: "write", call: func(ctx context.Context, path string) error {
			return writeTextFileContext(ctx, path, "contents")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "blocked.fifo")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatalf("create FIFO: %v", err)
			}
			peer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatalf("open FIFO peer: %v", err)
			}
			defer peer.Close()
			if test.name == "write" {
				chunk := make([]byte, 4096)
				for {
					if _, err := syscall.Write(int(peer.Fd()), chunk); err != nil {
						if err != syscall.EAGAIN {
							t.Fatalf("fill FIFO: %v", err)
						}
						break
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- test.call(ctx, path) }()
			time.Sleep(20 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if err != errFilesystemCancelled {
					t.Fatalf("cancelled %s = %v, want %v", test.name, err, errFilesystemCancelled)
				}
			case <-time.After(time.Second):
				t.Fatalf("cancelled %s remained blocked", test.name)
			}
		})
	}
}
