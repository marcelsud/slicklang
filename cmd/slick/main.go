package main

import (
	"errors"
	"fmt"
	"os"

	"slick/internal/compiler"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return reportUsage()
	}
	if args[0] == "describe" {
		return runDescribe(args[1:], os.Stdout, os.Stderr)
	}
	if args[0] == "build" {
		path, output, err := parseBuildArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return reportUsage()
		}
		diagnostics, err := compiler.BuildPath(path, output)
		if err != nil {
			return reportError("build", err)
		}
		if reportDiagnostics(diagnostics) {
			return 1
		}
		fmt.Printf("built %s\n", output)
		return 0
	}
	if len(args) > 2 || (args[0] != "check" && args[0] != "run") {
		return reportUsage()
	}

	path := "."
	if len(args) == 2 {
		path = args[1]
	}
	if args[0] == "check" {
		diagnostics, err := compiler.CheckPath(path)
		if err != nil {
			return reportError("check", err)
		}
		if reportDiagnostics(diagnostics) {
			return 1
		}
		fmt.Println("ok")
		return 0
	}

	output, diagnostics, err := compiler.RunPath(path)
	if err != nil {
		return reportError("run", err)
	}
	if reportDiagnostics(diagnostics) {
		return 1
	}
	if output != "" {
		fmt.Println(output)
	}
	return 0
}

func parseBuildArgs(args []string) (string, string, error) {
	path := "."
	output := ""
	pathSet := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "-o", "--output":
			index++
			if index >= len(args) {
				return "", "", errors.New("build output path is missing")
			}
			output = args[index]
		default:
			if pathSet {
				return "", "", fmt.Errorf("unexpected build argument %q", args[index])
			}
			path = args[index]
			pathSet = true
		}
	}
	if output == "" {
		return "", "", errors.New("build requires -o <output>")
	}
	return path, output, nil
}

func reportUsage() int {
	return reportUsageTo(os.Stderr)
}

func reportDiagnostics(diagnostics []compiler.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		fmt.Printf("%s:%d:%d: error[%s]: %s\n", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
	}
	return len(diagnostics) > 0
}

func reportError(command string, err error) int {
	if errors.Is(err, compiler.ErrNoSources) {
		fmt.Fprintln(os.Stderr, err)
	} else {
		fmt.Fprintf(os.Stderr, "%s: %v\n", command, err)
	}
	return 2
}
