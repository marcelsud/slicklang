package compiler

import (
	"strings"
	"text/scanner"
)

// TokenClass names the role a span of source plays, for syntax highlighting.
type TokenClass string

const (
	// ClassPlain is whitespace and anything the scanner does not classify.
	ClassPlain       TokenClass = "plain"
	ClassComment     TokenClass = "comment"
	ClassKeyword     TokenClass = "keyword"
	ClassType        TokenClass = "type"
	ClassConstructor TokenClass = "constructor"
	ClassConstant    TokenClass = "constant"
	ClassNumber      TokenClass = "number"
	ClassString      TokenClass = "string"
	ClassTemplate    TokenClass = "template"
	ClassIdent       TokenClass = "ident"
	ClassPunct       TokenClass = "punct"
)

// HighlightToken is one classified span of the original source.
type HighlightToken struct {
	Class TokenClass
	Text  string
}

var highlightKeywords = map[string]struct{}{
	"as": {}, "break": {}, "catch": {}, "class": {}, "continue": {},
	"else": {}, "extension": {}, "for": {}, "function": {}, "if": {},
	"implements": {}, "in": {}, "interface": {}, "let": {}, "map": {},
	"match": {}, "return": {}, "self": {}, "throw": {}, "throws": {},
	"use": {},
}

// Highlight splits source into classified spans. Concatenating every Text
// reproduces source byte for byte, so a renderer never has to reconstruct
// whitespace. It uses the same scanner configuration as lex, minus the comment
// skipping, so what it classifies is exactly what the compiler tokenizes.
func Highlight(source string) []HighlightToken {
	var s scanner.Scanner
	s.Init(strings.NewReader(source))
	s.Mode = scanner.GoTokens &^ scanner.SkipComments
	// A file being edited is often not lexable; render it anyway.
	s.Error = func(*scanner.Scanner, string) {}

	var tokens []HighlightToken
	emit := func(class TokenClass, text string) {
		if text != "" {
			tokens = append(tokens, HighlightToken{Class: class, Text: text})
		}
	}
	consumed := 0
	for kind := s.Scan(); kind != scanner.EOF; kind = s.Scan() {
		start, end := s.Position.Offset, s.Pos().Offset
		if start < consumed || end > len(source) {
			break
		}
		emit(ClassPlain, source[consumed:start])
		emit(classifyToken(kind, source[start:end]), source[start:end])
		consumed = end
	}
	emit(ClassPlain, source[consumed:])
	return tokens
}

func classifyToken(kind rune, text string) TokenClass {
	switch kind {
	case scanner.Comment:
		return ClassComment
	case scanner.Int, scanner.Float:
		return ClassNumber
	case scanner.String, scanner.Char:
		return ClassString
	case scanner.RawString:
		// Slick's raw strings are interpolated templates, not inert text.
		return ClassTemplate
	case scanner.Ident:
		return classifyIdent(text)
	default:
		return ClassPunct
	}
}

func classifyIdent(text string) TokenClass {
	if _, keyword := highlightKeywords[text]; keyword {
		return ClassKeyword
	}
	switch text {
	case "true", "false", "null":
		return ClassConstant
	case "Error", resultTypeName, mapTypeName, "Iterable":
		return ClassType
	}
	if isResultConstructor(text) || isIterableBuiltin(text) {
		return ClassConstructor
	}
	if isBuiltinType(text) {
		return ClassType
	}
	return ClassIdent
}
