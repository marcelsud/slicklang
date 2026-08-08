package compiler

import "strings"

const resultTypeName = "Result"

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

func isResultConstructor(name string) bool {
	return name == "Ok" || name == "Err"
}

func isReservedTypeName(name string) bool {
	return name == resultTypeName || isResultConstructor(name)
}
