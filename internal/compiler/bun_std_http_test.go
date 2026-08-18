package compiler

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBunStdHTTPMatchesInterpreter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/get":
			response.Header().Set("X-Reply", "yes")
			response.Header().Add("Set-Cookie", "a=1")
			response.Header().Add("Set-Cookie", "b=2")
			_, _ = response.Write([]byte("hello"))
		case "/echo":
			body, _ := io.ReadAll(request.Body)
			valid := request.Method == "POST" && string(body) == "hello" &&
				strings.Join(request.Header.Values("X-Trace"), ",") == "one,two,three" &&
				request.UserAgent() == "Slick"
			if !valid {
				http.Error(response, "invalid echo request", http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write(body)
		case "/status":
			http.Error(response, "missing", http.StatusNotFound)
		case "/redirect":
			http.Redirect(response, request, "/final", http.StatusFound)
		case "/final":
			_, _ = response.Write([]byte("done"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SLICK_HTTP_TEST_URL", server.URL)
	t.Setenv("SLICK_HTTP_REFUSED_URL", refusedURL)

	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	want := "200|hello|yes|a=1,b=2|201|hello|404|302|200|true|Transport:null|No Content|null"
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}

	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun output = %q, want interpreter output %q", output, interpreted+"\n")
	}
}

func runBunHTTPProgram(t *testing.T, text string) string {
	t.Helper()
	source := Source{Name: "main.slk", Namespace: "root", Text: text}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun output = %q, want interpreter output %q", output, interpreted+"\n")
	}
	return interpreted
}

func bunStdHTTPServeRaw(t *testing.T, payload string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go bunStdHTTPWriteRaw(conn, payload)
		}
	}()
	return "http://" + listener.Addr().String()
}

func bunStdHTTPWriteRaw(conn net.Conn, payload string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	received := ""
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			received += string(buf[:n])
		}
		if strings.Contains(received, "\r\n\r\n") || readErr != nil {
			break
		}
	}
	_, _ = conn.Write([]byte(payload))
}

func TestBunStdHTTPMatchesInterpreterRedirectAuth(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			_, _ = response.Write([]byte("leaked"))
			return
		}
		_, _ = response.Write([]byte("stripped"))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL+"/final", http.StatusFound)
	}))
	defer origin.Close()

	start := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/cross"
	t.Setenv("SLICK_HTTP_START_URL", start)

	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Start = std.env.Get("SLICK_HTTP_START_URL")
    if (Start == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Start Headers: map { "Authorization": ["Bearer secret"] "Cookie": ["sid=1"] } FollowRedirects: true })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "stripped" {
		t.Fatalf("cross-origin redirect = %q, want stripped", got)
	}
}

func TestBunStdHTTPMatchesInterpreterHEAD(t *testing.T) {
	url := bunStdHTTPServeRaw(t, "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello")
	t.Setenv("SLICK_HTTP_HEAD_URL", url)
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_HEAD_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "HEAD" URL: Target })
        match Result {
            Ok(Response) => std.convert.IntToString(Response.Status) + ":" + std.convert.IntToString(std.bytes.Length(Response.Body))
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "200:0" {
		t.Fatalf("HEAD = %q, want 200:0", got)
	}
}

func TestBunStdHTTPMatchesInterpreterOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("four"))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_LARGE_URL", server.URL+"/large")
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Status(Value: int?) -> string {
    if (Value == null) { "null" } else { std.convert.IntToString(Value) }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_LARGE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target MaxResponseBytes: 3 })
        match Result {
            Ok(_) => "ok"
            Err(Failure) => Failure.Kind + ":" + Status(Failure.Status)
        }
    }
}
`)
	if got != "BodyTooLarge:200" {
		t.Fatalf("oversized body = %q, want BodyTooLarge:200", got)
	}
}

func TestBunStdHTTPMatchesInterpreterCancelled(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/ready" {
			<-started
			_, _ = response.Write([]byte("ok"))
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SLICK_HTTP_HANG_URL", server.URL+"/hang")
	t.Setenv("SLICK_HTTP_READY_URL", server.URL+"/ready")
	t.Setenv("SLICK_HTTP_REFUSED_URL", refusedURL)

	source := Source{Name: "main.slk", Namespace: "root", Text: `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Child(Hang: string) -> string effects { environment, network } {
    let Result = Fetch(HTTPRequest { Method: "GET" URL: Hang TimeoutMilliseconds: 30000 })
    let Kind = match Result {
        Ok(_) => "ok"
        Err(Failure) => Failure.Kind
    }
    let _ = std.env.Set("SLICK_HTTP_KIND", Kind)
    Kind
}

function Parent(Hang: string, Ready: string, Refused: string) -> Result<string, std.http.Failure> effects { environment, network } {
    async let Work = Child(Hang)
    let _ = Fetch(HTTPRequest { Method: "GET" URL: Ready })?
    let _ = Fetch(HTTPRequest { Method: "GET" URL: Refused })?
    let Kind = await Work
    Ok(Kind)
}

function main() -> string effects { environment, network } {
    let Hang = std.env.Get("SLICK_HTTP_HANG_URL")
    let Ready = std.env.Get("SLICK_HTTP_READY_URL")
    let Refused = std.env.Get("SLICK_HTTP_REFUSED_URL")
    if (Hang == null) {
        "missing"
    } else {
        if (Ready == null) {
            "missing"
        } else {
            if (Refused == null) {
                "missing"
            } else {
                let _ = Parent(Hang, Ready, Refused)
                let Kind = std.env.Get("SLICK_HTTP_KIND")
                if (Kind == null) { "missing" } else { Kind }
            }
        }
    }
}
`}
	binary := buildBunTestProgram(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun cancelled request error = %v, output = %q", err, output)
	}
	if string(output) != "Cancelled\n" {
		t.Fatalf("cancelled request = %q, want Cancelled", output)
	}
}

const bunStdHTTPCaseFetchProgram = `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`

func TestBunStdHTTPMatchesInterpreterIPv6(t *testing.T) {
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback not available")
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go bunStdHTTPWriteRaw(conn, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\nipv6")
		}
	}()
	t.Setenv("SLICK_HTTP_CASE_URL", "http://"+listener.Addr().String()+"/")
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "ipv6" {
		t.Fatalf("IPv6 loopback = %q, want ipv6", got)
	}
}

func TestBunStdHTTPMatchesInterpreterProxyCredentials(t *testing.T) {
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Proxy-Authorization") != wantAuth {
			response.WriteHeader(http.StatusProxyAuthRequired)
			_, _ = response.Write([]byte("need-auth"))
			return
		}
		_, _ = response.Write([]byte("authed"))
	}))
	defer proxy.Close()

	parsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("user", "pass")
	t.Setenv("HTTP_PROXY", parsed.String())
	t.Setenv("http_proxy", parsed.String())
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")
	t.Setenv("SLICK_HTTP_CASE_URL", "http://proxy.test.example/via")
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPCaseFetchProgram}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun proxy credentials error = %v, output = %q", err, output)
	}
	if string(output) != "authed\n" {
		t.Fatalf("proxy credentials = %q, want authed", output)
	}
}

func TestBunStdHTTPMatchesInterpreterHeaderLimit(t *testing.T) {
	pad := strings.Repeat("a", 2*1024*1024)
	payload := "HTTP/1.1 200 OK\r\nX-Pad: " + pad + "\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "ok" {
		t.Fatalf("2MiB headers = %q, want ok", got)
	}
}

func TestBunStdHTTPMatchesInterpreterContentLength(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("conflicting Content-Length = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterTransferEncoding(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip\r\nConnection: close\r\n\r\nraw"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("unsupported Transfer-Encoding = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterLargeBody(t *testing.T) {
	body := strings.Repeat("x", 2*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target })
        match Result {
            Ok(Response) => std.convert.IntToString(std.bytes.Length(Response.Body))
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "2097152" {
		t.Fatalf("large body length = %q, want 2097152", got)
	}
}

func TestBunStdHTTPMatchesInterpreterUTF8Header(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Note") != "é" {
			http.Error(response, "bad note", http.StatusBadRequest)
			return
		}
		response.Header().Set("X-Echo", "café")
		_, _ = response.Write([]byte("é"))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target Headers: map { "X-Note": ["é"] } })
        match Result {
            Ok(Response) => Text(Response.Body) + "|" + std.text.Join(HeaderValues(Response.Headers, "X-Echo"), ",")
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "é|café" {
		t.Fatalf("UTF-8 header = %q, want é|café", got)
	}
}

func TestBunStdHTTPMatchesInterpreterChunkedHeaders(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\nX-Ok: yes\r\nTrailer: X-Trail\r\n\r\n5\r\nhello\r\n0\r\nX-Trail: t\r\n\r\n"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target })
        match Result {
            Ok(Response) => std.text.Join([
                Text(Response.Body),
                std.text.Join(HeaderValues(Response.Headers, "X-Ok"), ","),
                std.text.Join(HeaderValues(Response.Headers, "Transfer-Encoding"), ","),
                std.text.Join(HeaderValues(Response.Headers, "Trailer"), ","),
                std.text.Join(HeaderValues(Response.Headers, "Content-Length"), ",")
            ], "|")
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "hello|yes|||" {
		t.Fatalf("chunked headers = %q, want hello|yes|||", got)
	}
}

func TestBunStdHTTPMatchesInterpreterWhitespaceURL(t *testing.T) {
	t.Setenv("SLICK_HTTP_CASE_URL", "\thttp://127.0.0.1/\n")
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "InvalidRequest" {
		t.Fatalf("whitespace URL = %q, want InvalidRequest", got)
	}
}

func TestBunStdHTTPMatchesInterpreterHeaderWhitespace(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nX-Test:   padded   \r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target })
        match Result {
            Ok(Response) => std.text.Join(HeaderValues(Response.Headers, "X-Test"), ",")
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "padded" {
		t.Fatalf("header whitespace = %q, want padded", got)
	}
}

const bunStdHTTPLimitedFetchProgram = `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target MaxResponseBytes: 3 })
        match Result {
            Ok(_) => "ok"
            Err(Failure) => Failure.Kind
        }
    }
}
`

func TestBunStdHTTPMatchesInterpreterTruncatedContentLength(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\nhi"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
	if got != "BodyRead" {
		t.Fatalf("truncated Content-Length = %q, want BodyRead", got)
	}
}

func TestBunStdHTTPMatchesInterpreterTruncatedChunk(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n64\r\nhi"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
	if got != "BodyRead" {
		t.Fatalf("truncated chunk = %q, want BodyRead", got)
	}
}

func TestBunStdHTTPMatchesInterpreterLongTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = response.Write([]byte("ok"))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target TimeoutMilliseconds: 3000000000 })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "ok" {
		t.Fatalf("long timeout = %q, want ok", got)
	}
}

func TestBunStdHTTPMatchesInterpreterUserAgentTrailer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, "ua="+strings.Join(request.Header["User-Agent"], ",")+"|tr="+request.Header.Get("Trailer"))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)

	cases := []struct {
		name    string
		headers string
		want    string
	}{
		{"multi", `"User-Agent": ["First", "Second"]`, "ua=First|tr="},
		{"empty", `"User-Agent": [""]`, "ua=|tr="},
		{"whitespace", `"User-Agent": ["  Custom  ", "Other"]`, "ua=Custom|tr="},
		{"trailer", `"Trailer": ["X-Trail"]`, "ua=Slick|tr="},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target Headers: map { `+test.headers+` } })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`)
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestBunStdHTTPMatchesInterpreterObsFold(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nX-Fold: hello\r\n world\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target })
        match Result {
            Ok(Response) => std.text.Join(HeaderValues(Response.Headers, "X-Fold"), ",")
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "hello world" {
		t.Fatalf("obs-fold = %q, want hello world", got)
	}
}

func TestBunStdHTTPMatchesInterpreterInvalidURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"percent", "http://127.0.0.1/%zz"},
		{"control", "http://127.0.0.1/\tfoo"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SLICK_HTTP_CASE_URL", test.raw)
			got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
			if got != "InvalidRequest" {
				t.Fatalf("URL %q = %q, want InvalidRequest", test.raw, got)
			}
		})
	}
}

func TestBunStdHTTPMatchesInterpreterIPv6Zone(t *testing.T) {
	t.Setenv("SLICK_HTTP_CASE_URL", "http://[fe80::1%25no-such-if]/")
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target TimeoutMilliseconds: 2000 })
        match Result {
            Ok(_) => "ok"
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got == "InvalidRequest" || got == "ok" || got == "missing" {
		t.Fatalf("IPv6 zone = %q, want a connect-time failure matching the interpreter", got)
	}
}

const bunStdHTTPInspectProgram = `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    let Method = std.env.Get("SLICK_HTTP_CASE_METHOD")
    if (Target == null) {
        "missing"
    } else {
        let Verb = "GET"
        if (Method != null) {
            Verb = Method
        }
        let Result = Fetch(HTTPRequest { Method: Verb URL: Target TimeoutMilliseconds: 9000000000 MaxResponseBytes: 256 FollowRedirects: true })
        match Result {
            Ok(Response) => std.text.Join([std.convert.IntToString(Response.Status), Response.URL, Text(Response.Body), std.text.Join(HeaderValues(Response.Headers, "Content-Length"), ","), std.text.Join(HeaderValues(Response.Headers, "Trailer"), ",")], "|")
            Err(Failure) => Failure.Kind
        }
    }
}
`

func TestBunStdHTTPMatchesInterpreterDurationSaturation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("ok"))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.HasPrefix(got, "200|") || !strings.Contains(got, "|ok|") {
		t.Fatalf("duration saturation = %q, want 200 ok", got)
	}
}
func TestBunStdHTTPMatchesInterpreterBackslashHost(t *testing.T) {
	t.Setenv("SLICK_HTTP_CASE_URL", `http://example.com\admin`)
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "InvalidRequest" {
		t.Fatalf("backslash host = %q, want InvalidRequest", got)
	}
}

func TestBunStdHTTPMatchesInterpreterNumericHost(t *testing.T) {
	t.Setenv("SLICK_HTTP_CASE_URL", "http://0x7f000001/")
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("numeric host = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterDefaultPortAndPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cl := strings.Join(request.Header.Values("Content-Length"), ",")
		conn := strings.Join(request.Header.Values("Connection"), ",")
		_, _ = response.Write([]byte(request.Host + "|" + request.RequestURI + "|cl=" + cl + "|conn=" + conn))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL+"/admin/../public#frag")
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.Contains(got, "/admin/../public") || strings.Contains(got, "#frag") {
		t.Fatalf("preserved URL = %q, want raw path without fragment", got)
	}
	if !strings.Contains(got, "|/admin/../public|cl=|conn=") {
		t.Fatalf("wire target = %q, want raw path no CL no Connection", got)
	}
}

func TestBunStdHTTPMatchesInterpreterExplicitDefaultPort(t *testing.T) {
	var gotHost, gotURI, gotCL, gotConn string
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotHost = request.Host
		gotURI = request.RequestURI
		gotCL = strings.Join(request.Header.Values("Content-Length"), ",")
		gotConn = strings.Join(request.Header.Values("Connection"), ",")
		_, _ = response.Write([]byte("ok"))
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")
	t.Setenv("SLICK_HTTP_CASE_URL", "http://example.test:80/x#frag")
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdHTTPInspectProgram}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun default port error = %v, output = %q", err, output)
	}
	if gotHost != "example.test:80" || !strings.Contains(gotURI, "example.test:80/x") || gotCL != "" || gotConn != "" {
		t.Fatalf("default port wire host=%q uri=%q cl=%q conn=%q", gotHost, gotURI, gotCL, gotConn)
	}
	if !strings.Contains(string(output), "http://example.test:80/x") {
		t.Fatalf("default port URL = %q, want :80", output)
	}
}

func TestBunStdHTTPMatchesInterpreterPostContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("cl=" + strings.Join(request.Header.Values("Content-Length"), ",")))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	t.Setenv("SLICK_HTTP_CASE_METHOD", "POST")
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.Contains(got, "cl=0") {
		t.Fatalf("POST empty body = %q, want Content-Length 0", got)
	}
}

func TestBunStdHTTPMatchesInterpreterOWSHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("note=" + request.Header.Get("X-Note")))
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", server.URL)
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target Headers: map { "X-Note": ["  padded  "] } })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "note=padded" {
		t.Fatalf("OWS trim = %q, want note=padded", got)
	}
}

func TestBunStdHTTPMatchesInterpreterProxyAuthorizationReplace(t *testing.T) {
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		values := request.Header.Values("Proxy-Authorization")
		_, _ = response.Write([]byte(strings.Join(values, ",") + "|count=" + strconv.Itoa(len(values))))
	}))
	defer proxy.Close()
	parsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("user", "pass")
	t.Setenv("HTTP_PROXY", parsed.String())
	t.Setenv("http_proxy", parsed.String())
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")
	t.Setenv("SLICK_HTTP_CASE_URL", "http://proxy.test.example/via")
	source := Source{Name: "main.slk", Namespace: "root", Text: `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "GET" URL: Target Headers: map { "Proxy-Authorization": ["Basic other"] } })
        match Result {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun proxy authorization error = %v, output = %q", err, output)
	}
	if string(output) != wantAuth+"|count=1\n" {
		t.Fatalf("proxy authorization = %q, want %q", output, wantAuth+"|count=1")
	}
}

func TestBunStdHTTPMatchesInterpreterRedirectBodyLimit(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("done"))
	}))
	defer final.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, final.URL+"/ok", http.StatusFound)
		_, _ = response.Write(bytesRepeat('x', 4096))
	}))
	defer origin.Close()
	t.Setenv("SLICK_HTTP_CASE_URL", origin.URL+"/start")
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.Contains(got, "|done|") {
		t.Fatalf("redirect oversized intermediate = %q, want followed to done", got)
	}
}

func TestBunStdHTTPMatchesInterpreterHugeContentLength(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 9223372036854775807\r\nConnection: close\r\n\r\n0123456789"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
	if got != "BodyTooLarge" {
		t.Fatalf("huge Content-Length = %q, want BodyTooLarge", got)
	}
}

func TestBunStdHTTPMatchesInterpreterDuplicateContentLength(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.HasSuffix(got, "|2|") && !strings.Contains(got, "|ok|2|") {
		t.Fatalf("duplicate equal Content-Length = %q, want single 2", got)
	}
}

func TestBunStdHTTPMatchesInterpreterConflictingPaddedLength(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nContent-Length: 05\r\nConnection: close\r\n\r\nhello"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("padded Content-Length = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterInvalidResponseHeader(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nX-Note: a\x00b\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("invalid response header = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterChunkExactLimit(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n4\r\nabcd"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeStall(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
	if got != "BodyTooLarge" {
		t.Fatalf("exact chunk limit = %q, want BodyTooLarge", got)
	}
}

func TestBunStdHTTPMatchesInterpreterChunkSizeToken(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"leading space", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n 1\r\nA\r\n0\r\n\r\n"},
		{"space before ext", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n1 ;x\r\nA\r\n0\r\n\r\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, test.payload))
			got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
			if got != "BodyRead" {
				t.Fatalf("chunk token %s = %q, want BodyRead", test.name, got)
			}
		})
	}
}

func TestBunStdHTTPMatchesInterpreterChunkLineLimit(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" + strings.Repeat("a", 5000)
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeStall(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPLimitedFetchProgram)
	if got != "BodyRead" {
		t.Fatalf("unbounded chunk line = %q, want BodyRead", got)
	}
}

func TestBunStdHTTPMatchesInterpreterNonChunkedTrailer(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nTrailer: X-Trail\r\nConnection: close\r\n\r\nok"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPInspectProgram)
	if !strings.Contains(got, "|ok|2|X-Trail") {
		t.Fatalf("non-chunked Trailer = %q, want kept X-Trail", got)
	}
}

func TestBunStdHTTPMatchesInterpreterForbiddenTrailer(t *testing.T) {
	payload := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: Content-Length\r\nConnection: close\r\n\r\n0\r\n\r\n"
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeRaw(t, payload))
	got := runBunHTTPProgram(t, bunStdHTTPCaseFetchProgram)
	if got != "Transport" {
		t.Fatalf("forbidden trailer declaration = %q, want Transport", got)
	}
}

func TestBunStdHTTPMatchesInterpreterExpectContinue(t *testing.T) {
	t.Setenv("SLICK_HTTP_CASE_URL", bunStdHTTPServeExpect(t))
	got := runBunHTTPProgram(t, `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function main() -> string effects { environment, network } {
    let Target = std.env.Get("SLICK_HTTP_CASE_URL")
    if (Target == null) {
        "missing"
    } else {
        let Result = Fetch(HTTPRequest { Method: "POST" URL: Target Headers: map { "Expect": ["100-continue"] } Body: std.bytes.FromUtf8("payload") TimeoutMilliseconds: 3000 })
        match Result {
            Ok(Response) => std.text.Join([std.convert.IntToString(Response.Status), Text(Response.Body)], "|")
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if got != "417|rejected" {
		t.Fatalf("expect continue = %q, want 417|rejected", got)
	}
}

func bunStdHTTPServeStall(t *testing.T, payload string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				buf := make([]byte, 4096)
				received := ""
				for {
					n, readErr := c.Read(buf)
					if n > 0 {
						received += string(buf[:n])
					}
					if strings.Contains(received, "\r\n\r\n") || readErr != nil {
						break
					}
				}
				_, _ = c.Write([]byte(payload))
				time.Sleep(2500 * time.Millisecond)
			}(conn)
		}
	}()
	return "http://" + listener.Addr().String()
}

func bunStdHTTPServeExpect(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				buf := make([]byte, 4096)
				received := ""
				for {
					n, readErr := c.Read(buf)
					if n > 0 {
						received += string(buf[:n])
					}
					if strings.Contains(received, "\r\n\r\n") || readErr != nil {
						break
					}
				}
				extra := make(chan bool, 1)
				go func() {
					_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
					n, _ := c.Read(buf)
					extra <- n > 0
				}()
				sent := <-extra
				if sent {
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\nsent"))
					return
				}
				_, _ = c.Write([]byte("HTTP/1.1 417 Expectation Failed\r\nContent-Length: 8\r\nConnection: close\r\n\r\nrejected"))
			}(conn)
		}
	}()
	return "http://" + listener.Addr().String()
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

const bunStdHTTPProgram = `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.HeaderValues as HeaderValues
use std.http.StatusText as StatusText

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function OptInt(Value: int?) -> string {
    if (Value == null) { "null" } else { std.convert.IntToString(Value) }
}

function OptText(Value: string?) -> string {
    if (Value == null) { "null" } else { Value }
}

function Bool(Value: bool) -> string {
    if (Value) { "true" } else { "false" }
}

function Kind(URL: string) -> string effects { network } {
    let Request = HTTPRequest { Method: "GET" URL: URL }
    match Fetch(Request) {
        Ok(Response) => "Ok:" + std.convert.IntToString(Response.Status)
        Err(Failure) => Failure.Kind + ":" + OptInt(Failure.Status)
    }
}

function Run(Base: string, Refused: string) -> Result<string, std.http.Failure> effects { network } {
    let Get = Fetch(HTTPRequest { Method: "GET" URL: Base + "/get" Headers: map { "X-Custom": ["abc"] } })?
    let Echo = Fetch(HTTPRequest {
        Method: "POST"
        URL: Base + "/echo"
        Headers: map {
            "X-Trace": ["one", "two"]
            "x-trace": ["three"]
        }
        Body: Get.Body
    })?
    let Status = Fetch(HTTPRequest { Method: "GET" URL: Base + "/status" })?
    let Redirect = Fetch(HTTPRequest { Method: "GET" URL: Base + "/redirect" })?
    let Followed = Fetch(HTTPRequest { Method: "GET" URL: Base + "/redirect" FollowRedirects: true })?
    Ok(std.text.Join([
        std.convert.IntToString(Get.Status),
        Text(Get.Body),
        std.text.Join(HeaderValues(Get.Headers, "X-Reply"), ","),
        std.text.Join(HeaderValues(Get.Headers, "set-cookie"), ","),
        std.convert.IntToString(Echo.Status),
        Text(Echo.Body),
        std.convert.IntToString(Status.Status),
        std.convert.IntToString(Redirect.Status),
        std.convert.IntToString(Followed.Status),
        Bool(std.text.EndsWith(Followed.URL, "/final")),
        Kind(Refused),
        OptText(StatusText(204)),
        OptText(StatusText(999))
    ], "|"))
}

function main() -> string effects { environment, network } {
    let Base = std.env.Get("SLICK_HTTP_TEST_URL")
    let Refused = std.env.Get("SLICK_HTTP_REFUSED_URL")
    if (Base == null) {
        "missing URL"
    } else {
        if (Refused == null) {
            "missing URL"
        } else {
            match Run(Base, Refused) {
                Ok(Output) => Output
                Err(Failure) => Failure.Kind + ":" + Failure.Message
            }
        }
    }
}
`
