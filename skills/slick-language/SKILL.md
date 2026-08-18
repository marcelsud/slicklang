---
name: slick-language
description: Write, explain, and repair Slick language source, including declarations, unions, constants, annotations, generics, callables, types, namespaces, optionals, Results, checked errors, authority effects, async work, and resource ownership. Use when creating or editing .slk files or reasoning about Slick compiler behavior.
compatibility: Use with a matching `slick` compiler; inspect installed APIs with `slick describe`.
metadata:
  author: marcelsud
  version: "0.2.0"
---

# Slick language

Write source for the installed compiler, not from memory alone. Read nearby `.slk` files, use `slick describe --json` for unfamiliar language and standard-library symbols, then validate every change with `slick fmt`, `slick check`, and `slick quality --check`.

A block marked `slk program` is a complete source that this repository compiles, formats, and gates in its own test suite. An unmarked `slk` block is a fragment that needs surrounding declarations.

## Source and namespaces

Slick source files end in `.slk`. There is no namespace declaration in source. Directory layout defines canonical namespaces when a project directory is compiled:

```text
project/main.slk                 -> root
project/models/user.slk          -> root.models
project/models/admin/user.slk    -> root.models.admin
```

A single compiled file belongs to `root`. Use fully qualified names across namespaces or introduce an exact alias:

```slk
use root.models.User as User
```

The application entry point is `root.main`.

## Declarations

Top-level syntax supports `use`, `const`, `annotation`, `class`, `interface`, `union`, and `function`.

```slk program
/// Stores a display name.
class User {
    Name: string
    Nickname: string?
    function Display() -> string {
        self.Name
    }
}

interface Named {
    function Display() -> string
}

function greet(User: User) -> string {
    User.Display()
}
```

A block's final expression is its value. Statements do not require semicolons. Parameters and fields use `Name: Type`; functions declare results with `-> Type`.

`///` lines immediately attached to a describable declaration form its documentation. Use ordinary `//` for non-documentation comments. Do not separate an attached doc block from its declaration with a blank line, and do not leave one attached to nothing (`SLK391`).

Names beginning with an uppercase letter are public across namespaces. Lowercase declarations are private to their owning namespace. `main` remains lowercase because it is the language entry point.

`async`, `await`, and `effects` are reserved and cannot name a declaration.

Package-aware projects keep application declarations under `root` and load each dependency under its canonical manifest name, such as `acme.redis`. Import public package declarations with an exact canonical path:

```slk
use acme.redis.Client
```

Slick source never names a Go, Rust, LLVM, Bun, sidecar, or other implementation provider. `slick build` selects the package adapter declared for the whole-program backend and target; changing that backend does not change application source. The supported native backends are `bun`, `go`, `llvm`, and `rust`. The advertised targets are `bun-linux-x64-baseline`, `bun-linux-x64-modern`, `linux-x64`, and `x86_64-unknown-linux-gnu`. Alpha backends and targets require `--allow-alpha`; technical eligibility never promotes them to stable.

## Classes, interfaces, and methods

Methods may be implemented inline, or declared bodyless and completed by a detached implementation:

```slk program
class User {
    Name: string
    function Display() -> string
}

function User.Display() -> string {
    self.Name
}
```

Use an absolute receiver outside the class namespace:

```slk
function root.models.User.Display() -> string {
    self.Name
}
```

A detached implementation must complete a method already declared by the class and must match its parameters, return type, checked errors, and effects exactly. It cannot add a new method.

Class extension policies control where detached implementations may live:

```slk program
class Local extension(none) {}

class Package extension(namespace) {}

class Open extension(global) {}
```

`extension(namespace)` is the default. Global implementations still require a public method.

Classes declare conformance with `implements`:

```slk program
class ParseError implements Error {
    Message: string
}
```

Interfaces contain method declarations without bodies. A value passed to an interface-typed parameter must satisfy the complete method contract. Interfaces are how an application inverts authority: declare a port as an interface, and the implementation carries the effects.

A class that reaches itself by value, such as `class Node { Next: Node? }`, checks and interprets but fails native build with `invalid recursive type`. Recurse through an array or `Map` instead.

## Types and values

Core types:

- `bool`
- `bytes`
- `float`
- `int`
- `null`
- `string`
- `Error`
- `Iterable<T>`
- `Map<K, V>`
- `Result<T, E>`

Structural type forms:

```slk
string[]                  // array
User?                     // optional User
(int, string)             // tuple
(int, int) -> int         // callable
Result<User, LookupError> // success or failure value
Map<string, int>          // immutable ordered map
```

`T?` has exactly two states: a `T` value or `null`. There is no `undefined`, implicit truthiness, or repeated optional marker such as `T??`.

Arrays are homogeneous. Map keys must be `string`, `int`, or `bool`. Generic arity and nested type shapes are checked exactly.

`->` binds more weakly than postfix `?` and `[]`, so parenthesize a callable that is itself optional, an array element, or a result type: `((int) -> int)[]` and `-> ((int) -> int)`. A callable type spells checked errors before effects: `(string) -> int throws Invalid effects { io }`.

Common literals and expressions:

```slk
true
false
null
42
-3.5
"plain text"
`interpolated ${Value}`
["Ada", "Grace"]
(1, "Ada")
User { Name: "Ada" }
map { "Ada": 37 "Grace": 36 }
```

Object construction must provide every required field. Optional fields may be omitted and then contain `null`.

Interpolation accepts a name or dotted field access only. `${Value}` and `${Entry.Name}` work; `${f(x)}` and `${!Flag}` do not. Bind the value with `let` first.

## Operators

The source operators are `+`, `-`, `*`, `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `||`, unary `-`, and unary `!`.

There is no division operator and no remainder operator: a zero divisor is a checked failure, so use `std.math.Divide` and `std.math.Remainder`, which return `Result<int, std.math.ArithmeticFailure>`. `+` accepts matching numeric types or strings, and there are no implicit conversions; use `std.convert` to parse. Inspect both namespaces with `slick describe`.

## Bindings and control flow

`let` introduces an inferred local. Assignment preserves the local's original storage type:

```slk
let Total = 0
Total = Total + 1
```

Destructure a tuple by binding each position, and use `_` for a slot you intend to discard:

```slk
let (Prefix, Suffix) = Parts
let (_, Wanted) = Parts
```

`if` is an expression. Its condition must be `bool`, and value-producing branches must share a compatible type. Chain alternatives with `else if` rather than nesting, which keeps cognitive complexity flat:

```slk
let Label = if (Value == 0) {
    "zero"
} else if (Value < 0) {
    "negative"
} else {
    "positive"
}
```

A half-open range excludes its end:

```slk
for Index in 0..3 {
    // Index is 0, 1, then 2.
}
```

A literal lower bound needs a space before `..` when the upper bound is a name, because `0..` alone scans as a float: write `for Value in 0 .. Limit`.

Arrays, maps, ranges, and other `Iterable` values work with `for`. Maps and tuple-producing iterables can bind multiple names:

```slk
for Name, Age in Ages {
    Output = Output + `${Name}=${Age};`
}

for Index, Name in enumerate(Names) {
    if (Name == "skip") {
        continue
    }
    if (Index == 3) {
        break
    }
}
```

`enumerate` and `zip` are the iterable builtins; `zip` requires at least two iterable arguments. `break` and `continue` are valid only inside a loop. `return Expression` exits the enclosing function explicitly; otherwise the block's final expression is returned. A statement written after `return`, `throw`, `break`, or `continue` in the same block is dead source (`SLK502`).

## Authority effects

Every callable declares the observable authority it uses, with the narrowest set that covers its body, its callees, its callbacks, and automatic resource cleanup:

```slk program
function Save(Path: string, Body: string) -> Result<null, std.fs.Failure> effects { filesystem } {
    std.fs.WriteText(Path, Body)?
    Ok(null)
}
```

The effects are `database`, `environment`, `filesystem`, `io`, `network`, `process`, `random`, `state`, and `time`. Each name may appear once, and the clause must not be empty. Pure computation declares nothing. An undeclared effect is `SLK207`, so a function that opens a file, prints, reads the clock, or pushes into a `std.buffer` will not compile until its clause says so.

Effects are part of a signature: they must match on detached implementations and interface conformance, and they appear in callable types. Placing an authority behind an interface is how an application keeps a caller pure.

## Optional values

Never access a field or method through `T?` directly. Bind it to a simple local and compare that local with `null`; the compiler narrows only the branch that proves presence:

```slk
function display(Found: User?) -> string {
    if (Found == null) {
        "missing"
    } else {
        Found.Name
    }
}
```

Narrowing is branch-local. Assignment clears a prior narrowing. Field paths do not narrow directly, so bind an optional field first:

```slk
let Nickname = Person.Nickname
if (Nickname != null) {
    Nickname
} else {
    Person.Name
}
```

Use optionals for absence, not for checked failures.

## Result values

`Result<T, E>` is ordinary value flow. Construct it with contextual `Ok(value)` and `Err(error)` expressions:

```slk
function parse(Text: string) -> Result<int, ParseError> {
    if (Text == "") {
        Err(ParseError {
            Message: "empty"
        })
    } else {
        Ok(1)
    }
}
```

Read a Result with exhaustive `match`:

```slk
match parse(Text) {
    Ok(Value) => `${Value}`
    Err(Problem) => Problem.Message
}
```

Supported patterns are `Ok(Name)`, `Err(Name)`, `Ok(_)`, `Err(_)`, and `_`. Handle both variants or use `_`; do not duplicate or place arms after a catch-all. Every reachable arm must produce one common type, and an arm takes a single expression, never a block: factor a multi-statement arm into a function.

Postfix `?` unwraps `Ok` or returns the same `Err` from the enclosing function:

```slk
function relay(Text: string) -> Result<int, ParseError> {
    let Value = parse(Text)?
    Ok(Value + 1)
}
```

The operand and enclosing function must both be Results with exactly matching error types. `?` is not `throw`, and `catch` does not intercept an `Err`. When a binding exists only to propagate, drop it and keep the expression: write `parse(Text)?` rather than `let Ignored = parse(Text)?` (`SLK500`).

`unwrap` turns a Result into its success payload and throws the failure, which requires the enclosing function to declare that error:

```slk
function main() -> int throws ParseError {
    unwrap(parse("7"))
}
```

## Checked errors

Checked errors use `Error`, `throws`, `throw`, and `catch`:

```slk program
class ReadError implements Error {
    Message: string
}

function read() -> string throws ReadError {
    throw ReadError {
        Message: "failed"
    }
}

function main() -> string {
    read()
    catch (Problem) {
        ReadError => Problem.Message
    }
}
```

A thrown value must implement `Error`. A function must catch each error produced by its body or list it in `throws`. Catch arms must exhaust the protected expression's checked error set. An arm may bind the caught value itself with `as`:

```slk
Operation(Text) catch {
    Invalid as Failure => Failure.Message
}
```

Keep checked errors and `Result` failures separate: `Result` adds nothing to a `throws` set, an `Err` is a value rather than a thrown error, and `?` returns instead of throwing. Reach for `throws` when a failure should interrupt the caller by default, and for `Result` when the caller should have to spell the failure out.

## Unions

A `union` declares a closed set of variants, with or without payload fields:

```slk program
/// Method is the closed set of verbs this service accepts.
union Method {
    Get
    Post
    Delete
}

/// Expression is a small tree.
union Expression {
    Number(Value: int)
    Add(Left: Expression, Right: Expression)
    Missing
}
```

Match a union exhaustively. Variants are always qualified by their union or its exact alias, payload bindings are positional, and payload fields are readable only through an arm:

```slk
function Render(Node: Expression) -> string {
    match Node {
        Expression.Number(Value) => `${Value}`
        Expression.Add(Left, Right) => RenderPair(Left, Right)
        Expression.Missing => "?"
    }
}
```

Adding a variant fails compilation everywhere the union is matched, which is the point: the compiler lists the phases still to update.

## Constants

`const` declares one typed value at namespace scope, evaluated once during compilation:

```slk program
const RetryBudget: int = 3 * 2

const SupportsColor: bool = !false && (RetryBudget > 4 || false)
```

A constant may refer to another constant, including one declared later, as long as the references stay acyclic. A fieldless union variant is a constant expression too. The initializer must sit on the `const` line.

## Generics

A generic declaration binds its own type parameters and is used at explicit concrete type arguments:

```slk program
class Box<T> {
    Value: T
    function Get() -> T {
        self.Value
    }
}

function Identity<T>(Value: T) -> T {
    Value
}

function main() -> string {
    let Number = Box<int> {
        Value: 42
    }
    let Held = Number.Get()
    Identity<string>(`${Held}`)
}
```

`Box<int>` and `Box<string>` are distinct types. Substitution reaches through optionals, arrays, tuples, `Map`, `Result`, checked errors, and effects. A method on a generic class is implemented with the class's parameters, `function Store<V>.Lookup(...)`, and never declares its own. Interfaces may be generic too.

## Callables and lambdas

A callable value has a callable type, and a named function already is one:

```slk program
function Apply(Operation: (int, int) -> int, A: int, B: int) -> int {
    Operation(A, B)
}

function Add(A: int, B: int) -> int {
    A + B
}

function main() -> int {
    let SumIt = (A: int, B: int) -> int {
        A + B
    }
    Apply(SumIt, 20, 22) + Apply(Add, 0, 0)
}
```

A lambda is an expression bound with `let`, so every parameter and result type is explicit. Captures are copied by value when the lambda is created and are read-only: assigning a captured binding is an error, and a later write to the original is not observed. A callable is neither comparable nor a `Map` key, and a lambda cannot refer to the binding it initializes; use a named function for recursion. A lambda's checked errors and effects belong to its type, so its caller handles them exactly as it would for a named function.

## Resource ownership

Use `using` for deterministic lexical cleanup:

```slk program
function ReadHead(Data: bytes) -> Result<bytes?, std.io.Failure> throws std.io.Failure effects { io } {
    using Reader = std.io.ReaderFromBytes(Data) {
        Reader.Read(4096)
    }
}
```

A managed type must expose an accessible `Close()` method that takes no arguments and returns `null`. The `using` binding is immutable, cannot escape its scope, and must not be closed manually. Cleanup runs automatically, so both its effects and the errors it throws belong to the enclosing declaration: the example above declares `throws std.io.Failure` for `Reader.Close`, and `Read` returns `bytes?` because `null` means end-of-stream. Standard I/O readers and writers, SQLite databases, and transactions all require a `using` scope. A `?` inside a `using` initializer requires the enclosing function to return a `Result`.

## Concurrency

`async let` starts a call, and `await` consumes its pending binding exactly once:

```slk program
function Both(Left: string, Right: string) -> Result<string, std.http.Failure> effects { network } {
    async let First = std.http.Fetch(std.http.Request {
        Method: "GET"
        URL: Left
    })
    async let Second = std.http.Fetch(std.http.Request {
        Method: "GET"
        URL: Right
    })
    let A = await First?
    let B = await Second?
    Ok(`${A.Status}:${B.Status}`)
}
```

A pending binding must be awaited on every path, cannot be awaited twice, and cannot cross a loop boundary. Values shared with async work must be safe to copy.

## Annotations

An annotation attaches compiler-owned metadata to a declaration. `@std.json.Name` renames a field on the JSON boundary for the interpreter and every native backend:

```slk program
class Todo {
    @std.json.Name("id")
    Id: int
    @std.json.Name("title")
    Title: string
}
```

Declare a project-local alias with `annotation` when a name should read in domain terms. Inspect an annotation with `slick describe` before applying it: targets, arity, and repeatability are part of its contract.

## Collections

Maps are immutable and preserve insertion order. Empty maps require an expected `Map<K, V>` type:

```slk
let Ages = map { "Ada": 37 }
let Updated = Ages.With("Grace", 36)
let Removed = Updated.Without("Ada")
let Ada = Ages.Get("Ada")
let Known = Ages.Contains("Ada")
let Count = Ages.Length()
```

Arrays expose `Length()`, `Get(Index)` returning an optional, and `Slice(Start, End)` returning a `Result`. There is no index syntax:

```slk
let Names = ["Ada", "Grace"]
let First = Names.Get(0)
let Total = Names.Length()
```

Build a sequence with `std.buffer`, which mutates caller-observable state and therefore requires the `state` effect:

```slk program
function Squares(Limit: int) -> int[] effects { state } {
    let Items = std.buffer.New<int>()
    for Value in 0 .. Limit {
        std.buffer.Push<int>(Items, Value * Value)
    }
    std.buffer.Freeze<int>(Items)
}
```

## Standard library

The compiler owns `std.buffer`, `std.bytes`, `std.collections`, `std.convert`, `std.env`, `std.fs`, `std.http`, `std.http.server`, `std.io`, `std.json`, `std.math`, `std.path`, `std.process`, `std.sqlite`, `std.text`, `std.unicode`, and `std.utf8`. Do not guess names, arity, optional results, resource rules, failure types, or effects. Discover them from the installed compiler:

Blocking standard-library calls inherit the current HTTP-handler or async-task cancellation scope in both the interpreter and native binaries. Cancellation returns the module's typed failure: `std.http.Failure.Kind` and `std.process.Failure.Operation` are `Cancelled`; `std.process.Run` signals and reaps its child; a cancelled `std.fs.WriteText` may leave the target truncated or partially written. Whole-file filesystem calls accept regular files and named pipes and reject other non-regular inputs.

```bash
slick describe std
slick describe --json std.fs.ReadText
slick describe --json std.json.Decode
```

Use canonical names returned by `describe`.

## Required workflow

1. Read the target file and neighboring declarations that establish its namespace and contracts.
2. Inspect unfamiliar types, methods, diagnostics, and standard-library APIs with `slick describe --json`.
3. Reuse existing source patterns; do not add syntax or implicit behavior absent from the language.
4. Make the smallest complete change.
5. Run `slick fmt <path>`.
6. Run `slick check <path>`.
7. For each `SLKxxx` diagnostic, run `slick describe --json SLKxxx`, repair the violated rule, and re-check.
8. Run `slick quality --check <path>` and repair every warning; there is no suppression.
9. Run the program when output matters. Also build and execute a native binary when native behavior is part of the task.
