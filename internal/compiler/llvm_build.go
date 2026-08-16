package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

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
	temporary, err := os.MkdirTemp("", "slick-llvm-*")
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
	dir := llvmRuntimeDir()
	usesJSON := strings.Contains(ir, "@slick_nat_json_decode") || strings.Contains(ir, "@slick_nat_json_encode")
	jsonCompile, jsonLink, err := llvmJSONFlags(usesJSON)
	if err != nil {
		return err
	}
	objects := []string{objPath}
	for _, name := range []string{"runtime.c", "natives.c"} {
		src := filepath.Join(dir, name)
		obj := filepath.Join(temporary, name+".o")
		compile := []string{"-c", "-std=c11", "-O2", "-fPIC", "-o", obj, src}
		if program.usesStdSQLite {
			compile = append(compile, "-DSLICK_HAS_SQLITE")
		}
		if program.usesStdHTTP {
			compile = append(compile, "-DSLICK_HAS_CURL")
		}
		compile = append(compile, jsonCompile...)
		if out, err := runCC(tool.cc, compile...); err != nil {
			return fmt.Errorf("compile %s: %w: %s", name, err, strings.TrimSpace(out))
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
	if err := tool.link(output, objects, libs); err != nil {
		_ = os.Remove(output)
		return err
	}
	return nil
}

func llvmRuntimeDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "llvmlib"
	}
	return filepath.Join(filepath.Dir(file), "llvmlib")
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
