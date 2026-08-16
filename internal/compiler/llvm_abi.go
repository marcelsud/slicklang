package compiler

// NativeABIVersion is the versioned Slick native ABI implemented by the LLVM
// backend and its C runtime. A mismatch between generated IR declarations and
// the linked runtime fails at build or link time.
const NativeABIVersion = 1

// LLVMMajorVersion is the pinned LLVM toolchain major version.
const LLVMMajorVersion = 18

// LLVM IR / C value layout
//
// Every Slick value is a 16-byte tagged word:
//
//	type slick_value { i32 kind; i32 flags; i64 bits }
//
// kind is one of the slickValue* constants. bits holds an immediate integer,
// float bits, boolean 0/1, or a pointer to a heap object. flags carries
// optional presence, result ok, and union tag where those live on the word
// itself (optional and result payloads stay on bits as a pointer when the
// payload is not an immediate).
//
// Optional presence is independent of a zero payload: a present 0, false, or
// "" is slickValueOptional with flags=1. An absent optional has flags=0 and
// a zero payload pointer.
//
// Result is slickValueResult: flags=1 is Ok, flags=0 is Err, bits points at a
// heap pair (success, failure) so both sides keep their static types.
//
// Calling convention
//
// User functions and methods have the C shape
//
//	slick_outcome fn(slick_ctx* ctx, slick_value* args)
//
// slick_outcome is { i32 code; slick_value value }. code is slickCodeOK on
// success, slickCodeThrow for a checked throw, slickCodeReturn for an early
// return carrying value, slickCodeBreak, slickCodeContinue, or
// slickCodeCancel. Host/panic-equivalent failures use slickCodeThrow with an
// error object. There is no C++ exception ABI and no landing pads.
//
// ctx carries cancellation and the current task scope. Native runtime calls
// receive the same ctx so blocking I/O can observe cancellation.
//
// Aggregates
//
// Class: heap object { i32 type_id; i32 field_count; slick_value fields[] }
// in sorted field-name order. Error classes set flags bit 0 on the value word
// and store an optional message in field slickMessage when constructed from
// Error("...").
//
// Interface: heap object { i32 type_id; slick_value receiver; fn* vtable }
// with one function pointer per interface method in sorted method-name order.
// Dispatch is the vtable slot; there is no name lookup at runtime.
//
// Union: heap object { i32 type_id; i32 tag; slick_value payloads[] }.
// tag is the 1-based variant discriminant. Tag 0 is never constructed.
// Recursive payloads are pointers, so a union may contain itself.
//
// Callable: heap object { fn* code; i32 capture_count; slick_value captures[] }.
// Captures are copied once when the lambda is created and are read-only.
// Named function values have capture_count 0.
//
// Collections
//
// string/bytes: heap { i64 len; u8 data[] } UTF-8 for strings.
// array/tuple: heap { i64 len; slick_value items[] }.
// map: ordered entries plus an index; With/Without copy on write.
// buffer: mutable heap array; Freeze snapshots.
// range/enumerate/zip: iterable objects with Len/Width/At.
//
// Resources
//
// Native resource classes store a host pointer in a reserved slot. Object
// literals leave that pointer null; every native method must survive that.
//
// Symbol names
//
// Generated functions use slick_fn_<hex of qualified name>.
// Generated methods use slick_method_<hex of owner\x00name>.
// Runtime symbols are slick_* and carry abi_version in slick_abi_version.

const (
	slickValueNull      = 0
	slickValueBool      = 1
	slickValueInt       = 2
	slickValueFloat     = 3
	slickValueString    = 4
	slickValueBytes     = 5
	slickValueArray     = 6
	slickValueTuple     = 7
	slickValueMap       = 8
	slickValueBuffer    = 9
	slickValueOptional  = 10
	slickValueResult    = 11
	slickValueClass     = 12
	slickValueUnion     = 13
	slickValueInterface = 14
	slickValueCallable  = 15
	slickValueIterable  = 16
	slickValueError     = 17

	slickCodeOK       = 0
	slickCodeThrow    = 1
	slickCodeReturn   = 2
	slickCodeBreak    = 3
	slickCodeContinue = 4
	slickCodeCancel   = 5
)

func llvmFunctionSymbol(qualified string) string {
	return "slick_fn_" + hexName(qualified)
}

func llvmMethodSymbol(owner, method string) string {
	return "slick_method_" + hexName(owner+"\x00"+method)
}

func hexName(name string) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(name)*2)
	for i := range name {
		out[i*2] = digits[name[i]>>4]
		out[i*2+1] = digits[name[i]&0xf]
	}
	return string(out)
}
