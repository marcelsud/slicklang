package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type llvmToolchain struct {
	assemble string
	compile  string
	cc       string
	version  string
}

func locateLLVMToolchain() (llvmToolchain, error) {
	bin := os.Getenv("SLICK_LLVM_BIN")
	search := []string{}
	if bin != "" {
		search = append(search, bin)
	}
	search = append(search, llvmSearchPaths()...)
	assemble := findTool(search, "llvm-as-18", "llvm-as")
	compile := findTool(search, "llc-18", "llc")
	cc := findTool(append([]string{"/usr/bin"}, search...), "cc", "clang-18", "clang", "gcc")
	if assemble == "" || compile == "" {
		return llvmToolchain{}, fmt.Errorf("LLVM %d toolchain not found: need llvm-as-%d and llc-%d (set SLICK_LLVM_BIN)", LLVMMajorVersion, LLVMMajorVersion, LLVMMajorVersion)
	}
	if cc == "" {
		return llvmToolchain{}, fmt.Errorf("C compiler not found: need cc to link LLVM objects")
	}
	version, err := llvmToolVersion(compile)
	if err != nil {
		return llvmToolchain{}, err
	}
	major, err := llvmMajor(version)
	if err != nil {
		return llvmToolchain{}, err
	}
	if major != LLVMMajorVersion {
		return llvmToolchain{}, fmt.Errorf("LLVM toolchain major version %d is required, found %s", LLVMMajorVersion, version)
	}
	return llvmToolchain{assemble: assemble, compile: compile, cc: cc, version: version}, nil
}

func llvmSearchPaths() []string {
	var paths []string
	if env := os.Getenv("PATH"); env != "" {
		paths = append(paths, filepath.SplitList(env)...)
	}
	paths = append(paths,
		"/usr/lib/llvm-18/bin",
		"/usr/bin",
	)
	dir, err := os.Getwd()
	if err == nil {
		for i := 0; i < 6 && dir != string(filepath.Separator); i++ {
			paths = append(paths, filepath.Join(dir, ".tools", "llvm", "usr", "bin"))
			paths = append(paths, filepath.Join(dir, ".tools", "llvm", "usr", "lib", "llvm-18", "bin"))
			dir = filepath.Dir(dir)
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		paths = append(paths, filepath.Join(root, ".tools", "llvm", "usr", "bin"))
		paths = append(paths, filepath.Join(root, ".tools", "llvm", "usr", "lib", "llvm-18", "bin"))
	}
	return paths
}

func findTool(dirs []string, names ...string) string {
	for _, dir := range dirs {
		for _, name := range names {
			candidate := name
			if dir != "" && !strings.ContainsRune(name, os.PathSeparator) {
				candidate = filepath.Join(dir, name)
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate
			}
			if resolved, err := exec.LookPath(name); err == nil {
				return resolved
			}
		}
	}
	return ""
}

func llvmToolVersion(tool string) (string, error) {
	out, err := exec.Command(tool, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read LLVM version from %s: %w: %s", tool, err, strings.TrimSpace(string(out)))
	}
	text := string(out)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "LLVM version") || strings.Contains(line, "Ubuntu LLVM version") {
			return line, nil
		}
	}
	return strings.TrimSpace(text), nil
}

func llvmMajor(versionLine string) (int, error) {
	fields := strings.Fields(versionLine)
	for i, field := range fields {
		if field == "version" && i+1 < len(fields) {
			major := fields[i+1]
			if dot := strings.IndexByte(major, '.'); dot >= 0 {
				major = major[:dot]
			}
			n, err := strconv.Atoi(major)
			if err != nil {
				return 0, fmt.Errorf("parse LLVM version %q: %w", versionLine, err)
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("parse LLVM version %q", versionLine)
}

func (tool llvmToolchain) verifyIR(path string) error {
	out, err := exec.Command(tool.assemble, "-o", os.DevNull, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("llvm-as verify: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tool llvmToolchain) assembleIR(irPath, objPath string) error {
	out, err := exec.Command(tool.compile, "-filetype=obj", "-relocation-model=pic", "-o", objPath, irPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("llc: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tool llvmToolchain) link(output string, objects []string, libs []string) error {
	args := []string{"-o", output}
	args = append(args, objects...)
	args = append(args, libs...)
	command := exec.Command(tool.cc, args...)
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("link LLVM binary: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
