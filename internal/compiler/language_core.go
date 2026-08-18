package compiler

import (
	"fmt"
	"strings"
)

// validateLanguageCore is the shared gate for backends that implement the whole
// Slick language from Core IR. It runs before any toolchain work so an
// unsupported program never replaces an existing artifact. `supported` reports
// whether the backend implements a standard-library operation.
func validateLanguageCore(core coreProgram, runtime backendRuntimeInputs, backend string, supported func(runtimeOperationID) bool) error {
	if err := validateNativeCore(core, runtime); err != nil {
		return fmt.Errorf("%s lowering: %w", backend, err)
	}
	if operation, location, ok := firstUnsupportedOperation(core, supported); ok {
		return backendLoweringError(backend, location, "standard-library operation %s is not supported", operation)
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

// firstUnsupportedOperation reports the first standard-library operation a Core
// program reaches that the backend does not implement. Compiler-owned "core."
// operations are part of the language and are always implemented.
func firstUnsupportedOperation(core coreProgram, supported func(runtimeOperationID) bool) (runtimeOperationID, coreLocation, bool) {
	unsupported := func(operation runtimeOperationID) bool {
		return operation != "" && !strings.HasPrefix(string(operation), "core.") && !supported(operation)
	}
	var block func(coreBlock) (runtimeOperationID, coreLocation, bool)
	var expression func(coreExpression) (runtimeOperationID, coreLocation, bool)
	expression = func(value coreExpression) (runtimeOperationID, coreLocation, bool) {
		if unsupported(value.Operation) {
			return value.Operation, value.Location, true
		}
		if value.Cleanup != nil && unsupported(value.Cleanup.Operation) {
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
