package compiler

import (
	"sort"
	"strings"
)

const (
	resultTypeName   = "Result"
	mapTypeName      = "Map"
	bufferTypeName   = "Buffer"
	iterableTypeName = "Iterable"
	errorTypeName    = "Error"
)

const (
	coreKindPrimitive = "language primitive"
	coreKindGeneric   = "generic type"
	coreKindInterface = "interface"
)

type coreTypeDecl struct {
	kind       string
	typeParams []string
}

// coreTypeRegistry is the authoritative declaration registry for types owned
// by the language. Type checking, highlighting, and discovery all consume it.
var coreTypeRegistry = map[string]coreTypeDecl{
	"bool":           {kind: coreKindPrimitive},
	"bytes":          {kind: coreKindPrimitive},
	"float":          {kind: coreKindPrimitive},
	"int":            {kind: coreKindPrimitive},
	"null":           {kind: coreKindPrimitive},
	"string":         {kind: coreKindPrimitive},
	errorTypeName:    {kind: coreKindInterface},
	iterableTypeName: {kind: coreKindGeneric, typeParams: []string{"T"}},
	mapTypeName:      {kind: coreKindGeneric, typeParams: []string{"K", "V"}},
	bufferTypeName:   {kind: coreKindGeneric, typeParams: []string{"T"}},
	resultTypeName:   {kind: coreKindGeneric, typeParams: []string{"T", "E"}},
}

func coreType(name string) (coreTypeDecl, bool) {
	declaration, ok := coreTypeRegistry[name]
	return declaration, ok
}

func coreGenericType(name string) (coreTypeDecl, bool) {
	declaration, ok := coreType(name)
	return declaration, ok && declaration.kind == coreKindGeneric
}

func isBuiltinType(name string) bool {
	declaration, ok := coreType(name)
	return ok && declaration.kind == coreKindPrimitive
}

// splitTypeList splits a comma-separated type list at bracket depth zero, so
// nested generic and tuple commas stay inside their own element.
func splitTypeList(list string) []string {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	var parts []string
	depth, start := 0, 0
	for index := 0; index < len(list); index++ {
		if isTypeArrow(list, index) {
			index++
			continue
		}
		switch list[index] {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(list[start:index]))
				start = index + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(list[start:]))
}

// isTypeArrow reports whether index starts the -> of a callable type. Every
// scan that tracks angle brackets consults it first, so a callable's arrow is
// never mistaken for the end of a generic application.
func isTypeArrow(name string, index int) bool {
	return name[index] == '-' && index+1 < len(name) && name[index+1] == '>'
}

// genericType decomposes a generic type such as "Result<int,root.Failure>" into
// its base name and argument list. It reports false unless the outermost shape
// of name is a well-formed generic application, so callers never see a blank or
// unbalanced type argument: "Result<int,E>[]", tuples, "Result<int,>", and
// "Result<int,(Failure>" are all rejected.
func genericType(name string) (string, []string, bool) {
	open := strings.IndexByte(name, '<')
	if open <= 0 || !strings.HasSuffix(name, ">") {
		return "", nil, false
	}
	if closingAngle(name, open) != len(name)-1 {
		return "", nil, false
	}
	args := splitTypeList(name[open+1 : len(name)-1])
	for _, arg := range args {
		if arg == "" || !balancedType(arg) {
			return "", nil, false
		}
	}
	return name[:open], args, true
}

// balancedType reports whether every bracket in name is closed by its own
// matching delimiter. splitTypeList only tracks depth, so it happily yields a
// fragment like "(Failure"; this rejects that before it is treated as a type.
func balancedType(name string) bool {
	var open []byte
	for index := 0; index < len(name); index++ {
		if isTypeArrow(name, index) {
			index++
			continue
		}
		switch name[index] {
		case '(', '[', '<':
			open = append(open, name[index])
		case ')', ']', '>':
			if len(open) == 0 || open[len(open)-1] != openingDelimiter(name[index]) {
				return false
			}
			open = open[:len(open)-1]
		}
	}
	return len(open) == 0
}

func openingDelimiter(closer byte) byte {
	switch closer {
	case ')':
		return '('
	case ']':
		return '['
	default:
		return '<'
	}
}

func closingAngle(name string, open int) int {
	depth := 0
	for index := open; index < len(name); index++ {
		if isTypeArrow(name, index) {
			index++
			continue
		}
		switch name[index] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

// resultTypeArgs returns the success and failure types of a Result type.
func resultTypeArgs(name string) (success, failure string, ok bool) {
	base, args, generic := genericType(name)
	if !generic || base != resultTypeName || len(args) != 2 {
		return "", "", false
	}
	return args[0], args[1], true
}

// mapTypeArgs returns the key and value types of a Map type.
func mapTypeArgs(name string) (key, value string, ok bool) {
	base, args, generic := genericType(name)
	if !generic || base != mapTypeName || len(args) != 2 {
		return "", "", false
	}
	return args[0], args[1], true
}

func mapType(key, value string) string {
	return mapTypeName + "<" + key + "," + value + ">"
}

func isMapType(name string) bool {
	_, _, ok := mapTypeArgs(name)
	return ok
}

func bufferTypeArg(name string) (string, bool) {
	base, args, generic := genericType(name)
	if !generic || base != bufferTypeName || len(args) != 1 {
		return "", false
	}
	return args[0], true
}

func bufferType(element string) string {
	return bufferTypeName + "<" + element + ">"
}

func isMapKeyType(name string) bool {
	return name == "string" || name == "int" || name == "bool"
}

func isResultConstructor(name string) bool {
	return name == "Ok" || name == "Err"
}

func isReservedTypeName(name string) bool {
	_, core := coreType(name)
	return core || isResultConstructor(name)
}

// typeKind names the outermost shape of a Slick type.
type typeKind int

const (
	typeKindName typeKind = iota
	typeKindOptional
	typeKindArray
	typeKindTuple
	typeKindGeneric
	typeKindCallable
)

// parsedType is one level of a Slick type's structure. Every helper that needs
// to look inside a type goes through parseTypeName instead of trimming
// suffixes, so User[]? and User?[] can never collapse into each other and a
// nested Result<User?, LookupError> keeps its argument shape.
type parsedType struct {
	kind typeKind
	// base is the element of an optional or array, the base name of a generic
	// application, the result of a callable, and the whole type otherwise.
	base string
	// args holds generic arguments, tuple elements, or callable parameters.
	args []string
	// throws holds the checked effects of a callable type.
	throws []string
}

// parseTypeName decomposes the outermost layer of name. An unbalanced type is
// reported as a plain name so a malformed fragment never looks structural.
//
// A callable is recognised before any suffix is trimmed, because -> binds more
// weakly than postfix ? and []: (int)->int[] returns int[], while the array of
// callables spells its element in parentheses as ((int)->int)[].
func parseTypeName(name string) parsedType {
	if !balancedType(name) {
		return parsedType{kind: typeKindName, base: name}
	}
	if params, result, throws, callable := callableTypeParts(name); callable {
		return parsedType{kind: typeKindCallable, base: result, args: params, throws: throws}
	}
	if suffix := strings.TrimSuffix(name, "?"); suffix != name {
		return parsedType{kind: typeKindOptional, base: ungroupType(suffix)}
	}
	if suffix := strings.TrimSuffix(name, "[]"); suffix != name {
		return parsedType{kind: typeKindArray, base: ungroupType(suffix)}
	}
	if strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")") {
		// Parentheses around a callable are grouping, not a one-element tuple.
		if inner := ungroupType(name); inner != name {
			return parseTypeName(inner)
		}
		return parsedType{kind: typeKindTuple, base: name, args: splitTypeList(name[1 : len(name)-1])}
	}
	if base, args, generic := genericType(name); generic {
		return parsedType{kind: typeKindGeneric, base: base, args: args}
	}
	return parsedType{kind: typeKindName, base: name}
}

// throwsSeparator joins a callable type to its checked effects. The space keeps
// the keyword readable and the pipe matches a declaration's throws list, which
// a comma could not because a comma already separates parameter types.
const throwsSeparator = " throws "

// callableTypeParts decomposes a callable type such as "(int,int)->int" or
// "(string)->root.User throws root.Invalid". It reports false unless the
// outermost shape of name is a well-formed callable.
func callableTypeParts(name string) (params []string, result string, throws []string, ok bool) {
	if !strings.HasPrefix(name, "(") {
		return nil, "", nil, false
	}
	close := matchingParen(name, 0)
	if close < 0 || !strings.HasPrefix(name[close+1:], "->") {
		return nil, "", nil, false
	}
	params = splitTypeList(name[1:close])
	for _, param := range params {
		if param == "" || !balancedType(param) {
			return nil, "", nil, false
		}
	}
	result = name[close+3:]
	// throws always binds to the outermost callable, so a nested callable that
	// declares its own effects has to be parenthesized.
	if index := topLevelThrows(result); index >= 0 {
		throws = strings.Split(result[index+len(throwsSeparator):], "|")
		result = result[:index]
		for _, thrown := range throws {
			if thrown == "" {
				return nil, "", nil, false
			}
		}
	}
	// A result is one type: a comma at depth zero means these parentheses open a
	// tuple whose first element is a callable, not a callable of its own.
	if result == "" || !balancedType(result) || len(splitTypeList(result)) != 1 {
		return nil, "", nil, false
	}
	return params, result, throws, true
}

// callableType builds the canonical spelling of a callable type. The throw set
// is sorted so one callable has exactly one spelling and generated output stays
// deterministic across runs.
func callableType(params []string, result string, throws []string) string {
	var builder strings.Builder
	builder.WriteByte('(')
	builder.WriteString(strings.Join(params, ","))
	builder.WriteString(")->")
	builder.WriteString(result)
	if len(throws) > 0 {
		sorted := append([]string(nil), throws...)
		sort.Strings(sorted)
		builder.WriteString(throwsSeparator)
		builder.WriteString(strings.Join(dedupeSorted(sorted), "|"))
	}
	return builder.String()
}

func dedupeSorted(values []string) []string {
	unique := values[:0]
	for index, value := range values {
		if index == 0 || value != values[index-1] {
			unique = append(unique, value)
		}
	}
	return unique
}

func isCallableType(name string) bool {
	_, _, _, callable := callableTypeParts(name)
	return callable
}

// matchingParen returns the index of the parenthesis closing the one at start.
// Only parentheses are counted: every other bracket pair nests inside one.
func matchingParen(name string, start int) int {
	if start >= len(name) || name[start] != '(' {
		return -1
	}
	depth := 0
	for index := start; index < len(name); index++ {
		switch name[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

// topLevelThrows returns the index of the throws clause that belongs to the
// callable owning name, skipping any that sits inside a nested type.
func topLevelThrows(name string) int {
	depth := 0
	for index := 0; index < len(name); index++ {
		if isTypeArrow(name, index) {
			index++
			continue
		}
		switch name[index] {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(name[index:], throwsSeparator) {
				return index
			}
		}
	}
	return -1
}

// groupType parenthesizes a callable so a following ? or [] suffix applies to
// the callable itself rather than to its result type.
func groupType(name string) string {
	if isCallableType(name) {
		return "(" + name + ")"
	}
	return name
}

// ungroupType is groupType's inverse: it drops parentheses that only group a
// callable, so one type keeps one canonical spelling.
func ungroupType(name string) string {
	if !strings.HasPrefix(name, "(") || matchingParen(name, 0) != len(name)-1 {
		return name
	}
	if inner := name[1 : len(name)-1]; isCallableType(inner) {
		return inner
	}
	return name
}

// arrayOf returns the array type whose element is element.
func arrayOf(element string) string {
	return groupType(element) + "[]"
}

// optionalBase returns the type contained by an optional type. It is the only
// place the "?" suffix is recognised.
func optionalBase(name string) (string, bool) {
	parsed := parseTypeName(name)
	if parsed.kind != typeKindOptional {
		return "", false
	}
	return parsed.base, true
}

func isOptionalType(name string) bool {
	_, optional := optionalBase(name)
	return optional
}

// arrayElementType returns the element type of an array type.
func arrayElementType(name string) (string, bool) {
	parsed := parseTypeName(name)
	if parsed.kind != typeKindArray {
		return "", false
	}
	return parsed.base, true
}

// optionalOf returns the optional form of name. null is already the absent
// value and an optional is its own optional form, so both are left alone: one
// type has exactly one canonical spelling.
func optionalOf(name string) string {
	switch {
	case name == "null", name == typeUnknown, name == typeNever, isOptionalType(name):
		return name
	default:
		return groupType(name) + "?"
	}
}

// redundantOptional reports whether name spells an optional of an optional at
// any depth, such as User?? or Result<User??, E>.
func redundantOptional(name string) bool {
	parsed := parseTypeName(name)
	switch parsed.kind {
	case typeKindOptional:
		return isOptionalType(parsed.base) || redundantOptional(parsed.base)
	case typeKindArray:
		return redundantOptional(parsed.base)
	case typeKindCallable:
		if redundantOptional(parsed.base) {
			return true
		}
		fallthrough
	case typeKindTuple, typeKindGeneric:
		for _, arg := range parsed.args {
			if redundantOptional(arg) {
				return true
			}
		}
	}
	return false
}

// joinTypes returns the single type able to hold both left and right. A branch
// or collection that mixes a value with null joins to that value's optional
// type; anything else must already agree.
func joinTypes(left, right string) (string, bool) {
	switch {
	case left == right:
		return left, true
	case left == typeNever:
		return right, true
	case right == typeNever:
		return left, true
	case left == typeUnknown || right == typeUnknown:
		return typeUnknown, true
	case left == "null":
		return optionalOf(right), true
	case right == "null":
		return optionalOf(left), true
	}
	leftBase, leftOptional := optionalBase(left)
	rightBase, rightOptional := optionalBase(right)
	switch {
	case leftOptional && rightOptional:
		return "", false
	case leftOptional && leftBase == right:
		return left, true
	case rightOptional && rightBase == left:
		return right, true
	}
	return "", false
}

// comparableTypes reports whether two values may be compared with == or !=.
// An optional compares with null, with its own base type, and with an optional
// of the same base. Nothing else becomes comparable by being optional.
func comparableTypes(left, right string) bool {
	if left == "null" {
		return isOptionalType(right)
	}
	if right == "null" {
		return isOptionalType(left)
	}
	if isMapOrOptionalMap(left) || isMapOrOptionalMap(right) ||
		isBufferOrOptionalBuffer(left) || isBufferOrOptionalBuffer(right) ||
		isCallableOrOptionalCallable(left) || isCallableOrOptionalCallable(right) {
		return false
	}
	if left == right {
		return true
	}
	leftBase, leftOptional := optionalBase(left)
	rightBase, rightOptional := optionalBase(right)
	switch {
	case leftOptional && rightOptional:
		return leftBase == rightBase
	case leftOptional:
		return leftBase == right
	case rightOptional:
		return rightBase == left
	}
	return false
}

func isMapOrOptionalMap(name string) bool {
	if base, optional := optionalBase(name); optional {
		name = base
	}
	return isMapType(name)
}

func isBufferOrOptionalBuffer(name string) bool {
	if base, optional := optionalBase(name); optional {
		name = base
	}
	_, buffer := bufferTypeArg(name)
	return buffer
}

// isCallableOrOptionalCallable reports whether a value of this type is a
// callable. Callables are neither comparable nor orderable, so equality rejects
// them; only a comparison with null stays valid, and that is decided before
// this is consulted.
func isCallableOrOptionalCallable(name string) bool {
	if base, optional := optionalBase(name); optional {
		name = base
	}
	return isCallableType(name)
}
