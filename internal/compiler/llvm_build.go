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

func buildLLVMBinary(program *program, output string) error {
	tool, err := locateLLVMToolchain()
	if err != nil {
		return err
	}
	ir, err := program.generateLLVM()
	if err != nil {
		return err
	}
	if dump := os.Getenv("SLICK_DUMP_LL"); dump != "" {
		_ = os.WriteFile(dump, []byte(ir), 0o644)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".slick-llvm-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	irPath := filepath.Join(temporary, "main.ll")
	if err := os.WriteFile(irPath, []byte(ir), 0o644); err != nil {
		return err
	}
	if err := tool.verifyIR(irPath); err != nil {
		return err
	}
	objPath := filepath.Join(temporary, "main.o")
	if err := tool.assembleIR(irPath, objPath); err != nil {
		return err
	}
	jsonCompile, jsonLink, err := llvmJSONFlags(program.usesStdJSON())
	if err != nil {
		return err
	}
	objects := []string{objPath}
	sources := []struct {
		name string
		data []byte
	}{
		{name: "runtime.c", data: llvmRuntimeSource},
		{name: "natives.c", data: llvmNativesSource},
	}
	for _, source := range sources {
		src := filepath.Join(temporary, source.name)
		if err := os.WriteFile(src, source.data, 0o644); err != nil {
			return fmt.Errorf("write embedded %s: %w", source.name, err)
		}
		obj := filepath.Join(temporary, source.name+".o")
		compile := []string{"-c", "-std=c11", "-O2", "-fPIC", "-o", obj, src}
		if program.usesStdSQLite {
			compile = append(compile, "-DSLICK_HAS_SQLITE")
		}
		if program.usesStdHTTP {
			compile = append(compile, "-DSLICK_HAS_CURL")
		}
		compile = append(compile, jsonCompile...)
		if out, err := runCC(tool.cc, compile...); err != nil {
			return fmt.Errorf("compile %s: %w: %s", source.name, err, strings.TrimSpace(out))
		}
		objects = append(objects, obj)
	}
	libs := []string{"-lpthread", "-lm"}
	if program.usesStdSQLite {
		if !sqliteDevPresent() {
			return fmt.Errorf("LLVM backend requires libsqlite3 (sqlite3.h and -lsqlite3)")
		}
		libs = append(libs, "-lsqlite3")
	}
	if program.usesStdHTTP {
		if !curlDevPresent() {
			return fmt.Errorf("LLVM backend requires libcurl development files (curl/curl.h and -lcurl)")
		}
		libs = append(libs, "-lcurl")
	}
	libs = append(libs, jsonLink...)
	linkedOutput := filepath.Join(temporary, "program")
	if err := tool.link(linkedOutput, objects, libs); err != nil {
		return err
	}
	if err := os.Rename(linkedOutput, output); err != nil {
		return fmt.Errorf("install LLVM output: %w", err)
	}
	return nil
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
