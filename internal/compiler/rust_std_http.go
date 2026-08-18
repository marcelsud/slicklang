package compiler

var rustStdHTTP = rustStdFamily{
	family: runtimeFamilyHTTP,
	module: rustStdHTTPModule,
	functions: map[runtimeOperationID]string{
		nativeStdHTTPFetch:        "slick_nat_http_fetch",
		nativeStdHTTPHeaderValues: "slick_nat_http_header_values",
		nativeStdHTTPStatusText:   "slick_nat_http_status_text",
	},
	dependencies: []rustCrate{{
		name:     rustHTTPCrate,
		version:  rustHTTPVersion,
		features: []string{"rustls"},
	}},
}

// rustStdHTTPModule implements std.http. A Fetch builds the request from the
// declared std.http.Request fields, enforces the documented limits and
// timeouts through a process-shared ureq agent, and returns std.http.Response
// with deterministic canonical headers, normalizing every transport, timeout,
// redirect, status, limit, and cancellation condition into std.http.Failure
// exactly as the interpreter and generated Go do. In-flight connect, write, and
// read are sliced against the task cancellation flags so a scope unwind aborts
// the socket instead of waiting for the peer. Proxy selection honours
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY (and the lowercase forms) through
// slick_environment_read, including bracketed IPv6 authorities, CIDR entries,
// and omitted-port defaults matching golang.org/x/net/http/httpproxy. The Rust
// source contains no backtick so it fits a Go raw string; the HTTP token
// backtick (0x60) is matched by value.
const rustStdHTTPModule = `fn slick_nat_http_fetch(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let request = slick_arg(&args, 0);
    let method = match slick_field(&request, "Method") {
        Ok(SlickValue::String(value)) => value,
        _ => return SlickOutcome::Throw(SlickFailure::host("std.http.Request.Method is not string")),
    };
    let url = match slick_field(&request, "URL") {
        Ok(SlickValue::String(value)) => value,
        _ => return SlickOutcome::Throw(SlickFailure::host("std.http.Request.URL is not string")),
    };
    let mut timeout_ms: i64 = 30000;
    let mut max_bytes: i64 = 8 * 1024 * 1024;
    let mut follow_redirects = false;
    let mut body: Vec<u8> = Vec::new();
    let mut body_present = false;
    let mut headers: Vec<(String, Vec<String>)> = Vec::new();
    if let Some(SlickValue::Map(entries)) = slick_http_optional_field(&request, "Headers") {
        for (key, value) in entries {
            let name = match key { SlickValue::String(name) => name, _ => continue };
            let mut values: Vec<String> = Vec::new();
            if let SlickValue::Array(items) = value {
                for item in items {
                    if let SlickValue::String(text) = item { values.push(text); }
                }
            }
            headers.push((name, values));
        }
    }
    if let Some(SlickValue::Bytes(bytes)) = slick_http_optional_field(&request, "Body") {
        body = bytes;
        body_present = true;
    }
    if let Some(SlickValue::Int(value)) = slick_http_optional_field(&request, "TimeoutMilliseconds") {
        timeout_ms = value;
    }
    if let Some(SlickValue::Int(value)) = slick_http_optional_field(&request, "MaxResponseBytes") {
        max_bytes = value;
    }
    if let Some(SlickValue::Bool(value)) = slick_http_optional_field(&request, "FollowRedirects") {
        follow_redirects = value;
    }

    let prepared = match slick_http_validate(&method, &url, &headers, timeout_ms, max_bytes) {
        Ok(prepared) => prepared,
        Err(failure) => return slick_err(failure),
    };

    if context.cancelled() {
        return slick_http_cancelled(&slick_http_sanitized(&url), None);
    }
    let cancel_guard = slick_http_bind_cancel(context);
    let outcome = slick_http_fetch_io(context, method, url, prepared, body, body_present, timeout_ms, max_bytes, follow_redirects);
    drop(cancel_guard);
    outcome
}

fn slick_http_fetch_io(
    context: &SlickContext,
    method: String,
    url: String,
    prepared: Vec<(String, Vec<String>)>,
    body: Vec<u8>,
    body_present: bool,
    timeout_ms: i64,
    max_bytes: i64,
    follow_redirects: bool,
) -> SlickOutcome {
    use ureq::ResponseExt;
    let timeout = std::time::Duration::from_millis(timeout_ms as u64);
    let max_redirects: u32 = if follow_redirects { 9 } else { 0 };
    let agent = slick_http_agent();

    let response_result = if body_present {
        let request = match ureq::http::Request::builder().method(method.as_str()).uri(url.as_str()).body(body.clone()) {
            Ok(request) => request,
            Err(_) => return slick_err(slick_http_failure("InvalidRequest", &slick_http_sanitized(&url), None, "method or URL is invalid")),
        };
        slick_http_run(agent, request, &prepared, max_redirects, timeout)
    } else if method.eq_ignore_ascii_case("POST") || method.eq_ignore_ascii_case("PUT") || method.eq_ignore_ascii_case("PATCH") {
        // An empty POST must still declare Content-Length 0. ureq's () body
        // omits the header, so Go's server treats the connection as an
        // unfinished body and never cancels request.Context() on disconnect.
        let request = match ureq::http::Request::builder().method(method.as_str()).uri(url.as_str()).body(Vec::<u8>::new()) {
            Ok(request) => request,
            Err(_) => return slick_err(slick_http_failure("InvalidRequest", &slick_http_sanitized(&url), None, "method or URL is invalid")),
        };
        slick_http_run(agent, request, &prepared, max_redirects, timeout)
    } else {
        let request = match ureq::http::Request::builder().method(method.as_str()).uri(url.as_str()).body(()) {
            Ok(request) => request,
            Err(_) => return slick_err(slick_http_failure("InvalidRequest", &slick_http_sanitized(&url), None, "method or URL is invalid")),
        };
        slick_http_run(agent, request, &prepared, max_redirects, timeout)
    };

    let mut response = match response_result {
        Ok(response) => response,
        Err(error) => {
            let sanitized = slick_http_sanitized(&url);
            if context.cancelled() {
                return slick_http_cancelled(&sanitized, None);
            }
            let failure = match error {
                ureq::Error::Timeout(_) => slick_http_failure("Timeout", &sanitized, None, "HTTP request timed out"),
                _ => slick_http_failure("Transport", &sanitized, None, "HTTP transport failed"),
            };
            return slick_err(failure);
        }
    };

    let status = response.status().as_u16() as i64;
    let final_uri = response.get_uri().to_string();

    // When following redirects, ureq returns the final 3xx as a normal response
    // once the redirect limit is reached (max_redirects_will_error is false).
    // The interpreter reports this as a Redirect failure carrying that status.
    if follow_redirects && slick_http_is_redirect(status) {
        let sanitized = slick_http_sanitized(&final_uri);
        return slick_err(slick_http_failure("Redirect", &sanitized, Some(status), "HTTP redirect failed"));
    }

    let limit: u64 = if max_bytes < i64::MAX { (max_bytes + 1) as u64 } else { u64::MAX };
    let body_result = response.body_mut().with_config().limit(limit).read_to_vec();

    match body_result {
        Ok(contents) => {
            if contents.len() as i64 > max_bytes {
                let sanitized = slick_http_sanitized(&final_uri);
                let message = format!("response body exceeds {} bytes", max_bytes);
                return slick_err(slick_http_failure("BodyTooLarge", &sanitized, Some(status), &message));
            }
            let response_headers = slick_http_response_headers(response.headers());
            slick_ok(slick_http_response(status, &final_uri, response_headers, contents))
        }
        Err(error) => {
            let sanitized = slick_http_sanitized(&final_uri);
            if context.cancelled() {
                return slick_http_cancelled(&sanitized, Some(status));
            }
            let failure = match error {
                ureq::Error::Timeout(_) => slick_http_failure("Timeout", &sanitized, Some(status), "HTTP request timed out"),
                _ => slick_http_failure("BodyRead", &sanitized, Some(status), "failed to read response body"),
            };
            slick_err(failure)
        }
    }
}

fn slick_nat_http_header_values(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let name = match slick_arg_string(&args, 1) {
        Ok(name) => name,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    let entries = match slick_arg(&args, 0) {
        SlickValue::Map(entries) => entries,
        _ => return SlickOutcome::Value(SlickValue::Array(Vec::new())),
    };
    for (key, value) in &entries {
        if let (SlickValue::String(header_name), SlickValue::Array(items)) = (key, value) {
            if header_name.eq_ignore_ascii_case(&name) {
                return SlickOutcome::Value(SlickValue::Array(items.clone()));
            }
        }
    }
    SlickOutcome::Value(SlickValue::Array(Vec::new()))
}

fn slick_nat_http_status_text(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let status = match slick_arg_int(&args, 0) {
        Ok(status) => status,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    match slick_http_status_text(status) {
        Some(text) => SlickOutcome::Value(SlickValue::Optional(Some(Box::new(SlickValue::String(text.to_string()))))),
        None => SlickOutcome::Value(SlickValue::Optional(None)),
    }
}

// slick_http_agent is the process-shared ureq agent. A single agent keeps one
// connection pool so consecutive requests to the same host reuse their
// connection, matching the interpreter's shared transport. Persistent settings
// (no status-as-error, Slick user agent, no auto Accept/Accept-Encoding) live
// here; per-request redirects, timeouts, and proxy selection override the
// cloned agent config via slick_http_run. The connector chain is the stock
// CONNECT-proxy + TCP + rustls path, with TCP replaced by a transport that
// observes task cancellation while blocked.
fn slick_http_agent() -> &'static ureq::Agent {
    static SLICK_HTTP_AGENT: std::sync::OnceLock<ureq::Agent> = std::sync::OnceLock::new();
    SLICK_HTTP_AGENT.get_or_init(|| {
        use ureq::unversioned::transport::Connector;
        let config = ureq::config::Config::builder()
            .http_status_as_error(false)
            .user_agent("Slick")
            .accept(ureq::config::AutoHeaderValue::None)
            .accept_encoding(ureq::config::AutoHeaderValue::None)
            .proxy(None)
            .build();
        let connector = ()
            .chain(ureq::unversioned::transport::ConnectProxyConnector::default())
            .chain(SlickHttpTcpConnector)
            .chain(ureq::unversioned::transport::RustlsConnector::default());
        ureq::Agent::with_parts(config, connector, ureq::unversioned::resolver::DefaultResolver::default())
    })
}

// slick_http_run attaches the canonical headers and per-request redirect and
// timeout configuration, then executes the request through the shared agent.
fn slick_http_run<S: ureq::AsSendBody>(
    agent: &ureq::Agent,
    mut request: ureq::http::Request<S>,
    headers: &[(String, Vec<String>)],
    max_redirects: u32,
    timeout: std::time::Duration,
) -> Result<ureq::http::Response<ureq::Body>, ureq::Error> {
    for (name, values) in headers {
        let Ok(header_name) = name.parse::<ureq::http::HeaderName>() else { continue };
        for value in values {
            if let Ok(header_value) = ureq::http::HeaderValue::from_bytes(value.as_bytes()) {
                request.headers_mut().append(header_name.clone(), header_value);
            }
        }
    }
    let proxy = slick_http_proxy_for(&request.uri().to_string());
    let request = agent.configure_request(request)
        .max_redirects(max_redirects)
        .max_redirects_will_error(false)
        .timeout_global(Some(timeout))
        .timeout_recv_body(Some(timeout))
        .proxy(proxy)
        .build();
    agent.run(request)
}

// Cancellation flags live in SlickContext. Bind them for the whole request,
// including body read after slick_http_run returns. Connect, write, and read
// use short socket timeouts so a sibling scope unwind is observed while the
// call is blocked; the socket is then shut down so the peer sees disconnect.
thread_local! {
    static SLICK_HTTP_CANCEL: std::cell::RefCell<Option<SlickContext>> = std::cell::RefCell::new(None);
}

struct SlickHttpCancelGuard;

impl Drop for SlickHttpCancelGuard {
    fn drop(&mut self) {
        SLICK_HTTP_CANCEL.with(|slot| {
            *slot.borrow_mut() = None;
        });
    }
}

fn slick_http_bind_cancel(context: &SlickContext) -> SlickHttpCancelGuard {
    SLICK_HTTP_CANCEL.with(|slot| {
        *slot.borrow_mut() = Some(context.clone());
    });
    SlickHttpCancelGuard
}

fn slick_http_context_cancelled() -> bool {
    SLICK_HTTP_CANCEL.with(|slot| slot.borrow().as_ref().map(SlickContext::cancelled).unwrap_or(false))
}

fn slick_http_cancel_io() -> ureq::Error {
    ureq::Error::Io(std::io::Error::new(std::io::ErrorKind::Interrupted, "HTTP request cancelled"))
}

const SLICK_HTTP_CANCEL_POLL: std::time::Duration = std::time::Duration::from_millis(25);

fn slick_http_deadline(timeout: ureq::unversioned::transport::NextTimeout) -> Option<std::time::Instant> {
    if timeout.after.is_not_happening() {
        None
    } else {
        Some(std::time::Instant::now() + *timeout.after)
    }
}

fn slick_http_slice(deadline: Option<std::time::Instant>, reason: ureq::Timeout) -> Result<std::time::Duration, ureq::Error> {
    if slick_http_context_cancelled() {
        return Err(slick_http_cancel_io());
    }
    let wait = match deadline {
        Some(deadline) => {
            let remaining = deadline.saturating_duration_since(std::time::Instant::now());
            if remaining.is_zero() {
                return Err(ureq::Error::Timeout(reason));
            }
            if remaining < SLICK_HTTP_CANCEL_POLL { remaining } else { SLICK_HTTP_CANCEL_POLL }
        }
        None => SLICK_HTTP_CANCEL_POLL,
    };
    if wait.as_millis() == 0 {
        Ok(std::time::Duration::from_millis(1))
    } else {
        Ok(wait)
    }
}

#[derive(Debug)]
struct SlickHttpTcpConnector;

impl<In: ureq::unversioned::transport::Transport> ureq::unversioned::transport::Connector<In> for SlickHttpTcpConnector {
    type Out = ureq::unversioned::transport::Either<In, SlickHttpTransport>;

    fn connect(
        &self,
        details: &ureq::unversioned::transport::ConnectionDetails,
        chained: Option<In>,
    ) -> Result<Option<Self::Out>, ureq::Error> {
        if let Some(transport) = chained {
            return Ok(Some(ureq::unversioned::transport::Either::A(transport)));
        }
        let stream = slick_http_tcp_connect(details)?;
        let buffers = ureq::unversioned::transport::LazyBuffers::new(
            details.config.input_buffer_size(),
            details.config.output_buffer_size(),
        );
        Ok(Some(ureq::unversioned::transport::Either::B(SlickHttpTransport { stream: Some(stream), buffers })))
    }
}

fn slick_http_tcp_connect(details: &ureq::unversioned::transport::ConnectionDetails) -> Result<std::net::TcpStream, ureq::Error> {
    let deadline = slick_http_deadline(details.timeout);
    for addr in &details.addrs {
        loop {
            let wait = slick_http_slice(deadline, details.timeout.reason)?;
            match std::net::TcpStream::connect_timeout(addr, wait) {
                Ok(stream) => {
                    if details.config.no_delay() {
                        if let Err(error) = stream.set_nodelay(true) {
                            return Err(error.into());
                        }
                    }
                    return Ok(stream);
                }
                Err(error) if error.kind() == std::io::ErrorKind::TimedOut || error.kind() == std::io::ErrorKind::WouldBlock => {}
                Err(error) if error.kind() == std::io::ErrorKind::ConnectionRefused => break,
                Err(error) => {
                    if slick_http_context_cancelled() {
                        return Err(slick_http_cancel_io());
                    }
                    return Err(error.into());
                }
            }
        }
    }
    if slick_http_context_cancelled() {
        return Err(slick_http_cancel_io());
    }
    Err(ureq::Error::Io(std::io::Error::new(std::io::ErrorKind::ConnectionRefused, "Connection refused")))
}

struct SlickHttpTransport {
    stream: Option<std::net::TcpStream>,
    buffers: ureq::unversioned::transport::LazyBuffers,
}

impl SlickHttpTransport {
    fn abort(&mut self) -> ureq::Error {
        self.stream = None;
        slick_http_cancel_io()
    }
}

impl std::fmt::Debug for SlickHttpTransport {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SlickHttpTransport").finish()
    }
}

impl ureq::unversioned::transport::Transport for SlickHttpTransport {
    fn buffers(&mut self) -> &mut dyn ureq::unversioned::transport::Buffers {
        &mut self.buffers
    }

    fn transmit_output(&mut self, amount: usize, timeout: ureq::unversioned::transport::NextTimeout) -> Result<(), ureq::Error> {
        use std::io::Write;
        use ureq::unversioned::transport::Buffers;
        let deadline = slick_http_deadline(timeout);
        let mut written = 0;
        while written < amount {
            if slick_http_context_cancelled() {
                return Err(self.abort());
            }
            let wait = match slick_http_slice(deadline, timeout.reason) {
                Ok(wait) => wait,
                Err(error) => {
                    if slick_http_context_cancelled() {
                        return Err(self.abort());
                    }
                    return Err(error);
                }
            };
            {
                let Some(stream) = self.stream.as_mut() else { return Err(slick_http_cancel_io()) };
                if let Err(error) = stream.set_write_timeout(Some(wait)) {
                    return Err(error.into());
                }
            }
            let output = self.buffers.output();
            let Some(stream) = self.stream.as_mut() else { return Err(slick_http_cancel_io()) };
            match stream.write(&output[written..amount]) {
                Ok(0) => return Err(std::io::Error::new(std::io::ErrorKind::WriteZero, "write zero").into()),
                Ok(count) => written += count,
                Err(error) if error.kind() == std::io::ErrorKind::TimedOut || error.kind() == std::io::ErrorKind::WouldBlock => {}
                Err(error) => {
                    if slick_http_context_cancelled() {
                        return Err(self.abort());
                    }
                    return Err(error.into());
                }
            }
        }
        Ok(())
    }

    fn await_input(&mut self, timeout: ureq::unversioned::transport::NextTimeout) -> Result<bool, ureq::Error> {
        use std::io::Read;
        use ureq::unversioned::transport::Buffers;
        let deadline = slick_http_deadline(timeout);
        loop {
            if slick_http_context_cancelled() {
                return Err(self.abort());
            }
            let wait = match slick_http_slice(deadline, timeout.reason) {
                Ok(wait) => wait,
                Err(error) => {
                    if slick_http_context_cancelled() {
                        return Err(self.abort());
                    }
                    return Err(error);
                }
            };
            let Some(stream) = self.stream.as_mut() else { return Err(slick_http_cancel_io()) };
            if let Err(error) = stream.set_read_timeout(Some(wait)) {
                return Err(error.into());
            }
            let input = self.buffers.input_append_buf();
            match stream.read(input) {
                Ok(amount) => {
                    self.buffers.input_appended(amount);
                    return Ok(amount > 0);
                }
                Err(error) if error.kind() == std::io::ErrorKind::TimedOut || error.kind() == std::io::ErrorKind::WouldBlock => {}
                Err(error) => {
                    if slick_http_context_cancelled() {
                        return Err(self.abort());
                    }
                    return Err(error.into());
                }
            }
        }
    }

    fn is_open(&mut self) -> bool {
        let Some(stream) = self.stream.as_mut() else { return false };
        if stream.set_nonblocking(true).is_err() {
            return false;
        }
        let mut buf = [0u8; 1];
        let open = match std::io::Read::read(stream, &mut buf) {
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => true,
            _ => false,
        };
        if stream.set_nonblocking(false).is_err() {
            return false;
        }
        open
    }
}

// slick_http_validate mirrors the interpreter's validateHTTPRequest: method
// token, absolute http/https URL with a host and no userinfo, positive timeout
// and response limit, and header name, restricted-header, empty-value, and
// forbidden-control-byte checks. It returns the canonical header list with the
// Slick user agent appended when the caller did not supply one.
fn slick_http_validate(method: &str, url: &str, headers: &[(String, Vec<String>)], timeout_ms: i64, max_bytes: i64) -> Result<Vec<(String, Vec<String>)>, SlickValue> {
    if !slick_http_valid_token(method) {
        return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "method must be a non-empty HTTP token"));
    }
    let parsed = match url.parse::<ureq::http::Uri>() {
        Ok(parsed) => parsed,
        Err(_) => return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "URL must be an absolute http or https URL")),
    };
    let scheme = parsed.scheme_str().unwrap_or("");
    let host = parsed.host().unwrap_or("");
    if (!slick_http_eq_fold(scheme, "http") && !slick_http_eq_fold(scheme, "https")) || host.is_empty() {
        return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "URL must be an absolute http or https URL"));
    }
    if let Some(authority) = parsed.authority() {
        if authority.as_str().contains('@') {
            return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "URL userinfo is not allowed"));
        }
    }
    if timeout_ms <= 0 {
        return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "TimeoutMilliseconds must be positive"));
    }
    if max_bytes <= 0 {
        return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "MaxResponseBytes must be positive"));
    }
    let restricted = ["Host", "Content-Length", "Transfer-Encoding", "Connection"];
    let mut prepared: Vec<(String, Vec<String>)> = Vec::new();
    let mut has_user_agent = false;
    for (name, values) in headers {
        let canonical = slick_http_canonical(name);
        if !slick_http_valid_token(name) || canonical.is_empty() {
            return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, "invalid header name"));
        }
        if restricted.contains(&canonical.as_str()) {
            let message = format!("{} header cannot be controlled", canonical);
            return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, &message));
        }
        if values.is_empty() {
            let message = format!("{} header values must not be empty", canonical);
            return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, &message));
        }
        for value in values {
            if !slick_http_valid_field_value(value) {
                let message = format!("{} header value contains a forbidden control byte", canonical);
                return Err(slick_http_failure("InvalidRequest", &slick_http_sanitized(url), None, &message));
            }
        }
        if canonical == "User-Agent" {
            has_user_agent = true;
        }
        if let Some(entry) = prepared.iter_mut().find(|(candidate, _)| *candidate == canonical) {
            entry.1.extend(values.iter().cloned());
        } else {
            prepared.push((canonical, values.clone()));
        }
    }
    if !has_user_agent {
        prepared.push(("User-Agent".to_string(), vec!["Slick".to_string()]));
    }
    Ok(prepared)
}

// slick_http_valid_token is the HTTP tchar set: ASCII alphanumerics and the
// RFC 7230 special characters. The backtick (0x60) is matched by value so the
// Rust source never contains a backtick inside the Go raw string.
fn slick_http_valid_token(value: &str) -> bool {
    if value.is_empty() {
        return false;
    }
    for &byte in value.as_bytes() {
        let valid = (byte >= b'a' && byte <= b'z')
            || (byte >= b'A' && byte <= b'Z')
            || (byte >= b'0' && byte <= b'9')
            || matches!(byte, b'!' | b'#' | b'$' | b'%' | b'&' | b'\'' | b'*' | b'+' | b'-' | b'.' | b'^' | b'_' | 0x60 | b'|' | b'~');
        if !valid {
            return false;
        }
    }
    true
}

// slick_http_valid_field_value rejects every byte except tab and the printable
// range, treating 0x7f (DEL) as forbidden, exactly like the interpreter.
fn slick_http_valid_field_value(value: &str) -> bool {
    for &byte in value.as_bytes() {
        if byte != b'\t' && (byte < 0x20 || byte == 0x7f) {
            return false;
        }
    }
    true
}

// slick_http_canonical reproduces net/http CanonicalHeaderKey: the first byte
// and every byte following a hyphen is upper-cased, the rest lower-cased.
fn slick_http_canonical(name: &str) -> String {
    let mut upper = true;
    let mut out: Vec<u8> = Vec::with_capacity(name.len());
    for &byte in name.as_bytes() {
        let converted = if upper && byte >= b'a' && byte <= b'z' {
            byte - 32
        } else if !upper && byte >= b'A' && byte <= b'Z' {
            byte + 32
        } else {
            byte
        };
        out.push(converted);
        upper = converted == b'-';
    }
    String::from_utf8(out).unwrap_or_else(|_| name.to_string())
}

// slick_http_sanitized strips userinfo, query, and fragment and re-stringifies,
// matching the interpreter's sanitizedHTTPURL used for failure URLs.
fn slick_http_sanitized(raw: &str) -> String {
    let parsed = match raw.parse::<ureq::http::Uri>() {
        Ok(parsed) => parsed,
        Err(_) => return String::new(),
    };
    let scheme = parsed.scheme_str().unwrap_or("");
    let host_part = match parsed.authority() {
        Some(authority) => {
            let authority_str = authority.as_str();
            match authority_str.rfind('@') {
                Some(position) => &authority_str[position + 1..],
                None => authority_str,
            }
        }
        None => "",
    };
    let path = parsed.path();
    if scheme.is_empty() {
        if host_part.is_empty() { path.to_string() } else { format!("//{}{}", host_part, path) }
    } else {
        format!("{}://{}{}", scheme, host_part, path)
    }
}

fn slick_http_eq_fold(left: &str, right: &str) -> bool {
    left.eq_ignore_ascii_case(right)
}

fn slick_http_is_redirect(status: i64) -> bool {
    matches!(status, 301 | 302 | 303 | 307 | 308)
}

// slick_http_response_headers merges response header values by their canonical
// name and sorts the canonical names, matching the interpreter's deterministic
// header ordering. ureq delivers lowercase header names; canonicalization
// restores the Go net/http form the interpreter exposes.
fn slick_http_response_headers(headers: &ureq::http::HeaderMap) -> Vec<(String, Vec<String>)> {
    let mut merged: Vec<(String, Vec<String>)> = Vec::new();
    for (name, value) in headers.iter() {
        let canonical = slick_http_canonical(name.as_str());
        let text = String::from_utf8_lossy(value.as_bytes()).into_owned();
        if let Some(entry) = merged.iter_mut().find(|(candidate, _)| *candidate == canonical) {
            entry.1.push(text);
        } else {
            merged.push((canonical, vec![text]));
        }
    }
    merged.sort_by(|left, right| left.0.cmp(&right.0));
    merged
}

fn slick_http_optional_field(request: &SlickValue, name: &str) -> Option<SlickValue> {
    match slick_field(request, name) {
        Ok(SlickValue::Optional(Some(value))) => Some(*value),
        Ok(SlickValue::Optional(None)) | Ok(SlickValue::Null) => None,
        Ok(value) => Some(value),
        Err(_) => None,
    }
}

fn slick_http_failure(kind: &str, url: &str, status: Option<i64>, message: &str) -> SlickValue {
    let status_value = match status {
        Some(code) => SlickValue::Optional(Some(Box::new(SlickValue::Int(code)))),
        None => SlickValue::Optional(None),
    };
    slick_object("std.http.Failure", vec![
        ("Kind", SlickValue::String(kind.to_string())),
        ("URL", SlickValue::String(url.to_string())),
        ("Status", status_value),
        ("Message", SlickValue::String(message.to_string())),
    ])
}

fn slick_http_cancelled(url: &str, status: Option<i64>) -> SlickOutcome {
    slick_err(slick_http_failure("Cancelled", url, status, "HTTP request cancelled"))
}

// slick_http_proxy_for honours the same environment the interpreter's
// net/http.ProxyFromEnvironment reads: HTTP_PROXY/HTTPS_PROXY/NO_PROXY and
// their lowercase forms, CGI suppression of HTTP_PROXY, implicit loopback
// bypass, and comma-separated NO_PROXY host/suffix/port/CIDR/* rules, including
// bracketed IPv6 authorities such as [2001:db8::10]:8443. An omitted URL port
// is canonicalized to 80 for http and 443 for https so a NO_PROXY entry of
// host:80 matches http://host. Every read goes through slick_environment_read
// so a Slick std.env.Set is visible here exactly as a host assignment is
// visible to the interpreter.
fn slick_http_proxy_for(url: &str) -> Option<ureq::Proxy> {
    let parsed = url.parse::<ureq::http::Uri>().ok()?;
    let host = parsed.host().unwrap_or("");
    let scheme = parsed.scheme_str().unwrap_or("http");
    let port = slick_http_canonical_port(scheme, parsed.port_u16());
    let no_proxy = slick_http_env_lookup("NO_PROXY", "no_proxy").unwrap_or_default();
    let proxy_value = if slick_http_eq_fold(scheme, "https") {
        slick_http_env_lookup("HTTPS_PROXY", "https_proxy")
    } else if slick_http_cgi_request_method() {
        None
    } else {
        slick_http_env_lookup("HTTP_PROXY", "http_proxy")
    };
    let proxy_value = proxy_value?;
    if slick_http_bypass_proxy(host, port, &no_proxy) {
        return None;
    }
    ureq::Proxy::new(&proxy_value).ok()
}

fn slick_http_env_lookup(upper: &str, lower: &str) -> Option<String> {
    match slick_environment_read(upper) {
        Some(value) if !value.is_empty() => return Some(value),
        _ => {}
    }
    match slick_environment_read(lower) {
        Some(value) if !value.is_empty() => Some(value),
        _ => None,
    }
}

fn slick_http_cgi_request_method() -> bool {
    match slick_environment_read("REQUEST_METHOD") {
        Some(value) => !value.is_empty(),
        None => false,
    }
}

fn slick_http_canonical_port(scheme: &str, port: Option<u16>) -> u16 {
    match port {
        Some(port) => port,
        None if slick_http_eq_fold(scheme, "https") => 443,
        None => 80,
    }
}

fn slick_http_parse_ip(host: &str) -> Option<std::net::IpAddr> {
    let host = match host.strip_prefix('[') {
        Some(inner) => inner.strip_suffix(']').unwrap_or(host),
        None => host,
    };
    host.parse().ok()
}

fn slick_http_parse_cidr(entry: &str) -> Option<(std::net::IpAddr, u32)> {
    let (addr, bits) = entry.rsplit_once('/')?;
    let prefix: u32 = bits.parse().ok()?;
    let ip = slick_http_parse_ip(addr)?;
    let maximum = if ip.is_ipv4() { 32 } else { 128 };
    if prefix > maximum {
        return None;
    }
    Some((ip, prefix))
}

fn slick_http_ip_in_cidr(ip: std::net::IpAddr, network: std::net::IpAddr, prefix: u32) -> bool {
    match (ip, network) {
        (std::net::IpAddr::V4(ip), std::net::IpAddr::V4(network)) => {
            if prefix == 0 {
                return true;
            }
            let shift = 32 - prefix;
            (u32::from(ip) >> shift) == (u32::from(network) >> shift)
        }
        (std::net::IpAddr::V6(ip), std::net::IpAddr::V6(network)) => {
            if prefix == 0 {
                return true;
            }
            let shift = 128 - prefix;
            (u128::from(ip) >> shift) == (u128::from(network) >> shift)
        }
        _ => false,
    }
}

// slick_http_split_host_port mirrors net.SplitHostPort for a NO_PROXY entry:
// "host:port" and "[ipv6]:port" yield the host (brackets stripped) and port.
// Missing port or too many colons is None so the caller keeps the whole entry.
fn slick_http_split_host_port(entry: &str) -> Option<(&str, Option<u16>)> {
    let colon = entry.rfind(':')?;
    let (host, port_text) = if let Some(rest) = entry.strip_prefix('[') {
        let end = rest.find(']')?;
        if 1 + end + 1 != colon {
            return None;
        }
        (&rest[..end], &entry[colon + 1..])
    } else {
        let host = &entry[..colon];
        if host.contains(':') {
            return None;
        }
        (host, &entry[colon + 1..])
    };
    if host.is_empty() {
        return None;
    }
    if port_text.is_empty() {
        return Some((host, None));
    }
    if !port_text.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    Some((host, port_text.parse::<u16>().ok()))
}

fn slick_http_bypass_proxy(host: &str, port: u16, no_proxy: &str) -> bool {
    if slick_http_is_loopback_host(host) {
        return true;
    }
    let host_ip = slick_http_parse_ip(host);
    for raw in no_proxy.split(',') {
        let entry = raw.trim();
        if entry.is_empty() {
            continue;
        }
        if entry == "*" {
            return true;
        }
        if let Some((network, prefix)) = slick_http_parse_cidr(entry) {
            if let Some(ip) = host_ip {
                if slick_http_ip_in_cidr(ip, network, prefix) {
                    return true;
                }
            }
            continue;
        }
        let (entry_host, entry_port) = match slick_http_split_host_port(entry) {
            Some(split) => split,
            None => (entry, None),
        };
        if let Some(want) = entry_port {
            if port != want {
                continue;
            }
        }
        if slick_http_no_proxy_host(host, entry_host) {
            return true;
        }
    }
    false
}

fn slick_http_is_loopback_host(host: &str) -> bool {
    if host.eq_ignore_ascii_case("localhost") {
        return true;
    }
    match slick_http_parse_ip(host) {
        Some(ip) => ip.is_loopback(),
        None => false,
    }
}

fn slick_http_no_proxy_host(host: &str, pattern: &str) -> bool {
    // Go strips a leading "*" so "*.corp.example" is the domain suffix rule.
    let pattern = pattern.strip_prefix('*').unwrap_or(pattern);
    if let Some(domain) = pattern.strip_prefix('.') {
        return slick_http_host_has_suffix(host, domain);
    }
    if let (Some(host_ip), Some(pattern_ip)) = (slick_http_parse_ip(host), slick_http_parse_ip(pattern)) {
        return host_ip == pattern_ip;
    }
    host.eq_ignore_ascii_case(pattern) || slick_http_host_has_suffix(host, pattern)
}

fn slick_http_host_has_suffix(host: &str, suffix: &str) -> bool {
    if suffix.is_empty() {
        return false;
    }
    let host = host.to_ascii_lowercase();
    let suffix = suffix.to_ascii_lowercase();
    match host.strip_suffix(suffix.as_str()) {
        Some(rest) => rest.ends_with('.'),
        None => false,
    }
}

fn slick_http_response(status: i64, url: &str, headers: Vec<(String, Vec<String>)>, body: Vec<u8>) -> SlickValue {
    let entries: Vec<(SlickValue, SlickValue)> = headers.into_iter().map(|(name, values)| {
        let name_value = SlickValue::String(name);
        let values_array = SlickValue::Array(values.into_iter().map(SlickValue::String).collect());
        (name_value, values_array)
    }).collect();
    slick_object("std.http.Response", vec![
        ("Status", SlickValue::Int(status)),
        ("URL", SlickValue::String(url.to_string())),
        ("Headers", SlickValue::Map(entries)),
        ("Body", SlickValue::Bytes(body)),
    ])
}

// slick_http_status_text is the net/http StatusText table for codes 0..=999.
fn slick_http_status_text(code: i64) -> Option<&'static str> {
    if code < 0 || code > 999 {
        return None;
    }
    match code {
        100 => Some("Continue"),
        101 => Some("Switching Protocols"),
        102 => Some("Processing"),
        103 => Some("Early Hints"),
        200 => Some("OK"),
        201 => Some("Created"),
        202 => Some("Accepted"),
        203 => Some("Non-Authoritative Information"),
        204 => Some("No Content"),
        205 => Some("Reset Content"),
        206 => Some("Partial Content"),
        207 => Some("Multi-Status"),
        208 => Some("Already Reported"),
        226 => Some("IM Used"),
        300 => Some("Multiple Choices"),
        301 => Some("Moved Permanently"),
        302 => Some("Found"),
        303 => Some("See Other"),
        304 => Some("Not Modified"),
        305 => Some("Use Proxy"),
        307 => Some("Temporary Redirect"),
        308 => Some("Permanent Redirect"),
        400 => Some("Bad Request"),
        401 => Some("Unauthorized"),
        402 => Some("Payment Required"),
        403 => Some("Forbidden"),
        404 => Some("Not Found"),
        405 => Some("Method Not Allowed"),
        406 => Some("Not Acceptable"),
        407 => Some("Proxy Authentication Required"),
        408 => Some("Request Timeout"),
        409 => Some("Conflict"),
        410 => Some("Gone"),
        411 => Some("Length Required"),
        412 => Some("Precondition Failed"),
        413 => Some("Request Entity Too Large"),
        414 => Some("Request URI Too Long"),
        415 => Some("Unsupported Media Type"),
        416 => Some("Requested Range Not Satisfiable"),
        417 => Some("Expectation Failed"),
        418 => Some("I'm a teapot"),
        421 => Some("Misdirected Request"),
        422 => Some("Unprocessable Entity"),
        423 => Some("Locked"),
        424 => Some("Failed Dependency"),
        425 => Some("Too Early"),
        426 => Some("Upgrade Required"),
        428 => Some("Precondition Required"),
        429 => Some("Too Many Requests"),
        431 => Some("Request Header Fields Too Large"),
        451 => Some("Unavailable For Legal Reasons"),
        500 => Some("Internal Server Error"),
        501 => Some("Not Implemented"),
        502 => Some("Bad Gateway"),
        503 => Some("Service Unavailable"),
        504 => Some("Gateway Timeout"),
        505 => Some("HTTP Version Not Supported"),
        506 => Some("Variant Also Negotiates"),
        507 => Some("Insufficient Storage"),
        508 => Some("Loop Detected"),
        510 => Some("Not Extended"),
        511 => Some("Network Authentication Required"),
        _ => None,
    }
}
`
