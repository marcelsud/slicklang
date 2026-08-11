package compiler_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"slick/internal/compiler"
)

const httpServerEchoHandler = `
class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response {
        if (Request.Path == "/bad-status") {
            return std.http.server.Response {
                Status: 0
                Body: std.bytes.FromUtf8("nope")
            }
        }
        if (Request.Path == "/bad-header") {
            return std.http.server.Response {
                Status: 200
                Headers: map { "Bad Name": ["value"] }
                Body: std.bytes.FromUtf8("nope")
            }
        }
        if (Request.Path == "/no-content") {
            return std.http.server.Response {
                Status: 204
                Body: std.bytes.FromUtf8("hidden")
            }
        }
        if (Request.Path == "/count") {
            return std.http.server.Response {
                Status: 200
                Body: std.bytes.FromUtf8("hit")
            }
        }
        if (Request.Path == "/slow") {
            let URL = std.env.Get("SLICK_HTTP_BLOCK_URL")
            if (URL != null) {
                let Result = std.http.Fetch(std.http.Request {
                    Method: "GET"
                    URL: URL
                })
            }
            return std.http.server.Response {
                Status: 200
                Body: std.bytes.FromUtf8("slow")
            }
        }
        let Query = Request.Query
        let QueryText = ""
        let QueryValues = Query.Get("q")
        if (QueryValues != null) {
            QueryText = std.text.Join(QueryValues, ",")
        }
        let Headers = Request.Headers
        let HeaderText = ""
        let HeaderValues = Headers.Get("X-Trace")
        if (HeaderValues != null) {
            HeaderText = std.text.Join(HeaderValues, ",")
        }
        let BodyText = match std.bytes.ToUtf8(Request.Body) {
            Ok(Text) => Text
            Err(_) => "invalid"
        }
        let Payload = std.text.Join([
            Request.Method,
            Request.Path,
            QueryText,
            HeaderText,
            BodyText,
            std.convert.IntToString(std.bytes.Length(Request.Body)),
        ], "|")
        std.http.server.Response {
            Status: 200
            Headers: map {
                "X-Echo": ["yes"]
                "X-Multi": ["a", "b"]
            }
            Body: std.bytes.FromUtf8(Payload)
        }
    }
}
`

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback address: %v", err)
	}
	return address
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			response.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", url)
}

func buildHTTPServerBinary(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	binary := filepath.Join(root, "server")
	diagnostics, err := compiler.BuildPath(path, binary)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	return binary
}

func startHTTPServerBinary(t *testing.T, binary, address string, env ...string) *exec.Cmd {
	t.Helper()
	command := exec.Command(binary)
	command.Env = append(os.Environ(), env...)
	command.Env = append(command.Env, "SLICK_HTTP_SERVER_ADDR="+address)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	return command
}

func assertMalformedQueryAndConnectRejected(t *testing.T, address string) {
	t.Helper()
	request := func(method, target string) (*http.Response, string) {
		connection, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatalf("dial raw request: %v", err)
		}
		defer connection.Close()
		if _, err := fmt.Fprintf(connection, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", method, target, address); err != nil {
			t.Fatalf("write raw request: %v", err)
		}
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil {
			t.Fatalf("read raw response: %v", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatalf("read raw response body: %v", err)
		}
		return response, string(body)
	}

	malformed, body := request(http.MethodGet, "/count?q=%zz")
	if malformed.StatusCode != http.StatusBadRequest || strings.Contains(body, "hit") {
		t.Fatalf("malformed query status=%d body=%q", malformed.StatusCode, body)
	}
	connect, body := request(http.MethodConnect, address)
	if connect.StatusCode != http.StatusInternalServerError || strings.Contains(body, "hit") {
		t.Fatalf("CONNECT status=%d body=%q", connect.StatusCode, body)
	}
}

func shutdownBlocker(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(150 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server.URL, started
}

func assertShutdownDeadlineSucceeds(t *testing.T, command *exec.Cmd, base string, started <-chan struct{}, output *strings.Builder) {
	t.Helper()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(base + "/slow")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow handler did not start")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal server: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit after forced shutdown: %v output=%q", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server did not exit after forced shutdown: %q", output.String())
	}
	command.Process = nil
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("slow request did not finish")
	}
}

func TestStdHTTPServerConfigAndBindFailuresEverywhere(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy address: %v", err)
	}
	defer occupied.Close()

	source := fmt.Sprintf(`
%s
function Describe(Result: Result<null, std.http.server.Failure>) -> string {
    match Result {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation + ":" + Failure.Message
    }
}

function main() -> string {
    let Empty = Describe(std.http.server.Serve(std.http.server.Config { Address: "" }, Echo {}))
    let ZeroBody = Describe(std.http.server.Serve(std.http.server.Config {
        Address: "127.0.0.1:1"
        MaxBodyBytes: 0
    }, Echo {}))
    let Negative = Describe(std.http.server.Serve(std.http.server.Config {
        Address: "127.0.0.1:1"
        ReadTimeoutMilliseconds: -1
    }, Echo {}))
    let Occupied = Describe(std.http.server.Serve(std.http.server.Config {
        Address: %q
    }, Echo {}))
    let Invalid = Describe(std.http.server.Serve(std.http.server.Config {
        Address: "not-a-valid-address!!!"
    }, Echo {}))
    std.text.Join([Empty, ZeroBody, Negative, Occupied, Invalid], "|")
}
`, httpServerEchoHandler, occupied.Addr().String())

	output := runResultEverywhere(t, source)
	parts := strings.Split(output, "|")
	if len(parts) != 5 {
		t.Fatalf("unexpected output %q", output)
	}
	for index, wantPrefix := range []string{"Config:", "Config:", "Config:", "Bind:", "Bind:"} {
		if !strings.HasPrefix(parts[index], wantPrefix) {
			t.Fatalf("part %d = %q, want prefix %q", index, parts[index], wantPrefix)
		}
	}
	if strings.Contains(output, "TOP_SECRET") {
		t.Fatalf("failure text leaked secrets: %q", output)
	}
}

func TestStdHTTPServerServeContractsNative(t *testing.T) {
	address := freeLoopbackAddress(t)
	source := httpServerEchoHandler + `
function main() -> Result<null, std.http.server.Failure> {
    let Address = std.env.Get("SLICK_HTTP_SERVER_ADDR")
    if (Address == null) {
        Err(std.http.server.Failure {
            Operation: "Config"
            Address: ""
            Message: "missing address"
        })
    } else {
        std.http.server.Serve(std.http.server.Config {
            Address: Address
            MaxBodyBytes: 64
            MaxHeaderBytes: 2048
            ReadHeaderTimeoutMilliseconds: 1000
            ReadTimeoutMilliseconds: 1000
            WriteTimeoutMilliseconds: 1000
            IdleTimeoutMilliseconds: 1000
            ShutdownTimeoutMilliseconds: 10
        }, Echo {})
    }
}
`
	binary := buildHTTPServerBinary(t, source)
	blockURL, blockerStarted := shutdownBlocker(t)
	command := startHTTPServerBinary(t, binary, address, "SLICK_HTTP_BLOCK_URL="+blockURL)
	base := "http://" + address
	waitForHTTP(t, base+"/count")
	assertMalformedQueryAndConnectRejected(t, address)

	// Methods including an extension token, query, headers, and body.
	request, err := http.NewRequest("PROPFIND", base+"/items?q=one&q=two&flag=1", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Add("X-Trace", "alpha")
	request.Header.Add("X-Trace", "beta")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PROPFIND: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("PROPFIND status %d body %q", response.StatusCode, body)
	}
	if got := string(body); got != "PROPFIND|/items|one,two|alpha,beta|payload|7" {
		t.Fatalf("PROPFIND body %q", got)
	}
	if values := response.Header.Values("X-Multi"); strings.Join(values, ",") != "a,b" {
		t.Fatalf("response multi headers = %v", values)
	}

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, err := http.NewRequest(method, base+"/m", nil)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s request: %v", method, err)
		}
		payload, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(string(payload), method+"|") {
			t.Fatalf("%s => %d %q", method, resp.StatusCode, payload)
		}
	}

	// HEAD suppresses body while keeping status.
	head, err := http.Head(base + "/count")
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	headBody, _ := io.ReadAll(head.Body)
	head.Body.Close()
	if head.StatusCode != 200 || len(headBody) != 0 {
		t.Fatalf("HEAD status=%d body=%q", head.StatusCode, headBody)
	}

	// No-content status suppresses body.
	noContent, err := http.Get(base + "/no-content")
	if err != nil {
		t.Fatalf("no-content: %v", err)
	}
	noContentBody, _ := io.ReadAll(noContent.Body)
	noContent.Body.Close()
	if noContent.StatusCode != 204 || len(noContentBody) != 0 {
		t.Fatalf("no-content status=%d body=%q", noContent.StatusCode, noContentBody)
	}

	// Invalid response status becomes sanitized 500 without process death.
	bad, err := http.Get(base + "/bad-status")
	if err != nil {
		t.Fatalf("bad-status: %v", err)
	}
	badBody, _ := io.ReadAll(bad.Body)
	bad.Body.Close()
	if bad.StatusCode != 500 {
		t.Fatalf("bad-status status=%d body=%q", bad.StatusCode, badBody)
	}
	if strings.Contains(string(badBody), "nope") {
		t.Fatalf("invalid response echoed body: %q", badBody)
	}

	// Invalid response header becomes sanitized 500.
	badHeader, err := http.Get(base + "/bad-header")
	if err != nil {
		t.Fatalf("bad-header: %v", err)
	}
	badHeaderBody, _ := io.ReadAll(badHeader.Body)
	badHeader.Body.Close()
	if badHeader.StatusCode != 500 || strings.Contains(string(badHeaderBody), "nope") {
		t.Fatalf("bad-header status=%d body=%q", badHeader.StatusCode, badHeaderBody)
	}

	// Oversized body is rejected without invoking handler payload echo.
	oversized := strings.Repeat("x", 128)
	tooLarge, err := http.Post(base+"/count", "text/plain", strings.NewReader(oversized))
	if err != nil {
		t.Fatalf("oversized body: %v", err)
	}
	tooLargeBody, _ := io.ReadAll(tooLarge.Body)
	tooLarge.Body.Close()
	if tooLarge.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%q", tooLarge.StatusCode, tooLargeBody)
	}
	if strings.Contains(string(tooLargeBody), "xxx") {
		t.Fatalf("oversized rejection echoed body: %q", tooLargeBody)
	}

	// Concurrent isolated requests.
	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	for index := 0; index < 16; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			path := fmt.Sprintf("/c/%d", index)
			resp, err := http.Get(base + path)
			if err != nil {
				failures.Add(1)
				return
			}
			payload, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			// Method|Path|Query|Header|Body|Length with three empty middle fields.
			want := fmt.Sprintf("GET|%s||||0", path)
			if resp.StatusCode != 200 || string(payload) != want {
				failures.Add(1)
			}
		}(index)
	}
	waitGroup.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent requests had %d failures", failures.Load())
	}

	// Server remains available after a bad handler response.
	alive, err := http.Get(base + "/count")
	if err != nil {
		t.Fatalf("post-failure request: %v", err)
	}
	alive.Body.Close()
	if alive.StatusCode != 200 {
		t.Fatalf("post-failure status %d", alive.StatusCode)
	}

	// A handler outliving the graceful deadline is force-closed and still exits successfully.
	assertShutdownDeadlineSucceeds(t, command, base, blockerStarted, &strings.Builder{})
}

func TestStdHTTPServerServeContractsInterpreter(t *testing.T) {
	address := freeLoopbackAddress(t)
	source := httpServerEchoHandler + `
function main() -> Result<null, std.http.server.Failure> {
    let Address = std.env.Get("SLICK_HTTP_SERVER_ADDR")
    if (Address == null) {
        Err(std.http.server.Failure {
            Operation: "Config"
            Address: ""
            Message: "missing address"
        })
    } else {
        std.http.server.Serve(std.http.server.Config {
            Address: Address
            MaxBodyBytes: 64
            ShutdownTimeoutMilliseconds: 10
        }, Echo {})
    }
}
`
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	slick := buildSlickTool(t)
	command := exec.Command(slick, "run", path)
	blockURL, blockerStarted := shutdownBlocker(t)
	command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address, "SLICK_HTTP_BLOCK_URL="+blockURL)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start interpreter server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})

	base := "http://" + address
	waitForHTTP(t, base+"/count")
	assertMalformedQueryAndConnectRejected(t, address)

	response, err := http.Post(base+"/echo?q=one&q=two", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("interpreter POST: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	// Method|Path|Query|Header|Body|Length — Content-Type is not X-Trace.
	if response.StatusCode != 200 || string(body) != "POST|/echo|one,two||hi|2" {
		t.Fatalf("interpreter body %q status %d", body, response.StatusCode)
	}

	// Concurrent requests through the interpreter path.
	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	for index := 0; index < 8; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			path := "/i/" + strconv.Itoa(index)
			resp, err := http.Get(base + path)
			if err != nil {
				failures.Add(1)
				return
			}
			payload, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := fmt.Sprintf("GET|%s||||0", path)
			if resp.StatusCode != 200 || string(payload) != want {
				failures.Add(1)
			}
		}(index)
	}
	waitGroup.Wait()
	if failures.Load() != 0 {
		t.Fatalf("interpreter concurrent failures: %d output=%q", failures.Load(), output.String())
	}

	assertShutdownDeadlineSucceeds(t, command, base, blockerStarted, &output)
}

func buildSlickTool(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "slick")
	command := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "./cmd/slick")
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build slick tool: %v\n%s", err, output)
	}
	return binary
}

func TestStdHTTPServerStaticDiagnostics(t *testing.T) {
	tests := map[string]struct {
		source  string
		code    string
		message string
	}{
		"Serve arity": {
			source:  `function main() -> null { std.http.server.Serve() null }`,
			code:    "SLK320",
			message: "Serve expects 2 arguments, found 0",
		},
		"Serve config type": {
			source:  `function main() -> null { std.http.server.Serve("nope", Echo {}) null }` + httpServerEchoHandler,
			code:    "SLK320",
			message: "argument 1 to std.http.server.Serve must be Config, found string",
		},
		"Handler missing method": {
			source: `
class Empty {}
function main() -> null {
    std.http.server.Serve(std.http.server.Config { Address: "127.0.0.1:0" }, Empty {})
    null
}
`,
			code:    "SLK320",
			message: "does not implement std.http.server.Handler",
		},
		"Config requires Address": {
			source:  `function main() -> null { let _ = std.http.server.Config {} null }`,
			code:    "SLK376",
			message: "Config requires field Address of type string",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := checkResult(t, test.source)
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestStdHTTPServerDescribeSurface(t *testing.T) {
	description, diagnostics, err := compiler.DescribePath("std.http.server.Serve", "")
	if err != nil {
		t.Fatalf("describe Serve: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if description.Symbol.CanonicalName != "std.http.server.Serve" || !description.Symbol.Native {
		t.Fatalf("Serve description = %+v", description.Symbol)
	}
	if description.Symbol.ReturnType != "Result<null,std.http.server.Failure>" {
		t.Fatalf("Serve return = %s", description.Symbol.ReturnType)
	}

	handler, diagnostics, err := compiler.DescribePath("std.http.server.Handler", "")
	if err != nil {
		t.Fatalf("describe Handler: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if handler.Symbol.Kind != "interface" || len(handler.Symbol.DeclaredMethods) != 1 {
		t.Fatalf("Handler description = %+v", handler.Symbol)
	}
	if !strings.Contains(handler.Symbol.DeclaredMethods[0].CanonicalName, "Handle") {
		t.Fatalf("Handler methods = %+v", handler.Symbol.DeclaredMethods)
	}

	namespace, diagnostics, err := compiler.DescribePath("std.http.server", "")
	if err != nil {
		t.Fatalf("describe namespace: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	children := make([]string, 0, len(namespace.Symbol.Children))
	for _, child := range namespace.Symbol.Children {
		children = append(children, child.Kind+" "+child.CanonicalName)
	}
	want := []string{
		"class std.http.server.Config",
		"class std.http.server.Failure",
		"interface std.http.server.Handler",
		"class std.http.server.Request",
		"class std.http.server.Response",
		"function std.http.server.Serve",
	}
	if strings.Join(children, ",") != strings.Join(want, ",") {
		t.Fatalf("namespace children = %v, want %v", children, want)
	}
}

func TestStdHTTPServerClientUnchanged(t *testing.T) {
	// Outbound Fetch must remain available and must not require the server surface.
	server := httptestServer(t)
	t.Setenv("SLICK_HTTP_TEST_URL", server)
	source := `
function main() -> string {
    let Base = std.env.Get("SLICK_HTTP_TEST_URL")
    if (Base == null) {
        "missing"
    } else {
        let Request = std.http.Request {
            Method: "GET"
            URL: Base + "/ok"
        }
        match std.http.Fetch(Request) {
            Ok(Response) => std.convert.IntToString(Response.Status)
            Err(Failure) => Failure.Kind
        }
    }
}
`
	if output := runResultEverywhere(t, source); output != "200" {
		t.Fatalf("client fetch = %q", output)
	}
}

func httptestServer(t *testing.T) string {
	t.Helper()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return "http://" + listener.Addr().String()
}
