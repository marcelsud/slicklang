package compiler

import (
	"fmt"
	"sort"
	"strings"
)

// validateLanguageCore is the shared gate for backends that implement the whole
// Slick language from Core IR but no host standard-library operation yet. It
// runs before any toolchain work so an unsupported program never replaces an
// existing artifact.
func validateLanguageCore(core coreProgram, runtime backendRuntimeInputs, backend string) error {
	if err := validateNativeCore(core, runtime); err != nil {
		return fmt.Errorf("%s lowering: %w", backend, err)
	}
	if operation, location, ok := firstStandardRuntimeOperation(core); ok {
		return backendLoweringError(backend, location, "standard-library operation %s is not supported", operation)
	}
	if len(runtime.families) > 0 {
		families := make([]string, 0, len(runtime.families))
		for family := range runtime.families {
			families = append(families, string(family))
		}
		sort.Strings(families)
		return backendLoweringError(backend, coreLocation{}, "runtime families %s are not supported", strings.Join(families, ", "))
	}
	for _, function := range core.Functions {
		if function.ID == "root.main" && len(function.Parameters) != 0 {
			return backendLoweringError(backend, function.Location, "root.main parameters are not supported")
		}
	}
	return nil
}

func backendLoweringError(backend string, location coreLocation, format string, arguments ...any) error {
	message := fmt.Sprintf(format, arguments...)
	if location.File == "" {
		return fmt.Errorf("%s lowering: %s", backend, message)
	}
	return fmt.Errorf("%s lowering %s:%d:%d: %s", backend, location.File, location.Line, location.Column, message)
}

// firstStandardRuntimeOperation reports the first host standard-library
// operation a Core program reaches. Compiler-owned "core." operations are part
// of the language and are always implemented.
func firstStandardRuntimeOperation(core coreProgram) (runtimeOperationID, coreLocation, bool) {
	var block func(coreBlock) (runtimeOperationID, coreLocation, bool)
	var expression func(coreExpression) (runtimeOperationID, coreLocation, bool)
	expression = func(value coreExpression) (runtimeOperationID, coreLocation, bool) {
		if value.Operation != "" && !strings.HasPrefix(string(value.Operation), "core.") {
			return value.Operation, value.Location, true
		}
		if value.Cleanup != nil && value.Cleanup.Operation != "" &&
			!strings.HasPrefix(string(value.Cleanup.Operation), "core.") {
			return value.Cleanup.Operation, value.Location, true
		}
		for _, child := range []*coreExpression{value.Value, value.Left, value.Right, value.Receiver} {
			if child != nil {
				if operation, location, ok := expression(*child); ok {
					return operation, location, true
				}
			}
		}
		for _, children := range [][]coreExpression{value.Elements, value.Arguments} {
			for _, child := range children {
				if operation, location, ok := expression(child); ok {
					return operation, location, true
				}
			}
		}
		for _, entry := range value.Entries {
			for _, child := range []coreExpression{entry.Key, entry.Value} {
				if operation, location, ok := expression(child); ok {
					return operation, location, true
				}
			}
		}
		for _, field := range value.Fields {
			if operation, location, ok := expression(field.Value); ok {
				return operation, location, true
			}
		}
		for _, arm := range value.Arms {
			if operation, location, ok := expression(arm.Value); ok {
				return operation, location, true
			}
		}
		for _, child := range []*coreBlock{value.Body, value.Alternate} {
			if child != nil {
				if operation, location, ok := block(*child); ok {
					return operation, location, true
				}
			}
		}
		return "", coreLocation{}, false
	}
	block = func(value coreBlock) (runtimeOperationID, coreLocation, bool) {
		for _, statement := range value.Statements {
			if statement.Value != nil {
				if operation, location, ok := expression(*statement.Value); ok {
					return operation, location, true
				}
			}
			if statement.Body != nil {
				if operation, location, ok := block(*statement.Body); ok {
					return operation, location, true
				}
			}
		}
		return "", coreLocation{}, false
	}
	for _, function := range core.Functions {
		if operation, location, ok := block(function.Body); ok {
			return operation, location, true
		}
	}
	return "", coreLocation{}, false
}
