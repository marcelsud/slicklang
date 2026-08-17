package compiler

var rustStdEnvironment = rustStdFamily{
	family: runtimeFamilyEnvironment,
	module: rustStdEnvironmentModule,
	functions: map[runtimeOperationID]string{
		nativeStdEnvGet:   "slick_nat_env_get",
		nativeStdEnvSet:   "slick_nat_env_set",
		nativeStdEnvUnset: "slick_nat_env_unset",
	},
}

// rustStdEnvironmentModule implements std.env. Get returns the value or an
// absent optional, matching os.LookupEnv; Set and Unset return the typed
// std.env.Failure the interpreter produces. Set and Unset record a compiler-owned
// overlay via slick_environment_overlay instead of mutating the host environment:
// set_var/remove_var are unsafe once runtime threads exist. Get uses
// slick_environment_read so overlay assignments are visible to HTTP, process,
// and every other family. Name validation matches syscall.Setenv; Unset never
// reports an error.
const rustStdEnvironmentModule = `fn slick_nat_env_get(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let name = match slick_arg_string(&args, 0) { Ok(name) => name, Err(failure) => return SlickOutcome::Throw(failure) };
    // os.LookupEnv distinguishes present-but-empty from absent.
    // slick_environment_read consults the overlay first, then the host.
    match slick_environment_read(&name) {
        Some(text) => SlickOutcome::Value(SlickValue::Optional(Some(Box::new(slick_string(text))))),
        None => SlickOutcome::Value(SlickValue::Optional(None)),
    }
}

fn slick_nat_env_set(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let name = match slick_arg_string(&args, 0) { Ok(name) => name, Err(failure) => return SlickOutcome::Throw(failure) };
    let value = match slick_arg_string(&args, 1) { Ok(value) => value, Err(failure) => return SlickOutcome::Throw(failure) };
    if slick_env_name_invalid(&name) || value.contains('\0') {
        // os.Setenv wraps syscall.EINVAL in NewSyscallError("setenv", ...),
        // whose text is "setenv: invalid argument".
        return slick_err(slick_env_failure("Set", &name, "setenv: invalid argument"));
    }
    slick_environment_overlay(|overlay| {
        overlay.insert(name, Some(value));
    });
    slick_ok(SlickValue::Null)
}

fn slick_nat_env_unset(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let name = match slick_arg_string(&args, 0) { Ok(name) => name, Err(failure) => return SlickOutcome::Throw(failure) };
    // os.Unsetenv never reports an error: a missing key is a no-op. A name
    // carrying '=' or NUL is accepted without recording an overlay entry.
    if slick_env_name_invalid(&name) {
        return slick_ok(SlickValue::Null);
    }
    slick_environment_overlay(|overlay| {
        overlay.insert(name, None);
    });
    slick_ok(SlickValue::Null)
}

fn slick_env_name_invalid(name: &str) -> bool {
    name.is_empty() || name.contains('=') || name.contains('\0')
}

fn slick_env_failure(operation: &str, name: &str, message: &str) -> SlickValue {
    slick_object("std.env.Failure", vec![
        ("Operation", slick_string(operation)),
        ("Name", slick_string(name)),
        ("Message", slick_string(message)),
    ])
}
`
