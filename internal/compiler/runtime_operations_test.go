package compiler

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeOperationTablesAreExhaustive(t *testing.T) {
	if err := validateStabilityRegistries(); err != nil {
		t.Fatal(err)
	}
	kinds := []runtimeImplementationKind{
		runtimeImplementationInterpreter,
		runtimeImplementationGo,
		runtimeImplementationLLVM,
	}
	for operation, declaration := range runtimeOperationRegistry {
		for _, kind := range kinds {
			implementation, ok := declaration.implementations[kind]
			if !ok || implementation.entry == "" {
				t.Fatalf("runtime operation %s has no %s implementation", operation, kind)
			}
			if tableEntry, ok := runtimeOperationsFor(kind)[operation]; !ok || tableEntry != implementation {
				t.Fatalf("runtime operation %s %s table entry = %#v, want %#v", operation, kind, tableEntry, implementation)
			}
		}
	}
}

func TestRuntimeInputsComeFromReachedCoreOperations(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `function main() -> bytes {
    let Convert = std.bytes.FromUtf8
    Convert("ok")
}`,
	}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	want := []runtimeOperationID{nativeStdBytesFromUtf8}
	if !reflect.DeepEqual(inputs.operations, want) {
		t.Fatalf("runtime operations = %v, want %v", inputs.operations, want)
	}
	if inputs.abiVersion != runtimeABIVersion || !inputs.families[runtimeFamilyBytes] {
		t.Fatalf("runtime inputs = %#v", inputs)
	}
	for _, family := range []runtimeFamily{runtimeFamilyFilesystem, runtimeFamilyHTTP, runtimeFamilyProcess, runtimeFamilySQLite} {
		if inputs.families[family] {
			t.Fatalf("unused runtime family %s was selected", family)
		}
	}
}

func TestRuntimeFamiliesIncludeTypeOnlyNativeResources(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `function Ignore(Database: std.sqlite.Database) -> int { 0 }
function main() -> int {
    0
}`,
	}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(core.RuntimeFamilies, []string{string(runtimeFamilySQLite)}) ||
		!inputs.usesSQLite || len(inputs.operations) != 0 {
		t.Fatalf("type-only SQLite runtime inputs = %#v, Core families = %v", inputs, core.RuntimeFamilies)
	}
	emission, err := emitGoSource(program, inputs, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(emission.primary), "go.mod")); err != nil {
		t.Fatalf("type-only SQLite runtime did not emit its pinned module: %v", err)
	}
}

func TestRuntimeInputsIncludeImplicitNativeCleanup(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `function Consume(Database: std.sqlite.Database) -> int throws std.sqlite.Failure effects { database } {
    using Current = Database {
        0
    }
}
function main() -> int { 0 }`}
	program, diagnostics := compile([]Source{source})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	want := []runtimeOperationID{nativeStdSQLiteDatabaseClose}
	if !reflect.DeepEqual(inputs.operations, want) {
		t.Fatalf("cleanup runtime operations = %v, want %v", inputs.operations, want)
	}

	original := backendRegistry
	backendRegistry = append(append([]backendRegistration(nil), original...), backendRegistration{
		name:       "cleanup-gap",
		stability:  StabilityAlpha,
		targets:    []backendTargetRegistration{{name: "test-x64", stability: StabilityAlpha}},
		runtimeABI: runtimeABIVersion,
		operations: runtimeOperationTable{nativeStdSQLiteOpen: {entry: "open"}},
		driver:     "missing",
	})
	defer func() { backendRegistry = original }()

	output := filepath.Join(t.TempDir(), "program")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = BuildSourcesWithOptions([]Source{source}, output, BuildOptions{Backend: "cleanup-gap", Target: "test-x64", AllowAlpha: true})
	if err == nil || !strings.Contains(err.Error(), string(nativeStdSQLiteDatabaseClose)) {
		t.Fatalf("missing cleanup operation error = %v", err)
	}
	contents, readErr := os.ReadFile(output)
	if readErr != nil || string(contents) != "sentinel" {
		t.Fatalf("pre-emission cleanup gap changed output: %q, %v", contents, readErr)
	}
}

func TestRuntimeInputsIncludeConstructedNativeResourceMethods(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `function main() -> int {
    let Database = std.sqlite.Database {}
    0
}`,
	}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[runtimeOperationID]bool)
	for _, operation := range inputs.operations {
		found[operation] = true
	}
	for _, operation := range []runtimeOperationID{
		nativeStdSQLiteDatabaseExecute,
		nativeStdSQLiteDatabaseQuery,
		nativeStdSQLiteDatabaseBegin,
		nativeStdSQLiteDatabaseClose,
		nativeStdSQLiteTransactionExecute,
		nativeStdSQLiteTransactionQuery,
		nativeStdSQLiteTransactionCommit,
		nativeStdSQLiteTransactionRollback,
		nativeStdSQLiteTransactionClose,
	} {
		if !found[operation] {
			t.Fatalf("constructed resource operation %s missing from %v", operation, inputs.operations)
		}
	}
}

func TestRuntimeInputsExpandOperationDependencies(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text: `function main() -> Result<bytes,std.io.Failure> effects { io } {
    std.io.ReadAll(std.io.ReaderFromBytes(std.bytes.FromUtf8("ok")), 10)
}`,
	}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[runtimeOperationID]bool)
	for _, operation := range inputs.operations {
		found[operation] = true
	}
	for _, operation := range []runtimeOperationID{nativeStdIOReadAll, nativeStdIOReaderRead} {
		if !found[operation] {
			t.Fatalf("runtime operation dependency %s missing from %v", operation, inputs.operations)
		}
	}
}

func TestRuntimeDependenciesCoverNativeResourcesUpcastToInterfaces(t *testing.T) {
	source := Source{Name: "main.slk", Namespace: "root", Text: `function AsReader(Reader: std.io.Reader) -> std.io.Reader { Reader }
function main() -> int throws std.io.Failure effects { io } {
    using Reader = AsReader(std.io.ReaderFromBytes(std.bytes.FromUtf8("ok"))) {
        match Reader.Read(1) {
            Ok(_) => 1
            Err(_) => 0
        }
    }
}`}
	program, diagnostics := compile([]Source{source})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[runtimeOperationID]bool)
	for _, operation := range inputs.operations {
		found[operation] = true
	}
	for _, operation := range []runtimeOperationID{nativeStdIOReaderFromBytes, nativeStdIOReaderRead, nativeStdIOReaderClose} {
		if !found[operation] {
			t.Fatalf("upcast resource operation %s missing from %v", operation, inputs.operations)
		}
	}

	original := backendRegistry
	backendRegistry = append(append([]backendRegistration(nil), original...), backendRegistration{
		name:       "upcast-gap",
		stability:  StabilityAlpha,
		targets:    []backendTargetRegistration{{name: "test-x64", stability: StabilityAlpha}},
		runtimeABI: runtimeABIVersion,
		operations: runtimeOperationTable{
			nativeStdBytesFromUtf8:     {entry: "bytes"},
			nativeStdIOReaderFromBytes: {entry: "reader"},
		},
		driver: "missing",
	})
	defer func() { backendRegistry = original }()

	output := filepath.Join(t.TempDir(), "program")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = BuildSourcesWithOptions([]Source{source}, output, BuildOptions{Backend: "upcast-gap", Target: "test-x64", AllowAlpha: true})
	if err == nil || !strings.Contains(err.Error(), "std.io.bytesReader.") {
		t.Fatalf("missing upcast resource operation error = %v", err)
	}
	contents, readErr := os.ReadFile(output)
	if readErr != nil || string(contents) != "sentinel" {
		t.Fatalf("pre-emission upcast gap changed output: %q, %v", contents, readErr)
	}
}

func TestRuntimeInputsRejectUnknownCoreOperation(t *testing.T) {
	core := coreProgram{Functions: []coreFunction{{Body: coreBlock{Statements: []coreStatement{{
		Kind:  "expression",
		Value: &coreExpression{Kind: "call", Operation: "std.unknown.Operation"},
	}}}}}}
	if _, err := runtimeInputsForCore(core); err == nil || !strings.Contains(err.Error(), "unknown runtime operation") {
		t.Fatalf("unknown Core operation error = %v", err)
	}
}

func TestStableBackendRuntimeABIMustMatchCompiler(t *testing.T) {
	original := backendRegistry
	backendRegistry = append([]backendRegistration(nil), original...)
	backendRegistry[0].runtimeABI = runtimeABIVersion + 1
	defer func() { backendRegistry = original }()

	if err := validateStabilityRegistries(); err == nil || !strings.Contains(err.Error(), "runtime ABI") {
		t.Fatalf("stable backend ABI validation error = %v", err)
	}
}

func TestRuntimeABIMismatchFailsBeforeEmission(t *testing.T) {
	original := backendRegistry
	backendRegistry = append(append([]backendRegistration(nil), original...), backendRegistration{
		name:       "abi-test",
		stability:  StabilityAlpha,
		targets:    []backendTargetRegistration{{name: "test-x64", stability: StabilityAlpha}},
		runtimeABI: runtimeABIVersion + 1,
		operations: goRuntimeOperations,
		driver:     "missing",
	})
	defer func() { backendRegistry = original }()

	root := t.TempDir()
	output := filepath.Join(root, "program")
	if err := os.WriteFile(output, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := Source{Name: "main.slk", Namespace: "root", Text: `function main() -> bytes { std.bytes.FromUtf8("ok") }`}
	_, err := BuildSourcesWithOptions([]Source{source}, output, BuildOptions{Backend: "abi-test", Target: "test-x64", AllowAlpha: true})
	if err == nil || !strings.Contains(err.Error(), "runtime ABI") {
		t.Fatalf("ABI mismatch error = %v", err)
	}
	contents, readErr := os.ReadFile(output)
	if readErr != nil || string(contents) != "sentinel" {
		t.Fatalf("pre-emission ABI failure changed output: %q, %v", contents, readErr)
	}
}
