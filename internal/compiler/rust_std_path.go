package compiler

var rustStdPath = rustStdFamily{
	family: runtimeFamilyPath,
	module: rustStdPathModule,
	functions: map[runtimeOperationID]string{
		nativeStdPathBase:       "slick_nat_path_base",
		nativeStdPathClean:      "slick_nat_path_clean",
		nativeStdPathDirectory:  "slick_nat_path_dir",
		nativeStdPathExtension:  "slick_nat_path_ext",
		nativeStdPathIsAbsolute: "slick_nat_path_abs",
		nativeStdPathJoin:       "slick_nat_path_join",
	},
}

// rustStdPathModule implements std.path with Go path/filepath semantics on
// Linux: '/' is the only separator, volume names are empty, and Clean
// normalizes separators and dot segments. The byte-wise algorithms mirror the
// interpreter's filepath.Clean/Base/Dir/Ext/IsAbs/Join exactly, so observable
// output, dot-segment handling, and the empty-result "." match Go.
const rustStdPathModule = `fn slick_path_clean_bytes(path: &[u8]) -> Vec<u8> {
    if path.is_empty() {
        return vec![b'.'];
    }
    let rooted = path[0] == b'/';
    let n = path.len();
    let mut out: Vec<u8> = Vec::with_capacity(n + 1);
    let mut r = 0usize;
    let mut dotdot = 0usize;
    if rooted {
        out.push(b'/');
        r = 1;
        dotdot = 1;
    }
    while r < n {
        if path[r] == b'/' {
            // empty path element
            r += 1;
        } else if path[r] == b'.' && (r + 1 == n || path[r + 1] == b'/') {
            // . element
            r += 1;
        } else if path[r] == b'.' && r + 1 < n && path[r + 1] == b'.' && (r + 2 == n || path[r + 2] == b'/') {
            // .. element: remove to last separator
            r += 2;
            if out.len() > dotdot {
                // can backtrack
                let mut w = out.len() - 1;
                while w > dotdot && out[w] != b'/' {
                    w -= 1;
                }
                out.truncate(w);
            } else if !rooted {
                // cannot backtrack, and not rooted, so append .. element.
                if !out.is_empty() {
                    out.push(b'/');
                }
                out.push(b'.');
                out.push(b'.');
                dotdot = out.len();
            }
        } else {
            // real path element: add slash if needed
            if (rooted && out.len() != 1) || (!rooted && !out.is_empty()) {
                out.push(b'/');
            }
            while r < n && path[r] != b'/' {
                out.push(path[r]);
                r += 1;
            }
        }
    }
    if out.is_empty() {
        out.push(b'.');
    }
    out
}

fn slick_path_clean(path: &str) -> String {
    let cleaned = slick_path_clean_bytes(path.as_bytes());
    String::from_utf8(cleaned).unwrap_or_else(|_| path.to_string())
}

fn slick_path_base(path: &str) -> String {
    let bytes = path.as_bytes();
    if bytes.is_empty() {
        return ".".to_string();
    }
    // Strip trailing slashes.
    let mut end = bytes.len();
    while end > 0 && bytes[end - 1] == b'/' {
        end -= 1;
    }
    // Find the last element.
    let mut i = end as isize - 1;
    while i >= 0 && bytes[i as usize] != b'/' {
        i -= 1;
    }
    let start = (i + 1) as usize;
    if start >= end {
        // The path had only slashes.
        return "/".to_string();
    }
    match std::str::from_utf8(&bytes[start..end]) {
        Ok(value) => value.to_string(),
        Err(_) => path.to_string(),
    }
}

fn slick_path_dir(path: &str) -> String {
    let bytes = path.as_bytes();
    let mut i = bytes.len() as isize - 1;
    while i >= 0 && bytes[i as usize] != b'/' {
        i -= 1;
    }
    let end = (i + 1) as usize;
    let cleaned = slick_path_clean_bytes(&bytes[..end]);
    String::from_utf8(cleaned).unwrap_or_else(|_| path.to_string())
}

fn slick_path_ext(path: &str) -> String {
    let bytes = path.as_bytes();
    let mut i = bytes.len() as isize - 1;
    while i >= 0 && bytes[i as usize] != b'/' {
        if bytes[i as usize] == b'.' {
            return match std::str::from_utf8(&bytes[i as usize..]) {
                Ok(value) => value.to_string(),
                Err(_) => path.to_string(),
            };
        }
        i -= 1;
    }
    String::new()
}

fn slick_path_is_abs(path: &str) -> bool {
    path.as_bytes().first().copied() == Some(b'/')
}

fn slick_path_join(parts: &[String]) -> String {
    // filepath.Join: skip leading empty elements, join the rest with the
    // separator, then Clean. An all-empty list yields the empty string.
    let start = match parts.iter().position(|part| !part.is_empty()) {
        Some(index) => index,
        None => return String::new(),
    };
    let mut joined = String::new();
    for (offset, part) in parts[start..].iter().enumerate() {
        if offset > 0 {
            joined.push('/');
        }
        joined.push_str(part);
    }
    slick_path_clean(&joined)
}

fn slick_nat_path_clean(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(slick_string(slick_path_clean(&path)))
}

fn slick_nat_path_base(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(slick_string(slick_path_base(&path)))
}

fn slick_nat_path_dir(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(slick_string(slick_path_dir(&path)))
}

fn slick_nat_path_ext(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    let extension = slick_path_ext(&path);
    if extension.is_empty() {
        SlickOutcome::Value(SlickValue::Optional(None))
    } else {
        SlickOutcome::Value(SlickValue::Optional(Some(Box::new(slick_string(extension)))))
    }
}

fn slick_nat_path_abs(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(SlickValue::Bool(slick_path_is_abs(&path)))
}

fn slick_nat_path_join(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let values = match slick_arg_values(&args, 0) { Ok(values) => values, Err(failure) => return SlickOutcome::Throw(failure) };
    let mut parts = Vec::with_capacity(values.len());
    for value in values {
        match value {
            SlickValue::String(part) => parts.push(part),
            value => return SlickOutcome::Throw(SlickFailure::host(
                format!("std.path.Join part is {} and not string", slick_type_name(&value)))),
        }
    }
    SlickOutcome::Value(slick_string(slick_path_join(&parts)))
}
`
