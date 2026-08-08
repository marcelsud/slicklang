---
name: slick-cli
description: Use the Slick CLI to format, check, run, build, inspect symbols, and explain compiler diagnostics. Use when operating Slick projects, validating .slk changes, diagnosing SLK codes, scripting describe output, or choosing the correct Slick command.
compatibility: Requires the `slick` executable on PATH.
metadata:
  author: marcelsud
  version: "0.1.0"
---

# Slick CLI

Use the CLI as the source of truth for the installed compiler. Prefer exact commands and machine-readable `describe --json` output over guessing language or standard-library behavior.

## Choose the command

| Goal | Command |
| --- | --- |
| Check one file or project | `slick check [path]` |
| Run `root.main` with the interpreter | `slick run [path]` |
| Build a standalone native executable | `slick build [path] -o <output>` |
| Format one file or project in place | `slick fmt [path]` |
| Verify formatting without writing | `slick fmt --check [path]` |
| Describe a language, standard-library, or project symbol | `slick describe [--json] [--budget <lines>] <symbol> [path]` |
| Explain a compiler diagnostic | `slick describe [--json] SLKxxx` |

The default path is `.` for `check`, `run`, `build`, and `fmt`. `build` always requires `-o` or `--output`.

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

5. Check it:

   ```bash
   slick check path/to/project
   ```

6. If behavior changed, run it. If native generation changed or matters to the task, build and execute the produced binary too.

Do not replace `check` with `run`: `run` adds interpreter behavior, while `check` is the direct static-validation command.

## Repair a diagnostic

Compiler diagnostics have stable uppercase codes in human output:

```text
main.slk:7:5: error[SLK370]: Found is User? and may be null
```

Use the code as an exact lookup key:

```bash
slick describe --json SLK370
```

The response gives the stable rule, compiler phase, trigger, repair strategies, examples, and related codes. Apply the repair to the source-specific message, then run `slick fmt` and `slick check` again.

Diagnostic identifiers must match `^SLK[0-9]{3}$` exactly. Do not lowercase, truncate, search by range, or fuzzy-match them. Do not pass a project path when describing a diagnostic; diagnostic lookup is compiler-owned and project-free.

For an unknown canonical code, preserve the nonzero result. In JSON mode the command returns the versioned structured error document rather than a partial description.

## Inspect symbols

Use canonical names:

- Language types: `string`, `Result`, `Map`, `Error`
- Standard library: `std`, `std.text`, `std.text.Trim`
- Project declarations: `root.models.User`, `root.models.User.Name`

Compiler-owned language and `std.*` symbols need no project path. `root` symbols require the project path unless the current directory is the project root.

Use `--json` for agent or program consumption. Treat `schema_version` as versioned input. Arrays and member ordering are deterministic. Large symbol descriptions may include a `budget` object and omit member sections; raise `--budget <lines>` when the omitted members are required.

Malformed diagnostic-like text, such as `slk370` or `SLK37`, follows ordinary exact symbol resolution and is not a diagnostic lookup.

## Understand paths and namespaces

A file path compiles that one `.slk` file in namespace `root`. A directory recursively loads only `.slk` files in stable path order. Subdirectories become namespace segments:

```text
project/models/user.slk -> root.models
```

Pass the project directory when declarations span files or namespaces. A path with no `.slk` sources is a usage/environment failure, not a clean program.

## Interpret command results

- `slick check` prints `ok` only when the program has no diagnostics.
- `slick fmt --check` exits nonzero and prints each file that would change.
- Compiler diagnostics use exit status 1.
- Invalid arguments, missing sources, filesystem failures, and command-level failures use exit status 2.
- `slick run` prints the non-null result of `root.main`.
- `slick build` prints the output path after producing the executable.

Preserve stdout and stderr when automating error behavior. Do not treat output text from a failed command as successful program output.

## Final verification

For ordinary source changes, finish with:

```bash
slick fmt --check <path>
slick check <path>
```

Also run `slick run <path>` for observable interpreter behavior. Run `slick build <path> -o <temporary-output>` and execute it when native behavior is part of the contract.
