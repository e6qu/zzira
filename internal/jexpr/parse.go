// Package jexpr implements Jira expressions: a JavaScript-like expression
// language evaluated against Jira data, with Jira's step, expensive operation,
// bean and primitive value limits, and the syntax, type and complexity checks
// of Jira's expression analysis.
package jexpr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Pos is a location in an expression; lines and columns count from 1.
type Pos struct {
	Offset int
	Line   int
	Column int
}

// SyntaxError reports an expression that cannot be parsed.
type SyntaxError struct {
	Pos     Pos
	Message string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("Line %d, column %d: %s", e.Pos.Line, e.Pos.Column, e.Message)
}

// ---- AST ----

type span struct {
	start Pos
	end   int
}

func (s span) Span() span { return s }

// Node is a parsed expression.
type Node interface{ Span() span }

type Literal struct {
	span
	Value Value
}

type Ident struct {
	span
	Name string
}

type Member struct {
	span
	Object   Node
	Name     string
	Optional bool
}

type Index struct {
	span
	Object   Node
	Key      Node
	Optional bool
}

type Call struct {
	span
	Callee   Node
	Args     []Node
	Optional bool
}

type Unary struct {
	span
	Op string
	X  Node
}

type Binary struct {
	span
	Op   string
	L, R Node
}

type Conditional struct {
	span
	Test, Then, Else Node
}

type Arrow struct {
	span
	Params []string
	Body   Node
}

type ArrayLit struct {
	span
	Elements []Node
}

type Property struct {
	Key    string
	Value  Node
	Spread bool
}

type ObjectLit struct {
	span
	Properties []Property
}

type Template struct {
	span
	Strings []string
	Exprs   []Node
}

type New struct {
	span
	Name string
	Args []Node
}

type Spread struct {
	span
	X Node
}

// ---- lexer ----

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokString
	tokTemplate
	tokIdent
	tokPunct
)

type templatePart struct {
	literal string
	expr    string
	pos     Pos
	isExpr  bool
}

type token struct {
	kind  tokenKind
	text  string
	num   float64
	str   string
	parts []templatePart
	pos   Pos
	end   int
}

var punctuators = []string{"===", "!==", "...", "?.", "??", "=>", "==", "!=", "<=", ">=", "&&", "||",
	"(", ")", "[", "]", "{", "}", ",", ".", ":", "?", "!", "<", ">", "+", "-", "*", "/", "%"}

type lexer struct {
	src  string
	off  int
	line int
	col  int
	base Pos
}

func (l *lexer) pos() Pos {
	return Pos{Offset: l.base.Offset + l.off, Line: l.line, Column: l.col}
}

func (l *lexer) peekRune() (rune, int) {
	if l.off >= len(l.src) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(l.src[l.off:])
}

func (l *lexer) advance() rune {
	r, size := l.peekRune()
	l.off += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func lex(src string, base Pos) ([]token, error) {
	l := &lexer{src: src, line: base.Line, col: base.Column, base: base}
	var tokens []token
	for {
		for l.off < len(l.src) {
			r, _ := l.peekRune()
			if unicode.IsSpace(r) {
				l.advance()
				continue
			}
			if strings.HasPrefix(l.src[l.off:], "//") {
				for l.off < len(l.src) {
					if r, _ := l.peekRune(); r == '\n' {
						break
					}
					l.advance()
				}
				continue
			}
			if strings.HasPrefix(l.src[l.off:], "/*") {
				start := l.pos()
				l.advance()
				l.advance()
				closed := false
				for l.off < len(l.src) {
					if strings.HasPrefix(l.src[l.off:], "*/") {
						l.advance()
						l.advance()
						closed = true
						break
					}
					l.advance()
				}
				if !closed {
					return nil, &SyntaxError{Pos: start, Message: "Unterminated comment."}
				}
				continue
			}
			break
		}
		start := l.pos()
		if l.off >= len(l.src) {
			tokens = append(tokens, token{kind: tokEOF, text: "EOF", pos: start, end: l.base.Offset + l.off})
			return tokens, nil
		}
		r, _ := l.peekRune()
		rest := l.src[l.off:]
		switch {
		case r >= '0' && r <= '9' || (r == '.' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9'):
			begin := l.off
			for l.off < len(l.src) {
				c := l.src[l.off]
				if c >= '0' && c <= '9' || c == '.' {
					l.advance()
					continue
				}
				if (c == 'e' || c == 'E') && l.off+1 < len(l.src) {
					l.advance()
					if next := l.src[l.off]; next == '+' || next == '-' {
						l.advance()
					}
					continue
				}
				break
			}
			text := l.src[begin:l.off]
			number, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, &SyntaxError{Pos: start, Message: "Invalid number " + strconv.Quote(text) + "."}
			}
			tokens = append(tokens, token{kind: tokNumber, text: text, num: number, pos: start, end: l.base.Offset + l.off})
		case r == '\'' || r == '"':
			quote := l.advance()
			var b strings.Builder
			closed := false
			for l.off < len(l.src) {
				c := l.advance()
				if c == quote {
					closed = true
					break
				}
				if c == '\\' {
					escaped, err := l.escape()
					if err != nil {
						return nil, err
					}
					b.WriteString(escaped)
					continue
				}
				b.WriteRune(c)
			}
			if !closed {
				return nil, &SyntaxError{Pos: start, Message: "Unterminated string literal."}
			}
			tokens = append(tokens, token{kind: tokString, text: l.src[start.Offset-l.base.Offset : l.off], str: b.String(), pos: start, end: l.base.Offset + l.off})
		case r == '`':
			l.advance()
			parts, err := l.template(start)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokTemplate, text: "TEMPLATE_LITERAL", parts: parts, pos: start, end: l.base.Offset + l.off})
		case r == '_' || r == '$' || unicode.IsLetter(r):
			begin := l.off
			for l.off < len(l.src) {
				c, _ := l.peekRune()
				if c == '_' || c == '$' || unicode.IsLetter(c) || unicode.IsDigit(c) {
					l.advance()
					continue
				}
				break
			}
			tokens = append(tokens, token{kind: tokIdent, text: l.src[begin:l.off], pos: start, end: l.base.Offset + l.off})
		default:
			matched := ""
			for _, punct := range punctuators {
				if strings.HasPrefix(rest, punct) {
					// "?." followed by a digit is a conditional followed by a number.
					if punct == "?." && len(rest) > 2 && rest[2] >= '0' && rest[2] <= '9' {
						continue
					}
					matched = punct
					break
				}
			}
			if matched == "" {
				return nil, &SyntaxError{Pos: start, Message: "Unexpected character " + strconv.QuoteRune(r) + "."}
			}
			for range matched {
				l.advance()
			}
			tokens = append(tokens, token{kind: tokPunct, text: matched, pos: start, end: l.base.Offset + l.off})
		}
	}
}

func (l *lexer) escape() (string, error) {
	start := l.pos()
	if l.off >= len(l.src) {
		return "", &SyntaxError{Pos: start, Message: "Unterminated escape sequence."}
	}
	c := l.advance()
	switch c {
	case 'n':
		return "\n", nil
	case 't':
		return "\t", nil
	case 'r':
		return "\r", nil
	case 'b':
		return "\b", nil
	case 'f':
		return "\f", nil
	case 'u':
		if l.off+4 > len(l.src) {
			return "", &SyntaxError{Pos: start, Message: "Invalid unicode escape."}
		}
		var code rune
		for _, digit := range l.src[l.off : l.off+4] {
			switch {
			case digit >= '0' && digit <= '9':
				code = code<<4 | (digit - '0')
			case digit >= 'a' && digit <= 'f':
				code = code<<4 | (digit - 'a' + 10)
			case digit >= 'A' && digit <= 'F':
				code = code<<4 | (digit - 'A' + 10)
			default:
				return "", &SyntaxError{Pos: start, Message: "Invalid unicode escape."}
			}
		}
		for range 4 {
			l.advance()
		}
		return string(code), nil
	default:
		return string(c), nil
	}
}

func (l *lexer) template(start Pos) ([]templatePart, error) {
	var parts []templatePart
	var literal strings.Builder
	for l.off < len(l.src) {
		if strings.HasPrefix(l.src[l.off:], "${") {
			parts = append(parts, templatePart{literal: literal.String()})
			literal.Reset()
			l.advance()
			l.advance()
			exprStart := l.pos()
			begin := l.off
			depth := 0
			for {
				if l.off >= len(l.src) {
					return nil, &SyntaxError{Pos: start, Message: "Unterminated template literal."}
				}
				c, _ := l.peekRune()
				if c == '\'' || c == '"' {
					quote := l.advance()
					for l.off < len(l.src) {
						inner := l.advance()
						if inner == '\\' && l.off < len(l.src) {
							l.advance()
							continue
						}
						if inner == quote {
							break
						}
					}
					continue
				}
				if c == '{' {
					depth++
				}
				if c == '}' {
					if depth == 0 {
						break
					}
					depth--
				}
				l.advance()
			}
			parts = append(parts, templatePart{expr: l.src[begin:l.off], pos: exprStart, isExpr: true})
			l.advance()
			continue
		}
		c := l.advance()
		if c == '`' {
			parts = append(parts, templatePart{literal: literal.String()})
			return parts, nil
		}
		if c == '\\' {
			escaped, err := l.escape()
			if err != nil {
				return nil, err
			}
			literal.WriteString(escaped)
			continue
		}
		literal.WriteRune(c)
	}
	return nil, &SyntaxError{Pos: start, Message: "Unterminated template literal."}
}

// ---- parser ----

type parser struct {
	tokens []token
	i      int
}

// Parse parses an expression.
func Parse(src string) (Node, error) {
	return parseAt(src, Pos{Offset: 0, Line: 1, Column: 1})
}

func parseAt(src string, base Pos) (Node, error) {
	tokens, err := lex(src, base)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	if p.peek().kind == tokEOF {
		return nil, p.expected(primaryExpectation)
	}
	node, err := p.expression()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, p.expected("EOF")
	}
	return node, nil
}

const primaryExpectation = "!, -, typeof, (, IDENTIFIER, null, true, false, NUMBER, STRING, TEMPLATE_LITERAL, new, [ or {"

func (p *parser) peek() token { return p.tokens[p.i] }
func (p *parser) peekAt(n int) token {
	if p.i+n < len(p.tokens) {
		return p.tokens[p.i+n]
	}
	return p.tokens[len(p.tokens)-1]
}
func (p *parser) next() token {
	t := p.tokens[p.i]
	if p.i < len(p.tokens)-1 {
		p.i++
	}
	return t
}

func (p *parser) isPunct(text string) bool {
	t := p.peek()
	return t.kind == tokPunct && t.text == text
}

func (p *parser) isWord(word string) bool {
	t := p.peek()
	return t.kind == tokIdent && t.text == word
}

func (p *parser) expected(what string) error {
	t := p.peek()
	return &SyntaxError{Pos: t.pos, Message: what + " expected, " + t.text + " encountered."}
}

func (p *parser) expect(text string) (token, error) {
	if !p.isPunct(text) {
		return token{}, p.expected(text)
	}
	return p.next(), nil
}

func (p *parser) last() token { return p.tokens[max(p.i-1, 0)] }

func spanFrom(start Pos, end token) span { return span{start: start, end: end.end} }

func (p *parser) expression() (Node, error) {
	if arrow, ok, err := p.arrow(); ok || err != nil {
		return arrow, err
	}
	return p.conditional()
}

// arrow parses x => body and (a, b) => body.
func (p *parser) arrow() (Node, bool, error) {
	start := p.peek()
	if start.kind == tokIdent && p.peekAt(1).kind == tokPunct && p.peekAt(1).text == "=>" {
		p.next()
		p.next()
		body, err := p.expression()
		if err != nil {
			return nil, true, err
		}
		return &Arrow{span: span{start: start.pos, end: body.Span().end}, Params: []string{start.text}, Body: body}, true, nil
	}
	if !p.isPunct("(") {
		return nil, false, nil
	}
	depth := 0
	closeAt := -1
	for j := p.i; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.kind == tokEOF {
			break
		}
		if t.kind == tokPunct && t.text == "(" {
			depth++
		}
		if t.kind == tokPunct && t.text == ")" {
			depth--
			if depth == 0 {
				closeAt = j
				break
			}
		}
	}
	if closeAt < 0 || closeAt+1 >= len(p.tokens) || p.tokens[closeAt+1].kind != tokPunct || p.tokens[closeAt+1].text != "=>" {
		return nil, false, nil
	}
	p.next()
	params := []string{}
	for !p.isPunct(")") {
		name := p.peek()
		if name.kind != tokIdent {
			return nil, true, p.expected("IDENTIFIER")
		}
		p.next()
		params = append(params, name.text)
		if p.isPunct(",") {
			p.next()
			continue
		}
		if !p.isPunct(")") {
			return nil, true, p.expected(", or )")
		}
	}
	p.next()
	p.next()
	body, err := p.expression()
	if err != nil {
		return nil, true, err
	}
	return &Arrow{span: span{start: start.pos, end: body.Span().end}, Params: params, Body: body}, true, nil
}

func (p *parser) conditional() (Node, error) {
	test, err := p.nullish()
	if err != nil {
		return nil, err
	}
	if !p.isPunct("?") {
		return test, nil
	}
	p.next()
	then, err := p.expression()
	if err != nil {
		return nil, err
	}
	if _, err = p.expect(":"); err != nil {
		return nil, err
	}
	otherwise, err := p.expression()
	if err != nil {
		return nil, err
	}
	return &Conditional{span: span{start: test.Span().start, end: otherwise.Span().end}, Test: test, Then: then, Else: otherwise}, nil
}

func (p *parser) binaryLevel(operators []string, operand func() (Node, error)) (Node, error) {
	left, err := operand()
	if err != nil {
		return nil, err
	}
	for {
		matched := ""
		for _, op := range operators {
			if p.isPunct(op) {
				matched = op
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		p.next()
		right, err := operand()
		if err != nil {
			return nil, err
		}
		left = &Binary{span: span{start: left.Span().start, end: right.Span().end}, Op: matched, L: left, R: right}
	}
}

func (p *parser) nullish() (Node, error) {
	return p.binaryLevel([]string{"??"}, p.or)
}
func (p *parser) or() (Node, error) { return p.binaryLevel([]string{"||"}, p.and) }
func (p *parser) and() (Node, error) {
	return p.binaryLevel([]string{"&&"}, p.equality)
}
func (p *parser) equality() (Node, error) {
	return p.binaryLevel([]string{"===", "!==", "==", "!="}, p.relational)
}
func (p *parser) relational() (Node, error) {
	return p.binaryLevel([]string{"<=", ">=", "<", ">"}, p.additive)
}
func (p *parser) additive() (Node, error) {
	return p.binaryLevel([]string{"+", "-"}, p.multiplicative)
}
func (p *parser) multiplicative() (Node, error) {
	return p.binaryLevel([]string{"*", "/", "%"}, p.unary)
}

func (p *parser) unary() (Node, error) {
	start := p.peek()
	if start.kind == tokPunct && (start.text == "!" || start.text == "-" || start.text == "+") || p.isWord("typeof") {
		p.next()
		operand, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &Unary{span: span{start: start.pos, end: operand.Span().end}, Op: start.text, X: operand}, nil
	}
	return p.postfix()
}

func (p *parser) arguments() ([]Node, error) {
	if _, err := p.expect("("); err != nil {
		return nil, err
	}
	args := []Node{}
	for !p.isPunct(")") {
		arg, err := p.element()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.isPunct(",") {
			p.next()
			continue
		}
		if !p.isPunct(")") {
			return nil, p.expected(", or )")
		}
	}
	p.next()
	return args, nil
}

func (p *parser) element() (Node, error) {
	if p.isPunct("...") {
		start := p.next()
		inner, err := p.expression()
		if err != nil {
			return nil, err
		}
		return &Spread{span: span{start: start.pos, end: inner.Span().end}, X: inner}, nil
	}
	return p.expression()
}

func (p *parser) postfix() (Node, error) {
	node, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		start := node.Span().start
		switch {
		case p.isPunct("."):
			p.next()
			name := p.peek()
			if name.kind != tokIdent {
				return nil, p.expected("IDENTIFIER")
			}
			p.next()
			node = &Member{span: spanFrom(start, name), Object: node, Name: name.text}
		case p.isPunct("?."):
			p.next()
			switch {
			case p.isPunct("("):
				args, err := p.arguments()
				if err != nil {
					return nil, err
				}
				node = &Call{span: spanFrom(start, p.last()), Callee: node, Args: args, Optional: true}
			case p.isPunct("["):
				p.next()
				key, err := p.expression()
				if err != nil {
					return nil, err
				}
				closing, err := p.expect("]")
				if err != nil {
					return nil, err
				}
				node = &Index{span: spanFrom(start, closing), Object: node, Key: key, Optional: true}
			default:
				name := p.peek()
				if name.kind != tokIdent {
					return nil, p.expected("IDENTIFIER")
				}
				p.next()
				node = &Member{span: spanFrom(start, name), Object: node, Name: name.text, Optional: true}
			}
		case p.isPunct("["):
			p.next()
			key, err := p.expression()
			if err != nil {
				return nil, err
			}
			closing, err := p.expect("]")
			if err != nil {
				return nil, err
			}
			node = &Index{span: spanFrom(start, closing), Object: node, Key: key}
		case p.isPunct("("):
			args, err := p.arguments()
			if err != nil {
				return nil, err
			}
			node = &Call{span: spanFrom(start, p.last()), Callee: node, Args: args}
		default:
			return node, nil
		}
	}
}

func (p *parser) primary() (Node, error) {
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.next()
		return &Literal{span: spanFrom(t.pos, t), Value: t.num}, nil
	case tokString:
		p.next()
		return &Literal{span: spanFrom(t.pos, t), Value: t.str}, nil
	case tokTemplate:
		p.next()
		template := &Template{span: spanFrom(t.pos, t)}
		for _, part := range t.parts {
			if !part.isExpr {
				template.Strings = append(template.Strings, part.literal)
				continue
			}
			expr, err := parseAt(part.expr, part.pos)
			if err != nil {
				return nil, err
			}
			template.Exprs = append(template.Exprs, expr)
		}
		return template, nil
	case tokIdent:
		switch t.text {
		case "true", "false":
			p.next()
			return &Literal{span: spanFrom(t.pos, t), Value: t.text == "true"}, nil
		case "null":
			p.next()
			return &Literal{span: spanFrom(t.pos, t), Value: nil}, nil
		case "new":
			p.next()
			name := p.peek()
			if name.kind != tokIdent {
				return nil, p.expected("IDENTIFIER")
			}
			p.next()
			args := []Node{}
			if p.isPunct("(") {
				var err error
				if args, err = p.arguments(); err != nil {
					return nil, err
				}
			}
			return &New{span: spanFrom(t.pos, p.last()), Name: name.text, Args: args}, nil
		}
		p.next()
		return &Ident{span: spanFrom(t.pos, t), Name: t.text}, nil
	case tokPunct:
		switch t.text {
		case "(":
			p.next()
			inner, err := p.expression()
			if err != nil {
				return nil, err
			}
			if _, err = p.expect(")"); err != nil {
				return nil, err
			}
			return inner, nil
		case "[":
			p.next()
			array := &ArrayLit{Elements: []Node{}}
			for !p.isPunct("]") {
				element, err := p.element()
				if err != nil {
					return nil, err
				}
				array.Elements = append(array.Elements, element)
				if p.isPunct(",") {
					p.next()
					continue
				}
				if !p.isPunct("]") {
					return nil, p.expected(", or ]")
				}
			}
			closing := p.next()
			array.span = spanFrom(t.pos, closing)
			return array, nil
		case "{":
			p.next()
			object := &ObjectLit{}
			for !p.isPunct("}") {
				if p.isPunct("...") {
					p.next()
					inner, err := p.expression()
					if err != nil {
						return nil, err
					}
					object.Properties = append(object.Properties, Property{Value: inner, Spread: true})
				} else {
					key := p.peek()
					var name string
					switch key.kind {
					case tokIdent:
						name = key.text
					case tokString:
						name = key.str
					case tokNumber:
						name = formatNumber(key.num)
					default:
						return nil, p.expected("IDENTIFIER, STRING or NUMBER")
					}
					p.next()
					if p.isPunct(":") {
						p.next()
						value, err := p.expression()
						if err != nil {
							return nil, err
						}
						object.Properties = append(object.Properties, Property{Key: name, Value: value})
					} else if key.kind == tokIdent {
						object.Properties = append(object.Properties, Property{Key: name, Value: &Ident{span: spanFrom(key.pos, key), Name: name}})
					} else {
						return nil, p.expected(":")
					}
				}
				if p.isPunct(",") {
					p.next()
					continue
				}
				if !p.isPunct("}") {
					return nil, p.expected(", or }")
				}
			}
			closing := p.next()
			object.span = spanFrom(t.pos, closing)
			return object, nil
		}
	}
	return nil, p.expected(primaryExpectation)
}
