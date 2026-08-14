package compiler_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"slick/internal/compiler"
)

func runSQLiteEverywhere(t *testing.T, source string) string {
	t.Helper()
	interpreted, diags, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: source}})
	assertNoDiagnostics(t, diags)
	if err != nil {
		t.Fatalf("interpreter error: %v", err)
	}

	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.slk")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	binary := filepath.Join(root, "app")
	buildDiags, err := compiler.BuildPath(sourcePath, binary)
	if err != nil {
		t.Fatalf("build native binary: %v", err)
	}
	assertNoDiagnostics(t, buildDiags)

	nativeOutput, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run native binary: %v: %s", err, nativeOutput)
	}
	native := strings.TrimSuffix(string(nativeOutput), "\n")

	if interpreted != native {
		t.Fatalf("interpreter produced %q, native binary produced %q", interpreted, native)
	}
	return interpreted
}

func TestStdSQLiteMemoryCRUDAndParity(t *testing.T) {
	source := `
function ReadId(RowVals: Map<string, std.sqlite.Value>) -> string {
    let Val = RowVals.Get("id")
    if (Val == null) {
        "missing-id"
    } else {
        match Val {
            std.sqlite.Value.Integer(V) => std.convert.IntToString(V)
            _ => "wrong-id-type"
        }
    }
}

function ReadTitle(RowVals: Map<string, std.sqlite.Value>) -> string {
    let Val = RowVals.Get("title")
    if (Val == null) {
        "missing-title"
    } else {
        match Val {
            std.sqlite.Value.Text(V) => V
            _ => "wrong-title-type"
        }
    }
}

function ReadPrice(RowVals: Map<string, std.sqlite.Value>) -> string {
    let Val = RowVals.Get("price")
    if (Val == null) {
        "missing-price"
    } else {
        match Val {
            std.sqlite.Value.Float(V) => std.convert.FloatToString(V)
            _ => "wrong-price-type"
        }
    }
}

function ReadData(RowVals: Map<string, std.sqlite.Value>) -> string {
    let Val = RowVals.Get("data")
    if (Val == null) {
        "missing-data"
    } else {
        match Val {
            std.sqlite.Value.Blob(V) => match std.bytes.ToUtf8(V) {
                Ok(Text) => Text
                Err(_) => "bad-utf8"
            }
            _ => "wrong-data-type"
        }
    }
}

function ReadExtra(RowVals: Map<string, std.sqlite.Value>) -> string {
    let Val = RowVals.Get("extra")
    if (Val == null) {
        "missing-extra"
    } else {
        match Val {
            std.sqlite.Value.Null => "is-null"
            _ => "wrong-extra-type"
        }
    }
}

function ProcessRow(First: std.sqlite.Row, Affected: int) -> string {
    let RowVals = First.Values
    let Id = ReadId(RowVals)
    let Title = ReadTitle(RowVals)
    let Price = ReadPrice(RowVals)
    let Data = ReadData(RowVals)
    let Extra = ReadExtra(RowVals)
    Id + "|" + Title + "|" + Price + "|" + Data + "|" + Extra + "|affected=" + std.convert.IntToString(Affected)
}

function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
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
                std.sqlite.Value.Blob(std.bytes.FromUtf8("binary-payload")),
                std.sqlite.Value.Null
            ]
        }
        let InsertRes = DB.Execute(InsertStmt)?

        let Query = std.sqlite.Query {
            SQL: "SELECT id, title, price, data, extra FROM items WHERE id = ?"
            Parameters: [std.sqlite.Value.Integer(1)]
            MaxRows: 10
            MaxBytes: 4096
        }
        let Rows = DB.Query(Query)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("no row")
        } else {
            Ok(ProcessRow(First, InsertRes.RowsAffected))
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(Res) => Res
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	want := "1|apple|1.5|binary-payload|is-null|affected=1"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteFileDatabaseAndPersistence(t *testing.T) {
	// 1. Interpreter write + read on file
	dbFileInterp := filepath.Join(t.TempDir(), "interp.db")
	sourceWriteInterp := fmt.Sprintf(`
function WriteData() -> Result<int, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(%s)? {
        let CreateStmt = std.sqlite.Statement {
            SQL: "CREATE TABLE notes (id INTEGER PRIMARY KEY, content TEXT)"
            Parameters: []
        }
        DB.Execute(CreateStmt)?

        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO notes (id, content) VALUES (?, ?)"
            Parameters: [std.sqlite.Value.Integer(42), std.sqlite.Value.Text("persisted note")]
        }
        let Res = DB.Execute(InsertStmt)?
        Ok(Res.RowsAffected)
    }
}

function main() -> string throws std.sqlite.Failure {
    match WriteData() {
        Ok(Affected) => "inserted=" + std.convert.IntToString(Affected)
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`, strconv.Quote(dbFileInterp))

	outWriteInterp, diags, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: sourceWriteInterp}})
	assertNoDiagnostics(t, diags)
	if err != nil || outWriteInterp != "inserted=1" {
		t.Fatalf("interpreter write failed: out=%q err=%v", outWriteInterp, err)
	}

	sourceReadInterp := fmt.Sprintf(`
function ExtractContent(Row: std.sqlite.Row) -> string {
    let Vals = Row.Values
    let Val = Vals.Get("content")
    if (Val == null) {
        "missing"
    } else {
        match Val {
            std.sqlite.Value.Text(T) => T
            _ => "bad-type"
        }
    }
}

function ReadData() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(%s)? {
        let Query = std.sqlite.Query {
            SQL: "SELECT content FROM notes WHERE id = ?"
            Parameters: [std.sqlite.Value.Integer(42)]
            MaxRows: 10
            MaxBytes: 1024
        }
        let Rows = DB.Query(Query)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("none")
        } else {
            Ok(ExtractContent(First))
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match ReadData() {
        Ok(Content) => Content
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`, strconv.Quote(dbFileInterp))

	outReadInterp, diags, err := compiler.Run([]compiler.Source{{Name: "main.slk", Namespace: "root", Text: sourceReadInterp}})
	assertNoDiagnostics(t, diags)
	if err != nil || outReadInterp != "persisted note" {
		t.Fatalf("interpreter read failed: out=%q err=%v", outReadInterp, err)
	}

	// 2. Native write + read on separate file across process restarts
	dbFileNative := filepath.Join(t.TempDir(), "native.db")
	sourceWriteNative := fmt.Sprintf(`
function WriteData() -> Result<int, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(%s)? {
        let CreateStmt = std.sqlite.Statement {
            SQL: "CREATE TABLE notes (id INTEGER PRIMARY KEY, content TEXT)"
            Parameters: []
        }
        DB.Execute(CreateStmt)?

        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO notes (id, content) VALUES (?, ?)"
            Parameters: [std.sqlite.Value.Integer(42), std.sqlite.Value.Text("persisted note")]
        }
        let Res = DB.Execute(InsertStmt)?
        Ok(Res.RowsAffected)
    }
}

function main() -> string throws std.sqlite.Failure {
    match WriteData() {
        Ok(Affected) => "inserted=" + std.convert.IntToString(Affected)
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`, strconv.Quote(dbFileNative))

	writeApp := filepath.Join(t.TempDir(), "write_app")
	sourceWritePath := filepath.Join(t.TempDir(), "write.slk")
	os.WriteFile(sourceWritePath, []byte(sourceWriteNative), 0o644)
	buildDiags, err := compiler.BuildPath(sourceWritePath, writeApp)
	if err != nil {
		t.Fatalf("build writeApp: %v", err)
	}
	assertNoDiagnostics(t, buildDiags)
	writeOut, err := exec.Command(writeApp).CombinedOutput()
	if err != nil || strings.TrimSpace(string(writeOut)) != "inserted=1" {
		t.Fatalf("writeApp run failed: out=%s err=%v", writeOut, err)
	}

	sourceReadNative := fmt.Sprintf(`
function ExtractContent(Row: std.sqlite.Row) -> string {
    let Vals = Row.Values
    let Val = Vals.Get("content")
    if (Val == null) {
        "missing"
    } else {
        match Val {
            std.sqlite.Value.Text(T) => T
            _ => "bad-type"
        }
    }
}

function ReadData() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(%s)? {
        let Query = std.sqlite.Query {
            SQL: "SELECT content FROM notes WHERE id = ?"
            Parameters: [std.sqlite.Value.Integer(42)]
            MaxRows: 10
            MaxBytes: 1024
        }
        let Rows = DB.Query(Query)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("none")
        } else {
            Ok(ExtractContent(First))
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match ReadData() {
        Ok(Content) => Content
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`, strconv.Quote(dbFileNative))

	readApp := filepath.Join(t.TempDir(), "read_app")
	sourceReadPath := filepath.Join(t.TempDir(), "read.slk")
	os.WriteFile(sourceReadPath, []byte(sourceReadNative), 0o644)
	buildDiags2, err := compiler.BuildPath(sourceReadPath, readApp)
	if err != nil {
		t.Fatalf("build readApp: %v", err)
	}
	assertNoDiagnostics(t, buildDiags2)
	readOut, err := exec.Command(readApp).CombinedOutput()
	if err != nil || strings.TrimSpace(string(readOut)) != "persisted note" {
		t.Fatalf("readApp run failed: out=%s err=%v", readOut, err)
	}
}

func TestStdSQLiteMissingParentDirectoryFails(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "nonexistent", "sub", "test.db")
	source := fmt.Sprintf(`
function main() -> string {
    match std.sqlite.Open(%s) {
        Ok(_) => "opened"
        Err(Fail) => Fail.Operation + "|" + (if (std.text.Contains(Fail.Message, "parent directory does not exist")) { "missing-dir" } else { Fail.Message })
    }
}
`, strconv.Quote(badPath))

	output := runSQLiteEverywhere(t, source)
	if output != "Open|missing-dir" {
		t.Fatalf("output = %q, want Open|missing-dir", output)
	}
}

func TestStdSQLiteParameterBindingAndInjectionSafety(t *testing.T) {
	source := `
function ExtractName(First: std.sqlite.Row) -> string {
    let Vals = First.Values
    let Val = Vals.Get("name")
    if (Val == null) {
        "missing"
    } else {
        match Val {
            std.sqlite.Value.Text(T) => T
            _ => "wrong-type"
        }
    }
}

function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement {
            SQL: "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"
            Parameters: []
        }
        DB.Execute(CreateStmt)?

        let DangerousPayload = "Robert'); DROP TABLE users; --"
        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO users (id, name) VALUES (1, ?)"
            Parameters: [std.sqlite.Value.Text(DangerousPayload)]
        }
        DB.Execute(InsertStmt)?

        let Query = std.sqlite.Query {
            SQL: "SELECT name FROM users WHERE id = 1"
            Parameters: []
            MaxRows: 10
            MaxBytes: 1024
        }
        let Rows = DB.Query(Query)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("none")
        } else {
            Ok(ExtractName(First))
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(Name) => Name
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	want := "Robert'); DROP TABLE users; --"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteQueryBoundsAndLimits(t *testing.T) {
	source := `
function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement {
            SQL: "CREATE TABLE t (id INTEGER, val TEXT)"
            Parameters: []
        }
        DB.Execute(CreateStmt)?

        let InsertStmt = std.sqlite.Statement {
            SQL: "INSERT INTO t VALUES (1, 'alpha'), (2, 'beta'), (3, 'gamma')"
            Parameters: []
        }
        DB.Execute(InsertStmt)?

        // 1. Exceed MaxRows
        let Q1 = std.sqlite.Query {
            SQL: "SELECT id, val FROM t ORDER BY id"
            Parameters: []
            MaxRows: 2
            MaxBytes: 1024
        }
        let ResRows = DB.Query(Q1)
        let RowExceeded = match ResRows {
            Ok(_) => "unbounded-rows-succeeded"
            Err(_) => "row-limit-hit"
        }

        // 2. Exceed MaxBytes
        let Q2 = std.sqlite.Query {
            SQL: "SELECT id, val FROM t ORDER BY id"
            Parameters: []
            MaxRows: 10
            MaxBytes: 5
        }
        let ResBytes = DB.Query(Q2)
        let ByteExceeded = match ResBytes {
            Ok(_) => "unbounded-bytes-succeeded"
            Err(_) => "byte-limit-hit"
        }

        // 3. Non-positive bounds
        let Q3 = std.sqlite.Query {
            SQL: "SELECT id FROM t"
            Parameters: []
            MaxRows: 0
            MaxBytes: 100
        }
        let ResZero = DB.Query(Q3)
        let ZeroRejected = match ResZero {
            Ok(_) => "zero-limit-succeeded"
            Err(_) => "zero-limit-hit"
        }

        Ok(RowExceeded + "|" + ByteExceeded + "|" + ZeroRejected)
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(Res) => Res
        Err(Fail) => Fail.Operation + ":" + Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	want := "row-limit-hit|byte-limit-hit|zero-limit-hit"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteDuplicateColumnsRejected(t *testing.T) {
	source := `
function QueryDuplicate(DB: std.sqlite.Database) -> string throws std.sqlite.Failure {
    let Q = std.sqlite.Query {
        SQL: "SELECT 1 AS x, 2 AS x"
        Parameters: []
        MaxRows: 10
        MaxBytes: 1024
    }
    match DB.Query(Q) {
        Ok(_) => "succeeded"
        Err(Fail) => Fail.Operation + ":" + (if (std.text.Contains(Fail.Message, "duplicate column name")) { "dup-col" } else { Fail.Message })
    }
}

function RunTest() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        Ok(QueryDuplicate(DB))
    }
}

function main() -> string throws std.sqlite.Failure {
    match RunTest() {
        Ok(Res) => Res
        Err(Fail) => Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "Query:dup-col" {
		t.Fatalf("output = %q, want Query:dup-col", output)
	}
}

func TestStdSQLiteSingleStatementEnforced(t *testing.T) {
	source := `
function ExecMulti(DB: std.sqlite.Database) -> string throws std.sqlite.Failure {
    let Stmt = std.sqlite.Statement {
        SQL: "CREATE TABLE a (x INT); CREATE TABLE b (y INT);"
        Parameters: []
    }
    match DB.Execute(Stmt) {
        Ok(_) => "multi-stmt-succeeded"
        Err(Fail) => Fail.Operation + ":" + (if (std.text.Contains(Fail.Message, "multiple SQL statements")) { "rejected-multi" } else { Fail.Message })
    }
}

function RunTest() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        Ok(ExecMulti(DB))
    }
}

function main() -> string throws std.sqlite.Failure {
    match RunTest() {
        Ok(Res) => Res
        Err(Fail) => Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "Execute:rejected-multi" {
		t.Fatalf("output = %q, want Execute:rejected-multi", output)
	}
}

func TestStdSQLiteTransactionsCommitRollbackAndUsingAutoRollback(t *testing.T) {
	source := `
function ExtractCount(First: std.sqlite.Row) -> int {
    let Vals = First.Values
    let Val = Vals.Get("c")
    if (Val == null) {
        0
    } else {
        match Val {
            std.sqlite.Value.Integer(C) => C
            _ => 0
        }
    }
}

function ReadCount(DB: std.sqlite.Database) -> Result<int, std.sqlite.Failure> {
    let Query = std.sqlite.Query { SQL: "SELECT COUNT(*) as c FROM t", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
    let Rows = DB.Query(Query)?
    let First = Rows.Get(0)
    if (First == null) {
        Ok(0)
    } else {
        Ok(ExtractCount(First))
    }
}

function TestCommit() -> Result<int, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?

        let Tx = DB.Begin()?
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (100)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Tx.Commit()?

        ReadCount(DB)
    }
}

function TestRollback() -> Result<int, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?

        let Tx = DB.Begin()?
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (100)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Tx.Rollback()?

        ReadCount(DB)
    }
}

function RunAutoRollback(DB: std.sqlite.Database) -> Result<null, std.sqlite.Failure> throws std.sqlite.Failure {
    using Tx = DB.Begin()? {
        let InsertStmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (999)", Parameters: [] }
        Tx.Execute(InsertStmt)?
        Ok(null)
    }
}

function TestUsingAutoRollback() -> Result<int, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let CreateStmt = std.sqlite.Statement { SQL: "CREATE TABLE t (v INT)", Parameters: [] }
        DB.Execute(CreateStmt)?
        RunAutoRollback(DB)?
        ReadCount(DB)
    }
}

function main() -> string throws std.sqlite.Failure {
    let C = match TestCommit() { Ok(V) => std.convert.IntToString(V), Err(_) => "err" }
    let R = match TestRollback() { Ok(V) => std.convert.IntToString(V), Err(_) => "err" }
    let U = match TestUsingAutoRollback() { Ok(V) => std.convert.IntToString(V), Err(_) => "err" }
    C + "|" + R + "|" + U
}
`
	output := runSQLiteEverywhere(t, source)
	want := "1|0|0"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteOperationsAfterClose(t *testing.T) {
	source := `
function TestClosedOps(DB: std.sqlite.Database) -> string {
    let Stmt = std.sqlite.Statement { SQL: "CREATE TABLE t(x)", Parameters: [] }
    let ExecRes = match DB.Execute(Stmt) {
        Ok(_) => "succeeded"
        Err(Fail) => "closed-exec"
    }
    let Q = std.sqlite.Query { SQL: "SELECT 1", Parameters: [], MaxRows: 1, MaxBytes: 10 }
    let QueryRes = match DB.Query(Q) {
        Ok(_) => "succeeded"
        Err(Fail) => "closed-query"
    }
    let BeginRes = match DB.Begin() {
        Ok(_) => "succeeded"
        Err(Fail) => "closed-begin"
    }
    ExecRes + "|" + QueryRes + "|" + BeginRes
}

function RunCloseFlow() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    let DB = std.sqlite.Open(":memory:")?
    DB.Close()
    Ok(TestClosedOps(DB))
}

function main() -> string throws std.sqlite.Failure {
    match RunCloseFlow() {
        Ok(S) => S
        Err(Fail) => Fail.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	want := "closed-exec|closed-query|closed-begin"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteObjectLiteralsSurviveWithoutPanic(t *testing.T) {
	source := `
function main() -> string {
    let RawDB = std.sqlite.Database {}
    let Stmt = std.sqlite.Statement { SQL: "SELECT 1", Parameters: [] }
    let E1 = match RawDB.Execute(Stmt) {
        Ok(_) => "ok"
        Err(_) => "err-exec"
    }
    let Q = std.sqlite.Query { SQL: "SELECT 1", Parameters: [], MaxRows: 1, MaxBytes: 10 }
    let E2 = match RawDB.Query(Q) {
        Ok(_) => "ok"
        Err(_) => "err-query"
    }
    let E3 = match RawDB.Begin() {
        Ok(_) => "ok"
        Err(_) => "err-begin"
    }

    let RawTx = std.sqlite.Transaction {}
    let T1 = match RawTx.Execute(Stmt) {
        Ok(_) => "ok"
        Err(_) => "err-tx-exec"
    }
    let T2 = match RawTx.Query(Q) {
        Ok(_) => "ok"
        Err(_) => "err-tx-query"
    }
    let T3 = match RawTx.Commit() {
        Ok(_) => "ok"
        Err(_) => "err-tx-commit"
    }
    let T4 = match RawTx.Rollback() {
        Ok(_) => "ok"
        Err(_) => "err-tx-rollback"
    }

    E1 + "|" + E2 + "|" + E3 + "|" + T1 + "|" + T2 + "|" + T3 + "|" + T4
}
`
	output := runSQLiteEverywhere(t, source)
	want := "err-exec|err-query|err-begin|err-tx-exec|err-tx-query|err-tx-commit|err-tx-rollback"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestStdSQLiteSharedHandleConcurrentWrites(t *testing.T) {
	source := `
function WriteOne(DB: std.sqlite.Database, Value: int) -> Result<null, std.sqlite.Failure> {
    let Stmt = std.sqlite.Statement {
        SQL: "INSERT INTO t(x) VALUES (?)"
        Parameters: [std.sqlite.Value.Integer(Value)]
    }
    DB.Execute(Stmt)?
    Ok(null)
}

function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let Create = std.sqlite.Statement { SQL: "CREATE TABLE t(x INT)", Parameters: [] }
        DB.Execute(Create)?
        async let A = WriteOne(DB, 1)
        async let B = WriteOne(DB, 2)
        let FirstWrite = await A?
        let SecondWrite = await B?
        let Q = std.sqlite.Query { SQL: "SELECT COUNT(*) as c FROM t", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
        let Rows = DB.Query(Q)?
        let First = Rows.Get(0)
        if (First == null) {
            Ok("none")
        } else {
            let Vals = First.Values
            let Val = Vals.Get("c")
            if (Val == null) {
                Ok("missing")
            } else {
                match Val {
                    std.sqlite.Value.Integer(C) => Ok(std.convert.IntToString(C))
                    _ => Ok("bad")
                }
            }
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(S) => S
        Err(F) => F.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "2" {
		t.Fatalf("shared-handle concurrent writes = %q, want 2", output)
	}
}

func TestStdSQLiteMemoryTransactionDoesNotHang(t *testing.T) {
	source := `
function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let Create = std.sqlite.Statement { SQL: "CREATE TABLE t(x INT)", Parameters: [] }
        DB.Execute(Create)?
        using Tx = DB.Begin()? {
            let Insert = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1)", Parameters: [] }
            Tx.Execute(Insert)?
            let Outside = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (2)", Parameters: [] }
            let During = match DB.Execute(Outside) {
                Ok(_) => "ok"
                Err(_) => "fail"
            }
            Tx.Commit()?
            Ok(During)
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(S) => S
        Err(F) => F.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "fail" {
		t.Fatalf("database execute during transaction = %q, want fail", output)
	}
}

func TestStdSQLiteCloseInvalidatesTransaction(t *testing.T) {
	source := `
function AfterClose(Tx: std.sqlite.Transaction) -> string {
    let Stmt = std.sqlite.Statement { SQL: "INSERT INTO t VALUES (1)", Parameters: [] }
    match Tx.Execute(Stmt) {
        Ok(_) => "executed"
        Err(_) => "inactive"
    }
}

function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    let DB = std.sqlite.Open(":memory:")?
    let Create = std.sqlite.Statement { SQL: "CREATE TABLE t(x INT)", Parameters: [] }
    DB.Execute(Create)?
    let Tx = DB.Begin()?
    DB.Close()
    Ok(AfterClose(Tx))
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(S) => S
        Err(F) => F.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "inactive" {
		t.Fatalf("transaction after database close = %q, want inactive", output)
	}
}

func TestStdSQLiteValueOnlyProgramBuilds(t *testing.T) {
	source := `
function main() -> string {
    match std.sqlite.Value.Integer(1) {
        std.sqlite.Value.Integer(V) => std.convert.IntToString(V)
        _ => "other"
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "1" {
		t.Fatalf("value-only program = %q, want 1", output)
	}
}

func TestStdSQLiteRejectsInvalidResultCells(t *testing.T) {
	source := `
function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let Inf = std.sqlite.Query { SQL: "SELECT 1e308 * 1e308 as v", Parameters: [], MaxRows: 10, MaxBytes: 1024 }
        match DB.Query(Inf) {
            Ok(_) => Ok("inf-ok")
            Err(_) => Ok("inf-fail")
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(S) => S
        Err(F) => F.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "inf-fail" {
		t.Fatalf("non-finite query cell = %q, want inf-fail", output)
	}
}

func TestStdSQLitePreservesZeroLastInsertId(t *testing.T) {
	source := `
function Exercise() -> Result<string, std.sqlite.Failure> throws std.sqlite.Failure {
    using DB = std.sqlite.Open(":memory:")? {
        let Create = std.sqlite.Statement { SQL: "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT)", Parameters: [] }
        DB.Execute(Create)?
        let Insert = std.sqlite.Statement {
            SQL: "INSERT INTO t(id, name) VALUES (?, ?)"
            Parameters: [std.sqlite.Value.Integer(0), std.sqlite.Value.Text("zero")]
        }
        let Res = DB.Execute(Insert)?
        let Id = Res.LastInsertId
        if (Id == null) {
            Ok("absent")
        } else {
            Ok(std.convert.IntToString(Id))
        }
    }
}

function main() -> string throws std.sqlite.Failure {
    match Exercise() {
        Ok(S) => S
        Err(F) => F.Message
    }
}
`
	output := runSQLiteEverywhere(t, source)
	if output != "0" {
		t.Fatalf("LastInsertId after explicit 0 = %q, want 0", output)
	}
}
