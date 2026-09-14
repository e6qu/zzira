package jexpr

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Value is a Jira expression value: nil, bool, float64, string, *List, *Map,
// Date, *Lambda, *Method or an Entity.
type Value = any

// List is Jira's List.
type List struct{ Items []Value }

// Map is Jira's Map; object literals create maps.
type Map struct {
	keys   []string
	values map[string]Value
}

func NewMap() *Map { return &Map{values: map[string]Value{}} }

// Set stores a key, keeping insertion order.
func (m *Map) Set(key string, value Value) {
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *Map) Get(key string) (Value, bool) {
	value, ok := m.values[key]
	return value, ok
}

func (m *Map) Keys() []string { return append([]string(nil), m.keys...) }

func (m *Map) clone() *Map {
	copied := NewMap()
	for _, key := range m.keys {
		copied.Set(key, m.values[key])
	}
	return copied
}

// Date is Jira's Date, or its CalendarDate when Calendar is set.
type Date struct {
	Time     time.Time
	Calendar bool
}

// Lambda is an arrow function.
type Lambda struct {
	Params []string
	Body   Node
	scope  *scope
	source string
}

// Method is a built-in function or a method bound to its receiver.
type Method struct {
	Name string
	Fn   func(c *Context, args []Value) (Value, error)
}

// Entity is a Jira object, such as an issue or a user.
type Entity interface {
	TypeName() string
	Member(c *Context, name string) (Value, bool, error)
	JSON(c *Context) (any, error)
}

// Record is an Entity with eager fields, lazily loaded fields that count as
// expensive operations, methods and its Jira REST representation.
type Record struct {
	Type   string
	Fields map[string]Value
	Lazy   map[string]func(c *Context) (Value, error)
	// Deferred fields load on first use without counting as expensive.
	Deferred map[string]func(c *Context) (Value, error)
	Methods  map[string]func(c *Context, args []Value) (Value, error)
	Bean     func(c *Context) (any, error)
	cache    map[string]Value
}

func (r *Record) TypeName() string { return r.Type }

func (r *Record) Member(c *Context, name string) (Value, bool, error) {
	if value, ok := r.Fields[name]; ok {
		return value, true, nil
	}
	if value, ok := r.cache[name]; ok {
		return value, true, nil
	}
	load, expensive := r.Lazy[name]
	if !expensive {
		load = r.Deferred[name]
	}
	if load != nil {
		if expensive {
			if err := c.expensive(); err != nil {
				return nil, true, err
			}
		}
		value, err := load(c)
		if err != nil {
			return nil, true, err
		}
		if r.cache == nil {
			r.cache = map[string]Value{}
		}
		r.cache[name] = value
		return value, true, nil
	}
	if fn, ok := r.Methods[name]; ok {
		return &Method{Name: name, Fn: fn}, true, nil
	}
	return nil, false, nil
}

func (r *Record) JSON(c *Context) (any, error) {
	if r.Bean != nil {
		return r.Bean(c)
	}
	out := map[string]any{}
	for key, value := range r.Fields {
		encoded, err := c.toJSON(value)
		if err != nil {
			return nil, err
		}
		out[key] = encoded
	}
	return out, nil
}

func (r *Record) memberNames() []string {
	names := []string{}
	for name := range r.Fields {
		names = append(names, name)
	}
	for name := range r.Lazy {
		names = append(names, name)
	}
	for name := range r.Deferred {
		names = append(names, name)
	}
	for name := range r.Methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Limits are Jira's expression evaluation limits.
type Limits struct {
	Steps               int
	ExpensiveOperations int
	Beans               int
	PrimitiveValues     int
}

var DefaultLimits = Limits{Steps: 10000, ExpensiveOperations: 10, Beans: 1000, PrimitiveValues: 10000}

// Complexity is what an evaluation used.
type Complexity struct {
	Steps               int
	ExpensiveOperations int
	Beans               int
	PrimitiveValues     int
}

// Loader resolves the entities expressions construct with new.
type Loader interface {
	Issue(ctx context.Context, idOrKey string) (Value, error)
	User(ctx context.Context, accountID string) (Value, error)
	Project(ctx context.Context, idOrKey string) (Value, error)
}

// Context holds an evaluation's variables, loader, limits and usage.
type Context struct {
	Ctx       context.Context
	Variables map[string]Value
	Loader    Loader
	Limits    Limits
	Now       func() time.Time
	source    string
	used      Complexity
}

// Used reports the complexity used so far.
func (c *Context) Used() Complexity { return c.used }

// EvalError is a failed evaluation.
type EvalError struct {
	Message string
	Snippet string
}

func (e *EvalError) Error() string {
	if e.Snippet == "" {
		return "Evaluation failed: " + e.Message
	}
	return "Evaluation failed: \"" + e.Snippet + "\" - " + e.Message
}

// Errorf reports a runtime failure from an entity or loader.
func Errorf(format string, args ...any) error {
	return &EvalError{Message: fmt.Sprintf(format, args...)}
}

func (c *Context) fail(node Node, message string) error {
	return &EvalError{Message: message, Snippet: c.snippet(node)}
}

func (c *Context) snippet(node Node) string {
	if node == nil {
		return ""
	}
	s := node.Span()
	if s.start.Offset < 0 || s.end > len(c.source) || s.start.Offset > s.end {
		return ""
	}
	return c.source[s.start.Offset:s.end]
}

func (c *Context) step() error {
	c.used.Steps++
	if c.Limits.Steps > 0 && c.used.Steps > c.Limits.Steps {
		return &EvalError{Message: fmt.Sprintf("The expression exceeded the limit of %d steps.", c.Limits.Steps)}
	}
	return nil
}

func (c *Context) expensive() error {
	c.used.ExpensiveOperations++
	if c.Limits.ExpensiveOperations > 0 && c.used.ExpensiveOperations > c.Limits.ExpensiveOperations {
		return &EvalError{Message: fmt.Sprintf("The expression exceeded the limit of %d expensive operations.", c.Limits.ExpensiveOperations)}
	}
	return nil
}

func (c *Context) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Evaluate parses and evaluates an expression.
func (c *Context) Evaluate(expression string) (Value, error) {
	node, err := Parse(expression)
	if err != nil {
		return nil, err
	}
	c.source = expression
	if c.Ctx == nil {
		c.Ctx = context.Background()
	}
	if c.Limits == (Limits{}) {
		c.Limits = DefaultLimits
	}
	return c.eval(node, nil)
}

type scope struct {
	vars   map[string]Value
	parent *scope
}

func (s *scope) lookup(name string) (Value, bool) {
	for current := s; current != nil; current = current.parent {
		if value, ok := current.vars[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func (c *Context) eval(node Node, env *scope) (Value, error) {
	if err := c.step(); err != nil {
		return nil, err
	}
	switch n := node.(type) {
	case *Literal:
		return n.Value, nil
	case *Ident:
		if value, ok := env.lookup(n.Name); ok {
			return value, nil
		}
		if value, ok := c.Variables[n.Name]; ok {
			return value, nil
		}
		if value, ok := globals[n.Name]; ok {
			return value, nil
		}
		return nil, c.fail(n, "Unrecognized identifier: \""+n.Name+"\".")
	case *Member:
		object, err := c.eval(n.Object, env)
		if err != nil {
			return nil, err
		}
		if object == nil {
			if n.Optional {
				return nil, nil
			}
			return nil, c.fail(n, "Cannot read property \""+n.Name+"\" of null.")
		}
		return c.member(n, object, n.Name)
	case *Index:
		object, err := c.eval(n.Object, env)
		if err != nil {
			return nil, err
		}
		if object == nil {
			if n.Optional {
				return nil, nil
			}
			return nil, c.fail(n, "Cannot read an index of null.")
		}
		key, err := c.eval(n.Key, env)
		if err != nil {
			return nil, err
		}
		return c.index(n, object, key)
	case *Call:
		callee, err := c.eval(n.Callee, env)
		if err != nil {
			return nil, err
		}
		if callee == nil && n.Optional {
			return nil, nil
		}
		args, err := c.evalElements(n.Args, env)
		if err != nil {
			return nil, err
		}
		return c.call(n, callee, args)
	case *Unary:
		operand, err := c.eval(n.X, env)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case "!":
			return !truthy(operand), nil
		case "typeof":
			return typeOf(operand), nil
		default:
			number, ok := operand.(float64)
			if !ok {
				return nil, c.fail(n, "The operator "+n.Op+" needs a number, not "+typeName(operand)+".")
			}
			if n.Op == "-" {
				return -number, nil
			}
			return number, nil
		}
	case *Binary:
		return c.binary(n, env)
	case *Conditional:
		test, err := c.eval(n.Test, env)
		if err != nil {
			return nil, err
		}
		if truthy(test) {
			return c.eval(n.Then, env)
		}
		return c.eval(n.Else, env)
	case *Arrow:
		return &Lambda{Params: n.Params, Body: n.Body, scope: env, source: c.snippet(n)}, nil
	case *ArrayLit:
		items, err := c.evalElements(n.Elements, env)
		if err != nil {
			return nil, err
		}
		return &List{Items: items}, nil
	case *ObjectLit:
		object := NewMap()
		for _, property := range n.Properties {
			value, err := c.eval(property.Value, env)
			if err != nil {
				return nil, err
			}
			if property.Spread {
				spread, ok := value.(*Map)
				if !ok {
					return nil, c.fail(property.Value, "Only a Map can be spread into an object.")
				}
				for _, key := range spread.keys {
					object.Set(key, spread.values[key])
				}
				continue
			}
			object.Set(property.Key, value)
		}
		return object, nil
	case *Template:
		var b strings.Builder
		for index, literal := range n.Strings {
			b.WriteString(literal)
			if index < len(n.Exprs) {
				value, err := c.eval(n.Exprs[index], env)
				if err != nil {
					return nil, err
				}
				b.WriteString(toString(value))
			}
		}
		return b.String(), nil
	case *New:
		args, err := c.evalElements(n.Args, env)
		if err != nil {
			return nil, err
		}
		return c.construct(n, args)
	case *Spread:
		return nil, c.fail(n, "Spread is only allowed in lists, objects and arguments.")
	}
	return nil, &EvalError{Message: "Unsupported expression."}
}

func (c *Context) evalElements(nodes []Node, env *scope) ([]Value, error) {
	values := make([]Value, 0, len(nodes))
	for _, node := range nodes {
		if spread, ok := node.(*Spread); ok {
			value, err := c.eval(spread.X, env)
			if err != nil {
				return nil, err
			}
			list, ok := value.(*List)
			if !ok {
				return nil, c.fail(spread, "Only a List can be spread.")
			}
			values = append(values, list.Items...)
			continue
		}
		value, err := c.eval(node, env)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (c *Context) binary(n *Binary, env *scope) (Value, error) {
	left, err := c.eval(n.L, env)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case "&&":
		if !truthy(left) {
			return left, nil
		}
		return c.eval(n.R, env)
	case "||":
		if truthy(left) {
			return left, nil
		}
		return c.eval(n.R, env)
	case "??":
		if left != nil {
			return left, nil
		}
		return c.eval(n.R, env)
	}
	right, err := c.eval(n.R, env)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case "==", "===":
		return equal(left, right), nil
	case "!=", "!==":
		return !equal(left, right), nil
	case "+":
		_, leftString := left.(string)
		_, rightString := right.(string)
		if leftString || rightString {
			return toString(left) + toString(right), nil
		}
		if l, ok := left.(float64); ok {
			if r, ok := right.(float64); ok {
				return l + r, nil
			}
		}
		return nil, c.fail(n, "Cannot add "+typeName(left)+" and "+typeName(right)+".")
	case "-", "*", "/", "%":
		l, lok := left.(float64)
		r, rok := right.(float64)
		if !lok || !rok {
			return nil, c.fail(n, "The operator "+n.Op+" needs numbers, not "+typeName(left)+" and "+typeName(right)+".")
		}
		switch n.Op {
		case "-":
			return l - r, nil
		case "*":
			return l * r, nil
		case "/":
			return l / r, nil
		default:
			return math.Mod(l, r), nil
		}
	case "<", "<=", ">", ">=":
		order, ok := compare(left, right)
		if !ok {
			return nil, c.fail(n, "Cannot compare "+typeName(left)+" and "+typeName(right)+".")
		}
		switch n.Op {
		case "<":
			return order < 0, nil
		case "<=":
			return order <= 0, nil
		case ">":
			return order > 0, nil
		default:
			return order >= 0, nil
		}
	}
	return nil, c.fail(n, "Unsupported operator "+n.Op+".")
}

func (c *Context) call(node Node, callee Value, args []Value) (Value, error) {
	switch fn := callee.(type) {
	case *Method:
		value, err := fn.Fn(c, args)
		if err != nil {
			if _, ok := err.(*EvalError); ok {
				return nil, err
			}
			return nil, c.fail(node, err.Error())
		}
		return value, nil
	case *Lambda:
		return c.invoke(fn, args)
	}
	return nil, c.fail(node, typeName(callee)+" is not a function.")
}

func (c *Context) invoke(fn *Lambda, args []Value) (Value, error) {
	vars := make(map[string]Value, len(fn.Params))
	for index, name := range fn.Params {
		if index < len(args) {
			vars[name] = args[index]
		} else {
			vars[name] = nil
		}
	}
	return c.eval(fn.Body, &scope{vars: vars, parent: fn.scope})
}

func (c *Context) index(node Node, object, key Value) (Value, error) {
	switch o := object.(type) {
	case *List:
		index, ok := key.(float64)
		if !ok {
			return nil, c.fail(node, "A List is indexed by a number.")
		}
		if index < 0 || int(index) >= len(o.Items) || index != math.Trunc(index) {
			return nil, nil
		}
		return o.Items[int(index)], nil
	case *Map:
		value, _ := o.Get(toString(key))
		return value, nil
	case string:
		index, ok := key.(float64)
		runes := []rune(o)
		if !ok || index < 0 || int(index) >= len(runes) {
			return nil, nil
		}
		return string(runes[int(index)]), nil
	case Entity:
		name, ok := key.(string)
		if !ok {
			return nil, c.fail(node, "Properties are accessed by name.")
		}
		return c.member(node, object, name)
	}
	return nil, c.fail(node, "Cannot index "+typeName(object)+".")
}

func (c *Context) member(node Node, object Value, name string) (Value, error) {
	switch o := object.(type) {
	case Entity:
		value, found, err := o.Member(c, name)
		if err != nil {
			if evalErr, ok := err.(*EvalError); ok {
				if evalErr.Snippet == "" {
					evalErr.Snippet = c.snippet(node)
				}
				return nil, evalErr
			}
			return nil, c.fail(node, err.Error())
		}
		if !found {
			message := "Unrecognized property of `" + c.objectSnippet(node) + "`: \"" + name + "\" ('" + name + "')."
			if record, ok := o.(*Record); ok {
				message += " Available properties of type '" + record.Type + "' are: '" + strings.Join(record.memberNames(), "', '") + "'."
			}
			return nil, c.fail(node, message)
		}
		return value, nil
	case *Map:
		if value, ok := o.Get(name); ok {
			return value, nil
		}
		if method := mapMethod(o, name); method != nil {
			return method, nil
		}
		return nil, nil
	case *List:
		if name == "length" {
			return float64(len(o.Items)), nil
		}
		if method := listMethod(o, name); method != nil {
			return method, nil
		}
	case string:
		if name == "length" {
			return float64(len([]rune(o))), nil
		}
		if method := stringMethod(o, name); method != nil {
			return method, nil
		}
	case Date:
		if method := dateMethod(o, name); method != nil {
			return method, nil
		}
	case float64:
		if method := numberMethod(o, name); method != nil {
			return method, nil
		}
	}
	return nil, c.fail(node, "Unrecognized property of `"+c.objectSnippet(node)+"`: \""+name+"\" ('"+name+"').")
}

func (c *Context) objectSnippet(node Node) string {
	switch n := node.(type) {
	case *Member:
		return c.snippet(n.Object)
	case *Index:
		return c.snippet(n.Object)
	}
	return c.snippet(node)
}

func (c *Context) construct(node *New, args []Value) (Value, error) {
	switch node.Name {
	case "Date":
		switch len(args) {
		case 0:
			return Date{Time: c.now()}, nil
		case 1:
			switch arg := args[0].(type) {
			case float64:
				return Date{Time: time.UnixMilli(int64(arg)).UTC()}, nil
			case string:
				parsed, calendar, err := parseDate(arg)
				if err != nil {
					return nil, c.fail(node, "Invalid date "+strconv.Quote(arg)+".")
				}
				return Date{Time: parsed, Calendar: calendar}, nil
			case Date:
				return arg, nil
			}
			return nil, c.fail(node, "A Date is created from a number or a string.")
		default:
			parts := []int{0, 0, 1, 0, 0, 0, 0}
			for index, arg := range args {
				number, ok := arg.(float64)
				if !ok || index >= len(parts) {
					return nil, c.fail(node, "A Date is created from numeric year, month, day, hours, minutes and seconds.")
				}
				parts[index] = int(number)
			}
			return Date{Time: time.Date(parts[0], time.Month(parts[1]+1), parts[2], parts[3], parts[4], parts[5], parts[6]*int(time.Millisecond), time.UTC)}, nil
		}
	case "Map":
		return NewMap(), nil
	case "Issue", "User", "Project":
		if len(args) != 1 {
			return nil, c.fail(node, "new "+node.Name+" takes one argument.")
		}
		if c.Loader == nil {
			return nil, c.fail(node, node.Name+" objects cannot be created here.")
		}
		if err := c.expensive(); err != nil {
			return nil, err
		}
		ref := toString(args[0])
		var value Value
		var err error
		switch node.Name {
		case "Issue":
			value, err = c.Loader.Issue(c.Ctx, ref)
		case "User":
			value, err = c.Loader.User(c.Ctx, ref)
		default:
			value, err = c.Loader.Project(c.Ctx, ref)
		}
		if err != nil {
			return nil, c.fail(node, err.Error())
		}
		return value, nil
	}
	return nil, c.fail(node, "Unrecognized type: \""+node.Name+"\".")
}

// ---- conversions ----

// ToJSON converts a result into its JSON value, counting beans and primitive
// values against the limits.
func (c *Context) ToJSON(value Value) (any, error) {
	if c.Limits == (Limits{}) {
		c.Limits = DefaultLimits
	}
	return c.toJSON(value)
}

func (c *Context) primitive() error {
	c.used.PrimitiveValues++
	if c.Limits.PrimitiveValues > 0 && c.used.PrimitiveValues > c.Limits.PrimitiveValues {
		return &EvalError{Message: fmt.Sprintf("The expression exceeded the limit of %d primitive values.", c.Limits.PrimitiveValues)}
	}
	return nil
}

func (c *Context) toJSON(value Value) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, c.primitive()
	case bool, string:
		return v, c.primitive()
	case float64:
		if err := c.primitive(); err != nil {
			return nil, err
		}
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v), nil
		}
		return v, nil
	case *List:
		out := make([]any, 0, len(v.Items))
		for _, item := range v.Items {
			encoded, err := c.toJSON(item)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded)
		}
		return out, nil
	case *Map:
		out := make(map[string]any, len(v.keys))
		for _, key := range v.keys {
			encoded, err := c.toJSON(v.values[key])
			if err != nil {
				return nil, err
			}
			out[key] = encoded
		}
		return out, nil
	case Date:
		return v.String(), c.primitive()
	case *Lambda:
		return v.source, c.primitive()
	case *Method:
		return v.Name, c.primitive()
	case Entity:
		c.used.Beans++
		if c.Limits.Beans > 0 && c.used.Beans > c.Limits.Beans {
			return nil, &EvalError{Message: fmt.Sprintf("The expression exceeded the limit of %d beans.", c.Limits.Beans)}
		}
		return v.JSON(c)
	}
	return nil, &EvalError{Message: "The value cannot be returned."}
}

// FromJSON converts decoded JSON into an expression value.
func FromJSON(value any) Value {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := NewMap()
		for _, key := range keys {
			out.Set(key, FromJSON(v[key]))
		}
		return out
	case []any:
		items := make([]Value, 0, len(v))
		for _, item := range v {
			items = append(items, FromJSON(item))
		}
		return &List{Items: items}
	case json.Number:
		number, _ := v.Float64()
		return number
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return value
}

func (d Date) String() string {
	if d.Calendar {
		return d.Time.Format("2006-01-02")
	}
	return d.Time.UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseDate(text string) (time.Time, bool, error) {
	if parsed, err := time.Parse("2006-01-02", text); err == nil {
		return parsed, true, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid date")
}

// ParseDate converts a stored timestamp into a Date, or nil when it is empty.
func ParseDate(text string) Value {
	if text == "" {
		return nil
	}
	parsed, calendar, err := parseDate(text)
	if err != nil {
		return nil
	}
	return Date{Time: parsed, Calendar: calendar}
}

func formatNumber(number float64) string {
	if number == math.Trunc(number) && math.Abs(number) < 1e21 {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return strconv.FormatFloat(number, 'g', -1, 64)
}

func toString(value Value) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return formatNumber(v)
	case string:
		return v
	case *List:
		parts := make([]string, 0, len(v.Items))
		for _, item := range v.Items {
			parts = append(parts, toString(item))
		}
		return strings.Join(parts, ",")
	case *Map:
		return "[object Map]"
	case Date:
		return v.String()
	case *Lambda:
		return v.source
	case *Method:
		return v.Name
	case Entity:
		return "[object " + v.TypeName() + "]"
	}
	return fmt.Sprint(value)
}

func truthy(value Value) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}

func typeOf(value Value) string {
	switch value.(type) {
	case nil:
		return "object"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case *Lambda, *Method:
		return "function"
	}
	return "object"
}

func typeName(value Value) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "Boolean"
	case float64:
		return "Number"
	case string:
		return "String"
	case *List:
		return "List"
	case *Map:
		return "Map"
	case Date:
		if v.Calendar {
			return "CalendarDate"
		}
		return "Date"
	case *Lambda, *Method:
		return "function"
	case Entity:
		return v.TypeName()
	}
	return "value"
}

func equal(left, right Value) bool {
	switch l := left.(type) {
	case nil:
		return right == nil
	case bool, float64, string:
		return left == right
	case Date:
		r, ok := right.(Date)
		return ok && l.Time.Equal(r.Time)
	case *List:
		r, ok := right.(*List)
		if !ok || len(l.Items) != len(r.Items) {
			return false
		}
		for index := range l.Items {
			if !equal(l.Items[index], r.Items[index]) {
				return false
			}
		}
		return true
	case *Map:
		r, ok := right.(*Map)
		if !ok || len(l.keys) != len(r.keys) {
			return false
		}
		for _, key := range l.keys {
			value, exists := r.values[key]
			if !exists || !equal(l.values[key], value) {
				return false
			}
		}
		return true
	case *Record:
		r, ok := right.(*Record)
		if !ok {
			return false
		}
		if l == r {
			return true
		}
		leftID, lok := l.Fields["id"]
		rightID, rok := r.Fields["id"]
		return l.Type == r.Type && lok && rok && equal(leftID, rightID)
	}
	return left == right
}

func compare(left, right Value) (int, bool) {
	switch l := left.(type) {
	case float64:
		if r, ok := right.(float64); ok {
			switch {
			case l < r:
				return -1, true
			case l > r:
				return 1, true
			}
			return 0, true
		}
	case string:
		if r, ok := right.(string); ok {
			return strings.Compare(l, r), true
		}
	case Date:
		if r, ok := right.(Date); ok {
			return l.Time.Compare(r.Time), true
		}
	}
	return 0, false
}

// ---- built-ins ----

func method(name string, fn func(c *Context, args []Value) (Value, error)) *Method {
	return &Method{Name: name, Fn: fn}
}

func numberArg(args []Value, index int, name string) (float64, error) {
	if index >= len(args) {
		return 0, fmt.Errorf("%s needs %d arguments", name, index+1)
	}
	number, ok := args[index].(float64)
	if !ok {
		return 0, fmt.Errorf("argument %d of %s must be a number, not %s", index+1, name, typeName(args[index]))
	}
	return number, nil
}

func stringArg(args []Value, index int, name string) (string, error) {
	if index >= len(args) {
		return "", fmt.Errorf("%s needs %d arguments", name, index+1)
	}
	text, ok := args[index].(string)
	if !ok {
		return "", fmt.Errorf("argument %d of %s must be a String, not %s", index+1, name, typeName(args[index]))
	}
	return text, nil
}

func lambdaArg(args []Value, index int, name string) (Value, error) {
	if index >= len(args) {
		return nil, fmt.Errorf("%s needs a function", name)
	}
	switch args[index].(type) {
	case *Lambda, *Method:
		return args[index], nil
	}
	return nil, fmt.Errorf("argument %d of %s must be a function, not %s", index+1, name, typeName(args[index]))
}

func (c *Context) apply(fn Value, args ...Value) (Value, error) {
	return c.call(nil, fn, args)
}

var globals = map[string]Value{
	"Math":    mathObject(),
	"JSON":    jsonObject(),
	"Number":  method("Number", func(c *Context, args []Value) (Value, error) { return toNumber(firstArg(args)), nil }),
	"String":  method("String", func(c *Context, args []Value) (Value, error) { return toString(firstArg(args)), nil }),
	"Boolean": method("Boolean", func(c *Context, args []Value) (Value, error) { return truthy(firstArg(args)), nil }),
}

func firstArg(args []Value) Value {
	if len(args) == 0 {
		return nil
	}
	return args[0]
}

func toNumber(value Value) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return math.NaN()
		}
		return number
	case nil:
		return 0
	case Date:
		return float64(v.Time.UnixMilli())
	}
	return math.NaN()
}

func mathObject() *Map {
	object := NewMap()
	unary := func(name string, fn func(float64) float64) {
		object.Set(name, method(name, func(c *Context, args []Value) (Value, error) {
			number, err := numberArg(args, 0, "Math."+name)
			if err != nil {
				return nil, err
			}
			return fn(number), nil
		}))
	}
	unary("abs", math.Abs)
	unary("ceil", math.Ceil)
	unary("floor", math.Floor)
	unary("round", func(x float64) float64 { return math.Floor(x + 0.5) })
	unary("sqrt", math.Sqrt)
	unary("trunc", math.Trunc)
	unary("sign", func(x float64) float64 {
		switch {
		case x > 0:
			return 1
		case x < 0:
			return -1
		}
		return 0
	})
	object.Set("pow", method("pow", func(c *Context, args []Value) (Value, error) {
		base, err := numberArg(args, 0, "Math.pow")
		if err != nil {
			return nil, err
		}
		exponent, err := numberArg(args, 1, "Math.pow")
		if err != nil {
			return nil, err
		}
		return math.Pow(base, exponent), nil
	}))
	extreme := func(name string, pick func(a, b float64) float64, start float64) {
		object.Set(name, method(name, func(c *Context, args []Value) (Value, error) {
			result := start
			for index := range args {
				number, err := numberArg(args, index, "Math."+name)
				if err != nil {
					return nil, err
				}
				result = pick(result, number)
			}
			return result, nil
		}))
	}
	extreme("max", math.Max, math.Inf(-1))
	extreme("min", math.Min, math.Inf(1))
	return object
}

func jsonObject() *Map {
	object := NewMap()
	object.Set("stringify", method("stringify", func(c *Context, args []Value) (Value, error) {
		encoded, err := c.toJSON(firstArg(args))
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(encoded)
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	}))
	object.Set("parse", method("parse", func(c *Context, args []Value) (Value, error) {
		text, err := stringArg(args, 0, "JSON.parse")
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		return FromJSON(decoded), nil
	}))
	return object
}

func stringMethod(s string, name string) *Method {
	runes := []rune(s)
	clampIndex := func(value float64) int {
		index := int(value)
		if index < 0 {
			index += len(runes)
		}
		return max(0, min(index, len(runes)))
	}
	switch name {
	case "includes", "startsWith", "endsWith", "indexOf", "lastIndexOf":
		return method(name, func(c *Context, args []Value) (Value, error) {
			needle, err := stringArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			switch name {
			case "includes":
				return strings.Contains(s, needle), nil
			case "startsWith":
				return strings.HasPrefix(s, needle), nil
			case "endsWith":
				return strings.HasSuffix(s, needle), nil
			case "indexOf":
				index := strings.Index(s, needle)
				if index < 0 {
					return float64(-1), nil
				}
				return float64(len([]rune(s[:index]))), nil
			default:
				index := strings.LastIndex(s, needle)
				if index < 0 {
					return float64(-1), nil
				}
				return float64(len([]rune(s[:index]))), nil
			}
		})
	case "toLowerCase", "toUpperCase", "trim", "trimStart", "trimEnd", "toString":
		return method(name, func(c *Context, args []Value) (Value, error) {
			switch name {
			case "toLowerCase":
				return strings.ToLower(s), nil
			case "toUpperCase":
				return strings.ToUpper(s), nil
			case "trim":
				return strings.TrimSpace(s), nil
			case "trimStart":
				return strings.TrimLeft(s, " \t\n\r"), nil
			case "trimEnd":
				return strings.TrimRight(s, " \t\n\r"), nil
			}
			return s, nil
		})
	case "slice", "substring":
		return method(name, func(c *Context, args []Value) (Value, error) {
			start, err := numberArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			from, to := clampIndex(start), len(runes)
			if len(args) > 1 {
				end, err := numberArg(args, 1, name)
				if err != nil {
					return nil, err
				}
				to = clampIndex(end)
			}
			if from > to {
				if name == "substring" {
					from, to = to, from
				} else {
					return "", nil
				}
			}
			return string(runes[from:to]), nil
		})
	case "split":
		return method(name, func(c *Context, args []Value) (Value, error) {
			separator, err := stringArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			items := []Value{}
			for _, part := range strings.Split(s, separator) {
				items = append(items, part)
			}
			return &List{Items: items}, nil
		})
	case "replace", "replaceAll":
		return method(name, func(c *Context, args []Value) (Value, error) {
			old, err := stringArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			replacement, err := stringArg(args, 1, name)
			if err != nil {
				return nil, err
			}
			if name == "replace" {
				return strings.Replace(s, old, replacement, 1), nil
			}
			return strings.ReplaceAll(s, old, replacement), nil
		})
	case "charAt":
		return method(name, func(c *Context, args []Value) (Value, error) {
			index, err := numberArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			if index < 0 || int(index) >= len(runes) {
				return "", nil
			}
			return string(runes[int(index)]), nil
		})
	case "concat":
		return method(name, func(c *Context, args []Value) (Value, error) {
			var b strings.Builder
			b.WriteString(s)
			for _, arg := range args {
				b.WriteString(toString(arg))
			}
			return b.String(), nil
		})
	case "repeat":
		return method(name, func(c *Context, args []Value) (Value, error) {
			count, err := numberArg(args, 0, name)
			if err != nil || count < 0 || count > 10000 {
				return nil, fmt.Errorf("repeat needs a count from 0 to 10000")
			}
			return strings.Repeat(s, int(count)), nil
		})
	case "padStart", "padEnd":
		return method(name, func(c *Context, args []Value) (Value, error) {
			length, err := numberArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			pad := " "
			if len(args) > 1 {
				if pad, err = stringArg(args, 1, name); err != nil {
					return nil, err
				}
			}
			missing := int(length) - len(runes)
			if missing <= 0 || pad == "" {
				return s, nil
			}
			fill := []rune(strings.Repeat(pad, missing/len([]rune(pad))+1))[:missing]
			if name == "padStart" {
				return string(fill) + s, nil
			}
			return s + string(fill), nil
		})
	}
	return nil
}

func listMethod(list *List, name string) *Method {
	items := list.Items
	switch name {
	case "get":
		return method(name, func(c *Context, args []Value) (Value, error) {
			index, err := numberArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			if index < 0 || int(index) >= len(items) {
				return nil, nil
			}
			return items[int(index)], nil
		})
	case "getLast":
		return method(name, func(c *Context, args []Value) (Value, error) {
			if len(items) == 0 {
				return nil, nil
			}
			return items[len(items)-1], nil
		})
	case "map", "filter", "some", "every", "flatMap", "find", "findIndex":
		return method(name, func(c *Context, args []Value) (Value, error) {
			fn, err := lambdaArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			out := []Value{}
			for index, item := range items {
				result, err := c.apply(fn, item, float64(index))
				if err != nil {
					return nil, err
				}
				switch name {
				case "map":
					out = append(out, result)
				case "filter":
					if truthy(result) {
						out = append(out, item)
					}
				case "some":
					if truthy(result) {
						return true, nil
					}
				case "every":
					if !truthy(result) {
						return false, nil
					}
				case "find":
					if truthy(result) {
						return item, nil
					}
				case "findIndex":
					if truthy(result) {
						return float64(index), nil
					}
				case "flatMap":
					if inner, ok := result.(*List); ok {
						out = append(out, inner.Items...)
					} else {
						out = append(out, result)
					}
				}
			}
			switch name {
			case "some":
				return false, nil
			case "every":
				return true, nil
			case "find":
				return nil, nil
			case "findIndex":
				return float64(-1), nil
			}
			return &List{Items: out}, nil
		})
	case "reduce":
		return method(name, func(c *Context, args []Value) (Value, error) {
			fn, err := lambdaArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			start := 0
			var accumulator Value
			if len(args) > 1 {
				accumulator = args[1]
			} else {
				if len(items) == 0 {
					return nil, fmt.Errorf("reduce of an empty list needs an initial value")
				}
				accumulator = items[0]
				start = 1
			}
			for index := start; index < len(items); index++ {
				if accumulator, err = c.apply(fn, accumulator, items[index], float64(index)); err != nil {
					return nil, err
				}
			}
			return accumulator, nil
		})
	case "includes", "indexOf":
		return method(name, func(c *Context, args []Value) (Value, error) {
			needle := firstArg(args)
			for index, item := range items {
				if equal(item, needle) {
					if name == "includes" {
						return true, nil
					}
					return float64(index), nil
				}
			}
			if name == "includes" {
				return false, nil
			}
			return float64(-1), nil
		})
	case "flatten":
		return method(name, func(c *Context, args []Value) (Value, error) {
			out := []Value{}
			for _, item := range items {
				if inner, ok := item.(*List); ok {
					out = append(out, inner.Items...)
				} else {
					out = append(out, item)
				}
			}
			return &List{Items: out}, nil
		})
	case "reverse":
		return method(name, func(c *Context, args []Value) (Value, error) {
			out := make([]Value, len(items))
			for index, item := range items {
				out[len(items)-1-index] = item
			}
			return &List{Items: out}, nil
		})
	case "slice":
		return method(name, func(c *Context, args []Value) (Value, error) {
			clamp := func(value float64) int {
				index := int(value)
				if index < 0 {
					index += len(items)
				}
				return max(0, min(index, len(items)))
			}
			from, to := 0, len(items)
			if len(args) > 0 {
				start, err := numberArg(args, 0, name)
				if err != nil {
					return nil, err
				}
				from = clamp(start)
			}
			if len(args) > 1 {
				end, err := numberArg(args, 1, name)
				if err != nil {
					return nil, err
				}
				to = clamp(end)
			}
			if from > to {
				return &List{Items: []Value{}}, nil
			}
			return &List{Items: append([]Value(nil), items[from:to]...)}, nil
		})
	case "concat":
		return method(name, func(c *Context, args []Value) (Value, error) {
			out := append([]Value(nil), items...)
			for _, arg := range args {
				if inner, ok := arg.(*List); ok {
					out = append(out, inner.Items...)
				} else {
					out = append(out, arg)
				}
			}
			return &List{Items: out}, nil
		})
	case "join":
		return method(name, func(c *Context, args []Value) (Value, error) {
			separator := ","
			if len(args) > 0 {
				separator = toString(args[0])
			}
			parts := make([]string, 0, len(items))
			for _, item := range items {
				parts = append(parts, toString(item))
			}
			return strings.Join(parts, separator), nil
		})
	case "sort":
		return method(name, func(c *Context, args []Value) (Value, error) {
			out := append([]Value(nil), items...)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if len(args) > 0 {
					result, err := c.apply(args[0], out[i], out[j])
					if err != nil {
						sortErr = err
						return false
					}
					return toNumber(result) < 0
				}
				order, ok := compare(out[i], out[j])
				if !ok {
					return toString(out[i]) < toString(out[j])
				}
				return order < 0
			})
			return &List{Items: out}, sortErr
		})
	}
	return nil
}

func mapMethod(m *Map, name string) *Method {
	switch name {
	case "get":
		return method(name, func(c *Context, args []Value) (Value, error) {
			value, _ := m.Get(toString(firstArg(args)))
			return value, nil
		})
	case "set":
		return method(name, func(c *Context, args []Value) (Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("set needs a key and a value")
			}
			copied := m.clone()
			copied.Set(toString(args[0]), args[1])
			return copied, nil
		})
	case "entries":
		return method(name, func(c *Context, args []Value) (Value, error) {
			items := make([]Value, 0, len(m.keys))
			for _, key := range m.keys {
				items = append(items, &List{Items: []Value{key, m.values[key]}})
			}
			return &List{Items: items}, nil
		})
	case "keys":
		return method(name, func(c *Context, args []Value) (Value, error) {
			items := make([]Value, 0, len(m.keys))
			for _, key := range m.keys {
				items = append(items, key)
			}
			return &List{Items: items}, nil
		})
	case "values":
		return method(name, func(c *Context, args []Value) (Value, error) {
			items := make([]Value, 0, len(m.keys))
			for _, key := range m.keys {
				items = append(items, m.values[key])
			}
			return &List{Items: items}, nil
		})
	}
	return nil
}

func numberMethod(number float64, name string) *Method {
	switch name {
	case "toString":
		return method(name, func(c *Context, args []Value) (Value, error) { return formatNumber(number), nil })
	case "toFixed":
		return method(name, func(c *Context, args []Value) (Value, error) {
			digits := 0.0
			if len(args) > 0 {
				var err error
				if digits, err = numberArg(args, 0, name); err != nil {
					return nil, err
				}
			}
			return strconv.FormatFloat(number, 'f', int(digits), 64), nil
		})
	}
	return nil
}

func dateMethod(d Date, name string) *Method {
	plus := func(years, months, days int, duration time.Duration) func(c *Context, args []Value) (Value, error) {
		return func(c *Context, args []Value) (Value, error) {
			amount, err := numberArg(args, 0, name)
			if err != nil {
				return nil, err
			}
			n := int(amount)
			moved := d.Time
			if years != 0 || months != 0 {
				// Like Jira's dates, moving by months keeps the day within the target month.
				total := int(moved.Month()) - 1 + years*n*12 + months*n
				year, month := moved.Year()+floorDiv(total, 12), time.Month(floorMod(total, 12)+1)
				day := min(moved.Day(), time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day())
				moved = time.Date(year, month, day, moved.Hour(), moved.Minute(), moved.Second(), moved.Nanosecond(), moved.Location())
			}
			moved = moved.AddDate(0, 0, days*n).Add(time.Duration(n) * duration)
			return Date{Time: moved, Calendar: d.Calendar}, nil
		}
	}
	switch name {
	case "getTime":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.UnixMilli()), nil })
	case "toISOString", "toString":
		return method(name, func(c *Context, args []Value) (Value, error) { return d.String(), nil })
	case "toCalendarDate", "toCalendarDateUTC":
		return method(name, func(c *Context, args []Value) (Value, error) {
			t := d.Time
			if name == "toCalendarDateUTC" {
				t = t.UTC()
			}
			return Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Calendar: true}, nil
		})
	case "getFullYear":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Year()), nil })
	case "getMonth":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Month() - 1), nil })
	case "getDate":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Day()), nil })
	case "getDay":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Weekday()), nil })
	case "getHours":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Hour()), nil })
	case "getMinutes":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Minute()), nil })
	case "getSeconds":
		return method(name, func(c *Context, args []Value) (Value, error) { return float64(d.Time.Second()), nil })
	case "plusYears":
		return method(name, plus(1, 0, 0, 0))
	case "minusYears":
		return method(name, plus(-1, 0, 0, 0))
	case "plusMonths":
		return method(name, plus(0, 1, 0, 0))
	case "minusMonths":
		return method(name, plus(0, -1, 0, 0))
	case "plusWeeks":
		return method(name, plus(0, 0, 7, 0))
	case "minusWeeks":
		return method(name, plus(0, 0, -7, 0))
	case "plusDays":
		return method(name, plus(0, 0, 1, 0))
	case "minusDays":
		return method(name, plus(0, 0, -1, 0))
	case "plusHours":
		return method(name, plus(0, 0, 0, time.Hour))
	case "minusHours":
		return method(name, plus(0, 0, 0, -time.Hour))
	case "plusMinutes":
		return method(name, plus(0, 0, 0, time.Minute))
	case "minusMinutes":
		return method(name, plus(0, 0, 0, -time.Minute))
	}
	return nil
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }
