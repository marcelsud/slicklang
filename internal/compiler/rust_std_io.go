package compiler

var rustStdIO = rustStdFamily{
	family: runtimeFamilyIO,
	module: rustStdIOModule,
	functions: map[runtimeOperationID]string{
		nativeStdIOReaderFromBytes: "slick_nat_io_reader_from_bytes",
		nativeStdIOWriterToBytes:   "slick_nat_io_writer_to_bytes",
		nativeStdIOReadAll:         "slick_nat_io_read_all",
		nativeStdIOCopy:            "slick_nat_io_copy",
		nativeStdIOReaderRead:      "slick_nat_io_reader_read",
		nativeStdIOReaderClose:     "slick_nat_io_reader_close",
		nativeStdIOWriterWrite:     "slick_nat_io_writer_write",
		nativeStdIOWriterBytes:     "slick_nat_io_writer_bytes",
		nativeStdIOWriterClose:     "slick_nat_io_writer_close",
	},
}

// rustStdIOModule implements std.io. Readers wrap an immutable byte snapshot
// and writers collect written bytes in memory. Close is a flag on the owned
// native state: a closed writer keeps its final bytes so Bytes still answers,
// while Read, Write, and a second Close report the documented std.io.Failure.
// An object literal with no resource reports the same failure. Bounds, the
// null-only-at-end-of-stream Read contract, the explicit Copy byte limit, the
// immutable Writer.Bytes snapshot, and the idempotent Close failure all match
// the interpreter and generated Go exactly.
const rustStdIOModule = `const SLICK_IO_CHUNK: i64 = 32768;

struct SlickIOReader {
    bytes: Vec<u8>,
    position: usize,
    closed: bool,
}

struct SlickIOWriter {
    buffer: Vec<u8>,
    closed: bool,
}

// slick_io_failure builds the documented std.io.Failure (Operation, Message),
// substituting "operation failed" for an empty message exactly like the
// interpreter's runtimeIOFailure.
fn slick_io_failure(operation: &str, message: &str) -> SlickValue {
    let text = if message.trim().is_empty() { "operation failed".to_string() } else { message.to_string() };
    slick_object("std.io.Failure", vec![
        ("Operation", SlickValue::String(operation.to_string())),
        ("Message", SlickValue::String(text)),
    ])
}

// slick_io_failure_message reads the Message field of a Failure object, falling
// back to the formatted value like runtimeIOFailureMessage.
fn slick_io_failure_message(value: &SlickValue) -> String {
    if let SlickValue::Object { fields, .. } = value {
        for (name, field) in fields {
            if *name == "Message" {
                return slick_format(field);
            }
        }
    }
    slick_format(value)
}

// slick_io_resource recovers the owned native handle from a Reader or Writer
// (or an optional wrapping one). An object literal has no state.
fn slick_io_resource(value: &SlickValue) -> Option<SlickResource> {
    slick_arg_resource(std::slice::from_ref(value), 0)
}

enum SlickIORead {
    Eof,
    Chunk(Vec<u8>),
    Failed(String),
}

enum SlickIOWrite {
    Ok,
    Failed(String),
    Thrown(SlickOutcome),
}

// slick_io_read_size mirrors boundedReadSize: request one byte past the
// remaining budget when it is below the chunk size so a reader that reaches the
// limit is detected, and cap at the chunk size otherwise.
fn slick_io_read_size(remaining: i64) -> i64 {
    if remaining < SLICK_IO_CHUNK { remaining + 1 } else { SLICK_IO_CHUNK }
}

// slick_io_reader_read_chunk reads one bounded chunk from a Reader. Native
// bytesReader state is read from the owned resource; any other receiver is a
// user-defined Reader implementation dispatched through the generated method
// table, matching the interpreter's callRuntimeIOMethod.
fn slick_io_reader_read_chunk(context: &SlickContext, reader: &SlickValue, max_bytes: i64) -> SlickIORead {
    if slick_type_name(reader) == "std.io.bytesReader" {
        let Some(resource) = slick_io_resource(reader) else {
            return SlickIORead::Failed("reader is closed".to_string());
        };
        let Some(outcome) = slick_resource_with_state::<SlickIOReader, SlickIORead>(&resource, |state| {
            slick_io_reader_read_state(state, max_bytes)
        }) else {
            return SlickIORead::Failed("reader is closed".to_string());
        };
        return outcome;
    }
    slick_io_dispatch_read(context, reader, max_bytes)
}

fn slick_io_reader_read_state(state: &mut SlickIOReader, max_bytes: i64) -> SlickIORead {
    if state.closed {
        return SlickIORead::Failed("reader is closed".to_string());
    }
    if max_bytes <= 0 {
        return SlickIORead::Failed("MaxBytes must be greater than zero".to_string());
    }
    let available = state.bytes.len().saturating_sub(state.position);
    if available == 0 {
        return SlickIORead::Eof;
    }
    let cap = if max_bytes > SLICK_IO_CHUNK { SLICK_IO_CHUNK } else { max_bytes };
    let count = (cap as usize).min(available);
    let chunk = state.bytes[state.position..state.position + count].to_vec();
    state.position += count;
    SlickIORead::Chunk(chunk)
}

fn slick_io_dispatch_read(context: &SlickContext, reader: &SlickValue, max_bytes: i64) -> SlickIORead {
    let outcome = slick_call_method(context, reader.clone(), "Read", vec![SlickValue::Int(max_bytes)]);
    match outcome {
        SlickOutcome::Value(value) => {
            let SlickValue::Result(ok, payload) = value else {
                return SlickIORead::Failed("Reader.Read returned a non-Result value".to_string());
            };
            if !ok {
                return SlickIORead::Failed(slick_io_failure_message(payload.as_ref()));
            }
            match *payload {
                SlickValue::Null => SlickIORead::Eof,
                SlickValue::Optional(None) => SlickIORead::Eof,
                SlickValue::Optional(Some(inner)) => match *inner {
                    SlickValue::Bytes(chunk) => {
                        if (chunk.len() as i64) > max_bytes {
                            SlickIORead::Failed("reader returned a chunk larger than MaxBytes".to_string())
                        } else {
                            SlickIORead::Chunk(chunk)
                        }
                    }
                    _ => SlickIORead::Failed("Reader.Read returned a non-bytes chunk".to_string()),
                },
                SlickValue::Bytes(chunk) => {
                    if (chunk.len() as i64) > max_bytes {
                        SlickIORead::Failed("reader returned a chunk larger than MaxBytes".to_string())
                    } else {
                        SlickIORead::Chunk(chunk)
                    }
                }
                _ => SlickIORead::Failed("Reader.Read returned a non-optional success value".to_string()),
            }
        }
        SlickOutcome::Throw(failure) => SlickIORead::Failed(slick_failure_text(&failure)),
        _ => SlickIORead::Failed("Reader.Read returned an unexpected outcome".to_string()),
    }
}

// slick_io_write_chunk writes one complete chunk to a Writer. Native
// BytesWriter state is appended directly; any other receiver is a user-defined
// Writer implementation dispatched through the generated method table.
fn slick_io_write_chunk(context: &SlickContext, writer: &SlickValue, data: Vec<u8>) -> SlickIOWrite {
    if slick_type_name(writer) == "std.io.BytesWriter" {
        let Some(resource) = slick_io_resource(writer) else {
            return SlickIOWrite::Failed("writer is closed".to_string());
        };
        let Some(outcome) = slick_resource_with_state::<SlickIOWriter, SlickIOWrite>(&resource, |state| {
            if state.closed {
                return SlickIOWrite::Failed("writer is closed".to_string());
            }
            if !data.is_empty() {
                state.buffer.extend_from_slice(&data);
            }
            SlickIOWrite::Ok
        }) else {
            return SlickIOWrite::Failed("writer is closed".to_string());
        };
        return outcome;
    }
    slick_io_dispatch_write(context, writer, data)
}


fn slick_io_dispatch_write(context: &SlickContext, writer: &SlickValue, data: Vec<u8>) -> SlickIOWrite {
    let outcome = slick_call_method(context, writer.clone(), "Write", vec![SlickValue::Bytes(data)]);
    match outcome {
        SlickOutcome::Value(value) => {
            let SlickValue::Result(ok, payload) = value else {
                return SlickIOWrite::Failed("Writer.Write returned a non-Result value".to_string());
            };
            if ok { SlickIOWrite::Ok } else { SlickIOWrite::Failed(slick_io_failure_message(payload.as_ref())) }
        }
        SlickOutcome::Throw(failure) => SlickIOWrite::Thrown(SlickOutcome::Throw(failure)),
        _ => SlickIOWrite::Failed("Writer.Write returned an unexpected outcome".to_string()),
    }
}

fn slick_nat_io_reader_from_bytes(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let value = match slick_arg_bytes(&args, 0) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    let resource = slick_resource_new(Box::new(SlickIOReader { bytes: value, position: 0, closed: false }));
    SlickOutcome::Value(slick_resource_object("std.io.bytesReader", vec![], resource))
}

fn slick_nat_io_writer_to_bytes(_context: &SlickContext, _args: Vec<SlickValue>) -> SlickOutcome {
    let resource = slick_resource_new(Box::new(SlickIOWriter { buffer: Vec::new(), closed: false }));
    SlickOutcome::Value(slick_resource_object("std.io.BytesWriter", vec![], resource))
}

fn slick_nat_io_reader_read(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let max_bytes = match slick_arg_int(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    let reader = slick_arg(&args, 0);
    match slick_io_reader_read_chunk(context, &reader, max_bytes) {
        SlickIORead::Eof => slick_ok(SlickValue::Optional(None)),
        SlickIORead::Chunk(chunk) => slick_ok(SlickValue::Optional(Some(Box::new(SlickValue::Bytes(chunk))))),
        SlickIORead::Failed(message) => slick_err(slick_io_failure("Read", &message)),
    }
}

fn slick_nat_io_reader_close(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let Some(resource) = slick_arg_resource(&args, 0) else {
        return SlickOutcome::Throw(SlickFailure::slick(slick_io_failure("Close", "reader is already closed")));
    };
    match slick_resource_with_state::<SlickIOReader, bool>(&resource, |state| {
        if state.closed {
            false
        } else {
            state.closed = true;
            true
        }
    }) {
        Some(true) => SlickOutcome::Value(SlickValue::Null),
        _ => SlickOutcome::Throw(SlickFailure::slick(slick_io_failure("Close", "reader is already closed"))),
    }
}

fn slick_nat_io_writer_write(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let data = match slick_arg_bytes(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    let writer = slick_arg(&args, 0);
    match slick_io_write_chunk(context, &writer, data) {
        SlickIOWrite::Ok => slick_ok(SlickValue::Null),
        SlickIOWrite::Failed(message) => slick_err(slick_io_failure("Write", &message)),
        SlickIOWrite::Thrown(outcome) => outcome,
    }
}

fn slick_nat_io_writer_bytes(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let Some(resource) = slick_arg_resource(&args, 0) else {
        return SlickOutcome::Throw(SlickFailure::host("resource does not expose bytes"));
    };
    match slick_resource_with_state::<SlickIOWriter, SlickValue>(&resource, |state| {
        SlickValue::Bytes(state.buffer.clone())
    }) {
        Some(value) => SlickOutcome::Value(value),
        None => SlickOutcome::Throw(SlickFailure::host("resource does not expose bytes")),
    }
}

fn slick_nat_io_writer_close(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let Some(resource) = slick_arg_resource(&args, 0) else {
        return SlickOutcome::Throw(SlickFailure::slick(slick_io_failure("Close", "writer is already closed")));
    };
    match slick_resource_with_state::<SlickIOWriter, bool>(&resource, |state| {
        if state.closed {
            false
        } else {
            state.closed = true;
            true
        }
    }) {
        Some(true) => SlickOutcome::Value(SlickValue::Null),
        _ => SlickOutcome::Throw(SlickFailure::slick(slick_io_failure("Close", "writer is already closed"))),
    }
}

fn slick_nat_io_read_all(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let reader = slick_arg(&args, 0);
    let max_bytes = match slick_arg_int(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if max_bytes < 0 {
        return slick_err(slick_io_failure("ReadAll", "MaxBytes must not be negative"));
    }
    let mut output: Vec<u8> = Vec::new();
    loop {
        if let Some(outcome) = slick_cancelled(context) { return outcome; }
        let request = slick_io_read_size(max_bytes - (output.len() as i64));
        match slick_io_reader_read_chunk(context, &reader, request) {
            SlickIORead::Failed(message) => return slick_err(slick_io_failure("ReadAll", &message)),
            SlickIORead::Eof => return slick_ok(SlickValue::Bytes(output)),
            SlickIORead::Chunk(chunk) => {
                if chunk.is_empty() {
                    return slick_err(slick_io_failure("ReadAll", "reader made no progress"));
                }
                if (chunk.len() as i64) > max_bytes - (output.len() as i64) {
                    return slick_err(slick_io_failure("ReadAll", "byte limit exceeded"));
                }
                output.extend_from_slice(&chunk);
            }
        }
    }
}

fn slick_nat_io_copy(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let reader = slick_arg(&args, 0);
    let writer = slick_arg(&args, 1);
    let max_bytes = match slick_arg_int(&args, 2) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if max_bytes < 0 {
        return slick_err(slick_io_failure("Copy", "MaxBytes must not be negative"));
    }
    let mut total: i64 = 0;
    loop {
        if let Some(outcome) = slick_cancelled(context) { return outcome; }
        let request = slick_io_read_size(max_bytes - total);
        match slick_io_reader_read_chunk(context, &reader, request) {
            SlickIORead::Failed(message) => return slick_err(slick_io_failure("Copy", &message)),
            SlickIORead::Eof => return slick_ok(SlickValue::Int(total)),
            SlickIORead::Chunk(chunk) => {
                if chunk.is_empty() {
                    return slick_err(slick_io_failure("Copy", "reader made no progress"));
                }
                let remaining = max_bytes - total;
                if (chunk.len() as i64) > remaining {
                    if remaining > 0 {
                        let prefix = chunk[..(remaining as usize)].to_vec();
                        match slick_io_write_chunk(context, &writer, prefix) {
                            SlickIOWrite::Ok => {}
                            SlickIOWrite::Failed(message) => return slick_err(slick_io_failure("Copy", &message)),
                            SlickIOWrite::Thrown(outcome) => return outcome,
                        }
                    }
                    return slick_err(slick_io_failure("Copy", "byte limit exceeded"));
                }
                let written = chunk.len() as i64;
                match slick_io_write_chunk(context, &writer, chunk) {
                    SlickIOWrite::Ok => {}
                    SlickIOWrite::Failed(message) => return slick_err(slick_io_failure("Copy", &message)),
                    SlickIOWrite::Thrown(outcome) => return outcome,
                }
                total += written;
            }
        }
    }
}`
