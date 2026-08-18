package compiler

var rustStdConvert = rustStdFamily{
	family: runtimeFamilyConvert,
	module: rustStdConvertModule,
	functions: map[runtimeOperationID]string{
		nativeStdConvertIntToString:   "slick_nat_int_to_string",
		nativeStdConvertFloatToString: "slick_nat_float_to_string",
		nativeStdConvertParseInt:      "slick_nat_parse_int",
		nativeStdConvertParseFloat:    "slick_nat_parse_float",
	},
}

// rustStdConvertModule implements std.convert. IntToString and FloatToString
// format primitives exactly as the interpreter and generated Go do; ParseInt
// and ParseFloat mirror strconv.ParseInt / strconv.ParseFloat, returning the
// documented std.convert.Failure for invalid or out-of-range input. Float
// formatting reuses the runtime slick_float renderer so a printed float matches
// strconv.FormatFloat(value, 'g', -1, 64) byte for byte.
const rustStdConvertModule = `fn slick_nat_int_to_string(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let value = match slick_arg_int(&args, 0) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    SlickOutcome::Value(slick_string(value.to_string()))
}

fn slick_nat_float_to_string(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let value = match slick_arg_float(&args, 0) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if value.is_nan() || value.is_infinite() {
        return SlickOutcome::Throw(SlickFailure::host("std.convert.FloatToString cannot format non-finite float".to_string()));
    }
    SlickOutcome::Value(slick_string(slick_float(value)))
}

fn slick_nat_parse_int(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let text = match slick_arg_string(&args, 0) { Ok(text) => text, Err(failure) => return SlickOutcome::Throw(failure) };
    match text.parse::<i64>() {
        Ok(value) => slick_ok(SlickValue::Int(value)),
        Err(error) => {
            // strconv.ParseInt reports ErrRange for an out-of-range magnitude and a
            // syntax error for everything else; i64::from_str distinguishes the same
            // two classes through its overflow kinds.
            let message = match error.kind() {
                std::num::IntErrorKind::PosOverflow | std::num::IntErrorKind::NegOverflow => "integer out of range",
                _ => "invalid base-10 integer",
            };
            slick_err(slick_object("std.convert.Failure", vec![
                ("Target", slick_string("int")),
                ("Message", slick_string(message)),
            ]))
        }
    }
}

fn slick_nat_parse_float(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let text = match slick_arg_string(&args, 0) { Ok(text) => text, Err(failure) => return SlickOutcome::Throw(failure) };
    match text.parse::<f64>() {
        Ok(value) => {
            if value.is_nan() || value.is_infinite() {
                // strconv.ParseFloat accepts Inf/Infinity/NaN (case-insensitive, with an
                // optional leading sign on Inf) and reports ErrRange when a numeric
                // mantissa overflows to infinity. Both classes are rejected here; the
                // message distinguishes a non-finite literal from an out-of-range
                // magnitude, matching the interpreter and generated Go.
                let message = if slick_convert_special_token(&text) {
                    "invalid floating-point number"
                } else {
                    "floating-point value out of range"
                };
                slick_err(slick_object("std.convert.Failure", vec![
                    ("Target", slick_string("float")),
                    ("Message", slick_string(message)),
                ]))
            } else {
                slick_ok(SlickValue::Float(value))
            }
        }
        Err(_) => slick_err(slick_object("std.convert.Failure", vec![
            ("Target", slick_string("float")),
            ("Message", slick_string("invalid floating-point number")),
        ])),
    }
}

// slick_convert_special_token reports whether Text is one of the Inf/Infinity/NaN
// forms Go's strconv.ParseFloat accepts (case-insensitive, with an optional
// leading sign), so an overflowing numeric mantissa can be told apart from a
// non-finite literal. Both kinds are rejected, only the message differs.
fn slick_convert_special_token(text: &str) -> bool {
    let mut lowered = String::with_capacity(text.len());
    for character in text.chars() {
        lowered.push(character.to_ascii_lowercase());
    }
    let stripped = lowered.trim_start_matches(|character: char| character == '+' || character == '-');
    stripped == "inf" || stripped == "infinity" || stripped == "nan"
}
`
