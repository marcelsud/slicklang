package main

import (
	"bytes"
	"encoding/json"

	"slick/internal/compiler"
)

const defaultDescribeBudget = 30

type describeDocument struct {
	SchemaVersion int                        `json:"schema_version"`
	Budget        *describeBudget            `json:"budget,omitempty"`
	Symbol        compiler.SymbolDescription `json:"symbol"`
}

type describeBudget struct {
	Unit      string             `json:"unit"`
	Limit     int                `json:"limit"`
	Required  int                `json:"required"`
	Truncated bool               `json:"truncated"`
	Omitted   []describeOmission `json:"omitted"`
}

type describeOmission struct {
	Section string `json:"section"`
	Count   int    `json:"count"`
}

func applyDescriptionBudget(symbol compiler.SymbolDescription, limit int, jsonOutput bool) (compiler.SymbolDescription, *describeBudget) {
	fullLines := descriptionLineCount(symbol, nil, jsonOutput)
	if fullLines <= limit {
		return symbol, nil
	}

	omitted := omissionCounts(symbol)
	if len(omitted) == 0 {
		return symbol, nil
	}

	current := symbol
	current.Fields = []compiler.FieldDescription{}
	current.DeclaredMethods = []compiler.MethodDescription{}
	current.ImplementedMethods = []compiler.MethodDescription{}
	current.Interfaces = []string{}
	current.Children = []compiler.ChildDescription{}

	budget := &describeBudget{
		Unit:      "lines",
		Limit:     limit,
		Required:  fullLines,
		Truncated: true,
		Omitted:   omitted,
	}
	try := func(candidate compiler.SymbolDescription, section string) bool {
		candidateBudget := copyBudget(budget)
		decrementOmission(candidateBudget, section)
		if descriptionLineCount(candidate, candidateBudget, jsonOutput) > limit {
			return false
		}
		current = candidate
		budget = candidateBudget
		return true
	}

	for _, field := range symbol.Fields {
		candidate := current
		candidate.Fields = append(candidate.Fields, field)
		if !try(candidate, "fields") {
			return current, budget
		}
	}
	for _, method := range symbol.DeclaredMethods {
		candidate := current
		candidate.DeclaredMethods = append(candidate.DeclaredMethods, method)
		if !try(candidate, "declared_methods") {
			return current, budget
		}
	}
	for _, method := range symbol.ImplementedMethods {
		candidate := current
		candidate.ImplementedMethods = append(candidate.ImplementedMethods, method)
		if !try(candidate, "implemented_methods") {
			return current, budget
		}
	}
	for _, name := range symbol.Interfaces {
		candidate := current
		candidate.Interfaces = append(candidate.Interfaces, name)
		if !try(candidate, "interfaces") {
			return current, budget
		}
	}
	for _, child := range symbol.Children {
		candidate := current
		candidate.Children = append(candidate.Children, child)
		if !try(candidate, "children") {
			return current, budget
		}
	}
	return current, budget
}

func descriptionLineCount(symbol compiler.SymbolDescription, budget *describeBudget, jsonOutput bool) int {
	var output bytes.Buffer
	if jsonOutput {
		document := describeDocument{
			SchemaVersion: compiler.DescriptionSchemaVersion,
			Budget:        budget,
			Symbol:        symbol,
		}
		encoded, _ := json.MarshalIndent(document, "", "  ")
		return bytes.Count(encoded, []byte{'\n'}) + 1
	}
	writeHumanDescription(&output, symbol, budget)
	return bytes.Count(output.Bytes(), []byte{'\n'})
}

func omissionCounts(symbol compiler.SymbolDescription) []describeOmission {
	sections := []describeOmission{
		{Section: "fields", Count: len(symbol.Fields)},
		{Section: "declared_methods", Count: len(symbol.DeclaredMethods)},
		{Section: "implemented_methods", Count: len(symbol.ImplementedMethods)},
		{Section: "interfaces", Count: len(symbol.Interfaces)},
		{Section: "children", Count: len(symbol.Children)},
	}
	omitted := make([]describeOmission, 0, len(sections))
	for _, section := range sections {
		if section.Count > 0 {
			omitted = append(omitted, section)
		}
	}
	return omitted
}

func copyBudget(budget *describeBudget) *describeBudget {
	copy := *budget
	copy.Omitted = append([]describeOmission(nil), budget.Omitted...)
	return &copy
}

func decrementOmission(budget *describeBudget, section string) {
	for index := range budget.Omitted {
		if budget.Omitted[index].Section != section {
			continue
		}
		budget.Omitted[index].Count--
		if budget.Omitted[index].Count == 0 {
			budget.Omitted = append(budget.Omitted[:index], budget.Omitted[index+1:]...)
		}
		return
	}
}

func omittedFrom(budget *describeBudget, section string) int {
	if budget == nil {
		return 0
	}
	for _, omission := range budget.Omitted {
		if omission.Section == section {
			return omission.Count
		}
	}
	return 0
}
