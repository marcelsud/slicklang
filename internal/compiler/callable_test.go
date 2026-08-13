package compiler_test

import (
	"strings"
	"testing"

	"slick/internal/compiler"
)

// TestLambdaValuesRunTheSameEverywhere covers the callable surface end to end:
// every case is a whole program whose single observable output must match
// between the interpreter and the native binary.
func TestLambdaValuesRunTheSameEverywhere(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "bind and invoke a local lambda",
			source: `
function main() -> int {
    let SumIt = (A: int, B: int) -> int {
        A + B
    }
    SumIt(20, 22)
}
`,
			want: "42",
		},
		{
			name: "lambda parameter shadows a pending outer binding",
			source: `
function Compute() -> int {
    42
}

function main() -> int {
    async let Value = Compute()
    let Identity = (Value: int) -> int {
        Value
    }
    let Ready = await Value
    Identity(Ready)
}
`,
			want: "42",
		},
		{
			name: "lambda local shadows an active using binding",
			source: `
class Handle {
    function Close() -> null {
        null
    }
}

function main() -> int {
    using Value = Handle {} {
        let Read = () -> int {
            let Value = 42
            Value
        }
        Read()
    }
}
`,
			want: "42",
		},
		{
			name: "local callable shadows iterable builtin",
			source: `
function main() -> int {
    let zip = (A: int, B: int) -> int {
        A + B
    }
    zip(20, 22)
}
`,
			want: "42",
		},
		{
			name: "local callable shadows error constructor",
			source: `
class Failure implements Error {}

function main() -> int {
    let Failure = (Value: int) -> int {
        Value + 2
    }
    Failure(40)
}
`,
			want: "42",
		},
		{
			name: "zero parameters",
			source: `
function main() -> string {
    let Greet = () -> string {
        "hello"
    }
    Greet()
}
`,
			want: "hello",
		},
		{
			name: "one parameter",
			source: `
function main() -> int {
    let Double = (Value: int) -> int {
        Value * 2
    }
    Double(21)
}
`,
			want: "42",
		},
		{
			name: "lambda as an argument",
			source: `
function Apply(
    Operation: (int, int) -> int,
    A: int,
    B: int,
) -> int {
    Operation(A, B)
}

function main() -> int {
    Apply(
        (A: int, B: int) -> int {
            A + B
        },
        20,
        22,
    )
}
`,
			want: "42",
		},
		{
			name: "named function as a value",
			source: `
function Add(A: int, B: int) -> int {
    A + B
}

function main() -> int {
    let Operation = Add
    Operation(20, 22)
}
`,
			want: "42",
		},
		{
			name: "named function passed as a callable argument",
			source: `
function Add(A: int, B: int) -> int {
    A + B
}

function Apply(Operation: (int, int) -> int) -> int {
    Operation(20, 22)
}

function main() -> int {
    Apply(Add)
}
`,
			want: "42",
		},
		{
			name: "returned lambda with a by-value capture",
			source: `
function Adder(Offset: int) -> ((int) -> int) {
    (Value: int) -> int {
        Value + Offset
    }
}

function main() -> int {
    let AddTen = Adder(10)
    AddTen(32)
}
`,
			want: "42",
		},
		{
			name: "outer reassignment does not change the capture",
			source: `
function main() -> int {
    let Offset = 10
    let AddOffset = (Value: int) -> int {
        Value + Offset
    }
    Offset = 100
    AddOffset(32)
}
`,
			want: "42",
		},
		{
			name: "nested captures",
			source: `
function main() -> int {
    let Base = 2
    let Outer = (Factor: int) -> ((int) -> int) {
        (Value: int) -> int {
            Value * Factor + Base
        }
    }
    let Scale = Outer(4)
    Scale(10)
}
`,
			want: "42",
		},
		{
			name: "capture outlives the scope that created it",
			source: `
function Make() -> (() -> int) {
    let Total = 42
    () -> int {
        Total
    }
}

function main() -> int {
    let Value = Make()
    Value()
}
`,
			want: "42",
		},
		{
			name: "immediately invoked lambda",
			source: `
function main() -> int {
    ((A: int) -> int {
        A + 2
    })(40)
}
`,
			want: "42",
		},
		{
			name: "invoke a nested call result",
			source: `
function Factory() -> ((int) -> int) {
    (Value: int) -> int {
        Value + 2
    }
}

function main() -> int {
    Factory()(40)
}
`,
			want: "42",
		},
		{
			name: "invoke through a parameter and a return",
			source: `
function Pick(Operation: (int) -> int) -> ((int) -> int) {
    Operation
}

function main() -> int {
    let Chosen = Pick((Value: int) -> int {
        Value + 2
    })
    Chosen(40)
}
`,
			want: "42",
		},
		{
			name: "invoke through a field",
			source: `
class Record {
    Transform: (int) -> int
}

function main() -> int {
    let Held = Record {
        Transform: (Value: int) -> int {
            Value + 2
        },
    }
    Held.Transform(40)
}
`,
			want: "42",
		},
		{
			name: "callables in an array",
			source: `
function Add(A: int, B: int) -> int {
    A + B
}

function Multiply(A: int, B: int) -> int {
    A * B
}

function main() -> int {
    let Operations = [Add, Multiply]
    let First = Operations.Get(0)
    let Second = Operations.Get(1)
    if (First == null) {
        0
    } else {
        if (Second == null) {
            0
        } else {
            First(2, 4) + Second(6, 6)
        }
    }
}
`,
			want: "42",
		},
		{
			name: "callable as a map value",
			source: `
function main() -> int {
    let Operations = map {
        "double": (Value: int) -> int {
            Value * 2
        },
    }
    let Found = Operations.Get("double")
    if (Found != null) {
        Found(21)
    } else {
        0
    }
}
`,
			want: "42",
		},
		{
			name: "callable inside a tuple",
			source: `
function main() -> int {
    let Pair = ((Value: int) -> int {
        Value + 2
    }, 40)
    let (Operation, Amount) = Pair
    Operation(Amount)
}
`,
			want: "42",
		},
		{
			name: "callable inside a Result",
			source: `
class Missing implements Error {
    Message: string
}

function Lookup() -> Result<(int) -> int, Missing> {
    Ok((Value: int) -> int {
        Value + 2
    })
}

function main() -> int {
    match Lookup() {
        Ok(Operation) => Operation(40)
        Err(Failure) => 0
    }
}
`,
			want: "42",
		},
		{
			name: "optional callable narrowed before invocation",
			source: `
class Holder {
    Transform: ((int) -> int)?
}

function main() -> int {
    let Held = Holder {
        Transform: (Value: int) -> int {
            Value + 2
        },
    }
    let Operation = Held.Transform
    if (Operation != null) {
        Operation(40)
    } else {
        0
    }
}
`,
			want: "42",
		},
		{
			name: "checked effects propagate through invocation",
			source: `
class Invalid implements Error {
    Message: string
}

function main() -> string {
    let Parse = (Text: string) -> string throws Invalid {
        if (Text == "") {
            throw Invalid { Message: "empty" }
        }
        Text
    }
    Parse("") catch {
        Invalid as Failure => Failure.Message
    }
}
`,
			want: "empty",
		},
		{
			name: "declared effect leaves through the enclosing function",
			source: `
class Invalid implements Error {
    Message: string
}

function Run(Operation: (string) -> string throws Invalid) -> string throws Invalid {
    Operation("")
}

function main() -> string {
    let Parse = (Text: string) -> string throws Invalid {
        throw Invalid { Message: "declared" }
    }
    Run(Parse) catch {
        Invalid as Failure => Failure.Message
    }
}
`,
			want: "declared",
		},
		{
			name: "a non-throwing callable fits a throwing callable type",
			source: `
class Invalid implements Error {
    Message: string
}

function Run(Operation: (string) -> string throws Invalid) -> string throws Invalid {
    Operation("value")
}

function main() -> string {
    let Echo = (Text: string) -> string {
        Text
    }
    Run(Echo) catch {
        Invalid as Failure => Failure.Message
    }
}
`,
			want: "value",
		},
		{
			name: "return leaves the lambda and not its enclosing function",
			source: `
function main() -> int {
    let Choose = (Value: int) -> int {
        if (Value > 10) {
            return 42
        }
        0
    }
    Choose(11) + Choose(1)
}
`,
			want: "42",
		},
		{
			name: "captured self follows the value rule",
			source: `
class Counter {
    Start: int

    function Stepper() -> ((int) -> int) {
        (Value: int) -> int {
            Value + self.Start
        }
    }
}

function main() -> int {
    let Machine = Counter { Start: 2 }
    let Step = Machine.Stepper()
    Step(40)
}
`,
			want: "42",
		},
		{
			name: "callable taking a callable",
			source: `
function Compose(Operation: ((int) -> int, int) -> int) -> int {
    Operation(
        (Value: int) -> int {
            Value * 2
        },
        21,
    )
}

function main() -> int {
    Compose((Operation: (int) -> int, Value: int) -> int {
        Operation(Value)
    })
}
`,
			want: "42",
		},
		{
			name: "callee and arguments evaluate exactly once in source order",
			source: `
function Note(Log: Buffer<string>, Text: string, Value: int) -> int {
    std.buffer.Push<string>(Log, Text)
    Value
}

function main() -> string {
    let Log = std.buffer.New<string>()
    let Choose = (Left: int, Right: int) -> ((int, int) -> int) {
        std.buffer.Push<string>(Log, "callee")
        (A: int, B: int) -> int {
            A + B
        }
    }
    let Total = Choose(Note(Log, "callee-left", 1), Note(Log, "callee-right", 2))(Note(Log, "first", 20), Note(Log, "second", 22))
    let Entries = std.buffer.Freeze<string>(Log)
    ` + "`${Total}:${Entries}`" + `
}
`,
			want: "42:[callee-left, callee-right, callee, first, second]",
		},
		{
			name: "lambdas in an array literal",
			source: `
function main() -> int {
    let Operations = [
        (Value: int) -> int {
            Value + 1
        },
        (Value: int) -> int {
            Value * 2
        },
    ]
    let First = Operations.Get(0)
    let Second = Operations.Get(1)
    if (First == null) {
        0
    } else {
        if (Second == null) {
            0
        } else {
            First(1) + Second(20)
        }
    }
}
`,
			want: "42",
		},
		{
			name: "a two-effect callable type has one spelling",
			source: `
class Invalid implements Error {
    Message: string
}

class Missing implements Error {
    Message: string
}

function Read(Text: string) -> int throws Invalid | Missing {
    if (Text == "") {
        throw Missing { Message: "missing" }
    }
    42
}

function Run(Operation: (string) -> int throws Missing | Invalid) -> int throws Invalid | Missing {
    Operation("value")
}

function main() -> int {
    Run(Read) catch {
        Error => 0
    }
}
`,
			want: "42",
		},
		{
			name: "a narrowed binding is captured as its payload",
			source: `
class Holder {
    Amount: int?
}

function main() -> int {
    let Held = Holder { Amount: 40 }
    let Maybe = Held.Amount
    if (Maybe == null) {
        0
    } else {
        let Operation = (Value: int) -> int {
            Value + Maybe
        }
        Operation(2)
    }
}
`,
			want: "42",
		},
		{
			name: "lambdas coexist with async tasks",
			source: `
function Compute(Value: int) -> int {
    Value * 2
}

function main() -> int {
    let Offset = 2
    let AddOffset = (Value: int) -> int {
        Value + Offset
    }
    async let Pending = Compute(20)
    let Doubled = await Pending
    AddOffset(Doubled)
}
`,
			want: "42",
		},
		{
			name: "printing a callable is one deterministic marker",
			source: `
function main() -> string {
    let Operation = (Value: int) -> int {
        Value
    }
    ` + "`${Operation}`" + `
}
`,
			want: "<callable>",
		},
		{
			name: "a collection of callables prints the same marker",
			source: `
function main() -> ((int) -> int)[] {
    [
        (Value: int) -> int {
            Value
        },
        (Value: int) -> int {
            Value
        },
    ]
}
`,
			want: "[<callable>, <callable>]",
		},
		{
			name: "a class holding a callable prints its own name",
			source: `
class Record {
    Transform: (int) -> int
}

function main() -> Record {
    Record {
        Transform: (Value: int) -> int {
            Value
        },
    }
}
`,
			want: "root.Record",
		},
		{
			name: "classes holding callables compare unequal in both backends",
			source: `
class Record {
    Transform: (int) -> int
}

function main() -> bool {
    let Operation = (Value: int) -> int {
        Value
    }
    let Left = Record { Transform: Operation }
    let Right = Record { Transform: Operation }
    Left == Right
}
`,
			want: "false",
		},
		{
			name: "a call keeps its argument list across a line break",
			source: `
function Apply(Operation: (int, int) -> int, A: int, B: int) -> int {
    Operation(A, B)
}

function main() -> int {
    let SumIt = (A: int, B: int) -> int {
        A + B
    }
    Apply
        (SumIt, 20, 22)
}
`,
			want: "42",
		},
		{
			name: "a lambda statement is not swallowed as an argument list",
			source: `
function Make() -> ((int) -> int) {
    let Offset = 2
    (Value: int) -> int {
        Value + Offset
    }
}

function main() -> int {
    Make()(40)
}
`,
			want: "42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runResultEverywhere(t, test.source); got != test.want {
				t.Fatalf("program produced %q, want %q", got, test.want)
			}
		})
	}
}

// TestNamedFunctionsAreValuesAcrossNamespaces holds the three ways a function is
// named as a value from another file: an exact alias, a renamed alias, and its
// absolute canonical path.
func TestNamedFunctionsAreValuesAcrossNamespaces(t *testing.T) {
	sources := []compiler.Source{
		{Name: "math/ops.slk", Namespace: "root.math", Text: `
function Add(A: int, B: int) -> int {
    A + B
}

function Multiply(A: int, B: int) -> int {
    A * B
}

function Subtract(A: int, B: int) -> int {
    A - B
}
`},
		{Name: "main.slk", Namespace: "root", Text: `
use root.math.Add

use root.math.Multiply as Times

function Apply(Operation: (int, int) -> int, A: int, B: int) -> int {
    Operation(A, B)
}

function main() -> string {
    let Sum = Apply(Add, 20, 22)
    let Product = Apply(Times, 6, 7)
    let Difference = Apply(root.math.Subtract, 50, 8)
    ` + "`${Sum}:${Product}:${Difference}`" + `
}
`},
	}
	output, diagnostics, err := compiler.Run(sources)
	if err != nil {
		t.Fatalf("run Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if output != "42:42:42" {
		t.Fatalf("program produced %q, want %q", output, "42:42:42")
	}
}

// TestLocalBindingShadowsAFunctionName keeps ordinary lexical scope in charge:
// a local named like a function wins inside its scope.
func TestLocalBindingShadowsAFunctionName(t *testing.T) {
	output := runResultEverywhere(t, `
function Operation(Value: int) -> int {
    Value + 1
}

function main() -> int {
    let Operation = (Value: int) -> int {
        Value + 2
    }
    Operation(40)
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want %q", output, "42")
	}
}

func TestGenericTypesInsideLambdasAreInstantiated(t *testing.T) {
	output := runResultEverywhere(t, `
class Box<T> {
    Value: T
}

function Read<T>(Input: Box<T>) -> T {
    let Unbox = (Value: Box<T>) -> T {
        Value.Value
    }
    Unbox(Input)
}

function main() -> int {
    Read<int>(Box<int> { Value: 42 })
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want 42", output)
	}
}

func TestGenericCallableComponentsKeepTypeParameterScope(t *testing.T) {
	output := runResultEverywhere(t, `
class Box<T> {
    Value: T
}

class Failure<T> implements Error {
    Message: string
}

function Apply<T>(
    Operation: (Box<T>) -> T throws Failure<T>,
    Input: Box<T>,
) -> T throws Failure<T> {
    Operation(Input)
}

function main() -> int {
    let Read = (Value: Box<int>) -> int throws Failure<int> {
        Value.Value
    }
    Apply<int>(Read, Box<int> { Value: 42 }) catch {
        Failure<int> => 0
    }
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want 42", output)
	}
}

func TestLambdaParameterShadowsUsingBinding(t *testing.T) {
	output := runResultEverywhere(t, `
class Handle {
    function Close() -> null {
        null
    }
}

function main() -> int {
    using Resource = Handle {} {
        let Identity = (Resource: int) -> int {
            Resource
        }
        Identity(42)
    }
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want 42", output)
	}
}

func TestLambdaLocalShadowsUsingBinding(t *testing.T) {
	output := runResultEverywhere(t, `
class Handle {
    function Close() -> null {
        null
    }
}

function main() -> int {
    using Resource = Handle {} {
        let Operation = () -> int {
            let Resource = 42
            Resource
        }
        Operation()
    }
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want 42", output)
	}
}

func TestCallableAndLambdaThrowsAcceptGenericErrors(t *testing.T) {
	output := runResultEverywhere(t, `
class Empty<T> implements Error {
    Message: string
}

function Apply(Operation: () -> int throws Empty<int>) -> int throws Empty<int> {
    Operation()
}

function main() -> int {
    let Operation = () -> int throws Empty<int> {
        42
    }
    Apply(Operation) catch {
        Empty<int> => 0
    }
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want 42", output)
	}
}

// TestCallableDiagnostics states the rules as the errors a program gets when it
// breaks them. Diagnostics are the observable behaviour here.
func TestCallableDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		code    string
		message string
	}{
		{
			name: "wrong argument count",
			source: `
function main() -> int {
    let Add = (A: int, B: int) -> int {
        A + B
    }
    Add(1)
}
`,
			code:    "SLK320",
			message: "Add expects 2 arguments, found 1",
		},
		{
			name: "wrong argument type",
			source: `
function main() -> int {
    let Add = (A: int, B: int) -> int {
        A + B
    }
    Add(1, "two")
}
`,
			code:    "SLK320",
			message: "argument 2 to Add must be int, found string",
		},
		{
			name: "calling a value that is not callable",
			source: `
function main() -> int {
    let Value = 1
    Value(2)
}
`,
			code:    "SLK412",
			message: "Value is int and cannot be called",
		},
		{
			name: "calling a nested non-callable result",
			source: `
function Amount() -> int {
    1
}

function main() -> int {
    Amount()(2)
}
`,
			code:    "SLK412",
			message: "cannot be called",
		},
		{
			name: "arguments are still checked after a non-callable target",
			source: `
function main() -> int {
    let Value = 1
    Value(Missing)
}
`,
			code:    "SLK341",
			message: "unknown value Missing",
		},
		{
			name: "parameter arity is invariant",
			source: `
function Apply(Operation: (int, int) -> int) -> int {
    Operation(1, 2)
}

function main() -> int {
    Apply((Value: int) -> int {
        Value
    })
}
`,
			code:    "SLK320",
			message: "argument 1 to Apply must be (int, int) -> int",
		},
		{
			name: "parameter types are invariant",
			source: `
function Apply(Operation: (int) -> int) -> int {
    Operation(1)
}

function main() -> int {
    Apply((Value: float) -> int {
        1
    })
}
`,
			code:    "SLK320",
			message: "argument 1 to Apply must be (int) -> int",
		},
		{
			name: "result types are invariant",
			source: `
function Apply(Operation: (int) -> int) -> int {
    Operation(1)
}

function main() -> int {
    Apply((Value: int) -> float {
        1.0
    })
}
`,
			code:    "SLK320",
			message: "argument 1 to Apply must be (int) -> int",
		},
		{
			name: "a checked effect cannot be erased",
			source: `
class Invalid implements Error {
    Message: string
}

function Apply(Operation: (int) -> int) -> int {
    Operation(1)
}

function main() -> int {
    Apply((Value: int) -> int throws Invalid {
        throw Invalid { Message: "no" }
    })
}
`,
			code:    "SLK320",
			message: "argument 1 to Apply must be (int) -> int",
		},
		{
			name: "an undeclared effect leaves the lambda",
			source: `
class Invalid implements Error {
    Message: string
}

function main() -> int {
    let Operation = (Value: int) -> int {
        throw Invalid { Message: "no" }
    }
    Operation(1)
}
`,
			code:    "SLK201",
			message: "signature does not declare it",
		},
		{
			name: "an invoked effect must be handled",
			source: `
class Invalid implements Error {
    Message: string
}

function main() -> int {
    let Operation = (Value: int) -> int throws Invalid {
        throw Invalid { Message: "no" }
    }
    Operation(1)
}
`,
			code:    "SLK201",
			message: "unhandled Invalid",
		},
		{
			name: "a lambda result must match its declared type",
			source: `
function main() -> string {
    let Operation = () -> string {
        1
    }
    Operation()
}
`,
			code:    "SLK340",
			message: "returns string, but its body produces int",
		},
		{
			name: "a captured binding is read-only",
			source: `
function main() -> int {
    let Total = 1
    let Operation = () -> int {
        Total = 2
        Total
    }
    Operation()
}
`,
			code:    "SLK414",
			message: "captured binding Total is read-only",
		},
		{
			name: "a pending binding cannot be captured",
			source: `
function Compute() -> int {
    1
}

function main() -> int {
    async let Pending = Compute()
    let Operation = () -> int {
        Pending
    }
    let Value = await Pending
    Operation()
}
`,
			code:    "SLK414",
			message: "cannot capture pending binding Pending",
		},
		{
			name: "an active using binding cannot be captured",
			source: `
class Handle {
    Name: string

    function Close() -> null {
        null
    }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Operation = () -> string {
            Resource.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Resource",
		},
		{
			name: "an alias of a using binding cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = Resource
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "an assigned using alias cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = Handle { Name: "two" }
        Alias = Resource
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a branch-assigned using alias cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = Handle { Name: "two" }
        if (true) {
            Alias = Resource
        }
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a shadowed branch assignment still cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = Handle { Name: "two" }
        if (true) {
            Alias = Resource
            let Alias = Handle { Name: "three" }
            Alias.Name
        }
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a branch-selected using alias cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = if (true) {
            Resource
        } else {
            Handle { Name: "two" }
        }
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a match-expression using alias cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

class Failure implements Error {
    Message: string
}

function Load() -> Result<int, Failure> {
    Ok(1)
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let Alias = match Load() {
            Ok(_) => Resource
            Err(_) => Resource
        }
        let Operation = () -> string {
            Alias.Name
        }
        1
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a destructured using alias cannot be captured",
			source: `
class Handle {
    Name: string
    function Close() -> null { null }
}

function main() -> int {
    using Resource = Handle { Name: "one" } {
        let (Alias, Count) = (Resource, 1)
        let Operation = () -> string {
            Alias.Name
        }
        Count
    }
}
`,
			code:    "SLK414",
			message: "cannot capture using binding Alias",
		},
		{
			name: "a lambda cannot call the binding it initializes",
			source: `
function main() -> int {
    let Repeat = (Value: int) -> int {
        Repeat(Value)
    }
    Repeat(1)
}
`,
			code:    "SLK414",
			message: "Repeat is not in scope inside its own initializer",
		},
		{
			name: "a method is not a value",
			source: `
class Record {
    Name: string

    function Describe() -> string {
        self.Name
    }
}

function main() -> string {
    let Held = Record { Name: "one" }
    let Operation = Held.Describe
    "done"
}
`,
			code:    "SLK413",
			message: "Held.Describe is a method and not a value",
		},
		{
			name: "an optional callable must be narrowed",
			source: `
class Holder {
    Transform: ((int) -> int)?
}

function main() -> int {
    let Held = Holder { Transform: null }
    let Operation = Held.Transform
    Operation(1)
}
`,
			code:    "SLK370",
			message: "may be null",
		},
		{
			name: "callables are not comparable",
			source: `
function main() -> bool {
    let Left = (Value: int) -> int {
        Value
    }
    let Right = (Value: int) -> int {
        Value
    }
    Left == Right
}
`,
			code:    "SLK342",
			message: "cannot compare",
		},
		{
			name: "callables cannot be map keys",
			source: `
function main() -> int {
    let Operations = map {
        (Value: int) -> int {
            Value
        }: 1,
    }
    Operations.Length()
}
`,
			code:    "SLK383",
			message: "Map key type must be string, int, or bool",
		},
		{
			name: "a malformed signature is a syntax error",
			source: `
function main() -> int {
    let Operation = (Value) -> int {
        1
    }
    Operation(1)
}
`,
			code:    "SLK001",
			message: "expected ':' and a type after lambda parameter name",
		},
		{
			name: "a lambda cannot start an async let",
			source: `
function main() -> int {
    let Operation = () -> int {
        1
    }
    async let Pending = Operation()
    await Pending
}
`,
			code:    "SLK394",
			message: "async let initializer must resolve to one function or method call",
		},
		{
			name: "a generic function has no single callable type",
			source: `
function main() -> int {
    let Decode = std.json.Decode
    1
}
`,
			code:    "SLK380",
			message: "has no single callable type",
		},
		{
			name: "a private function is not reachable as a value",
			source: `
function main() -> int {
    let Operation = root.other.helper
    1
}
`,
			code:    "SLK330",
			message: "is private to",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := checkCallableSources(test.source)
			assertDiagnostic(t, diagnostics, test.code, test.message)
		})
	}
}

// checkCallableSources compiles main.slk together with a second namespace that
// owns a private declaration, so visibility rules are observable.
func checkCallableSources(source string) []compiler.Diagnostic {
	return compiler.Check([]compiler.Source{
		{Name: "main.slk", Namespace: "root", Text: source},
		{Name: "other/helper.slk", Namespace: "root.other", Text: `
function helper(Value: int) -> int {
    Value
}

function Use() -> int {
    helper(1)
}
`},
	})
}

// TestExistingCallsKeepTheirDirectForm holds the efficiency contract: a direct
// call to a named function still generates a direct Go call, with no callable
// value in between.
func TestExistingCallsKeepTheirDirectForm(t *testing.T) {
	output := runResultEverywhere(t, `
function Add(A: int, B: int) -> int {
    A + B
}

class Record {
    Amount: int

    function Doubled() -> int {
        self.Amount * 2
    }
}

function main() -> int {
    let Held = Record { Amount: 10 }
    Add(Held.Doubled(), 22)
}
`)
	if output != "42" {
		t.Fatalf("program produced %q, want %q", output, "42")
	}
}

// TestCallableFormattingIsCanonical holds the one spelling a callable has in
// source, and that formatting it again changes nothing.
func TestCallableFormattingIsCanonical(t *testing.T) {
	source := `function Apply(Operation: (int, int) -> int, A: int, B: int) -> int {
    Operation(A, B)
}

function main() -> int {
    let SumIt = (A: int, B: int) -> int {
        A + B
    }
    Apply(SumIt, 20, 22)
}
`
	formatted, diagnostics, err := compiler.Format(compiler.Source{Name: "main.slk", Namespace: "root", Text: source})
	if err != nil {
		t.Fatalf("format Slick: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if formatted != source {
		t.Fatalf("formatter produced:\n%s\nwant:\n%s", formatted, source)
	}
}

// TestCallableTypesAreHighlighted keeps a lambda's signature classified: the
// declared types read as types and the arrow as punctuation.
func TestCallableTypesAreHighlighted(t *testing.T) {
	tokens := compiler.Highlight("let SumIt = (A: int, B: int) -> int {\n    A + B\n}\n")
	var arrow, declared bool
	var text strings.Builder
	for _, token := range tokens {
		text.WriteString(token.Text)
		if token.Text == "->" && token.Class == compiler.ClassPunct {
			arrow = true
		}
		if token.Text == "int" && token.Class == compiler.ClassType {
			declared = true
		}
	}
	if !arrow {
		t.Error("-> was not highlighted as punctuation")
	}
	if !declared {
		t.Error("int was not highlighted as a type")
	}
	if text.String() != "let SumIt = (A: int, B: int) -> int {\n    A + B\n}\n" {
		t.Errorf("highlighting did not reproduce the source: %q", text.String())
	}
}

// TestDescribeExposesCallableStructure holds the machine surface: a callable
// parameter reports its parameter types, result, and checked effects as data.
func TestDescribeExposesCallableStructure(t *testing.T) {
	root := writeProject(t, map[string]string{"main.slk": `
class Invalid implements Error {
    Message: string
}

/// Applies one operation.
function Apply(Operation: (string) -> int throws Invalid) -> ((int) -> int) {
    (Value: int) -> int {
        Value
    }
}

function main() -> int {
    let Chosen = Apply((Text: string) -> int throws Invalid {
        throw Invalid { Message: Text }
    })
    Chosen(1)
}
`})
	description, diagnostics, err := compiler.DescribePath("root.Apply", root)
	if err != nil {
		t.Fatalf("describe symbol: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)
	if description.SchemaVersion != compiler.DescriptionSchemaVersion {
		t.Fatalf("schema version is %d, want %d", description.SchemaVersion, compiler.DescriptionSchemaVersion)
	}
	if len(description.Symbol.Parameters) != 1 {
		t.Fatalf("described %d parameters, want 1", len(description.Symbol.Parameters))
	}
	parameter := description.Symbol.Parameters[0]
	if parameter.Callable == nil {
		t.Fatalf("parameter %s has no callable structure", parameter.Name)
	}
	if len(parameter.Callable.ParameterTypes) != 1 || parameter.Callable.ParameterTypes[0] != "string" {
		t.Errorf("callable parameter types are %v, want [string]", parameter.Callable.ParameterTypes)
	}
	if parameter.Callable.ReturnType != "int" {
		t.Errorf("callable return type is %q, want %q", parameter.Callable.ReturnType, "int")
	}
	if len(parameter.Callable.Throws) != 1 || parameter.Callable.Throws[0] != "root.Invalid" {
		t.Errorf("callable throws are %v, want [root.Invalid]", parameter.Callable.Throws)
	}
	if description.Symbol.ReturnCallable == nil || description.Symbol.ReturnCallable.ReturnType != "int" {
		t.Errorf("return callable is %+v, want a callable returning int", description.Symbol.ReturnCallable)
	}
}
