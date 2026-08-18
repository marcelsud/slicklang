package compiler

var rustStdProcess = rustStdFamily{
	family: runtimeFamilyProcess,
	module: rustStdProcessModule,
	functions: map[runtimeOperationID]string{
		nativeStdProcessRun: "slick_nat_process_run",
	},
}

// rustStdProcessModule implements std.process.Run for the Rust backend. It
// mirrors the interpreter's runProcess: a direct exec with no shell, the exact
// argument vector, an inherited environment plus the compiler-owned env overlay,
// a working-directory check, a combined stdout/stderr capture bounded by
// MaxOutputBytes, and cancellation that signals and reaps the child before
// returning. Bare names search PATH like os/exec.LookPath, so a candidate that
// exists but is not executable is skipped and reports not-found rather than
// permission denied. A relative PATH hit is resolved against the parent cwd
// before the child starts, so a WorkingDirectory cannot change which file runs.
// Every failure is normalized into std.process.Failure
// (Operation, Program, Message); a child that exits nonzero still reports
// std.process.Completed. No operation panics (the release profile builds with
// panic=abort), so all decoding, locking, and arithmetic is checked or bounded.
// Only the Rust standard library is used.
const rustStdProcessModule = `struct SlickProcessCompleted {
    exit_code: i64,
    output: Vec<u8>,
    error_output: Vec<u8>,
}

struct SlickProcessFailure {
    operation: String,
    program: String,
    message: String,
}

struct SlickProcessCaptureData {
    limit: i64,
    total: i64,
    overflow: bool,
    output: Vec<u8>,
    error_output: Vec<u8>,
}

fn slick_process_failure(operation: &str, program: &str, message: String) -> SlickProcessFailure {
    SlickProcessFailure {
        operation: operation.to_string(),
        program: program.to_string(),
        message,
    }
}

// slick_process_capture_write accepts as many bytes as the remaining budget
// allows, sets overflow when a write exceeds it, and discards the excess. It
// never fails: a failing copy would stop draining the pipe and block the child
// forever, so the caller kills an overflowing child instead.
fn slick_process_capture_write(data: &mut SlickProcessCaptureData, bytes: &[u8], is_error: bool) {
    let remaining = data.limit - data.total;
    let mut accepted = bytes.len() as i64;
    if accepted > remaining {
        data.overflow = true;
        accepted = remaining;
    }
    if accepted > 0 {
        let end = accepted as usize;
        if is_error {
            data.error_output.extend_from_slice(&bytes[..end]);
        } else {
            data.output.extend_from_slice(&bytes[..end]);
        }
        data.total += accepted;
    }
}

// slick_process_drain copies one child stream into the shared capture until
// EOF, always draining the pipe so the child never blocks on a full buffer.
fn slick_process_drain(reader: &mut dyn std::io::Read, capture: &std::sync::Arc<std::sync::Mutex<SlickProcessCaptureData>>, is_error: bool) {
    use std::io::Read;
    let mut buffer = [0u8; 4096];
    loop {
        let count = match reader.read(&mut buffer) {
            Ok(0) => break,
            Ok(count) => count,
            Err(_) => break,
        };
        let mut data = capture.lock().unwrap_or_else(|error| error.into_inner());
        slick_process_capture_write(&mut data, &buffer[..count], is_error);
    }
}

// slick_process_perform runs program directly with the exact argument vector
// and mirrors the interpreter's runProcess step for step, including the
// failure Operation, Message, and precedence order.
fn slick_process_perform(context: &SlickContext, program: &str, arguments: &[String], working_directory: Option<&str>, max_output_bytes: i64) -> Result<SlickProcessCompleted, SlickProcessFailure> {
    if max_output_bytes < 0 {
        return Err(slick_process_failure("OutputLimit", program, "MaxOutputBytes must not be negative".to_string()));
    }
    if context.cancelled() {
        return Err(slick_process_failure("Cancelled", program, "operation cancelled before child start".to_string()));
    }
    let spawn_program = if program.contains('/') {
        program.to_string()
    } else {
        match slick_process_look_path(program) {
            SlickProcessLookup::Found(resolved) => resolved,
            SlickProcessLookup::Relative => {
                return Err(slick_process_failure("Spawn", program,
                    format!("exec: \"{}\": cannot run executable found relative to current directory", program)));
            }
            SlickProcessLookup::Missing => {
                return Err(slick_process_failure("Spawn", program, format!("exec: \"{}\": executable file not found in $PATH", program)));
            }
        }
    };
    let mut command = std::process::Command::new(&spawn_program);
    if spawn_program != program {
        use std::os::unix::process::CommandExt;
        command.arg0(program);
    }
    command.args(arguments);
    slick_process_apply_env_overlay(&mut command);
    if let Some(directory) = working_directory {
        match std::fs::metadata(directory) {
            Ok(info) if info.is_dir() => {
                command.current_dir(directory);
            }
            _ => {
                return Err(slick_process_failure("WorkingDirectory", program, "working directory is not an existing directory".to_string()));
            }
        }
    }
    if context.cancelled() {
        return Err(slick_process_failure("Cancelled", program, "operation cancelled before child start".to_string()));
    }
    command.stdout(std::process::Stdio::piped());
    command.stderr(std::process::Stdio::piped());
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(error) => {
            if context.cancelled() {
                return Err(slick_process_failure("Cancelled", program, "operation cancelled before child start".to_string()));
            }
            return Err(slick_process_failure("Spawn", program, slick_process_spawn_message(&spawn_program, &error)));
        }
    };
    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    let capture = std::sync::Arc::new(std::sync::Mutex::new(SlickProcessCaptureData {
        limit: max_output_bytes,
        total: 0,
        overflow: false,
        output: Vec::new(),
        error_output: Vec::new(),
    }));
    let capture_out = std::sync::Arc::clone(&capture);
    let out_thread = std::thread::spawn(move || {
        if let Some(mut reader) = stdout {
            slick_process_drain(&mut reader, &capture_out, false);
        }
    });
    let capture_err = std::sync::Arc::clone(&capture);
    let err_thread = std::thread::spawn(move || {
        if let Some(mut reader) = stderr {
            slick_process_drain(&mut reader, &capture_err, true);
        }
    });
    // Poll the child rather than blocking forever: cancellation and capture
    // overflow are observed here so a cancelled or runaway child is signalled
    // and reaped instead of stranding the wait.
    let mut finished: Option<std::process::ExitStatus> = None;
    let mut wait_error: Option<String> = None;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                finished = Some(status);
                break;
            }
            Ok(None) => {
                if context.cancelled() {
                    let _ = child.kill();
                    break;
                }
                let overflow = capture.lock().unwrap_or_else(|error| error.into_inner()).overflow;
                if overflow {
                    let _ = child.kill();
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(1));
            }
            Err(error) => {
                wait_error = Some(error.to_string());
                break;
            }
        }
    }
    // Reap any child we signalled or that did not report an exit status, so no
    // zombie survives the operation.
    if finished.is_none() {
        let _ = child.wait();
    }
    let _ = out_thread.join();
    let _ = err_thread.join();
    if context.cancelled() {
        return Err(slick_process_failure("Cancelled", program, "operation cancelled; child process was signalled".to_string()));
    }
    let data = capture.lock().unwrap_or_else(|error| error.into_inner());
    if data.overflow {
        return Err(slick_process_failure("OutputLimit", program, format!("captured output exceeds {} bytes", max_output_bytes)));
    }
    if let Some(message) = wait_error {
        return Err(slick_process_failure("Wait", program, message));
    }
    match finished {
        Some(status) => match status.code() {
            Some(code) => Ok(SlickProcessCompleted {
                exit_code: code as i64,
                output: data.output.clone(),
                error_output: data.error_output.clone(),
            }),
            None => Err(slick_process_failure("Signal", program, "child process was terminated by a signal".to_string())),
        },
        None => Err(slick_process_failure("Wait", program, "child process did not report a status".to_string())),
    }
}

// slick_process_spawn_message rebuilds os/exec Start error text so a missing
// program, a permission failure, and other spawn errors match the interpreter.
fn slick_process_spawn_message(program: &str, error: &std::io::Error) -> String {
    if !program.contains('/') && error.kind() == std::io::ErrorKind::NotFound {
        return format!("exec: \"{}\": executable file not found in $PATH", program);
    }
    let desc = slick_process_io_desc(error);
    if program.contains('/') {
        format!("fork/exec {}: {}", program, desc)
    } else {
        format!("exec: \"{}\": {}", program, desc)
    }
}

fn slick_process_io_desc(error: &std::io::Error) -> String {
    match error.raw_os_error() {
        Some(1) => return "operation not permitted".to_string(),
        Some(2) => return "no such file or directory".to_string(),
        Some(8) => return "exec format error".to_string(),
        Some(13) => return "permission denied".to_string(),
        Some(21) => return "is a directory".to_string(),
        Some(26) => return "text file busy".to_string(),
        _ => {}
    }
    let text = error.to_string();
    match text.split_once(" (os error ") {
        Some((head, _)) => {
            let mut chars = head.chars();
            match chars.next() {
                Some(first) => {
                    let mut lowered = first.to_lowercase().collect::<String>();
                    lowered.push_str(chars.as_str());
                    lowered
                }
                None => String::new(),
            }
        }
        None => text,
    }
}
// slick_process_look_path mirrors os/exec.LookPath for a bare name: empty PATH
// finds nothing, empty PATH components mean the current directory, directories
// and non-executable files are skipped, and a hit reached through a relative
// PATH entry is refused the way Go refuses ErrDot, because running it would let
// the current directory decide which executable a program starts.
enum SlickProcessLookup {
    Found(String),
    Relative,
    Missing,
}

fn slick_process_look_path(program: &str) -> SlickProcessLookup {
    if program.is_empty() {
        return SlickProcessLookup::Missing;
    }
    let path_var = slick_environment_read("PATH").unwrap_or_default();
    if path_var.is_empty() {
        return SlickProcessLookup::Missing;
    }
    for dir in path_var.split(':') {
        let relative = dir.is_empty() || !dir.starts_with('/');
        let candidate = if dir.is_empty() {
            format!("./{}", program)
        } else if dir.ends_with('/') {
            format!("{}{}", dir, program)
        } else {
            format!("{}/{}", dir, program)
        };
        if slick_process_is_executable(&candidate) {
            if relative {
                return SlickProcessLookup::Relative;
            }
            return SlickProcessLookup::Found(candidate);
        }
    }
    SlickProcessLookup::Missing
}

fn slick_process_absolute_path(path: &str) -> String {
    let candidate = std::path::Path::new(path);
    if candidate.is_absolute() {
        return path.to_string();
    }
    match std::env::current_dir() {
        Ok(cwd) => cwd.join(candidate).to_string_lossy().into_owned(),
        Err(_) => path.to_string(),
    }
}

fn slick_process_is_executable(path: &str) -> bool {
    let meta = match std::fs::metadata(path) {
        Ok(meta) => meta,
        Err(_) => return false,
    };
    if meta.is_dir() {
        return false;
    }
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode() & 0o111 != 0
}

// slick_process_apply_env_overlay rebuilds the child environment from the
// host plus the compiler-owned overlay so Set-then-Run matches the interpreter
// without mutating the host process environment.
// A child inherits the host environment as-is, including entries that are not
// valid Unicode, and then receives the compiler-owned Set and Unset changes.
fn slick_process_apply_env_overlay(command: &mut std::process::Command) {
    for (name, value) in slick_environment_changes() {
        match value {
            Some(value) => { command.env(name, value); }
            None => { command.env_remove(name); }
        }
    }
}


fn slick_nat_process_run(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let program = match slick_arg_string(&args, 0) {
        Ok(value) => value,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    let arguments_value = match slick_arg_values(&args, 1) {
        Ok(values) => values,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    let mut arguments = Vec::with_capacity(arguments_value.len());
    for value in arguments_value {
        match value {
            SlickValue::String(text) => arguments.push(text),
            value => return SlickOutcome::Throw(SlickFailure::host(format!("std.process.Run argument is {} and not string", slick_type_name(&value)))),
        }
    }
    let working_directory = match slick_arg_optional(&args, 2) {
        None => None,
        Some(SlickValue::String(text)) => Some(text),
        Some(value) => return SlickOutcome::Throw(SlickFailure::host(format!("std.process.Run WorkingDirectory is {} and not string", slick_type_name(&value)))),
    };
    let max_output_bytes = match slick_arg_int(&args, 3) {
        Ok(value) => value,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    match slick_process_perform(context, &program, &arguments, working_directory.as_deref(), max_output_bytes) {
        Ok(completed) => slick_ok(slick_object("std.process.Completed", vec![
            ("ExitCode", SlickValue::Int(completed.exit_code)),
            ("Output", SlickValue::Bytes(completed.output)),
            ("ErrorOutput", SlickValue::Bytes(completed.error_output)),
        ])),
        Err(failure) => slick_err(slick_object("std.process.Failure", vec![
            ("Operation", slick_string(failure.operation)),
            ("Program", slick_string(failure.program)),
            ("Message", slick_string(failure.message)),
        ])),
    }
}
`
