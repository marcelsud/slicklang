package compiler

var bunStdProcess = bunStdFamily{
	family: runtimeFamilyProcess,
	module: bunStdProcessModule,
	functions: map[runtimeOperationID]string{
		nativeStdProcessRun: "slickNatProcessRun",
	},
}

// bunStdProcessModule implements std.process.Run: a direct exec with no shell,
// LookPath that skips non-executables the way Go 1.26 exec.LookPath does,
// refuses relative PATH hits with Go's ErrDot text, bounded combined capture,
// working-directory validation, the compiler-owned env overlay, and
// cancellation that kills and reaps the child.
const bunStdProcessModule = `let slickProcessFsMod = null;

async function slickProcessFs() {
  if (slickProcessFsMod === null) slickProcessFsMod = await import("node:fs");
  return slickProcessFsMod;
}

function slickProcessFailure(operation, program, message) {
  return { ok: false, operation: operation, program: program, message: message };
}

function slickProcessSleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function slickProcessCaptureWrite(capture, bytes, isError) {
  const remaining = capture.limit - capture.total;
  let accepted = bytes.length;
  if (accepted > remaining) {
    capture.overflow = true;
    accepted = remaining;
  }
  if (accepted > 0) {
    const chunk = accepted === bytes.length ? bytes : bytes.subarray(0, accepted);
    if (isError) capture.errorOutput.push(chunk);
    else capture.output.push(chunk);
    capture.total += accepted;
  }
}

function slickProcessConcat(chunks) {
  let total = 0;
  for (const chunk of chunks) total += chunk.length;
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}

async function slickProcessDrain(stream, capture, isError) {
  if (stream === undefined || stream === null) return;
  try {
    const reader = stream.getReader();
    for (;;) {
      const result = await reader.read();
      if (result.done) break;
      slickProcessCaptureWrite(capture, result.value, isError);
    }
  } catch (_error) {}
}

function slickProcessIsExecutable(fs, path) {
  let meta;
  try {
    meta = fs.statSync(path);
  } catch (_error) {
    return false;
  }
  if (meta.isDirectory()) return false;
  // Go's findExecutable asks the kernel (X_OK) rather than reading mode bits, so
  // a noexec mount or an ACL denial skips the candidate; only when the check
  // itself is unsupported does it fall back to the mode bits.
  try {
    fs.accessSync(path, fs.constants.X_OK);
    return true;
  } catch (error) {
    const code = error && error.code;
    if (code === "ENOSYS" || code === "EPERM") return (meta.mode & 0o111) !== 0;
    return false;
  }
}

function slickProcessLookPath(fs, program) {
  if (program.length === 0) return { kind: "missing" };
  // filepath.SplitList("") yields one empty element, which Go resolves against
  // the current directory and then refuses as a relative hit.
  const pathVar = slickEnvironmentRead("PATH");
  const dirs = (pathVar === null ? "" : pathVar).split(":");
  for (const dir of dirs) {
    const relative = dir.length === 0 || !dir.startsWith("/");
    const candidate = dir.length === 0 ? "./" + program : dir.endsWith("/") ? dir + program : dir + "/" + program;
    if (slickProcessIsExecutable(fs, candidate)) {
      if (relative) return { kind: "relative" };
      return { kind: "found", path: candidate };
    }
  }
  return { kind: "missing" };
}

function slickProcessIoDesc(error) {
  const code = error && error.code;
  if (code === "EPERM") return "operation not permitted";
  if (code === "ENOENT") return "no such file or directory";
  if (code === "ENOEXEC") return "exec format error";
  if (code === "EACCES") return "permission denied";
  if (code === "EISDIR") return "is a directory";
  if (code === "ETXTBSY") return "text file busy";
  const text = String(error && error.message ? error.message : error);
  const split = text.indexOf(" (os error ");
  const head = split >= 0 ? text.slice(0, split) : text;
  if (head.length === 0) return "";
  return head.charAt(0).toLowerCase() + head.slice(1);
}

function slickProcessSpawnMessage(program, error) {
  const code = error && error.code;
  if (program.indexOf("/") < 0 && (code === "ENOENT" || code === "ERR_MODULE_NOT_FOUND")) {
    return "exec: \"" + program + "\": executable file not found in $PATH";
  }
  const desc = slickProcessIoDesc(error);
  if (program.indexOf("/") >= 0) return "fork/exec " + program + ": " + desc;
  return "exec: \"" + program + "\": " + desc;
}

function slickProcessChildEnv() {
  const env = Object.assign({}, process.env);
  for (const entry of slickEnvironmentChanges()) {
    if (entry[1] === null || entry[1] === undefined) delete env[entry[0]];
    else env[entry[0]] = entry[1];
  }
  return env;
}

async function slickProcessPerform(context, program, argv, workingDirectory, maxOutputBytes) {
  if (maxOutputBytes < 0n) return slickProcessFailure("OutputLimit", program, "MaxOutputBytes must not be negative");
  if (context.cancelled()) return slickProcessFailure("Cancelled", program, "operation cancelled before child start");
  const fs = await slickProcessFs();
  let spawnProgram = program;
  if (program.indexOf("/") < 0) {
    const lookup = slickProcessLookPath(fs, program);
    if (lookup.kind === "relative") {
      return slickProcessFailure("Spawn", program, "exec: \"" + program + "\": cannot run executable found relative to current directory");
    }
    if (lookup.kind === "missing") {
      return slickProcessFailure("Spawn", program, "exec: \"" + program + "\": executable file not found in $PATH");
    }
    spawnProgram = lookup.path;
  }
  if (workingDirectory !== null) {
    try {
      const info = fs.statSync(workingDirectory);
      if (!info.isDirectory()) return slickProcessFailure("WorkingDirectory", program, "working directory is not an existing directory");
    } catch (_error) {
      return slickProcessFailure("WorkingDirectory", program, "working directory is not an existing directory");
    }
  }
  if (context.cancelled()) return slickProcessFailure("Cancelled", program, "operation cancelled before child start");
  const limit = maxOutputBytes > BigInt(Number.MAX_SAFE_INTEGER) ? Number.MAX_SAFE_INTEGER : Number(maxOutputBytes);
  const capture = { limit: limit, total: 0, overflow: false, output: [], errorOutput: [] };
  let proc;
  try {
    const options = {
      cmd: [spawnProgram].concat(argv),
      env: slickProcessChildEnv(),
      stdout: "pipe",
      stderr: "pipe",
      stdin: "ignore",
    };
    if (workingDirectory !== null) options.cwd = workingDirectory;
    proc = Bun.spawn(options);
  } catch (error) {
    if (context.cancelled()) return slickProcessFailure("Cancelled", program, "operation cancelled before child start");
    return slickProcessFailure("Spawn", program, slickProcessSpawnMessage(spawnProgram, error));
  }
  const stdoutDone = slickProcessDrain(proc.stdout, capture, false);
  const stderrDone = slickProcessDrain(proc.stderr, capture, true);
  let exitCode = null;
  let waitError = null;
  let finished = false;
  const exited = Promise.resolve(proc.exited).then((code) => {
    finished = true;
    exitCode = code;
  }, (error) => {
    finished = true;
    waitError = String(error);
  });
  while (!finished) {
    if (context.cancelled() || capture.overflow) {
      try { proc.kill("SIGKILL"); } catch (_error) {}
      break;
    }
    await slickProcessSleep(1);
  }
  if (!finished) {
    try { await exited; } catch (_error) {}
  }
  await stdoutDone;
  await stderrDone;
  if (context.cancelled()) return slickProcessFailure("Cancelled", program, "operation cancelled; child process was signalled");
  if (capture.overflow) return slickProcessFailure("OutputLimit", program, "captured output exceeds " + maxOutputBytes.toString() + " bytes");
  if (waitError !== null) return slickProcessFailure("Wait", program, waitError);
  if (typeof exitCode !== "number") return slickProcessFailure("Signal", program, "child process was terminated by a signal");
  return {
    ok: true,
    exitCode: BigInt(exitCode),
    output: slickProcessConcat(capture.output),
    errorOutput: slickProcessConcat(capture.errorOutput),
  };
}

export async function slickNatProcessRun(context, args) {
  try {
    const program = slickArgString(args, 0);
    const argumentValues = slickArgValues(args, 1);
    const argv = [];
    for (const value of argumentValues) {
      if (typeof value !== "string") {
        throw SlickFailure.host("std.process.Run argument is " + slickTypeName(value) + " and not string");
      }
      argv.push(value);
    }
    const working = slickArgOptional(args, 2);
    let workingDirectory = null;
    if (working !== undefined) {
      if (typeof working !== "string") {
        throw SlickFailure.host("std.process.Run WorkingDirectory is " + slickTypeName(working) + " and not string");
      }
      workingDirectory = working;
    }
    const maxOutputBytes = slickArgInt(args, 3);
    const result = await slickProcessPerform(context, program, argv, workingDirectory, maxOutputBytes);
    if (result.ok) {
      return slickOk(slickStdObject("std.process.Completed", [
        ["ExitCode", result.exitCode],
        ["Output", result.output],
        ["ErrorOutput", result.errorOutput],
      ]));
    }
    return slickErr(slickStdObject("std.process.Failure", [
      ["Operation", result.operation],
      ["Program", result.program],
      ["Message", result.message],
    ]));
  } catch (error) {
    throw slickAsFailure(error);
  }
}
`
