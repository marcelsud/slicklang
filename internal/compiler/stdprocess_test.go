package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"slick/internal/compiler"
)

// processHelperSentinel marks a test-binary invocation as the deterministic
// child process the std.process tests execute. Directives follow it as ordinary
// arguments, so the helper needs no environment, shell, or network.
const processHelperSentinel = "slick-process-helper"

// TestProcessHelperProgram is that child process. Every directive is applied in
// order and the helper always exits without returning, so the testing framework
// never adds its own output to the bytes under assertion.
func TestProcessHelperProgram(t *testing.T) {
	directives, isHelper := processHelperDirectives()
	if !isHelper {
		t.Skip("runs only as the child process spawned by the std.process tests")
	}
	for _, directive := range directives {
		switch {
		case strings.HasPrefix(directive, "out="):
			os.Stdout.WriteString(strings.TrimPrefix(directive, "out="))
		case strings.HasPrefix(directive, "err="):
			os.Stderr.WriteString(strings.TrimPrefix(directive, "err="))
		case strings.HasPrefix(directive, "exit="):
			code, err := strconv.Atoi(strings.TrimPrefix(directive, "exit="))
			if err != nil {
				os.Exit(120)
			}
			os.Exit(code)
		case directive == "cwd":
			directory, err := os.Getwd()
			if err != nil {
				os.Exit(121)
			}
			os.Stdout.WriteString(directory)
		case directive == "block":
			// Sleeps rather than parking forever: a parked goroutine trips Go's
			// deadlock detector and the child would exit on its own, which would
			// hide a caller that never terminates and reaps it.
			time.Sleep(time.Hour)
		default:
			os.Exit(122)
		}
	}
	os.Exit(0)
}

func processHelperDirectives() ([]string, bool) {
	for index, argument := range os.Args {
		if argument == processHelperSentinel {
			return os.Args[index+1:], true
		}
	}
	return nil, false
}

func helperProgram(t *testing.T) string {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	return program
}

func helperArguments(directives ...string) []string {
	return append([]string{"-test.run=^TestProcessHelperProgram$", processHelperSentinel}, directives...)
}

// processDriverSource is a complete Slick command-line tool: it takes its
// arguments from the process, runs a child program with a bounded capture, and
// reports the child's exact bytes and exit code as its own status.
const processDriverSource = `
function Fail(Message: string) -> std.process.Status {
    std.process.Status {
        ExitCode: 9
        Output: std.bytes.FromUtf8("")
        ErrorOutput: std.bytes.FromUtf8(Message)
    }
}

function Execute(Program: string, Limit: int, Directory: string?, Arguments: string[]) -> std.process.Status {
    match std.process.Run(Program, Arguments, Directory, Limit) {
        Ok(Done) => std.process.Status {
            ExitCode: Done.ExitCode
            Output: Done.Output
            ErrorOutput: Done.ErrorOutput
        }
        Err(Failure) => Fail(Failure.Operation + "|" + Failure.Program + "|" + Failure.Message)
    }
}

function WithDirectory(Program: string, Limit: int, Directory: string, Arguments: string[]) -> std.process.Status {
    if (Directory == "") {
        Execute(Program, Limit, null, Arguments)
    } else {
        Execute(Program, Limit, Directory, Arguments)
    }
}

function WithLimit(Program: string, LimitText: string, Directory: string, Arguments: string[]) -> std.process.Status {
    match std.convert.ParseInt(LimitText) {
        Ok(Limit) => WithDirectory(Program, Limit, Directory, Arguments)
        Err(Failure) => Fail(Failure.Message)
    }
}

function WithArguments(Program: string, LimitText: string, Directory: string, Arguments: string[]) -> std.process.Status {
    match Arguments.Slice(3, Arguments.Length()) {
        Ok(Rest) => WithLimit(Program, LimitText, Directory, Rest)
        Err(Failure) => Fail("usage")
    }
}

function WithDirectoryArgument(Program: string, LimitText: string, Arguments: string[]) -> std.process.Status {
    let Directory = Arguments.Get(2)
    if (Directory == null) {
        Fail("usage")
    } else {
        WithArguments(Program, LimitText, Directory, Arguments)
    }
}

function WithLimitArgument(Program: string, Arguments: string[]) -> std.process.Status {
    let LimitText = Arguments.Get(1)
    if (LimitText == null) {
        Fail("usage")
    } else {
        WithDirectoryArgument(Program, LimitText, Arguments)
    }
}

function main(Arguments: string[]) -> std.process.Status {
    let Program = Arguments.Get(0)
    if (Program == null) {
        Fail("usage")
    } else {
        WithLimitArgument(Program, Arguments)
    }
}
`

// processCLISource reports its own argument vector so the exact arguments, the
// exact bytes, and the exit code a Slick CLI produces are all observable.
const processCLISource = `
function Fail(Message: string) -> std.process.Status {
    std.process.Status {
        ExitCode: 9
        Output: std.bytes.FromUtf8("")
        ErrorOutput: std.bytes.FromUtf8(Message)
    }
}

function Report(Code: int, Arguments: string[]) -> std.process.Status {
    std.process.Status {
        ExitCode: Code
        Output: std.bytes.FromUtf8(std.text.Join(Arguments, "|"))
        ErrorOutput: std.bytes.FromUtf8("count=" + std.convert.IntToString(Arguments.Length()))
    }
}

function main(Arguments: string[]) -> std.process.Status {
    let CodeText = Arguments.Get(0)
    if (CodeText == null) {
        Fail("usage")
    } else {
        match std.convert.ParseInt(CodeText) {
            Ok(Code) => Report(Code, Arguments)
            Err(Failure) => Fail(Failure.Message)
        }
    }
}
`

type programResult struct {
	exitCode    int
	output      string
	errorOutput string
	failure     error
}

// interpretProgram runs source through the interpreter exactly as `slick run`
// does, including reporting an out-of-range status as a runtime failure after
// the produced bytes are available.
func interpretProgram(t *testing.T, source string, arguments []string) programResult {
	t.Helper()
	outcome, diagnostics, err := compiler.RunArguments(
		[]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}},
		arguments,
	)
	assertNoDiagnostics(t, diagnostics)
	if outcome.Status == nil {
		if err != nil {
			return programResult{failure: err}
		}
		t.Fatalf("program produced no status: %+v", outcome)
	}
	return programResult{
		exitCode:    outcome.Status.ExitCode,
		output:      string(outcome.Status.Output),
		errorOutput: string(outcome.Status.ErrorOutput),
		failure:     err,
	}
}

func buildProgram(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(root, binary)
	if err != nil {
		t.Fatalf("build Slick binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	return binary
}

func executeProgram(t *testing.T, binary string, arguments []string) programResult {
	t.Helper()
	var output, errorOutput strings.Builder
	command := exec.Command(binary, arguments...)
	command.Stdout = &output
	command.Stderr = &errorOutput
	err := command.Run()
	var exitError *exec.ExitError
	if err != nil && !asExitError(err, &exitError) {
		t.Fatalf("run Slick binary: %v", err)
	}
	return programResult{
		exitCode:    command.ProcessState.ExitCode(),
		output:      output.String(),
		errorOutput: errorOutput.String(),
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	exitError, ok := err.(*exec.ExitError)
	if ok {
		*target = exitError
	}
	return ok
}

type processCase struct {
	name       string
	program    string
	limit      string
	directory  string
	directives []string
	// exitCode, output, and errorOutput describe a child that ran; failure
	// describes the "Operation|Program|" prefix of a typed std.process.Failure.
	exitCode    int
	output      string
	errorOutput string
	failure     string
}

func processCases(t *testing.T) []processCase {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	explicitDirectory := resolvedDirectory(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "absent-program")
	return []processCase{
		{
			name:       "shell metacharacters stay ordinary arguments",
			directives: []string{"out=a b;$HOME|&x*?(){}[]<>"},
			output:     "a b;$HOME|&x*?(){}[]<>",
		},
		{
			name:        "both streams and a nonzero exit are captured",
			directives:  []string{"out=OUT", "err=ERR", "exit=2"},
			exitCode:    2,
			output:      "OUT",
			errorOutput: "ERR",
		},
		{
			name:       "exit code one is an ordinary result",
			directives: []string{"exit=1"},
			exitCode:   1,
		},
		{
			name:       "exit code 255 is an ordinary result",
			directives: []string{"exit=255"},
			exitCode:   255,
		},
		{
			name:       "empty output is not a failure",
			directives: []string{},
		},
		{
			name:       "absent working directory runs in the current directory",
			directives: []string{"cwd"},
			output:     resolvedDirectory(t, workingDirectory),
		},
		{
			name:       "explicit working directory is used",
			directory:  explicitDirectory,
			directives: []string{"cwd"},
			output:     explicitDirectory,
		},
		{
			name:       "output exactly at the limit succeeds",
			limit:      "5",
			directives: []string{"out=12345"},
			output:     "12345",
		},
		{
			name:       "output beyond the limit fails",
			limit:      "5",
			directives: []string{"out=123456"},
			failure:    "OutputLimit",
		},
		{
			name:       "the limit covers both streams together",
			limit:      "5",
			directives: []string{"out=123", "err=456"},
			failure:    "OutputLimit",
		},
		{
			name:       "a zero limit rejects any output",
			limit:      "0",
			directives: []string{"out=1"},
			failure:    "OutputLimit",
		},
		{
			name:       "a negative limit is rejected",
			limit:      "-1",
			directives: []string{"out=x"},
			failure:    "OutputLimit",
		},
		{
			name:       "an overflowing child is terminated and reaped",
			limit:      "2",
			directives: []string{"out=overflowing", "block"},
			failure:    "OutputLimit",
		},
		{
			name:       "a missing executable fails to spawn",
			program:    missing,
			directives: []string{"out=unreachable"},
			failure:    "Spawn",
		},
		{
			name:       "an invalid working directory fails",
			directory:  filepath.Join(missing, "nested"),
			directives: []string{"out=unreachable"},
			failure:    "WorkingDirectory",
		},
	}
}

func resolvedDirectory(t *testing.T, directory string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve %s: %v", directory, err)
	}
	return resolved
}

func (testCase processCase) arguments(t *testing.T) []string {
	t.Helper()
	program := testCase.program
	if program == "" {
		program = helperProgram(t)
	}
	limit := testCase.limit
	if limit == "" {
		limit = "4096"
	}
	arguments := []string{program, limit, testCase.directory}
	return append(arguments, helperArguments(testCase.directives...)...)
}

func (testCase processCase) assert(t *testing.T, result programResult) {
	t.Helper()
	if result.failure != nil {
		t.Fatalf("unexpected runtime failure: %v", result.failure)
	}
	if testCase.failure != "" {
		program := testCase.program
		if program == "" {
			program = helperProgram(t)
		}
		prefix := testCase.failure + "|" + program + "|"
		if result.exitCode != 9 {
			t.Fatalf("expected the failure path, found exit %d with %q", result.exitCode, result.errorOutput)
		}
		if !strings.HasPrefix(result.errorOutput, prefix) {
			t.Fatalf("expected failure %q, found %q", prefix, result.errorOutput)
		}
		if strings.TrimPrefix(result.errorOutput, prefix) == "" {
			t.Fatalf("failure %q carries no message", result.errorOutput)
		}
		if result.output != "" {
			t.Fatalf("failure produced output %q", result.output)
		}
		return
	}
	if result.exitCode != testCase.exitCode {
		t.Fatalf("expected exit %d, found %d (stderr %q)", testCase.exitCode, result.exitCode, result.errorOutput)
	}
	if result.output != testCase.output {
		t.Fatalf("expected stdout %q, found %q", testCase.output, result.output)
	}
	if result.errorOutput != testCase.errorOutput {
		t.Fatalf("expected stderr %q, found %q", testCase.errorOutput, result.errorOutput)
	}
}

func TestInterpretedProcessRunCoversChildContracts(t *testing.T) {
	for _, testCase := range processCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.assert(t, interpretProgram(t, processDriverSource, testCase.arguments(t)))
		})
	}
}

func TestNativeProcessRunMatchesTheInterpreter(t *testing.T) {
	binary := buildProgram(t, processDriverSource)
	for _, testCase := range processCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.assert(t, executeProgram(t, binary, testCase.arguments(t)))
		})
	}
}

// cliArguments exercises the argument vector itself: no arguments beyond the
// exit code, empty strings, Unicode, and shell-looking text.
var cliArguments = map[string][]string{
	"single argument":    {"0"},
	"empty and unicode":  {"7", "", "héllo 🌍", "a;b"},
	"repeated arguments": {"1", "same", "same"},
}

func TestInterpretedCLIStatusReportsArgumentsBytesAndExitCode(t *testing.T) {
	for name, arguments := range cliArguments {
		t.Run(name, func(t *testing.T) {
			assertCLIResult(t, arguments, interpretProgram(t, processCLISource, arguments))
		})
	}
}

func TestNativeCLIStatusMatchesTheInterpreter(t *testing.T) {
	binary := buildProgram(t, processCLISource)
	for name, arguments := range cliArguments {
		t.Run(name, func(t *testing.T) {
			assertCLIResult(t, arguments, executeProgram(t, binary, arguments))
		})
	}
}

func assertCLIResult(t *testing.T, arguments []string, result programResult) {
	t.Helper()
	if result.failure != nil {
		t.Fatalf("unexpected runtime failure: %v", result.failure)
	}
	code, err := strconv.Atoi(arguments[0])
	if err != nil {
		t.Fatalf("test exit code %q: %v", arguments[0], err)
	}
	if result.exitCode != code {
		t.Fatalf("expected exit %d, found %d", code, result.exitCode)
	}
	if result.output != strings.Join(arguments, "|") {
		t.Fatalf("expected stdout %q, found %q", strings.Join(arguments, "|"), result.output)
	}
	if result.errorOutput != "count="+strconv.Itoa(len(arguments)) {
		t.Fatalf("unexpected stderr %q", result.errorOutput)
	}
}

// TestEmptyCLIArgumentsReachMain covers the empty argument vector: a CLI main
// still runs, sees no arguments, and reports its own status.
func TestEmptyCLIArgumentsReachMain(t *testing.T) {
	interpreted := interpretProgram(t, processCLISource, nil)
	assertEmptyCLIResult(t, interpreted)
	assertEmptyCLIResult(t, executeProgram(t, buildProgram(t, processCLISource), nil))
}

func assertEmptyCLIResult(t *testing.T, result programResult) {
	t.Helper()
	if result.failure != nil {
		t.Fatalf("unexpected runtime failure: %v", result.failure)
	}
	if result.exitCode != 9 {
		t.Fatalf("expected the usage exit, found %d", result.exitCode)
	}
	if result.output != "" {
		t.Fatalf("expected no output, found %q", result.output)
	}
	if result.errorOutput != "usage" {
		t.Fatalf("expected the usage message, found %q", result.errorOutput)
	}
}

// TestExitCodeOutsideTheValidRangeFailsAfterWritingOutput pins both halves of
// the contract: the bytes a Status names are still written exactly, and the
// out-of-range code is then a deterministic runtime failure rather than a
// truncated or wrapped exit status.
func TestExitCodeOutsideTheValidRangeFailsAfterWritingOutput(t *testing.T) {
	for _, code := range []string{"256", "-1"} {
		t.Run(code, func(t *testing.T) {
			arguments := []string{code, "tail"}
			interpreted := interpretProgram(t, processCLISource, arguments)
			if interpreted.failure == nil {
				t.Fatalf("interpreter accepted exit code %s", code)
			}
			if !strings.Contains(interpreted.failure.Error(), "ExitCode must be 0 through 255") {
				t.Fatalf("unexpected interpreter failure %v", interpreted.failure)
			}
			if interpreted.output != strings.Join(arguments, "|") {
				t.Fatalf("interpreter dropped output %q", interpreted.output)
			}

			native := executeProgram(t, buildProgram(t, processCLISource), arguments)
			if native.exitCode != 1 {
				t.Fatalf("expected the native failure exit, found %d", native.exitCode)
			}
			if native.output != strings.Join(arguments, "|") {
				t.Fatalf("native binary dropped output %q", native.output)
			}
			if !strings.Contains(native.errorOutput, "ExitCode must be 0 through 255") {
				t.Fatalf("unexpected native stderr %q", native.errorOutput)
			}
		})
	}
}

// TestUsingCleanupCompletesBeforeStatusIsApplied proves the cleanup ordering:
// the trace a using scope writes on close is already visible in the bytes the
// returned Status carries.
func TestUsingCleanupCompletesBeforeStatusIsApplied(t *testing.T) {
	source := usingTraceSupport + `
function Work() -> string {
    using Handle = Acquire("C") {
        Append("B")
        "value"
    }
}

function main() -> std.process.Status {
    Reset()
    let Value = Work()
    std.process.Status {
        ExitCode: 0
        Output: std.bytes.FromUtf8(Value + ";" + Trace())
        ErrorOutput: std.bytes.FromUtf8("")
    }
}
`
	interpreted := interpretProgram(t, source, nil)
	if interpreted.output != "value;ABC" {
		t.Fatalf("interpreted cleanup trace %q", interpreted.output)
	}
	native := executeProgram(t, buildProgram(t, source), nil)
	if native.output != "value;ABC" {
		t.Fatalf("native cleanup trace %q", native.output)
	}
	if native.exitCode != 0 {
		t.Fatalf("native exit %d", native.exitCode)
	}
}

// TestZeroParameterMainFormsAreUnchanged holds the existing entry points to
// their current behavior now that main may also accept arguments.
func TestZeroParameterMainFormsAreUnchanged(t *testing.T) {
	outcome, diagnostics, err := compiler.RunArguments(
		[]compiler.Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> string { "unchanged" }`}},
		[]string{"ignored"},
	)
	if err != nil {
		t.Fatalf("run zero-parameter main: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if outcome.Text != "unchanged" || outcome.Status != nil {
		t.Fatalf("unexpected outcome %+v", outcome)
	}
}

func TestUnsupportedMainParametersAreRejected(t *testing.T) {
	source := `function main(Count: int) -> null { null }`
	_, _, err := compiler.RunArguments(
		[]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "one string[] parameter") {
		t.Fatalf("interpreter accepted main(Count: int): %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.slk"), []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	if _, err := compiler.BuildPath(root, filepath.Join(root, "app")); err == nil || !strings.Contains(err.Error(), "one string[] parameter") {
		t.Fatalf("build accepted main(Count: int): %v", err)
	}
}
