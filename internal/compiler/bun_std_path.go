package compiler

var bunStdPath = bunStdFamily{
	family: runtimeFamilyPath,
	module: bunStdPathModule,
	functions: map[runtimeOperationID]string{
		nativeStdPathBase:       "slickNatPathBase",
		nativeStdPathClean:      "slickNatPathClean",
		nativeStdPathDirectory:  "slickNatPathDirectory",
		nativeStdPathExtension:  "slickNatPathExtension",
		nativeStdPathIsAbsolute: "slickNatPathIsAbsolute",
		nativeStdPathJoin:       "slickNatPathJoin",
	},
}

// bunStdPathModule implements std.path with Go path/filepath semantics on
// Linux: '/' is the only separator, volume names are empty, and Clean
// normalizes separators and dot segments. The algorithms mirror the
// interpreter's filepath.Clean/Base/Dir/Ext/IsAbs/Join exactly, so
// observable output, dot-segment handling, and the empty-result "." match Go.
const bunStdPathModule = `export async function slickNatPathClean(context, args) {
  return slickPathClean(slickArgString(args, 0));
}

export async function slickNatPathBase(context, args) {
  return slickPathBase(slickArgString(args, 0));
}

export async function slickNatPathDirectory(context, args) {
  return slickPathDir(slickArgString(args, 0));
}

export async function slickNatPathExtension(context, args) {
  const extension = slickPathExt(slickArgString(args, 0));
  if (extension.length === 0) return slickAbsent;
  return slickOptional(extension);
}

export async function slickNatPathIsAbsolute(context, args) {
  return slickPathIsAbs(slickArgString(args, 0));
}

export async function slickNatPathJoin(context, args) {
  const values = slickArgValues(args, 0);
  const parts = [];
  for (const value of values) {
    if (typeof value !== "string") {
      throw SlickFailure.host("std.path.Join part is " + slickTypeName(value) + " and not string");
    }
    parts.push(value);
  }
  return slickPathJoin(parts);
}

function slickPathClean(path) {
  if (path.length === 0) return ".";
  const rooted = path.charCodeAt(0) === 47;
  const n = path.length;
  let out = "";
  let r = 0;
  let dotdot = 0;
  if (rooted) {
    out = "/";
    r = 1;
    dotdot = 1;
  }
  while (r < n) {
    const code = path.charCodeAt(r);
    if (code === 47) {
      r += 1;
    } else if (code === 46 && (r + 1 === n || path.charCodeAt(r + 1) === 47)) {
      r += 1;
    } else if (code === 46 && r + 1 < n && path.charCodeAt(r + 1) === 46 && (r + 2 === n || path.charCodeAt(r + 2) === 47)) {
      r += 2;
      if (out.length > dotdot) {
        let w = out.length - 1;
        while (w > dotdot && out.charCodeAt(w) !== 47) w -= 1;
        out = out.slice(0, w);
      } else if (!rooted) {
        if (out.length !== 0) out += "/";
        out += "..";
        dotdot = out.length;
      }
    } else {
      if ((rooted && out.length !== 1) || (!rooted && out.length !== 0)) out += "/";
      while (r < n && path.charCodeAt(r) !== 47) {
        out += path.charAt(r);
        r += 1;
      }
    }
  }
  if (out.length === 0) out = ".";
  return out;
}

function slickPathBase(path) {
  if (path.length === 0) return ".";
  let end = path.length;
  while (end > 0 && path.charCodeAt(end - 1) === 47) end -= 1;
  let i = end - 1;
  while (i >= 0 && path.charCodeAt(i) !== 47) i -= 1;
  const start = i + 1;
  if (start >= end) return "/";
  return path.slice(start, end);
}

function slickPathDir(path) {
  let i = path.length - 1;
  while (i >= 0 && path.charCodeAt(i) !== 47) i -= 1;
  return slickPathClean(path.slice(0, i + 1));
}

function slickPathExt(path) {
  let i = path.length - 1;
  while (i >= 0 && path.charCodeAt(i) !== 47) {
    if (path.charCodeAt(i) === 46) return path.slice(i);
    i -= 1;
  }
  return "";
}

function slickPathIsAbs(path) {
  return path.length > 0 && path.charCodeAt(0) === 47;
}

function slickPathJoin(parts) {
  let start = -1;
  for (let i = 0; i < parts.length; i += 1) {
    if (parts[i].length !== 0) {
      start = i;
      break;
    }
  }
  if (start < 0) return "";
  let joined = "";
  for (let i = start; i < parts.length; i += 1) {
    if (i > start) joined += "/";
    joined += parts[i];
  }
  return slickPathClean(joined);
}
`
