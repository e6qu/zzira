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
	// OrderBy is what the filter's ORDER BY asks for, ready to follow the
	// caller's own ORDER BY keyword, or empty when the filter has none.
	OrderBy string
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
	// ObjectID, SchemaID and DeskID are what a reference or an object type
	// hierarchy is followed with: the object row's id, its schema's id, and
	// the service desk whose inventory is being searched.
	ObjectID string
	SchemaID string
	DeskID   string
}

// DefaultColumns are the aliases the service desk's own object query uses.
func DefaultColumns() Columns {
	return Columns{Label: "o.label", Key: "o.object_key", SchemaName: "s.name", Values: "o.values",
		ObjectID: "o.id", SchemaID: "s.id"}
}

type compiler struct {
	columns Columns
	args    []any
	offset  int
	// depth counts the references walked so far, so each level's aliases are
	// its own and a path through two relationships joins two sets of rows.
	depth int
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
	return Compiled{Where: where, Args: c.args, OrderBy: q.orderBy(c)}
}

// orderBy writes the filter's ORDER BY against the same columns.
func (q *Query) orderBy(c *compiler) string {
	if q == nil || q.order == nil {
		return ""
	}
	column, _ := c.column(q.order.field)
	direction := " ASC"
	if q.order.descending {
		direction = " DESC"
	}
	return "lower(COALESCE(" + column + ",''))" + direction
}

// Order is the field a filter's ORDER BY names and whether it descends. A
// caller that merges several queries' rows sorts them itself, because each
// query only orders its own.
func (q *Query) Order() (field string, descending, ok bool) {
	if q == nil || q.order == nil {
		return "", false, false
	}
	return q.order.field, q.order.descending, true
}

// AttributeKey is the key an attribute's value is stored under, given the name
// a filter writes: lower case, with anything that is not a letter or a number
// as an underscore.
func AttributeKey(name string) string {
	return strings.Trim(quoteAttribute(name), "'")
}

// IsObjectField reports whether a name is one of the object's own fields
// rather than an attribute, and which one it is.
func IsObjectField(name string) (kind string, ok bool) {
	switch strings.ToLower(name) {
	case "objecttype", "type", "schema":
		return "schema", true
	case "name", "label":
		return "label", true
	case "key", "objectkey":
		return "key", true
	}
	return "", false
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

// sql walks one relationship by name and asks the objects at the other end.
func (n referenceNode) sql(c *compiler) string {
	return c.through(n.inner, "lower(related_link"+strconv.Itoa(c.depth+1)+".relationship)=lower("+c.arg(n.relationship)+")", false)
}

// sql asks the objects on the other side of any relationship, in the
// direction the function names.
func (n referencesNode) sql(c *compiler) string {
	return c.through(n.inner, "TRUE", n.inbound)
}

// through writes the EXISTS a reference compiles to: the relationship rows
// this object is an end of, the object at the other end, and what the nested
// condition asks of it.
func (c *compiler) through(inner node, relationship string, inbound bool) string {
	c.depth++
	level := strconv.Itoa(c.depth)
	link, object, schema := "related_link"+level, "related_object"+level, "related_schema"+level
	near, far := link+".from_object_id", link+".to_object_id"
	if inbound {
		near, far = far, near
	}
	outer := c.columns
	c.columns = Columns{
		Label: object + ".label", Key: object + ".object_key", SchemaName: schema + ".name",
		Values: object + ".values", Attributes: outer.Attributes,
		ObjectID: object + ".id", SchemaID: schema + ".id", DeskID: outer.DeskID,
	}
	condition := inner.sql(c)
	c.columns = outer
	c.depth--
	return "EXISTS (SELECT 1 FROM service_asset_relationships " + link +
		" JOIN service_asset_objects " + object + " ON " + object + ".id=" + far +
		" JOIN service_asset_schemas " + schema + " ON " + schema + ".id=" + object + ".schema_id" +
		" WHERE " + near + "=" + outer.ObjectID + " AND " + relationship + " AND " + condition + ")"
}

// sql matches an object type and every type beneath it, which is what the
// hierarchy an object type sits in is for.
func (n typeAndChildrenNode) sql(c *compiler) string {
	level := strconv.Itoa(c.depth)
	roots := make([]string, 0, len(n.names))
	for _, name := range n.names {
		named := c.arg(name)
		roots = append(roots, "lower(root"+level+".name)=lower("+named+") OR root"+level+".schema_key="+named+" OR root"+level+".id::text="+named)
	}
	desk := "TRUE"
	if c.columns.DeskID != "" {
		desk = "root" + level + ".service_desk_id=" + c.arg(c.columns.DeskID)
	}
	tree := "WITH RECURSIVE descendants" + level + " AS (" +
		"SELECT root" + level + ".id FROM service_asset_schemas root" + level + " WHERE " + desk + " AND (" + strings.Join(roots, " OR ") + ")" +
		" UNION ALL SELECT child" + level + ".id FROM service_asset_schemas child" + level +
		" JOIN descendants" + level + " parent" + level + " ON child" + level + ".parent_id=parent" + level + ".id)" +
		" SELECT id FROM descendants" + level
	match := "(" + c.columns.SchemaID + " IN (" + tree + "))"
	if n.negated {
		return "(NOT " + match + ")"
	}
	return match
}
