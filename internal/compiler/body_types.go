package compiler

import (
	"sort"
	"strings"
	"text/scanner"
)

type callableTarget struct {
	name      string
	namespace string
	aliases   map[string]aliasDecl
	params    []paramDecl
	result    typeRef
	throwSet  map[string]struct{}
}

func (p *program) initialLocals(function *functionDecl) map[string]string {
	locals := make(map[string]string, len(function.params)+1)
	for _, param := range function.params {
		locals[param.name] = p.resolveType(function.namespace, function.aliases, param.typ)
	}
	if function.receiverCanonical != "" {
		locals["self"] = function.receiverCanonical
	}
	return locals
}

func (p *program) resolveCall(function *functionDecl, ref qualifiedRef, locals map[string]string) (*callableTarget, bool) {
	parts := strings.Split(ref.name, ".")
	if len(parts) == 2 {
		if receiverType := locals[parts[0]]; receiverType != "" {
			method, ok := p.methodForType(receiverType, parts[1])
			if !ok {
				p.add(ref.pos, "SLK321", "%s has no method %s", displayName(receiverType), parts[1])
				return nil, true
			}
			if !p.requireAccess(ref.pos, function.namespace, method.ownerNamespace, method.name, "method") {
				return nil, true
			}
			return &callableTarget{
				name:      ref.name,
				namespace: method.namespace,
				aliases:   method.aliases,
				params:    method.params,
				result:    method.result,
				throwSet:  method.throwSet,
			}, true
		}
	}
	if strings.Contains(ref.name, ".") && !strings.HasPrefix(ref.name, "root.") {
		return nil, false
	}
	callee := p.resolveFunction(function, ref.name)
	if callee == nil {
		return nil, false
	}
	if !p.requireAccess(ref.pos, function.namespace, callee.namespace, callee.name, "function") {
		return nil, true
	}
	return &callableTarget{
		name:      ref.name,
		namespace: callee.namespace,
		aliases:   callee.aliases,
		params:    callee.params,
		result:    callee.result,
		throwSet:  callee.throwSet,
	}, true
}

func (p *program) recordLocal(function *functionDecl, body []token, index int, locals map[string]string) {
	if index+3 >= len(body) || body[index].text != "let" || body[index+1].kind != scanner.Ident || body[index+2].text != "=" {
		return
	}
	if inferred := p.inferType(function.namespace, function.aliases, body[index+3:], locals); inferred != "" {
		locals[body[index+1].text] = inferred
	}
}

func (p *program) inferType(namespace string, aliases map[string]aliasDecl, expression []token, locals map[string]string) string {
	if len(expression) == 0 {
		return ""
	}
	if expression[0].kind == scanner.String || expression[0].kind == scanner.RawString {
		return "string"
	}
	if expression[0].kind == scanner.Int {
		return "int"
	}
	if expression[0].kind == scanner.Float {
		return "float"
	}
	if expression[0].text == "true" || expression[0].text == "false" {
		return "bool"
	}
	ref, next, ok := readQualified(expression, 0)
	if !ok {
		return ""
	}
	if next < len(expression) && expression[next].text == "{" {
		return p.resolveNameIn(namespace, aliases, ref.name)
	}
	if next == 1 {
		return locals[ref.name]
	}
	return ""
}

func (p *program) checkFieldReference(function *functionDecl, ref qualifiedRef, locals map[string]string) bool {
	parts := strings.Split(ref.name, ".")
	if len(parts) != 2 {
		return false
	}
	class := p.classes[locals[parts[0]]]
	if class == nil {
		return false
	}
	field, ok := class.fields[parts[1]]
	if !ok {
		p.add(ref.pos, "SLK322", "%s has no field %s", class.name, parts[1])
		return true
	}
	p.requireAccess(ref.pos, function.namespace, class.namespace, field.name, "field")
	return true
}

func (p *program) checkConstructorFields(function *functionDecl, class *classDecl, tokens []token, open int) {
	close := matching(tokens, open, "{", "}")
	if close < 0 {
		return
	}
	depth := 1
	for index := open + 1; index < close; index++ {
		switch tokens[index].text {
		case "{":
			depth++
			continue
		case "}":
			depth--
			continue
		}
		if depth != 1 || tokens[index].kind != scanner.Ident || index+1 >= close || tokens[index+1].text != ":" {
			continue
		}
		field, ok := class.fields[tokens[index].text]
		if !ok {
			p.add(tokens[index].pos, "SLK322", "%s has no field %s", class.name, tokens[index].text)
			continue
		}
		p.requireAccess(tokens[index].pos, function.namespace, class.namespace, field.name, "field")
	}
}

func (p *program) checkCallArguments(function *functionDecl, target *callableTarget, tokens []token, locals map[string]string, pos position) {
	arguments := splitArguments(tokens)
	if len(arguments) == 1 && len(arguments[0]) == 0 {
		arguments = nil
	}
	if len(arguments) != len(target.params) {
		p.add(pos, "SLK320", "%s expects %d arguments, found %d", target.name, len(target.params), len(arguments))
		return
	}
	for index, argument := range arguments {
		actual := p.inferType(function.namespace, function.aliases, argument, locals)
		if actual == "" {
			continue
		}
		expected := p.resolveType(target.namespace, target.aliases, target.params[index].typ)
		if iface := p.interfaces[expected]; iface != nil {
			class := p.classes[actual]
			if class == nil {
				if actual != expected {
					p.add(pos, "SLK320", "argument %d to %s must implement %s, found %s", index+1, target.name, displayName(expected), displayName(actual))
				}
				continue
			}
			if reasons := p.classSatisfies(class, iface); len(reasons) > 0 {
				p.add(pos, "SLK320", "%s does not implement %s: %s", class.qualified, iface.qualified, strings.Join(reasons, "; "))
			}
			continue
		}
		if expected != actual {
			p.add(pos, "SLK320", "argument %d to %s must be %s, found %s", index+1, target.name, displayName(expected), displayName(actual))
		}
	}
}

func splitArguments(tokens []token) [][]token {
	if len(tokens) == 0 {
		return nil
	}
	var arguments [][]token
	start := 0
	paren, bracket, brace := 0, 0, 0
	for index, tok := range tokens {
		switch tok.text {
		case "(":
			paren++
		case ")":
			paren--
		case "[":
			bracket++
		case "]":
			bracket--
		case "{":
			brace++
		case "}":
			brace--
		case ",":
			if paren == 0 && bracket == 0 && brace == 0 {
				arguments = append(arguments, tokens[start:index])
				start = index + 1
			}
		}
	}
	return append(arguments, tokens[start:])
}

func (p *program) checkCallErrors(function *functionDecl, target *callableTarget, body []token, close int, pos position) {
	if len(target.throwSet) == 0 {
		return
	}
	catch, hasCatch := parseCatch(body, close+1)
	if !hasCatch {
		for _, thrown := range sortedSet(target.throwSet) {
			if !containsError(function.throwSet, thrown) {
				p.add(pos, "SLK201", "unhandled %s from %s; catch it or declare it in %s", displayName(thrown), target.name, function.qualified)
			}
		}
		return
	}

	handled := make(map[string]struct{})
	catchAll := false
	for _, arm := range catch.arms {
		resolved, isError := p.resolveError(function, arm.name)
		if !isError {
			p.add(arm.pos, "SLK200", "%s does not name an Error type", arm.name)
			continue
		}
		if errorClass := p.classes[resolved]; errorClass != nil {
			p.requireAccess(arm.pos, function.namespace, errorClass.namespace, errorClass.name, "error class")
		}
		if resolved == "Error" {
			catchAll = true
		} else {
			handled[resolved] = struct{}{}
		}
	}
	if catchAll {
		return
	}
	var missing []string
	for thrown := range target.throwSet {
		if _, ok := handled[thrown]; !ok {
			missing = append(missing, displayName(thrown))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		p.add(catch.pos, "SLK202", "non-exhaustive catch for %s; missing %s", target.name, strings.Join(missing, ", "))
	}
}

func (p *program) resolveNameIn(namespace string, aliases map[string]aliasDecl, name string) string {
	if strings.HasPrefix(name, "root.") {
		return name
	}
	if alias, ok := aliases[name]; ok {
		return alias.target
	}
	return qualify(namespace, name)
}
