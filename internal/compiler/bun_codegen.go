package compiler

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func generateBun(core coreProgram) (string, error) {
	runtime, err := runtimeInputsForCore(core)
	if err != nil {
		return "", err
	}
	if err := validatePrimitiveCore(core, runtime, "Bun"); err != nil {
		return "", err
	}
	generator := bunGenerator{
		core:      core,
		functions: make(map[string]coreFunction, len(core.Functions)),
		constants: make(map[string]coreConstant, len(core.Constants)),
	}
	for _, function := range core.Functions {
		generator.functions[function.ID] = function
	}
	for _, constant := range core.Constants {
		generator.constants[constant.ID] = constant
	}
	return generator.generate()
}

type bunGenerator struct {
	core      coreProgram
	functions map[string]coreFunction
	constants map[string]coreConstant
	output    strings.Builder
	indent    int
	temporary int
	locals    map[string]string
}

func (g *bunGenerator) generate() (string, error) {
	g.line(`import { slickFormatFloat, slickWrapInt, slickWrite } from "@slick/runtime";`)
	g.line("")
	for _, constant := range g.core.Constants {
		literal, err := bunLiteral(constant.Value)
		if err != nil {
			return "", err
		}
		g.line("const %s = %s;", bunConstantName(constant.ID), literal)
	}
	if len(g.core.Constants) > 0 {
		g.line("")
	}
	for _, function := range g.core.Functions {
		if err := g.function(function); err != nil {
			return "", err
		}
		g.line("")
	}
	main := g.functions["root.main"]
	if main.Result == "null" {
		g.line("%s();", bunFunctionName(main.ID))
		return g.output.String(), nil
	}
	g.line("const slick_main_value = %s();", bunFunctionName(main.ID))
	if main.Result == "string" {
		g.line("if (slick_main_value.length !== 0) {")
		g.indent++
		g.printValue("slick_main_value", main.Result)
		g.line(`slickWrite("\n");`)
		g.indent--
		g.line("}")
		return g.output.String(), nil
	}
	if err := g.printValue("slick_main_value", main.Result); err != nil {
		return "", err
	}
	g.line(`slickWrite("\n");`)
	return g.output.String(), nil
}

func (g *bunGenerator) function(function coreFunction) error {
	previous := g.locals
	g.locals = make(map[string]string, len(function.Parameters))
	parameters := make([]string, len(function.Parameters))
	for index, parameter := range function.Parameters {
		parameters[index] = g.declareLocal(parameter.Name)
	}
	g.line("function %s(%s) {", bunFunctionName(function.ID), strings.Join(parameters, ", "))
	g.indent++
	err := g.block(function.Body, func(value string) { g.line("return %s;", value) })
	g.indent--
	g.line("}")
	g.locals = previous
	return err
}

func (g *bunGenerator) block(block coreBlock, final func(string)) error {
	for index, statement := range block.Statements {
		if final != nil && index == len(block.Statements)-1 && statement.Kind == "expression" {
			value, err := g.expression(*statement.Value)
			if err != nil {
				return err
			}
			final(value)
			return nil
		}
		if err := g.statement(statement); err != nil {
			return err
		}
	}
	if final != nil && block.Result != typeNever {
		final("null")
	}
	return nil
}

func (g *bunGenerator) statement(statement coreStatement) error {
	switch statement.Kind {
	case "bind":
		value, err := g.expression(*statement.Value)
		if err != nil {
			return err
		}
		if len(statement.Bindings) == 1 {
			if statement.Bindings[0].Name == "_" {
				g.line("void %s;", value)
			} else {
				g.line("let %s = %s;", g.declareLocal(statement.Bindings[0].Name), value)
			}
			return nil
		}
		bindings := make([]string, len(statement.Bindings))
		for index, binding := range statement.Bindings {
			if binding.Name != "_" {
				bindings[index] = g.declareLocal(binding.Name)
			}
		}
		g.line("let [%s] = %s;", strings.Join(bindings, ", "), value)
	case "assign":
		value, err := g.expression(*statement.Value)
		if err != nil {
			return err
		}
		g.line("%s = %s;", g.localName(statement.Target), value)
	case "loop":
		start, err := g.expression(*statement.Value.Left)
		if err != nil {
			return err
		}
		startName := g.nextTemporary()
		g.line("const %s = %s;", startName, start)
		end, err := g.expression(*statement.Value.Right)
		if err != nil {
			return err
		}
		endName := g.nextTemporary()
		g.line("const %s = %s;", endName, end)
		previous := cloneBunLocals(g.locals)
		binding := statement.Bindings[0].Name
		if binding == "_" {
			binding = g.nextTemporary()
		} else {
			binding = g.declareLocal(binding)
		}
		g.line("for (let %s = %s; %s < %s; %s = slickWrapInt(%s + 1n)) {",
			binding, startName, binding, endName, binding, binding)
		g.indent++
		err = g.block(*statement.Body, nil)
		g.indent--
		g.line("}")
		g.locals = previous
		if err != nil {
			return err
		}
	case "break", "continue":
		g.line("%s;", statement.Kind)
	case "return":
		value, err := g.expression(*statement.Value)
		if err != nil {
			return err
		}
		g.line("return %s;", value)
	case "expression":
		value, err := g.expression(*statement.Value)
		if err != nil {
			return err
		}
		g.line("void %s;", value)
	default:
		return primitiveLoweringError("Bun", statement.Location, "statement %s is not supported", statement.Kind)
	}
	return nil
}

func (g *bunGenerator) expression(expression coreExpression) (string, error) {
	switch expression.Kind {
	case "literal":
		return bunLiteral(*expression.Literal)
	case "name":
		if expression.Declaration != "" {
			return bunConstantName(expression.Declaration), nil
		}
		name := g.localName(expression.Name)
		if name == "" {
			return "", primitiveLoweringError("Bun", expression.Location, "unknown local %s", expression.Name)
		}
		return name, nil
	case "tuple":
		values := make([]string, len(expression.Elements))
		for index, element := range expression.Elements {
			value, err := g.expression(element)
			if err != nil {
				return "", err
			}
			name := g.nextTemporary()
			g.line("const %s = %s;", name, value)
			values[index] = name
		}
		return "[" + strings.Join(values, ", ") + "]", nil
	case "call":
		arguments := make([]string, len(expression.Arguments))
		for index, argument := range expression.Arguments {
			value, err := g.expression(argument)
			if err != nil {
				return "", err
			}
			name := g.nextTemporary()
			g.line("const %s = %s;", name, value)
			arguments[index] = name
		}
		return fmt.Sprintf("%s(%s)", bunFunctionName(expression.Declaration), strings.Join(arguments, ", ")), nil
	case "unary":
		value, err := g.expression(*expression.Value)
		if err != nil {
			return "", err
		}
		name := g.nextTemporary()
		g.line("const %s = %s;", name, value)
		if expression.Operator == "-" && expression.Type == "int" {
			return "slickWrapInt(-" + name + ")", nil
		}
		return expression.Operator + name, nil
	case "binary":
		left, err := g.expression(*expression.Left)
		if err != nil {
			return "", err
		}
		leftName := g.nextTemporary()
		g.line("const %s = %s;", leftName, left)
		if expression.Operator == "&&" || expression.Operator == "||" {
			result := g.nextTemporary()
			g.line("let %s;", result)
			condition, shortValue := "!"+leftName, "false"
			if expression.Operator == "||" {
				condition, shortValue = leftName, "true"
			}
			g.line("if (%s) {", condition)
			g.indent++
			g.line("%s = %s;", result, shortValue)
			g.indent--
			g.line("} else {")
			g.indent++
			right, err := g.expression(*expression.Right)
			if err != nil {
				return "", err
			}
			g.line("%s = %s;", result, right)
			g.indent--
			g.line("}")
			return result, nil
		}
		right, err := g.expression(*expression.Right)
		if err != nil {
			return "", err
		}
		rightName := g.nextTemporary()
		g.line("const %s = %s;", rightName, right)
		operation := leftName + " " + expression.Operator + " " + rightName
		if expression.Operator == "==" || expression.Operator == "!=" {
			operation = bunEquality(leftName, rightName, expression.Left.Type)
			if expression.Operator == "!=" {
				operation = "!" + operation
			}
		}
		if expression.Type == "int" {
			switch expression.Operator {
			case "+", "-", "*":
				operation = "slickWrapInt(" + leftName + " " + expression.Operator + " " + rightName + ")"
			}
		}
		return operation, nil
	case "branch":
		condition, err := g.expression(*expression.Value)
		if err != nil {
			return "", err
		}
		conditionName := g.nextTemporary()
		g.line("const %s = %s;", conditionName, condition)
		result := g.nextTemporary()
		g.line("let %s;", result)
		previous := cloneBunLocals(g.locals)
		g.line("if (%s) {", conditionName)
		g.indent++
		if err := g.block(*expression.Body, func(value string) { g.line("%s = %s;", result, value) }); err != nil {
			return "", err
		}
		g.locals = cloneBunLocals(previous)
		g.indent--
		g.line("} else {")
		g.indent++
		if err := g.block(*expression.Alternate, func(value string) { g.line("%s = %s;", result, value) }); err != nil {
			return "", err
		}
		g.locals = previous
		g.indent--
		g.line("}")
		return result, nil
	default:
		return "", primitiveLoweringError("Bun", expression.Location, "expression %s is not supported", expression.Kind)
	}
}

func (g *bunGenerator) printValue(expression, typ string) error {
	switch typ {
	case "null":
		return nil
	case "string":
		g.line("slickWrite(%s);", expression)
		return nil
	case "int":
		g.line("slickWrite(%s.toString());", expression)
		return nil
	case "float":
		g.line("slickWrite(slickFormatFloat(%s));", expression)
		return nil
	case "bool":
		g.line(`slickWrite(%s ? "true" : "false");`, expression)
		return nil
	}
	parsed := parseTypeName(typ)
	if parsed.kind != typeKindTuple {
		return fmt.Errorf("Bun main result type %s is not printable", typ)
	}
	g.line(`slickWrite("(");`)
	for index, elementType := range parsed.args {
		if index > 0 {
			g.line(`slickWrite(", ");`)
		}
		if err := g.printValue(fmt.Sprintf("%s[%d]", expression, index), elementType); err != nil {
			return err
		}
	}
	g.line(`slickWrite(")");`)
	return nil
}

func (g *bunGenerator) nextTemporary() string {
	name := fmt.Sprintf("slick_tmp_%d", g.temporary)
	g.temporary++
	return name
}

func (g *bunGenerator) declareLocal(name string) string {
	local := fmt.Sprintf("%s_%d", bunLocalName(name), g.temporary)
	g.temporary++
	g.locals[name] = local
	return local
}

func (g *bunGenerator) localName(name string) string {
	return g.locals[name]
}

func cloneBunLocals(locals map[string]string) map[string]string {
	cloned := make(map[string]string, len(locals))
	for name, local := range locals {
		cloned[name] = local
	}
	return cloned
}

func (g *bunGenerator) line(format string, arguments ...any) {
	if format == "" {
		g.output.WriteByte('\n')
		return
	}
	g.output.WriteString(strings.Repeat("  ", g.indent))
	fmt.Fprintf(&g.output, format, arguments...)
	g.output.WriteByte('\n')
}

func bunEquality(left, right, typ string) string {
	parsed := parseTypeName(typ)
	if parsed.kind != typeKindTuple {
		return "(" + left + " === " + right + ")"
	}
	parts := make([]string, len(parsed.args))
	for index, elementType := range parsed.args {
		parts[index] = bunEquality(
			fmt.Sprintf("%s[%d]", left, index),
			fmt.Sprintf("%s[%d]", right, index),
			elementType,
		)
	}
	return "(" + strings.Join(parts, " && ") + ")"
}

func bunLiteral(literal coreLiteral) (string, error) {
	switch literal.Kind {
	case "null":
		return "null", nil
	case "bool":
		return strconv.FormatBool(literal.Boolean), nil
	case "int":
		return strconv.FormatInt(literal.Integer, 10) + "n", nil
	case "float":
		switch {
		case math.IsNaN(literal.Float):
			return "NaN", nil
		case math.IsInf(literal.Float, 1):
			return "Infinity", nil
		case math.IsInf(literal.Float, -1):
			return "-Infinity", nil
		default:
			return strconv.FormatFloat(literal.Float, 'g', -1, 64), nil
		}
	case "string":
		return bunStringLiteral(literal.Text), nil
	default:
		return "", fmt.Errorf("literal kind %s is not supported", literal.Kind)
	}
}

func bunStringLiteral(value string) string {
	var output strings.Builder
	output.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			output.WriteString("\\\\")
		case '"':
			output.WriteString("\\\"")
		case '\b':
			output.WriteString("\\b")
		case '\f':
			output.WriteString("\\f")
		case '\n':
			output.WriteString("\\n")
		case '\r':
			output.WriteString("\\r")
		case '\t':
			output.WriteString("\\t")
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				output.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&output, "\\u%04x", r)
			default:
				value := r - 0x10000
				fmt.Fprintf(&output, "\\u%04x\\u%04x", 0xd800+(value>>10), 0xdc00+(value&0x3ff))
			}
		}
	}
	output.WriteByte('"')
	return output.String()
}

func bunFunctionName(name string) string { return "slick_fn_" + hex.EncodeToString([]byte(name)) }
func bunConstantName(name string) string {
	return "SLICK_CONST_" + strings.ToUpper(hex.EncodeToString([]byte(name)))
}
func bunLocalName(name string) string { return "slick_local_" + hex.EncodeToString([]byte(name)) }
