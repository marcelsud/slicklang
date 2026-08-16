package main

import (
	"fmt"
	"io"

	"slick/internal/compiler"
)

// runLint compiles the project once and prints the semantic findings of a valid
// program. Compiler errors are printed instead of lint findings, so one mistake
// cannot cascade into claims about an invalid AST.
func runLint(args []string, stdout, stderr io.Writer) int {
	path, err := parsePathArgs("lint", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return reportUsageTo(stderr)
	}
	diagnostics, err := compiler.LintPath(path)
	if err != nil {
		return reportErrorTo(stderr, "lint", err)
	}
	if reportDiagnosticsTo(stdout, diagnostics) {
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// parsePathArgs reads the one optional project path a read-only command takes.
func parsePathArgs(command string, args []string) (string, error) {
	path := "."
	set := false
	for _, arg := range args {
		if len(arg) > 0 && arg[0] == '-' {
			return "", fmt.Errorf("unknown %s flag %q", command, arg)
		}
		if set {
			return "", fmt.Errorf("unexpected %s argument %q", command, arg)
		}
		path = arg
		set = true
	}
	return path, nil
}
