package compiler_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func TestStdEnvExactAliasesAndFailureTypeCheck(t *testing.T) {
	diagnostics := checkResult(t, `
use std.env.Get as GetEnv
use std.env.Failure as EnvFailure

function Read(Name: string) -> string? {
    GetEnv(Name)
}

function Write(Name: string, Value: string) -> Result<null, EnvFailure> {
    std.env.Set(Name, Value)
}

function NewFailure(Name: string) -> EnvFailure {
    EnvFailure {
        Operation: "Test"
        Name: Name
        Message: "failure"
    }
}

function Raise(Name: string) -> null throws EnvFailure {
    throw NewFailure(Name)
}
`)
	assertNoDiagnostics(t, diagnostics)
}

func TestStdEnvStaticCallContracts(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name:    "Get argument type",
			source:  `function main() -> string? { std.env.Get(1) }`,
			code:    "SLK320",
			message: "argument 1 to std.env.Get must be string, found int",
		},
		{
			name:    "Set name type",
			source:  `function main() -> Result<null, std.env.Failure> { std.env.Set(1, "value") }`,
			code:    "SLK320",
			message: "argument 1 to std.env.Set must be string, found int",
		},
		{
			name:    "Set value type",
			source:  `function main() -> Result<null, std.env.Failure> { std.env.Set("name", 1) }`,
			code:    "SLK320",
			message: "argument 2 to std.env.Set must be string, found int",
		},
		{
			name:    "Unset argument type",
			source:  `function main() -> Result<null, std.env.Failure> { std.env.Unset(1) }`,
			code:    "SLK320",
			message: "argument 1 to std.env.Unset must be string, found int",
		},
		{
			name:    "Get arity",
			source:  `function main() -> string? { std.env.Get() }`,
			code:    "SLK320",
			message: "std.env.Get expects 1 arguments, found 0",
		},
		{
			name:    "Set arity",
			source:  `function main() -> Result<null, std.env.Failure> { std.env.Set("name") }`,
			code:    "SLK320",
			message: "std.env.Set expects 2 arguments, found 1",
		},
		{
			name:    "Unset arity",
			source:  `function main() -> Result<null, std.env.Failure> { std.env.Unset("name", "extra") }`,
			code:    "SLK320",
			message: "std.env.Unset expects 1 arguments, found 2",
		},
		{
			name:    "unknown declaration",
			source:  `function main() -> null { std.env.Unknown() }`,
			code:    "SLK203",
			message: "unknown function or method std.env.Unknown",
		},
		{
			name: "optional requires narrowing",
			source: `
function Need(Value: string) -> string { Value }
function main() -> string { Need(std.env.Get("SLICK_OPTIONAL")) }
`,
			code:    "SLK372",
			message: "string? may be null",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDiagnostic(t, checkResult(t, test.source), test.code, test.message)
		})
	}
}

func TestStdEnvRejectsUnknownAndNamespaceAliases(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{name: "unknown declaration", target: "std.env.Unknown"},
		{name: "namespace alias", target: "std.env"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkResult(t, "use "+test.target+` as Env
function main() -> null { null }
`)
			assertDiagnostic(t, diagnostics, "SLK204", "alias target "+test.target+" does not exist")
		})
	}
}

func TestStdEnvResultHasNoCheckedThrowEffect(t *testing.T) {
	diagnostics := checkResult(t, `
function Relay(Name: string, Value: string) -> Result<null, std.env.Failure> {
    std.env.Set(Name, Value)
}
`)
	assertNoDiagnostics(t, diagnostics)
}

func TestInterpreterStdEnvGetStates(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(*testing.T, string)
		expected string
	}{
		{
			name: "missing",
			prepare: func(t *testing.T, name string) {
				preserveAndUnsetEnvironment(t, name)
			},
			expected: "missing",
		},
		{
			name: "present empty",
			prepare: func(t *testing.T, name string) {
				t.Setenv(name, "")
			},
			expected: "empty",
		},
		{
			name: "present non-empty",
			prepare: func(t *testing.T, name string) {
				t.Setenv(name, "Ada")
			},
			expected: "Ada",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name := stdEnvTestName(t)
			test.prepare(t, name)
			output := runResult(t, describeEnvironmentValue+fmt.Sprintf(`
function main() -> string {
    DescribeEnvironmentValue(std.env.Get(%q))
}
`, name))
			if output != test.expected {
				t.Fatalf("expected %q, found %q", test.expected, output)
			}
		})
	}
}

func TestInterpreterStdEnvMutationsAreObserved(t *testing.T) {
	t.Run("Set then Get", func(t *testing.T) {
		name := stdEnvTestName(t)
		preserveAndUnsetEnvironment(t, name)
		output := runResult(t, describeEnvironmentValue+fmt.Sprintf(`
function main() -> string {
    match std.env.Set(%q, "Ada") {
        Ok(_) => DescribeEnvironmentValue(std.env.Get(%q))
        Err(Failure) => Failure.Message
    }
}
`, name, name))
		if output != "Ada" {
			t.Fatalf("expected Ada, found %q", output)
		}
	})

	t.Run("Set replaces", func(t *testing.T) {
		name := stdEnvTestName(t)
		t.Setenv(name, "Grace")
		output := runResult(t, describeEnvironmentValue+fmt.Sprintf(`
function main() -> string {
    match std.env.Set(%q, "Ada") {
        Ok(_) => DescribeEnvironmentValue(std.env.Get(%q))
        Err(Failure) => Failure.Message
    }
}
`, name, name))
		if output != "Ada" {
			t.Fatalf("expected replacement Ada, found %q", output)
		}
	})

	t.Run("Unset then Get", func(t *testing.T) {
		name := stdEnvTestName(t)
		t.Setenv(name, "Ada")
		output := runResult(t, describeEnvironmentValue+fmt.Sprintf(`
function main() -> string {
    match std.env.Unset(%q) {
        Ok(_) => DescribeEnvironmentValue(std.env.Get(%q))
        Err(Failure) => Failure.Message
    }
}
`, name, name))
		if output != "missing" {
			t.Fatalf("expected missing, found %q", output)
		}
	})
}

func TestInterpreterStdEnvFailuresAreTypedResults(t *testing.T) {
	const invalidName = "SLICK_STDLIB_INVALID=NAME"
	const secret = "std-env-secret-value"

	t.Run("Set", func(t *testing.T) {
		output := runResult(t, fmt.Sprintf(`
function main() -> string {
    match std.env.Set(%q, %q) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Name}|${Failure.Message}`+"`"+`
    }
}
`, invalidName, secret))
		assertEnvironmentFailure(t, output, "Set", invalidName, secret)
	})

	t.Run("Unset", func(t *testing.T) {
		requireUnsetenvFailure(t, invalidName)
		output := runResult(t, fmt.Sprintf(`
function main() -> string {
    match std.env.Unset(%q) {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Name}|${Failure.Message}`+"`"+`
    }
}
`, invalidName))
		assertEnvironmentFailure(t, output, "Unset", invalidName, "")
	})
}

func TestInterpreterStdEnvQuestionPropagatesFailures(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		source    string
	}{
		{
			name:      "Set",
			operation: "Set",
			source: `
function Through(Name: string, Value: string) -> Result<string, std.env.Failure> {
    std.env.Set(Name, Value)?
    Ok(Value)
}
function main() -> string {
    match Through("SLICK_INVALID=NAME", "secret") {
        Ok(_) => "unexpected success"
        Err(Failure) => Failure.Operation
    }
}
`,
		},
		{
			name:      "Unset",
			operation: "Unset",
			source: `
function Through(Name: string) -> Result<string, std.env.Failure> {
    std.env.Unset(Name)?
    Ok(Name)
}
function main() -> string {
    match Through("SLICK_INVALID=NAME") {
        Ok(_) => "unexpected success"
        Err(Failure) => Failure.Operation
    }
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.operation == "Unset" {
				requireUnsetenvFailure(t, "SLICK_INVALID=NAME")
			}
			if output := runResult(t, test.source); output != test.operation {
				t.Fatalf("expected propagated %s failure, found %q", test.operation, output)
			}
		})
	}
}

func TestInterpreterStdEnvErrIsNotCaughtAsThrow(t *testing.T) {
	output := runResult(t, `
function Recovery() -> Result<null, std.env.Failure> {
    Ok(null)
}

function main() -> string {
    match (std.env.Set("SLICK_INVALID=NAME", "secret") catch (error) {
        std.env.Failure => Recovery()
    }) {
        Ok(_) => "caught"
        Err(Failure) => Failure.Operation
    }
}
`)
	if output != "Set" {
		t.Fatalf("expected ordinary Err, found %q", output)
	}
}

func TestNativeStdEnvReadsChildEnvironmentAtRuntime(t *testing.T) {
	name := stdEnvTestName(t)
	t.Setenv(name, "build-time")
	binary := buildStdEnvProgram(t, describeEnvironmentValue+fmt.Sprintf(`
function main() -> string {
    DescribeEnvironmentValue(std.env.Get(%q))
}
`, name))

	if output := runStdEnvBinary(t, binary, environmentWith(name, "native", true)); output != "native" {
		t.Fatalf("expected child value native, found %q", output)
	}
	if output := runStdEnvBinary(t, binary, environmentWith(name, "", true)); output != "empty" {
		t.Fatalf("expected present empty child value, found %q", output)
	}
}

func TestNativeStdEnvSetAndUnsetAreObserved(t *testing.T) {
	name := stdEnvTestName(t)
	program := describeEnvironmentValue + fmt.Sprintf(`
function Exercise() -> Result<string, std.env.Failure> {
    std.env.Set(%q, "Ada")?
    let During = DescribeEnvironmentValue(std.env.Get(%q))
    std.env.Unset(%q)?
    let After = DescribeEnvironmentValue(std.env.Get(%q))
    Ok(`+"`"+`${During};${After}`+"`"+`)
}
function main() -> string {
    match Exercise() {
        Ok(Output) => Output
        Err(Failure) => Failure.Message
    }
}
`, name, name, name, name)
	binary := buildStdEnvProgram(t, program)
	if output := runStdEnvBinary(t, binary, environmentWith(name, "Grace", true)); output != "Ada;missing" {
		t.Fatalf("expected Ada;missing, found %q", output)
	}
}

func TestNativeStdEnvSetFailureIsSuccessfulErrProcess(t *testing.T) {
	const invalidName = "SLICK_NATIVE_INVALID=NAME"
	program := fmt.Sprintf(`
function main() -> string {
    match std.env.Set(%q, "secret") {
        Ok(_) => "unexpected success"
        Err(Failure) => `+"`"+`${Failure.Operation}|${Failure.Name}`+"`"+`
    }
}
`, invalidName)
	binary := buildStdEnvProgram(t, program)
	if output := runStdEnvBinary(t, binary, os.Environ()); output != "Set|"+invalidName {
		t.Fatalf("expected typed native Err, found %q", output)
	}
	if interpreted := runResult(t, program); interpreted != "Set|"+invalidName {
		t.Fatalf("interpreter and native differ: interpreter produced %q", interpreted)
	}
}

func TestNativeStdEnvBinaryNeedsNoSourceOrSlickRuntime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.slk")
	name := stdEnvTestName(t)
	program := describeEnvironmentValue + fmt.Sprintf(`
function main() -> string {
    DescribeEnvironmentValue(std.env.Get(%q))
}
`, name)
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(source, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if err := os.Remove(source); err != nil {
		t.Fatalf("remove Slick source: %v", err)
	}
	if output := runStdEnvBinary(t, binary, []string{name + "=standalone"}); output != "standalone" {
		t.Fatalf("expected standalone output, found %q", output)
	}
}

const describeEnvironmentValue = `
function DescribeEnvironmentValue(Value: string?) -> string {
    if (Value == null) {
        "missing"
    } else {
        if (Value == "") {
            "empty"
        } else {
            Value
        }
    }
}
`

func stdEnvTestName(t *testing.T) string {
	t.Helper()
	name := strings.ToUpper(t.Name())
	name = strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(name)
	return "SLICK_STDLIB_" + name
}

func preserveAndUnsetEnvironment(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	t.Cleanup(func() {
		if present {
			if err := os.Setenv(name, value); err != nil {
				t.Errorf("restore %s: %v", name, err)
			}
			return
		}
		if err := os.Unsetenv(name); err != nil {
			t.Errorf("restore missing %s: %v", name, err)
		}
	})
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}

func requireUnsetenvFailure(t *testing.T, name string) {
	t.Helper()
	if err := os.Unsetenv(name); err == nil {
		t.Skip("os.Unsetenv does not reject invalid names on this platform")
	}
}

func assertEnvironmentFailure(t *testing.T, output, operation, name, secret string) {
	t.Helper()
	prefix := operation + "|" + name + "|"
	if !strings.HasPrefix(output, prefix) || len(output) == len(prefix) {
		t.Fatalf("expected populated %s Failure, found %q", operation, output)
	}
	if secret != "" && strings.Contains(output, secret) {
		t.Fatalf("Failure exposed attempted environment value %q", secret)
	}
}

func buildStdEnvProgram(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write Slick source: %v", err)
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(path, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	return binary
}

func runStdEnvBinary(t *testing.T, binary string, environment []string) string {
	t.Helper()
	command := exec.Command(binary)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run native binary: %v: %s", err, output)
	}
	return strings.TrimSuffix(string(output), "\n")
}

func environmentWith(name, value string, present bool) []string {
	prefix := name + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			environment = append(environment, entry)
		}
	}
	if present {
		environment = append(environment, prefix+value)
	}
	return environment
}
