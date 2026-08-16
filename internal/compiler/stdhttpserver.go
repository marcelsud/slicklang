package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	defaultHTTPServerMaxHeaderBytes                int64 = 1 << 20
	defaultHTTPServerMaxBodyBytes                  int64 = 8 << 20
	defaultHTTPServerReadHeaderTimeoutMilliseconds int64 = 10_000
	defaultHTTPServerReadTimeoutMilliseconds       int64 = 30_000
	defaultHTTPServerWriteTimeoutMilliseconds      int64 = 30_000
	defaultHTTPServerIdleTimeoutMilliseconds       int64 = 120_000
	defaultHTTPServerShutdownTimeoutMilliseconds   int64 = 30_000
)

// markUsesStdHTTP records whether a type or name pulls in the outbound client
// surface, the inbound server surface, or both. Server names also match the
// std.http. prefix, so the more specific server check must run first.
func markUsesStdHTTP(p *program, name string) {
	if strings.Contains(name, "std.http.server.") || strings.Contains(name, "std.http.server") {
		p.usesStdHTTPServer = true
		p.usesContext = true
		return
	}
	if strings.Contains(name, "std.http.") || name == "std.http" {
		p.usesStdHTTP = true
	}
}

func markUsesStdHTTPNamespace(p *program, namespace string) {
	switch namespace {
	case "std.http.server":
		p.usesStdHTTPServer = true
		p.usesContext = true
	case "std.http":
		p.usesStdHTTP = true
	}
}

// skipStdHTTP hides client or server declarations that the program never names
// so generated support is emitted only when used.
func (g *goGenerator) skipStdHTTP(name string) bool {
	if strings.HasPrefix(name, "std.http.server.") {
		return !g.program.usesStdHTTPServer
	}
	if strings.HasPrefix(name, "std.http.") {
		return !g.program.usesStdHTTP
	}
	return false
}

type httpServerConfigData struct {
	address                 string
	maxHeaderBytes          int64
	maxBodyBytes            int64
	readHeaderTimeoutMillis int64
	readTimeoutMillis       int64
	writeTimeoutMillis      int64
	idleTimeoutMillis       int64
	shutdownTimeoutMillis   int64
}

type httpServerRequestData struct {
	method  string
	path    string
	query   []httpHeaderData
	headers []httpHeaderData
	body    []byte
}

type httpServerResponseData struct {
	status  int64
	headers []httpHeaderData
	body    []byte
}

type httpServerFailureData struct {
	operation string
	address   string
	message   string
}

func httpServerFailure(operation, address, message string) *httpServerFailureData {
	if strings.TrimSpace(message) == "" {
		message = "operation failed"
	}
	return &httpServerFailureData{operation: operation, address: address, message: message}
}

func runtimeHTTPServerConfig(value runtimeValue) httpServerConfigData {
	config := httpServerConfigData{
		address:                 value.fields["Address"].scalar.(string),
		maxHeaderBytes:          defaultHTTPServerMaxHeaderBytes,
		maxBodyBytes:            defaultHTTPServerMaxBodyBytes,
		readHeaderTimeoutMillis: defaultHTTPServerReadHeaderTimeoutMilliseconds,
		readTimeoutMillis:       defaultHTTPServerReadTimeoutMilliseconds,
		writeTimeoutMillis:      defaultHTTPServerWriteTimeoutMilliseconds,
		idleTimeoutMillis:       defaultHTTPServerIdleTimeoutMilliseconds,
		shutdownTimeoutMillis:   defaultHTTPServerShutdownTimeoutMilliseconds,
	}
	if limit, present := runtimePresentValue(value.fields["MaxHeaderBytes"]); present {
		config.maxHeaderBytes = limit.scalar.(int64)
	}
	if limit, present := runtimePresentValue(value.fields["MaxBodyBytes"]); present {
		config.maxBodyBytes = limit.scalar.(int64)
	}
	if timeout, present := runtimePresentValue(value.fields["ReadHeaderTimeoutMilliseconds"]); present {
		config.readHeaderTimeoutMillis = timeout.scalar.(int64)
	}
	if timeout, present := runtimePresentValue(value.fields["ReadTimeoutMilliseconds"]); present {
		config.readTimeoutMillis = timeout.scalar.(int64)
	}
	if timeout, present := runtimePresentValue(value.fields["WriteTimeoutMilliseconds"]); present {
		config.writeTimeoutMillis = timeout.scalar.(int64)
	}
	if timeout, present := runtimePresentValue(value.fields["IdleTimeoutMilliseconds"]); present {
		config.idleTimeoutMillis = timeout.scalar.(int64)
	}
	if timeout, present := runtimePresentValue(value.fields["ShutdownTimeoutMilliseconds"]); present {
		config.shutdownTimeoutMillis = timeout.scalar.(int64)
	}
	return config
}

func validateHTTPServerConfig(config httpServerConfigData) *httpServerFailureData {
	if strings.TrimSpace(config.address) == "" {
		return httpServerFailure("Config", config.address, "Address must not be empty")
	}
	checks := []struct {
		name  string
		value int64
	}{
		{"MaxHeaderBytes", config.maxHeaderBytes},
		{"MaxBodyBytes", config.maxBodyBytes},
		{"ReadHeaderTimeoutMilliseconds", config.readHeaderTimeoutMillis},
		{"ReadTimeoutMilliseconds", config.readTimeoutMillis},
		{"WriteTimeoutMilliseconds", config.writeTimeoutMillis},
		{"IdleTimeoutMilliseconds", config.idleTimeoutMillis},
		{"ShutdownTimeoutMilliseconds", config.shutdownTimeoutMillis},
	}
	for _, check := range checks {
		if check.value <= 0 {
			return httpServerFailure("Config", config.address, check.name+" must be positive")
		}
	}
	return nil
}

func httpServerTimeoutDuration(milliseconds int64) time.Duration {
	if milliseconds > int64(math.MaxInt64/time.Millisecond) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

var hopByHopHeaderNames = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Transfer-Encoding": true, "Te": true, "Trailer": true, "Upgrade": true,
}

func isHopByHopHeader(name string) bool {
	return hopByHopHeaderNames[http.CanonicalHeaderKey(name)]
}

func hopByHopRequestHeaders(headers http.Header) map[string]bool {
	names := make(map[string]bool, len(hopByHopHeaderNames))
	for name, hop := range hopByHopHeaderNames {
		names[name] = hop
	}
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			token = http.CanonicalHeaderKey(strings.TrimSpace(token))
			if token != "" {
				names[token] = true
			}
		}
	}
	return names
}

func parseHTTPServerQuery(raw string) ([]httpHeaderData, error) {
	if raw == "" {
		return nil, nil
	}
	order := make([]string, 0)
	seen := make(map[string]int)
	values := make(map[string][]string)
	for raw != "" {
		pair := raw
		if index := strings.IndexByte(raw, '&'); index >= 0 {
			pair, raw = raw[:index], raw[index+1:]
		} else {
			raw = ""
		}
		if pair == "" {
			continue
		}
		key, value := pair, ""
		if index := strings.IndexByte(pair, '='); index >= 0 {
			key, value = pair[:index], pair[index+1:]
		}
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			return nil, err
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[decodedKey]; !exists {
			seen[decodedKey] = len(order)
			order = append(order, decodedKey)
		}
		values[decodedKey] = append(values[decodedKey], decodedValue)
	}
	result := make([]httpHeaderData, len(order))
	for index, key := range order {
		result[index] = httpHeaderData{name: key, values: values[key]}
	}
	return result, nil
}

func requestHTTPServerHeaders(headers http.Header) []httpHeaderData {
	hopByHop := hopByHopRequestHeaders(headers)
	sourceNames := make([]string, 0, len(headers))
	for name := range headers {
		if hopByHop[http.CanonicalHeaderKey(name)] {
			continue
		}
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
	merged := make(map[string][]string)
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
		result[index] = httpHeaderData{name: name, values: append([]string(nil), merged[name]...)}
	}
	return result
}

func readHTTPServerBody(request *http.Request, maxBodyBytes int64) ([]byte, int, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return []byte{}, 0, nil
	}
	defer request.Body.Close()
	limited := http.MaxBytesReader(nil, request.Body, maxBodyBytes)
	contents, err := io.ReadAll(limited)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return nil, http.StatusRequestEntityTooLarge, err
		}
		return nil, http.StatusBadRequest, err
	}
	return append([]byte(nil), contents...), 0, nil
}

func convertHTTPServerRequest(request *http.Request, maxBodyBytes int64) (httpServerRequestData, int, error) {
	query, err := parseHTTPServerQuery(request.URL.RawQuery)
	if err != nil {
		return httpServerRequestData{}, http.StatusBadRequest, err
	}
	body, status, err := readHTTPServerBody(request, maxBodyBytes)
	if err != nil {
		return httpServerRequestData{}, status, err
	}
	path := request.URL.Path
	if path == "" {
		path = "/"
	}
	return httpServerRequestData{
		method:  request.Method,
		path:    path,
		query:   query,
		headers: requestHTTPServerHeaders(request.Header),
		body:    body,
	}, 0, nil
}

func validateHTTPServerResponse(response httpServerResponseData) error {
	if response.status < 200 || response.status > 599 {
		return errors.New("response status must be between 200 and 599")
	}
	for _, header := range response.headers {
		if isHopByHopHeader(header.name) {
			return fmt.Errorf("%s header cannot be controlled", http.CanonicalHeaderKey(header.name))
		}
		canonical := http.CanonicalHeaderKey(header.name)
		if !validHTTPToken(header.name) || canonical == "" {
			return errors.New("invalid response header name")
		}
		if canonical == "Content-Length" || canonical == "Host" || canonical == "Transfer-Encoding" {
			return fmt.Errorf("%s header cannot be controlled", canonical)
		}
		if len(header.values) == 0 {
			return fmt.Errorf("%s header values must not be empty", canonical)
		}
		for _, value := range header.values {
			if !validHTTPFieldValue(value) {
				return fmt.Errorf("%s header value contains a forbidden control byte", canonical)
			}
		}
	}
	return nil
}

func suppressHTTPServerBody(method string, status int64, body []byte) []byte {
	if method == http.MethodHead {
		return nil
	}
	switch status {
	case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified:
		return nil
	}
	return body
}

func writeHTTPServerResponse(writer http.ResponseWriter, method string, response httpServerResponseData) {
	if method == http.MethodConnect && response.status >= 200 && response.status < 300 {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := validateHTTPServerResponse(response); err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := suppressHTTPServerBody(method, response.status, response.body)
	header := writer.Header()
	for _, item := range response.headers {
		canonical := http.CanonicalHeaderKey(item.name)
		for _, value := range item.values {
			header.Add(canonical, value)
		}
	}
	if method != http.MethodHead && body != nil {
		header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	} else if method == http.MethodHead {
		header.Set("Content-Length", fmt.Sprintf("%d", len(response.body)))
	} else {
		header.Set("Content-Length", "0")
	}
	writer.WriteHeader(int(response.status))
	if method != http.MethodHead && len(body) > 0 {
		_, _ = writer.Write(body)
	}
}

func sanitizedHTTPServerInternalResponse() httpServerResponseData {
	return httpServerResponseData{status: http.StatusInternalServerError, body: nil}
}

func runtimeHTTPServerRequest(data httpServerRequestData) runtimeValue {
	queryEntries := make([]runtimeMapEntry, len(data.query))
	for index, item := range data.query {
		values := make([]runtimeValue, len(item.values))
		for valueIndex, value := range item.values {
			values[valueIndex] = runtimeValue{typ: "string", scalar: value}
		}
		queryEntries[index] = runtimeMapEntry{
			key:   runtimeValue{typ: "string", scalar: item.name},
			value: runtimeValue{typ: "string[]", elements: values},
		}
	}
	query, _ := newRuntimeMap(queryEntries)
	headerEntries := make([]runtimeMapEntry, len(data.headers))
	for index, item := range data.headers {
		values := make([]runtimeValue, len(item.values))
		for valueIndex, value := range item.values {
			values[valueIndex] = runtimeValue{typ: "string", scalar: value}
		}
		headerEntries[index] = runtimeMapEntry{
			key:   runtimeValue{typ: "string", scalar: item.name},
			value: runtimeValue{typ: "string[]", elements: values},
		}
	}
	headers, _ := newRuntimeMap(headerEntries)
	return runtimeValue{
		typ: stdHTTPServerRequestName,
		fields: map[string]runtimeValue{
			"Method":  {typ: "string", scalar: data.method},
			"Path":    {typ: "string", scalar: data.path},
			"Query":   {typ: "Map<string,string[]>", mapping: query},
			"Headers": {typ: "Map<string,string[]>", mapping: headers},
			"Body":    {typ: "bytes", scalar: append([]byte(nil), data.body...)},
		},
	}
}

func runtimeHTTPServerResponse(value runtimeValue) httpServerResponseData {
	response := httpServerResponseData{
		status: value.fields["Status"].scalar.(int64),
		body:   append([]byte(nil), value.fields["Body"].scalar.([]byte)...),
	}
	if headers, present := runtimePresentValue(value.fields["Headers"]); present {
		response.headers = make([]httpHeaderData, len(headers.mapping.entries))
		for index, entry := range headers.mapping.entries {
			values := make([]string, len(entry.value.elements))
			for valueIndex, item := range entry.value.elements {
				values[valueIndex] = item.scalar.(string)
			}
			response.headers[index] = httpHeaderData{name: entry.key.scalar.(string), values: values}
		}
	}
	return response
}

func runtimeHTTPServerFailure(resultType string, failure *httpServerFailureData) runtimeValue {
	return runtimeResultValue(resultType, false, runtimeValue{
		typ: stdHTTPServerFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: failure.operation},
			"Address":   {typ: "string", scalar: failure.address},
			"Message":   {typ: "string", scalar: failure.message},
		},
	})
}

func (p *program) callNativeStdHTTPServer(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdHTTPServerServe:
		config := runtimeHTTPServerConfig(frame.locals["Config"])
		failure := p.serveHTTP(frame.ctx, config, frame.locals["Application"])
		if failure != nil {
			return runtimeHTTPServerFailure(resultType, failure), nil, true
		}
		return runtimeResultValue(resultType, true, nullRuntimeValue()), nil, true
	default:
		return runtimeValue{}, nil, false
	}
}

func (p *program) invokeHTTPServerHandler(ctx context.Context, handler runtimeValue, request runtimeValue) (response runtimeValue, ok bool) {
	defer func() {
		if failure := recover(); failure != nil {
			response = runtimeValue{}
			ok = false
		}
	}()
	class := p.classes[handler.typ]
	if class == nil || class.implementations["Handle"] == nil {
		return runtimeValue{}, false
	}
	value, err := p.callFunctionContext(ctx, class.implementations["Handle"], []runtimeValue{request}, &handler, nil)
	if err != nil {
		return runtimeValue{}, false
	}
	if value.typ != stdHTTPServerResponseName {
		return runtimeValue{}, false
	}
	return value, true
}

func (p *program) serveHTTP(ctx context.Context, config httpServerConfigData, handler runtimeValue) *httpServerFailureData {
	if !p.taskSafeType(handler.typ, make(map[string]bool)) {
		return httpServerFailure("Config", config.address, "Application must be task-safe")
	}
	if failure := validateHTTPServerConfig(config); failure != nil {
		return failure
	}
	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		return httpServerFailure("Bind", config.address, "failed to bind listen address")
	}
	defer listener.Close()
	handlerContext, cancelHandlers := context.WithCancel(ctx)
	defer cancelHandlers()

	server := &http.Server{
		BaseContext: func(net.Listener) context.Context {
			return handlerContext
		},
		MaxHeaderBytes:    int(config.maxHeaderBytes),
		ReadHeaderTimeout: httpServerTimeoutDuration(config.readHeaderTimeoutMillis),
		ReadTimeout:       httpServerTimeoutDuration(config.readTimeoutMillis),
		WriteTimeout:      httpServerTimeoutDuration(config.writeTimeoutMillis),
		IdleTimeout:       httpServerTimeoutDuration(config.idleTimeoutMillis),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			data, status, err := convertHTTPServerRequest(request, config.maxBodyBytes)
			if err != nil {
				if status == 0 {
					status = http.StatusBadRequest
				}
				writer.WriteHeader(status)
				return
			}
			slickRequest := runtimeHTTPServerRequest(data)
			responseValue, ok := p.invokeHTTPServerHandler(request.Context(), handler, slickRequest)
			if !ok {
				writeHTTPServerResponse(writer, request.Method, sanitizedHTTPServerInternalResponse())
				return
			}
			writeHTTPServerResponse(writer, request.Method, runtimeHTTPServerResponse(responseValue))
		}),
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serveErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return httpServerFailure("Serve", config.address, "HTTP server failed")
	case <-signals:
	case <-ctx.Done():
	}
	cancelHandlers()

	shutdownContext, cancel := context.WithTimeout(context.Background(), httpServerTimeoutDuration(config.shutdownTimeoutMillis))
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return httpServerFailure("Shutdown", config.address, "graceful shutdown failed")
		}
		if err := server.Close(); err != nil {
			return httpServerFailure("Shutdown", config.address, "forced shutdown failed")
		}
	}
	if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return httpServerFailure("Serve", config.address, "HTTP server failed")
	}
	return nil
}

func (g *goGenerator) emitHTTPServerRuntimeSupport() {
	configClass := goClassName(stdHTTPServerConfigName)
	requestClass := goClassName(stdHTTPServerRequestName)
	responseClass := goClassName(stdHTTPServerResponseName)
	failureClass := goClassName(stdHTTPServerFailureName)
	handlerInterface := goInterfaceName(stdHTTPServerHandlerName)
	resultType := g.goType("Result<null," + stdHTTPServerFailureName + ">")
	methodField := goFieldName("Method")
	pathField := goFieldName("Path")
	queryField := goFieldName("Query")
	headersField := goFieldName("Headers")
	bodyField := goFieldName("Body")
	statusField := goFieldName("Status")
	operationField := goFieldName("Operation")
	addressField := goFieldName("Address")
	messageField := goFieldName("Message")
	configAddress := goFieldName("Address")
	maxHeaderField := goFieldName("MaxHeaderBytes")
	maxBodyField := goFieldName("MaxBodyBytes")
	readHeaderField := goFieldName("ReadHeaderTimeoutMilliseconds")
	readField := goFieldName("ReadTimeoutMilliseconds")
	writeField := goFieldName("WriteTimeoutMilliseconds")
	idleField := goFieldName("IdleTimeoutMilliseconds")
	shutdownField := goFieldName("ShutdownTimeoutMilliseconds")

	g.line(`type slickHTTPServerHeader struct { name string; values []string }`)
	g.line(`type slickHTTPServerConfigData struct { address string; maxHeaderBytes int64; maxBodyBytes int64; readHeaderTimeoutMillis int64; readTimeoutMillis int64; writeTimeoutMillis int64; idleTimeoutMillis int64; shutdownTimeoutMillis int64 }`)
	g.line(`type slickHTTPServerRequestData struct { method string; path string; query []slickHTTPServerHeader; headers []slickHTTPServerHeader; body slickBytes }`)
	g.line(`type slickHTTPServerResponseData struct { status int64; headers []slickHTTPServerHeader; body slickBytes }`)
	g.line(`type slickHTTPServerFailureData struct { operation string; address string; message string }`)
	g.line("func slickHTTPServerFailure(operation, address, message string) *slickHTTPServerFailureData { if strings.TrimSpace(message) == \"\" { message = \"operation failed\" }; return &slickHTTPServerFailureData{operation: operation, address: address, message: message} }")
	g.line("func slickHTTPServerTimeoutDuration(milliseconds int64) time.Duration { if milliseconds > int64(math.MaxInt64 / time.Millisecond) { return time.Duration(math.MaxInt64) }; return time.Duration(milliseconds) * time.Millisecond }")
	g.line(`var slickHTTPServerHopByHop = map[string]bool{"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Transfer-Encoding": true, "Te": true, "Trailer": true, "Upgrade": true}`)
	g.line(`func slickHTTPServerIsHopByHop(name string) bool { return slickHTTPServerHopByHop[http.CanonicalHeaderKey(name)] }`)
	g.line(`func slickHTTPServerRequestHopByHop(headers http.Header) map[string]bool { names := make(map[string]bool, len(slickHTTPServerHopByHop)); for name, hop := range slickHTTPServerHopByHop { names[name] = hop }; for _, value := range headers.Values("Connection") { for _, token := range strings.Split(value, ",") { token = http.CanonicalHeaderKey(strings.TrimSpace(token)); if token != "" { names[token] = true } } }; return names }`)
	g.line(`func slickHTTPServerValidateConfig(config slickHTTPServerConfigData) *slickHTTPServerFailureData {`)
	g.line(`if strings.TrimSpace(config.address) == "" { return slickHTTPServerFailure("Config", config.address, "Address must not be empty") }`)
	g.line(`checks := []struct{ name string; value int64 }{`)
	g.line(`{"MaxHeaderBytes", config.maxHeaderBytes}, {"MaxBodyBytes", config.maxBodyBytes},`)
	g.line(`{"ReadHeaderTimeoutMilliseconds", config.readHeaderTimeoutMillis}, {"ReadTimeoutMilliseconds", config.readTimeoutMillis},`)
	g.line(`{"WriteTimeoutMilliseconds", config.writeTimeoutMillis}, {"IdleTimeoutMilliseconds", config.idleTimeoutMillis},`)
	g.line(`{"ShutdownTimeoutMilliseconds", config.shutdownTimeoutMillis},`)
	g.line(`}`)
	g.line(`for _, check := range checks { if check.value <= 0 { return slickHTTPServerFailure("Config", config.address, check.name + " must be positive") } }`)
	g.line(`return nil`)
	g.line(`}`)
	g.line(`func slickHTTPServerParseQuery(raw string) ([]slickHTTPServerHeader, error) {`)
	g.line(`if raw == "" { return nil, nil }`)
	g.line(`order := make([]string, 0); seen := make(map[string]int); values := make(map[string][]string)`)
	g.line(`for raw != "" {`)
	g.line(`pair := raw`)
	g.line(`if index := strings.IndexByte(raw, '&'); index >= 0 { pair, raw = raw[:index], raw[index+1:] } else { raw = "" }`)
	g.line(`if pair == "" { continue }`)
	g.line(`key, value := pair, ""`)
	g.line(`if index := strings.IndexByte(pair, '='); index >= 0 { key, value = pair[:index], pair[index+1:] }`)
	g.line(`decodedKey, err := url.QueryUnescape(key); if err != nil { return nil, err }`)
	g.line(`decodedValue, err := url.QueryUnescape(value); if err != nil { return nil, err }`)
	g.line(`if _, exists := seen[decodedKey]; !exists { seen[decodedKey] = len(order); order = append(order, decodedKey) }`)
	g.line(`values[decodedKey] = append(values[decodedKey], decodedValue)`)
	g.line(`}`)
	g.line(`result := make([]slickHTTPServerHeader, len(order)); for index, key := range order { result[index] = slickHTTPServerHeader{name: key, values: values[key]} }; return result, nil`)
	g.line(`}`)
	g.line(`func slickHTTPServerRequestHeaders(headers http.Header) []slickHTTPServerHeader {`)
	g.line(`hopByHop := slickHTTPServerRequestHopByHop(headers); sourceNames := make([]string, 0, len(headers)); for name := range headers { if hopByHop[http.CanonicalHeaderKey(name)] { continue }; sourceNames = append(sourceNames, name) }`)
	g.line(`sort.Slice(sourceNames, func(left, right int) bool { leftCanonical := http.CanonicalHeaderKey(sourceNames[left]); rightCanonical := http.CanonicalHeaderKey(sourceNames[right]); if leftCanonical == rightCanonical { return sourceNames[left] < sourceNames[right] }; return leftCanonical < rightCanonical })`)
	g.line(`merged := make(map[string][]string); for _, name := range sourceNames { canonical := http.CanonicalHeaderKey(name); merged[canonical] = append(merged[canonical], headers[name]...) }`)
	g.line(`names := make([]string, 0, len(merged)); for name := range merged { names = append(names, name) }; sort.Strings(names)`)
	g.line(`result := make([]slickHTTPServerHeader, len(names)); for index, name := range names { result[index] = slickHTTPServerHeader{name: name, values: append([]string(nil), merged[name]...)} }; return result`)
	g.line(`}`)
	// Reuse client token validation when client support is also present; otherwise emit a local copy.
	if !g.program.usesStdHTTP {
		g.line("func slickHTTPValidToken(value string) bool { if value == \"\" { return false }; for index := 0; index < len(value); index++ { character := value[index]; if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune(\"!#$%%&'*+-.^_`|~\", rune(character)) { continue }; return false }; return true }")
		g.line(`func slickHTTPValidFieldValue(value string) bool { for index := 0; index < len(value); index++ { character := value[index]; if character != '\t' && (character < ' ' || character == 0x7f) { return false } }; return true }`)
	}
	g.line(`func slickHTTPServerReadBody(request *http.Request, maxBodyBytes int64) (slickBytes, int, error) {`)
	g.line(`if request.Body == nil || request.Body == http.NoBody { return slickBytes{}, 0, nil }`)
	g.line(`defer request.Body.Close()`)
	g.line(`limited := http.MaxBytesReader(nil, request.Body, maxBodyBytes)`)
	g.line(`contents, err := io.ReadAll(limited)`)
	g.line(`if err != nil { var maxBytesError *http.MaxBytesError; if errors.As(err, &maxBytesError) { return nil, http.StatusRequestEntityTooLarge, err }; return nil, http.StatusBadRequest, err }`)
	g.line(`return append(slickBytes(nil), contents...), 0, nil`)
	g.line(`}`)
	g.line(`func slickHTTPServerConvertRequest(request *http.Request, maxBodyBytes int64) (slickHTTPServerRequestData, int, error) {`)
	g.line(`query, err := slickHTTPServerParseQuery(request.URL.RawQuery); if err != nil { return slickHTTPServerRequestData{}, http.StatusBadRequest, err }`)
	g.line(`body, status, err := slickHTTPServerReadBody(request, maxBodyBytes); if err != nil { return slickHTTPServerRequestData{}, status, err }`)
	g.line(`path := request.URL.Path; if path == "" { path = "/" }`)
	g.line(`return slickHTTPServerRequestData{method: request.Method, path: path, query: query, headers: slickHTTPServerRequestHeaders(request.Header), body: body}, 0, nil`)
	g.line(`}`)
	g.line(`func slickHTTPServerValidateResponse(response slickHTTPServerResponseData) error {`)
	g.line(`if response.status < 200 || response.status > 599 { return errors.New("response status must be between 200 and 599") }`)
	g.line(`for _, header := range response.headers {`)
	g.line(`if slickHTTPServerIsHopByHop(header.name) { return fmt.Errorf("%%s header cannot be controlled", http.CanonicalHeaderKey(header.name)) }`)
	g.line(`canonical := http.CanonicalHeaderKey(header.name)`)
	g.line(`if !slickHTTPValidToken(header.name) || canonical == "" { return errors.New("invalid response header name") }`)
	g.line(`if canonical == "Content-Length" || canonical == "Host" || canonical == "Transfer-Encoding" { return fmt.Errorf("%%s header cannot be controlled", canonical) }`)
	g.line(`if len(header.values) == 0 { return fmt.Errorf("%%s header values must not be empty", canonical) }`)
	g.line(`for _, value := range header.values { if !slickHTTPValidFieldValue(value) { return fmt.Errorf("%%s header value contains a forbidden control byte", canonical) } }`)
	g.line(`}`)
	g.line(`return nil`)
	g.line(`}`)
	g.line(`func slickHTTPServerSuppressBody(method string, status int64, body slickBytes) slickBytes {`)
	g.line(`if method == http.MethodHead { return nil }`)
	g.line(`switch status { case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified: return nil }`)
	g.line(`return body`)
	g.line(`}`)
	g.line(`func slickHTTPServerWriteResponse(writer http.ResponseWriter, method string, response slickHTTPServerResponseData) {`)
	g.line(`if method == http.MethodConnect && response.status >= 200 && response.status < 300 { writer.WriteHeader(http.StatusInternalServerError); return }`)
	g.line(`if err := slickHTTPServerValidateResponse(response); err != nil { writer.WriteHeader(http.StatusInternalServerError); return }`)
	g.line(`body := slickHTTPServerSuppressBody(method, response.status, response.body)`)
	g.line(`header := writer.Header()`)
	g.line(`for _, item := range response.headers { canonical := http.CanonicalHeaderKey(item.name); for _, value := range item.values { header.Add(canonical, value) } }`)
	g.line(`if method != http.MethodHead && body != nil { header.Set("Content-Length", fmt.Sprintf("%%d", len(body))) } else if method == http.MethodHead { header.Set("Content-Length", fmt.Sprintf("%%d", len(response.body))) } else { header.Set("Content-Length", "0") }`)
	g.line(`writer.WriteHeader(int(response.status))`)
	g.line(`if method != http.MethodHead && len(body) > 0 { _, _ = writer.Write(body) }`)
	g.line(`}`)
	g.line("func slickHTTPServerToRequest(data slickHTTPServerRequestData) %s {", requestClass)
	g.line(`queryEntries := make([]slickMapEntry[string, []string], len(data.query)); for index, item := range data.query { queryEntries[index] = slickMapEntry[string, []string]{key: item.name, value: item.values} }`)
	g.line(`headerEntries := make([]slickMapEntry[string, []string], len(data.headers)); for index, item := range data.headers { headerEntries[index] = slickMapEntry[string, []string]{key: item.name, value: item.values} }`)
	g.line("return %s{%s: data.method, %s: data.path, %s: slickMapOf(queryEntries...), %s: slickMapOf(headerEntries...), %s: append(slickBytes(nil), data.body...)}",
		requestClass, methodField, pathField, queryField, headersField, bodyField)
	g.line(`}`)
	g.line("func slickHTTPServerFromResponse(response %s) slickHTTPServerResponseData {", responseClass)
	g.line("data := slickHTTPServerResponseData{status: response.%s, body: append(slickBytes(nil), response.%s...)}", statusField, bodyField)
	g.line("if response.%s.present { data.headers = make([]slickHTTPServerHeader, len(response.%s.value.entries)); for index, entry := range response.%s.value.entries { data.headers[index] = slickHTTPServerHeader{name: entry.key, values: entry.value} } }", headersField, headersField, headersField)
	g.line(`return data`)
	g.line(`}`)
	contextParameter, contextCheck := "", ""
	if g.program.usesContext {
		contextParameter = "slickContext context.Context, "
		contextCheck = "if err := slickCheckCancellation(slickContext); err != nil { return " + resultType + "{}, err }\n\t"
	} else {
		contextParameter = ""
	}
	parentContext := "context.Background()"
	if g.program.usesContext {
		parentContext = "slickContext"
	}
	g.line("func slickHTTPServerServe(%sconfig %s, application %s) (%s, error) {", contextParameter, configClass, handlerInterface, resultType)
	if contextCheck != "" {
		g.line("%s", strings.TrimSuffix(contextCheck, "\n\t"))
	}
	g.line("data := slickHTTPServerConfigData{address: config.%s, maxHeaderBytes: %d, maxBodyBytes: %d, readHeaderTimeoutMillis: %d, readTimeoutMillis: %d, writeTimeoutMillis: %d, idleTimeoutMillis: %d, shutdownTimeoutMillis: %d}",
		configAddress,
		defaultHTTPServerMaxHeaderBytes, defaultHTTPServerMaxBodyBytes,
		defaultHTTPServerReadHeaderTimeoutMilliseconds, defaultHTTPServerReadTimeoutMilliseconds,
		defaultHTTPServerWriteTimeoutMilliseconds, defaultHTTPServerIdleTimeoutMilliseconds,
		defaultHTTPServerShutdownTimeoutMilliseconds)
	g.line("if config.%s.present { data.maxHeaderBytes = config.%s.value }", maxHeaderField, maxHeaderField)
	g.line("if config.%s.present { data.maxBodyBytes = config.%s.value }", maxBodyField, maxBodyField)
	g.line("if config.%s.present { data.readHeaderTimeoutMillis = config.%s.value }", readHeaderField, readHeaderField)
	g.line("if config.%s.present { data.readTimeoutMillis = config.%s.value }", readField, readField)
	g.line("if config.%s.present { data.writeTimeoutMillis = config.%s.value }", writeField, writeField)
	g.line("if config.%s.present { data.idleTimeoutMillis = config.%s.value }", idleField, idleField)
	g.line("if config.%s.present { data.shutdownTimeoutMillis = config.%s.value }", shutdownField, shutdownField)
	g.line("if _, safe := application.(interface{ slickHTTPServerTaskSafe() }); !safe { return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil }",
		resultType, failureClass, operationField, "Config", addressField, messageField, "Application must be task-safe")
	g.line("if failure := slickHTTPServerValidateConfig(data); failure != nil { return %s{failure: &%s{%s: failure.operation, %s: failure.address, %s: failure.message}}, nil }",
		resultType, failureClass, operationField, addressField, messageField)
	g.line(`listener, err := net.Listen("tcp", data.address)`)
	g.line("if err != nil { return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil }",
		resultType, failureClass, operationField, "Bind", addressField, messageField, "failed to bind listen address")
	g.line(`defer listener.Close()`)
	g.line(`handlerContext, cancelHandlers := context.WithCancel(%s)`, parentContext)
	g.line(`defer cancelHandlers()`)
	handleMethod := goMethodName("Handle")
	handleCallArgs := "slickRequest"
	if g.program.usesContext {
		handleCallArgs = "request.Context(), slickRequest"
	}
	g.line(`server := &http.Server{`)
	g.line(`BaseContext: func(net.Listener) context.Context { return handlerContext },`)
	g.line(`MaxHeaderBytes: int(data.maxHeaderBytes),`)
	g.line(`ReadHeaderTimeout: slickHTTPServerTimeoutDuration(data.readHeaderTimeoutMillis),`)
	g.line(`ReadTimeout: slickHTTPServerTimeoutDuration(data.readTimeoutMillis),`)
	g.line(`WriteTimeout: slickHTTPServerTimeoutDuration(data.writeTimeoutMillis),`)
	g.line(`IdleTimeout: slickHTTPServerTimeoutDuration(data.idleTimeoutMillis),`)
	g.line(`Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {`)
	g.line(`converted, status, err := slickHTTPServerConvertRequest(request, data.maxBodyBytes)`)
	g.line(`if err != nil { if status == 0 { status = http.StatusBadRequest }; writer.WriteHeader(status); return }`)
	g.line(`slickRequest := slickHTTPServerToRequest(converted)`)
	g.line(`func() {`)
	g.line(`defer func() { if recover() != nil { slickHTTPServerWriteResponse(writer, request.Method, slickHTTPServerResponseData{status: http.StatusInternalServerError}) } }()`)
	g.line(`response, handleError := application.%s(%s)`, handleMethod, handleCallArgs)
	g.line(`if handleError != nil { slickHTTPServerWriteResponse(writer, request.Method, slickHTTPServerResponseData{status: http.StatusInternalServerError}); return }`)
	g.line(`slickHTTPServerWriteResponse(writer, request.Method, slickHTTPServerFromResponse(response))`)
	g.line(`}()`)
	g.line(`}),`)
	g.line(`}`)
	g.line(`serveErrors := make(chan error, 1)`)
	g.line(`go func() { serveErrors <- server.Serve(listener) }()`)
	g.line(`signals := make(chan os.Signal, 1)`)
	g.line(`signal.Notify(signals, os.Interrupt, syscall.SIGTERM)`)
	g.line(`defer signal.Stop(signals)`)
	g.line(`select {`)
	g.line(`case err := <-serveErrors:`)
	g.line("if err == nil || errors.Is(err, http.ErrServerClosed) { return %s{ok: true, value: struct{}{}}, nil }", resultType)
	g.line("return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil",
		resultType, failureClass, operationField, "Serve", addressField, messageField, "HTTP server failed")
	g.line(`case <-signals:`)
	g.line("case <-%s.Done():", parentContext)
	g.line(`}`)
	g.line(`cancelHandlers()`)
	g.line(`shutdownContext, cancel := context.WithTimeout(context.Background(), slickHTTPServerTimeoutDuration(data.shutdownTimeoutMillis))`)
	g.line(`defer cancel()`)
	g.line(`if err := server.Shutdown(shutdownContext); err != nil {`)
	g.line(`if !errors.Is(err, context.DeadlineExceeded) {`)
	g.line("return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil",
		resultType, failureClass, operationField, "Shutdown", addressField, messageField, "graceful shutdown failed")
	g.line(`}`)
	g.line(`if err := server.Close(); err != nil {`)
	g.line("return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil",
		resultType, failureClass, operationField, "Shutdown", addressField, messageField, "forced shutdown failed")
	g.line(`}`)
	g.line(`}`)
	g.line(`if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {`)
	g.line("return %s{failure: &%s{%s: %q, %s: data.address, %s: %q}}, nil",
		resultType, failureClass, operationField, "Serve", addressField, messageField, "HTTP server failed")
	g.line(`}`)
	g.line("return %s{ok: true, value: struct{}{}}, nil", resultType)
	g.line(`}`)
}
