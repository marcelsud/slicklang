package compiler

import (
	"bufio"
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

func TestBunStdHTTPServerMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPServerProgram}
	binary := buildBunTestProgram(t, source)
	slick := bunStdHTTPServerBuildSlick(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.slk")
	if err := os.WriteFile(path, []byte(bunStdHTTPServerProgram), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	bunObserved := bunStdHTTPServerObserve(t, func(address string) (*exec.Cmd, *strings.Builder) {
		command := exec.Command(binary)
		command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
		var output strings.Builder
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			t.Fatalf("start bun server: %v", err)
		}
		return command, &output
	})

	interpObserved := bunStdHTTPServerObserve(t, func(address string) (*exec.Cmd, *strings.Builder) {
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

	if bunObserved != interpObserved {
		t.Fatalf("Bun observed %q, interpreter observed %q", bunObserved, interpObserved)
	}
}

const bunStdHTTPServerPortlessProgram = `class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response {
        std.http.server.Response {
            Status: 200
            Body: std.bytes.FromUtf8("ok")
        }
    }
}

function Describe(Result: Result<null, std.http.server.Failure>) -> string {
    match Result {
        Ok(_) => "ok"
        Err(Failure) => Failure.Operation
    }
}

function main() -> string effects { database, environment, filesystem, io, network, process, random, state, time } {
    Describe(std.http.server.Serve(std.http.server.Config { Address: "127.0.0.1" }, Echo {}))
}
`

func TestBunStdHTTPServerMatchesInterpreterPortlessAddress(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPServerPortlessProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	if interpreted != "Bind" {
		t.Fatalf("interpreter port-less address = %q, want Bind", interpreted)
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun port-less address error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun port-less address = %q, want interpreter output %q", output, interpreted+"\n")
	}
}

func TestBunStdHTTPServerMatchesInterpreterOversizedBody(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPServerProgram}
	binary := buildBunTestProgram(t, source)
	address := bunStdHTTPServerFreeAddress(t)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start bun server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	bunStdHTTPServerWaitReady(t, "http://"+address)
	bomResponse, err := http.Get("http://" + address + "/echo?q=%EF%BB%BFhello")
	if err != nil {
		t.Fatalf("BOM query: %v", err)
	}
	bomBody, _ := io.ReadAll(bomResponse.Body)
	bomResponse.Body.Close()
	if bomResponse.StatusCode != http.StatusOK || !strings.Contains(string(bomBody), "\uFEFFhello") {
		t.Fatalf("BOM query status=%d body=%q, want U+FEFF hello", bomResponse.StatusCode, bomBody)
	}

	payload := "POST /echo HTTP/1.1\r\nHost: " + address + "\r\nTransfer-Encoding: chunked\r\n\r\n80\r\n" +
		strings.Repeat("x", 128) + "\r\n0\r\n\r\n"
	raw := bunStdHTTPServerRaw(t, address, payload)
	if !strings.Contains(raw, "413") {
		t.Fatalf("chunked oversized raw = %q, want 413", raw)
	}
	if !strings.Contains(strings.ToLower(raw), "connection: close") {
		t.Fatalf("chunked oversized raw missing Connection: close: %q", raw)
	}

	reuse, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial reuse probe: %v", err)
	}
	defer reuse.Close()
	if err := reuse.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("reuse deadline: %v", err)
	}
	if _, err := reuse.Write([]byte("POST /echo HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 128\r\n\r\n" + strings.Repeat("z", 128))); err != nil {
		t.Fatalf("write oversized reuse probe: %v", err)
	}
	first, err := http.ReadResponse(bufio.NewReader(reuse), nil)
	if err != nil {
		t.Fatalf("read oversized reuse probe: %v", err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("reuse probe status = %d, want 413", first.StatusCode)
	}
	_, writeErr := reuse.Write([]byte("GET /echo HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n"))
	if writeErr == nil {
		if err := reuse.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			t.Fatalf("reuse follow-up deadline: %v", err)
		}
		second, err := http.ReadResponse(bufio.NewReader(reuse), nil)
		if err == nil {
			body, _ := io.ReadAll(second.Body)
			second.Body.Close()
			t.Fatalf("connection reusable after 413: status=%d body=%q", second.StatusCode, body)
		}
	}
}

const bunStdHTTPServerSlowProgram = `class Hang implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        if (Request.Path == "/slow") {
            let Result = std.process.Run("sleep", ["30"], null, 1024)
        }
        std.http.server.Response {
            Status: 200
            Body: std.bytes.FromUtf8("ok")
        }
    }
}

function Run(Address: string) -> Result<null, std.http.server.Failure> effects { database, environment, filesystem, io, network, process, random, state, time } {
    std.http.server.Serve(std.http.server.Config {
        Address: Address
        ShutdownTimeoutMilliseconds: 50
    }, Hang {})
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
        async let Server = Run(Address)
        await Server
    }
}
`

func TestBunStdHTTPServerMatchesInterpreterShutdownDeadline(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPServerSlowProgram}
	binary := buildBunTestProgram(t, source)
	address := bunStdHTTPServerFreeAddress(t)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start bun server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})
	bunStdHTTPServerWaitReady(t, "http://"+address)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		response, err := http.Get("http://" + address + "/slow")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-slowDone:
		t.Fatalf("slow handler returned before shutdown: %q", output.String())
	case <-time.After(300 * time.Millisecond):
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal server: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		command.Process = nil
		if err != nil {
			t.Fatalf("server exit after shutdown deadline: %v output=%q", err, output.String())
		}
		if got := strings.TrimSpace(output.String()); got != "Ok()" {
			t.Fatalf("forced shutdown result %q, want Ok()", got)
		}
	case <-time.After(2 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server did not exit within shutdown deadline: %q", output.String())
	}
	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("slow request did not finish after forced shutdown")
	}
}

const bunStdHTTPServerParityProgram = `class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        if (Request.Method == "CONNECT") {
            std.http.server.Response {
                Status: 404
                Body: std.bytes.FromUtf8("connect-hit")
            }
        } else {
            let Query = Request.Query
            let QueryValues = Query.Get("x")
            let QueryText = ""
            if (QueryValues != null) {
                QueryText = std.text.Join(QueryValues, ",")
            }
            let Headers = Request.Headers
            let NameValues = Headers.Get("X-Name")
            let Name = ""
            if (NameValues != null) {
                Name = std.text.Join(NameValues, ",")
            }
            let Payload = std.text.Join([Request.Method, Request.Path, QueryText, Name], "|")
            std.http.server.Response {
                Status: 200
                Headers: map { "X-Name": [Name] }
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
            ShutdownTimeoutMilliseconds: 2000
        }, Echo {})
    }
}
`

func bunStdHTTPServerExchange(t *testing.T, address, payload string) (int, http.Header, string) {
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
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, response.Header, string(body)
}

func TestBunStdHTTPServerMatchesInterpreterHeaderTargetConnect(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPServerParityProgram}
	binary := buildBunTestProgram(t, source)
	address := bunStdHTTPServerFreeAddress(t)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start bun server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	bunStdHTTPServerWaitReady(t, "http://"+address)

	headerStatus, headerHeaders, headerBody := bunStdHTTPServerExchange(t, address,
		"GET /echo HTTP/1.1\r\nHost: "+address+"\r\nX-Name: é\r\nConnection: close\r\n\r\n")
	if headerStatus != http.StatusOK || headerHeaders.Get("X-Name") != "é" || !strings.Contains(headerBody, "|é") {
		t.Fatalf("non-ASCII header status=%d X-Name=%q body=%q", headerStatus, headerHeaders.Get("X-Name"), headerBody)
	}

	absoluteStatus, _, absoluteBody := bunStdHTTPServerExchange(t, address,
		"GET http://"+address+"/a?x=1 HTTP/1.1\r\nHost: "+address+"\r\nConnection: close\r\n\r\n")
	if absoluteStatus != http.StatusOK || !strings.Contains(absoluteBody, "GET|/a|1|") {
		t.Fatalf("absolute-form status=%d body=%q, want GET|/a|1|", absoluteStatus, absoluteBody)
	}

	connectStatus, _, connectBody := bunStdHTTPServerExchange(t, address,
		"CONNECT "+address+" HTTP/1.1\r\nHost: "+address+"\r\n\r\n")
	if connectStatus != http.StatusNotFound || !strings.Contains(connectBody, "connect-hit") {
		t.Fatalf("CONNECT status=%d body=%q, want 404 connect-hit", connectStatus, connectBody)
	}
}

func startBunHTTPServer(t *testing.T, program string) string {
	t.Helper()
	source := Source{Name: "main.slk", Namespace: "root", Text: program}
	binary := buildBunTestProgram(t, source)
	address := bunStdHTTPServerFreeAddress(t)
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "SLICK_HTTP_SERVER_ADDR="+address)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start bun server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process == nil {
			return
		}
		_ = command.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_, _ = command.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})
	bunStdHTTPServerWaitReady(t, "http://"+address)
	return address
}

const bunStdHTTPServerLongTimeoutProgram = `class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        std.http.server.Response {
            Status: 200
            Body: std.bytes.FromUtf8("ok")
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
            ReadHeaderTimeoutMilliseconds: 9000000000
            ReadTimeoutMilliseconds: 9000000000
            WriteTimeoutMilliseconds: 9000000000
            IdleTimeoutMilliseconds: 9000000000
            ShutdownTimeoutMilliseconds: 2000
        }, Echo {})
    }
}
`

const bunStdHTTPServerPhaseProgram = `class Echo implements std.http.server.Handler {
    function Handle(Request: std.http.server.Request) -> std.http.server.Response effects { environment, filesystem, network, process } {
        std.http.server.Response {
            Status: 200
            Body: std.bytes.FromUtf8("ok")
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
            ReadHeaderTimeoutMilliseconds: 200
            ReadTimeoutMilliseconds: 5000
            WriteTimeoutMilliseconds: 200
            IdleTimeoutMilliseconds: 2000
            ShutdownTimeoutMilliseconds: 2000
        }, Echo {})
    }
}
`

func TestBunStdHTTPServerMatchesInterpreterLongTimeout(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerLongTimeoutProgram)
	status, _, body := bunStdHTTPServerExchange(t, address,
		"GET /echo HTTP/1.1\r\nHost: "+address+"\r\nConnection: close\r\n\r\n")
	if status != http.StatusOK || body != "ok" {
		t.Fatalf("long timeout status=%d body=%q, want 200 ok", status, body)
	}
}

func TestBunStdHTTPServerMatchesInterpreterHeaderTimeout(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerPhaseProgram)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /echo HTTP/1.1\r\nHost: " + address + "\r\n")); err != nil {
		t.Fatalf("write partial headers: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(1500 * time.Millisecond)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if n > 0 {
		t.Fatalf("header timeout still served %q", buf[:n])
	}
	if err == nil {
		t.Fatal("header timeout left the connection open")
	}
}

func TestBunStdHTTPServerMatchesInterpreterSlowBodyWriteTimeout(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerPhaseProgram)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := conn.Write([]byte("POST /echo HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 4\r\n\r\n")); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := conn.Write([]byte("abcd")); err != nil {
		t.Fatalf("write body after pause: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response after slow body: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("slow body status=%d body=%q, want 200 ok", response.StatusCode, body)
	}
}

func TestBunStdHTTPServerMatchesInterpreterDeclaredLength(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerProgram)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("POST /echo HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 100\r\n\r\nhello")); err != nil {
		t.Fatalf("write truncated body: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("short deadline: %v", err)
	}
	early := make([]byte, 64)
	if n, err := conn.Read(early); err == nil && n > 0 && strings.Contains(string(early[:n]), "413") {
		t.Fatalf("declared Content-Length rejected before body bytes: %q", early[:n])
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode == http.StatusRequestEntityTooLarge {
		t.Fatalf("truncated declared length status=413, want body-read outcome")
	}
}

func TestBunStdHTTPServerMatchesInterpreterKeepAliveValidation(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerProgram)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := conn.Write([]byte("GET /%zz HTTP/1.1\r\nHost: " + address + "\r\n\r\n")); err != nil {
		t.Fatalf("write invalid path: %v", err)
	}
	first, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read validation 400: %v", err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusBadRequest {
		t.Fatalf("validation status=%d, want 400", first.StatusCode)
	}
	if _, err := conn.Write([]byte("GET /echo HTTP/1.1\r\nHost: " + address + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write reuse after 400: %v", err)
	}
	second, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("connection not reusable after validation 400: %v", err)
	}
	body, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("reuse after 400 status=%d body=%q, want 200", second.StatusCode, body)
	}
}

func TestBunStdHTTPServerMatchesInterpreterUpgrade(t *testing.T) {
	address := startBunHTTPServer(t, bunStdHTTPServerParityProgram)
	status, _, body := bunStdHTTPServerExchange(t, address,
		"GET /echo HTTP/1.1\r\nHost: "+address+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if status != http.StatusOK || !strings.Contains(body, "GET|/echo|") {
		t.Fatalf("upgrade status=%d body=%q, want 200 GET|/echo|", status, body)
	}
}

const bunStdHTTPServerProgram = `class Echo implements std.http.server.Handler {
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

func bunStdHTTPServerFreeAddress(t *testing.T) string {
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

func bunStdHTTPServerWaitReady(t *testing.T, base string) {
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

func bunStdHTTPServerBuildSlick(t *testing.T) string {
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

func bunStdHTTPServerRaw(t *testing.T, address, payload string) string {
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

func bunStdHTTPServerHTTPBody(raw string) string {
	if index := strings.Index(raw, "\r\n\r\n"); index >= 0 {
		return raw[index+4:]
	}
	return raw
}

func bunStdHTTPServerObserve(t *testing.T, start func(address string) (*exec.Cmd, *strings.Builder)) string {
	t.Helper()
	address := bunStdHTTPServerFreeAddress(t)
	command, output := start(address)
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	base := "http://" + address
	bunStdHTTPServerWaitReady(t, base)

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

	keepAlive := bunStdHTTPServerRaw(t, address,
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

	headerRaw := bunStdHTTPServerRaw(t, address,
		"GET /echo HTTP/1.1\r\n"+
			"Host: "+address+"\r\n"+
			"X-Zebra: z\r\n"+
			"x-trace: one\r\n"+
			"X-Trace: two\r\n"+
			"Connection: close\r\n\r\n")
	parts = append(parts, "HEADERS:"+bunStdHTTPServerHTTPBody(headerRaw))

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
