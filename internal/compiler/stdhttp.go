package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	nativeStdHTTPFetch        runtimeOperationID = "std.http.Fetch"
	nativeStdHTTPHeaderValues runtimeOperationID = "std.http.HeaderValues"
	nativeStdHTTPStatusText   runtimeOperationID = "std.http.StatusText"

	stdHTTPRequestName  = "std.http.Request"
	stdHTTPResponseName = "std.http.Response"
	stdHTTPFailureName  = "std.http.Failure"

	defaultHTTPTimeoutMilliseconds int64 = 30_000
	defaultHTTPMaxResponseBytes    int64 = 8 * 1024 * 1024
)

type httpHeaderData struct {
	name   string
	values []string
}

type httpRequestData struct {
	method           string
	url              string
	headers          []httpHeaderData
	body             []byte
	bodyPresent      bool
	timeoutMillis    int64
	maxResponseBytes int64
	followRedirects  bool
}

type httpResponseData struct {
	status  int64
	url     string
	headers []httpHeaderData
	body    []byte
}

type httpFailureData struct {
	kind    string
	url     string
	status  *int64
	message string
}

var sharedHTTPTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return transport
}()

var errHTTPRedirect = errors.New("HTTP redirect failed")

func sanitizedHTTPURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func invalidHTTPRequest(rawURL, message string) *httpFailureData {
	return &httpFailureData{kind: "InvalidRequest", url: sanitizedHTTPURL(rawURL), message: message}
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHTTPFieldValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '\t' && (character < ' ' || character == 0x7f) {
			return false
		}
	}
	return true
}

func validateHTTPRequest(request httpRequestData) (*url.URL, http.Header, *httpFailureData) {
	if !validHTTPToken(request.method) {
		return nil, nil, invalidHTTPRequest(request.url, "method must be a non-empty HTTP token")
	}
	parsed, err := url.Parse(request.url)
	if err != nil || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, nil, invalidHTTPRequest(request.url, "URL must be an absolute http or https URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.User != nil {
		return nil, nil, invalidHTTPRequest(request.url, "URL userinfo is not allowed")
	}
	parsed.Fragment = ""
	if _, err := http.NewRequest(request.method, parsed.String(), nil); err != nil {
		return nil, nil, invalidHTTPRequest(request.url, "method or URL is invalid")
	}
	if request.timeoutMillis <= 0 {
		return nil, nil, invalidHTTPRequest(request.url, "TimeoutMilliseconds must be positive")
	}
	if request.maxResponseBytes <= 0 {
		return nil, nil, invalidHTTPRequest(request.url, "MaxResponseBytes must be positive")
	}

	headers := make(http.Header)
	restricted := map[string]bool{
		"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	}
	for _, header := range request.headers {
		canonical := http.CanonicalHeaderKey(header.name)
		if !validHTTPToken(header.name) || canonical == "" {
			return nil, nil, invalidHTTPRequest(request.url, "invalid header name")
		}
		if restricted[canonical] {
			return nil, nil, invalidHTTPRequest(request.url, canonical+" header cannot be controlled")
		}
		if len(header.values) == 0 {
			return nil, nil, invalidHTTPRequest(request.url, canonical+" header values must not be empty")
		}
		for _, value := range header.values {
			if !validHTTPFieldValue(value) {
				return nil, nil, invalidHTTPRequest(request.url, canonical+" header value contains a forbidden control byte")
			}
			headers.Add(canonical, value)
		}
	}
	if _, present := headers["User-Agent"]; !present {
		headers.Set("User-Agent", "Slick")
	}
	return parsed, headers, nil
}

func httpTimeoutDuration(milliseconds int64) time.Duration {
	if milliseconds > int64(math.MaxInt64/time.Millisecond) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func isHTTPTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func responseHTTPHeaders(headers http.Header) []httpHeaderData {
	sourceNames := make([]string, 0, len(headers))
	for name := range headers {
		sourceNames = append(sourceNames, name)
	}
	sort.Slice(sourceNames, func(left, right int) bool {
		leftCanonical := http.CanonicalHeaderKey(sourceNames[left])
		rightCanonical := http.CanonicalHeaderKey(sourceNames[right])
		if leftCanonical == rightCanonical {
			return sourceNames[left] < sourceNames[right]
		}
		return leftCanonical < rightCanonical
	})
	merged := make(map[string][]string, len(headers))
	for _, name := range sourceNames {
		canonical := http.CanonicalHeaderKey(name)
		merged[canonical] = append(merged[canonical], headers[name]...)
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]httpHeaderData, len(names))
	for index, name := range names {
		result[index] = httpHeaderData{name: name, values: merged[name]}
	}
	return result
}

func performHTTPRequest(request httpRequestData) (httpResponseData, *httpFailureData) {
	return performHTTPRequestContext(context.Background(), request)
}

func performHTTPRequestContext(parent context.Context, request httpRequestData) (httpResponseData, *httpFailureData) {
	parsed, headers, failure := validateHTTPRequest(request)
	if failure != nil {
		return httpResponseData{}, failure
	}

	ctx, cancel := context.WithTimeout(parent, httpTimeoutDuration(request.timeoutMillis))
	defer cancel()
	var body io.Reader
	if request.bodyPresent {
		body = struct{ io.Reader }{bytes.NewReader(request.body)}
	}
	nativeRequest, err := http.NewRequestWithContext(ctx, request.method, parsed.String(), body)
	if err != nil {
		return httpResponseData{}, invalidHTTPRequest(request.url, "method or URL is invalid")
	}
	if request.bodyPresent {
		nativeRequest.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(request.body)), nil
		}
	}
	nativeRequest.Header = headers

	client := &http.Client{Transport: sharedHTTPTransport}
	if request.followRedirects {
		client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errHTTPRedirect
			}
			return nil
		}
	} else {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}

	response, err := client.Do(nativeRequest)
	if err != nil {
		failureURL := sanitizedHTTPURL(request.url)
		var status *int64
		if response != nil {
			value := int64(response.StatusCode)
			status = &value
			if response.Request != nil && response.Request.URL != nil {
				failureURL = sanitizedHTTPURL(response.Request.URL.String())
			}
		}
		switch {
		case parent.Err() != nil:
			return httpResponseData{}, &httpFailureData{kind: "Cancelled", url: failureURL, status: status, message: "HTTP request cancelled"}
		case errors.Is(err, errHTTPRedirect) || (request.followRedirects && response != nil):
			return httpResponseData{}, &httpFailureData{kind: "Redirect", url: failureURL, status: status, message: "HTTP redirect failed"}
		case errors.Is(err, context.DeadlineExceeded) || isHTTPTimeout(err):
			return httpResponseData{}, &httpFailureData{kind: "Timeout", url: failureURL, status: status, message: "HTTP request timed out"}
		default:
			return httpResponseData{}, &httpFailureData{kind: "Transport", url: failureURL, status: status, message: "HTTP transport failed"}
		}
	}
	defer response.Body.Close()

	limit := request.maxResponseBytes
	if limit < math.MaxInt64 {
		limit++
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, limit))
	failureURL := sanitizedHTTPURL(response.Request.URL.String())
	status := int64(response.StatusCode)
	if err != nil {
		if parent.Err() != nil {
			return httpResponseData{}, &httpFailureData{kind: "Cancelled", url: failureURL, status: &status, message: "HTTP request cancelled"}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || isHTTPTimeout(err) {
			return httpResponseData{}, &httpFailureData{kind: "Timeout", url: failureURL, status: &status, message: "HTTP request timed out"}
		}
		return httpResponseData{}, &httpFailureData{kind: "BodyRead", url: failureURL, status: &status, message: "failed to read response body"}
	}
	if int64(len(contents)) > request.maxResponseBytes {
		return httpResponseData{}, &httpFailureData{
			kind: "BodyTooLarge", url: failureURL, status: &status,
			message: fmt.Sprintf("response body exceeds %d bytes", request.maxResponseBytes),
		}
	}
	return httpResponseData{
		status: status, url: response.Request.URL.String(),
		headers: responseHTTPHeaders(response.Header), body: contents,
	}, nil
}

func runtimeHTTPRequest(value runtimeValue) httpRequestData {
	request := httpRequestData{
		method:           value.fields["Method"].scalar.(string),
		url:              value.fields["URL"].scalar.(string),
		timeoutMillis:    defaultHTTPTimeoutMilliseconds,
		maxResponseBytes: defaultHTTPMaxResponseBytes,
	}
	if headers, present := runtimePresentValue(value.fields["Headers"]); present {
		request.headers = make([]httpHeaderData, len(headers.mapping.entries))
		for index, entry := range headers.mapping.entries {
			values := make([]string, len(entry.value.elements))
			for valueIndex, item := range entry.value.elements {
				values[valueIndex] = item.scalar.(string)
			}
			request.headers[index] = httpHeaderData{name: entry.key.scalar.(string), values: values}
		}
	}
	if body, present := runtimePresentValue(value.fields["Body"]); present {
		request.bodyPresent = true
		request.body = body.scalar.([]byte)
	}
	if timeout, present := runtimePresentValue(value.fields["TimeoutMilliseconds"]); present {
		request.timeoutMillis = timeout.scalar.(int64)
	}
	if limit, present := runtimePresentValue(value.fields["MaxResponseBytes"]); present {
		request.maxResponseBytes = limit.scalar.(int64)
	}
	if redirects, present := runtimePresentValue(value.fields["FollowRedirects"]); present {
		request.followRedirects = redirects.scalar.(bool)
	}
	return request
}

func runtimeHTTPFailure(resultType string, failure *httpFailureData) runtimeValue {
	status := runtimeValue{typ: "int?", optional: &runtimeOptional{}}
	if failure.status != nil {
		status.optional.present = true
		status.optional.value = runtimeValue{typ: "int", scalar: *failure.status}
	}
	value := runtimeValue{typ: stdHTTPFailureName, fields: map[string]runtimeValue{
		"Kind":    {typ: "string", scalar: failure.kind},
		"URL":     {typ: "string", scalar: failure.url},
		"Status":  status,
		"Message": {typ: "string", scalar: failure.message},
	}}
	return runtimeResultValue(resultType, false, value)
}

func runtimeHTTPResponse(resultType string, response httpResponseData) runtimeValue {
	entries := make([]runtimeMapEntry, len(response.headers))
	for index, header := range response.headers {
		values := make([]runtimeValue, len(header.values))
		for valueIndex, item := range header.values {
			values[valueIndex] = runtimeValue{typ: "string", scalar: item}
		}
		entries[index] = runtimeMapEntry{
			key:   runtimeValue{typ: "string", scalar: header.name},
			value: runtimeValue{typ: "string[]", elements: values},
		}
	}
	mapping, _ := newRuntimeMap(entries)
	value := runtimeValue{typ: stdHTTPResponseName, fields: map[string]runtimeValue{
		"Status":  {typ: "int", scalar: response.status},
		"URL":     {typ: "string", scalar: response.url},
		"Headers": {typ: "Map<string,string[]>", mapping: mapping},
		"Body":    {typ: "bytes", scalar: response.body},
	}}
	return runtimeResultValue(resultType, true, value)
}

func (p *program) callNativeStdHTTP(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdHTTPFetch:
		response, failure := performHTTPRequestContext(frame.ctx, runtimeHTTPRequest(frame.locals["Request"]))
		if failure != nil {
			return runtimeHTTPFailure(resultType, failure), nil, true
		}
		return runtimeHTTPResponse(resultType, response), nil, true
	case nativeStdHTTPHeaderValues:
		name := frame.locals["Name"].scalar.(string)
		for _, entry := range frame.locals["Headers"].mapping.entries {
			if !strings.EqualFold(entry.key.scalar.(string), name) {
				continue
			}
			return runtimeValue{typ: "string[]", elements: entry.value.elements}, nil, true
		}
		return runtimeValue{typ: "string[]", elements: []runtimeValue{}}, nil, true
	case nativeStdHTTPStatusText:
		status := frame.locals["Status"].scalar.(int64)
		text := ""
		if status >= 0 && status <= 999 {
			text = http.StatusText(int(status))
		}
		optional := &runtimeOptional{present: text != ""}
		if text != "" {
			optional.value = runtimeValue{typ: "string", scalar: text}
		}
		return runtimeValue{typ: "string?", optional: optional}, nil, true
	default:
		return runtimeValue{}, nil, false
	}
}

func (g *goGenerator) emitHTTPRuntimeSupport() {
	requestClass := goClassName(stdHTTPRequestName)
	responseClass := goClassName(stdHTTPResponseName)
	failureClass := goClassName(stdHTTPFailureName)
	resultType := g.goType("Result<" + stdHTTPResponseName + "," + stdHTTPFailureName + ">")

	g.line(`type slickHTTPHeader struct { name string; values []string }`)
	g.line(`type slickHTTPRequestData struct { method string; url string; headers []slickHTTPHeader; body slickBytes; bodyPresent bool; timeoutMillis int64; maxResponseBytes int64; followRedirects bool }`)
	g.line(`type slickHTTPResponseData struct { status int64; url string; headers []slickHTTPHeader; body slickBytes }`)
	g.line(`type slickHTTPFailureData struct { kind string; url string; status *int64; message string }`)
	g.line(`var slickHTTPTransport = func() *http.Transport { transport := http.DefaultTransport.(*http.Transport).Clone(); transport.DisableCompression = true; return transport }()`)
	g.line(`var slickHTTPRedirectError = errors.New("HTTP redirect failed")`)
	g.line(`func slickHTTPSanitizedURL(raw string) string {`)
	g.line(`parsed, err := url.Parse(raw); if err != nil { return "" }`)
	g.line(`parsed.User = nil; parsed.RawQuery = ""; parsed.ForceQuery = false; parsed.Fragment = ""`)
	g.line(`return parsed.String()`)
	g.line(`}`)
	g.line(`func slickHTTPInvalid(rawURL, message string) *slickHTTPFailureData { return &slickHTTPFailureData{kind: "InvalidRequest", url: slickHTTPSanitizedURL(rawURL), message: message} }`)
	g.line("func slickHTTPValidToken(value string) bool { if value == \"\" { return false }; for index := 0; index < len(value); index++ { character := value[index]; if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune(\"!#$%%&'*+-.^_`|~\", rune(character)) { continue }; return false }; return true }")
	g.line(`func slickHTTPValidFieldValue(value string) bool { for index := 0; index < len(value); index++ { character := value[index]; if character != '\t' && (character < ' ' || character == 0x7f) { return false } }; return true }`)
	g.line(`func slickHTTPValidate(request slickHTTPRequestData) (*url.URL, http.Header, *slickHTTPFailureData) {`)
	g.line(`if !slickHTTPValidToken(request.method) { return nil, nil, slickHTTPInvalid(request.url, "method must be a non-empty HTTP token") }`)
	g.line(`parsed, err := url.Parse(request.url)`)
	g.line(`if err != nil || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Host == "" || parsed.Hostname() == "" { return nil, nil, slickHTTPInvalid(request.url, "URL must be an absolute http or https URL") }`)
	g.line(`parsed.Scheme = strings.ToLower(parsed.Scheme)`)
	g.line(`if parsed.User != nil { return nil, nil, slickHTTPInvalid(request.url, "URL userinfo is not allowed") }`)
	g.line(`parsed.Fragment = ""`)
	g.line(`if _, err := http.NewRequest(request.method, parsed.String(), nil); err != nil { return nil, nil, slickHTTPInvalid(request.url, "method or URL is invalid") }`)
	g.line(`if request.timeoutMillis <= 0 { return nil, nil, slickHTTPInvalid(request.url, "TimeoutMilliseconds must be positive") }`)
	g.line(`if request.maxResponseBytes <= 0 { return nil, nil, slickHTTPInvalid(request.url, "MaxResponseBytes must be positive") }`)
	g.line(`headers := make(http.Header)`)
	g.line(`restricted := map[string]bool{"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true}`)
	g.line(`for _, header := range request.headers {`)
	g.line(`canonical := http.CanonicalHeaderKey(header.name)`)
	g.line(`if !slickHTTPValidToken(header.name) || canonical == "" { return nil, nil, slickHTTPInvalid(request.url, "invalid header name") }`)
	g.line(`if restricted[canonical] { return nil, nil, slickHTTPInvalid(request.url, canonical + " header cannot be controlled") }`)
	g.line(`if len(header.values) == 0 { return nil, nil, slickHTTPInvalid(request.url, canonical + " header values must not be empty") }`)
	g.line(`for _, value := range header.values { if !slickHTTPValidFieldValue(value) { return nil, nil, slickHTTPInvalid(request.url, canonical + " header value contains a forbidden control byte") }; headers.Add(canonical, value) }`)
	g.line(`}`)
	g.line(`if _, present := headers["User-Agent"]; !present { headers.Set("User-Agent", "Slick") }`)
	g.line(`return parsed, headers, nil`)
	g.line(`}`)
	g.line(`func slickHTTPTimeoutDuration(milliseconds int64) time.Duration { if milliseconds > int64(math.MaxInt64 / time.Millisecond) { return time.Duration(math.MaxInt64) }; return time.Duration(milliseconds) * time.Millisecond }`)
	g.line(`func slickHTTPIsTimeout(err error) bool { var timeout interface{ Timeout() bool }; return errors.As(err, &timeout) && timeout.Timeout() }`)
	g.line(`func slickHTTPResponseHeaders(headers http.Header) []slickHTTPHeader {`)
	g.line(`sourceNames := make([]string, 0, len(headers)); for name := range headers { sourceNames = append(sourceNames, name) }`)
	g.line(`sort.Slice(sourceNames, func(left, right int) bool { leftCanonical := http.CanonicalHeaderKey(sourceNames[left]); rightCanonical := http.CanonicalHeaderKey(sourceNames[right]); if leftCanonical == rightCanonical { return sourceNames[left] < sourceNames[right] }; return leftCanonical < rightCanonical })`)
	g.line(`merged := make(map[string][]string, len(headers)); for _, name := range sourceNames { canonical := http.CanonicalHeaderKey(name); merged[canonical] = append(merged[canonical], headers[name]...) }`)
	g.line(`names := make([]string, 0, len(merged)); for name := range merged { names = append(names, name) }; sort.Strings(names)`)
	g.line(`result := make([]slickHTTPHeader, len(names)); for index, name := range names { result[index] = slickHTTPHeader{name: name, values: merged[name]} }; return result`)
	g.line(`}`)
	g.line(`func slickHTTPPerform(parent context.Context, request slickHTTPRequestData) (slickHTTPResponseData, *slickHTTPFailureData) {`)
	g.line(`parsed, headers, failure := slickHTTPValidate(request); if failure != nil { return slickHTTPResponseData{}, failure }`)
	g.line(`ctx, cancel := context.WithTimeout(parent, slickHTTPTimeoutDuration(request.timeoutMillis)); defer cancel()`)
	g.line(`var body io.Reader; if request.bodyPresent { body = struct{ io.Reader }{bytes.NewReader(request.body)} }`)
	g.line(`nativeRequest, err := http.NewRequestWithContext(ctx, request.method, parsed.String(), body); if err != nil { return slickHTTPResponseData{}, slickHTTPInvalid(request.url, "method or URL is invalid") }`)
	g.line(`if request.bodyPresent { nativeRequest.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(request.body)), nil } }`)
	g.line(`nativeRequest.Header = headers`)
	g.line(`client := &http.Client{Transport: slickHTTPTransport}`)
	g.line(`if request.followRedirects { client.CheckRedirect = func(_ *http.Request, via []*http.Request) error { if len(via) >= 10 { return slickHTTPRedirectError }; return nil } } else { client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse } }`)
	g.line(`response, err := client.Do(nativeRequest)`)
	g.line(`if err != nil {`)
	g.line(`failureURL := slickHTTPSanitizedURL(request.url); var status *int64`)
	g.line(`if response != nil { value := int64(response.StatusCode); status = &value; if response.Request != nil && response.Request.URL != nil { failureURL = slickHTTPSanitizedURL(response.Request.URL.String()) } }`)
	g.line(`if parent.Err() != nil { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Cancelled", url: failureURL, status: status, message: "HTTP request cancelled"} }`)
	g.line(`if errors.Is(err, slickHTTPRedirectError) || (request.followRedirects && response != nil) { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Redirect", url: failureURL, status: status, message: "HTTP redirect failed"} }`)
	g.line(`if errors.Is(err, context.DeadlineExceeded) || slickHTTPIsTimeout(err) { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Timeout", url: failureURL, status: status, message: "HTTP request timed out"} }`)
	g.line(`return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Transport", url: failureURL, status: status, message: "HTTP transport failed"}`)
	g.line(`}`)
	g.line(`defer response.Body.Close()`)
	g.line(`limit := request.maxResponseBytes; if limit < math.MaxInt64 { limit++ }`)
	g.line(`contents, err := io.ReadAll(io.LimitReader(response.Body, limit)); failureURL := slickHTTPSanitizedURL(response.Request.URL.String()); status := int64(response.StatusCode)`)
	g.line(`if err != nil { if parent.Err() != nil { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Cancelled", url: failureURL, status: &status, message: "HTTP request cancelled"} }; if errors.Is(ctx.Err(), context.DeadlineExceeded) || slickHTTPIsTimeout(err) { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "Timeout", url: failureURL, status: &status, message: "HTTP request timed out"} }; return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "BodyRead", url: failureURL, status: &status, message: "failed to read response body"} }`)
	g.line(`if int64(len(contents)) > request.maxResponseBytes { return slickHTTPResponseData{}, &slickHTTPFailureData{kind: "BodyTooLarge", url: failureURL, status: &status, message: fmt.Sprintf("response body exceeds %%d bytes", request.maxResponseBytes)} }`)
	g.line(`return slickHTTPResponseData{status: status, url: response.Request.URL.String(), headers: slickHTTPResponseHeaders(response.Header), body: slickBytes(contents)}, nil`)
	g.line(`}`)
	g.line("func slickHTTPFetch(ctx context.Context, request %s) (%s, error) {", requestClass, resultType)
	g.line(`data := slickHTTPRequestData{method: request.%s, url: request.%s, timeoutMillis: %d, maxResponseBytes: %d}`, goFieldName("Method"), goFieldName("URL"), defaultHTTPTimeoutMilliseconds, defaultHTTPMaxResponseBytes)
	g.line(`if request.%s.present { data.headers = make([]slickHTTPHeader, len(request.%s.value.entries)); for index, entry := range request.%s.value.entries { data.headers[index] = slickHTTPHeader{name: entry.key, values: entry.value} } }`, goFieldName("Headers"), goFieldName("Headers"), goFieldName("Headers"))
	g.line(`if request.%s.present { data.bodyPresent = true; data.body = request.%s.value }`, goFieldName("Body"), goFieldName("Body"))
	g.line(`if request.%s.present { data.timeoutMillis = request.%s.value }`, goFieldName("TimeoutMilliseconds"), goFieldName("TimeoutMilliseconds"))
	g.line(`if request.%s.present { data.maxResponseBytes = request.%s.value }`, goFieldName("MaxResponseBytes"), goFieldName("MaxResponseBytes"))
	g.line(`if request.%s.present { data.followRedirects = request.%s.value }`, goFieldName("FollowRedirects"), goFieldName("FollowRedirects"))
	g.line(`response, failure := slickHTTPPerform(ctx, data)`)
	g.line(`if failure != nil { status := slickNone[int64](); if failure.status != nil { status = slickSome(*failure.status) }; return %s{failure: &%s{%s: failure.kind, %s: failure.url, %s: status, %s: failure.message}}, nil`, resultType, failureClass, goFieldName("Kind"), goFieldName("URL"), goFieldName("Status"), goFieldName("Message"))
	g.line(`}`)
	g.line(`entries := make([]slickMapEntry[string, []string], len(response.headers)); for index, header := range response.headers { entries[index] = slickMapEntry[string, []string]{key: header.name, value: header.values} }`)
	g.line(`return %s{ok: true, value: %s{%s: response.status, %s: response.url, %s: slickMapOf(entries...), %s: response.body}}, nil`, resultType, responseClass, goFieldName("Status"), goFieldName("URL"), goFieldName("Headers"), goFieldName("Body"))
	g.line(`}`)
	g.line(`func slickHTTPHeaderValues(headers slickMap[string, []string], name string) []string { for _, entry := range headers.entries { if strings.EqualFold(entry.key, name) { return entry.value } }; return []string{} }`)
	g.line(`func slickHTTPStatusText(status int64) slickOptional[string] { if status < 0 || status > 999 { return slickNone[string]() }; text := http.StatusText(int(status)); if text == "" { return slickNone[string]() }; return slickSome(text) }`)
}
