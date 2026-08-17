package compiler

var bunStdEnvironment = bunStdFamily{
	family: runtimeFamilyEnvironment,
	module: bunStdEnvironmentModule,
	functions: map[runtimeOperationID]string{
		nativeStdEnvGet:   "slickNatEnvGet",
		nativeStdEnvSet:   "slickNatEnvSet",
		nativeStdEnvUnset: "slickNatEnvUnset",
	},
}

// bunStdEnvironmentModule implements std.env. Get returns the value or an
// absent optional, matching os.LookupEnv. Set and Unset record a compiler-owned
// overlay via slickEnvironmentRecord instead of mutating process.env. Get uses
// slickEnvironmentRead so overlay assignments are visible to HTTP, process,
// and every other family. Name validation matches syscall.Setenv; Unset
// reports std.env.Failure for an empty name or a name carrying = or NUL.
const bunStdEnvironmentModule = `export async function slickNatEnvGet(context, args) {
  const name = slickArgString(args, 0);
  const value = slickEnvironmentRead(name);
  return value === null || value === undefined ? slickAbsent : slickOptional(value);
}

export async function slickNatEnvSet(context, args) {
  const name = slickArgString(args, 0);
  const value = slickArgString(args, 1);
  if (slickEnvNameInvalid(name) || value.includes("\0")) {
    return slickErr(slickEnvFailure("Set", name, "setenv: invalid argument"));
  }
  slickEnvironmentRecord(name, value);
  return slickOk(null);
}

export async function slickNatEnvUnset(context, args) {
  const name = slickArgString(args, 0);
  if (slickEnvNameInvalid(name)) {
    return slickErr(slickEnvFailure("Unset", name, "unsetenv: invalid argument"));
  }
  slickEnvironmentRecord(name, null);
  return slickOk(null);
}

function slickEnvNameInvalid(name) {
  return name.length === 0 || name.includes("=") || name.includes("\0");
}

function slickEnvFailure(operation, name, message) {
  return slickStdObject("std.env.Failure", [
    ["Operation", operation],
    ["Name", name],
    ["Message", message],
  ]);
}
`
