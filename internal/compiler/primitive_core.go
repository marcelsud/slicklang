package compiler

import (
	"fmt"
	"sort"
	"strings"
)

func validatePrimitiveCore(core coreProgram, runtime backendRuntimeInputs, backend string) error {
	for _, class := range core.Classes {
		if !strings.HasPrefix(class.ID, "std.") {
			return primitiveLoweringError(backend, class.Location, "classes are not supported (%s)", class.ID)
		}
	}
	for _, iface := range core.Interfaces {
		if !strings.HasPrefix(iface.ID, "std.") {
			return primitiveLoweringError(backend, iface.Location, "interfaces are not supported (%s)", iface.ID)
		}
	}
	for _, union := range core.Unions {
		if !strings.HasPrefix(union.ID, "std.") {
			return primitiveLoweringError(backend, union.Location, "unions are not supported (%s)", union.ID)
		}
	}
	constants := make(map[string]coreConstant, len(core.Constants))
	for _, constant := range core.Constants {
		if err := validatePrimitiveType(constant.Type); err != nil {
			return primitiveLoweringError(backend, constant.Location, "constant %s: %v", constant.ID, err)
		}
		if err := validatePrimitiveLiteral(constant.Value); err != nil {
			return primitiveLoweringError(backend, constant.Location, "constant %s: %v", constant.ID, err)
		}
		constants[constant.ID] = constant
	}
	functions := make(map[string]coreFunction, len(core.Functions))
	for _, function := range core.Functions {
		functions[function.ID] = function
	}
	main, ok := functions["root.main"]
	if !ok {
		return fmt.Errorf("%s lowering: program has no root.main", backend)
	}
	if len(main.Parameters) != 0 {
		return primitiveLoweringError(backend, main.Location, "root.main parameters are not supported")
	}
	validator := primitiveCoreValidator{backend: backend, functions: functions, constants: constants}
	for _, function := range core.Functions {
		if function.Receiver != "" {
			return primitiveLoweringError(backend, function.Location, "methods are not supported (%s)", function.ID)
		}
		if len(function.Throws) > 0 {
			return primitiveLoweringError(backend, function.Location, "checked failures are not supported (%s)", function.ID)
		}
		if err := validatePrimitiveType(function.Result); err != nil {
			return primitiveLoweringError(backend, function.Location, "function %s result: %v", function.ID, err)
		}
		for _, parameter := range function.Parameters {
			if err := validatePrimitiveType(parameter.Type); err != nil {
				return primitiveLoweringError(backend, function.Location, "function %s parameter %s: %v", function.ID, parameter.Name, err)
			}
		}
		if err := validator.block(function.Body); err != nil {
			return err
		}
	}
	if len(runtime.operations) > 0 {
		return fmt.Errorf("%s lowering contract missed runtime operation %s", backend, runtime.operations[0])
	}
	if len(runtime.families) > 0 {
		families := make([]string, 0, len(runtime.families))
		for family := range runtime.families {
			families = append(families, string(family))
		}
		sort.Strings(families)
		return fmt.Errorf("%s lowering contract missed runtime families %s", backend, strings.Join(families, ", "))
	}
	return nil
}

type primitiveCoreValidator struct {
	backend   string
	functions map[string]coreFunction
	constants map[string]coreConstant
}

func (v primitiveCoreValidator) loweringError(location coreLocation, format string, arguments ...any) error {
	return primitiveLoweringError(v.backend, location, format, arguments...)
}

func (v primitiveCoreValidator) block(block coreBlock) error {
	if block.StructuredTasks {
		return v.loweringError(block.Location, "structured tasks are not supported")
	}
	if block.ResultConversion != "" {
		return v.loweringError(block.Location, "storage conversion %s is not supported", block.ResultConversion)
	}
	for _, statement := range block.Statements {
		switch statement.Kind {
		case "bind":
			if statement.Value == nil || len(statement.Bindings) == 0 {
				return v.loweringError(statement.Location, "invalid binding")
			}
			for _, binding := range statement.Bindings {
				if err := validatePrimitiveType(binding.Type); err != nil {
					return v.loweringError(statement.Location, "binding %s: %v", binding.Name, err)
				}
			}
			if err := v.expression(*statement.Value); err != nil {
				return err
			}
		case "assign":
			if statement.Value == nil || statement.Target == "" {
				return v.loweringError(statement.Location, "invalid assignment")
			}
			if err := v.expression(*statement.Value); err != nil {
				return err
			}
		case "loop":
			if statement.Value == nil || statement.Value.Kind != "range" || len(statement.Bindings) != 1 || statement.Body == nil {
				return v.loweringError(statement.Location, "only one-binding integer range loops are supported")
			}
			if statement.Bindings[0].Type != "int" {
				return v.loweringError(statement.Location, "range loop binding must be int")
			}
			if err := v.expression(*statement.Value); err != nil {
				return err
			}
			if err := v.block(*statement.Body); err != nil {
				return err
			}
		case "break", "continue":
		case "return", "expression":
			if statement.Value == nil {
				return v.loweringError(statement.Location, "%s has no value", statement.Kind)
			}
			if err := v.expression(*statement.Value); err != nil {
				return err
			}
		default:
			return v.loweringError(statement.Location, "statement %s is not supported", statement.Kind)
		}
	}
	return nil
}

func (v primitiveCoreValidator) expression(expression coreExpression) error {
	if err := validatePrimitiveType(expression.Type); err != nil &&
		expression.Kind != "range" && !(expression.Kind == "branch" && expression.Type == typeNever) {
		return v.loweringError(expression.Location, "%s expression: %v", expression.Kind, err)
	}
	if expression.Conversion != "" || expression.ReadConversion != "" {
		return v.loweringError(expression.Location, "storage conversion is not supported")
	}
	if expression.Cleanup != nil {
		return v.loweringError(expression.Location, "checked cleanup is not supported")
	}
	check := func(value *coreExpression) error {
		if value == nil {
			return v.loweringError(expression.Location, "%s expression has a missing operand", expression.Kind)
		}
		return v.expression(*value)
	}
	switch expression.Kind {
	case "literal":
		if expression.Literal == nil {
			return v.loweringError(expression.Location, "literal has no value")
		}
		if err := validatePrimitiveLiteral(*expression.Literal); err != nil {
			return v.loweringError(expression.Location, "%v", err)
		}
		return nil
	case "tuple":
		if len(expression.Elements) < 2 {
			return v.loweringError(expression.Location, "tuple needs at least two elements")
		}
		for _, element := range expression.Elements {
			if err := v.expression(element); err != nil {
				return err
			}
		}
		return nil
	case "name":
		if expression.Declaration != "" {
			if _, ok := v.constants[expression.Declaration]; !ok {
				return v.loweringError(expression.Location, "value reference %s is not supported", expression.Declaration)
			}
		}
		return nil
	case "call":
		if expression.Operation != "" {
			return v.loweringError(expression.Location, "standard-library operation %s is not supported", expression.Operation)
		}
		if expression.Receiver != nil || expression.Value != nil || expression.Declaration == "" {
			return v.loweringError(expression.Location, "only static user-function calls are supported")
		}
		if _, ok := v.functions[expression.Declaration]; !ok {
			return v.loweringError(expression.Location, "unknown static function %s", expression.Declaration)
		}
		if len(expression.Throws) > 0 {
			return v.loweringError(expression.Location, "checked call outcomes are not supported")
		}
		for _, argument := range expression.Arguments {
			if err := v.expression(argument); err != nil {
				return err
			}
		}
		return nil
	case "unary":
		if expression.Operator != "-" && expression.Operator != "!" {
			return v.loweringError(expression.Location, "unary operator %s is not supported", expression.Operator)
		}
		return check(expression.Value)
	case "binary":
		if expression.Operator == "+" && expression.Type == "string" {
			return v.loweringError(expression.Location, "allocating string concatenation is not supported")
		}
		switch expression.Operator {
		case "+", "-", "*", "<", "<=", ">", ">=", "==", "!=", "&&", "||":
		default:
			return v.loweringError(expression.Location, "binary operator %s is not supported", expression.Operator)
		}
		if err := check(expression.Left); err != nil {
			return err
		}
		return check(expression.Right)
	case "branch":
		if err := check(expression.Value); err != nil {
			return err
		}
		if expression.Body == nil || expression.Alternate == nil {
			return v.loweringError(expression.Location, "branch has a missing arm")
		}
		if err := v.block(*expression.Body); err != nil {
			return err
		}
		return v.block(*expression.Alternate)
	case "range":
		if expression.Left == nil || expression.Right == nil {
			return v.loweringError(expression.Location, "range has a missing bound")
		}
		if err := check(expression.Left); err != nil {
			return err
		}
		return check(expression.Right)
	default:
		return v.loweringError(expression.Location, "expression %s is not supported", expression.Kind)
	}
}

func validatePrimitiveType(name string) error {
	switch name {
	case "null", "bool", "int", "float", "string":
		return nil
	}
	parsed := parseTypeName(name)
	if parsed.kind != typeKindTuple || len(parsed.args) < 2 {
		return fmt.Errorf("type %s is not supported", name)
	}
	for _, argument := range parsed.args {
		if err := validatePrimitiveType(argument); err != nil {
			return err
		}
	}
	return nil
}

func validatePrimitiveLiteral(literal coreLiteral) error {
	switch literal.Kind {
	case "null", "bool", "int", "float", "string":
		return nil
	default:
		return fmt.Errorf("literal kind %s is not supported", literal.Kind)
	}
}

func primitiveLoweringError(backend string, location coreLocation, format string, arguments ...any) error {
	message := fmt.Sprintf(format, arguments...)
	if location.File == "" {
		return fmt.Errorf("%s lowering: %s", backend, message)
	}
	return fmt.Errorf("%s lowering %s:%d:%d: %s", backend, location.File, location.Line, location.Column, message)
}
