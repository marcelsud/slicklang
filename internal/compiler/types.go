package compiler

import "strings"

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
	for index, char := range list {
		switch char {
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
	var open []rune
	for _, character := range name {
		switch character {
		case '(', '[', '<':
			open = append(open, character)
		case ')', ']', '>':
			if len(open) == 0 || open[len(open)-1] != openingDelimiter(character) {
				return false
			}
			open = open[:len(open)-1]
		}
	}
	return len(open) == 0
}

func openingDelimiter(closer rune) rune {
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
)

// parsedType is one level of a Slick type's structure. Every helper that needs
// to look inside a type goes through parseTypeName instead of trimming
// suffixes, so User[]? and User?[] can never collapse into each other and a
// nested Result<User?, LookupError> keeps its argument shape.
type parsedType struct {
	kind typeKind
	// base is the element of an optional or array, the base name of a generic
	// application, and the whole type otherwise.
	base string
	// args holds generic arguments or tuple elements.
	args []string
}

// parseTypeName decomposes the outermost layer of name. An unbalanced type is
// reported as a plain name so a malformed fragment never looks structural.
func parseTypeName(name string) parsedType {
	if !balancedType(name) {
		return parsedType{kind: typeKindName, base: name}
	}
	if suffix := strings.TrimSuffix(name, "?"); suffix != name {
		return parsedType{kind: typeKindOptional, base: suffix}
	}
	if suffix := strings.TrimSuffix(name, "[]"); suffix != name {
		return parsedType{kind: typeKindArray, base: suffix}
	}
	if strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")") {
		return parsedType{kind: typeKindTuple, base: name, args: splitTypeList(name[1 : len(name)-1])}
	}
	if base, args, generic := genericType(name); generic {
		return parsedType{kind: typeKindGeneric, base: base, args: args}
	}
	return parsedType{kind: typeKindName, base: name}
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
		return name + "?"
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
		isBufferOrOptionalBuffer(left) || isBufferOrOptionalBuffer(right) {
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
