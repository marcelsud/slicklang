package compiler

var bunStdJSON = bunStdFamily{
	family: runtimeFamilyJSON,
	module: bunStdJSONModule,
	functions: map[runtimeOperationID]string{
		nativeStdJsonDecode: "slickNatJsonDecode",
		nativeStdJsonEncode: "slickNatJsonEncode",
	},
}

// bunStdJSONModule implements std.json.Encode and std.json.Decode. Encoding
// and decoding are descriptor-driven from SLICK_TYPES (name, json_name, typ,
// union variants). The walk is iterative so a 10000-deep document matches
// encoding/json without overflowing the JavaScript stack.
const bunStdJSONModule = `const SLICK_JSON_MAX_DEPTH = 10000;

function slickJsonParseType(text) {
  const trimmed = text.trim();
  if (trimmed.endsWith("?")) return { kind: "optional", base: slickJsonParseType(trimmed.slice(0, -1)) };
  if (trimmed.endsWith("[]")) return { kind: "array", element: slickJsonParseType(trimmed.slice(0, -2)) };
  if (trimmed.startsWith("Map<")) return { kind: "map" };
  if (trimmed.startsWith("Result<")) return { kind: "result" };
  if (trimmed.startsWith("(")) return { kind: "tuple" };
  if (trimmed === "bytes") return { kind: "bytes" };
  return { kind: "named", name: trimmed };
}

function slickJsonFindType(name) {
  const types = slickTypes();
  for (const descriptor of types) {
    if (descriptor.name === name) return descriptor;
  }
  return null;
}

function slickJsonFailure(operation, path, message) {
  return slickStdObject("std.json.Failure", [
    ["Message", message],
    ["Operation", operation],
    ["Path", path],
  ]);
}

function slickJsonErr(operation, error) {
  return slickErr(slickJsonFailure(operation, error.path, error.message));
}

function slickJsonError(path, message) {
  const error = new Error(message);
  error.slickJson = true;
  error.path = path;
  return error;
}

function slickJsonIsError(error) {
  return error !== null && typeof error === "object" && error.slickJson === true;
}

function slickJsonInvalidChar(byte, context) {
  let quoted;
  if (byte === 39) quoted = "'\\''";
  else if (byte === 34) quoted = "'\"'";
  else {
    let escaped;
    if (byte === 0x07) escaped = "\\a";
    else if (byte === 0x08) escaped = "\\b";
    else if (byte === 0x09) escaped = "\\t";
    else if (byte === 0x0a) escaped = "\\n";
    else if (byte === 0x0b) escaped = "\\v";
    else if (byte === 0x0c) escaped = "\\f";
    else if (byte === 0x0d) escaped = "\\r";
    else if (byte === 0x5c) escaped = "\\\\";
    else if (byte >= 0x20 && byte < 0x7f) escaped = String.fromCharCode(byte);
    else {
      const hex = byte.toString(16);
      escaped = "\\x" + (hex.length < 2 ? "0" + hex : hex);
    }
    quoted = "'" + escaped + "'";
  }
  return "invalid character " + quoted + " " + context;
}

function slickJsonParser(text) {
  return { text: text, pos: 0 };
}

function slickJsonPeek(parser) {
  return parser.pos < parser.text.length ? parser.text.charCodeAt(parser.pos) : -1;
}

function slickJsonSkipWs(parser) {
  while (parser.pos < parser.text.length) {
    const byte = parser.text.charCodeAt(parser.pos);
    if (byte !== 32 && byte !== 9 && byte !== 10 && byte !== 13) break;
    parser.pos += 1;
  }
}

function slickJsonParseAll(parser, path) {
  slickJsonSkipWs(parser);
  if (parser.pos >= parser.text.length) throw slickJsonError(path, "unexpected end of JSON input");
  const value = slickJsonParseValue(parser, path);
  slickJsonSkipWs(parser);
  if (parser.pos < parser.text.length) throw slickJsonError(path, "input contains more than one JSON value");
  return value;
}

function slickJsonParseValue(parser, startPath) {
  const stack = [];
  let path = startPath;
  for (;;) {
    const finished = slickJsonParseStart(parser, path, stack);
    if (finished === null) {
      path = stack[stack.length - 1].nextPath;
      continue;
    }
    let value = finished;
    for (;;) {
      if (stack.length === 0) return value;
      const frame = stack.pop();
      if (frame.kind === "array") {
        frame.elements.push(value);
        slickJsonSkipWs(parser);
        const next = slickJsonPeek(parser);
        if (next === 44) {
          parser.pos += 1;
          frame.nextPath = frame.path + "[" + frame.elements.length + "]";
          stack.push(frame);
          path = frame.nextPath;
          break;
        }
        if (next === 93) {
          parser.pos += 1;
          value = { kind: "array", elements: frame.elements };
          continue;
        }
        throw slickJsonError(frame.path, "expected end of array");
      }
      frame.entries.push([frame.key, value]);
      slickJsonSkipWs(parser);
      const next = slickJsonPeek(parser);
      if (next === 44) {
        parser.pos += 1;
        // Keys are tracked in a set, so a wide object costs linear work overall
        // instead of rescanning every earlier key per field.
        const nextKey = slickJsonParseObjectKey(parser, frame.path, frame.seen);
        slickJsonSkipWs(parser);
        if (slickJsonPeek(parser) !== 58) throw slickJsonError(frame.path, "expected end of object");
        parser.pos += 1;
        frame.key = nextKey;
        frame.nextPath = frame.path + "." + nextKey;
        stack.push(frame);
        path = frame.nextPath;
        break;
      }
      if (next === 125) {
        parser.pos += 1;
        value = { kind: "object", entries: frame.entries };
        continue;
      }
      throw slickJsonError(frame.path, "expected end of object");
    }
  }
}

function slickJsonParseObjectKey(parser, path, seen) {
  slickJsonSkipWs(parser);
  if (slickJsonPeek(parser) !== 34) throw slickJsonError(path, "object key must be a string");
  const key = slickJsonParseString(parser, path);
  if (seen.has(key)) throw slickJsonError(path + "." + key, "duplicate object key");
  seen.add(key);
  return key;
}

function slickJsonParseStart(parser, path, stack) {
  slickJsonSkipWs(parser);
  const byte = slickJsonPeek(parser);
  if (byte < 0) throw slickJsonError(path, "unexpected end of JSON input");
  if (byte === 91) {
    if (stack.length >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(path, "exceeded max depth");
    parser.pos += 1;
    slickJsonSkipWs(parser);
    if (slickJsonPeek(parser) === 93) {
      parser.pos += 1;
      return { kind: "array", elements: [] };
    }
    stack.push({ kind: "array", path: path, elements: [], nextPath: path + "[0]" });
    return null;
  }
  if (byte === 123) {
    if (stack.length >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(path, "exceeded max depth");
    parser.pos += 1;
    slickJsonSkipWs(parser);
    if (slickJsonPeek(parser) === 125) {
      parser.pos += 1;
      return { kind: "object", entries: [] };
    }
    const seen = new Set();
    const key = slickJsonParseObjectKey(parser, path, seen);
    slickJsonSkipWs(parser);
    if (slickJsonPeek(parser) !== 58) throw slickJsonError(path, "expected end of object");
    parser.pos += 1;
    stack.push({ kind: "object", path: path, entries: [], seen: seen, key: key, nextPath: path + "." + key });
    return null;
  }
  if (byte === 34) return { kind: "str", value: slickJsonParseString(parser, path) };
  if (byte === 116) return slickJsonParseLiteral(parser, "true", { kind: "bool", value: true }, path);
  if (byte === 102) return slickJsonParseLiteral(parser, "false", { kind: "bool", value: false }, path);
  if (byte === 110) return slickJsonParseLiteral(parser, "null", { kind: "null" }, path);
  if (byte === 45 || (byte >= 48 && byte <= 57)) return slickJsonParseNumber(parser, path);
  throw slickJsonError(path, "invalid JSON token");
}

function slickJsonParseLiteral(parser, literal, value, path) {
  if (parser.pos + literal.length <= parser.text.length && parser.text.slice(parser.pos, parser.pos + literal.length) === literal) {
    parser.pos += literal.length;
    return value;
  }
  throw slickJsonError(path, "invalid JSON token");
}

function slickJsonParseNumber(parser, path) {
  const start = parser.pos;
  if (slickJsonPeek(parser) === 45) {
    parser.pos += 1;
    const next = slickJsonPeek(parser);
    if (next < 0) throw slickJsonError(path, "unexpected EOF");
    if (next < 48 || next > 57) throw slickJsonError(path, slickJsonInvalidChar(next, "in numeric literal"));
  }
  const first = slickJsonPeek(parser);
  if (first === 48) {
    parser.pos += 1;
  } else if (first >= 49 && first <= 57) {
    parser.pos += 1;
    while (slickJsonPeek(parser) >= 48 && slickJsonPeek(parser) <= 57) parser.pos += 1;
  } else if (first < 0) {
    throw slickJsonError(path, "unexpected EOF");
  } else {
    throw slickJsonError(path, slickJsonInvalidChar(first, "in numeric literal"));
  }
  if (slickJsonPeek(parser) === 46) {
    parser.pos += 1;
    const frac = slickJsonPeek(parser);
    if (frac < 48 || frac > 57) {
      if (frac < 0) throw slickJsonError(path, "unexpected EOF");
      throw slickJsonError(path, slickJsonInvalidChar(frac, "after decimal point in numeric literal"));
    }
    while (slickJsonPeek(parser) >= 48 && slickJsonPeek(parser) <= 57) parser.pos += 1;
  }
  const exp = slickJsonPeek(parser);
  if (exp === 101 || exp === 69) {
    parser.pos += 1;
    const sign = slickJsonPeek(parser);
    if (sign === 43 || sign === 45) parser.pos += 1;
    const digit = slickJsonPeek(parser);
    if (digit < 48 || digit > 57) {
      if (digit < 0) throw slickJsonError(path, "unexpected EOF");
      throw slickJsonError(path, slickJsonInvalidChar(digit, "in exponent of numeric literal"));
    }
    while (slickJsonPeek(parser) >= 48 && slickJsonPeek(parser) <= 57) parser.pos += 1;
  }
  return { kind: "number", text: parser.text.slice(start, parser.pos) };
}

function slickJsonPeekUEscape(parser) {
  if (parser.pos + 6 > parser.text.length) return -1;
  if (parser.text.charCodeAt(parser.pos) !== 92 || parser.text.charCodeAt(parser.pos + 1) !== 117) return -1;
  let value = 0;
  for (let offset = 2; offset < 6; offset++) {
    const byte = parser.text.charCodeAt(parser.pos + offset);
    let digit;
    if (byte >= 48 && byte <= 57) digit = byte - 48;
    else if (byte >= 97 && byte <= 102) digit = byte - 87;
    else if (byte >= 65 && byte <= 70) digit = byte - 55;
    else return -1;
    value = (value * 16 + digit) >>> 0;
  }
  return value;
}

function slickJsonParseHex4(parser, path) {
  let value = 0;
  for (let i = 0; i < 4; i++) {
    if (parser.pos >= parser.text.length) throw slickJsonError(path, "unexpected end of JSON input");
    const byte = parser.text.charCodeAt(parser.pos);
    let digit;
    if (byte >= 48 && byte <= 57) digit = byte - 48;
    else if (byte >= 97 && byte <= 102) digit = byte - 87;
    else if (byte >= 65 && byte <= 70) digit = byte - 55;
    else throw slickJsonError(path, "invalid JSON token");
    value = (value * 16 + digit) >>> 0;
    parser.pos += 1;
  }
  return value;
}

function slickJsonFromCodepoint(codepoint) {
  if (codepoint > 0x10ffff || (codepoint >= 0xd800 && codepoint <= 0xdfff)) return "\uFFFD";
  return String.fromCodePoint(codepoint);
}

function slickJsonParseString(parser, path) {
  parser.pos += 1;
  let out = "";
  for (;;) {
    if (parser.pos >= parser.text.length) throw slickJsonError(path, "unexpected end of JSON input");
    const byte = parser.text.charCodeAt(parser.pos);
    if (byte === 34) {
      parser.pos += 1;
      return out;
    }
    if (byte === 92) {
      parser.pos += 1;
      if (parser.pos >= parser.text.length) throw slickJsonError(path, "unexpected end of JSON input");
      const escape = parser.text.charCodeAt(parser.pos);
      parser.pos += 1;
      if (escape === 34) out += "\"";
      else if (escape === 92) out += "\\";
      else if (escape === 47) out += "/";
      else if (escape === 98) out += "\b";
      else if (escape === 102) out += "\f";
      else if (escape === 110) out += "\n";
      else if (escape === 114) out += "\r";
      else if (escape === 116) out += "\t";
      else if (escape === 117) {
        const code = slickJsonParseHex4(parser, path);
        if (code >= 0xd800 && code <= 0xdbff) {
          const low = slickJsonPeekUEscape(parser);
          if (low >= 0xdc00 && low <= 0xdfff) {
            parser.pos += 6;
            out += slickJsonFromCodepoint(0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00));
          } else {
            out += "\uFFFD";
          }
        } else if (code >= 0xdc00 && code <= 0xdfff) {
          out += "\uFFFD";
        } else {
          out += slickJsonFromCodepoint(code);
        }
      } else {
        throw slickJsonError(path, "invalid JSON token");
      }
      continue;
    }
    if (byte < 0x20) throw slickJsonError(path, slickJsonInvalidChar(byte, "in string literal"));
    out += parser.text[parser.pos];
    parser.pos += 1;
  }
}

function slickJsonFloat(value) {
  if (value === 0) return Object.is(value, -0) ? "-0" : "0";
  const magnitude = Math.abs(value);
  if (magnitude < 1e-6 || magnitude >= 1e21) {
    let text = value.toExponential();
    const index = text.indexOf("e");
    if (index >= 0) {
      const after = index + 1;
      if (after < text.length) {
        const sign = text.charCodeAt(after);
        if (sign !== 45 && sign !== 43) text = text.slice(0, after) + "+" + text.slice(after);
      }
    }
    return text;
  }
  return String(value);
}

function slickJsonWriteString(text) {
  let out = "\"";
  for (const character of text) {
    const code = character.codePointAt(0);
    if (character === "\"") out += "\\\"";
    else if (character === "\\") out += "\\\\";
    else if (code === 0x08) out += "\\b";
    else if (code === 0x0c) out += "\\f";
    else if (character === "\n") out += "\\n";
    else if (character === "\r") out += "\\r";
    else if (character === "\t") out += "\\t";
    else if (character === "<") out += "\\u003c";
    else if (character === ">") out += "\\u003e";
    else if (character === "&") out += "\\u0026";
    else if (code === 0x2028) out += "\\u2028";
    else if (code === 0x2029) out += "\\u2029";
    else if (code < 0x20) {
      const hex = code.toString(16);
      out += "\\u" + "0000".slice(hex.length) + hex;
    } else {
      out += character;
    }
  }
  return out + "\"";
}

function slickJsonWriteOut(root) {
  let out = "";
  const stack = [{ kind: "node", node: root }];
  while (stack.length > 0) {
    const task = stack.pop();
    if (task.kind === "char") {
      out += task.ch;
      continue;
    }
    if (task.kind === "key") {
      out += slickJsonWriteString(task.key);
      continue;
    }
    const node = task.node;
    if (node.kind === "null") out += "null";
    else if (node.kind === "bool") out += node.value ? "true" : "false";
    else if (node.kind === "number") out += node.text;
    else if (node.kind === "str") out += slickJsonWriteString(node.value);
    else if (node.kind === "array") {
      stack.push({ kind: "char", ch: "]" });
      for (let i = node.items.length - 1; i >= 0; i--) {
        stack.push({ kind: "node", node: node.items[i] });
        if (i > 0) stack.push({ kind: "char", ch: "," });
      }
      out += "[";
    } else if (node.kind === "object") {
      stack.push({ kind: "char", ch: "}" });
      for (let i = node.pairs.length - 1; i >= 0; i--) {
        stack.push({ kind: "node", node: node.pairs[i][1] });
        stack.push({ kind: "char", ch: ":" });
        stack.push({ kind: "key", key: node.pairs[i][0] });
        if (i > 0) stack.push({ kind: "char", ch: "," });
      }
      out += "{";
    }
  }
  return out;
}

function slickJsonObjectField(value, name) {
  if (value instanceof SlickObject && value.fields.has(name)) return value.fields.get(name);
  return undefined;
}

function slickJsonIsAbsent(value) {
  return value === undefined || value === null || (value instanceof SlickOptional && !value.present);
}

function slickJsonValueOut(value, typ, path, depth) {
  const stack = [{ kind: "value", value: value, typ: typ, path: path, depth: depth }];
  const done = [];
  while (stack.length > 0) {
    const work = stack.pop();
    if (work.kind === "ready") {
      done.push(work.node);
      continue;
    }
    if (work.kind === "finish-array") {
      const items = done.splice(done.length - work.count, work.count);
      done.push({ kind: "array", items: items });
      continue;
    }
    if (work.kind === "finish-object") {
      const values = done.splice(done.length - work.keys.length, work.keys.length);
      const pairs = [];
      for (let i = 0; i < work.keys.length; i++) pairs.push([work.keys[i], values[i]]);
      // encoding/json sorts object keys by UTF-8 bytes, not UTF-16 code units.
      pairs.sort((left, right) => Buffer.compare(Buffer.from(left[0], "utf8"), Buffer.from(right[0], "utf8")));
      done.push({ kind: "object", pairs: pairs });
      continue;
    }
    slickJsonEncodeStep(work.value, work.typ, work.path, work.depth, stack, done);
  }
  if (done.length === 0) throw slickJsonError(path, "unsupported JSON source type");
  return done.pop();
}

function slickJsonEncodeStep(value, typ, path, depth, stack, done) {
  if (typ !== null && typ !== undefined) {
    if (typ.kind === "optional") {
      if (slickJsonIsAbsent(value)) {
        done.push({ kind: "null" });
        return;
      }
      const inner = value instanceof SlickOptional ? value.value : value;
      stack.push({ kind: "value", value: inner, typ: typ.base, path: path, depth: depth });
      return;
    }
    if (typ.kind === "array") {
      if (Array.isArray(value)) {
        slickJsonEncodeArray(value, typ.element, path, depth, stack);
        return;
      }
      slickJsonEncodeStep(value, null, path, depth, stack, done);
      return;
    }
    if (typ.kind === "named") {
      if (typ.name === "null") {
        done.push({ kind: "null" });
        return;
      }
      if (typ.name === "bool") {
        if (typeof value === "boolean") {
          done.push({ kind: "bool", value: value });
          return;
        }
        slickJsonEncodeStep(value, null, path, depth, stack, done);
        return;
      }
      if (typ.name === "string") {
        if (typeof value === "string") {
          done.push({ kind: "str", value: value });
          return;
        }
        slickJsonEncodeStep(value, null, path, depth, stack, done);
        return;
      }
      if (typ.name === "int") {
        if (typeof value === "bigint") {
          done.push({ kind: "number", text: value.toString() });
          return;
        }
        slickJsonEncodeStep(value, null, path, depth, stack, done);
        return;
      }
      if (typ.name === "float") {
        if (typeof value === "number") {
          if (!Number.isFinite(value)) throw slickJsonError(path, "non-finite float cannot be encoded as JSON");
          done.push({ kind: "number", text: slickJsonFloat(value) });
          return;
        }
        slickJsonEncodeStep(value, null, path, depth, stack, done);
        return;
      }
      const descriptor = slickJsonFindType(typ.name);
      if (descriptor !== null && descriptor.kind === "class") {
        if (value instanceof SlickObject) {
          slickJsonEncodeClass(value, descriptor, path, depth, stack, done);
          return;
        }
        slickJsonEncodeStep(value, null, path, depth, stack, done);
        return;
      }
      throw slickJsonError(path, "unsupported JSON source type");
    }
    throw slickJsonError(path, "unsupported JSON source type");
  }
  if (value === null) done.push({ kind: "null" });
  else if (typeof value === "boolean") done.push({ kind: "bool", value: value });
  else if (typeof value === "bigint") done.push({ kind: "number", text: value.toString() });
  else if (typeof value === "number") {
    if (!Number.isFinite(value)) throw slickJsonError(path, "non-finite float cannot be encoded as JSON");
    done.push({ kind: "number", text: slickJsonFloat(value) });
  } else if (typeof value === "string") done.push({ kind: "str", value: value });
  else if (Array.isArray(value)) slickJsonEncodeArray(value, null, path, depth, stack);
  else if (value instanceof SlickOptional) {
    if (!value.present) done.push({ kind: "null" });
    else stack.push({ kind: "value", value: value.value, typ: null, path: path, depth: depth });
  } else if (value instanceof SlickObject) {
    const descriptor = slickJsonFindType(value.typeName);
    if (descriptor !== null && descriptor.kind === "class") {
      slickJsonEncodeClass(value, descriptor, path, depth, stack, done);
      return;
    }
    throw slickJsonError(path, "unsupported JSON source type");
  } else {
    throw slickJsonError(path, "unsupported JSON source type");
  }
}

function slickJsonEncodeArray(elements, elementType, path, depth, stack) {
  if (depth >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(path, "exceeded max depth");
  stack.push({ kind: "finish-array", count: elements.length });
  for (let i = elements.length - 1; i >= 0; i--) {
    stack.push({ kind: "value", value: elements[i], typ: elementType, path: path + "[" + i + "]", depth: depth + 1 });
  }
}

function slickJsonEncodeFieldWork(value, typ, path, depth) {
  if (typ.kind === "optional") {
    if (slickJsonIsAbsent(value)) return null;
    const inner = value instanceof SlickOptional ? value.value : value;
    return { kind: "value", value: inner, typ: typ.base, path: path, depth: depth };
  }
  if (value === undefined) return { kind: "ready", node: { kind: "null" } };
  return { kind: "value", value: value, typ: typ, path: path, depth: depth };
}

function slickJsonEncodeClass(value, descriptor, path, depth, stack, done) {
  if (depth >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(path, "exceeded max depth");
  const keys = [];
  const children = [];
  for (const field of descriptor.fields) {
    const fieldType = slickJsonParseType(field.typ);
    const work = slickJsonEncodeFieldWork(slickJsonObjectField(value, field.name), fieldType, path + "." + field.json_name, depth + 1);
    if (work !== null) {
      keys.push(field.json_name);
      children.push(work);
    }
  }
  stack.push({ kind: "finish-object", keys: keys });
  for (let i = children.length - 1; i >= 0; i--) stack.push(children[i]);
}

function slickJsonInferClass(value) {
  if (value.kind !== "object") return null;
  const keys = [];
  for (const entry of value.entries) keys.push(entry[0]);
  let matched = null;
  let count = 0;
  for (const descriptor of slickTypes()) {
    if (descriptor.kind !== "class") continue;
    const all = [];
    const required = [];
    for (const field of descriptor.fields) {
      all.push(field.json_name);
      if (!field.typ.endsWith("?")) required.push(field.json_name);
    }
    let requiredOk = true;
    for (const name of required) {
      if (keys.indexOf(name) < 0) {
        requiredOk = false;
        break;
      }
    }
    let keysOk = true;
    for (const key of keys) {
      if (all.indexOf(key) < 0) {
        keysOk = false;
        break;
      }
    }
    if (requiredOk && keysOk) {
      count += 1;
      matched = descriptor;
    }
  }
  return count === 1 ? matched : null;
}

function slickJsonInferValueType(value) {
  if (value.kind === "null") return { kind: "named", name: "null" };
  if (value.kind === "bool") return { kind: "named", name: "bool" };
  if (value.kind === "str") return { kind: "named", name: "string" };
  if (value.kind === "number") {
    const text = value.text;
    if (text.indexOf(".") >= 0 || text.indexOf("e") >= 0 || text.indexOf("E") >= 0) return { kind: "named", name: "float" };
    return { kind: "named", name: "int" };
  }
  if (value.kind === "array") {
    const element = value.elements.length === 0 ? { kind: "named", name: "null" } : slickJsonInferValueType(value.elements[0]);
    return { kind: "array", element: element };
  }
  const descriptor = slickJsonInferClass(value);
  return { kind: "named", name: descriptor === null ? "" : descriptor.name };
}

function slickJsonParseInt(value, path) {
  if (value.kind !== "number") throw slickJsonError(path, "expected JSON integer");
  const text = value.text;
  if (text.indexOf(".") >= 0 || text.indexOf("e") >= 0 || text.indexOf("E") >= 0) {
    throw slickJsonError(path, "expected JSON integer without fraction or exponent");
  }
  let number;
  try {
    number = BigInt(text);
  } catch (error) {
    throw slickJsonError(path, "integer out of int64 range");
  }
  if (slickWrapInt(number) !== number) throw slickJsonError(path, "integer out of int64 range");
  return number;
}

function slickJsonParseFloat(value, path) {
  if (value.kind !== "number") throw slickJsonError(path, "expected JSON number");
  const number = Number(value.text);
  if (!Number.isFinite(number)) throw slickJsonError(path, "number out of float64 range");
  return number;
}

function slickJsonConvert(value, typ, path) {
  const stack = [{ kind: "do", value: value, typ: typ, path: path, depth: 0 }];
  const done = [];
  while (stack.length > 0) {
    const work = stack.pop();
    if (work.kind === "finish-array") {
      done.push(done.splice(done.length - work.count, work.count));
      continue;
    }
    if (work.kind === "finish-optional") {
      if (done.length === 0) throw slickJsonError(path, "unsupported JSON target type");
      done.push(slickOptional(done.pop()));
      continue;
    }
    if (work.kind === "finish-class") {
      const converted = done.splice(done.length - work.fieldNames.length, work.fieldNames.length);
      const fields = [];
      const seen = {};
      for (let i = 0; i < work.fieldNames.length; i++) {
        fields.push([work.fieldNames[i], converted[i]]);
        seen[work.fieldNames[i]] = true;
      }
      for (const field of work.descriptor.fields) {
        if (seen[field.name] === true) continue;
        if (field.typ.endsWith("?")) fields.push([field.name, slickAbsent]);
        else throw slickJsonError(work.path + "." + field.json_name, "missing required field");
      }
      done.push(slickStdObject(work.typeName, fields));
      continue;
    }
    const current = work.value;
    const currentType = work.typ;
    if (currentType.kind === "optional") {
      if (current.kind === "null") done.push(slickAbsent);
      else {
        stack.push({ kind: "finish-optional" });
        stack.push({ kind: "do", value: current, typ: currentType.base, path: work.path, depth: work.depth });
      }
      continue;
    }
    if (currentType.kind === "array") {
      if (work.depth >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(work.path, "exceeded max depth");
      if (current.kind !== "array") throw slickJsonError(work.path, "expected JSON array");
      stack.push({ kind: "finish-array", count: current.elements.length });
      for (let i = current.elements.length - 1; i >= 0; i--) {
        stack.push({
          kind: "do",
          value: current.elements[i],
          typ: currentType.element,
          path: work.path + "[" + i + "]",
          depth: work.depth + 1,
        });
      }
      continue;
    }
    if (currentType.kind !== "named") throw slickJsonError(work.path, "unsupported JSON target type");
    if (currentType.name === "null") {
      if (current.kind !== "null") throw slickJsonError(work.path, "expected JSON null");
      done.push(null);
      continue;
    }
    if (currentType.name === "bool") {
      if (current.kind !== "bool") throw slickJsonError(work.path, "expected JSON boolean");
      done.push(current.value);
      continue;
    }
    if (currentType.name === "string") {
      if (current.kind !== "str") throw slickJsonError(work.path, "expected JSON string");
      done.push(current.value);
      continue;
    }
    if (currentType.name === "int") {
      done.push(slickJsonParseInt(current, work.path));
      continue;
    }
    if (currentType.name === "float") {
      done.push(slickJsonParseFloat(current, work.path));
      continue;
    }
    if (work.depth >= SLICK_JSON_MAX_DEPTH) throw slickJsonError(work.path, "exceeded max depth");
    const descriptor = slickJsonFindType(currentType.name);
    if (descriptor === null || descriptor.kind !== "class") throw slickJsonError(work.path, "unsupported JSON target type");
    if (current.kind !== "object") throw slickJsonError(work.path, "expected JSON object");
    const fieldNames = [];
    const children = [];
    for (const entry of current.entries) {
      let fieldDescriptor = null;
      for (const field of descriptor.fields) {
        if (field.json_name === entry[0]) {
          fieldDescriptor = field;
          break;
        }
      }
      if (fieldDescriptor === null) throw slickJsonError(work.path + "." + entry[0], "unknown field");
      fieldNames.push(fieldDescriptor.name);
      children.push({
        kind: "do",
        value: entry[1],
        typ: slickJsonParseType(fieldDescriptor.typ),
        path: work.path + "." + entry[0],
        depth: work.depth + 1,
      });
    }
    stack.push({ kind: "finish-class", typeName: descriptor.name, fieldNames: fieldNames, descriptor: descriptor, path: work.path });
    for (let i = children.length - 1; i >= 0; i--) stack.push(children[i]);
  }
  if (done.length === 0) throw slickJsonError(path, "unsupported JSON target type");
  return done.pop();
}

export async function slickNatJsonEncode(context, args) {
  try {
    const value = slickArg(args, 0);
    let typ = null;
    if (args.length > 1 && typeof args[1] === "string") typ = slickJsonParseType(args[1]);
    const node = slickJsonValueOut(value, typ, "$", 0);
    return slickOk(slickJsonWriteOut(node));
  } catch (error) {
    if (slickJsonIsError(error)) return slickJsonErr("Encode", error);
    throw slickAsFailure(error);
  }
}

export async function slickNatJsonDecode(context, args) {
  try {
    const text = slickArgString(args, 0);
    const parser = slickJsonParser(text);
    const value = slickJsonParseAll(parser, "$");
    const typ = args.length > 1 && typeof args[1] === "string" ? slickJsonParseType(args[1]) : slickJsonInferValueType(value);
    return slickOk(slickJsonConvert(value, typ, "$"));
  } catch (error) {
    if (slickJsonIsError(error)) return slickJsonErr("Decode", error);
    throw slickAsFailure(error);
  }
}
`
