package compiler

var rustStdJSON = rustStdFamily{
	family: runtimeFamilyJSON,
	module: rustStdJSONModule,
	functions: map[runtimeOperationID]string{
		nativeStdJsonDecode: "slick_nat_json_decode",
		nativeStdJsonEncode: "slick_nat_json_encode",
	},
}

// rustStdJSONModule implements std.json.Encode and std.json.Decode for the
// Rust backend. Encoding is descriptor-driven: the value's own type name
// selects its SlickTypeDescriptor, so no host reflection is needed. Decoding
// is driven by the same descriptor table; the declared target type is taken
// from the second argument when the call emitter supplies it, and otherwise
// inferred from the JSON structure against the descriptor table so a round
// trip still reproduces interpreter output. Every failure is reported as a
// std.json.Failure value with the same Operation, Path, and Message the
// interpreter and generated Go produce.
const rustStdJSONModule = `use std::str::FromStr;

const SLICK_JSON_MAX_DEPTH: usize = 10000;

// A parsed JSON tree. Numbers keep their original text so an int field and a
// float field decode the same way the interpreter's json.Number does.
enum SlickJsonValue {
    Null,
    Bool(bool),
    Number(String),
    Str(String),
    Array(Vec<SlickJsonValue>),
    Object(Vec<(String, SlickJsonValue)>),
}

// A parse or conversion failure carries the structural path the interpreter
// reports; the Operation field is always supplied by the caller.
struct SlickJsonError {
    path: String,
    message: String,
}

enum SlickJsonParseFrame {
    Array { path: String, elements: Vec<SlickJsonValue> },
    Object { path: String, entries: Vec<(String, SlickJsonValue)>, key: String },
}

enum SlickJsonOut {
    Null,
    Bool(bool),
    Number(String),
    Str(String),
    Array(Vec<SlickJsonOut>),
    Object(Vec<(String, SlickJsonOut)>),
}

// A parsed canonical Slick type spelling. The descriptor table already
// resolved type parameters and json names, so this only needs to mirror the
// shape spellings the compiler emits.
#[derive(Clone)]
enum SlickJsonType {
    Optional(Box<SlickJsonType>),
    Array(Box<SlickJsonType>),
    Map,
    Tuple,
    ResultType,
    Bytes,
    Named(String),
}

fn slick_json_parse_type(text: &str) -> SlickJsonType {
    let trimmed = text.trim();
    if let Some(base) = trimmed.strip_suffix('?') {
        return SlickJsonType::Optional(Box::new(slick_json_parse_type(base)));
    }
    if let Some(base) = trimmed.strip_suffix("[]") {
        return SlickJsonType::Array(Box::new(slick_json_parse_type(base)));
    }
    if trimmed.starts_with("Map<") {
        return SlickJsonType::Map;
    }
    if trimmed.starts_with("Result<") {
        return SlickJsonType::ResultType;
    }
    if trimmed.starts_with('(') {
        return SlickJsonType::Tuple;
    }
    if trimmed == "bytes" {
        return SlickJsonType::Bytes;
    }
    SlickJsonType::Named(trimmed.to_string())
}

fn slick_json_find_type(name: &str) -> Option<&'static SlickTypeDescriptor> {
    SLICK_TYPES.iter().find(|descriptor| descriptor.name == name)
}

// Builds a std.json.Failure value with the three fields the interpreter and
// generated Go populate, in the descriptor's field order.
fn slick_json_failure(operation: &str, path: &str, message: &str) -> SlickValue {
    slick_object("std.json.Failure", vec![
        ("Message", slick_string(message)),
        ("Operation", slick_string(operation)),
        ("Path", slick_string(path)),
    ])
}

fn slick_json_err(operation: &str, error: &SlickJsonError) -> SlickOutcome {
    slick_err(slick_json_failure(operation, &error.path, &error.message))
}

struct SlickJsonParser<'a> {
    bytes: &'a [u8],
    pos: usize,
}

impl<'a> SlickJsonParser<'a> {
    fn new(text: &'a str) -> SlickJsonParser<'a> {
        SlickJsonParser { bytes: text.as_bytes(), pos: 0 }
    }

    fn peek(&self) -> Option<u8> {
        self.bytes.get(self.pos).copied()
    }

    fn skip_ws(&mut self) {
        while self.pos < self.bytes.len() {
            match self.bytes[self.pos] {
                b' ' | b'\t' | b'\n' | b'\r' => self.pos += 1,
                _ => break,
            }
        }
    }

    // Parses exactly one JSON value and rejects trailing content, matching the
    // interpreter's single-value decoder.
    fn parse_all(&mut self, path: &str) -> Result<SlickJsonValue, SlickJsonError> {
        self.skip_ws();
        if self.pos >= self.bytes.len() {
            return Err(SlickJsonError { path: path.to_string(), message: "unexpected end of JSON input".to_string() });
        }
        let value = self.parse_value(path)?;
        self.skip_ws();
        if self.pos < self.bytes.len() {
            return Err(SlickJsonError { path: path.to_string(), message: "input contains more than one JSON value".to_string() });
        }
        Ok(value)
    }

    // Parses one JSON value with an explicit work stack so a document as
    // deep as encoding/json allows (10000) does not overflow the native
    // stack. Opening the 10001st container is the typed max-depth failure.
    fn parse_value(&mut self, path: &str) -> Result<SlickJsonValue, SlickJsonError> {
        let mut stack: Vec<SlickJsonParseFrame> = Vec::new();
        let mut path = path.to_string();
        loop {
            let finished = self.parse_start(&mut path, &mut stack)?;
            let Some(mut value) = finished else {
                continue;
            };
            loop {
                match stack.pop() {
                    None => return Ok(value),
                    Some(SlickJsonParseFrame::Array { path: array_path, mut elements }) => {
                        elements.push(value);
                        self.skip_ws();
                        match self.peek() {
                            Some(b',') => {
                                self.pos += 1;
                                path = format!("{}[{}]", array_path, elements.len());
                                stack.push(SlickJsonParseFrame::Array { path: array_path, elements });
                                break;
                            }
                            Some(b']') => {
                                self.pos += 1;
                                value = SlickJsonValue::Array(elements);
                            }
                            _ => return Err(SlickJsonError { path: array_path, message: "expected end of array".to_string() }),
                        }
                    }
                    Some(SlickJsonParseFrame::Object { path: object_path, mut entries, key }) => {
                        entries.push((key, value));
                        self.skip_ws();
                        match self.peek() {
                            Some(b',') => {
                                self.pos += 1;
                                let existing: Vec<String> = entries.iter().map(|(field, _)| field.clone()).collect();
                                let next_key = self.parse_object_key_against(&object_path, &existing)?;
                                self.skip_ws();
                                if self.peek() != Some(b':') {
                                    return Err(SlickJsonError { path: object_path, message: "expected end of object".to_string() });
                                }
                                self.pos += 1;
                                path = format!("{}.{}", object_path, next_key);
                                stack.push(SlickJsonParseFrame::Object { path: object_path, entries, key: next_key });
                                break;
                            }
                            Some(b'}') => {
                                self.pos += 1;
                                value = SlickJsonValue::Object(entries);
                            }
                            _ => return Err(SlickJsonError { path: object_path, message: "expected end of object".to_string() }),
                        }
                    }
                }
            }
        }
    }

    fn parse_object_key(&mut self, path: &str, entries: &[(String, SlickJsonValue)]) -> Result<String, SlickJsonError> {
        let existing: Vec<String> = entries.iter().map(|(key, _)| key.clone()).collect();
        self.parse_object_key_against(path, &existing)
    }

    fn parse_object_key_against(&mut self, path: &str, existing: &[String]) -> Result<String, SlickJsonError> {
        self.skip_ws();
        if self.peek() != Some(b'"') {
            return Err(SlickJsonError { path: path.to_string(), message: "object key must be a string".to_string() });
        }
        let key = self.parse_string(path)?;
        if existing.iter().any(|found| found == &key) {
            return Err(SlickJsonError { path: format!("{}.{}", path, key), message: "duplicate object key".to_string() });
        }
        Ok(key)
    }

    fn parse_start(&mut self, path: &mut String, stack: &mut Vec<SlickJsonParseFrame>) -> Result<Option<SlickJsonValue>, SlickJsonError> {
        self.skip_ws();
        let byte = match self.peek() {
            None => return Err(SlickJsonError { path: path.clone(), message: "unexpected end of JSON input".to_string() }),
            Some(byte) => byte,
        };
        match byte {
            b'[' => {
                if stack.len() >= SLICK_JSON_MAX_DEPTH {
                    return Err(SlickJsonError { path: path.clone(), message: "exceeded max depth".to_string() });
                }
                self.pos += 1;
                self.skip_ws();
                if self.peek() == Some(b']') {
                    self.pos += 1;
                    return Ok(Some(SlickJsonValue::Array(Vec::new())));
                }
                stack.push(SlickJsonParseFrame::Array { path: path.clone(), elements: Vec::new() });
                *path = format!("{}[0]", path);
                Ok(None)
            }
            b'{' => {
                if stack.len() >= SLICK_JSON_MAX_DEPTH {
                    return Err(SlickJsonError { path: path.clone(), message: "exceeded max depth".to_string() });
                }
                self.pos += 1;
                self.skip_ws();
                if self.peek() == Some(b'}') {
                    self.pos += 1;
                    return Ok(Some(SlickJsonValue::Object(Vec::new())));
                }
                let key = self.parse_object_key(path, &[])?;
                self.skip_ws();
                if self.peek() != Some(b':') {
                    return Err(SlickJsonError { path: path.clone(), message: "expected end of object".to_string() });
                }
                self.pos += 1;
                let field_path = format!("{}.{}", path, key);
                stack.push(SlickJsonParseFrame::Object { path: path.clone(), entries: Vec::new(), key });
                *path = field_path;
                Ok(None)
            }
            b'"' => {
                let text = self.parse_string(path)?;
                Ok(Some(SlickJsonValue::Str(text)))
            }
            b't' => self.parse_literal("true", SlickJsonValue::Bool(true), path).map(Some),
            b'f' => self.parse_literal("false", SlickJsonValue::Bool(false), path).map(Some),
            b'n' => self.parse_literal("null", SlickJsonValue::Null, path).map(Some),
            b'-' | b'0'..=b'9' => self.parse_number(path).map(Some),
            _ => Err(SlickJsonError { path: path.clone(), message: "invalid JSON token".to_string() }),
        }
    }

    fn parse_literal(&mut self, literal: &str, value: SlickJsonValue, path: &str) -> Result<SlickJsonValue, SlickJsonError> {
        let want = literal.as_bytes();
        if self.pos + want.len() <= self.bytes.len() && &self.bytes[self.pos..self.pos + want.len()] == want {
            self.pos += want.len();
            Ok(value)
        } else {
            Err(SlickJsonError { path: path.to_string(), message: "invalid JSON token".to_string() })
        }
    }

    // JSON number = [ minus ] int [ frac ] [ exp ]. int has no leading zeros;
    // frac and exp each require at least one digit. Incomplete forms at EOF
    // report unexpected EOF, matching encoding/json's Decoder.Token.
    fn parse_number(&mut self, path: &str) -> Result<SlickJsonValue, SlickJsonError> {
        let start = self.pos;
        if self.peek() == Some(b'-') {
            self.pos += 1;
            match self.peek() {
                None => return Err(SlickJsonError { path: path.to_string(), message: "unexpected EOF".to_string() }),
                Some(b'0'..=b'9') => {}
                Some(byte) => return Err(SlickJsonError { path: path.to_string(), message: slick_json_invalid_char(byte, "in numeric literal") }),
            }
        }
        match self.peek() {
            Some(b'0') => self.pos += 1,
            Some(b'1'..=b'9') => {
                self.pos += 1;
                while matches!(self.peek(), Some(b'0'..=b'9')) {
                    self.pos += 1;
                }
            }
            None => return Err(SlickJsonError { path: path.to_string(), message: "unexpected EOF".to_string() }),
            Some(byte) => return Err(SlickJsonError { path: path.to_string(), message: slick_json_invalid_char(byte, "in numeric literal") }),
        }
        if self.peek() == Some(b'.') {
            self.pos += 1;
            if !matches!(self.peek(), Some(b'0'..=b'9')) {
                return match self.peek() {
                    None => Err(SlickJsonError { path: path.to_string(), message: "unexpected EOF".to_string() }),
                    Some(byte) => Err(SlickJsonError { path: path.to_string(), message: slick_json_invalid_char(byte, "after decimal point in numeric literal") }),
                };
            }
            while matches!(self.peek(), Some(b'0'..=b'9')) {
                self.pos += 1;
            }
        }
        if matches!(self.peek(), Some(b'e' | b'E')) {
            self.pos += 1;
            if matches!(self.peek(), Some(b'+' | b'-')) {
                self.pos += 1;
            }
            if !matches!(self.peek(), Some(b'0'..=b'9')) {
                return match self.peek() {
                    None => Err(SlickJsonError { path: path.to_string(), message: "unexpected EOF".to_string() }),
                    Some(byte) => Err(SlickJsonError { path: path.to_string(), message: slick_json_invalid_char(byte, "in exponent of numeric literal") }),
                };
            }
            while matches!(self.peek(), Some(b'0'..=b'9')) {
                self.pos += 1;
            }
        }
        Ok(SlickJsonValue::Number(String::from_utf8_lossy(&self.bytes[start..self.pos]).into_owned()))
    }

    fn peek_u_escape(&self) -> Option<u32> {
        if self.bytes.get(self.pos) != Some(&b'\\') || self.bytes.get(self.pos + 1) != Some(&b'u') {
            return None;
        }
        if self.pos + 6 > self.bytes.len() {
            return None;
        }
        let mut value: u32 = 0;
        for offset in 2..6 {
            let byte = self.bytes[self.pos + offset];
            let digit = match byte {
                b'0'..=b'9' => (byte - b'0') as u32,
                b'a'..=b'f' => (byte - b'a' + 10) as u32,
                b'A'..=b'F' => (byte - b'A' + 10) as u32,
                _ => return None,
            };
            value = value.wrapping_mul(16).wrapping_add(digit);
        }
        Some(value)
    }

    fn parse_hex4(&mut self, path: &str) -> Result<u32, SlickJsonError> {
        let mut value: u32 = 0;
        for _ in 0..4 {
            let byte = match self.bytes.get(self.pos) {
                None => return Err(SlickJsonError { path: path.to_string(), message: "unexpected end of JSON input".to_string() }),
                Some(byte) => *byte,
            };
            let digit = match byte {
                b'0'..=b'9' => (byte - b'0') as u32,
                b'a'..=b'f' => (byte - b'a' + 10) as u32,
                b'A'..=b'F' => (byte - b'A' + 10) as u32,
                _ => return Err(SlickJsonError { path: path.to_string(), message: "invalid JSON token".to_string() }),
            };
            value = value.wrapping_mul(16).wrapping_add(digit);
            self.pos += 1;
        }
        Ok(value)
    }

    fn push_codepoint(bytes: &mut Vec<u8>, codepoint: u32) {
        if let Some(character) = char::from_u32(codepoint) {
            let mut buffer = [0u8; 4];
            let encoded = character.encode_utf8(&mut buffer);
            bytes.extend_from_slice(encoded.as_bytes());
        } else {
            bytes.extend_from_slice("\u{fffd}".as_bytes());
        }
    }

    // Parses a double-quoted JSON string, resolving escapes and surrogate
    // pairs the way Go's json.Decoder does.
    fn parse_string(&mut self, path: &str) -> Result<String, SlickJsonError> {
        self.pos += 1; // consume the opening quote
        let mut bytes: Vec<u8> = Vec::new();
        loop {
            let byte = match self.bytes.get(self.pos) {
                None => return Err(SlickJsonError { path: path.to_string(), message: "unexpected end of JSON input".to_string() }),
                Some(byte) => *byte,
            };
            match byte {
                b'"' => {
                    self.pos += 1;
                    break;
                }
                b'\\' => {
                    self.pos += 1;
                    let escape = match self.bytes.get(self.pos) {
                        None => return Err(SlickJsonError { path: path.to_string(), message: "unexpected end of JSON input".to_string() }),
                        Some(byte) => *byte,
                    };
                    self.pos += 1;
                    match escape {
                        b'"' => bytes.push(b'"'),
                        b'\\' => bytes.push(b'\\'),
                        b'/' => bytes.push(b'/'),
                        b'b' => bytes.push(0x08),
                        b'f' => bytes.push(0x0c),
                        b'n' => bytes.push(0x0a),
                        b'r' => bytes.push(0x0d),
                        b't' => bytes.push(0x09),
                        b'u' => {
                            let code = self.parse_hex4(path)?;
                            if (0xD800..=0xDBFF).contains(&code) {
                                if let Some(low) = self.peek_u_escape() {
                                    if (0xDC00..=0xDFFF).contains(&low) {
                                        self.pos += 6;
                                        let combined = 0x10000 + ((code - 0xD800) << 10) + (low - 0xDC00);
                                        SlickJsonParser::push_codepoint(&mut bytes, combined);
                                    } else {
                                        SlickJsonParser::push_codepoint(&mut bytes, 0xFFFD);
                                    }
                                } else {
                                    SlickJsonParser::push_codepoint(&mut bytes, 0xFFFD);
                                }
                            } else if (0xDC00..=0xDFFF).contains(&code) {
                                SlickJsonParser::push_codepoint(&mut bytes, 0xFFFD);
                            } else {
                                SlickJsonParser::push_codepoint(&mut bytes, code);
                            }
                        }
                        _ => return Err(SlickJsonError { path: path.to_string(), message: "invalid JSON token".to_string() }),
                    }
                }
                byte if byte < 0x20 => {
                    return Err(SlickJsonError { path: path.to_string(), message: slick_json_invalid_char(byte, "in string literal") });
                }
                _ => {
                    bytes.push(byte);
                    self.pos += 1;
                }
            }
        }
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

}

fn slick_json_object_field<'a>(fields: &'a [(&'static str, SlickValue)], name: &str) -> Option<&'a SlickValue> {
    fields.iter().find(|(field, _)| *field == name).map(|(_, value)| value)
}

// Formats a byte the way Go's encoding/json quoteChar does, then wraps it in
// the "invalid character ... <context>" syntax-error message.
fn slick_json_invalid_char(byte: u8, context: &str) -> String {
    let quoted = if byte == b'\'' {
        "'\\''".to_string()
    } else if byte == b'"' {
        "'\"'".to_string()
    } else {
        let escaped = match byte {
            0x07 => "\\a".to_string(),
            0x08 => "\\b".to_string(),
            0x09 => "\\t".to_string(),
            0x0a => "\\n".to_string(),
            0x0b => "\\v".to_string(),
            0x0c => "\\f".to_string(),
            0x0d => "\\r".to_string(),
            0x5c => "\\\\".to_string(),
            b if (0x20..0x7f).contains(&b) => (b as char).to_string(),
            b => format!("\\x{:02x}", b),
        };
        format!("'{}'", escaped)
    };
    format!("invalid character {} {}", quoted, context)
}

// Renders an f64 with the same shortest representation Go's encoding/json
// emits: 'f' for the common magnitude range, 'e' with a signed exponent
// outside it, and -0 for a negative zero.

fn slick_json_float(f: f64) -> String {
    if f == 0.0 {
        if f.is_sign_negative() {
            return "-0".to_string();
        }
        return "0".to_string();
    }
    let magnitude = f.abs();
    if magnitude < 1e-6 || magnitude >= 1e21 {
        let mut text = format!("{:e}", f);
        if let Some(index) = text.find('e') {
            let after = index + 1;
            if after < text.len() {
                let sign = text.as_bytes()[after];
                if sign != b'-' && sign != b'+' {
                    text.insert(after, '+');
                }
            }
        }
        text
    } else {
        format!("{}", f)
    }
}

// Writes a JSON string with the exact escapes Go's json.Marshal produces,
// including HTML escaping of <, >, and & and the line/paragraph separators.
fn slick_json_write_string(text: &str, out: &mut String) {
    out.push('"');
    for character in text.chars() {
        match character {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\u{08}' => out.push_str("\\b"),
            '\u{0c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '<' => out.push_str("\\u003c"),
            '>' => out.push_str("\\u003e"),
            '&' => out.push_str("\\u0026"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            other if (other as u32) < 0x20 => {
                out.push_str(&format!("\\u{:04x}", other as u32));
            }
            other => out.push(other),
        }
    }
    out.push('"');
}

// Encodes a value against a declared type into out. Returns true when a field
// was emitted and false when an optional field was absent and therefore
// omitted, matching the interpreter's optional-omission rule.
fn slick_json_encode_field(value: Option<&SlickValue>, typ: &SlickJsonType, path: &str, out: &mut String) -> Result<bool, SlickJsonError> {
    match slick_json_field_out(value, typ, path, 0)? {
        None => Ok(false),
        Some(node) => {
            slick_json_write_out(&node, out);
            Ok(true)
        }
    }
}

fn slick_json_encode_typed(value: &SlickValue, typ: &SlickJsonType, path: &str, out: &mut String) -> Result<(), SlickJsonError> {
    let node = slick_json_value_out(value, Some(typ), path, 0)?;
    slick_json_write_out(&node, out);
    Ok(())
}

fn slick_json_encode_class(fields: &[(&'static str, SlickValue)], descriptor: &'static SlickTypeDescriptor, path: &str, out: &mut String) -> Result<(), SlickJsonError> {
    let node = slick_json_class_out(fields, descriptor, path, 0)?;
    slick_json_write_out(&node, out);
    Ok(())
}

fn slick_json_encode_value(value: &SlickValue, path: &str, out: &mut String) -> Result<(), SlickJsonError> {
    let node = slick_json_value_out(value, None, path, 0)?;
    slick_json_write_out(&node, out);
    Ok(())
}

enum SlickJsonEncodeWork<'a> {
    Value { value: &'a SlickValue, typ: Option<SlickJsonType>, path: String, depth: usize },
    Ready(SlickJsonOut),
    FinishArray { count: usize },
    FinishObject { keys: Vec<String> },
}

fn slick_json_field_out(value: Option<&SlickValue>, typ: &SlickJsonType, path: &str, depth: usize) -> Result<Option<SlickJsonOut>, SlickJsonError> {
    match typ {
        SlickJsonType::Optional(base) => match value {
            None | Some(SlickValue::Optional(None)) | Some(SlickValue::Null) => Ok(None),
            Some(SlickValue::Optional(Some(inner))) => Ok(Some(slick_json_value_out(inner, Some(base), path, depth)?)),
            Some(other) => Ok(Some(slick_json_value_out(other, Some(base), path, depth)?)),
        },
        _ => match value {
            None => Ok(Some(slick_json_value_out(&SlickValue::Null, Some(typ), path, depth)?)),
            Some(other) => Ok(Some(slick_json_value_out(other, Some(typ), path, depth)?)),
        },
    }
}

fn slick_json_class_out(fields: &[(&'static str, SlickValue)], descriptor: &'static SlickTypeDescriptor, path: &str, depth: usize) -> Result<SlickJsonOut, SlickJsonError> {
    if depth >= SLICK_JSON_MAX_DEPTH {
        return Err(SlickJsonError { path: path.to_string(), message: "exceeded max depth".to_string() });
    }
    let object = SlickValue::Object { type_name: descriptor.name, fields: fields.to_vec(), resource: None, message: String::new() };
    slick_json_value_out(&object, Some(&SlickJsonType::Named(descriptor.name.to_string())), path, depth)
}

fn slick_json_value_out(value: &SlickValue, typ: Option<&SlickJsonType>, path: &str, depth: usize) -> Result<SlickJsonOut, SlickJsonError> {
    let mut stack: Vec<SlickJsonEncodeWork> = vec![SlickJsonEncodeWork::Value {
        value,
        typ: typ.cloned(),
        path: path.to_string(),
        depth,
    }];
    let mut done: Vec<SlickJsonOut> = Vec::new();
    while let Some(work) = stack.pop() {
        match work {
            SlickJsonEncodeWork::Ready(node) => done.push(node),
            SlickJsonEncodeWork::FinishArray { count } => {
                let items = done.split_off(done.len() - count);
                done.push(SlickJsonOut::Array(items));
            }
            SlickJsonEncodeWork::FinishObject { keys } => {
                let values = done.split_off(done.len() - keys.len());
                let mut pairs: Vec<(String, SlickJsonOut)> = keys.into_iter().zip(values).collect();
                pairs.sort_by(|left, right| left.0.cmp(&right.0));
                done.push(SlickJsonOut::Object(pairs));
            }
            SlickJsonEncodeWork::Value { value, typ, path, depth } => {
                slick_json_encode_step(value, typ, path, depth, &mut stack, &mut done)?;
            }
        }
    }
    match done.pop() {
        Some(node) => Ok(node),
        None => Err(SlickJsonError { path: path.to_string(), message: "unsupported JSON source type".to_string() }),
    }
}

fn slick_json_encode_step<'a>(
    value: &'a SlickValue,
    typ: Option<SlickJsonType>,
    path: String,
    depth: usize,
    stack: &mut Vec<SlickJsonEncodeWork<'a>>,
    done: &mut Vec<SlickJsonOut>,
) -> Result<(), SlickJsonError> {
    if let Some(typ) = typ {
        match typ {
            SlickJsonType::Optional(base) => {
                match value {
                    SlickValue::Optional(None) | SlickValue::Null => done.push(SlickJsonOut::Null),
                    SlickValue::Optional(Some(inner)) => stack.push(SlickJsonEncodeWork::Value { value: inner, typ: Some(*base), path, depth }),
                    other => stack.push(SlickJsonEncodeWork::Value { value: other, typ: Some(*base), path, depth }),
                }
                return Ok(());
            }
            SlickJsonType::Array(element) => {
                if let SlickValue::Array(elements) = value {
                    return slick_json_encode_array(elements, Some(*element), path, depth, stack);
                }
                return slick_json_encode_step(value, None, path, depth, stack, done);
            }
            SlickJsonType::Named(name) => match name.as_str() {
                "null" => {
                    done.push(SlickJsonOut::Null);
                    return Ok(());
                }
                "bool" => {
                    if let SlickValue::Bool(flag) = value {
                        done.push(SlickJsonOut::Bool(*flag));
                        return Ok(());
                    }
                    return slick_json_encode_step(value, None, path, depth, stack, done);
                }
                "string" => {
                    if let SlickValue::String(text) = value {
                        done.push(SlickJsonOut::Str(text.clone()));
                        return Ok(());
                    }
                    return slick_json_encode_step(value, None, path, depth, stack, done);
                }
                "int" => {
                    if let SlickValue::Int(number) = value {
                        done.push(SlickJsonOut::Number(number.to_string()));
                        return Ok(());
                    }
                    return slick_json_encode_step(value, None, path, depth, stack, done);
                }
                "float" => {
                    if let SlickValue::Float(number) = value {
                        if number.is_nan() || number.is_infinite() {
                            return Err(SlickJsonError { path, message: "non-finite float cannot be encoded as JSON".to_string() });
                        }
                        done.push(SlickJsonOut::Number(slick_json_float(*number)));
                        return Ok(());
                    }
                    return slick_json_encode_step(value, None, path, depth, stack, done);
                }
                _ => {
                    if let Some(descriptor) = slick_json_find_type(&name) {
                        if descriptor.kind == "class" {
                            if let SlickValue::Object { fields, .. } = value {
                                return slick_json_encode_class_step(fields, descriptor, path, depth, stack, done);
                            }
                            return slick_json_encode_step(value, None, path, depth, stack, done);
                        }
                    }
                    return Err(SlickJsonError { path, message: "unsupported JSON source type".to_string() });
                }
            },
            _ => return Err(SlickJsonError { path, message: "unsupported JSON source type".to_string() }),
        }
    }
    match value {
        SlickValue::Null => done.push(SlickJsonOut::Null),
        SlickValue::Bool(flag) => done.push(SlickJsonOut::Bool(*flag)),
        SlickValue::Int(number) => done.push(SlickJsonOut::Number(number.to_string())),
        SlickValue::Float(number) => {
            if number.is_nan() || number.is_infinite() {
                return Err(SlickJsonError { path, message: "non-finite float cannot be encoded as JSON".to_string() });
            }
            done.push(SlickJsonOut::Number(slick_json_float(*number)));
        }
        SlickValue::String(text) => done.push(SlickJsonOut::Str(text.clone())),
        SlickValue::Array(elements) => return slick_json_encode_array(elements, None, path, depth, stack),
        SlickValue::Optional(Some(inner)) => stack.push(SlickJsonEncodeWork::Value { value: inner, typ: None, path, depth }),
        SlickValue::Optional(None) => done.push(SlickJsonOut::Null),
        SlickValue::Object { type_name, fields, .. } => {
            if let Some(descriptor) = slick_json_find_type(type_name) {
                if descriptor.kind == "class" {
                    return slick_json_encode_class_step(fields, descriptor, path, depth, stack, done);
                }
            }
            return Err(SlickJsonError { path, message: "unsupported JSON source type".to_string() });
        }
        _ => return Err(SlickJsonError { path, message: "unsupported JSON source type".to_string() }),
    }
    Ok(())
}

fn slick_json_encode_array<'a>(
    elements: &'a [SlickValue],
    element_type: Option<SlickJsonType>,
    path: String,
    depth: usize,
    stack: &mut Vec<SlickJsonEncodeWork<'a>>,
) -> Result<(), SlickJsonError> {
    if depth >= SLICK_JSON_MAX_DEPTH {
        return Err(SlickJsonError { path, message: "exceeded max depth".to_string() });
    }
    stack.push(SlickJsonEncodeWork::FinishArray { count: elements.len() });
    for (index, item) in elements.iter().enumerate().rev() {
        stack.push(SlickJsonEncodeWork::Value {
            value: item,
            typ: element_type.clone(),
            path: format!("{}[{}]", path, index),
            depth: depth + 1,
        });
    }
    Ok(())
}

fn slick_json_encode_class_step<'a>(
    fields: &'a [(&'static str, SlickValue)],
    descriptor: &'static SlickTypeDescriptor,
    path: String,
    depth: usize,
    stack: &mut Vec<SlickJsonEncodeWork<'a>>,
    done: &mut Vec<SlickJsonOut>,
) -> Result<(), SlickJsonError> {
    if depth >= SLICK_JSON_MAX_DEPTH {
        return Err(SlickJsonError { path, message: "exceeded max depth".to_string() });
    }
    let mut keys: Vec<String> = Vec::new();
    let mut children: Vec<SlickJsonEncodeWork<'a>> = Vec::new();
    for field in descriptor.fields.iter() {
        let field_type = slick_json_parse_type(field.typ);
        let field_path = format!("{}.{}", path, field.json_name);
        match slick_json_encode_field_work(slick_json_object_field(fields, field.name), field_type, field_path, depth + 1, done)? {
            None => {}
            Some(work) => {
                keys.push(field.json_name.to_string());
                children.push(work);
            }
        }
    }
    stack.push(SlickJsonEncodeWork::FinishObject { keys });
    for work in children.into_iter().rev() {
        stack.push(work);
    }
    Ok(())
}

fn slick_json_encode_field_work<'a>(
    value: Option<&'a SlickValue>,
    typ: SlickJsonType,
    path: String,
    depth: usize,
    _done: &mut Vec<SlickJsonOut>,
) -> Result<Option<SlickJsonEncodeWork<'a>>, SlickJsonError> {
    match typ {
        SlickJsonType::Optional(base) => match value {
            None | Some(SlickValue::Optional(None)) | Some(SlickValue::Null) => Ok(None),
            Some(SlickValue::Optional(Some(inner))) => Ok(Some(SlickJsonEncodeWork::Value { value: inner, typ: Some(*base), path, depth })),
            Some(other) => Ok(Some(SlickJsonEncodeWork::Value { value: other, typ: Some(*base), path, depth })),
        },
        other => match value {
            None => Ok(Some(SlickJsonEncodeWork::Ready(SlickJsonOut::Null))),
            Some(found) => Ok(Some(SlickJsonEncodeWork::Value { value: found, typ: Some(other), path, depth })),
        },
    }
}

fn slick_json_write_out(root: &SlickJsonOut, out: &mut String) {
    enum Task<'a> {
        Node(&'a SlickJsonOut),
        Char(char),
        Key(&'a str),
    }
    let mut stack = vec![Task::Node(root)];
    while let Some(task) = stack.pop() {
        match task {
            Task::Char(character) => out.push(character),
            Task::Key(key) => slick_json_write_string(key, out),
            Task::Node(SlickJsonOut::Null) => out.push_str("null"),
            Task::Node(SlickJsonOut::Bool(flag)) => out.push_str(if *flag { "true" } else { "false" }),
            Task::Node(SlickJsonOut::Number(text)) => out.push_str(text),
            Task::Node(SlickJsonOut::Str(text)) => slick_json_write_string(text, out),
            Task::Node(SlickJsonOut::Array(items)) => {
                stack.push(Task::Char(']'));
                for (index, item) in items.iter().enumerate().rev() {
                    stack.push(Task::Node(item));
                    if index > 0 {
                        stack.push(Task::Char(','));
                    }
                }
                out.push('[');
            }
            Task::Node(SlickJsonOut::Object(pairs)) => {
                stack.push(Task::Char('}'));
                for (index, (key, value)) in pairs.iter().enumerate().rev() {
                    stack.push(Task::Node(value));
                    stack.push(Task::Char(':'));
                    stack.push(Task::Key(key));
                    if index > 0 {
                        stack.push(Task::Char(','));
                    }
                }
                out.push('{');
            }
        }
    }
}

fn slick_nat_json_encode(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let value = slick_arg(&args, 0);
    let mut out = String::new();
    match slick_json_encode_value(&value, "$", &mut out) {
        Ok(()) => slick_ok(slick_string(out)),
        Err(error) => slick_json_err("Encode", &error),
    }
}

// Infers the target class for an object whose type argument was not supplied
// by selecting the single class descriptor whose required json names are all
// present and whose json names cover every key, so a round trip still resolves
// to the same class the caller declared.
fn slick_json_infer_class(value: &SlickJsonValue) -> Option<&'static SlickTypeDescriptor> {
    let entries = match value {
        SlickJsonValue::Object(entries) => entries,
        _ => return None,
    };
    let keys: Vec<&str> = entries.iter().map(|(key, _)| key.as_str()).collect();
    let mut matched: Option<&'static SlickTypeDescriptor> = None;
    let mut count = 0usize;
    for descriptor in SLICK_TYPES.iter() {
        if descriptor.kind != "class" {
            continue;
        }
        let all: Vec<&str> = descriptor.fields.iter().map(|field| field.json_name).collect();
        let required: Vec<&str> = descriptor.fields.iter().filter(|field| !field.typ.ends_with('?')).map(|field| field.json_name).collect();
        let required_ok = required.iter().all(|name| keys.contains(name));
        let keys_ok = keys.iter().all(|key| all.contains(key));
        if required_ok && keys_ok {
            count += 1;
            matched = Some(descriptor);
        }
    }
    if count == 1 {
        matched
    } else {
        None
    }
}

fn slick_json_infer_value_type(value: &SlickJsonValue) -> SlickJsonType {
    match value {
        SlickJsonValue::Null => SlickJsonType::Named("null".to_string()),
        SlickJsonValue::Bool(_) => SlickJsonType::Named("bool".to_string()),
        SlickJsonValue::Str(_) => SlickJsonType::Named("string".to_string()),
        SlickJsonValue::Number(text) => {
            if text.contains('.') || text.contains('e') || text.contains('E') {
                SlickJsonType::Named("float".to_string())
            } else {
                SlickJsonType::Named("int".to_string())
            }
        }
        SlickJsonValue::Array(elements) => {
            let element = elements.first().map(slick_json_infer_value_type).unwrap_or(SlickJsonType::Named("null".to_string()));
            SlickJsonType::Array(Box::new(element))
        }
        SlickJsonValue::Object(_) => match slick_json_infer_class(value) {
            Some(descriptor) => SlickJsonType::Named(descriptor.name.to_string()),
            None => SlickJsonType::Named(String::new()),
        },
    }
}

fn slick_json_parse_int(value: SlickJsonValue, path: &str) -> Result<SlickValue, SlickJsonError> {
    let text = match value {
        SlickJsonValue::Number(text) => text,
        _ => return Err(SlickJsonError { path: path.to_string(), message: "expected JSON integer".to_string() }),
    };
    if text.contains('.') || text.contains('e') || text.contains('E') {
        return Err(SlickJsonError { path: path.to_string(), message: "expected JSON integer without fraction or exponent".to_string() });
    }
    match i64::from_str(&text) {
        Ok(number) => Ok(SlickValue::Int(number)),
        Err(_) => Err(SlickJsonError { path: path.to_string(), message: "integer out of int64 range".to_string() }),
    }
}

fn slick_json_parse_float(value: SlickJsonValue, path: &str) -> Result<SlickValue, SlickJsonError> {
    let text = match value {
        SlickJsonValue::Number(text) => text,
        _ => return Err(SlickJsonError { path: path.to_string(), message: "expected JSON number".to_string() }),
    };
    match f64::from_str(&text) {
        Ok(number) if !number.is_nan() && !number.is_infinite() => Ok(SlickValue::Float(number)),
        _ => Err(SlickJsonError { path: path.to_string(), message: "number out of float64 range".to_string() }),
    }
}

// Converts a parsed JSON value into a Slick value of the declared type,
// reporting the same structural-path failures the interpreter does. The walk
// uses an explicit stack so a 10000-deep tree does not overflow.
fn slick_json_convert(value: SlickJsonValue, typ: &SlickJsonType, path: &str) -> Result<SlickValue, SlickJsonError> {
    enum Work {
        Do { value: SlickJsonValue, typ: SlickJsonType, path: String, depth: usize },
        FinishArray { count: usize },
        FinishOptional,
        FinishClass { type_name: &'static str, field_names: Vec<&'static str>, descriptor: &'static SlickTypeDescriptor, path: String },
    }
    let mut stack = vec![Work::Do { value, typ: typ.clone(), path: path.to_string(), depth: 0 }];
    let mut done: Vec<SlickValue> = Vec::new();
    while let Some(work) = stack.pop() {
        match work {
            Work::FinishArray { count } => {
                let items = done.split_off(done.len() - count);
                done.push(SlickValue::Array(items));
            }
            Work::FinishOptional => {
                let inner = match done.pop() {
                    Some(inner) => inner,
                    None => return Err(SlickJsonError { path: path.to_string(), message: "unsupported JSON target type".to_string() }),
                };
                done.push(SlickValue::Optional(Some(Box::new(inner))));
            }
            Work::FinishClass { type_name, field_names, descriptor, path } => {
                let count = field_names.len();
                let converted = done.split_off(done.len() - count);
                let mut fields: Vec<(&'static str, SlickValue)> = field_names.into_iter().zip(converted).collect();
                let seen: Vec<&'static str> = fields.iter().map(|(name, _)| *name).collect();
                for field in descriptor.fields.iter() {
                    if !seen.iter().any(|name| *name == field.name) {
                        if field.typ.ends_with('?') {
                            fields.push((field.name, SlickValue::Optional(None)));
                        } else {
                            return Err(SlickJsonError { path: format!("{}.{}", path, field.json_name), message: "missing required field".to_string() });
                        }
                    }
                }
                done.push(SlickValue::Object { type_name, fields, resource: None, message: String::new() });
            }
            Work::Do { value, typ, path, depth } => {
                match typ {
                    SlickJsonType::Optional(base) => {
                        if matches!(value, SlickJsonValue::Null) {
                            done.push(SlickValue::Optional(None));
                        } else {
                            stack.push(Work::FinishOptional);
                            stack.push(Work::Do { value, typ: *base, path, depth });
                        }
                    }
                    SlickJsonType::Array(element) => {
                        if depth >= SLICK_JSON_MAX_DEPTH {
                            return Err(SlickJsonError { path, message: "exceeded max depth".to_string() });
                        }
                        let elements = match value {
                            SlickJsonValue::Array(elements) => elements,
                            _ => return Err(SlickJsonError { path, message: "expected JSON array".to_string() }),
                        };
                        stack.push(Work::FinishArray { count: elements.len() });
                        for (index, item) in elements.into_iter().enumerate().rev() {
                            stack.push(Work::Do {
                                value: item,
                                typ: (*element).clone(),
                                path: format!("{}[{}]", path, index),
                                depth: depth + 1,
                            });
                        }
                    }
                    SlickJsonType::Named(name) => match name.as_str() {
                        "null" => match value {
                            SlickJsonValue::Null => done.push(SlickValue::Null),
                            _ => return Err(SlickJsonError { path, message: "expected JSON null".to_string() }),
                        },
                        "bool" => match value {
                            SlickJsonValue::Bool(flag) => done.push(SlickValue::Bool(flag)),
                            _ => return Err(SlickJsonError { path, message: "expected JSON boolean".to_string() }),
                        },
                        "string" => match value {
                            SlickJsonValue::Str(text) => done.push(slick_string(text)),
                            _ => return Err(SlickJsonError { path, message: "expected JSON string".to_string() }),
                        },
                        "int" => done.push(slick_json_parse_int(value, &path)?),
                        "float" => done.push(slick_json_parse_float(value, &path)?),
                        _ => {
                            if depth >= SLICK_JSON_MAX_DEPTH {
                                return Err(SlickJsonError { path, message: "exceeded max depth".to_string() });
                            }
                            let descriptor = match slick_json_find_type(&name) {
                                Some(descriptor) if descriptor.kind == "class" => descriptor,
                                _ => return Err(SlickJsonError { path, message: "unsupported JSON target type".to_string() }),
                            };
                            let entries = match value {
                                SlickJsonValue::Object(entries) => entries,
                                _ => return Err(SlickJsonError { path, message: "expected JSON object".to_string() }),
                            };
                            let mut field_names: Vec<&'static str> = Vec::new();
                            let mut children: Vec<Work> = Vec::new();
                            for (key, field_value) in entries {
                                let field_descriptor = match descriptor.fields.iter().find(|field| field.json_name == key) {
                                    Some(field) => field,
                                    None => return Err(SlickJsonError { path: format!("{}.{}", path, key), message: "unknown field".to_string() }),
                                };
                                field_names.push(field_descriptor.name);
                                children.push(Work::Do {
                                    value: field_value,
                                    typ: slick_json_parse_type(field_descriptor.typ),
                                    path: format!("{}.{}", path, key),
                                    depth: depth + 1,
                                });
                            }
                            stack.push(Work::FinishClass { type_name: descriptor.name, field_names, descriptor, path });
                            for child in children.into_iter().rev() {
                                stack.push(child);
                            }
                        }
                    },
                    _ => return Err(SlickJsonError { path, message: "unsupported JSON target type".to_string() }),
                }
            }
        }
    }
    match done.pop() {
        Some(value) => Ok(value),
        None => Err(SlickJsonError { path: path.to_string(), message: "unsupported JSON target type".to_string() }),
    }
}

fn slick_nat_json_decode(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let text = match slick_arg_string(&args, 0) {
        Ok(text) => text,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    // Slick strings are always valid UTF-8, so this mirrors the interpreter's
    // check without ever failing for a real literal; malformed input is caught
    // by the parser instead.
    let mut parser = SlickJsonParser::new(&text);
    let value = match parser.parse_all("$") {
        Ok(value) => value,
        Err(error) => return slick_json_err("Decode", &error),
    };
    let typ = if args.len() > 1 {
        match slick_arg_string(&args, 1) {
            Ok(name) => slick_json_parse_type(&name),
            Err(failure) => return SlickOutcome::Throw(failure),
        }
    } else {
        slick_json_infer_value_type(&value)
    };
    match slick_json_convert(value, &typ, "$") {
        Ok(converted) => slick_ok(converted),
        Err(error) => slick_json_err("Decode", &error),
    }
}
`
