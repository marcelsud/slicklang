package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// bunStdSQLiteProgram exercises every std.sqlite operation the Bun backend
// owns: Open, Database.Execute/Query/Begin/Close, and Transaction
// Execute/Query/Commit/Rollback/Close. It covers create/insert/query
// round-trips with every Value kind (Null, Integer, Float, Text, Blob), a
// transaction commit and a rollback, a using-scope auto-rollback, a UNIQUE
// constraint-violation failure, a MaxRows limit (including a large table
// that must stop at the limit without materializing the rest), use-after-close,
// a completed transaction handle that must not act on a later transaction,
// nil-safe methods on object literals, a database carried into a launched
// task, and a failing Commit that leaves the connection usable and
// transaction-free. Interpolation formats ints and floats so the program
// does not depend on std.convert.
const bunStdSQLiteProgram = `function IntStr(V: int) -> string {
    ` + "`${V}`" + `
}

function FloatStr(V: float) -> string {
    ` + "`${V}`" + `
}

function ReadValue(RowVals: Map<string, std.sqlite.Value>, Col: string) -> string {
    let Val = RowVals.Get(Col)
    if (Val == null) {
        "missing-" + Col
    } else {
        match Val {
            std.sqlite.Value.Null => "null"
            std.sqlite.Value.Integer(V) => IntStr(V)
            std.sqlite.Value.Float(V) => FloatStr(V)
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
    let IdDesc = if (LastId == null) { "none" } else { IntStr(LastId) }
    IntStr(Res.RowsAffected) + "|" + IdDesc
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
        Ok("commit:" + IntStr(Count))
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
        Ok("rollback:" + IntStr(Count))
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
        Ok("auto:" + IntStr(Count))
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
    Ok(StaleExec + "|" + StaleQuery + "|" + StaleCommit + "|" + StaleRollback + "|count:" + IntStr(Count))
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

function ExerciseLiteral() -> string throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Database {}
    let Stmt = std.sqlite.Statement { SQL: "SELECT 1", Parameters: [] }
    let ExecRes = match DB.Execute(Stmt) {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    let Tx = std.sqlite.Transaction {}
    let TxRes = match Tx.Commit() {
        Ok(_) => "ok"
        Err(Fail) => Fail.Message
    }
    DB.Close()
    Tx.Close()
    ExecRes + "|" + TxRes
}

function TaskHold(DB: std.sqlite.Database) -> int {
    7
}

// A database opened by the parent must stay usable from a launched task, which
// is only true when the connection lives with its owner rather than the caller.
function TaskQuery(DB: std.sqlite.Database) -> Result<int, std.sqlite.Failure> effects { database } {
    let Rows = DB.Query(std.sqlite.Query { SQL: "SELECT 41 + 1", Parameters: [], MaxRows: 10, MaxBytes: 4096 })?
    let First = Rows.Get(0)
    if (First == null) {
        Ok(0)
    } else {
        let Values = First.Values
        let Value = Values.Get("41 + 1")
        if (Value == null) {
            Ok(0)
        } else {
            match Value {
                std.sqlite.Value.Integer(N) => Ok(N)
                _ => Ok(0)
            }
        }
    }
}

function ExerciseTask() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    let DB = std.sqlite.Open(":memory:")?
    async let Work = TaskHold(DB)
    async let Queried = TaskQuery(DB)
    let Value = await Work
    let Answer = await Queried?
    DB.Close()
    Ok("task:" + IntStr(Value) + ":" + IntStr(Answer))
}

function ExerciseFailedCommit() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let Pragma = std.sqlite.Statement { SQL: "PRAGMA foreign_keys = ON", Parameters: [] }
        DB.Execute(Pragma)?
        let ParentStmt = std.sqlite.Statement { SQL: "CREATE TABLE parent (id INTEGER PRIMARY KEY)", Parameters: [] }
        DB.Execute(ParentStmt)?
        let ChildStmt = std.sqlite.Statement { SQL: "CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)", Parameters: [] }
        DB.Execute(ChildStmt)?
        let Tx = DB.Begin()?
        let InsertChild = std.sqlite.Statement { SQL: "INSERT INTO child (id, parent_id) VALUES (1, 999)", Parameters: [] }
        Tx.Execute(InsertChild)?
        let CommitRes = match Tx.Commit() {
            Ok(_) => "commit-ok"
            Err(Fail) => "commit-fail:" + Fail.Operation
        }
        let InsertParent = std.sqlite.Statement { SQL: "INSERT INTO parent (id) VALUES (1)", Parameters: [] }
        let After = match DB.Execute(InsertParent) {
            Ok(_) => "usable"
            Err(Fail) => "blocked:" + Fail.Message
        }
        let Tx2 = DB.Begin()?
        Tx2.Rollback()?
        Ok(CommitRes + "|" + After)
    }
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
    let Literal = ExerciseLiteral()
    let Task = match ExerciseTask() { Ok(S) => S, Err(Fail) => "task-err:" + DescribeFailure(Fail) }
    let FailedCommit = match ExerciseFailedCommit() { Ok(S) => S, Err(Fail) => "failed-commit-err:" + DescribeFailure(Fail) }
    CRUD + "|" + Commit + "|" + Rollback + "|" + Auto + "|" + Constraint + "|" + MaxRows + "|" + Closed + "|" + Stale + "|" + ClosedTx + "|" + ClosedDBTx + "|" + Literal + "|" + Task + "|" + FailedCommit
}
`

const bunStdSQLiteLargeMaxRowsProgram = `
function main() -> string throws std.sqlite.Failure effects { database } {
    match FillAndLimit() {
        Ok(S) => S
        Err(Fail) => "err:" + Fail.Operation + ":" + Fail.Message
    }
}

function FillAndLimit() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure effects { database } {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (id INT, val TEXT)", Parameters: [] }
        DB.Execute(CreateStmt)?
        let Fill = std.sqlite.Statement {
            SQL: "INSERT INTO t(id, val) WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 200) SELECT i, 'x' FROM n"
            Parameters: []
        }
        DB.Execute(Fill)?
        let Q = std.sqlite.Query { SQL: "SELECT id, val FROM t ORDER BY id", Parameters: [], MaxRows: 2, MaxBytes: 1048576 }
        match DB.Query(Q) {
            Ok(_) => Ok("large:succeeded")
            Err(Fail) => Ok("large:" + Fail.Operation + ":" + Fail.Message)
        }
    }
}
`

func TestBunStdSQLiteMatchesInterpreter(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: bunStdSQLiteProgram}
	interpreted, diagnostics, err := Run([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDiagnostics(t, diagnostics)
	if strings.TrimSpace(interpreted) == "" {
		t.Fatal("interpreter produced no output")
	}
	binary := buildBunTestProgram(t, source)
	output, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun binary error = %v, output = %q", err, output)
	}
	if string(output) != interpreted+"\n" {
		t.Fatalf("Bun output = %q, want interpreter output %q", output, interpreted+"\n")
	}
	t.Run("MaxRowsLargeTable", func(t *testing.T) {
		source := Source{Name: "main.slk", Namespace: "root", Text: bunStdSQLiteLargeMaxRowsProgram}
		interpreted, diagnostics, err := Run([]Source{source})
		if err != nil {
			t.Fatal(err)
		}
		requireNoDiagnostics(t, diagnostics)
		if interpreted != "large:Query:query exceeded maximum row limit of 2" {
			t.Fatalf("interpreter output = %q", interpreted)
		}
		binary := buildBunTestProgram(t, source)
		output, err := exec.Command(binary).CombinedOutput()
		if err != nil {
			t.Fatalf("Bun binary error = %v, output = %q", err, output)
		}
		if string(output) != interpreted+"\n" {
			t.Fatalf("Bun output = %q, want interpreter output %q", output, interpreted+"\n")
		}
	})

}
