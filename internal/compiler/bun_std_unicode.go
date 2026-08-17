package compiler

import (
	"fmt"
	"strings"
	"unicode"
)

var bunStdUnicode = bunStdFamily{
	family: runtimeFamilyUnicode,
	module: bunUnicodeModule(),
	functions: map[runtimeOperationID]string{
		nativeStdUnicodeIsDigit:      "slickNatUnicodeIsDigit",
		nativeStdUnicodeIsLetter:     "slickNatUnicodeIsLetter",
		nativeStdUnicodeIsUpper:      "slickNatUnicodeIsUpper",
		nativeStdUnicodeIsWhitespace: "slickNatUnicodeIsWhitespace",
	},
}

// bunUnicodeModule implements std.unicode from Go's category tables so the
// predicates match the interpreter: IsDigit is Nd, IsUpper is Lu, IsLetter is
// L*, and IsWhitespace is White_Space. A non-scalar reports false.
func bunUnicodeModule() string {
	var module strings.Builder
	module.WriteString(`export async function slickNatUnicodeIsDigit(context, args) {
  return slickUnicodeHas(slickArgInt(args, 0), SLICK_UNICODE_ND);
}

export async function slickNatUnicodeIsLetter(context, args) {
  return slickUnicodeHas(slickArgInt(args, 0), SLICK_UNICODE_L);
}

export async function slickNatUnicodeIsUpper(context, args) {
  return slickUnicodeHas(slickArgInt(args, 0), SLICK_UNICODE_LU);
}

export async function slickNatUnicodeIsWhitespace(context, args) {
  return slickUnicodeHas(slickArgInt(args, 0), SLICK_UNICODE_SPACE);
}

function slickUnicodeHas(value, table) {
  if (value < 0n || value > 0x10FFFFn || value >= 0xD800n && value <= 0xDFFFn) return false;
  const code = Number(value);
  let low = 0;
  let high = table.length;
  while (low < high) {
    const mid = low + high >> 1;
    if (table[mid][0] <= code) low = mid + 1;
    else high = mid;
  }
  if (low === 0) return false;
  const range = table[low - 1];
  return code <= range[1] && (range[2] <= 1 || (code - range[0]) % range[2] === 0);
}

`)
	module.WriteString("const SLICK_UNICODE_ND = ")
	bunWriteUnicodeTable(&module, unicode.Digit)
	module.WriteString(";\nconst SLICK_UNICODE_LU = ")
	bunWriteUnicodeTable(&module, unicode.Upper)
	module.WriteString(";\nconst SLICK_UNICODE_L = ")
	bunWriteUnicodeTable(&module, unicode.Letter)
	module.WriteString(";\nconst SLICK_UNICODE_SPACE = ")
	bunWriteUnicodeTable(&module, unicode.White_Space)
	module.WriteString(";\n")
	return module.String()
}

func bunWriteUnicodeTable(module *strings.Builder, table *unicode.RangeTable) {
	module.WriteByte('[')
	first := true
	write := func(lo, hi, stride uint32) {
		if !first {
			module.WriteByte(',')
		}
		first = false
		fmt.Fprintf(module, "[%d,%d,%d]", lo, hi, stride)
	}
	for _, r := range table.R16 {
		write(uint32(r.Lo), uint32(r.Hi), uint32(r.Stride))
	}
	for _, r := range table.R32 {
		write(r.Lo, r.Hi, r.Stride)
	}
	module.WriteByte(']')
}
