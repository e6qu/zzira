package jexpr

import (
	"sort"
	"strconv"
	"strings"
)

// AnalysisError is a syntax or type problem in an expression.
type AnalysisError struct {
	Line       int
	Column     int
	Message    string
	Type       string // syntax, type or other
	Expression string
}

// ComplexityFormula describes how many expensive operations an expression may
// perform, in terms of the sizes of the lists it iterates.
type ComplexityFormula struct {
	ExpensiveOperations string
	Variables           map[string]string
}

// Analysis is the result of Jira's expression analysis.
type Analysis struct {
	Expression string
	Valid      bool
	Errors     []AnalysisError
	Type       string
	Complexity *ComplexityFormula
}

// DefaultContextTypes are the types of Jira's common context variables.
var DefaultContextTypes = map[string]string{
	"user": "User", "app": "App", "issue": "Issue", "issues": "List<Issue>", "project": "Project",
	"sprint": "Sprint", "board": "Board", "serviceDesk": "ServiceDesk", "customerRequest": "CustomerRequest",
}

// MemberTypes are the properties of Jira's expression types.
var MemberTypes = map[string]map[string]string{
	"Issue": {
		"id": "Number", "key": "String", "summary": "String", "description": "RichText",
		"issueType": "IssueType", "project": "Project", "status": "Status", "priority": "Priority", "resolution": "Resolution",
		"assignee": "User", "reporter": "User", "creator": "User", "created": "Date", "updated": "Date",
		"resolutionDate": "Date", "labels": "List<String>", "parent": "Issue",
		"subtasks": "List<Issue>", "links": "List<IssueLink>", "comments": "List<Comment>", "attachments": "List<Attachment>",
		"worklogs": "List<Worklog>", "changelogs": "List<Changelog>", "properties": "EntityProperties",
		"isEpic": "Boolean", "epic": "Issue", "stories": "List<Issue>",
		"sprint": "Sprint", "closedSprints": "List<Sprint>", "flagged": "Boolean", "votes": "Number", "watches": "Number",
	},
	"User":             {"accountId": "String", "displayName": "String", "timeZone": "String"},
	"App":              {"key": "String"},
	"Project":          {"id": "Number", "key": "String", "name": "String", "projectTypeKey": "String", "lead": "User"},
	"IssueType":        {"id": "Number", "name": "String", "description": "String", "iconUrl": "String", "isSubtask": "Boolean", "hierarchyLevel": "Number"},
	"Status":           {"id": "Number", "name": "String", "description": "String", "category": "StatusCategory"},
	"StatusCategory":   {"id": "Number", "key": "String", "name": "String", "colorName": "String"},
	"Priority":         {"id": "Number", "name": "String", "description": "String", "iconUrl": "String"},
	"Resolution":       {"id": "Number", "name": "String", "description": "String"},
	"Comment":          {"id": "Number", "body": "RichText", "author": "User", "created": "Date", "updated": "Date"},
	"Attachment":       {"id": "Number", "author": "User", "filename": "String", "size": "Number", "mimeType": "String", "created": "Date"},
	"IssueLink":        {"id": "Number", "type": "IssueLinkType", "direction": "String", "linkedIssue": "Issue"},
	"IssueLinkType":    {"id": "Number", "name": "String", "inward": "String", "outward": "String"},
	"Changelog":        {"id": "String", "author": "User", "created": "Date", "items": "List<ChangelogItem>"},
	"ChangelogItem":    {"field": "String", "fieldId": "String", "fieldtype": "String", "from": "String", "fromString": "String", "to": "String", "toString": "String"},
	"Worklog":          {"id": "Number", "author": "User", "timeSpentSeconds": "Number", "comment": "RichText", "created": "Date", "started": "Date"},
	"RichText":         {"plainText": "String"},
	"Sprint":           {"id": "Number", "name": "String", "state": "String", "goal": "String", "startDate": "Date", "endDate": "Date", "originBoardId": "Number"},
	"Board":            {"id": "Number", "name": "String", "type": "String", "hasBacklog": "Boolean", "hasSprints": "Boolean"},
	"ServiceDesk":      {"id": "Number", "project": "Project"},
	"CustomerRequest":  {"id": "Number", "key": "String", "issue": "Issue", "reporter": "User"},
	"EntityProperties": {},
}

// ExpensiveMembers are the properties whose loading counts as an expensive
// operation.
var ExpensiveMembers = map[string]map[string]bool{
	"Issue": {"subtasks": true, "links": true, "comments": true, "attachments": true, "worklogs": true, "changelogs": true,
		"properties": true, "parent": true, "epic": true, "stories": true, "sprint": true, "closedSprints": true,
		"flagged": true, "votes": true, "watches": true},
	"User":        {"groups": true, "properties": true},
	"Project":     {"properties": true},
	"IssueType":   {"properties": true},
	"Comment":     {"properties": true},
	"Sprint":      {"properties": true},
	"Board":       {"activeSprints": true, "futureSprints": true, "properties": true},
	"ServiceDesk": {"project": true},
}

// Analyse runs Jira's syntax, type or complexity check on an expression.
func Analyse(expression, check string, contextTypes map[string]string) Analysis {
	result := Analysis{Expression: expression, Valid: true}
	node, err := Parse(expression)
	if err != nil {
		result.Valid = false
		problem := AnalysisError{Message: err.Error(), Type: "syntax"}
		if syntaxErr, ok := err.(*SyntaxError); ok {
			problem.Line, problem.Column, problem.Message = syntaxErr.Pos.Line, syntaxErr.Pos.Column, syntaxErr.Message
		}
		result.Errors = []AnalysisError{problem}
		return result
	}
	types := map[string]string{}
	for name, typ := range DefaultContextTypes {
		types[name] = typ
	}
	for name, typ := range contextTypes {
		types[name] = typ
	}
	checker := &typeChecker{source: expression, types: types}
	switch check {
	case "type":
		result.Type = checker.infer(node, nil)
		if len(checker.errors) > 0 {
			result.Valid = false
			result.Errors = checker.errors
			result.Type = ""
		}
	case "complexity":
		checker.infer(node, nil)
		counter := &complexityCounter{checker: checker, terms: map[string]int{}, variables: map[string]string{}}
		counter.walk(node, "", nil)
		result.Complexity = counter.formula()
	}
	return result
}

type typeChecker struct {
	source string
	types  map[string]string
	errors []AnalysisError
	// nodeTypes remembers inferred types for the complexity count.
	nodeTypes map[Node]string
}

func (t *typeChecker) snippet(node Node) string {
	s := node.Span()
	if s.start.Offset < 0 || s.end > len(t.source) || s.start.Offset > s.end {
		return ""
	}
	return t.source[s.start.Offset:s.end]
}

func (t *typeChecker) fail(node Node, message string) string {
	s := node.Span()
	t.errors = append(t.errors, AnalysisError{Line: s.start.Line, Column: s.start.Column, Message: message, Type: "type", Expression: t.snippet(node)})
	return "Any"
}

func listElement(typ string) (string, bool) {
	if strings.HasPrefix(typ, "List<") && strings.HasSuffix(typ, ">") {
		return typ[len("List<") : len(typ)-1], true
	}
	return "", false
}

func (t *typeChecker) remember(node Node, typ string) string {
	if t.nodeTypes == nil {
		t.nodeTypes = map[Node]string{}
	}
	t.nodeTypes[node] = typ
	return typ
}

func (t *typeChecker) infer(node Node, locals map[string]string) string {
	return t.remember(node, t.inferNode(node, locals))
}

func (t *typeChecker) inferNode(node Node, locals map[string]string) string {
	switch n := node.(type) {
	case *Literal:
		switch n.Value.(type) {
		case nil:
			return "Null"
		case bool:
			return "Boolean"
		case float64:
			return "Number"
		case string:
			return "String"
		}
	case *Ident:
		if typ, ok := locals[n.Name]; ok {
			return typ
		}
		if typ, ok := t.types[n.Name]; ok {
			return typ
		}
		switch n.Name {
		case "Math", "JSON":
			return "Map"
		case "Number", "String", "Boolean":
			return "Function"
		}
		return t.fail(n, "Unrecognized identifier: \""+n.Name+"\".")
	case *Member:
		return t.memberType(n, t.infer(n.Object, locals), n.Name)
	case *Index:
		object := t.infer(n.Object, locals)
		t.infer(n.Key, locals)
		if element, ok := listElement(object); ok {
			return element
		}
		if object == "String" {
			return "String"
		}
		return "Any"
	case *Call:
		return t.callType(n, locals)
	case *Unary:
		operand := t.infer(n.X, locals)
		switch n.Op {
		case "!":
			return "Boolean"
		case "typeof":
			return "String"
		}
		if operand != "Number" && operand != "Any" {
			return t.fail(n, "The operator "+n.Op+" needs a Number, not "+operand+".")
		}
		return "Number"
	case *Binary:
		left, right := t.infer(n.L, locals), t.infer(n.R, locals)
		switch n.Op {
		case "==", "!=", "===", "!==", "<", "<=", ">", ">=":
			return "Boolean"
		case "&&", "||", "??":
			if left == right {
				return left
			}
			return "Any"
		case "+":
			if left == "String" || right == "String" {
				return "String"
			}
			if (left == "Number" || left == "Any") && (right == "Number" || right == "Any") {
				return "Number"
			}
			return t.fail(n, "Cannot add "+left+" and "+right+".")
		default:
			if (left != "Number" && left != "Any") || (right != "Number" && right != "Any") {
				return t.fail(n, "The operator "+n.Op+" needs Numbers, not "+left+" and "+right+".")
			}
			return "Number"
		}
	case *Conditional:
		t.infer(n.Test, locals)
		then, otherwise := t.infer(n.Then, locals), t.infer(n.Else, locals)
		if then == otherwise {
			return then
		}
		return "Any"
	case *Arrow:
		inner := map[string]string{}
		for name, typ := range locals {
			inner[name] = typ
		}
		for _, param := range n.Params {
			inner[param] = "Any"
		}
		return "(" + strings.Repeat("Any, ", max(len(n.Params)-1, 0)) + map[bool]string{true: "Any", false: ""}[len(n.Params) > 0] + ") => " + t.infer(n.Body, inner)
	case *ArrayLit:
		element := ""
		for _, item := range n.Elements {
			typ := t.infer(item, locals)
			if spread, ok := item.(*Spread); ok {
				typ, _ = listElement(t.infer(spread.X, locals))
			}
			if element == "" {
				element = typ
			} else if element != typ {
				element = "Any"
			}
		}
		if element == "" {
			element = "Any"
		}
		return "List<" + element + ">"
	case *ObjectLit:
		for _, property := range n.Properties {
			t.infer(property.Value, locals)
		}
		return "Map"
	case *Template:
		for _, expr := range n.Exprs {
			t.infer(expr, locals)
		}
		return "String"
	case *New:
		for _, arg := range n.Args {
			t.infer(arg, locals)
		}
		switch n.Name {
		case "Date", "Map", "Issue", "User", "Project":
			return n.Name
		}
		return t.fail(n, "Unrecognized type: \""+n.Name+"\".")
	case *Spread:
		return t.infer(n.X, locals)
	}
	return "Any"
}

func (t *typeChecker) memberType(node *Member, object, name string) string {
	if object == "Any" || object == "Map" || object == "Null" {
		return "Any"
	}
	if element, ok := listElement(object); ok {
		_ = element
		if name == "length" {
			return "Number"
		}
		return "Function"
	}
	switch object {
	case "String":
		if name == "length" {
			return "Number"
		}
		return "Function"
	case "Number", "Date", "CalendarDate", "EntityProperties":
		return "Function"
	}
	if object == "Issue" && strings.HasPrefix(name, "customfield_") {
		return "Any"
	}
	if members, ok := MemberTypes[object]; ok {
		if typ, ok := members[name]; ok {
			return typ
		}
		names := make([]string, 0, len(members))
		for member := range members {
			names = append(names, member)
		}
		sort.Strings(names)
		return t.fail(node, "Unrecognized property of `"+t.snippet(node.Object)+"`: \""+name+"\" ('"+name+"'). Available properties of type '"+object+"' are: '"+strings.Join(names, "', '")+"'.")
	}
	return "Any"
}

var listLambdaMethods = map[string]bool{"map": true, "filter": true, "some": true, "every": true, "flatMap": true, "find": true, "findIndex": true, "reduce": true, "sort": true}

func (t *typeChecker) callType(n *Call, locals map[string]string) string {
	member, isMember := n.Callee.(*Member)
	if !isMember {
		t.infer(n.Callee, locals)
		for _, arg := range n.Args {
			t.infer(arg, locals)
		}
		if ident, ok := n.Callee.(*Ident); ok {
			switch ident.Name {
			case "Number":
				return "Number"
			case "String":
				return "String"
			case "Boolean":
				return "Boolean"
			}
		}
		return "Any"
	}
	object := t.infer(member.Object, locals)
	t.remember(member, "Function")
	element, isList := listElement(object)
	lambdaResult := ""
	for _, arg := range n.Args {
		if arrow, ok := arg.(*Arrow); ok && isList && listLambdaMethods[member.Name] {
			inner := map[string]string{}
			for name, typ := range locals {
				inner[name] = typ
			}
			for index, param := range arrow.Params {
				switch {
				case member.Name == "reduce" && index == 0:
					inner[param] = "Any"
				case member.Name == "reduce" && index == 1, member.Name != "reduce" && index == 0, member.Name == "sort":
					inner[param] = element
				default:
					inner[param] = "Number"
				}
			}
			lambdaResult = t.infer(arrow.Body, inner)
			t.remember(arrow, "Function")
			continue
		}
		t.infer(arg, locals)
	}
	if isList {
		switch member.Name {
		case "map":
			return "List<" + lambdaResult + ">"
		case "filter", "sort", "reverse", "slice", "concat":
			return object
		case "flatMap":
			if inner, ok := listElement(lambdaResult); ok {
				return "List<" + inner + ">"
			}
			return "List<" + lambdaResult + ">"
		case "flatten":
			if inner, ok := listElement(element); ok {
				return "List<" + inner + ">"
			}
			return object
		case "some", "every", "includes":
			return "Boolean"
		case "indexOf", "findIndex":
			return "Number"
		case "get", "getLast", "find":
			return element
		case "join":
			return "String"
		case "reduce":
			if len(n.Args) > 1 {
				return t.nodeTypes[n.Args[1]]
			}
			return "Any"
		}
		return t.fail(n, "Unrecognized function of `"+t.snippet(member.Object)+"`: \""+member.Name+"\".")
	}
	switch object {
	case "String":
		switch member.Name {
		case "includes", "startsWith", "endsWith":
			return "Boolean"
		case "indexOf", "lastIndexOf":
			return "Number"
		case "split":
			return "List<String>"
		}
		return "String"
	case "Date", "CalendarDate":
		switch member.Name {
		case "getTime", "getFullYear", "getMonth", "getDate", "getDay", "getHours", "getMinutes", "getSeconds":
			return "Number"
		case "toISOString", "toString":
			return "String"
		case "toCalendarDate", "toCalendarDateUTC":
			return "CalendarDate"
		}
		return object
	case "Number":
		return "String"
	case "EntityProperties":
		if member.Name == "keys" {
			return "List<String>"
		}
		return "Any"
	case "Map":
		switch member.Name {
		case "set":
			return "Map"
		case "entries":
			return "List<List<Any>>"
		case "keys":
			return "List<String>"
		}
	}
	return "Any"
}

type complexityCounter struct {
	checker   *typeChecker
	constant  int
	terms     map[string]int
	variables map[string]string
	names     []string
}

func (c *complexityCounter) variableFor(node Node) string {
	source := c.checker.snippet(node)
	for name, expr := range c.variables {
		if expr == source {
			return name
		}
	}
	letters := []string{"N", "M", "K", "L", "P", "Q", "R", "S"}
	name := letters[len(c.variables)%len(letters)]
	if len(c.variables) >= len(letters) {
		name += strconv.Itoa(len(c.variables) / len(letters))
	}
	c.variables[name] = source
	c.names = append(c.names, name)
	return name
}

func (c *complexityCounter) count(multiplier string) {
	if multiplier == "" {
		c.constant++
		return
	}
	c.terms[multiplier]++
}

// walk counts expensive operations; inside a lambda iterating a list the count
// is multiplied by that list's size.
func (c *complexityCounter) walk(node Node, multiplier string, _ map[string]string) {
	switch n := node.(type) {
	case *Member:
		c.walk(n.Object, multiplier, nil)
		objectType := c.checker.nodeTypes[n.Object]
		if ExpensiveMembers[objectType][n.Name] || (objectType == "Issue" && strings.HasPrefix(n.Name, "customfield_")) {
			c.count(multiplier)
		}
	case *Index:
		c.walk(n.Object, multiplier, nil)
		c.walk(n.Key, multiplier, nil)
	case *Call:
		member, isMember := n.Callee.(*Member)
		if isMember && listLambdaMethods[member.Name] {
			if _, isList := listElement(c.checker.nodeTypes[member.Object]); isList {
				c.walk(member.Object, multiplier, nil)
				inner := c.variableFor(member.Object)
				if multiplier != "" {
					inner = multiplier
				}
				for _, arg := range n.Args {
					if arrow, ok := arg.(*Arrow); ok {
						c.walk(arrow.Body, inner, nil)
						continue
					}
					c.walk(arg, multiplier, nil)
				}
				return
			}
		}
		c.walk(n.Callee, multiplier, nil)
		for _, arg := range n.Args {
			c.walk(arg, multiplier, nil)
		}
	case *Unary:
		c.walk(n.X, multiplier, nil)
	case *Binary:
		c.walk(n.L, multiplier, nil)
		c.walk(n.R, multiplier, nil)
	case *Conditional:
		c.walk(n.Test, multiplier, nil)
		c.walk(n.Then, multiplier, nil)
		c.walk(n.Else, multiplier, nil)
	case *Arrow:
		c.walk(n.Body, multiplier, nil)
	case *ArrayLit:
		for _, element := range n.Elements {
			c.walk(element, multiplier, nil)
		}
	case *ObjectLit:
		for _, property := range n.Properties {
			c.walk(property.Value, multiplier, nil)
		}
	case *Template:
		for _, expr := range n.Exprs {
			c.walk(expr, multiplier, nil)
		}
	case *New:
		for _, arg := range n.Args {
			c.walk(arg, multiplier, nil)
		}
		switch n.Name {
		case "Issue", "User", "Project":
			c.count(multiplier)
		}
	case *Spread:
		c.walk(n.X, multiplier, nil)
	}
}

func (c *complexityCounter) formula() *ComplexityFormula {
	parts := []string{}
	if c.constant > 0 || len(c.terms) == 0 {
		parts = append(parts, strconv.Itoa(c.constant))
	}
	variables := map[string]string{}
	for _, name := range c.names {
		count, ok := c.terms[name]
		if !ok {
			continue
		}
		variables[name] = c.variables[name]
		if count == 1 {
			parts = append(parts, name)
		} else {
			parts = append(parts, strconv.Itoa(count)+" * "+name)
		}
	}
	formula := &ComplexityFormula{ExpensiveOperations: strings.Join(parts, " + ")}
	if len(variables) > 0 {
		formula.Variables = variables
	}
	return formula
}
