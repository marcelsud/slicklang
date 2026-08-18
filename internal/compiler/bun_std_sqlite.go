package compiler

var bunStdSQLite = bunStdFamily{
	family: runtimeFamilySQLite,
	module: bunStdSQLiteModule,
	functions: map[runtimeOperationID]string{
		nativeStdSQLiteOpen:                "slickNatSQLiteOpen",
		nativeStdSQLiteDatabaseExecute:     "slickNatSQLiteDatabaseExecute",
		nativeStdSQLiteDatabaseQuery:       "slickNatSQLiteDatabaseQuery",
		nativeStdSQLiteDatabaseBegin:       "slickNatSQLiteDatabaseBegin",
		nativeStdSQLiteDatabaseClose:       "slickNatSQLiteDatabaseClose",
		nativeStdSQLiteTransactionExecute:  "slickNatSQLiteTransactionExecute",
		nativeStdSQLiteTransactionQuery:    "slickNatSQLiteTransactionQuery",
		nativeStdSQLiteTransactionCommit:   "slickNatSQLiteTransactionCommit",
		nativeStdSQLiteTransactionRollback: "slickNatSQLiteTransactionRollback",
		nativeStdSQLiteTransactionClose:    "slickNatSQLiteTransactionClose",
	},
}

// bunStdSQLiteModule implements std.sqlite on bun:sqlite. Database and
// transaction handles are owner-held SlickOwnedResource values, so a launched
// task or HTTP handler can carry them: the connection stays on the creating
// thread and every operation is routed through slickOwnerCall. Each
// transaction keeps a monotonically increasing token so a completed handle
// cannot drive a later transaction. Closed is a flag inside the owner state.
// Every operation matches the interpreter and generated Go: single-statement
// validation, typed Value binding, MaxRows/MaxBytes bounds checked per row
// while iterating (the iterator is closed and the statement is finalized
// exactly once on every exit), Failure normalization, close-exactly-once,
// and nil-safe methods on object literals. A failed COMMIT or ROLLBACK
// rolls back and clears transaction state; if that cleanup fails, the
// connection is discarded. The JavaScript contains no backtick characters.
const bunStdSQLiteModule = `const slickSqliteTick = String.fromCharCode(96);
let slickSqliteDatabaseClass = null;

async function slickSqliteLoadDatabase() {
  if (slickSqliteDatabaseClass === null) {
    const loaded = await import("bun:sqlite");
    slickSqliteDatabaseClass = loaded.Database;
  }
  return slickSqliteDatabaseClass;
}

function slickSqliteFailure(operation, code, message) {
  return slickStdObject("std.sqlite.Failure", [
    ["Operation", operation],
    ["Code", code],
    ["Message", message],
  ]);
}

function slickSqliteFailureFromError(operation, error) {
  if (error == null) return slickSqliteFailure(operation, slickAbsent, "unknown error");
  const message = String(error && error.message ? error.message : error);
  let code = slickAbsent;
  // The interpreter reports the extended result code, so the low byte is kept
  // together with the extended bits rather than masked away.
  if (typeof error.errno === "number") code = slickOptional(BigInt(error.errno));
  return slickSqliteFailure(operation, code, message);
}

function slickSqliteIsSpace(character) {
  return character === " " || character === "\t" || character === "\r" || character === "\n";
}

function slickSqliteValidateSingleSQL(sqlText, op) {
  if (sqlText.trim().length === 0) {
    return slickSqliteFailure(op, slickAbsent, "SQL statement must not be empty");
  }
  let hasContent = false;
  let inSingle = false;
  let inDouble = false;
  let inBacktick = false;
  let inLine = false;
  let inBlock = false;
  let sawSemi = false;
  const chars = Array.from(sqlText);
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i];
    if (inLine) {
      if (c === "\n") inLine = false;
      continue;
    }
    if (inBlock) {
      if (c === "*" && i + 1 < chars.length && chars[i + 1] === "/") {
        inBlock = false;
        i += 1;
      }
      continue;
    }
    if (inSingle) {
      if (c === "'") {
        if (i + 1 < chars.length && chars[i + 1] === "'") i += 1;
        else inSingle = false;
      }
      continue;
    }
    if (inDouble) {
      if (c === "\"") {
        if (i + 1 < chars.length && chars[i + 1] === "\"") i += 1;
        else inDouble = false;
      }
      continue;
    }
    if (inBacktick) {
      if (c === slickSqliteTick) inBacktick = false;
      continue;
    }
    if (c === "-" && i + 1 < chars.length && chars[i + 1] === "-") {
      inLine = true;
      i += 1;
      continue;
    }
    if (c === "/" && i + 1 < chars.length && chars[i + 1] === "*") {
      inBlock = true;
      i += 1;
      continue;
    }
    if (c === "'") {
      inSingle = true;
      hasContent = true;
      continue;
    }
    if (c === "\"") {
      inDouble = true;
      hasContent = true;
      continue;
    }
    if (c === slickSqliteTick) {
      inBacktick = true;
      hasContent = true;
      continue;
    }
    if (c === ";") {
      sawSemi = true;
      continue;
    }
    if (!slickSqliteIsSpace(c)) {
      if (sawSemi) {
        return slickSqliteFailure(op, slickAbsent, "statement contains multiple SQL statements");
      }
      hasContent = true;
    }
  }
  if (!hasContent) return slickSqliteFailure(op, slickAbsent, "SQL statement must not be empty");
  return null;
}

function slickSqliteValueToSQL(value) {
  if (!(value instanceof SlickUnion)) return { error: "invalid SQLite parameter value" };
  switch (value.variant) {
    case "Null":
      return { value: null };
    case "Integer":
      if (value.fields.length !== 1 || typeof value.fields[0] !== "bigint") {
        return { error: "invalid Integer variant payload" };
      }
      return { value: value.fields[0] };
    case "Float": {
      if (value.fields.length !== 1 || typeof value.fields[0] !== "number") {
        return { error: "invalid Float variant payload" };
      }
      const number = value.fields[0];
      if (!Number.isFinite(number)) return { error: "cannot bind non-finite floating-point value" };
      return { value: number };
    }
    case "Text":
      if (value.fields.length !== 1 || typeof value.fields[0] !== "string") {
        return { error: "invalid Text variant payload" };
      }
      return { value: value.fields[0] };
    case "Blob":
      if (value.fields.length !== 1 || !(value.fields[0] instanceof Uint8Array)) {
        return { error: "invalid Blob variant payload" };
      }
      return { value: value.fields[0] };
    default:
      return { error: "unknown SQLite Value variant " + value.variant };
  }
}

function slickSqliteSQLToValue(value) {
  if (value === null || value === undefined) {
    return { value: new SlickUnion("std.sqlite.Value", "Null", 1, []) };
  }
  if (typeof value === "bigint") {
    return { value: new SlickUnion("std.sqlite.Value", "Integer", 2, [value]) };
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return { error: slickSqliteFailure("Query", slickAbsent, "query returned a non-finite floating-point value") };
    }
    return { value: new SlickUnion("std.sqlite.Value", "Float", 3, [value]) };
  }
  if (typeof value === "string") {
    return { value: new SlickUnion("std.sqlite.Value", "Text", 4, [value]) };
  }
  if (value instanceof Uint8Array) {
    return { value: new SlickUnion("std.sqlite.Value", "Blob", 5, [value]) };
  }
  if (typeof value === "boolean") {
    return { value: new SlickUnion("std.sqlite.Value", "Integer", 2, [value ? 1n : 0n]) };
  }
  return { error: slickSqliteFailure("Query", slickAbsent, "query returned an unsupported SQLite value") };
}

function slickSqliteReadField(value, name, op, invalid) {
  try {
    return { value: slickField(value, name) };
  } catch (error) {
    return { error: slickSqliteFailure(op, slickAbsent, invalid) };
  }
}

function slickSqliteExtractSQL(stmt, op) {
  const field = slickSqliteReadField(stmt, "SQL", op, "invalid SQL field");
  if (field.error) return field;
  if (typeof field.value !== "string") {
    return { error: slickSqliteFailure(op, slickAbsent, "invalid SQL field") };
  }
  return { sql: field.value };
}

function slickSqliteExtractParameters(stmt, op) {
  const field = slickSqliteReadField(stmt, "Parameters", op, "invalid SQLite parameter value");
  if (field.error) return field;
  if (!Array.isArray(field.value)) {
    return { error: slickSqliteFailure(op, slickAbsent, "invalid SQLite parameter value") };
  }
  const params = [];
  for (const element of field.value) {
    const converted = slickSqliteValueToSQL(element);
    if (converted.error) return { error: slickSqliteFailure(op, slickAbsent, converted.error) };
    params.push(converted.value);
  }
  return { params };
}

function slickSqliteExtractQuery(query, op) {
  const extracted = slickSqliteExtractSQL(query, op);
  if (extracted.error) return extracted;
  const maxRowsField = slickSqliteReadField(query, "MaxRows", op, "invalid MaxRows field");
  if (maxRowsField.error) return maxRowsField;
  if (typeof maxRowsField.value !== "bigint") {
    return { error: slickSqliteFailure(op, slickAbsent, "invalid MaxRows field") };
  }
  const maxBytesField = slickSqliteReadField(query, "MaxBytes", op, "invalid MaxBytes field");
  if (maxBytesField.error) return maxBytesField;
  if (typeof maxBytesField.value !== "bigint") {
    return { error: slickSqliteFailure(op, slickAbsent, "invalid MaxBytes field") };
  }
  if (maxRowsField.value <= 0n || maxBytesField.value <= 0n) {
    return { error: slickSqliteFailure(op, slickAbsent, "MaxRows and MaxBytes must be greater than zero") };
  }
  const invalid = slickSqliteValidateSingleSQL(extracted.sql, op);
  if (invalid !== null) return { error: invalid };
  const parameters = slickSqliteExtractParameters(query, op);
  if (parameters.error) return parameters;
  return { sql: extracted.sql, params: parameters.params, maxRows: maxRowsField.value, maxBytes: maxBytesField.value };
}

function slickSqliteExecution(info) {
  let rows = 0n;
  let last = 0n;
  if (info != null) {
    if (typeof info.changes === "bigint") rows = info.changes;
    else if (typeof info.changes === "number") rows = BigInt(info.changes);
    if (typeof info.lastInsertRowid === "bigint") last = info.lastInsertRowid;
    else if (typeof info.lastInsertRowid === "number") last = BigInt(info.lastInsertRowid);
  }
  return slickStdObject("std.sqlite.Execution", [
    ["RowsAffected", rows],
    ["LastInsertId", slickOptional(last)],
  ]);
}

function slickSqliteUtf8Length(text) {
  return new TextEncoder().encode(text).length;
}

function slickSqliteFlagsCancelled(flags) {
  if (!Array.isArray(flags)) return false;
  for (const buffer of flags) {
    if (Atomics.load(new Int32Array(buffer), 0) === 1) return true;
  }
  return false;
}

function slickSqliteScanRows(flags, stmt, params, maxRows, maxBytes) {
  const names = Array.from(stmt.columnNames);
  const types = stmt.columnTypes;
  if (Array.isArray(types) && types.length !== names.length) {
    const reported = names.length > 0 ? names[0] : "";
    return slickErr(slickSqliteFailure("Query", slickAbsent, "query returned duplicate column name " + JSON.stringify(reported) + "; use SQL aliases"));
  }
  const seen = [];
  for (const name of names) {
    if (seen.includes(name)) {
      return slickErr(slickSqliteFailure("Query", slickAbsent, "query returned duplicate column name " + JSON.stringify(name) + "; use SQL aliases"));
    }
    seen.push(name);
  }
  let iterator = null;
  try {
    iterator = stmt.iterate(...params);
    const rows = [];
    let cumulative = 0n;
    let rowCount = 0n;
    for (;;) {
      if (slickSqliteFlagsCancelled(flags)) {
        return slickErr(slickSqliteFailure("Query", slickAbsent, "context canceled"));
      }
      let step;
      try {
        step = iterator.next();
      } catch (error) {
        return slickErr(slickSqliteFailureFromError("Query", error));
      }
      if (step.done) break;
      const raw = step.value;
      rowCount += 1n;
      if (rowCount > maxRows) {
        return slickErr(slickSqliteFailure("Query", slickAbsent, "query exceeded maximum row limit of " + maxRows.toString()));
      }
      const entries = [];
      for (let index = 0; index < names.length; index++) {
        const converted = slickSqliteSQLToValue(raw[names[index]]);
        if (converted.error) return slickErr(converted.error);
        const value = converted.value;
        if (value.variant === "Text") cumulative += BigInt(slickSqliteUtf8Length(value.fields[0]));
        else if (value.variant === "Blob") cumulative += BigInt(value.fields[0].byteLength);
        else cumulative += 8n;
        if (cumulative > maxBytes) {
          return slickErr(slickSqliteFailure("Query", slickAbsent, "query exceeded maximum byte limit of " + maxBytes.toString()));
        }
        entries.push([names[index], value]);
      }
      rows.push(slickStdObject("std.sqlite.Row", [["Values", slickMap(entries)]]));
    }
    return slickOk(rows);
  } finally {
    if (iterator != null) {
      try { iterator.return(); } catch (error) {}
    }
  }
}

function slickSqliteExecuteOn(conn, sql, params, flags) {
  if (slickSqliteFlagsCancelled(flags)) {
    return slickErr(slickSqliteFailure("Execute", slickAbsent, "context canceled"));
  }
  try {
    return slickOk(slickSqliteExecution(conn.run(sql, ...params)));
  } catch (error) {
    return slickErr(slickSqliteFailureFromError("Execute", error));
  }
}

function slickSqliteQueryOn(conn, sql, params, maxRows, maxBytes, flags) {
  if (slickSqliteFlagsCancelled(flags)) {
    return slickErr(slickSqliteFailure("Query", slickAbsent, "context canceled"));
  }
  let stmt = null;
  try {
    stmt = conn.prepare(sql);
    return slickSqliteScanRows(flags, stmt, params, maxRows, maxBytes);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Query", error));
  } finally {
    if (stmt != null && !stmt.isFinalized) {
      try { stmt.finalize(); } catch (error) {}
    }
  }
}

function slickSqliteTxMatches(db, token) {
  return !db.closed && db.conn != null && !db.txClosed && db.txState === 1 && db.txToken === token;
}

function slickSqliteReleaseTx(db) {
  if (db.txHandle === null || db.txHandle === undefined) return;
  const handle = db.txHandle;
  db.txHandle = null;
  slickOwnerRelease(handle);
}

function slickSqliteDiscard(db) {
  slickSqliteReleaseTx(db);
  db.closed = true;
  db.txState = 0;
  db.txClosed = true;
  if (db.conn != null) {
    try { db.conn.close(); } catch (error) {}
    db.conn = null;
  }
}

function slickSqliteTerminal(db, token, operation, command) {
  if (!slickSqliteTxMatches(db, token)) {
    return slickErr(slickSqliteFailure(operation, slickAbsent, "transaction is no longer active"));
  }
  db.txState = operation === "Commit" ? 2 : 3;
  try {
    db.conn.exec(command);
    db.txState = 0;
    db.txClosed = true;
    slickSqliteReleaseTx(db);
    return slickOk(null);
  } catch (error) {
    try {
      db.conn.exec("ROLLBACK");
      db.txState = 0;
      db.txClosed = true;
      slickSqliteReleaseTx(db);
      return slickErr(slickSqliteFailureFromError(operation, error));
    } catch (cleanup) {
      slickSqliteDiscard(db);
      return slickErr(slickSqliteFailureFromError(operation, cleanup));
    }
  }
}

// slickSqliteCleanPath is filepath.Clean's lexical algorithm: the parent of a
// path is taken after dot segments resolve, so "missing/../db.sqlite" has no
// parent directory requirement at all.
function slickSqliteCleanPath(path) {
  if (path === "") return ".";
  const rooted = path.startsWith("/");
  const parts = [];
  for (const piece of path.split("/")) {
    if (piece === "" || piece === ".") continue;
    if (piece === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") {
        parts.pop();
        continue;
      }
      if (rooted) continue;
      parts.push("..");
      continue;
    }
    parts.push(piece);
  }
  const cleaned = (rooted ? "/" : "") + parts.join("/");
  if (cleaned === "") return ".";
  return cleaned;
}

function slickSqliteParentDir(path) {
  const cleaned = slickSqliteCleanPath(path);
  const index = cleaned.lastIndexOf("/");
  if (index < 0) return "";
  if (index === 0) return "/";
  return cleaned.slice(0, index);
}

async function slickSqliteParentMissing(path) {
  if (path === ":memory:") return null;
  const dir = slickSqliteParentDir(path);
  if (dir === "" || dir === "." || dir === "/") return null;
  try {
    const fs = await import("node:fs");
    if (!fs.existsSync(dir) || !fs.statSync(dir).isDirectory()) return dir;
  } catch (error) {
    return dir;
  }
  return null;
}

function slickSqliteOwnedHandle(args, index) {
  let value = slickArg(args, index);
  if (value instanceof SlickOptional && value.present) value = value.value;
  if (value instanceof SlickObject && value.resource instanceof SlickOwnedResource) return value.resource;
  return null;
}

function slickSqliteClosedOp(method) {
  if (method === "begin") return "Begin";
  if (method === "execute" || method === "txExecute") return "Execute";
  if (method === "txCommit") return "Commit";
  if (method === "txRollback") return "Rollback";
  return "Query";
}

async function slickSqliteDBInvoke(handle, method, rawArgs, context) {
  const db = slickOwnerState(handle);
  if (db === undefined) {
    // A released handle is a closed database, which is what every method
    // documents rather than a host fault; a transaction method on a released
    // database reports the inactive transaction instead.
    if (method === "close" || method === "txClose") return null;
    const message = method.startsWith("tx") ? "transaction is no longer active" : "database is closed";
    return slickErr(slickSqliteFailure(slickSqliteClosedOp(method), slickAbsent, message));
  }
  const args = rawArgs;
  // Cancellation reaches the owning thread through the call's Context, which the
  // runtime carries out of band; it is never smuggled through the arguments.
  const flags = context === undefined ? [] : context.buffers;
  if (method === "close") {
    if (db.closed) return null;
    db.closed = true;
    if (db.txState === 1) {
      db.txState = 3;
      db.txClosed = true;
      if (db.conn != null) {
        try { db.conn.exec("ROLLBACK"); } catch (error) {}
      }
    }
    slickSqliteReleaseTx(db);
    // Close is terminal either way, so the owner entry is released even when the
    // native close reports its documented failure; a later call then sees an
    // absent handle and reports the documented closed result.
    try {
      if (db.conn != null) {
        const conn = db.conn;
        db.conn = null;
        conn.close();
      }
    } catch (error) {
      throw SlickFailure.slick(slickSqliteFailureFromError("Close", error));
    } finally {
      slickOwnerRelease(handle);
    }
    return null;
  }
  if (db.closed || db.conn == null) {
    if (method === "txClose") return null;
    const message = method.startsWith("tx") ? "transaction is no longer active" : "database is closed";
    return slickErr(slickSqliteFailure(slickSqliteClosedOp(method), slickAbsent, message));
  }
  if (method === "execute") {
    if (db.txState === 1) return slickErr(slickSqliteFailure("Execute", slickAbsent, "a transaction is already active"));
    return slickSqliteExecuteOn(db.conn, args[0], args[1], flags);
  }
  if (method === "query") {
    if (db.txState === 1) return slickErr(slickSqliteFailure("Query", slickAbsent, "a transaction is already active"));
    return slickSqliteQueryOn(db.conn, args[0], args[1], args[2], args[3], flags);
  }
  if (method === "begin") {
    if (db.txState === 1) return slickErr(slickSqliteFailure("Begin", slickAbsent, "a transaction is already active"));
    if (slickSqliteFlagsCancelled(flags)) return slickErr(slickSqliteFailure("Begin", slickAbsent, "context canceled"));
    try {
      db.conn.exec("BEGIN");
    } catch (error) {
      return slickErr(slickSqliteFailureFromError("Begin", error));
    }
    db.txToken += 1n;
    db.txState = 1;
    db.txClosed = false;
    const tx = slickOwnerCreate("sqlite.transaction", { database: handle, token: db.txToken, closed: false });
    // The database owns the active transaction's handle too, so closing or
    // discarding the database releases it instead of leaking one entry per Begin.
    db.txHandle = tx;
    return slickOk(slickResourceObject("std.sqlite.Transaction", [], tx));
  }
  if (method === "txActive") {
    if (!slickSqliteTxMatches(db, args[0])) {
      return slickErr(slickSqliteFailure(args[1], slickAbsent, "transaction is no longer active"));
    }
    return slickOk(null);
  }
  if (method === "txExecute") {
    if (!slickSqliteTxMatches(db, args[0])) return slickErr(slickSqliteFailure("Execute", slickAbsent, "transaction is no longer active"));
    return slickSqliteExecuteOn(db.conn, args[1], args[2], flags);
  }
  if (method === "txQuery") {
    if (!slickSqliteTxMatches(db, args[0])) return slickErr(slickSqliteFailure("Query", slickAbsent, "transaction is no longer active"));
    return slickSqliteQueryOn(db.conn, args[1], args[2], args[3], args[4], flags);
  }
  if (method === "txCommit") {
    if (slickSqliteFlagsCancelled(flags)) return slickErr(slickSqliteFailure("Commit", slickAbsent, "context canceled"));
    return slickSqliteTerminal(db, args[0], "Commit", "COMMIT");
  }
  if (method === "txRollback") {
    if (slickSqliteFlagsCancelled(flags)) return slickErr(slickSqliteFailure("Rollback", slickAbsent, "context canceled"));
    return slickSqliteTerminal(db, args[0], "Rollback", "ROLLBACK");
  }
  if (method === "txClose") {
    if (db.txToken !== args[0] || db.txClosed) return null;
    db.txClosed = true;
    slickSqliteReleaseTx(db);
    if (db.txState === 1) {
      db.txState = 3;
      try {
        db.conn.exec("ROLLBACK");
        db.txState = 0;
      } catch (error) {
        // Cleanup failed, so the connection can never be used again.
        slickSqliteDiscard(db);
        throw SlickFailure.slick(slickSqliteFailureFromError("Close", error));
      }
    }
    return null;
  }
  throw SlickFailure.host("unknown sqlite owner method " + method);
}

async function slickSqliteTxInvoke(handle, method, args, context) {
  // The active probe names the operation it is guarding, so an inactive
  // transaction reports that operation rather than the method's default.
  const failed = method === "active" ? args[0] : slickSqliteClosedOp(method);
  const tx = slickOwnerState(handle);
  if (tx === undefined) {
    if (method === "close") return null;
    return slickErr(slickSqliteFailure(failed, slickAbsent, "transaction is no longer active"));
  }
  if (method === "close") {
    if (tx.closed) return null;
    tx.closed = true;
    try {
      return await slickOwnerCall(tx.database, "txClose", [tx.token], context);
    } finally {
      slickOwnerRelease(handle);
    }
  }
  if (tx.closed) {
    return slickErr(slickSqliteFailure(failed, slickAbsent, "transaction is no longer active"));
  }
  if (method === "active") return await slickOwnerCall(tx.database, "txActive", [tx.token, ...args], context);
  if (method === "execute") return await slickOwnerCall(tx.database, "txExecute", [tx.token, ...args], context);
  if (method === "query") return await slickOwnerCall(tx.database, "txQuery", [tx.token, ...args], context);
  if (method === "commit" || method === "rollback") {
    const forwarded = method === "commit" ? "txCommit" : "txRollback";
    const outcome = await slickOwnerCall(tx.database, forwarded, [tx.token, ...args], context);
    // A pre-terminal failure (a cancelled call) leaves the transaction active, so
    // only a database that no longer accepts this token releases the entry.
    const db = slickOwnerState(tx.database);
    if (db === undefined || !slickSqliteTxMatches(db, tx.token)) {
      tx.closed = true;
      slickOwnerRelease(handle);
    }
    return outcome;
  }
  throw SlickFailure.host("unknown sqlite transaction method " + method);
}

slickOwnerRegister("sqlite.database", {
  states: new Map(),
  invoke: slickSqliteDBInvoke,
  create: slickSqliteDBCreate,
});
slickOwnerRegister("sqlite.transaction", {
  states: new Map(),
  invoke: slickSqliteTxInvoke,
  create: () => { throw SlickFailure.host("sqlite transactions are created by Begin"); },
});

// A database opened inside a task worker must outlive that worker, so the
// connection is always created on the owning thread.
async function slickSqliteDBCreate(factory, args) {
  if (factory !== "open") throw SlickFailure.host("unknown sqlite database factory " + factory);
  const path = args[0];
  const Database = await slickSqliteLoadDatabase();
  let conn;
  try {
    conn = new Database(path, { safeIntegers: true });
  } catch (error) {
    // A native open failure is the documented std.sqlite.Failure, so it travels
    // back as a Slick value; only real faults cross as a host failure.
    return slickErr(slickSqliteFailureFromError("Open", error));
  }
  return slickOwnerCreate("sqlite.database", {
    conn,
    path,
    closed: false,
    txState: 0,
    txClosed: false,
    txToken: 0n,
    txHandle: null,
  });
}

async function slickSqliteForward(context, handle, op, method, payload) {
  if (context.cancelled()) return slickErr(slickSqliteFailure(op, slickAbsent, "context canceled"));
  // The caller's cancellation buffers travel with the call, so the owning thread
  // observes cancellation of work it performs for a task worker too.
  return await slickOwnerCall(handle, method, payload, context);
}

export async function slickNatSQLiteOpen(context, args) {
  try {
    if (context.cancelled()) return slickErr(slickSqliteFailure("Open", slickAbsent, "context canceled"));
    const path = slickArgString(args, 0);
    const missing = await slickSqliteParentMissing(path);
    if (missing !== null) {
      return slickErr(slickSqliteFailure("Open", slickAbsent, "parent directory does not exist: " + missing));
    }
    const opened = await slickOwnerFactory("sqlite.database", "open", [path]);
    if (opened instanceof SlickResult) return opened;
    return slickOk(slickResourceObject("std.sqlite.Database", [], opened));
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Open", error));
  }
}

export async function slickNatSQLiteDatabaseExecute(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Execute", slickAbsent, "database is closed"));
    const extracted = slickSqliteExtractSQL(slickArg(args, 1), "Execute");
    if (extracted.error) return slickErr(extracted.error);
    const invalid = slickSqliteValidateSingleSQL(extracted.sql, "Execute");
    if (invalid !== null) return slickErr(invalid);
    const parameters = slickSqliteExtractParameters(slickArg(args, 1), "Execute");
    if (parameters.error) return slickErr(parameters.error);
    return await slickSqliteForward(context, handle, "Execute", "execute", [extracted.sql, parameters.params]);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Execute", error));
  }
}

export async function slickNatSQLiteDatabaseQuery(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Query", slickAbsent, "database is closed"));
    const extracted = slickSqliteExtractQuery(slickArg(args, 1), "Query");
    if (extracted.error) return slickErr(extracted.error);
    return await slickSqliteForward(context, handle, "Query", "query", [extracted.sql, extracted.params, extracted.maxRows, extracted.maxBytes]);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Query", error));
  }
}

export async function slickNatSQLiteDatabaseBegin(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Begin", slickAbsent, "database is closed"));
    return await slickSqliteForward(context, handle, "Begin", "begin", []);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Begin", error));
  }
}

export async function slickNatSQLiteDatabaseClose(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return null;
    return await slickOwnerCall(handle, "close", []);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    throw SlickFailure.slick(slickSqliteFailureFromError("Close", error));
  }
}

export async function slickNatSQLiteTransactionExecute(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Execute", slickAbsent, "transaction is no longer active"));
    // The interpreter reports an inactive transaction before it looks at the
    // statement, so the owner's active-token check runs first.
    const active = await slickSqliteForward(context, handle, "Execute", "active", ["Execute"]);
    if (active instanceof SlickResult && !active.ok) return active;
    const extracted = slickSqliteExtractSQL(slickArg(args, 1), "Execute");
    if (extracted.error) return slickErr(extracted.error);
    const invalid = slickSqliteValidateSingleSQL(extracted.sql, "Execute");
    if (invalid !== null) return slickErr(invalid);
    const parameters = slickSqliteExtractParameters(slickArg(args, 1), "Execute");
    if (parameters.error) return slickErr(parameters.error);
    return await slickSqliteForward(context, handle, "Execute", "execute", [extracted.sql, parameters.params]);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Execute", error));
  }
}

export async function slickNatSQLiteTransactionQuery(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Query", slickAbsent, "transaction is no longer active"));
    const active = await slickSqliteForward(context, handle, "Query", "active", ["Query"]);
    if (active instanceof SlickResult && !active.ok) return active;
    const extracted = slickSqliteExtractQuery(slickArg(args, 1), "Query");
    if (extracted.error) return slickErr(extracted.error);
    return await slickSqliteForward(context, handle, "Query", "query", [extracted.sql, extracted.params, extracted.maxRows, extracted.maxBytes]);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Query", error));
  }
}

export async function slickNatSQLiteTransactionCommit(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Commit", slickAbsent, "transaction is no longer active"));
    return await slickSqliteForward(context, handle, "Commit", "commit", []);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Commit", error));
  }
}

export async function slickNatSQLiteTransactionRollback(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return slickErr(slickSqliteFailure("Rollback", slickAbsent, "transaction is no longer active"));
    return await slickSqliteForward(context, handle, "Rollback", "rollback", []);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    return slickErr(slickSqliteFailureFromError("Rollback", error));
  }
}

export async function slickNatSQLiteTransactionClose(context, args) {
  try {
    const handle = slickSqliteOwnedHandle(args, 0);
    if (handle === null) return null;
    return await slickOwnerCall(handle, "close", []);
  } catch (error) {
    if (error instanceof SlickFailure) throw error;
    throw SlickFailure.slick(slickSqliteFailureFromError("Close", error));
  }
}
`
