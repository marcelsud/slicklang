# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

## Build and test

- `go build ./...` fails with `error obtaining VCS status` in a git worktree; use `go vet ./...` and `go test ./...` instead, or pass `-buildvcs=false`.
- `go test ./...` builds real native binaries through `BuildPath`, so it needs a working Go toolchain and takes ~30s.

## Adding a standard-library declaration

`internal/compiler/stdlib.go` holds `standardLibraryRegistry`, the single authoritative public Slick surface. A new declaration must be wired through every backend or one of them fails at runtime:

1. registry entry (namespace, function, class, or interface) plus `documentation` on every symbol and field — `undocumentedStandardLibrarySymbols` fails the build otherwise;
2. interpreter case in the matching `callNativeStd*` dispatcher (`stdlib.go`, `stdio.go`, `stdhttp.go`, `stdfs.go`, `stdprocess.go`), reached from `callNativeFunction`;
3. generated-Go case in `emitNativeFunction` (functions) or `emitNativeMethod` in `stdio.go` (methods) — both `default` branches are hard errors;
4. conditional runtime support: a `uses*` flag on `program`, set in `ast_check.go` (parameter and result types, object expressions, call targets) and `visibility.go` (`checkTypeName`), then honoured in `codegen.go` by `emitRuntime`, `emitDeclarations`, and `emitFunctions`.

`std.io`, `std.http`, `std.process`, and the `std.fs` traversal declarations are gated this way; the rest of `std.fs` is emitted unconditionally. Adding a namespace shifts the `std` child count, so the budget pins in `cmd/slick/describe_test.go` need updating.

Native resource classes set `nativeResource` to the Go pointer type of their runtime state, which `emitDeclarations` emits as a `slickResource` field. Object literals of such classes are legal Slick, so their state pointer is nil and every method must survive that.

## Adding a top-level declaration form

`internal/compiler/unions.go` and `internal/compiler/constants.go` are the worked examples (`union`, `const`): one feature file holding parsing, checking, interpretation, and Go emission, wired into the shared dispatchers. Miss one and a single backend disagrees at runtime instead of failing the build:

1. the `program` map, initialized in both `compile` (`compiler.go`) and `parseFormatSource` (`format.go`);
2. the `parseSourceTokens` dispatch and the "expected ..." error listing the forms;
3. name resolution: `canonicalTypeName` (`methods.go`), `checkTypeName` (`visibility.go`), `checkAliases` and `checkDeclaredTypes` (`compiler.go`);
4. both backends: `evalExpression`, `formatRuntimeValue`, and `runtimeEqual` (`runtime.go`) plus `goType`, `resolveDeclaredType`, and `emitDeclarations` (`codegen.go`) — a type the Go backend cannot map silently becomes `any`;
5. the surfaces: `describe.go` with `cmd/slick/describe.go`, `isTopDeclaration` and `collectBreaks` (`format.go`), `highlightKeywords` (`highlight.go`);
6. diagnostics in `diagnostics.go`, whose definitions must stay sorted by code, and an example project pinned in `exampleOutputs`.

## Slick language sharp edges when writing tests and examples

- String interpolation accepts only names and dotted field access: `${Value}` and `${Entry.Name}` work, `${f(x)}` and `${!Flag}` do not. Bind to a `let` first.
- `match` arms take a single expression, never a block. Factor multi-statement arms into a function.
- Arrays expose `.Get(index)` (returns an optional) and `.Length()`; there is no index syntax.
- `?` requires the enclosing function to return `Result`, including inside a `using` initializer.
- Union variants are always qualified by their union or its exact alias, in construction and in patterns; payload bindings are positional, and payload fields are readable only through a match arm.
- `text/scanner` skips newlines, so a declaration whose tail is an expression has no terminator token. `const` bounds its initializer by line: the value must sit on the `const` line, and a formatter or parser change there must keep that rule.
- Example projects are pinned by observable output in `exampleOutputs` (`internal/compiler/build_test.go`) and must be a `slick fmt` fixed point.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
