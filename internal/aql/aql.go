// Package aql reads Assets Query Language: the small language Jira Service
// Management filters a Assets object picker with. A request type can say that
// its object field offers only the laptops of one team, and that sentence is
// written in AQL.
//
// What is understood here is the part of AQL a form filter uses: the object's
// own type, name and key, its attributes by name, the comparisons a picker
// needs, and AND, OR, NOT and brackets between them. Everything else -- object
// references, functions, ORDER BY -- is refused with a message that says so,
// rather than silently matching everything.
package aql

import (
	"strconv"
	"strings"
	"unicode"
)

// Query is a parsed filter. Compile turns it into a condition over the objects
// of one service desk.
type Query struct {
	root node
}

// SyntaxError is a filter this package cannot read, with where it gave up.
type SyntaxError struct {
	Message string
	Offset  int
}

func (e *SyntaxError) Error() string { return e.Message }

type node interface{ sql(*compiler) string }

type andNode struct{ left, right node }
type orNode struct{ left, right node }
type notNode struct{ inner node }

// comparison is one test: a field, an operator and the values it takes.
type comparison struct {
	field    string
	operator string
	values   []string
}

// Parse reads a filter. An empty filter is no filter at all, which the caller
// treats as "every object".
func Parse(input string) (*Query, error) {
	tokens, err := scan(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return &Query{}, nil
	}
	parser := &parser{tokens: tokens}
	root, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if !parser.done() {
		return nil, &SyntaxError{Message: "unexpected " + strconv.Quote(parser.peek().text) + " after the filter", Offset: parser.peek().offset}
	}
	return &Query{root: root}, nil
}

// Empty reports a filter that selects everything, which is what no filter is.
func (q *Query) Empty() bool { return q == nil || q.root == nil }

// ---- scanning ----

type tokenKind int

const (
	tokenWord tokenKind = iota
	tokenString
	tokenOperator
	tokenOpen
	tokenClose
	tokenComma
)

type token struct {
	kind   tokenKind
	text   string
	offset int
}

// scan reads the filter into words, quoted strings, operators and brackets.
func scan(input string) ([]token, error) {
	tokens := []token{}
	runes := []rune(input)
	for index := 0; index < len(runes); {
		switch current := runes[index]; {
		case unicode.IsSpace(current):
			index++
		case current == '(':
			tokens = append(tokens, token{kind: tokenOpen, text: "(", offset: index})
			index++
		case current == ')':
			tokens = append(tokens, token{kind: tokenClose, text: ")", offset: index})
			index++
		case current == ',':
			tokens = append(tokens, token{kind: tokenComma, text: ",", offset: index})
			index++
		case current == '"' || current == '\'':
			text, next, err := scanQuoted(runes, index)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenString, text: text, offset: index})
			index = next
		case current == '=' || current == '!' || current == '<' || current == '>':
			text, next := scanOperator(runes, index)
			tokens = append(tokens, token{kind: tokenOperator, text: text, offset: index})
			index = next
		default:
			start := index
			for index < len(runes) && !unicode.IsSpace(runes[index]) && !strings.ContainsRune("()\",'=!<>", runes[index]) {
				index++
			}
			if start == index {
				return nil, &SyntaxError{Message: "cannot read " + strconv.Quote(string(runes[index])), Offset: index}
			}
			tokens = append(tokens, token{kind: tokenWord, text: string(runes[start:index]), offset: start})
		}
	}
	return tokens, nil
}

// scanQuoted reads a quoted value, where a quote is doubled to include one.
func scanQuoted(runes []rune, start int) (string, int, error) {
	quote := runes[start]
	var text strings.Builder
	for index := start + 1; index < len(runes); index++ {
		if runes[index] != quote {
			text.WriteRune(runes[index])
			continue
		}
		if index+1 < len(runes) && runes[index+1] == quote {
			text.WriteRune(quote)
			index++
			continue
		}
		return text.String(), index + 1, nil
	}
	return "", 0, &SyntaxError{Message: "a quoted value is never closed", Offset: start}
}

// scanOperator reads =, !=, <, <=, > and >=.
func scanOperator(runes []rune, start int) (string, int) {
	if start+1 < len(runes) && runes[start+1] == '=' {
		return string(runes[start : start+2]), start + 2
	}
	return string(runes[start : start+1]), start + 1
}

// ---- parsing ----

type parser struct {
	tokens []token
	at     int
}

func (p *parser) done() bool { return p.at >= len(p.tokens) }

func (p *parser) peek() token {
	if p.done() {
		return token{offset: -1}
	}
	return p.tokens[p.at]
}

func (p *parser) takeWord(word string) bool {
	if p.done() {
		return false
	}
	current := p.tokens[p.at]
	if current.kind == tokenWord && strings.EqualFold(current.text, word) {
		p.at++
		return true
	}
	return false
}

// parseOr reads a filter: comparisons joined by OR, which binds loosest.
func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.takeWord("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.takeWord("AND") {
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = andNode{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.takeWord("NOT") {
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notNode{inner: inner}, nil
	}
	if !p.done() && p.peek().kind == tokenOpen {
		p.at++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.done() || p.peek().kind != tokenClose {
			return nil, &SyntaxError{Message: "a bracket is never closed", Offset: p.peek().offset}
		}
		p.at++
		return inner, nil
	}
	return p.parseComparison()
}

// parseComparison reads one test: a field, an operator and what it is
// compared with.
func (p *parser) parseComparison() (node, error) {
	if p.done() {
		return nil, &SyntaxError{Message: "the filter ends where a condition was expected", Offset: -1}
	}
	field := p.tokens[p.at]
	if field.kind != tokenWord && field.kind != tokenString {
		return nil, &SyntaxError{Message: "expected something to compare, found " + strconv.Quote(field.text), Offset: field.offset}
	}
	p.at++
	if p.done() {
		return nil, &SyntaxError{Message: strconv.Quote(field.text) + " is compared with nothing", Offset: field.offset}
	}
	operator := strings.ToUpper(p.tokens[p.at].text)
	switch {
	case p.tokens[p.at].kind == tokenOperator:
		if operator != "=" && operator != "!=" {
			return nil, &SyntaxError{Message: "only = and != compare an object's values here, not " + operator, Offset: p.tokens[p.at].offset}
		}
		p.at++
	case operator == "LIKE", operator == "IS", operator == "IN":
		p.at++
	case operator == "NOT":
		// "NOT IN" and "IS NOT EMPTY" read as one operator.
		p.at++
		if p.done() {
			return nil, &SyntaxError{Message: "NOT what?", Offset: field.offset}
		}
		next := strings.ToUpper(p.tokens[p.at].text)
		if next != "IN" {
			return nil, &SyntaxError{Message: "expected IN after NOT, found " + strconv.Quote(p.tokens[p.at].text), Offset: p.tokens[p.at].offset}
		}
		p.at++
		operator = "NOT IN"
	default:
		return nil, &SyntaxError{Message: strconv.Quote(p.tokens[p.at].text) + " is not something that compares", Offset: p.tokens[p.at].offset}
	}
	if operator == "IS" {
		negated := p.takeWord("NOT")
		if !p.takeWord("EMPTY") {
			return nil, &SyntaxError{Message: "expected EMPTY after IS", Offset: field.offset}
		}
		operator = "IS EMPTY"
		if negated {
			operator = "IS NOT EMPTY"
		}
		return comparison{field: field.text, operator: operator}, nil
	}
	values, err := p.parseValues(operator)
	if err != nil {
		return nil, err
	}
	return comparison{field: field.text, operator: operator, values: values}, nil
}

// parseValues reads what a comparison takes: one value, or a bracketed list
// for IN and NOT IN.
func (p *parser) parseValues(operator string) ([]string, error) {
	if operator != "IN" && operator != "NOT IN" {
		if p.done() || (p.peek().kind != tokenWord && p.peek().kind != tokenString) {
			return nil, &SyntaxError{Message: "expected a value", Offset: p.peek().offset}
		}
		value := p.tokens[p.at].text
		p.at++
		return []string{value}, nil
	}
	if p.done() || p.peek().kind != tokenOpen {
		return nil, &SyntaxError{Message: "IN takes a bracketed list", Offset: p.peek().offset}
	}
	p.at++
	values := []string{}
	for {
		if p.done() {
			return nil, &SyntaxError{Message: "a bracket is never closed", Offset: -1}
		}
		current := p.tokens[p.at]
		if current.kind != tokenWord && current.kind != tokenString {
			return nil, &SyntaxError{Message: "expected a value in the list, found " + strconv.Quote(current.text), Offset: current.offset}
		}
		values = append(values, current.text)
		p.at++
		if p.done() {
			return nil, &SyntaxError{Message: "a bracket is never closed", Offset: -1}
		}
		switch p.tokens[p.at].kind {
		case tokenComma:
			p.at++
		case tokenClose:
			p.at++
			if len(values) == 0 {
				return nil, &SyntaxError{Message: "an empty list matches nothing", Offset: current.offset}
			}
			return values, nil
		default:
			return nil, &SyntaxError{Message: "expected a comma or a closing bracket", Offset: p.tokens[p.at].offset}
		}
	}
}
