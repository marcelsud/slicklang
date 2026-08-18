package compiler

var rustStdMath = rustStdFamily{
	family: runtimeFamilyMath,
	module: rustStdMathModule,
	functions: map[runtimeOperationID]string{
		nativeStdMathDivide:    "slick_nat_math_div",
		nativeStdMathRemainder: "slick_nat_math_rem",
	},
}

// rustStdMathModule implements std.math. Divide and Remainder perform checked
// truncating integer arithmetic, returning std.math.ArithmeticFailure for a
// zero divisor or the non-representable minimum-int / -1 quotient instead of
// letting host integer division trap. Rust integer division and remainder
// truncate toward zero and take the dividend's sign, matching Go.
const rustStdMathModule = `fn slick_nat_math_div(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let dividend = match slick_arg_int(&args, 0) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    let divisor = match slick_arg_int(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if divisor == 0 {
        return slick_err(slick_math_failure("Divide", "DivisionByZero", "division by zero"));
    }
    // Minimum-int / -1 is 2^63, which has no i64 representation.
    if dividend == i64::MIN && divisor == -1 {
        return slick_err(slick_math_failure("Divide", "Overflow", "integer division overflow"));
    }
    slick_ok(SlickValue::Int(dividend / divisor))
}

fn slick_nat_math_rem(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let dividend = match slick_arg_int(&args, 0) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    let divisor = match slick_arg_int(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if divisor == 0 {
        return slick_err(slick_math_failure("Remainder", "DivisionByZero", "division by zero"));
    }
    // Minimum-int % -1 is mathematically zero but can trap on platforms that
    // implement remainder through the same dividing instruction as division.
    if dividend == i64::MIN && divisor == -1 {
        return slick_ok(SlickValue::Int(0));
    }
    slick_ok(SlickValue::Int(dividend % divisor))
}

fn slick_math_failure(operation: &'static str, kind: &'static str, message: &'static str) -> SlickValue {
    slick_object("std.math.ArithmeticFailure", vec![
        ("Operation", slick_string(operation)),
        ("Kind", slick_string(kind)),
        ("Message", slick_string(message)),
    ])
}
`
