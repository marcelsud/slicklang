package compiler

import (
	"os/exec"
	"testing"
)

// rustStdPathProgram exercises every std.path operation against absolute
// paths, trailing separators, dot segments, empty inputs, and multi-element
// joins, including the null extension path. Results are flattened to strings
// so interpreter and Rust output compare byte for byte.
const rustStdPathProgram = `
function ExtOr(Path: string, Fallback: string) -> string {
    let Extension = std.path.Extension(Path)
    if (Extension == null) {
        Fallback
    } else {
        Extension
    }
}

function BoolText(Value: bool) -> string {
    if (Value) {
        "true"
    } else {
        "false"
    }
}

function main() -> string {
    let Cleans = std.text.Join([
        std.path.Clean(""),
        std.path.Clean("."),
        std.path.Clean(".."),
        std.path.Clean("/"),
        std.path.Clean("//"),
        std.path.Clean("a/b/c"),
        std.path.Clean("a//b"),
        std.path.Clean("a/./b"),
        std.path.Clean("a/../b"),
        std.path.Clean("/../a"),
        std.path.Clean("a/b/.."),
        std.path.Clean("./a"),
        std.path.Clean("/a/../b"),
        std.path.Clean("a/./b/."),
        std.path.Clean("//a//b//"),
        std.path.Clean("../.."),
        std.path.Clean("./../.."),
        std.path.Clean("/.."),
        std.path.Clean("/../.."),
        std.path.Clean("a/.."),
        std.path.Clean("/a/b/../../c"),
        std.path.Clean("./."),
        std.path.Clean("./"),
        std.path.Clean("/."),
        std.path.Clean("x/y//../.."),
        std.path.Clean("a/b/c/...")
    ], "|")
    let Bases = std.text.Join([
        std.path.Base(""),
        std.path.Base("."),
        std.path.Base(".."),
        std.path.Base("/"),
        std.path.Base("//"),
        std.path.Base("/foo/"),
        std.path.Base("/foo/bar"),
        std.path.Base("foo"),
        std.path.Base("foo/bar"),
        std.path.Base("./"),
        std.path.Base("/."),
        std.path.Base("x/"),
        std.path.Base("/x")
    ], "|")
    let Dirs = std.text.Join([
        std.path.Directory(""),
        std.path.Directory("."),
        std.path.Directory(".."),
        std.path.Directory("/"),
        std.path.Directory("//"),
        std.path.Directory("/foo/"),
        std.path.Directory("/foo/bar"),
        std.path.Directory("foo"),
        std.path.Directory("foo/bar"),
        std.path.Directory("./"),
        std.path.Directory("/."),
        std.path.Directory("a/b/c"),
        std.path.Directory("/a/b/c"),
        std.path.Directory("a"),
        std.path.Directory("/a"),
        std.path.Directory("a/.."),
        std.path.Directory("/.."),
        std.path.Directory("x/y")
    ], "|")
    let Exts = std.text.Join([
        ExtOr("", "null"),
        ExtOr("foo", "null"),
        ExtOr("foo.bar", "null"),
        ExtOr("foo.bar.baz", "null"),
        ExtOr(".bashrc", "null"),
        ExtOr("foo.", "null"),
        ExtOr("foo..", "null"),
        ExtOr("a/b.c", "null"),
        ExtOr("/tmp/", "null"),
        ExtOr("a.", "null")
    ], "|")
    let Absolutes = std.text.Join([
        BoolText(std.path.IsAbsolute("")),
        BoolText(std.path.IsAbsolute("/")),
        BoolText(std.path.IsAbsolute("/a")),
        BoolText(std.path.IsAbsolute("a")),
        BoolText(std.path.IsAbsolute("a/b")),
        BoolText(std.path.IsAbsolute("//a")),
        BoolText(std.path.IsAbsolute("./a")),
        BoolText(std.path.IsAbsolute("../a"))
    ], "|")
    let Joins = std.text.Join([
        std.path.Join(["a", "b", "c"]),
        std.path.Join(["", ""]),
        std.path.Join(["a", "", "b"]),
        std.path.Join(["/a", "b"]),
        std.path.Join(["a", "/b"]),
        std.path.Join(["a/b", "c/../d"]),
        std.path.Join(["a", "..", "b"]),
        std.path.Join(["", "a"]),
        std.path.Join(["a", "b/c", "..", "d"]),
        std.path.Join(["//a", "b"]),
        std.path.Join(["a", "./b"]),
        std.path.Join([])
    ], "|")
    let Sections = [Cleans, Bases, Dirs, Exts, Absolutes, Joins]
    std.text.Join(Sections, "##")
}
`

func TestRustStdPathMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdPathProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatalf("interpreter error: %v\ndiagnostics: %v", err, diagnostics)
	}
	const want = ".|.|..|/|/|a/b/c|a/b|a/b|b|/a|a|a|/b|a/b|/a/b|../..|../..|/|/|.|/c|.|.|/|.|a/b/c/..." +
		"##.|.|..|/|/|foo|bar|foo|bar|.|.|x|x" +
		"##.|.|.|/|/|/foo|/foo|.|foo|.|/|a/b|/a/b|.|/|a|/|x" +
		"##null|null|.bar|.baz|.bashrc|.|.|.c|null|." +
		"##false|true|true|false|false|true|false|false" +
		"##a/b/c||a/b|/a/b|a/b|a/b/d|b|a|a/b/d|/a/b|a/b|"
	if interpreted != want {
		t.Fatalf("interpreter std.path output=%q, want %q", interpreted, want)
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run Rust binary: %v: %s", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust std.path output=%q, want interpreter %q", string(output), interpreted+"\n")
	}
}
