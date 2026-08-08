package compiler

import (
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BuildPath compiles a Slick file or project into a standalone native binary.
func BuildPath(path, output string) ([]Diagnostic, error) {
	sources, err := loadSources(path)
	if err != nil {
		return nil, err
	}
	program, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		return diagnostics, nil
	}
	generated, err := program.generateGo()
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source([]byte(generated))
	if err != nil {
		return nil, fmt.Errorf("format generated Go: %w", err)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp("", "slick-build-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	sourcePath := filepath.Join(temporary, "main.go")
	if err := os.WriteFile(sourcePath, formatted, 0o644); err != nil {
		return nil, err
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", output, sourcePath)
	buildOutput, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go build: %w: %s", err, strings.TrimSpace(string(buildOutput)))
	}
	return nil, nil
}

type goBinding struct {
	name string
	typ  string
}

type goScope struct {
	function *functionDecl
	locals   map[string]goBinding
}

func (scope *goScope) clone() *goScope {
	locals := make(map[string]goBinding, len(scope.locals))
	for name, binding := range scope.locals {
		locals[name] = binding
	}
	return &goScope{function: scope.function, locals: locals}
}

type goGenerator struct {
	program *program
	output  strings.Builder
	nextID  int
}

func (p *program) generateGo() (string, error) {
	main := p.functions["root.main"]
	if main == nil {
		return "", fmt.Errorf("root.main is not defined")
	}
	if len(main.params) != 0 {
		return "", fmt.Errorf("root.main must not accept parameters")
	}
	generator := &goGenerator{program: p}
	generator.line("package main")
	generator.line("")
	generator.line("import (")
	generator.line("\"errors\"")
	generator.line("\"fmt\"")
	generator.line("\"os\"")
	generator.line("\"reflect\"")
	generator.line("\"strings\"")
	generator.line(")")
	generator.line("")
	generator.emitRuntime()
	if err := generator.emitDeclarations(); err != nil {
		return "", err
	}
	if err := generator.emitFunctions(); err != nil {
		return "", err
	}
	resultType, err := generator.declaredType(main.namespace, main.aliases, main.result)
	if err != nil {
		return "", err
	}
	generator.line("func main() {")
	generator.line("value, err := %s()", goFunctionName(main.qualified))
	generator.line("if err != nil {")
	generator.line("fmt.Fprintln(os.Stderr, err)")
	generator.line("os.Exit(1)")
	generator.line("}")
	if resultType != "null" {
		generator.line("if output := slickFormat(value); output != \"\" {")
		generator.line("fmt.Println(output)")
		generator.line("}")
	}
	generator.line("}")
	return generator.output.String(), nil
}

func (g *goGenerator) emitRuntime() {
	g.line(`type slickReturn struct { value any }`)
	g.line(`func (*slickReturn) Error() string { return "return" }`)
	g.line(`type slickBreak struct{}`)
	g.line(`func (*slickBreak) Error() string { return "break" }`)
	g.line(`type slickContinue struct{}`)
	g.line(`func (*slickContinue) Error() string { return "continue" }`)
	g.line(`func slickIsControl(err error) bool {`)
	g.line(`switch err.(type) { case *slickReturn, *slickBreak, *slickContinue: return true }`)
	g.line(`return false`)
	g.line(`}`)
	g.line("")
	g.line(`type slickSeq interface { Len() int; Width() int; At(int, int) any }`)
	g.line(`type slickSliceSeq[T any] struct { values []T }`)
	g.line(`func (s slickSliceSeq[T]) Len() int { return len(s.values) }`)
	g.line(`func (slickSliceSeq[T]) Width() int { return 1 }`)
	g.line(`func (s slickSliceSeq[T]) At(index, _ int) any { return s.values[index] }`)
	g.line(`func slickSeqOf[T any](values []T) slickSeq { return slickSliceSeq[T]{values: values} }`)
	g.line(`type slickRangeSeq struct { start int64; length int }`)
	g.line(`func (s slickRangeSeq) Len() int { return s.length }`)
	g.line(`func (slickRangeSeq) Width() int { return 1 }`)
	g.line(`func (s slickRangeSeq) At(index, _ int) any { return s.start + int64(index) }`)
	g.line(`func slickRange(start, end int64) (slickSeq, error) {`)
	g.line(`if end <= start { return slickRangeSeq{start: start}, nil }`)
	g.line(`length := end - start`)
	g.line(`if int64(int(length)) != length { return nil, errors.New("range is too large") }`)
	g.line(`return slickRangeSeq{start: start, length: int(length)}, nil`)
	g.line(`}`)
	g.line(`type slickEnumerateSeq struct { source slickSeq }`)
	g.line(`func (s slickEnumerateSeq) Len() int { return s.source.Len() }`)
	g.line(`func (slickEnumerateSeq) Width() int { return 2 }`)
	g.line(`func (s slickEnumerateSeq) At(index, slot int) any {`)
	g.line(`if slot == 0 { return int64(index) }`)
	g.line(`return slickItem(s.source, index)`)
	g.line(`}`)
	g.line(`type slickZipSeq struct { sources []slickSeq; length int }`)
	g.line(`func (s slickZipSeq) Len() int { return s.length }`)
	g.line(`func (s slickZipSeq) Width() int { return len(s.sources) }`)
	g.line(`func (s slickZipSeq) At(index, slot int) any { return slickItem(s.sources[slot], index) }`)
	g.line(`func slickZip(sources ...slickSeq) slickSeq {`)
	g.line(`length := sources[0].Len()`)
	g.line(`for _, source := range sources[1:] { if source.Len() < length { length = source.Len() } }`)
	g.line(`return slickZipSeq{sources: sources, length: length}`)
	g.line(`}`)
	g.line(`func slickItem(sequence slickSeq, index int) any {`)
	g.line(`if sequence.Width() == 1 { return sequence.At(index, 0) }`)
	g.line(`values := make([]any, sequence.Width())`)
	g.line(`for slot := range values { values[slot] = sequence.At(index, slot) }`)
	g.line(`return values`)
	g.line(`}`)
	g.line(`func slickEqual(left, right any) bool { return reflect.DeepEqual(left, right) }`)
	g.line(`func slickFormat(value any) string {`)
	g.line(`if value == nil { return "" }`)
	g.line(`if _, ok := value.(struct{}); ok { return "" }`)
	g.line(`reflection := reflect.ValueOf(value)`)
	g.line(`if reflection.Kind() == reflect.Slice || reflection.Kind() == reflect.Array {`)
	g.line(`items := make([]string, reflection.Len())`)
	g.line(`for index := range items { items[index] = slickFormat(reflection.Index(index).Interface()) }`)
	g.line(`open, close := "[", "]"`)
	g.line(`if _, ok := value.([]any); ok { open, close = "(", ")" }`)
	g.line(`return open + strings.Join(items, ", ") + close`)
	g.line(`}`)
	g.line(`return fmt.Sprint(value)`)
	g.line(`}`)
	g.line("")
}

func (g *goGenerator) emitDeclarations() error {
	interfaceNames := sortedKeys(g.program.interfaces)
	for _, name := range interfaceNames {
		iface := g.program.interfaces[name]
		g.line("type %s interface {", goInterfaceName(name))
		methodNames := sortedKeys(iface.methods)
		for _, methodName := range methodNames {
			method := iface.methods[methodName]
			result, err := g.declaredType(method.namespace, method.aliases, method.result)
			if err != nil {
				return err
			}
			parameters, err := g.parameterTypes(method.namespace, method.aliases, method.params)
			if err != nil {
				return err
			}
			g.line("%s(%s) (%s, error)", goMethodName(method.name), strings.Join(parameters, ", "), g.goType(result))
		}
		g.line("}")
		g.line("")
	}
	classNames := sortedKeys(g.program.classes)
	for _, name := range classNames {
		class := g.program.classes[name]
		g.line("type %s struct {", goClassName(name))
		fieldNames := sortedKeys(class.fields)
		for _, fieldName := range fieldNames {
			field := class.fields[fieldName]
			typ, err := g.declaredType(class.namespace, class.aliases, field.typ)
			if err != nil {
				return err
			}
			g.line("%s %s", goFieldName(field.name), g.goType(typ))
		}
		if class.isError {
			g.line("slickMessage string")
		}
		g.line("}")
		if class.isError {
			g.line("func (value *%s) Error() string {", goClassName(name))
			g.line("if value == nil { return %s }", strconv.Quote(name))
			for _, candidate := range []string{"Message", "message"} {
				field, exists := class.fields[candidate]
				if !exists {
					continue
				}
				typ, err := g.declaredType(class.namespace, class.aliases, field.typ)
				if err != nil {
					return err
				}
				if typ == "string" {
					g.line("if value.%s != \"\" { return %s + value.%s }", goFieldName(candidate), strconv.Quote(name+": "), goFieldName(candidate))
				}
			}
			g.line("if value.slickMessage != \"\" { return %s + value.slickMessage }", strconv.Quote(name+": "))
			g.line("return %s", strconv.Quote(name))
			g.line("}")
		}
		g.line("")
	}
	return nil
}

func (g *goGenerator) emitFunctions() error {
	functionNames := sortedKeys(g.program.functions)
	for _, name := range functionNames {
		if err := g.emitFunction(g.program.functions[name], ""); err != nil {
			return err
		}
	}
	classNames := sortedKeys(g.program.classes)
	for _, className := range classNames {
		class := g.program.classes[className]
		methodNames := sortedKeys(class.implementations)
		for _, methodName := range methodNames {
			if err := g.emitFunction(class.implementations[methodName], className); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *goGenerator) emitFunction(function *functionDecl, receiver string) error {
	resultType, err := g.declaredType(function.namespace, function.aliases, function.result)
	if err != nil {
		return err
	}
	scope := &goScope{function: function, locals: make(map[string]goBinding, len(function.params)+1)}
	parameters := make([]string, 0, len(function.params))
	for _, parameter := range function.params {
		typ, err := g.declaredType(function.namespace, function.aliases, parameter.typ)
		if err != nil {
			return err
		}
		variable := g.unique("argument")
		scope.locals[parameter.name] = goBinding{name: variable, typ: typ}
		parameters = append(parameters, variable+" "+g.goType(typ))
	}
	functionName := goFunctionName(function.qualified)
	if receiver != "" {
		self := g.unique("self")
		scope.locals["self"] = goBinding{name: self, typ: receiver}
		functionName = goMethodName(function.name)
		g.line("func (%s %s) %s(%s) (%s, error) {", self, g.goType(receiver), functionName, strings.Join(parameters, ", "), g.goType(resultType))
	} else {
		g.line("func %s(%s) (%s, error) {", functionName, strings.Join(parameters, ", "), g.goType(resultType))
	}
	body, err := g.blockExpression(function.ast, scope, resultType)
	if err != nil {
		return err
	}
	value := g.unique("value")
	callError := g.unique("error")
	returned := g.unique("returned")
	g.line("%s, %s := %s", value, callError, body)
	g.line("if %s != nil {", callError)
	g.line("var %s *slickReturn", returned)
	g.line("if errors.As(%s, &%s) { return %s.value.(%s), nil }", callError, returned, returned, g.goType(resultType))
	g.line("}")
	g.line("return %s, %s", value, callError)
	g.line("}")
	g.line("")
	return nil
}

func (g *goGenerator) blockExpression(block *blockNode, scope *goScope, resultType string) (string, error) {
	var body strings.Builder
	body.WriteString("func() (")
	body.WriteString(g.goType(resultType))
	body.WriteString(", error) {\n")
	if block == nil || len(block.statements) == 0 {
		fmt.Fprintf(&body, "return %s, nil\n", g.zero(resultType))
	} else {
		for index, statement := range block.statements {
			if err := g.emitStatement(&body, statement, scope, resultType, index == len(block.statements)-1); err != nil {
				return "", err
			}
		}
	}
	body.WriteString("}()")
	return body.String(), nil
}

func (g *goGenerator) emitStatement(body *strings.Builder, statement statementNode, scope *goScope, resultType string, last bool) error {
	switch node := statement.(type) {
	case *letStatement:
		typ, err := g.expressionType(node.value, scope)
		if err != nil {
			return err
		}
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		variable := g.unique("local")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", variable, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		scope.locals[node.name] = goBinding{name: variable, typ: typ}
	case *assignmentStatement:
		binding, ok := scope.locals[node.name]
		if !ok {
			return fmt.Errorf("unknown generated binding %s", node.name)
		}
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("assigned")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		fmt.Fprintf(body, "%s = %s\n", binding.name, value)
	case *forStatement:
		if err := g.emitFor(body, node, scope, resultType); err != nil {
			return err
		}
	case *breakStatement:
		fmt.Fprintf(body, "return %s, &slickBreak{}\n", g.zero(resultType))
		return nil
	case *continueStatement:
		fmt.Fprintf(body, "return %s, &slickContinue{}\n", g.zero(resultType))
		return nil
	case *throwStatement:
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("thrown")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		fmt.Fprintf(body, "return %s, %s\n", g.zero(resultType), value)
		return nil
	case *returnStatement:
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("returned")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		fmt.Fprintf(body, "return %s, &slickReturn{value: %s}\n", g.zero(resultType), value)
		return nil
	case *expressionStatement:
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("expression")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		actualType, err := g.expressionType(node.value, scope)
		if err != nil {
			return err
		}
		if last && actualType == resultType {
			fmt.Fprintf(body, "return %s, nil\n", value)
			return nil
		}
		fmt.Fprintf(body, "_ = %s\n", value)
	default:
		return fmt.Errorf("unsupported generated statement %T", statement)
	}
	if last {
		fmt.Fprintf(body, "return %s, nil\n", g.zero(resultType))
	}
	return nil
}

func (g *goGenerator) emitFor(body *strings.Builder, node *forStatement, scope *goScope, resultType string) error {
	expression, err := g.expression(node.iterable, scope)
	if err != nil {
		return err
	}
	iterableType, err := g.expressionType(node.iterable, scope)
	if err != nil {
		return err
	}
	iterable := g.unique("iterable")
	callError := g.unique("error")
	sequence := g.unique("sequence")
	fmt.Fprintf(body, "%s, %s := %s\n", iterable, callError, expression)
	g.emitErrorReturn(body, callError, resultType)
	if strings.HasSuffix(iterableType, "[]") {
		fmt.Fprintf(body, "%s := slickSeqOf(%s)\n", sequence, iterable)
	} else {
		fmt.Fprintf(body, "%s := %s\n", sequence, iterable)
	}
	elementType, _ := iterableElementType(iterableType)
	bindingTypes := []string{elementType}
	if len(node.bindings) > 1 {
		bindingTypes, _ = tupleElementTypes(elementType)
	}
	index := g.unique("index")
	label := g.unique("loop")
	fmt.Fprintf(body, "%s: for %s := 0; %s < %s.Len(); %s++ {\n", label, index, index, sequence, index)
	loopScope := scope.clone()
	for bindingIndex, name := range node.bindings {
		if name == "_" {
			continue
		}
		variable := g.unique("binding")
		valueExpression := fmt.Sprintf("%s.At(%s, %d)", sequence, index, bindingIndex)
		if len(node.bindings) == 1 {
			valueExpression = fmt.Sprintf("slickItem(%s, %s)", sequence, index)
		}
		fmt.Fprintf(body, "%s := %s.(%s)\n", variable, valueExpression, g.goType(bindingTypes[bindingIndex]))
		loopScope.locals[name] = goBinding{name: variable, typ: bindingTypes[bindingIndex]}
	}
	loopBody, err := g.blockExpression(node.body, loopScope, "null")
	if err != nil {
		return err
	}
	loopError := g.unique("loopError")
	fmt.Fprintf(body, "_, %s := %s\n", loopError, loopBody)
	fmt.Fprintf(body, "if %s != nil {\n", loopError)
	fmt.Fprintf(body, "switch %s.(type) {\n", loopError)
	fmt.Fprintf(body, "case *slickBreak: break %s\n", label)
	fmt.Fprintf(body, "case *slickContinue: continue %s\n", label)
	fmt.Fprintf(body, "default: return %s, %s\n", g.zero(resultType), loopError)
	fmt.Fprintf(body, "}\n}\n}\n")
	return nil
}

func (g *goGenerator) expression(expression expressionNode, scope *goScope) (string, error) {
	typ, err := g.expressionType(expression, scope)
	if err != nil {
		return "", err
	}
	goType := g.goType(typ)
	var body strings.Builder
	fmt.Fprintf(&body, "func() (%s, error) {\n", goType)
	switch node := expression.(type) {
	case *literalExpression:
		fmt.Fprintf(&body, "return %s, nil\n", goLiteral(node.value))
	case *arrayExpression:
		elementType, _ := iterableElementType(typ)
		values := make([]string, 0, len(node.elements))
		for _, element := range node.elements {
			value, err := g.evalExpression(&body, element, scope, "array", typ)
			if err != nil {
				return "", err
			}
			values = append(values, value)
		}
		fmt.Fprintf(&body, "return []%s{%s}, nil\n", g.goType(elementType), strings.Join(values, ", "))
	case *rangeExpression:
		start, err := g.evalExpression(&body, node.start, scope, "rangeStart", typ)
		if err != nil {
			return "", err
		}
		end, err := g.evalExpression(&body, node.end, scope, "rangeEnd", typ)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "return slickRange(%s, %s)\n", start, end)
	case *templateExpression:
		text, err := g.template(node.text, scope)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "return %s, nil\n", text)
	case *nameExpression:
		name, err := g.nameExpression(node, scope)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "return %s, nil\n", name)
	case *objectExpression:
		if err := g.emitObjectExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	case *callExpression:
		if err := g.emitCallExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	case *binaryExpression:
		left, err := g.evalExpression(&body, node.left, scope, "left", typ)
		if err != nil {
			return "", err
		}
		right, err := g.evalExpression(&body, node.right, scope, "right", typ)
		if err != nil {
			return "", err
		}
		switch node.op {
		case "+":
			fmt.Fprintf(&body, "return %s + %s, nil\n", left, right)
		case "==":
			fmt.Fprintf(&body, "return slickEqual(%s, %s), nil\n", left, right)
		case "!=":
			fmt.Fprintf(&body, "return !slickEqual(%s, %s), nil\n", left, right)
		}
	case *ifExpression:
		condition, err := g.evalExpression(&body, node.condition, scope, "condition", typ)
		if err != nil {
			return "", err
		}
		thenBlock, err := g.blockExpression(node.thenBlock, scope.clone(), typ)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "if %s { return %s }\n", condition, thenBlock)
		if node.elseBlock != nil {
			elseBlock, err := g.blockExpression(node.elseBlock, scope.clone(), typ)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&body, "return %s\n", elseBlock)
		} else {
			fmt.Fprintf(&body, "return %s, nil\n", g.zero(typ))
		}
	case *catchExpression:
		if err := g.emitCatchExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported generated expression %T", expression)
	}
	body.WriteString("}()")
	return body.String(), nil
}

func (g *goGenerator) evalExpression(body *strings.Builder, expression expressionNode, scope *goScope, prefix, resultType string) (string, error) {
	generated, err := g.expression(expression, scope)
	if err != nil {
		return "", err
	}
	value := g.unique(prefix)
	callError := g.unique("error")
	fmt.Fprintf(body, "%s, %s := %s\n", value, callError, generated)
	g.emitErrorReturn(body, callError, resultType)
	return value, nil
}

func (g *goGenerator) emitObjectExpression(body *strings.Builder, node *objectExpression, scope *goScope, typ string) error {
	class := g.program.classes[typ]
	if class == nil {
		return fmt.Errorf("unknown generated class %s", typ)
	}
	fields := make([]string, 0, len(node.fields))
	for _, field := range node.fields {
		value, err := g.evalExpression(body, field.value, scope, "field", typ)
		if err != nil {
			return err
		}
		fields = append(fields, goFieldName(field.name)+": "+value)
	}
	value := goClassName(typ) + "{" + strings.Join(fields, ", ") + "}"
	if class.isError {
		value = "&" + value
	}
	fmt.Fprintf(body, "return %s, nil\n", value)
	return nil
}

func (g *goGenerator) emitCallExpression(body *strings.Builder, node *callExpression, scope *goScope, resultType string) error {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		return fmt.Errorf("generated call target is not a name")
	}
	arguments := make([]string, 0, len(node.args))
	argumentTypes := make([]string, 0, len(node.args))
	for _, argument := range node.args {
		value, err := g.evalExpression(body, argument, scope, "argument", resultType)
		if err != nil {
			return err
		}
		arguments = append(arguments, value)
		typ, err := g.expressionType(argument, scope)
		if err != nil {
			return err
		}
		argumentTypes = append(argumentTypes, typ)
	}
	if name.name == "enumerate" {
		sequence := g.sequenceExpression(arguments[0], argumentTypes[0])
		fmt.Fprintf(body, "return slickEnumerateSeq{source: %s}, nil\n", sequence)
		return nil
	}
	if name.name == "zip" {
		sequences := make([]string, 0, len(arguments))
		for index, argument := range arguments {
			sequences = append(sequences, g.sequenceExpression(argument, argumentTypes[index]))
		}
		fmt.Fprintf(body, "return slickZip(%s), nil\n", strings.Join(sequences, ", "))
		return nil
	}
	if errorType, isError := g.program.resolveErrorIn(scope.function.namespace, scope.function.aliases, name.name); isError && g.program.classes[errorType] != nil {
		message := "\"\""
		if len(arguments) > 0 {
			message = "slickFormat(" + arguments[0] + ")"
		}
		fmt.Fprintf(body, "return &%s{slickMessage: %s}, nil\n", goClassName(errorType), message)
		return nil
	}
	call := ""
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 {
		if receiver, exists := scope.locals[parts[0]]; exists {
			call = receiver.name + "." + goMethodName(parts[1])
		}
	}
	if call == "" {
		function := g.program.resolveFunction(scope.function, name.name)
		if function == nil {
			return fmt.Errorf("unknown generated function %s", name.name)
		}
		call = goFunctionName(function.qualified)
	}
	fmt.Fprintf(body, "return %s(%s)\n", call, strings.Join(arguments, ", "))
	return nil
}

func (g *goGenerator) emitCatchExpression(body *strings.Builder, node *catchExpression, scope *goScope, resultType string) error {
	generated, err := g.expression(node.value, scope)
	if err != nil {
		return err
	}
	value := g.unique("caughtValue")
	caughtError := g.unique("caughtError")
	fmt.Fprintf(body, "%s, %s := %s\n", value, caughtError, generated)
	fmt.Fprintf(body, "if %s == nil { return %s, nil }\n", caughtError, value)
	fmt.Fprintf(body, "if slickIsControl(%s) { return %s, %s }\n", caughtError, g.zero(resultType), caughtError)
	for _, arm := range node.arms {
		errorType, ok := g.program.resolveErrorIn(scope.function.namespace, scope.function.aliases, arm.errorType.name)
		if !ok {
			continue
		}
		armScope := scope.clone()
		if errorType == "Error" {
			if node.binding != "" {
				armScope.locals[node.binding] = goBinding{name: caughtError, typ: "Error"}
			}
			armValue, err := g.expression(arm.value, armScope)
			if err != nil {
				return err
			}
			fmt.Fprintf(body, "return %s\n", armValue)
			return nil
		}
		caught := g.unique("caught")
		fmt.Fprintf(body, "if %s, ok := %s.(*%s); ok {\n", caught, caughtError, goClassName(errorType))
		fmt.Fprintf(body, "_ = %s\n", caught)
		if node.binding != "" {
			armScope.locals[node.binding] = goBinding{name: caught, typ: errorType}
		}
		armValue, err := g.expression(arm.value, armScope)
		if err != nil {
			return err
		}
		fmt.Fprintf(body, "return %s\n", armValue)
		fmt.Fprintf(body, "}\n")
	}
	fmt.Fprintf(body, "return %s, %s\n", g.zero(resultType), caughtError)
	return nil
}

func (g *goGenerator) template(template string, scope *goScope) (string, error) {
	var pieces []string
	for {
		start := strings.Index(template, "${")
		if start < 0 {
			pieces = append(pieces, strconv.Quote(template))
			break
		}
		pieces = append(pieces, strconv.Quote(template[:start]))
		template = template[start+2:]
		end := strings.IndexByte(template, '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated generated template")
		}
		name := strings.TrimSpace(template[:end])
		value, err := g.nameExpression(&nameExpression{name: name}, scope)
		if err != nil {
			return "", err
		}
		pieces = append(pieces, "slickFormat("+value+")")
		template = template[end+1:]
	}
	return strings.Join(pieces, " + "), nil
}

func (g *goGenerator) nameExpression(node *nameExpression, scope *goScope) (string, error) {
	parts := strings.Split(node.name, ".")
	binding, ok := scope.locals[parts[0]]
	if !ok {
		return "", fmt.Errorf("unknown generated value %s", node.name)
	}
	value := binding.name
	typ := binding.typ
	for _, fieldName := range parts[1:] {
		class := g.program.classes[typ]
		if class == nil {
			return "", fmt.Errorf("%s has no generated fields", typ)
		}
		field, exists := class.fields[fieldName]
		if !exists {
			return "", fmt.Errorf("%s has no generated field %s", typ, fieldName)
		}
		value += "." + goFieldName(fieldName)
		resolved, err := g.declaredType(class.namespace, class.aliases, field.typ)
		if err != nil {
			return "", err
		}
		typ = resolved
	}
	return value, nil
}

func (g *goGenerator) expressionType(expression expressionNode, scope *goScope) (string, error) {
	locals := make(map[string]string, len(scope.locals))
	for name, binding := range scope.locals {
		locals[name] = binding.typ
	}
	info := g.program.checkASTExpression(expression, &astScope{function: scope.function, locals: locals})
	if info.typ == typeUnknown {
		return "", fmt.Errorf("cannot generate unknown expression type at %s:%d:%d", expression.expressionPos().file, expression.expressionPos().line, expression.expressionPos().column)
	}
	return info.typ, nil
}

func (g *goGenerator) declaredType(namespace string, aliases map[string]aliasDecl, ref typeRef) (string, error) {
	return g.resolveDeclaredType(namespace, aliases, ref.name)
}

func (g *goGenerator) resolveDeclaredType(namespace string, aliases map[string]aliasDecl, name string) (string, error) {
	if strings.HasSuffix(name, "[]") {
		element, err := g.resolveDeclaredType(namespace, aliases, strings.TrimSuffix(name, "[]"))
		if err != nil {
			return "", err
		}
		return element + "[]", nil
	}
	if isBuiltinType(name) || name == "Error" || strings.HasPrefix(name, "Iterable<") || strings.HasPrefix(name, "(") {
		return name, nil
	}
	if strings.ContainsAny(name, "?|") || strings.Contains(name, "<") {
		return "", fmt.Errorf("Go backend does not support type %s", name)
	}
	resolved := g.program.resolveNameIn(namespace, aliases, name)
	if g.program.classes[resolved] == nil && g.program.interfaces[resolved] == nil {
		return "", fmt.Errorf("Go backend cannot resolve type %s", name)
	}
	return resolved, nil
}

func (g *goGenerator) parameterTypes(namespace string, aliases map[string]aliasDecl, parameters []paramDecl) ([]string, error) {
	types := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		typ, err := g.declaredType(namespace, aliases, parameter.typ)
		if err != nil {
			return nil, err
		}
		types = append(types, g.goType(typ))
	}
	return types, nil
}

func (g *goGenerator) goType(typ string) string {
	if strings.HasSuffix(typ, "[]") {
		return "[]" + g.goType(strings.TrimSuffix(typ, "[]"))
	}
	if strings.HasPrefix(typ, "Iterable<") {
		return "slickSeq"
	}
	if strings.HasPrefix(typ, "(") {
		return "[]any"
	}
	switch typ {
	case "bool":
		return "bool"
	case "float":
		return "float64"
	case "int":
		return "int64"
	case "null":
		return "struct{}"
	case "string":
		return "string"
	case "Error":
		return "error"
	}
	if class := g.program.classes[typ]; class != nil {
		if class.isError {
			return "*" + goClassName(typ)
		}
		return goClassName(typ)
	}
	if g.program.interfaces[typ] != nil {
		return goInterfaceName(typ)
	}
	return "any"
}

func (g *goGenerator) zero(typ string) string {
	goType := g.goType(typ)
	switch goType {
	case "bool":
		return "false"
	case "float64", "int64":
		return "0"
	case "string":
		return "\"\""
	case "struct{}":
		return "struct{}{}"
	case "slickSeq", "error", "[]any":
		return "nil"
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "*") || g.program.interfaces[typ] != nil {
		return "nil"
	}
	return goType + "{}"
}

func (g *goGenerator) sequenceExpression(value, typ string) string {
	if strings.HasSuffix(typ, "[]") {
		return "slickSeqOf(" + value + ")"
	}
	return value
}

func (g *goGenerator) emitErrorReturn(body *strings.Builder, errorName, resultType string) {
	fmt.Fprintf(body, "if %s != nil { return %s, %s }\n", errorName, g.zero(resultType), errorName)
}

func (g *goGenerator) unique(prefix string) string {
	g.nextID++
	return fmt.Sprintf("slick_%s_%d", prefix, g.nextID)
}

func (g *goGenerator) line(format string, arguments ...any) {
	fmt.Fprintf(&g.output, format, arguments...)
	g.output.WriteByte('\n')
}

func goFunctionName(name string) string  { return goEncodedName("Function", name) }
func goClassName(name string) string     { return goEncodedName("Class", name) }
func goInterfaceName(name string) string { return goEncodedName("Interface", name) }
func goMethodName(name string) string    { return goEncodedName("Method", name) }
func goFieldName(name string) string     { return goEncodedName("Field", name) }

func goEncodedName(prefix, name string) string {
	return fmt.Sprintf("%s_%x", prefix, []byte(name))
}

func goLiteral(value any) string {
	switch value := value.(type) {
	case nil:
		return "struct{}{}"
	case bool:
		return strconv.FormatBool(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case string:
		return strconv.Quote(value)
	default:
		return "nil"
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
