package compiler

var rustStdBuffer = rustStdFamily{
	family: runtimeFamilyBuffer,
	module: rustStdBufferModule,
	functions: map[runtimeOperationID]string{
		nativeStdBufferFreeze: "slick_nat_buffer_freeze",
		nativeStdBufferGet:    "slick_nat_buffer_get",
		nativeStdBufferLength: "slick_nat_buffer_length",
		nativeStdBufferNew:    "slick_nat_buffer_new",
		nativeStdBufferPush:   "slick_nat_buffer_push",
		nativeStdBufferSet:    "slick_nat_buffer_set",
	},
}

// rustStdBufferModule implements std.buffer. A Buffer is a shared growable
// SlickValue::Buffer(Arc<Mutex<Vec<SlickValue>>>) handle: Push/Set mutate the
// cell in place so every alias observes the change, and Freeze copies the
// current values into an immutable Array snapshot, matching the interpreter.
const rustStdBufferModule = `fn slick_nat_buffer_new(_context: &SlickContext, _args: Vec<SlickValue>) -> SlickOutcome {
    SlickOutcome::Value(SlickValue::Buffer(std::sync::Arc::new(std::sync::Mutex::new(Vec::new()))))
}

fn slick_nat_buffer_push(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let buffer = match slick_buffer_arg_buffer(&args, 0) { Ok(buffer) => buffer, Err(failure) => return SlickOutcome::Throw(failure) };
    let value = slick_arg(&args, 1);
    let mut values = buffer.lock().unwrap_or_else(|error| error.into_inner());
    values.push(value);
    SlickOutcome::Value(SlickValue::Null)
}

fn slick_nat_buffer_get(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let buffer = match slick_buffer_arg_buffer(&args, 0) { Ok(buffer) => buffer, Err(failure) => return SlickOutcome::Throw(failure) };
    let index = match slick_arg_int(&args, 1) { Ok(index) => index, Err(failure) => return SlickOutcome::Throw(failure) };
    let values = buffer.lock().unwrap_or_else(|error| error.into_inner());
    let value = usize::try_from(index).ok().and_then(|index| values.get(index)).cloned();
    SlickOutcome::Value(slick_optional(value))
}

fn slick_nat_buffer_set(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let buffer = match slick_buffer_arg_buffer(&args, 0) { Ok(buffer) => buffer, Err(failure) => return SlickOutcome::Throw(failure) };
    let index = match slick_arg_int(&args, 1) { Ok(index) => index, Err(failure) => return SlickOutcome::Throw(failure) };
    let value = slick_arg(&args, 2);
    let mut values = buffer.lock().unwrap_or_else(|error| error.into_inner());
    match usize::try_from(index).ok().and_then(|index| values.get_mut(index)) {
        Some(slot) => {
            *slot = value;
            slick_ok(SlickValue::Null)
        }
        None => slick_err(slick_object("std.collections.BoundsFailure", vec![])),
    }
}

fn slick_nat_buffer_length(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let buffer = match slick_buffer_arg_buffer(&args, 0) { Ok(buffer) => buffer, Err(failure) => return SlickOutcome::Throw(failure) };
    let values = buffer.lock().unwrap_or_else(|error| error.into_inner());
    SlickOutcome::Value(SlickValue::Int(values.len() as i64))
}

fn slick_nat_buffer_freeze(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let buffer = match slick_buffer_arg_buffer(&args, 0) { Ok(buffer) => buffer, Err(failure) => return SlickOutcome::Throw(failure) };
    let values = buffer.lock().unwrap_or_else(|error| error.into_inner());
    SlickOutcome::Value(SlickValue::Array(values.clone()))
}

fn slick_buffer_arg_buffer(args: &[SlickValue], index: usize) -> Result<std::sync::Arc<std::sync::Mutex<Vec<SlickValue>>>, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Buffer(buffer) => Ok(buffer),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not Buffer", slick_type_name(&value)))),
    }
}
`
