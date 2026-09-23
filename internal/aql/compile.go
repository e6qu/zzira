package aql

import (
	"strconv"
	"strings"
)

// Compiled is a filter as SQL: a condition over the object row and the
// arguments it takes, numbered from where the caller's own arguments end.
type Compiled struct {
	Where string
	Args  []any
}

// Columns names the SQL the condition is written against: the object row, its
// schema and the JSONB column its attribute values live in. A caller passes
// the aliases its own query uses.
type Columns struct {
	Label      string
	Key        string
	SchemaName string
	Values     string
	// Attributes maps an attribute's name, lower-cased, to the key its value
	// is stored under. A filter names an attribute as the schema names it;
	// what the value is stored under is the schema's business.
	Attributes map[string]string
}

// DefaultColumns are the aliases the service desk's own object query uses.
func DefaultColumns() Columns {
	return Columns{Label: "o.label", Key: "o.object_key", SchemaName: "s.name", Values: "o.values"}
}

type compiler struct {
	columns Columns
	args    []any
	offset  int
}

// Compile writes the filter as a SQL condition. first is the number the
// caller's next placeholder would take, so the condition's arguments follow
// the caller's own.
func (q *Query) Compile(columns Columns, first int) Compiled {
	if q.Empty() {
		return Compiled{Where: "TRUE"}
	}
	c := &compiler{columns: columns, offset: first}
	where := q.root.sql(c)
	return Compiled{Where: where, Args: c.args}
}

// arg records one value and answers the placeholder that reads it.
func (c *compiler) arg(value any) string {
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(c.offset+len(c.args)-1)
}

func (n andNode) sql(c *compiler) string { return "(" + n.left.sql(c) + " AND " + n.right.sql(c) + ")" }
func (n orNode) sql(c *compiler) string  { return "(" + n.left.sql(c) + " OR " + n.right.sql(c) + ")" }
func (n notNode) sql(c *compiler) string { return "(NOT " + n.inner.sql(c) + ")" }

// sql writes one comparison. A field is the object's own type, name or key,
// or one of its attributes by name; an attribute nothing carries is simply
// absent, which is what IS EMPTY asks about.
func (n comparison) sql(c *compiler) string {
	column, attribute := c.column(n.field)
	switch n.operator {
	case "IS EMPTY":
		return "(COALESCE(" + column + ",'')='')"
	case "IS NOT EMPTY":
		return "(COALESCE(" + column + ",'')<>'')"
	case "LIKE":
		return "(" + column + " ILIKE " + c.arg("%"+n.values[0]+"%") + ")"
	case "=", "!=":
		condition := "lower(COALESCE(" + column + ",''))=lower(" + c.arg(n.values[0]) + ")"
		if n.operator == "!=" {
			return "(NOT (" + condition + "))"
		}
		return "(" + condition + ")"
	case "IN", "NOT IN":
		parts := make([]string, 0, len(n.values))
		for _, value := range n.values {
			parts = append(parts, "lower(COALESCE("+column+",''))=lower("+c.arg(value)+")")
		}
		condition := "(" + strings.Join(parts, " OR ") + ")"
		if n.operator == "NOT IN" {
			return "(NOT " + condition + ")"
		}
		return condition
	}
	_ = attribute
	return "FALSE"
}

// column is the SQL a field reads. Jira's own object fields are named without
// quotes -- objectType, Name, Key, Label -- and anything else is an attribute
// of the object, read out of its values.
func (c *compiler) column(field string) (sql string, attribute bool) {
	switch strings.ToLower(field) {
	case "objecttype", "type", "schema":
		return c.columns.SchemaName, false
	case "name", "label":
		return c.columns.Label, false
	case "key", "objectkey":
		return c.columns.Key, false
	}
	if key, ok := c.columns.Attributes[strings.ToLower(field)]; ok {
		return c.columns.Values + "->>" + quoteLiteral(key), true
	}
	return c.columns.Values + "->>" + quoteAttribute(field), true
}

// quoteLiteral writes a value as a SQL string literal.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// quoteAttribute writes an attribute name as the key it is stored under:
// lower case, with anything that is not a letter or a number as an
// underscore, which is how a scenario and the settings page write them.
func quoteAttribute(name string) string {
	key := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '_'
	}, name)
	return quoteLiteral(key)
}
