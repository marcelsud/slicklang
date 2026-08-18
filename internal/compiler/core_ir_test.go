package compiler

import (
	"bytes"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"reflect"
	"strings"
	"testing"
)

type unclassifiedCoreExpression struct{ pos position }

func (n *unclassifiedCoreExpression) expressionPos() position { return n.pos }

var coreIRTestSource = Source{Name: "main.slk", Namespace: "root", Text: `
/// Expression is a test union.
union Expression {
    /// Number carries a value.
    Number(Value: int)
    /// Missing carries no value.
    Missing
}

/// Render formats one expression.
function Render(Node: Expression) -> string {
    match Node {
        Expression.Number(Value) => ` + "`${Value}`" + `
        Expression.Missing => "?"
    }
}

/// Convert applies one callable.
function Convert(Operation: (int) -> int, Value: int) -> int {
    Operation(Value)
}

/// Value is shadowed by a parameter below.
function Value() -> int {
    0
}

/// Shadow reads its parameter, not the same-named function.
function Shadow(Value: int) -> int {
    Value
}

/// ShadowText interpolates its parameter.
function ShadowText(Value: int) -> string {
    ` + "`${Value}`" + `
}

/// MissingValue returns a fieldless union variant.
function MissingValue() -> Expression {
    Expression.Missing
}

/// main exercises construction and calls.
function main() -> string {
    let Offset = 1
    let Node = Expression.Number(Convert((Value: int) -> int { Value + Offset }, 41))
    Render(Node)
}
`}

func TestCoreIRIsTypedResolvedAndDeterministic(t *testing.T) {
	program, diagnostics := compile([]Source{coreIRTestSource})
	requireNoDiagnostics(t, diagnostics)

	first, err := program.debugCore()
	if err != nil {
		t.Fatal(err)
	}
	second, err := program.debugCore()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("Core IR is nondeterministic:\n%s\n%s", first, second)
	}

	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	union := coreUnionNamed(t, core, "root.Expression")
	if got := union.Variants[0]; got.ID != "root.Expression.Number" || got.Tag != 1 || got.Fields[0].Type != "int" {
		t.Fatalf("typed union variant = %#v", got)
	}
	convert := coreFunctionNamed(t, core, "root.Convert")
	call := convert.Body.Statements[0].Value
	if call == nil || call.Kind != "call" || call.Value == nil || !isCallableType(call.Value.Type) || call.Type != "int" {
		t.Fatalf("typed callable invocation = %#v", call)
	}
	main := coreFunctionNamed(t, core, "root.main")
	constructor := main.Body.Statements[1].Value
	if constructor == nil || len(constructor.Arguments) != 1 {
		t.Fatalf("union construction = %#v", constructor)
	}
	orderedCall := &constructor.Arguments[0]
	if orderedCall.Declaration != "root.Convert" || len(orderedCall.Arguments) != 2 ||
		orderedCall.Arguments[0].Kind != "lambda" || orderedCall.Arguments[1].Literal == nil ||
		orderedCall.Arguments[1].Literal.Kind != "int" || orderedCall.Arguments[1].Literal.Integer != 41 {
		t.Fatalf("left-to-right call arguments = %#v", orderedCall)
	}
	if captures := orderedCall.Arguments[0].Captures; len(captures) != 1 ||
		captures[0].Name != "Offset" || captures[0].Type != "int" {
		t.Fatalf("typed closure environment = %#v", captures)
	}
	render := coreFunctionNamed(t, core, "root.Render")
	match := render.Body.Statements[0].Value
	if match == nil || len(match.Arms) != 2 || match.Arms[0].Variant != "root.Expression.Number" || match.Arms[0].Bindings[0].Type != "int" {
		t.Fatalf("resolved union match = %#v", match)
	}
	if template := match.Arms[0].Value.Template; len(template) != 1 || template[0].Name != "Value" || template[0].Type != "int" {
		t.Fatalf("typed template = %#v", template)
	}
	shadow := coreFunctionNamed(t, core, "root.Shadow").Body.Statements[0].Value
	if shadow == nil || shadow.Declaration != "" {
		t.Fatalf("shadowed local declaration = %#v", shadow)
	}
	shadowTemplate := coreFunctionNamed(t, core, "root.ShadowText").Body.Statements[0].Value
	if shadowTemplate == nil || len(shadowTemplate.Template) != 1 || shadowTemplate.Template[0].Declaration != "" {
		t.Fatalf("shadowed template declaration = %#v", shadowTemplate)
	}
	missing := coreFunctionNamed(t, core, "root.MissingValue").Body.Statements[0].Value
	if missing == nil || missing.Declaration != "root.Expression.Missing" {
		t.Fatalf("fieldless variant declaration = %#v", missing)
	}
}

func TestCoreIRSemanticControlContracts(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name: "semantics.slk", Namespace: "root", Text: `
/// Failure is a checked failure.
class Failure implements Error {
    /// Message describes the failure.
    Message: string
}

/// Resource is closed after its using body.
class Resource {
    /// Marker identifies the resource.
    Marker: string
    /// Close releases the resource.
    function Close() -> null {
        null
    }
}

/// Counter supplies a value through interface dispatch.
interface Counter {
    /// Read returns the current count.
    function Read() -> int
}

/// Work is safe to launch as a child task.
function Work(Value: int) -> int {
    Value
}

/// Maybe returns an optional value.
function Maybe(Present: bool) -> int? {
    if (Present) {
        1
    } else {
        null
    }
}

/// Done returns a zero-payload success.
function Done() -> Result<null, Failure> {
    Ok(null)
}

/// Failed returns a checked Result failure.
function Failed() -> Result<null, Failure> {
    Err(Failure {
        Message: "empty"
    })
}

/// Fail raises a checked failure.
function Fail() -> int throws Failure {
    throw Failure {
        Message: "failed"
    }
}

/// RecoverWide uses one catch-wide binding.
function RecoverWide() -> int {
    Fail() catch (FailureValue) {
        Failure => 0
    }
}

/// ReadOptional returns a branch-proven payload.
function ReadOptional(Value: int?) -> int {
    if (Value == null) {
        0
    } else {
        Value
    }
}


/// ReadMaybe calls through a narrowed Optional receiver.
function ReadMaybe(Input: Counter?) -> int {
    if (Input == null) {
        0
    } else {
        Input.Read()
    }
}

/// TextMaybe interpolates a narrowed Optional payload.
function TextMaybe(Value: int?) -> string {
    if (Value == null) {
        "none"
    } else {
        ` + "`${Value}`" + `
    }
}

/// Actions stores a callable field.
class Actions {
    /// Run is invoked through its owning value.
    Run: (int) -> int
}

/// CallMaybe invokes a narrowed Optional callable.
function CallMaybe(Action: ((int) -> int)?) -> int {
    if (Action == null) {
        0
    } else {
        Action(1)
    }
}

/// CallField invokes a callable field through a narrowed owner.
function CallField(Value: Actions?) -> int {
    if (Value == null) {
        0
    } else {
        Value.Run(1)
    }
}
/// Inject stores a concrete value in Optional storage.
function Inject(Value: int) -> int? {
    Value
}

/// FinalLoop ends in a null-producing statement.
function FinalLoop() -> null {
    1
    for Item in [1] {
        Item
    }
}

/// Control exercises nested semantic boundaries.
function Control(Input: Counter, Flag: bool) -> Result<null, Failure> {
    async let Job = Work(1)
    let Value = await Job
    let Closed = using Handle = Resource {
        Marker: "resource"
    } {
        let Recovered = Fail() catch {
            Failure as ErrorValue => 0
        }
        if (Flag && (Input.Read() > 0)) {
            for Item in [Value, Recovered] {
                Item
            }
            Done()
        } else {
            Failed()
        }
    }
    Closed
}
`,
	}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	if core.EvaluationOrder != "left_to_right_once" || core.CleanupSuppression != "immutable_primary_then_cleanup" {
		t.Fatalf("program semantic policies = %#v", core)
	}

	control := coreFunctionNamed(t, core, "root.Control")
	if !control.Body.StructuredTasks || control.Body.TaskExitPolicy != "cancel_then_join" ||
		control.Body.Statements[0].Kind != "task_launch" ||
		control.Body.Statements[1].Value == nil || control.Body.Statements[1].Value.Kind != "task_await" {
		t.Fatalf("structured task lowering = %#v", control.Body)
	}
	using := control.Body.Statements[2].Value
	if using == nil || using.Kind != "using" || using.Cleanup == nil ||
		using.Cleanup.Operation != "core.resource.Close" ||
		using.Cleanup.Suppression != "immutable_primary_then_cleanup" {
		t.Fatalf("using cleanup lowering = %#v", using)
	}
	if caught := using.Body.Statements[0].Value; caught == nil || caught.Kind != "catch" ||
		len(caught.Arms) != 1 || caught.Arms[0].Pattern != "root.Failure" ||
		caught.Arms[0].Bindings[0].Type != "root.Failure" {
		t.Fatalf("checked catch lowering = %#v", caught)
	}
	branch := using.Body.Statements[1].Value
	if branch == nil || branch.Kind != "branch" || branch.Value == nil ||
		branch.Value.Kind != "binary" || !branch.Value.ShortCircuit ||
		branch.Body == nil || branch.Body.Statements[0].Kind != "loop" ||
		branch.Alternate == nil {
		t.Fatalf("nested control lowering = %#v", branch)
	}
	interfaceCall := branch.Value.Right.Left
	if interfaceCall == nil || interfaceCall.Kind != "call" ||
		interfaceCall.Declaration != "root.Counter.Read" ||
		interfaceCall.ReceiverType != "root.Counter" ||
		interfaceCall.Receiver == nil || interfaceCall.Receiver.Name != "Input" {
		t.Fatalf("interface dispatch lowering = %#v", interfaceCall)
	}

	done := coreFunctionNamed(t, core, "root.Done").Body.Statements[0].Value
	if done == nil || done.Kind != "result" || done.ResultVariant != "ok" ||
		done.Value == nil || done.Value.Kind != "literal" || done.Value.Literal == nil ||
		done.Value.Literal.Kind != "null" {
		t.Fatalf("zero-payload Result lowering = %#v", done)
	}
	failed := coreFunctionNamed(t, core, "root.Failed").Body.Statements[0].Value
	if failed == nil || failed.Kind != "result" || failed.ResultVariant != "error" ||
		failed.Value == nil || failed.Value.Type != "root.Failure" {
		t.Fatalf("Result failure lowering = %#v", failed)
	}
	maybe := coreFunctionNamed(t, core, "root.Maybe")
	if maybe.Result != "int?" || maybe.Body.Statements[0].Value == nil ||
		maybe.Body.Statements[0].Value.Alternate.Statements[0].Value.Literal == nil ||
		maybe.Body.Statements[0].Value.Alternate.Statements[0].Value.Literal.Kind != "null" {
		t.Fatalf("Optional boundary lowering = %#v", maybe)
	}
	wideCatch := coreFunctionNamed(t, core, "root.RecoverWide").Body.Statements[0].Value
	if wideCatch == nil || len(wideCatch.Arms) != 1 || len(wideCatch.Arms[0].Bindings) != 1 ||
		wideCatch.Arms[0].Bindings[0].Name != "FailureValue" {
		t.Fatalf("catch-wide binding lowering = %#v", wideCatch)
	}
	optionalRead := coreFunctionNamed(t, core, "root.ReadOptional").Body.Statements[0].Value.Alternate.Statements[0].Value
	if optionalRead == nil || optionalRead.ReadStorageType != "int?" ||
		optionalRead.ReadConversion != "optional_unwrap_proven" {
		t.Fatalf("Optional narrowing lowering = %#v", optionalRead)
	}
	optionalReceiver := coreFunctionNamed(t, core, "root.ReadMaybe").Body.Statements[0].Value.Alternate.Statements[0].Value.Receiver
	if optionalReceiver == nil || optionalReceiver.ReadStorageType != "root.Counter?" ||
		optionalReceiver.ReadConversion != "optional_unwrap_proven" {
		t.Fatalf("Optional receiver lowering = %#v", optionalReceiver)
	}
	optionalTemplate := coreFunctionNamed(t, core, "root.TextMaybe").Body.Statements[0].Value.Alternate.Statements[0].Value.Template
	if len(optionalTemplate) != 1 || optionalTemplate[0].ReadStorageType != "int?" ||
		optionalTemplate[0].ReadConversion != "optional_unwrap_proven" {
		t.Fatalf("Optional template lowering = %#v", optionalTemplate)
	}
	callableCall := coreFunctionNamed(t, core, "root.CallMaybe").Body.Statements[0].Value.Alternate.Statements[0].Value
	if callableCall == nil || callableCall.Value == nil ||
		callableCall.Value.ReadStorageType != "((int)->int)?" ||
		callableCall.Value.ReadConversion != "optional_unwrap_proven" {
		t.Fatalf("Optional callable lowering = %#v", callableCall)
	}
	fieldCall := coreFunctionNamed(t, core, "root.CallField").Body.Statements[0].Value.Alternate.Statements[0].Value
	if fieldCall == nil || fieldCall.Receiver == nil ||
		fieldCall.Receiver.ReadStorageType != "root.Actions?" ||
		fieldCall.Receiver.ReadConversion != "optional_unwrap_proven" ||
		fieldCall.Value == nil || fieldCall.Value.Declaration != "root.Actions.Run" {
		t.Fatalf("callable field lowering = %#v", fieldCall)
	}
	inject := coreFunctionNamed(t, core, "root.Inject")
	if inject.Body.StorageResult != "int?" || inject.Body.ResultConversion != "optional_inject" ||
		inject.Body.Statements[0].Value.StorageType != "int?" ||
		inject.Body.Statements[0].Value.Conversion != "optional_inject" {
		t.Fatalf("Optional injection lowering = %#v", inject)
	}
	if finalLoop := coreFunctionNamed(t, core, "root.FinalLoop"); finalLoop.Body.Result != "null" {
		t.Fatalf("final statement block result = %#v", finalLoop.Body)
	}
}

func TestCoreIRUsesStableStandardOperationIDs(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name: "main.slk", Namespace: "root",
		Text: "/// main returns bytes.\nfunction main() -> bytes { std.bytes.FromUtf8(\"ok\") }\n",
	}})
	requireNoDiagnostics(t, diagnostics)

	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	call := coreFunctionNamed(t, core, "root.main").Body.Statements[0].Value
	if call == nil || call.Declaration != "std.bytes.FromUtf8" || call.Operation != runtimeOperationID(nativeStdBytesFromUtf8) {
		t.Fatalf("standard operation = %#v", call)
	}
}
func TestCoreIRTypesContainNoBackendRepresentations(t *testing.T) {
	assertCoreIRType(t, reflect.TypeOf(coreProgram{}), make(map[reflect.Type]bool))
}

func assertCoreIRType(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if seen[typ] {
		return
	}
	seen[typ] = true
	if typ == reflect.TypeOf(runtimeOperationID("")) {
		return
	}
	if typ.PkgPath() == "" {
		if typ.Kind() == reflect.Interface || typ.Kind() == reflect.Map {
			t.Fatalf("Core IR contains open host type %s", typ)
		}
		return
	}
	if typ.PkgPath() != "slick/internal/compiler" || !strings.HasPrefix(typ.Name(), "core") {
		t.Fatalf("Core IR contains backend or host type %s", typ)
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < typ.NumField(); index++ {
		assertCoreIRType(t, typ.Field(index).Type, seen)
	}
}

func TestCoreIRClassifiesEveryASTNode(t *testing.T) {
	nodes := astNodeTypes(t)
	classified := coreLoweringCases(t)
	for _, node := range sortedKeys(nodes) {
		if _, decided := classified[node]; !decided {
			t.Fatalf("Core IR has no case for %s; classify it in core_ir.go", node)
		}
	}
	for _, node := range sortedKeys(classified) {
		if _, exists := nodes[node]; !exists {
			t.Fatalf("Core IR classifies %s, which is no longer an AST node", node)
		}
	}
}

func TestCoreIRRejectsUnclassifiedInputBeforeEmission(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name: "main.slk", Namespace: "root",
		Text: "/// main returns one.\nfunction main() -> int { 1 }\n",
	}})
	requireNoDiagnostics(t, diagnostics)
	unknown := &unclassifiedCoreExpression{pos: program.functions["root.main"].pos}
	program.expressionTypes[unknown] = "int"
	program.functions["root.main"].ast.statements[0] = &expressionStatement{value: unknown, pos: unknown.pos}

	if _, err := program.lowerCore(); err == nil || !strings.Contains(err.Error(), "unclassified Core IR expression") {
		t.Fatalf("unclassified lowering error = %v", err)
	}
}

func coreFunctionNamed(t *testing.T, core coreProgram, name string) coreFunction {
	t.Helper()
	for _, function := range core.Functions {
		if function.ID == name {
			return function
		}
	}
	t.Fatalf("Core IR has no function %s", name)
	return coreFunction{}
}

func coreUnionNamed(t *testing.T, core coreProgram, name string) coreUnion {
	t.Helper()
	for _, union := range core.Unions {
		if union.ID == name {
			return union
		}
	}
	t.Fatalf("Core IR has no union %s", name)
	return coreUnion{}
}

func coreLoweringCases(t *testing.T) map[string]struct{} {
	t.Helper()
	fileSet := gotoken.NewFileSet()
	parsed, err := goparser.ParseFile(fileSet, "core_ir.go", nil, 0)
	if err != nil {
		t.Fatalf("parse core_ir.go: %v", err)
	}
	cases := make(map[string]struct{})
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if !ok || (function.Name.Name != "statement" && function.Name.Name != "expression") {
			continue
		}
		goast.Inspect(function.Body, func(node goast.Node) bool {
			clause, ok := node.(*goast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				if name, ok := pointerTypeName(expression); ok {
					cases[name] = struct{}{}
				}
			}
			return true
		})
	}
	return cases
}
