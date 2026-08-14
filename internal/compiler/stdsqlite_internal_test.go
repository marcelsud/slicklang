package compiler

import (
	"strings"
	"testing"
)

func TestStdSQLiteSyntheticDeclarations(t *testing.T) {
	program := newProgram()
	registerStandardLibrary(program)

	// Namespace
	if doc := program.namespaceDocumentation["std.sqlite"]; doc == nil || *doc == "" {
		t.Fatal("std.sqlite namespace documentation missing")
	}

	// Union Value
	valUnion := program.unions[stdSQLiteValueName]
	if valUnion == nil {
		t.Fatal("std.sqlite.Value union not registered")
	}
	if len(valUnion.order) != 5 {
		t.Fatalf("std.sqlite.Value must have 5 variants, found %d", len(valUnion.order))
	}
	for _, name := range []string{"Null", "Integer", "Float", "Text", "Blob"} {
		variant, exists := valUnion.variants[name]
		if !exists {
			t.Fatalf("variant %s missing in std.sqlite.Value", name)
		}
		if variant.documentation == nil || *variant.documentation == "" {
			t.Fatalf("variant %s documentation missing", name)
		}
	}

	// Open function
	openFn := program.functions[string(nativeStdSQLiteOpen)]
	assertStandardFunction(t, openFn, "std.sqlite", "Open", []string{"string"}, "Result<std.sqlite.Database,std.sqlite.Failure>", nativeStdSQLiteOpen)

	// Classes
	for _, name := range []string{
		stdSQLiteStatementName,
		stdSQLiteQueryName,
		stdSQLiteRowName,
		stdSQLiteExecutionName,
		stdSQLiteFailureName,
		stdSQLiteDatabaseName,
		stdSQLiteTransactionName,
	} {
		class := program.classes[name]
		if class == nil {
			t.Fatalf("class %s not registered", name)
		}
		if class.documentation == nil || *class.documentation == "" {
			t.Fatalf("class %s documentation missing", name)
		}
	}

	failure := program.classes[stdSQLiteFailureName]
	if !failure.isError {
		t.Fatal("std.sqlite.Failure must implement Error (isError=true)")
	}

	dbClass := program.classes[stdSQLiteDatabaseName]
	if dbClass.nativeResource != "*slickSQLiteDatabase" {
		t.Fatalf("Database nativeResource = %q, want *slickSQLiteDatabase", dbClass.nativeResource)
	}
	for _, method := range []string{"Execute", "Query", "Begin", "Close"} {
		if dbClass.methods[method] == nil {
			t.Fatalf("Database missing method %s", method)
		}
	}

	txClass := program.classes[stdSQLiteTransactionName]
	if txClass.nativeResource != "*slickSQLiteTransaction" {
		t.Fatalf("Transaction nativeResource = %q, want *slickSQLiteTransaction", txClass.nativeResource)
	}
	for _, method := range []string{"Execute", "Query", "Commit", "Rollback", "Close"} {
		if txClass.methods[method] == nil {
			t.Fatalf("Transaction missing method %s", method)
		}
	}
}
func TestStdSQLiteUnusedProgramOmitsDriver(t *testing.T) {
	program, diags := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      `function main() -> string { "hello without sqlite" }`,
	}})
	requireNoDiagnostics(t, diags)
	generated, err := program.generateGo()
	if err != nil {
		t.Fatalf("generate Go: %v", err)
	}
	if strings.Contains(generated, "modernc.org/sqlite") {
		t.Fatal("generated Go contains modernc.org/sqlite import for unused program")
	}
	if strings.Contains(generated, "slickSQLiteDatabase") {
		t.Fatal("generated Go contains slickSQLiteDatabase for unused program")
	}
	if strings.Contains(generated, "slickUnion_std_002e_sqlite_002e_Value") {
		t.Fatal("generated Go contains std.sqlite.Value union for unused program")
	}
}
