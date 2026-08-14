package compiler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"slick/internal/compiler"
)

func runAsyncBackend(source string, native bool) (string, error) {
	if !native {
		output, diagnostics, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
		if len(diagnostics) != 0 {
			return "", fmt.Errorf("async diagnostics: %v", diagnostics)
		}
		return output, err
	}

	root, err := os.MkdirTemp("", "slick-async-test-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	sourcePath := filepath.Join(root, "main.slk")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		return "", err
	}
	binary := filepath.Join(root, "app")
	diagnostics, err := compiler.BuildPath(sourcePath, binary)
	if err != nil {
		return "", err
	}
	if len(diagnostics) != 0 {
		return "", fmt.Errorf("async diagnostics: %v", diagnostics)
	}
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, output)
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

func receiveAsyncSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestAsyncLetFunctionMethodGenericAndCheckedEffects(t *testing.T) {
	source := `
class Box {
    Value: int

    function Get() -> int {
        self.Value
    }
}

class ChildFailure implements Error {
    Message: string
}

function Fail() -> int throws ChildFailure {
    throw ChildFailure { Message: "caught" }
}

function main() -> Result<int, std.json.Failure> {
    let BoxValue = Box { Value: 41 }
    async let MethodJob = BoxValue.Get()
    async let GenericJob = std.json.Decode<int>("1")
    async let FailureJob = Fail()
    let MethodValue = await MethodJob
    let GenericValue = await GenericJob?
    let FailureValue = await FailureJob catch {
        ChildFailure as Failure => 0
    }
    Ok(MethodValue + GenericValue + FailureValue)
}
`
	if output := runResultEverywhere(t, source); output != "Ok(42)" {
		t.Fatalf("unexpected async output %q", output)
	}
}

func TestAsyncPendingDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		code    string
		message string
	}{
		{"ordinary await", "let Value = 1 await Value", "SLK398", "ordinary value"},
		{"unknown await", "await Missing", "SLK397", "unknown pending binding"},
		{"direct read", "async let Work = Load() let Value = Work await Work", "SLK395", "not a value"},
		{"template read", "async let Work = Load() let Text = `${Work}` await Work", "SLK395", "not a value"},
		{"redeclare pending", "async let Work = Load() async let Work = Load() await Work", "SLK396", "cannot be redeclared"},
		{"assignment", "async let Work = Load() Work = 2 await Work", "SLK396", "immutable"},
		{"double await", "async let Work = Load() let First = await Work await Work", "SLK399", "already been awaited"},
		{"missing await", "async let Work = Load() 0", "SLK400", "every normal path"},
		{"partial branch", "async let Work = Load() if (true) { await Work } else { 0 }", "SLK401", "only some normal branches"},
		{"outer loop", "async let Work = Load() for Item in [1] { let Value = await Work } await Work", "SLK402", "more than once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "function Load() -> int { 1 } function main() -> int { " + test.body + " }"
			assertDiagnostic(t, checkResult(t, source), test.code, test.message)
		})
	}
	validLoop := `function Load() -> int { 1 } function main() -> int { for Item in [1] { async let Work = Load() let Value = await Work } 0 }`
	assertNoDiagnostics(t, checkResult(t, validLoop))
}

func TestAsyncFormattingHighlightingAndDiagnostics(t *testing.T) {
	source := compiler.Source{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function Load()->int{1}function main()->int{async let Work=Load();let Value=await Work?;Value}",
	}
	formatted, diagnostics, err := compiler.Format(source)
	if err != nil {
		t.Fatalf("format async source: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	expected := `function Load() -> int {
    1
}

function main() -> int {
    async let Work = Load()
    let Value = await Work?
    Value
}
`
	if formatted != expected {
		t.Fatalf("formatted async source:\n%s", formatted)
	}
	source.Text = formatted
	again, diagnostics, err := compiler.Format(source)
	if err != nil {
		t.Fatalf("reformat async source: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if again != formatted {
		t.Fatal("async formatting is not idempotent")
	}

	highlightSource := "async let Work = Load()\nawait Work"
	var reproduced strings.Builder
	keywords := map[string]bool{}
	for _, token := range compiler.Highlight(highlightSource) {
		reproduced.WriteString(token.Text)
		if token.Class == compiler.ClassKeyword {
			keywords[token.Text] = true
		}
	}
	if reproduced.String() != highlightSource {
		t.Fatal("async highlighting did not preserve source bytes")
	}
	if !keywords["async"] || !keywords["await"] {
		t.Fatalf("async keyword classes = %v", keywords)
	}

	for code := 394; code <= 403; code++ {
		if _, err := compiler.DescribeDiagnostic(fmt.Sprintf("SLK%d", code)); err != nil {
			t.Fatalf("describe async diagnostic SLK%d: %v", code, err)
		}
	}
}

func TestAsyncRejectsTaskUnsafeValues(t *testing.T) {
	source := `
function Read(Reader: std.io.Reader) -> int { 1 }
function main() -> int {
    using Reader = std.io.ReaderFromBytes(std.bytes.FromUtf8("x")) {
        async let Work = Read(Reader)
        await Work
    }
}
`
	assertDiagnostic(t, checkResult(t, source), "SLK403", "task-unsafe type")
	resultSource := `
interface Value { function Get() -> int }
class Box { function Get() -> int { 1 } }
function Make() -> Value { Box {} }
function main() -> int {
    async let Work = Make()
    let Value = await Work
    Value.Get()
}
`
	assertDiagnostic(t, checkResult(t, resultSource), "SLK403", "async call result has task-unsafe type")
}

func TestAsyncPreparationFailureLaunchesNoChild(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "interpreter"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = response.Write([]byte("unexpected"))
			}))
			defer server.Close()
			source := fmt.Sprintf(`
class PrepFailure implements Error { Message: string }
function Prepare() -> Result<int, PrepFailure> { Err(PrepFailure { Message: "stop" }) }
function Child(Value: int, URL: string) -> int {
    let Response = std.http.Fetch(std.http.Request { Method: "GET" URL: URL })
    Value
}
function main() -> Result<int, PrepFailure> {
    async let Work = Child(Prepare()?, %q)
    let Value = await Work
    Ok(Value)
}
`, server.URL)
			output, err := runAsyncBackend(source, native)
			if err != nil {
				t.Fatalf("run preparation scenario: %v", err)
			}
			if !strings.HasPrefix(output, "Err(") {
				t.Fatalf("unexpected preparation output %q", output)
			}
			if hits.Load() != 0 {
				t.Fatalf("preparation failure launched %d children", hits.Load())
			}
		})
	}
}

func TestAsyncArgumentsEvaluateOnceInSourceOrder(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "interpreter"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			var mutex sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				mutex.Lock()
				paths = append(paths, request.URL.Path)
				mutex.Unlock()
				_, _ = response.Write([]byte("x"))
			}))
			defer server.Close()
			source := fmt.Sprintf(`
function Prepare(Value: int, URL: string) -> Result<int, std.http.Failure> {
    let Response = std.http.Fetch(std.http.Request { Method: "GET" URL: URL })?
    Ok(Value)
}
function Add(Left: int, Right: int) -> int { Left + Right }
function main() -> Result<int, std.http.Failure> {
    async let Work = Add(Prepare(1, %q)?, Prepare(2, %q)?)
    Ok(await Work)
}
`, server.URL+"/first", server.URL+"/second")
			output, err := runAsyncBackend(source, native)
			if err != nil {
				t.Fatalf("run argument-order scenario: %v", err)
			}
			if output != "Ok(3)" {
				t.Fatalf("unexpected argument-order output %q", output)
			}
			mutex.Lock()
			got := append([]string(nil), paths...)
			mutex.Unlock()
			if strings.Join(got, ",") != "/first,/second" {
				t.Fatalf("argument evaluation order/count = %v", got)
			}
		})
	}
}

func TestAsyncHTTPChildrenOverlap(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "interpreter"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{}, 2)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				started <- struct{}{}
				<-release
				_, _ = response.Write([]byte("x"))
			}))
			defer server.Close()

			source := fmt.Sprintf(`
function Fetch(URL: string) -> Result<int, std.http.Failure> {
    let Response = std.http.Fetch(std.http.Request { Method: "GET" URL: URL })?
    Ok(Response.Status)
}
function main() -> Result<int, std.http.Failure> {
    async let Left = Fetch(%q)
    async let Right = Fetch(%q)
    let LeftStatus = await Left?
    let RightStatus = await Right?
    Ok(LeftStatus + RightStatus)
}
`, server.URL+"/left", server.URL+"/right")

			result := make(chan struct {
				output string
				err    error
			}, 1)
			go func() {
				output, err := runAsyncBackend(source, native)
				result <- struct {
					output string
					err    error
				}{output, err}
			}()
			receiveAsyncSignal(t, started, "first request")
			receiveAsyncSignal(t, started, "second request")
			close(release)
			completed := <-result
			if completed.err != nil {
				t.Fatalf("run overlap scenario: %v", completed.err)
			}
			if completed.output != "Ok(400)" {
				t.Fatalf("unexpected overlap output %q", completed.output)
			}
		})
	}
}

func TestAsyncResultPropagationCancelsAndJoinsHTTPChild(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "interpreter"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			blocked := make(chan struct{}, 1)
			cancelled := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/blocked" {
					blocked <- struct{}{}
					<-request.Context().Done()
					cancelled <- struct{}{}
					return
				}
				<-blocked
				hijacker := response.(http.Hijacker)
				connection, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("hijack failing response: %v", err)
					return
				}
				_ = connection.Close()
			}))
			defer server.Close()

			source := fmt.Sprintf(`
function Pair() -> Result<null, std.http.Failure> {
    async let FailureJob = std.http.Fetch(std.http.Request { Method: "POST" URL: %q })
    async let BlockedJob = std.http.Fetch(std.http.Request { Method: "POST" URL: %q })
    let Failure = await FailureJob?
    let Blocked = await BlockedJob?
    Ok(null)
}
function main() -> string {
    match Pair() {
        Ok(_) => "unexpected"
        Err(Failure) => Failure.Kind
    }
}
`, server.URL+"/failure", server.URL+"/blocked")

			result := make(chan struct {
				output string
				err    error
			}, 1)
			go func() {
				output, err := runAsyncBackend(source, native)
				result <- struct {
					output string
					err    error
				}{output, err}
			}()
			receiveAsyncSignal(t, cancelled, "cancelled sibling request")
			completed := <-result
			if completed.err != nil {
				t.Fatalf("run cancellation scenario: %v", completed.err)
			}
			if completed.output != "Transport" {
				t.Fatalf("unexpected cancellation output %q", completed.output)
			}
		})
	}
}

func TestAsyncReturnJoinsChildBeforeParentUsingCleanup(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "interpreter"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			childDone := make(chan struct{}, 1)
			var closes atomic.Int32
			var cleanupEarly atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/consume":
					started <- struct{}{}
					<-request.Context().Done()
					childDone <- struct{}{}
				case "/wait":
					<-started
					_, _ = response.Write([]byte("ready"))
				case "/close":
					select {
					case <-childDone:
					default:
						cleanupEarly.Store(true)
					}
					closes.Add(1)
					_, _ = response.Write([]byte("closed"))
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()

			source := fmt.Sprintf(`
class Resource {
    URL: string
    function Close() -> null {
        let Closed = std.http.Fetch(std.http.Request { Method: "POST" URL: self.URL + "/close" })
        null
    }
}
function Open(URL: string) -> Resource { Resource { URL: URL } }
function Consume(URL: string) -> null {
    let Response = std.http.Fetch(std.http.Request { Method: "POST" URL: URL + "/consume" })
    null
}
function Run(URL: string) -> null {
    using Resource = Open(URL) {
        async let Work = Consume(URL)
        let Ready = std.http.Fetch(std.http.Request { Method: "GET" URL: URL + "/wait" })
        return null
    }
}
function main() -> string {
    let Done = Run(%q)
    "ok"
}
`, server.URL)
			output, err := runAsyncBackend(source, native)
			if err != nil {
				t.Fatalf("run cleanup scenario: %v", err)
			}
			if output != "ok" {
				t.Fatalf("unexpected cleanup output %q", output)
			}
			if cleanupEarly.Load() {
				t.Fatal("parent cleanup ran before the child joined")
			}
			if closes.Load() != 1 {
				t.Fatalf("parent cleanup ran %d times", closes.Load())
			}
		})
	}
}
