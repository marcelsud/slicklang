package compiler

import "strings"

// bunStdFamily is one standard-library family's Bun implementation. The compiler
// owns the runtime source and the operation table; a Slick project never selects
// a provider or configures an adapter.
type bunStdFamily struct {
	family    runtimeFamily
	module    string
	functions map[runtimeOperationID]string
}

// bunStdFamilies lists every family the Bun backend implements. A family missing
// from this list makes every operation it owns an explicit lowering error instead
// of a silent gap.
var bunStdFamilies = []bunStdFamily{
	bunStdBuffer,
	bunStdBytes,
	bunStdConvert,
	bunStdEnvironment,
	bunStdFilesystem,
	bunStdHTTP,
	bunStdHTTPServer,
	bunStdIO,
	bunStdJSON,
	bunStdMath,
	bunStdPath,
	bunStdProcess,
	bunStdSQLite,
	bunStdText,
	bunStdUnicode,
	bunStdUTF8,
}

var bunStdOperations = func() map[runtimeOperationID]string {
	functions := make(map[runtimeOperationID]string)
	for _, family := range bunStdFamilies {
		for operation, function := range family.functions {
			functions[operation] = function
		}
	}
	return functions
}()

func bunStdFunction(operation runtimeOperationID) (string, bool) {
	function, ok := bunStdOperations[operation]
	return function, ok
}

// bunStdModules returns the runtime source for every linked family, so a program
// bundles only the standard-library code its Core IR reaches.
func bunStdModules(inputs backendRuntimeInputs) string {
	var output strings.Builder
	for _, family := range bunStdFamilies {
		if !inputs.families[family.family] {
			continue
		}
		output.WriteString(family.module)
		output.WriteByte('\n')
	}
	return output.String()
}

func bunImplementedRuntimeOperations() runtimeOperationTable {
	table := make(runtimeOperationTable)
	for operation, implementation := range runtimeOperationsFor(runtimeImplementationBun) {
		if _, ok := bunStdFunction(operation); ok {
			table[operation] = implementation
		}
	}
	return table
}
