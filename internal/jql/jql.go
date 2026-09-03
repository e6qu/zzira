// Package jql is the V1 JQL engine: a hand-rolled recursive-descent parser and
// a Postgres SQL compiler with mandatory permission predicates injected by the
// caller. Pure — shared by server and wasm targets.
//
// V1 grammar (case-insensitive keywords, unquoted/quoted values):
//
//	query   := orExpr [ ORDER BY field [ASC|DESC] ]
//	orExpr  := andExpr ( OR andExpr )*
//	andExpr := unit ( AND unit )*
//	unit    := NOT unit | '(' query ')' | field op value | text
//	op      := = | != | ~ | !~ | in '(' value, ... ')' | is empty | is not empty
package jql

import (
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// ---- AST ----

type Query struct {
	Root    Node
	OrderBy *Order
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

type Text struct{ Value string }

func (Or) isNode()     {}
func (And) isNode()    {}
func (Not) isNode()    {}
func (Clause) isNode() {}
func (Text) isNode()   {}

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
	"=": "=", "!=": "!=", "~": "~", "!~": "!~", ">": ">", ">=": ">=", "<": "<", "<=": "<=",
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
		field, err := op.expectWord("field after ORDER BY")
		if err != nil {
			return nil, err
		}
		desc := false
		if op.atWord("desc") {
			desc = true
			op.next()
		} else if op.atWord("asc") {
			op.next()
		}
		if op.peek().kind != "eof" {
			return nil, &SyntaxError{op.peek().pos, "unexpected input after ORDER BY field"}
		}
		q.OrderBy = &Order{Field: strings.ToLower(field), Desc: desc}
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
	case "order", "by", "asc", "desc", "and", "or", "not", "in", "is", "empty", "null":
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
	return strings.EqualFold(op, "in") || strings.EqualFold(op, "is")
}

func (p *parser) parseClause() (Node, error) {
	field := strings.ToLower(p.word())
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
		if p.peek().kind != "lparen" {
			return nil, &SyntaxError{p.peek().pos, "expected ( after IN"}
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
		return Clause{Field: field, Op: "in", Values: vals}, nil
	case strings.EqualFold(opTok.text, "is"):
		neg := false
		if p.atWord("not") {
			neg = true
			p.next()
		}
		w := p.word()
		if !strings.EqualFold(w, "empty") && !strings.EqualFold(w, "null") {
			return nil, &SyntaxError{p.peek().pos, "expected EMPTY after IS"}
		}
		if neg {
			return Clause{Field: field, Op: "notempty"}, nil
		}
		return Clause{Field: field, Op: "empty"}, nil
	}
	return nil, &SyntaxError{opTok.pos, "unsupported operator " + opTok.text}
}

func (p *parser) parseValue() (string, error) {
	t := p.peek()
	if t.kind == "word" || t.kind == "quoted" {
		p.next()
		// function call: word followed immediately by () (e.g. currentUser())
		if t.kind == "word" && p.peek().kind == "lparen" && p.peek().pos == t.pos+len(t.text) {
			p.next()
			if p.peek().kind == "rparen" {
				p.next()
				return t.text + "()", nil
			}
			return "", &SyntaxError{p.peek().pos, "expected ) after function call"}
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

// WithCustomFields extends a resolver with customfield_NNNNN columns.
// Values live in issues.fields JSONB; numbers compare numerically.
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
		res.TextColumns = append(res.TextColumns, `i.fields->>'`+f.ID+`'`)
	}
	return res
}

func DefaultResolver() FieldResolver {
	return FieldResolver{
		Columns: map[string]string{
			"key":       "i.key",
			"summary":   "i.summary",
			"status":    "st.name",
			"project":   "pr.key",
			"assignee":  "i.assignee_id",
			"reporter":  "i.reporter_id",
			"priority":  "pr2.name",
			"issuetype": "it.name",
			"updated":   "i.updated_at",
			"created":   "i.created_at",
		},
		TextColumns: []string{"i.summary", "i.description::text"},
		DefaultOrder: map[string]string{
			"updated": "i.updated_at", "created": "i.created_at", "key": "i.key", "summary": "i.summary",
			"status": "st.name", "priority": "pr2.name", "assignee": "a.display_name", "issuetype": "it.name",
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
	where := c.node(q.Root)
	if c.err != nil {
		return Compiled{Err: c.err}
	}
	order := "i.updated_at DESC"
	if q.OrderBy != nil {
		if col, ok := res.DefaultOrder[q.OrderBy.Field]; ok {
			dir := "ASC"
			if q.OrderBy.Desc {
				dir = "DESC"
			}
			order = col + " " + dir
		} else {
			return Compiled{Err: &SyntaxError{0, "cannot order by " + q.OrderBy.Field}}
		}
	}
	return Compiled{Where: where, Args: c.args, OrderSQL: order}
}

type compiler struct {
	res    FieldResolver
	user   string
	args   []any
	err    error
	offset int
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
	}
	c.err = &SyntaxError{0, "unknown node"}
	return ""
}

func (c *compiler) clause(cl Clause) string {
	col, ok := c.res.Columns[cl.Field]
	if !ok {
		c.err = &SyntaxError{0, "field does not exist or is not searchable: " + cl.Field}
		return ""
	}
	switch cl.Op {
	case "=":
		return col + " = " + c.arg(c.fieldValue(cl.Field, cl.Values[0]))
	case "!=":
		return "(" + col + " IS DISTINCT FROM " + c.arg(c.fieldValue(cl.Field, cl.Values[0])) + ")"
	case "~":
		return col + " ILIKE " + c.arg("%"+cl.Values[0]+"%")
	case "!~":
		return "(" + col + " NOT ILIKE " + c.arg("%"+cl.Values[0]+"%") + " OR " + col + " IS NULL)"
	case "in":
		placeholders := make([]string, 0, len(cl.Values))
		for _, v := range cl.Values {
			placeholders = append(placeholders, c.arg(c.fieldValue(cl.Field, v)))
		}
		return col + " IN (" + strings.Join(placeholders, ",") + ")"
	case "empty":
		return "(" + col + " IS NULL OR " + col + " = '')"
	case "notempty":
		return "(" + col + " IS NOT NULL AND " + col + " <> '')"
	}
	c.err = &SyntaxError{0, "unsupported operator " + cl.Op}
	return ""
}

// fieldValue resolves semantic values: status names, currentUser(), EMPTY/null.
func (c *compiler) fieldValue(field, value string) any {
	switch field {
	case "assignee", "reporter":
		if strings.EqualFold(value, "currentUser()") {
			return c.user
		}
		if strings.EqualFold(value, "empty") || strings.EqualFold(value, "null") {
			return nil
		}
		return value
	case "project":
		return strings.ToUpper(value)
	}
	return value
}
