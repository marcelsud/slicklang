package compiler

var bunStdHTTPServer = bunStdFamily{
	family: runtimeFamilyHTTPServer,
	module: bunStdHTTPServerModule,
	functions: map[runtimeOperationID]string{
		nativeStdHTTPServerServe: "slickNatHTTPServerServe",
	},
}

// bunStdHTTPServerModule implements std.http.server.Serve. It honours every
// Config field, parses inbound requests into the declared Request (decoded
// Path, ordered Query, canonical sorted Headers without Host), dispatches
// through slickCallMethod, writes the declared Response with Go StatusText
// reason phrases, and performs documented cancellation and graceful shutdown.
// Duplicate header values and exact status phrases cannot be expressed with
// Bun.serve, so the listener is node:http. SIGINT/SIGTERM are installed on the
// main thread and broadcast to serving workers. The JavaScript contains no backtick.
const bunStdHTTPServerModule = `export async function slickNatHTTPServerServe(context, args) {
  const config = slickHTTPServerConfig(slickArg(args, 0));
  const handler = slickArg(args, 1);
  if (!slickHTTPServerTaskSafe(handler)) {
    return slickErr(slickHTTPServerFailure("Config", config.address, "Application must be task-safe"));
  }
  const invalid = slickHTTPServerValidateConfig(config);
  if (invalid) return slickErr(invalid);
  const spec = slickHTTPServerListenSpec(config.address);
  if (spec === null) {
    return slickErr(slickHTTPServerFailure("Bind", config.address, "failed to bind listen address"));
  }
  const http = await slickHTTPServerHTTP();
  const cancelBuffer = new SharedArrayBuffer(4);
  const cancelFlag = new Int32Array(cancelBuffer);
  const handlerContext = context.child(cancelBuffer);
  let inFlight = 0;
  let serveError = false;
  const sockets = new Set();
  const writeTimeoutMs = slickHTTPServerTimeoutNumber(config.writeTimeoutMs);
  const idleTimeoutMs = slickHTTPServerTimeoutNumber(config.idleTimeoutMs);
  const headerTimeoutMs = slickHTTPServerTimeoutNumber(config.readHeaderTimeoutMs);
  const requestTimeoutMs = slickHTTPServerTimeoutNumber(config.readTimeoutMs);
  const beginRequest = (socket) => {
    if (!socket) return;
    socket.slickHTTPServerRequestStart = Date.now();
    slickHTTPServerArmDeadline(socket, headerTimeoutMs);
  };
  const dispatch = (req, res) => {
    const socket = req.socket;
    if (socket) socket.slickHTTPServerWriteTimeoutMs = writeTimeoutMs;
    const start = socket && socket.slickHTTPServerRequestStart ? socket.slickHTTPServerRequestStart : Date.now();
    const remaining = requestTimeoutMs - (Date.now() - start);
    if (remaining <= 0) {
      if (socket) try { socket.destroy(); } catch (error) {}
      return;
    }
    slickHTTPServerArmDeadline(socket, remaining);
    inFlight += 1;
    slickHTTPServerHandle(handlerContext, handler, config, req, res).then(() => {
      inFlight -= 1;
      if (!socket || socket.destroyed) return;
      slickHTTPServerArmDeadline(socket, idleTimeoutMs);
      socket.slickHTTPServerRequestStart = 0;
      socket.once("data", () => beginRequest(socket));
    }, () => {
      inFlight -= 1;
      if (!res.headersSent) {
        try { res.writeHead(500, slickHTTPServerStatusReason(500)); } catch (error) {}
      }
      try { res.end(); } catch (error) {}
    });
  };
  const server = http.createServer({
    keepAlive: true,
    keepAliveTimeout: slickHTTPServerNativeTimerMs(idleTimeoutMs),
    headersTimeout: slickHTTPServerNativeTimerMs(headerTimeoutMs),
    requestTimeout: slickHTTPServerNativeTimerMs(requestTimeoutMs),
    connectionsCheckingInterval: 100,
    maxHeaderSize: slickHTTPServerTimeoutNumber(config.maxHeaderBytes),
  }, dispatch);
  server.on("connect", (req, socket) => {
    const res = new http.ServerResponse(req);
    res.shouldKeepAlive = false;
    res.assignSocket(socket);
    dispatch(req, res);
  });
  server.on("upgrade", (req, socket, head) => {
    const res = new http.ServerResponse(req);
    res.assignSocket(socket);
    if (head && head.length > 0 && typeof socket.unshift === "function") socket.unshift(head);
    dispatch(req, res);
  });
  server.on("connection", (socket) => {
    sockets.add(socket);
    socket.on("close", () => {
      sockets.delete(socket);
      slickHTTPServerClearDeadline(socket);
    });
    beginRequest(socket);
  });
  server.on("error", () => {
    if (!slickHTTPServerBroker.stop && !context.cancelled()) serveError = true;
  });
  try {
    await new Promise((resolve, reject) => {
      const onError = (error) => {
        server.off("error", onError);
        reject(error);
      };
      server.once("error", onError);
      const onListen = () => {
        server.off("error", onError);
        resolve();
      };
      if (spec.host === undefined) server.listen(spec.port, onListen);
      else server.listen(spec.port, spec.host, onListen);
    });
  } catch (error) {
    return slickErr(slickHTTPServerFailure("Bind", config.address, "failed to bind listen address"));
  }
  slickHTTPServerAcquireSignals();
  try {
    await new Promise((resolve) => {
      const timer = setInterval(() => {
        if (slickHTTPServerBroker.stop || context.cancelled() || serveError) {
          clearInterval(timer);
          resolve();
        }
      }, 15);
      server.on("close", () => {
        clearInterval(timer);
        resolve();
      });
    });
    Atomics.store(cancelFlag, 0, 1);
    const shutdownMs = slickHTTPServerTimeoutNumber(config.shutdownTimeoutMs);
    let closed = false;
    const closedPromise = new Promise((resolve) => server.close(() => {
      closed = true;
      resolve();
    }));
    await Promise.race([
      closedPromise,
      slickHTTPServerDelay(shutdownMs),
    ]);
    if (!closed) {
      if (typeof server.closeAllConnections === "function") server.closeAllConnections();
      for (const socket of sockets) {
        try { socket.destroy(); } catch (error) {}
      }
    }
    if (serveError) return slickErr(slickHTTPServerFailure("Serve", config.address, "HTTP server failed"));
    return slickOk(null);
  } finally {
    slickHTTPServerReleaseSignals();
  }
}

const slickHTTPServerBroker = { refs: 0, stop: false, handler: null };
const slickHTTPServerChannel = new BroadcastChannel("slick.http.server.signals");
if (typeof slickHTTPServerChannel.unref === "function") slickHTTPServerChannel.unref();
slickHTTPServerChannel.onmessage = (event) => {
  const kind = event.data;
  if (kind === "stop") {
    slickHTTPServerBroker.stop = true;
    return;
  }
  if (!isMainThread) return;
  if (kind === "acquire") slickHTTPServerInstallSignals();
  else if (kind === "release") slickHTTPServerUninstallSignals();
};

let slickHTTPServerHTTPMod = null;

async function slickHTTPServerHTTP() {
  if (slickHTTPServerHTTPMod === null) slickHTTPServerHTTPMod = await import("node:http");
  return slickHTTPServerHTTPMod;
}

function slickHTTPServerInstallSignals() {
  if (slickHTTPServerBroker.refs === 0) {
    slickHTTPServerBroker.stop = false;
    slickHTTPServerBroker.handler = () => {
      slickHTTPServerBroker.stop = true;
      try { slickHTTPServerChannel.postMessage("stop"); } catch (error) {}
    };
    process.on("SIGINT", slickHTTPServerBroker.handler);
    process.on("SIGTERM", slickHTTPServerBroker.handler);
  }
  slickHTTPServerBroker.refs += 1;
}

function slickHTTPServerUninstallSignals() {
  if (slickHTTPServerBroker.refs === 0) return;
  slickHTTPServerBroker.refs -= 1;
  if (slickHTTPServerBroker.refs !== 0) return;
  if (slickHTTPServerBroker.handler) {
    process.off("SIGINT", slickHTTPServerBroker.handler);
    process.off("SIGTERM", slickHTTPServerBroker.handler);
    slickHTTPServerBroker.handler = null;
  }
}

function slickHTTPServerAcquireSignals() {
  slickHTTPServerBroker.stop = false;
  if (isMainThread) slickHTTPServerInstallSignals();
  else {
    try { slickHTTPServerChannel.postMessage("acquire"); } catch (error) {}
  }
}

function slickHTTPServerReleaseSignals() {
  if (isMainThread) slickHTTPServerUninstallSignals();
  else {
    try { slickHTTPServerChannel.postMessage("release"); } catch (error) {}
  }
}

function slickHTTPServerFailure(operation, address, message) {
  const text = message.trim() === "" ? "operation failed" : message;
  return slickStdObject("std.http.server.Failure", [
    ["Operation", operation],
    ["Address", address],
    ["Message", text],
  ]);
}

function slickHTTPServerOptionalInt(value, name) {
  const field = slickField(value, name);
  if (field instanceof SlickOptional) {
    return field.present && typeof field.value === "bigint" ? field.value : undefined;
  }
  return typeof field === "bigint" ? field : undefined;
}

function slickHTTPServerConfig(value) {
  const addressField = slickField(value, "Address");
  return {
    address: typeof addressField === "string" ? addressField : "",
    maxHeaderBytes: slickHTTPServerOptionalInt(value, "MaxHeaderBytes") ?? 1048576n,
    maxBodyBytes: slickHTTPServerOptionalInt(value, "MaxBodyBytes") ?? 8388608n,
    readHeaderTimeoutMs: slickHTTPServerOptionalInt(value, "ReadHeaderTimeoutMilliseconds") ?? 10000n,
    readTimeoutMs: slickHTTPServerOptionalInt(value, "ReadTimeoutMilliseconds") ?? 30000n,
    writeTimeoutMs: slickHTTPServerOptionalInt(value, "WriteTimeoutMilliseconds") ?? 30000n,
    idleTimeoutMs: slickHTTPServerOptionalInt(value, "IdleTimeoutMilliseconds") ?? 120000n,
    shutdownTimeoutMs: slickHTTPServerOptionalInt(value, "ShutdownTimeoutMilliseconds") ?? 30000n,
  };
}

function slickHTTPServerValidateConfig(config) {
  if (config.address.trim() === "") {
    return slickHTTPServerFailure("Config", config.address, "Address must not be empty");
  }
  const checks = [
    ["MaxHeaderBytes", config.maxHeaderBytes],
    ["MaxBodyBytes", config.maxBodyBytes],
    ["ReadHeaderTimeoutMilliseconds", config.readHeaderTimeoutMs],
    ["ReadTimeoutMilliseconds", config.readTimeoutMs],
    ["WriteTimeoutMilliseconds", config.writeTimeoutMs],
    ["IdleTimeoutMilliseconds", config.idleTimeoutMs],
    ["ShutdownTimeoutMilliseconds", config.shutdownTimeoutMs],
  ];
  for (const [name, value] of checks) {
    if (value <= 0n) return slickHTTPServerFailure("Config", config.address, name + " must be positive");
  }
  return null;
}

function slickHTTPServerTimeoutNumber(milliseconds) {
  if (milliseconds <= 0n) return 1;
  if (milliseconds > 9223372036854n) return 9223372036854;
  return Number(milliseconds);
}

const slickHTTPServerMaxTimerMs = 2147483646;

function slickHTTPServerNativeTimerMs(ms) {
  return ms > slickHTTPServerMaxTimerMs ? 0 : ms;
}

function slickHTTPServerDelay(ms) {
  return new Promise((resolve) => {
    const deadline = Date.now() + ms;
    const tick = () => {
      const left = deadline - Date.now();
      if (left <= 0) {
        resolve();
        return;
      }
      setTimeout(tick, Math.min(left, slickHTTPServerMaxTimerMs));
    };
    tick();
  });
}

function slickHTTPServerClearDeadline(socket) {
  if (!socket || !socket.slickHTTPServerDeadlineTimer) return;
  clearTimeout(socket.slickHTTPServerDeadlineTimer);
  socket.slickHTTPServerDeadlineTimer = null;
}

function slickHTTPServerArmDeadline(socket, ms) {
  slickHTTPServerClearDeadline(socket);
  if (!socket || ms == null) return;
  const deadline = Date.now() + ms;
  const tick = () => {
    const left = deadline - Date.now();
    if (left <= 0) {
      socket.slickHTTPServerDeadlineTimer = null;
      try { socket.destroy(); } catch (error) {}
      return;
    }
    socket.slickHTTPServerDeadlineTimer = setTimeout(tick, Math.min(left, slickHTTPServerMaxTimerMs));
  };
  tick();
}

function slickHTTPServerArmWrite(res) {
  const socket = res.socket || (res.req && res.req.socket);
  if (!socket || socket.slickHTTPServerWriteTimeoutMs == null) return;
  slickHTTPServerArmDeadline(socket, socket.slickHTTPServerWriteTimeoutMs);
}

function slickHTTPServerListenSpec(address) {
  let host;
  let portText;
  if (address.charAt(0) === "[") {
    const end = address.indexOf("]");
    if (end < 0) return null;
    host = address.slice(1, end);
    if (address.charAt(end + 1) !== ":") return null;
    portText = address.slice(end + 2);
  } else {
    const index = address.lastIndexOf(":");
    if (index < 0) return null;
    if (address.indexOf(":") !== index) return null;
    host = address.slice(0, index);
    portText = address.slice(index + 1);
  }
  if (portText.length === 0) return null;
  for (let i = 0; i < portText.length; i += 1) {
    const code = portText.charCodeAt(i);
    if (code < 48 || code > 57) return null;
  }
  const port = Number(portText);
  if (!Number.isInteger(port) || port < 0 || port > 65535) return null;
  return { host: host === "" ? undefined : host, port };
}

function slickHTTPServerTaskSafe(value) {
  if (value === null || typeof value === "boolean" || typeof value === "bigint" || typeof value === "number" || typeof value === "string") return true;
  if (value instanceof Uint8Array) return true;
  if (value instanceof SlickRange) return true;
  if (value instanceof SlickBuffer) return false;
  if (Array.isArray(value)) return value.every(slickHTTPServerTaskSafe);
  if (value instanceof SlickTuple) return value.values.every(slickHTTPServerTaskSafe);
  if (value instanceof SlickMap) {
    return value.entries.every((entry) => slickHTTPServerTaskSafe(entry[0]) && slickHTTPServerTaskSafe(entry[1]));
  }
  if (value instanceof SlickOptional) return !value.present || slickHTTPServerTaskSafe(value.value);
  if (value instanceof SlickResult) return slickHTTPServerTaskSafe(value.value);
  if (value instanceof SlickObject) {
    // A compiler-owned database handle is task-safe by declaration: only its
    // opaque owner identity crosses into a serving worker. Every other native
    // resource stays unsafe.
    if (value.resource !== null) {
      if (!(value.resource instanceof SlickOwnedResource) || value.resource.kind !== "sqlite.database") return false;
    }
    for (const field of value.fields.values()) {
      if (!slickHTTPServerTaskSafe(field)) return false;
    }
    return true;
  }
  if (value instanceof SlickUnion) return value.fields.every(slickHTTPServerTaskSafe);
  if (value instanceof SlickCallable) return value.captures.every(slickHTTPServerTaskSafe);
  if (value instanceof SlickEnumerate || value instanceof SlickZip) return false;
  return true;
}

function slickHTTPServerCanonical(name) {
  let upper = true;
  let out = "";
  for (let index = 0; index < name.length; index += 1) {
    let code = name.charCodeAt(index);
    if (upper && code >= 97 && code <= 122) code -= 32;
    else if (!upper && code >= 65 && code <= 90) code += 32;
    out += String.fromCharCode(code);
    upper = code === 45;
  }
  return out;
}

function slickHTTPServerValidToken(value) {
  if (value.length === 0) return false;
  for (let index = 0; index < value.length; index += 1) {
    const byte = value.charCodeAt(index);
    const valid = (byte >= 97 && byte <= 122) || (byte >= 65 && byte <= 90) || (byte >= 48 && byte <= 57) ||
      byte === 33 || byte === 35 || byte === 36 || byte === 37 || byte === 38 || byte === 39 ||
      byte === 42 || byte === 43 || byte === 45 || byte === 46 || byte === 94 || byte === 95 ||
      byte === 0x60 || byte === 124 || byte === 126;
    if (!valid) return false;
  }
  return true;
}

function slickHTTPServerValidFieldValue(value) {
  for (let index = 0; index < value.length; index += 1) {
    const byte = value.charCodeAt(index);
    if (byte !== 9 && (byte < 32 || byte === 127)) return false;
  }
  return true;
}

function slickHTTPServerFromLatin1(value) {
  return Buffer.from(String(value), "latin1").toString("utf8");
}

function slickHTTPServerToLatin1(value) {
  return Buffer.from(String(value), "utf8").toString("latin1");
}

function slickHTTPServerOriginTarget(rawURL) {
  const raw = rawURL || "/";
  if (raw === "*") return raw;
  const scheme = raw.indexOf("://");
  if (scheme < 1) return raw;
  const first = raw.charCodeAt(0);
  if (!((first >= 65 && first <= 90) || (first >= 97 && first <= 122))) return raw;
  for (let i = 1; i < scheme; i += 1) {
    const code = raw.charCodeAt(i);
    const ok = (code >= 65 && code <= 90) || (code >= 97 && code <= 122) ||
      (code >= 48 && code <= 57) || code === 43 || code === 45 || code === 46;
    if (!ok) return raw;
  }
  let index = scheme + 3;
  while (index < raw.length) {
    const code = raw.charCodeAt(index);
    if (code === 47 || code === 63 || code === 35) break;
    index += 1;
  }
  if (index >= raw.length || raw.charCodeAt(index) === 35) return "/";
  return raw.slice(index);
}

function slickHTTPServerIsHopByHop(name) {
  return name === "Connection" || name === "Keep-Alive" || name === "Proxy-Connection" ||
    name === "Transfer-Encoding" || name === "Te" || name === "Trailer" || name === "Upgrade";
}

function slickHTTPServerHex(byte) {
  if (byte >= 48 && byte <= 57) return byte - 48;
  if (byte >= 97 && byte <= 102) return byte - 97 + 10;
  if (byte >= 65 && byte <= 70) return byte - 65 + 10;
  return -1;
}

function slickHTTPServerDecode(value, plus) {
  const bytes = [];
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (plus && code === 43) {
      bytes.push(32);
      continue;
    }
    if (code === 37) {
      if (index + 2 >= value.length) return null;
      const high = slickHTTPServerHex(value.charCodeAt(index + 1));
      const low = slickHTTPServerHex(value.charCodeAt(index + 2));
      if (high < 0 || low < 0) return null;
      bytes.push((high << 4) | low);
      index += 2;
      continue;
    }
    bytes.push(code);
  }
  try {
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(new Uint8Array(bytes));
  } catch (error) {
    return null;
  }
}

function slickHTTPServerParseQuery(raw) {
  const entries = [];
  if (raw === "") return entries;
  for (const pair of raw.split("&")) {
    if (pair === "") continue;
    const eq = pair.indexOf("=");
    const key = eq < 0 ? pair : pair.slice(0, eq);
    const value = eq < 0 ? "" : pair.slice(eq + 1);
    const decodedKey = slickHTTPServerDecode(key, true);
    const decodedValue = slickHTTPServerDecode(value, true);
    if (decodedKey === null || decodedValue === null) return null;
    const existing = entries.find((entry) => entry[0] === decodedKey);
    if (existing) existing[1].push(decodedValue);
    else entries.push([decodedKey, [decodedValue]]);
  }
  return entries;
}

function slickHTTPServerRequestHeaders(req) {
  const nominated = {};
  const raw = req.rawHeaders || [];
  for (let index = 0; index < raw.length; index += 2) {
    if (slickHTTPServerCanonical(slickHTTPServerFromLatin1(raw[index])) !== "Connection") continue;
    for (const token of slickHTTPServerFromLatin1(raw[index + 1]).split(",")) {
      const trimmed = token.trim();
      if (trimmed !== "") nominated[slickHTTPServerCanonical(trimmed)] = true;
    }
  }
  const merged = [];
  for (let index = 0; index < raw.length; index += 2) {
    const canonical = slickHTTPServerCanonical(slickHTTPServerFromLatin1(raw[index]));
    if (canonical === "Host") continue;
    if (slickHTTPServerIsHopByHop(canonical) || nominated[canonical]) continue;
    const value = slickHTTPServerFromLatin1(raw[index + 1]);
    const existing = merged.find((entry) => entry[0] === canonical);
    if (existing) existing[1].push(value);
    else merged.push([canonical, [value]]);
  }
  merged.sort((left, right) => left[0] < right[0] ? -1 : left[0] > right[0] ? 1 : 0);
  return merged;
}

function slickHTTPServerStatusReason(status) {
  const text = slickHTTPServerStatusText(status);
  return text === null ? "status code " + String(status) : text;
}

function slickHTTPServerStatusText(code) {
  switch (Number(code)) {
    case 100: return "Continue";
    case 101: return "Switching Protocols";
    case 102: return "Processing";
    case 103: return "Early Hints";
    case 200: return "OK";
    case 201: return "Created";
    case 202: return "Accepted";
    case 203: return "Non-Authoritative Information";
    case 204: return "No Content";
    case 205: return "Reset Content";
    case 206: return "Partial Content";
    case 207: return "Multi-Status";
    case 208: return "Already Reported";
    case 226: return "IM Used";
    case 300: return "Multiple Choices";
    case 301: return "Moved Permanently";
    case 302: return "Found";
    case 303: return "See Other";
    case 304: return "Not Modified";
    case 305: return "Use Proxy";
    case 307: return "Temporary Redirect";
    case 308: return "Permanent Redirect";
    case 400: return "Bad Request";
    case 401: return "Unauthorized";
    case 402: return "Payment Required";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 406: return "Not Acceptable";
    case 407: return "Proxy Authentication Required";
    case 408: return "Request Timeout";
    case 409: return "Conflict";
    case 410: return "Gone";
    case 411: return "Length Required";
    case 412: return "Precondition Failed";
    case 413: return "Request Entity Too Large";
    case 414: return "Request URI Too Long";
    case 415: return "Unsupported Media Type";
    case 416: return "Requested Range Not Satisfiable";
    case 417: return "Expectation Failed";
    case 418: return "I'm a teapot";
    case 421: return "Misdirected Request";
    case 422: return "Unprocessable Entity";
    case 423: return "Locked";
    case 424: return "Failed Dependency";
    case 425: return "Too Early";
    case 426: return "Upgrade Required";
    case 428: return "Precondition Required";
    case 429: return "Too Many Requests";
    case 431: return "Request Header Fields Too Large";
    case 451: return "Unavailable For Legal Reasons";
    case 500: return "Internal Server Error";
    case 501: return "Not Implemented";
    case 502: return "Bad Gateway";
    case 503: return "Service Unavailable";
    case 504: return "Gateway Timeout";
    case 505: return "HTTP Version Not Supported";
    case 506: return "Variant Also Negotiates";
    case 507: return "Insufficient Storage";
    case 508: return "Loop Detected";
    case 510: return "Not Extended";
    case 511: return "Network Authentication Required";
    default: return null;
  }
}

function slickHTTPServerWriteSimple(res, status, close) {
  try {
    slickHTTPServerArmWrite(res);
    if (close) {
      res.shouldKeepAlive = false;
      if (res.req) res.req.shouldKeepAlive = false;
    }
    res.statusCode = status;
    res.statusMessage = slickHTTPServerStatusReason(status);
    res.setHeader("Content-Length", "0");
    if (close) res.setHeader("Connection", "close");
    const req = res.req;
    const socket = res.socket || (req && req.socket);
    res.end(() => {
      if (!close) return;
      try { if (req && typeof req.destroy === "function") req.destroy(); } catch (error) {}
      try { if (socket) socket.destroy(); } catch (error) {}
    });
  } catch (error) {
    const socket = res.socket || (res.req && res.req.socket);
    try { if (socket) socket.destroy(); } catch (ignored) {}
  }
}

function slickHTTPServerResponseHeaders(value) {
  const field = slickField(value, "Headers");
  let mapping = field;
  if (field instanceof SlickOptional) mapping = field.present ? field.value : null;
  if (!(mapping instanceof SlickMap)) return [];
  const pairs = [];
  for (const [key, bucket] of mapping.entries) {
    if (typeof key !== "string") continue;
    const values = [];
    if (Array.isArray(bucket)) {
      for (const item of bucket) {
        if (typeof item === "string") values.push(item);
      }
    }
    pairs.push([key, values]);
  }
  return pairs;
}

function slickHTTPServerWriteResponse(res, method, response) {
  const statusField = slickField(response, "Status");
  if (typeof statusField !== "bigint") {
    slickHTTPServerWriteSimple(res, 500);
    return;
  }
  const status = Number(statusField);
  if (method === "CONNECT" && status >= 200 && status < 300) {
    slickHTTPServerWriteSimple(res, 500);
    return;
  }
  if (status < 200 || status > 599) {
    slickHTTPServerWriteSimple(res, 500);
    return;
  }
  const bodyField = slickField(response, "Body");
  if (!(bodyField instanceof Uint8Array)) {
    slickHTTPServerWriteSimple(res, 500);
    return;
  }
  const headerPairs = slickHTTPServerResponseHeaders(response);
  for (const [name, values] of headerPairs) {
    const canonical = slickHTTPServerCanonical(name);
    if (slickHTTPServerIsHopByHop(canonical) || !slickHTTPServerValidToken(name) || canonical === "") {
      slickHTTPServerWriteSimple(res, 500);
      return;
    }
    if (canonical === "Content-Length" || canonical === "Host" || canonical === "Transfer-Encoding") {
      slickHTTPServerWriteSimple(res, 500);
      return;
    }
    if (values.length === 0) {
      slickHTTPServerWriteSimple(res, 500);
      return;
    }
    for (const value of values) {
      if (!slickHTTPServerValidFieldValue(value)) {
        slickHTTPServerWriteSimple(res, 500);
        return;
      }
    }
  }
  const suppress = method === "HEAD" || status === 204 || status === 205 || status === 304;
  const outBody = suppress ? new Uint8Array() : bodyField;
  const length = method === "HEAD" ? bodyField.length : outBody.length;
  slickHTTPServerArmWrite(res);
  res.statusCode = status;
  res.statusMessage = slickHTTPServerStatusReason(status);
  for (const [name, values] of headerPairs) {
    const canonical = slickHTTPServerCanonical(name);
    for (const value of values) res.appendHeader(slickHTTPServerToLatin1(canonical), slickHTTPServerToLatin1(value));
  }
  res.setHeader("Content-Length", String(length));
  if (method !== "HEAD" && outBody.length > 0) res.end(Buffer.from(outBody));
  else res.end();
}

function slickHTTPServerReadBody(req, res, maxBytes) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    let settled = false;
    const declared = Number(req.headers["content-length"]);
    const succeed = (value) => { if (settled) return; settled = true; resolve(value); };
    const fail = (error) => {
      if (settled) return;
      settled = true;
      const status = error.slickHTTPServerStatus || 400;
      const socket = (res && (res.socket || res.req && res.req.socket)) || req.socket;
      try {
        if (socket && !socket.destroyed) {
          socket.write("HTTP/1.1 " + status + " " + slickHTTPServerStatusReason(status) + "\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
        }
      } catch (ignored) {}
      reject(error);
    };
    if (req.aborted || req.destroyed) {
      const error = new Error("aborted");
      error.slickHTTPServerStatus = 400;
      fail(error);
      return;
    }
    req.on("data", (chunk) => {
      if (settled) return;
      size += chunk.length;
      if (size > maxBytes) {
        req.pause();
        const error = new Error("too large");
        error.slickHTTPServerStatus = 413;
        fail(error);
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => {
      if (Number.isFinite(declared) && declared > size) {
        const error = new Error("truncated");
        error.slickHTTPServerStatus = 400;
        fail(error);
        return;
      }
      succeed(new Uint8Array(Buffer.concat(chunks)));
    });
    req.on("aborted", () => {
      const error = new Error("aborted");
      error.slickHTTPServerStatus = 400;
      fail(error);
    });
    req.on("error", (error) => {
      error.slickHTTPServerStatus = 400;
      fail(error);
    });
  });
}

async function slickHTTPServerHandle(context, handler, config, req, res) {
  const bodyPromise = req.method === "CONNECT" ? null : slickHTTPServerReadBody(req, res, Number(config.maxBodyBytes));
  try {
    let path;
    let query;
    let body;
    if (req.method === "CONNECT") {
      path = "/";
      query = [];
      body = new Uint8Array();
    } else {
      const rawURL = slickHTTPServerOriginTarget(req.url || "/");
      const q = rawURL.indexOf("?");
      const rawPath = q < 0 ? rawURL : rawURL.slice(0, q);
      const rawQuery = q < 0 ? "" : rawURL.slice(q + 1);
      path = slickHTTPServerDecode(rawPath, false);
      if (path === null) {
        slickHTTPServerWriteSimple(res, 400);
        return;
      }
      query = slickHTTPServerParseQuery(rawQuery);
      if (query === null) {
        slickHTTPServerWriteSimple(res, 400);
        return;
      }
      try {
        body = await bodyPromise;
      } catch (error) {
        if (!res.headersSent) {
          const status = error && error.slickHTTPServerStatus ? error.slickHTTPServerStatus : 400;
          slickHTTPServerWriteSimple(res, status, true);
        }
        return;
      }
    }
    const request = slickStdObject("std.http.server.Request", [
      ["Method", req.method || "GET"],
      ["Path", path === "" ? "/" : path],
      ["Query", slickMap(query)],
      ["Headers", slickMap(slickHTTPServerRequestHeaders(req))],
      ["Body", body],
    ]);
    let response;
    try {
      response = await slickCallMethod(context, handler, "Handle", [request]);
    } catch (error) {
      slickHTTPServerWriteSimple(res, 500);
      return;
    }
    if (!(response instanceof SlickObject) || response.typeName !== "std.http.server.Response") {
      slickHTTPServerWriteSimple(res, 500);
      return;
    }
    slickHTTPServerWriteResponse(res, req.method || "GET", response);
  } catch (error) {
    if (!res.headersSent) slickHTTPServerWriteSimple(res, 500);
    else {
      try { res.end(); } catch (ignored) {}
    }
  }
}
`
