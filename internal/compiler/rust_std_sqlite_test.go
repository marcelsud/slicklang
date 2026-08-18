package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// rustStdSQLiteProgram exercises every std.sqlite operation the Rust backend
// owns: Open, Database.Execute/Query/Begin/Close, and Transaction
// Execute/Query/Commit/Rollback/Close. It covers create/insert/query
// round-trips with every Value kind (Null, Integer, Float, Text, Blob), a
// transaction commit and a rollback, a using-scope auto-rollback, a UNIQUE
// constraint-violation failure, a MaxRows limit, use-after-close, and a
// completed transaction handle that must not act on a later transaction.
// String concatenation avoids interpolation so the Go raw string needs no backtick.
const rustStdSQLiteProgram = `function ReadValue(RowVals: Map<string, std.sqlite.Value>, Col: string) -> string {
    let Val = RowVals.Get(Col)
    if (Val == null) {
        "missing-" + Col
    } else {
        match Val {
            std.sqlite.Value.Null => "null"
            std.sqlite.Value.Integer(V) => std.convert.IntToString(V)
            std.sqlite.Value.Float(V) => std.convert.FloatToString(V)
            std.sqlite.Value.Text(V) => V
            std.sqlite.Value.Blob(V) => match std.bytes.ToUtf8(V) { Ok(Text) => Text, Err(_) => "bad-utf8" }
        }
    }
}

function DescribeRow(First: std.sqlite.Row) -> string {
    let Vals = First.Values
    let Id = ReadValue(Vals, "id")
    let Title = ReadValue(Vals, "title")
    let Price = ReadValue(Vals, "price")
    let Data = ReadValue(Vals, "data")
    let Extra = ReadValue(Vals, "extra")
    Id + "|" + Title + "|" + Price + "|" + Data + "|" + Extra
}

function DescribeExec(Res: std.sqlite.Execution) -> string {
    let LastId = Res.LastInsertId
    let IdDesc = if (LastId == null) { "none" } else { std.convert.IntToString(LastId) }
    std.convert.IntToString(Res.RowsAffected) + "|" + IdDesc
}

function DescribeFailure(Fail: std.sqlite.Failure) -> string {
    Fail.Operation + ":" + (if (std.text.Contains(Fail.Message, "constraint")) { "constraint" } else { Fail.Message })
}

function ReadCount(DB: std.sqlite.Database) -> Result<int, std.sqlite.Failure> effects { database } {
    let Query = std.sqlite.Query { SQL: "SELECT COUNT(*) as c FROM t", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
    let Rows = DB.Query(Query)?
    let First = Rows.Get(0)
    if (First == null) {
        Ok(0)
    } else {
        let Vals = First.Values
        let Val = Vals.Get("c")
        if (Val == null) {
            Ok(0)
        } else {
            match Val {
                std.sqlite.Value.Integer(C) => Ok(C)
                _ => Ok(0)
            }
        }
    }
}

function ExerciseCRUD() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement {
            SQL: "CREATE TABLE items (id INTEGER PRIMARY KEY, title TEXT, price REAL, data BLOB, extra TEXT)"
            Parameters: []
        }
        DB.Execute(CreateStmt)?

        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO items (id, title, price, data, extra) VALUES (?, ?, ?, ?, ?)"
            Parameters: [
                std.sqlite.Value.Integer(1),
                std.sqlite.Value.Text("apple"),
                std.sqlite.Value.Float(1.5),
                std.sqlite.Value.Blob(std.bytes.FromUtf8("binary")),
                std.sqlite.Value.Null
            ]
        }
        let Exec = DB.Execute(InsertStmt)?
        let ExecDesc = DescribeExec(Exec)

        let Query = std.sqlite.Query {
            SQL: "SELECT id, title, price, data, extra FROM items WHERE id = ?"
            Parameters: [std.sqlite.Value.Integer(1)]
            MaxRows: 10
            MaxBytes: 4096
        }
        let Rows = DB.Query(Query)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("no-row|" + ExecDesc)
        } else {
            Ok(DescribeRow(First) + "|" + ExecDesc)
        }
    }
}

function ExerciseCommit() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?

        let Tx = DB.Begin()?
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (100)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Tx.Commit()?

        let Count = ReadCount(DB)?
        Ok("commit:" + std.convert.IntToString(Count))
    }
}

function ExerciseRollback() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?

        let Tx = DB.Begin()?
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (100)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Tx.Rollback()?

        let Count = ReadCount(DB)?
        Ok("rollback:" + std.convert.IntToString(Count))
    }
}

function RunAutoRollback(DB: std.sqlite.Database) -> Result<null, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using Tx = DB.Begin()? {
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (999)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Ok(null)
    }
}

function ExerciseAutoRollback() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?
        RunAutoRollback(DB)?
        let Count = ReadCount(DB)?
        Ok("auto:" + std.convert.IntToString(Count))
    }
}

function ExerciseConstraint() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE u (id INTEGER PRIMARY KEY, title TEXT UNIQUE)", Parameters: [] }
        DB.Execute(CreateStmt)?

        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO u (id, title) VALUES (?, ?)"
            Parameters: [std.sqlite.Value.Integer(1), std.sqlite.Value.Text("dup")]
        }
        DB.Execute(InsertStmt)?

        let DupStmt = std.sqlite.Statement {
            SQL: "INSERT INTO u (id, title) VALUES (?, ?)"
            Parameters: [std.sqlite.Value.Integer(2), std.sqlite.Value.Text("dup")]
        }
        match DB.Execute(DupStmt) {
            Ok(_) => Ok("constraint:succeeded")
            Err(Fail) => Ok("constraint:" + DescribeFailure(Fail))
        }
    }
}

function ExerciseMaxRows() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (id INT, val TEXT)", Parameters: [] }
        DB.Execute(CreateStmt)?
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'c')", Parameters: [] }
        DB.Execute(InsertStmt)?

        let Q = std.sqlite.Query { SQL: "SELECT id, val FROM t ORDER BY id", Parameters: [], MaxRows: 2, MaxBytes: 1024 }
        match DB.Query(Q) {
            Ok(_) => Ok("maxrows:succeeded")
            Err(Fail) => Ok("maxrows:" + DescribeFailure(Fail))
        }
    }
}

function ExerciseClosedDB() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Open(":memory:")?
    DB.Close()
    let Stmt = std.sqlite.Statement { SQL: "CREATE TABLE t(x)", Parameters: [] }
    let ExecRes = match DB.Execute(Stmt) {
        Ok(_) => "succeeded"
        Err(Fail) => Fail.Message
    }
    let Q = std.sqlite.Query { SQL: "SELECT 1", Parameters: [], MaxRows: 1, MaxBytes: 10 }
    let QueryRes = match DB.Query(Q) {
        Ok(_) => "succeeded"
        Err(Fail) => Fail.Message
    }
    let BeginRes = match DB.Begin() {
        Ok(_) => "succeeded"
        Err(Fail) => Fail.Message
    }
    Ok(ExecRes + "|" + QueryRes + "|" + BeginRes)
}

function ExerciseStaleTx() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Open(":memory:")?
    let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
    DB.Execute(CreateStmt)?
    let Tx1 = DB.Begin()?
    let Insert1 = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1)", Parameters: [] }
    Tx1.Execute(Insert1)?
    Tx1.Commit()?
    let Tx2 = DB.Begin()?
    let Insert2 = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (2)", Parameters: [] }
    Tx2.Execute(Insert2)?
    let StaleExec = match Tx1.Execute(Insert1) {
        Ok(_) => "stale-exec-ok"
        Err(Fail) => "stale-exec:" + Fail.Message
    }
    let StaleQ = std.sqlite.Query { SQL: "SELECT v FROM t", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
    let StaleQuery = match Tx1.Query(StaleQ) {
        Ok(_) => "stale-query-ok"
        Err(Fail) => "stale-query:" + Fail.Message
    }
    let StaleCommit = match Tx1.Commit() {
        Ok(_) => "stale-commit-ok"
        Err(Fail) => "stale-commit:" + Fail.Message
    }
    let StaleRollback = match Tx1.Rollback() {
        Ok(_) => "stale-rollback-ok"
        Err(Fail) => "stale-rollback:" + Fail.Message
    }
    Tx1.Close()
    Tx2.Commit()?
    let Count = ReadCount(DB)?
    DB.Close()
    Ok(StaleExec + "|" + StaleQuery + "|" + StaleCommit + "|" + StaleRollback + "|count:" + std.convert.IntToString(Count))
}

function ExerciseClosedTx() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Open(":memory:")?
    let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
    DB.Execute(CreateStmt)?
    let Tx = DB.Begin()?
    Tx.Close()
    let ClosedStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1)", Parameters: [] }
    let ExecRes = match Tx.Execute(ClosedStmt) {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    let ClosedQ = std.sqlite.Query { SQL: "SELECT v FROM t", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
    let QueryRes = match Tx.Query(ClosedQ) {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    let CommitRes = match Tx.Commit() {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    let RollbackRes = match Tx.Rollback() {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    DB.Close()
    Ok(ExecRes + "|" + QueryRes + "|" + CommitRes + "|" + RollbackRes)
}

function ExerciseDBCloseRollsBackTx() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Open(":memory:")?
    let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
    DB.Execute(CreateStmt)?
    let Tx = DB.Begin()?
    let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1)", Parameters: [] }
    Tx.Execute(InsertStmt)?
    DB.Close()
    let ExecRes = match Tx.Execute(InsertStmt) {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    let CommitRes = match Tx.Commit() {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    Ok(ExecRes + "|" + CommitRes)
}

function main() -> string throws std.sqlite.Failure effects { database } {
    let CRUD = match ExerciseCRUD() { Ok(S) => S, Err(Fail) => "crud-err:" + DescribeFailure(Fail) }
    let Commit = match ExerciseCommit() { Ok(S) => S, Err(Fail) => "commit-err:" + DescribeFailure(Fail) }
    let Rollback = match ExerciseRollback() { Ok(S) => S, Err(Fail) => "rollback-err:" + DescribeFailure(Fail) }
    let Auto = match ExerciseAutoRollback() { Ok(S) => S, Err(Fail) => "auto-err:" + DescribeFailure(Fail) }
    let Constraint = match ExerciseConstraint() { Ok(S) => S, Err(Fail) => "constraint-err:" + DescribeFailure(Fail) }
    let MaxRows = match ExerciseMaxRows() { Ok(S) => S, Err(Fail) => "maxrows-err:" + DescribeFailure(Fail) }
    let Closed = match ExerciseClosedDB() { Ok(S) => S, Err(Fail) => "closed-err:" + DescribeFailure(Fail) }
    let Stale = match ExerciseStaleTx() { Ok(S) => S, Err(Fail) => "stale-err:" + DescribeFailure(Fail) }
    let ClosedTx = match ExerciseClosedTx() { Ok(S) => S, Err(Fail) => "closed-tx-err:" + DescribeFailure(Fail) }
    let ClosedDBTx = match ExerciseDBCloseRollsBackTx() { Ok(S) => S, Err(Fail) => "closed-db-tx-err:" + DescribeFailure(Fail) }
    CRUD + "|" + Commit + "|" + Rollback + "|" + Auto + "|" + Constraint + "|" + MaxRows + "|" + Closed + "|" + Stale + "|" + ClosedTx + "|" + ClosedDBTx
}
`

func TestRustStdSQLiteMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: rustStdSQLiteProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoRustDiagnostics(t, diagnostics)
	if strings.TrimSpace(interpreted) == "" {
		t.Fatal("interpreter produced no output")
	}
	binary := buildRustTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Rust binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Rust output = %q, want interpreter output %q", output, interpreted+"\n")
	}
}
