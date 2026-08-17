package compiler

var rustStdFilesystem = rustStdFamily{
	family: runtimeFamilyFilesystem,
	module: rustStdFilesystemModule,
	functions: map[runtimeOperationID]string{
		nativeStdFSExists:                   "slick_nat_fs_exists",
		nativeStdFSReadText:                 "slick_nat_fs_read_text",
		nativeStdFSWriteText:                "slick_nat_fs_write_text",
		nativeStdFSCreateDirectoryAll:       "slick_nat_fs_mkdir",
		nativeStdFSRemove:                   "slick_nat_fs_remove",
		nativeStdFSReadDirectory:            "slick_nat_fs_read_dir",
		nativeStdFSCreateTemporaryDirectory: "slick_nat_fs_tmp",
		nativeStdFSTemporaryDirectoryClose:  "slick_nat_fs_tmp_close",
	},
}

// rustStdFilesystemModule implements std.fs. The interpreter runs main under
// context.Background, whose Done channel is nil, so ReadText and WriteText take
// the plain os.ReadFile/os.WriteFile path rather than the cancellable one: no
// non-regular-file rejection, and failures carry the os.PathError op prefix
// ("open", "read", "write"). Each host io::Error is normalized into that
// "<op> <path>: <errno description>" shape so the failure Message matches the
// interpreter and generated Go byte for byte. Exists reports a missing path as
// Ok(false) and only surfaces a Failure for any other stat error. ReadDirectory
// sorts entries by name the way os.ReadDir does. CreateTemporaryDirectory
// stores a SlickFSTemporary on the owned resource; Close removes the tree
// exactly once, treats a missing path as success the way os.RemoveAll does,
// and throws the documented std.fs.Failure when the object never owned a
// resource. Temporary-directory creation reads TMPDIR through the compiler-owned
// overlay so a std.env.Set is visible the way os.Setenv is to the interpreter.
const rustStdFilesystemModule = `static SLICK_FS_TEMP_SEQUENCE: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);

struct SlickFSTemporary {
    path: String,
    closed: bool,
}

impl SlickFSTemporary {
    fn close(&mut self) -> Result<(), String> {
        if self.closed {
            return Ok(());
        }
        let cleaned = slick_fs_clean(&self.path);
        if slick_fs_unsafe_cleanup(&cleaned) {
            return Err("refusing to remove unsafe cleanup target".to_string());
        }
        if let Err(error) = std::fs::remove_dir_all(&cleaned) {
            if error.kind() != std::io::ErrorKind::NotFound {
                return Err(slick_fs_io_message("remove", &self.path, &error));
            }
        }
        self.closed = true;
        Ok(())
    }
}

fn slick_nat_fs_exists(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    match std::fs::metadata(&path) {
        Ok(_) => slick_ok(SlickValue::Bool(true)),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => slick_ok(SlickValue::Bool(false)),
        Err(error) => slick_err(slick_fs_failure("Exists", &path, &slick_fs_io_message("stat", &path, &error))),
    }
}

fn slick_nat_fs_read_text(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    // Mirror os.ReadFile: open first (op "open"), then read (op "read"), then
    // validate UTF-8 with the literal message the interpreter uses.
    let mut file = match std::fs::File::open(&path) {
        Ok(file) => file,
        Err(error) => return slick_err(slick_fs_failure("ReadText", &path, &slick_fs_io_message("open", &path, &error))),
    };
    let mut bytes = Vec::new();
    if let Err(error) = std::io::Read::read_to_end(&mut file, &mut bytes) {
        return slick_err(slick_fs_failure("ReadText", &path, &slick_fs_io_message("read", &path, &error)));
    }
    match std::str::from_utf8(&bytes) {
        Ok(text) => slick_ok(slick_string(text.to_string())),
        Err(_) => slick_err(slick_fs_failure("ReadText", &path, "invalid UTF-8")),
    }
}

fn slick_nat_fs_write_text(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    let contents = match slick_arg_string(&args, 1) { Ok(contents) => contents, Err(failure) => return SlickOutcome::Throw(failure) };
    // Mirror os.WriteFile: OpenFile with O_WRONLY|O_CREATE|O_TRUNC (op "open")
    // then Write (op "write").
    let mut file = match std::fs::File::create(&path) {
        Ok(file) => file,
        Err(error) => return slick_err(slick_fs_failure("WriteText", &path, &slick_fs_io_message("open", &path, &error))),
    };
    if let Err(error) = std::io::Write::write_all(&mut file, contents.as_bytes()) {
        return slick_err(slick_fs_failure("WriteText", &path, &slick_fs_io_message("write", &path, &error)));
    }
    slick_ok(SlickValue::Null)
}

fn slick_nat_fs_mkdir(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    match std::fs::create_dir_all(&path) {
        Ok(()) => slick_ok(SlickValue::Null),
        Err(error) => slick_err(slick_fs_failure("CreateDirectoryAll", &path, &slick_fs_io_message("mkdir", &path, &error))),
    }
}

fn slick_nat_fs_remove(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    match slick_fs_remove(&path) {
        Ok(()) => slick_ok(SlickValue::Null),
        Err(error) => slick_err(slick_fs_failure("Remove", &path, &slick_fs_io_message("remove", &path, &error))),
    }
}

// slick_fs_remove mirrors os.Remove: unlink, and when the path is a directory
// (EISDIR) retry with rmdir. Either way the reported op is "remove".
fn slick_fs_remove(path: &str) -> std::io::Result<()> {
    match std::fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::IsADirectory => std::fs::remove_dir(path),
        Err(error) => Err(error),
    }
}

fn slick_nat_fs_read_dir(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) { Ok(path) => path, Err(failure) => return SlickOutcome::Throw(failure) };
    let reader = match std::fs::read_dir(&path) {
        Ok(reader) => reader,
        Err(error) => return slick_err(slick_fs_failure("ReadDirectory", &path, &slick_fs_io_message("open", &path, &error))),
    };
    let mut entries: Vec<(String, bool)> = Vec::new();
    for result in reader {
        match result {
            Ok(entry) => {
                let name = entry.file_name().to_string_lossy().into_owned();
                let is_directory = entry.file_type().map(|kind| kind.is_dir()).unwrap_or(false);
                entries.push((name, is_directory));
            }
            Err(error) => return slick_err(slick_fs_failure("ReadDirectory", &path, &slick_fs_io_message("read", &path, &error))),
        }
    }
    // os.ReadDir sorts by file name; byte-order comparison matches Go string
    // comparison for UTF-8 names.
    entries.sort_by(|left, right| left.0.cmp(&right.0));
    let values: Vec<SlickValue> = entries.into_iter().map(|(name, is_directory)| {
        let entry_path = slick_fs_join(&path, &name);
        slick_object("std.fs.Entry", vec![
            ("Name", slick_string(name)),
            ("Path", slick_string(entry_path)),
            ("IsDirectory", SlickValue::Bool(is_directory)),
        ])
    }).collect();
    slick_ok(SlickValue::Array(values))
}

fn slick_nat_fs_tmp(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let prefix = match slick_arg_string(&args, 0) { Ok(prefix) => prefix, Err(failure) => return SlickOutcome::Throw(failure) };
    match slick_fs_create_temp(&prefix) {
        Ok(path) => {
            let resource = slick_resource_new(Box::new(SlickFSTemporary { path: path.clone(), closed: false }));
            slick_ok(slick_resource_object("std.fs.TemporaryDirectory", vec![("Path", slick_string(path))], resource))
        }
        Err(error) => slick_err(slick_fs_failure("CreateTemporaryDirectory", &prefix, &slick_fs_io_message("mkdir", &prefix, &error))),
    }
}

fn slick_nat_fs_tmp_close(_context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = slick_fs_self_path(&slick_arg(&args, 0));
    // Close is a throwing operation, not a Result: failures become
    // SlickOutcome::Throw carrying the std.fs.Failure value.
    match slick_arg_resource(&args, 0) {
        Some(resource) => match slick_resource_with_state::<SlickFSTemporary, Result<(), String>>(&resource, |directory| directory.close()) {
            Some(Ok(())) => SlickOutcome::Value(SlickValue::Null),
            Some(Err(message)) => SlickOutcome::Throw(SlickFailure::slick(slick_fs_failure("Close", &path, &message))),
            None => SlickOutcome::Value(SlickValue::Null),
        },
        None => SlickOutcome::Throw(SlickFailure::slick(slick_fs_failure("Close", &path, "temporary directory is not owned by this resource"))),
    }
}

// slick_fs_temp_dir honours an overlay assignment of TMPDIR the way
// os.TempDir honours os.Setenv("TMPDIR", ...). An overlay Unset hides the
// host value and falls back to /tmp, matching Unix os.TempDir.
fn slick_fs_temp_dir() -> std::path::PathBuf {
    match slick_environment_read("TMPDIR") {
        Some(dir) if !dir.is_empty() => std::path::PathBuf::from(dir),
        _ => std::path::PathBuf::from("/tmp"),
    }
}

// slick_fs_create_temp places a unique directory under the platform temporary
// root and returns its absolute path, mirroring createTemporaryDirectory.
fn slick_fs_create_temp(prefix: &str) -> std::io::Result<String> {
    let base = slick_fs_temp_dir();
    let pid = std::process::id();
    let sequence = SLICK_FS_TEMP_SEQUENCE.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
    for attempt in 0u64..10000 {
        let name = format!("{}{}{}{}", prefix, pid, sequence, attempt);
        let candidate = base.join(&name);
        match std::fs::create_dir(&candidate) {
            Ok(()) => return Ok(slick_fs_absolute_path(&candidate)),
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(error) => return Err(error),
        }
    }
    Err(std::io::Error::from_raw_os_error(17))
}

fn slick_fs_absolute_path(path: &std::path::Path) -> String {
    if path.is_absolute() {
        return slick_fs_clean(&path.to_string_lossy());
    }
    match std::env::current_dir() {
        Ok(cwd) => slick_fs_clean(&cwd.join(path).to_string_lossy()),
        Err(_) => slick_fs_clean(&path.to_string_lossy()),
    }
}

// slick_fs_io_message rebuilds the os.PathError text "<op> <path>: <desc>" so
// the failure Message matches the interpreter and generated Go exactly.
fn slick_fs_io_message(op: &str, path: &str, error: &std::io::Error) -> String {
    format!("{} {}: {}", op, path, slick_fs_errno_desc(error))
}

// slick_fs_errno_desc maps a host errno to the lowercase POSIX description
// syscall.Errno.Error produces. The table covers every errno a regular std.fs
// operation surfaces on Linux; anything else falls back to the io::Error
// Display with its trailing " (os error N)" stripped and decapitalized, which
// also matches Go for the common kinds.
fn slick_fs_errno_desc(error: &std::io::Error) -> String {
    match error.raw_os_error() {
        Some(1) => return "operation not permitted".to_string(),
        Some(2) => return "no such file or directory".to_string(),
        Some(5) => return "input/output error".to_string(),
        Some(9) => return "bad file descriptor".to_string(),
        Some(13) => return "permission denied".to_string(),
        Some(16) => return "device or resource busy".to_string(),
        Some(17) => return "file exists".to_string(),
        Some(20) => return "not a directory".to_string(),
        Some(21) => return "is a directory".to_string(),
        Some(22) => return "invalid argument".to_string(),
        Some(26) => return "text file busy".to_string(),
        Some(28) => return "no space left on device".to_string(),
        Some(30) => return "read-only file system".to_string(),
        Some(31) => return "too many links".to_string(),
        Some(36) => return "file name too long".to_string(),
        Some(39) => return "directory not empty".to_string(),
        Some(40) => return "too many levels of symbolic links".to_string(),
        _ => {}
    }
    let text = error.to_string();
    match text.split_once(" (os error ") {
        Some((head, _)) => slick_fs_lowercase_first(head),
        None => text,
    }
}

fn slick_fs_lowercase_first(text: &str) -> String {
    let mut chars = text.chars();
    match chars.next() {
        Some(first) => {
            let mut lowered = first.to_lowercase().collect::<String>();
            lowered.push_str(chars.as_str());
            lowered
        }
        None => String::new(),
    }
}

fn slick_fs_failure(operation: &str, path: &str, message: &str) -> SlickValue {
    slick_object("std.fs.Failure", vec![
        ("Operation", slick_string(operation)),
        ("Path", slick_string(path)),
        ("Message", slick_string(message)),
    ])
}

fn slick_fs_self_path(value: &SlickValue) -> String {
    match slick_field(value, "Path") {
        Ok(SlickValue::String(path)) => path,
        _ => String::new(),
    }
}

// slick_fs_join mirrors filepath.Join for two elements: combine and clean.
fn slick_fs_join(path: &str, name: &str) -> String {
    let combined = if path.is_empty() {
        name.to_string()
    } else if path.ends_with('/') {
        format!("{}{}", path, name)
    } else {
        format!("{}/{}", path, name)
    };
    slick_fs_clean(&combined)
}

// slick_fs_clean is a lexical filepath.Clean: it drops "." segments, collapses
// repeated separators, and resolves ".." against the preceding component. A
// real TemporaryDirectory always owns an absolute, already-clean path, so this
// is faithful for the inputs Close and Join actually receive.
fn slick_fs_clean(path: &str) -> String {
    if path.is_empty() {
        return ".".to_string();
    }
    let mut parts: Vec<&str> = Vec::new();
    let rooted = path.starts_with('/');
    for segment in path.split('/') {
        match segment {
            "" | "." => {}
            ".." => {
                if let Some(last) = parts.last() {
                    if *last != ".." {
                        parts.pop();
                        continue;
                    }
                }
                if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    if parts.is_empty() {
        return if rooted { "/".to_string() } else { ".".to_string() };
    }
    let body = parts.join("/");
    if rooted { format!("/{body}") } else { body }
}

// slick_fs_dir mirrors filepath.Dir: the cleaned parent. A root path has no
// parent and returns itself; a bare relative name returns ".".
fn slick_fs_dir(path: &str) -> String {
    match std::path::Path::new(path).parent() {
        Some(parent) if !parent.as_os_str().is_empty() => slick_fs_clean(&parent.to_string_lossy()),
        _ => {
            if path.starts_with('/') { "/".to_string() } else { ".".to_string() }
        }
    }
}

// slick_fs_unsafe_cleanup mirrors unsafeCleanupTarget: refuse "." and any root
// path (where filepath.Dir(path) == path) so Close can never become a
// recursive host wipe.
fn slick_fs_unsafe_cleanup(cleaned: &str) -> bool {
    if cleaned == "." {
        return true;
    }
    slick_fs_dir(cleaned) == cleaned
}
`
