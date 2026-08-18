package compiler

var bunStdMath = bunStdFamily{
	family: runtimeFamilyMath,
	module: bunStdMathModule,
	functions: map[runtimeOperationID]string{
		nativeStdMathDivide:    "slickNatMathDivide",
		nativeStdMathRemainder: "slickNatMathRemainder",
	},
}

// bunStdMathModule implements std.math. Divide and Remainder perform checked
// truncating integer arithmetic, returning std.math.ArithmeticFailure for a
// zero divisor or the non-representable minimum-int / -1 quotient instead of
// letting host integer division trap. JavaScript BigInt division and remainder
// truncate toward zero and take the dividend's sign, matching Go.
const bunStdMathModule = `export async function slickNatMathDivide(context, args) {
  const dividend = slickArgInt(args, 0);
  const divisor = slickArgInt(args, 1);
  if (divisor === 0n) {
    return slickErr(slickMathFailure("Divide", "DivisionByZero", "division by zero"));
  }
  if (dividend === -9223372036854775808n && divisor === -1n) {
    return slickErr(slickMathFailure("Divide", "Overflow", "integer division overflow"));
  }
  return slickOk(dividend / divisor);
}

export async function slickNatMathRemainder(context, args) {
  const dividend = slickArgInt(args, 0);
  const divisor = slickArgInt(args, 1);
  if (divisor === 0n) {
    return slickErr(slickMathFailure("Remainder", "DivisionByZero", "division by zero"));
  }
  if (dividend === -9223372036854775808n && divisor === -1n) {
    return slickOk(0n);
  }
  return slickOk(dividend % divisor);
}

function slickMathFailure(operation, kind, message) {
  return slickStdObject("std.math.ArithmeticFailure", [
    ["Operation", operation],
    ["Kind", kind],
    ["Message", message],
  ]);
}
`
