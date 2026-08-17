# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

## Build and test

- `go build ./...` fails with `error obtaining VCS status` in a git worktree; use `go vet ./...` and `go test ./...` instead, or pass `-buildvcs=false`.
- `go test ./...` builds real native binaries through `BuildPath`, so it needs a working Go toolchain and takes ~30s.

## Core IR

`internal/compiler/core_ir.go` is the typed, backend-neutral contract between checking and native emission. `BuildSourcesWithOptions` lowers every valid program before touching the output path; an unclassified or untyped node is therefore a lowering error, not a backend fallback. Call targets use resolved declaration or standard-operation IDs, and native resource state remains outside the IR. A new statement or expression node must be added to the Core lowerer; `TestCoreIRClassifiesEveryASTNode` is the completeness gate.

## Backend drivers

`internal/compiler/backend.go` is the authoritative backend, target, artifact, toolchain, capability, operation-support, and driver registry. `backend_driver.go` owns the fixed validate → emit → build → verify → atomic-install sequence and the compiler-owned workspace. Target and toolchain failures happen before workspace creation; drivers write only a candidate path inside that workspace. `BuildPath` remains the stable-Go compatibility entry point, while `BuildOptions.Target` selects only targets advertised by the chosen driver.

## Adding a standard-library declaration

`internal/compiler/stdlib.go` holds `standardLibraryRegistry`, the single authoritative public Slick surface. A new declaration must be wired through every backend or one of them fails at runtime:

1. registry entry (namespace, function, class, or interface) plus `documentation` on every symbol and field and the narrowest `effects` set on every authority-using or mutating function/method — undocumented symbols and undeclared transitive effects fail the build;
2. interpreter case in the matching `callNativeStd*` dispatcher (`stdlib.go`, `stdio.go`, `stdhttp.go`, `stdhttpserver.go`, `stdfs.go`, `stdprocess.go`, `stdsqlite.go`), reached from `callNativeFunction`;
3. generated-Go case in `emitNativeFunction` (functions) or `emitNativeMethod` in `stdio.go` (methods) — both `default` branches are hard errors;
4. conditional runtime support: a `uses*` flag on `program`, set in `ast_check.go` (parameter and result types, object expressions, call targets) and `visibility.go` (`checkTypeName`), then honoured in `codegen.go` by `emitRuntime`, `emitDeclarations`, and `emitFunctions`.

`std.io`, `std.http`, `std.http.server`, `std.process`, `std.sqlite`, and the `std.fs` traversal declarations are gated this way; the rest of `std.fs` is emitted unconditionally. Client `std.http` and inbound `std.http.server` are separate flags — check the more specific `std.http.server` prefix first (`markUsesStdHTTP` / `skipStdHTTP` in `stdhttpserver.go`). Nested namespaces (`std.http.server`) appear as children of their parent in `slick describe`, so only a new top-level `std.*` child shifts the budget pins in `cmd/slick/describe_test.go`.

Native resource classes set `nativeResource` to the Go pointer type of their runtime state, which `emitDeclarations` emits as a `slickResource` field. Object literals of such classes are legal Slick, so their state pointer is nil and every method must survive that.

## Adding a top-level declaration form

`internal/compiler/unions.go` and `internal/compiler/constants.go` are the worked examples (`union`, `const`): one feature file holding parsing, checking, interpretation, and Go emission, wired into the shared dispatchers. Miss one and a single backend disagrees at runtime instead of failing the build:

1. the `program` map, initialized in both `compile` (`compiler.go`) and `parseFormatSource` (`format.go`);
2. the `parseSourceTokens` dispatch and the "expected ..." error listing the forms;
3. name resolution: `canonicalTypeName` (`methods.go`), `checkTypeName` (`visibility.go`), `checkAliases` and `checkDeclaredTypes` (`compiler.go`);
4. backend-neutral lowering in `core_ir.go`, then both backends: `evalExpression`, `formatRuntimeValue`, and `runtimeEqual` (`runtime.go`) plus `goType`, `resolveDeclaredType`, and `emitDeclarations` (`codegen.go`) — a type the Go backend cannot map silently becomes `any`;
5. the surfaces: `describe.go` with `cmd/slick/describe.go`, `isTopDeclaration` and `collectBreaks` (`format.go`), `highlightKeywords` (`highlight.go`);
6. diagnostics in `diagnostics.go`, whose definitions must stay sorted by code, and an example project pinned in `exampleOutputs`.

## Adding a lint or quality rule

`slick lint` and `slick quality` report warnings about programs that already
compile. `internal/compiler/lint.go` owns the three lint rules, `complexity.go`
the two complexity metrics, and `quality.go` the aggregation both the gate and
`cmd/slick/quality.go` render from; neither command compiles a project twice,
because quality calls the private `p.lint()` rather than public `Lint`.

- A new code goes in `diagnosticDefinitions` sorted by code, in phase `lint` or
  `quality` and marked `asWarning()`. The registry rejects either half alone:
  those two phases are warnings by construction, every other phase is an error.
- Both walkers iterate `p.authoredCallables()`, which skips natives and
  monomorphized clones, so a mistake in a generic is reported once.
- A new statement or expression node must be classified in `complexity.go`, or
  `TestComplexityWalkerClassifiesEveryASTNode` fails and the walker's fallback
  turns the analysis into an error rather than a silent zero.
- Per-callable measurement needs the source extent, so `callableTail.end` and
  `blockNode.end` carry the closing brace the parser matched.
- Limits are fixed constants with no configuration, baseline, or suppression:
  every example project must pass `slick quality --check`.

## The published agent skills are tested, not prose

`skills/slick-cli` and `skills/slick-language`, shipped with `plugin.json`, are
what an agent reads instead of this codebase, so `cmd/slick/skills_test.go` holds
them to the toolchain: every fenced block marked ```` ```slk program ```` must
compile, format, and pass `slick quality --check`; every `SLKxxx` cited must be
registered; the command table and the `reportUsageTo` usage text must name the
same commands; and the standard-library sentence must list every `std` child
`slick describe std` reports.

A new command, `std` namespace, authority effect, or operator therefore updates a
skill in the same change. Unmarked ```` ```slk ```` blocks are fragments and are
not compiled, so prefer marking an example complete when it can stand alone.

## User-defined generics

`internal/compiler/generics.go` monomorphizes them: an open declaration lives only in `program.genericClasses`, `genericInterfaces`, `genericFunctions`, or `genericMethodImpls`, and every concrete instantiation a program mentions is registered in the ordinary maps under its canonical name (`root.Box<int>`). Downstream code therefore needs no generic awareness — `goEncodedName` already hex-encodes the whole name, and `p.classes[...]` lookups find instances for free. Two consequences worth knowing before touching this:

- A declaration that must see both forms uses `classDeclaration`, `interfaceDeclaration`, or `functionDeclaration`; anything iterating `p.classes` for output must skip `instanceOf != ""`.
- Instance-derived checks run inside `checkingInstance`, which deduplicates a diagnostic already reported, so one mistake in a generic is reported once instead of once per instantiation.

A class that reaches itself by value — `Node { Next: Node? }`, generic or not — checks and interprets but fails `go build` with `invalid recursive type`. That predates generics; recursion through an array or Map compiles.

## Adding a compiler-owned annotation terminal

`internal/compiler/annotations.go` owns annotation parsing, alias expansion, typed argument resolution, target validation, and ordered hook application. A framework terminal supplies one canonical name, canonical parameter types, allowed targets, repeatability, documentation, and an `apply` callback through `compileWithTerminals`; the callback mutates the already-parsed declaration once, before either backend checks it. Keep backend behavior in that shared mutation rather than adding interpreter and generated-Go cases. Generic cloning in `generics.go` must preserve annotation metadata so each concrete method receives the same hook, and instance diagnostics must run through `checkingInstance`. Allowed targets are class, method, parameter, function, and field. `@std.json.Name` is the field-targeted terminal; it writes `fieldDecl.jsonName` for both JSON backends.

Annotations are part of the machine-readable description contract. Changing their shape requires a `DescriptionSchemaVersion` bump and the exact JSON/budget pins in `cmd/slick`.

## Adding an expression form

`internal/compiler/callables.go` is the worked example (lambdas and callable values). An expression node has to reach every dispatcher or one backend disagrees at runtime: `parsePrimary` and the node type (`ast.go`), `checkASTExpressionExpecting` and `expressionLabel` (`ast_check.go`), `coreLowerer.expression` (`core_ir.go`), `evalExpression` (`runtime.go`), `expression` and `expressionType` (`codegen.go`), and `collectExpression` (`format.go`). A node that caches a resolved type must return it unchanged on a second visit, because `codegen.go`'s `expressionType` re-runs the checker over sub-expressions.

## Types are strings, and every scanner over them shares one grammar

A Slick type is its canonical spelling; `types.go` owns the decomposition and `parseTypeTokens` (`compiler.go`) is the one parser that builds it, shared by declarations and call type arguments. Three rules keep the spelling unique:

- `->` collides with the `<`/`>` scan, so every depth-tracking helper skips it through `isTypeArrow`, and tokens use `matchingAngle` rather than `matching(..., "<", ">")`.
- `->` binds more weakly than postfix `?` and `[]`, so build those types with `optionalOf` and `arrayOf`, never by appending the suffix. Parentheses that only group a callable are normalized away by `ungroupType`, and `goType`/`resolveDeclaredType` ungroup before reading a spelling.
- A callable's throw and operation-effect sets are sorted by `callableType`, because both originate as maps and generation must be deterministic. Callable contracts spell checked errors before authority effects: `throws Failure effects { filesystem }`.

## Slick language sharp edges when writing tests and examples

- String interpolation accepts only names and dotted field access: `${Value}` and `${Entry.Name}` work, `${f(x)}` and `${!Flag}` do not. Bind to a `let` first.
- `match` arms take a single expression, never a block. Factor multi-statement arms into a function.
- Arrays expose `.Get(index)` (returns an optional) and `.Length()`; there is no index syntax.
- `?` requires the enclosing function to return `Result`, including inside a `using` initializer.
- Union variants are always qualified by their union or its exact alias, in construction and in patterns; payload bindings are positional, and payload fields are readable only through a match arm.
- `text/scanner` skips newlines, so a declaration whose tail is an expression has no terminator token. `const` bounds its initializer by line: the value must sit on the `const` line, and a formatter or parser change there must keep that rule. `parsePostfix` refuses to read a lambda's `(` as an argument list for the same reason.
- A lambda is an expression: `let Name = (A: int) -> int { ... }`. Every parameter and result type is explicit, captures are by value and read-only, and a callable is neither comparable nor a Map key.
- Example projects are pinned by observable output in `exampleOutputs` (`internal/compiler/build_test.go`) and must be a `slick fmt` fixed point.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
