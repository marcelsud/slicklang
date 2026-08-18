package compiler

var bunStdBuffer = bunStdFamily{
	family: runtimeFamilyBuffer,
	module: bunStdBufferModule,
	functions: map[runtimeOperationID]string{
		nativeStdBufferFreeze: "slickNatBufferFreeze",
		nativeStdBufferGet:    "slickNatBufferGet",
		nativeStdBufferLength: "slickNatBufferLength",
		nativeStdBufferNew:    "slickNatBufferNew",
		nativeStdBufferPush:   "slickNatBufferPush",
		nativeStdBufferSet:    "slickNatBufferSet",
	},
}

// bunStdBufferModule implements std.buffer. A Buffer is a shared growable
// SlickBuffer handle: Push/Set mutate the cell in place so every alias
// observes the change, and Freeze copies the current values into an
// immutable Array snapshot, matching the interpreter.
const bunStdBufferModule = `export async function slickNatBufferNew(context, args) {
  return new SlickBuffer([]);
}

export async function slickNatBufferPush(context, args) {
  slickBufferArg(args, 0).values.push(slickArg(args, 1));
  return null;
}

export async function slickNatBufferGet(context, args) {
  const buffer = slickBufferArg(args, 0);
  const index = slickArgInt(args, 1);
  if (index < 0n || index >= BigInt(buffer.values.length)) return slickAbsent;
  return slickOptional(buffer.values[Number(index)]);
}

export async function slickNatBufferSet(context, args) {
  const buffer = slickBufferArg(args, 0);
  const index = slickArgInt(args, 1);
  const value = slickArg(args, 2);
  if (index < 0n || index >= BigInt(buffer.values.length)) {
    return slickErr(slickStdObject("std.collections.BoundsFailure", []));
  }
  buffer.values[Number(index)] = value;
  return slickOk(null);
}

export async function slickNatBufferLength(context, args) {
  return BigInt(slickBufferArg(args, 0).values.length);
}

export async function slickNatBufferFreeze(context, args) {
  return slickBufferArg(args, 0).values.slice();
}

function slickBufferArg(args, index) {
  const value = slickArg(args, index);
  if (!(value instanceof SlickBuffer)) {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not Buffer");
  }
  return value;
}
`
