package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"slick/internal/compiler"
)

type describeErrorDocument struct {
	SchemaVersion int           `json:"schema_version"`
	Error         describeError `json:"error"`
}

type describeError struct {
	Code        string               `json:"code"`
	Message     string               `json:"message"`
	Symbol      string               `json:"symbol,omitempty"`
	Diagnostics []describeDiagnostic `json:"diagnostics,omitempty"`
}

type describeDiagnostic struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runDescribe(args []string, stdout, stderr io.Writer) int {
	symbol, path, jsonOutput, budgetLimit, err := parseDescribeArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return reportUsageTo(stderr)
	}

	description, diagnostics, err := compiler.DescribePath(symbol, path)
	if err != nil {
		if errors.Is(err, compiler.ErrUnknownSymbol) {
			if jsonOutput {
				writeJSON(stdout, describeErrorDocument{
					SchemaVersion: compiler.DescriptionSchemaVersion,
					Error: describeError{
						Code:    "unknown_symbol",
						Message: err.Error(),
						Symbol:  symbol,
					},
				})
			} else {
				fmt.Fprintln(stderr, err)
			}
			return 1
		}
		if errors.Is(err, compiler.ErrNoSources) {
			fmt.Fprintln(stderr, err)
		} else {
			fmt.Fprintf(stderr, "describe: %v\n", err)
		}
		return 2
	}
	if len(diagnostics) > 0 {
		if jsonOutput {
			items := make([]describeDiagnostic, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				items = append(items, describeDiagnostic{
					File:    diagnostic.File,
					Line:    diagnostic.Line,
					Column:  diagnostic.Column,
					Code:    diagnostic.Code,
					Message: diagnostic.Message,
				})
			}
			writeJSON(stdout, describeErrorDocument{
				SchemaVersion: compiler.DescriptionSchemaVersion,
				Error: describeError{
					Code:        "invalid_project",
					Message:     "project contains diagnostics",
					Diagnostics: items,
				},
			})
		} else {
			for _, diagnostic := range diagnostics {
				fmt.Fprintf(stdout, "%s:%d:%d: error[%s]: %s\n", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
			}
		}
		return 1
	}

	budgeted, budget := applyDescriptionBudget(description.Symbol, budgetLimit, jsonOutput)
	if jsonOutput {
		writeJSON(stdout, describeDocument{
			SchemaVersion: compiler.DescriptionSchemaVersion,
			Budget:        budget,
			Symbol:        budgeted,
		})
	} else {
		writeHumanDescription(stdout, budgeted, budget)
	}
	return 0
}

func parseDescribeArgs(args []string) (symbol, path string, jsonOutput bool, budget int, err error) {
	positionals := make([]string, 0, 2)
	budget = defaultDescribeBudget
	budgetSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			if jsonOutput {
				return "", "", false, 0, errors.New("describe --json may only be specified once")
			}
			jsonOutput = true
		case arg == "--budget":
			if budgetSet {
				return "", "", false, 0, errors.New("describe --budget may only be specified once")
			}
			index++
			if index >= len(args) {
				return "", "", false, 0, errors.New("describe budget is missing")
			}
			parsed, parseErr := strconv.Atoi(args[index])
			if parseErr != nil || parsed < 0 {
				return "", "", false, 0, fmt.Errorf("describe budget must be a non-negative integer, found %q", args[index])
			}
			budget = parsed
			budgetSet = true
		case strings.HasPrefix(arg, "-"):
			return "", "", false, 0, fmt.Errorf("unknown describe flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return "", "", false, 0, errors.New("describe requires a symbol")
	}
	if len(positionals) > 2 {
		return "", "", false, 0, fmt.Errorf("unexpected describe argument %q", positionals[2])
	}
	symbol = positionals[0]
	if len(positionals) == 2 {
		path = positionals[1]
	}
	return symbol, path, jsonOutput, budget, nil
}

func reportUsageTo(stderr io.Writer) int {
	fmt.Fprintln(stderr, "usage: slick <check|run> [path]")
	fmt.Fprintln(stderr, "       slick build [path] -o <output>")
	fmt.Fprintln(stderr, "       slick describe [--json] [--budget <lines>] <symbol> [path]")
	fmt.Fprintln(stderr, "       slick fmt [--check] [path]")
	return 2
}

func writeJSON(output io.Writer, document any) {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(document)
}

func writeHumanDescription(output io.Writer, description compiler.SymbolDescription, budget *describeBudget) {
	fmt.Fprintf(output, "Name: %s\n", description.CanonicalName)
	fmt.Fprintf(output, "Kind: %s\n", description.Kind)
	fmt.Fprintf(output, "Visibility: %s\n", description.Visibility)

	if description.Kind == "generic type" {
		fmt.Fprintf(output, "Type parameters: %s\n", strings.Join(description.TypeParameters, ", "))
	}
	if description.Kind == "function" {
		writeParameters(output, description.Parameters)
		fmt.Fprintf(output, "Returns: %s\n", description.ReturnType)
		writeThrows(output, description.Throws)
		fmt.Fprintf(output, "Native: %t\n", description.Native)
	}
	if description.Kind == "class" {
		writeFields(output, description.Fields, omittedFrom(budget, "fields"), budget)
		writeMethods(output, "Declared methods", description.DeclaredMethods, omittedFrom(budget, "declared_methods"), budget)
		writeMethods(output, "Implemented methods", description.ImplementedMethods, omittedFrom(budget, "implemented_methods"), budget)
		writeNames(output, "Interfaces", description.Interfaces, omittedFrom(budget, "interfaces"), budget)
	}
	if description.Kind == "interface" {
		writeMethods(output, "Declared methods", description.DeclaredMethods, omittedFrom(budget, "declared_methods"), budget)
	}
	if description.Kind == "namespace" {
		omitted := omittedFrom(budget, "children")
		fmt.Fprintln(output, "Children:")
		if len(description.Children) == 0 && omitted == 0 {
			fmt.Fprintln(output, "  none")
		}
		for _, child := range description.Children {
			fmt.Fprintf(output, "  %s %s (%s)\n", child.Kind, child.CanonicalName, child.Visibility)
		}
		writeElisionMarker(output, omitted, budget)
	}
	if description.Source != nil {
		fmt.Fprintf(output, "Source: %s:%d:%d\n", description.Source.File, description.Source.Line, description.Source.Column)
	}
}

func writeParameters(output io.Writer, parameters []compiler.ParameterDescription) {
	fmt.Fprintln(output, "Parameters:")
	if len(parameters) == 0 {
		fmt.Fprintln(output, "  none")
	}
	for _, parameter := range parameters {
		fmt.Fprintf(output, "  %s: %s\n", parameter.Name, parameter.Type)
	}
}

func writeThrows(output io.Writer, throws []string) {
	if len(throws) == 0 {
		fmt.Fprintln(output, "Throws: none")
		return
	}
	fmt.Fprintf(output, "Throws: %s\n", strings.Join(throws, ", "))
}

func writeFields(output io.Writer, fields []compiler.FieldDescription, omitted int, budget *describeBudget) {
	fmt.Fprintln(output, "Fields:")
	if len(fields) == 0 && omitted == 0 {
		fmt.Fprintln(output, "  none")
	}
	for _, field := range fields {
		fmt.Fprintf(output, "  %s %s: %s%s\n", field.Visibility, field.Name, field.Type, sourceSuffix(field.Source))
	}
	writeElisionMarker(output, omitted, budget)
}

func writeMethods(output io.Writer, label string, methods []compiler.MethodDescription, omitted int, budget *describeBudget) {
	fmt.Fprintf(output, "%s:\n", label)
	if len(methods) == 0 && omitted == 0 {
		fmt.Fprintln(output, "  none")
	}
	for _, method := range methods {
		parameters := make([]string, 0, len(method.Parameters))
		for _, parameter := range method.Parameters {
			parameters = append(parameters, parameter.Name+": "+parameter.Type)
		}
		throws := ""
		if len(method.Throws) > 0 {
			throws = " throws " + strings.Join(method.Throws, ", ")
		}
		fmt.Fprintf(output, "  %s %s(%s) -> %s%s%s\n", method.Visibility, method.CanonicalName, strings.Join(parameters, ", "), method.ReturnType, throws, sourceSuffix(method.Source))
	}
	writeElisionMarker(output, omitted, budget)
}

func writeNames(output io.Writer, label string, names []string, omitted int, budget *describeBudget) {
	if len(names) == 0 && omitted == 0 {
		fmt.Fprintf(output, "%s: none\n", label)
		return
	}
	fmt.Fprintf(output, "%s: %s\n", label, strings.Join(names, ", "))
	writeElisionMarker(output, omitted, budget)
}

func writeElisionMarker(output io.Writer, omitted int, budget *describeBudget) {
	if omitted == 0 {
		return
	}
	fmt.Fprintf(output, "  … %d more entries (re-run with a higher `--budget`; use `--budget %d` for full output)\n", omitted, budget.Required)
}

func sourceSuffix(source *compiler.SourceDescription) string {
	if source == nil {
		return ""
	}
	return fmt.Sprintf(" @ %s:%d:%d", source.File, source.Line, source.Column)
}
