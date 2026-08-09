package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// nativeTemporaryDirectory owns exactly one created directory. Close removes
// that directory and nothing else, so a TemporaryDirectory built from an
// object literal carries no resource and can never delete a host path.
type nativeTemporaryDirectory struct {
	path   string
	closed bool
}

// createTemporaryDirectory places a unique directory under the platform
// temporary root. The trailing "*" keeps Prefix a literal base-name prefix
// even when it already contains one, and os.MkdirTemp rejects any pattern
// carrying a path separator, so Prefix can never select the parent.
func createTemporaryDirectory(prefix string) (string, error) {
	created, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(created)
	if err != nil {
		os.RemoveAll(created)
		return "", err
	}
	return absolute, nil
}

// unsafeCleanupTarget rejects an empty or root-equivalent removal target so a
// misconfigured host adapter cannot turn Close into a recursive host wipe.
func unsafeCleanupTarget(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned == "." || filepath.Dir(cleaned) == cleaned
}

// close removes the owned tree. A second close after a successful one does
// nothing; a failed close leaves the resource open so it can be retried.
func (resource *nativeTemporaryDirectory) close() error {
	if resource == nil {
		return fmt.Errorf("temporary directory is not owned by this resource")
	}
	if resource.closed {
		return nil
	}
	if unsafeCleanupTarget(resource.path) {
		return fmt.Errorf("refusing to remove unsafe cleanup target")
	}
	if err := os.RemoveAll(filepath.Clean(resource.path)); err != nil {
		return err
	}
	resource.closed = true
	return nil
}

// usesStdFSDirectoryName reports whether a canonical type name pulls in the
// directory-traversal and temporary-workspace declarations. Only these four
// declarations are gated; the whole-file operations stay unconditional.
func usesStdFSDirectoryName(name string) bool {
	return strings.Contains(name, stdFSEntryName) || strings.Contains(name, stdFSTemporaryDirectoryName)
}

// skipStdFSDirectory hides the gated declarations from a program that never
// names them, so no traversal or temporary-resource helper is generated.
func (g *goGenerator) skipStdFSDirectory(name string) bool {
	if g.program.usesStdFSDirectory {
		return false
	}
	switch name {
	case stdFSEntryName, stdFSTemporaryDirectoryName,
		string(nativeStdFSReadDirectory), string(nativeStdFSCreateTemporaryDirectory):
		return true
	}
	return false
}

func (p *program) callNativeStdFS(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdFSReadDirectory:
		path := frame.locals["Path"].scalar.(string)
		// os.ReadDir sorts by file name, so the order never depends on host
		// enumeration order, and a failing entry surfaces as an error instead
		// of a silently skipped child.
		entries, err := os.ReadDir(path)
		if err != nil {
			return runtimeFSFailure(resultType, "ReadDirectory", path, err), nil, true
		}
		values := make([]runtimeValue, len(entries))
		for index, entry := range entries {
			values[index] = runtimeValue{
				typ: stdFSEntryName,
				fields: map[string]runtimeValue{
					"Name":        {typ: "string", scalar: entry.Name()},
					"Path":        {typ: "string", scalar: filepath.Join(path, entry.Name())},
					"IsDirectory": {typ: "bool", scalar: entry.IsDir()},
				},
			}
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: stdFSEntryName + "[]", elements: values}), nil, true
	case nativeStdFSCreateTemporaryDirectory:
		prefix := frame.locals["Prefix"].scalar.(string)
		path, err := createTemporaryDirectory(prefix)
		if err != nil {
			return runtimeFSFailure(resultType, "CreateTemporaryDirectory", prefix, err), nil, true
		}
		value := runtimeValue{
			typ:       stdFSTemporaryDirectoryName,
			fields:    map[string]runtimeValue{"Path": {typ: "string", scalar: path}},
			directory: &nativeTemporaryDirectory{path: path},
		}
		return runtimeResultValue(resultType, true, value), nil, true
	case nativeStdFSTemporaryDirectoryClose:
		self := frame.locals["self"]
		if err := self.directory.close(); err != nil {
			path := ""
			if value, ok := self.fields["Path"]; ok {
				path, _ = value.scalar.(string)
			}
			failure := runtimeFSFailureValue("Close", path, err.Error())
			return runtimeValue{}, &slickThrow{typ: stdFSFailureName, message: err.Error(), value: failure}, true
		}
		return nullRuntimeValue(), nil, true
	default:
		return runtimeValue{}, nil, false
	}
}

// emitNativeStdFSFunction emits the two gated std.fs functions. The caller
// dispatches on the same natives, so no other case can reach this switch.
func (g *goGenerator) emitNativeStdFSFunction(function *functionDecl, resultType string, arguments []string) {
	switch function.native {
	case nativeStdFSReadDirectory:
		entry := goClassName(stdFSEntryName)
		g.line("entries, err := os.ReadDir(%s)", arguments[0])
		g.line("if err != nil {")
		g.emitNativeFSFailure(resultType, "ReadDirectory", arguments[0], "err")
		g.line("}")
		g.line("values := make([]%s, len(entries))", entry)
		g.line("for index, entry := range entries {")
		g.line("values[index] = %s{%s: entry.Name(), %s: filepath.Join(%s, entry.Name()), %s: entry.IsDir()}",
			entry, goFieldName("Name"), goFieldName("Path"), arguments[0], goFieldName("IsDirectory"))
		g.line("}")
		g.line("return %s{ok: true, value: values}, nil", g.goType(resultType))
	case nativeStdFSCreateTemporaryDirectory:
		g.line("path, err := slickFSCreateTemporary(%s)", arguments[0])
		g.line("if err != nil {")
		g.emitNativeFSFailure(resultType, "CreateTemporaryDirectory", arguments[0], "err")
		g.line("}")
		g.line("return %s{ok: true, value: %s{%s: path, slickResource: &slickFSTemporary{path: path}}}, nil",
			g.goType(resultType), goClassName(stdFSTemporaryDirectoryName), goFieldName("Path"))
	}
}

// emitStdFSRuntime emits the temporary-workspace helpers. It runs only for
// programs that name std.fs.TemporaryDirectory or std.fs.ReadDirectory.
func (g *goGenerator) emitStdFSRuntime() {
	failure := goClassName(stdFSFailureName)
	g.line(`type slickFSTemporary struct {`)
	g.line(`path string`)
	g.line(`closed bool`)
	g.line(`}`)
	g.line(`func slickFSCreateTemporary(prefix string) (string, error) {`)
	g.line(`created, err := os.MkdirTemp("", prefix + "*")`)
	g.line(`if err != nil { return "", err }`)
	g.line(`absolute, err := filepath.Abs(created)`)
	g.line(`if err != nil { os.RemoveAll(created); return "", err }`)
	g.line(`return absolute, nil`)
	g.line(`}`)
	g.line("func slickFSClose(resource *slickFSTemporary, path string) error {")
	g.line("fail := func(message string) error {")
	g.line("return &%s{%s: %q, %s: path, %s: message}", failure, goFieldName("Operation"), "Close", goFieldName("Path"), goFieldName("Message"))
	g.line("}")
	g.line(`if resource == nil { return fail(%q) }`, "temporary directory is not owned by this resource")
	g.line(`if resource.closed { return nil }`)
	g.line(`cleaned := filepath.Clean(resource.path)`)
	g.line(`if cleaned == "." || filepath.Dir(cleaned) == cleaned { return fail(%q) }`, "refusing to remove unsafe cleanup target")
	g.line(`if err := os.RemoveAll(cleaned); err != nil { return fail(err.Error()) }`)
	g.line(`resource.closed = true`)
	g.line(`return nil`)
	g.line(`}`)
	g.line("")
}
