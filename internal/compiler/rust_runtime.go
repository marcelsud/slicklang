package compiler

const rustRuntimeModule = `use std::sync::{Arc, Mutex};
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread::JoinHandle;

#[derive(Clone)]
struct SlickCallable {
    target: &'static str,
    captures: Vec<SlickValue>,
}

#[derive(Clone)]
enum SlickValue {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    String(String),
    Bytes(Vec<u8>),
    Array(Vec<SlickValue>),
    Tuple(Vec<SlickValue>),
    Enumerate(Box<SlickValue>),
    Zip(Vec<SlickValue>),
    Range(i64, i64),
    Map(Vec<(SlickValue, SlickValue)>),
    Buffer(Arc<Mutex<Vec<SlickValue>>>),
    Optional(Option<Box<SlickValue>>),
    Result(bool, Box<SlickValue>),
    // The message slot carries a shorthand error constructor's text. It is
    // failure metadata, not a declared field: it never participates in field
    // access or structural equality.
    Object { type_name: &'static str, fields: Vec<(&'static str, SlickValue)>, resource: Option<u64>, message: String },
    Union { type_name: &'static str, variant: &'static str, tag: i32, fields: Vec<SlickValue> },
    Callable(SlickCallable),
}

#[derive(Clone)]
enum SlickFailureKind {
    Slick(SlickValue),
    Host(String),
    Cancelled,
}

#[derive(Clone)]
struct SlickFailure {
    kind: SlickFailureKind,
    suppressed: Vec<SlickFailure>,
}

impl SlickFailure {
    fn slick(value: SlickValue) -> Self {
        Self { kind: SlickFailureKind::Slick(value), suppressed: Vec::new() }
    }

    fn host(message: impl Into<String>) -> Self {
        Self { kind: SlickFailureKind::Host(message.into()), suppressed: Vec::new() }
    }

    fn cancelled() -> Self {
        Self { kind: SlickFailureKind::Cancelled, suppressed: Vec::new() }
    }

    fn suppress(mut self, failure: SlickFailure) -> Self {
        self.suppressed.push(failure);
        self
    }
}

enum SlickOutcome {
    Value(SlickValue),
    Return(SlickValue),
    Throw(SlickFailure),
    Break,
    Continue,
}

macro_rules! slick_value {
    ($expression:expr) => {
        match $expression {
            SlickOutcome::Value(value) => value,
            outcome => return outcome,
        }
    };
}

#[derive(Clone)]
struct SlickContext {
    cancellations: Vec<Arc<AtomicBool>>,
}

impl SlickContext {
    fn root() -> Self {
        Self { cancellations: Vec::new() }
    }

    fn child(&self, cancellation: Arc<AtomicBool>) -> Self {
        let mut cancellations = self.cancellations.clone();
        cancellations.push(cancellation);
        Self { cancellations }
    }

    fn cancelled(&self) -> bool {
        self.cancellations.iter().any(|flag| flag.load(Ordering::Acquire))
    }

    // Cleanup observes no cancellation: a cancelled scope still runs Close.
    fn without_cancel(&self) -> Self {
        Self { cancellations: Vec::new() }
    }
}

struct SlickTaskScope {
    cancellation: Arc<AtomicBool>,
    child_context: SlickContext,
    children: Vec<Option<JoinHandle<SlickOutcome>>>,
}

impl SlickTaskScope {
    fn new(parent: &SlickContext) -> Self {
        let cancellation = Arc::new(AtomicBool::new(false));
        Self {
            child_context: parent.child(cancellation.clone()),
            cancellation,
            children: Vec::new(),
        }
    }

    fn launch<F>(&mut self, work: F) -> Result<usize, SlickFailure>
    where
        F: FnOnce(SlickContext) -> SlickOutcome + Send + 'static,
    {
        let context = self.child_context.clone();
        let index = self.children.len();
        let handle = std::thread::Builder::new().spawn(move || work(context))
            .map_err(|failure| SlickFailure::host(format!("launch child task: {failure}")))?;
        self.children.push(Some(handle));
        Ok(index)
    }

    fn await_task(&mut self, index: usize) -> SlickOutcome {
        let Some(slot) = self.children.get_mut(index) else {
            return SlickOutcome::Throw(SlickFailure::host("unknown task"));
        };
        let Some(handle) = slot.take() else {
            return SlickOutcome::Throw(SlickFailure::host("task was already awaited"));
        };
        match handle.join() {
            Ok(outcome) => outcome,
            Err(_) => SlickOutcome::Throw(SlickFailure::host("panic in child task")),
        }
    }

    fn finish(mut self, mut primary: SlickOutcome) -> SlickOutcome {
        if self.children.iter().any(Option::is_some) {
            self.cancellation.store(true, Ordering::Release);
        }
        for slot in &mut self.children {
            let Some(handle) = slot.take() else { continue };
            let child = match handle.join() {
                Ok(outcome) => outcome,
                Err(_) => SlickOutcome::Throw(SlickFailure::host("panic in child task")),
            };
            if matches!(&child, SlickOutcome::Throw(failure) if matches!(failure.kind, SlickFailureKind::Cancelled)) {
                continue;
            }
            primary = slick_combine_outcomes(primary, child);
        }
        self.cancellation.store(true, Ordering::Release);
        primary
    }
}

fn slick_combine_outcomes(primary: SlickOutcome, secondary: SlickOutcome) -> SlickOutcome {
    let SlickOutcome::Throw(secondary_failure) = secondary else { return primary };
    match primary {
        SlickOutcome::Value(_) | SlickOutcome::Return(_) | SlickOutcome::Break | SlickOutcome::Continue => {
            SlickOutcome::Throw(secondary_failure)
        }
        SlickOutcome::Throw(primary_failure) => {
            SlickOutcome::Throw(primary_failure.suppress(secondary_failure))
        }
    }
}

fn slick_finish_function(outcome: SlickOutcome) -> SlickOutcome {
    match outcome {
        SlickOutcome::Return(value) => SlickOutcome::Value(value),
        SlickOutcome::Break => SlickOutcome::Throw(SlickFailure::host("break escaped its loop")),
        SlickOutcome::Continue => SlickOutcome::Throw(SlickFailure::host("continue escaped its loop")),
        outcome => outcome,
    }
}

fn slick_cancelled(context: &SlickContext) -> Option<SlickOutcome> {
    if context.cancelled() {
        Some(SlickOutcome::Throw(SlickFailure::cancelled()))
    } else {
        None
    }
}

fn slick_convert(outcome: SlickOutcome, conversion: &str) -> SlickOutcome {
    let SlickOutcome::Value(value) = outcome else { return outcome };
    match conversion {
        "" => SlickOutcome::Value(value),
        "optional_inject" => match value {
            SlickValue::Null => SlickOutcome::Value(SlickValue::Optional(None)),
            value => SlickOutcome::Value(SlickValue::Optional(Some(Box::new(value)))),
        },
        "optional_unwrap_proven" => match value {
            SlickValue::Optional(Some(value)) => SlickOutcome::Value(*value),
            SlickValue::Optional(None) | SlickValue::Null => {
                SlickOutcome::Throw(SlickFailure::host("proved Optional value is absent"))
            }
            value => SlickOutcome::Value(value),
        },
        _ => SlickOutcome::Throw(SlickFailure::host(format!("unknown storage conversion {conversion}"))),
    }
}

fn slick_truth(value: &SlickValue) -> Result<bool, SlickFailure> {
    match value {
        SlickValue::Bool(value) => Ok(*value),
        _ => Err(SlickFailure::host("condition is not bool")),
    }
}

fn slick_unary(operator: &str, value: SlickValue) -> SlickOutcome {
    match (operator, value) {
        ("!", SlickValue::Bool(value)) => SlickOutcome::Value(SlickValue::Bool(!value)),
        ("-", SlickValue::Int(value)) => SlickOutcome::Value(SlickValue::Int(value.wrapping_neg())),
        ("-", SlickValue::Float(value)) => SlickOutcome::Value(SlickValue::Float(-value)),
        _ => SlickOutcome::Throw(SlickFailure::host(format!("invalid unary operator {operator}"))),
    }
}

fn slick_binary(operator: &str, left: SlickValue, right: SlickValue) -> SlickOutcome {
    let value = match (operator, &left, &right) {
        ("==", _, _) => SlickValue::Bool(slick_equal(&left, &right)),
        ("!=", _, _) => SlickValue::Bool(!slick_equal(&left, &right)),
        ("+", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Int(left.wrapping_add(*right)),
        ("-", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Int(left.wrapping_sub(*right)),
        ("*", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Int(left.wrapping_mul(*right)),
        ("+", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Float(left + right),
        ("-", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Float(left - right),
        ("*", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Float(left * right),
        ("+", SlickValue::String(left), SlickValue::String(right)) => SlickValue::String(left.clone() + right),
        ("<", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Bool(left < right),
        ("<=", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Bool(left <= right),
        (">", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Bool(left > right),
        (">=", SlickValue::Int(left), SlickValue::Int(right)) => SlickValue::Bool(left >= right),
        ("<", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Bool(left < right),
        ("<=", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Bool(left <= right),
        (">", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Bool(left > right),
        (">=", SlickValue::Float(left), SlickValue::Float(right)) => SlickValue::Bool(left >= right),
        ("<", SlickValue::String(left), SlickValue::String(right)) => SlickValue::Bool(left < right),
        ("<=", SlickValue::String(left), SlickValue::String(right)) => SlickValue::Bool(left <= right),
        (">", SlickValue::String(left), SlickValue::String(right)) => SlickValue::Bool(left > right),
        (">=", SlickValue::String(left), SlickValue::String(right)) => SlickValue::Bool(left >= right),
        _ => return SlickOutcome::Throw(SlickFailure::host(format!("invalid binary operator {operator}"))),
    };
    SlickOutcome::Value(value)
}

fn slick_equal(left: &SlickValue, right: &SlickValue) -> bool {
    match (left, right) {
        (SlickValue::Null, SlickValue::Null) => true,
        (SlickValue::Bool(left), SlickValue::Bool(right)) => left == right,
        (SlickValue::Int(left), SlickValue::Int(right)) => left == right,
        (SlickValue::Float(left), SlickValue::Float(right)) => left == right,
        (SlickValue::String(left), SlickValue::String(right)) => left == right,
        (SlickValue::Bytes(left), SlickValue::Bytes(right)) => left == right,
        (SlickValue::Array(left), SlickValue::Array(right)) | (SlickValue::Tuple(left), SlickValue::Tuple(right)) => {
            slick_equal_values(left, right)
        }
        (SlickValue::Range(left_start, left_end), SlickValue::Range(right_start, right_end)) => {
            left_start == right_start && left_end == right_end
        }
        (SlickValue::Map(left), SlickValue::Map(right)) => {
            left.len() == right.len() && left.iter().zip(right).all(|((left_key, left_value), (right_key, right_value))| {
                slick_equal(left_key, right_key) && slick_equal(left_value, right_value)
            })
        }
        (SlickValue::Buffer(left), SlickValue::Buffer(right)) => Arc::ptr_eq(left, right),
        (SlickValue::Optional(left), SlickValue::Optional(right)) => match (left, right) {
            (None, None) => true,
            (Some(left), Some(right)) => slick_equal(left, right),
            _ => false,
        },
        (SlickValue::Optional(Some(left)), right) => slick_equal(left, right),
        (left, SlickValue::Optional(Some(right))) => slick_equal(left, right),
        (SlickValue::Result(left_ok, left), SlickValue::Result(right_ok, right)) => {
            left_ok == right_ok && slick_equal(left, right)
        }
        (
            SlickValue::Object { type_name: left_type, fields: left_fields, resource: left_resource, .. },
            SlickValue::Object { type_name: right_type, fields: right_fields, resource: right_resource, .. },
        ) => {
            if left_resource.is_some() || right_resource.is_some() {
                left_resource.is_some() && left_resource == right_resource
            } else {
                left_type == right_type && left_fields.len() == right_fields.len() && left_fields.iter().all(|(name, value)| {
                    right_fields.iter().find(|(other, _)| other == name).is_some_and(|(_, other)| slick_equal(value, other))
                })
            }
        }
        (
            SlickValue::Union { type_name: left_type, tag: left_tag, fields: left, .. },
            SlickValue::Union { type_name: right_type, tag: right_tag, fields: right, .. },
        ) => left_type == right_type && left_tag == right_tag && slick_equal_values(left, right),
        (SlickValue::Optional(None), SlickValue::Null) | (SlickValue::Null, SlickValue::Optional(None)) => true,
        _ => false,
    }
}

fn slick_equal_values(left: &[SlickValue], right: &[SlickValue]) -> bool {
    left.len() == right.len() && left.iter().zip(right).all(|(left, right)| slick_equal(left, right))
}

fn slick_map(entries: Vec<(SlickValue, SlickValue)>) -> SlickValue {
    let mut ordered: Vec<(SlickValue, SlickValue)> = Vec::new();
    for (key, value) in entries {
        if let Some((_, stored)) = ordered.iter_mut().find(|(stored, _)| slick_equal(stored, &key)) {
            *stored = value;
        } else {
            ordered.push((key, value));
        }
    }
    SlickValue::Map(ordered)
}

enum SlickIterator {
    Values(std::vec::IntoIter<SlickValue>),
    Range(std::ops::Range<i64>),
    Enumerate(usize, Box<SlickIterator>),
    Zip(Vec<SlickIterator>),
}

impl Iterator for SlickIterator {
    type Item = SlickValue;

    fn next(&mut self) -> Option<Self::Item> {
        match self {
            SlickIterator::Values(values) => values.next(),
            SlickIterator::Range(values) => values.next().map(SlickValue::Int),
            SlickIterator::Enumerate(index, values) => {
                let value = values.next()?;
                let current = *index;
                *index += 1;
                Some(SlickValue::Tuple(vec![SlickValue::Int(current as i64), value]))
            }
            SlickIterator::Zip(sources) => {
                let mut values = Vec::with_capacity(sources.len());
                for source in sources {
                    values.push(source.next()?);
                }
                Some(SlickValue::Tuple(values))
            }
        }
    }
}

fn slick_iter(value: SlickValue) -> Result<SlickIterator, SlickFailure> {
    match value {
        SlickValue::Array(values) | SlickValue::Tuple(values) => Ok(SlickIterator::Values(values.into_iter())),
        SlickValue::Range(start, end) => Ok(SlickIterator::Range(start..end.max(start))),
        SlickValue::Enumerate(value) => Ok(SlickIterator::Enumerate(0, Box::new(slick_iter(*value)?))),
        SlickValue::Zip(values) => {
            let mut sources = Vec::with_capacity(values.len());
            for value in values {
                sources.push(slick_iter(value)?);
            }
            Ok(SlickIterator::Zip(sources))
        }
        SlickValue::Map(entries) => Ok(SlickIterator::Values(entries.into_iter().map(|(key, value)| {
            SlickValue::Tuple(vec![key, value])
        }).collect::<Vec<_>>().into_iter())),
        _ => Err(SlickFailure::host("value is not iterable")),
    }
}

fn slick_optional(value: Option<SlickValue>) -> SlickValue {
    match value {
        Some(SlickValue::Optional(value)) => SlickValue::Optional(value),
        Some(value) => SlickValue::Optional(Some(Box::new(value))),
        None => SlickValue::Optional(None),
    }
}

fn slick_builtin_method(receiver: &SlickValue, method: &str, arguments: &[SlickValue]) -> Option<SlickOutcome> {
    let method = method.rsplit('.').next().unwrap_or(method);
    match (receiver, method) {
        (SlickValue::Array(values), "Length") => Some(SlickOutcome::Value(SlickValue::Int(values.len() as i64))),
        (SlickValue::Array(values), "Get") => {
            let index = match arguments.first() {
                Some(SlickValue::Int(index)) => *index,
                _ => return Some(SlickOutcome::Throw(SlickFailure::host("array index is not int"))),
            };
            let value = usize::try_from(index).ok().and_then(|index| values.get(index)).cloned();
            Some(SlickOutcome::Value(slick_optional(value)))
        }
        (SlickValue::Array(values), "Slice") => {
            let (start, end) = match (arguments.first(), arguments.get(1)) {
                (Some(SlickValue::Int(start)), Some(SlickValue::Int(end))) => (*start, *end),
                _ => return Some(SlickOutcome::Throw(SlickFailure::host("array slice bounds are not int"))),
            };
            let sliced = usize::try_from(start).ok().zip(usize::try_from(end).ok())
                .filter(|(start, end)| start <= end && *end <= values.len())
                .map(|(start, end)| values[start..end].to_vec());
            Some(SlickOutcome::Value(match sliced {
                Some(values) => SlickValue::Result(true, Box::new(SlickValue::Array(values))),
                None => SlickValue::Result(false, Box::new(SlickValue::Object {
                    type_name: "std.collections.BoundsFailure", fields: vec![], resource: None, message: String::new(),
                })),
            }))
        }
        (SlickValue::Map(entries), "Length") => Some(SlickOutcome::Value(SlickValue::Int(entries.len() as i64))),
        (SlickValue::Map(entries), "Get") => {
            let Some(key) = arguments.first() else {
                return Some(SlickOutcome::Throw(SlickFailure::host("map key is missing")));
            };
            let value = entries.iter().find(|(stored, _)| slick_equal(stored, key)).map(|(_, value)| value.clone());
            Some(SlickOutcome::Value(slick_optional(value)))
        }
        (SlickValue::Map(entries), "Contains") => {
            let Some(key) = arguments.first() else {
                return Some(SlickOutcome::Throw(SlickFailure::host("map key is missing")));
            };
            Some(SlickOutcome::Value(SlickValue::Bool(entries.iter().any(|(stored, _)| slick_equal(stored, key)))))
        }
        (SlickValue::Map(entries), "With") => {
            let (Some(key), Some(value)) = (arguments.first(), arguments.get(1)) else {
                return Some(SlickOutcome::Throw(SlickFailure::host("map update arguments are missing")));
            };
            let mut updated = entries.clone();
            if let Some((_, stored)) = updated.iter_mut().find(|(stored, _)| slick_equal(stored, key)) {
                *stored = value.clone();
            } else {
                updated.push((key.clone(), value.clone()));
            }
            Some(SlickOutcome::Value(SlickValue::Map(updated)))
        }
        (SlickValue::Map(entries), "Without") => {
            let Some(key) = arguments.first() else {
                return Some(SlickOutcome::Throw(SlickFailure::host("map key is missing")));
            };
            Some(SlickOutcome::Value(SlickValue::Map(entries.iter()
                .filter(|(stored, _)| !slick_equal(stored, key)).cloned().collect())))
        }
        _ => None,
    }
}
fn slick_field(value: &SlickValue, name: &str) -> Result<SlickValue, SlickFailure> {
    // Narrowing proves an optional receiver present before a field read is
    // allowed, so the payload is the real receiver.
    let receiver = match value {
        SlickValue::Optional(Some(payload)) => payload.as_ref(),
        receiver => receiver,
    };
    match receiver {
        SlickValue::Object { fields, .. } => fields.iter().find(|(field, _)| *field == name)
            .map(|(_, value)| value.clone())
            .ok_or_else(|| SlickFailure::host(format!("object has no field {name}"))),
        SlickValue::Optional(None) => Err(SlickFailure::host(format!("null has no field {name}"))),
        _ => Err(SlickFailure::host(format!("value has no field {name}"))),
    }
}

fn slick_path(mut value: SlickValue, path: &[&str]) -> SlickOutcome {
    for name in path {
        value = match slick_field(&value, name) {
            Ok(value) => value,
            Err(failure) => return SlickOutcome::Throw(failure),
        };
    }
    SlickOutcome::Value(value)
}


fn slick_tuple_item(value: &SlickValue, index: usize) -> SlickValue {
    match value {
        SlickValue::Tuple(values) => values.get(index).cloned().unwrap_or(SlickValue::Null),
        _ => SlickValue::Null,
    }
}

fn slick_failure_value(failure: &SlickFailure) -> SlickValue {
    match &failure.kind {
        SlickFailureKind::Slick(value) => value.clone(),
        _ => SlickValue::Null,
    }
}
fn slick_type_name(value: &SlickValue) -> &str {
    match value {
        SlickValue::Null => "null",
        SlickValue::Bool(_) => "bool",
        SlickValue::Int(_) => "int",
        SlickValue::Float(_) => "float",
        SlickValue::String(_) => "string",
        SlickValue::Bytes(_) => "bytes",
        SlickValue::Array(_) => "array",
        SlickValue::Tuple(_) => "tuple",
        SlickValue::Enumerate(_) | SlickValue::Zip(_) => "iterable",
        SlickValue::Range(_, _) => "range",
        SlickValue::Map(_) => "Map",
        SlickValue::Buffer(_) => "Buffer",
        SlickValue::Optional(_) => "Optional",
        SlickValue::Result(_, _) => "Result",
        SlickValue::Object { type_name, .. } | SlickValue::Union { type_name, .. } => type_name,
        SlickValue::Callable(_) => "callable",
    }
}

fn slick_float(value: f64) -> String {
    if value.is_nan() { return "NaN".to_string(); }
    if value == f64::INFINITY { return "+Inf".to_string(); }
    if value == f64::NEG_INFINITY { return "-Inf".to_string(); }
    let magnitude = value.abs();
    if value != 0.0 && (magnitude < 1e-4 || magnitude >= 1e6) {
        let text = format!("{value:e}");
        let Some((mantissa, exponent)) = text.split_once('e') else { return text };
        let exponent: i32 = exponent.parse().unwrap_or(0);
        if exponent < 0 {
            return format!("{mantissa}e-{negative:02}", negative = -exponent);
        }
        return format!("{mantissa}e+{exponent:02}");
    }
    format!("{value}")
}

fn slick_format(value: &SlickValue) -> String {
    match value {
        SlickValue::Null => String::new(),
        SlickValue::Bool(value) => value.to_string(),
        SlickValue::Int(value) => value.to_string(),
        SlickValue::Float(value) => slick_float(*value),
        SlickValue::String(value) => value.clone(),
        SlickValue::Bytes(value) => format!("bytes[{}]", value.len()),
        SlickValue::Array(values) => format!("[{}]", values.iter().map(slick_format).collect::<Vec<_>>().join(", ")),
        SlickValue::Tuple(values) => format!("({})", values.iter().map(slick_format).collect::<Vec<_>>().join(", ")),
        SlickValue::Enumerate(_) | SlickValue::Zip(_) => match slick_iter(value.clone()) {
            Ok(values) => format!("[{}]", values.map(|value| slick_format(&value)).collect::<Vec<_>>().join(", ")),
            Err(_) => "iterable".to_string(),
        },
        SlickValue::Range(start, end) => {
            let values = (*start..(*end).max(*start)).map(|value| value.to_string()).collect::<Vec<_>>();
            format!("[{}]", values.join(", "))
        }
        SlickValue::Map(entries) => format!("map {{{}}}", entries.iter().map(|(key, value)| {
            format!("{}: {}", slick_format(key), slick_format(value))
        }).collect::<Vec<_>>().join(", ")),
        SlickValue::Buffer(_) => "Buffer".to_string(),
        SlickValue::Optional(None) => String::new(),
        SlickValue::Optional(Some(value)) => slick_format(value),
        SlickValue::Result(ok, value) => format!("{}({})", if *ok { "Ok" } else { "Err" }, slick_format(value)),
        SlickValue::Object { type_name, .. } => (*type_name).to_string(),
        SlickValue::Union { variant, fields, .. } => {
            if fields.is_empty() {
                (*variant).to_string()
            } else {
                format!("{}({})", variant, fields.iter().map(slick_format).collect::<Vec<_>>().join(", "))
            }
        }
        SlickValue::Callable(_) => "<callable>".to_string(),
    }
}

fn slick_failure_text(failure: &SlickFailure) -> String {
    let primary = match &failure.kind {
        SlickFailureKind::Host(message) => message.clone(),
        SlickFailureKind::Cancelled => "task cancelled".to_string(),
        SlickFailureKind::Slick(value) => {
            let type_name = slick_type_name(value);
            let message = match value {
                SlickValue::String(message) => message.clone(),
                SlickValue::Object { fields, message, .. } => fields.iter()
                    .find(|(name, _)| *name == "Message" || *name == "message")
                    .map(|(_, value)| slick_format(value))
                    .filter(|message| !message.is_empty())
                    .unwrap_or_else(|| message.clone()),
                _ => String::new(),
            };
            if message.is_empty() { type_name.to_string() } else { format!("{type_name}: {message}") }
        }
    };
    if failure.suppressed.is_empty() {
        primary
    } else {
        format!("{} (suppressed: {})", primary, failure.suppressed.iter().map(slick_failure_text).collect::<Vec<_>>().join("; "))
    }
}

// A catch arm on the Error interface claims every Slick failure; a concrete
// error class claims only its own type.
fn slick_match_failure(failure: &SlickFailure, type_name: &str) -> bool {
    matches!(&failure.kind, SlickFailureKind::Slick(value)
        if type_name == "Error" || slick_type_name(value) == type_name)
}

fn slick_result_payload(value: SlickValue, want_ok: bool) -> Option<SlickValue> {
    match value {
        SlickValue::Result(ok, value) if ok == want_ok => Some(*value),
        _ => None,
    }
}

fn slick_union_payload(value: SlickValue, variant: &str) -> Option<Vec<SlickValue>> {
    match value {
        SlickValue::Union { variant: found, fields, .. } if found == variant => Some(fields),
        _ => None,
    }
}

struct SlickTypeDescriptor {
    name: &'static str,
    fields: &'static [&'static str],
    methods: &'static [&'static str],
    interfaces: &'static [&'static str],
    variants: &'static [(&'static str, i32)],
}

`
