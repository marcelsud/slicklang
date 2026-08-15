package compiler_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStdHTTPSuccessEverywhere(t *testing.T) {
	var connection struct {
		sync.Mutex
		remote string
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/binary":
			if request.Body == http.NoBody {
				response.Header().Set("X-No-Body", "true")
			}
			response.Header().Add("Set-Cookie", "a=1")
			response.Header().Add("Set-Cookie", "b=2")
			response.Header().Set("X-Zed", "last")
			response.Header().Set("X-Alpha", "first")
			_, _ = response.Write([]byte{0, 255, 1})
		case "/echo":
			body, _ := io.ReadAll(request.Body)
			valid := request.Method == "POST" && string(body) == string([]byte{0, 255, 1}) &&
				strings.Join(request.Header.Values("X-Trace"), ",") == "one,two,three" && request.UserAgent() == "Slick"
			response.Header().Add("X-Reply", "a")
			response.Header().Add("X-Reply", "b")
			if !valid {
				http.Error(response, "invalid echo request", http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write(body)
		case "/text":
			body, _ := io.ReadAll(request.Body)
			_, _ = response.Write(body)
		case "/empty-request":
			body, _ := io.ReadAll(request.Body)
			if len(body) == 0 && len(request.TransferEncoding) > 0 {
				_, _ = response.Write([]byte("present"))
			} else {
				_, _ = response.Write([]byte("absent"))
			}
		case "/empty-response":
			response.WriteHeader(http.StatusNoContent)
		case "/chunked":
			flusher := response.(http.Flusher)
			_, _ = response.Write([]byte("ab"))
			flusher.Flush()
			_, _ = response.Write([]byte("cd"))
		case "/redirect":
			http.Redirect(response, request, "/final?done=1", http.StatusFound)
		case "/final":
			response.WriteHeader(http.StatusNoContent)
		case "/missing":
			http.Error(response, "missing", http.StatusNotFound)
		case "/failure":
			http.Error(response, "failure", http.StatusInternalServerError)
		case "/method":
			_, _ = response.Write([]byte(request.Method))
		case "/conn/start":
			connection.Lock()
			connection.remote = request.RemoteAddr
			connection.Unlock()
			_, _ = response.Write([]byte("start"))
		case "/conn/check":
			connection.Lock()
			same := connection.remote == request.RemoteAddr
			connection.Unlock()
			if same {
				_, _ = response.Write([]byte("same"))
			} else {
				_, _ = response.Write([]byte("new"))
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("SLICK_HTTP_TEST_URL", server.URL)

	source := `
use std.http.Fetch as Fetch
use std.http.Request as HTTPRequest
use std.http.Failure as HTTPFailure
use std.http.HeaderValues as HeaderValues
use std.http.StatusText as StatusText

function Text(Value: bytes) -> string {
    match std.bytes.ToUtf8(Value) {
        Ok(Output) => Output
        Err(_) => "invalid"
    }
}

function OptionalInt(Value: int?) -> string {
    if (Value == null) { "null" } else { std.convert.IntToString(Value) }
}

function OptionalText(Value: string?) -> string {
    if (Value == null) { "null" } else { Value }
}

function Bool(Value: bool) -> string {
    if (Value) { "true" } else { "false" }
}

function Run(Base: string) -> Result<string, HTTPFailure> effects { network } {
    let Binary = Fetch(HTTPRequest { Method: "GET" URL: Base + "/binary" })?
    let Echo = Fetch(HTTPRequest {
        Method: "POST"
        URL: Base + "/echo"
        Headers: map {
            "X-Trace": ["one", "two"]
            "x-trace": ["three"]
        }
        Body: Binary.Body
    })?
    let TextEcho = Fetch(HTTPRequest { Method: "POST" URL: Base + "/text" Body: std.bytes.FromUtf8("hello") })?
    let EmptyRequest = Fetch(HTTPRequest { Method: "POST" URL: Base + "/empty-request" Body: std.bytes.FromUtf8("") })?
    let EmptyResponse = Fetch(HTTPRequest { Method: "GET" URL: Base + "/empty-response" })?
    let Chunked = Fetch(HTTPRequest { Method: "GET" URL: Base + "/chunked" })?
    let Redirect = Fetch(HTTPRequest { Method: "GET" URL: Base + "/redirect" })?
    let Followed = Fetch(HTTPRequest { Method: "GET" URL: Base + "/redirect" FollowRedirects: true })?
    let Missing = Fetch(HTTPRequest { Method: "GET" URL: Base + "/missing" })?
    let Failed = Fetch(HTTPRequest { Method: "GET" URL: Base + "/failure" })?
    let Method = Fetch(HTTPRequest { Method: "PROPFIND" URL: Base + "/method" })?
    let Start = Fetch(HTTPRequest { Method: "GET" URL: Base + "/conn/start" })?
    let Connection = Fetch(HTTPRequest { Method: "GET" URL: Base + "/conn/check" })?

    Ok(std.text.Join([
        std.convert.IntToString(Binary.Status),
        std.convert.IntToString(std.bytes.Length(Binary.Body)),
        OptionalInt(std.bytes.At(Binary.Body, 1)),
        std.text.Join(HeaderValues(Binary.Headers, "set-cookie"), ","),
        std.text.Join(HeaderValues(Echo.Headers, "X-REPLY"), ","),
        Text(Echo.Body),
        Text(TextEcho.Body),
        Text(EmptyRequest.Body),
        std.convert.IntToString(std.bytes.Length(EmptyResponse.Body)),
        Text(Chunked.Body),
        std.convert.IntToString(Redirect.Status),
        std.convert.IntToString(Followed.Status),
        Bool(std.text.EndsWith(Followed.URL, "/final?done=1")),
        std.convert.IntToString(Missing.Status),
        std.convert.IntToString(Failed.Status),
        Text(Method.Body),
        Text(Connection.Body),
        std.text.Join(HeaderValues(Binary.Headers, "x-no-body"), ","),
        OptionalText(StatusText(204)),
        OptionalText(StatusText(999)),
    ], "|"))
}

function main() -> string effects { environment, network } {
    let Base = std.env.Get("SLICK_HTTP_TEST_URL")
    if (Base == null) {
        "missing URL"
    } else {
        match Run(Base) {
            Ok(Output) => Output
            Err(Failure) => Failure.Kind + ":" + Failure.Message
        }
    }
}
`
	output := runResultEverywhere(t, source)
	expected := "200|3|255|a=1,b=2|a,b|invalid|hello|present|0|abcd|302|204|true|404|500|PROPFIND|same|true|No Content|null"
	if output != expected {
		t.Fatalf("std.http success flow produced %q", output)
	}
}

func TestStdHTTPCallableDiagnostics(t *testing.T) {
	tests := map[string]struct {
		body    string
		code    string
		message string
	}{
		"Fetch arity": {
			body: `std.http.Fetch()`,
			code: "SLK320", message: "Fetch expects 1 arguments, found 0",
		},
		"Fetch request type": {
			body: `std.http.Fetch("not a request")`,
			code: "SLK320", message: "argument 1 to std.http.Fetch must be Request, found string",
		},
		"HeaderValues header type": {
			body: `std.http.HeaderValues(map { "X": "value" }, "X")`,
			code: "SLK320", message: "argument 1 to std.http.HeaderValues must be Map<string, string[]>",
		},
		"StatusText status type": {
			body: `std.http.StatusText("200")`,
			code: "SLK320", message: "argument 1 to std.http.StatusText must be int, found string",
		},
		"Request required URL": {
			body: `std.http.Request { Method: "GET" }`,
			code: "SLK376", message: "Request requires field URL of type string",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			diagnostics := checkResult(t, "function main() -> null { "+test.body+" null }")
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

func TestStdHTTPFailuresEverywhere(t *testing.T) {
	var validationHits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/validation":
			validationHits.Add(1)
			_, _ = response.Write([]byte("unexpected network activity"))
		case "/slow-headers":
			time.Sleep(80 * time.Millisecond)
			_, _ = response.Write([]byte("late"))
		case "/slow-body":
			response.Header().Set("Content-Length", "4")
			response.WriteHeader(http.StatusOK)
			response.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
			_, _ = response.Write([]byte("late"))
		case "/large":
			_, _ = response.Write([]byte("four"))
		case "/short":
			hijacker := response.(http.Hijacker)
			connection, buffer, err := hijacker.Hijack()
			if err != nil {
				return
			}
			_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nabc")
			_ = buffer.Flush()
			_ = connection.Close()
		case "/loop":
			http.Redirect(response, request, "/loop", http.StatusFound)
		case "/chain":
			step, _ := strconv.Atoi(request.URL.Query().Get("n"))
			http.Redirect(response, request, "/chain?n="+strconv.Itoa(step+1), http.StatusFound)
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
	_ = listener.Close()

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("secure"))
	}))
	defer tlsServer.Close()

	t.Setenv("SLICK_HTTP_TEST_URL", server.URL)
	t.Setenv("SLICK_HTTP_REFUSED_URL", refusedURL)
	t.Setenv("SLICK_HTTP_TLS_URL", tlsServer.URL)

	source := `
function Kind(Request: std.http.Request) -> string effects { network } {
    match std.http.Fetch(Request) {
        Ok(_) => "Ok"
        Err(Failure) => Failure.Kind
    }
}

function Detail(Request: std.http.Request) -> string effects { network } {
    match std.http.Fetch(Request) {
        Ok(_) => "Ok"
        Err(Failure) => Failure.Kind + ":" + Failure.URL + ":" + Failure.Message
    }
}

function DescribeFailure(Failure: std.http.Failure) -> string {
    if (std.text.Contains(Failure.URL, "TOP_SECRET") || std.text.Contains(Failure.Message, "TOP_SECRET")) {
        "LEAKED"
    } else {
        let Status = Failure.Status
        if (Status == null) {
            Failure.Kind + ":null"
        } else {
            Failure.Kind + ":" + std.convert.IntToString(Status)
        }
    }
}

function Summary(Request: std.http.Request) -> string effects { network } {
    match std.http.Fetch(Request) {
        Ok(_) => "Ok"
        Err(Failure) => DescribeFailure(Failure)
    }
}

function EmptyHeaders() -> Map<string, string[]> {
    map { "X-Test": std.text.Split("", "") }
}

function main() -> string effects { environment, network } {
    let Base = std.env.Get("SLICK_HTTP_TEST_URL")
    let Refused = std.env.Get("SLICK_HTTP_REFUSED_URL")
    let TLS = std.env.Get("SLICK_HTTP_TLS_URL")
    if (Base == null) {
        "missing URL"
    } else {
        if (Refused == null) {
            "missing URL"
        } else {
            if (TLS == null) {
                "missing URL"
            } else {
                std.text.Join([
                    Kind(std.http.Request { Method: "GET" URL: "relative" }),
                    Kind(std.http.Request { Method: "GET" URL: "ftp://example.com/file" }),
                    Detail(std.http.Request { Method: "GET" URL: "http://user:TOP_SECRET@example.com/path?QUERY_SECRET#fragment" }),
                    Kind(std.http.Request { Method: "" URL: Base + "/validation" }),
                    Kind(std.http.Request { Method: "BAD METHOD" URL: Base + "/validation" }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" Headers: map { "Bad Name": ["value"] } }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" Headers: map { "X-Test": ["safe\r\nInjected: yes"] } }),
					Kind(std.http.Request { Method: "GET" URL: Base + "/validation" Headers: map { "X-Test": ["nul\u0000del\u007f"] } }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" Headers: EmptyHeaders() }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" Headers: map { "Content-Length": ["3"] } }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" TimeoutMilliseconds: 0 }),
                    Kind(std.http.Request { Method: "GET" URL: Base + "/validation" MaxResponseBytes: 0 }),
                    Summary(std.http.Request { Method: "GET" URL: Refused }),
                    Summary(std.http.Request { Method: "GET" URL: TLS }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/slow-headers?TOP_SECRET" TimeoutMilliseconds: 20 }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/slow-body?TOP_SECRET" TimeoutMilliseconds: 20 }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/large?TOP_SECRET" MaxResponseBytes: 3 }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/short?TOP_SECRET" }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/loop?TOP_SECRET" FollowRedirects: true }),
                    Summary(std.http.Request { Method: "GET" URL: Base + "/chain?n=0&TOP_SECRET" FollowRedirects: true }),
                ], "|")
            }
        }
    }
}
`
	output := runResultEverywhere(t, source)
	parts := strings.Split(output, "|")
	want := []string{
		"InvalidRequest", "InvalidRequest", "InvalidRequest:http://example.com/path:URL userinfo is not allowed",
		"InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest", "InvalidRequest",
		"Transport:null", "Transport:null", "Timeout:null", "Timeout:200", "BodyTooLarge:200", "BodyRead:200", "Redirect:302", "Redirect:302",
	}
	if fmt.Sprint(parts) != fmt.Sprint(want) {
		t.Fatalf("std.http failures = %q\nwant %q", parts, want)
	}
	if validationHits.Load() != 0 {
		t.Fatalf("invalid requests performed %d network calls", validationHits.Load())
	}
	for _, secret := range []string{"TOP_SECRET", "QUERY_SECRET", "user:"} {
		if strings.Contains(output, secret) {
			t.Fatalf("failure output exposed %q: %q", secret, output)
		}
	}
}

func TestStdHTTPResultControlFlowEverywhere(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv("SLICK_HTTP_TEST_URL", server.URL)

	output := runResultEverywhere(t, `
function Inner(URL: string) -> Result<int, std.http.Failure> effects { network } {
    let Response = std.http.Fetch(std.http.Request { Method: "GET" URL: URL MaxResponseBytes: 1 })?
    Ok(Response.Status)
}
function Outer(URL: string) -> Result<int, std.http.Failure> effects { network } {
    let Status = Inner(URL)?
    Ok(Status)
}
function Recover(URL: string) -> Result<int, std.http.Failure> effects { network } {
    Outer(URL) catch (error) {
        std.http.Failure => Ok(999)
    }
}
function main() -> string effects { environment, network } {
    let URL = std.env.Get("SLICK_HTTP_TEST_URL")
    if (URL == null) {
        "missing URL"
    } else {
        match Recover(URL) {
            Ok(Status) => std.convert.IntToString(Status)
            Err(Failure) => Failure.Kind
        }
    }
}
`)
	if output != "BodyTooLarge" {
		t.Fatalf("HTTP Result control flow produced %q", output)
	}
}
