package compiler

// bunRuntimeModule is the compiler-owned Bun runtime. It is versioned with the
// backend, bundled into the executable, and is the only implementation of Slick
// value, failure, cleanup, resource-handle, and structured-task behavior the
// generated module may rely on. JavaScript truthiness, `null`, `undefined`,
// object identity, and Promise rejection never carry Slick meaning.
const bunRuntimeModule = `import { isMainThread, parentPort, workerData, Worker } from "node:worker_threads";

export class SlickTuple {
  constructor(values) { this.values = values; }
}

export class SlickRange {
  constructor(start, end) { this.start = start; this.end = end > start ? end : start; }
}

export class SlickMap {
  constructor(entries) { this.entries = entries; }
}

export class SlickBuffer {
  constructor(values) { this.values = values; }
}

export class SlickOptional {
  constructor(present, value) { this.present = present; this.value = value; }
}

export const slickAbsent = new SlickOptional(false, null);

export class SlickResult {
  constructor(ok, value) { this.ok = ok; this.value = value; }
}

export class SlickObject {
  // The message slot carries a shorthand error constructor's text. It is
  // failure metadata, not a declared field: it never participates in field
  // access or structural equality.
  constructor(typeName, fields, resource, message = "") {
    this.typeName = typeName;
    this.fields = fields;
    this.resource = resource;
    this.message = message;
  }
}

export class SlickUnion {
  constructor(typeName, variant, tag, fields) {
    this.typeName = typeName;
    this.variant = variant;
    this.tag = tag;
    this.fields = fields;
  }
}

export class SlickCallable {
  constructor(target, captures) { this.target = target; this.captures = captures; }
}

export class SlickEnumerate {
  constructor(source) { this.source = source; }
}

export class SlickZip {
  constructor(sources) { this.sources = sources; }
}

export class SlickFailure {
  constructor(kind, value, message) {
    this.kind = kind;
    this.value = value;
    this.message = message;
    this.suppressed = [];
  }

  static slick(value) { return new SlickFailure("slick", value, ""); }

  static host(message) { return new SlickFailure("host", null, message); }

  static cancelled() { return new SlickFailure("cancelled", null, ""); }
}

export function slickSuppress(primary, secondary) {
  primary.suppressed.push(secondary);
  return primary;
}

export function slickAsFailure(error) {
  if (error instanceof SlickFailure) return error;
  return SlickFailure.host("host error: " + String(error));
}

export function slickFailureFromValue(value) {
  return SlickFailure.slick(value);
}

// Resource handles are opaque and scoped to one runtime generation. A handle
// minted by another process, worker, or generation never resolves.
const slickGeneration = ((Date.now() & 0xffff) << 16) | (Math.floor(Math.random() * 0xffff) + 1);
const slickResources = new Map();
let slickResourceSequence = 0;

export function slickCreateHandle(state) {
  slickResourceSequence += 1;
  const handle = { generation: slickGeneration, id: slickResourceSequence };
  slickResources.set(handle.id, state);
  return handle;
}

export function slickResolveHandle(handle) {
  if (handle === null || typeof handle !== "object" || handle.generation !== slickGeneration) {
    throw SlickFailure.host("resource handle does not belong to this runtime generation");
  }
  if (!slickResources.has(handle.id)) throw SlickFailure.host("resource handle is closed");
  return slickResources.get(handle.id);
}

export function slickReleaseHandle(handle) {
  if (handle !== null && typeof handle === "object" && handle.generation === slickGeneration) {
    slickResources.delete(handle.id);
  }
}

// A context carries every cancellation flag its ancestors own, so a worker child
// observes an outer scope's cancellation as well as its own scope's.
export class SlickContext {
  constructor(buffers) {
    this.buffers = buffers;
    this.flags = buffers.map((buffer) => new Int32Array(buffer));
  }

  child(buffer) { return new SlickContext(this.buffers.concat([buffer])); }

  cancelled() {
    for (const flag of this.flags) {
      if (Atomics.load(flag, 0) === 1) return true;
    }
    return false;
  }
}

export function slickCheckCancelled(context) {
  if (context.cancelled()) throw SlickFailure.cancelled();
}

// Cleanup observes no cancellation: a cancelled scope still runs Close.
export function slickCleanupContext(context) {
  return context.flags.length === 0 ? context : new SlickContext([]);
}

let slickProgram = null;

export function slickInstall(moduleUrl, program) {
  slickProgram = { moduleUrl, ...program };
}

export function slickTypes() {
  return slickProgram === null ? [] : slickProgram.types;
}

export async function slickCallCallable(context, value, args) {
  if (!(value instanceof SlickCallable)) throw SlickFailure.host("value is not callable");
  const target = slickProgram.callables[value.target];
  if (target === undefined) throw SlickFailure.host("unknown callable target " + value.target);
  return await target(context, value.captures, args);
}

export async function slickCallMethod(context, receiver, method, args) {
  const name = slickMethodName(method);
  const builtin = slickBuiltinMethod(receiver, name, args);
  if (builtin !== slickNoBuiltin) return builtin;
  const typeName = slickTypeName(receiver);
  const target = slickProgram.methods[typeName + "." + name];
  if (target === undefined) {
    throw SlickFailure.host("method " + method + " is unavailable on " + typeName);
  }
  return await target(context, receiver, args);
}

function slickMethodName(method) {
  const index = method.lastIndexOf(".");
  return index < 0 ? method : method.slice(index + 1);
}

export async function slickInvoke(context, request) {
  if (request.kind === "callable") {
    return await slickCallCallable(context, request.callee, request.arguments);
  }
  if (request.kind === "method") {
    return await slickCallMethod(context, request.receiver, request.target, request.arguments);
  }
  if (request.kind === "operation") {
    const operation = slickProgram.operations[request.target];
    if (operation === undefined) {
      throw SlickFailure.host("unknown standard-library operation " + request.target);
    }
    return await operation(context, request.arguments);
  }
  const target = slickProgram.functions[request.target];
  if (target === undefined) throw SlickFailure.host("unknown function " + request.target);
  return await target(context, null, request.arguments);
}

// A task scope owns every child it launches: cancellation is a shared flag each
// child observes cooperatively, and no scope exits before every child is joined
// and its worker terminated.
export class SlickTaskScope {
  constructor(parent) {
    this.cancellation = new SharedArrayBuffer(4);
    this.flag = new Int32Array(this.cancellation);
    this.context = parent.child(this.cancellation);
    this.children = [];
  }

  launch(request) {
    const child = slickSpawn(this.context.buffers, request);
    this.children.push(child);
    return this.children.length - 1;
  }

  async awaitTask(index) {
    const child = this.children[index];
    if (child === undefined) throw SlickFailure.host("unknown task");
    if (child.awaited) throw SlickFailure.host("task was already awaited");
    child.awaited = true;
    return await slickJoin(child);
  }

  async finish() {
    if (this.children.length !== 0) Atomics.store(this.flag, 0, 1);
    let failure = null;
    for (const child of this.children) {
      if (child.awaited) continue;
      child.awaited = true;
      try {
        await slickJoin(child);
      } catch (error) {
        const childFailure = slickAsFailure(error);
        if (childFailure.kind === "cancelled") continue;
        failure = failure === null ? childFailure : slickSuppress(failure, childFailure);
      }
    }
    for (const child of this.children) await slickTerminate(child);
    return failure;
  }
}

function slickSpawn(cancellations, request) {
  const encoded = slickEncodeRequest(request);
  let worker;
  try {
    worker = new Worker(slickProgram.moduleUrl, {
      workerData: { request: encoded, cancellations, environment: slickEnvironmentChanges() },
    });
  } catch (error) {
    throw SlickFailure.host("launch child task: " + String(error));
  }
  const child = { worker, awaited: false, settled: null, terminated: false };
  child.promise = new Promise((resolve) => {
    worker.on("message", (message) => {
      // A worker reaches an owner-held resource through this channel, so a
      // task-safe handle such as a database works from any scope.
      if (message !== null && typeof message === "object" && message.rpc !== undefined) {
        slickOwnerServe(worker, message.rpc);
        return;
      }
      resolve({ message });
    });
    worker.on("error", (error) => resolve({ error: SlickFailure.host("child task failed: " + String(error)) }));
    worker.on("exit", (code) => resolve({ error: SlickFailure.host("child task exited with status " + code) }));
  });
  return child;
}

// Owner-held resources live in the process that created them. Every kind
// registers one handler, and a worker call is forwarded to that handler.
const slickOwners = new Map();
let slickOwnerSequence = 1;
const slickOwnerPending = new Map();
let slickOwnerRequestSequence = 1;

export class SlickOwnedResource {
  constructor(kind, id) { this.kind = kind; this.id = id; }
}

export function slickOwnerRegister(kind, handler) {
  slickOwners.set(kind, handler);
}

export function slickOwnerCreate(kind, state) {
  const owner = slickOwners.get(kind);
  if (owner === undefined) throw SlickFailure.host("no owner registered for " + kind);
  slickOwnerSequence += 1;
  owner.states.set(slickOwnerSequence, state);
  return new SlickOwnedResource(kind, slickOwnerSequence);
}

export function slickOwnerState(handle) {
  const owner = slickOwners.get(handle.kind);
  return owner === undefined ? undefined : owner.states.get(handle.id);
}

export function slickOwnerRelease(handle) {
  const owner = slickOwners.get(handle.kind);
  if (owner !== undefined) owner.states.delete(handle.id);
}

// slickOwnerCall runs a method against an owner-held resource: directly when the
// caller owns it, and over the worker channel otherwise. The caller's
// cancellation buffers travel with the request so the owning thread can observe
// cancellation of work it performs on the caller's behalf.
export async function slickOwnerCall(handle, method, args, context) {
  const buffers = context === undefined ? [] : context.buffers;
  const owner = slickOwners.get(handle.kind);
  // The owning thread answers even when the state is gone, so a released handle
  // reports the family's documented closed result instead of a host fault.
  if (owner !== undefined && (owner.states.has(handle.id) || isMainThread)) {
    return await owner.invoke(handle, method, args, new SlickContext(buffers));
  }
  if (isMainThread) throw SlickFailure.host("resource " + handle.kind + " is not available");
  return await slickOwnerRequest({
    kind: handle.kind, handle: handle.id, method,
    args: args.map(slickEncode), cancellations: buffers,
  });
}

// A resource opened inside a task worker must outlive that worker, so creation
// runs on the owning thread and only the opaque handle travels back.
export async function slickOwnerFactory(kind, factory, args) {
  if (isMainThread) {
    const owner = slickOwners.get(kind);
    if (owner === undefined) throw SlickFailure.host("no owner registered for " + kind);
    return await owner.create(factory, args);
  }
  return await slickOwnerRequest({ kind, factory, args: args.map(slickEncode) });
}

async function slickOwnerRequest(payload) {
  const id = slickOwnerRequestSequence++;
  const reply = new Promise((resolve) => slickOwnerPending.set(id, resolve));
  parentPort.postMessage({ rpc: { id, ...payload } });
  const answer = await reply;
  if (answer.ok) return slickDecode(answer.value);
  throw slickDecodeFailure(answer.failure);
}

async function slickOwnerServe(worker, request) {
  let answer;
  try {
    const args = request.args.map(slickDecode);
    const value = request.factory !== undefined
      ? await slickOwnerFactory(request.kind, request.factory, args)
      : await slickOwnerCall(
        new SlickOwnedResource(request.kind, request.handle), request.method, args,
        new SlickContext(request.cancellations === undefined ? [] : request.cancellations),
      );
    answer = { id: request.id, ok: true, value: slickEncode(value) };
  } catch (error) {
    answer = { id: request.id, ok: false, failure: slickEncodeFailure(slickAsFailure(error)) };
  }
  worker.postMessage({ rpcReply: answer });
}

// A worker is terminated exactly once: repeating the request never settles.
async function slickTerminate(child) {
  if (child.terminated) return;
  child.terminated = true;
  await child.worker.terminate();
}

async function slickJoin(child) {
  if (child.settled === null) child.settled = await child.promise;
  await slickTerminate(child);
  if (child.settled.error !== undefined) throw child.settled.error;
  const message = child.settled.message;
  slickEnvironmentMerge(message.environment);
  if (message.ok) return slickDecode(message.value);
  throw slickDecodeFailure(message.failure);
}

// A worker consumes owner replies on its own port; every other message is a
// launch payload the runtime already handled.
export function slickOwnerListen() {
  if (isMainThread) return;
  parentPort.on("message", (message) => {
    if (message === null || typeof message !== "object" || message.rpcReply === undefined) return;
    const resolve = slickOwnerPending.get(message.rpcReply.id);
    if (resolve === undefined) return;
    slickOwnerPending.delete(message.rpcReply.id);
    resolve(message.rpcReply);
  });
}

// Only task-safe values cross a worker boundary: mutable buffers and opaque
// host resources never do.
export function slickEncode(value) {
  if (value === null) return { k: "null" };
  if (typeof value === "boolean") return { k: "bool", v: value };
  if (typeof value === "bigint") return { k: "int", v: value };
  if (typeof value === "number") return { k: "float", v: value };
  if (typeof value === "string") return { k: "string", v: value };
  if (value instanceof Uint8Array) return { k: "bytes", v: new Uint8Array(value) };
  if (Array.isArray(value)) return { k: "array", v: value.map(slickEncode) };
  if (value instanceof SlickTuple) return { k: "tuple", v: value.values.map(slickEncode) };
  if (value instanceof SlickRange) return { k: "range", start: value.start, end: value.end };
  if (value instanceof SlickMap) {
    return { k: "map", v: value.entries.map(([key, entry]) => [slickEncode(key), slickEncode(entry)]) };
  }
  if (value instanceof SlickOptional) {
    return { k: "optional", present: value.present, v: value.present ? slickEncode(value.value) : null };
  }
  if (value instanceof SlickResult) return { k: "result", ok: value.ok, v: slickEncode(value.value) };
  // A bare owner handle crosses a boundary on its own when a factory creates a
  // resource for another thread; only the opaque identity travels.
  if (value instanceof SlickOwnedResource) return { k: "owned", kind: value.kind, id: value.id };
  if (value instanceof SlickObject) {
    // An owner-held resource is task-safe: only its opaque owner identity
    // crosses the boundary, never a host object.
    if (value.resource instanceof SlickOwnedResource) {
      const fields = [];
      for (const [name, field] of value.fields) fields.push([name, slickEncode(field)]);
      return {
        k: "object", type: value.typeName, fields, message: value.message,
        owned: { kind: value.resource.kind, id: value.resource.id },
      };
    }
    if (value.resource !== null) {
      throw SlickFailure.host("resource " + value.typeName + " is not task-safe");
    }
    const fields = [];
    for (const [name, field] of value.fields) fields.push([name, slickEncode(field)]);
    return { k: "object", type: value.typeName, fields, message: value.message };
  }
  if (value instanceof SlickUnion) {
    return {
      k: "union", type: value.typeName, variant: value.variant, tag: value.tag,
      fields: value.fields.map(slickEncode),
    };
  }
  if (value instanceof SlickCallable) {
    return { k: "callable", target: value.target, captures: value.captures.map(slickEncode) };
  }
  if (value instanceof SlickEnumerate) return { k: "enumerate", v: slickEncode(value.source) };
  if (value instanceof SlickZip) return { k: "zip", v: value.sources.map(slickEncode) };
  if (value instanceof SlickBuffer) throw SlickFailure.host("Buffer is not task-safe");
  throw SlickFailure.host("value is not task-safe");
}

export function slickDecode(encoded) {
  switch (encoded.k) {
    case "null": return null;
    case "owned": return new SlickOwnedResource(encoded.kind, encoded.id);
    case "bool": return encoded.v;
    case "int": return BigInt(encoded.v);
    case "float": return encoded.v;
    case "string": return encoded.v;
    case "bytes": return new Uint8Array(encoded.v);
    case "array": return encoded.v.map(slickDecode);
    case "tuple": return new SlickTuple(encoded.v.map(slickDecode));
    case "range": return new SlickRange(BigInt(encoded.start), BigInt(encoded.end));
    case "map": return new SlickMap(encoded.v.map(([key, value]) => [slickDecode(key), slickDecode(value)]));
    case "optional": return encoded.present ? new SlickOptional(true, slickDecode(encoded.v)) : slickAbsent;
    case "result": return new SlickResult(encoded.ok, slickDecode(encoded.v));
    case "object": {
      const fields = new Map();
      for (const [name, value] of encoded.fields) fields.set(name, slickDecode(value));
      const owned = encoded.owned === undefined
        ? null
        : new SlickOwnedResource(encoded.owned.kind, encoded.owned.id);
      return new SlickObject(encoded.type, fields, owned, encoded.message);
    }
    case "union":
      return new SlickUnion(encoded.type, encoded.variant, encoded.tag, encoded.fields.map(slickDecode));
    case "callable": return new SlickCallable(encoded.target, encoded.captures.map(slickDecode));
    case "enumerate": return new SlickEnumerate(slickDecode(encoded.v));
    case "zip": return new SlickZip(encoded.v.map(slickDecode));
    default: throw SlickFailure.host("unknown encoded value " + encoded.k);
  }
}

function slickEncodeRequest(request) {
  const encoded = { kind: request.kind, target: request.target, arguments: request.arguments.map(slickEncode) };
  if (request.kind === "method") encoded.receiver = slickEncode(request.receiver);
  if (request.kind === "callable") encoded.callee = slickEncode(request.callee);
  return encoded;
}

function slickDecodeRequest(encoded) {
  const request = { kind: encoded.kind, target: encoded.target, arguments: encoded.arguments.map(slickDecode) };
  if (encoded.kind === "method") request.receiver = slickDecode(encoded.receiver);
  if (encoded.kind === "callable") request.callee = slickDecode(encoded.callee);
  return request;
}

function slickEncodeFailure(failure) {
  return {
    kind: failure.kind,
    message: failure.message,
    value: failure.kind === "slick" ? slickEncode(failure.value) : null,
    suppressed: failure.suppressed.map(slickEncodeFailure),
  };
}

function slickDecodeFailure(encoded) {
  const failure = new SlickFailure(
    encoded.kind,
    encoded.kind === "slick" ? slickDecode(encoded.value) : null,
    encoded.message,
  );
  failure.suppressed = encoded.suppressed.map(slickDecodeFailure);
  return failure;
}

export function slickWrapInt(value) {
  return BigInt.asIntN(64, value);
}

export function slickTruth(value) {
  if (typeof value !== "boolean") throw SlickFailure.host("condition is not bool");
  return value;
}

export function slickUnary(operator, value) {
  if (operator === "!" && typeof value === "boolean") return !value;
  if (operator === "-" && typeof value === "bigint") return slickWrapInt(-value);
  if (operator === "-" && typeof value === "number") return -value;
  throw SlickFailure.host("invalid unary operator " + operator);
}

export function slickBinary(operator, left, right) {
  if (operator === "==") return slickEqual(left, right);
  if (operator === "!=") return !slickEqual(left, right);
  const ints = typeof left === "bigint" && typeof right === "bigint";
  const floats = typeof left === "number" && typeof right === "number";
  const strings = typeof left === "string" && typeof right === "string";
  switch (operator) {
    case "+":
      if (ints) return slickWrapInt(left + right);
      if (floats) return left + right;
      if (strings) return left + right;
      break;
    case "-":
      if (ints) return slickWrapInt(left - right);
      if (floats) return left - right;
      break;
    case "*":
      if (ints) return slickWrapInt(left * right);
      if (floats) return left * right;
      break;
    case "<":
      if (ints || floats || strings) return left < right;
      break;
    case "<=":
      if (ints || floats || strings) return left <= right;
      break;
    case ">":
      if (ints || floats || strings) return left > right;
      break;
    case ">=":
      if (ints || floats || strings) return left >= right;
      break;
    default:
      break;
  }
  throw SlickFailure.host("invalid binary operator " + operator);
}

export function slickEqual(left, right) {
  // An absent Optional and a null literal are both absent, and any other value
  // is its own payload, so T? compares with null, with T, and with T?.
  if (left instanceof SlickOptional || right instanceof SlickOptional) {
    const leftPresent = left instanceof SlickOptional ? left.present : left !== null;
    const rightPresent = right instanceof SlickOptional ? right.present : right !== null;
    if (leftPresent !== rightPresent) return false;
    if (!leftPresent) return true;
    return slickEqual(
      left instanceof SlickOptional ? left.value : left,
      right instanceof SlickOptional ? right.value : right,
    );
  }
  if (left === null || right === null) return left === right;
  if (typeof left === "boolean" || typeof left === "bigint" || typeof left === "string") return left === right;
  if (typeof left === "number") return typeof right === "number" && left === right;
  if (left instanceof Uint8Array) {
    if (!(right instanceof Uint8Array) || left.length !== right.length) return false;
    for (let index = 0; index < left.length; index += 1) {
      if (left[index] !== right[index]) return false;
    }
    return true;
  }
  if (Array.isArray(left)) return Array.isArray(right) && slickEqualValues(left, right);
  if (left instanceof SlickTuple) return right instanceof SlickTuple && slickEqualValues(left.values, right.values);
  if (left instanceof SlickRange) {
    return right instanceof SlickRange && left.start === right.start && left.end === right.end;
  }
  if (left instanceof SlickMap) {
    if (!(right instanceof SlickMap) || left.entries.length !== right.entries.length) return false;
    for (let index = 0; index < left.entries.length; index += 1) {
      if (!slickEqual(left.entries[index][0], right.entries[index][0])) return false;
      if (!slickEqual(left.entries[index][1], right.entries[index][1])) return false;
    }
    return true;
  }
  if (left instanceof SlickBuffer) return left === right;
  if (left instanceof SlickResult) {
    return right instanceof SlickResult && left.ok === right.ok && slickEqual(left.value, right.value);
  }
  if (left instanceof SlickObject) {
    if (!(right instanceof SlickObject)) return false;
    if (left.resource !== null || right.resource !== null) {
      if (left.resource instanceof SlickOwnedResource || right.resource instanceof SlickOwnedResource) {
        // An owner handle survives a worker boundary as a copy, so the opaque
        // owner identity, not the JS object, decides equality.
        return left.resource instanceof SlickOwnedResource && right.resource instanceof SlickOwnedResource
          && left.resource.kind === right.resource.kind && left.resource.id === right.resource.id;
      }
      return left.resource !== null && left.resource === right.resource;
    }
    if (left.typeName !== right.typeName || left.fields.size !== right.fields.size) return false;
    for (const [name, value] of left.fields) {
      if (!right.fields.has(name) || !slickEqual(value, right.fields.get(name))) return false;
    }
    return true;
  }
  if (left instanceof SlickUnion) {
    return right instanceof SlickUnion && left.typeName === right.typeName && left.tag === right.tag &&
      slickEqualValues(left.fields, right.fields);
  }
  return false;
}

function slickEqualValues(left, right) {
  if (left.length !== right.length) return false;
  for (let index = 0; index < left.length; index += 1) {
    if (!slickEqual(left[index], right[index])) return false;
  }
  return true;
}

export function slickMap(entries) {
  const ordered = [];
  for (const [key, value] of entries) {
    const existing = ordered.find((entry) => slickEqual(entry[0], key));
    if (existing === undefined) ordered.push([key, value]);
    else existing[1] = value;
  }
  return new SlickMap(ordered);
}

export function slickRange(start, end) {
  if (typeof start !== "bigint" || typeof end !== "bigint") throw SlickFailure.host("range bounds are not int");
  return new SlickRange(start, end);
}

export function slickOptional(value) {
  if (value === undefined) return slickAbsent;
  if (value instanceof SlickOptional) return value;
  return new SlickOptional(true, value);
}

export function slickConvert(value, conversion) {
  if (conversion === "") return value;
  if (conversion === "optional_inject") {
    if (value instanceof SlickOptional) return value;
    return value === null ? slickAbsent : new SlickOptional(true, value);
  }
  if (conversion === "optional_unwrap_proven") {
    if (!(value instanceof SlickOptional)) return value;
    if (!value.present) throw SlickFailure.host("proved Optional value is absent");
    return value.value;
  }
  throw SlickFailure.host("unknown storage conversion " + conversion);
}

export function* slickIter(value) {
  if (Array.isArray(value)) {
    yield* value;
    return;
  }
  if (value instanceof SlickTuple) {
    yield* value.values;
    return;
  }
  if (value instanceof SlickRange) {
    for (let current = value.start; current < value.end; current += 1n) yield current;
    return;
  }
  if (value instanceof SlickMap) {
    for (const [key, entry] of value.entries) yield new SlickTuple([key, entry]);
    return;
  }
  if (value instanceof SlickEnumerate) {
    let index = 0n;
    for (const item of slickIter(value.source)) {
      yield new SlickTuple([index, item]);
      index += 1n;
    }
    return;
  }
  if (value instanceof SlickZip) {
    const sources = value.sources.map((source) => slickIter(source));
    for (;;) {
      const items = [];
      for (const source of sources) {
        const next = source.next();
        if (next.done) return;
        items.push(next.value);
      }
      yield new SlickTuple(items);
    }
  }
  throw SlickFailure.host("value is not iterable");
}

export function slickTupleItem(value, index) {
  if (value instanceof SlickTuple) {
    return index < value.values.length ? value.values[index] : null;
  }
  return null;
}

export function slickField(value, name) {
  // Narrowing proves an optional receiver present before a field read is
  // allowed, so the payload is the real receiver.
  const receiver = value instanceof SlickOptional && value.present ? value.value : value;
  if (receiver instanceof SlickObject) {
    if (!receiver.fields.has(name)) throw SlickFailure.host("object has no field " + name);
    return receiver.fields.get(name);
  }
  if (receiver instanceof SlickOptional) throw SlickFailure.host("null has no field " + name);
  throw SlickFailure.host("value has no field " + name);
}

export function slickPath(value, path) {
  let current = value;
  for (const name of path) current = slickField(current, name);
  return current;
}

export const slickNoBuiltin = Symbol("slick.no-builtin");

function slickBuiltinMethod(receiver, name, args) {
  if (Array.isArray(receiver)) {
    if (name === "Length") return BigInt(receiver.length);
    if (name === "Get") {
      const index = slickIntArgument(args[0], "array index");
      if (index < 0n || index >= BigInt(receiver.length)) return slickAbsent;
      return slickOptional(receiver[Number(index)]);
    }
    if (name === "Slice") {
      const start = slickIntArgument(args[0], "array slice bound");
      const end = slickIntArgument(args[1], "array slice bound");
      if (start < 0n || end < start || end > BigInt(receiver.length)) {
        return new SlickResult(false, new SlickObject("std.collections.BoundsFailure", new Map(), null));
      }
      return new SlickResult(true, receiver.slice(Number(start), Number(end)));
    }
  }
  if (receiver instanceof SlickMap) {
    if (name === "Length") return BigInt(receiver.entries.length);
    if (name === "Get") {
      const entry = receiver.entries.find(([key]) => slickEqual(key, args[0]));
      return entry === undefined ? slickAbsent : slickOptional(entry[1]);
    }
    if (name === "Contains") return receiver.entries.some(([key]) => slickEqual(key, args[0]));
    if (name === "With") {
      const entries = receiver.entries.map((entry) => [entry[0], entry[1]]);
      const existing = entries.find((entry) => slickEqual(entry[0], args[0]));
      if (existing === undefined) entries.push([args[0], args[1]]);
      else existing[1] = args[1];
      return new SlickMap(entries);
    }
    if (name === "Without") {
      return new SlickMap(receiver.entries.filter(([key]) => !slickEqual(key, args[0])));
    }
  }
  return slickNoBuiltin;
}

function slickIntArgument(value, label) {
  if (typeof value !== "bigint") throw SlickFailure.host(label + " is not int");
  return value;
}

export function slickTypeName(value) {
  if (value === null) return "null";
  if (typeof value === "boolean") return "bool";
  if (typeof value === "bigint") return "int";
  if (typeof value === "number") return "float";
  if (typeof value === "string") return "string";
  if (value instanceof Uint8Array) return "bytes";
  if (Array.isArray(value)) return "array";
  if (value instanceof SlickTuple) return "tuple";
  if (value instanceof SlickEnumerate || value instanceof SlickZip) return "iterable";
  if (value instanceof SlickRange) return "range";
  if (value instanceof SlickMap) return "Map";
  if (value instanceof SlickBuffer) return "Buffer";
  if (value instanceof SlickOptional) return "Optional";
  if (value instanceof SlickResult) return "Result";
  if (value instanceof SlickObject || value instanceof SlickUnion) return value.typeName;
  if (value instanceof SlickCallable) return "callable";
  return "unknown";
}

export function slickFormatFloat(value) {
  if (Number.isNaN(value)) return "NaN";
  if (value === Infinity) return "+Inf";
  if (value === -Infinity) return "-Inf";
  if (Object.is(value, -0)) return "-0";
  const magnitude = Math.abs(value);
  if (value === 0 || (magnitude >= 1e-4 && magnitude < 1e6)) return String(value);
  const [mantissa, rawExponent] = value.toExponential().split("e");
  const exponent = Number(rawExponent);
  const sign = exponent < 0 ? "-" : "+";
  const digits = String(Math.abs(exponent));
  return mantissa + "e" + sign + (digits.length < 2 ? "0" + digits : digits);
}

export function slickFormat(value) {
  if (value === null) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "bigint") return value.toString();
  if (typeof value === "number") return slickFormatFloat(value);
  if (typeof value === "string") return value;
  if (value instanceof Uint8Array) return "bytes[" + value.length + "]";
  if (Array.isArray(value)) return "[" + value.map(slickFormat).join(", ") + "]";
  if (value instanceof SlickTuple) return "(" + value.values.map(slickFormat).join(", ") + ")";
  if (value instanceof SlickRange || value instanceof SlickEnumerate || value instanceof SlickZip) {
    const items = [];
    for (const item of slickIter(value)) items.push(slickFormat(item));
    return "[" + items.join(", ") + "]";
  }
  if (value instanceof SlickMap) {
    return "map {" + value.entries.map(([key, entry]) => slickFormat(key) + ": " + slickFormat(entry)).join(", ") + "}";
  }
  if (value instanceof SlickBuffer) return "Buffer";
  if (value instanceof SlickOptional) return value.present ? slickFormat(value.value) : "";
  if (value instanceof SlickResult) return (value.ok ? "Ok(" : "Err(") + slickFormat(value.value) + ")";
  if (value instanceof SlickObject) return value.typeName;
  if (value instanceof SlickUnion) {
    if (value.fields.length === 0) return value.variant;
    return value.variant + "(" + value.fields.map(slickFormat).join(", ") + ")";
  }
  if (value instanceof SlickCallable) return "<callable>";
  return "";
}

export function slickFailureText(failure) {
  let primary = "";
  if (failure.kind === "host") primary = failure.message;
  else if (failure.kind === "cancelled") primary = "task cancelled";
  else {
    const typeName = slickTypeName(failure.value);
    let message = "";
    if (typeof failure.value === "string") message = failure.value;
    else if (failure.value instanceof SlickObject) {
      if (failure.value.fields.has("Message")) message = slickFormat(failure.value.fields.get("Message"));
      else if (failure.value.fields.has("message")) message = slickFormat(failure.value.fields.get("message"));
      if (message.length === 0) message = failure.value.message;
    }
    primary = message.length === 0 ? typeName : typeName + ": " + message;
  }
  if (failure.suppressed.length === 0) return primary;
  return primary + " (suppressed: " + failure.suppressed.map(slickFailureText).join("; ") + ")";
}

// A catch arm on the Error interface claims every Slick failure; a concrete
// error class claims only its own type.
export function slickMatchFailure(failure, typeName) {
  if (failure.kind !== "slick") return false;
  return typeName === "Error" || slickTypeName(failure.value) === typeName;
}

export function slickFailureValue(failure) {
  return failure.kind === "slick" ? failure.value : null;
}

export function slickResultPayload(value, wantOk) {
  if (value instanceof SlickResult && value.ok === wantOk) return { value: value.value };
  return null;
}

export function slickUnionPayload(value, variant) {
  if (value instanceof SlickUnion && value.variant === variant) return value.fields;
  return null;
}

export function slickUnwrap(value) {
  if (!(value instanceof SlickResult)) throw SlickFailure.host("unwrap value is not Result");
  if (value.ok) return value.value;
  throw SlickFailure.slick(value.value);
}

export function slickPropagateFailure(value) {
  if (!(value instanceof SlickResult)) throw SlickFailure.host("propagation value is not Result");
  return value.ok ? null : value;
}

function slickWrite(text) {
  process.stdout.write(text);
}

function slickWriteError(text) {
  process.stderr.write(text);
}

// slickRunTask executes one launched request in a worker and reports the result,
// the failure, and only the environment changes this worker made.
async function slickRunTask() {
  let message;
  try {
    slickEnvironmentAdopt(workerData.environment);
    const request = slickDecodeRequest(workerData.request);
    const value = await slickInvoke(new SlickContext(workerData.cancellations), request);
    message = { ok: true, value: slickEncode(value), environment: slickEnvironmentMutations() };
  } catch (error) {
    message = {
      ok: false,
      failure: slickEncodeFailure(slickAsFailure(error)),
      environment: slickEnvironmentMutations(),
    };
  }
  parentPort.postMessage(message);
}

export async function slickRun(moduleUrl, program) {
  slickInstall(moduleUrl, program);
  if (!isMainThread) {
    // A worker receives messages only after its module finishes evaluating, so
    // the task body is scheduled instead of awaited here. Without this, a task
    // that reaches an owner-held resource would wait for a reply the worker
    // cannot yet observe.
    slickOwnerListen();
    setTimeout(() => { void slickRunTask(); }, 0);
    return;
  }
  try {
    const args = [];
    if (slickProgram.entry === "arguments") {
      args.push(process.argv.slice(2).map((argument) => argument));
    }
    const value = await slickInvoke(new SlickContext([]), {
      kind: "function", target: "root.main", arguments: args,
    });
    if (slickProgram.status === true) {
      // A command-line main writes its exact bytes before the exit code is
      // validated, so a Status always produces the output it asked for.
      const output = slickField(value, "Output");
      const errorOutput = slickField(value, "ErrorOutput");
      const code = slickField(value, "ExitCode");
      if (output instanceof Uint8Array) process.stdout.write(output);
      if (errorOutput instanceof Uint8Array) process.stderr.write(errorOutput);
      const exit = typeof code === "bigint" ? code : 0n;
      if (exit < 0n || exit > 255n) {
        slickWriteError("std.process.Status ExitCode must be 0 through 255, found " + exit + "\n");
        process.exit(1);
      }
      process.exit(Number(exit));
    }
    const text = slickFormat(value);
    if (text.length !== 0) slickWrite(text + "\n");
  } catch (error) {
    slickWriteError(slickFailureText(slickAsFailure(error)) + "\n");
    process.exit(1);
  }
}

// Every standard-library family shares these helpers: argument decoding,
// documented failure construction, owned native resources, and the
// compiler-owned environment overlay.
export class SlickNativeResource {
  constructor(state) { this.state = state; }
}

export function slickResourceNew(state) {
  return new SlickNativeResource(state);
}

export function slickResourceObject(typeName, fields, resource) {
  return new SlickObject(typeName, new Map(fields), resource);
}

export function slickStdObject(typeName, fields) {
  return new SlickObject(typeName, new Map(fields), null);
}

export function slickOk(value) {
  return new SlickResult(true, value);
}

export function slickErr(value) {
  return new SlickResult(false, value);
}

export function slickArg(args, index) {
  return index < args.length ? args[index] : null;
}

export function slickArgString(args, index) {
  const value = slickArg(args, index);
  if (typeof value !== "string") {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not string");
  }
  return value;
}

export function slickArgInt(args, index) {
  const value = slickArg(args, index);
  if (typeof value !== "bigint") {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not int");
  }
  return value;
}

export function slickArgFloat(args, index) {
  const value = slickArg(args, index);
  if (typeof value !== "number") {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not float");
  }
  return value;
}

export function slickArgBool(args, index) {
  const value = slickArg(args, index);
  if (typeof value !== "boolean") {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not bool");
  }
  return value;
}

export function slickArgBytes(args, index) {
  const value = slickArg(args, index);
  if (!(value instanceof Uint8Array)) {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not bytes");
  }
  return value;
}

export function slickArgValues(args, index) {
  const value = slickArg(args, index);
  if (Array.isArray(value)) return value;
  if (value instanceof SlickTuple) return value.values;
  throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not array");
}

export function slickArgEntries(args, index) {
  const value = slickArg(args, index);
  if (!(value instanceof SlickMap)) {
    throw SlickFailure.host("standard-library argument " + index + " is " + slickTypeName(value) + " and not Map");
  }
  return value.entries;
}

export function slickArgOptional(args, index) {
  const value = slickArg(args, index);
  if (value instanceof SlickOptional) return value.present ? value.value : undefined;
  return value === null ? undefined : value;
}

// A native resource method must survive an object literal of its class, whose
// state is absent.
export function slickArgResource(args, index) {
  let value = slickArg(args, index);
  if (value instanceof SlickOptional && value.present) value = value.value;
  if (value instanceof SlickObject && value.resource instanceof SlickNativeResource) return value.resource;
  return null;
}

export function slickArgField(args, index, name) {
  return slickField(slickArg(args, index), name);
}

// std.env.Set and std.env.Unset record their effect in a compiler-owned overlay
// instead of mutating the host environment. A worker adopts its parent's
// recorded state at launch and reports back only what it changed itself, so a
// no-op child never rolls back a parent assignment.
const slickEnvironmentOverlay = new Map();
const slickEnvironmentDirty = new Set();

export function slickEnvironmentRecord(name, value) {
  slickEnvironmentOverlay.set(name, value);
  slickEnvironmentDirty.add(name);
}

export function slickEnvironmentRead(name) {
  if (slickEnvironmentOverlay.has(name)) return slickEnvironmentOverlay.get(name);
  const value = process.env[name];
  return value === undefined ? null : value;
}

export function slickEnvironmentChanges() {
  return Array.from(slickEnvironmentOverlay.entries());
}

export function slickEnvironmentMutations() {
  const mutations = [];
  for (const name of slickEnvironmentDirty) mutations.push([name, slickEnvironmentOverlay.get(name)]);
  return mutations;
}

export function slickEnvironmentAdopt(changes) {
  if (!Array.isArray(changes)) return;
  for (const [name, value] of changes) slickEnvironmentOverlay.set(name, value);
}

// A joined child's mutations become this scope's own, so a mutation made deep in
// a task subtree keeps travelling upward instead of stopping at the first join.
export function slickEnvironmentMerge(changes) {
  if (!Array.isArray(changes)) return;
  for (const [name, value] of changes) slickEnvironmentRecord(name, value);
}
`
