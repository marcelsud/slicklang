package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	if args[0] == "lint" {
		return runLint(args[1:], os.Stdout, os.Stderr)
	}
	if args[0] == "quality" {
		return runQuality(args[1:], os.Stdout, os.Stderr)
	}
	if args[0] == "build" {
		path, output, options, err := parseBuildOptions(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return reportUsage()
		}
		diagnostics, err := compiler.BuildPathWithOptions(path, output, options)
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
		path, options, err := parseCheckArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return reportUsage()
		}
		diagnostics, err := compiler.CheckPathWithOptions(path, options)
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

func parseBuildArgs(args []string) (string, string, compiler.Backend, error) {
	path, output, options, err := parseBuildOptions(args)
	return path, output, options.Backend, err
}

func parseBuildOptions(args []string) (string, string, compiler.BuildOptions, error) {
	path := "."
	output := ""
	options := compiler.BuildOptions{Backend: compiler.BackendGo}
	pathSet := false
	alphaSet := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "-o", "--output":
			index++
			if index >= len(args) {
				return "", "", compiler.BuildOptions{}, errors.New("build output path is missing")
			}
			output = args[index]
		case "--backend":
			index++
			if index >= len(args) {
				return "", "", compiler.BuildOptions{}, errors.New("build backend is missing")
			}
			parsed, err := compiler.ParseBackend(args[index])
			if err != nil {
				return "", "", compiler.BuildOptions{}, err
			}
			options.Backend = parsed
		case "--allow-alpha":
			if alphaSet {
				return "", "", compiler.BuildOptions{}, errors.New("build --allow-alpha may only be specified once")
			}
			options.AllowAlpha = true
			alphaSet = true
		default:
			if strings.HasPrefix(args[index], "--backend=") {
				parsed, err := compiler.ParseBackend(strings.TrimPrefix(args[index], "--backend="))
				if err != nil {
					return "", "", compiler.BuildOptions{}, err
				}
				options.Backend = parsed
				continue
			}
			if strings.HasPrefix(args[index], "-") {
				return "", "", compiler.BuildOptions{}, fmt.Errorf("unknown build flag %q", args[index])
			}
			if pathSet {
				return "", "", compiler.BuildOptions{}, fmt.Errorf("unexpected build argument %q", args[index])
			}
			path = args[index]
			pathSet = true
		}
	}
	if output == "" {
		return "", "", compiler.BuildOptions{}, errors.New("build requires -o <output>")
	}
	return path, output, options, nil
}

func parseCheckArgs(args []string) (string, compiler.CheckOptions, error) {
	path := "."
	pathSet := false
	options := compiler.CheckOptions{}
	for _, arg := range args {
		switch {
		case arg == "--allow-alpha":
			if options.AllowAlpha {
				return "", compiler.CheckOptions{}, errors.New("check --allow-alpha may only be specified once")
			}
			options.AllowAlpha = true
		case strings.HasPrefix(arg, "-"):
			return "", compiler.CheckOptions{}, fmt.Errorf("unknown check flag %q", arg)
		case pathSet:
			return "", compiler.CheckOptions{}, fmt.Errorf("unexpected check argument %q", arg)
		default:
			path = arg
			pathSet = true
		}
	}
	return path, options, nil
}

func reportUsage() int {
	return reportUsageTo(os.Stderr)
}

func reportDiagnostics(diagnostics []compiler.Diagnostic) bool {
	return reportDiagnosticsTo(os.Stdout, diagnostics)
}

func reportDiagnosticsTo(stdout io.Writer, diagnostics []compiler.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(stdout, formatDiagnostic(diagnostic))
	}
	return len(diagnostics) > 0
}

// formatDiagnostic is the one line every command prints for one diagnostic. The
// registered severity of the code decides whether it reads error or warning.
func formatDiagnostic(diagnostic compiler.Diagnostic) string {
	return fmt.Sprintf("%s:%d:%d: %s[%s]: %s",
		diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Severity, diagnostic.Code, diagnostic.Message)
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
