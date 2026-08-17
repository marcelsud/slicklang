package compiler

var bunStdIO = bunStdFamily{
	family: runtimeFamilyIO,
	module: bunStdIOModule,
	functions: map[runtimeOperationID]string{
		nativeStdIOReaderFromBytes: "slickNatIOReaderFromBytes",
		nativeStdIOWriterToBytes:   "slickNatIOWriterToBytes",
		nativeStdIOReadAll:         "slickNatIOReadAll",
		nativeStdIOCopy:            "slickNatIOCopy",
		nativeStdIOReaderRead:      "slickNatIOReaderRead",
		nativeStdIOReaderClose:     "slickNatIOReaderClose",
		nativeStdIOWriterWrite:     "slickNatIOWriterWrite",
		nativeStdIOWriterBytes:     "slickNatIOWriterBytes",
		nativeStdIOWriterClose:     "slickNatIOWriterClose",
	},
}

// bunStdIOModule implements std.io. Readers wrap an immutable byte snapshot
// and writers collect written bytes in memory. Close is a flag on the owned
// native state: a closed writer keeps its final bytes so Bytes still answers,
// while Read, Write, and a second Close report the documented std.io.Failure.
// An object literal with no resource reports the same failure. Bounds, the
// null-only-at-end-of-stream Read contract, the explicit Copy byte limit, the
// immutable Writer.Bytes snapshot, and the idempotent Close failure all match
// the interpreter and generated Go exactly.
const bunStdIOModule = `const SLICK_IO_CHUNK = 32768n;

function slickIoFailure(operation, message) {
  const text = typeof message === "string" && message.trim().length !== 0 ? message : "operation failed";
  return slickStdObject("std.io.Failure", [
    ["Operation", operation],
    ["Message", text],
  ]);
}

function slickIoFailureMessage(value) {
  if (value instanceof SlickObject && value.fields.has("Message")) {
    return slickFormat(value.fields.get("Message"));
  }
  return slickFormat(value);
}

function slickIoReadSize(remaining) {
  return remaining < SLICK_IO_CHUNK ? remaining + 1n : SLICK_IO_CHUNK;
}

function slickIoConcat(chunks) {
  let total = 0;
  for (const chunk of chunks) total += chunk.length;
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

function slickIoCopyBytes(value) {
  return new Uint8Array(value);
}

function slickIoNativeRead(state, maxBytes) {
  if (state.closed) return { kind: "failed", message: "reader is closed" };
  if (maxBytes <= 0n) return { kind: "failed", message: "MaxBytes must be greater than zero" };
  const available = state.bytes.length - state.position;
  if (available === 0) return { kind: "eof" };
  const cap = maxBytes > SLICK_IO_CHUNK ? SLICK_IO_CHUNK : maxBytes;
  const count = Number(cap < BigInt(available) ? cap : BigInt(available));
  const chunk = slickIoCopyBytes(state.bytes.subarray(state.position, state.position + count));
  state.position += count;
  return { kind: "chunk", chunk };
}

function slickIoNativeWrite(state, data) {
  if (state.closed) return { kind: "failed", message: "writer is closed" };
  if (data.length !== 0) state.chunks.push(slickIoCopyBytes(data));
  return { kind: "ok" };
}

async function slickIoDispatchRead(context, reader, maxBytes) {
  try {
    const value = await slickCallMethod(context, reader, "Read", [maxBytes]);
    if (!(value instanceof SlickResult)) {
      return { kind: "failed", message: "Reader.Read returned a non-Result value" };
    }
    if (!value.ok) return { kind: "failed", message: slickIoFailureMessage(value.value) };
    const payload = value.value;
    if (payload === null) return { kind: "eof" };
    if (payload instanceof SlickOptional) {
      if (!payload.present) return { kind: "eof" };
      if (!(payload.value instanceof Uint8Array)) {
        return { kind: "failed", message: "Reader.Read returned a non-bytes chunk" };
      }
      if (BigInt(payload.value.length) > maxBytes) {
        return { kind: "failed", message: "reader returned a chunk larger than MaxBytes" };
      }
      return { kind: "chunk", chunk: slickIoCopyBytes(payload.value) };
    }
    if (payload instanceof Uint8Array) {
      if (BigInt(payload.length) > maxBytes) {
        return { kind: "failed", message: "reader returned a chunk larger than MaxBytes" };
      }
      return { kind: "chunk", chunk: slickIoCopyBytes(payload) };
    }
    return { kind: "failed", message: "Reader.Read returned a non-optional success value" };
  } catch (error) {
    if (error instanceof SlickFailure && error.kind === "cancelled") throw error;
    if (error instanceof SlickFailure) return { kind: "failed", message: slickFailureText(error) };
    return { kind: "failed", message: String(error) };
  }
}

async function slickIoDispatchWrite(context, writer, data) {
  try {
    const value = await slickCallMethod(context, writer, "Write", [data]);
    if (!(value instanceof SlickResult)) {
      return { kind: "failed", message: "Writer.Write returned a non-Result value" };
    }
    if (value.ok) return { kind: "ok" };
    return { kind: "failed", message: slickIoFailureMessage(value.value) };
  } catch (error) {
    if (error instanceof SlickFailure && error.kind === "cancelled") throw error;
    if (error instanceof SlickFailure) return { kind: "failed", message: slickFailureText(error) };
    return { kind: "failed", message: String(error) };
  }
}

async function slickIoReaderReadChunk(context, reader, maxBytes) {
  if (slickTypeName(reader) === "std.io.bytesReader") {
    const resource = slickArgResource([reader], 0);
    if (resource === null || resource.state == null) {
      return { kind: "failed", message: "reader is closed" };
    }
    return slickIoNativeRead(resource.state, maxBytes);
  }
  return slickIoDispatchRead(context, reader, maxBytes);
}

async function slickIoWriteChunk(context, writer, data) {
  if (slickTypeName(writer) === "std.io.BytesWriter") {
    const resource = slickArgResource([writer], 0);
    if (resource === null || resource.state == null) {
      return { kind: "failed", message: "writer is closed" };
    }
    return slickIoNativeWrite(resource.state, data);
  }
  return slickIoDispatchWrite(context, writer, data);
}

export async function slickNatIOReaderFromBytes(context, args) {
  const value = slickArgBytes(args, 0);
  return slickResourceObject("std.io.bytesReader", [], slickResourceNew({
    bytes: slickIoCopyBytes(value),
    position: 0,
    closed: false,
  }));
}

export async function slickNatIOWriterToBytes(context, args) {
  return slickResourceObject("std.io.BytesWriter", [], slickResourceNew({
    chunks: [],
    closed: false,
  }));
}

export async function slickNatIOReaderRead(context, args) {
  const reader = slickArg(args, 0);
  const maxBytes = slickArgInt(args, 1);
  const result = await slickIoReaderReadChunk(context, reader, maxBytes);
  if (result.kind === "eof") return slickOk(slickAbsent);
  if (result.kind === "chunk") return slickOk(slickOptional(result.chunk));
  return slickErr(slickIoFailure("Read", result.message));
}

export async function slickNatIOReaderClose(context, args) {
  const resource = slickArgResource(args, 0);
  if (resource === null || resource.state == null || resource.state.closed) {
    throw SlickFailure.slick(slickIoFailure("Close", "reader is already closed"));
  }
  resource.state.closed = true;
  return null;
}

export async function slickNatIOWriterWrite(context, args) {
  const writer = slickArg(args, 0);
  const data = slickArgBytes(args, 1);
  const result = await slickIoWriteChunk(context, writer, data);
  if (result.kind === "ok") return slickOk(null);
  return slickErr(slickIoFailure("Write", result.message));
}

export async function slickNatIOWriterBytes(context, args) {
  const resource = slickArgResource(args, 0);
  if (resource === null || resource.state == null || !Array.isArray(resource.state.chunks)) {
    throw SlickFailure.host("resource does not expose bytes");
  }
  return slickIoConcat(resource.state.chunks);
}

export async function slickNatIOWriterClose(context, args) {
  const resource = slickArgResource(args, 0);
  if (resource === null || resource.state == null || resource.state.closed) {
    throw SlickFailure.slick(slickIoFailure("Close", "writer is already closed"));
  }
  resource.state.closed = true;
  return null;
}

export async function slickNatIOReadAll(context, args) {
  const reader = slickArg(args, 0);
  const maxBytes = slickArgInt(args, 1);
  if (maxBytes < 0n) return slickErr(slickIoFailure("ReadAll", "MaxBytes must not be negative"));
  const output = [];
  let length = 0n;
  for (;;) {
    slickCheckCancelled(context);
    const result = await slickIoReaderReadChunk(context, reader, slickIoReadSize(maxBytes - length));
    if (result.kind === "failed") return slickErr(slickIoFailure("ReadAll", result.message));
    if (result.kind === "eof") return slickOk(slickIoConcat(output));
    if (result.chunk.length === 0) return slickErr(slickIoFailure("ReadAll", "reader made no progress"));
    if (BigInt(result.chunk.length) > maxBytes - length) {
      return slickErr(slickIoFailure("ReadAll", "byte limit exceeded"));
    }
    output.push(result.chunk);
    length += BigInt(result.chunk.length);
  }
}

export async function slickNatIOCopy(context, args) {
  const reader = slickArg(args, 0);
  const writer = slickArg(args, 1);
  const maxBytes = slickArgInt(args, 2);
  if (maxBytes < 0n) return slickErr(slickIoFailure("Copy", "MaxBytes must not be negative"));
  let total = 0n;
  for (;;) {
    slickCheckCancelled(context);
    const result = await slickIoReaderReadChunk(context, reader, slickIoReadSize(maxBytes - total));
    if (result.kind === "failed") return slickErr(slickIoFailure("Copy", result.message));
    if (result.kind === "eof") return slickOk(total);
    if (result.chunk.length === 0) return slickErr(slickIoFailure("Copy", "reader made no progress"));
    const remaining = maxBytes - total;
    if (BigInt(result.chunk.length) > remaining) {
      if (remaining > 0n) {
        const prefix = slickIoCopyBytes(result.chunk.subarray(0, Number(remaining)));
        const written = await slickIoWriteChunk(context, writer, prefix);
        if (written.kind === "failed") return slickErr(slickIoFailure("Copy", written.message));
      }
      return slickErr(slickIoFailure("Copy", "byte limit exceeded"));
    }
    const written = await slickIoWriteChunk(context, writer, result.chunk);
    if (written.kind === "failed") return slickErr(slickIoFailure("Copy", written.message));
    total += BigInt(result.chunk.length);
  }
}
`
