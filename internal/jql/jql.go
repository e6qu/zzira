// Package jql is the V1 JQL engine: a hand-rolled recursive-descent parser and
// a Postgres SQL compiler with mandatory permission predicates injected by the
// caller. Pure — shared by server and wasm targets.
//
// Grammar (case-insensitive keywords, unquoted/quoted values):
//
//	query   := orExpr [ ORDER BY field [ASC|DESC] (, field [ASC|DESC])* ]
//	orExpr  := andExpr ( OR andExpr )*
//	andExpr := unit ( AND unit )*
//	unit    := NOT unit | '(' query ')' | field op value | field history | text
//	op      := = | != | ~ | !~ | < | <= | > | >= | [NOT] IN (...) | IS [NOT] EMPTY
//	history := WAS [NOT] [IN] value | CHANGED [FROM value] [TO value]
//	           [BY value] [BEFORE value] [AFTER value] [DURING (value,value)]
package jql

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// ---- AST ----

type Query struct {
	Root    Node
	OrderBy *Order
	Orders  []Order
}

type Order struct {
	Field string
	Desc  bool
}

type Node interface{ isNode() }

type Or struct{ Terms []Node }
type And struct{ Terms []Node }
type Not struct{ Inner Node }

type Clause struct {
	Field  string
	Op     string // = != ~ !~ in empty notempty
	Values []string
}

type HistoryPredicate struct {
	Kind   string
	Values []string
}

type HistoryClause struct {
	Field      string
	Op         string
	Values     []string
	Predicates []HistoryPredicate
}

type Text struct{ Value string }

// FunctionInvocation identifies a function used as the complete right-hand
// side of a clause. Custom Jira functions replace that whole clause with the
// JQL fragment returned by their app.
type FunctionInvocation struct {
	Field, Operator, Name string
	Arguments             []string
}

func (Or) isNode()            {}
func (And) isNode()           {}
func (Not) isNode()           {}
func (Clause) isNode()        {}
func (HistoryClause) isNode() {}
func (Text) isNode()          {}

// TransformClauseFunctions walks a parsed query and replaces recognized
// function clauses. The callback returns handled=false for built-in functions.
// History predicates are intentionally excluded because Jira app functions
// are value functions used in ordinary terminal clauses.
func TransformClauseFunctions(query *Query, transform func(FunctionInvocation) (Node, bool, error)) error {
	root, err := transformClauseFunctionNode(query.Root, transform)
	if err != nil {
		return err
	}
	query.Root = root
	return nil
}

func transformClauseFunctionNode(node Node, transform func(FunctionInvocation) (Node, bool, error)) (Node, error) {
	switch value := node.(type) {
	case Or:
		for index, term := range value.Terms {
			replacement, err := transformClauseFunctionNode(term, transform)
			if err != nil {
				return nil, err
			}
			value.Terms[index] = replacement
		}
		return value, nil
	case And:
		for index, term := range value.Terms {
			replacement, err := transformClauseFunctionNode(term, transform)
			if err != nil {
				return nil, err
			}
			value.Terms[index] = replacement
		}
		return value, nil
	case Not:
		replacement, err := transformClauseFunctionNode(value.Inner, transform)
		if err != nil {
			return nil, err
		}
		value.Inner = replacement
		return value, nil
	case Clause:
		if len(value.Values) != 1 {
			return value, nil
		}
		name, arguments, ok := splitFunction(value.Values[0])
		if !ok {
			return value, nil
		}
		replacement, handled, err := transform(FunctionInvocation{Field: value.Field, Operator: value.Op, Name: name, Arguments: arguments})
		if err != nil {
			return nil, err
		}
		if handled {
			return replacement, nil
		}
		return value, nil
	default:
		return value, nil
	}
}

// ---- Errors ----

type SyntaxError struct {
	Pos int
	Msg string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("JQL syntax error at %d: %s", e.Pos, e.Msg) }

// ValueError is a value a field cannot hold. The query parses; it just names
// something that is not there. Jira answers it with this exact sentence, so
// clients that show the message show Jira's.
type ValueError struct {
	Field string
	Value string
}

// QueryMessage is how a client is told a query failed: Jira heads a syntax
// error with "Error in the JQL Query", and answers a value that does not
// exist with a sentence of its own, which is shown as it is.
func QueryMessage(message string) string {
	if strings.HasPrefix(message, "The value '") {
		return message
	}
	return "Error in the JQL Query: " + message
}

func (e *ValueError) Error() string {
	return "The value '" + e.Value + "' does not exist for the field '" + e.Field + "'."
}

// ---- Lexer ----

type token struct {
	kind string // word, quoted, lparen, rparen, comma, eof
	text string
	pos  int
	end  int // offset just past the token in the source, closing quote included
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			out = append(out, token{"lparen", "(", i, i + 1})
			i++
		case c == ')':
			out = append(out, token{"rparen", ")", i, i + 1})
			i++
		case c == ',':
			out = append(out, token{"comma", ",", i, i + 1})
			i++
		case strings.ContainsRune("=!~<>", rune(c)):
			if i+1 < len(src) {
				two := string(c) + string(src[i+1])
				if two == "!=" || two == "!~" || two == "~=" || two == ">=" || two == "<=" {
					out = append(out, token{"word", two, i, i + 2})
					i += 2
					continue
				}
			}
			out = append(out, token{"word", string(c), i, i + 1})
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
			out = append(out, token{"quoted", b.String(), i, j + 1})
			i = j + 1
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\n()',=!~<>", rune(src[j])) {
				j++
			}
			out = append(out, token{"word", src[i:j], i, j})
			i = j
		}
	}
	out = append(out, token{"eof", "", len(src), len(src)})
	return out, nil
}

// ---- Parser ----

var operators = map[string]string{
	"=": "=", "!=": "!=", "~": "~", "~=": "~=", "!~": "!~", ">": ">", ">=": ">=", "<": "<", "<=": "<=",
}

type parser struct {
	toks []token
	pos  int
	// collect records each clause's field and operands with where they sit in
	// the source, for rewriting a query without reformatting it.
	collect  bool
	operands []Operand
	fields   []FieldReference
	clause   Operand
	call     int
}

// Operand is one value a clause compares with and where it sits in the query.
// Role is "value" for the clause's own values, or the history predicate the
// value belongs to: by, from, to, before, after or during.
type Operand struct {
	Field, Operator, Role, Value      string
	Start, End                        int
	OperatorStart, OperatorEnd        int
	Quoted, Function, InParenthesized bool
}

// FieldReference is a clause's field name and where it sits in the query.
type FieldReference struct {
	Name       string
	Start, End int
}

// Operands parses a query and lists its clauses' fields and operands, in
// source order. Function arguments and ORDER BY fields are not operands.
func Operands(src string) ([]Operand, []FieldReference, error) {
	if _, err := Parse(src); err != nil {
		return nil, nil, err
	}
	toks, err := lex(src)
	if err != nil {
		return nil, nil, err
	}
	main, _ := splitOrderClause(toks)
	p := &parser{toks: main, collect: true}
	if p.peek().kind != "eof" {
		if _, err = p.parseOr(); err != nil {
			return nil, nil, err
		}
	}
	return p.operands, p.fields, nil
}

// Edit replaces the source between Start and End with Text.
type Edit struct {
	Start, End int
	Text       string
}

// ApplyEdits rewrites non-overlapping spans of a query, leaving the rest of
// its text as written.
func ApplyEdits(src string, edits []Edit) string {
	ordered := append([]Edit(nil), edits...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start > ordered[j].Start })
	for _, edit := range ordered {
		src = src[:edit.Start] + edit.Text + src[edit.End:]
	}
	return src
}

// Quote writes a value as a JQL operand: bare when it is a plain word, and
// double-quoted otherwise.
func Quote(value string) string {
	if value != "" && !reserved(strings.ToLower(value)) && !strings.ContainsAny(value, " \t\n()',=!~<>\"\\") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func (p *parser) record(t token, value string, end int, function bool) {
	if !p.collect || p.call > 0 || p.clause.Field == "" {
		return
	}
	operand := p.clause
	operand.Value, operand.Start, operand.End = value, t.pos, end
	operand.Quoted, operand.Function = t.kind == "quoted", function
	p.operands = append(p.operands, operand)
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) word() string {
	t := p.peek()
	if t.kind == "word" || t.kind == "quoted" {
		p.pos++
		return t.text
	}
	return ""
}

func (p *parser) expectWord(what string) (string, error) {
	if w := p.word(); w != "" {
		return w, nil
	}
	t := p.peek()
	return "", &SyntaxError{t.pos, "expected " + what}
}

func (p *parser) atWord(w string) bool {
	t := p.peek()
	return (t.kind == "word") && strings.EqualFold(t.text, w)
}

func Parse(src string) (*Query, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	main, orderToks := splitOrderClause(toks)
	p := &parser{toks: main}
	var root Node
	if p.peek().kind == "eof" {
		root = Text{Value: ""} // empty query = match all
	} else {
		root, err = p.parseOr()
		if err != nil {
			return nil, err
		}
	}
	q := &Query{Root: root}
	if orderToks != nil {
		op := &parser{toks: orderToks}
		for {
			field, err := op.expectWord("field after ORDER BY")
			if err != nil {
				return nil, err
			}
			order := Order{Field: canonicalField(field)}
			if op.atWord("desc") {
				order.Desc = true
				op.next()
			} else if op.atWord("asc") {
				op.next()
			}
			q.Orders = append(q.Orders, order)
			if len(q.Orders) > 7 {
				return nil, &SyntaxError{op.peek().pos, "ORDER BY accepts at most 7 fields"}
			}
			if op.peek().kind != "comma" {
				break
			}
			op.next()
		}
		if op.peek().kind != "eof" {
			return nil, &SyntaxError{op.peek().pos, "unexpected input after ORDER BY field"}
		}
		q.OrderBy = &q.Orders[0]
	}
	if p.peek().kind != "eof" {
		t := p.peek()
		return nil, &SyntaxError{t.pos, "unexpected trailing input: " + t.text}
	}
	return q, nil
}

// SetOrder replaces the top-level ORDER BY clause while preserving the user's
// query text. Quoted text and parenthesized expressions containing the words
// "order by" are not mistaken for the query's ordering clause.
func SetOrder(src, field string, desc bool) (string, error) {
	if _, err := Parse(src); err != nil {
		return "", err
	}
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	cut := len(src)
	depth := 0
	for i, token := range toks {
		switch token.kind {
		case "lparen":
			depth++
		case "rparen":
			depth--
		case "word":
			if depth == 0 && strings.EqualFold(token.text, "order") && i+1 < len(toks) && toks[i+1].kind == "word" && strings.EqualFold(toks[i+1].text, "by") {
				cut = token.pos
			}
		}
	}
	base := strings.TrimSpace(src[:cut])
	if base != "" {
		base += " "
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	return base + "ORDER BY " + field + " " + direction, nil
}

// splitOrderClause cuts the token stream at a depth-0 "ORDER BY".
func splitOrderClause(toks []token) (main, order []token) {
	depth := 0
	for i, t := range toks {
		switch t.kind {
		case "lparen":
			depth++
		case "rparen":
			depth--
		case "word":
			if depth == 0 && strings.EqualFold(t.text, "order") &&
				i+1 < len(toks) && toks[i+1].kind == "word" && strings.EqualFold(toks[i+1].text, "by") {
				mainToks := append(append([]token{}, toks[:i]...), token{"eof", "", toks[i].pos, toks[i].pos})
				return mainToks, toks[i+2:]
			}
		}
	}
	return toks, nil
}

func (p *parser) parseOr() (Node, error) {
	first, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	terms := []Node{first}
	for p.atWord("or") {
		p.next()
		t, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return Or{Terms: terms}, nil
}

func (p *parser) parseAnd() (Node, error) {
	first, err := p.parseUnit()
	if err != nil {
		return nil, err
	}
	terms := []Node{first}
	for {
		if p.atWord("and") {
			p.next()
		} else {
			// implicit AND before a new clause token
			t := p.peek()
			if t.kind == "word" && !p.atWord("or") && !p.atWord("order") {
				// lookahead: another unit starts
			} else if t.kind == "lparen" {
				// implicit AND with parenthesized group
			} else {
				break
			}
		}
		t, err := p.parseUnit()
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return And{Terms: terms}, nil
}

func (p *parser) parseUnit() (Node, error) {
	t := p.peek()
	if t.kind == "word" && strings.EqualFold(t.text, "not") {
		p.next()
		inner, err := p.parseUnit()
		if err != nil {
			return nil, err
		}
		return Not{Inner: inner}, nil
	}
	if t.kind == "lparen" {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != "rparen" {
			return nil, &SyntaxError{p.peek().pos, "expected )"}
		}
		p.next()
		return inner, nil
	}
	if t.kind == "quoted" {
		if p.isClauseStart() {
			return p.parseClause()
		}
		p.next()
		return Text{Value: t.text}, nil
	}
	if t.kind == "word" {
		// lookahead: field op value?
		if p.isClauseStart() {
			return p.parseClause()
		}
		if reserved(strings.ToLower(t.text)) {
			return nil, &SyntaxError{t.pos, "unexpected keyword " + t.text}
		}
		p.next()
		return Text{Value: t.text}, nil
	}
	return nil, &SyntaxError{t.pos, "expected clause"}
}

func reserved(w string) bool {
	switch w {
	case "order", "by", "asc", "desc", "and", "or", "not", "in", "is", "empty", "null",
		"was", "changed", "from", "to", "before", "after", "during":
		return true
	}
	return false
}

func (p *parser) isClauseStart() bool {
	if p.toks[p.pos+1].kind != "word" {
		return false
	}
	op := p.toks[p.pos+1].text
	if _, ok := operators[op]; ok {
		return true
	}
	return strings.EqualFold(op, "in") || strings.EqualFold(op, "is") ||
		strings.EqualFold(op, "not") || strings.EqualFold(op, "was") || strings.EqualFold(op, "changed")
}

func (p *parser) parseClause() (Node, error) {
	fieldTok := p.peek()
	field := canonicalField(p.word())
	opTok := p.next()
	if p.collect {
		p.fields = append(p.fields, FieldReference{Name: field, Start: fieldTok.pos, End: fieldTok.end})
		p.clause = Operand{Field: field, Operator: strings.ToLower(opTok.text), Role: "value", OperatorStart: opTok.pos, OperatorEnd: opTok.end}
	}
	op := operators[opTok.text]
	switch {
	case op != "":
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return Clause{Field: field, Op: op, Values: []string{val}}, nil
	case strings.EqualFold(opTok.text, "in"):
		return p.parseInClause(field, false)
	case strings.EqualFold(opTok.text, "not"):
		if !p.atWord("in") {
			return nil, &SyntaxError{p.peek().pos, "expected IN after NOT"}
		}
		p.next()
		return p.parseInClause(field, true)
	case strings.EqualFold(opTok.text, "was"):
		return p.parseWasClause(field)
	case strings.EqualFold(opTok.text, "changed"):
		return p.parseChangedClause(field)
	case strings.EqualFold(opTok.text, "is"):
		neg := false
		if p.atWord("not") {
			neg = true
			p.next()
		}
		if p.atWord("empty") || p.atWord("null") {
			p.next()
			if neg {
				return Clause{Field: field, Op: "notempty"}, nil
			}
			return Clause{Field: field, Op: "empty"}, nil
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, &SyntaxError{p.peek().pos, "expected EMPTY or a function after IS"}
		}
		if _, _, function := splitFunction(value); !function {
			return nil, &SyntaxError{p.peek().pos, "expected EMPTY or a function after IS"}
		}
		if neg {
			return Clause{Field: field, Op: "isnot", Values: []string{value}}, nil
		}
		return Clause{Field: field, Op: "is", Values: []string{value}}, nil
	}
	return nil, &SyntaxError{opTok.pos, "unsupported operator " + opTok.text}
}

// canonicalField keeps established Jira JQL names working while accepting the
// work-item terminology exposed by current Jira Cloud documentation.
func canonicalField(field string) string {
	switch strings.ToLower(field) {
	case "workitem", "workitemkey":
		return "key"
	case "space":
		return "project"
	case "worktype":
		return "issuetype"
	case "components":
		return "component"
	case "duedate":
		return "due"
	case "resolved":
		return "resolutiondate"
	case "statuscategorychangedate":
		return "statuscategorychangeddate"
	case "issuekey":
		return "key"
	case "type":
		return "issuetype"
	case "timeoriginalestimate":
		return "originalestimate"
	case "timeestimate":
		return "remainingestimate"
	case "watchers":
		return "watcher"
	case "voters":
		return "voter"
	case "request participants", "request-participants":
		return "requestparticipants"
	case "request channel type", "request-channel-type":
		return "requestchanneltype"
	case "request type", "request-type", "customer request type":
		return "requesttype"
	default:
		// cf[10000] names a custom field by its number.
		lower := strings.ToLower(field)
		if strings.HasPrefix(lower, "cf[") && strings.HasSuffix(lower, "]") {
			if number := lower[3 : len(lower)-1]; number != "" && strings.Trim(number, "0123456789") == "" {
				return "customfield_" + number
			}
		}
		return lower
	}
}

func (p *parser) parseInClause(field string, negated bool) (Node, error) {
	if p.peek().kind != "lparen" {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if _, _, function := splitFunction(value); !function {
			return nil, &SyntaxError{p.peek().pos, "expected a list or list function after IN"}
		}
		op := "in"
		if negated {
			op = "notin"
		}
		return Clause{Field: field, Op: op, Values: []string{value}}, nil
	}
	p.next()
	p.clause.InParenthesized = true
	var vals []string
	for {
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		if p.peek().kind == "comma" {
			p.next()
			continue
		}
		break
	}
	if p.peek().kind != "rparen" {
		return nil, &SyntaxError{p.peek().pos, "expected ) to close IN"}
	}
	p.next()
	p.clause.InParenthesized = false
	op := "in"
	if negated {
		op = "notin"
	}
	return Clause{Field: field, Op: op, Values: vals}, nil
}

func (p *parser) parseWasClause(field string) (Node, error) {
	negated := false
	if p.atWord("not") {
		negated = true
		p.next()
	}
	if p.atWord("in") {
		p.next()
		clause, err := p.parseInClause(field, negated)
		if err != nil {
			return nil, err
		}
		values := clause.(Clause).Values
		op := "wasin"
		if negated {
			op = "wasnotin"
		}
		predicates, err := p.parseWasPredicates()
		if err != nil {
			return nil, err
		}
		return HistoryClause{Field: field, Op: op, Values: values, Predicates: predicates}, nil
	}
	value, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	op := "was"
	if negated {
		op = "wasnot"
	}
	predicates, err := p.parseWasPredicates()
	if err != nil {
		return nil, err
	}
	return HistoryClause{Field: field, Op: op, Values: []string{value}, Predicates: predicates}, nil
}

func (p *parser) parseWasPredicates() ([]HistoryPredicate, error) {
	predicates := []HistoryPredicate{}
	seen := map[string]bool{}
	for p.peek().kind == "word" {
		kind := strings.ToLower(p.peek().text)
		if kind != "by" && kind != "before" && kind != "after" && kind != "during" {
			break
		}
		if seen[kind] {
			return nil, &SyntaxError{p.peek().pos, "duplicate " + strings.ToUpper(kind) + " predicate"}
		}
		seen[kind] = true
		p.next()
		p.clause.Role = kind
		predicate := HistoryPredicate{Kind: kind}
		if kind == "during" {
			if p.peek().kind != "lparen" {
				return nil, &SyntaxError{p.peek().pos, "expected ( after DURING"}
			}
			p.next()
			first, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			if p.peek().kind != "comma" {
				return nil, &SyntaxError{p.peek().pos, "expected comma in DURING"}
			}
			p.next()
			second, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			if p.peek().kind != "rparen" {
				return nil, &SyntaxError{p.peek().pos, "expected ) to close DURING"}
			}
			p.next()
			predicate.Values = []string{first, second}
		} else {
			value, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			predicate.Values = []string{value}
		}
		predicates = append(predicates, predicate)
	}
	return predicates, nil
}

func (p *parser) parseChangedClause(field string) (Node, error) {
	clause := HistoryClause{Field: field, Op: "changed"}
	seen := map[string]bool{}
	for p.peek().kind == "word" {
		kind := strings.ToLower(p.peek().text)
		if kind != "from" && kind != "to" && kind != "by" && kind != "before" && kind != "after" && kind != "during" {
			break
		}
		if seen[kind] {
			return nil, &SyntaxError{p.peek().pos, "duplicate " + strings.ToUpper(kind) + " predicate"}
		}
		seen[kind] = true
		p.next()
		p.clause.Role = kind
		predicate := HistoryPredicate{Kind: kind}
		if kind == "during" {
			if p.peek().kind != "lparen" {
				return nil, &SyntaxError{p.peek().pos, "expected ( after DURING"}
			}
			p.next()
			first, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			if p.peek().kind != "comma" {
				return nil, &SyntaxError{p.peek().pos, "expected comma in DURING"}
			}
			p.next()
			second, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			if p.peek().kind != "rparen" {
				return nil, &SyntaxError{p.peek().pos, "expected ) to close DURING"}
			}
			p.next()
			predicate.Values = []string{first, second}
		} else {
			value, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			predicate.Values = []string{value}
		}
		clause.Predicates = append(clause.Predicates, predicate)
	}
	return clause, nil
}

func (p *parser) parseValue() (string, error) {
	t := p.peek()
	if t.kind == "word" || t.kind == "quoted" {
		p.next()
		// Function calls retain their arguments for semantic resolution by the
		// compiler (for example startOfMonth(-1M) or currentUser()).
		if t.kind == "word" && p.peek().kind == "lparen" {
			p.next()
			p.call++
			args := []string{}
			if p.peek().kind != "rparen" {
				for {
					arg, err := p.parseValue()
					if err != nil {
						return "", err
					}
					args = append(args, arg)
					if p.peek().kind != "comma" {
						break
					}
					p.next()
				}
			}
			if p.peek().kind != "rparen" {
				return "", &SyntaxError{p.peek().pos, "expected ) after function call"}
			}
			closing := p.next()
			p.call--
			call := t.text + "(" + strings.Join(args, ",") + ")"
			p.record(t, call, closing.end, true)
			return call, nil
		}
		p.record(t, t.text, t.end, false)
		return t.text, nil
	}
	return "", &SyntaxError{t.pos, "expected value"}
}

// ---- Compiler ----

// FieldResolver maps a JQL field name to a SQL column expression (possibly
// qualified). The compiler only knows the V1 field registry.
type FieldResolver struct {
	Columns      map[string]string // jql field → SQL expression
	TextColumns  []string          // columns searched by bare text and ~
	DefaultOrder map[string]string
	DateFields   map[string]bool
	// DurationFields hold time in seconds and compare with durations such as
	// "2h" or "1w 2d"; NumberFields hold plain numbers. Both are empty when
	// unset.
	DurationFields map[string]bool
	NumberFields   map[string]bool
	// JSONArrayFields are fields whose value is a JSON array of ids.
	JSONArrayFields map[string]string
	// CustomValueFields are custom fields whose values name options, people or
	// groups, or are lists, by the name a query uses.
	CustomValueFields map[string]CustomValueField
	// CollapsedFields maps a collapsed name, such as component[dropdown], to
	// the custom fields sharing that name and type.
	CollapsedFields map[string][]string
	// EntityProperties are the indexed issue property values apps declare, by
	// their JQL name: issue.property[key].path, or the app's alias.
	EntityProperties map[string]EntityPropertyField
	// FieldOperators are the operators a custom field's searcher allows.
	FieldOperators map[string][]string
	// SLAFields are the lower-cased names of the site's SLAs, which the SLA
	// functions search.
	SLAFields map[string]bool
	// KnownValues are the values a field can take, lower-cased, for the
	// fields Jira checks a query against. A value that is not among them is
	// an error rather than a query that matches nothing, because a typed
	// status is a mistake and an empty result hides it.
	KnownValues map[string]map[string]bool
	// FilterJQL resolves a saved filter a query names -- by id or by name --
	// to the JQL it holds, for the person searching. It answers false for a
	// filter that does not exist or that they may not see, which are the
	// same answer on purpose.
	FilterJQL func(userID, nameOrID string) (string, bool)
}

// WithFilterJQL gives a resolver the saved filters a query may name.
func WithFilterJQL(res FieldResolver, lookup func(userID, nameOrID string) (string, bool)) FieldResolver {
	res.FilterJQL = lookup
	return res
}

// WithSLAFields adds the names of a site's SLAs to the fields SLA functions
// search.
func WithSLAFields(res FieldResolver, names []string) FieldResolver {
	fields := make(map[string]bool, len(res.SLAFields)+len(names))
	for name := range res.SLAFields {
		fields[name] = true
	}
	for _, name := range names {
		fields[strings.ToLower(name)] = true
	}
	res.SLAFields = fields
	return res
}

// EntityPropertyField is an indexed value inside an issue property: the value
// at Path in the property PropertyKey, compared as Type (number, string,
// text, date or user). A value that is a JSON array matches by any element.
type EntityPropertyField struct {
	PropertyKey string
	Path        []string
	Type        string
}

// EntityPropertyFieldName is the JQL name of an indexed issue property value.
func EntityPropertyFieldName(propertyKey, objectName string) string {
	return "issue.property[" + propertyKey + "]." + objectName
}

// WithEntityProperties extends a resolver with the issue property values apps
// index. Each is searchable as issue.property[key].path and, when the app gives
// one, by its alias, which never hides a system or custom field. Indexed values
// can also order results.
func WithEntityProperties(base FieldResolver, indexes []models.AppEntityPropertyIndex) FieldResolver {
	res := base
	if res.EntityProperties == nil {
		res.EntityProperties = map[string]EntityPropertyField{}
	}
	if res.DateFields == nil {
		res.DateFields = map[string]bool{}
	}
	if res.DefaultOrder == nil {
		res.DefaultOrder = map[string]string{}
	}
	for _, index := range indexes {
		if index.EntityType != "issue" {
			continue
		}
		field := EntityPropertyField{PropertyKey: index.PropertyKey, Path: strings.Split(index.ObjectName, "."), Type: index.Type}
		names := []string{strings.ToLower(EntityPropertyFieldName(index.PropertyKey, index.ObjectName))}
		if alias := strings.ToLower(strings.TrimSpace(index.Alias)); alias != "" {
			_, column := res.Columns[alias]
			_, custom := res.CustomValueFields[alias]
			_, taken := res.EntityProperties[alias]
			if !column && !custom && !taken {
				names = append(names, alias)
			}
		}
		for _, name := range names {
			res.EntityProperties[name] = field
			if field.Type == "date" {
				res.DateFields[name] = true
			}
			res.DefaultOrder[name] = entityPropertyOrder(field)
		}
	}
	return res
}

// entityPropertyOrder is the value an indexed property orders by. The key and
// path are validated when the app installs, and are quoted as SQL literals.
func entityPropertyOrder(field EntityPropertyField) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	value := "ep.value #>> ARRAY[" + func() string {
		parts := make([]string, 0, len(field.Path))
		for _, part := range field.Path {
			parts = append(parts, quote(part))
		}
		return strings.Join(parts, ",")
	}() + "]::text[]"
	switch field.Type {
	case "number":
		value = "jql_try_numeric(" + value + ")"
	case "date":
		value = "jql_try_timestamptz(" + value + ")"
	}
	return "(SELECT " + value + " FROM issue_properties ep WHERE ep.issue_id = i.id AND ep.key = " + quote(field.PropertyKey) + ")"
}

// CustomValueField is a custom field a query matches by what its value names.
type CustomValueField struct {
	ID, Type string
}

// WithCustomFields extends a resolver with customfield_NNNNN columns and app
// aliases. Values live in issues.fields JSONB; numbers compare numerically.
func WithCustomFields(base FieldResolver, fields []*models.CustomField) FieldResolver {
	res := base
	if res.Columns == nil {
		res.Columns = map[string]string{}
	}
	if res.DateFields == nil {
		res.DateFields = map[string]bool{}
	}
	for _, f := range fields {
		if operators := models.SearcherOperators(f.SearcherKey); operators != nil {
			if res.FieldOperators == nil {
				res.FieldOperators = map[string][]string{}
			}
			res.FieldOperators[f.ID] = operators
			res.FieldOperators[strings.ToLower(f.Name)] = operators
			if f.AppKey != "" {
				res.FieldOperators[strings.ToLower(f.AppKey+"__"+f.AppModuleKey)] = operators
			}
		}
		col := `i.fields->>'` + f.ID + `'`
		switch f.Type {
		case models.CustomFieldNumber:
			col = `NULLIF(i.fields->>'` + f.ID + `','')::numeric`
		case models.CustomFieldDatetime:
			col = `NULLIF(i.fields->>'` + f.ID + `','')::timestamptz`
		}
		res.Columns[f.ID] = col
		res.Columns[strings.ToLower(f.Name)] = col
		switch f.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect,
			models.CustomFieldUser, models.CustomFieldMultiUser, models.CustomFieldGroup, models.CustomFieldMultiGroup, models.CustomFieldLabels,
			models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldTeam:
			if res.CustomValueFields == nil {
				res.CustomValueFields = map[string]CustomValueField{}
			}
			field := CustomValueField{ID: f.ID, Type: f.Type}
			res.CustomValueFields[f.ID] = field
			res.CustomValueFields[strings.ToLower(f.Name)] = field
			if f.AppKey != "" {
				res.CustomValueFields[strings.ToLower(f.AppKey+"__"+f.AppModuleKey)] = field
			}
		case models.CustomFieldDate:
			col = `NULLIF(i.fields->>'` + f.ID + `','')::date`
			res.Columns[f.ID] = col
			res.Columns[strings.ToLower(f.Name)] = col
			res.DateFields[f.ID] = true
			res.DateFields[strings.ToLower(f.Name)] = true
		}
		if f.Type == models.CustomFieldDatetime {
			res.DateFields[f.ID] = true
			res.DateFields[strings.ToLower(f.Name)] = true
		}
		if f.AppKey != "" {
			alias := strings.ToLower(f.AppKey + "__" + f.AppModuleKey)
			res.Columns[alias] = col
			if f.Type == models.CustomFieldDatetime {
				res.DateFields[alias] = true
			}
		}
		res.TextColumns = append(res.TextColumns, `i.fields->>'`+f.ID+`'`)
	}
	groups := map[string][]string{}
	for _, f := range fields {
		alias := strings.ToLower(models.CollapsedFieldName(f.Name, f.Type))
		groups[alias] = append(groups[alias], f.ID)
	}
	for alias, members := range groups {
		if len(members) < 2 {
			continue
		}
		if res.CollapsedFields == nil {
			res.CollapsedFields = map[string][]string{}
		}
		res.CollapsedFields[alias] = members
	}
	return res
}

// CurrentUserPlaceholder stands for the person searching inside a column
// expression. A field that asks about them -- what they have opened, what
// they follow -- is theirs alone, so the expression carries the reader
// rather than being the same for everyone.
const CurrentUserPlaceholder = "{{currentUser}}"

func DefaultResolver() FieldResolver {
	return FieldResolver{
		// Every service desk has Jira's two built-in SLAs.
		SLAFields: map[string]bool{"time to first response": true, "time to resolution": true},
		Columns: map[string]string{
			"key":                       "i.key",
			"issue":                     "i.key",
			"id":                        "i.jira_id",
			"summary":                   "i.summary",
			"description":               "i.description::text",
			"status":                    "st.name",
			"statuscategory":            "st.category",
			"project":                   "pr.key",
			"assignee":                  "i.assignee_id",
			"reporter":                  "i.reporter_id",
			"creator":                   "i.reporter_id",
			"priority":                  "COALESCE(pro.name, pr2.name)",
			"issuetype":                 "COALESCE(ito.name, it.name)",
			"updated":                   "i.updated_at",
			"created":                   "i.created_at",
			"labels":                    "i.labels",
			"parent":                    "parent.key",
			"resolution":                "COALESCE(reso.name, res.name)",
			"resolutiondate":            "i.resolved_at",
			"statuscategorychangeddate": "i.status_category_changed_at",
			"due":                       `i.due_date::timestamptz`,
			"environment":               `i.fields->>'environment'`,
			"component":                 `i.fields->>'component'`,
			"sprint":                    `i.fields->>'sprint'`,
			// Time tracking, in seconds; work ratio is time spent as a
			// percentage of the original estimate.
			"originalestimate":  "i.original_estimate_seconds",
			"remainingestimate": "i.remaining_estimate_seconds",
			"timespent":         "(SELECT COALESCE(sum(w.time_spent_seconds),0) FROM worklogs w WHERE w.issue_id=i.id)",
			"workratio":         "CASE WHEN i.original_estimate_seconds > 0 THEN (SELECT COALESCE(sum(w.time_spent_seconds),0) FROM worklogs w WHERE w.issue_id=i.id) * 100 / i.original_estimate_seconds END",
			// How many people voted, what level of the work type hierarchy
			// the work item sits on, the security level it carries and the
			// category of its project.
			"votes":          "(SELECT count(*) FROM issue_votes vote_count WHERE vote_count.issue_id=i.id)",
			"hierarchylevel": "COALESCE(ito.hierarchy_level, it.hierarchy_level)",
			"level":          "(SELECT level_entry->>'name' FROM security_schemes level_scheme, jsonb_array_elements(level_scheme.levels) level_entry WHERE level_scheme.workspace_id=pr.workspace_id AND level_entry->>'id'=i.security_level_id LIMIT 1)",
			"category":       "(SELECT project_category.name FROM project_categories project_category WHERE project_category.id=pr.category_id)",
			// When the person searching last opened this work item.
			"lastviewed": "(SELECT issue_view.viewed_at FROM issue_views issue_view WHERE issue_view.issue_id=i.id AND issue_view.user_id=" + CurrentUserPlaceholder + ")",
			// The channel a service request came in on, which only a request
			// has at all.
			"requestchanneltype": "(SELECT service_request.channel FROM service_requests service_request WHERE service_request.issue_id=i.id)",
			// The request type a customer raised this under, by name, which
			// only a request has at all.
			"requesttype": "(SELECT request_type.name FROM service_requests service_request JOIN service_request_types request_type ON request_type.id=service_request.request_type_id WHERE service_request.issue_id=i.id)",
		},
		TextColumns: []string{"i.summary", "i.description::text"},
		DefaultOrder: map[string]string{
			"updated": "i.updated_at", "created": "i.created_at", "key": "i.key", "summary": "i.summary",
			"status": "st.name", "priority": "COALESCE(pro.position, pr2.position)", "assignee": "a.display_name", "issuetype": "COALESCE(ito.name, it.name)",
			"reporter": "r.display_name", "project": "pr.key", "parent": "parent.key", "resolution": "COALESCE(reso.position, res.position)",
			"due": `i.due_date::timestamptz`, "resolutiondate": "i.resolved_at",
			"statuscategorychangeddate": "i.status_category_changed_at",
			"lastviewed":                "(SELECT issue_view.viewed_at FROM issue_views issue_view WHERE issue_view.issue_id=i.id AND issue_view.user_id=" + CurrentUserPlaceholder + ")",
			"originalestimate":          "i.original_estimate_seconds", "remainingestimate": "i.remaining_estimate_seconds",
			"timespent":      "(SELECT COALESCE(sum(w.time_spent_seconds),0) FROM worklogs w WHERE w.issue_id=i.id)",
			"workratio":      "CASE WHEN i.original_estimate_seconds > 0 THEN (SELECT COALESCE(sum(w.time_spent_seconds),0) FROM worklogs w WHERE w.issue_id=i.id) * 100 / i.original_estimate_seconds END",
			"votes":          "(SELECT count(*) FROM issue_votes vote_count WHERE vote_count.issue_id=i.id)",
			"hierarchylevel": "COALESCE(ito.hierarchy_level, it.hierarchy_level)",
		},
		DateFields:     map[string]bool{"updated": true, "created": true, "due": true, "resolutiondate": true, "statuscategorychangeddate": true, "lastviewed": true},
		DurationFields: map[string]bool{"originalestimate": true, "remainingestimate": true, "timespent": true},
		NumberFields:   map[string]bool{"workratio": true, "votes": true, "hierarchylevel": true},
	}
}

type Compiled struct {
	Where    string
	Args     []any
	OrderSQL string
	Err      error
	// Warnings are the clause and ordering errors a lenient compile skipped.
	Warnings []string
}

// Compile turns a parsed query into a WHERE fragment. `userArg` is the
// current user's account id (for currentUser()); the caller must append the
// workspace permission predicate separately.
func Compile(q *Query, currentUserID string, res FieldResolver) Compiled {
	return CompileAt(q, currentUserID, res, 1)
}

// CompileAt is Compile with placeholder numbering starting at paramOffset
// (use when the caller prepends its own parameters, e.g. workspace id = $1).
func CompileAt(q *Query, currentUserID string, res FieldResolver, paramOffset int) Compiled {
	return compileQuery(q, currentUserID, res, paramOffset, false)
}

// CompileLenientAt compiles a query the way Jira validates it in warn mode:
// a clause that fails validation matches nothing and an ordering field that
// cannot be sorted is skipped, each reported in Warnings instead of failing
// the whole query.
func CompileLenientAt(q *Query, currentUserID string, res FieldResolver, paramOffset int) Compiled {
	return compileQuery(q, currentUserID, res, paramOffset, true)
}

func compileQuery(q *Query, currentUserID string, res FieldResolver, paramOffset int, lenient bool) Compiled {
	if q == nil || q.Root == nil {
		return Compiled{Err: &SyntaxError{0, "empty query"}}
	}
	c := &compiler{res: res, user: currentUserID, offset: paramOffset - 1, lenient: lenient}
	c.now = time.Now().UTC()
	where := c.node(q.Root)
	if c.err != nil {
		return Compiled{Err: c.err}
	}
	orders := q.Orders
	if len(orders) == 0 && q.OrderBy != nil {
		orders = []Order{*q.OrderBy}
	}
	orderParts := []string{}
	if len(orders) == 0 {
		orderParts = append(orderParts, "i.updated_at DESC")
	} else {
		for _, requested := range orders {
			col, ok := res.DefaultOrder[requested.Field]
			if !ok {
				if lenient {
					c.warnings = append(c.warnings, (&SyntaxError{0, "cannot order by " + requested.Field}).Error())
					continue
				}
				return Compiled{Err: &SyntaxError{0, "cannot order by " + requested.Field}}
			}
			dir := "ASC"
			if requested.Desc {
				dir = "DESC"
			}
			orderParts = append(orderParts, c.forCurrentUser(col)+" "+dir)
		}
	}
	// A stable final key prevents duplicate or skipped rows when requested sort
	// values are equal and is required by Jira's cursor search contract.
	if !strings.Contains(strings.Join(orderParts, ","), "i.id ") {
		orderParts = append(orderParts, "i.id ASC")
	}
	return Compiled{Where: where, Args: c.args, OrderSQL: strings.Join(orderParts, ", "), Warnings: c.warnings}
}

type compiler struct {
	res    FieldResolver
	user   string
	args   []any
	err    error
	offset int
	now    time.Time
	// lenient turns a failing clause into a warning and FALSE.
	lenient  bool
	warnings []string
	// filters is the chain of saved filters being compiled, so a filter
	// that names itself, directly or through others, is refused rather than
	// followed for ever.
	filters []string
}

// forCurrentUser binds the person searching into a column expression that
// asks about them. The placeholder becomes an ordinary parameter, numbered
// where it is used, so the expression carries no identity of its own.
func (c *compiler) forCurrentUser(col string) string {
	if !strings.Contains(col, CurrentUserPlaceholder) {
		return col
	}
	return strings.ReplaceAll(col, CurrentUserPlaceholder, c.arg(c.user))
}

// terminal compiles one clause. In a lenient compile a clause that fails is
// recorded as a warning, its parameters are dropped so later placeholders stay
// consecutive, and it matches nothing.
func (c *compiler) terminal(compile func() string) string {
	if !c.lenient {
		return compile()
	}
	mark := len(c.args)
	sql := compile()
	if c.err == nil {
		return sql
	}
	c.warnings = append(c.warnings, c.err.Error())
	c.err, c.args = nil, c.args[:mark]
	return "FALSE"
}

func (c *compiler) arg(v any) string {
	c.args = append(c.args, v)
	return fmt.Sprintf("$%d", len(c.args)+c.offset)
}

func (c *compiler) node(n Node) string {
	if c.err != nil {
		return ""
	}
	switch t := n.(type) {
	case Or:
		parts := make([]string, 0, len(t.Terms))
		for _, term := range t.Terms {
			parts = append(parts, "("+c.node(term)+")")
		}
		return strings.Join(parts, " OR ")
	case And:
		parts := make([]string, 0, len(t.Terms))
		for _, term := range t.Terms {
			parts = append(parts, "("+c.node(term)+")")
		}
		return strings.Join(parts, " AND ")
	case Not:
		return "NOT (" + c.node(t.Inner) + ")"
	case Text:
		if t.Value == "" {
			return "TRUE" // empty query matches all
		}
		// A bare term is Jira's text field written without naming it, so it
		// looks where that field looks: the work item's own text and the
		// text of its comments.
		term := c.arg("%" + t.Value + "%")
		likes := make([]string, 0, len(c.res.TextColumns)+1)
		for _, col := range c.res.TextColumns {
			likes = append(likes, col+" ILIKE "+term)
		}
		likes = append(likes, commentTextMatch(term))
		return "(" + strings.Join(likes, " OR ") + ")"
	case Clause:
		return c.terminal(func() string { return c.clause(t) })
	case HistoryClause:
		return c.terminal(func() string { return c.historyClause(t) })
	}
	c.err = &SyntaxError{0, "unknown node"}
	return ""
}

func (c *compiler) clause(cl Clause) string {
	// A collapsed field searches each field sharing its name and type: any of
	// them may match, and a negative condition must hold for all of them.
	if members, ok := c.res.CollapsedFields[cl.Field]; ok {
		joiner := " OR "
		switch cl.Op {
		case "!=", "notin", "!~", "empty":
			joiner = " AND "
		}
		parts := make([]string, 0, len(members))
		for _, member := range members {
			memberClause := cl
			memberClause.Field = member
			parts = append(parts, "("+c.clause(memberClause)+")")
			if c.err != nil {
				return ""
			}
		}
		return "(" + strings.Join(parts, joiner) + ")"
	}
	// A custom field's searcher decides which operators may search it.
	if allowed, ok := c.res.FieldOperators[cl.Field]; ok && !slices.Contains(allowed, cl.Op) {
		c.err = &SyntaxError{0, "operator " + cl.Op + " is not supported by " + cl.Field}
		return ""
	}
	if containsJQLFunction(cl.Values, "breached", "completed", "everBreached", "paused", "remaining", "running", "withinCalendarHours") {
		return c.slaClause(cl)
	}
	if containsJQLFunction(cl.Values, "approved", "approver", "myApproval", "myPendingApproval", "myPending", "pending", "pendingApprovalBy", "pendingBy") {
		return c.approvalClause(cl)
	}
	if cl.Field == "fixversion" || cl.Field == "affectedversion" {
		return c.versionClause(cl)
	}
	if cl.Field == "labels" {
		return c.labelsClause(cl)
	}
	// Fields that are not values on the work item but things attached to it:
	// its text, its comments, the people watching or voting, its attachments
	// and the links it takes part in.
	switch cl.Field {
	case "text":
		return c.freeTextClause(cl)
	case "comment":
		return c.commentClause(cl)
	case "watcher":
		return c.userSetClause(cl, "watchers", "watcher_row", "issue_id")
	case "voter":
		return c.userSetClause(cl, "issue_votes", "voter_row", "issue_id")
	case "requestparticipants":
		return c.userSetClause(cl, "service_request_participants", "participant_row", "request_issue_id")
	case "attachments":
		return c.attachmentsClause(cl)
	case "issuelinktype":
		return c.issueLinkTypeClause(cl)
	case "filter", "request", "savedfilter", "searchrequest":
		return c.savedFilterClause(cl)
	}
	if field, ok := c.res.CustomValueFields[cl.Field]; ok {
		return c.customValueClause(field, cl)
	}
	if array, ok := c.res.JSONArrayFields[cl.Field]; ok {
		return c.jsonArrayClause(array, cl)
	}
	if cl.Field == "sprint" {
		return c.sprintClause(cl)
	}
	if cl.Field == "component" {
		return c.componentClause(cl)
	}
	if cl.Field == "issuetype" && containsJQLFunction(cl.Values, "standardIssueTypes", "subtaskIssueTypes", "standardWorkTypes", "subtaskWorkTypes") {
		return c.issueTypeListClause(cl)
	}
	if (cl.Field == "assignee" || cl.Field == "reporter" || cl.Field == "creator") && containsJQLFunction(cl.Values, "membersOf") {
		return c.userListClause(cl)
	}
	if cl.Field == "project" && containsJQLFunction(cl.Values, "projectsLeadByUser", "spacesLeadByUser", "projectsWhereUserHasRole", "spacesWhereUserHasRole", "projectsWhereUserHasPermission", "spacesWhereUserHasPermission") {
		return c.projectFunctionClause(cl)
	}
	if (cl.Field == "issue" || cl.Field == "key" || cl.Field == "id") && containsJQLFunction(cl.Values,
		"linkedIssues", "linkedWorkItems", "watchedIssues", "watchedWorkItems", "votedIssues", "votedWorkItems",
		"issueHistory", "workItemHistory", "issuesWithRemoteLinksByGlobalId", "workItemsWithRemoteLinksByGlobalId",
		"updatedBy") {
		return c.issueFunctionClause(cl)
	}
	if cl.Field == "project" && (cl.Op == "=" || cl.Op == "!=" || cl.Op == "in" || cl.Op == "notin") && !containsAnyFunction(cl.Values) {
		return c.projectClause(cl)
	}
	if field, indexed := c.res.EntityProperties[cl.Field]; indexed {
		return c.entityPropertyClause(cl, field)
	}
	col, ok := c.res.Columns[cl.Field]
	if !ok {
		c.err = &SyntaxError{0, "field does not exist or is not searchable: " + cl.Field}
		return ""
	}
	col = c.forCurrentUser(col)
	if containsJQLFunction(cl.Values, "currentLogin", "lastLogin", "now", "startOfDay", "endOfDay", "startOfWeek", "endOfWeek", "startOfMonth", "endOfMonth", "startOfYear", "endOfYear") {
		if !c.res.DateFields[cl.Field] {
			c.err = &SyntaxError{0, "date functions require a date field"}
			return ""
		}
		if len(cl.Values) != 1 || (cl.Op != "=" && cl.Op != "!=" && cl.Op != ">" && cl.Op != ">=" && cl.Op != "<" && cl.Op != "<=") {
			c.err = &SyntaxError{0, "date functions support only single-value date comparisons"}
			return ""
		}
		name, _, _ := splitFunction(cl.Values[0])
		value := ""
		if strings.EqualFold(name, "currentLogin") || strings.EqualFold(name, "lastLogin") {
			value = c.loginDateSQL(cl.Values[0])
		} else {
			value = c.arg(c.fieldValue(cl.Field, cl.Values[0]))
		}
		if c.err != nil {
			return ""
		}
		op := cl.Op
		if op == "!=" {
			op = "<>"
		}
		return col + " " + op + " " + value
	}
	switch cl.Op {
	case "=":
		value := c.fieldValue(cl.Field, cl.Values[0])
		if value == nil {
			return col + " IS NULL"
		}
		return col + " = " + c.arg(value)
	case "!=":
		value := c.fieldValue(cl.Field, cl.Values[0])
		if value == nil {
			return col + " IS NOT NULL"
		}
		return "(" + col + " IS DISTINCT FROM " + c.arg(value) + ")"
	case "~":
		return col + " ILIKE " + c.arg("%"+cl.Values[0]+"%")
	case "!~":
		return "(" + col + " NOT ILIKE " + c.arg("%"+cl.Values[0]+"%") + " OR " + col + " IS NULL)"
	case "in", "notin":
		placeholders := make([]string, 0, len(cl.Values))
		includesNone := false
		for _, v := range cl.Values {
			value := c.fieldValue(cl.Field, v)
			// A value standing for "no value" — Unresolved — matches the
			// absence of one; an IN list with NULL in it would match nothing.
			if value == nil {
				includesNone = true
				continue
			}
			placeholders = append(placeholders, c.arg(value))
		}
		membership := "FALSE"
		if len(placeholders) > 0 {
			membership = col + " IN (" + strings.Join(placeholders, ",") + ")"
		}
		// NOT IN never matches an empty value in Jira, whether or not the list
		// names one.
		if cl.Op == "notin" {
			return "(" + col + " IS NOT NULL AND NOT (" + membership + "))"
		}
		if includesNone {
			return "(" + col + " IS NULL OR " + membership + ")"
		}
		return membership
	case ">", ">=", "<", "<=":
		return col + " " + cl.Op + " " + c.arg(c.fieldValue(cl.Field, cl.Values[0]))
	case "empty":
		// A date has no text form to be blank; it is only ever absent.
		if c.res.DurationFields[cl.Field] || c.res.NumberFields[cl.Field] || c.res.DateFields[cl.Field] {
			return "(" + col + " IS NULL)"
		}
		return "(" + col + " IS NULL OR " + col + " = '')"
	case "notempty":
		if c.res.DurationFields[cl.Field] || c.res.NumberFields[cl.Field] || c.res.DateFields[cl.Field] {
			return "(" + col + " IS NOT NULL)"
		}
		return "(" + col + " IS NOT NULL AND " + col + " <> '')"
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op}
	return ""
}

// entityPropertyClause compares an indexed issue property value. A property
// whose value at the path is an array matches when any element does, and, as
// for other fields, negative operators never match work items without a value.
func (c *compiler) entityPropertyClause(cl Clause, field EntityPropertyField) string {
	supported := map[string][]string{
		"number": {"=", "!=", ">", ">=", "<", "<=", "in", "notin", "empty", "notempty"},
		"date":   {"=", "!=", ">", ">=", "<", "<=", "in", "notin", "empty", "notempty"},
		"string": {"=", "!=", "in", "notin", "empty", "notempty"},
		"user":   {"=", "!=", "in", "notin", "empty", "notempty"},
		"text":   {"~", "!~", "empty", "notempty"},
	}[field.Type]
	if !slices.Contains(supported, cl.Op) {
		c.err = &SyntaxError{0, "operator " + cl.Op + " is not supported by " + cl.Field}
		return ""
	}
	key, path := c.arg(field.PropertyKey), c.arg(field.Path)
	value, cast := "v.value", "::text"
	switch field.Type {
	case "number":
		value, cast = "jql_try_numeric(v.value)", "::numeric"
	case "date":
		value, cast = "jql_try_timestamptz(v.value)", "::timestamptz"
	}
	matching := func(condition string) string {
		at := "ep.value #> " + path + "::text[]"
		return "EXISTS (SELECT 1 FROM issue_properties ep CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(" + at + ") = 'array' THEN " + at + " ELSE jsonb_build_array(" + at + ") END) AS v(value) WHERE ep.issue_id = i.id AND ep.key = " + key + "::text AND " + value + " IS NOT NULL AND (" + condition + "))"
	}
	operand := func(raw string) string {
		trimmed := strings.Trim(strings.TrimSpace(raw), `"'`)
		switch field.Type {
		case "number":
			number, err := strconv.ParseFloat(trimmed, 64)
			if err != nil {
				c.err = &SyntaxError{0, "invalid number " + strconv.Quote(raw) + " for " + cl.Field}
				return "NULL"
			}
			return c.arg(strconv.FormatFloat(number, 'f', -1, 64)) + cast
		case "date":
			return c.arg(c.fieldValue(cl.Field, raw)) + cast
		case "user":
			if name, args, function := splitFunction(raw); function {
				if strings.EqualFold(name, "currentUser") && len(args) == 0 {
					return c.arg(c.user) + cast
				}
				c.err = &SyntaxError{0, "unsupported function " + name + "() for " + cl.Field}
				return "NULL"
			}
		}
		return c.arg(raw) + cast
	}
	present := matching("TRUE")
	switch cl.Op {
	case "empty":
		return "NOT " + present
	case "notempty":
		return present
	case "~":
		return matching("v.value ILIKE " + c.arg("%"+cl.Values[0]+"%"))
	case "!~":
		return "(" + present + " AND NOT " + matching("v.value ILIKE "+c.arg("%"+cl.Values[0]+"%")) + ")"
	case "=", "!=":
		equal := matching(value + " = " + operand(cl.Values[0]))
		if cl.Op == "=" {
			return equal
		}
		return "(" + present + " AND NOT " + equal + ")"
	case "in", "notin":
		operands := make([]string, 0, len(cl.Values))
		for _, raw := range cl.Values {
			operands = append(operands, operand(raw))
		}
		member := matching(value + " IN (" + strings.Join(operands, ",") + ")")
		if cl.Op == "in" {
			return member
		}
		return "(" + present + " AND NOT " + member + ")"
	default:
		return matching(value + " " + cl.Op + " " + operand(cl.Values[0]))
	}
}

// projectClause matches a project by its key, id or name, as Jira does.
func (c *compiler) projectClause(cl Clause) string {
	matches := make([]string, 0, len(cl.Values))
	for _, value := range cl.Values {
		placeholder := c.arg(value)
		matches = append(matches, "(pr.key = upper("+placeholder+"::text) OR pr.id = "+placeholder+"::text OR lower(pr.name) = lower("+placeholder+"::text))")
	}
	match := "(" + strings.Join(matches, " OR ") + ")"
	if cl.Op == "!=" || cl.Op == "notin" {
		return "NOT " + match
	}
	return match
}

func containsAnyFunction(values []string) bool {
	for _, value := range values {
		if _, _, function := splitFunction(value); function {
			return true
		}
	}
	return false
}

func (c *compiler) slaClause(cl Clause) string {
	if _, systemField := c.res.Columns[cl.Field]; systemField || cl.Field == "approval" || cl.Field == "approvals" {
		c.err = &SyntaxError{0, "SLA functions require an SLA field"}
		return ""
	}
	if !c.res.SLAFields[cl.Field] {
		c.err = &SyntaxError{0, "field does not exist or is not searchable: " + cl.Field}
		return ""
	}
	if len(cl.Values) != 1 {
		c.err = &SyntaxError{0, "SLA functions require one function value"}
		return ""
	}
	name, args, ok := splitFunction(cl.Values[0])
	if !ok {
		c.err = &SyntaxError{0, "expected an SLA function"}
		return ""
	}
	name = strings.ToLower(name)
	metric := c.arg(cl.Field)
	metricMatch := "(lower(sla_metric.name)=lower(" + metric + ") OR sla_metric.id=" + metric + ")"
	latest := "sla_cycle.cycle_number=(SELECT max(sla_latest.cycle_number) FROM service_sla_cycles sla_latest WHERE sla_latest.request_issue_id=i.id AND sla_latest.metric_id=sla_metric.id)"
	base := "sla_cycle.request_issue_id=i.id AND " + metricMatch
	boolean := name != "remaining"
	if boolean && len(args) != 0 {
		c.err = &SyntaxError{0, name + "() does not accept arguments"}
		return ""
	}
	if boolean && cl.Op != "=" && cl.Op != "!=" {
		c.err = &SyntaxError{0, name + "() supports only = and !="}
		return ""
	}
	condition := ""
	switch name {
	case "breached":
		condition = latest + " AND jira_service_sla_elapsed_millis(sla_cycle.id,CURRENT_TIMESTAMP)>=COALESCE(sla_cycle.goal_millis,sla_metric.goal_millis)"
	case "completed":
		condition = latest + " AND sla_cycle.stopped_at IS NOT NULL"
	case "everbreached":
		condition = "jira_service_sla_elapsed_millis(sla_cycle.id,COALESCE(sla_cycle.stopped_at,CURRENT_TIMESTAMP))>=COALESCE(sla_cycle.goal_millis,sla_metric.goal_millis)"
	case "paused":
		condition = latest + " AND sla_cycle.stopped_at IS NULL AND EXISTS (SELECT 1 FROM service_sla_cycle_pauses sla_pause WHERE sla_pause.cycle_id=sla_cycle.id AND sla_pause.stopped_at IS NULL)"
	case "running":
		condition = latest + " AND sla_cycle.stopped_at IS NULL AND NOT EXISTS (SELECT 1 FROM service_sla_cycle_pauses sla_pause WHERE sla_pause.cycle_id=sla_cycle.id AND sla_pause.stopped_at IS NULL)"
	case "withincalendarhours":
		condition = latest + " AND sla_cycle.stopped_at IS NULL AND jira_service_within_calendar(sla_metric.calendar_id,CURRENT_TIMESTAMP)"
	case "remaining":
		if len(args) > 1 {
			c.err = &SyntaxError{0, "remaining() accepts at most one duration"}
			return ""
		}
		if cl.Op != "=" && cl.Op != "!=" && cl.Op != ">" && cl.Op != ">=" && cl.Op != "<" && cl.Op != "<=" {
			c.err = &SyntaxError{0, "remaining() requires a comparison operator"}
			return ""
		}
		threshold := int64(0)
		if len(args) == 1 {
			var err error
			threshold, err = parseSLADurationMillis(args[0])
			if err != nil {
				c.err = &SyntaxError{0, "remaining(): " + err.Error()}
				return ""
			}
		}
		op := cl.Op
		if op == "!=" {
			op = "<>"
		}
		condition = latest + " AND (COALESCE(sla_cycle.goal_millis,sla_metric.goal_millis)-jira_service_sla_elapsed_millis(sla_cycle.id,CURRENT_TIMESTAMP)) " + op + " " + c.arg(threshold)
	default:
		c.err = &SyntaxError{0, "unsupported SLA function " + name + "()"}
		return ""
	}
	match := "EXISTS (SELECT 1 FROM service_sla_cycles sla_cycle JOIN service_sla_metrics sla_metric ON sla_metric.id=sla_cycle.metric_id WHERE " + base + " AND " + condition + ")"
	if boolean && cl.Op == "!=" {
		anyMetric := "EXISTS (SELECT 1 FROM service_sla_cycles sla_any JOIN service_sla_metrics sla_any_metric ON sla_any_metric.id=sla_any.metric_id WHERE sla_any.request_issue_id=i.id AND (lower(sla_any_metric.name)=lower(" + metric + ") OR sla_any_metric.id=" + metric + "))"
		return "(" + anyMetric + " AND NOT (" + match + "))"
	}
	return match
}

func parseSLADurationMillis(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("duration cannot be empty")
	}
	unit := raw[len(raw)-1:]
	amount, err := strconv.ParseInt(strings.TrimSpace(raw[:len(raw)-1]), 10, 64)
	if err != nil || amount < 0 {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	multiplier := int64(0)
	switch unit {
	case "w":
		multiplier = (7 * 24 * time.Hour).Milliseconds()
	case "d":
		multiplier = (24 * time.Hour).Milliseconds()
	case "h":
		multiplier = time.Hour.Milliseconds()
	case "m":
		multiplier = time.Minute.Milliseconds()
	default:
		return 0, fmt.Errorf("duration must use w, d, h, or m")
	}
	if amount > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("duration is too large")
	}
	return amount * multiplier, nil
}

func (c *compiler) approvalClause(cl Clause) string {
	if cl.Field != "approval" && cl.Field != "approvals" {
		c.err = &SyntaxError{0, "approval functions require an approval field"}
		return ""
	}
	if len(cl.Values) != 1 {
		c.err = &SyntaxError{0, "approval functions require one function value"}
		return ""
	}
	name, args, ok := splitFunction(cl.Values[0])
	if !ok {
		c.err = &SyntaxError{0, "expected an approval function"}
		return ""
	}
	name = strings.ToLower(name)
	allowNotEqual := name == "pending" || name == "pendingapprovalby" || name == "pendingby"
	if cl.Op != "=" && !(allowNotEqual && cl.Op == "!=") {
		c.err = &SyntaxError{0, name + "() does not support operator " + cl.Op}
		return ""
	}
	conditions := []string{"approval.request_issue_id=i.id"}
	switch name {
	case "approved":
		if len(args) != 0 {
			c.err = &SyntaxError{0, "approved() does not accept arguments"}
			return ""
		}
		conditions = append(conditions, "approval.final_decision='approved'")
	case "approver":
		conditions = append(conditions, c.approvalUserMatch(args))
	case "myapproval":
		if len(args) != 0 {
			c.err = &SyntaxError{0, "myApproval() does not accept arguments"}
			return ""
		}
		conditions = append(conditions, c.approvalUserMatch([]string{"currentUser()"}))
	case "mypendingapproval":
		if len(args) != 0 {
			c.err = &SyntaxError{0, "myPendingApproval() does not accept arguments"}
			return ""
		}
		conditions = append(conditions, "approval.final_decision='pending'", "approval_actor.decision='pending'", c.approvalUserMatch([]string{"currentUser()"}))
	case "mypending":
		if len(args) != 0 {
			c.err = &SyntaxError{0, "myPending() does not accept arguments"}
			return ""
		}
		conditions = append(conditions, "approval.final_decision='pending'", c.approvalUserMatch([]string{"currentUser()"}))
	case "pending":
		if len(args) != 0 {
			c.err = &SyntaxError{0, "pending() does not accept arguments"}
			return ""
		}
		conditions = append(conditions, "approval.final_decision='pending'")
	case "pendingapprovalby":
		conditions = append(conditions, "approval.final_decision='pending'", "approval_actor.decision='pending'", c.approvalUserMatch(args))
	case "pendingby":
		conditions = append(conditions, "approval.final_decision='pending'", c.approvalUserMatch(args))
	default:
		c.err = &SyntaxError{0, "unsupported approval function " + name + "()"}
		return ""
	}
	if c.err != nil {
		return ""
	}
	match := "EXISTS (SELECT 1 FROM service_request_approvals approval JOIN service_request_approvers approval_actor ON approval_actor.approval_id=approval.id WHERE " + strings.Join(conditions, " AND ") + ")"
	if cl.Op == "!=" {
		anyApproval := "EXISTS (SELECT 1 FROM service_request_approvals approval_any WHERE approval_any.request_issue_id=i.id)"
		return "(" + anyApproval + " AND NOT (" + match + "))"
	}
	return match
}

func (c *compiler) approvalUserMatch(values []string) string {
	if len(values) == 0 || len(values) > 100 {
		c.err = &SyntaxError{0, "approval user functions accept between 1 and 100 users"}
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if function, args, ok := splitFunction(value); ok {
			if !strings.EqualFold(function, "currentUser") || len(args) != 0 {
				c.err = &SyntaxError{0, "approval users must be account IDs, usernames, email addresses, display names, or currentUser()"}
				return ""
			}
			value = c.user
		}
		if value == "" {
			c.err = &SyntaxError{0, "approval user cannot be empty"}
			return ""
		}
		user := c.arg(value)
		parts = append(parts, "EXISTS (SELECT 1 FROM users approval_user WHERE approval_user.id=approval_actor.user_id AND (approval_user.id="+user+" OR lower(approval_user.email)=lower("+user+") OR lower(split_part(approval_user.email,'@',1))=lower("+user+") OR lower(COALESCE(approval_user.username,''))=lower("+user+") OR lower(approval_user.display_name)=lower("+user+")))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (c *compiler) historyClause(cl HistoryClause) string {
	keys := map[string]string{
		"status": "status", "assignee": "assignee", "reporter": "reporter",
		"priority": "priority", "parent": "parent", "labels": "labels",
		"summary": "summary", "description": "description", "security": "security",
		"fixversion": "fixVersions", "affectedversion": "versions",
		"resolution": "resolution",
	}
	key, ok := keys[cl.Field]
	if !ok {
		c.err = &SyntaxError{0, "history is not searchable for field: " + cl.Field}
		return ""
	}
	base := []string{
		"ah.workspace_id=i.workspace_id",
		"ah.entity_type='issue'",
		"ah.entity_id=i.id",
		"ah.payload->'diff' ? '" + key + "'",
	}
	for _, predicate := range cl.Predicates {
		switch predicate.Kind {
		case "from", "to":
			base = append(base, c.historyValueMatch(key, predicate.Kind, predicate.Values))
		case "by":
			base = append(base, "ah.actor_id="+c.arg(c.fieldValue("assignee", predicate.Values[0])))
		case "before", "after":
			op := "<"
			if predicate.Kind == "after" {
				op = ">"
			}
			base = append(base, "ah.created_at"+op+c.datePredicateSQL(predicate.Values[0]))
		case "during":
			base = append(base, "ah.created_at BETWEEN "+c.datePredicateSQL(predicate.Values[0])+
				" AND "+c.datePredicateSQL(predicate.Values[1]))
		}
	}
	if cl.Op == "changed" {
		return "EXISTS (SELECT 1 FROM actions ah WHERE " + strings.Join(base, " AND ") + ")"
	}

	match := c.historyValueMatch(key, "either", cl.Values)
	exists := "EXISTS (SELECT 1 FROM actions ah WHERE " + strings.Join(append(base, match), " AND ") + ")"
	if current, present := c.res.Columns[cl.Field]; present && len(cl.Predicates) == 0 {
		currentMatches := make([]string, 0, len(cl.Values))
		for _, value := range cl.Values {
			currentMatches = append(currentMatches, "lower(COALESCE("+current+"::text,''))=lower("+c.arg(c.historyValue(cl.Field, value))+"::text)")
		}
		exists = "(" + exists + " OR " + strings.Join(currentMatches, " OR ") + ")"
	}
	if cl.Op == "wasnot" || cl.Op == "wasnotin" {
		return "NOT (" + exists + ")"
	}
	return exists
}

// historyValue is a value as history compares it. Unresolved is the only
// value a clause reads as the absence of one, and the log records that
// absence as an empty value rather than as nothing at all.
func (c *compiler) historyValue(field, value string) any {
	compared := c.fieldValue(field, value)
	if compared == nil {
		return ""
	}
	return compared
}

func (c *compiler) historyValueMatch(key, side string, values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		placeholder := c.arg(c.historyValue(key, value))
		var columns []string
		switch side {
		case "from":
			columns = []string{"from", "fromString"}
		case "to":
			columns = []string{"to", "toString"}
		default:
			columns = []string{"from", "fromString", "to", "toString"}
		}
		matches := make([]string, 0, len(columns))
		for _, column := range columns {
			matches = append(matches, "lower(COALESCE(ah.payload->'diff'->'"+key+"'->>'"+column+"',''))=lower("+placeholder+"::text)")
		}
		parts = append(parts, "("+strings.Join(matches, " OR ")+")")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (c *compiler) labelsClause(cl Clause) string {
	array := "i.labels"
	nonempty := "cardinality(" + array + ") > 0"
	switch cl.Op {
	case "=":
		return c.arg(cl.Values[0]) + " = ANY(" + array + ")"
	case "!=":
		return "(" + nonempty + " AND NOT (" + c.arg(cl.Values[0]) + " = ANY(" + array + ")))"
	case "~":
		return "array_to_string(" + array + ",' ') ILIKE " + c.arg("%"+cl.Values[0]+"%")
	case "!~":
		return "(" + nonempty + " AND array_to_string(" + array + ",' ') NOT ILIKE " + c.arg("%"+cl.Values[0]+"%") + ")"
	case "in", "notin":
		parts := make([]string, 0, len(cl.Values))
		for _, value := range cl.Values {
			parts = append(parts, c.arg(value)+" = ANY("+array+")")
		}
		membership := "(" + strings.Join(parts, " OR ") + ")"
		if cl.Op == "notin" {
			return "(" + nonempty + " AND NOT " + membership + ")"
		}
		return membership
	case "empty":
		return "cardinality(" + array + ") = 0"
	case "notempty":
		return nonempty
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op}
	return ""
}

// remoteLinkMatch matches the work items carrying a remote link with one of
// the global ids named, which is how Jira finds work by what another system
// calls it.
func (c *compiler) remoteLinkMatch(name string, args []string) string {
	if len(args) == 0 || len(args) > 100 {
		c.err = &SyntaxError{0, name + "() takes between 1 and 100 global ids"}
		return ""
	}
	placeholders := make([]string, 0, len(args))
	for _, id := range args {
		trimmed := strings.Trim(strings.TrimSpace(id), `"'`)
		if trimmed == "" {
			c.err = &SyntaxError{0, name + "() global ids cannot be empty"}
			return ""
		}
		placeholders = append(placeholders, c.arg(trimmed))
	}
	return "EXISTS (SELECT 1 FROM remote_issue_links remote_link WHERE remote_link.issue_id=i.id AND remote_link.global_id IN (" +
		strings.Join(placeholders, ",") + "))"
}

// maxFilterDepth bounds how many saved filters one query may lead through.
const maxFilterDepth = 10

// savedFilterClause matches the work a saved filter matches, which is how
// Jira's filter field searches: the filter's own query is compiled in place.
func (c *compiler) savedFilterClause(cl Clause) string {
	if c.res.FilterJQL == nil {
		c.err = &SyntaxError{0, "saved filters are not searchable here"}
		return ""
	}
	switch cl.Op {
	case "=", "!=", "in", "notin":
	default:
		c.err = &SyntaxError{0, "filter supports =, !=, IN and NOT IN"}
		return ""
	}
	if len(c.filters) >= maxFilterDepth {
		c.err = &SyntaxError{0, "a filter leads through too many other filters"}
		return ""
	}
	matches := make([]string, 0, len(cl.Values))
	for _, value := range cl.Values {
		named := strings.Trim(strings.TrimSpace(value), `"'`)
		for _, seen := range c.filters {
			if strings.EqualFold(seen, named) {
				c.err = &SyntaxError{0, "filter " + strconv.Quote(named) + " leads back to itself"}
				return ""
			}
		}
		text, ok := c.res.FilterJQL(c.user, named)
		if !ok {
			c.err = &SyntaxError{0, "filter " + strconv.Quote(named) + " does not exist or you do not have permission to see it"}
			return ""
		}
		parsed, parseErr := Parse(text)
		if parseErr != nil {
			c.err = &SyntaxError{0, "filter " + strconv.Quote(named) + " holds a query that no longer parses"}
			return ""
		}
		c.filters = append(c.filters, named)
		sql := c.node(parsed.Root)
		c.filters = c.filters[:len(c.filters)-1]
		if c.err != nil {
			return ""
		}
		matches = append(matches, "("+sql+")")
	}
	match := "(" + strings.Join(matches, " OR ") + ")"
	if cl.Op == "!=" || cl.Op == "notin" {
		return "(NOT " + match + ")"
	}
	return match
}

// freeTextClause searches the text of a work item the way Jira's text field
// does: its own text and the text of its comments, with ~ and !~ alone.
func (c *compiler) freeTextClause(cl Clause) string {
	if cl.Op != "~" && cl.Op != "!~" {
		c.err = &SyntaxError{0, "text supports only ~ and !~"}
		return ""
	}
	term := c.arg("%" + cl.Values[0] + "%")
	parts := make([]string, 0, len(c.res.TextColumns)+1)
	for _, col := range c.res.TextColumns {
		parts = append(parts, col+" ILIKE "+term)
	}
	parts = append(parts, commentTextMatch(term))
	match := "(" + strings.Join(parts, " OR ") + ")"
	if cl.Op == "!~" {
		return "(NOT " + match + ")"
	}
	return match
}

// commentTextMatch is one work item's comments holding a phrase.
func commentTextMatch(term string) string {
	return "EXISTS (SELECT 1 FROM comments comment_row WHERE comment_row.issue_id=i.id AND comment_row.body::text ILIKE " + term + ")"
}

// commentClause searches only the comments, as Jira's comment field does.
func (c *compiler) commentClause(cl Clause) string {
	if cl.Op != "~" && cl.Op != "!~" {
		c.err = &SyntaxError{0, "comment supports only ~ and !~"}
		return ""
	}
	match := commentTextMatch(c.arg("%" + cl.Values[0] + "%"))
	if cl.Op == "!~" {
		return "(NOT " + match + ")"
	}
	return match
}

// userSetClause matches the people attached to a work item rather than named
// on it -- who watches it, who voted for it -- which is how Jira's watcher
// and voter fields search.
func (c *compiler) userSetClause(cl Clause, table, alias, issueColumn string) string {
	any := "EXISTS (SELECT 1 FROM " + table + " " + alias + " WHERE " + alias + "." + issueColumn + "=i.id)"
	switch cl.Op {
	case "empty":
		return "(NOT " + any + ")"
	case "notempty":
		return any
	case "=", "!=", "in", "notin":
	default:
		c.err = &SyntaxError{0, cl.Field + " supports =, !=, IN, NOT IN, IS EMPTY and IS NOT EMPTY"}
		return ""
	}
	matches := make([]string, 0, len(cl.Values))
	for _, value := range cl.Values {
		user := value
		if name, args, ok := splitFunction(value); ok {
			if !strings.EqualFold(name, "currentUser") || len(args) != 0 {
				c.err = &SyntaxError{0, "unsupported function " + name + "() for " + cl.Field}
				return ""
			}
			user = c.user
		}
		matches = append(matches, "EXISTS (SELECT 1 FROM "+table+" "+alias+" WHERE "+alias+"."+issueColumn+"=i.id AND "+alias+".user_id="+c.arg(user)+")")
	}
	match := "(" + strings.Join(matches, " OR ") + ")"
	if cl.Op == "!=" || cl.Op == "notin" {
		return "(NOT " + match + ")"
	}
	return match
}

// attachmentsClause answers whether a work item has files, which is all Jira
// asks of the attachments field.
func (c *compiler) attachmentsClause(cl Clause) string {
	any := "EXISTS (SELECT 1 FROM attachments attachment_row WHERE attachment_row.issue_id=i.id)"
	switch cl.Op {
	case "empty":
		return "(NOT " + any + ")"
	case "notempty":
		return any
	}
	c.err = &SyntaxError{0, "attachments supports only IS EMPTY and IS NOT EMPTY"}
	return ""
}

// issueLinkTypeClause matches work items taking part in a link of a named
// type, by the type's name or by either direction's wording.
func (c *compiler) issueLinkTypeClause(cl Clause) string {
	any := "EXISTS (SELECT 1 FROM issue_links link_row WHERE link_row.inward_id=i.id OR link_row.outward_id=i.id)"
	switch cl.Op {
	case "empty":
		return "(NOT " + any + ")"
	case "notempty":
		return any
	case "=", "!=", "in", "notin":
	default:
		c.err = &SyntaxError{0, "issueLinkType supports =, !=, IN, NOT IN, IS EMPTY and IS NOT EMPTY"}
		return ""
	}
	matches := make([]string, 0, len(cl.Values))
	for _, value := range cl.Values {
		if _, _, ok := splitFunction(value); ok {
			c.err = &SyntaxError{0, "issueLinkType takes link type names, not functions"}
			return ""
		}
		name := c.arg(value)
		matches = append(matches, "EXISTS (SELECT 1 FROM issue_links link_row JOIN issue_link_types link_type ON link_type.id=link_row.link_type_id"+
			" WHERE (link_row.inward_id=i.id OR link_row.outward_id=i.id)"+
			" AND (lower(link_type.name)=lower("+name+") OR lower(link_type.inward)=lower("+name+") OR lower(link_type.outward)=lower("+name+")))")
	}
	match := "(" + strings.Join(matches, " OR ") + ")"
	if cl.Op == "!=" || cl.Op == "notin" {
		return "(" + any + " AND NOT " + match + ")"
	}
	return match
}

func (c *compiler) componentClause(cl Clause) string {
	array := "CASE WHEN jsonb_typeof(i.fields->'components')='array' THEN i.fields->'components' ELSE '[]'::jsonb END"
	legacy := "NULLIF(i.fields->>'component','')"
	nonempty := "(jsonb_array_length(" + array + ")>0 OR " + legacy + " IS NOT NULL)"
	matches := make([]string, 0, len(cl.Values))
	for _, value := range cl.Values {
		if name, args, ok := splitFunction(value); ok {
			if !strings.EqualFold(name, "componentsLeadByUser") || (cl.Op != "in" && cl.Op != "notin") || len(args) > 1 {
				c.err = &SyntaxError{0, "componentsLeadByUser() is supported only with component IN or NOT IN and accepts at most one user"}
				return ""
			}
			user := c.user
			if len(args) == 1 {
				user = strings.TrimSpace(args[0])
				if function, functionArgs, nested := splitFunction(user); nested {
					if !strings.EqualFold(function, "currentUser") || len(functionArgs) != 0 {
						c.err = &SyntaxError{0, "componentsLeadByUser() user must be an account ID or currentUser()"}
						return ""
					}
					user = c.user
				}
				if user == "" {
					c.err = &SyntaxError{0, "componentsLeadByUser() user cannot be empty"}
					return ""
				}
			}
			userPH := c.arg(user)
			matches = append(matches, "EXISTS (SELECT 1 FROM project_components component_lead WHERE component_lead.project_id=i.project_id AND component_lead.lead_account_id="+userPH+" AND EXISTS (SELECT 1 FROM jsonb_array_elements("+array+") component_ref WHERE component_ref->>'id'=component_lead.id))")
			continue
		}
		valuePH := c.arg(value)
		matches = append(matches, "(EXISTS (SELECT 1 FROM jsonb_array_elements("+array+") component_ref WHERE component_ref->>'id'="+valuePH+" OR lower(component_ref->>'name')=lower("+valuePH+")) OR lower("+legacy+")=lower("+valuePH+"))")
	}
	match := "(" + strings.Join(matches, " OR ") + ")"
	switch cl.Op {
	case "=", "in":
		return match
	case "!=", "notin":
		return "(" + nonempty + " AND NOT " + match + ")"
	case "empty":
		return "NOT " + nonempty
	case "notempty":
		return nonempty
	default:
		c.err = &SyntaxError{0, "unsupported operator " + cl.Op + " for component"}
		return ""
	}
}

// fieldValue resolves semantic values: status names, currentUser(), EMPTY/null.
func (c *compiler) fieldValue(field, value string) any {
	if c.res.DurationFields[field] || c.res.NumberFields[field] {
		text := strings.Trim(strings.TrimSpace(value), `"'`)
		if c.res.NumberFields[field] {
			number, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				c.err = &SyntaxError{0, "invalid number " + strconv.Quote(value) + " for " + field}
				return nil
			}
			return number
		}
		// Durations use Jira's default working time: 8 hours a day, 5 days a week.
		seconds, err := models.ParseJiraDuration(text, models.TimeTrackingConfiguration{DefaultUnit: "minute", WorkingHoursPerDay: 8, WorkingDaysPerWeek: 5})
		if err != nil {
			c.err = &SyntaxError{0, "invalid duration " + strconv.Quote(value) + " for " + field}
			return nil
		}
		return seconds
	}
	// Unresolved is Jira's name for having no resolution at all, so it compares
	// as the absence of one rather than as a resolution called "Unresolved".
	if field == "resolution" && strings.EqualFold(strings.Trim(strings.TrimSpace(value), `"'`), "unresolved") {
		return nil
	}
	if name, args, ok := splitFunction(value); ok {
		switch strings.ToLower(name) {
		case "currentuser":
			if len(args) == 0 && (field == "assignee" || field == "reporter" || field == "creator") {
				return c.user
			}
		case "now", "startofday", "endofday", "startofweek", "endofweek", "startofmonth", "endofmonth", "startofyear", "endofyear":
			resolved, err := resolveDateFunction(name, args, c.now)
			if err == nil {
				return resolved
			}
			c.err = &SyntaxError{0, err.Error()}
			return value
		}
		c.err = &SyntaxError{0, "unsupported function " + name + "() for " + field}
		return value
	}
	if c.res.DateFields[field] {
		if len(value) >= 2 {
			if relative, err := applyDateIncrement(c.now, value); err == nil {
				return relative
			}
		}
		for _, layout := range []string{time.RFC3339, "2006/01/02 15:04", "2006-01-02 15:04", "2006/01/02", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
				return parsed
			}
		}
		c.err = &SyntaxError{0, "invalid date value " + strconv.Quote(value)}
		return value
	}
	switch field {
	case "id":
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 {
			c.err = &SyntaxError{0, "issue ID must be a positive integer"}
			return value
		}
		return id
	case "assignee", "reporter", "creator":
		if strings.EqualFold(value, "empty") || strings.EqualFold(value, "null") {
			return nil
		}
		return value
	case "project":
		return strings.ToUpper(value)
	}
	if !c.knownValue(field, value) {
		return value
	}
	return value
}

// knownValue reports whether a field can hold the value a query names, and
// records Jira's own error when it cannot. A field the site has no catalogue
// for is not checked.
func (c *compiler) knownValue(field, value string) bool {
	known, checked := c.res.KnownValues[field]
	if !checked {
		return true
	}
	text := strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))
	if text == "" || known[text] {
		return true
	}
	c.err = &ValueError{Field: field, Value: strings.Trim(strings.TrimSpace(value), `"'`)}
	return false
}

func (c *compiler) datePredicateSQL(value string) string {
	if name, _, ok := splitFunction(value); ok && (strings.EqualFold(name, "currentLogin") || strings.EqualFold(name, "lastLogin")) {
		return c.loginDateSQL(value)
	}
	return c.arg(c.fieldValue("updated", value))
}

func (c *compiler) loginDateSQL(value string) string {
	name, args, ok := splitFunction(value)
	if !ok || (!strings.EqualFold(name, "currentLogin") && !strings.EqualFold(name, "lastLogin")) {
		c.err = &SyntaxError{0, "expected currentLogin() or lastLogin()"}
		return ""
	}
	if len(args) != 0 {
		c.err = &SyntaxError{0, name + "() does not accept arguments"}
		return ""
	}
	if c.user == "" {
		c.err = &SyntaxError{0, name + "() requires an authenticated user"}
		return ""
	}
	column := "current_started_at"
	if strings.EqualFold(name, "lastLogin") {
		column = "previous_started_at"
	}
	return "(SELECT login_state." + column + " FROM user_login_state login_state WHERE login_state.user_id=" + c.arg(c.user) + ")"
}

func containsJQLFunction(values []string, names ...string) bool {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	for _, value := range values {
		name, _, ok := splitFunction(value)
		if ok && wanted[strings.ToLower(name)] {
			return true
		}
	}
	return false
}

func (c *compiler) listResult(cl Clause, match, nonempty string) string {
	switch cl.Op {
	case "=", "in":
		return match
	case "!=", "notin":
		return "(" + nonempty + " AND NOT (" + match + "))"
	case "empty":
		return "NOT (" + nonempty + ")"
	case "notempty":
		return nonempty
	default:
		c.err = &SyntaxError{0, "unsupported list operator " + cl.Op + " for " + cl.Field}
		return ""
	}
}

func (c *compiler) sprintClause(cl Clause) string {
	nonempty := "EXISTS (SELECT 1 FROM sprint_issues sprint_any WHERE sprint_any.issue_id=i.id)"
	if cl.Op == "empty" || cl.Op == "notempty" {
		return c.listResult(cl, "FALSE", nonempty)
	}
	matches := []string{}
	for _, value := range cl.Values {
		if name, args, ok := splitFunction(value); ok {
			state := map[string]string{"opensprints": "active", "closedsprints": "closed", "futuresprints": "future"}[strings.ToLower(name)]
			if state == "" {
				c.err = &SyntaxError{0, "unsupported function " + name + "() for sprint"}
				return ""
			}
			if len(args) != 0 {
				c.err = &SyntaxError{0, name + "() does not accept arguments"}
				return ""
			}
			matches = append(matches, "EXISTS (SELECT 1 FROM sprint_issues sprint_match JOIN sprints sprint_value ON sprint_value.id=sprint_match.sprint_id WHERE sprint_match.issue_id=i.id AND sprint_value.state="+c.arg(state)+")")
			continue
		}
		ph := c.arg(value)
		matches = append(matches, "EXISTS (SELECT 1 FROM sprint_issues sprint_match JOIN sprints sprint_value ON sprint_value.id=sprint_match.sprint_id WHERE sprint_match.issue_id=i.id AND (sprint_value.id="+ph+" OR lower(sprint_value.name)=lower("+ph+")))")
	}
	return c.listResult(cl, "("+strings.Join(matches, " OR ")+")", nonempty)
}

func (c *compiler) issueTypeListClause(cl Clause) string {
	matches := []string{}
	for _, value := range cl.Values {
		if name, args, ok := splitFunction(value); ok {
			if len(args) != 0 {
				c.err = &SyntaxError{0, name + "() does not accept arguments"}
				return ""
			}
			switch strings.ToLower(name) {
			case "standardissuetypes", "standardworktypes":
				matches = append(matches, "NOT it.subtask")
			case "subtaskissuetypes", "subtaskworktypes":
				matches = append(matches, "it.subtask")
			default:
				c.err = &SyntaxError{0, "unsupported function " + name + "() for issuetype"}
				return ""
			}
			continue
		}
		ph := c.arg(value)
		matches = append(matches, "(it.id="+ph+" OR lower(it.name)=lower("+ph+"))")
	}
	return c.listResult(cl, "("+strings.Join(matches, " OR ")+")", "TRUE")
}

func (c *compiler) userListClause(cl Clause) string {
	col := c.res.Columns[cl.Field]
	matches := []string{}
	for _, value := range cl.Values {
		if name, args, ok := splitFunction(value); ok {
			if !strings.EqualFold(name, "membersOf") || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				c.err = &SyntaxError{0, "membersOf() requires one group name or ID"}
				return ""
			}
			group := c.arg(args[0])
			matches = append(matches, "EXISTS (SELECT 1 FROM sites member_site JOIN directories member_directory ON member_directory.organization_id=member_site.organization_id AND member_directory.active JOIN groups member_group ON member_group.directory_id=member_directory.id JOIN group_members member_entry ON member_entry.group_id=member_group.id WHERE member_site.workspace_id=i.workspace_id AND member_entry.user_id="+col+" AND (member_group.id::text="+group+" OR lower(member_group.name)=lower("+group+")))")
			continue
		}
		matches = append(matches, col+"="+c.arg(c.fieldValue(cl.Field, value)))
	}
	return c.listResult(cl, "("+strings.Join(matches, " OR ")+")", col+" IS NOT NULL")
}

func (c *compiler) issueFunctionClause(cl Clause) string {
	if len(cl.Values) != 1 {
		c.err = &SyntaxError{0, "issue-list functions must be the only list value"}
		return ""
	}
	name, args, ok := splitFunction(cl.Values[0])
	if !ok {
		c.err = &SyntaxError{0, "invalid issue-list function"}
		return ""
	}
	var match string
	switch strings.ToLower(name) {
	case "linkedissues", "linkedworkitems":
		match = c.linkedIssuesMatch(name, args)
	case "watchedissues", "watchedworkitems":
		if len(args) != 0 {
			c.err = &SyntaxError{0, name + "() does not accept arguments"}
			return ""
		}
		match = "EXISTS (SELECT 1 FROM watchers watched WHERE watched.issue_id=i.id AND watched.user_id=" + c.arg(c.user) + ")"
	case "votedissues", "votedworkitems":
		if len(args) != 0 {
			c.err = &SyntaxError{0, name + "() does not accept arguments"}
			return ""
		}
		match = "EXISTS (SELECT 1 FROM issue_votes voted WHERE voted.issue_id=i.id AND voted.user_id=" + c.arg(c.user) + ")"
	case "issueswithremotelinksbyglobalid", "workitemswithremotelinksbyglobalid":
		match = c.remoteLinkMatch(name, args)
	case "issuehistory", "workitemhistory":
		if len(args) != 0 {
			c.err = &SyntaxError{0, name + "() does not accept arguments"}
			return ""
		}
		// What the person searching has opened, which is what Jira's
		// issueHistory() means by recently viewed.
		match = "EXISTS (SELECT 1 FROM issue_views viewed WHERE viewed.issue_id=i.id AND viewed.user_id=" + c.arg(c.user) + ")"
	case "updatedby":
		match = c.updatedByMatch(args)
	default:
		c.err = &SyntaxError{0, "unsupported function " + name + "() for " + cl.Field}
		return ""
	}
	if c.err != nil {
		return ""
	}
	return c.listResult(cl, match, "TRUE")
}

func (c *compiler) linkedIssuesMatch(name string, args []string) string {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		c.err = &SyntaxError{0, name + "() requires an issue key and optional link types"}
		return ""
	}
	key := c.arg(strings.ToUpper(args[0]))
	conditions := []string{
		"linked.workspace_id=i.workspace_id",
		"(linked.inward_id=i.id OR linked.outward_id=i.id)",
		"upper(linked_source.key)=" + key,
	}
	joinType := ""
	if len(args) > 1 {
		linkTypes := make([]string, 0, len(args)-1)
		for _, value := range args[1:] {
			if strings.TrimSpace(value) == "" {
				c.err = &SyntaxError{0, name + "() link types cannot be empty"}
				return ""
			}
			linkType := c.arg(value)
			linkTypes = append(linkTypes, "lower(linked_type.name)=lower("+linkType+") OR lower(linked_type.inward)=lower("+linkType+") OR lower(linked_type.outward)=lower("+linkType+")")
		}
		joinType = " JOIN issue_link_types linked_type ON linked_type.id=linked.link_type_id"
		conditions = append(conditions, "("+strings.Join(linkTypes, " OR ")+")")
	}
	return "EXISTS (SELECT 1 FROM issue_links linked" + joinType + " JOIN issues linked_source ON linked_source.id=CASE WHEN linked.inward_id=i.id THEN linked.outward_id ELSE linked.inward_id END WHERE " + strings.Join(conditions, " AND ") + ")"
}

func (c *compiler) updatedByMatch(args []string) string {
	if len(args) < 1 || len(args) > 3 || strings.TrimSpace(args[0]) == "" {
		c.err = &SyntaxError{0, "updatedBy() requires a user and optional from/to dates"}
		return ""
	}
	user := args[0]
	if name, functionArgs, ok := splitFunction(user); ok {
		if !strings.EqualFold(name, "currentUser") || len(functionArgs) != 0 {
			c.err = &SyntaxError{0, "updatedBy() user must be an account ID or currentUser()"}
			return ""
		}
		user = c.user
	}
	conditions := []string{
		"updated_action.workspace_id=i.workspace_id",
		"updated_action.entity_type='issue'",
		"updated_action.entity_id=i.id",
		"updated_action.actor_id=" + c.arg(user),
	}
	for index, op := range []string{">=", "<="} {
		if len(args) <= index+1 || strings.TrimSpace(args[index+1]) == "" {
			continue
		}
		value := c.fieldValue("updated", args[index+1])
		if c.err != nil {
			return ""
		}
		conditions = append(conditions, "updated_action.created_at"+op+c.arg(value))
	}
	return "EXISTS (SELECT 1 FROM actions updated_action WHERE " + strings.Join(conditions, " AND ") + ")"
}

func (c *compiler) projectFunctionClause(cl Clause) string {
	if len(cl.Values) != 1 {
		c.err = &SyntaxError{0, "project-list functions must be the only list value"}
		return ""
	}
	name, args, ok := splitFunction(cl.Values[0])
	if !ok {
		c.err = &SyntaxError{0, "invalid project-list function"}
		return ""
	}
	var match string
	switch strings.ToLower(name) {
	case "projectsleadbyuser", "spacesleadbyuser":
		if len(args) > 1 {
			c.err = &SyntaxError{0, name + "() accepts at most one user"}
			return ""
		}
		user := c.user
		if len(args) == 1 {
			user = args[0]
		}
		if name, functionArgs, ok := splitFunction(user); ok {
			if !strings.EqualFold(name, "currentUser") || len(functionArgs) != 0 {
				c.err = &SyntaxError{0, name + "() is not a supported user argument"}
				return ""
			}
			user = c.user
		}
		if strings.TrimSpace(user) == "" {
			c.err = &SyntaxError{0, name + "() user cannot be empty"}
			return ""
		}
		match = "pr.lead_account_id=" + c.arg(user)
	case "projectswhereuserhaspermission", "spaceswhereuserhaspermission":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			c.err = &SyntaxError{0, name + "() requires one permission key"}
			return ""
		}
		// The permission is evaluated for the searching user against each
		// project the search reaches, by the same rule the rest of the site
		// grants a project permission.
		match = "jira_has_project_permission(pr.workspace_id, pr.id, " + c.arg(c.user) + ", NULL, upper(" + c.arg(strings.TrimSpace(args[0])) + "))"
	case "projectswhereuserhasrole", "spaceswhereuserhasrole":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			c.err = &SyntaxError{0, name + "() requires one project role name or ID"}
			return ""
		}
		role := c.arg(args[0])
		user := c.arg(c.user)
		match = "EXISTS (SELECT 1 FROM role_bindings project_role WHERE project_role.scope_type='project' AND project_role.scope_id=pr.id AND lower(project_role.role_key)=lower(" + role + ") AND ((project_role.principal_type='user' AND project_role.principal_id=" + user + ") OR (project_role.principal_type='group' AND EXISTS (SELECT 1 FROM group_members project_role_member WHERE project_role_member.group_id::text=project_role.principal_id AND project_role_member.user_id=" + user + "))))"
	default:
		c.err = &SyntaxError{0, "unsupported function " + name + "() for project"}
		return ""
	}
	return c.listResult(cl, match, "TRUE")
}

func splitFunction(value string) (string, []string, bool) {
	open := strings.IndexByte(value, '(')
	if open < 1 || !strings.HasSuffix(value, ")") {
		return "", nil, false
	}
	name := value[:open]
	body := value[open+1 : len(value)-1]
	if body == "" {
		return name, nil, true
	}
	return name, strings.Split(body, ","), true
}

func resolveDateFunction(name string, args []string, now time.Time) (time.Time, error) {
	if len(args) > 1 {
		return time.Time{}, fmt.Errorf("%s() accepts at most one increment", name)
	}
	value := now.UTC()
	switch strings.ToLower(name) {
	case "now":
		if len(args) != 0 {
			return time.Time{}, fmt.Errorf("now() does not accept arguments")
		}
	case "startofday":
		value = time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	case "endofday":
		value = time.Date(value.Year(), value.Month(), value.Day()+1, 0, 0, 0, -1, time.UTC)
	case "startofweek", "endofweek":
		days := int(value.Weekday())
		value = time.Date(value.Year(), value.Month(), value.Day()-days, 0, 0, 0, 0, time.UTC)
		if strings.EqualFold(name, "endofweek") {
			value = value.AddDate(0, 0, 7).Add(-time.Nanosecond)
		}
	case "startofmonth":
		value = time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "endofmonth":
		value = time.Date(value.Year(), value.Month()+1, 1, 0, 0, 0, -1, time.UTC)
	case "startofyear":
		value = time.Date(value.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	case "endofyear":
		value = time.Date(value.Year()+1, time.January, 1, 0, 0, 0, -1, time.UTC)
	default:
		return time.Time{}, fmt.Errorf("unsupported date function %s()", name)
	}
	if len(args) == 1 {
		var err error
		defaultUnit := map[string]string{
			"startofday": "d", "endofday": "d", "startofweek": "w", "endofweek": "w",
			"startofmonth": "M", "endofmonth": "M", "startofyear": "y", "endofyear": "y",
		}[strings.ToLower(name)]
		value, err = applyDateIncrementWithDefault(value, args[0], defaultUnit)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s(): %w", name, err)
		}
	}
	return value, nil
}

func applyDateIncrement(value time.Time, raw string) (time.Time, error) {
	return applyDateIncrementWithDefault(value, raw, "")
}

func applyDateIncrementWithDefault(value time.Time, raw, defaultUnit string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("increment must include a number")
	}
	if _, err := strconv.Atoi(raw); err == nil && defaultUnit != "" {
		raw += defaultUnit
	}
	if len(raw) < 2 {
		return time.Time{}, fmt.Errorf("increment must include a number and unit")
	}
	unit := raw[len(raw)-1:]
	amount, err := strconv.Atoi(raw[:len(raw)-1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid increment %q", raw)
	}
	switch unit {
	case "y":
		return value.AddDate(amount, 0, 0), nil
	case "M":
		return value.AddDate(0, amount, 0), nil
	case "w":
		return value.AddDate(0, 0, amount*7), nil
	case "d":
		return value.AddDate(0, 0, amount), nil
	case "h":
		return value.Add(time.Duration(amount) * time.Hour), nil
	case "m":
		return value.Add(time.Duration(amount) * time.Minute), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported increment unit %q", unit)
	}
}

// versionClause searches every version reference by ID or name. Negated
// membership excludes empty fields, matching Jira multi-value field semantics.
func (c *compiler) versionClause(cl Clause) string {
	field := "fixVersions"
	if cl.Field == "affectedversion" {
		field = "versions"
	}
	array := "COALESCE(i.fields->'" + field + "','[]'::jsonb)"
	nonempty := "jsonb_array_length(" + array + ") > 0"
	switch cl.Op {
	case "empty":
		return "jsonb_array_length(" + array + ") = 0"
	case "notempty":
		return nonempty
	case "=", "!=", "in", "notin":
		matches := []string{}
		for _, value := range cl.Values {
			if name, args, ok := splitFunction(value); ok {
				match := c.versionFunctionMatch("v", name, args)
				if c.err != nil {
					return ""
				}
				matches = append(matches, match)
				continue
			}
			ph := c.arg(value)
			matches = append(matches, "(v->>'id' = "+ph+" OR lower(v->>'name') = lower("+ph+"))")
		}
		exists := "EXISTS (SELECT 1 FROM jsonb_array_elements(" + array + ") v WHERE " + strings.Join(matches, " OR ") + ")"
		if cl.Op == "!=" || cl.Op == "notin" {
			return "(" + nonempty + " AND NOT " + exists + ")"
		}
		return exists
	default:
		c.err = &SyntaxError{0, "unsupported version operator " + cl.Op}
		return ""
	}
}

func (c *compiler) versionFunctionMatch(element, name string, args []string) string {
	switch strings.ToLower(name) {
	case "releasedversions", "unreleasedversions":
		if len(args) > 1 {
			c.err = &SyntaxError{0, name + "() accepts at most one project"}
			return ""
		}
		released := strings.EqualFold(name, "releasedVersions")
		conditions := []string{
			"version_value.id=" + element + "->>'id'",
			"version_value.project_id=i.project_id",
			"version_value.released=" + strconv.FormatBool(released),
		}
		join := ""
		if len(args) == 1 {
			if strings.TrimSpace(args[0]) == "" {
				c.err = &SyntaxError{0, name + "() project cannot be empty"}
				return ""
			}
			project := c.arg(args[0])
			join = " JOIN projects version_project ON version_project.id=version_value.project_id"
			conditions = append(conditions, "(version_project.id="+project+" OR upper(version_project.key)=upper("+project+"))")
		}
		return "EXISTS (SELECT 1 FROM project_versions version_value" + join + " WHERE " + strings.Join(conditions, " AND ") + ")"
	case "latestreleasedversion", "earliestunreleasedversion":
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			c.err = &SyntaxError{0, name + "() requires one project key or ID"}
			return ""
		}
		project := c.arg(args[0])
		released, direction := true, "DESC"
		if strings.EqualFold(name, "earliestUnreleasedVersion") {
			released, direction = false, "ASC"
		}
		return element + "->>'id'=(SELECT version_value.id FROM project_versions version_value JOIN projects version_project ON version_project.id=version_value.project_id WHERE version_value.project_id=i.project_id AND version_value.released=" + strconv.FormatBool(released) + " AND (version_project.id=" + project + " OR upper(version_project.key)=upper(" + project + ")) ORDER BY version_value.release_date " + direction + " NULLS LAST,version_value.position " + direction + ",version_value.id " + direction + " LIMIT 1)"
	default:
		c.err = &SyntaxError{0, "unsupported function " + name + "() for version"}
		return ""
	}
}

// jsonArrayClause matches a field holding a JSON array of ids.
func (c *compiler) jsonArrayClause(expression string, cl Clause) string {
	array := "COALESCE(CASE WHEN jsonb_typeof(" + expression + ")='array' THEN " + expression + " END,'[]'::jsonb)"
	nonempty := "jsonb_array_length(" + array + ") > 0"
	switch cl.Op {
	case "=":
		return "jsonb_exists(" + array + ", " + c.arg(cl.Values[0]) + ")"
	case "!=":
		return "(" + nonempty + " AND NOT jsonb_exists(" + array + ", " + c.arg(cl.Values[0]) + "))"
	case "in", "notin":
		parts := make([]string, 0, len(cl.Values))
		for _, value := range cl.Values {
			parts = append(parts, "jsonb_exists("+array+", "+c.arg(value)+")")
		}
		membership := "(" + strings.Join(parts, " OR ") + ")"
		if cl.Op == "notin" {
			return "(" + nonempty + " AND NOT " + membership + ")"
		}
		return membership
	case "empty":
		return "jsonb_array_length(" + array + ") = 0"
	case "notempty":
		return nonempty
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op}
	return ""
}

// customValueCandidates is the SQL text[] of stored ids a query value stands
// for: the value itself, and the options or groups of that name, or the caller
// for currentUser().
func (c *compiler) customValueCandidates(field CustomValueField, value string) string {
	if name, args, ok := splitFunction(value); ok {
		if strings.EqualFold(name, "currentUser") && len(args) == 0 && (field.Type == models.CustomFieldUser || field.Type == models.CustomFieldMultiUser) {
			return "ARRAY[" + c.arg(c.user) + "::text]"
		}
		c.err = &SyntaxError{0, "unsupported function " + name + "() for " + field.ID}
		return "ARRAY[]::text[]"
	}
	literal := "ARRAY[" + c.arg(value) + "::text]"
	switch field.Type {
	case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
		return "(" + literal + " || ARRAY(SELECT o.id::text FROM custom_field_options o JOIN custom_field_contexts x ON x.id=o.context_id WHERE x.field_id=" +
			c.arg(field.ID) + " AND lower(o.value)=lower(" + c.arg(value) + ")))"
	case models.CustomFieldGroup, models.CustomFieldMultiGroup:
		return "(" + literal + " || ARRAY(SELECT g.id::text FROM groups g WHERE g.name=" + c.arg(value) + "))"
	case models.CustomFieldProject:
		return "(" + literal + " || ARRAY(SELECT p.id FROM projects p WHERE upper(p.key)=upper(" + c.arg(value) + ")))"
	case models.CustomFieldVersion, models.CustomFieldMultiVersion:
		return "(" + literal + " || ARRAY(SELECT v.id FROM project_versions v WHERE v.name=" + c.arg(value) + "))"
	case models.CustomFieldTeam:
		return "(" + literal + " || ARRAY(SELECT t.id::text FROM atlassian_teams t WHERE lower(t.name)=lower(" + c.arg(value) + ")))"
	}
	return literal
}

// customValueClause matches option, cascading, user, group and labels custom
// fields by the ids, option values, group names or people a query names.
func (c *compiler) customValueClause(field CustomValueField, cl Clause) string {
	stored := "i.fields->'" + field.ID + "'"
	switch field.Type {
	case models.CustomFieldMultiSelect, models.CustomFieldMultiUser, models.CustomFieldMultiGroup, models.CustomFieldLabels, models.CustomFieldMultiVersion:
		array := "COALESCE(CASE WHEN jsonb_typeof(" + stored + ")='array' THEN " + stored + " END,'[]'::jsonb)"
		nonempty := "jsonb_array_length(" + array + ") > 0"
		matches := func(values []string) string {
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, "jsonb_exists_any("+array+", "+c.customValueCandidates(field, value)+")")
			}
			return "(" + strings.Join(parts, " OR ") + ")"
		}
		switch cl.Op {
		case "=", "in":
			return matches(cl.Values)
		case "!=", "notin":
			return "(" + nonempty + " AND NOT " + matches(cl.Values) + ")"
		case "empty":
			return "jsonb_array_length(" + array + ") = 0"
		case "notempty":
			return nonempty
		}
	case models.CustomFieldCascadingSelect:
		parent := "(" + stored + "->>'parent')"
		child := "(" + stored + "->>'child')"
		match := func(value string) string {
			if name, args, ok := splitFunction(value); ok && strings.EqualFold(name, "cascadeOption") {
				if len(args) == 0 || len(args) > 2 {
					c.err = &SyntaxError{0, "cascadeOption() takes an option and an optional child option"}
					return "FALSE"
				}
				clause := parent + " = ANY(" + c.customValueCandidates(field, args[0]) + ")"
				if len(args) == 2 {
					if strings.EqualFold(args[1], "none") {
						return "(" + clause + " AND " + child + " IS NULL)"
					}
					return "(" + clause + " AND " + child + " = ANY(" + c.customValueCandidates(field, args[1]) + "))"
				}
				return clause
			}
			candidates := c.customValueCandidates(field, value)
			return "(" + parent + " = ANY(" + candidates + ") OR " + child + " = ANY(" + candidates + "))"
		}
		matches := func(values []string) string {
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, match(value))
			}
			return "(" + strings.Join(parts, " OR ") + ")"
		}
		switch cl.Op {
		case "=", "in":
			return matches(cl.Values)
		case "!=", "notin":
			return "(" + parent + " IS NOT NULL AND NOT " + matches(cl.Values) + ")"
		case "empty":
			return parent + " IS NULL"
		case "notempty":
			return parent + " IS NOT NULL"
		}
	default:
		column := "NULLIF(i.fields->>'" + field.ID + "','')"
		matches := func(values []string) string {
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, column+" = ANY("+c.customValueCandidates(field, value)+")")
			}
			return "(" + strings.Join(parts, " OR ") + ")"
		}
		switch cl.Op {
		case "=", "in":
			return matches(cl.Values)
		case "!=", "notin":
			return "(" + column + " IS NOT NULL AND NOT " + matches(cl.Values) + ")"
		case "empty":
			return column + " IS NULL"
		case "notempty":
			return column + " IS NOT NULL"
		}
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op + " for " + field.ID}
	return ""
}
