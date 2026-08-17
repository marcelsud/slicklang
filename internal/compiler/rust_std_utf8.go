package compiler

var rustStdUTF8 = rustStdFamily{
	family: runtimeFamilyUTF8,
	module: rustStdUTF8Module,
	functions: map[runtimeOperationID]string{
		nativeStdUTF8DecodeAt: "slick_nat_utf8_decode_at",
	},
}

// rustStdUTF8Module implements std.utf8. DecodeAt mirrors the interpreter,
// which applies Go's utf8.DecodeRune to the byte slice starting at Index: an
// out-of-range Index yields std.utf8.Failure{Index, "byte index out of range"},
// an invalid encoding yields std.utf8.Failure{Index, "invalid UTF-8 encoding"},
// and a valid sequence yields std.utf8.DecodedRune{Value, Width}. The decoder
// reproduces Go's accept-range tables so overlong sequences, surrogates, and
// values above U+10FFFF all decode to (RuneError, 1).
const rustStdUTF8Module = `fn slick_utf8_decode_rune(bytes: &[u8]) -> (u32, usize) {
    let length = bytes.len();
    if length < 1 {
        return (0xFFFD, 0);
    }
    let lead = bytes[0];
    if lead < 0x80 {
        return (lead as u32, 1);
    }
    // A lead byte below 0xC2 starts no valid sequence: 0x80-0xBF are stray
    // continuation bytes and 0xC0-0xC1 can only encode overlong one-byte runes.
    if lead < 0xC2 {
        return (0xFFFD, 1);
    }
    let (size, lowest, highest): (usize, u8, u8) = if lead < 0xE0 {
        (2, 0x80, 0xBF)
    } else if lead < 0xF0 {
        // E0 forbids the 0x80-0x9F second byte (overlong) and ED forbids
        // 0xA0-0xBF (surrogates), matching Go's acceptRanges table.
        if lead == 0xE0 {
            (3, 0xA0, 0xBF)
        } else if lead == 0xED {
            (3, 0x80, 0x9F)
        } else {
            (3, 0x80, 0xBF)
        }
    } else if lead < 0xF8 {
        // F0 forbids 0x80-0x8F (overlong) and F4 forbids 0x90-0xBF (above
        // U+10FFFF), matching Go's acceptRanges table.
        if lead == 0xF0 {
            (4, 0x90, 0xBF)
        } else if lead == 0xF4 {
            (4, 0x80, 0x8F)
        } else {
            (4, 0x80, 0xBF)
        }
    } else {
        // 0xF8 and above are not valid UTF-8 lead bytes.
        return (0xFFFD, 1);
    };
    if length < size {
        return (0xFFFD, 1);
    }
    let first = bytes[1];
    if first < lowest || first > highest {
        return (0xFFFD, 1);
    }
    if size <= 2 {
        return (((lead & 0x1F) as u32) << 6 | ((first & 0x3F) as u32), 2);
    }
    let second = bytes[2];
    if second < 0x80 || second > 0xBF {
        return (0xFFFD, 1);
    }
    if size <= 3 {
        return (((lead & 0x0F) as u32) << 12 | ((first & 0x3F) as u32) << 6 | ((second & 0x3F) as u32), 3);
    }
    let third = bytes[3];
    if third < 0x80 || third > 0xBF {
        return (0xFFFD, 1);
    }
    (((lead & 0x07) as u32) << 18 | ((first & 0x3F) as u32) << 12 | ((second & 0x3F) as u32) << 6 | ((third & 0x3F) as u32), 4)
}

fn slick_nat_utf8_decode_at(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let bytes = match slick_arg_bytes(&args, 0) { Ok(bytes) => bytes, Err(failure) => return SlickOutcome::Throw(failure) };
    let index = match slick_arg_int(&args, 1) { Ok(index) => index, Err(failure) => return SlickOutcome::Throw(failure) };
    let length = bytes.len() as i64;
    if index < 0 || index >= length {
        let failure = slick_object("std.utf8.Failure", vec![
            ("Index", SlickValue::Int(index)),
            ("Message", slick_string("byte index out of range")),
        ]);
        return slick_err(failure);
    }
    let (rune, width) = slick_utf8_decode_rune(&bytes[index as usize..]);
    if rune == 0xFFFD && width == 1 {
        let failure = slick_object("std.utf8.Failure", vec![
            ("Index", SlickValue::Int(index)),
            ("Message", slick_string("invalid UTF-8 encoding")),
        ]);
        return slick_err(failure);
    }
    let decoded = slick_object("std.utf8.DecodedRune", vec![
        ("Value", SlickValue::Int(rune as i64)),
        ("Width", SlickValue::Int(width as i64)),
    ]);
    slick_ok(decoded)
}
`
