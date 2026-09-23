// Package aql reads Assets Query Language: the small language Jira Service
// Management filters a Assets object picker with. A request type can say that
// its object field offers only the laptops of one team, and that sentence is
// written in AQL.
//
// What is understood here is the part of AQL a form filter uses: the object's
// own type, name and key, its attributes by name, the comparisons a picker
// needs, and AND, OR, NOT and brackets between them. Beside those it reads the
// references between objects -- a dotted path through a relationship, and
// inboundReferences()/outboundReferences() over the objects at the other end
// -- the objectTypeAndChildren() function over the object type hierarchy, and
// ORDER BY. Anything else is refused with a message that says so, rather than
// silently matching everything.
package aql

import (
	"strconv"
	"strings"
	"unicode"
)

// Query is a parsed filter. Compile turns it into a condition over the objects
// of one service desk.
type Query struct {
	root  node
	order *ordering
}

// ordering is an ORDER BY: the field to sort on and whether it descends.
type ordering struct {
	field      string
	descending bool
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

// referenceNode is a dotted path: the objects one relationship leads to, and
// what must be true of them. "Runs on".Tier = "1" is every object that runs on
// something in tier one.
type referenceNode struct {
	relationship string
	inner        node
}

// referencesNode is inboundReferences() or outboundReferences(): the objects
// pointing at this one, or the ones it points at, whatever the relationship is
// called.
type referencesNode struct {
	inbound bool
	inner   node
}

// typeAndChildrenNode is objectTypeAndChildren(): an object type and every
// type beneath it.
type typeAndChildrenNode struct {
	names   []string
	negated bool
}

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
	order, err := parser.parseOrder()
	if err != nil {
		return nil, err
	}
	if !parser.done() {
		return nil, &SyntaxError{Message: "unexpected " + strconv.Quote(parser.peek().text) + " after the filter", Offset: parser.peek().offset}
	}
	return &Query{root: root, order: order}, nil
}

// parseOrder reads the ORDER BY a filter may end with.
func (p *parser) parseOrder() (*ordering, error) {
	start := p.at
	if !p.takeWord("ORDER") {
		return nil, nil
	}
	if !p.takeWord("BY") {
		return nil, &SyntaxError{Message: "expected BY after ORDER", Offset: p.tokens[start].offset}
	}
	if p.done() || (p.peek().kind != tokenWord && p.peek().kind != tokenString) {
		return nil, &SyntaxError{Message: "ORDER BY what?", Offset: p.tokens[start].offset}
	}
	order := &ordering{field: p.tokens[p.at].text}
	p.at++
	switch {
	case p.takeWord("DESC"):
		order.descending = true
	case p.takeWord("ASC"):
	}
	return order, nil
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
	if !p.done() && p.peek().kind == tokenWord && p.at+1 < len(p.tokens) && p.tokens[p.at+1].kind == tokenOpen {
		switch strings.ToLower(p.peek().text) {
		case "inboundreferences", "outboundreferences":
			return p.parseReferences()
		}
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

// parseReferences reads inboundReferences(<filter>) or
// outboundReferences(<filter>): what must be true of the objects at the other
// end of a relationship, whatever that relationship is called.
func (p *parser) parseReferences() (node, error) {
	name := p.tokens[p.at].text
	inbound := strings.EqualFold(name, "inboundReferences")
	p.at += 2 // the name and its opening bracket
	inner, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.done() || p.peek().kind != tokenClose {
		return nil, &SyntaxError{Message: name + "() is never closed", Offset: p.peek().offset}
	}
	p.at++
	return referencesNode{inbound: inbound, inner: inner}, nil
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
	// A dotted path walks a relationship: "Runs on".Tier is the tier of
	// whatever this object runs on. A quoted first segment is its own token,
	// and the rest arrives as one word beginning with a dot.
	path := splitPath(field.text)
	for !p.done() && p.peek().kind == tokenWord && strings.HasPrefix(p.peek().text, ".") {
		rest := strings.TrimPrefix(p.peek().text, ".")
		p.at++
		if rest != "" {
			path = append(path, splitPath(rest)...)
			continue
		}
		// A quoted step, such as "Runs on"."Depends on", arrives as a lone dot
		// and then the quoted name.
		if p.done() || (p.peek().kind != tokenWord && p.peek().kind != tokenString) {
			return nil, &SyntaxError{Message: "a path through references has an empty step", Offset: field.offset}
		}
		path = append(path, p.tokens[p.at].text)
		p.at++
	}
	for _, segment := range path {
		if strings.TrimSpace(segment) == "" {
			return nil, &SyntaxError{Message: "a path through references has an empty step", Offset: field.offset}
		}
	}
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
		return throughReferences(path, comparison{field: path[len(path)-1], operator: operator}), nil
	}
	// objectType IN objectTypeAndChildren("Servers") asks for a type and
	// everything beneath it, which is the one function a type takes.
	if function, arguments, ok := p.takeFunction(); ok {
		if !strings.EqualFold(function, "objectTypeAndChildren") {
			return nil, &SyntaxError{Message: "unsupported function " + function + "()", Offset: field.offset}
		}
		if !isObjectTypeField(path[len(path)-1]) {
			return nil, &SyntaxError{Message: "objectTypeAndChildren() compares an object type, not " + strconv.Quote(path[len(path)-1]), Offset: field.offset}
		}
		if len(arguments) == 0 {
			return nil, &SyntaxError{Message: "objectTypeAndChildren() names an object type", Offset: field.offset}
		}
		switch operator {
		case "=", "IN":
			return throughReferences(path, typeAndChildrenNode{names: arguments}), nil
		case "!=", "NOT IN":
			return throughReferences(path, typeAndChildrenNode{names: arguments, negated: true}), nil
		}
		return nil, &SyntaxError{Message: "objectTypeAndChildren() compares with =, !=, IN or NOT IN", Offset: field.offset}
	}
	values, err := p.parseValues(operator)
	if err != nil {
		return nil, err
	}
	return throughReferences(path, comparison{field: path[len(path)-1], operator: operator, values: values}), nil
}

// splitPath reads a field written as steps through references.
func splitPath(field string) []string {
	if !strings.Contains(field, ".") {
		return []string{field}
	}
	return strings.Split(field, ".")
}

// throughReferences wraps a condition in the relationships its path walks, so
// "Runs on".Tier = "1" asks the objects this one runs on about their tier.
func throughReferences(path []string, inner node) node {
	for index := len(path) - 2; index >= 0; index-- {
		inner = referenceNode{relationship: path[index], inner: inner}
	}
	return inner
}

// isObjectTypeField reports the names AQL gives an object's own type.
func isObjectTypeField(field string) bool {
	switch strings.ToLower(field) {
	case "objecttype", "type", "schema":
		return true
	}
	return false
}

// takeFunction reads a function call where a value was expected, such as
// objectTypeAndChildren("Servers").
func (p *parser) takeFunction() (string, []string, bool) {
	if p.done() || p.peek().kind != tokenWord || p.at+1 >= len(p.tokens) || p.tokens[p.at+1].kind != tokenOpen {
		return "", nil, false
	}
	name := p.tokens[p.at].text
	at := p.at + 2
	arguments := []string{}
	for at < len(p.tokens) {
		switch p.tokens[at].kind {
		case tokenWord, tokenString:
			arguments = append(arguments, p.tokens[at].text)
			at++
		case tokenComma:
			at++
		case tokenClose:
			p.at = at + 1
			return name, arguments, true
		default:
			return "", nil, false
		}
	}
	return "", nil, false
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
