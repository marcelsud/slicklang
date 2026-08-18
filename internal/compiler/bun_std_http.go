package compiler

var bunStdHTTP = bunStdFamily{
	family: runtimeFamilyHTTP,
	module: bunStdHTTPModule,
	functions: map[runtimeOperationID]string{
		nativeStdHTTPFetch:        "slickNatHTTPFetch",
		nativeStdHTTPHeaderValues: "slickNatHTTPHeaderValues",
		nativeStdHTTPStatusText:   "slickNatHTTPStatusText",
	},
}

// bunStdHTTPModule implements std.http. Fetch builds the request from the
// declared std.http.Request fields, enforces the documented limits and
// timeouts, and returns std.http.Response with deterministic canonical
// headers. Every transport, timeout, redirect, status, limit, and
// cancellation condition becomes std.http.Failure with the same Kind values
// as the interpreter. Proxy selection honours HTTP_PROXY/HTTPS_PROXY/NO_PROXY
// (and the lowercase forms) through slickEnvironmentRead. The JavaScript
// contains no backtick so it fits a Go raw string; the HTTP token backtick
// (0x60) is matched by value. Bun's fetch combines duplicate header values,
// so the client speaks HTTP/1.1 over node:net / node:tls.
const bunStdHTTPModule = `export async function slickNatHTTPFetch(context, args) {
  const request = slickArg(args, 0);
  const method = slickHTTPFieldString(request, "Method");
  const url = slickHTTPFieldString(request, "URL");
  let timeoutMs = 30000n;
  let maxBytes = 8n * 1024n * 1024n;
  let followRedirects = false;
  let body = new Uint8Array();
  let bodyPresent = false;
  const headers = [];
  const headerField = slickHTTPOptionalField(request, "Headers");
  if (headerField instanceof SlickMap) {
    for (const [key, value] of headerField.entries) {
      if (typeof key !== "string") continue;
      const values = [];
      if (Array.isArray(value)) {
        for (const item of value) {
          if (typeof item === "string") values.push(item);
        }
      }
      headers.push([key, values]);
    }
  }
  const bodyField = slickHTTPOptionalField(request, "Body");
  if (bodyField instanceof Uint8Array) {
    body = bodyField;
    bodyPresent = true;
  }
  const timeoutField = slickHTTPOptionalField(request, "TimeoutMilliseconds");
  if (typeof timeoutField === "bigint") timeoutMs = timeoutField;
  const limitField = slickHTTPOptionalField(request, "MaxResponseBytes");
  if (typeof limitField === "bigint") maxBytes = limitField;
  const followField = slickHTTPOptionalField(request, "FollowRedirects");
  if (typeof followField === "boolean") followRedirects = followField;

  const prepared = slickHTTPValidate(method, url, headers, timeoutMs, maxBytes);
  if (prepared.failure) return slickErr(prepared.failure);
  if (context.cancelled()) return slickHTTPCancelled(slickHTTPSanitized(url), null);

  const timeoutNumber = slickHTTPTimeoutNumber(timeoutMs);
  const deadline = Date.now() + timeoutNumber;
  let currentURL = prepared.href;
  let currentMethod = method;
  let currentBody = body;
  let currentPresent = bodyPresent;
  let currentHeaders = slickHTTPCopyHeaders(prepared.headers);
  let redirects = 0;
  try {
    while (true) {
      if (context.cancelled()) return slickHTTPCancelled(slickHTTPSanitized(currentURL), null);
      if (Date.now() >= deadline) {
        return slickErr(slickHTTPFailure("Timeout", slickHTTPSanitized(currentURL), null, "HTTP request timed out"));
      }
      const exchanged = await slickHTTPExchange(context, deadline, currentURL, currentMethod, currentHeaders, currentBody, currentPresent, maxBytes, followRedirects);
      if (exchanged.failure) return slickErr(exchanged.failure);
      const response = exchanged.response;
      if (!followRedirects || !slickHTTPIsRedirect(response.status)) {
        return slickOk(slickHTTPResponse(response.status, response.url, response.headers, response.body));
      }
      redirects += 1;
      if (redirects > 9) {
        return slickErr(slickHTTPFailure("Redirect", slickHTTPSanitized(response.url), response.status, "HTTP redirect failed"));
      }
      const location = slickHTTPHeaderValue(response.headers, "Location");
      if (location === null) {
        return slickOk(slickHTTPResponse(response.status, response.url, response.headers, response.body));
      }
      if (slickHTTPHasBoundaryControl(location)) {
        return slickErr(slickHTTPFailure("Redirect", slickHTTPSanitized(response.url), response.status, "HTTP redirect failed"));
      }
      const next = slickHTTPParseURL(location, currentURL);
      if (!next) {
        return slickErr(slickHTTPFailure("Redirect", slickHTTPSanitized(response.url), response.status, "HTTP redirect failed"));
      }
      if (next.username !== "" || next.password !== "") {
        return slickErr(slickHTTPFailure("Redirect", slickHTTPSanitized(response.url), response.status, "HTTP redirect failed"));
      }
      const scheme = next.protocol.slice(0, -1).toLowerCase();
      if ((scheme !== "http" && scheme !== "https") || next.hostname === "") {
        return slickErr(slickHTTPFailure("Redirect", slickHTTPSanitized(response.url), response.status, "HTTP redirect failed"));
      }
      const dropBody = response.status === 301 || response.status === 302 || response.status === 303;
      if (dropBody && currentMethod !== "GET" && currentMethod !== "HEAD") currentMethod = "GET";
      if (dropBody) {
        currentBody = new Uint8Array();
        currentPresent = false;
      }
      const stripSensitive = !slickHTTPRedirectAllows(currentURL, next.href);
      currentHeaders = slickHTTPFilterHeaders(currentHeaders, stripSensitive, dropBody);
      currentURL = next.href;
    }
  } catch (error) {
    const sanitized = slickHTTPSanitized(currentURL);
    if (context.cancelled() || (error && error.slickHTTPCancelled)) {
      return slickHTTPCancelled(sanitized, error && error.slickHTTPStatus != null ? error.slickHTTPStatus : null);
    }
    if (error && error.slickHTTPTimeout) {
      return slickErr(slickHTTPFailure("Timeout", sanitized, error.slickHTTPStatus != null ? error.slickHTTPStatus : null, "HTTP request timed out"));
    }
    if (error && error.slickHTTPFailure) return slickErr(error.slickHTTPFailure);
    return slickErr(slickHTTPFailure("Transport", sanitized, null, "HTTP transport failed"));
  }
}

export async function slickNatHTTPHeaderValues(context, args) {
  const name = slickArgString(args, 1);
  const entries = slickArgEntries(args, 0);
  for (const [key, value] of entries) {
    if (typeof key === "string" && key.toLowerCase() === name.toLowerCase()) {
      return Array.isArray(value) ? value.slice() : [];
    }
  }
  return [];
}

export async function slickNatHTTPStatusText(context, args) {
  const status = slickArgInt(args, 0);
  const text = slickHTTPStatusText(status);
  return text === null ? slickAbsent : slickOptional(text);
}

function slickHTTPFieldString(request, name) {
  const value = slickField(request, name);
  if (typeof value !== "string") throw SlickFailure.host("std.http.Request." + name + " is not string");
  return value;
}

function slickHTTPOptionalField(request, name) {
  const value = slickField(request, name);
  if (value instanceof SlickOptional) return value.present ? value.value : undefined;
  return value === null ? undefined : value;
}

function slickHTTPTimeoutNumber(milliseconds) {
  if (milliseconds <= 0n) return 1;
  if (milliseconds > 9223372036854n) return 9223372036854;
  return Number(milliseconds);
}

function slickHTTPHasBoundaryControl(value) {
  if (value.length === 0) return false;
  return value.charCodeAt(0) <= 32 || value.charCodeAt(value.length - 1) <= 32;
}

function slickHTTPContainsCTL(value) {
  for (let index = 0; index < value.length; index += 1) {
    const byte = value.charCodeAt(index);
    if (byte < 32 || byte === 127) return true;
  }
  return false;
}

function slickHTTPIsHex(byte) {
  return (byte >= 48 && byte <= 57) || (byte >= 65 && byte <= 70) || (byte >= 97 && byte <= 102);
}

function slickHTTPValidURLEscapes(value) {
  for (let index = 0; index < value.length; index += 1) {
    if (value.charCodeAt(index) !== 37) continue;
    if (!slickHTTPIsHex(value.charCodeAt(index + 1)) || !slickHTTPIsHex(value.charCodeAt(index + 2))) return false;
    index += 2;
  }
  return true;
}

function slickHTTPTakeIPv6Zone(raw) {
  const schemeSep = raw.indexOf("://");
  const from = schemeSep >= 0 ? schemeSep + 3 : 0;
  const rest = raw.slice(from);
  const stop = rest.search(/[/?#]/);
  const auth = stop < 0 ? rest : rest.slice(0, stop);
  const at = auth.lastIndexOf("@");
  const hostport = at >= 0 ? auth.slice(at + 1) : auth;
  if (hostport.charAt(0) !== "[") return null;
  const close = hostport.indexOf("]");
  if (close < 0) return null;
  const inner = hostport.slice(1, close);
  const zoneAt = inner.indexOf("%25");
  if (zoneAt < 0) return null;
  const addr = inner.slice(0, zoneAt);
  let zone;
  try {
    zone = decodeURIComponent(inner.slice(zoneAt + 3));
  } catch (error) {
    return null;
  }
  const hostStart = from + (at >= 0 ? at + 1 : 0);
  return {
    stripped: raw.slice(0, hostStart + 1) + addr + raw.slice(hostStart + 1 + inner.length),
    addr: addr,
    zone: zone,
  };
}

function slickHTTPURLParts(parsed) {
  let href = parsed.href;
  const hash = parsed.hash;
  if (hash && href.endsWith(hash)) href = href.slice(0, href.length - hash.length);
  return {
    protocol: parsed.protocol,
    hostname: parsed.hostname,
    host: parsed.host,
    port: parsed.port,
    pathname: parsed.pathname,
    search: parsed.search,
    href: href,
    username: parsed.username,
    password: parsed.password,
    hash: "",
  };
}

function slickHTTPSplitRawURL(raw) {
  const hashAt = raw.indexOf("#");
  const fragment = hashAt >= 0 ? raw.slice(hashAt) : "";
  const noHash = hashAt >= 0 ? raw.slice(0, hashAt) : raw;
  const schemeSep = noHash.indexOf("://");
  if (schemeSep < 0) return null;
  const scheme = noHash.slice(0, schemeSep);
  const after = noHash.slice(schemeSep + 3);
  const authEndMatch = after.search(/[/?]/);
  const authEnd = authEndMatch < 0 ? after.length : authEndMatch;
  let authority = after.slice(0, authEnd);
  const userinfoAt = authority.lastIndexOf("@");
  if (userinfoAt >= 0) authority = authority.slice(userinfoAt + 1);
  const pathQuery = authEndMatch < 0 ? "" : after.slice(authEnd);
  return {
    scheme: scheme,
    host: authority,
    pathQuery: pathQuery,
    fragment: fragment,
    href: scheme + "://" + authority + pathQuery + fragment,
  };
}

function slickHTTPRawHostName(host) {
  if (host.charAt(0) === "[") {
    const close = host.indexOf("]");
    if (close < 0) return host;
    return host.slice(0, close + 1);
  }
  const colon = host.lastIndexOf(":");
  return colon < 0 ? host : host.slice(0, colon);
}

function slickHTTPRawHostPort(host) {
  if (host.charAt(0) === "[") {
    const close = host.indexOf("]");
    if (close < 0 || host.charAt(close + 1) !== ":") return "";
    return host.slice(close + 2);
  }
  const colon = host.lastIndexOf(":");
  return colon < 0 ? "" : host.slice(colon + 1);
}

function slickHTTPApplyRawParts(parts, split) {
  parts.host = split.host;
  parts.hostname = slickHTTPRawHostName(split.host);
  parts.port = slickHTTPRawHostPort(split.host);
  const q = split.pathQuery.indexOf("?");
  parts.pathname = q < 0 ? split.pathQuery : split.pathQuery.slice(0, q);
  parts.search = q < 0 ? "" : split.pathQuery.slice(q);
  parts.hash = "";
  parts.href = split.scheme + "://" + split.host + split.pathQuery;
  return parts;
}

function slickHTTPZoneHost(parts, zone) {
  if (!zone) return false;
  let hostname = parts.hostname;
  if (hostname.charAt(0) === "[" && hostname.charAt(hostname.length - 1) === "]") {
    hostname = hostname.slice(1, -1);
  }
  const zoneAt = hostname.indexOf("%");
  if (zoneAt >= 0) hostname = hostname.slice(0, zoneAt);
  return hostname === zone.addr;
}

function slickHTTPApplyZone(parts, zone) {
  if (!zone) return parts;
  const encoded = encodeURIComponent(zone.zone);
  const wrapped = "[" + zone.addr + "%25" + encoded + "]";
  let host = parts.host;
  if (host.charAt(0) === "[") {
    host = wrapped + host.slice(host.indexOf("]") + 1);
  }
  parts.hostname = parts.hostname.charAt(0) === "[" ? wrapped : zone.addr + "%25" + encoded;
  parts.host = host;
  parts.href = parts.protocol + "//" + host + parts.pathname + parts.search + (parts.hash || "");
  return parts;
}

function slickHTTPParseURL(raw, base) {
  if (slickHTTPContainsCTL(raw) || !slickHTTPValidURLEscapes(raw)) return null;
  if (base !== undefined && (slickHTTPContainsCTL(base) || !slickHTTPValidURLEscapes(base))) return null;
  const rawZone = slickHTTPTakeIPv6Zone(raw);
  const baseZone = base !== undefined ? slickHTTPTakeIPv6Zone(base) : null;
  let parsed;
  try {
    parsed = base !== undefined
      ? new URL(rawZone ? rawZone.stripped : raw, baseZone ? baseZone.stripped : base)
      : new URL(rawZone ? rawZone.stripped : raw);
  } catch (error) {
    return null;
  }
  const parts = slickHTTPURLParts(parsed);
  const zoned = slickHTTPApplyZone(parts, rawZone ? rawZone : (slickHTTPZoneHost(parts, baseZone) ? baseZone : null));
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(raw)) {
    const split = slickHTTPSplitRawURL(raw);
    if (split && split.host.indexOf("\\") >= 0) return null;
    if (split) return slickHTTPApplyRawParts(zoned, split);
    return zoned;
  }
  if (base !== undefined) {
    const baseSplit = slickHTTPSplitRawURL(base);
    if (baseSplit) {
      zoned.host = baseSplit.host;
      zoned.hostname = slickHTTPRawHostName(baseSplit.host);
      zoned.port = slickHTTPRawHostPort(baseSplit.host);
      if (raw.charAt(0) === "/" && raw.charAt(1) !== "/") {
        const hashAt = raw.indexOf("#");
        const noHash = hashAt >= 0 ? raw.slice(0, hashAt) : raw;
        const frag = hashAt >= 0 ? raw.slice(hashAt) : "";
        const q = noHash.indexOf("?");
        zoned.pathname = q < 0 ? noHash : noHash.slice(0, q);
        zoned.search = q < 0 ? "" : noHash.slice(q);
        zoned.hash = frag;
        zoned.href = baseSplit.scheme + "://" + baseSplit.host + noHash + frag;
      } else {
        zoned.href = baseSplit.scheme + "://" + baseSplit.host + zoned.pathname + zoned.search + (zoned.hash || "");
      }
    }
  }
  return zoned;
}

function slickHTTPTrimOWS(value) {
  return value.replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
}

function slickHTTPValidate(method, url, headers, timeoutMs, maxBytes) {
  if (!slickHTTPValidToken(method)) {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "method must be a non-empty HTTP token") };
  }
  if (slickHTTPHasBoundaryControl(url)) {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "URL must be an absolute http or https URL") };
  }
  const parsed = slickHTTPParseURL(url);
  if (!parsed) {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "URL must be an absolute http or https URL") };
  }
  const scheme = parsed.protocol.slice(0, -1).toLowerCase();
  if ((scheme !== "http" && scheme !== "https") || parsed.hostname === "" || parsed.host === "") {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "URL must be an absolute http or https URL") };
  }
  if (parsed.username !== "" || parsed.password !== "") {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "URL userinfo is not allowed") };
  }
  if (timeoutMs <= 0n) {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "TimeoutMilliseconds must be positive") };
  }
  if (maxBytes <= 0n) {
    return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "MaxResponseBytes must be positive") };
  }
  const restricted = { Host: true, "Content-Length": true, "Transfer-Encoding": true, Connection: true };
  const prepared = [];
  let hasUserAgent = false;
  for (const [name, values] of headers) {
    const canonical = slickHTTPCanonical(name);
    if (!slickHTTPValidToken(name) || canonical === "") {
      return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, "invalid header name") };
    }
    if (restricted[canonical]) {
      return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, canonical + " header cannot be controlled") };
    }
    if (values.length === 0) {
      return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, canonical + " header values must not be empty") };
    }
    for (const value of values) {
      if (!slickHTTPValidFieldValue(value)) {
        return { failure: slickHTTPFailure("InvalidRequest", slickHTTPSanitized(url), null, canonical + " header value contains a forbidden control byte") };
      }
    }
    if (canonical === "User-Agent") hasUserAgent = true;
    const existing = prepared.find((entry) => entry[0] === canonical);
    if (existing) existing[1].push(...values);
    else prepared.push([canonical, values.slice()]);
  }
  if (!hasUserAgent) prepared.push(["User-Agent", ["Slick"]]);
  return { headers: prepared, href: parsed.href };
}

function slickHTTPValidToken(value) {
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

function slickHTTPValidFieldValue(value) {
  for (let index = 0; index < value.length; index += 1) {
    const byte = value.charCodeAt(index);
    if (byte !== 9 && (byte < 32 || byte === 127)) return false;
  }
  return true;
}

function slickHTTPCanonical(name) {
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

function slickHTTPSanitized(raw) {
  // Go url.Parse rejects boundary control bytes outright, so the failure carries
  // no URL at all rather than the raw control-containing text.
  if (slickHTTPHasBoundaryControl(raw)) {
    return "";
  }
  const parsed = slickHTTPParseURL(raw);
  if (!parsed) {
    return "";
  }
  const scheme = parsed.protocol.slice(0, -1);
  const host = parsed.host;
  const stripped = raw.split("#")[0].split("?")[0];
  const schemeSep = stripped.indexOf("://");
  const afterScheme = schemeSep >= 0 ? stripped.slice(schemeSep + 3) : stripped;
  const at = afterScheme.lastIndexOf("@");
  const hostAndPath = at >= 0 ? afterScheme.slice(at + 1) : afterScheme;
  const slash = hostAndPath.indexOf("/");
  const path = slash < 0 ? "" : parsed.pathname;
  return scheme + "://" + host + path;
}

function slickHTTPIsRedirect(status) {
  return status === 301 || status === 302 || status === 303 || status === 307 || status === 308;
}

function slickHTTPCopyHeaders(headers) {
  return headers.map((entry) => [entry[0], entry[1].slice()]);
}

function slickHTTPSensitiveHeader(name) {
  return name === "Authorization" || name === "Www-Authenticate" || name === "Cookie" ||
    name === "Cookie2" || name === "Proxy-Authorization" || name === "Proxy-Authenticate";
}

function slickHTTPBodyHeader(name) {
  return name === "Content-Encoding" || name === "Content-Language" ||
    name === "Content-Location" || name === "Content-Type";
}

function slickHTTPRedirectAllows(initial, dest) {
  const from = slickHTTPParseURL(initial);
  const to = slickHTTPParseURL(dest);
  if (!from || !to) return false;
  if (from.host === to.host) return true;
  const parent = from.hostname.toLowerCase();
  const sub = to.hostname.toLowerCase();
  if (sub === parent) return true;
  if (sub.indexOf(":") >= 0 || sub.indexOf("%") >= 0) return false;
  if (parent.length === 0 || !sub.endsWith(parent)) return false;
  return sub.charAt(sub.length - parent.length - 1) === ".";
}

function slickHTTPFilterHeaders(headers, stripSensitive, stripBody) {
  const out = [];
  for (const [name, values] of headers) {
    if (stripSensitive && slickHTTPSensitiveHeader(name)) continue;
    if (stripBody && slickHTTPBodyHeader(name)) continue;
    out.push([name, values.slice()]);
  }
  return out;
}

function slickHTTPNoResponseBody(method, status) {
  return method === "HEAD" || status === 204 || status === 304 || (status >= 100 && status < 200);
}

function slickHTTPFailure(kind, url, status, message) {
  return slickStdObject("std.http.Failure", [
    ["Kind", kind],
    ["URL", url],
    ["Status", status == null ? slickAbsent : slickOptional(typeof status === "bigint" ? status : BigInt(status))],
    ["Message", message],
  ]);
}

function slickHTTPCancelled(url, status) {
  return slickErr(slickHTTPFailure("Cancelled", url, status, "HTTP request cancelled"));
}

function slickHTTPResponse(status, url, headers, body) {
  const entries = headers.map((entry) => [entry[0], entry[1].slice()]);
  return slickStdObject("std.http.Response", [
    ["Status", BigInt(status)],
    ["URL", url],
    ["Headers", slickMap(entries)],
    ["Body", body],
  ]);
}

function slickHTTPHeaderValue(headers, name) {
  const canonical = slickHTTPCanonical(name);
  for (const [key, values] of headers) {
    if (key === canonical && values.length > 0) return values[0];
  }
  return null;
}

function slickHTTPHeaderValues(headers, name) {
  const canonical = slickHTTPCanonical(name);
  for (const [key, values] of headers) {
    if (key === canonical) return values;
  }
  return null;
}

function slickHTTPResponseFraming(headers) {
  const encodings = slickHTTPHeaderValues(headers, "Transfer-Encoding");
  const lengths = slickHTTPHeaderValues(headers, "Content-Length");
  let chunked = false;
  if (encodings !== null) {
    if (encodings.length !== 1 || encodings[0].trim().toLowerCase() !== "chunked") {
      return { failure: true };
    }
    chunked = true;
  }
  let length = null;
  let lengthText = null;
  if (lengths !== null) {
    for (const raw of lengths) {
      const text = raw.trim();
      if (!/^[0-9]+$/.test(text)) return { failure: true };
      if (lengthText !== null && lengthText !== text) return { failure: true };
      let value;
      try { value = BigInt(text); } catch (error) { return { failure: true }; }
      if (value < 0n || value > 9223372036854775807n) return { failure: true };
      lengthText = text;
      length = value;
    }
  }
  if (chunked) {
    const trailers = slickHTTPHeaderValues(headers, "Trailer");
    if (trailers !== null) {
      for (const raw of trailers) {
        for (const token of raw.split(",")) {
          const name = token.replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
          if (name !== "" && slickHTTPForbiddenTrailer(name)) return { failure: true };
        }
      }
    }
    return { chunked: true };
  }
  if (length !== null) return { length: length, lengthText: lengthText };
  return { close: true };
}

function slickHTTPForbiddenTrailer(name) {
  const canonical = slickHTTPCanonical(name);
  return canonical === "Content-Length" || canonical === "Transfer-Encoding" || canonical === "Trailer";
}

function slickHTTPStripTransferHeaders(headers, chunked) {
  const out = [];
  for (const [name, values] of headers) {
    if (name === "Transfer-Encoding") continue;
    if (chunked && (name === "Trailer" || name === "Content-Length")) continue;
    out.push([name, values.slice()]);
  }
  return out;
}

function slickHTTPLegacyNumericHost(host) {
  const bare = slickHTTPRawHostName(host);
  if (bare.charAt(0) === "[") return false;
  if (/^0[xX][0-9a-fA-F]+$/.test(bare)) return true;
  if (/^\d+$/.test(bare)) return true;
  const parts = bare.split(".");
  if (!parts.every((part) => /^(0[xX][0-9a-fA-F]+|\d+)$/.test(part))) return false;
  if (parts.length < 4) return true;
  return parts.some((part) => /^0[xX]/.test(part) || /^0\d+/.test(part));
}

function slickHTTPDialHost(host) {
  let bare = host;
  if (bare.length >= 2 && bare.charAt(0) === "[" && bare.charAt(bare.length - 1) === "]") {
    bare = bare.slice(1, -1);
  }
  const zoneAt = bare.indexOf("%25");
  if (zoneAt >= 0) {
    try {
      return bare.slice(0, zoneAt) + "%" + decodeURIComponent(bare.slice(zoneAt + 3));
    } catch (error) {
      return bare.slice(0, zoneAt) + "%" + bare.slice(zoneAt + 3);
    }
  }
  return bare;
}

function slickHTTPConnectAuthority(host, port) {
  const bare = slickHTTPDialHost(host);
  if (bare.indexOf(":") >= 0) return "[" + bare + "]:" + port;
  return bare + ":" + port;
}

function slickHTTPDecodeUserinfo(value) {
  try {
    return decodeURIComponent(value);
  } catch (error) {
    return value;
  }
}


function slickHTTPMergeHeaders(pairs) {
  const merged = [];
  for (const [name, value] of pairs) {
    const canonical = slickHTTPCanonical(name);
    const existing = merged.find((entry) => entry[0] === canonical);
    if (existing) existing[1].push(value);
    else merged.push([canonical, [value]]);
  }
  merged.sort((left, right) => left[0] < right[0] ? -1 : left[0] > right[0] ? 1 : 0);
  return merged;
}

let slickHTTPNet = null;
let slickHTTPTls = null;

async function slickHTTPModules() {
  if (slickHTTPNet === null) slickHTTPNet = await import("node:net");
  if (slickHTTPTls === null) slickHTTPTls = await import("node:tls");
  return { net: slickHTTPNet, tls: slickHTTPTls };
}

function slickHTTPThrowTimeout(status) {
  const error = new Error("timeout");
  error.slickHTTPTimeout = true;
  if (status != null) error.slickHTTPStatus = status;
  throw error;
}

function slickHTTPThrowCancelled(status) {
  const error = new Error("cancelled");
  error.slickHTTPCancelled = true;
  if (status != null) error.slickHTTPStatus = status;
  throw error;
}

function slickHTTPRemaining(deadline) {
  return Math.max(1, deadline - Date.now());
}

const slickHTTPMaxTimerMs = 2147483647;

function slickHTTPTimerDelay(deadline) {
  return Math.min(slickHTTPRemaining(deadline), slickHTTPMaxTimerMs);
}

function slickHTTPCheckWait(context, deadline, status) {
  if (context.cancelled()) slickHTTPThrowCancelled(status);
  if (Date.now() >= deadline) slickHTTPThrowTimeout(status);
}

function slickHTTPGuardSocket(socket, context, deadline, status, finish) {
  return setInterval(() => {
    if (context.cancelled()) {
      socket.destroy();
      const error = new Error("cancelled");
      error.slickHTTPCancelled = true;
      if (status != null) error.slickHTTPStatus = status;
      finish(error);
    } else if (Date.now() >= deadline) {
      socket.destroy();
      const error = new Error("timeout");
      error.slickHTTPTimeout = true;
      if (status != null) error.slickHTTPStatus = status;
      finish(error);
    }
  }, 10);
}

async function slickHTTPDial(net, host, port, deadline, context, existing) {
  slickHTTPCheckWait(context, deadline, null);
  return await new Promise((resolve, reject) => {
    const socket = existing || net.connect({ host: slickHTTPDialHost(host), port: port });
    let done = false;
    let guard = null;
    const finish = (error, value) => {
      if (done) return;
      done = true;
      if (guard !== null) clearInterval(guard);
      socket.off("connect", onConnect);
      socket.off("error", onError);
      socket.off("timeout", onTimeout);
      if (error) reject(error);
      else resolve(value);
    };
    const onConnect = () => finish(null, socket);
    const onError = (error) => finish(error);
    const onTimeout = () => {
      if (Date.now() < deadline) {
        socket.setTimeout(slickHTTPTimerDelay(deadline));
        return;
      }
      socket.destroy();
      const error = new Error("timeout");
      error.slickHTTPTimeout = true;
      finish(error);
    };
    guard = slickHTTPGuardSocket(socket, context, deadline, null, finish);
    socket.setTimeout(slickHTTPTimerDelay(deadline));
    socket.on("connect", onConnect);
    socket.on("error", onError);
    socket.on("timeout", onTimeout);
  });
}

async function slickHTTPWrapTLS(tls, socket, servername, deadline, context) {
  slickHTTPCheckWait(context, deadline, null);
  return await new Promise((resolve, reject) => {
    const options = { socket: socket };
    if (servername && !/^\d+\.\d+\.\d+\.\d+$/.test(servername) && servername.indexOf(":") < 0) {
      options.servername = servername;
    }
    const secure = tls.connect(options);
    let done = false;
    let guard = null;
    const finish = (error, value) => {
      if (done) return;
      done = true;
      if (guard !== null) clearInterval(guard);
      secure.off("secureConnect", onConnect);
      secure.off("error", onError);
      secure.off("timeout", onTimeout);
      if (error) reject(error);
      else resolve(value);
    };
    const onConnect = () => finish(null, secure);
    const onError = (error) => finish(error);
    const onTimeout = () => {
      if (Date.now() < deadline) {
        secure.setTimeout(slickHTTPTimerDelay(deadline));
        return;
      }
      secure.destroy();
      const error = new Error("timeout");
      error.slickHTTPTimeout = true;
      finish(error);
    };
    guard = slickHTTPGuardSocket(secure, context, deadline, null, finish);
    secure.setTimeout(slickHTTPTimerDelay(deadline));
    secure.on("secureConnect", onConnect);
    secure.on("error", onError);
    secure.on("timeout", onTimeout);
  });
}

function slickHTTPWriter(socket, data, context, deadline, status) {
  slickHTTPCheckWait(context, deadline, status);
  return new Promise((resolve, reject) => {
    let done = false;
    let guard = null;
    const finish = (error) => {
      if (done) return;
      done = true;
      if (guard !== null) clearInterval(guard);
      socket.off("error", onError);
      socket.off("timeout", onTimeout);
      socket.setTimeout(0);
      if (error) reject(error);
      else resolve();
    };
    const onError = (error) => finish(error);
    const onTimeout = () => {
      if (Date.now() < deadline) {
        socket.setTimeout(slickHTTPTimerDelay(deadline));
        return;
      }
      socket.destroy();
      const error = new Error("timeout");
      error.slickHTTPTimeout = true;
      if (status != null) error.slickHTTPStatus = status;
      finish(error);
    };
    guard = slickHTTPGuardSocket(socket, context, deadline, status, finish);
    socket.setTimeout(slickHTTPTimerDelay(deadline));
    socket.on("error", onError);
    socket.on("timeout", onTimeout);
    socket.write(data, (error) => {
      if (error) finish(error);
      else finish(null);
    });
  });
}

function slickHTTPReader(socket) {
  const state = { chunks: [], length: 0, ended: false, err: null, wait: null };
  const onData = (chunk) => {
    if (!chunk || chunk.length === 0) return;
    state.chunks.push(chunk);
    state.length += chunk.length;
    if (state.wait) {
      const wait = state.wait;
      state.wait = null;
      wait();
    }
  };
  const wake = () => {
    if (state.wait) {
      const wait = state.wait;
      state.wait = null;
      wait();
    }
  };
  socket.on("data", onData);
  socket.on("end", () => {
    state.ended = true;
    wake();
  });
  socket.on("error", (error) => {
    state.err = error;
    wake();
  });
  state.detach = () => {
    socket.off("data", onData);
  };
  return state;
}

async function slickHTTPWait(state, context, deadline, pred, status) {
  while (true) {
    slickHTTPCheckWait(context, deadline, status);
    if (state.err) throw state.err;
    if (pred()) return;
    if (state.ended) {
      const error = new Error("eof");
      throw error;
    }
    await new Promise((resolve) => {
      state.wait = resolve;
      setTimeout(resolve, Math.min(10, slickHTTPRemaining(deadline)));
    });
  }
}

function slickHTTPIndexOf(state, needle) {
  const nlen = needle.length;
  if (state.length < nlen) return -1;
  const first = needle.charCodeAt(0);
  let matched = 0;
  let pos = 0;
  for (let ci = 0; ci < state.chunks.length; ci++) {
    const chunk = state.chunks[ci];
    for (let i = 0; i < chunk.length; i++) {
      if (chunk[i] === needle.charCodeAt(matched)) {
        matched += 1;
        if (matched === nlen) return pos - nlen + 1;
      } else if (chunk[i] === first) {
        matched = 1;
      } else {
        matched = 0;
      }
      pos += 1;
    }
  }
  return -1;
}

function slickHTTPConsume(state, count) {
  if (count <= 0) return Buffer.alloc(0);
  if (count > state.length) count = state.length;
  const parts = [];
  let remaining = count;
  while (remaining > 0 && state.chunks.length > 0) {
    const chunk = state.chunks[0];
    if (chunk.length <= remaining) {
      parts.push(chunk);
      remaining -= chunk.length;
      state.chunks.shift();
    } else {
      parts.push(chunk.subarray(0, remaining));
      state.chunks[0] = chunk.subarray(remaining);
      remaining = 0;
    }
  }
  state.length -= count;
  if (parts.length === 1) return parts[0];
  return Buffer.concat(parts);
}

function slickHTTPParseHeaders(headerText) {
  const lines = headerText.split("\r\n");
  const statusLine = lines[0] || "";
  const match = /^HTTP\/\d+\.\d+ (\d{3})(?: (.*))?$/.exec(statusLine);
  if (match === null) throw new Error("status");
  const status = Number(match[1]);
  const pairs = [];
  for (let index = 1; index < lines.length; index += 1) {
    const line = lines[index];
    if (line === "") continue;
    const lead = line.charCodeAt(0);
    if (lead === 32 || lead === 9) {
      if (pairs.length === 0) throw new Error("header");
      pairs[pairs.length - 1][1] += " " + line.replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
      if (!slickHTTPValidFieldValue(pairs[pairs.length - 1][1])) throw new Error("header");
      continue;
    }
    const colon = line.indexOf(":");
    if (colon < 0) throw new Error("header");
    const name = line.slice(0, colon);
    let value = line.slice(colon + 1);
    value = value.replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
    if (name.length === 0 || slickHTTPContainsCTL(name) || !slickHTTPValidFieldValue(value)) throw new Error("header");
    pairs.push([name, value]);
  }
  return { status: status, headers: slickHTTPMergeHeaders(pairs) };
}

const slickHTTPMaxResponseHeaderBytes = 10 * 1024 * 1024;

async function slickHTTPReadHeaders(state, context, deadline, acceptContinue) {
  let used = 0;
  while (true) {
    await slickHTTPWait(state, context, deadline, () => {
      return slickHTTPIndexOf(state, "\r\n\r\n") >= 0 || used + state.length > slickHTTPMaxResponseHeaderBytes;
    }, null);
    const headerAt = slickHTTPIndexOf(state, "\r\n\r\n");
    if (headerAt < 0 || used + headerAt + 4 > slickHTTPMaxResponseHeaderBytes) throw new Error("headers");
    used += headerAt + 4;
    const parsed = slickHTTPParseHeaders(slickHTTPConsume(state, headerAt + 4).toString("utf8"));
    if (parsed.status >= 100 && parsed.status < 200 && parsed.status !== 101) {
      if (acceptContinue && parsed.status === 100) return parsed;
      continue;
    }
    return parsed;
  }
}

function slickHTTPThrowBodyFailure(kind, status, message) {
  const error = new Error(kind);
  error.slickHTTPFailure = slickHTTPFailure(kind, "", status, message);
  error.slickHTTPStatus = status;
  throw error;
}

function slickHTTPClassifyBodyError(error, status, maxBody) {
  if (error && (error.slickHTTPCancelled || error.slickHTTPTimeout)) {
    if (error.slickHTTPStatus == null) error.slickHTTPStatus = status;
    throw error;
  }
  if (error && error.slickHTTPFailure) {
    if (error.slickHTTPStatus == null) error.slickHTTPStatus = status;
    throw error;
  }
  if (error && error.slickHTTPTooLarge) {
    slickHTTPThrowBodyFailure("BodyTooLarge", status, "response body exceeds " + maxBody.toString() + " bytes");
  }
  slickHTTPThrowBodyFailure("BodyRead", status, "failed to read response body");
}

async function slickHTTPReadResponse(socket, context, deadline, maxBody, method, options) {
  const state = options && options.state ? options.state : slickHTTPReader(socket);
  const parsed = options && options.parsed ? options.parsed : await slickHTTPReadHeaders(state, context, deadline);
  const status = parsed.status;
  const framing = slickHTTPResponseFraming(parsed.headers);
  if (framing.failure) {
    state.detach();
    const error = new Error("framing");
    error.slickHTTPFailure = slickHTTPFailure("Transport", "", null, "HTTP transport failed");
    throw error;
  }
  let headers = slickHTTPStripTransferHeaders(parsed.headers, !!framing.chunked);
  if (framing.lengthText != null) {
    const collapsed = [];
    let replaced = false;
    for (const [name, values] of headers) {
      if (name === "Content-Length") {
        if (!replaced) {
          collapsed.push([name, [framing.lengthText]]);
          replaced = true;
        }
        continue;
      }
      collapsed.push([name, values.slice()]);
    }
    headers = collapsed;
  }
  if (slickHTTPNoResponseBody(method, status)) {
    state.detach();
    return { status: status, headers: headers, body: new Uint8Array() };
  }
  const drainLimit = options && options.drainLimit != null ? options.drainLimit : null;
  const limit = drainLimit != null ? drainLimit : (maxBody < 9223372036854775807n ? Number(maxBody) + 1 : Number.MAX_SAFE_INTEGER);
  let body;
  try {
    if (framing.chunked) {
      body = await slickHTTPReadChunked(state, context, deadline, limit, status);
    } else if (framing.length != null) {
      const take = framing.length > BigInt(limit) ? limit : Number(framing.length);
      body = await slickHTTPReadExact(state, context, deadline, take, status);
    } else {
      body = await slickHTTPReadUntilClose(state, context, deadline, limit, status);
    }
  } catch (error) {
    state.detach();
    if (drainLimit != null) {
      if (error && (error.slickHTTPCancelled || error.slickHTTPTimeout)) throw error;
      return { status: status, headers: headers, body: new Uint8Array() };
    }
    slickHTTPClassifyBodyError(error, status, maxBody);
  }
  state.detach();
  if (drainLimit != null) return { status: status, headers: headers, body: new Uint8Array() };
  if (body.length > Number(maxBody)) {
    return {
      failure: slickHTTPFailure("BodyTooLarge", "", status, "response body exceeds " + maxBody.toString() + " bytes"),
      status: status,
      headers: headers,
      body: body,
    };
  }
  return { status: status, headers: headers, body: new Uint8Array(body) };
}

async function slickHTTPReadExact(state, context, deadline, length, status) {
  await slickHTTPWait(state, context, deadline, () => state.length >= length || state.ended, status);
  if (state.length < length) throw new Error("body");
  return Buffer.from(slickHTTPConsume(state, length));
}

async function slickHTTPReadUntilClose(state, context, deadline, limit, status) {
  // Go's LimitReader stops at the first byte past the limit, so the wait must
  // wake as soon as that byte has arrived.
  await slickHTTPWait(state, context, deadline, () => state.ended || state.length >= limit, status);
  const take = Math.min(state.length, limit);
  return Buffer.from(slickHTTPConsume(state, take));
}

const slickHTTPMaxChunkLine = 4096;

async function slickHTTPReadLine(state, context, deadline, status) {
  await slickHTTPWait(state, context, deadline, () => {
    const at = slickHTTPIndexOf(state, "\r\n");
    return at >= 0 || state.ended || state.length > slickHTTPMaxChunkLine;
  }, status);
  const at = slickHTTPIndexOf(state, "\r\n");
  if (at < 0 || at > slickHTTPMaxChunkLine) throw new Error("line");
  const line = slickHTTPConsume(state, at + 2).toString("latin1");
  return line.slice(0, -2);
}

async function slickHTTPReadChunked(state, context, deadline, limit, status) {
  const chunks = [];
  let total = 0;
  while (true) {
    const line = await slickHTTPReadLine(state, context, deadline, status);
    const trimmed = line.replace(/[ \t\r\n]+$/, "");
    const semi = trimmed.indexOf(";");
    const sizeText = semi < 0 ? trimmed : trimmed.slice(0, semi);
    if (!/^[0-9a-fA-F]+$/.test(sizeText)) throw new Error("chunk");
    const size = parseInt(sizeText, 16);
    if (!Number.isSafeInteger(size) || size < 0) throw new Error("chunk");
    if (size === 0) {
      while (true) {
        const trailer = await slickHTTPReadLine(state, context, deadline, status);
        if (trailer === "") break;
        const colon = trailer.indexOf(":");
        if (colon < 0) throw new Error("trailer");
        const name = trailer.slice(0, colon);
        const value = trailer.slice(colon + 1).replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
        if (!slickHTTPValidToken(name) || !slickHTTPValidFieldValue(value)) throw new Error("trailer");
        const canonical = slickHTTPCanonical(name);
        if (canonical === "Transfer-Encoding" || canonical === "Content-Length" || canonical === "Trailer") {
          throw new Error("trailer");
        }
      }
      break;
    }
    const remaining = limit - total;
    if (size >= remaining) {
      await slickHTTPReadExact(state, context, deadline, remaining, status);
      const error = new Error("too large");
      error.slickHTTPTooLarge = true;
      throw error;
    }
    const piece = await slickHTTPReadExact(state, context, deadline, size, status);
    const crlf = await slickHTTPReadExact(state, context, deadline, 2, status);
    if (crlf.toString("latin1") !== "\r\n") throw new Error("chunk end");
    chunks.push(piece);
    total += size;
  }
  return Buffer.concat(chunks);
}

function slickHTTPAttachFailureURL(error, urlString) {
  if (!error || !error.slickHTTPFailure) return error;
  const kind = error.slickHTTPFailure.fields.get("Kind");
  const message = error.slickHTTPFailure.fields.get("Message");
  const status = error.slickHTTPStatus != null ? error.slickHTTPStatus : null;
  error.slickHTTPFailure = slickHTTPFailure(kind, slickHTTPSanitized(urlString), status, message);
  return error;
}

function slickHTTPShouldSendContentLength(method, length) {
  if (length > 0) return true;
  if (method === "POST" || method === "PUT" || method === "PATCH") return true;
  if (method === "GET" || method === "HEAD" || method === "DELETE" || method === "CONNECT" || method === "OPTIONS" || method === "TRACE" || method === "") return false;
  return true;
}

async function slickHTTPExchange(context, deadline, urlString, method, headers, body, bodyPresent, maxBytes, peekRedirect) {
  const parsedURL = slickHTTPParseURL(urlString);
  if (!parsedURL) throw new Error("url");
  if (slickHTTPLegacyNumericHost(parsedURL.host)) throw new Error("host");
  const https = parsedURL.protocol === "https:";
  const hostname = parsedURL.hostname;
  const port = parsedURL.port !== "" ? Number(parsedURL.port) : (https ? 443 : 80);
  const path = (parsedURL.pathname || "/") + parsedURL.search;
  const proxy = slickHTTPProxyFor(urlString);
  const mods = await slickHTTPModules();
  let connectHost = hostname;
  let connectPort = port;
  let absoluteForm = false;
  let tunnel = false;
  if (proxy) {
    if (proxy.refuseCGI) {
      return { failure: slickHTTPFailure("Transport", slickHTTPSanitized(urlString), null, "HTTP transport failed") };
    }
    connectHost = proxy.hostname;
    connectPort = proxy.port;
    if (https) tunnel = true;
    else absoluteForm = true;
  }
  let socket;
  try {
    socket = await slickHTTPDial(mods.net, connectHost, connectPort, deadline, context, null);
    if (proxy && proxy.https) {
      socket = await slickHTTPWrapTLS(mods.tls, socket, slickHTTPDialHost(connectHost), deadline, context);
    }
    if (tunnel) {
      const connectTarget = slickHTTPConnectAuthority(hostname, port);
      let connectHead = "CONNECT " + connectTarget + " HTTP/1.1\r\nHost: " + connectTarget + "\r\n";
      if (proxy.authorization) connectHead += "Proxy-Authorization: Basic " + proxy.authorization + "\r\n";
      connectHead += "\r\n";
      await slickHTTPWriter(socket, Buffer.from(connectHead, "utf8"), context, deadline, null);
      const tunnelState = slickHTTPReader(socket);
      let tunneled;
      try {
        tunneled = await slickHTTPReadHeaders(tunnelState, context, deadline);
      } finally {
        tunnelState.detach();
        if (tunnelState.length > 0 && typeof socket.unshift === "function") socket.unshift(slickHTTPConsume(tunnelState, tunnelState.length));
      }
      if (tunneled.status < 200 || tunneled.status > 299) {
        socket.destroy();
        return { failure: slickHTTPFailure("Transport", slickHTTPSanitized(urlString), null, "HTTP transport failed") };
      }
      socket = await slickHTTPWrapTLS(mods.tls, socket, slickHTTPDialHost(hostname), deadline, context);
    } else if (https) {
      socket = await slickHTTPWrapTLS(mods.tls, socket, slickHTTPDialHost(hostname), deadline, context);
    }
    const target = absoluteForm ? parsedURL.protocol + "//" + parsedURL.host + path : path;
    let head = method + " " + target + " HTTP/1.1\r\n";
    head += "Host: " + parsedURL.host + "\r\n";
    const replaceProxyAuth = !!(absoluteForm && proxy && proxy.authorization);
    let expectContinue = false;
    for (const [name, values] of headers) {
      if (name === "Trailer") continue;
      if (name === "Proxy-Authorization" && replaceProxyAuth) continue;
      if (name === "Expect") {
        for (const value of values) {
          if (slickHTTPTrimOWS(value).toLowerCase() === "100-continue") expectContinue = true;
        }
      }
      if (name === "User-Agent") {
        if (values.length === 0 || values[0] === "") continue;
        head += "User-Agent: " + slickHTTPTrimOWS(values[0]) + "\r\n";
        continue;
      }
      for (const value of values) head += name + ": " + slickHTTPTrimOWS(value) + "\r\n";
    }
    if (replaceProxyAuth) head += "Proxy-Authorization: Basic " + proxy.authorization + "\r\n";
    if (slickHTTPShouldSendContentLength(method, body.length)) head += "Content-Length: " + body.length + "\r\n";
    head += "\r\n";
    const headBytes = Buffer.from(head, "utf8");
    const hasBody = bodyPresent && body.length > 0;
    if (hasBody && !expectContinue) {
      await slickHTTPWriter(socket, Buffer.concat([headBytes, Buffer.from(body)]), context, deadline, null);
    } else {
      await slickHTTPWriter(socket, headBytes, context, deadline, null);
    }
    const state = slickHTTPReader(socket);
    let parsed = null;
    if (hasBody && expectContinue) {
      const continueDeadline = Math.min(deadline, Date.now() + 1000);
      try {
        parsed = await slickHTTPReadHeaders(state, context, continueDeadline, true);
      } catch (error) {
        if (!(error && error.slickHTTPTimeout) || Date.now() >= deadline) throw error;
        parsed = null;
      }
      if (!parsed || parsed.status === 100) {
        parsed = null;
        await slickHTTPWriter(socket, Buffer.from(body), context, deadline, null);
      }
    }
    if (!parsed) parsed = await slickHTTPReadHeaders(state, context, deadline);
    const headerList = slickHTTPStripTransferHeaders(parsed.headers, false);
    const location = slickHTTPHeaderValue(headerList, "Location");
    const drainLimit = peekRedirect && slickHTTPIsRedirect(parsed.status) && location !== null ? 2048 : null;
    const read = await slickHTTPReadResponse(socket, context, deadline, maxBytes, method, { state: state, parsed: parsed, drainLimit: drainLimit });
    socket.destroy();
    if (read.failure) {
      read.failure = slickHTTPFailure(read.failure.fields.get("Kind"), slickHTTPSanitized(urlString), read.status, read.failure.fields.get("Message"));
      return { failure: read.failure };
    }
    return { response: { status: read.status, url: urlString, headers: read.headers, body: read.body } };
  } catch (error) {
    if (socket) socket.destroy();
    throw slickHTTPAttachFailureURL(error, urlString);
  }
}

function slickHTTPEnvLookup(upper, lower) {
  const first = slickEnvironmentRead(upper);
  if (first !== null && first !== "") return first;
  const second = slickEnvironmentRead(lower);
  if (second !== null && second !== "") return second;
  return null;
}

function slickHTTPCGIRequestMethod() {
  const value = slickEnvironmentRead("REQUEST_METHOD");
  return value !== null && value !== "";
}

function slickHTTPCanonicalPort(scheme, port) {
  if (port) return port;
  return scheme.toLowerCase() === "https" ? 443 : 80;
}

function slickHTTPParseIP(host) {
  let value = host;
  if (value.charAt(0) === "[" && value.charAt(value.length - 1) === "]") value = value.slice(1, -1);
  return value;
}

function slickHTTPParseCIDR(entry) {
  const slash = entry.lastIndexOf("/");
  if (slash < 0) return null;
  const prefix = Number(entry.slice(slash + 1));
  if (!Number.isInteger(prefix) || prefix < 0) return null;
  const ip = slickHTTPParseIP(entry.slice(0, slash));
  const v4 = /^(\d{1,3}\.){3}\d{1,3}$/.test(ip);
  const maximum = v4 ? 32 : 128;
  if (prefix > maximum) return null;
  return { ip: ip, prefix: prefix, v4: v4 };
}

function slickHTTPIPToBits(ip, v4) {
  if (v4) {
    const parts = ip.split(".");
    if (parts.length !== 4) return null;
    let value = 0;
    for (const part of parts) {
      const octet = Number(part);
      if (!Number.isInteger(octet) || octet < 0 || octet > 255) return null;
      value = (value << 8) + octet;
    }
    return BigInt(value >>> 0);
  }
  let text = ip;
  if (text.charAt(0) === "[" && text.charAt(text.length - 1) === "]") text = text.slice(1, -1);
  const halves = text.split("::");
  if (halves.length > 2) return null;
  const parseHalf = (half) => {
    if (half === "") return [];
    return half.split(":");
  };
  let head = parseHalf(halves[0]);
  let tail = halves.length === 2 ? parseHalf(halves[1]) : [];
  if (head.length + tail.length > 8) return null;
  const missing = 8 - head.length - tail.length;
  const groups = halves.length === 2 ? head.concat(Array(missing).fill("0"), tail) : head;
  if (groups.length !== 8) return null;
  let value = 0n;
  for (const group of groups) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(group)) return null;
    value = (value << 16n) + BigInt(parseInt(group, 16));
  }
  return value;
}

function slickHTTPIPInCIDR(ip, network, prefix, v4) {
  const left = slickHTTPIPToBits(ip, v4);
  const right = slickHTTPIPToBits(network, v4);
  if (left === null || right === null) return false;
  if (prefix === 0) return true;
  const width = v4 ? 32n : 128n;
  const shift = width - BigInt(prefix);
  return (left >> shift) === (right >> shift);
}

function slickHTTPSplitHostPort(entry) {
  if (entry.charAt(0) === "[") {
    const end = entry.indexOf("]");
    if (end < 0) return null;
    if (entry.length === end + 1) return null;
    if (entry.charAt(end + 1) !== ":") return null;
    const host = entry.slice(1, end);
    const portText = entry.slice(end + 2);
    if (host === "") return null;
    if (portText === "") return { host: host, port: null };
    if (!/^\d+$/.test(portText)) return null;
    const port = Number(portText);
    if (!Number.isInteger(port) || port < 0 || port > 65535) return null;
    return { host: host, port: port };
  }
  const colon = entry.lastIndexOf(":");
  if (colon < 0) return null;
  const host = entry.slice(0, colon);
  if (host === "" || host.indexOf(":") >= 0) return null;
  const portText = entry.slice(colon + 1);
  if (portText === "") return { host: host, port: null };
  if (!/^\d+$/.test(portText)) return null;
  const port = Number(portText);
  if (!Number.isInteger(port) || port < 0 || port > 65535) return null;
  return { host: host, port: port };
}

function slickHTTPIsLoopbackHost(host) {
  if (host.toLowerCase() === "localhost") return true;
  const ip = slickHTTPParseIP(host);
  if (ip === "127.0.0.1" || ip === "::1") return true;
  if (/^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(ip)) return true;
  const bits = slickHTTPIPToBits(ip, false);
  if (bits === null) return false;
  return (bits >> 120n) === 0n && ((bits >> 112n) & 0xffn) === 1n;
}

function slickHTTPHostHasSuffix(host, suffix) {
  if (suffix === "") return false;
  const lowerHost = host.toLowerCase();
  const lowerSuffix = suffix.toLowerCase();
  if (!lowerHost.endsWith(lowerSuffix)) return false;
  const rest = lowerHost.slice(0, lowerHost.length - lowerSuffix.length);
  return rest.endsWith(".");
}

function slickHTTPNoProxyHost(host, pattern) {
  let match = pattern;
  if (match.charAt(0) === "*") match = match.slice(1);
  if (match.charAt(0) === ".") return slickHTTPHostHasSuffix(host, match.slice(1));
  const hostIP = slickHTTPIPToBits(slickHTTPParseIP(host), /^(\d{1,3}\.){3}\d{1,3}$/.test(slickHTTPParseIP(host)));
  const patternIP = slickHTTPIPToBits(slickHTTPParseIP(match), /^(\d{1,3}\.){3}\d{1,3}$/.test(slickHTTPParseIP(match)));
  if (hostIP !== null && patternIP !== null) return hostIP === patternIP;
  return host.toLowerCase() === match.toLowerCase() || slickHTTPHostHasSuffix(host, match);
}

function slickHTTPBypassProxy(host, port, noProxy) {
  if (slickHTTPIsLoopbackHost(host)) return true;
  const hostIP = slickHTTPParseIP(host);
  const hostIsV4 = /^(\d{1,3}\.){3}\d{1,3}$/.test(hostIP);
  for (const raw of noProxy.split(",")) {
    const entry = raw.trim();
    if (entry === "") continue;
    if (entry === "*") return true;
    const cidr = slickHTTPParseCIDR(entry);
    if (cidr) {
      if (slickHTTPIPToBits(hostIP, hostIsV4) !== null && cidr.v4 === hostIsV4) {
        if (slickHTTPIPInCIDR(hostIP, cidr.ip, cidr.prefix, cidr.v4)) return true;
      }
      continue;
    }
    const split = slickHTTPSplitHostPort(entry);
    const entryHost = split ? split.host : entry;
    const entryPort = split ? split.port : null;
    if (entryPort !== null && port !== entryPort) continue;
    if (slickHTTPNoProxyHost(host, entryHost)) return true;
  }
  return false;
}

function slickHTTPProxyFor(url) {
  const parsed = slickHTTPParseURL(url);
  if (!parsed) return null;
  const host = parsed.hostname;
  const scheme = parsed.protocol.slice(0, -1);
  const port = slickHTTPCanonicalPort(scheme, parsed.port !== "" ? Number(parsed.port) : 0);
  const noProxy = slickHTTPEnvLookup("NO_PROXY", "no_proxy") || "";
  let proxyValue = null;
  let refuseCGI = false;
  if (scheme.toLowerCase() === "https") {
    proxyValue = slickHTTPEnvLookup("HTTPS_PROXY", "https_proxy");
  } else if (slickHTTPCGIRequestMethod()) {
    const configured = slickHTTPEnvLookup("HTTP_PROXY", "http_proxy");
    if (configured !== null) refuseCGI = true;
  } else {
    proxyValue = slickHTTPEnvLookup("HTTP_PROXY", "http_proxy");
  }
  if (refuseCGI) return { refuseCGI: true };
  if (proxyValue === null) return null;
  if (slickHTTPBypassProxy(host, port, noProxy)) return null;
  let proxyURL;
  try {
    proxyURL = new URL(proxyValue.includes("://") ? proxyValue : "http://" + proxyValue);
  } catch (error) {
    return { refuseCGI: true };
  }
  if (proxyURL.protocol !== "http:" && proxyURL.protocol !== "https:") {
    return { refuseCGI: true };
  }
  const proxyPort = proxyURL.port !== "" ? Number(proxyURL.port) : (proxyURL.protocol === "https:" ? 443 : 80);
  let authorization = null;
  if (proxyURL.username !== "" || proxyURL.password !== "") {
    const user = slickHTTPDecodeUserinfo(proxyURL.username);
    const pass = slickHTTPDecodeUserinfo(proxyURL.password);
    authorization = Buffer.from(user + ":" + pass, "utf8").toString("base64");
  }
  return { hostname: proxyURL.hostname, port: proxyPort, https: proxyURL.protocol === "https:", authorization: authorization, refuseCGI: false };
}

function slickHTTPStatusText(code) {
  if (code < 0n || code > 999n) return null;
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
`
