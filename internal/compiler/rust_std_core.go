package compiler

// rustStdCoreModule holds the helpers every standard-library family shares:
// argument decoding, documented failure construction, owned native resources,
// and the compiler-owned environment overlay. It is linked whenever a program
// reaches any family.
const rustStdCoreModule = `use std::any::Any;
use std::collections::BTreeMap;

// A native resource is created once and then owned by the Slick values holding
// it, so its host state is released when the last alias is dropped and no
// process-wide table can grow without bound.
fn slick_resource_new(state: Box<dyn Any + Send>) -> SlickResource {
    SlickResource::new(state)
}

fn slick_resource_with_state<T: Any + Send, R>(resource: &SlickResource, work: impl FnOnce(&mut T) -> R) -> Option<R> {
    resource.with(work)
}

// std.env.Set and std.env.Unset record their effect in a compiler-owned overlay
// instead of mutating the host environment, which is unsound once runtime
// threads exist. Every compiler-owned read consults the overlay first, and a
// spawned child receives the overlay applied to its inherited environment.
static SLICK_ENVIRONMENT_OVERLAY: Mutex<Option<BTreeMap<String, Option<String>>>> = Mutex::new(None);

fn slick_environment_overlay<R>(work: impl FnOnce(&mut BTreeMap<String, Option<String>>) -> R) -> R {
    let mut overlay = SLICK_ENVIRONMENT_OVERLAY.lock().unwrap_or_else(|error| error.into_inner());
    work(overlay.get_or_insert_with(BTreeMap::new))
}

// slick_environment_read is the only environment read every family uses, so an
// overlay assignment is visible to text, filesystem, HTTP, and process code
// exactly as a host assignment is visible to the interpreter.
fn slick_environment_read(name: &str) -> Option<String> {
    if let Some(recorded) = slick_environment_overlay(|overlay| overlay.get(name).cloned()) {
        return recorded;
    }
    std::env::var(name).ok()
}

// slick_environment_changes reports only the compiler-owned Set and Unset
// entries, so a child process inherits the host environment untouched (including
// non-Unicode entries) and then has the overlay applied.
fn slick_environment_changes() -> Vec<(String, Option<String>)> {
    slick_environment_overlay(|overlay| overlay.iter().map(|(name, value)| (name.clone(), value.clone())).collect())
}

fn slick_object(type_name: &'static str, fields: Vec<(&'static str, SlickValue)>) -> SlickValue {
    SlickValue::Object { type_name, fields, resource: None, message: String::new() }
}

fn slick_resource_object(type_name: &'static str, fields: Vec<(&'static str, SlickValue)>, resource: SlickResource) -> SlickValue {
    SlickValue::Object { type_name, fields, resource: Some(resource), message: String::new() }
}

fn slick_string(value: impl Into<String>) -> SlickValue {
    SlickValue::String(value.into())
}

fn slick_ok(value: SlickValue) -> SlickOutcome {
    SlickOutcome::Value(SlickValue::Result(true, Box::new(value)))
}

fn slick_err(value: SlickValue) -> SlickOutcome {
    SlickOutcome::Value(SlickValue::Result(false, Box::new(value)))
}

fn slick_arg(args: &[SlickValue], index: usize) -> SlickValue {
    args.get(index).cloned().unwrap_or(SlickValue::Null)
}

fn slick_arg_string(args: &[SlickValue], index: usize) -> Result<String, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::String(value) => Ok(value),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not string", slick_type_name(&value)))),
    }
}

fn slick_arg_int(args: &[SlickValue], index: usize) -> Result<i64, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Int(value) => Ok(value),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not int", slick_type_name(&value)))),
    }
}

fn slick_arg_float(args: &[SlickValue], index: usize) -> Result<f64, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Float(value) => Ok(value),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not float", slick_type_name(&value)))),
    }
}

fn slick_arg_bool(args: &[SlickValue], index: usize) -> Result<bool, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Bool(value) => Ok(value),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not bool", slick_type_name(&value)))),
    }
}

fn slick_arg_bytes(args: &[SlickValue], index: usize) -> Result<Vec<u8>, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Bytes(value) => Ok(value),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not bytes", slick_type_name(&value)))),
    }
}

fn slick_arg_values(args: &[SlickValue], index: usize) -> Result<Vec<SlickValue>, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Array(values) | SlickValue::Tuple(values) => Ok(values),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not array", slick_type_name(&value)))),
    }
}

fn slick_arg_entries(args: &[SlickValue], index: usize) -> Result<Vec<(SlickValue, SlickValue)>, SlickFailure> {
    match slick_arg(args, index) {
        SlickValue::Map(entries) => Ok(entries),
        value => Err(SlickFailure::host(format!("standard-library argument {index} is {} and not Map", slick_type_name(&value)))),
    }
}

fn slick_arg_optional(args: &[SlickValue], index: usize) -> Option<SlickValue> {
    match slick_arg(args, index) {
        SlickValue::Optional(Some(value)) => Some(*value),
        SlickValue::Optional(None) | SlickValue::Null => None,
        value => Some(value),
    }
}

fn slick_arg_field(args: &[SlickValue], index: usize, name: &str) -> Result<SlickValue, SlickFailure> {
    slick_field(&slick_arg(args, index), name)
}

// A native resource method must survive an object literal of its class, whose
// state pointer is absent.
fn slick_arg_resource(args: &[SlickValue], index: usize) -> Option<SlickResource> {
    match slick_arg(args, index) {
        SlickValue::Object { resource, .. } => resource,
        SlickValue::Optional(Some(value)) => match *value {
            SlickValue::Object { resource, .. } => resource,
            _ => None,
        },
        _ => None,
    }
}
`
