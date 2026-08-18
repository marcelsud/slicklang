package compiler

var rustStdHTTPServer = rustStdFamily{
	family: runtimeFamilyHTTPServer,
	module: rustStdHTTPServerModule,
	functions: map[runtimeOperationID]string{
		nativeStdHTTPServerServe: "slick_nat_http_server_serve",
	},
}

// rustStdHTTPServerModule implements std.http.server.Serve for the Rust backend
// using only the Rust standard library (std::net, threads, timeouts). It mirrors
// internal/compiler/stdhttpserver.go and the LLVM C reference in llvmlib/natives.c:
// it honours every Config field, parses HTTP/1.1 requests into the declared
// Request, dispatches to the Slick handler through slick_call_method, writes the
// declared Response, reports Failure for bind and serve faults, and performs
// documented cancellation and graceful-shutdown: stop accepting, cancel in-flight
// handlers, prune finished workers continuously, and at the shutdown deadline
// close remaining sockets without joining so ShutdownTimeoutMilliseconds is
// honoured. SIGINT/SIGTERM use a process-wide reference-counted broker so
// overlapping Serve calls share one installation and restore the original
// disposition only when the last server returns. No operation panics (the
// release profile builds with panic=abort), so all locking, decoding, and
// arithmetic is checked or bounded. The raw string contains no backtick.
const rustStdHTTPServerModule = `struct SlickHTTPServerConfig {
    address: String,
    max_header_bytes: i64,
    max_body_bytes: i64,
    read_header_timeout_ms: i64,
    read_timeout_ms: i64,
    write_timeout_ms: i64,
    idle_timeout_ms: i64,
    shutdown_timeout_ms: i64,
}

#[derive(Clone, Copy)]
struct SlickHTTPServerLimits {
    max_header_bytes: i64,
    max_body_bytes: i64,
    read_header_timeout_ms: i64,
    read_timeout_ms: i64,
    write_timeout_ms: i64,
    idle_timeout_ms: i64,
}

struct SlickHTTPServerWorker {
    handle: JoinHandle<()>,
    closer: Option<std::net::TcpStream>,
}

enum SlickHTTPServerError {
    Malformed,
    Overflow,
}

// A process-wide stop flag set by the SIGINT/SIGTERM handler. The Rust runtime
// does not install signal handlers, so Serve installs its own (mirroring the LLVM
// runtime) so the documented "blocks until SIGINT, SIGTERM" contract holds and a
// graceful shutdown can drain in-flight handlers instead of terminating abruptly.
// A reference-counted broker owns the process dispositions: the first Serve
// captures the original handlers, overlapping Serves only increment the count,
// and the last return restores the originals so a later signal is not swallowed.
static SLICK_HTTP_SERVER_STOP: AtomicBool = AtomicBool::new(false);

struct SlickHTTPServerSignalBroker {
    refs: usize,
    previous: Option<(usize, usize)>,
}

static SLICK_HTTP_SERVER_SIGNALS: Mutex<SlickHTTPServerSignalBroker> = Mutex::new(SlickHTTPServerSignalBroker {
    refs: 0,
    previous: None,
});

unsafe extern "C" {
    #[link_name = "signal"]
    fn slick_http_server_signal(signum: i32, handler: usize) -> usize;
}

extern "C" fn slick_http_server_handle_signal(_signum: i32) {
    SLICK_HTTP_SERVER_STOP.store(true, Ordering::Release);
}

fn slick_http_server_acquire_signals() {
    let mut broker = SLICK_HTTP_SERVER_SIGNALS.lock().unwrap_or_else(|error| error.into_inner());
    if broker.refs == 0 {
        SLICK_HTTP_SERVER_STOP.store(false, Ordering::Release);
        let handler = slick_http_server_handle_signal as usize;
        broker.previous = Some(unsafe {
            (slick_http_server_signal(2, handler), slick_http_server_signal(15, handler))
        });
    }
    broker.refs = broker.refs.saturating_add(1);
}

fn slick_http_server_release_signals() {
    let mut broker = SLICK_HTTP_SERVER_SIGNALS.lock().unwrap_or_else(|error| error.into_inner());
    if broker.refs == 0 {
        return;
    }
    broker.refs -= 1;
    if broker.refs != 0 {
        return;
    }
    if let Some(previous) = broker.previous.take() {
        unsafe {
            slick_http_server_signal(2, previous.0);
            slick_http_server_signal(15, previous.1);
        }
    }
}

fn slick_http_server_failure(operation: &str, address: &str, message: &str) -> SlickValue {
    let text = if message.trim().is_empty() { "operation failed" } else { message };
    slick_object("std.http.server.Failure", vec![
        ("Operation", slick_string(operation)),
        ("Address", slick_string(address)),
        ("Message", slick_string(text)),
    ])
}

fn slick_http_server_optional_int(value: &SlickValue, name: &str) -> Option<i64> {
    match slick_field(value, name) {
        Ok(SlickValue::Optional(Some(inner))) => match inner.as_ref() {
            SlickValue::Int(n) => Some(*n),
            _ => None,
        },
        Ok(SlickValue::Int(n)) => Some(n),
        _ => None,
    }
}

fn slick_http_server_config(value: &SlickValue) -> SlickHTTPServerConfig {
    let address = match slick_field(value, "Address") {
        Ok(SlickValue::String(s)) => s,
        _ => String::new(),
    };
    SlickHTTPServerConfig {
        address,
        max_header_bytes: slick_http_server_optional_int(value, "MaxHeaderBytes").unwrap_or(1 << 20),
        max_body_bytes: slick_http_server_optional_int(value, "MaxBodyBytes").unwrap_or(8 << 20),
        read_header_timeout_ms: slick_http_server_optional_int(value, "ReadHeaderTimeoutMilliseconds").unwrap_or(10_000),
        read_timeout_ms: slick_http_server_optional_int(value, "ReadTimeoutMilliseconds").unwrap_or(30_000),
        write_timeout_ms: slick_http_server_optional_int(value, "WriteTimeoutMilliseconds").unwrap_or(30_000),
        idle_timeout_ms: slick_http_server_optional_int(value, "IdleTimeoutMilliseconds").unwrap_or(120_000),
        shutdown_timeout_ms: slick_http_server_optional_int(value, "ShutdownTimeoutMilliseconds").unwrap_or(30_000),
    }
}

// slick_http_server_task_safe mirrors the runtime task-safety check the
// interpreter and generated Go perform: a Buffer or a resource-bearing Object is
// not task-safe, and the recursion extends through every composite a handler can
// carry. The compile-time path already rejects task-unsafe handler types; this
// guards the dynamic case where an unsafe value reaches Serve through the
// Handler interface.
fn slick_http_server_task_safe(value: &SlickValue) -> bool {
    match value {
        SlickValue::Null | SlickValue::Bool(_) | SlickValue::Int(_) | SlickValue::Float(_)
        | SlickValue::String(_) | SlickValue::Bytes(_) | SlickValue::Range(_, _) => true,
        SlickValue::Buffer(_) => false,
        SlickValue::Array(items) | SlickValue::Tuple(items) => items.iter().all(slick_http_server_task_safe),
        SlickValue::Map(entries) => entries.iter().all(|(key, val)| {
            slick_http_server_task_safe(key) && slick_http_server_task_safe(val)
        }),
        SlickValue::Optional(Some(inner)) => slick_http_server_task_safe(inner.as_ref()),
        SlickValue::Optional(None) => true,
        SlickValue::Result(_, payload) => slick_http_server_task_safe(payload),
        SlickValue::Object { fields, resource, .. } => {
            resource.is_none() && fields.iter().all(|(_, val)| slick_http_server_task_safe(val))
        }
        SlickValue::Union { fields, .. } => fields.iter().all(slick_http_server_task_safe),
        SlickValue::Callable(callable) => callable.captures.iter().all(slick_http_server_task_safe),
        SlickValue::Enumerate(_) | SlickValue::Zip(_) => false,
    }
}

fn slick_http_server_validate_config(config: &SlickHTTPServerConfig) -> Option<SlickValue> {
    if config.address.trim().is_empty() {
        return Some(slick_http_server_failure("Config", &config.address, "Address must not be empty"));
    }
    let checks: [(&str, i64); 7] = [
        ("MaxHeaderBytes", config.max_header_bytes),
        ("MaxBodyBytes", config.max_body_bytes),
        ("ReadHeaderTimeoutMilliseconds", config.read_header_timeout_ms),
        ("ReadTimeoutMilliseconds", config.read_timeout_ms),
        ("WriteTimeoutMilliseconds", config.write_timeout_ms),
        ("IdleTimeoutMilliseconds", config.idle_timeout_ms),
        ("ShutdownTimeoutMilliseconds", config.shutdown_timeout_ms),
    ];
    for (name, value) in checks {
        if value <= 0 {
            return Some(slick_http_server_failure("Config", &config.address, &format!("{} must be positive", name)));
        }
    }
    None
}

// slick_http_server_status_reason is Go net/http StatusText plus the
// "status code NNN" fallback WriteHeader uses for unregistered codes.
fn slick_http_server_status_reason(status: i64) -> String {
    let text = match status {
        100 => "Continue",
        101 => "Switching Protocols",
        102 => "Processing",
        103 => "Early Hints",
        200 => "OK",
        201 => "Created",
        202 => "Accepted",
        203 => "Non-Authoritative Information",
        204 => "No Content",
        205 => "Reset Content",
        206 => "Partial Content",
        207 => "Multi-Status",
        208 => "Already Reported",
        226 => "IM Used",
        300 => "Multiple Choices",
        301 => "Moved Permanently",
        302 => "Found",
        303 => "See Other",
        304 => "Not Modified",
        305 => "Use Proxy",
        307 => "Temporary Redirect",
        308 => "Permanent Redirect",
        400 => "Bad Request",
        401 => "Unauthorized",
        402 => "Payment Required",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        406 => "Not Acceptable",
        407 => "Proxy Authentication Required",
        408 => "Request Timeout",
        409 => "Conflict",
        410 => "Gone",
        411 => "Length Required",
        412 => "Precondition Failed",
        413 => "Request Entity Too Large",
        414 => "Request URI Too Long",
        415 => "Unsupported Media Type",
        416 => "Requested Range Not Satisfiable",
        417 => "Expectation Failed",
        418 => "I'm a teapot",
        421 => "Misdirected Request",
        422 => "Unprocessable Entity",
        423 => "Locked",
        424 => "Failed Dependency",
        425 => "Too Early",
        426 => "Upgrade Required",
        428 => "Precondition Required",
        429 => "Too Many Requests",
        431 => "Request Header Fields Too Large",
        451 => "Unavailable For Legal Reasons",
        500 => "Internal Server Error",
        501 => "Not Implemented",
        502 => "Bad Gateway",
        503 => "Service Unavailable",
        504 => "Gateway Timeout",
        505 => "HTTP Version Not Supported",
        506 => "Variant Also Negotiates",
        507 => "Insufficient Storage",
        508 => "Loop Detected",
        510 => "Not Extended",
        511 => "Network Authentication Required",
        _ => return format!("status code {:03}", status),
    };
    text.to_string()
}

fn slick_http_server_valid_token(value: &str) -> bool {
    if value.is_empty() {
        return false;
    }
    value.bytes().all(|c| {
        c.is_ascii_alphanumeric()
            || matches!(c, b'!' | b'#' | b'$' | b'%' | b'&' | b'\'' | b'*' | b'+' | b'-' | b'.' | b'^' | b'_' | 0x60 | b'|' | b'~')
    })
}

fn slick_http_server_valid_field_value(value: &str) -> bool {
    value.bytes().all(|c| c == b'\t' || (c >= 0x20 && c != 0x7f))
}

fn slick_http_server_canonical_header(name: &str) -> String {
    let mut out = String::with_capacity(name.len());
    let mut upper = true;
    for c in name.chars() {
        if upper {
            out.extend(c.to_uppercase());
        } else {
            out.extend(c.to_lowercase());
        }
        upper = c == '-';
    }
    out
}

fn slick_http_server_is_hop_by_hop(name: &str) -> bool {
    matches!(name, "Connection" | "Keep-Alive" | "Proxy-Connection" | "Transfer-Encoding" | "Te" | "Trailer" | "Upgrade")
}

fn slick_http_server_trim(value: &str) -> &str {
    value.trim_matches(|c: char| c == ' ' || c == '\t')
}

fn slick_http_server_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

// slick_http_server_url_decode decodes application/x-www-form-urlencoded content
// (plus to space, percent-hex to a byte) and validates UTF-8, matching the
// interpreter url.QueryUnescape behaviour used by parseHTTPServerQuery.
fn slick_http_server_url_decode(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        if byte == b'+' {
            out.push(b' ');
            index += 1;
        } else if byte == b'%' {
            if index + 2 >= bytes.len() {
                return None;
            }
            let high = slick_http_server_hex(bytes[index + 1])?;
            let low = slick_http_server_hex(bytes[index + 2])?;
            out.push((high << 4) | low);
            index += 3;
        } else {
            out.push(byte);
            index += 1;
        }
    }
    String::from_utf8(out).ok()
}

// slick_http_server_path_decode applies URL path unescape rules (percent-hex
// only; plus stays plus), matching net/url PathUnescape / Request.URL.Path.
fn slick_http_server_path_decode(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        if byte == b'%' {
            if index + 2 >= bytes.len() {
                return None;
            }
            let high = slick_http_server_hex(bytes[index + 1])?;
            let low = slick_http_server_hex(bytes[index + 2])?;
            out.push((high << 4) | low);
            index += 3;
        } else {
            out.push(byte);
            index += 1;
        }
    }
    String::from_utf8(out).ok()
}

fn slick_http_server_map_append(map: &mut Vec<(SlickValue, SlickValue)>, key: &str, value: &str) {
    let key_value = slick_string(key);
    for (stored, bucket) in map.iter_mut() {
        if slick_equal(stored, &key_value) {
            if let SlickValue::Array(items) = bucket {
                items.push(slick_string(value));
            }
            return;
        }
    }
    map.push((key_value, SlickValue::Array(vec![slick_string(value)])));
}

fn slick_http_server_parse_query(raw: &str, map: &mut Vec<(SlickValue, SlickValue)>) -> bool {
    if raw.is_empty() {
        return true;
    }
    for pair in raw.split('&') {
        if pair.is_empty() {
            continue;
        }
        let (key, value) = match pair.split_once('=') {
            Some((k, v)) => (k, v),
            None => (pair, ""),
        };
        let decoded_key = match slick_http_server_url_decode(key) {
            Some(s) => s,
            None => return false,
        };
        let decoded_value = match slick_http_server_url_decode(value) {
            Some(s) => s,
            None => return false,
        };
        slick_http_server_map_append(map, &decoded_key, &decoded_value);
    }
    true
}

fn slick_http_server_header_terminator(buf: &[u8]) -> Option<usize> {
    if buf.len() < 4 {
        return None;
    }
    buf.windows(4).position(|window| window == b"\r\n\r\n")
}

fn slick_http_server_response_headers(value: &SlickValue) -> Vec<(String, Vec<String>)> {
    match slick_field(value, "Headers") {
        Ok(SlickValue::Optional(Some(inner))) => match inner.as_ref() {
            SlickValue::Map(entries) => entries.iter().filter_map(|(key, bucket)| {
                let name = if let SlickValue::String(text) = key { text.clone() } else { return None };
                let values: Vec<String> = match bucket {
                    SlickValue::Array(items) => items.iter().filter_map(|item| {
                        if let SlickValue::String(text) = item { Some(text.clone()) } else { None }
                    }).collect(),
                    _ => Vec::new(),
                };
                Some((name, values))
            }).collect(),
            _ => Vec::new(),
        },
        _ => Vec::new(),
    }
}

fn slick_http_server_connection_close(version: &str, headers: &[(String, String)]) -> bool {
    let mut close = false;
    let mut keep_alive = false;
    for (name, value) in headers {
        if name != "Connection" {
            continue;
        }
        for token in value.split(',') {
            let trimmed = slick_http_server_trim(token);
            if trimmed.eq_ignore_ascii_case("close") {
                close = true;
            } else if trimmed.eq_ignore_ascii_case("keep-alive") {
                keep_alive = true;
            }
        }
    }
    if version == "HTTP/1.0" {
        close || !keep_alive
    } else {
        close
    }
}

fn slick_http_server_now_deadline(timeout_ms: i64) -> std::time::Instant {
    let millis = if timeout_ms < 0 { 0 } else { timeout_ms as u64 };
    std::time::Instant::now() + std::time::Duration::from_millis(millis)
}

fn slick_http_server_set_read_deadline(stream: &std::net::TcpStream, deadline: std::time::Instant) -> bool {
    let now = std::time::Instant::now();
    if now >= deadline {
        return false;
    }
    let _ = stream.set_read_timeout(Some(deadline.saturating_duration_since(now)));
    true
}

fn slick_http_server_set_write_timeout(stream: &std::net::TcpStream, timeout_ms: i64) {
    let millis = if timeout_ms < 1 { 1 } else { timeout_ms as u64 };
    let _ = stream.set_write_timeout(Some(std::time::Duration::from_millis(millis)));
}

fn slick_http_server_fill(
    stream: &std::net::TcpStream,
    pending: &mut Vec<u8>,
    deadline: std::time::Instant,
) -> Option<usize> {
    if !slick_http_server_set_read_deadline(stream, deadline) {
        return None;
    }
    let mut temporary = [0u8; 4096];
    match std::io::Read::read(&mut &*stream, &mut temporary) {
        Ok(0) => Some(0),
        Ok(count) => {
            pending.extend_from_slice(&temporary[..count]);
            Some(count)
        }
        Err(_) => None,
    }
}

fn slick_http_server_write_simple(stream: &std::net::TcpStream, status: i64) {
    use std::io::Write;
    let reason = slick_http_server_status_reason(status);
    let mut out: Vec<u8> = Vec::new();
    let _ = write!(out, "HTTP/1.1 {} {}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status, reason);
    let _ = std::io::Write::write_all(&mut &*stream, &out);
}

fn slick_http_server_write_response(
    stream: &std::net::TcpStream,
    method: &str,
    proto: &str,
    keep_alive: bool,
    response: &SlickValue,
) -> bool {
    use std::io::Write;
    let status = match slick_field(response, "Status") {
        Ok(SlickValue::Int(value)) => value,
        _ => {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
    };
    if !(200..=599).contains(&status) {
        slick_http_server_write_simple(stream, 500);
        return false;
    }
    let body = match slick_field(response, "Body") {
        Ok(SlickValue::Bytes(bytes)) => bytes,
        _ => {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
    };
    let header_pairs = slick_http_server_response_headers(response);
    for (name, values) in &header_pairs {
        let canonical = slick_http_server_canonical_header(name);
        if slick_http_server_is_hop_by_hop(&canonical) {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
        if !slick_http_server_valid_token(name) || canonical.is_empty() {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
        if canonical == "Content-Length" || canonical == "Host" || canonical == "Transfer-Encoding" {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
        if values.is_empty() {
            slick_http_server_write_simple(stream, 500);
            return false;
        }
        for value in values {
            if !slick_http_server_valid_field_value(value) {
                slick_http_server_write_simple(stream, 500);
                return false;
            }
        }
    }
    let suppress = method == "HEAD" || status == 204 || status == 205 || status == 304;
    let body_length: i64 = if method == "HEAD" {
        body.len() as i64
    } else if suppress {
        0
    } else {
        body.len() as i64
    };
    let reason = slick_http_server_status_reason(status);
    let mut out: Vec<u8> = Vec::new();
    let _ = write!(out, "{} {} {}\r\n", proto, status, reason);
    for (name, values) in &header_pairs {
        let canonical = slick_http_server_canonical_header(name);
        for value in values {
            let _ = write!(out, "{}: {}\r\n", canonical, value);
        }
    }
    let _ = write!(out, "Content-Length: {}\r\n", body_length);
    if keep_alive {
        if proto == "HTTP/1.0" {
            let _ = write!(out, "Connection: keep-alive\r\n");
        }
    } else {
        let _ = write!(out, "Connection: close\r\n");
    }
    let _ = write!(out, "\r\n");
    if method != "HEAD" && !suppress && !body.is_empty() {
        out.extend_from_slice(&body);
    }
    let _ = std::io::Write::write_all(&mut &*stream, &out);
    keep_alive
}

// SlickHTTPServerInput feeds the request-body decoder from the bytes already
// read past the header terminator and then from the live stream, so a chunked or
// content-length body never re-reads the header block.
struct SlickHTTPServerInput<'a> {
    stream: &'a std::net::TcpStream,
    pending: &'a [u8],
    leftover_pos: usize,
    deadline: std::time::Instant,
}

impl<'a> SlickHTTPServerInput<'a> {
    fn fill(&mut self, destination: &mut [u8]) -> Option<usize> {
        if self.leftover_pos < self.pending.len() {
            let take = (self.pending.len() - self.leftover_pos).min(destination.len());
            destination[..take].copy_from_slice(&self.pending[self.leftover_pos..self.leftover_pos + take]);
            self.leftover_pos += take;
            return Some(take);
        }
        if !slick_http_server_set_read_deadline(self.stream, self.deadline) {
            return None;
        }
        match std::io::Read::read(&mut self.stream, destination) {
            Ok(0) => None,
            Ok(count) => Some(count),
            Err(_) => None,
        }
    }

    fn read_exact(&mut self, destination: &mut [u8]) -> bool {
        let mut filled = 0;
        while filled < destination.len() {
            match self.fill(&mut destination[filled..]) {
                Some(count) if count > 0 => filled += count,
                _ => return false,
            }
        }
        true
    }
}

fn slick_http_server_read_line(input: &mut SlickHTTPServerInput, max: usize) -> Result<String, SlickHTTPServerError> {
    let mut bytes: Vec<u8> = Vec::new();
    let mut single = [0u8; 1];
    loop {
        if input.fill(&mut single).is_none() {
            return Err(SlickHTTPServerError::Malformed);
        }
        if single[0] == b'\r' {
            let mut newline = [0u8; 1];
            if input.fill(&mut newline).is_none() || newline[0] != b'\n' {
                return Err(SlickHTTPServerError::Malformed);
            }
            return Ok(String::from_utf8_lossy(&bytes).into_owned());
        }
        if single[0] == b'\n' {
            return Err(SlickHTTPServerError::Malformed);
        }
        bytes.push(single[0]);
        if bytes.len() > max {
            return Err(SlickHTTPServerError::Malformed);
        }
    }
}

fn slick_http_server_read_chunked(input: &mut SlickHTTPServerInput, max_body: usize, max_header: usize) -> Result<Vec<u8>, SlickHTTPServerError> {
    let mut body: Vec<u8> = Vec::new();
    let mut trailer_bytes: usize = 0;
    loop {
        let line = slick_http_server_read_line(input, max_header)?;
        let size_text = match line.split_once(';') {
            Some((before, _)) => before.trim(),
            None => line.trim(),
        };
        if size_text.is_empty() {
            return Err(SlickHTTPServerError::Malformed);
        }
        let chunk = match usize::from_str_radix(size_text, 16) {
            Ok(value) => value,
            Err(_) => return Err(SlickHTTPServerError::Malformed),
        };
        if chunk == 0 {
            loop {
                let trailer = slick_http_server_read_line(input, max_header)?;
                trailer_bytes = trailer_bytes.saturating_add(trailer.len()).saturating_add(2);
                if trailer_bytes > max_header {
                    return Err(SlickHTTPServerError::Malformed);
                }
                if trailer.is_empty() {
                    return Ok(body);
                }
            }
        }
        if chunk > max_body || body.len() > max_body - chunk {
            return Err(SlickHTTPServerError::Overflow);
        }
        let mut remaining = chunk;
        while remaining > 0 {
            let take = remaining.min(4096);
            let mut piece = [0u8; 4096];
            if !input.read_exact(&mut piece[..take]) {
                return Err(SlickHTTPServerError::Malformed);
            }
            body.extend_from_slice(&piece[..take]);
            remaining -= take;
        }
        let mut ending = [0u8; 2];
        if !input.read_exact(&mut ending) || ending[0] != b'\r' || ending[1] != b'\n' {
            return Err(SlickHTTPServerError::Malformed);
        }
    }
}

fn slick_http_server_read_fixed_body(
    stream: &std::net::TcpStream,
    pending: &mut Vec<u8>,
    start: usize,
    need: usize,
    deadline: std::time::Instant,
) -> Result<Vec<u8>, SlickHTTPServerError> {
    let mut body: Vec<u8> = Vec::new();
    let available = pending.len().saturating_sub(start);
    let take = available.min(need);
    if take > 0 {
        body.extend_from_slice(&pending[start..start + take]);
    }
    let leftover_at = start + take;
    if leftover_at < pending.len() {
        pending.copy_within(leftover_at.., 0);
        pending.truncate(pending.len() - leftover_at);
    } else {
        pending.clear();
    }
    while body.len() < need {
        if !slick_http_server_set_read_deadline(stream, deadline) {
            return Err(SlickHTTPServerError::Malformed);
        }
        let want = (need - body.len()).min(4096);
        let mut piece = [0u8; 4096];
        match std::io::Read::read(&mut &*stream, &mut piece[..want]) {
            Ok(0) => return Err(SlickHTTPServerError::Malformed),
            Ok(count) => body.extend_from_slice(&piece[..count]),
            Err(_) => return Err(SlickHTTPServerError::Malformed),
        }
    }
    Ok(body)
}

fn slick_http_server_collect_headers(
    stream: &std::net::TcpStream,
    pending: &mut Vec<u8>,
    first: bool,
    limits: &SlickHTTPServerLimits,
) -> Result<usize, Option<i64>> {
    let max_header = limits.max_header_bytes.max(1) as usize;
    let header_deadline;
    if pending.is_empty() {
        if first {
            header_deadline = slick_http_server_now_deadline(limits.read_header_timeout_ms);
            match slick_http_server_fill(stream, pending, header_deadline) {
                None | Some(0) => return Err(None),
                Some(_) => {}
            }
        } else {
            match slick_http_server_fill(stream, pending, slick_http_server_now_deadline(limits.idle_timeout_ms)) {
                None | Some(0) => return Err(None),
                Some(_) => {}
            }
            header_deadline = slick_http_server_now_deadline(limits.read_header_timeout_ms);
        }
    } else {
        header_deadline = slick_http_server_now_deadline(limits.read_header_timeout_ms);
    }
    let mut scan_from: usize = 0;
    loop {
        let start = scan_from.saturating_sub(3);
        if pending.len() >= 4 {
            if let Some(position) = slick_http_server_header_terminator(&pending[start..]) {
                return Ok(start + position + 4);
            }
        }
        if pending.len() >= max_header {
            return Err(Some(431));
        }
        scan_from = pending.len();
        match slick_http_server_fill(stream, pending, header_deadline) {
            None | Some(0) => {
                if pending.len() >= max_header {
                    return Err(Some(431));
                }
                if pending.is_empty() {
                    return Err(None);
                }
                return Err(Some(400));
            }
            Some(_) => {}
        }
    }
}


// slick_http_server_parse_version accepts only HTTP/<major>.<minor> with digits
// and reports the normalized protocol the response must use.
fn slick_http_server_parse_version(value: &str) -> Option<String> {
    let rest = value.strip_prefix("HTTP/")?;
    let (major, minor) = rest.split_once('.')?;
    if major.is_empty() || minor.is_empty() || major.len() > 2 || minor.len() > 2 {
        return None;
    }
    if !major.bytes().all(|byte| byte.is_ascii_digit()) || !minor.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    let major: u32 = major.parse().ok()?;
    let minor: u32 = minor.parse().ok()?;
    if major != 1 {
        return None;
    }
    if minor == 0 {
        return Some("HTTP/1.0".to_string());
    }
    Some("HTTP/1.1".to_string())
}

fn slick_http_server_handle_connection(stream: std::net::TcpStream, context: SlickContext, handler: SlickValue, limits: SlickHTTPServerLimits) {
    let mut pending: Vec<u8> = Vec::with_capacity(4096);
    let mut first = true;
    loop {
        if !first && (context.cancelled() || SLICK_HTTP_SERVER_STOP.load(Ordering::Acquire)) {
            return;
        }
        let header_end = match slick_http_server_collect_headers(&stream, &mut pending, first, &limits) {
            Ok(value) => value,
            Err(Some(status)) => {
                slick_http_server_set_write_timeout(&stream, limits.write_timeout_ms);
                slick_http_server_write_simple(&stream, status);
                return;
            }
            Err(None) => return,
        };
        let header_text = String::from_utf8_lossy(&pending[..header_end]).into_owned();
        let lines: Vec<&str> = header_text.split("\r\n").collect();
        let request_line = match lines.first() {
            Some(line) => *line,
            None => {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
        };
        let parts: Vec<&str> = request_line.split(' ').filter(|segment| !segment.is_empty()).collect();
        // The version is parsed numerically, like Go ParseHTTPVersion, so a
        // malformed "HTTP/1.foo" never reaches a handler, and an HTTP/1.x request
        // normalizes to the 1.0 or 1.1 contract.
        let version = match parts.get(2).and_then(|value| slick_http_server_parse_version(value)) {
            Some(version) => version,
            None => {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
        };
        if parts.len() != 3 {
            slick_http_server_write_simple(&stream, 400);
            return;
        }
        let method = match parts.first() {
            Some(value) => (*value).to_string(),
            None => {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
        };
        let target = match parts.get(1) {
            Some(value) => (*value).to_string(),
            None => {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
        };
        if method.eq_ignore_ascii_case("CONNECT") {
            slick_http_server_write_simple(&stream, 500);
            return;
        }
        if !target.starts_with('/') {
            slick_http_server_write_simple(&stream, 400);
            return;
        }
        let mut headers: Vec<(String, String)> = Vec::new();
        let mut content_length: Option<i64> = None;
        let mut transfer_encoding_count: i32 = 0;
        let mut transfer_encoding_supported = true;
        let mut host_count: i32 = 0;
        for line in lines.iter().skip(1) {
            if line.is_empty() {
                break;
            }
            let (name, value) = match line.split_once(':') {
                Some((left, right)) => (left, slick_http_server_trim(right)),
                None => {
                    slick_http_server_write_simple(&stream, 400);
                    return;
                }
            };
            if !slick_http_server_valid_token(name) || !slick_http_server_valid_field_value(value) {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
            let canonical = slick_http_server_canonical_header(name);
            if canonical == "Host" {
                host_count += 1;
                continue;
            }
            if canonical == "Content-Length" {
                let length: i64 = match value.parse() {
                    Ok(value) => value,
                    Err(_) => {
                        slick_http_server_write_simple(&stream, 400);
                        return;
                    }
                };
                if length < 0 {
                    slick_http_server_write_simple(&stream, 400);
                    return;
                }
                if let Some(previous) = content_length {
                    if previous != length {
                        slick_http_server_write_simple(&stream, 400);
                        return;
                    }
                }
                content_length = Some(length);
            }
            if canonical == "Transfer-Encoding" {
                transfer_encoding_count += 1;
                if !value.eq_ignore_ascii_case("chunked") {
                    transfer_encoding_supported = false;
                }
            }
            headers.push((canonical, value.to_string()));
        }
        if host_count > 1 || (version == "HTTP/1.1" && host_count == 0) {
            slick_http_server_write_simple(&stream, 400);
            return;
        }
        let transfer_encoding_seen = transfer_encoding_count > 0;
        if (transfer_encoding_seen && content_length.is_some())
            || transfer_encoding_count > 1
            || (transfer_encoding_seen && !transfer_encoding_supported)
        {
            let unsupported = transfer_encoding_count == 1 && content_length.is_none() && !transfer_encoding_supported;
            slick_http_server_write_simple(&stream, if unsupported { 501 } else { 400 });
            return;
        }
        if let Some(length) = content_length {
            if length > limits.max_body_bytes {
                slick_http_server_write_simple(&stream, 413);
                return;
            }
        }
        let (raw_path, raw_query) = match target.split_once('?') {
            Some((left, right)) => (left, right),
            None => (target.as_str(), ""),
        };
        let path = match slick_http_server_path_decode(raw_path) {
            Some(decoded) if decoded.is_empty() => "/".to_string(),
            Some(decoded) => decoded,
            None => {
                slick_http_server_write_simple(&stream, 400);
                return;
            }
        };
        let mut query_map: Vec<(SlickValue, SlickValue)> = Vec::new();
        if !slick_http_server_parse_query(raw_query, &mut query_map) {
            slick_http_server_write_simple(&stream, 400);
            return;
        }
        let max_header = limits.max_header_bytes.max(1) as usize;
        let max_body = limits.max_body_bytes.max(0) as usize;
        let body_deadline = slick_http_server_now_deadline(limits.read_timeout_ms);
        let body: Vec<u8> = if transfer_encoding_seen {
            let (body, consumed) = {
                let mut input = SlickHTTPServerInput {
                    stream: &stream,
                    pending: &pending,
                    leftover_pos: header_end,
                    deadline: body_deadline,
                };
                let body = match slick_http_server_read_chunked(&mut input, max_body, max_header) {
                    Ok(body) => body,
                    Err(SlickHTTPServerError::Overflow) => {
                        slick_http_server_write_simple(&stream, 413);
                        return;
                    }
                    Err(SlickHTTPServerError::Malformed) => {
                        slick_http_server_write_simple(&stream, 400);
                        return;
                    }
                };
                (body, input.leftover_pos.min(pending.len()))
            };
            pending.drain(..consumed);
            body
        } else if let Some(length) = content_length {
            if length <= 0 {
                if header_end < pending.len() {
                    pending.drain(..header_end);
                } else {
                    pending.clear();
                }
                Vec::new()
            } else {
                match slick_http_server_read_fixed_body(&stream, &mut pending, header_end, length as usize, body_deadline) {
                    Ok(body) => body,
                    Err(_) => {
                        slick_http_server_write_simple(&stream, 400);
                        return;
                    }
                }
            }
        } else {
            if header_end < pending.len() {
                pending.drain(..header_end);
            } else {
                pending.clear();
            }
            Vec::new()
        };
        let mut nominated: Vec<String> = Vec::new();
        for (name, value) in &headers {
            if name == "Connection" {
                for token in value.split(',') {
                    let trimmed = slick_http_server_trim(token);
                    if !trimmed.is_empty() {
                        nominated.push(slick_http_server_canonical_header(trimmed));
                    }
                }
            }
        }
        let mut header_map: Vec<(SlickValue, SlickValue)> = Vec::new();
        for (name, value) in &headers {
            if slick_http_server_is_hop_by_hop(name) {
                continue;
            }
            if nominated.iter().any(|entry| entry == name) {
                continue;
            }
            slick_http_server_map_append(&mut header_map, name, value);
        }
        header_map.sort_by(|left, right| match (&left.0, &right.0) {
            (SlickValue::String(left_name), SlickValue::String(right_name)) => left_name.cmp(right_name),
            _ => std::cmp::Ordering::Equal,
        });
        let request = slick_object("std.http.server.Request", vec![
            ("Method", slick_string(method.clone())),
            ("Path", slick_string(path)),
            ("Query", SlickValue::Map(query_map)),
            ("Headers", SlickValue::Map(header_map)),
            ("Body", SlickValue::Bytes(body)),
        ]);
        let outcome = slick_call_method(&context, handler.clone(), "Handle", vec![request]);
        let response: Option<SlickValue> = match &outcome {
            SlickOutcome::Value(value) => match value {
                SlickValue::Object { type_name, .. } if *type_name == "std.http.server.Response" => Some(value.clone()),
                _ => None,
            },
            _ => None,
        };
        slick_http_server_set_write_timeout(&stream, limits.write_timeout_ms);
        let keep_alive = !slick_http_server_connection_close(&version, &headers);
        let keep_open = match response {
            Some(value) => slick_http_server_write_response(&stream, &method, &version, keep_alive, &value),
            None => {
                slick_http_server_write_simple(&stream, 500);
                false
            }
        };
        if !keep_open {
            return;
        }
        first = false;
    }
}

fn slick_http_server_prune_workers(workers: &Arc<Mutex<Vec<SlickHTTPServerWorker>>>) {
    let finished: Vec<SlickHTTPServerWorker> = {
        let mut guard = workers.lock().unwrap_or_else(|error| error.into_inner());
        let mut keep: Vec<SlickHTTPServerWorker> = Vec::with_capacity(guard.len());
        let mut done: Vec<SlickHTTPServerWorker> = Vec::new();
        for worker in guard.drain(..) {
            if worker.handle.is_finished() {
                done.push(worker);
            } else {
                keep.push(worker);
            }
        }
        *guard = keep;
        done
    };
    for worker in finished {
        let _ = worker.handle.join();
    }
}

// slick_http_server_drain_workers waits for in-flight handler threads to finish
// within the shutdown timeout, joining each finished worker exactly once. When
// the deadline elapses, remaining sockets are closed and unfinished JoinHandles
// are dropped so Serve returns instead of blocking past ShutdownTimeoutMilliseconds.
fn slick_http_server_drain_workers(workers: &Arc<Mutex<Vec<SlickHTTPServerWorker>>>, timeout_ms: i64) {
    let limit: u128 = if timeout_ms < 0 { 0 } else { timeout_ms as u128 };
    let mut waited: u128 = 0;
    loop {
        slick_http_server_prune_workers(workers);
        let remaining = workers.lock().unwrap_or_else(|error| error.into_inner()).len();
        if remaining == 0 {
            return;
        }
        if waited >= limit {
            break;
        }
        std::thread::sleep(std::time::Duration::from_millis(5));
        waited = waited.saturating_add(5);
    }
    let leftover: Vec<SlickHTTPServerWorker> = workers.lock().unwrap_or_else(|error| error.into_inner()).drain(..).collect();
    for worker in leftover {
        if let Some(closer) = worker.closer {
            let _ = closer.shutdown(std::net::Shutdown::Both);
        }
    }
}

fn slick_nat_http_server_serve(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    use std::io::ErrorKind;
    use std::time::Duration;
    let config = slick_http_server_config(&slick_arg(&args, 0));
    let handler = slick_arg(&args, 1);
    if !slick_http_server_task_safe(&handler) {
        return slick_err(slick_http_server_failure("Config", &config.address, "Application must be task-safe"));
    }
    if let Some(failure) = slick_http_server_validate_config(&config) {
        return slick_err(failure);
    }
    let listener = match std::net::TcpListener::bind(&config.address) {
        Ok(listener) => listener,
        Err(_) => return slick_err(slick_http_server_failure("Bind", &config.address, "failed to bind listen address")),
    };
    slick_http_server_acquire_signals();
    let _ = listener.set_nonblocking(true);
    let limits = SlickHTTPServerLimits {
        max_header_bytes: config.max_header_bytes,
        max_body_bytes: config.max_body_bytes,
        read_header_timeout_ms: config.read_header_timeout_ms,
        read_timeout_ms: config.read_timeout_ms,
        write_timeout_ms: config.write_timeout_ms,
        idle_timeout_ms: config.idle_timeout_ms,
    };
    let handler_cancel = Arc::new(AtomicBool::new(false));
    let workers: Arc<Mutex<Vec<SlickHTTPServerWorker>>> = Arc::new(Mutex::new(Vec::new()));
    let mut serve_error = false;
    loop {
        if context.cancelled() || SLICK_HTTP_SERVER_STOP.load(Ordering::Acquire) {
            break;
        }
        match listener.accept() {
            Ok((stream, _)) => {
                let _ = stream.set_nonblocking(false);
                let closer = stream.try_clone().ok();
                let child_context = context.child(handler_cancel.clone());
                let handler_clone = handler.clone();
                let handle = std::thread::Builder::new()
                    .spawn(move || slick_http_server_handle_connection(stream, child_context, handler_clone, limits));
                match handle {
                    Ok(handle) => {
                        let mut guard = workers.lock().unwrap_or_else(|error| error.into_inner());
                        guard.push(SlickHTTPServerWorker { handle, closer });
                    }
                    Err(_) => {}
                }
                slick_http_server_prune_workers(&workers);
            }
            Err(ref error) if error.kind() == ErrorKind::WouldBlock => {
                slick_http_server_prune_workers(&workers);
                std::thread::sleep(Duration::from_millis(5));
            }
            Err(_) => {
                if !context.cancelled() && !SLICK_HTTP_SERVER_STOP.load(Ordering::Acquire) {
                    serve_error = true;
                }
                break;
            }
        }
    }
    handler_cancel.store(true, Ordering::Release);
    drop(listener);
    slick_http_server_drain_workers(&workers, config.shutdown_timeout_ms);
    slick_http_server_release_signals();
    if serve_error {
        return slick_err(slick_http_server_failure("Serve", &config.address, "HTTP server failed"));
    }
    slick_ok(SlickValue::Null)
}
`
