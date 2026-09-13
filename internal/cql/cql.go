// Package cql is the Confluence Query Language engine: a hand-rolled
// recursive-descent parser and a PostgreSQL compiler. It is pure, so the server
// and the WebAssembly replica share it.
//
// The compiler emits predicates against a normalised view of everything that
// can be searched — pages, blog posts, comments, attachments and spaces reduced
// to one set of columns. The caller builds that view with its own visibility
// rules already applied, so a query can never widen what a reader may see.
//
// Grammar (keywords are case-insensitive):
//
//	query   := orExpr [ ORDER BY field [ASC|DESC] (, field [ASC|DESC])* ]
//	orExpr  := andExpr ( OR andExpr )*
//	andExpr := unit ( AND unit )*
//	unit    := NOT unit | '(' orExpr ')' | clause
//	clause  := field op value | field [NOT] IN '(' value (, value)* ')'
//	         | field [NOT] IN function
//	op      := = | != | ~ | !~ | < | <= | > | >=
package cql

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- AST ----

// Query is a parsed CQL string.
type Query struct {
	Root   Node
	Orders []Order
}

// Order is one ORDER BY term.
type Order struct {
	Field string
	Desc  bool
}

// Node is one part of the boolean tree.
type Node interface{ isNode() }

// Or matches when any term matches.
type Or struct{ Terms []Node }

// And matches when every term matches.
type And struct{ Terms []Node }

// Not inverts its inner node.
type Not struct{ Inner Node }

// Clause is one field, one operator and the values compared against it.
type Clause struct {
	Field  string
	Op     string
	Values []Value
}

// Value is either a literal or a call. A call is resolved at compile time,
// because what it means depends on who is asking and when.
type Value struct {
	Literal   string
	Function  string
	Arguments []string
}

func (Or) isNode()     {}
func (And) isNode()    {}
func (Not) isNode()    {}
func (Clause) isNode() {}

// ---- Errors ----

// SyntaxError is a query that could not be read.
type SyntaxError struct {
	Pos int
	Msg string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("CQL syntax error at %d: %s", e.Pos, e.Msg) }

// SemanticError is a query that was read but asks for something CQL does not
// mean here — an unknown field, or an operator a field does not take.
type SemanticError struct{ Msg string }

func (e *SemanticError) Error() string { return "CQL error: " + e.Msg }

// ---- Lexer ----

type token struct {
	kind string // word, quoted, lparen, rparen, comma, eof
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{"lparen", "(", i})
			i++
		case c == ')':
			out = append(out, token{"rparen", ")", i})
			i++
		case c == ',':
			out = append(out, token{"comma", ",", i})
			i++
		case strings.ContainsRune("=!~<>", rune(c)):
			if i+1 < len(src) {
				two := src[i : i+2]
				if two == "!=" || two == "!~" || two == ">=" || two == "<=" {
					out = append(out, token{"word", two, i})
					i += 2
					continue
				}
			}
			out = append(out, token{"word", string(c), i})
			i++
		case c == '\'' || c == '"':
			j := i + 1
			var b strings.Builder
			closed := false
			for j < len(src) {
				if src[j] == '\\' && j+1 < len(src) {
					b.WriteByte(src[j+1])
					j += 2
					continue
				}
				if src[j] == c {
					closed = true
					break
				}
				b.WriteByte(src[j])
				j++
			}
			if !closed {
				return nil, &SyntaxError{i, "unterminated string"}
			}
			out = append(out, token{"quoted", b.String(), i})
			i = j + 1
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\n\r(),'\"=!~<>", rune(src[j])) {
				j++
			}
			out = append(out, token{"word", src[i:j], i})
			i = j
		}
	}
	out = append(out, token{"eof", "", len(src)})
	return out, nil
}

// ---- Parser ----

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) atEnd() bool { return p.peek().kind == "eof" }
func (p *parser) at(w string) bool {
	t := p.peek()
	return t.kind == "word" && strings.EqualFold(t.text, w)
}

// Parse reads a CQL string. An empty query matches everything, which is what
// Confluence does with a bare search.
func Parse(src string) (*Query, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	main, orderToks := splitOrderBy(toks)
	p := &parser{toks: main}
	query := &Query{Root: And{}}
	if !p.atEnd() {
		root, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.atEnd() {
			return nil, &SyntaxError{p.peek().pos, "unexpected " + strconv.Quote(p.peek().text)}
		}
		query.Root = root
	}
	if orderToks == nil {
		return query, nil
	}
	op := &parser{toks: orderToks}
	for {
		field := op.peek()
		if field.kind != "word" && field.kind != "quoted" {
			return nil, &SyntaxError{field.pos, "expected a field after ORDER BY"}
		}
		op.next()
		order := Order{Field: strings.ToLower(field.text)}
		if op.at("desc") {
			order.Desc = true
			op.next()
		} else if op.at("asc") {
			op.next()
		}
		query.Orders = append(query.Orders, order)
		if op.peek().kind != "comma" {
			break
		}
		op.next()
	}
	if !op.atEnd() {
		return nil, &SyntaxError{op.peek().pos, "unexpected input after the ORDER BY fields"}
	}
	return query, nil
}

// splitOrderBy cuts the trailing ORDER BY, which binds to the whole query
// rather than to the term it follows.
func splitOrderBy(toks []token) (main, order []token) {
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].kind == "word" && strings.EqualFold(toks[i].text, "order") &&
			toks[i+1].kind == "word" && strings.EqualFold(toks[i+1].text, "by") {
			main = append(append([]token{}, toks[:i]...), token{"eof", "", toks[i].pos})
			order = append([]token{}, toks[i+2:]...)
			return main, order
		}
	}
	return toks, nil
}

func (p *parser) parseOr() (Node, error) {
	terms := []Node{}
	for {
		term, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
		if !p.at("or") {
			break
		}
		p.next()
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return Or{Terms: terms}, nil
}

func (p *parser) parseAnd() (Node, error) {
	terms := []Node{}
	for {
		term, err := p.parseUnit()
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
		if !p.at("and") {
			break
		}
		p.next()
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return And{Terms: terms}, nil
}

func (p *parser) parseUnit() (Node, error) {
	if p.at("not") {
		p.next()
		inner, err := p.parseUnit()
		if err != nil {
			return nil, err
		}
		return Not{Inner: inner}, nil
	}
	if p.peek().kind == "lparen" {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != "rparen" {
			return nil, &SyntaxError{p.peek().pos, "expected a closing bracket"}
		}
		p.next()
		return inner, nil
	}
	return p.parseClause()
}

func (p *parser) parseClause() (Node, error) {
	field := p.peek()
	if field.kind != "word" && field.kind != "quoted" {
		return nil, &SyntaxError{field.pos, "expected a field name"}
	}
	p.next()
	clause := Clause{Field: strings.ToLower(field.text)}

	negated := false
	if p.at("not") {
		negated = true
		p.next()
		if !p.at("in") {
			return nil, &SyntaxError{p.peek().pos, "expected IN after NOT"}
		}
	}
	if p.at("in") {
		p.next()
		clause.Op = "in"
		if negated {
			clause.Op = "not in"
		}
		values, err := p.parseValueList()
		if err != nil {
			return nil, err
		}
		clause.Values = values
		return clause, nil
	}

	operator := p.peek()
	if operator.kind != "word" {
		return nil, &SyntaxError{operator.pos, "expected an operator"}
	}
	switch operator.text {
	case "=", "!=", "~", "!~", "<", "<=", ">", ">=":
		clause.Op = operator.text
	default:
		return nil, &SyntaxError{operator.pos, "unknown operator " + strconv.Quote(operator.text)}
	}
	p.next()
	value, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	clause.Values = []Value{value}
	return clause, nil
}

// parseValueList reads either a bracketed list or a bare function call, both of
// which CQL accepts on the right of IN.
func (p *parser) parseValueList() ([]Value, error) {
	if p.peek().kind != "lparen" {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if value.Function == "" {
			return nil, &SyntaxError{p.peek().pos, "expected a bracketed list after IN"}
		}
		return []Value{value}, nil
	}
	p.next()
	values := []Value{}
	for {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		if p.peek().kind == "comma" {
			p.next()
			continue
		}
		break
	}
	if p.peek().kind != "rparen" {
		return nil, &SyntaxError{p.peek().pos, "expected a closing bracket"}
	}
	p.next()
	if len(values) == 0 {
		return nil, &SyntaxError{p.peek().pos, "expected at least one value"}
	}
	return values, nil
}

func (p *parser) parseValue() (Value, error) {
	t := p.peek()
	if t.kind == "quoted" {
		p.next()
		return Value{Literal: t.text}, nil
	}
	if t.kind != "word" {
		return Value{}, &SyntaxError{t.pos, "expected a value"}
	}
	p.next()
	if p.peek().kind != "lparen" {
		return Value{Literal: t.text}, nil
	}
	// A word followed immediately by a bracket is a call.
	p.next()
	value := Value{Function: strings.ToLower(t.text)}
	if p.peek().kind == "rparen" {
		p.next()
		return value, nil
	}
	for {
		argument := p.peek()
		if argument.kind != "word" && argument.kind != "quoted" {
			return Value{}, &SyntaxError{argument.pos, "expected an argument"}
		}
		p.next()
		value.Arguments = append(value.Arguments, argument.text)
		if p.peek().kind == "comma" {
			p.next()
			continue
		}
		break
	}
	if p.peek().kind != "rparen" {
		return Value{}, &SyntaxError{p.peek().pos, "expected a closing bracket"}
	}
	p.next()
	return value, nil
}
