package compiler

import (
	"fmt"
	"sort"
	"strings"
)

// runtimeOperationID is a stable compiler-owned ABI identifier. Slick source
// resolves public standard-library names to these IDs; backends never select a
// provider by source name.
type runtimeOperationID string

type runtimeFamily string

const (
	runtimeABIVersion                      = 1
	runtimeFamilyBuffer      runtimeFamily = "buffer"
	runtimeFamilyBytes                     = "bytes"
	runtimeFamilyConvert                   = "convert"
	runtimeFamilyEnvironment               = "environment"
	runtimeFamilyFilesystem                = "filesystem"
	runtimeFamilyHTTP                      = "http"
	runtimeFamilyHTTPServer                = "http-server"
	runtimeFamilyIO                        = "io"
	runtimeFamilyJSON                      = "json"
	runtimeFamilyMath                      = "math"
	runtimeFamilyPath                      = "path"
	runtimeFamilyProcess                   = "process"
	runtimeFamilySQLite                    = "sqlite"
	runtimeFamilyText                      = "text"
	runtimeFamilyUnicode                   = "unicode"
	runtimeFamilyUTF8                      = "utf8"
)

func (family runtimeFamily) valid() bool {
	switch family {
	case runtimeFamilyBuffer, runtimeFamilyBytes, runtimeFamilyConvert,
		runtimeFamilyEnvironment, runtimeFamilyFilesystem, runtimeFamilyHTTP,
		runtimeFamilyHTTPServer, runtimeFamilyIO, runtimeFamilyJSON,
		runtimeFamilyMath, runtimeFamilyPath, runtimeFamilyProcess,
		runtimeFamilySQLite, runtimeFamilyText, runtimeFamilyUnicode, runtimeFamilyUTF8:
		return true
	default:
		return false
	}
}

type runtimeImplementationKind string

const (
	runtimeImplementationInterpreter runtimeImplementationKind = "interpreter"
	runtimeImplementationGo          runtimeImplementationKind = "go"
	runtimeImplementationLLVM        runtimeImplementationKind = "llvm"
	runtimeImplementationRust        runtimeImplementationKind = "rust"
	runtimeImplementationBun         runtimeImplementationKind = "bun"
)

type runtimeOperationImplementation struct {
	entry string
}

type runtimeOperationDeclaration struct {
	family          runtimeFamily
	dependencies    []runtimeOperationID
	implementations map[runtimeImplementationKind]runtimeOperationImplementation
}

type runtimeOperationTable map[runtimeOperationID]runtimeOperationImplementation

func declareRuntimeOperation(family runtimeFamily, llvmEntry string, dependencies ...runtimeOperationID) runtimeOperationDeclaration {
	return runtimeOperationDeclaration{
		family:       family,
		dependencies: dependencies,
		implementations: map[runtimeImplementationKind]runtimeOperationImplementation{
			runtimeImplementationInterpreter: {entry: "dispatch:" + string(family)},
			runtimeImplementationGo:          {entry: "emit:" + string(family)},
			runtimeImplementationLLVM:        {entry: llvmEntry},
			runtimeImplementationRust:        {entry: "rust:" + string(family)},
			runtimeImplementationBun:         {entry: "bun:" + string(family)},
		},
	}
}

var runtimeOperationRegistry = map[runtimeOperationID]runtimeOperationDeclaration{
	nativeStdBufferFreeze:               declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBufferGet:                  declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBufferLength:               declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBufferNew:                  declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBufferPush:                 declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBufferSet:                  declareRuntimeOperation(runtimeFamilyBuffer, "core"),
	nativeStdBytesAt:                    declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_at"),
	nativeStdBytesConcat:                declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_concat"),
	nativeStdBytesFromUtf8:              declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_from_utf8"),
	nativeStdBytesFromValues:            declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_from_values"),
	nativeStdBytesLength:                declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_length"),
	nativeStdBytesSlice:                 declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_slice"),
	nativeStdBytesToUtf8:                declareRuntimeOperation(runtimeFamilyBytes, "slick_nat_bytes_to_utf8"),
	nativeStdConvertFloatToString:       declareRuntimeOperation(runtimeFamilyConvert, "slick_nat_float_to_string"),
	nativeStdConvertIntToString:         declareRuntimeOperation(runtimeFamilyConvert, "slick_nat_int_to_string"),
	nativeStdConvertParseFloat:          declareRuntimeOperation(runtimeFamilyConvert, "slick_nat_parse_float"),
	nativeStdConvertParseInt:            declareRuntimeOperation(runtimeFamilyConvert, "slick_nat_parse_int"),
	nativeStdEnvGet:                     declareRuntimeOperation(runtimeFamilyEnvironment, "slick_nat_env_get"),
	nativeStdEnvSet:                     declareRuntimeOperation(runtimeFamilyEnvironment, "slick_nat_env_set"),
	nativeStdEnvUnset:                   declareRuntimeOperation(runtimeFamilyEnvironment, "slick_nat_env_unset"),
	nativeStdFSCreateDirectoryAll:       declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_mkdir"),
	nativeStdFSCreateTemporaryDirectory: declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_tmp", nativeStdFSTemporaryDirectoryClose),
	nativeStdFSExists:                   declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_exists"),
	nativeStdFSReadDirectory:            declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_read_dir"),
	nativeStdFSReadText:                 declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_read_text"),
	nativeStdFSRemove:                   declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_remove"),
	nativeStdFSTemporaryDirectoryClose:  declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_tmp_close"),
	nativeStdFSWriteText:                declareRuntimeOperation(runtimeFamilyFilesystem, "slick_nat_fs_write_text"),
	nativeStdHTTPFetch:                  declareRuntimeOperation(runtimeFamilyHTTP, "slick_nat_http_fetch"),
	nativeStdHTTPHeaderValues:           declareRuntimeOperation(runtimeFamilyHTTP, "slick_nat_http_header_values"),
	nativeStdHTTPStatusText:             declareRuntimeOperation(runtimeFamilyHTTP, "slick_nat_http_status_text"),
	nativeStdHTTPServerServe:            declareRuntimeOperation(runtimeFamilyHTTPServer, "slick_nat_http_serve"),
	nativeStdIOWriterBytes:              declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_bytes"),
	nativeStdIOWriterClose:              declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_write_close"),
	nativeStdIOWriterWrite:              declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_write"),
	nativeStdIOCopy:                     declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_copy", nativeStdIOReaderRead, nativeStdIOWriterWrite),
	nativeStdIOReadAll:                  declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_read_all", nativeStdIOReaderRead),
	nativeStdIOReaderFromBytes:          declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_reader", nativeStdIOReaderRead, nativeStdIOReaderClose),
	nativeStdIOWriterToBytes:            declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_writer", nativeStdIOWriterWrite, nativeStdIOWriterBytes, nativeStdIOWriterClose),
	nativeStdIOReaderClose:              declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_read_close"),
	nativeStdIOReaderRead:               declareRuntimeOperation(runtimeFamilyIO, "slick_nat_io_read"),
	nativeStdJsonDecode:                 declareRuntimeOperation(runtimeFamilyJSON, "generated-json"),
	nativeStdJsonEncode:                 declareRuntimeOperation(runtimeFamilyJSON, "generated-json"),
	nativeStdMathDivide:                 declareRuntimeOperation(runtimeFamilyMath, "slick_nat_math_div"),
	nativeStdMathRemainder:              declareRuntimeOperation(runtimeFamilyMath, "slick_nat_math_rem"),
	nativeStdPathBase:                   declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_base"),
	nativeStdPathClean:                  declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_clean"),
	nativeStdPathDirectory:              declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_dir"),
	nativeStdPathExtension:              declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_ext"),
	nativeStdPathIsAbsolute:             declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_abs"),
	nativeStdPathJoin:                   declareRuntimeOperation(runtimeFamilyPath, "slick_nat_path_join"),
	nativeStdProcessRun:                 declareRuntimeOperation(runtimeFamilyProcess, "slick_nat_process_run"),
	nativeStdSQLiteDatabaseBegin: declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_db_begin",
		nativeStdSQLiteTransactionExecute, nativeStdSQLiteTransactionQuery, nativeStdSQLiteTransactionCommit,
		nativeStdSQLiteTransactionRollback, nativeStdSQLiteTransactionClose),
	nativeStdSQLiteDatabaseClose:   declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_db_close"),
	nativeStdSQLiteDatabaseExecute: declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_db_exec"),
	nativeStdSQLiteDatabaseQuery:   declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_db_query"),
	nativeStdSQLiteOpen: declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_open",
		nativeStdSQLiteDatabaseExecute, nativeStdSQLiteDatabaseQuery, nativeStdSQLiteDatabaseBegin, nativeStdSQLiteDatabaseClose),
	nativeStdSQLiteTransactionClose:    declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_tx_close"),
	nativeStdSQLiteTransactionCommit:   declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_tx_commit"),
	nativeStdSQLiteTransactionExecute:  declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_tx_exec"),
	nativeStdSQLiteTransactionQuery:    declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_tx_query"),
	nativeStdSQLiteTransactionRollback: declareRuntimeOperation(runtimeFamilySQLite, "slick_nat_sqlite_tx_rollback"),
	nativeStdTextContains:              declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_contains"),
	nativeStdTextCut:                   declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_cut"),
	nativeStdTextEndsWith:              declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_ends"),
	nativeStdTextJoin:                  declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_join"),
	nativeStdTextQuote:                 declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_quote"),
	nativeStdTextReplaceAll:            declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_replace"),
	nativeStdTextSplit:                 declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_split"),
	nativeStdTextStartsWith:            declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_starts"),
	nativeStdTextTrim:                  declareRuntimeOperation(runtimeFamilyText, "slick_nat_text_trim"),
	nativeStdUnicodeIsDigit:            declareRuntimeOperation(runtimeFamilyUnicode, "slick_nat_unicode_is_digit"),
	nativeStdUnicodeIsLetter:           declareRuntimeOperation(runtimeFamilyUnicode, "slick_nat_unicode_is_letter"),
	nativeStdUnicodeIsUpper:            declareRuntimeOperation(runtimeFamilyUnicode, "slick_nat_unicode_is_upper"),
	nativeStdUnicodeIsWhitespace:       declareRuntimeOperation(runtimeFamilyUnicode, "slick_nat_unicode_is_space"),
	nativeStdUTF8DecodeAt:              declareRuntimeOperation(runtimeFamilyUTF8, "slick_nat_utf8_decode_at"),
}

func runtimeOperationsFor(kind runtimeImplementationKind) runtimeOperationTable {
	table := make(runtimeOperationTable)
	for operation, declaration := range runtimeOperationRegistry {
		if implementation, ok := declaration.implementations[kind]; ok {
			table[operation] = implementation
		}
	}
	return table
}

func (table runtimeOperationTable) implements(operation runtimeOperationID) bool {
	_, ok := table[operation]
	return ok
}

func validateRuntimeOperationRegistry() error {
	for operation, declaration := range runtimeOperationRegistry {
		if operation == "" {
			return fmt.Errorf("runtime operation ID is empty")
		}
		if !declaration.family.valid() {
			return fmt.Errorf("runtime operation %s has invalid family %q", operation, declaration.family)
		}
		for implementation, entry := range declaration.implementations {
			switch implementation {
			case runtimeImplementationInterpreter, runtimeImplementationGo, runtimeImplementationLLVM,
				runtimeImplementationRust, runtimeImplementationBun:
			default:
				return fmt.Errorf("runtime operation %s has unknown implementation %q", operation, implementation)
			}
			if entry.entry == "" {
				return fmt.Errorf("runtime operation %s has an empty %s implementation entry", operation, implementation)
			}
		}
		for _, dependency := range declaration.dependencies {
			if _, ok := runtimeOperationRegistry[dependency]; !ok {
				return fmt.Errorf("runtime operation %s depends on unknown operation %s", operation, dependency)
			}
		}
	}
	state := make(map[runtimeOperationID]uint8)
	var visit func(runtimeOperationID) error
	visit = func(operation runtimeOperationID) error {
		if state[operation] == 1 {
			return fmt.Errorf("runtime operation dependency cycle at %s", operation)
		}
		if state[operation] == 2 {
			return nil
		}
		state[operation] = 1
		for _, dependency := range runtimeOperationRegistry[operation].dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[operation] = 2
		return nil
	}
	for operation := range runtimeOperationRegistry {
		if err := visit(operation); err != nil {
			return err
		}
	}
	return nil
}

var (
	interpreterRuntimeOperations = runtimeOperationsFor(runtimeImplementationInterpreter)
	goRuntimeOperations          = runtimeOperationsFor(runtimeImplementationGo)
	llvmRuntimeOperations        = runtimeOperationsFor(runtimeImplementationLLVM)
	// The Rust backend advertises only the operations its generator emits, so an
	// unimplemented family fails before any toolchain work.
	rustRuntimeOperations = rustImplementedRuntimeOperations()
	// The Bun backend advertises only the operations its generator emits.
	bunRuntimeOperations = bunImplementedRuntimeOperations()
)

func rustImplementedRuntimeOperations() runtimeOperationTable {
	table := make(runtimeOperationTable)
	for operation, implementation := range runtimeOperationsFor(runtimeImplementationRust) {
		if _, ok := rustStdFunction(operation); ok {
			table[operation] = implementation
		}
	}
	return table
}

func runtimeInputsForCore(core coreProgram) (backendRuntimeInputs, error) {
	inputs := backendRuntimeInputs{abiVersion: runtimeABIVersion, families: make(map[runtimeFamily]bool)}
	for _, name := range core.RuntimeFamilies {
		family := runtimeFamily(name)
		if !family.valid() {
			return backendRuntimeInputs{}, fmt.Errorf("unknown Core runtime family %q", name)
		}
		inputs.families[family] = true
	}
	found := make(map[runtimeOperationID]struct{})
	var collectExpression func(coreExpression) error
	resourceOperations := make(map[string][]runtimeOperationID)
	for _, class := range core.Classes {
		if !class.Resource {
			continue
		}
		for _, method := range class.Methods {
			if method.Operation != "" {
				resourceOperations[class.ID] = append(resourceOperations[class.ID], method.Operation)
			}
		}
	}
	var collectBlock func(coreBlock) error
	var collectOperation func(runtimeOperationID) error
	collectOperation = func(operation runtimeOperationID) error {
		if operation == "" || strings.HasPrefix(string(operation), "core.") {
			return nil
		}
		declaration, ok := runtimeOperationRegistry[operation]
		if !ok {
			return fmt.Errorf("unknown runtime operation %q", operation)
		}
		if _, exists := found[operation]; exists {
			return nil
		}
		found[operation] = struct{}{}
		for _, dependency := range declaration.dependencies {
			if err := collectOperation(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	collectBlock = func(block coreBlock) error {
		for _, statement := range block.Statements {
			if statement.Value != nil {
				if err := collectExpression(*statement.Value); err != nil {
					return err
				}
			}
			if statement.Body != nil {
				if err := collectBlock(*statement.Body); err != nil {
					return err
				}
			}
		}
		return nil
	}
	collectExpression = func(expression coreExpression) error {
		if err := collectOperation(expression.Operation); err != nil {
			return err
		}
		if expression.Cleanup != nil {
			if err := collectOperation(expression.Cleanup.Operation); err != nil {
				return err
			}
		}
		children := []*coreExpression{expression.Receiver, expression.Value, expression.Left, expression.Right}
		if expression.Kind == "object" {
			for _, operation := range resourceOperations[expression.Declaration] {
				if err := collectOperation(operation); err != nil {
					return err
				}
			}
		}
		for _, child := range children {
			if child != nil {
				if err := collectExpression(*child); err != nil {
					return err
				}
			}
		}
		for _, child := range expression.Elements {
			if err := collectExpression(child); err != nil {
				return err
			}
		}
		for _, entry := range expression.Entries {
			if err := collectExpression(entry.Key); err != nil {
				return err
			}
			if err := collectExpression(entry.Value); err != nil {
				return err
			}
		}
		for _, field := range expression.Fields {
			if err := collectExpression(field.Value); err != nil {
				return err
			}
		}
		for _, argument := range expression.Arguments {
			if err := collectExpression(argument); err != nil {
				return err
			}
		}
		for _, arm := range expression.Arms {
			if err := collectExpression(arm.Value); err != nil {
				return err
			}
		}
		if expression.Body != nil {
			if err := collectBlock(*expression.Body); err != nil {
				return err
			}
		}
		if expression.Alternate != nil {
			if err := collectBlock(*expression.Alternate); err != nil {
				return err
			}
		}
		return nil
	}
	for _, function := range core.Functions {
		if err := collectBlock(function.Body); err != nil {
			return backendRuntimeInputs{}, err
		}
	}
	operations := make([]runtimeOperationID, 0, len(found))

	for operation := range found {
		operations = append(operations, operation)
		inputs.families[runtimeOperationRegistry[operation].family] = true
	}
	sort.Slice(operations, func(left, right int) bool { return operations[left] < operations[right] })
	inputs.operations = operations
	inputs.usesJSON = inputs.families[runtimeFamilyJSON]
	inputs.usesSQLite = inputs.families[runtimeFamilySQLite]
	inputs.usesHTTP = inputs.families[runtimeFamilyHTTP]
	return inputs, nil
}

func validateRuntimeContract(backend backendRegistration, inputs backendRuntimeInputs) error {
	if backend.runtimeABI != inputs.abiVersion {
		return fmt.Errorf("backend %s runtime ABI %d is incompatible with compiler ABI %d", backend.name, backend.runtimeABI, inputs.abiVersion)
	}
	for _, operation := range inputs.operations {
		if !backend.operations.implements(operation) {
			return fmt.Errorf("backend %s does not implement runtime operation %s", backend.name, operation)
		}
	}
	return nil
}
