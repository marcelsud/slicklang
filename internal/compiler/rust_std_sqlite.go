package compiler

var rustStdSQLite = rustStdFamily{
	family: runtimeFamilySQLite,
	module: rustStdSQLiteModule,
	functions: map[runtimeOperationID]string{
		nativeStdSQLiteOpen:                "slick_nat_sqlite_open",
		nativeStdSQLiteDatabaseExecute:     "slick_nat_sqlite_database_execute",
		nativeStdSQLiteDatabaseQuery:       "slick_nat_sqlite_database_query",
		nativeStdSQLiteDatabaseBegin:       "slick_nat_sqlite_database_begin",
		nativeStdSQLiteDatabaseClose:       "slick_nat_sqlite_database_close",
		nativeStdSQLiteTransactionExecute:  "slick_nat_sqlite_transaction_execute",
		nativeStdSQLiteTransactionQuery:    "slick_nat_sqlite_transaction_query",
		nativeStdSQLiteTransactionCommit:   "slick_nat_sqlite_transaction_commit",
		nativeStdSQLiteTransactionRollback: "slick_nat_sqlite_transaction_rollback",
		nativeStdSQLiteTransactionClose:    "slick_nat_sqlite_transaction_close",
	},
	dependencies: []rustCrate{{
		name:     rustSQLiteCrate,
		version:  rustSQLiteVersion,
		features: []string{"bundled"},
	}},
}

// rustStdSQLiteModule implements std.sqlite. Database and transaction handles
// are owned SlickResource values. Each transaction holds a cloned resource to
// its database plus a monotonically increasing token so a completed handle
// cannot drive a later transaction. Closed is a flag inside the state; the
// state is released when the last Slick alias drops. Transactions are managed
// with explicit BEGIN/COMMIT/ROLLBACK so the connection is never split across
// borrowed lifetimes. Every operation matches the interpreter and generated Go:
// single-statement validation, typed Value binding, MaxRows/MaxBytes bounds,
// Failure normalization, close-exactly-once, and nil-safe methods on object
// literals. The Rust source contains no backtick characters.
const rustStdSQLiteModule = `use rusqlite::Connection;
use rusqlite::types::Value as SqliteValue;
use rusqlite::types::ValueRef;
use rusqlite::params_from_iter;

struct SlickSqliteDatabase {
    conn: Option<Connection>,
    path: String,
    closed: bool,
    tx_state: i32,
    tx_closed: bool,
    tx_token: u64,
}

struct SlickSqliteTransaction {
    database: SlickResource,
    token: u64,
    closed: bool,
}

fn slick_sqlite_tx_matches(db: &SlickSqliteDatabase, token: u64) -> bool {
    !db.closed && db.conn.is_some() && !db.tx_closed && db.tx_state == 1 && db.tx_token == token
}

fn slick_sqlite_tx_binding(tx_resource: &SlickResource) -> Option<(SlickResource, u64)> {
    slick_resource_with_state::<SlickSqliteTransaction, Option<(SlickResource, u64)>>(tx_resource, |tx| {
        if tx.closed {
            None
        } else {
            Some((tx.database.clone(), tx.token))
        }
    }).flatten()
}

fn slick_sqlite_failure(operation: &str, code: Option<i64>, message: &str) -> SlickValue {
    let code_value = match code {
        Some(c) => SlickValue::Optional(Some(Box::new(SlickValue::Int(c)))),
        None => SlickValue::Optional(None),
    };
    slick_object("std.sqlite.Failure", vec![
        ("Operation", SlickValue::String(operation.to_string())),
        ("Code", code_value),
        ("Message", SlickValue::String(message.to_string())),
    ])
}

fn slick_sqlite_failure_from_error(operation: &str, error: &rusqlite::Error) -> SlickValue {
    let code = error.sqlite_error().map(|err| err.extended_code as i64);
    slick_sqlite_failure(operation, code, &error.to_string())
}

fn slick_sqlite_execution(rows_affected: i64, last_insert_id: i64) -> SlickValue {
    slick_object("std.sqlite.Execution", vec![
        ("RowsAffected", SlickValue::Int(rows_affected)),
        ("LastInsertId", SlickValue::Optional(Some(Box::new(SlickValue::Int(last_insert_id))))),
    ])
}

fn slick_sqlite_null() -> SlickValue {
    SlickValue::Union { type_name: "std.sqlite.Value", variant: "Null", tag: 1, fields: vec![] }
}

fn slick_sqlite_integer(v: i64) -> SlickValue {
    SlickValue::Union { type_name: "std.sqlite.Value", variant: "Integer", tag: 2, fields: vec![SlickValue::Int(v)] }
}

fn slick_sqlite_float(v: f64) -> SlickValue {
    SlickValue::Union { type_name: "std.sqlite.Value", variant: "Float", tag: 3, fields: vec![SlickValue::Float(v)] }
}

fn slick_sqlite_text(v: String) -> SlickValue {
    SlickValue::Union { type_name: "std.sqlite.Value", variant: "Text", tag: 4, fields: vec![SlickValue::String(v)] }
}

fn slick_sqlite_blob(v: Vec<u8>) -> SlickValue {
    SlickValue::Union { type_name: "std.sqlite.Value", variant: "Blob", tag: 5, fields: vec![SlickValue::Bytes(v)] }
}

fn slick_sqlite_is_space(c: char) -> bool {
    c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

fn slick_sqlite_validate_single_sql(sql_text: &str, op: &str) -> Option<SlickValue> {
    let trimmed = sql_text.trim();
    if trimmed.is_empty() {
        return Some(slick_sqlite_failure(op, None, "SQL statement must not be empty"));
    }
    let mut has_content = false;
    let mut in_single = false;
    let mut in_double = false;
    let mut in_backtick = false;
    let mut in_line = false;
    let mut in_block = false;
    let mut saw_semi = false;
    let chars: Vec<char> = sql_text.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        let c = chars[i];
        if in_line {
            if c == '\n' { in_line = false; }
            i += 1; continue;
        }
        if in_block {
            if c == '*' && i + 1 < chars.len() && chars[i + 1] == '/' {
                in_block = false;
                i += 2; continue;
            }
            i += 1; continue;
        }
        if in_single {
            if c == '\'' {
                if i + 1 < chars.len() && chars[i + 1] == '\'' {
                    i += 2; continue;
                }
                in_single = false;
                i += 1; continue;
            }
            i += 1; continue;
        }
        if in_double {
            if c == '"' {
                if i + 1 < chars.len() && chars[i + 1] == '"' {
                    i += 2; continue;
                }
                in_double = false;
                i += 1; continue;
            }
            i += 1; continue;
        }
        if in_backtick {
            if c == '\u{60}' { in_backtick = false; }
            i += 1; continue;
        }
        if c == '-' && i + 1 < chars.len() && chars[i + 1] == '-' {
            in_line = true;
            i += 2; continue;
        }
        if c == '/' && i + 1 < chars.len() && chars[i + 1] == '*' {
            in_block = true;
            i += 2; continue;
        }
        if c == '\'' { in_single = true; has_content = true; i += 1; continue; }
        if c == '"' { in_double = true; has_content = true; i += 1; continue; }
        if c == '\u{60}' { in_backtick = true; has_content = true; i += 1; continue; }
        if c == ';' { saw_semi = true; i += 1; continue; }
        if !slick_sqlite_is_space(c) {
            if saw_semi {
                return Some(slick_sqlite_failure(op, None, "statement contains multiple SQL statements"));
            }
            has_content = true;
        }
        i += 1;
    }
    if !has_content {
        return Some(slick_sqlite_failure(op, None, "SQL statement must not be empty"));
    }
    None
}

fn slick_sqlite_value_to_sql(value: &SlickValue) -> Result<SqliteValue, String> {
    match value {
        SlickValue::Union { variant: "Null", .. } => Ok(SqliteValue::Null),
        SlickValue::Union { variant: "Integer", fields, .. } => {
            if fields.len() != 1 { return Err("invalid Integer variant payload".to_string()); }
            match &fields[0] {
                SlickValue::Int(v) => Ok(SqliteValue::Integer(*v)),
                _ => Err("invalid Integer variant payload".to_string()),
            }
        }
        SlickValue::Union { variant: "Float", fields, .. } => {
            if fields.len() != 1 { return Err("invalid Float variant payload".to_string()); }
            match &fields[0] {
                SlickValue::Float(v) => {
                    if v.is_nan() || v.is_infinite() {
                        return Err("cannot bind non-finite floating-point value".to_string());
                    }
                    Ok(SqliteValue::Real(*v))
                }
                _ => Err("invalid Float variant payload".to_string()),
            }
        }
        SlickValue::Union { variant: "Text", fields, .. } => {
            if fields.len() != 1 { return Err("invalid Text variant payload".to_string()); }
            match &fields[0] {
                SlickValue::String(s) => Ok(SqliteValue::Text(s.clone())),
                _ => Err("invalid Text variant payload".to_string()),
            }
        }
        SlickValue::Union { variant: "Blob", fields, .. } => {
            if fields.len() != 1 { return Err("invalid Blob variant payload".to_string()); }
            match &fields[0] {
                SlickValue::Bytes(b) => Ok(SqliteValue::Blob(b.clone())),
                _ => Err("invalid Blob variant payload".to_string()),
            }
        }
        SlickValue::Union { variant, .. } => Err(format!("unknown SQLite Value variant {}", variant)),
        _ => Err("invalid SQLite parameter value".to_string()),
    }
}

fn slick_sqlite_sql_to_value(value: ValueRef) -> Result<SlickValue, SlickValue> {
    match value {
        ValueRef::Null => Ok(slick_sqlite_null()),
        ValueRef::Integer(v) => Ok(slick_sqlite_integer(v)),
        ValueRef::Real(v) => {
            if v.is_nan() || v.is_infinite() {
                Err(slick_sqlite_failure("Query", None, "query returned a non-finite floating-point value"))
            } else {
                Ok(slick_sqlite_float(v))
            }
        }
        ValueRef::Text(bytes) => {
            match std::str::from_utf8(bytes) {
                Ok(s) => Ok(slick_sqlite_text(s.to_string())),
                Err(_) => Err(slick_sqlite_failure("Query", None, "query returned invalid UTF-8 text")),
            }
        }
        ValueRef::Blob(bytes) => Ok(slick_sqlite_blob(bytes.to_vec())),
    }
}

fn slick_sqlite_extract_sql(stmt: &SlickValue, op: &str) -> Result<String, SlickValue> {
    match slick_field(stmt, "SQL") {
        Ok(SlickValue::String(s)) => Ok(s),
        _ => Err(slick_sqlite_failure(op, None, "invalid SQL field")),
    }
}

fn slick_sqlite_extract_parameters(stmt: &SlickValue, op: &str) -> Result<Vec<SqliteValue>, SlickValue> {
    let params_value = match slick_field(stmt, "Parameters") {
        Ok(v) => v,
        Err(_) => return Err(slick_sqlite_failure(op, None, "invalid SQLite parameter value")),
    };
    let elements = match params_value {
        SlickValue::Array(elements) => elements,
        _ => return Err(slick_sqlite_failure(op, None, "invalid SQLite parameter value")),
    };
    let mut params = Vec::with_capacity(elements.len());
    for el in &elements {
        match slick_sqlite_value_to_sql(el) {
            Ok(v) => params.push(v),
            Err(msg) => return Err(slick_sqlite_failure(op, None, &msg)),
        }
    }
    Ok(params)
}

fn slick_sqlite_scan_rows(
    context: &SlickContext,
    stmt: &mut rusqlite::Statement,
    params: Vec<SqliteValue>,
    max_rows: i64,
    max_bytes: i64,
) -> Result<SlickValue, SlickValue> {
    let cols: Vec<String> = stmt.column_names().iter().map(|s| s.to_string()).collect();
    let mut seen: Vec<String> = Vec::with_capacity(cols.len());
    for col in &cols {
        if seen.contains(col) {
            return Err(slick_sqlite_failure("Query", None, &format!("query returned duplicate column name {:?}; use SQL aliases", col)));
        }
        seen.push(col.clone());
    }
    let mut rows = match stmt.query(params_from_iter(params)) {
        Ok(r) => r,
        Err(err) => return Err(slick_sqlite_failure_from_error("Query", &err)),
    };
    let mut row_list: Vec<SlickValue> = Vec::new();
    let mut cumulative_bytes: i64 = 0;
    let mut row_count: i64 = 0;
    loop {
        if context.cancelled() {
            return Err(slick_sqlite_failure("Query", None, "context canceled"));
        }
        let row = match rows.next() {
            Ok(Some(row)) => row,
            Ok(None) => break,
            Err(err) => return Err(slick_sqlite_failure_from_error("Query", &err)),
        };
        row_count += 1;
        if row_count > max_rows {
            return Err(slick_sqlite_failure("Query", None, &format!("query exceeded maximum row limit of {}", max_rows)));
        }
        let mut entries: Vec<(SlickValue, SlickValue)> = Vec::with_capacity(cols.len());
        for (i, col) in cols.iter().enumerate() {
            let value_ref = match row.get_ref(i) {
                Ok(v) => v,
                Err(err) => return Err(slick_sqlite_failure_from_error("Query", &err)),
            };
            let bytes_for_col = match value_ref {
                ValueRef::Text(bytes) => bytes.len() as i64,
                ValueRef::Blob(bytes) => bytes.len() as i64,
                _ => 8,
            };
            cumulative_bytes += bytes_for_col;
            if cumulative_bytes > max_bytes {
                return Err(slick_sqlite_failure("Query", None, &format!("query exceeded maximum byte limit of {}", max_bytes)));
            }
            let slick_val = match slick_sqlite_sql_to_value(value_ref) {
                Ok(v) => v,
                Err(failure) => return Err(failure),
            };
            entries.push((slick_string(col.clone()), slick_val));
        }
        let row_map = slick_map(entries);
        let row_obj = slick_object("std.sqlite.Row", vec![
            ("Values", row_map),
        ]);
        row_list.push(row_obj);
    }
    Ok(SlickValue::Array(row_list))
}

fn slick_nat_sqlite_open(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let path = match slick_arg_string(&args, 0) {
        Ok(s) => s,
        Err(failure) => return SlickOutcome::Throw(failure),
    };
    if path != ":memory:" {
        let p = std::path::Path::new(&path);
        if let Some(parent) = p.parent() {
            let dir = parent.to_string_lossy();
            if dir != "." && !dir.is_empty() && dir != "/" {
                if !std::path::Path::new(&*dir).is_dir() {
                    return slick_err(slick_sqlite_failure("Open", None, &format!("parent directory does not exist: {}", dir)));
                }
            }
        }
    }
    let conn = if path == ":memory:" {
        match Connection::open_in_memory() {
            Ok(c) => c,
            Err(err) => return slick_err(slick_sqlite_failure_from_error("Open", &err)),
        }
    } else {
        match Connection::open(&path) {
            Ok(c) => c,
            Err(err) => return slick_err(slick_sqlite_failure_from_error("Open", &err)),
        }
    };
    let _ = context;
    let db = SlickSqliteDatabase {
        conn: Some(conn),
        path: path.clone(),
        closed: false,
        tx_state: 0,
        tx_closed: false,
        tx_token: 0,
    };
    let resource = slick_resource_new(Box::new(db));
    slick_ok(slick_resource_object("std.sqlite.Database", vec![], resource))
}

fn slick_nat_sqlite_database_execute(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Execute", None, "database is closed")),
    };
    let stmt = slick_arg(&args, 1);
    let sql_text = match slick_sqlite_extract_sql(&stmt, "Execute") {
        Ok(s) => s,
        Err(failure) => return slick_err(failure),
    };
    if let Some(failure) = slick_sqlite_validate_single_sql(&sql_text, "Execute") {
        return slick_err(failure);
    }
    let params = match slick_sqlite_extract_parameters(&stmt, "Execute") {
        Ok(p) => p,
        Err(failure) => return slick_err(failure),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&handle, |db| {
        if db.closed || db.conn.is_none() {
            return slick_err(slick_sqlite_failure("Execute", None, "database is closed"));
        }
        if db.tx_state == 1 {
            return slick_err(slick_sqlite_failure("Execute", None, "a transaction is already active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Execute", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        match conn.execute(&sql_text, params_from_iter(params)) {
            Ok(changes) => {
                let last_id = conn.last_insert_rowid();
                slick_ok(slick_sqlite_execution(changes as i64, last_id))
            }
            Err(err) => slick_err(slick_sqlite_failure_from_error("Execute", &err)),
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Execute", None, "database is closed")))
}

fn slick_nat_sqlite_database_query(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Query", None, "database is closed")),
    };
    let query = slick_arg(&args, 1);
    let sql_text = match slick_sqlite_extract_sql(&query, "Query") {
        Ok(s) => s,
        Err(failure) => return slick_err(failure),
    };
    let max_rows = match slick_field(&query, "MaxRows") {
        Ok(SlickValue::Int(v)) => v,
        _ => return slick_err(slick_sqlite_failure("Query", None, "invalid MaxRows field")),
    };
    let max_bytes = match slick_field(&query, "MaxBytes") {
        Ok(SlickValue::Int(v)) => v,
        _ => return slick_err(slick_sqlite_failure("Query", None, "invalid MaxBytes field")),
    };
    if max_rows <= 0 || max_bytes <= 0 {
        return slick_err(slick_sqlite_failure("Query", None, "MaxRows and MaxBytes must be greater than zero"));
    }
    if let Some(failure) = slick_sqlite_validate_single_sql(&sql_text, "Query") {
        return slick_err(failure);
    }
    let params = match slick_sqlite_extract_parameters(&query, "Query") {
        Ok(p) => p,
        Err(failure) => return slick_err(failure),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&handle, |db| {
        if db.closed || db.conn.is_none() {
            return slick_err(slick_sqlite_failure("Query", None, "database is closed"));
        }
        if db.tx_state == 1 {
            return slick_err(slick_sqlite_failure("Query", None, "a transaction is already active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Query", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        let mut stmt = match conn.prepare(&sql_text) {
            Ok(s) => s,
            Err(err) => return slick_err(slick_sqlite_failure_from_error("Query", &err)),
        };
        match slick_sqlite_scan_rows(context, &mut stmt, params, max_rows, max_bytes) {
            Ok(rows) => slick_ok(rows),
            Err(failure) => slick_err(failure),
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Query", None, "database is closed")))
}

fn slick_nat_sqlite_database_begin(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Begin", None, "database is closed")),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, Result<u64, SlickValue>>(&handle, |db| {
        if db.closed || db.conn.is_none() {
            return Err(slick_sqlite_failure("Begin", None, "database is closed"));
        }
        if db.tx_state == 1 {
            return Err(slick_sqlite_failure("Begin", None, "a transaction is already active"));
        }
        if context.cancelled() {
            return Err(slick_sqlite_failure("Begin", None, "context canceled"));
        }
        let conn = match db.conn.as_ref() {
            Some(conn) => conn,
            None => return Err(slick_sqlite_failure("Begin", None, "database is closed")),
        };
        match conn.execute_batch("BEGIN") {
            Ok(()) => {
                db.tx_token = db.tx_token.wrapping_add(1);
                db.tx_state = 1;
                db.tx_closed = false;
                Ok(db.tx_token)
            }
            Err(err) => Err(slick_sqlite_failure_from_error("Begin", &err)),
        }
    });
    match result {
        Some(Ok(token)) => {
            let tx = SlickSqliteTransaction { database: handle, token, closed: false };
            let tx_resource = slick_resource_new(Box::new(tx));
            slick_ok(slick_resource_object("std.sqlite.Transaction", vec![], tx_resource))
        }
        Some(Err(failure)) => slick_err(failure),
        None => slick_err(slick_sqlite_failure("Begin", None, "database is closed")),
    }
}

fn slick_nat_sqlite_database_close(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let _ = context;
    let handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return SlickOutcome::Value(SlickValue::Null),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&handle, |db| {
        if db.closed {
            return SlickOutcome::Value(SlickValue::Null);
        }
        db.closed = true;
        if db.tx_state == 1 {
            db.tx_state = 3;
            db.tx_closed = true;
            if let Some(ref conn) = db.conn {
                let _ = conn.execute_batch("ROLLBACK");
            }
        }
        if let Some(conn) = db.conn.take() {
            match conn.close() {
                Ok(()) => SlickOutcome::Value(SlickValue::Null),
                Err((returned_conn, err)) => {
                    db.conn = Some(returned_conn);
                    SlickOutcome::Throw(SlickFailure::slick(slick_sqlite_failure_from_error("Close", &err)))
                }
            }
        } else {
            SlickOutcome::Value(SlickValue::Null)
        }
    });
    result.unwrap_or(SlickOutcome::Value(SlickValue::Null))
}

fn slick_nat_sqlite_transaction_execute(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let tx_handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Execute", None, "transaction is no longer active")),
    };
    let (db_resource, token) = match slick_sqlite_tx_binding(&tx_handle) {
        Some(binding) => binding,
        None => return slick_err(slick_sqlite_failure("Execute", None, "transaction is no longer active")),
    };
    let stmt = slick_arg(&args, 1);
    let sql_text = match slick_sqlite_extract_sql(&stmt, "Execute") {
        Ok(s) => s,
        Err(failure) => return slick_err(failure),
    };
    if let Some(failure) = slick_sqlite_validate_single_sql(&sql_text, "Execute") {
        return slick_err(failure);
    }
    let params = match slick_sqlite_extract_parameters(&stmt, "Execute") {
        Ok(p) => p,
        Err(failure) => return slick_err(failure),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&db_resource, |db| {
        if !slick_sqlite_tx_matches(db, token) {
            return slick_err(slick_sqlite_failure("Execute", None, "transaction is no longer active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Execute", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        match conn.execute(&sql_text, params_from_iter(params)) {
            Ok(changes) => {
                let last_id = conn.last_insert_rowid();
                slick_ok(slick_sqlite_execution(changes as i64, last_id))
            }
            Err(err) => slick_err(slick_sqlite_failure_from_error("Execute", &err)),
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Execute", None, "transaction is no longer active")))
}

fn slick_nat_sqlite_transaction_query(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let tx_handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Query", None, "transaction is no longer active")),
    };
    let (db_resource, token) = match slick_sqlite_tx_binding(&tx_handle) {
        Some(binding) => binding,
        None => return slick_err(slick_sqlite_failure("Query", None, "transaction is no longer active")),
    };
    let query = slick_arg(&args, 1);
    let sql_text = match slick_sqlite_extract_sql(&query, "Query") {
        Ok(s) => s,
        Err(failure) => return slick_err(failure),
    };
    let max_rows = match slick_field(&query, "MaxRows") {
        Ok(SlickValue::Int(v)) => v,
        _ => return slick_err(slick_sqlite_failure("Query", None, "invalid MaxRows field")),
    };
    let max_bytes = match slick_field(&query, "MaxBytes") {
        Ok(SlickValue::Int(v)) => v,
        _ => return slick_err(slick_sqlite_failure("Query", None, "invalid MaxBytes field")),
    };
    if max_rows <= 0 || max_bytes <= 0 {
        return slick_err(slick_sqlite_failure("Query", None, "MaxRows and MaxBytes must be greater than zero"));
    }
    if let Some(failure) = slick_sqlite_validate_single_sql(&sql_text, "Query") {
        return slick_err(failure);
    }
    let params = match slick_sqlite_extract_parameters(&query, "Query") {
        Ok(p) => p,
        Err(failure) => return slick_err(failure),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&db_resource, |db| {
        if !slick_sqlite_tx_matches(db, token) {
            return slick_err(slick_sqlite_failure("Query", None, "transaction is no longer active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Query", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        let mut stmt = match conn.prepare(&sql_text) {
            Ok(s) => s,
            Err(err) => return slick_err(slick_sqlite_failure_from_error("Query", &err)),
        };
        match slick_sqlite_scan_rows(context, &mut stmt, params, max_rows, max_bytes) {
            Ok(rows) => slick_ok(rows),
            Err(failure) => slick_err(failure),
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Query", None, "transaction is no longer active")))
}

fn slick_nat_sqlite_transaction_commit(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let tx_handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Commit", None, "transaction is no longer active")),
    };
    let (db_resource, token) = match slick_sqlite_tx_binding(&tx_handle) {
        Some(binding) => binding,
        None => return slick_err(slick_sqlite_failure("Commit", None, "transaction is no longer active")),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&db_resource, |db| {
        if !slick_sqlite_tx_matches(db, token) {
            return slick_err(slick_sqlite_failure("Commit", None, "transaction is no longer active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Commit", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        db.tx_state = 2;
        match conn.execute_batch("COMMIT") {
            Ok(()) => {
                db.tx_state = 0;
                db.tx_closed = true;
                slick_ok(SlickValue::Null)
            }
            Err(err) => {
                slick_err(slick_sqlite_failure_from_error("Commit", &err))
            }
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Commit", None, "transaction is no longer active")))
}

fn slick_nat_sqlite_transaction_rollback(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let tx_handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return slick_err(slick_sqlite_failure("Rollback", None, "transaction is no longer active")),
    };
    let (db_resource, token) = match slick_sqlite_tx_binding(&tx_handle) {
        Some(binding) => binding,
        None => return slick_err(slick_sqlite_failure("Rollback", None, "transaction is no longer active")),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&db_resource, |db| {
        if !slick_sqlite_tx_matches(db, token) {
            return slick_err(slick_sqlite_failure("Rollback", None, "transaction is no longer active"));
        }
        if context.cancelled() {
            return slick_err(slick_sqlite_failure("Rollback", None, "context canceled"));
        }
        let conn = db.conn.as_ref().unwrap();
        db.tx_state = 3;
        match conn.execute_batch("ROLLBACK") {
            Ok(()) => {
                db.tx_state = 0;
                db.tx_closed = true;
                slick_ok(SlickValue::Null)
            }
            Err(err) => {
                slick_err(slick_sqlite_failure_from_error("Rollback", &err))
            }
        }
    });
    result.unwrap_or_else(|| slick_err(slick_sqlite_failure("Rollback", None, "transaction is no longer active")))
}

fn slick_nat_sqlite_transaction_close(context: &SlickContext, args: Vec<SlickValue>) -> SlickOutcome {
    let _ = context;
    let tx_handle = match slick_arg_resource(&args, 0) {
        Some(h) => h,
        None => return SlickOutcome::Value(SlickValue::Null),
    };
    let binding = slick_resource_with_state::<SlickSqliteTransaction, Option<(SlickResource, u64)>>(&tx_handle, |tx| {
        if tx.closed {
            None
        } else {
            tx.closed = true;
            Some((tx.database.clone(), tx.token))
        }
    });
    let (db_resource, token) = match binding {
        Some(Some(pair)) => pair,
        _ => return SlickOutcome::Value(SlickValue::Null),
    };
    let result = slick_resource_with_state::<SlickSqliteDatabase, SlickOutcome>(&db_resource, |db| {
        if db.tx_token != token || db.tx_closed {
            return SlickOutcome::Value(SlickValue::Null);
        }
        db.tx_closed = true;
        if db.tx_state == 1 {
            db.tx_state = 3;
            if let Some(ref conn) = db.conn {
                match conn.execute_batch("ROLLBACK") {
                    Ok(()) => {
                        db.tx_state = 0;
                        SlickOutcome::Value(SlickValue::Null)
                    }
                    Err(err) => {
                        SlickOutcome::Throw(SlickFailure::slick(slick_sqlite_failure_from_error("Close", &err)))
                    }
                }
            } else {
                SlickOutcome::Value(SlickValue::Null)
            }
        } else {
            SlickOutcome::Value(SlickValue::Null)
        }
    });
    result.unwrap_or(SlickOutcome::Value(SlickValue::Null))
}
`
