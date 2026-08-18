package compiler

var bunStdUTF8 = bunStdFamily{
	family: runtimeFamilyUTF8,
	module: bunStdUTF8Module,
	functions: map[runtimeOperationID]string{
		nativeStdUTF8DecodeAt: "slickNatUTF8DecodeAt",
	},
}

// bunStdUTF8Module implements std.utf8. DecodeAt mirrors the interpreter,
// which applies Go's utf8.DecodeRune to the byte slice starting at Index: an
// out-of-range Index yields std.utf8.Failure{Index, "byte index out of range"},
// an invalid encoding yields std.utf8.Failure{Index, "invalid UTF-8 encoding"},
// and a valid sequence yields std.utf8.DecodedRune{Value, Width}. The decoder
// reproduces Go's accept-range tables so overlong sequences, surrogates, and
// values above U+10FFFF all decode to (RuneError, 1).
const bunStdUTF8Module = `export async function slickNatUTF8DecodeAt(context, args) {
  const bytes = slickArgBytes(args, 0);
  const index = slickArgInt(args, 1);
  const length = BigInt(bytes.length);
  if (index < 0n || index >= length) {
    return slickErr(slickStdObject("std.utf8.Failure", [
      ["Index", index],
      ["Message", "byte index out of range"],
    ]));
  }
  const decoded = slickUTF8DecodeRune(bytes, Number(index));
  if (decoded[0] === 0xFFFD && decoded[1] === 1) {
    return slickErr(slickStdObject("std.utf8.Failure", [
      ["Index", index],
      ["Message", "invalid UTF-8 encoding"],
    ]));
  }
  return slickOk(slickStdObject("std.utf8.DecodedRune", [
    ["Value", BigInt(decoded[0])],
    ["Width", BigInt(decoded[1])],
  ]));
}

function slickUTF8DecodeRune(bytes, offset) {
  const length = bytes.length - offset;
  if (length < 1) return [0xFFFD, 0];
  const lead = bytes[offset];
  if (lead < 0x80) return [lead, 1];
  if (lead < 0xC2) return [0xFFFD, 1];
  let size = 0;
  let lowest = 0x80;
  let highest = 0xBF;
  if (lead < 0xE0) {
    size = 2;
  } else if (lead < 0xF0) {
    size = 3;
    if (lead === 0xE0) lowest = 0xA0;
    else if (lead === 0xED) highest = 0x9F;
  } else if (lead < 0xF5) {
    size = 4;
    if (lead === 0xF0) lowest = 0x90;
    else if (lead === 0xF4) highest = 0x8F;
  } else {
    return [0xFFFD, 1];
  }
  if (length < size) return [0xFFFD, 1];
  const first = bytes[offset + 1];
  if (first < lowest || first > highest) return [0xFFFD, 1];
  if (size <= 2) return [((lead & 0x1F) << 6) | (first & 0x3F), 2];
  const second = bytes[offset + 2];
  if (second < 0x80 || second > 0xBF) return [0xFFFD, 1];
  if (size <= 3) return [((lead & 0x0F) << 12) | ((first & 0x3F) << 6) | (second & 0x3F), 3];
  const third = bytes[offset + 3];
  if (third < 0x80 || third > 0xBF) return [0xFFFD, 1];
  return [((lead & 0x07) << 18) | ((first & 0x3F) << 12) | ((second & 0x3F) << 6) | (third & 0x3F), 4];
}
`
