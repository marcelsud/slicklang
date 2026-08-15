package compiler

import (
	"sort"
	"strings"
	"text/scanner"
)

type operationEffectRef struct {
	name string
	pos  position
}

type operationEffectSet map[string]struct{}

const (
	effectDatabase    = "database"
	effectEnvironment = "environment"
	effectFilesystem  = "filesystem"
	effectIO          = "io"
	effectNetwork     = "network"
	effectProcess     = "process"
	effectRandom      = "random"
	effectState       = "state"
	effectTime        = "time"
)

var operationEffectRegistry = map[string]string{
	effectDatabase:    "Accesses mutable database state.",
	effectEnvironment: "Reads or updates the process environment.",
	effectFilesystem:  "Reads or updates filesystem state.",
	effectIO:          "Reads or writes through a byte stream or terminal.",
	effectNetwork:     "Communicates over a network or accepts network traffic.",
	effectProcess:     "Starts or controls another process.",
	effectRandom:      "Consumes random or cryptographic entropy.",
	effectState:       "Reads or updates caller-observable in-memory state.",
	effectTime:        "Reads a clock or controls time-based execution.",
}

func allOperationEffects() operationEffectSet {
	effects := make(operationEffectSet, len(operationEffectRegistry))
	for name := range operationEffectRegistry {
		effects[name] = struct{}{}
	}
	return effects
}

func operationEffectRefs(names ...string) []operationEffectRef {
	refs := make([]operationEffectRef, len(names))
	for index, name := range names {
		refs[index] = operationEffectRef{name: name}
	}
	return refs
}
func operationEffectRefsAt(pos position, names ...string) []operationEffectRef {
	refs := operationEffectRefs(names...)
	for index := range refs {
		refs[index].pos = pos
	}
	return refs
}

func sortedOperationEffects(effects operationEffectSet) []string {
	names := make([]string, 0, len(effects))
	for name := range effects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func operationEffectsSubset(actual, expected operationEffectSet) bool {
	for name := range actual {
		if _, accepted := expected[name]; !accepted {
			return false
		}
	}
	return true
}

func operationEffectNamesSet(names []string) operationEffectSet {
	effects := make(operationEffectSet, len(names))
	for _, name := range names {
		effects[name] = struct{}{}
	}
	return effects
}

func (p *program) resolveOperationEffectSets() {
	for _, functions := range []map[string]*functionDecl{p.functions, p.genericFunctions} {
		for _, name := range sortedKeys(functions) {
			function := functions[name]
			p.checkingInstance(function.instanceOf != "", func() {
				function.operationSet = p.resolveOperationEffects(function.operations)
			})
		}
	}
	for _, implementations := range [][]*functionDecl{p.methodImpls, p.genericMethodImpls} {
		for _, implementation := range implementations {
			p.checkingInstance(implementation.instanceOf != "", func() {
				implementation.operationSet = p.resolveOperationEffects(implementation.operations)
			})
		}
	}
	for _, classes := range []map[string]*classDecl{p.classes, p.genericClasses} {
		for _, name := range sortedKeys(classes) {
			class := classes[name]
			p.checkingInstance(class.instanceOf != "", func() {
				for _, methodName := range sortedKeys(class.methods) {
					method := class.methods[methodName]
					method.operationSet = p.resolveOperationEffects(method.operations)
				}
			})
		}
	}
	for _, interfaces := range []map[string]*interfaceDecl{p.interfaces, p.genericInterfaces} {
		for _, name := range sortedKeys(interfaces) {
			iface := interfaces[name]
			p.checkingInstance(iface.instanceOf != "", func() {
				for _, methodName := range sortedKeys(iface.methods) {
					method := iface.methods[methodName]
					method.operationSet = p.resolveOperationEffects(method.operations)
				}
			})
		}
	}
}

func (p *program) resolveOperationEffects(refs []operationEffectRef) operationEffectSet {
	resolved := make(operationEffectSet, len(refs))
	for _, ref := range refs {
		if _, known := operationEffectRegistry[ref.name]; !known {
			p.add(ref.pos, diagnosticCodeEffectDeclaration,
				"unknown effect %s; expected one of %s", ref.name, strings.Join(sortedKeys(operationEffectRegistry), ", "))
			continue
		}
		if _, duplicate := resolved[ref.name]; duplicate {
			p.add(ref.pos, diagnosticCodeEffectDeclaration, "duplicate effect %s", ref.name)
			continue
		}
		resolved[ref.name] = struct{}{}
	}
	return resolved
}

func (p *program) requireOperationEffects(function *functionDecl, required operationEffectSet, pos position, origin string) {
	for _, name := range sortedOperationEffects(required) {
		if _, declared := function.operationSet[name]; declared {
			continue
		}
		p.add(pos, diagnosticCodeUndeclaredEffect,
			"%s uses effect %s through %s but does not declare it", function.qualified, name, origin)
	}
}

func parseOperationEffects(tokens []token, index int) ([]operationEffectRef, int, string, position) {
	if index >= len(tokens) || tokens[index].text != "{" {
		return nil, index, "expected '{' after 'effects'", typeTokenPos(tokens, index)
	}
	index++
	if index >= len(tokens) || tokens[index].text == "}" {
		return nil, index, "effects clause must contain at least one effect", typeTokenPos(tokens, index)
	}
	var refs []operationEffectRef
	for {
		if index >= len(tokens) || tokens[index].kind != scanner.Ident {
			return refs, index, "expected effect name", typeTokenPos(tokens, index)
		}
		refs = append(refs, operationEffectRef{name: tokens[index].text, pos: tokens[index].pos})
		index++
		if index < len(tokens) && tokens[index].text == "}" {
			return refs, index + 1, "", position{}
		}
		if index >= len(tokens) || tokens[index].text != "," {
			return refs, index, "expected ',' or '}' after effect name", typeTokenPos(tokens, index)
		}
		index++
		if index < len(tokens) && tokens[index].text == "}" {
			return refs, index, "expected effect name after ','", tokens[index].pos
		}
	}
}

func (p *parser) parseEffects() []operationEffectRef {
	if !p.accept("effects") {
		return nil
	}
	refs, next, message, errorPos := parseOperationEffects(p.tokens, p.index)
	if message != "" {
		p.error(errorPos, "%s", message)
		return refs
	}
	p.index = next
	return refs
}

func (p *bodyParser) parseLambdaEffects() []operationEffectRef {
	if !p.accept("effects") {
		return nil
	}
	refs, next, message, errorPos := parseOperationEffects(p.tokens, p.index)
	if message != "" {
		p.error(errorPos, "%s", message)
		return refs
	}
	p.index = next
	return refs
}
