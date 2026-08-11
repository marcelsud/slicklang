package compiler

import "testing"

// TestCallableTypeSpelling pins the layer every other callable rule assumes:
// -> binds more weakly than a postfix suffix, grouping parentheses are not a
// one-element tuple, and one callable has exactly one spelling.
func TestCallableTypeSpelling(t *testing.T) {
	tests := []struct {
		name     string
		typ      string
		kind     typeKind
		params   []string
		result   string
		throws   []string
		display  string
		optional string
		array    string
	}{
		{
			name:     "no parameters",
			typ:      "()->string",
			kind:     typeKindCallable,
			result:   "string",
			display:  "() -> string",
			optional: "(()->string)?",
			array:    "(()->string)[]",
		},
		{
			name:     "two parameters",
			typ:      "(int,int)->int",
			kind:     typeKindCallable,
			params:   []string{"int", "int"},
			result:   "int",
			display:  "(int, int) -> int",
			optional: "((int,int)->int)?",
			array:    "((int,int)->int)[]",
		},
		{
			name:    "result binds the suffix",
			typ:     "(int)->int[]",
			kind:    typeKindCallable,
			params:  []string{"int"},
			result:  "int[]",
			display: "(int) -> int[]",
		},
		{
			name:    "checked effects",
			typ:     "(string)->root.User throws root.Invalid",
			kind:    typeKindCallable,
			params:  []string{"string"},
			result:  "root.User",
			throws:  []string{"root.Invalid"},
			display: "(string) -> User throws Invalid",
		},
		{
			name:    "nested callable parameter",
			typ:     "((int)->int,string)->bool",
			kind:    typeKindCallable,
			params:  []string{"(int)->int", "string"},
			result:  "bool",
			display: "((int) -> int, string) -> bool",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseTypeName(test.typ)
			if parsed.kind != test.kind {
				t.Fatalf("kind of %s is %v, expected %v", test.typ, parsed.kind, test.kind)
			}
			if parsed.base != test.result {
				t.Errorf("result of %s is %q, expected %q", test.typ, parsed.base, test.result)
			}
			if len(parsed.args) != len(test.params) {
				t.Errorf("parameters of %s are %v, expected %v", test.typ, parsed.args, test.params)
			} else {
				for index, param := range test.params {
					if parsed.args[index] != param {
						t.Errorf("parameter %d of %s is %q, expected %q", index+1, test.typ, parsed.args[index], param)
					}
				}
			}
			if len(parsed.throws) != len(test.throws) {
				t.Errorf("throws of %s are %v, expected %v", test.typ, parsed.throws, test.throws)
			}
			if rebuilt := callableType(parsed.args, parsed.base, parsed.throws); rebuilt != test.typ {
				t.Errorf("rebuilt %s as %q", test.typ, rebuilt)
			}
			if display := displayName(test.typ); display != test.display {
				t.Errorf("display of %s is %q, expected %q", test.typ, display, test.display)
			}
			if test.optional == "" {
				return
			}
			optional := optionalOf(test.typ)
			if optional != test.optional {
				t.Errorf("optional of %s is %q, expected %q", test.typ, optional, test.optional)
			}
			if base, ok := optionalBase(optional); !ok || base != test.typ {
				t.Errorf("optional base of %s is %q (%t), expected %q", optional, base, ok, test.typ)
			}
			array := arrayOf(test.typ)
			if array != test.array {
				t.Errorf("array of %s is %q, expected %q", test.typ, array, test.array)
			}
			if element, ok := arrayElementType(array); !ok || element != test.typ {
				t.Errorf("array element of %s is %q (%t), expected %q", array, element, ok, test.typ)
			}
		})
	}
}

// TestCallableTypeIsNotATuple keeps the two paren shapes apart: a tuple never
// carries ->, and grouping parentheses around a callable are not an element.
func TestCallableTypeIsNotATuple(t *testing.T) {
	if parseTypeName("(int,string)").kind != typeKindTuple {
		t.Error("(int,string) is not a tuple")
	}
	if isCallableType("(int,string)") {
		t.Error("(int,string) reads as callable")
	}
	if parseTypeName("((int)->int)").kind != typeKindCallable {
		t.Error("grouping parentheses did not collapse to the callable")
	}
	if elements, ok := tupleElementTypes("((int)->int,string)"); !ok || len(elements) != 2 || elements[0] != "(int)->int" {
		t.Errorf("tuple of callable and string decomposed as %v (%t)", elements, ok)
	}
}

// TestCallableAssignability pins the narrow rule: invariant parameters and
// results, exact arity, and a throw set that may only shrink.
func TestCallableAssignability(t *testing.T) {
	tests := []struct {
		actual   string
		expected string
		allowed  bool
	}{
		{"(int)->int", "(int)->int", true},
		{"(int)->int", "(int)->int throws root.Invalid", true},
		{"(int)->int throws root.Invalid", "(int)->int", false},
		{"(int)->int throws root.Invalid", "(int)->int throws root.Invalid|root.Other", true},
		{"(int)->int throws root.Invalid", "(int)->int throws Error", true},
		{"(int)->int", "(int,int)->int", false},
		{"(int)->int", "(float)->int", false},
		{"(int)->int", "(int)->float", false},
		{"(int)->int?", "((int)->int)?", false},
	}
	for _, test := range tests {
		if allowed := callableAssignable(test.actual, test.expected); allowed != test.allowed {
			t.Errorf("assigning %s to %s reported %t, expected %t", test.actual, test.expected, allowed, test.allowed)
		}
	}
}
