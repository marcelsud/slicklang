package compiler

var bunStdFilesystem = bunStdFamily{
	family: runtimeFamilyFilesystem,
	module: bunStdFilesystemModule,
	functions: map[runtimeOperationID]string{
		nativeStdFSExists:                   "slickNatFSExists",
		nativeStdFSReadText:                 "slickNatFSReadText",
		nativeStdFSWriteText:                "slickNatFSWriteText",
		nativeStdFSCreateDirectoryAll:       "slickNatFSMkdir",
		nativeStdFSRemove:                   "slickNatFSRemove",
		nativeStdFSReadDirectory:            "slickNatFSReadDir",
		nativeStdFSCreateTemporaryDirectory: "slickNatFSTmp",
		nativeStdFSTemporaryDirectoryClose:  "slickNatFSTmpClose",
	},
}

// bunStdFilesystemModule implements std.fs. The interpreter runs main under
// context.Background, whose Done channel is nil, so ReadText and WriteText take
// the plain os.ReadFile/os.WriteFile path rather than the cancellable one: no
// non-regular-file rejection, and failures carry the os.PathError op prefix
// ("open", "read", "write"). A launched task or server handler has a cancellable
// context, so those calls stat the followed target first, reject non-regular
// non-FIFO paths with "non-regular files are not supported", and abort an
// unmatched FIFO with "operation cancelled". Each host error is normalized into
// that "<op> <path>: <errno description>" shape so the failure Message matches
// the interpreter and generated Go byte for byte; Node's ERR_INVALID_ARG_VALUE
// (a NUL in the path) becomes "invalid argument". Exists reports a missing path
// as Ok(false) and only surfaces a Failure for any other stat error.
// ReadDirectory sorts names by UTF-8 byte order the way os.ReadDir does.
// CreateTemporaryDirectory rejects a Prefix that contains a path separator,
// matching os.MkdirTemp, so the workspace and its Close delete stay under the
// temporary root. Each candidate uses mode 0o700 and a cryptographically random
// suffix. It stores owned state on the resource; Close removes the tree exactly
// once, treats a missing path as success the way os.RemoveAll does, and throws
// the documented std.fs.Failure when the object never owned a resource.
// Temporary-directory creation reads TMPDIR through the compiler-owned overlay
// so a std.env.Set is visible the way os.Setenv is to the interpreter.
const bunStdFilesystemModule = `import * as slickStdFilesystemNode from "node:fs";

const SLICK_FS_CODES = {
  EPERM: "operation not permitted",
  ENOENT: "no such file or directory",
  EIO: "input/output error",
  EBADF: "bad file descriptor",
  EACCES: "permission denied",
  EBUSY: "device or resource busy",
  EEXIST: "file exists",
  ENOTDIR: "not a directory",
  EISDIR: "is a directory",
  EINVAL: "invalid argument",
  ETXTBSY: "text file busy",
  ENOSPC: "no space left on device",
  EROFS: "read-only file system",
  EMLINK: "too many links",
  ENAMETOOLONG: "file name too long",
  ENOTEMPTY: "directory not empty",
  ELOOP: "too many levels of symbolic links",
};

const SLICK_FS_ERRNOS = {
  1: "operation not permitted",
  2: "no such file or directory",
  5: "input/output error",
  9: "bad file descriptor",
  13: "permission denied",
  16: "device or resource busy",
  17: "file exists",
  20: "not a directory",
  21: "is a directory",
  22: "invalid argument",
  26: "text file busy",
  28: "no space left on device",
  30: "read-only file system",
  31: "too many links",
  36: "file name too long",
  39: "directory not empty",
  40: "too many levels of symbolic links",
};

function slickFsFailure(operation, path, message) {
  return slickStdObject("std.fs.Failure", [
    ["Operation", operation],
    ["Path", path],
    ["Message", message],
  ]);
}

function slickFsLowercaseFirst(text) {
  if (text.length === 0) return text;
  return text[0].toLowerCase() + text.slice(1);
}

function slickFsErrnoDesc(error) {
  if (error && error.code === "ERR_INVALID_ARG_VALUE") return "invalid argument";
  if (error && typeof error.code === "string" && SLICK_FS_CODES[error.code]) {
    return SLICK_FS_CODES[error.code];
  }
  let errno = error && error.errno;
  if (typeof errno === "number") {
    if (errno < 0) errno = -errno;
    if (SLICK_FS_ERRNOS[errno]) return SLICK_FS_ERRNOS[errno];
  }
  const text = String(error && error.message ? error.message : error);
  const match = /^[A-Z][A-Z0-9]+: ([^,]+)(?:,|$)/.exec(text);
  if (match) return match[1];
  const stripped = text.split(" (os error ")[0];
  return slickFsLowercaseFirst(stripped);
}

function slickFsIoMessage(op, path, error) {
  return op + " " + path + ": " + slickFsErrnoDesc(error);
}

function slickFsIsNotFound(error) {
  if (!error) return false;
  if (error.code === "ENOENT") return true;
  return error.errno === 2 || error.errno === -2;
}

function slickFsIsExists(error) {
  if (!error) return false;
  if (error.code === "EEXIST") return true;
  return error.errno === 17 || error.errno === -17;
}

function slickFsHostErr(error) {
  return error instanceof SlickFailure;
}

function slickFsCancellable(context) {
  return context && context.flags && context.flags.length > 0;
}

function slickFsCancelledError() {
  const error = new Error("operation cancelled");
  error.slickFsCancelled = true;
  return error;
}

function slickFsSleep(ms) {
  return new Promise(function (resolve) { setTimeout(resolve, ms); });
}

function slickFsDecodeText(bytes) {
  return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
}

function slickFsPathMode(path, allowMissing) {
  let info;
  try {
    info = slickStdFilesystemNode.statSync(path);
  } catch (error) {
    if (allowMissing && slickFsIsNotFound(error)) return { pipe: false };
    throw error;
  }
  if (info.isFile() || info.isFIFO()) return { pipe: info.isFIFO() };
  const error = new Error("non-regular files are not supported");
  error.slickFsPlain = true;
  throw error;
}

function slickFsUnblockPipe(path, writing) {
  try {
    const fd = slickStdFilesystemNode.openSync(path, writing ? "r" : "w");
    try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
  } catch (error) {}
}

function slickFsOpenAsync(path, flags, mode) {
  return new Promise(function (resolve, reject) {
    slickStdFilesystemNode.open(path, flags, mode, function (err, fd) {
      if (err) reject(err);
      else resolve(fd);
    });
  });
}

function slickFsReadFileAsync(fd) {
  return new Promise(function (resolve, reject) {
    slickStdFilesystemNode.readFile(fd, function (err, bytes) {
      if (err) reject(err);
      else resolve(bytes);
    });
  });
}

function slickFsWriteFileAsync(fd, contents) {
  return new Promise(function (resolve, reject) {
    slickStdFilesystemNode.writeFile(fd, contents, function (err) {
      if (err) reject(err);
      else resolve(null);
    });
  });
}

async function slickFsOpenContext(context, path, flags, mode, pipe, writing) {
  if (!pipe) return slickStdFilesystemNode.openSync(path, flags, mode);
  let settled = false;
  const opened = slickFsOpenAsync(path, flags, mode).then(function (fd) {
    settled = true;
    return fd;
  }, function (error) {
    settled = true;
    throw error;
  });
  const cancelled = (async function () {
    while (!context.cancelled() && !settled) {
      await slickFsSleep(10);
    }
    // An open that already settled owns its descriptor, so the FIFO is only
    // unblocked when cancellation genuinely won the race.
    if (settled || !context.cancelled()) return await opened;
    slickFsUnblockPipe(path, writing);
    throw slickFsCancelledError();
  })();
  try {
    return await Promise.race([opened, cancelled]);
  } catch (error) {
    if (error && error.slickFsCancelled) {
      opened.then(function (fd) {
        try { slickStdFilesystemNode.closeSync(fd); } catch (e) {}
      }).catch(function () {});
    }
    throw error;
  }
}

async function slickFsCallContext(context, fd, call) {
  if (context.cancelled()) {
    try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    throw slickFsCancelledError();
  }
  // The descriptor has exactly one owner: whichever side observes completion or
  // cancellation first claims it, so a race can never close it twice.
  let claimed = false;
  const claim = function () {
    if (claimed) return false;
    claimed = true;
    return true;
  };
  let done = false;
  const work = Promise.resolve().then(call).then(function (value) {
    done = true;
    claim();
    return value;
  }, function (error) {
    done = true;
    claim();
    throw error;
  });
  const cancelled = (async function () {
    while (!context.cancelled() && !done) {
      await slickFsSleep(10);
    }
    if (done || !context.cancelled() || !claim()) return await work;
    try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    throw slickFsCancelledError();
  })();
  return await Promise.race([work, cancelled]);
}

function slickFsReadTextPlain(path) {
  let fd = null;
  try {
    try {
      fd = slickStdFilesystemNode.openSync(path, "r");
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      return slickErr(slickFsFailure("ReadText", path, slickFsIoMessage("open", path, error)));
    }
    let bytes;
    try {
      bytes = slickStdFilesystemNode.readFileSync(fd);
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      return slickErr(slickFsFailure("ReadText", path, slickFsIoMessage("read", path, error)));
    }
    try {
      return slickOk(slickFsDecodeText(bytes));
    } catch (error) {
      return slickErr(slickFsFailure("ReadText", path, "invalid UTF-8"));
    }
  } finally {
    if (fd !== null) {
      try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    }
  }
}

function slickFsWriteTextPlain(path, contents) {
  let fd = null;
  try {
    try {
      fd = slickStdFilesystemNode.openSync(path, "w", 0o666);
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      return slickErr(slickFsFailure("WriteText", path, slickFsIoMessage("open", path, error)));
    }
    try {
      slickStdFilesystemNode.writeFileSync(fd, contents);
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      return slickErr(slickFsFailure("WriteText", path, slickFsIoMessage("write", path, error)));
    }
    return slickOk(null);
  } finally {
    if (fd !== null) {
      try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    }
  }
}

function slickFsSelfPath(value) {
  try {
    const path = slickField(value, "Path");
    return typeof path === "string" ? path : "";
  } catch (error) {
    return "";
  }
}

function slickFsClean(path) {
  if (path.length === 0) return ".";
  const parts = [];
  const rooted = path.startsWith("/");
  for (const segment of path.split("/")) {
    if (segment === "" || segment === ".") continue;
    if (segment === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") {
        parts.pop();
        continue;
      }
      if (!rooted) parts.push("..");
      continue;
    }
    parts.push(segment);
  }
  if (parts.length === 0) return rooted ? "/" : ".";
  return rooted ? "/" + parts.join("/") : parts.join("/");
}

function slickFsJoin(path, name) {
  let combined;
  if (path.length === 0) combined = name;
  else if (path.endsWith("/")) combined = path + name;
  else combined = path + "/" + name;
  return slickFsClean(combined);
}

function slickFsDir(path) {
  const cleaned = slickFsClean(path);
  if (cleaned === "/") return "/";
  const index = cleaned.lastIndexOf("/");
  if (index < 0) return ".";
  if (index === 0) return "/";
  return cleaned.slice(0, index);
}

function slickFsUnsafeCleanup(cleaned) {
  return cleaned === "." || slickFsDir(cleaned) === cleaned;
}

function slickFsAbsolutePath(path) {
  if (path.startsWith("/")) return slickFsClean(path);
  try {
    return slickFsClean(process.cwd() + "/" + path);
  } catch (error) {
    return slickFsClean(path);
  }
}

function slickFsTempDir() {
  const dir = slickEnvironmentRead("TMPDIR");
  if (typeof dir === "string" && dir.length !== 0) return dir;
  return "/tmp";
}

function slickFsRandomSuffix() {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  let text = "";
  for (let i = 0; i < bytes.length; i += 1) {
    const hex = bytes[i].toString(16);
    text += hex.length === 1 ? "0" + hex : hex;
  }
  return text;
}

function slickFsCreateTemp(prefix) {
  const base = slickFsTempDir();
  for (let attempt = 0; attempt < 10000; attempt += 1) {
    const candidate = slickFsJoin(base, prefix + slickFsRandomSuffix());
    try {
      slickStdFilesystemNode.mkdirSync(candidate, { mode: 0o700 });
      return slickFsAbsolutePath(candidate);
    } catch (error) {
      if (slickFsIsExists(error)) continue;
      throw error;
    }
  }
  const error = new Error("file exists");
  error.code = "EEXIST";
  error.errno = 17;
  throw error;
}

function slickFsCloseTemp(state) {
  if (state.closed) return null;
  const cleaned = slickFsClean(state.path);
  if (slickFsUnsafeCleanup(cleaned)) return "refusing to remove unsafe cleanup target";
  try {
    slickStdFilesystemNode.rmSync(cleaned, { recursive: true });
  } catch (error) {
    if (!slickFsIsNotFound(error)) return slickFsIoMessage("remove", state.path, error);
  }
  state.closed = true;
  return null;
}

export async function slickNatFSExists(context, args) {
  const path = slickArgString(args, 0);
  try {
    slickStdFilesystemNode.statSync(path);
    return slickOk(true);
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    if (slickFsIsNotFound(error)) return slickOk(false);
    return slickErr(slickFsFailure("Exists", path, slickFsIoMessage("stat", path, error)));
  }
}

export async function slickNatFSReadText(context, args) {
  const path = slickArgString(args, 0);
  if (!slickFsCancellable(context)) return slickFsReadTextPlain(path);
  let mode;
  try {
    mode = slickFsPathMode(path, false);
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    if (error && error.slickFsPlain) {
      return slickErr(slickFsFailure("ReadText", path, error.message));
    }
    return slickErr(slickFsFailure("ReadText", path, slickFsIoMessage("stat", path, error)));
  }
  let fd = null;
  try {
    try {
      fd = await slickFsOpenContext(context, path, "r", 0, mode.pipe, false);
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      if (error && error.slickFsCancelled) {
        return slickErr(slickFsFailure("ReadText", path, "operation cancelled"));
      }
      return slickErr(slickFsFailure("ReadText", path, slickFsIoMessage("open", path, error)));
    }
    let bytes;
    try {
      // The descriptor stays owned here so the finally closes it exactly once;
      // only the cancellation path transfers ownership, having closed it itself.
      bytes = await slickFsCallContext(context, fd, function () { return slickFsReadFileAsync(fd); });
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      if (error && error.slickFsCancelled) {
        fd = null;
        return slickErr(slickFsFailure("ReadText", path, "operation cancelled"));
      }
      return slickErr(slickFsFailure("ReadText", path, slickFsIoMessage("read", path, error)));
    }
    try {
      return slickOk(slickFsDecodeText(bytes));
    } catch (error) {
      return slickErr(slickFsFailure("ReadText", path, "invalid UTF-8"));
    }
  } finally {
    if (fd !== null) {
      try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    }
  }
}

export async function slickNatFSWriteText(context, args) {
  const path = slickArgString(args, 0);
  const contents = slickArgString(args, 1);
  if (!slickFsCancellable(context)) return slickFsWriteTextPlain(path, contents);
  let mode;
  try {
    mode = slickFsPathMode(path, true);
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    if (error && error.slickFsPlain) {
      return slickErr(slickFsFailure("WriteText", path, error.message));
    }
    return slickErr(slickFsFailure("WriteText", path, slickFsIoMessage("stat", path, error)));
  }
  let fd = null;
  try {
    try {
      fd = await slickFsOpenContext(context, path, "w", 0o666, mode.pipe, true);
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      if (error && error.slickFsCancelled) {
        return slickErr(slickFsFailure("WriteText", path, "operation cancelled"));
      }
      return slickErr(slickFsFailure("WriteText", path, slickFsIoMessage("open", path, error)));
    }
    try {
      await slickFsCallContext(context, fd, function () { return slickFsWriteFileAsync(fd, contents); });
    } catch (error) {
      if (slickFsHostErr(error)) throw error;
      if (error && error.slickFsCancelled) {
        fd = null;
        return slickErr(slickFsFailure("WriteText", path, "operation cancelled"));
      }
      return slickErr(slickFsFailure("WriteText", path, slickFsIoMessage("write", path, error)));
    }
    return slickOk(null);
  } finally {
    if (fd !== null) {
      try { slickStdFilesystemNode.closeSync(fd); } catch (error) {}
    }
  }
}

export async function slickNatFSMkdir(context, args) {
  const path = slickArgString(args, 0);
  try {
    slickStdFilesystemNode.mkdirSync(path, { recursive: true, mode: 0o777 });
    return slickOk(null);
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    return slickErr(slickFsFailure("CreateDirectoryAll", path, slickFsIoMessage("mkdir", path, error)));
  }
}

export async function slickNatFSRemove(context, args) {
  const path = slickArgString(args, 0);
  try {
    slickStdFilesystemNode.unlinkSync(path);
    return slickOk(null);
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    if (error && (error.code === "EISDIR" || error.code === "EPERM")) {
      try {
        slickStdFilesystemNode.rmdirSync(path);
        return slickOk(null);
      } catch (inner) {
        if (slickFsHostErr(inner)) throw inner;
        return slickErr(slickFsFailure("Remove", path, slickFsIoMessage("remove", path, inner)));
      }
    }
    return slickErr(slickFsFailure("Remove", path, slickFsIoMessage("remove", path, error)));
  }
}

export async function slickNatFSReadDir(context, args) {
  const path = slickArgString(args, 0);
  let entries;
  try {
    entries = slickStdFilesystemNode.readdirSync(path, { withFileTypes: true });
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    return slickErr(slickFsFailure("ReadDirectory", path, slickFsIoMessage("open", path, error)));
  }
  const listed = [];
  for (const entry of entries) {
    listed.push({
      name: entry.name,
      isDirectory: typeof entry.isDirectory === "function" ? entry.isDirectory() : false,
    });
  }
  listed.sort(function (left, right) {
    // os.ReadDir sorts by name in UTF-8 byte order, which differs from the
    // UTF-16 order JavaScript string comparison uses.
    return Buffer.compare(Buffer.from(left.name, "utf8"), Buffer.from(right.name, "utf8"));
  });
  const values = [];
  for (const entry of listed) {
    values.push(slickStdObject("std.fs.Entry", [
      ["Name", entry.name],
      ["Path", slickFsJoin(path, entry.name)],
      ["IsDirectory", entry.isDirectory],
    ]));
  }
  return slickOk(values);
}

export async function slickNatFSTmp(context, args) {
  const prefix = slickArgString(args, 0);
  if (prefix.includes("/")) {
    return slickErr(slickFsFailure("CreateTemporaryDirectory", prefix, "mkdirtemp " + prefix + "*: pattern contains path separator"));
  }
  try {
    const created = slickFsCreateTemp(prefix);
    return slickOk(slickResourceObject(
      "std.fs.TemporaryDirectory",
      [["Path", created]],
      slickResourceNew({ path: created, closed: false }),
    ));
  } catch (error) {
    if (slickFsHostErr(error)) throw error;
    return slickErr(slickFsFailure("CreateTemporaryDirectory", prefix, slickFsIoMessage("mkdir", prefix, error)));
  }
}

export async function slickNatFSTmpClose(context, args) {
  const path = slickFsSelfPath(slickArg(args, 0));
  const resource = slickArgResource(args, 0);
  if (resource === null || resource.state == null) {
    throw SlickFailure.slick(slickFsFailure("Close", path, "temporary directory is not owned by this resource"));
  }
  const message = slickFsCloseTemp(resource.state);
  if (message !== null) throw SlickFailure.slick(slickFsFailure("Close", path, message));
  return null;
}
`
