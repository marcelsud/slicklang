package compiler

import (
	"fmt"
	"sort"
	"strings"
)

// rustStdFamily is one standard-library family's Rust implementation. The
// compiler owns the runtime source, the operation table, and every crate or
// native library the family links; a Slick project never selects a provider.
type rustStdFamily struct {
	family       runtimeFamily
	module       string
	functions    map[runtimeOperationID]string
	dependencies []rustCrate
}

// rustCrate is a compiler-owned Cargo dependency.
type rustCrate struct {
	name            string
	version         string
	features        []string
	defaultFeatures bool
}

// rustStdFamilies lists every family the Rust backend implements. A family
// missing from this list makes every operation it owns an explicit lowering
// error instead of a silent gap.
var rustStdFamilies = []rustStdFamily{
	rustStdBuffer,
	rustStdBytes,
	rustStdConvert,
	rustStdEnvironment,
	rustStdFilesystem,
	rustStdHTTP,
	rustStdHTTPServer,
	rustStdIO,
	rustStdJSON,
	rustStdMath,
	rustStdPath,
	rustStdProcess,
	rustStdSQLite,
	rustStdText,
	rustStdUnicode,
	rustStdUTF8,
}

var rustStdOperations = func() map[runtimeOperationID]string {
	functions := make(map[runtimeOperationID]string)
	for _, family := range rustStdFamilies {
		for operation, function := range family.functions {
			functions[operation] = function
		}
	}
	return functions
}()

func rustStdFunction(operation runtimeOperationID) (string, bool) {
	function, ok := rustStdOperations[operation]
	return function, ok
}

// rustStdModules returns the runtime source for every linked family, so a
// program links only the standard-library code its Core IR reaches.
func rustStdModules(inputs backendRuntimeInputs) string {
	var output strings.Builder
	for _, family := range rustStdFamilies {
		if !inputs.families[family.family] {
			continue
		}
		output.WriteString(family.module)
		output.WriteByte('\n')
	}
	return output.String()
}

func rustStdCrates(inputs backendRuntimeInputs) []rustCrate {
	crates := make(map[string]rustCrate)
	for _, family := range rustStdFamilies {
		if !inputs.families[family.family] {
			continue
		}
		for _, crate := range family.dependencies {
			crates[crate.name] = crate
		}
	}
	names := make([]string, 0, len(crates))
	for name := range crates {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := make([]rustCrate, 0, len(names))
	for _, name := range names {
		ordered = append(ordered, crates[name])
	}
	return ordered
}

func rustCargoManifestFor(inputs backendRuntimeInputs) string {
	var output strings.Builder
	output.WriteString(rustCargoManifest)
	crates := rustStdCrates(inputs)
	if len(crates) == 0 {
		return output.String()
	}
	output.WriteString("\n[dependencies]\n")
	for _, crate := range crates {
		fmt.Fprintf(&output, "%s = { version = %q", crate.name, crate.version)
		if !crate.defaultFeatures {
			output.WriteString(", default-features = false")
		}
		if len(crate.features) > 0 {
			quoted := make([]string, 0, len(crate.features))
			for _, feature := range crate.features {
				quoted = append(quoted, fmt.Sprintf("%q", feature))
			}
			fmt.Fprintf(&output, ", features = [%s]", strings.Join(quoted, ", "))
		}
		output.WriteString(" }\n")
	}
	return output.String()
}

// rustCargoLockFor returns the exact Cargo resolution for the dependency set a
// program links, so every build runs with --locked and stays reproducible.
func rustCargoLockFor(inputs backendRuntimeInputs) string {
	http, sqlite := false, false
	for _, crate := range rustStdCrates(inputs) {
		switch crate.name {
		case rustHTTPCrate:
			http = true
		case rustSQLiteCrate:
			sqlite = true
		}
	}
	switch {
	case http && sqlite:
		return rustCargoLockHTTPSQLite
	case http:
		return rustCargoLockHTTP
	case sqlite:
		return rustCargoLockSQLite
	default:
		return rustCargoLock
	}
}

// rustStdMissingOperation reports the first Core operation the Rust backend does
// not implement, so an unsupported program fails before any toolchain work.
func rustStdMissingOperation(inputs backendRuntimeInputs) (runtimeOperationID, bool) {
	for _, operation := range inputs.operations {
		if strings.HasPrefix(string(operation), "core.") {
			continue
		}
		if _, ok := rustStdFunction(operation); !ok {
			return operation, true
		}
	}
	return "", false
}
