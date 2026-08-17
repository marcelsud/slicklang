---
name: slick-cli
description: Use the Slick CLI to format, check, lint, gate quality, run, build, inspect symbols, and explain compiler diagnostics. Use when operating Slick projects, validating .slk changes, diagnosing SLK codes, scripting describe output, or choosing the correct Slick command.
compatibility: Requires the `slick` executable on PATH.
metadata:
  author: marcelsud
  version: "0.2.0"
---

# Slick CLI

Use the CLI as the source of truth for the installed compiler. Prefer exact commands and machine-readable `describe --json` output over guessing language or standard-library behavior.

## Choose the command

| Goal | Command |
| --- | --- |
| Check one file or project | `slick check [path] [--allow-alpha]` |
| Report dead source in a valid program | `slick lint [path]` |
| Report formatting, validity, lint, and complexity together | `slick quality [path]` |
| Enforce that report as a gate | `slick quality --check [path]` |
| Run `root.main` with the interpreter | `slick run [path] [arguments...]` |
| Build a standalone native executable | `slick build [path] -o <output> [--backend=bun\|go\|llvm\|rust] [--target=<target>] [--allow-alpha]` |
| Format one file or project in place | `slick fmt [path]` |
| Verify formatting without writing | `slick fmt --check [path]` |
| Describe a language, standard-library, or project symbol | `slick describe [--json] [--budget <lines>] <symbol> [path]` |
| Explain a compiler diagnostic | `slick describe [--json] SLKxxx` |

The default path is `.` for every command. `build` always requires `-o` or `--output`. The default backend is Go; pass `--backend=bun`, `--backend=llvm`, `--backend=rust`, or `--backend=go` to select a native backend explicitly. Omit `--target` to use that driver's default advertised target; an explicit target must be registered for the selected backend. Alpha declarations, backends, and targets require `--allow-alpha`; technical eligibility never promotes them to stable. `--check` may appear before or after the path for both `fmt` and `quality`. Everything after the project path in `slick run` belongs to the program, not the toolchain.

LLVM's `linux-x64` target uses the `x86_64-pc-linux-gnu` triple and requires a Linux/amd64 host, LLVM 18 (`llvm-as-18` and `llc-18`), and a C compiler. HTTP, JSON, and SQLite programs additionally require the libcurl, Jansson, and SQLite development libraries, respectively; native families are linked only when the checked program uses them. Set `SLICK_LLVM_BIN` when the LLVM tools are outside `PATH`.

Rust is alpha and requires `--allow-alpha`, Rust/Cargo 1.93.1, and the `x86_64-unknown-linux-gnu` target. It currently builds allocation-free Core programs over primitives, tuples, static functions, branches, integer ranges, and direct returns. Managed values, callable values, checked failures, cleanup, tasks, and standard-library operations fail with a source-located Rust lowering error rather than falling back to another backend.

Bun is alpha and requires `--allow-alpha` and Bun 1.3.14. Its Linux/x64 targets are `bun-linux-x64-modern` (default) and `bun-linux-x64-baseline`; choose baseline for older CPUs. Like Rust, Bun currently accepts only the allocation-free primitive Core subset and reports unsupported managed/runtime behavior before emitting JavaScript or invoking Bun.

## Build a package project

A directory containing `slick.project.json` is a package-aware project. Application source imports one canonical package interface, never a backend implementation:

```slk
use acme.redis.Client
```

The project manifest is strict JSON. Dependencies are local, exact-version package roots and must be sorted by canonical name:

```json
{
  "schema_version": 1,
  "name": "example.application",
  "source": "src",
  "dependencies": [
    {"name": "acme.redis", "version": "2.1.0", "path": "packages/redis"}
  ]
}
```

Each dependency root contains `slick.package.json`. Its canonical interface fixes the public source, effect/resource contract, conformance suite, and hashes. Every adapter names an exact backend, sorted targets, explicit stability, implementation entry, dependencies, assets, ABI, and hashes:

```json
{
  "schema_version": 1,
  "name": "acme.redis",
  "version": "2.1.0",
  "stability": "stable",
  "interface": {
    "path": "interface",
    "sha256": "<64 lowercase hex digits>",
    "effects": ["network"],
    "resources": [],
    "conformance_path": "conformance",
    "conformance_sha256": "<64 lowercase hex digits>"
  },
  "adapters": [
    {
      "id": "go-linux",
      "backend": "go",
      "targets": ["linux-x64"],
      "stability": "stable",
      "kind": "slick",
      "entry": "adapters/go",
      "dependencies": [],
      "checksum": "<64 lowercase hex digits>",
      "assets": [],
      "interface_sha256": "<same interface hash>",
      "conformance_sha256": "<same conformance hash>",
      "abi": "slick-core-1"
    }
  ],
  "dependencies": []
}
```

Canonical package names cannot use `root.*` or `std.*`, and names in one closure cannot overlap by prefix. Application source may import only direct project dependencies. Canonical interface source may import only the package-level dependencies; adapter source may import only names in that adapter's `dependencies` array.

The conformance directory is a complete pure Slick program with `root.main() -> bool`. Slick compiles and runs it against every declared portable Slick adapter; every run must return `true`. Each adapter must expose exactly the canonical public declarations, types, checked failures, and effects, must bind the exact interface and conformance hashes, and cannot add a public symbol. The interface `effects` list is the exact union of its public callable effects; every listed resource is a public class.

`slick build` resolves the full dependency closure, requires exactly one adapter for each package/backend/target tuple, rejects undeclared alpha interfaces or the selected alpha adapter without `--allow-alpha`, and validates all adapter entries/assets before code generation or toolchain invocation. Only `kind: "slick"` is linkable currently; other reserved implementation kinds fail explicitly when selected. A missing adapter reports the full dependency path and the package's available backend/target/stability tuples.

Slick hashes and compiles one retained source snapshot. Before native compilation it exclusively guards `slick.lock`; concurrent target builds cannot lose each other's selections or replace output after a lock conflict. After a successful build, Slick atomically creates or extends the lock with each exact package version, canonical interface hash, and selected adapter checksum. Later version, interface, adapter, or checksum drift fails; Slick never rewrites a conflicting lock automatically. Commit `slick.project.json`, every package manifest, and `slick.lock`. Do not add implementation-provider choices to Slick source.


## Work on a project

1. Identify the narrowest file or project directory that contains the change.
2. Inspect unfamiliar APIs before editing:

   ```bash
   slick describe --json std.text.Trim
   slick describe --json root.models.User examples/hello
   ```

3. Make the smallest source change.
4. Format the changed file or project:

   ```bash
   slick fmt path/to/project
   ```

5. Check it, then gate it:

   ```bash
   slick check path/to/project
   slick quality --check path/to/project
   ```

6. If behavior changed, run it. If native generation changed or matters to the task, build and execute the produced binary too.

Do not replace `check` with `run`: `run` adds interpreter behavior, while `check` is the direct static-validation command. Do not replace `check` with `quality` either; `quality` skips its semantic sections when compilation fails, so `check` still names the language error most directly.

## Repair a diagnostic

Diagnostics have stable uppercase codes and a severity in human output:

```text
main.slk:7:5: error[SLK370]: Found is User? and may be null
main.slk:2:5: warning[SLK500]: binding NeverRead is never read
```

An `error` makes the program invalid. A `warning` describes a program that already compiles, and only `slick lint` and `slick quality --check` fail on one.

Use the code as an exact lookup key:

```bash
slick describe --json SLK370
```

The response gives the stable rule, severity, compiler phase, trigger, repair strategies, examples, and related codes. Apply the repair to the source-specific message, then run `slick fmt` and `slick check` again.

Diagnostic identifiers must match `^SLK[0-9]{3}$` exactly. Do not lowercase, truncate, search by range, or fuzzy-match them. Do not pass a project path when describing a diagnostic; diagnostic lookup is compiler-owned and project-free.

For an unknown canonical code, preserve the nonzero result. In JSON mode the command returns the versioned structured error document rather than a partial description.

## Lint and the quality gate

`slick lint` compiles once and then reports mechanically dead source the language still accepts:

| Code | Rule |
| --- | --- |
| `SLK500` | a `let` binding is never read |
| `SLK501` | a non-final expression statement is provably pure, so it does nothing |
| `SLK502` | a statement directly follows `return`, `throw`, `break`, or `continue` |

`slick quality` adds per-callable complexity and prints one deterministic report:

```text
FORMAT      PASS
CHECK       PASS
LINT        PASS
COMPLEXITY  PASS

QUALITY GATE: PASS
Files: 4  Code lines: 478  Errors: 0  Warnings: 0  Complexity violations: 0
Max cyclomatic: 10 root.UpdateTodoInTx
Max cognitive: 11 root.DispatchTodoItem
Largest callable: 35 lines root.UpdateTodoInTx
```

The gate passes when every source is canonically formatted, compiler errors are zero, lint warnings are zero, every callable scores cyclomatic complexity at most 10 (`SLK503`), and every callable scores cognitive complexity at most 15 (`SLK504`). Code lines are navigation evidence and never fail the gate. Compiler errors mark `FORMAT`, `LINT`, and `COMPLEXITY` as `SKIP`, because claims read off an invalid AST would cascade.

There is no threshold, budget, baseline, exclusion, or suppression. Repair the source: split independent decisions into separate callables, flatten nesting with `else if` or an early return, and name a long boolean chain as a predicate.

## Inspect symbols

Use canonical names:

- Language types: `string`, `Result`, `Map`, `Iterable`, `Error`
- Standard library: `std`, `std.text`, `std.text.Trim`, `std.http.server`
- Project declarations: `root.models.User`, `root.models.User.Name`

Compiler-owned language and `std.*` symbols need no project path. `root` symbols require the project path unless the current directory is the project root.

Use `--json` for agent or program consumption. Treat `schema_version` as versioned input. Compiler-owned symbols report maintainer-declared `stability` separately from computed `eligible`; eligibility is information, not promotion. Arrays and member ordering are deterministic. Large symbol descriptions may include a `budget` object and omit member sections; raise `--budget <lines>` when the omitted members are required.

Malformed diagnostic-like text, such as `slk370` or `SLK37`, follows ordinary exact symbol resolution and is not a diagnostic lookup.

## Understand paths and namespaces

A file path compiles that one `.slk` file in namespace `root`. A directory recursively loads only `.slk` files in stable path order. Subdirectories become namespace segments:

```text
project/models/user.slk -> root.models
```

Pass the project directory when declarations span files or namespaces. A path with no `.slk` sources is a usage/environment failure, not a clean program.

## Interpret command results

- `slick check` and `slick lint` print `ok` only when nothing was reported.
- `slick fmt --check` exits nonzero and prints each file that would change.
- `slick quality` prints the same report in both modes. Without `--check` a failed gate still exits 0, so it works as a local report; with `--check` a failed gate exits 1.
- Diagnostics use exit status 1.
- Invalid arguments, missing sources, filesystem failures, and analyzer failures use exit status 2. A failed analysis is never reported as a passing gate.
- `slick run` prints the non-null result of `root.main`. A `std.process.Status` result writes its exact bytes and becomes the exit code.
- `slick build` prints the output path after producing the executable.

Preserve stdout and stderr when automating error behavior. Do not treat output text from a failed command as successful program output.

## Final verification

For ordinary source changes, finish with:

```bash
slick fmt --check <path>
slick check <path>
slick quality --check <path>
```

Also run `slick run <path>` for observable interpreter behavior. Run `slick build <path> -o <temporary-output>` and execute it when native behavior is part of the contract.
