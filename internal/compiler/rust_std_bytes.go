package compiler

var rustStdBytes = rustStdFamily{
	family: runtimeFamilyBytes,
	module: rustStdBytesModule,
	functions: map[runtimeOperationID]string{
		nativeStdBytesAt:         "slick_nat_bytes_at",
		nativeStdBytesConcat:     "slick_nat_bytes_concat",
		nativeStdBytesFromUtf8:   "slick_nat_bytes_from_utf8",
		nativeStdBytesFromValues: "slick_nat_bytes_from_values",
		nativeStdBytesLength:     "slick_nat_bytes_length",
		nativeStdBytesSlice:      "slick_nat_bytes_slice",
		nativeStdBytesToUtf8:     "slick_nat_bytes_to_utf8",
	},
}

// rustStdBytesModule implements std.bytes. Bytes are immutable SlickValue::Bytes
// values; byte offsets are exact, not rune-aware, matching the interpreter and
// generated Go.
const rustStdBytesModule = `fn slick_nat_bytes_from_utf8(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let text = match slick_arg_string(&args, 0) { Ok(text) => text, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(SlickValue::Bytes(text.into_bytes()))
}

fn slick_nat_bytes_to_utf8(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let bytes = match slick_arg_bytes(&args, 0) { Ok(bytes) => bytes, Err(failure) => return SlickOutcome::Throw(failure) };
    match String::from_utf8(bytes) {
        Ok(text) => slick_ok(slick_string(text)),
        Err(_) => slick_err(slick_object("std.bytes.Utf8Failure", vec![
            ("Message", slick_string("invalid UTF-8")),
        ])),
    }
}

fn slick_nat_bytes_length(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let bytes = match slick_arg_bytes(&args, 0) { Ok(bytes) => bytes, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(SlickValue::Int(bytes.len() as i64))
}

fn slick_nat_bytes_at(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let bytes = match slick_arg_bytes(&args, 0) { Ok(bytes) => bytes, Err(failure) => return SlickOutcome::Throw(failure) };
    let index = match slick_arg_int(&args, 1) { Ok(index) => index, Err(failure) => return SlickOutcome::Throw(failure) };
    let value = usize::try_from(index).ok().and_then(|index| bytes.get(index)).map(|byte| SlickValue::Int(*byte as i64));
    SlickOutcome::Value(slick_optional(value))
}

fn slick_nat_bytes_concat(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let values = match slick_arg_values(&args, 0) { Ok(values) => values, Err(failure) => return SlickOutcome::Throw(failure) };
    let total = values.iter().try_fold(0usize, |total, value| match value {
        SlickValue::Bytes(bytes) => total.checked_add(bytes.len()),
        _ => None,
    });
    let total = match total {
        Some(total) => total,
        None => return SlickOutcome::Throw(SlickFailure::host(format!(
            "std.bytes.Concat part is {} and not bytes", slick_type_name(values.iter().find(|value| !matches!(value, SlickValue::Bytes(_))).unwrap_or(&SlickValue::Null))))),
    };
    let mut joined = Vec::with_capacity(total);
    for value in values {
        match value {
            SlickValue::Bytes(bytes) => joined.extend_from_slice(&bytes),
            _ => {}
        }
    }
    SlickOutcome::Value(SlickValue::Bytes(joined))
}

fn slick_nat_bytes_slice(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let bytes = match slick_arg_bytes(&args, 0) { Ok(bytes) => bytes, Err(failure) => return SlickOutcome::Throw(failure) };
    let start = match slick_arg_int(&args, 1) { Ok(start) => start, Err(failure) => return SlickOutcome::Throw(failure) };
    let end = match slick_arg_int(&args, 2) { Ok(end) => end, Err(failure) => return SlickOutcome::Throw(failure) };
    let length = bytes.len() as i64;
    if start < 0 || end < start || end > length {
        return slick_err(slick_object("std.bytes.BoundsFailure", vec![
            ("Start", SlickValue::Int(start)),
            ("End", SlickValue::Int(end)),
            ("Length", SlickValue::Int(length)),
            ("Message", slick_string("slice bounds out of range")),
        ]));
    }
    let start = start as usize;
    let end = end as usize;
    slick_ok(SlickValue::Bytes(bytes[start..end].to_vec()))
}

fn slick_nat_bytes_from_values(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let values = match slick_arg_values(&args, 0) { Ok(values) => values, Err(failure) => return SlickOutcome::Throw(failure) };
    for (index, value) in values.iter().enumerate() {
        let number = match value {
            SlickValue::Int(number) => *number,
            _ => return SlickOutcome::Throw(SlickFailure::host(format!(
                "std.bytes.FromValues value {} is {} and not int", index, slick_type_name(value)))),
        };
        if number < 0 || number > 255 {
            return slick_err(slick_object("std.bytes.ValueFailure", vec![
                ("Index", SlickValue::Int(index as i64)),
                ("Value", SlickValue::Int(number)),
                ("Message", slick_string("byte value must be between 0 and 255")),
            ]));
        }
    }
    let mut bytes = Vec::with_capacity(values.len());
    for value in values {
        match value {
            SlickValue::Int(number) => bytes.push(number as u8),
            _ => {}
        }
    }
    slick_ok(SlickValue::Bytes(bytes))
}
`
