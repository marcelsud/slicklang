package compiler

import (
	"fmt"
	"strconv"
	"strings"
)

// unionVariantDecl is one alternative of a closed union: a name plus its
// ordered payload fields. tag is the 1-based discriminant every backend shares,
// so a zero value never names a variant.
type unionVariantDecl struct {
	name          string
	fields        []fieldDecl
	tag           int
	documentation *string
	pos           position
}

// unionDecl is a closed set of data alternatives. order is the declaration
// order every diagnostic, description, and generated switch walks, so nothing
// depends on map iteration.
type unionDecl struct {
	name          string
	qualified     string
	namespace     string
	aliases       map[string]aliasDecl
	variants      map[string]*unionVariantDecl
	order         []string
	documentation *string
	pos           position
}

func (union *unionDecl) variantNames() []string {
	names := make([]string, 0, len(union.order))
	for _, name := range union.order {
		names = append(names, union.name+"."+name)
	}
	return names
}

func (p *parser) parseUnion() {
	name, ok := p.expectIdent("union name")
	if !ok {
		return
	}
	if isReservedTypeName(name.text) {
		p.error(name.pos, "union name %s is reserved by the compiler", name.text)
	}
	union := &unionDecl{
		name:          name.text,
		qualified:     qualify(p.source.Namespace, name.text),
		namespace:     p.source.Namespace,
		aliases:       p.aliases,
		variants:      make(map[string]*unionVariantDecl),
		documentation: p.consumeDocumentation(),
		pos:           name.pos,
	}
	if !p.accept("{") {
		p.error(p.current().pos, "expected union body")
		return
	}
	registered := true
	switch {
	case p.prog.unions[union.qualified] != nil:
		previous := p.prog.unions[union.qualified]
		p.reportDocumentationConflict(name.pos, union.qualified, previous.documentation, union.documentation)
		p.error(name.pos, "duplicate union %s; first declared at %s:%d:%d", union.qualified, previous.pos.file, previous.pos.line, previous.pos.column)
		registered = false
	case p.prog.classes[union.qualified] != nil, p.prog.interfaces[union.qualified] != nil:
		p.error(name.pos, "union %s conflicts with a class or interface of the same name", union.qualified)
		registered = false
	default:
		p.prog.unions[union.qualified] = union
	}

	for !p.atEnd() && p.current().text != "}" {
		if p.accept(",") || p.accept(";") {
			continue
		}
		p.pendingDocumentation = p.takeDocumentation(p.current().pos.line)
		p.parseUnionVariant(union, registered)
	}
	if !p.accept("}") {
		p.error(p.current().pos, "unterminated union body")
		return
	}
	// A union with no variants has no value, so every match on it would be
	// vacuously exhaustive and no construction could ever satisfy it.
	if registered && len(union.order) == 0 {
		p.error(name.pos, "union %s must declare at least one variant", union.qualified)
	}
}

func (p *parser) parseUnionVariant(union *unionDecl, registered bool) {
	name, ok := p.expectIdent("variant name")
	if !ok {
		p.advance()
		return
	}
	variant := &unionVariantDecl{name: name.text, pos: name.pos}
	if p.current().text == "(" {
		params, ok := p.parseParams()
		if !ok {
			return
		}
		if len(params) == 0 {
			p.error(name.pos, "variant %s.%s must declare at least one field or omit its parentheses", union.name, name.text)
		}
		seen := make(map[string]struct{}, len(params))
		for _, param := range params {
			if _, duplicate := seen[param.name]; duplicate {
				p.error(name.pos, "duplicate field %s in variant %s.%s", param.name, union.name, name.text)
				continue
			}
			seen[param.name] = struct{}{}
			variant.fields = append(variant.fields, fieldDecl{name: param.name, typ: param.typ, pos: name.pos})
		}
	}
	variant.documentation = p.consumeDocumentation()
	p.accept(",")
	p.accept(";")
	if previous, exists := union.variants[variant.name]; exists {
		p.reportDocumentationConflict(name.pos, union.qualified+"."+variant.name, previous.documentation, variant.documentation)
		p.error(name.pos, "duplicate variant %s.%s; first declared at %s:%d:%d", union.qualified, variant.name, previous.pos.file, previous.pos.line, previous.pos.column)
		return
	}
	if !registered {
		return
	}
	variant.tag = len(union.order) + 1
	union.variants[variant.name] = variant
	union.order = append(union.order, variant.name)
}

// resolveVariant reads a qualified variant reference such as Expression.Literal
// or an aliased Expr.Literal. named reports that the prefix denotes a union, so
// a caller can tell "not a union at all" from "union without that variant".
func (p *program) resolveVariant(namespace string, aliases map[string]aliasDecl, name string) (union *unionDecl, variant *unionVariantDecl, named bool) {
	separator := strings.LastIndexByte(name, '.')
	if separator < 0 {
		return nil, nil, false
	}
	union = p.unions[p.resolveNameIn(namespace, aliases, name[:separator])]
	if union == nil {
		return nil, nil, false
	}
	return union, union.variants[name[separator+1:]], true
}

// requireVariant validates one variant reference and reports the single reason
// it is unusable. It returns nil when the reference cannot be constructed or
// matched.
func (p *program) requireVariant(pos position, fromNamespace, name string, union *unionDecl, variant *unionVariantDecl) *unionVariantDecl {
	if variant == nil {
		p.add(pos, diagnosticCodeUnionVariant, "union %s has no variant %s; it declares %s",
			displayName(union.qualified), name[strings.LastIndexByte(name, '.')+1:], strings.Join(union.variantNames(), ", "))
		return nil
	}
	if !p.requireAccess(pos, fromNamespace, union.namespace, union.name, "union") {
		return nil
	}
	if !p.requireAccess(pos, fromNamespace, union.namespace, variant.name, "variant") {
		return nil
	}
	return variant
}

func (p *program) variantFieldTypes(union *unionDecl, variant *unionVariantDecl) []string {
	types := make([]string, 0, len(variant.fields))
	for _, field := range variant.fields {
		types = append(types, p.resolveType(union.namespace, union.aliases, field.typ))
	}
	return types
}

// checkVariantValue types a fieldless variant reference such as
// Expression.Missing.
func (p *program) checkVariantValue(node *nameExpression, union *unionDecl, variant *unionVariantDecl, scope *astScope) expressionInfo {
	if strings.HasPrefix(union.qualified, "std.sqlite.") {
		p.usesStdSQLite = true
	}
	resolved := p.requireVariant(node.pos, scope.function.namespace, node.name, union, variant)
	if resolved == nil {
		return expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	}
	node.resolvedDeclaration = union.qualified + "." + resolved.name
	if len(resolved.fields) > 0 {
		p.add(node.pos, diagnosticCodeUnionVariant, "%s.%s carries a payload; construct it with %s.%s(...)",
			union.name, resolved.name, union.name, resolved.name)
	}
	return expressionInfo{typ: union.qualified, effects: make(effectSet)}
}

// checkVariantConstruction types a variant constructor call such as
// Expression.Binary(Left, "+", Right). Arity and payload assignability follow
// the ordinary call rules.
func (p *program) checkVariantConstruction(node *callExpression, name *nameExpression, union *unionDecl, variant *unionVariantDecl, scope *astScope) expressionInfo {
	if strings.HasPrefix(union.qualified, "std.sqlite.") {
		p.usesStdSQLite = true
	}
	info := expressionInfo{typ: union.qualified, effects: make(effectSet)}
	resolved := p.requireVariant(name.pos, scope.function.namespace, name.name, union, variant)
	if resolved == nil {
		info.typ = typeUnknown
		for _, argument := range node.args {
			mergeEffects(info.effects, p.checkASTExpression(argument, scope).effects)
		}
		return info
	}
	node.resolvedDeclaration = union.qualified + "." + resolved.name
	if len(node.typeArgs) > 0 {
		p.add(node.pos, diagnosticCodeTypeArguments, "%s does not take type arguments", name.name)
	}
	if len(resolved.fields) == 0 {
		p.add(node.pos, diagnosticCodeUnionVariant, "%s.%s has no payload; write it without parentheses", union.name, resolved.name)
	} else if len(node.args) != len(resolved.fields) {
		p.add(node.pos, diagnosticCodeCallArgument, "%s.%s expects %d payload values, found %d", union.name, resolved.name, len(resolved.fields), len(node.args))
	}
	// A constructor is not a call: both backends build the value inline from the
	// variant declaration. Leaving resolvedResult empty is what keeps
	// "async let X = Union.Variant(...)" a checked error instead of a backend
	// failure, since there is no function for a task to run.
	declared := p.variantFieldTypes(union, resolved)
	node.resolvedArgumentTypes = make([]string, len(node.args))
	for index, argument := range node.args {
		expected := ""
		if index < len(declared) {
			expected = declared[index]
		}
		argumentInfo := p.checkASTExpressionExpecting(argument, scope, expected)
		node.resolvedArgumentTypes[index] = argumentInfo.typ
		mergeEffects(info.effects, argumentInfo.effects)
		info.using = mergeUsingValues(info.using, argumentInfo.using)
		if index < len(declared) {
			p.checkAssignable(node.pos, argumentInfo.typ, expected, union.name+"."+resolved.name, index+1)
		}
	}
	info.using = p.usingForType(info.typ, info.using)
	return info
}

// checkUnionMatch types a match whose scrutinee is a union: every declared
// variant must be handled or a wildcard must close the set, payload bindings
// are scoped to their arm, and every reachable arm produces one type using the
// same rule Result matches use.
func (p *program) checkUnionMatch(node *matchExpression, union *unionDecl, valueEffects effectSet, valueUsing *usingValue, scope *astScope, expected string) expressionInfo {
	info := expressionInfo{typ: typeUnknown, effects: make(effectSet)}
	mergeEffects(info.effects, valueEffects)
	handled := make(map[string]position, len(union.order))
	catchAll := position{}
	hasCatchAll := false
	armType := ""
	var paths []pendingPath
	for index := range node.arms {
		arm := &node.arms[index]
		armScope := scope.clone()
		if arm.pattern == matchPatternAny {
			if hasCatchAll {
				p.add(arm.pos, diagnosticCodeMatchArm, "duplicate _ arm; already handled at %s:%d:%d", catchAll.file, catchAll.line, catchAll.column)
				continue
			}
			if len(handled) == len(union.order) {
				p.add(arm.pos, diagnosticCodeMatchArm, "unreachable _ arm; every variant of %s is already handled", displayName(union.qualified))
				continue
			}
			catchAll, hasCatchAll = arm.pos, true
		} else {
			if arm.pattern != matchPatternVariant {
				p.add(arm.pos, diagnosticCodeUnionVariant, "%s is not a variant of %s", arm.pattern, displayName(union.qualified))
				continue
			}
			variant := p.matchedVariant(arm, union, scope)
			if variant == nil {
				continue
			}
			if previous, duplicate := handled[variant.name]; duplicate {
				p.add(arm.pos, diagnosticCodeMatchArm, "duplicate %s.%s arm; already handled at %s:%d:%d", union.name, variant.name, previous.file, previous.line, previous.column)
				continue
			}
			if hasCatchAll {
				p.add(arm.pos, diagnosticCodeMatchArm, "unreachable %s.%s arm; the _ arm at %s:%d:%d already matches", union.name, variant.name, catchAll.file, catchAll.line, catchAll.column)
				continue
			}
			handled[variant.name] = arm.pos
			arm.resolvedVariant = variant.name
			p.bindVariantPayload(arm, union, variant, armScope, valueUsing)
		}
		armInfo := p.checkASTExpressionExpecting(arm.value, armScope, expected)
		mergeEffects(info.effects, armInfo.effects)
		info.using = mergeUsingValues(info.using, armInfo.using)
		paths = append(paths, pendingPath{scope: armScope, normal: armInfo.typ != typeNever})
		clearAssignedNarrowings(scope, arm.value)
		if armInfo.typ == typeNever || armInfo.typ == typeUnknown {
			continue
		}
		if armType == "" {
			armType = armInfo.typ
			continue
		}
		if armInfo.typ != armType {
			p.add(arm.pos, diagnosticCodeMatchArmType, "match arms must produce one type; found %s and %s", displayName(armType), displayName(armInfo.typ))
			armType = typeUnknown
		}
	}
	if !hasCatchAll {
		// A variant this namespace cannot name still reaches the match at
		// runtime, so it must be covered; only a _ arm can cover it, and naming
		// it here would leak a private declaration.
		inaccessible := false
		for _, name := range union.order {
			if _, ok := handled[name]; ok {
				continue
			}
			if !canAccess(name, union.namespace, scope.function.namespace) {
				inaccessible = true
				continue
			}
			p.add(node.pos, diagnosticCodeMatchExhaustiveness, "match does not handle %s.%s; add that arm or a _ arm", union.name, name)
		}
		if inaccessible {
			p.add(node.pos, diagnosticCodeMatchExhaustiveness, "match does not handle every variant of %s visible in %s; add a _ arm", displayName(union.qualified), union.namespace)
		}
	}
	p.mergePendingPaths(scope, node.pos, paths)
	mergeUsingPaths(scope, paths)
	if armType != "" {
		info.typ = armType
	}
	return info
}

// matchedVariant resolves one variant pattern against the union being matched
// and validates its payload shape.
func (p *program) matchedVariant(arm *matchArm, union *unionDecl, scope *astScope) *unionVariantDecl {
	patternUnion, variant, named := p.resolveVariant(scope.function.namespace, scope.function.aliases, arm.variant)
	if !named {
		p.add(arm.pos, diagnosticCodeUnionVariant, "%s does not name a variant of %s", arm.variant, displayName(union.qualified))
		return nil
	}
	if patternUnion.qualified != union.qualified {
		p.add(arm.pos, diagnosticCodeUnionVariant, "%s is a variant of %s, but the matched value is %s",
			arm.variant, displayName(patternUnion.qualified), displayName(union.qualified))
		return nil
	}
	resolved := p.requireVariant(arm.pos, scope.function.namespace, arm.variant, union, variant)
	if resolved == nil {
		return nil
	}
	if len(arm.bindings) != len(resolved.fields) {
		p.add(arm.pos, diagnosticCodeUnionVariant, "%s.%s binds %d payload values, but the variant declares %d",
			union.name, resolved.name, len(arm.bindings), len(resolved.fields))
		return nil
	}
	return resolved
}

func (p *program) bindVariantPayload(arm *matchArm, union *unionDecl, variant *unionVariantDecl, armScope *astScope, valueUsing *usingValue) {
	types := p.variantFieldTypes(union, variant)
	seen := make(map[string]struct{}, len(arm.bindings))
	for index, binding := range arm.bindings {
		if binding == "_" {
			continue
		}
		if _, duplicate := seen[binding]; duplicate {
			p.add(arm.pos, diagnosticCodeUnionVariant, "duplicate payload binding %s", binding)
			continue
		}
		seen[binding] = struct{}{}
		p.bindUsingLocal(armScope, binding, types[index], valueUsing)
	}
}

// runtimeVariant is the interpreter's tagged union payload. The complete static
// union type stays on the owning runtimeValue's typ, and fields keep their
// declaration order so formatting and equality never depend on a map.
type runtimeVariant struct {
	name   string
	fields []runtimeValue
}

func (p *program) runtimeVariantValue(union *unionDecl, variant *unionVariantDecl, args []runtimeValue) runtimeValue {
	types := p.variantFieldTypes(union, variant)
	fields := make([]runtimeValue, 0, len(types))
	for index, typ := range types {
		if index >= len(args) {
			break
		}
		fields = append(fields, coerceRuntimeValue(args[index], typ))
	}
	return runtimeValue{typ: union.qualified, variant: &runtimeVariant{name: variant.name, fields: fields}}
}

func formatRuntimeVariant(value *runtimeVariant) string {
	if len(value.fields) == 0 {
		return value.name
	}
	items := make([]string, 0, len(value.fields))
	for _, field := range value.fields {
		items = append(items, formatRuntimeValue(field))
	}
	return value.name + "(" + strings.Join(items, ", ") + ")"
}

func runtimeVariantEqual(left, right *runtimeVariant) bool {
	if left.name != right.name || len(left.fields) != len(right.fields) {
		return false
	}
	for index := range left.fields {
		if !runtimeEqual(left.fields[index], right.fields[index]) {
			return false
		}
	}
	return true
}

// evalMatchUnion selects the one arm that matches the scrutinee's tag. The
// scrutinee is already evaluated, so it is read exactly once.
func (p *program) evalMatchUnion(node *matchExpression, value runtimeValue, frame *runtimeFrame) (runtimeValue, error) {
	for _, arm := range node.arms {
		if arm.pattern == matchPatternVariant && arm.resolvedVariant != value.variant.name {
			continue
		}
		armFrame := frame.clone()
		if arm.pattern == matchPatternVariant {
			for index, binding := range arm.bindings {
				if binding == "_" || index >= len(value.variant.fields) {
					continue
				}
				armFrame.locals[binding] = value.variant.fields[index]
			}
		}
		return p.evalExpression(arm.value, armFrame)
	}
	return runtimeValue{}, runtimeError(node.pos, "match has no arm for %s.%s", displayName(value.typ), value.variant.name)
}

func goUnionName(name string) string { return goEncodedName("Union", name) }

func goVariantFieldName(variant, field string) string {
	return goEncodedName("Payload", variant+"."+field)
}

// emitUnionDeclarations emits one Go struct per union: a private tag plus the
// payload storage of every variant. The tag is written only by generated
// construction, so no checked Slick program can produce an invalid one.
func (g *goGenerator) emitUnionDeclarations() error {
	for _, name := range sortedKeys(g.program.unions) {
		if !g.program.usesStdSQLite && strings.HasPrefix(name, "std.sqlite.") {
			continue
		}
		union := g.program.unions[name]
		g.line("type %s struct {", goUnionName(name))
		g.line("slickTag int")
		for _, variantName := range union.order {
			for _, field := range union.variants[variantName].fields {
				typ, err := g.declaredType(union.namespace, union.aliases, field.typ)
				if err != nil {
					return err
				}
				g.line("%s %s", goVariantFieldName(variantName, field.name), g.goType(typ))
			}
		}
		g.line("}")
		g.line("func (value *%s) String() string {", goUnionName(name))
		g.line("if value == nil { return \"\" }")
		g.line("switch value.slickTag {")
		for _, variantName := range union.order {
			variant := union.variants[variantName]
			g.line("case %d:", variant.tag)
			if len(variant.fields) == 0 {
				g.line("return %s", strconv.Quote(variant.name))
				continue
			}
			pieces := make([]string, 0, len(variant.fields))
			for _, field := range variant.fields {
				pieces = append(pieces, fmt.Sprintf("slickFormat(value.%s)", goVariantFieldName(variantName, field.name)))
			}
			g.line("return %s + %s + %s", strconv.Quote(variant.name+"("), strings.Join(pieces, ` + ", " + `), strconv.Quote(")"))
		}
		g.line("}")
		g.line("return \"\"")
		g.line("}")
		g.line("")
	}
	return nil
}

// emitVariantConstruction writes one variant value. Every payload enters its
// declared storage type, so a T reaching a T? field promotes here exactly as it
// does for an ordinary call argument.
func (g *goGenerator) emitVariantConstruction(body *strings.Builder, union *unionDecl, variant *unionVariantDecl, arguments, argumentTypes []string) error {
	fields := make([]string, 0, len(variant.fields)+1)
	fields = append(fields, fmt.Sprintf("slickTag: %d", variant.tag))
	for index, field := range variant.fields {
		if index >= len(arguments) {
			break
		}
		declared, err := g.declaredType(union.namespace, union.aliases, field.typ)
		if err != nil {
			return err
		}
		fields = append(fields, goVariantFieldName(variant.name, field.name)+": "+g.convert(arguments[index], argumentTypes[index], declared))
	}
	fmt.Fprintf(body, "return &%s{%s}, nil\n", goUnionName(union.qualified), strings.Join(fields, ", "))
	return nil
}

// emitUnionMatch emits one tag comparison per arm in source order against a
// scrutinee that was already evaluated exactly once.
func (g *goGenerator) emitUnionMatch(body *strings.Builder, node *matchExpression, union *unionDecl, scrutinee string, scope *goScope, typ string) error {
	for _, arm := range node.arms {
		armScope := scope.clone()
		condition := ""
		if arm.pattern == matchPatternVariant {
			variant := union.variants[arm.resolvedVariant]
			if variant == nil {
				return fmt.Errorf("unknown generated variant %s", arm.variant)
			}
			condition = fmt.Sprintf("%s.slickTag == %d", scrutinee, variant.tag)
			fmt.Fprintf(body, "if %s {\n", condition)
			for index, binding := range arm.bindings {
				if binding == "_" || index >= len(variant.fields) {
					continue
				}
				declared, err := g.declaredType(union.namespace, union.aliases, variant.fields[index].typ)
				if err != nil {
					return err
				}
				variable := g.unique("bound")
				fmt.Fprintf(body, "%s := %s.%s\n_ = %s\n", variable, scrutinee, goVariantFieldName(variant.name, variant.fields[index].name), variable)
				armScope.locals[binding] = goBinding{name: variable, typ: declared, storage: variable, declared: declared}
			}
		}
		armValue, err := g.expression(arm.value, armScope)
		if err != nil {
			return err
		}
		fmt.Fprintf(body, "return %s\n", armValue)
		if condition == "" {
			return nil
		}
		fmt.Fprintf(body, "}\n")
	}
	fmt.Fprintf(body, "return %s, nil\n", g.zero(typ))
	return nil
}
