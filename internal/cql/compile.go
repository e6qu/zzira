package cql

import (
	"strconv"
	"strings"
	"time"
)

// The compiler emits predicates against a normalised view of everything
// searchable. The caller builds that view — pages, blog posts, comments,
// attachments and spaces reduced to one set of columns, each already filtered
// by the reader's own visibility rules — so a query narrows what a reader can
// see and can never widen it.
//
// These are the columns the view provides.
const (
	ColumnEntityType   = "entity_type"
	ColumnEntityID     = "entity_id"
	ColumnSpaceKey     = "space_key"
	ColumnSpaceType    = "space_type"
	ColumnTitle        = "title"
	ColumnBody         = "body"
	ColumnStatus       = "status"
	ColumnCreator      = "creator"
	ColumnParentID     = "parent_id"
	ColumnContainerID  = "container_id"
	ColumnCreatedAt    = "created_at"
	ColumnLastModified = "last_modified"
	ColumnLabels       = "labels"
	ColumnContributors = "contributors"
	ColumnWatchers     = "watchers"
	ColumnFavouritedBy = "favourited_by"
	ColumnAncestors    = "ancestors"
	ColumnMacros       = "macros"
	ColumnMentions     = "mentions"
)

// Context is what a query needs beyond its own text: who is asking, when they
// asked, and the space the search was scoped to.
type Context struct {
	Actor        string
	Now          time.Time
	CurrentSpace string
}

type fieldKind int

const (
	kindText fieldKind = iota
	kindArray
	kindDate
	kindFullText
)

type fieldSpec struct {
	column string
	kind   fieldKind
	// user reports whether the field holds account ids, which is what lets
	// currentUser() stand in for a value.
	user bool
}

// fields is the searchable vocabulary. A name that is not here is refused
// rather than quietly ignored, because a query that silently drops a condition
// returns more than it was asked for.
var fields = map[string]fieldSpec{
	"type":         {column: ColumnEntityType},
	"id":           {column: ColumnEntityID},
	"space":        {column: ColumnSpaceKey},
	"space.type":   {column: ColumnSpaceType},
	"title":        {column: ColumnTitle},
	"status":       {column: ColumnStatus},
	"text":         {column: "", kind: kindFullText},
	"parent":       {column: ColumnParentID},
	"container":    {column: ColumnContainerID},
	"creator":      {column: ColumnCreator, user: true},
	"created":      {column: ColumnCreatedAt, kind: kindDate},
	"lastmodified": {column: ColumnLastModified, kind: kindDate},
	"label":        {column: ColumnLabels, kind: kindArray},
	"contributor":  {column: ColumnContributors, kind: kindArray, user: true},
	"watcher":      {column: ColumnWatchers, kind: kindArray, user: true},
	"favourite":    {column: ColumnFavouritedBy, kind: kindArray, user: true},
	"ancestor":     {column: ColumnAncestors, kind: kindArray},
	"macro":        {column: ColumnMacros, kind: kindArray},
	"mention":      {column: ColumnMentions, kind: kindArray, user: true},
}

// Fields lists the searchable field names, so a caller can report them.
func Fields() []string {
	out := make([]string, 0, len(fields))
	for name := range fields {
		out = append(out, name)
	}
	return sorted(out)
}

// orderColumns are the fields a result set may be ordered by.
var orderColumns = map[string]string{
	"created":      ColumnCreatedAt,
	"lastmodified": ColumnLastModified,
	"title":        ColumnTitle,
	"id":           ColumnEntityID,
	"type":         ColumnEntityType,
	"space":        ColumnSpaceKey,
}

func sorted(values []string) []string {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}

type compiler struct {
	ctx  Context
	args []any
	next int
}

// placeholder records one argument and returns the marker that stands for it.
func (c *compiler) placeholder(value any) string {
	c.args = append(c.args, value)
	marker := "$" + strconv.Itoa(c.next)
	c.next++
	return marker
}

// Compile turns a parsed query into a SQL predicate over the normalised view,
// together with the arguments it refers to. firstArg is the number of the first
// placeholder to use, so the caller's own arguments keep their positions.
func Compile(query *Query, ctx Context, firstArg int) (predicate string, args []any, order string, err error) {
	c := &compiler{ctx: ctx, next: firstArg}
	predicate, err = c.node(query.Root)
	if err != nil {
		return "", nil, "", err
	}
	order, err = compileOrder(query.Orders)
	if err != nil {
		return "", nil, "", err
	}
	return predicate, c.args, order, nil
}

// compileOrder renders the ORDER BY. Confluence orders by relevance when
// nothing is asked for; this product has no relevance score, so the default is
// the most recently changed first, which is the order a reader of a search
// result list expects when nothing distinguishes the matches.
func compileOrder(orders []Order) (string, error) {
	if len(orders) == 0 {
		return ColumnLastModified + " DESC, " + ColumnEntityType + ", " + ColumnEntityID, nil
	}
	terms := make([]string, 0, len(orders)+1)
	for _, order := range orders {
		column, ok := orderColumns[order.Field]
		if !ok {
			return "", &SemanticError{"results cannot be ordered by " + strconv.Quote(order.Field)}
		}
		direction := " ASC"
		if order.Desc {
			direction = " DESC"
		}
		terms = append(terms, column+direction)
	}
	// Two rows that tie on every named field still need a settled order, or
	// paging through them would show one twice and another not at all.
	terms = append(terms, ColumnEntityType, ColumnEntityID)
	return strings.Join(terms, ", "), nil
}

func (c *compiler) node(node Node) (string, error) {
	switch value := node.(type) {
	case Or:
		return c.join(value.Terms, " OR ", "false")
	case And:
		return c.join(value.Terms, " AND ", "true")
	case Not:
		inner, err := c.node(value.Inner)
		if err != nil {
			return "", err
		}
		return "NOT (" + inner + ")", nil
	case Clause:
		return c.clause(value)
	default:
		return "", &SemanticError{"unsupported query"}
	}
}

func (c *compiler) join(terms []Node, separator, empty string) (string, error) {
	if len(terms) == 0 {
		return empty, nil
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		part, err := c.node(term)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+part+")")
	}
	return strings.Join(parts, separator), nil
}

func (c *compiler) clause(clause Clause) (string, error) {
	spec, ok := fields[clause.Field]
	if !ok {
		return "", &SemanticError{"unknown field " + strconv.Quote(clause.Field) + "; searchable fields are " + strings.Join(Fields(), ", ")}
	}
	switch spec.kind {
	case kindFullText:
		return c.fullText(clause)
	case kindDate:
		return c.date(clause, spec)
	case kindArray:
		return c.array(clause, spec)
	default:
		return c.text(clause, spec)
	}
}

// resolve turns one written value into what it stands for. A call is answered
// here, because what currentUser() or now() means depends on who is asking and
// when — never on the text of the query.
func (c *compiler) resolve(value Value, spec fieldSpec) (string, error) {
	if value.Function == "" {
		return value.Literal, nil
	}
	switch value.Function {
	case "currentuser":
		if len(value.Arguments) != 0 {
			return "", &SemanticError{"currentUser() takes no arguments"}
		}
		if !spec.user {
			return "", &SemanticError{"currentUser() names a person, so it belongs with a field that holds one"}
		}
		return c.ctx.Actor, nil
	case "currentspace":
		if len(value.Arguments) != 0 {
			return "", &SemanticError{"currentSpace() takes no arguments"}
		}
		if c.ctx.CurrentSpace == "" {
			return "", &SemanticError{"currentSpace() needs a space, which is given in cqlcontext"}
		}
		return c.ctx.CurrentSpace, nil
	default:
		return "", &SemanticError{"unknown function " + strconv.Quote(value.Function) + "()"}
	}
}

func (c *compiler) resolveAll(values []Value, spec fieldSpec) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		resolved, err := c.resolve(value, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// subquery answers the calls that stand for a set held in the database rather
// than for one value, by compiling to the set itself.
func (c *compiler) subquery(clause Clause, spec fieldSpec) (string, bool, error) {
	if len(clause.Values) != 1 || clause.Values[0].Function == "" {
		return "", false, nil
	}
	call := clause.Values[0]
	switch call.Function {
	case "favouritespaces":
		if spec.column != ColumnSpaceKey {
			return "", false, &SemanticError{"favouriteSpaces() names spaces, so it belongs with the space field"}
		}
		marker := c.placeholder(c.ctx.Actor)
		set := ColumnSpaceKey + " IN (SELECT target_key FROM wiki_relations WHERE name='favourite' AND source_type='user' AND target_type='space' AND source_key=" + marker + ")"
		switch clause.Op {
		case "=", "in":
			return set, true, nil
		case "!=", "not in":
			return "NOT (" + set + ")", true, nil
		default:
			return "", false, &SemanticError{"favouriteSpaces() compares with =, !=, IN or NOT IN"}
		}
	default:
		return "", false, nil
	}
}

func (c *compiler) text(clause Clause, spec fieldSpec) (string, error) {
	if predicate, handled, err := c.subquery(clause, spec); err != nil || handled {
		return predicate, err
	}
	values, err := c.resolveAll(clause.Values, spec)
	if err != nil {
		return "", err
	}
	switch clause.Op {
	case "=":
		return spec.column + " = " + c.placeholder(values[0]), nil
	case "!=":
		return spec.column + " <> " + c.placeholder(values[0]), nil
	case "~":
		return spec.column + " ILIKE " + c.placeholder("%"+escapeLike(values[0])+"%"), nil
	case "!~":
		return spec.column + " NOT ILIKE " + c.placeholder("%"+escapeLike(values[0])+"%"), nil
	case "in":
		return spec.column + " = ANY(" + c.placeholder(values) + ")", nil
	case "not in":
		return "NOT (" + spec.column + " = ANY(" + c.placeholder(values) + "))", nil
	default:
		return "", &SemanticError{strconv.Quote(clause.Field) + " compares with =, !=, ~, !~, IN or NOT IN"}
	}
}

// fullText searches the title and the body together, which is what a reader
// typing words into a search box means.
func (c *compiler) fullText(clause Clause) (string, error) {
	values, err := c.resolveAll(clause.Values, fieldSpec{})
	if err != nil {
		return "", err
	}
	switch clause.Op {
	case "~":
		marker := c.placeholder("%" + escapeLike(values[0]) + "%")
		return "(" + ColumnTitle + " ILIKE " + marker + " OR " + ColumnBody + " ILIKE " + marker + ")", nil
	case "!~":
		marker := c.placeholder("%" + escapeLike(values[0]) + "%")
		return "(" + ColumnTitle + " NOT ILIKE " + marker + " AND " + ColumnBody + " NOT ILIKE " + marker + ")", nil
	default:
		return "", &SemanticError{"text compares with ~ or !~"}
	}
}

func (c *compiler) array(clause Clause, spec fieldSpec) (string, error) {
	values, err := c.resolveAll(clause.Values, spec)
	if err != nil {
		return "", err
	}
	switch clause.Op {
	case "=":
		return c.placeholder(values[0]) + " = ANY(" + spec.column + ")", nil
	case "!=":
		return "NOT (" + c.placeholder(values[0]) + " = ANY(" + spec.column + "))", nil
	case "~":
		return "EXISTS (SELECT 1 FROM unnest(" + spec.column + ") AS element WHERE element ILIKE " + c.placeholder("%"+escapeLike(values[0])+"%") + ")", nil
	case "!~":
		return "NOT EXISTS (SELECT 1 FROM unnest(" + spec.column + ") AS element WHERE element ILIKE " + c.placeholder("%"+escapeLike(values[0])+"%") + ")", nil
	case "in":
		return spec.column + " && " + c.placeholder(values), nil
	case "not in":
		return "NOT (" + spec.column + " && " + c.placeholder(values) + ")", nil
	default:
		return "", &SemanticError{strconv.Quote(clause.Field) + " compares with =, !=, ~, !~, IN or NOT IN"}
	}
}

func (c *compiler) date(clause Clause, spec fieldSpec) (string, error) {
	switch clause.Op {
	case "=", "!=", "<", "<=", ">", ">=":
	default:
		return "", &SemanticError{strconv.Quote(clause.Field) + " compares with =, !=, <, <=, > or >="}
	}
	if len(clause.Values) != 1 {
		return "", &SemanticError{strconv.Quote(clause.Field) + " compares against one date"}
	}
	moment, err := c.instant(clause.Values[0])
	if err != nil {
		return "", err
	}
	// A bare date names a whole day, so equality is the day rather than the
	// midnight that starts it; anything else would match nothing.
	if clause.Op == "=" || clause.Op == "!=" {
		if isWholeDay(clause.Values[0]) {
			from, to := c.placeholder(moment), c.placeholder(moment.AddDate(0, 0, 1))
			within := "(" + spec.column + " >= " + from + " AND " + spec.column + " < " + to + ")"
			if clause.Op == "!=" {
				return "NOT " + within, nil
			}
			return within, nil
		}
	}
	return spec.column + " " + clause.Op + " " + c.placeholder(moment), nil
}

// isWholeDay reports whether the value names a day rather than a moment.
func isWholeDay(value Value) bool {
	if value.Function != "" {
		return false
	}
	return len(value.Literal) == len("2006-01-02")
}

// instant resolves a date. Both written dates and the calls Confluence provides
// for relative ones are answered here, against the moment the search was made.
func (c *compiler) instant(value Value) (time.Time, error) {
	now := c.ctx.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if value.Function == "" {
		return parseDate(value.Literal)
	}
	offset := ""
	if len(value.Arguments) == 1 {
		offset = value.Arguments[0]
	} else if len(value.Arguments) > 1 {
		return time.Time{}, &SemanticError{value.Function + "() takes at most one offset"}
	}
	var base time.Time
	switch value.Function {
	case "now":
		base = now
	case "startofday":
		base = truncateDay(now)
	case "endofday":
		base = truncateDay(now).AddDate(0, 0, 1).Add(-time.Second)
	case "startofweek":
		base = truncateDay(now).AddDate(0, 0, -weekdayOffset(now))
	case "endofweek":
		base = truncateDay(now).AddDate(0, 0, 7-weekdayOffset(now)).Add(-time.Second)
	case "startofmonth":
		base = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "endofmonth":
		base = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0).Add(-time.Second)
	case "startofyear":
		base = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	case "endofyear":
		base = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC).AddDate(1, 0, 0).Add(-time.Second)
	default:
		return time.Time{}, &SemanticError{"unknown function " + strconv.Quote(value.Function) + "() in a date"}
	}
	if offset == "" {
		return base, nil
	}
	return applyOffset(base, offset)
}

// weekdayOffset counts back to Monday, which is the day a week starts on here.
func weekdayOffset(moment time.Time) int {
	day := int(moment.Weekday())
	if day == 0 {
		return 6
	}
	return day - 1
}

func truncateDay(moment time.Time) time.Time {
	return time.Date(moment.Year(), moment.Month(), moment.Day(), 0, 0, 0, 0, time.UTC)
}

// applyOffset reads the increments Confluence writes: a signed number and a
// unit, as in "-7d" or "2w".
func applyOffset(base time.Time, offset string) (time.Time, error) {
	offset = strings.TrimSpace(offset)
	if offset == "" {
		return base, nil
	}
	sign := 1
	switch offset[0] {
	case '-':
		sign, offset = -1, offset[1:]
	case '+':
		offset = offset[1:]
	}
	if offset == "" {
		return time.Time{}, &SemanticError{"an offset is a number and a unit, as in \"-7d\""}
	}
	unit := offset[len(offset)-1]
	number, err := strconv.Atoi(offset[:len(offset)-1])
	if err != nil || number < 0 {
		return time.Time{}, &SemanticError{"an offset is a number and a unit, as in \"-7d\""}
	}
	number *= sign
	switch unit {
	case 'm':
		return base.Add(time.Duration(number) * time.Minute), nil
	case 'h':
		return base.Add(time.Duration(number) * time.Hour), nil
	case 'd':
		return base.AddDate(0, 0, number), nil
	case 'w':
		return base.AddDate(0, 0, 7*number), nil
	case 'M':
		return base.AddDate(0, number, 0), nil
	case 'y':
		return base.AddDate(number, 0, 0), nil
	default:
		return time.Time{}, &SemanticError{"an offset unit is m, h, d, w, M or y"}
	}
}

var dateLayouts = []string{
	"2006-01-02 15:04",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"2006-01",
	"2006",
}

func parseDate(literal string) (time.Time, error) {
	literal = strings.TrimSpace(literal)
	for _, layout := range dateLayouts {
		if moment, err := time.Parse(layout, literal); err == nil {
			return moment.UTC(), nil
		}
	}
	return time.Time{}, &SemanticError{"a date is written as yyyy-mm-dd or yyyy-mm-dd hh:mm, not " + strconv.Quote(literal)}
}

// escapeLike keeps a wildcard typed by a reader from acting as one. A search
// for "100%" means those four characters.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
