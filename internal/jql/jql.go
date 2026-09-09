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
		case c == ' ' || c == '\t' || c == '\n':
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
				two := string(c) + string(src[i+1])
				if two == "!=" || two == "!~" || two == "~=" || two == ">=" || two == "<=" {
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
			for j < len(src) && !strings.ContainsRune(" \t\n()',=!~<>", rune(src[j])) {
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

var operators = map[string]string{
	"=": "=", "!=": "!=", "~": "~", "~=": "~=", "!~": "!~", ">": ">", ">=": ">=", "<": "<", "<=": "<=",
}

type parser struct {
	toks []token
	pos  int
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
				mainToks := append(append([]token{}, toks[:i]...), token{"eof", "", toks[i].pos})
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
	field := canonicalField(p.word())
	opTok := p.next()
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
	default:
		return strings.ToLower(field)
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
			p.next()
			return t.text + "(" + strings.Join(args, ",") + ")", nil
		}
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
}

// WithCustomFields extends a resolver with customfield_NNNNN columns and app
// aliases. Values live in issues.fields JSONB; numbers compare numerically.
func WithCustomFields(base FieldResolver, fields []*models.CustomField) FieldResolver {
	res := base
	if res.Columns == nil {
		res.Columns = map[string]string{}
	}
	for _, f := range fields {
		col := `i.fields->>'` + f.ID + `'`
		if f.Type == models.CustomFieldNumber {
			col = `NULLIF(i.fields->>'` + f.ID + `','')::numeric`
		}
		res.Columns[f.ID] = col
		res.Columns[strings.ToLower(f.Name)] = col
		if f.AppKey != "" {
			res.Columns[strings.ToLower(f.AppKey+"__"+f.AppModuleKey)] = col
		}
		res.TextColumns = append(res.TextColumns, `i.fields->>'`+f.ID+`'`)
	}
	return res
}

func DefaultResolver() FieldResolver {
	return FieldResolver{
		Columns: map[string]string{
			"key":            "i.key",
			"issue":          "i.key",
			"id":             "i.jira_id",
			"summary":        "i.summary",
			"description":    "i.description::text",
			"status":         "st.name",
			"statuscategory": "st.category",
			"project":        "pr.key",
			"assignee":       "i.assignee_id",
			"reporter":       "i.reporter_id",
			"creator":        "i.reporter_id",
			"priority":       "pr2.name",
			"issuetype":      "it.name",
			"updated":        "i.updated_at",
			"created":        "i.created_at",
			"labels":         "i.labels",
			"parent":         "parent.key",
			"resolution":     `i.fields->>'resolution'`,
			"resolutiondate": `NULLIF(i.fields->>'resolutiondate','')::timestamptz`,
			"due":            `NULLIF(i.fields->>'duedate','')::timestamptz`,
			"environment":    `i.fields->>'environment'`,
			"component":      `i.fields->>'component'`,
			"sprint":         `i.fields->>'sprint'`,
		},
		TextColumns: []string{"i.summary", "i.description::text"},
		DefaultOrder: map[string]string{
			"updated": "i.updated_at", "created": "i.created_at", "key": "i.key", "summary": "i.summary",
			"status": "st.name", "priority": "pr2.name", "assignee": "a.display_name", "issuetype": "it.name",
			"reporter": "r.display_name", "project": "pr.key", "parent": "parent.key", "resolution": `i.fields->>'resolution'`,
			"due": `NULLIF(i.fields->>'duedate','')::timestamptz`, "resolutiondate": `NULLIF(i.fields->>'resolutiondate','')::timestamptz`,
		},
	}
}

type Compiled struct {
	Where    string
	Args     []any
	OrderSQL string
	Err      error
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
	if q == nil || q.Root == nil {
		return Compiled{Err: &SyntaxError{0, "empty query"}}
	}
	c := &compiler{res: res, user: currentUserID, offset: paramOffset - 1}
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
				return Compiled{Err: &SyntaxError{0, "cannot order by " + requested.Field}}
			}
			dir := "ASC"
			if requested.Desc {
				dir = "DESC"
			}
			orderParts = append(orderParts, col+" "+dir)
		}
	}
	// A stable final key prevents duplicate or skipped rows when requested sort
	// values are equal and is required by Jira's cursor search contract.
	if !strings.Contains(strings.Join(orderParts, ","), "i.id ") {
		orderParts = append(orderParts, "i.id ASC")
	}
	return Compiled{Where: where, Args: c.args, OrderSQL: strings.Join(orderParts, ", ")}
}

type compiler struct {
	res    FieldResolver
	user   string
	args   []any
	err    error
	offset int
	now    time.Time
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
		likes := make([]string, 0, len(c.res.TextColumns))
		for _, col := range c.res.TextColumns {
			likes = append(likes, col+" ILIKE "+c.arg("%"+t.Value+"%"))
		}
		return "(" + strings.Join(likes, " OR ") + ")"
	case Clause:
		return c.clause(t)
	case HistoryClause:
		return c.historyClause(t)
	}
	c.err = &SyntaxError{0, "unknown node"}
	return ""
}

func (c *compiler) clause(cl Clause) string {
	if cl.Field == "fixversion" || cl.Field == "affectedversion" {
		return c.versionClause(cl)
	}
	if cl.Field == "labels" {
		return c.labelsClause(cl)
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
	if cl.Field == "project" && containsJQLFunction(cl.Values, "projectsLeadByUser", "spacesLeadByUser", "projectsWhereUserHasRole", "spacesWhereUserHasRole") {
		return c.projectFunctionClause(cl)
	}
	if (cl.Field == "issue" || cl.Field == "key" || cl.Field == "id") && containsJQLFunction(cl.Values,
		"linkedIssues", "linkedWorkItems", "watchedIssues", "watchedWorkItems", "votedIssues", "votedWorkItems", "updatedBy") {
		return c.issueFunctionClause(cl)
	}
	col, ok := c.res.Columns[cl.Field]
	if !ok {
		c.err = &SyntaxError{0, "field does not exist or is not searchable: " + cl.Field}
		return ""
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
		for _, v := range cl.Values {
			placeholders = append(placeholders, c.arg(c.fieldValue(cl.Field, v)))
		}
		membership := col + " IN (" + strings.Join(placeholders, ",") + ")"
		if cl.Op == "notin" {
			return "(" + col + " IS NOT NULL AND NOT (" + membership + "))"
		}
		return membership
	case ">", ">=", "<", "<=":
		return col + " " + cl.Op + " " + c.arg(c.fieldValue(cl.Field, cl.Values[0]))
	case "empty":
		return "(" + col + " IS NULL OR " + col + " = '')"
	case "notempty":
		return "(" + col + " IS NOT NULL AND " + col + " <> '')"
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op}
	return ""
}

func (c *compiler) historyClause(cl HistoryClause) string {
	keys := map[string]string{
		"status": "status", "assignee": "assignee", "reporter": "reporter",
		"priority": "priority", "parent": "parent", "labels": "labels",
		"summary": "summary", "description": "description", "security": "security",
		"fixversion": "fixVersions", "affectedversion": "versions",
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
			base = append(base, "ah.created_at"+op+c.arg(c.fieldValue("updated", predicate.Values[0])))
		case "during":
			base = append(base, "ah.created_at BETWEEN "+c.arg(c.fieldValue("updated", predicate.Values[0]))+
				" AND "+c.arg(c.fieldValue("updated", predicate.Values[1])))
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
			currentMatches = append(currentMatches, "lower(COALESCE("+current+"::text,''))=lower("+c.arg(c.fieldValue(cl.Field, value))+"::text)")
		}
		exists = "(" + exists + " OR " + strings.Join(currentMatches, " OR ") + ")"
	}
	if cl.Op == "wasnot" || cl.Op == "wasnotin" {
		return "NOT (" + exists + ")"
	}
	return exists
}

func (c *compiler) historyValueMatch(key, side string, values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		placeholder := c.arg(c.fieldValue(key, value))
		columns := []string{}
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
	if field == "updated" || field == "created" || field == "due" || field == "resolutiondate" {
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
	return value
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
