package compiler

var bunStdConvert = bunStdFamily{
	family: runtimeFamilyConvert,
	module: bunStdConvertModule,
	functions: map[runtimeOperationID]string{
		nativeStdConvertIntToString:   "slickNatConvertIntToString",
		nativeStdConvertFloatToString: "slickNatConvertFloatToString",
		nativeStdConvertParseInt:      "slickNatConvertParseInt",
		nativeStdConvertParseFloat:    "slickNatConvertParseFloat",
	},
}

// bunStdConvertModule implements std.convert. IntToString and FloatToString
// format primitives exactly as the interpreter and generated Go do; ParseInt
// and ParseFloat mirror strconv.ParseInt / strconv.ParseFloat, returning the
// documented std.convert.Failure for invalid or out-of-range input. Float
// formatting reuses slickFormatFloat so a printed float matches
// strconv.FormatFloat(value, 'g', -1, 64).
const bunStdConvertModule = `export async function slickNatConvertIntToString(context, args) {
  return slickArgInt(args, 0).toString();
}

export async function slickNatConvertFloatToString(context, args) {
  const value = slickArgFloat(args, 0);
  if (!Number.isFinite(value)) {
    throw SlickFailure.host("std.convert.FloatToString cannot format non-finite float");
  }
  return slickFormatFloat(value);
}

export async function slickNatConvertParseInt(context, args) {
  const text = slickArgString(args, 0);
  if (!/^[+-]?\d+$/.test(text)) {
    return slickErr(slickConvertFailure("int", "invalid base-10 integer"));
  }
  const value = BigInt(text);
  if (value > 9223372036854775807n || value < -9223372036854775808n) {
    return slickErr(slickConvertFailure("int", "integer out of range"));
  }
  return slickOk(value);
}

export async function slickNatConvertParseFloat(context, args) {
  const text = slickArgString(args, 0);
  if (slickConvertSpecialToken(text)) {
    return slickErr(slickConvertFailure("float", "invalid floating-point number"));
  }
  const value = slickConvertParseFloatValue(text);
  if (value === undefined) {
    return slickErr(slickConvertFailure("float", "invalid floating-point number"));
  }
  if (!Number.isFinite(value)) {
    return slickErr(slickConvertFailure("float", "floating-point value out of range"));
  }
  return slickOk(value);
}

function slickConvertFailure(target, message) {
  return slickStdObject("std.convert.Failure", [["Target", target], ["Message", message]]);
}

function slickConvertSpecialToken(text) {
  let start = 0;
  if (text.startsWith("+") || text.startsWith("-")) start = 1;
  const token = text.slice(start).toLowerCase();
  return token === "inf" || token === "infinity" || token === "nan";
}

function slickConvertParseFloatValue(text) {
  if (text.includes("_")) {
    if (!slickConvertUnderscoreOK(text)) return undefined;
    text = text.split("_").join("");
  }
  if (/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(text)) return Number(text);
  const hex = text.match(/^([+-]?)0[xX]([0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)[pP]([+-]?\d+)$/);
  if (hex === null) return undefined;
  const sign = hex[1] === "-" ? -1 : 1;
  const dot = hex[2].indexOf(".");
  const digits = hex[2].replace(".", "");
  const fraction = dot < 0 ? 0 : hex[2].length - dot - 1;
  return slickConvertParseHexFloat(sign, digits, fraction, hex[3]);

}

function slickConvertBitsToFloat(bits) {
  const view = new DataView(new ArrayBuffer(8));
  view.setBigUint64(0, bits, true);
  return view.getFloat64(0, true);
}

function slickConvertParseHexFloat(sign, digits, fraction, pText) {
  let mantissa = 0n;
  for (let i = 0; i < digits.length; i += 1) {
    const c = digits[i].toLowerCase();
    const d = c >= "0" && c <= "9" ? BigInt(c.charCodeAt(0) - 48) : BigInt(c.charCodeAt(0) - 87);
    mantissa = (mantissa << 4n) | d;
  }
  if (mantissa === 0n) return sign < 0 ? -0 : 0;
  const expAdjust = BigInt(pText) - 4n * BigInt(fraction);
  const bitlen = mantissa.toString(2).length;
  let unbiasedExp = BigInt(bitlen - 1) + expAdjust;
  if (unbiasedExp > 1023n) return sign * Infinity;
  if (unbiasedExp < -1075n) return sign * 0;
  if (unbiasedExp >= -1022n) {
    const extra = bitlen - 53;
    let sig;
    if (extra > 0) {
      const shift = BigInt(extra);
      sig = mantissa >> shift;
      const guard = (mantissa >> (shift - 1n)) & 1n;
      const sticky = (mantissa & ((1n << (shift - 1n)) - 1n)) !== 0n;
      if (guard === 1n && (sticky || (sig & 1n) === 1n)) {
        sig += 1n;
        if (sig === (1n << 53n)) {
          sig >>= 1n;
          unbiasedExp += 1n;
          if (unbiasedExp > 1023n) return sign * Infinity;
        }
      }
    } else {
      sig = mantissa << BigInt(-extra);
    }
    const expBits = unbiasedExp + 1023n;
    const frac = sig & ((1n << 52n) - 1n);
    let bits = (expBits << 52n) | frac;
    if (sign < 0) bits |= (1n << 63n);
    return slickConvertBitsToFloat(bits);
  }
  const scale = expAdjust + 1074n;
  let sig;
  if (scale >= 0n) {
    sig = mantissa << scale;
  } else {
    const rshift = -scale;
    sig = mantissa >> rshift;
    const guard = (mantissa >> (rshift - 1n)) & 1n;
    const sticky = rshift > 1n && (mantissa & ((1n << (rshift - 1n)) - 1n)) !== 0n;
    if (guard === 1n && (sticky || (sig & 1n) === 1n)) sig += 1n;
  }
  if (sig >= (1n << 52n)) {
    let bits = 1n << 52n;
    if (sign < 0) bits |= (1n << 63n);
    return slickConvertBitsToFloat(bits);
  }
  if (sig === 0n) return sign * 0;
  let bits = sig;
  if (sign < 0) bits |= (1n << 63n);
  return slickConvertBitsToFloat(bits);
}

function slickConvertUnderscoreOK(text) {
  let i = 0;
  if (text.startsWith("+") || text.startsWith("-")) i = 1;
  let saw = "^";
  let hex = false;
  if (text.length - i >= 2 && text[i] === "0") {
    const base = text[i + 1];
    if (base === "x" || base === "X" || base === "b" || base === "B" || base === "o" || base === "O") {
      hex = base === "x" || base === "X";
      i += 2;
      saw = "0";
    }
  }
  for (; i < text.length; i++) {
    const c = text[i];
    const letter = c.toLowerCase();
    if (c >= "0" && c <= "9" || hex && letter >= "a" && letter <= "f") {
      saw = "0";
      continue;
    }
    if (c === "_") {
      if (saw !== "0") return false;
      saw = "_";
      continue;
    }
    if (saw === "_") return false;
    saw = "!";
  }
  return saw !== "_";
}
`
