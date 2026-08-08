---
name: slick-language
description: Write, explain, and repair Slick language source, including declarations, types, namespaces, optionals, Results, checked errors, methods, visibility, loops, maps, and resource ownership. Use when creating or editing .slk files or reasoning about Slick compiler behavior.
compatibility: Use with a matching `slick` compiler; inspect installed APIs with `slick describe`.
metadata:
  author: marcelsud
  version: "0.1.0"
---

# Slick language

Write source for the installed compiler, not from memory alone. Read nearby `.slk` files, use `slick describe --json` for unfamiliar language and standard-library symbols, then validate every change with `slick fmt` and `slick check`.

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

Top-level syntax supports `use`, `class`, `interface`, and `function` declarations.

```slk
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

`///` lines immediately attached to a describable declaration form its documentation. Use ordinary `//` for non-documentation comments. Do not separate an attached doc block from its declaration with a blank line.

Names beginning with an uppercase letter are public across namespaces. Lowercase declarations are private to their owning namespace. `main` remains lowercase because it is the language entry point.

## Classes, interfaces, and methods

Methods may be implemented inline, or declared bodyless and completed by a detached implementation:

```slk
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

A detached implementation must complete a method already declared by the class and must match its parameters, return type, and checked effects exactly. It cannot add a new method.

Class extension policies control where detached implementations may live:

```slk
class Local extension(none) {}
class Package extension(namespace) {}
class Open extension(global) {}
```

`extension(namespace)` is the default. Global implementations still require a public method.

Classes declare conformance with `implements`:

```slk
class ParseError implements Error {
    Message: string
}
```

Interfaces contain method declarations without bodies. A value passed to an interface-typed parameter must satisfy the complete method contract.

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
Result<User, LookupError> // success or failure value
Map<string, int>          // immutable ordered map
```

`T?` has exactly two states: a `T` value or `null`. There is no `undefined`, implicit truthiness, or repeated optional marker such as `T??`.

Arrays are homogeneous. Map keys must be `string`, `int`, or `bool`. Generic arity and nested type shapes are checked exactly.

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
User { Name: "Ada" }
map { "Ada": 37 "Grace": 36 }
```

Object construction must provide every required field. Optional fields may be omitted and then contain `null`.

The source operators are `+`, `==`, and `!=`. `+` accepts matching numeric types or strings. Do not invent implicit conversions; use the appropriate `std.convert` API after inspecting it with `slick describe`.

## Bindings and control flow

`let` introduces an inferred local. Assignment preserves the local's original storage type:

```slk
let Total = 0
Total = Total + 1
```

`if` is an expression. Its condition must be `bool`, and value-producing branches must share a compatible type:

```slk
let Label = if (Ready) { "ready" } else { "waiting" }
```

A half-open range excludes its end:

```slk
for Index in 0..3 {
    // Index is 0, 1, then 2.
}
```

Arrays, maps, ranges, and other `Iterable` values work with `for`. Maps and tuple-producing iterables can bind multiple names:

```slk
for Name, Age in Ages {
    Output = Output + `${Name}=${Age};`
}

for Index, Name in enumerate(Names) {
    if (Name == "skip") { continue }
    if (Index == 3) { break }
}
```

`zip` requires at least two iterable arguments. `break` and `continue` are valid only inside a loop. `return Expression` exits the enclosing function explicitly; otherwise the block's final expression is returned.

## Optional values

Never access a field or method through `T?` directly. Bind it to a simple local and compare that local with `null`; the compiler narrows only the branch that proves presence:

```slk
function display(User: User?) -> string {
    if (User == null) {
        "missing"
    } else {
        User.Name
    }
}
```

Narrowing is branch-local. Assignment clears a prior narrowing. Field paths do not narrow directly, so bind an optional field first:

```slk
let Nickname = User.Nickname
if (Nickname != null) {
    Nickname
} else {
    User.Name
}
```

Use optionals for absence, not for checked failures.

## Result values

`Result<T, E>` is ordinary value flow. Construct it with contextual `Ok(value)` and `Err(error)` expressions:

```slk
function parse(Text: string) -> Result<int, ParseError> {
    if (Text == "") {
        Err(ParseError { Message: "empty" })
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

Supported patterns are `Ok(Name)`, `Err(Name)`, `Ok(_)`, `Err(_)`, and `_`. Handle both variants or use `_`; do not duplicate or place arms after a catch-all. Every reachable arm must produce one common type.

Postfix `?` unwraps `Ok` or returns the same `Err` from the enclosing function:

```slk
function relay(Text: string) -> Result<int, ParseError> {
    let Value = parse(Text)?
    Ok(Value + 1)
}
```

The operand and enclosing function must both be Results with exactly matching error types. `?` is not `throw`, and `catch` does not intercept an `Err`.

## Checked errors

Checked effects use `Error`, `throws`, `throw`, and `catch`:

```slk
class ReadError implements Error {
    Message: string
}

function read() -> string throws ReadError {
    throw ReadError { Message: "failed" }
}

function main() -> string {
    read()
    catch (Problem) {
        ReadError => Problem.Message
    }
}
```

A thrown value must implement `Error`. A function must catch each error produced by its body or list it in `throws`. Catch arms must exhaust the protected expression's checked error set. Keep checked effects separate from `Result` failures.

## Resource ownership

Use `using` for deterministic lexical cleanup:

```slk
let Text = using Reader = std.io.ReaderFromBytes(Data) {
    Reader.Read(4096)
}
```

A managed type must expose an accessible `Close()` method that takes no arguments and returns `null`. The using binding is immutable, cannot escape its scope, and must not be closed manually. Standard I/O readers and writers require a using scope.

## Maps

Map literals infer their key and value types from entries. Empty maps require an expected `Map<K, V>` type. Maps preserve insertion order and update immutably:

```slk
let Ages = map { "Ada": 37 }
let Updated = Ages.With("Grace", 36)
let Removed = Updated.Without("Ada")
let Ada = Ages.Get("Ada")
```

Inspect `Map` with `slick describe Map` for its current method contract.

## Standard library

The compiler owns `std.bytes`, `std.convert`, `std.env`, `std.fs`, `std.io`, `std.json`, `std.path`, and `std.text`. Do not guess names, arity, optional results, resource rules, or failure types. Discover them from the installed compiler:

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
8. Run the program when output matters. Also build and execute a native binary when native behavior is part of the task.
