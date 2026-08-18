package compiler

var bunStdBytes = bunStdFamily{
	family: runtimeFamilyBytes,
	module: bunStdBytesModule,
	functions: map[runtimeOperationID]string{
		nativeStdBytesAt:         "slickNatBytesAt",
		nativeStdBytesConcat:     "slickNatBytesConcat",
		nativeStdBytesFromUtf8:   "slickNatBytesFromUtf8",
		nativeStdBytesFromValues: "slickNatBytesFromValues",
		nativeStdBytesLength:     "slickNatBytesLength",
		nativeStdBytesSlice:      "slickNatBytesSlice",
		nativeStdBytesToUtf8:     "slickNatBytesToUtf8",
	},
}

// bunStdBytesModule implements std.bytes. Bytes are immutable Uint8Array
// values; byte offsets are exact, not rune-aware, matching the interpreter
// and generated Go.
const bunStdBytesModule = `export async function slickNatBytesFromUtf8(context, args) {
  return new TextEncoder().encode(slickArgString(args, 0));
}

export async function slickNatBytesToUtf8(context, args) {
  const bytes = slickArgBytes(args, 0);
  try {
    return slickOk(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes));
  } catch (error) {
    if (error instanceof TypeError) {
      return slickErr(slickStdObject("std.bytes.Utf8Failure", [
        ["Message", "invalid UTF-8"],
      ]));
    }
    throw SlickFailure.host(String(error && error.message ? error.message : error));
  }
}

export async function slickNatBytesLength(context, args) {
  return BigInt(slickArgBytes(args, 0).length);
}

export async function slickNatBytesAt(context, args) {
  const bytes = slickArgBytes(args, 0);
  const index = slickArgInt(args, 1);
  if (index < 0n || index >= BigInt(bytes.length)) return slickAbsent;
  return slickOptional(BigInt(bytes[Number(index)]));
}

export async function slickNatBytesConcat(context, args) {
  const values = slickArgValues(args, 0);
  let total = 0;
  for (const value of values) {
    if (!(value instanceof Uint8Array)) {
      throw SlickFailure.host("std.bytes.Concat part is " + slickTypeName(value) + " and not bytes");
    }
    total += value.length;
  }
  const joined = new Uint8Array(total);
  let offset = 0;
  for (const value of values) {
    joined.set(value, offset);
    offset += value.length;
  }
  return joined;
}

export async function slickNatBytesSlice(context, args) {
  const bytes = slickArgBytes(args, 0);
  const start = slickArgInt(args, 1);
  const end = slickArgInt(args, 2);
  const length = BigInt(bytes.length);
  if (start < 0n || end < start || end > length) {
    return slickErr(slickStdObject("std.bytes.BoundsFailure", [
      ["Start", start],
      ["End", end],
      ["Length", length],
      ["Message", "slice bounds out of range"],
    ]));
  }
  return slickOk(bytes.slice(Number(start), Number(end)));
}

export async function slickNatBytesFromValues(context, args) {
  const values = slickArgValues(args, 0);
  for (let index = 0; index < values.length; index += 1) {
    const value = values[index];
    if (typeof value !== "bigint") {
      throw SlickFailure.host("std.bytes.FromValues value " + index + " is " + slickTypeName(value) + " and not int");
    }
    if (value < 0n || value > 255n) {
      return slickErr(slickStdObject("std.bytes.ValueFailure", [
        ["Index", BigInt(index)],
        ["Value", value],
        ["Message", "byte value must be between 0 and 255"],
      ]));
    }
  }
  const bytes = new Uint8Array(values.length);
  for (let index = 0; index < values.length; index += 1) bytes[index] = Number(values[index]);
  return slickOk(bytes);
}
`
