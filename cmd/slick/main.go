package main

import (
	"errors"
	"fmt"
	"io"
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
	if args[0] == "fmt" {
		return runFmt(args[1:], os.Stdout, os.Stderr)
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
	if args[0] == "check" {
		if len(args) > 2 {
			return reportUsage()
		}
		path := "."
		if len(args) == 2 {
			path = args[1]
		}
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
	if args[0] != "run" {
		return reportUsage()
	}
	return runProgram(args[1:], os.Stdout, os.Stderr)
}

// runProgram runs a Slick project as a command-line tool. Everything after the
// project path belongs to the program, not to the toolchain, and a program that
// returns std.process.Status writes its exact bytes and becomes the exit code.
func runProgram(args []string, stdout, stderr io.Writer) int {
	path := "."
	var programArguments []string
	if len(args) >= 1 {
		path = args[0]
		programArguments = args[1:]
	}
	outcome, diagnostics, err := compiler.RunPathArguments(path, programArguments)
	if outcome.Status != nil {
		stdout.Write(outcome.Status.Output)
		stderr.Write(outcome.Status.ErrorOutput)
	}
	if err != nil {
		return reportErrorTo(stderr, "run", err)
	}
	if reportDiagnosticsTo(stdout, diagnostics) {
		return 1
	}
	if outcome.Status != nil {
		return outcome.Status.ExitCode
	}
	if outcome.Text != "" {
		fmt.Fprintln(stdout, outcome.Text)
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
	return reportDiagnosticsTo(os.Stdout, diagnostics)
}

func reportDiagnosticsTo(stdout io.Writer, diagnostics []compiler.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stdout, "%s:%d:%d: error[%s]: %s\n", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
	}
	return len(diagnostics) > 0
}

func reportError(command string, err error) int {
	return reportErrorTo(os.Stderr, command, err)
}

func reportErrorTo(stderr io.Writer, command string, err error) int {
	if errors.Is(err, compiler.ErrNoSources) {
		fmt.Fprintln(stderr, err)
	} else {
		fmt.Fprintf(stderr, "%s: %v\n", command, err)
	}
	return 2
}
