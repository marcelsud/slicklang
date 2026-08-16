package main

import (
	"fmt"
	"io"

	"slick/internal/compiler"
)

// runQuality prints one deterministic quality report. Both modes are read-only
// and print the same report; --check turns a failed gate into exit 1, while a
// usage, filesystem, or analyzer failure always exits 2 and never prints a
// passing report.
func runQuality(args []string, stdout, stderr io.Writer) int {
	path, check, err := parseCheckPathArgs("quality", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return reportUsageTo(stderr)
	}
	report, err := compiler.QualityPath(path)
	if err != nil {
		return reportErrorTo(stderr, "quality", err)
	}
	writeQualityReport(stdout, report)
	if check && !report.Passed() {
		return 1
	}
	return 0
}

func writeQualityReport(stdout io.Writer, report compiler.QualityReport) {
	for _, section := range report.Sections() {
		fmt.Fprintf(stdout, "%-12s%s\n", section.Name, section.Status)
	}

	if len(report.Unformatted) > 0 || len(report.Diagnostics) > 0 {
		fmt.Fprintln(stdout)
		for _, file := range report.Unformatted {
			fmt.Fprintf(stdout, "%s: source is not canonically formatted\n", file)
		}
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(stdout, formatDiagnostic(diagnostic))
		}
	}

	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "QUALITY GATE: %s\n", statusWord(report.Passed()))
	fmt.Fprintf(stdout, "Files: %d  Code lines: %d  Errors: %d  Warnings: %d  Complexity violations: %d\n",
		report.Files, report.CodeLines, report.Errors(), report.Warnings(), report.ComplexityViolations())
	if callable, measured := report.MaxCyclomatic(); measured {
		fmt.Fprintf(stdout, "Max cyclomatic: %d %s\n", callable.CyclomaticComplexity, callable.Symbol)
	}
	if callable, measured := report.MaxCognitive(); measured {
		fmt.Fprintf(stdout, "Max cognitive: %d %s\n", callable.CognitiveComplexity, callable.Symbol)
	}
	if callable, measured := report.LargestCallable(); measured {
		fmt.Fprintf(stdout, "Largest callable: %d lines %s\n", callable.CodeLines, callable.Symbol)
	}
}

func statusWord(passed bool) string {
	if passed {
		return string(compiler.QualityStatusPass)
	}
	return string(compiler.QualityStatusFail)
}

// parseCheckPathArgs reads --check and the one optional project path in either
// order, which fmt and quality share.
func parseCheckPathArgs(command string, args []string) (path string, check bool, err error) {
	path = "."
	set := false
	for _, arg := range args {
		switch {
		case arg == "--check":
			if check {
				return "", false, fmt.Errorf("%s --check may only be specified once", command)
			}
			check = true
		case len(arg) > 0 && arg[0] == '-':
			return "", false, fmt.Errorf("unknown %s flag %q", command, arg)
		default:
			if set {
				return "", false, fmt.Errorf("unexpected %s argument %q", command, arg)
			}
			path = arg
			set = true
		}
	}
	return path, check, nil
}
