package compiler

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed llvmlib/runtime.c
var llvmRuntimeSource []byte

//go:embed llvmlib/natives.c
var llvmNativesSource []byte

func emitLLVMSource(program *program, workspace string) (backendEmission, error) {
	ir, err := program.generateLLVM()
	if err != nil {
		return backendEmission{}, err
	}
	if dump := os.Getenv("SLICK_DUMP_LL"); dump != "" {
		_ = os.WriteFile(dump, []byte(ir), 0o644)
	}
	irPath := filepath.Join(workspace, "main.ll")
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		return backendEmission{}, err
	}
	sources := []struct {
		name string
		data []byte
	}{
		{name: "runtime.c", data: llvmRuntimeSource},
		{name: "natives.c", data: llvmNativesSource},
	}
	for _, source := range sources {
		if err := os.WriteFile(filepath.Join(workspace, source.name), source.data, 0o644); err != nil {
			return backendEmission{}, fmt.Errorf("write embedded %s: %w", source.name, err)
		}
	}
	return backendEmission{primary: irPath}, nil
}

func buildLLVMEmission(tool llvmToolchain, runtime backendRuntimeInputs, emission backendEmission, output string) error {
	if err := tool.verifyIR(emission.primary); err != nil {
		return err
	}
	workspace := filepath.Dir(emission.primary)
	objPath := filepath.Join(workspace, "main.o")
	if err := tool.assembleIR(emission.primary, objPath); err != nil {
		return err
	}
	jsonCompile, jsonLink, err := llvmJSONFlags(runtime.usesJSON)
	if err != nil {
		return err
	}
	objects := []string{objPath}
	for _, name := range []string{"runtime.c", "natives.c"} {
		src := filepath.Join(workspace, name)
		obj := filepath.Join(workspace, name+".o")
		compile := []string{"-c", "-std=c11", "-O2", "-fPIC", "-ffunction-sections", "-fdata-sections", "-o", obj, src}
		if runtime.usesSQLite {
			compile = append(compile, "-DSLICK_HAS_SQLITE")
		}
		if runtime.usesHTTP {
			compile = append(compile, "-DSLICK_HAS_CURL")
		}
		compile = append(compile, jsonCompile...)
		if out, err := runCC(tool.cc, compile...); err != nil {
			return fmt.Errorf("compile %s: %w: %s", name, err, strings.TrimSpace(out))
		}
		objects = append(objects, obj)
	}
	libs := []string{"-Wl,--gc-sections", "-lpthread", "-lm"}
	if runtime.usesSQLite {
		libs = append(libs, "-lsqlite3")
	}
	if runtime.usesHTTP {
		libs = append(libs, "-lcurl")
	}
	libs = append(libs, jsonLink...)
	return tool.link(output, objects, libs)
}

func runCC(cc string, args ...string) (string, error) {
	out, err := exec.Command(cc, args...).CombinedOutput()
	return string(out), err
}

func sqliteDevPresent() bool {
	_, err := os.Stat("/usr/include/sqlite3.h")
	return err == nil
}

func curlDevPresent() bool {
	for _, path := range []string{"/usr/include/curl/curl.h", "/usr/include/x86_64-linux-gnu/curl/curl.h"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func llvmJSONFlags(used bool) ([]string, []string, error) {
	if !used {
		return nil, nil, nil
	}
	root := os.Getenv("SLICK_JANSSON_ROOT")
	include, library := "/usr/include", "/usr/lib/x86_64-linux-gnu"
	if root != "" {
		include = filepath.Join(root, "usr", "include")
		library = filepath.Join(root, "usr", "lib", "x86_64-linux-gnu")
	}
	if _, err := os.Stat(filepath.Join(include, "jansson.h")); err != nil {
		return nil, nil, fmt.Errorf("LLVM JSON support requires libjansson development files (jansson.h and -ljansson)")
	}
	compile := []string{"-DSLICK_HAS_JSON", "-I" + include}
	link := []string{"-L" + library, "-Wl,-rpath," + library, "-ljansson"}
	return compile, link, nil
}
