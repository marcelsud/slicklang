package compiler

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rustStdHTTPServerProgram starts an echo server on the address carried by
// SLICK_HTTP_SERVER_ADDR and blocks in std.http.server.Serve. The handler
// reconstructs Method, Path, query q values, body length, Host presence, and
// every exposed header so a driving client can observe keep-alive reuse, an
// unregistered status, a percent-decoded path, header canonicalization/sorting,
// and Host exclusion. MaxBodyBytes is small so an oversized POST exercises the
// 413 limit rejection. It uses only families the Rust backend already implements
// (std.env, std.bytes, std.text, std.convert) and fully-qualified calls plus
// string Join (no interpolation), so the Go raw string below never contains a
// backtick. Maps are bound to locals before Get/Contains, since dispatch only
// accepts names and dotted field access.
const rustStdHTTPServerProgram = `class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        if (Request.Path == "/odd-status") {
            std.http.server.Response {
                Status: 299
                Body: std.bytes.FromUtf8("odd")
            }
        } else {
            let Query = Request.Query
            let QueryValues = Query.Get("q")
            let QueryText = ""
            if (QueryValues != null) {
                QueryText = std.text.Join(QueryValues, ",")
            }
            let Headers = Request.Headers
            let HostText = "no-host"
            if (Headers.Contains("Host")) {
                HostText = "host"
            }
            let HeaderText = ""
            for Name, Values in Headers {
                let One = std.text.Join([Name, std.text.Join(Values, ",")], "=")
                if (HeaderText == "") {
                    HeaderText = One
                } else {
                    HeaderText = std.text.Join([HeaderText, One], ";")
                }
            }
            let Payload = std.text.Join([
                Request.Method,
                Request.Path,
                QueryText,
                std.convert.IntToString(std.bytes.Length(Request.Body)),
                HostText,
                HeaderText
            ], "|")
            std.http.server.Response {
                Status: 200
                Headers: map { "X-Echo": ["yes"] }
                Body: std.bytes.FromUtf8(Payload)
            }
        }
    }
}

function main() -> Result<null, std.http.server.Failure> effects { database, environment, filesystem, io, network, process, random, state, time } {
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
            ReadHeaderTimeoutMilliseconds: 2000
            ReadTimeoutMilliseconds: 2000
            WriteTimeoutMilliseconds: 2000
            IdleTimeoutMilliseconds: 2000
            ShutdownTimeoutMilliseconds: 2000
        }, Echo {})
    }
}
`

func rustStdHTTPServerFreeAddress(t *testing.T) string {
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

func rustStdHTTPServerWaitReady(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(base + "/echo?q=ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", base)
}

func rustStdHTTPServerBuildSlick(t *testing.T) string {
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

func rustStdHTTPServerRaw(t *testing.T, address, payload string) string {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := connection.Write([]byte(payload)); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	body, err := io.ReadAll(connection)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	return string(body)
}

func rustStdHTTPServerHTTPBody(raw string) string {
	if index := strings.Index(raw, "\r\n\r\n"); index >= 0 {
		return raw[index+4:]
	}
	return raw
}

// rustStdHTTPServerObserve drives one server instance through the acceptance
// scenarios (handled GET/POST, MaxBodyBytes 413, keep-alive reuse, an
// unregistered status phrase, a percent-decoded path, header
// canonicalization/sorting with a repeated header and Host exclusion) and then
// a cancellation-driven SIGTERM shutdown, returning a deterministic summary so
// the Rust binary and the interpreter can be compared.
func rustStdHTTPServerObserve(t *testing.T, start func(address string) (*exec.Cmd, *strings.Builder)) string {
	t.Helper()
	address := rustStdHTTPServerFreeAddress(t)
	command, output := start(address)
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	base := "http://" + address
	rustStdHTTPServerWaitReady(t, base)

	var parts []string

	getResponse, err := http.Get(base + "/echo?q=one&q=two")
	if err != nil {
		t.Fatalf("handled GET: %v", err)
	}
	getBody, _ := io.ReadAll(getResponse.Body)
	getResponse.Body.Close()
	parts = append(parts, fmt.Sprintf("GET:%d:%s:%s", getResponse.StatusCode, string(getBody), getResponse.Header.Get("X-Echo")))

	postResponse, err := http.Post(base+"/echo?q=x", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("handled POST: %v", err)
	}
	postBody, _ := io.ReadAll(postResponse.Body)
	postResponse.Body.Close()
	parts = append(parts, fmt.Sprintf("POST:%d:%s", postResponse.StatusCode, string(postBody)))

	oversizedResponse, err := http.Post(base+"/echo", "text/plain", strings.NewReader(strings.Repeat("x", 128)))
	if err != nil {
		t.Fatalf("limit rejection: %v", err)
	}
	oversizedBody, _ := io.ReadAll(oversizedResponse.Body)
	oversizedResponse.Body.Close()
	parts = append(parts, fmt.Sprintf("LIMIT:%d:%d", oversizedResponse.StatusCode, len(oversizedBody)))

	keepAlive := rustStdHTTPServerRaw(t, address,
		"GET /echo HTTP/1.1\r\nHost: "+address+"\r\n\r\n"+
			"GET /echo HTTP/1.1\r\nHost: "+address+"\r\nConnection: close\r\n\r\n")
	parts = append(parts, fmt.Sprintf("KEEPALIVE:%d", strings.Count(keepAlive, "HTTP/1.1 200")))

	oddResponse, err := http.Get(base + "/odd-status")
	if err != nil {
		t.Fatalf("unregistered status: %v", err)
	}
	oddBody, _ := io.ReadAll(oddResponse.Body)
	oddResponse.Body.Close()
	parts = append(parts, fmt.Sprintf("ODD:%s:%s", oddResponse.Status, string(oddBody)))

	pathResponse, err := http.Get(base + "/hello%20world")
	if err != nil {
		t.Fatalf("percent-encoded path: %v", err)
	}
	pathBody, _ := io.ReadAll(pathResponse.Body)
	pathResponse.Body.Close()
	parts = append(parts, fmt.Sprintf("PATH:%d:%s", pathResponse.StatusCode, string(pathBody)))

	headerRaw := rustStdHTTPServerRaw(t, address,
		"GET /echo HTTP/1.1\r\n"+
			"Host: "+address+"\r\n"+
			"X-Zebra: z\r\n"+
			"x-trace: one\r\n"+
			"X-Trace: two\r\n"+
			"Connection: close\r\n\r\n")
	parts = append(parts, "HEADERS:"+rustStdHTTPServerHTTPBody(headerRaw))

	if command.Process == nil {
		t.Fatal("server process missing before shutdown")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit after SIGTERM: %v output=%q", err, output.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server did not exit after SIGTERM output=%q", output.String())
	}
	command.Process = nil
	parts = append(parts, "SHUTDOWN:"+strings.TrimSpace(output.String()))
	return strings.Join(parts, "|")
}

func TestRustStdHTTPServerMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdHTTPServerProgram}
	binary := buildRustTestProgram(t, source)
	slick := rustStdHTTPServerBuildSlick(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(rustStdHTTPServerProgram), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	rustObserved := rustStdHTTPServerObserve(t, func(address string) (*exec.Cmd, *strings.Builder) {
		command := exec.Command(binary)
		command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
		var output strings.Builder
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			t.Fatalf("start rust server: %v", err)
		}
		return command, &output
	})

	interpObserved := rustStdHTTPServerObserve(t, func(address string) (*exec.Cmd, *strings.Builder) {
		command := exec.Command(slick, "run", path)
		command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
		var output strings.Builder
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			t.Fatalf("start interpreter server: %v", err)
		}
		return command, &output
	})

	if rustObserved != interpObserved {
		t.Fatalf("Rust observed %q, interpreter observed %q", rustObserved, interpObserved)
	}
}

// rustStdHTTPServerOverlappingProgram starts two Serve calls at once so the
// process-wide signal broker is held by both. After SIGTERM shuts them down it
// writes SLICK_HTTP_SERVER_READY and stays alive; a later SIGTERM must then
// use the restored original disposition and terminate the process.
const rustStdHTTPServerOverlappingProgram = `class OK implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        std.http.server.Response {
            Status: 200
            Body: std.bytes.FromUtf8("ok")
        }
    }
}

function ServeOne(Address: string) -> Result<null, std.http.server.Failure> effects { database, environment, filesystem, io, network, process, random, state, time } {
    std.http.server.Serve(std.http.server.Config {
        Address: Address
        ShutdownTimeoutMilliseconds: 2000
    }, OK {})
}

function main() -> string effects { database, environment, filesystem, io, network, process, random, state, time } {
    let AddressA = std.env.Get("SLICK_HTTP_SERVER_ADDR_A")
    let AddressB = std.env.Get("SLICK_HTTP_SERVER_ADDR_B")
    let Ready = std.env.Get("SLICK_HTTP_SERVER_READY")
    if (AddressA == null) {
        "missing"
    } else if (AddressB == null) {
        "missing"
    } else if (Ready == null) {
        "missing"
    } else {
        async let First = ServeOne(AddressA)
        async let Second = ServeOne(AddressB)
        let _ = await First
        let _ = await Second
        let _ = std.fs.WriteText(Ready, "restored")
        for Index in 0..2000000000 {
            Index
        }
        "done"
    }
}
`

func TestRustStdHTTPServerMatchesInterpreterOverlappingSignals(t *testing.T) {
	generated := mustGenerateRust(t, rustCoreForTest(t, rustStdHTTPServerOverlappingProgram))
	if !strings.Contains(generated, "slick_http_server_acquire_signals") || !strings.Contains(generated, "slick_http_server_release_signals") {
		t.Fatal("overlapping Serve must use a process-wide reference-counted signal broker")
	}
	if strings.Contains(generated, "slick_http_server_install_signals") || strings.Contains(generated, "slick_http_server_restore_signals") {
		t.Fatal("Serve must not capture and restore signal dispositions per call")
	}

	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdHTTPServerOverlappingProgram}
	binary := buildRustTestProgram(t, source)
	addrA := rustStdHTTPServerFreeAddress(t)
	addrB := rustStdHTTPServerFreeAddress(t)
	ready := filepath.Join(t.TempDir(), "restored")

	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"SLICK_HTTP_SERVER_ADDR_A="+addrA,
		"SLICK_HTTP_SERVER_ADDR_B="+addrB,
		"SLICK_HTTP_SERVER_READY="+ready,
	)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start overlapping rust servers: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})

	rustStdHTTPServerWaitReady(t, "http://"+addrA)
	rustStdHTTPServerWaitReady(t, "http://"+addrB)

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send first SIGTERM: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(ready)
		if err == nil && string(data) == "restored" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(ready)
	if err != nil || string(data) != "restored" {
		t.Fatalf("overlapping servers did not restore after first SIGTERM: %v output=%q", err, output.String())
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send second SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		command.Process = nil
		if err == nil {
			t.Fatalf("second SIGTERM left the process running under the custom handler, output=%q", output.String())
		}
		if !strings.Contains(err.Error(), "signal") && !strings.Contains(err.Error(), "terminated") {
			t.Fatalf("second SIGTERM exit = %v output=%q", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("second SIGTERM did not terminate; custom handler left installed output=%q", output.String())
	}
}
