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

// goBinding is one Slick local in generated Go. name is what an expression
// reads, which inside a narrowed branch is the unwrapped payload variable;
// storage is the variable the declared value lives in, so an assignment always
// writes the optional back rather than the refinement.
type goBinding struct {
	name     string
	typ      string
	storage  string
	declared string
}

func newGoBinding(name, typ string) goBinding {
	return goBinding{name: name, typ: typ, storage: name, declared: typ}
}

type goScope struct {
	function  *functionDecl
	locals    map[string]goBinding
	pending   map[string]string
	taskScope string
}

func (scope *goScope) clone() *goScope {
	locals := make(map[string]goBinding, len(scope.locals))
	for name, binding := range scope.locals {
		locals[name] = binding
	}
	pending := make(map[string]string, len(scope.pending))
	for name, binding := range scope.pending {
		pending[name] = binding
	}
	return &goScope{function: scope.function, locals: locals, pending: pending, taskScope: scope.taskScope}
}

type goGenerator struct {
	program *program
	output  strings.Builder
	nextID  int
	imports map[string]bool
}

func (p *program) generateGo() (string, error) {
	main := p.functions["root.main"]
	if main == nil {
		return "", fmt.Errorf("root.main is not defined")
	}
	if len(main.params) != 0 {
		return "", fmt.Errorf("root.main must not accept parameters")
	}
	generator := &goGenerator{program: p, imports: map[string]bool{
		"errors":        true,
		"fmt":           true,
		"math":          true,
		"os":            true,
		"path/filepath": true,
		"reflect":       true,
		"strconv":       true,
		"strings":       true,
		"unicode/utf8":  true,
	}}
	// Collect JSON codecs first so import decisions are stable before emission.
	jsonNeeds := generator.collectJSONCodecs()
	if len(jsonNeeds) > 0 {
		generator.imports["bytes"] = true
		generator.imports["encoding/json"] = true
		generator.imports["io"] = true
		generator.imports["math"] = true
		generator.imports["strconv"] = true
		for _, need := range jsonNeeds {
			if need.operation == string(nativeStdJsonDecode) {
				generator.imports["unicode/utf8"] = true
				break
			}
		}
	}
	if p.usesStdIO {
		generator.imports["bytes"] = true
		generator.imports["io"] = true
	}
	if p.usesStdHTTP {
		for _, name := range []string{"bytes", "context", "io", "net/http", "net/url", "sort", "time"} {
			generator.imports[name] = true
		}
	}
	if p.usesAsync {
		generator.imports["context"] = true
	}
	// Programs that use std.env need os; it is already imported unconditionally
	// because the shared runtime prints through os.Stdout/Stderr.
	generator.line("package main")
	generator.line("")
	generator.line("import (")
	for _, name := range sortedKeys(generator.imports) {
		generator.line("%q", name)
	}
	generator.line(")")
	generator.line("")
	generator.emitRuntime()
	if err := generator.emitDeclarations(); err != nil {
		return "", err
	}
	if err := generator.emitJSONSupport(); err != nil {
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
	if p.usesAsync {
		generator.line("value, err := %s(context.Background())", goFunctionName(main.qualified))
	} else {
		generator.line("value, err := %s()", goFunctionName(main.qualified))
	}
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
	if g.program.usesAsync {
		g.line(`var returned *slickReturn; var broken *slickBreak; var continued *slickContinue`)
		g.line(`return errors.As(err, &returned) || errors.As(err, &broken) || errors.As(err, &continued)`)
	} else {
		g.line(`switch err.(type) { case *slickReturn, *slickBreak, *slickContinue: return true }`)
		g.line(`return false`)
	}
	g.line(`}`)
	if g.program.usesAsync {
		g.emitTaskRuntime()
	}
	if g.program.usesUsing {
		g.emitUsingRuntime()
	}
	if g.program.usesStdIO {
		g.emitStdIORuntime()
	}
	if g.program.usesStdHTTP {
		g.emitHTTPRuntimeSupport()
	}
	g.line("")
	g.line(`type slickResult[T, E any] struct { ok bool; value T; failure E }`)
	g.line(`func (result slickResult[T, E]) String() string {`)
	g.line(`if result.ok { return "Ok(" + slickFormat(result.value) + ")" }`)
	g.line(`return "Err(" + slickFormat(result.failure) + ")"`)
	g.line(`}`)
	g.line("")
	// slickOptional is a tagged value, not a pointer: an absent int? costs no
	// allocation, and a present 0, false, or "" stays distinct from absence
	// because presence is a separate field and never the Go zero value.
	g.line(`type slickOptional[T any] struct { present bool; value T }`)
	g.line(`func slickSome[T any](value T) slickOptional[T] { return slickOptional[T]{present: true, value: value} }`)
	g.line(`func slickNone[T any]() slickOptional[T] { return slickOptional[T]{} }`)
	g.line(`func (optional slickOptional[T]) String() string {`)
	g.line(`if !optional.present { return "" }`)
	g.line(`return slickFormat(optional.value)`)
	g.line(`}`)
	g.line("")
	g.line(`type slickBytes []byte`)
	g.line(`type slickMapEntry[K comparable, V any] struct { key K; value V }`)
	g.line(`type slickMap[K comparable, V any] struct { entries []slickMapEntry[K, V]; index map[K]int }`)
	g.line(`func slickMapOf[K comparable, V any](entries ...slickMapEntry[K, V]) slickMap[K, V] {`)
	g.line(`index := make(map[K]int, len(entries))`)
	g.line(`ordered := entries[:0]`)
	g.line(`for _, entry := range entries {`)
	g.line(`if existing, ok := index[entry.key]; ok { ordered[existing].value = entry.value; continue }`)
	g.line(`index[entry.key] = len(ordered)`)
	g.line(`ordered = append(ordered, entry)`)
	g.line(`}`)
	g.line(`return slickMap[K, V]{entries: ordered, index: index}`)
	g.line(`}`)
	g.line(`func (m slickMap[K, V]) get(key K) (V, bool) {`)
	g.line(`index, ok := m.index[key]`)
	g.line(`if !ok { var zero V; return zero, false }`)
	g.line(`return m.entries[index].value, true`)
	g.line(`}`)
	g.line(`func slickMapWith[K comparable, V any](source slickMap[K, V], key K, value V) slickMap[K, V] {`)
	g.line(`if index, ok := source.index[key]; ok && reflect.DeepEqual(source.entries[index].value, value) { return source }`)
	g.line(`entries := append([]slickMapEntry[K, V](nil), source.entries...)`)
	g.line(`index := make(map[K]int, len(source.index)+1)`)
	g.line(`for stored, position := range source.index { index[stored] = position }`)
	g.line(`if position, ok := index[key]; ok { entries[position].value = value } else { index[key] = len(entries); entries = append(entries, slickMapEntry[K, V]{key: key, value: value}) }`)
	g.line(`return slickMap[K, V]{entries: entries, index: index}`)
	g.line(`}`)
	g.line(`func slickMapWithout[K comparable, V any](source slickMap[K, V], key K) slickMap[K, V] {`)
	g.line(`removed, ok := source.index[key]`)
	g.line(`if !ok { return source }`)
	g.line(`entries := make([]slickMapEntry[K, V], 0, len(source.entries)-1)`)
	g.line(`index := make(map[K]int, len(source.index)-1)`)
	g.line(`for position, entry := range source.entries { if position != removed { index[entry.key] = len(entries); entries = append(entries, entry) } }`)
	g.line(`return slickMap[K, V]{entries: entries, index: index}`)
	g.line(`}`)
	g.line(`func (m slickMap[K, V]) Len() int { return len(m.entries) }`)
	g.line(`func (slickMap[K, V]) Width() int { return 2 }`)
	g.line(`func (m slickMap[K, V]) At(index, slot int) any { if slot == 0 { return m.entries[index].key }; return m.entries[index].value }`)
	g.line(`func (m slickMap[K, V]) slickFormatMap() string {`)
	g.line(`items := make([]string, len(m.entries))`)
	g.line(`for index, entry := range m.entries { items[index] = slickFormat(entry.key) + ": " + slickFormat(entry.value) }`)
	g.line(`return "map {" + strings.Join(items, ", ") + "}"`)
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
	// slickNamed lets slickFormat render a class value as its canonical Slick
	// type name, matching the interpreter instead of dumping the Go struct or
	// calling the generated Error method.
	g.line(`type slickNamed interface { slickTypeName() string }`)
	g.line(`func slickFormat(value any) string {`)
	g.line(`if value == nil { return "" }`)
	g.line(`if _, ok := value.(struct{}); ok { return "" }`)
	g.line(`if bytes, ok := value.(slickBytes); ok { return fmt.Sprintf("bytes[%%d]", len(bytes)) }`)
	g.line(`if mapping, ok := value.(interface{ slickFormatMap() string }); ok { return mapping.slickFormatMap() }`)
	g.line(`if named, ok := value.(slickNamed); ok { return named.slickTypeName() }`)
	g.line(`reflection := reflect.ValueOf(value)`)
	g.line(`if reflection.Kind() == reflect.Slice || reflection.Kind() == reflect.Array {`)
	g.line(`items := make([]string, reflection.Len())`)
	g.line(`for index := range items { items[index] = slickFormat(reflection.Index(index).Interface()) }`)
	g.line(`open, close := "[", "]"`)
	g.line(`if _, ok := value.([]any); ok { open, close = "(", ")" }`)
	g.line(`return open + strings.Join(items, ", ") + close`)
	g.line(`}`)
	g.line(`if sequence, ok := value.(slickSeq); ok {`)
	g.line(`items := make([]string, sequence.Len())`)
	g.line(`for index := range items { items[index] = slickFormat(slickItem(sequence, index)) }`)
	g.line(`return "[" + strings.Join(items, ", ") + "]"`)
	g.line(`}`)
	g.line(`return fmt.Sprint(value)`)
	g.line(`}`)
	g.line("")
}

func (g *goGenerator) emitTaskRuntime() {
	g.line(`type slickTaskCancelled struct{}`)
	g.line(`func (*slickTaskCancelled) Error() string { return "task cancelled" }`)
	g.line(`func slickCheckCancellation(ctx context.Context) error { if ctx.Err() != nil { return &slickTaskCancelled{} }; return nil }`)
	g.line(`func slickIsTaskCancellation(err error) bool { var cancelled *slickTaskCancelled; return errors.As(err, &cancelled) }`)
	g.line(`type slickTaskPanicFailure struct { value any }`)
	g.line(`func (failure *slickTaskPanicFailure) Error() string { return fmt.Sprintf("panic: %%v", failure.value) }`)
	g.line(`type slickTaskSuppressedFailure struct { primary error; suppressed []error }`)
	g.line(`func (failure *slickTaskSuppressedFailure) Error() string { items := make([]string, len(failure.suppressed)); for index, err := range failure.suppressed { items[index] = err.Error() }; return failure.primary.Error() + " (suppressed: " + strings.Join(items, "; ") + ")" }`)
	g.line(`func (failure *slickTaskSuppressedFailure) Unwrap() error { return failure.primary }`)
	g.line(`func slickTaskSuppress(primary, suppressed error) error { if combined, ok := primary.(*slickTaskSuppressedFailure); ok { failures := append([]error(nil), combined.suppressed...); return &slickTaskSuppressedFailure{primary: combined.primary, suppressed: append(failures, suppressed)} }; return &slickTaskSuppressedFailure{primary: primary, suppressed: []error{suppressed}} }`)
	g.line(`type slickTaskChild interface { slickTaskConsumed() bool; slickTaskCancel(); slickTaskAwaitError() error }`)
	g.line(`type slickTaskResult[T any] struct { value T; err error }`)
	g.line(`type slickTask[T any] struct { result chan slickTaskResult[T]; cancel context.CancelFunc; consumed bool }`)
	g.line(`func (task *slickTask[T]) await() (T, error) { if task.consumed { var zero T; return zero, errors.New("pending binding already awaited") }; task.consumed = true; result := <-task.result; task.cancel(); return result.value, result.err }`)
	g.line(`func (task *slickTask[T]) slickTaskConsumed() bool { return task.consumed }`)
	g.line(`func (task *slickTask[T]) slickTaskCancel() { task.cancel() }`)
	g.line(`func (task *slickTask[T]) slickTaskAwaitError() error { _, err := task.await(); return err }`)
	g.line(`type slickTaskScope struct { context context.Context; cancel context.CancelFunc; children []slickTaskChild }`)
	g.line(`func slickNewTaskScope(parent context.Context) *slickTaskScope { ctx, cancel := context.WithCancel(parent); return &slickTaskScope{context: ctx, cancel: cancel} }`)
	g.line(`func slickStartTask[T any](scope *slickTaskScope, call func(context.Context) (T, error)) *slickTask[T] {`)
	g.line(`ctx, cancel := context.WithCancel(scope.context); task := &slickTask[T]{result: make(chan slickTaskResult[T], 1), cancel: cancel}; scope.children = append(scope.children, task)`)
	g.line(`go func() { result := slickTaskResult[T]{}; defer func() { if failure := recover(); failure != nil { result = slickTaskResult[T]{err: &slickTaskPanicFailure{value: failure}} }; task.result <- result }(); result.value, result.err = call(ctx) }()`)
	g.line(`return task`)
	g.line(`}`)
	g.line(`func slickFinishTaskScope(scope *slickTaskScope, primary error) error {`)
	g.line(`outstanding := false; for _, child := range scope.children { if !child.slickTaskConsumed() { outstanding = true; break } }; if outstanding { scope.cancel() }`)
	g.line(`for _, child := range scope.children { if child.slickTaskConsumed() { continue }; childError := child.slickTaskAwaitError(); if childError == nil || slickIsTaskCancellation(childError) { continue }; if primary == nil { primary = childError } else { primary = slickTaskSuppress(primary, childError) } }`)
	g.line(`scope.cancel(); return primary`)
	g.line(`}`)
	g.line("")
}

func (g *goGenerator) emitUsingRuntime() {
	g.line(`type slickPanicFailure struct { value any }`)
	g.line(`func (failure *slickPanicFailure) Error() string { return fmt.Sprintf("panic: %%v", failure.value) }`)
	g.line(`type slickSuppressedFailure struct { primary error; suppressed []error }`)
	g.line(`func (failure *slickSuppressedFailure) Error() string {`)
	g.line(`items := make([]string, len(failure.suppressed))`)
	g.line(`for index, err := range failure.suppressed { items[index] = err.Error() }`)
	g.line(`return failure.primary.Error() + " (suppressed: " + strings.Join(items, "; ") + ")"`)
	g.line(`}`)
	g.line(`func (failure *slickSuppressedFailure) Unwrap() error { return failure.primary }`)
	g.line(`func slickSuppress(primary, suppressed error) error {`)
	g.line(`if combined, ok := primary.(*slickSuppressedFailure); ok {`)
	g.line(`failures := append([]error(nil), combined.suppressed...)`)
	g.line(`return &slickSuppressedFailure{primary: combined.primary, suppressed: append(failures, suppressed)}`)
	g.line(`}`)
	g.line(`return &slickSuppressedFailure{primary: primary, suppressed: []error{suppressed}}`)
	g.line(`}`)
	g.line(`func slickUsingBody[T any](body func() (T, error)) (value T, err error) {`)
	g.line(`defer func() { if failure := recover(); failure != nil { var zero T; value = zero; err = &slickPanicFailure{value: failure} } }()`)
	g.line(`return body()`)
	g.line(`}`)
	g.line(`func slickUsingClose(close func() error) (err error) {`)
	g.line(`defer func() { if failure := recover(); failure != nil { err = &slickPanicFailure{value: failure} } }()`)
	g.line(`return close()`)
	g.line(`}`)
	g.line(`func slickUsing[T any](body func() (T, error), close func() error) (T, error) {`)
	g.line(`value, bodyError := slickUsingBody(body)`)
	g.line(`closeError := slickUsingClose(close)`)
	g.line(`if closeError == nil { return value, bodyError }`)
	g.line(`var zero T`)
	g.line(`if bodyError == nil || slickIsControl(bodyError) { return zero, closeError }`)
	g.line(`return zero, slickSuppress(bodyError, closeError)`)
	g.line(`}`)
	g.line("")
}

func (g *goGenerator) emitDeclarations() error {
	interfaceNames := sortedKeys(g.program.interfaces)
	for _, name := range interfaceNames {
		if !g.program.usesStdIO && strings.HasPrefix(name, "std.io.") {
			continue
		}
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
			if g.program.usesAsync {
				parameters = append([]string{"context.Context"}, parameters...)
			}
			g.line("%s(%s) (%s, error)", goMethodName(method.name), strings.Join(parameters, ", "), g.goType(result))
		}
		g.line("}")
		g.line("")
	}
	classNames := sortedKeys(g.program.classes)
	for _, name := range classNames {
		if !g.program.usesStdIO && strings.HasPrefix(name, "std.io.") {
			continue
		}
		if !g.program.usesStdHTTP && strings.HasPrefix(name, "std.http.") {
			continue
		}
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
		if class.nativeResource {
			g.line("slickResource *slickIOResource")
		}
		if class.isError {
			g.line("slickMessage string")
		}
		g.line("}")
		// The receiver matches goType's mapping for this class, so both the value
		// form and the error pointer form satisfy slickNamed.
		receiver := ""
		if class.isError {
			receiver = "*"
		}
		g.line("func (%s%s) slickTypeName() string { return %s }", receiver, goClassName(name), strconv.Quote(name))
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
		function := g.program.functions[name]
		if !g.program.usesStdIO && strings.HasPrefix(name, "std.io.") {
			continue
		}
		if !g.program.usesStdHTTP && strings.HasPrefix(name, "std.http.") {
			continue
		}
		if function.native != "" {
			if err := g.emitNativeFunction(function); err != nil {
				return err
			}
			continue
		}
		if err := g.emitFunction(function, ""); err != nil {
			return err
		}
	}
	classNames := sortedKeys(g.program.classes)
	for _, className := range classNames {
		class := g.program.classes[className]
		if !g.program.usesStdIO && strings.HasPrefix(className, "std.io.") {
			continue
		}
		if !g.program.usesStdHTTP && strings.HasPrefix(className, "std.http.") {
			continue
		}
		methodNames := sortedKeys(class.implementations)
		for _, methodName := range methodNames {
			if class.implementations[methodName].native != "" {
				if err := g.emitNativeMethod(class.implementations[methodName], className); err != nil {
					return err
				}
				continue
			}
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
	scope := &goScope{function: function, locals: make(map[string]goBinding, len(function.params)+1), pending: make(map[string]string)}
	parameters := make([]string, 0, len(function.params)+1)
	if g.program.usesAsync {
		parameters = append(parameters, "slickContext context.Context")
	}
	for _, parameter := range function.params {
		typ, err := g.declaredType(function.namespace, function.aliases, parameter.typ)
		if err != nil {
			return err
		}
		variable := g.unique("argument")
		scope.locals[parameter.name] = newGoBinding(variable, typ)
		parameters = append(parameters, variable+" "+g.goType(typ))
	}
	functionName := goFunctionName(function.qualified)
	if receiver != "" {
		self := g.unique("self")
		scope.locals["self"] = newGoBinding(self, receiver)
		functionName = goMethodName(function.name)
		g.line("func (%s %s) %s(%s) (%s, error) {", self, g.goType(receiver), functionName, strings.Join(parameters, ", "), g.goType(resultType))
	} else {
		g.line("func %s(%s) (%s, error) {", functionName, strings.Join(parameters, ", "), g.goType(resultType))
	}
	if g.program.usesAsync {
		g.line("if err := slickCheckCancellation(slickContext); err != nil { return %s, err }", g.zero(resultType))
	}
	body, err := g.blockExpression(function.ast, scope, resultType, "")
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

// blockExpression renders a block as a Go closure. prelude is emitted first and
// carries the payload reads a narrowed branch needs, so an optional is unwrapped
// only inside the branch that proved it present.
func (g *goGenerator) blockExpression(block *blockNode, scope *goScope, resultType, prelude string) (string, error) {
	var body strings.Builder
	if block != nil && block.hasAsync {
		value := g.unique("blockValue")
		blockError := g.unique("blockError")
		taskScope := g.unique("taskScope")
		scope.taskScope = taskScope
		fmt.Fprintf(&body, "func() (%s %s, %s error) {\n", value, g.goType(resultType), blockError)
		fmt.Fprintf(&body, "%s := slickNewTaskScope(slickContext)\n", taskScope)
		fmt.Fprintf(&body, "defer func() { if failure := recover(); failure != nil { %s = %s; %s = &slickTaskPanicFailure{value: failure} }; %s = slickFinishTaskScope(%s, %s) }()\n",
			value, g.zero(resultType), blockError, blockError, taskScope, blockError)
	} else {
		body.WriteString("func() (")
		body.WriteString(g.goType(resultType))
		body.WriteString(", error) {\n")
	}
	body.WriteString(prelude)
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
		fmt.Fprintf(body, "_ = %s\n", variable)
		scope.locals[node.name] = newGoBinding(variable, typ)
	case *asyncLetStatement:
		if err := g.emitAsyncLet(body, node, scope, resultType); err != nil {
			return err
		}
	case *assignmentStatement:
		binding, ok := scope.locals[node.name]
		if !ok {
			return fmt.Errorf("unknown generated binding %s", node.name)
		}
		valueType, err := g.expressionType(node.value, scope)
		if err != nil {
			return err
		}
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("assigned")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		fmt.Fprintf(body, "_ = %s\n", value)
		// The write lands on the declared storage, never on a narrowed payload,
		// and it retires the refinement the way the checker does.
		fmt.Fprintf(body, "%s = %s\n", binding.storage, g.convert(value, valueType, binding.declared))
		scope.locals[node.name] = newGoBinding(binding.storage, binding.declared)
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
		declared, err := g.declaredType(scope.function.namespace, scope.function.aliases, scope.function.result)
		if err != nil {
			return err
		}
		valueType, err := g.expressionType(node.value, scope)
		if err != nil {
			return err
		}
		expression, err := g.expression(node.value, scope)
		if err != nil {
			return err
		}
		value := g.unique("returned")
		callError := g.unique("error")
		fmt.Fprintf(body, "%s, %s := %s\n", value, callError, expression)
		g.emitErrorReturn(body, callError, resultType)
		fmt.Fprintf(body, "_ = %s\n", value)
		fmt.Fprintf(body, "return %s, &slickReturn{value: %s}\n", g.zero(resultType), g.convert(value, valueType, declared))
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
		fmt.Fprintf(body, "_ = %s\n", value)
		if last && actualType != typeNever && g.program.assignable(actualType, resultType) {
			fmt.Fprintf(body, "return %s, nil\n", g.convert(value, actualType, resultType))
			return nil
		}
	default:
		return fmt.Errorf("unsupported generated statement %T", statement)
	}
	if last {
		fmt.Fprintf(body, "return %s, nil\n", g.zero(resultType))
	}
	return nil
}

func (g *goGenerator) emitAsyncLet(body *strings.Builder, node *asyncLetStatement, scope *goScope, resultType string) error {
	if scope.taskScope == "" {
		return fmt.Errorf("async let has no generated task scope")
	}
	call := node.call
	name, ok := call.callee.(*nameExpression)
	if !ok {
		return fmt.Errorf("generated async call target is not a name")
	}
	fmt.Fprintf(body, "if err := slickCheckCancellation(slickContext); err != nil { return %s, err }\n", g.zero(resultType))

	callName := ""
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 && call.resolvedReceiver != "" {
		binding, exists := scope.locals[parts[0]]
		if !exists {
			return fmt.Errorf("unknown generated async receiver %s", parts[0])
		}
		receiver := g.unique("asyncReceiver")
		fmt.Fprintf(body, "%s := %s\n_ = %s\n", receiver, binding.name, receiver)
		callName = receiver + "." + goMethodName(parts[1])
	} else if call.resolvedNative == nativeStdJsonDecode || call.resolvedNative == nativeStdJsonEncode {
		if len(call.resolvedTypeArgs) != 1 {
			return fmt.Errorf("generated async JSON call is missing its type argument")
		}
		operation := "Decode"
		if call.resolvedNative == nativeStdJsonEncode {
			operation = "Encode"
		}
		callName = goJSONHelperName(operation, call.resolvedTypeArgs[0])
	} else {
		function := g.program.resolveFunction(scope.function, name.name)
		if function == nil {
			return fmt.Errorf("unknown generated async function %s", name.name)
		}
		callName = goFunctionName(function.qualified)
	}

	arguments := make([]string, 0, len(call.args)+1)
	for index, argument := range call.args {
		value, err := g.evalExpression(body, argument, scope, "asyncArgument", resultType)
		if err != nil {
			return err
		}
		actual := call.resolvedArgumentTypes[index]
		if index < len(call.resolvedParams) {
			value = g.convert(value, actual, call.resolvedParams[index])
		}
		arguments = append(arguments, value)
	}
	childContext := g.unique("childContext")
	childArguments := arguments
	if call.resolvedNative != nativeStdJsonDecode && call.resolvedNative != nativeStdJsonEncode {
		childArguments = append([]string{childContext}, childArguments...)
	}
	task := g.unique("task")
	fmt.Fprintf(body, "%s := slickStartTask[%s](%s, func(%s context.Context) (%s, error) {\n",
		task, g.goType(call.resolvedResult), scope.taskScope, childContext, g.goType(call.resolvedResult))
	fmt.Fprintf(body, "if err := slickCheckCancellation(%s); err != nil { return %s, err }\n", childContext, g.zero(call.resolvedResult))
	fmt.Fprintf(body, "return %s(%s)\n", callName, strings.Join(childArguments, ", "))
	fmt.Fprintf(body, "})\n_ = %s\n", task)
	scope.pending[node.name] = task
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
	if g.program.usesAsync {
		fmt.Fprintf(body, "if err := slickCheckCancellation(slickContext); err != nil { return %s, err }\n", g.zero(resultType))
	}
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
		loopScope.locals[name] = newGoBinding(variable, bindingTypes[bindingIndex])
	}
	loopBody, err := g.blockExpression(node.body, loopScope, "null", "")
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
		elementType, _ := arrayElementType(typ)
		values := make([]string, 0, len(node.elements))
		for _, element := range node.elements {
			value, err := g.evalExpression(&body, element, scope, "array", typ)
			if err != nil {
				return "", err
			}
			valueType, err := g.expressionType(element, scope)
			if err != nil {
				return "", err
			}
			values = append(values, g.convert(value, valueType, elementType))
		}
		fmt.Fprintf(&body, "return []%s{%s}, nil\n", g.goType(elementType), strings.Join(values, ", "))
	case *mapExpression:
		keyType, valueType, _ := mapTypeArgs(typ)
		entryType := fmt.Sprintf("slickMapEntry[%s, %s]", g.goType(keyType), g.goType(valueType))
		entries := make([]string, 0, len(node.entries))
		for _, entry := range node.entries {
			key, err := g.evalExpression(&body, entry.key, scope, "mapKey", typ)
			if err != nil {
				return "", err
			}
			keyActual, err := g.expressionType(entry.key, scope)
			if err != nil {
				return "", err
			}
			value, err := g.evalExpression(&body, entry.value, scope, "mapValue", typ)
			if err != nil {
				return "", err
			}
			valueActual, err := g.expressionType(entry.value, scope)
			if err != nil {
				return "", err
			}
			entries = append(entries, fmt.Sprintf("%s{key: %s, value: %s}",
				entryType,
				g.convert(key, keyActual, keyType),
				g.convert(value, valueActual, valueType),
			))
		}
		fmt.Fprintf(&body, "return slickMapOf[%s, %s](%s), nil\n",
			g.goType(keyType), g.goType(valueType), strings.Join(entries, ", "))
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
	case *awaitExpression:
		task, exists := scope.pending[node.name]
		if !exists {
			return "", fmt.Errorf("unknown generated pending binding %s", node.name)
		}
		fmt.Fprintf(&body, "return %s.await()\n", task)
	case *unaryExpression:
		value, err := g.evalExpression(&body, node.value, scope, "unary", typ)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "return %s%s, nil\n", node.op, value)

	case *binaryExpression:
		left, err := g.evalExpression(&body, node.left, scope, "left", typ)
		if err != nil {
			return "", err
		}
		if node.op == "&&" {
			fmt.Fprintf(&body, "if !%s { return false, nil }\n", left)
		} else if node.op == "||" {
			fmt.Fprintf(&body, "if %s { return true, nil }\n", left)
		}
		right, err := g.evalExpression(&body, node.right, scope, "right", typ)
		if err != nil {
			return "", err
		}
		switch node.op {
		case "+", "-", "*", "<", "<=", ">", ">=":
			fmt.Fprintf(&body, "return %s %s %s, nil\n", left, node.op, right)
		case "&&", "||":
			fmt.Fprintf(&body, "return %s, nil\n", right)
		default:
			// Both sides are lifted to their join first, so an optional and a
			// null literal are compared as the same tagged Go type instead of
			// an optional against an empty struct.
			left, right, err = g.comparedOperands(node, scope, left, right)
			if err != nil {
				return "", err
			}
			negate := ""
			if node.op == "!=" {
				negate = "!"
			}
			fmt.Fprintf(&body, "return %sslickEqual(%s, %s), nil\n", negate, left, right)
		}
	case *ifExpression:
		condition, err := g.evalExpression(&body, node.condition, scope, "condition", typ)
		if err != nil {
			return "", err
		}
		thenScope, elseScope := scope.clone(), scope.clone()
		thenBlock, err := g.blockExpression(node.thenBlock, thenScope, typ, g.narrowBranch(node.condition, scope, thenScope, true))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "if %s { return %s }\n", condition, thenBlock)
		if node.elseBlock != nil {
			elseBlock, err := g.blockExpression(node.elseBlock, elseScope, typ, g.narrowBranch(node.condition, scope, elseScope, false))
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
	case *resultExpression:
		if err := g.emitResultExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	case *propagateExpression:
		if err := g.emitPropagateExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	case *usingExpression:
		if err := g.emitUsingExpression(&body, node, scope, typ); err != nil {
			return "", err
		}
	case *matchExpression:
		if err := g.emitMatchExpression(&body, node, scope, typ); err != nil {
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
	// A conversion may drop the raw value, so keep it referenced either way.
	fmt.Fprintf(body, "_ = %s\n", value)
	return value, nil
}

// comparedOperands lifts both sides of == or != into the type that can hold
// them both, so an optional compares against null and against its own base
// type as one tagged Go value rather than two unrelated shapes.
func (g *goGenerator) comparedOperands(node *binaryExpression, scope *goScope, left, right string) (string, string, error) {
	leftType, err := g.expressionType(node.left, scope)
	if err != nil {
		return "", "", err
	}
	rightType, err := g.expressionType(node.right, scope)
	if err != nil {
		return "", "", err
	}
	compared, ok := joinTypes(leftType, rightType)
	if !ok {
		return left, right, nil
	}
	return g.convert(left, leftType, compared), g.convert(right, rightType, compared), nil
}

// narrowBranch applies to branch the refinement a null test proves, returning
// the prelude statement that reads the payload. The read is emitted inside the
// branch, so a generated program touches the stored value only where the test
// proved it present.
func (g *goGenerator) narrowBranch(condition expressionNode, outer, branch *goScope, thenBranch bool) string {
	name, present, ok := nullTestOf(condition)
	if !ok || present != thenBranch {
		return ""
	}
	binding, exists := outer.locals[name]
	if !exists {
		return ""
	}
	base, optional := optionalBase(binding.typ)
	if !optional {
		return ""
	}
	variable := g.unique("narrowed")
	branch.locals[name] = goBinding{name: variable, typ: base, storage: binding.storage, declared: binding.declared}
	return fmt.Sprintf("%s := %s.value\n_ = %s\n", variable, binding.name, variable)
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
		valueType, err := g.expressionType(field.value, scope)
		if err != nil {
			return err
		}
		declared, err := g.declaredType(class.namespace, class.aliases, class.fields[field.name].typ)
		if err != nil {
			return err
		}
		fields = append(fields, goFieldName(field.name)+": "+g.convert(value, valueType, declared))
	}
	value := goClassName(typ) + "{" + strings.Join(fields, ", ") + "}"
	if class.isError {
		value = "&" + value
	}
	fmt.Fprintf(body, "return %s, nil\n", value)
	return nil
}
func (g *goGenerator) emitUsingExpression(body *strings.Builder, node *usingExpression, scope *goScope, resultType string) error {
	resource, err := g.evalExpression(body, node.initializer, scope, "resource", resultType)
	if err != nil {
		return err
	}
	usingScope := scope.clone()
	usingScope.locals[node.name] = newGoBinding(resource, node.resolved)
	usingBody, err := g.blockExpression(node.body, usingScope, resultType, "")
	if err != nil {
		return err
	}
	closeArguments := ""
	if g.program.usesAsync {
		closeArguments = "context.WithoutCancel(slickContext)"
	}
	fmt.Fprintf(body, "return slickUsing(%s, func() error { _, err := %s.%s(%s); return err })\n",
		strings.TrimSuffix(usingBody, "()"), resource, goMethodName("Close"), closeArguments)
	return nil
}

func (g *goGenerator) emitCallExpression(body *strings.Builder, node *callExpression, scope *goScope, resultType string) error {
	name, ok := node.callee.(*nameExpression)
	if !ok {
		return fmt.Errorf("generated call target is not a name")
	}
	if g.program.usesAsync {
		fmt.Fprintf(body, "if err := slickCheckCancellation(slickContext); err != nil { return %s, err }\n", g.zero(resultType))
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
	if emitted, err := g.emitMapCallExpression(body, name, scope, arguments, argumentTypes); emitted || err != nil {
		return err
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
	if node.resolvedNative == nativeStdJsonDecode || node.resolvedNative == nativeStdJsonEncode {
		if len(node.resolvedTypeArgs) != 1 {
			return fmt.Errorf("generated JSON call is missing its type argument")
		}
		operation := "Decode"
		if node.resolvedNative == nativeStdJsonEncode {
			operation = "Encode"
		}
		// Arguments already match the substituted parameter types from checking.
		for index := range arguments {
			if index >= len(node.resolvedParams) {
				break
			}
			arguments[index] = g.convert(arguments[index], argumentTypes[index], node.resolvedParams[index])
		}
		fmt.Fprintf(body, "return %s(%s)\n", goJSONHelperName(operation, node.resolvedTypeArgs[0]), strings.Join(arguments, ", "))
		return nil
	}
	call := ""
	var params []paramDecl
	var owner *methodSignature
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 {
		if receiver, exists := scope.locals[parts[0]]; exists {
			call = receiver.name + "." + goMethodName(parts[1])
			method, found := g.program.methodForType(receiver.typ, parts[1])
			if !found {
				return fmt.Errorf("unknown generated method %s", name.name)
			}
			owner, params = method, method.params
		}
	}
	namespace, aliases := scope.function.namespace, scope.function.aliases
	if owner != nil {
		namespace, aliases = owner.namespace, owner.aliases
	}
	if call == "" {
		function := g.program.resolveFunction(scope.function, name.name)
		if function == nil {
			return fmt.Errorf("unknown generated function %s", name.name)
		}
		call = goFunctionName(function.qualified)
		namespace, aliases, params = function.namespace, function.aliases, function.params
	}
	// Each argument enters the parameter's storage type, so passing a T to a T?
	// parameter promotes at the call rather than inside the callee.
	for index := range arguments {
		if index >= len(params) {
			break
		}
		declared, err := g.declaredType(namespace, aliases, params[index].typ)
		if err != nil {
			return err
		}
		arguments[index] = g.convert(arguments[index], argumentTypes[index], declared)
	}
	if g.program.usesAsync {
		arguments = append([]string{"slickContext"}, arguments...)
	}
	fmt.Fprintf(body, "return %s(%s)\n", call, strings.Join(arguments, ", "))
	return nil
}
func (g *goGenerator) emitMapCallExpression(
	body *strings.Builder,
	name *nameExpression,
	scope *goScope,
	arguments, argumentTypes []string,
) (bool, error) {
	parts := strings.Split(name.name, ".")
	if len(parts) != 2 {
		return false, nil
	}
	receiver, exists := scope.locals[parts[0]]
	if !exists {
		return false, nil
	}
	keyType, valueType, isMap := mapTypeArgs(receiver.typ)
	if !isMap {
		return false, nil
	}
	argument := func(index int, target string) string {
		return g.convert(arguments[index], argumentTypes[index], target)
	}
	switch parts[1] {
	case "Get":
		value := g.unique("mapValue")
		present := g.unique("present")
		fmt.Fprintf(body, "%s, %s := %s.get(%s)\n", value, present, receiver.name, argument(0, keyType))
		fmt.Fprintf(body, "if !%s { return %s, nil }\n", present, g.zero(optionalOf(valueType)))
		if isOptionalType(valueType) {
			fmt.Fprintf(body, "return %s, nil\n", value)
		} else {
			fmt.Fprintf(body, "return slickSome(%s), nil\n", value)
		}
	case "Contains":
		value := g.unique("mapValue")
		present := g.unique("present")
		fmt.Fprintf(body, "%s, %s := %s.get(%s)\n", value, present, receiver.name, argument(0, keyType))
		fmt.Fprintf(body, "_ = %s\nreturn %s, nil\n", value, present)
	case "With":
		fmt.Fprintf(body, "return slickMapWith(%s, %s, %s), nil\n",
			receiver.name, argument(0, keyType), argument(1, valueType))
	case "Without":
		fmt.Fprintf(body, "return slickMapWithout(%s, %s), nil\n", receiver.name, argument(0, keyType))
	case "Length":
		fmt.Fprintf(body, "return int64(%s.Len()), nil\n", receiver.name)
	default:
		return false, nil
	}
	return true, nil
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
		binding := arm.binding
		if binding == "" {
			binding = node.binding
		}
		if errorType == "Error" {
			if binding != "" {
				armScope.locals[binding] = goBinding{name: caughtError, typ: "Error"}
			}
			armValue, err := g.expression(arm.value, armScope)
			if err != nil {
				return err
			}
			fmt.Fprintf(body, "return %s\n", armValue)
			return nil
		}
		caught := g.unique("caught")
		fmt.Fprintf(body, "var %s *%s\n", caught, goClassName(errorType))
		fmt.Fprintf(body, "if errors.As(%s, &%s) {\n", caughtError, caught)
		fmt.Fprintf(body, "_ = %s\n", caught)
		if binding != "" {
			armScope.locals[binding] = goBinding{name: caught, typ: errorType}
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

func (g *goGenerator) emitResultExpression(body *strings.Builder, node *resultExpression, scope *goScope, typ string) error {
	payload, err := g.evalExpression(body, node.value, scope, "payload", typ)
	if err != nil {
		return err
	}
	payloadType, err := g.expressionType(node.value, scope)
	if err != nil {
		return err
	}
	success, failure, ok := resultTypeArgs(typ)
	if !ok {
		return fmt.Errorf("generated %s outside a Result type", node.label())
	}
	field, declared := "value", success
	if !node.ok {
		field, declared = "failure", failure
	}
	fmt.Fprintf(body, "return %s{ok: %t, %s: %s}, nil\n", g.goType(typ), node.ok, field, g.convert(payload, payloadType, declared))
	return nil
}

// emitPropagateExpression evaluates the operand once and, on Err, leaves the
// enclosing generated function through the early-return signal carrying an Err
// Result value. Result failures never travel as a Go error.
func (g *goGenerator) emitPropagateExpression(body *strings.Builder, node *propagateExpression, scope *goScope, typ string) error {
	enclosing, err := g.declaredType(scope.function.namespace, scope.function.aliases, scope.function.result)
	if err != nil {
		return err
	}
	if _, _, ok := resultTypeArgs(enclosing); !ok {
		return fmt.Errorf("generated ? outside a Result function %s", scope.function.qualified)
	}
	operand, err := g.evalExpression(body, node.value, scope, "propagated", typ)
	if err != nil {
		return err
	}
	fmt.Fprintf(body, "if !%s.ok { return %s, &slickReturn{value: %s{failure: %s.failure}} }\n",
		operand, g.zero(typ), g.goType(enclosing), operand)
	fmt.Fprintf(body, "return %s.value, nil\n", operand)
	return nil
}

// emitMatchExpression evaluates the scrutinee once and emits one tagged branch
// per arm in source order.
func (g *goGenerator) emitMatchExpression(body *strings.Builder, node *matchExpression, scope *goScope, typ string) error {
	operand, err := g.expressionType(node.value, scope)
	if err != nil {
		return err
	}
	success, failure, ok := resultTypeArgs(operand)
	if !ok {
		return fmt.Errorf("generated match on non-Result type %s", operand)
	}
	scrutinee, err := g.evalExpression(body, node.value, scope, "matched", typ)
	if err != nil {
		return err
	}
	// The scrutinee is evaluated once regardless of whether any arm reads it; a
	// lone catch-all arm never touches it.
	fmt.Fprintf(body, "_ = %s\n", scrutinee)
	for _, arm := range node.arms {
		condition, field, binding := "", "value", success
		switch arm.pattern {
		case matchPatternOk:
			condition = scrutinee + ".ok"
		case matchPatternErr:
			condition, field, binding = "!"+scrutinee+".ok", "failure", failure
		}
		armScope := scope.clone()
		if condition != "" {
			fmt.Fprintf(body, "if %s {\n", condition)
		}
		if arm.binding != "" {
			variable := g.unique("bound")
			fmt.Fprintf(body, "%s := %s.%s\n", variable, scrutinee, field)
			fmt.Fprintf(body, "_ = %s\n", variable)
			armScope.locals[arm.binding] = goBinding{name: variable, typ: binding}
		}
		armValue, err := g.expression(arm.value, armScope)
		if err != nil {
			return err
		}
		fmt.Fprintf(body, "return %s\n", armValue)
		if condition == "" {
			return nil
		}
		fmt.Fprintf(body, "}\n")
	}
	fmt.Fprintf(body, "return %s, nil\n", g.zero(typ))
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
	if node, ok := expression.(*arrayExpression); ok && node.resolved != "" {
		return node.resolved, nil
	}
	if node, ok := expression.(*usingExpression); ok && node.result != "" {
		return node.result, nil
	}
	if node, ok := expression.(*awaitExpression); ok && node.resolved != "" {
		return node.resolved, nil
	}
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
	if base, optional := optionalBase(name); optional {
		element, err := g.resolveDeclaredType(namespace, aliases, base)
		if err != nil {
			return "", err
		}
		return optionalOf(element), nil
	}
	if element, isArray := arrayElementType(name); isArray {
		resolved, err := g.resolveDeclaredType(namespace, aliases, element)
		if err != nil {
			return "", err
		}
		return resolved + "[]", nil
	}
	// Generic arguments resolve exactly as canonicalTypeName resolves them, so a
	// declared Iterable<Dog> or Result<Dog, E> names the same type in the
	// checker and in generated Go.
	if base, args, generic := genericType(name); generic {
		declaration, supported := coreGenericType(base)
		if !supported || len(args) != len(declaration.typeParams) {
			return "", fmt.Errorf("Go backend does not support type %s", name)
		}
		resolved := make([]string, len(args))
		for index, arg := range args {
			argument, err := g.resolveDeclaredType(namespace, aliases, arg)
			if err != nil {
				return "", err
			}
			resolved[index] = argument
		}
		return base + "<" + strings.Join(resolved, ",") + ">", nil
	}
	if declaration, ok := coreType(name); ok && declaration.kind != coreKindGeneric || strings.HasPrefix(name, "(") {
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
	// Optional is checked before array so User[]? maps to an optional slice and
	// User?[] to a slice of optionals; the two never collapse.
	if base, optional := optionalBase(typ); optional {
		return "slickOptional[" + g.goType(base) + "]"
	}
	if element, isArray := arrayElementType(typ); isArray {
		return "[]" + g.goType(element)
	}
	if key, value, ok := mapTypeArgs(typ); ok {
		return "slickMap[" + g.goType(key) + ", " + g.goType(value) + "]"
	}
	if success, failure, ok := resultTypeArgs(typ); ok {
		return "slickResult[" + g.goType(success) + ", " + g.goType(failure) + "]"
	}
	if strings.HasPrefix(typ, "Iterable<") {
		return "slickSeq"
	}
	if strings.HasPrefix(typ, "(") {
		return "[]any"
	}
	switch typ {
	case "bytes":
		return "slickBytes"
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
	case "slickSeq", "error", "[]any", "any":
		return "nil"
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "*") || g.program.interfaces[typ] != nil {
		return "nil"
	}
	return goType + "{}"
}

// convert adapts a generated value to the Go type of to. It is the single
// place a T becomes a present T? and a null literal becomes an absent T?, so
// no call site invents its own promotion.
func (g *goGenerator) convert(value, from, to string) string {
	if from == to || to == "" || from == typeUnknown || from == typeNever {
		return value
	}
	base, optional := optionalBase(to)
	if !optional {
		return value
	}
	if from == "null" {
		return fmt.Sprintf("slickNone[%s]()", g.goType(base))
	}
	if isOptionalType(from) {
		return value
	}
	return fmt.Sprintf("slickSome[%s](%s)", g.goType(base), value)
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
