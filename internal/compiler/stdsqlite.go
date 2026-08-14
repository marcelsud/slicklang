package compiler

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	_ "modernc.org/sqlite"
	sqliteLib "modernc.org/sqlite"
)

const (
	nativeStdSQLiteOpen                nativeFunction = "std.sqlite.Open"
	nativeStdSQLiteDatabaseExecute     nativeFunction = "std.sqlite.Database.Execute"
	nativeStdSQLiteDatabaseQuery       nativeFunction = "std.sqlite.Database.Query"
	nativeStdSQLiteDatabaseBegin       nativeFunction = "std.sqlite.Database.Begin"
	nativeStdSQLiteDatabaseClose       nativeFunction = "std.sqlite.Database.Close"
	nativeStdSQLiteTransactionExecute  nativeFunction = "std.sqlite.Transaction.Execute"
	nativeStdSQLiteTransactionQuery    nativeFunction = "std.sqlite.Transaction.Query"
	nativeStdSQLiteTransactionCommit   nativeFunction = "std.sqlite.Transaction.Commit"
	nativeStdSQLiteTransactionRollback nativeFunction = "std.sqlite.Transaction.Rollback"
	nativeStdSQLiteTransactionClose    nativeFunction = "std.sqlite.Transaction.Close"

	stdSQLiteValueName       = "std.sqlite.Value"
	stdSQLiteStatementName   = "std.sqlite.Statement"
	stdSQLiteQueryName       = "std.sqlite.Query"
	stdSQLiteRowName         = "std.sqlite.Row"
	stdSQLiteExecutionName   = "std.sqlite.Execution"
	stdSQLiteFailureName     = "std.sqlite.Failure"
	stdSQLiteDatabaseName    = "std.sqlite.Database"
	stdSQLiteTransactionName = "std.sqlite.Transaction"

	sqlitePinnedGoMod = `module slickapp

go 1.25.0

require modernc.org/sqlite v1.56.0

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
`

	sqlitePinnedGoSum = `github.com/dustin/go-humanize v1.0.1 h1:GzkhY7T5VNhEkwH0PVJgjz+fX1rhBrR7pRT3mDkpeCY=
github.com/dustin/go-humanize v1.0.1/go.mod h1:Mu1zIs6XwVuF/gI1OepvI0qD18qycQx+mFykh5fBlto=
github.com/google/pprof v0.0.0-20260802141513-ef3492d7dac3 h1:LMLX+LgTNWpfvCBdFebv6EsYotImrt/Ppc5cXIriCSo=
github.com/google/pprof v0.0.0-20260802141513-ef3492d7dac3/go.mod h1:jl5iWTm0/hd5PjEYEOuwAJ57L/CibdZfrqZ5XA5GrCk=
github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=
github.com/google/uuid v1.6.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=
github.com/hashicorp/golang-lru/v2 v2.0.7 h1:a+bsQ5rvGLjzHuww6tVxozPZFVghXaHOwFs4luLUK2k=
github.com/hashicorp/golang-lru/v2 v2.0.7/go.mod h1:QeFd9opnmA6QUJc5vARoKUSoFhyfM2/ZepoAG6RGpeM=
github.com/mattn/go-isatty v0.0.24 h1:tGZZoVgT/KiqK1c8ocVLeDS8BSWMRd47J3Lbz7vsReI=
github.com/mattn/go-isatty v0.0.24/go.mod h1:nMCL3Zebbrt45jsMDgnfIwz6ydEQApk5oEI3HqDio6A=
github.com/ncruces/go-strftime v1.0.0 h1:HMFp8mLCTPp341M/ZnA4qaf7ZlsbTc+miZjCLOFAw7w=
github.com/ncruces/go-strftime v1.0.0/go.mod h1:Fwc5htZGVVkseilnfgOVb9mKy6w1naJmn9CehxcKcls=
github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec h1:W09IVJc94icq4NjY3clb7Lk8O1qJ8BdBEF8z0ibU0rE=
github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec/go.mod h1:qqbHyh8v60DhA7CoWK5oRCqLrMHRGoxYCSS9EjAz6Eo=
golang.org/x/mod v0.37.0 h1:vF1DjpVEshcIqoEaauuHebaLk1O1forxjxBaVn884JQ=
golang.org/x/mod v0.37.0/go.mod h1:m8S8VeM9r4dzDwjrKO0a1sZP3YjeMamRRlD+fmR2Q/0=
golang.org/x/sync v0.21.0 h1:HLII4xRRTtCRkxYp4HNFF0Js/Og6q2i++KXbg0gHCwM=
golang.org/x/sync v0.21.0/go.mod h1:9xrNwdLfx4jkKbNva9FpL6vEN7evnE43NNNJQ2LF3+0=
golang.org/x/sys v0.47.0 h1:o7XGOvZQCADBQQ4Y7VNq2dRWQR7JmOUW8Kxx4ZsNgWs=
golang.org/x/sys v0.47.0/go.mod h1:4GL1E5IUh+htKOUEOaiffhrAeqysfVGipDYzABqnCmw=
golang.org/x/tools v0.47.0 h1:7Kn5x/d1svx/PzryTsqeoZN4TZwqeH5pGWjefhLi/1Q=
golang.org/x/tools v0.47.0/go.mod h1:dFHnyTvFWY212G+h7ZY4Vsp/K3U4/7W9TyVaAul8uCA=
modernc.org/cc/v4 v4.29.1 h1:MKgdCV3WykTSPqpVrnxdEDS0HEd2FHpKZDzxzU5LyeI=
modernc.org/cc/v4 v4.29.1/go.mod h1:OnovgIhbbMXMu1aISnJ0wvVD1KnW+cAUJkIrAWh+kVI=
modernc.org/ccgo/v4 v4.34.6 h1:sBgfIwyN0TQ9C5hwIeuqyeAKyMWnbvj2fvpF4L11uzU=
modernc.org/ccgo/v4 v4.34.6/go.mod h1:SZ8YcN9NG7XVsQYdm6jYBvi8PQP1qi+kqB6OhjqI3Fk=
modernc.org/fileutil v1.4.0 h1:j6ZzNTftVS054gi281TyLjHPp6CPHr2KCxEXjEbD6SM=
modernc.org/fileutil v1.4.0/go.mod h1:EqdKFDxiByqxLk8ozOxObDSfcVOv/54xDs/DUHdvCUU=
modernc.org/gc/v2 v2.6.5 h1:nyqdV8q46KvTpZlsw66kWqwXRHdjIlJOhG6kxiV/9xI=
modernc.org/gc/v2 v2.6.5/go.mod h1:YgIahr1ypgfe7chRuJi2gD7DBQiKSLMPgBQe9oIiito=
modernc.org/gc/v3 v3.1.4 h1:2g65LGVSmFQrXeITAw97x7hCRvZFcyE1uDP+7Vng7JI=
modernc.org/gc/v3 v3.1.4/go.mod h1:HFK/6AGESC7Ex+EZJhJ2Gni6cTaYpSMmU/cT9RmlfYY=
modernc.org/goabi0 v0.2.0 h1:HvEowk7LxcPd0eq6mVOAEMai46V+i7Jrj13t4AzuNks=
modernc.org/goabi0 v0.2.0/go.mod h1:CEFRnnJhKvWT1c1JTI3Avm+tgOWbkOu5oPA8eH8LnMI=
modernc.org/libc v1.74.4 h1:fX1Omw4o2/1C2iRkkIsrQTasJQldLhRmuPreXLoWs9k=
modernc.org/libc v1.74.4/go.mod h1:eeQAS9W3sZeKYMFubydxJpII9ybHWshk+7or7bLG9co=
modernc.org/mathutil v1.7.1 h1:GCZVGXdaN8gTqB1Mf/usp1Y/hSqgI2vAGGP4jZMCxOU=
modernc.org/mathutil v1.7.1/go.mod h1:4p5IwJITfppl0G4sUEDtCr4DthTaT47/N3aT6MhfgJg=
modernc.org/memory v1.11.0 h1:o4QC8aMQzmcwCK3t3Ux/ZHmwFPzE6hf2Y5LbkRs+hbI=
modernc.org/memory v1.11.0/go.mod h1:/JP4VbVC+K5sU2wZi9bHoq2MAkCnrt2r98UGeSK7Mjw=
modernc.org/opt v0.2.0 h1:tGyef5ApycA7FSEOMraay9SaTk5zmbx7Tu+cJs4QKZg=
modernc.org/opt v0.2.0/go.mod h1:03fq9lsNfvkYSfxrfUhZCWPk1lm4cq4N+Bh//bEtgns=
modernc.org/sortutil v1.2.1 h1:+xyoGf15mM3NMlPDnFqrteY07klSFxLElE2PVuWIJ7w=
modernc.org/sortutil v1.2.1/go.mod h1:7ZI3a3REbai7gzCLcotuw9AC4VZVpYMjDzETGsSMqJE=
modernc.org/sqlite v1.56.0 h1:/D8e2RfFqoy/Zc6PuC76U28zFwmI/sYx1Kjm4yEn9e0=
modernc.org/sqlite v1.56.0/go.mod h1:yCJ2cmAaIkHQ25oXWrF8H4O1lIfPYPR26yCEDj2P3pQ=
modernc.org/strutil v1.2.1 h1:UneZBkQA+DX2Rp35KcM69cSsNES9ly8mQWD71HKlOA0=
modernc.org/strutil v1.2.1/go.mod h1:EHkiggD70koQxjVdSBM3JKM7k6L0FbGE5eymy9i3B9A=
modernc.org/token v1.1.0 h1:Xl7Ap9dKaEs5kLoOQeQmPWevfnk/DM5qcLcYlA8ys6Y=
modernc.org/token v1.1.0/go.mod h1:UGzOrNV1mAFSEB63lOFHIpNRUVMvYTc6yu1SMY/XTDM=
`
)

type sqliteFailureData struct {
	operation string
	code      *int64
	message   string
}

func sqliteFailure(operation string, code *int64, message string) *sqliteFailureData {
	return &sqliteFailureData{
		operation: operation,
		code:      code,
		message:   message,
	}
}

func sqliteFailureFromError(operation string, err error) *sqliteFailureData {
	if err == nil {
		return &sqliteFailureData{operation: operation, message: "unknown error"}
	}
	var sqliteErr *sqliteLib.Error
	var code *int64
	if errors.As(err, &sqliteErr) {
		c := int64(sqliteErr.Code())
		code = &c
	}
	return &sqliteFailureData{
		operation: operation,
		code:      code,
		message:   err.Error(),
	}
}

type nativeSQLiteResource struct {
	database    *nativeSQLiteDatabase
	transaction *nativeSQLiteTransaction
}

type nativeSQLiteDatabase struct {
	mu       sync.Mutex
	db       *sql.DB
	path     string
	closed   bool
	activeTx *nativeSQLiteTransaction
}

type nativeSQLiteTransaction struct {
	mu     sync.Mutex
	tx     *sql.Tx
	db     *nativeSQLiteDatabase
	state  int // 0: active, 1: committed, 2: rolled back
	closed bool
}

var sqliteMemorySeq atomic.Uint64

func sqliteOpenDSN(path string) string {
	if path != ":memory:" {
		return path
	}
	return fmt.Sprintf("file:slick-memory-%d?mode=memory&cache=shared", sqliteMemorySeq.Add(1))
}

func sqliteTxInactive(tx *nativeSQLiteTransaction) bool {
	return tx == nil || tx.closed || tx.state != 0 || tx.db == nil || tx.db.closed
}
func sqliteClearActiveTx(db *nativeSQLiteDatabase, tx *nativeSQLiteTransaction) {
	if db != nil && db.activeTx == tx {
		db.activeTx = nil
	}
}

func sqliteLockDB(tx *nativeSQLiteTransaction) func() {
	if tx == nil || tx.db == nil {
		return func() {}
	}
	tx.db.mu.Lock()
	return tx.db.mu.Unlock
}

// validateSingleSQL verifies that sql contains exactly one statement.
// Trailing comments and whitespace are allowed, but multiple statements or empty input are rejected.
func validateSingleSQL(sqlText string, op string) *sqliteFailureData {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return sqliteFailure(op, nil, "SQL statement must not be empty")
	}
	hasContent := false
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false
	sawSemicolon := false

	chars := []rune(sqlText)
	for i := 0; i < len(chars); i++ {
		c := chars[i]

		if inLineComment {
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(chars) && chars[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inSingleQuote {
			if c == '\'' {
				if i+1 < len(chars) && chars[i+1] == '\'' {
					i++ // escaped quote
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		if inDoubleQuote {
			if c == '"' {
				if i+1 < len(chars) && chars[i+1] == '"' {
					i++ // escaped quote
				} else {
					inDoubleQuote = false
				}
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}

		if c == '-' && i+1 < len(chars) && chars[i+1] == '-' {
			inLineComment = true
			i++
			continue
		}
		if c == '/' && i+1 < len(chars) && chars[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}
		if c == '\'' {
			inSingleQuote = true
			hasContent = true
			continue
		}
		if c == '"' {
			inDoubleQuote = true
			hasContent = true
			continue
		}
		if c == '`' {
			inBacktick = true
			hasContent = true
			continue
		}

		if c == ';' {
			sawSemicolon = true
			continue
		}

		if !strings.ContainsRune(" \t\r\n", c) {
			if sawSemicolon {
				return sqliteFailure(op, nil, "statement contains multiple SQL statements")
			}
			hasContent = true
		}
	}

	if !hasContent {
		return sqliteFailure(op, nil, "SQL statement must not be empty")
	}
	return nil
}

func convertSlickValueToSQL(val runtimeValue) (any, error) {
	if val.variant == nil {
		return nil, errors.New("invalid SQLite parameter value")
	}
	switch val.variant.name {
	case "Null":
		return nil, nil
	case "Integer":
		if len(val.variant.fields) != 1 {
			return nil, errors.New("invalid Integer variant payload")
		}
		return val.variant.fields[0].scalar.(int64), nil
	case "Float":
		if len(val.variant.fields) != 1 {
			return nil, errors.New("invalid Float variant payload")
		}
		f := val.variant.fields[0].scalar.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.New("cannot bind non-finite floating-point value")
		}
		return f, nil
	case "Text":
		if len(val.variant.fields) != 1 {
			return nil, errors.New("invalid Text variant payload")
		}
		text := val.variant.fields[0].scalar.(string)
		if !utf8.ValidString(text) {
			return nil, errors.New("text parameter contains invalid UTF-8")
		}
		return text, nil
	case "Blob":
		if len(val.variant.fields) != 1 {
			return nil, errors.New("invalid Blob variant payload")
		}
		data := val.variant.fields[0].scalar.([]byte)
		return append([]byte(nil), data...), nil
	default:
		return nil, fmt.Errorf("unknown SQLite Value variant %s", val.variant.name)
	}
}
func convertSQLValueToSlick(p *program, val any) (runtimeValue, *sqliteFailureData) {
	union := p.unions[stdSQLiteValueName]
	if val == nil {
		return p.runtimeVariantValue(union, union.variants["Null"], nil), nil
	}
	switch v := val.(type) {
	case int64:
		return p.runtimeVariantValue(union, union.variants["Integer"], []runtimeValue{{typ: "int", scalar: v}}), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return runtimeValue{}, sqliteFailure("Query", nil, "query returned a non-finite floating-point value")
		}
		return p.runtimeVariantValue(union, union.variants["Float"], []runtimeValue{{typ: "float", scalar: v}}), nil
	case string:
		if !utf8.ValidString(v) {
			return runtimeValue{}, sqliteFailure("Query", nil, "query returned invalid UTF-8 text")
		}
		return p.runtimeVariantValue(union, union.variants["Text"], []runtimeValue{{typ: "string", scalar: v}}), nil
	case []byte:
		return p.runtimeVariantValue(union, union.variants["Blob"], []runtimeValue{{typ: "bytes", scalar: append([]byte(nil), v...)}}), nil
	case bool:
		intVal := int64(0)
		if v {
			intVal = 1
		}
		return p.runtimeVariantValue(union, union.variants["Integer"], []runtimeValue{{typ: "int", scalar: intVal}}), nil
	default:
		return runtimeValue{}, sqliteFailure("Query", nil, "query returned an unsupported SQLite value")
	}
}

func sqliteLastInsertID(res sql.Result) *runtimeOptional {
	lastInsertId, err := res.LastInsertId()
	if err != nil {
		return &runtimeOptional{}
	}
	return &runtimeOptional{present: true, value: runtimeValue{typ: "int", scalar: lastInsertId}}
}

func runtimeSQLiteFailureValue(failure *sqliteFailureData) runtimeValue {
	optional := &runtimeOptional{}
	if failure.code != nil {
		optional.present = true
		optional.value = runtimeValue{typ: "int", scalar: *failure.code}
	}
	return runtimeValue{
		typ: stdSQLiteFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: failure.operation},
			"Code":      {typ: "int?", optional: optional},
			"Message":   {typ: "string", scalar: failure.message},
		},
	}
}

func runtimeSQLiteFailureResult(resultType string, failure *sqliteFailureData) runtimeValue {
	return runtimeResultValue(resultType, false, runtimeSQLiteFailureValue(failure))
}

func (p *program) callNativeStdSQLite(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdSQLiteOpen:
		path := frame.locals["Path"].scalar.(string)
		if path != ":memory:" {
			cleanPath := filepath.Clean(path)
			dir := filepath.Dir(cleanPath)
			if dir != "." && dir != "" && dir != "/" {
				stat, err := os.Stat(dir)
				if err != nil || !stat.IsDir() {
					failure := sqliteFailure("Open", nil, fmt.Sprintf("parent directory does not exist: %s", dir))
					return runtimeSQLiteFailureResult(resultType, failure), nil, true
				}
			}
		}
		db, err := sql.Open("sqlite", sqliteOpenDSN(path))
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Open", err)), nil, true
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Open", err)), nil, true
		}
		nativeDB := &nativeSQLiteDatabase{
			db:   db,
			path: path,
		}
		resVal := runtimeValue{
			typ:    stdSQLiteDatabaseName,
			sqlite: &nativeSQLiteResource{database: nativeDB},
		}
		return runtimeResultValue(resultType, true, resVal), nil, true

	case nativeStdSQLiteDatabaseExecute:
		self := frame.locals["self"].sqlite
		if self == nil || self.database == nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, "database is closed")), nil, true
		}
		stmtVal := frame.locals["Statement"]
		sqlText := stmtVal.fields["SQL"].scalar.(string)
		if f := validateSingleSQL(sqlText, "Execute"); f != nil {
			return runtimeSQLiteFailureResult(resultType, f), nil, true
		}

		paramElements := stmtVal.fields["Parameters"].elements
		params := make([]any, len(paramElements))
		for i, el := range paramElements {
			sqlVal, err := convertSlickValueToSQL(el)
			if err != nil {
				return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, err.Error())), nil, true
			}
			params[i] = sqlVal
		}

		self.database.mu.Lock()
		defer self.database.mu.Unlock()
		if self.database.closed {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, "database is closed")), nil, true
		}
		if self.database.activeTx != nil && !self.database.activeTx.closed && self.database.activeTx.state == 0 {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, "a transaction is already active")), nil, true
		}

		res, err := self.database.db.Exec(sqlText, params...)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Execute", err)), nil, true
		}

		rowsAffected, _ := res.RowsAffected()
		execVal := runtimeValue{
			typ: stdSQLiteExecutionName,
			fields: map[string]runtimeValue{
				"RowsAffected": {typ: "int", scalar: rowsAffected},
				"LastInsertId": {typ: "int?", optional: sqliteLastInsertID(res)},
			},
		}
		return runtimeResultValue(resultType, true, execVal), nil, true

	case nativeStdSQLiteDatabaseQuery:
		self := frame.locals["self"].sqlite
		if self == nil || self.database == nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "database is closed")), nil, true
		}
		queryVal := frame.locals["Query"]
		sqlText := queryVal.fields["SQL"].scalar.(string)
		maxRows := queryVal.fields["MaxRows"].scalar.(int64)
		maxBytes := queryVal.fields["MaxBytes"].scalar.(int64)

		if maxRows <= 0 || maxBytes <= 0 {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "MaxRows and MaxBytes must be greater than zero")), nil, true
		}

		if f := validateSingleSQL(sqlText, "Query"); f != nil {
			return runtimeSQLiteFailureResult(resultType, f), nil, true
		}

		paramElements := queryVal.fields["Parameters"].elements
		params := make([]any, len(paramElements))
		for i, el := range paramElements {
			sqlVal, err := convertSlickValueToSQL(el)
			if err != nil {
				return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, err.Error())), nil, true
			}
			params[i] = sqlVal
		}

		self.database.mu.Lock()
		defer self.database.mu.Unlock()
		if self.database.closed {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "database is closed")), nil, true
		}
		if self.database.activeTx != nil && !self.database.activeTx.closed && self.database.activeTx.state == 0 {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "a transaction is already active")), nil, true
		}

		rows, err := self.database.db.Query(sqlText, params...)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Query", err)), nil, true
		}
		defer rows.Close()

		rowList, failure := p.scanSQLiteRows(rows, maxRows, maxBytes)
		if failure != nil {
			return runtimeSQLiteFailureResult(resultType, failure), nil, true
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: stdSQLiteRowName + "[]", elements: rowList}), nil, true

	case nativeStdSQLiteDatabaseBegin:
		self := frame.locals["self"].sqlite
		if self == nil || self.database == nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Begin", nil, "database is closed")), nil, true
		}

		self.database.mu.Lock()
		defer self.database.mu.Unlock()
		if self.database.closed {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Begin", nil, "database is closed")), nil, true
		}
		if self.database.activeTx != nil && !self.database.activeTx.closed && self.database.activeTx.state == 0 {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Begin", nil, "a transaction is already active")), nil, true
		}

		tx, err := self.database.db.Begin()
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Begin", err)), nil, true
		}
		nativeTx := &nativeSQLiteTransaction{
			tx:    tx,
			db:    self.database,
			state: 0,
		}
		self.database.activeTx = nativeTx
		resVal := runtimeValue{
			typ:    stdSQLiteTransactionName,
			sqlite: &nativeSQLiteResource{transaction: nativeTx},
		}
		return runtimeResultValue(resultType, true, resVal), nil, true

	case nativeStdSQLiteDatabaseClose:
		self := frame.locals["self"].sqlite
		if self == nil || self.database == nil {
			return nullRuntimeValue(), nil, true
		}

		self.database.mu.Lock()
		defer self.database.mu.Unlock()
		if self.database.closed {
			return nullRuntimeValue(), nil, true
		}
		self.database.closed = true
		if active := self.database.activeTx; active != nil && !active.closed && active.state == 0 {
			active.closed = true
			active.state = 2
			_ = active.tx.Rollback()
		}
		self.database.activeTx = nil
		if err := self.database.db.Close(); err != nil {
			failure := sqliteFailureFromError("Close", err)
			return runtimeValue{}, &slickThrow{typ: stdSQLiteFailureName, message: failure.message, value: runtimeSQLiteFailureValue(failure)}, true
		}
		return nullRuntimeValue(), nil, true
	case nativeStdSQLiteTransactionExecute:
		self := frame.locals["self"].sqlite
		var tx *nativeSQLiteTransaction
		if self != nil {
			tx = self.transaction
		}
		unlock := sqliteLockDB(tx)
		defer unlock()
		if sqliteTxInactive(tx) {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, "transaction is no longer active")), nil, true
		}
		stmtVal := frame.locals["Statement"]
		sqlText := stmtVal.fields["SQL"].scalar.(string)
		if f := validateSingleSQL(sqlText, "Execute"); f != nil {
			return runtimeSQLiteFailureResult(resultType, f), nil, true
		}
		paramElements := stmtVal.fields["Parameters"].elements
		params := make([]any, len(paramElements))
		for i, el := range paramElements {
			sqlVal, err := convertSlickValueToSQL(el)
			if err != nil {
				return runtimeSQLiteFailureResult(resultType, sqliteFailure("Execute", nil, err.Error())), nil, true
			}
			params[i] = sqlVal
		}
		res, err := tx.tx.Exec(sqlText, params...)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Execute", err)), nil, true
		}
		rowsAffected, _ := res.RowsAffected()
		return runtimeResultValue(resultType, true, runtimeValue{
			typ: stdSQLiteExecutionName,
			fields: map[string]runtimeValue{
				"RowsAffected": {typ: "int", scalar: rowsAffected},
				"LastInsertId": {typ: "int?", optional: sqliteLastInsertID(res)},
			},
		}), nil, true

	case nativeStdSQLiteTransactionQuery:
		self := frame.locals["self"].sqlite
		var tx *nativeSQLiteTransaction
		if self != nil {
			tx = self.transaction
		}
		unlock := sqliteLockDB(tx)
		defer unlock()
		if sqliteTxInactive(tx) {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "transaction is no longer active")), nil, true
		}
		queryVal := frame.locals["Query"]
		sqlText := queryVal.fields["SQL"].scalar.(string)
		maxRows := queryVal.fields["MaxRows"].scalar.(int64)
		maxBytes := queryVal.fields["MaxBytes"].scalar.(int64)
		if maxRows <= 0 || maxBytes <= 0 {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, "MaxRows and MaxBytes must be greater than zero")), nil, true
		}
		if f := validateSingleSQL(sqlText, "Query"); f != nil {
			return runtimeSQLiteFailureResult(resultType, f), nil, true
		}
		paramElements := queryVal.fields["Parameters"].elements
		params := make([]any, len(paramElements))
		for i, el := range paramElements {
			sqlVal, err := convertSlickValueToSQL(el)
			if err != nil {
				return runtimeSQLiteFailureResult(resultType, sqliteFailure("Query", nil, err.Error())), nil, true
			}
			params[i] = sqlVal
		}
		rows, err := tx.tx.Query(sqlText, params...)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Query", err)), nil, true
		}
		defer rows.Close()
		rowList, failure := p.scanSQLiteRows(rows, maxRows, maxBytes)
		if failure != nil {
			return runtimeSQLiteFailureResult(resultType, failure), nil, true
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: stdSQLiteRowName + "[]", elements: rowList}), nil, true

	case nativeStdSQLiteTransactionCommit:
		self := frame.locals["self"].sqlite
		var tx *nativeSQLiteTransaction
		if self != nil {
			tx = self.transaction
		}
		unlock := sqliteLockDB(tx)
		defer unlock()
		if sqliteTxInactive(tx) {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Commit", nil, "transaction is no longer active")), nil, true
		}
		tx.state = 1
		err := tx.tx.Commit()
		sqliteClearActiveTx(tx.db, tx)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Commit", err)), nil, true
		}
		return runtimeResultValue(resultType, true, nullRuntimeValue()), nil, true

	case nativeStdSQLiteTransactionRollback:
		self := frame.locals["self"].sqlite
		var tx *nativeSQLiteTransaction
		if self != nil {
			tx = self.transaction
		}
		unlock := sqliteLockDB(tx)
		defer unlock()
		if sqliteTxInactive(tx) {
			return runtimeSQLiteFailureResult(resultType, sqliteFailure("Rollback", nil, "transaction is no longer active")), nil, true
		}
		tx.state = 2
		err := tx.tx.Rollback()
		sqliteClearActiveTx(tx.db, tx)
		if err != nil {
			return runtimeSQLiteFailureResult(resultType, sqliteFailureFromError("Rollback", err)), nil, true
		}
		return runtimeResultValue(resultType, true, nullRuntimeValue()), nil, true

	case nativeStdSQLiteTransactionClose:
		self := frame.locals["self"].sqlite
		var tx *nativeSQLiteTransaction
		if self != nil {
			tx = self.transaction
		}
		if tx == nil {
			return nullRuntimeValue(), nil, true
		}
		unlock := sqliteLockDB(tx)
		defer unlock()
		if tx.closed {
			return nullRuntimeValue(), nil, true
		}
		tx.closed = true
		if tx.state == 0 {
			tx.state = 2
			err := tx.tx.Rollback()
			sqliteClearActiveTx(tx.db, tx)
			if err != nil {
				failure := sqliteFailureFromError("Close", err)
				return runtimeValue{}, &slickThrow{typ: stdSQLiteFailureName, message: failure.message, value: runtimeSQLiteFailureValue(failure)}, true
			}
		}
		return nullRuntimeValue(), nil, true

	default:
		return runtimeValue{}, nil, false
	}
}

func (p *program) scanSQLiteRows(rows *sql.Rows, maxRows, maxBytes int64) ([]runtimeValue, *sqliteFailureData) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, sqliteFailureFromError("Query", err)
	}

	seenCols := make(map[string]bool, len(cols))
	for _, col := range cols {
		if seenCols[col] {
			return nil, sqliteFailure("Query", nil, fmt.Sprintf("query returned duplicate column name %q; use SQL aliases", col))
		}
		seenCols[col] = true
	}

	var rowList []runtimeValue
	var cumulativeBytes int64
	var rowCount int64

	for rows.Next() {
		rowCount++
		if rowCount > maxRows {
			return nil, sqliteFailure("Query", nil, fmt.Sprintf("query exceeded maximum row limit of %d", maxRows))
		}

		dest := make([]any, len(cols))
		destPointers := make([]any, len(cols))
		for i := range dest {
			destPointers[i] = &dest[i]
		}

		if err := rows.Scan(destPointers...); err != nil {
			return nil, sqliteFailureFromError("Query", err)
		}

		entries := make([]runtimeMapEntry, len(cols))
		for i, col := range cols {
			slickVal, convertFail := convertSQLValueToSlick(p, dest[i])
			if convertFail != nil {
				return nil, convertFail
			}
			switch slickVal.variant.name {
			case "Text":
				cumulativeBytes += int64(len(slickVal.variant.fields[0].scalar.(string)))
			case "Blob":
				cumulativeBytes += int64(len(slickVal.variant.fields[0].scalar.([]byte)))
			default:
				cumulativeBytes += 8
			}
			if cumulativeBytes > maxBytes {
				return nil, sqliteFailure("Query", nil, fmt.Sprintf("query exceeded maximum byte limit of %d", maxBytes))
			}

			keyVal := runtimeValue{typ: "string", scalar: col}
			mapKey, _ := canonicalRuntimeMapKey(keyVal)
			entries[i] = runtimeMapEntry{
				key:       keyVal,
				value:     slickVal,
				canonical: mapKey,
			}
		}

		rowMap, _ := newRuntimeMap(entries)
		rowVal := runtimeValue{
			typ: stdSQLiteRowName,
			fields: map[string]runtimeValue{
				"Values": {typ: fmt.Sprintf("Map<string,%s>", stdSQLiteValueName), mapping: rowMap},
			},
		}
		rowList = append(rowList, rowVal)
	}

	if err := rows.Err(); err != nil {
		return nil, sqliteFailureFromError("Query", err)
	}

	return rowList, nil
}

// emitSQLiteRuntimeSupport generates the Go runtime types and methods for std.sqlite.
func (g *goGenerator) emitSQLiteRuntimeSupport() {
	valueUnion := goUnionName(stdSQLiteValueName)
	failureClass := goClassName(stdSQLiteFailureName)
	executionClass := goClassName(stdSQLiteExecutionName)
	rowClass := goClassName(stdSQLiteRowName)
	databaseClass := goClassName(stdSQLiteDatabaseName)
	transactionClass := goClassName(stdSQLiteTransactionName)
	statementClass := goClassName(stdSQLiteStatementName)
	queryClass := goClassName(stdSQLiteQueryName)

	openResult := g.goType("Result<" + stdSQLiteDatabaseName + "," + stdSQLiteFailureName + ">")
	execResult := g.goType("Result<" + stdSQLiteExecutionName + "," + stdSQLiteFailureName + ">")
	queryResult := g.goType("Result<" + stdSQLiteRowName + "[]," + stdSQLiteFailureName + ">")
	beginResult := g.goType("Result<" + stdSQLiteTransactionName + "," + stdSQLiteFailureName + ">")
	voidResult := g.goType("Result<null," + stdSQLiteFailureName + ">")

	g.line(`var slickSQLiteMemorySeq atomic.Uint64`)
	g.line(`type slickSQLiteDatabase struct {`)
	g.line(`	mu       sync.Mutex`)
	g.line(`	db       *sql.DB`)
	g.line(`	path     string`)
	g.line(`	closed   bool`)
	g.line(`	activeTx *slickSQLiteTransaction`)
	g.line(`}`)
	g.line(``)
	g.line(`type slickSQLiteTransaction struct {`)
	g.line(`	mu     sync.Mutex`)
	g.line(`	tx     *sql.Tx`)
	g.line(`	db     *slickSQLiteDatabase`)
	g.line(`	state  int`)
	g.line(`	closed bool`)
	g.line(`}`)
	g.line(`func slickSQLiteOpenDSN(path string) string {`)
	g.line(`	if path != ":memory:" { return path }`)
	g.line(`	return fmt.Sprintf("file:slick-memory-%%d?mode=memory&cache=shared", slickSQLiteMemorySeq.Add(1))`)
	g.line(`}`)
	g.line(`func slickSQLiteTxInactive(tx *slickSQLiteTransaction) bool {`)
	g.line(`	return tx == nil || tx.closed || tx.state != 0 || tx.db == nil || tx.db.closed`)
	g.line(`}`)
	g.line(`func slickSQLiteClearActive(db *slickSQLiteDatabase, tx *slickSQLiteTransaction) {`)
	g.line(`	if db != nil && db.activeTx == tx { db.activeTx = nil }`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteFailure(op string, code *int64, msg string) *%s {`, failureClass)
	g.line(`	opt := slickNone[int64]()`)
	g.line(`	if code != nil { opt = slickSome(*code) }`)
	g.line(`	return &%s{%s: op, %s: opt, %s: msg}`, failureClass, goFieldName("Operation"), goFieldName("Code"), goFieldName("Message"))
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteFailureFromError(op string, err error) *%s {`, failureClass)
	g.line(`	if err == nil { return slickSQLiteFailure(op, nil, "unknown error") }`)
	g.line(`	var sqliteErr *sqlite.Error`)
	g.line(`	var code *int64`)
	g.line(`	if errors.As(err, &sqliteErr) {`)
	g.line(`		c := int64(sqliteErr.Code())`)
	g.line(`		code = &c`)
	g.line(`	}`)
	g.line(`	return slickSQLiteFailure(op, code, err.Error())`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteValidateSingleSQL(sqlText string, op string) *%s {`, failureClass)
	g.line(`	trimmed := strings.TrimSpace(sqlText)`)
	g.line(`	if trimmed == "" { return slickSQLiteFailure(op, nil, "SQL statement must not be empty") }`)
	g.line(`	hasContent, inSingle, inDouble, inBacktick, inLine, inBlock, sawSemi := false, false, false, false, false, false, false`)
	g.line(`	chars := []rune(sqlText)`)
	g.line(`	for i := 0; i < len(chars); i++ {`)
	g.line(`		c := chars[i]`)
	g.line(`		if inLine { if c == '\n' { inLine = false }; continue }`)
	g.line(`		if inBlock { if c == '*' && i+1 < len(chars) && chars[i+1] == '/' { inBlock = false; i++ }; continue }`)
	g.line(`		if inSingle { if c == '\'' { if i+1 < len(chars) && chars[i+1] == '\'' { i++ } else { inSingle = false } }; continue }`)
	g.line(`		if inDouble { if c == '"' { if i+1 < len(chars) && chars[i+1] == '"' { i++ } else { inDouble = false } }; continue }`)
	g.line(`		if inBacktick { if c == '` + "`" + `' { inBacktick = false }; continue }`)
	g.line(`		if c == '-' && i+1 < len(chars) && chars[i+1] == '-' { inLine = true; i++; continue }`)
	g.line(`		if c == '/' && i+1 < len(chars) && chars[i+1] == '*' { inBlock = true; i++; continue }`)
	g.line(`		if c == '\'' { inSingle = true; hasContent = true; continue }`)
	g.line(`		if c == '"' { inDouble = true; hasContent = true; continue }`)
	g.line(`		if c == '` + "`" + `' { inBacktick = true; hasContent = true; continue }`)
	g.line(`		if c == ';' { sawSemi = true; continue }`)
	g.line(`		if !strings.ContainsRune(" \t\r\n", c) {`)
	g.line(`			if sawSemi { return slickSQLiteFailure(op, nil, "statement contains multiple SQL statements") }`)
	g.line(`			hasContent = true`)
	g.line(`		}`)
	g.line(`	}`)
	g.line(`	if !hasContent { return slickSQLiteFailure(op, nil, "SQL statement must not be empty") }`)
	g.line(`	return nil`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteConvertValueToSQL(val *%s) (any, error) {`, valueUnion)
	g.line(`	if val == nil { return nil, errors.New("invalid SQLite parameter value") }`)
	g.line(`	switch val.slickTag {`)
	g.line(`	case 1: return nil, nil`)
	g.line(`	case 2: return val.%s, nil`, goVariantFieldName("Integer", "Value"))
	g.line(`	case 3:`)
	g.line(`		f := val.%s`, goVariantFieldName("Float", "Value"))
	g.line(`		if math.IsNaN(f) || math.IsInf(f, 0) { return nil, errors.New("cannot bind non-finite floating-point value") }`)
	g.line(`		return f, nil`)
	g.line(`	case 4:`)
	g.line(`		t := val.%s`, goVariantFieldName("Text", "Value"))
	g.line(`		if !utf8.ValidString(t) { return nil, errors.New("text parameter contains invalid UTF-8") }`)
	g.line(`		return t, nil`)
	g.line(`	case 5:`)
	g.line(`		b := val.%s`, goVariantFieldName("Blob", "Value"))
	g.line(`		return append([]byte(nil), b...), nil`)
	g.line(`	default: return nil, fmt.Errorf("unknown SQLite Value tag %%d", val.slickTag)`)
	g.line(`	}`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteConvertSQLToValue(val any) (*%s, *%s) {`, valueUnion, failureClass)
	g.line(`	if val == nil { return &%s{slickTag: 1}, nil }`, valueUnion)
	g.line(`	switch v := val.(type) {`)
	g.line(`	case int64: return &%s{slickTag: 2, %s: v}, nil`, valueUnion, goVariantFieldName("Integer", "Value"))
	g.line(`	case float64:`)
	g.line(`		if math.IsNaN(v) || math.IsInf(v, 0) { return nil, slickSQLiteFailure("Query", nil, "query returned a non-finite floating-point value") }`)
	g.line(`		return &%s{slickTag: 3, %s: v}, nil`, valueUnion, goVariantFieldName("Float", "Value"))
	g.line(`	case string:`)
	g.line(`		if !utf8.ValidString(v) { return nil, slickSQLiteFailure("Query", nil, "query returned invalid UTF-8 text") }`)
	g.line(`		return &%s{slickTag: 4, %s: v}, nil`, valueUnion, goVariantFieldName("Text", "Value"))
	g.line(`	case []byte: return &%s{slickTag: 5, %s: append(slickBytes(nil), v...)}, nil`, valueUnion, goVariantFieldName("Blob", "Value"))
	g.line(`	case bool:`)
	g.line(`		var intVal int64`)
	g.line(`		if v { intVal = 1 }`)
	g.line(`		return &%s{slickTag: 2, %s: intVal}, nil`, valueUnion, goVariantFieldName("Integer", "Value"))
	g.line(`	default: return nil, slickSQLiteFailure("Query", nil, "query returned an unsupported SQLite value")`)
	g.line(`	}`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteScanRows(rows *sql.Rows, maxRows, maxBytes int64) ([]%s, *%s) {`, rowClass, failureClass)
	g.line(`	cols, err := rows.Columns()`)
	g.line(`	if err != nil { return nil, slickSQLiteFailureFromError("Query", err) }`)
	g.line(`	seenCols := make(map[string]bool, len(cols))`)
	g.line(`	for _, col := range cols {`)
	g.line(`		if seenCols[col] { return nil, slickSQLiteFailure("Query", nil, fmt.Sprintf("query returned duplicate column name %%q; use SQL aliases", col)) }`)
	g.line(`		seenCols[col] = true`)
	g.line(`	}`)
	g.line(`	var rowList []%s`, rowClass)
	g.line(`	var cumulativeBytes int64`)
	g.line(`	var rowCount int64`)
	g.line(`	for rows.Next() {`)
	g.line(`		rowCount++`)
	g.line(`		if rowCount > maxRows { return nil, slickSQLiteFailure("Query", nil, fmt.Sprintf("query exceeded maximum row limit of %%d", maxRows)) }`)
	g.line(`		dest := make([]any, len(cols))`)
	g.line(`		destPointers := make([]any, len(cols))`)
	g.line(`		for i := range dest { destPointers[i] = &dest[i] }`)
	g.line(`		if err := rows.Scan(destPointers...); err != nil { return nil, slickSQLiteFailureFromError("Query", err) }`)
	g.line(`		entries := make([]slickMapEntry[string, *%s], len(cols))`, valueUnion)
	g.line(`		for i, col := range cols {`)
	g.line(`			slickVal, convertFail := slickSQLiteConvertSQLToValue(dest[i])`)
	g.line(`			if convertFail != nil { return nil, convertFail }`)
	g.line(`			switch slickVal.slickTag {`)
	g.line(`			case 4: cumulativeBytes += int64(len(slickVal.%s))`, goVariantFieldName("Text", "Value"))
	g.line(`			case 5: cumulativeBytes += int64(len(slickVal.%s))`, goVariantFieldName("Blob", "Value"))
	g.line(`			default: cumulativeBytes += 8`)
	g.line(`			}`)
	g.line(`			if cumulativeBytes > maxBytes { return nil, slickSQLiteFailure("Query", nil, fmt.Sprintf("query exceeded maximum byte limit of %%d", maxBytes)) }`)
	g.line(`			entries[i] = slickMapEntry[string, *%s]{key: col, value: slickVal}`, valueUnion)
	g.line(`		}`)
	g.line(`		rowList = append(rowList, %s{%s: slickMapOf(entries...)})`, rowClass, goFieldName("Values"))
	g.line(`	}`)
	g.line(`	if err := rows.Err(); err != nil { return nil, slickSQLiteFailureFromError("Query", err) }`)
	g.line(`	return rowList, nil`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteOpen(path string) (%s, error) {`, openResult)
	g.line(`	if path != ":memory:" {`)
	g.line(`		cleanPath := filepath.Clean(path)`)
	g.line(`		dir := filepath.Dir(cleanPath)`)
	g.line(`		if dir != "." && dir != "" && dir != "/" {`)
	g.line(`			stat, err := os.Stat(dir)`)
	g.line(`			if err != nil || !stat.IsDir() {`)
	g.line(`				return %s{failure: slickSQLiteFailure("Open", nil, fmt.Sprintf("parent directory does not exist: %%s", dir))}, nil`, openResult)
	g.line(`			}`)
	g.line(`		}`)
	g.line(`	}`)
	g.line(`	db, err := sql.Open("sqlite", slickSQLiteOpenDSN(path))`)
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Open", err)}, nil }`, openResult)
	g.line(`	if err := db.Ping(); err != nil { _ = db.Close(); return %s{failure: slickSQLiteFailureFromError("Open", err)}, nil }`, openResult)
	g.line(`	return %s{ok: true, value: %s{slickResource: &slickSQLiteDatabase{db: db, path: path}}}, nil`, openResult, databaseClass)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteDBExecute(res *slickSQLiteDatabase, stmt %s) %s {`, statementClass, execResult)
	g.line(`	if res == nil || res.db == nil { return %s{failure: slickSQLiteFailure("Execute", nil, "database is closed")} }`, execResult)
	g.line(`	if f := slickSQLiteValidateSingleSQL(stmt.%s, "Execute"); f != nil { return %s{failure: f} }`, goFieldName("SQL"), execResult)
	g.line(`	params := make([]any, len(stmt.%s))`, goFieldName("Parameters"))
	g.line(`	for i, el := range stmt.%s {`, goFieldName("Parameters"))
	g.line(`		sqlVal, err := slickSQLiteConvertValueToSQL(el)`)
	g.line(`		if err != nil { return %s{failure: slickSQLiteFailure("Execute", nil, err.Error())} }`, execResult)
	g.line(`		params[i] = sqlVal`)
	g.line(`	}`)
	g.line(`	res.mu.Lock(); defer res.mu.Unlock()`)
	g.line(`	if res.closed { return %s{failure: slickSQLiteFailure("Execute", nil, "database is closed")} }`, execResult)
	g.line(`	if res.activeTx != nil && !res.activeTx.closed && res.activeTx.state == 0 { return %s{failure: slickSQLiteFailure("Execute", nil, "a transaction is already active")} }`, execResult)
	g.line(`	execRes, err := res.db.Exec(stmt.%s, params...)`, goFieldName("SQL"))
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Execute", err)} }`, execResult)
	g.line(`	rowsAffected, _ := execRes.RowsAffected()`)
	g.line(`	lastInsertId, lastErr := execRes.LastInsertId()`)
	g.line(`	optId := slickNone[int64]()`)
	g.line(`	if lastErr == nil { optId = slickSome(lastInsertId) }`)
	g.line(`	return %s{ok: true, value: %s{%s: rowsAffected, %s: optId}}`, execResult, executionClass, goFieldName("RowsAffected"), goFieldName("LastInsertId"))
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteDBQuery(res *slickSQLiteDatabase, q %s) %s {`, queryClass, queryResult)
	g.line(`	if res == nil || res.db == nil { return %s{failure: slickSQLiteFailure("Query", nil, "database is closed")} }`, queryResult)
	g.line(`	if q.%s <= 0 || q.%s <= 0 { return %s{failure: slickSQLiteFailure("Query", nil, "MaxRows and MaxBytes must be greater than zero")} }`, goFieldName("MaxRows"), goFieldName("MaxBytes"), queryResult)
	g.line(`	if f := slickSQLiteValidateSingleSQL(q.%s, "Query"); f != nil { return %s{failure: f} }`, goFieldName("SQL"), queryResult)
	g.line(`	params := make([]any, len(q.%s))`, goFieldName("Parameters"))
	g.line(`	for i, el := range q.%s {`, goFieldName("Parameters"))
	g.line(`		sqlVal, err := slickSQLiteConvertValueToSQL(el)`)
	g.line(`		if err != nil { return %s{failure: slickSQLiteFailure("Query", nil, err.Error())} }`, queryResult)
	g.line(`		params[i] = sqlVal`)
	g.line(`	}`)
	g.line(`	res.mu.Lock(); defer res.mu.Unlock()`)
	g.line(`	if res.closed { return %s{failure: slickSQLiteFailure("Query", nil, "database is closed")} }`, queryResult)
	g.line(`	if res.activeTx != nil && !res.activeTx.closed && res.activeTx.state == 0 { return %s{failure: slickSQLiteFailure("Query", nil, "a transaction is already active")} }`, queryResult)
	g.line(`	rows, err := res.db.Query(q.%s, params...)`, goFieldName("SQL"))
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Query", err)} }`, queryResult)
	g.line(`	defer rows.Close()`)
	g.line(`	rowList, fail := slickSQLiteScanRows(rows, q.%s, q.%s)`, goFieldName("MaxRows"), goFieldName("MaxBytes"))
	g.line(`	if fail != nil { return %s{failure: fail} }`, queryResult)
	g.line(`	return %s{ok: true, value: rowList}`, queryResult)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteDBBegin(res *slickSQLiteDatabase) %s {`, beginResult)
	g.line(`	if res == nil || res.db == nil { return %s{failure: slickSQLiteFailure("Begin", nil, "database is closed")} }`, beginResult)
	g.line(`	res.mu.Lock(); defer res.mu.Unlock()`)
	g.line(`	if res.closed { return %s{failure: slickSQLiteFailure("Begin", nil, "database is closed")} }`, beginResult)
	g.line(`	if res.activeTx != nil && !res.activeTx.closed && res.activeTx.state == 0 { return %s{failure: slickSQLiteFailure("Begin", nil, "a transaction is already active")} }`, beginResult)
	g.line(`	tx, err := res.db.Begin()`)
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Begin", err)} }`, beginResult)
	g.line(`	nativeTx := &slickSQLiteTransaction{tx: tx, db: res, state: 0}`)
	g.line(`	res.activeTx = nativeTx`)
	g.line(`	return %s{ok: true, value: %s{slickResource: nativeTx}}`, beginResult, transactionClass)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteDBClose(res *slickSQLiteDatabase) error {`)
	g.line(`	if res == nil || res.db == nil { return nil }`)
	g.line(`	res.mu.Lock(); defer res.mu.Unlock()`)
	g.line(`	if res.closed { return nil }`)
	g.line(`	res.closed = true`)
	g.line(`	if active := res.activeTx; active != nil && !active.closed && active.state == 0 { active.closed = true; active.state = 2; _ = active.tx.Rollback() }`)
	g.line(`	res.activeTx = nil`)
	g.line(`	if err := res.db.Close(); err != nil { return slickSQLiteFailureFromError("Close", err) }`)
	g.line(`	return nil`)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteTxExecute(res *slickSQLiteTransaction, stmt %s) %s {`, statementClass, execResult)
	g.line(`	if res == nil { return %s{failure: slickSQLiteFailure("Execute", nil, "transaction is no longer active")} }`, execResult)
	g.line(`	if f := slickSQLiteValidateSingleSQL(stmt.%s, "Execute"); f != nil { return %s{failure: f} }`, goFieldName("SQL"), execResult)
	g.line(`	params := make([]any, len(stmt.%s))`, goFieldName("Parameters"))
	g.line(`	for i, el := range stmt.%s {`, goFieldName("Parameters"))
	g.line(`		sqlVal, err := slickSQLiteConvertValueToSQL(el)`)
	g.line(`		if err != nil { return %s{failure: slickSQLiteFailure("Execute", nil, err.Error())} }`, execResult)
	g.line(`		params[i] = sqlVal`)
	g.line(`	}`)
	g.line(`	if res.db != nil { res.db.mu.Lock(); defer res.db.mu.Unlock() }`)
	g.line(`	if slickSQLiteTxInactive(res) { return %s{failure: slickSQLiteFailure("Execute", nil, "transaction is no longer active")} }`, execResult)
	g.line(`	execRes, err := res.tx.Exec(stmt.%s, params...)`, goFieldName("SQL"))
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Execute", err)} }`, execResult)
	g.line(`	rowsAffected, _ := execRes.RowsAffected()`)
	g.line(`	lastInsertId, lastErr := execRes.LastInsertId()`)
	g.line(`	optId := slickNone[int64]()`)
	g.line(`	if lastErr == nil { optId = slickSome(lastInsertId) }`)
	g.line(`	return %s{ok: true, value: %s{%s: rowsAffected, %s: optId}}`, execResult, executionClass, goFieldName("RowsAffected"), goFieldName("LastInsertId"))
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteTxQuery(res *slickSQLiteTransaction, q %s) %s {`, queryClass, queryResult)
	g.line(`	if res == nil { return %s{failure: slickSQLiteFailure("Query", nil, "transaction is no longer active")} }`, queryResult)
	g.line(`	if q.%s <= 0 || q.%s <= 0 { return %s{failure: slickSQLiteFailure("Query", nil, "MaxRows and MaxBytes must be greater than zero")} }`, goFieldName("MaxRows"), goFieldName("MaxBytes"), queryResult)
	g.line(`	if f := slickSQLiteValidateSingleSQL(q.%s, "Query"); f != nil { return %s{failure: f} }`, goFieldName("SQL"), queryResult)
	g.line(`	params := make([]any, len(q.%s))`, goFieldName("Parameters"))
	g.line(`	for i, el := range q.%s {`, goFieldName("Parameters"))
	g.line(`		sqlVal, err := slickSQLiteConvertValueToSQL(el)`)
	g.line(`		if err != nil { return %s{failure: slickSQLiteFailure("Query", nil, err.Error())} }`, queryResult)
	g.line(`		params[i] = sqlVal`)
	g.line(`	}`)
	g.line(`	if res.db != nil { res.db.mu.Lock(); defer res.db.mu.Unlock() }`)
	g.line(`	if slickSQLiteTxInactive(res) { return %s{failure: slickSQLiteFailure("Query", nil, "transaction is no longer active")} }`, queryResult)
	g.line(`	rows, err := res.tx.Query(q.%s, params...)`, goFieldName("SQL"))
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Query", err)} }`, queryResult)
	g.line(`	defer rows.Close()`)
	g.line(`	rowList, fail := slickSQLiteScanRows(rows, q.%s, q.%s)`, goFieldName("MaxRows"), goFieldName("MaxBytes"))
	g.line(`	if fail != nil { return %s{failure: fail} }`, queryResult)
	g.line(`	return %s{ok: true, value: rowList}`, queryResult)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteTxCommit(res *slickSQLiteTransaction) %s {`, voidResult)
	g.line(`	if res == nil { return %s{failure: slickSQLiteFailure("Commit", nil, "transaction is no longer active")} }`, voidResult)
	g.line(`	if res.db != nil { res.db.mu.Lock(); defer res.db.mu.Unlock() }`)
	g.line(`	if slickSQLiteTxInactive(res) { return %s{failure: slickSQLiteFailure("Commit", nil, "transaction is no longer active")} }`, voidResult)
	g.line(`	res.state = 1`)
	g.line(`	err := res.tx.Commit()`)
	g.line(`	slickSQLiteClearActive(res.db, res)`)
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Commit", err)} }`, voidResult)
	g.line(`	return %s{ok: true, value: struct{}{}}`, voidResult)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteTxRollback(res *slickSQLiteTransaction) %s {`, voidResult)
	g.line(`	if res == nil { return %s{failure: slickSQLiteFailure("Rollback", nil, "transaction is no longer active")} }`, voidResult)
	g.line(`	if res.db != nil { res.db.mu.Lock(); defer res.db.mu.Unlock() }`)
	g.line(`	if slickSQLiteTxInactive(res) { return %s{failure: slickSQLiteFailure("Rollback", nil, "transaction is no longer active")} }`, voidResult)
	g.line(`	res.state = 2`)
	g.line(`	err := res.tx.Rollback()`)
	g.line(`	slickSQLiteClearActive(res.db, res)`)
	g.line(`	if err != nil { return %s{failure: slickSQLiteFailureFromError("Rollback", err)} }`, voidResult)
	g.line(`	return %s{ok: true, value: struct{}{}}`, voidResult)
	g.line(`}`)
	g.line(``)
	g.line(`func slickSQLiteTxClose(res *slickSQLiteTransaction) error {`)
	g.line(`	if res == nil || res.tx == nil { return nil }`)
	g.line(`	if res.db != nil { res.db.mu.Lock(); defer res.db.mu.Unlock() }`)
	g.line(`	if res.closed { return nil }`)
	g.line(`	res.closed = true`)
	g.line(`	if res.state == 0 {`)
	g.line(`		res.state = 2`)
	g.line(`		err := res.tx.Rollback()`)
	g.line(`		slickSQLiteClearActive(res.db, res)`)
	g.line(`		if err != nil { return slickSQLiteFailureFromError("Close", err) }`)
	g.line(`	}`)
	g.line(`	return nil`)
	g.line(`}`)
	g.line(``)
}
func (g *goGenerator) skipStdSQLite(name string) bool {
	if g.program.usesStdSQLite {
		return false
	}
	return strings.HasPrefix(name, "std.sqlite.")
}
