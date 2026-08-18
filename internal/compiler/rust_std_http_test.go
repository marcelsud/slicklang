package compiler

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// rustStdHTTPProgram exercises std.http.Fetch, std.http.HeaderValues, and
// std.http.StatusText against a local server: a successful GET with request
// headers and a response body, a POST that echoes a request body with
// canonicalized multi-case request headers and the Slick user agent, a non-2xx
// status, a redirect decision (not followed and followed), a transport failure
// against a closed port, and the status-text table. It uses fully-qualified
// standard-library calls and string concatenation (no interpolation) so the
// program needs no use imports beyond the convenience aliases and the Go raw
// string never contains a backtick.
const rustStdHTTPProgram = `use std.http.Fetch as Fetch
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

func TestRustStdHTTPMatchesInterpreter(t *testing.T) {
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

	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdHTTPProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	want := "200|hello|yes|a=1,b=2|201|hello|404|302|200|true|Transport:null|No Content|null"
	if interpreted != want {
		t.Fatalf("interpreter output = %q, want %q", interpreted, want)
	}

	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Rust binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust output = %q, want interpreter output %q", output, interpreted+"\n")
	}
}

// rustStdHTTPProxyProgram fetches a non-loopback URL so HTTP_PROXY is eligible.
// When SLICK_HTTP_PROXY_URL is set, the program installs it through std.env.Set
// so an overlay assignment is visible to Fetch. The body or Failure.Kind is
// printed so a local proxy or a NO_PROXY bypass can be observed without
// interpolation or a backtick in this raw string.
const rustStdHTTPProxyProgram = `use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function InstallOverlayProxy(Proxy: string?) -> null effects { environment } {
    if (Proxy == null) {
        null
    } else {
        if (Proxy == "") {
            null
        } else {
            let _ = std.env.Set("HTTP_PROXY", Proxy)
            let _ = std.env.Set("http_proxy", Proxy)
            let _ = std.env.Unset("HTTPS_PROXY")
            let _ = std.env.Unset("https_proxy")
            let _ = std.env.Unset("NO_PROXY")
            let _ = std.env.Unset("no_proxy")
            let _ = std.env.Unset("ALL_PROXY")
            let _ = std.env.Unset("all_proxy")
            null
        }
    }
}

function main() -> string effects { environment, network } {
    let URL = std.env.Get("SLICK_HTTP_PROXY_TARGET")
    let _ = InstallOverlayProxy(std.env.Get("SLICK_HTTP_PROXY_URL"))
    if (URL == null) {
        "missing URL"
    } else {
        let Request = HTTPRequest { Method: "GET" URL: URL TimeoutMilliseconds: 1500 }
        match Fetch(Request) {
            Ok(Response) => Text(Response.Body)
            Err(Failure) => Failure.Kind
        }
    }
}
`

func TestRustStdHTTPMatchesInterpreterCancellation(t *testing.T) {
	generated := mustGenerateRust(t, rustCoreForTest(t, `
function main() -> Result<std.http.Response, std.http.Failure> effects { network } {
    std.http.Fetch(std.http.Request { Method: "GET" URL: "http://127.0.0.1:1" })
}
`))
	if !strings.Contains(generated, `slick_http_failure("Cancelled"`) {
		t.Fatal("std.http.Fetch must return typed std.http.Failure{Kind: Cancelled}")
	}
	if !strings.Contains(generated, `"HTTP request cancelled"`) {
		t.Fatal("cancelled failure message must match the interpreter")
	}
	if strings.Contains(generated, "slick_cancelled(context)") {
		t.Fatal("std.http.Fetch must not throw a runtime cancellation")
	}
}

func TestRustStdHTTPMatchesInterpreterProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodConnect {
			hijacker, ok := response.(http.Hijacker)
			if !ok {
				http.Error(response, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			connection, buffer, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return
			}
			if err := buffer.Flush(); err != nil {
				return
			}
			tunneled, err := http.ReadRequest(buffer.Reader)
			if err != nil {
				return
			}
			_ = tunneled.Body.Close()
			_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 9\r\n\r\nvia-proxy")
			_ = buffer.Flush()
			return
		}
		_, _ = response.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdHTTPProxyProgram}
	binary := buildRustTestProgram(t, source)
	target := "http://proxy.test.example/via"

	t.Run("honours HTTP_PROXY", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", target)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		if string(output) != "via-proxy\n" {
			t.Fatalf("Rust HTTP_PROXY output = %q, want %q", output, "via-proxy\n")
		}
	})

	t.Run("honours NO_PROXY bypass", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "proxy.test.example")
		t.Setenv("no_proxy", "proxy.test.example")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", target)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours NO_PROXY CIDR bypass", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "10.0.0.0/8")
		t.Setenv("no_proxy", "10.0.0.0/8")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", "http://10.1.2.3/via")
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours NO_PROXY IPv6 CIDR bypass", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "2001:db8::/32")
		t.Setenv("no_proxy", "2001:db8::/32")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", "http://[2001:db8::1]/via")
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours NO_PROXY default http port", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "proxy.test.example:80")
		t.Setenv("no_proxy", "proxy.test.example:80")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", target)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours NO_PROXY default https port", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", proxy.URL)
		t.Setenv("https_proxy", proxy.URL)
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "proxy.test.example:443")
		t.Setenv("no_proxy", "proxy.test.example:443")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", "https://proxy.test.example/via")
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours NO_PROXY bracketed IPv6 authority", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("http_proxy", proxy.URL)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "[2001:db8::10]:8443")
		t.Setenv("no_proxy", "[2001:db8::10]:8443")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", "http://[2001:db8::10]:8443/via")
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		rustStdHTTPAssertProxyBypassed(t, output)
	})

	t.Run("honours overlay HTTP_PROXY", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("http_proxy", "")
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("https_proxy", "")
		t.Setenv("ALL_PROXY", "")
		t.Setenv("all_proxy", "")
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")
		t.Setenv("SLICK_HTTP_PROXY_TARGET", target)
		t.Setenv("SLICK_HTTP_PROXY_URL", proxy.URL)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Rust binary error = %v, output = %q", err, output)
		}
		if string(output) != "via-proxy\n" {
			t.Fatalf("Rust overlay HTTP_PROXY output = %q, want %q", output, "via-proxy\n")
		}
	})
}

func rustStdHTTPAssertProxyBypassed(t *testing.T, output []byte) {
	t.Helper()
	got := string(output)
	if got == "via-proxy\n" {
		t.Fatal("request was sent through HTTP_PROXY; NO_PROXY should have bypassed it")
	}
	if got != "Transport\n" && got != "Timeout\n" {
		t.Fatalf("Rust NO_PROXY output = %q, want Transport or Timeout", output)
	}
}
