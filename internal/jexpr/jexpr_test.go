package jexpr

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func evaluate(t *testing.T, c *Context, expression string) any {
	t.Helper()
	value, err := c.Evaluate(expression)
	if err != nil {
		t.Fatalf("%s: %v", expression, err)
	}
	encoded, err := c.ToJSON(value)
	if err != nil {
		t.Fatalf("%s: %v", expression, err)
	}
	return encoded
}

func asJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEvaluatesTheLanguage(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)
	cases := map[string]string{
		`1 + 2 * 3`:                 `7`,
		`(1 + 2) * 3 % 4`:           `1`,
		`'Jira' + ' ' + 5`:          `"Jira 5"`,
		"`Sum: ${1 + 1}!`":          `"Sum: 2!"`,
		`[1, 2, 3].map(x => x * 2)`: `[2,4,6]`,
		`[1, 2, 3, 4].filter(x => x % 2 == 0).length`:         `2`,
		`[3, 1, 2].sort()`:                                    `[1,2,3]`,
		`[3, 1, 2].sort((a, b) => b - a)`:                     `[3,2,1]`,
		`[1, 2, 3].reduce((sum, x) => sum + x, 10)`:           `16`,
		`[[1], [2, 3]].flatten()`:                             `[1,2,3]`,
		`[1, 2].some(x => x > 1) && [1, 2].every(x => x > 0)`: `true`,
		`['a', 'b'].includes('b') ? 'yes' : 'no'`:             `"yes"`,
		`{ a: 1, b: 'two' }`:                                  `{"a":1,"b":"two"}`,
		`{ a: 1 }.a + new Map().set('x', 4).get('x')`:         `5`,
		`{ ...{ a: 1 }, b: 2 }.b`:                             `2`,
		`[...[1, 2], 3]`:                                      `[1,2,3]`,
		`null ?? 'fallback'`:                                  `"fallback"`,
		`null?.missing`:                                       `null`,
		`'Hello World'.split(' ').map(w => w.toLowerCase())`:  `["hello","world"]`,
		`'abc'.slice(1) + 'abc'.substring(0, 1)`:              `"bca"`,
		`'7'.padStart(3, '0')`:                                `"007"`,
		`typeof 'x' + typeof 1 + typeof (x => x)`:             `"stringnumberfunction"`,
		`Math.max(1, 5, 3) + Math.floor(2.7)`:                 `7`,
		`JSON.stringify({ a: [1, true] })`:                    `"{\"a\":[1,true]}"`,
		`JSON.parse('{"b": 2}').b`:                            `2`,
		`new Date('2026-01-31').plusMonths(1).toString()`:     `"2026-02-28"`,
		`new Date().getFullYear()`:                            `2026`,
		`new Date(2026, 0, 2).toISOString()`:                  `"2026-01-02T00:00:00.000Z"`,
		`new Date('2026-09-14T10:00:00Z') < new Date()`:       `true`,
		`'\u0041\u00e9' + "\t".length`:                        `"Aé1"`,
		`!'' && !0 && !!'x'`:                                  `true`,
		`1 == 1.0 && 'a' != 'b'`:                              `true`,
		`// comment
		 [1, /* inline */ 2].length`: `2`,
	}
	for expression, want := range cases {
		c := &Context{Now: func() time.Time { return now }}
		if got := asJSON(t, evaluate(t, c, expression)); got != want {
			t.Errorf("%s = %s, want %s", expression, got, want)
		}
	}
}

func TestEntitiesCountExpensiveOperations(t *testing.T) {
	loads := 0
	comments := &List{Items: []Value{
		&Record{Type: "Comment", Fields: map[string]Value{"id": float64(1), "body": "first"}},
		&Record{Type: "Comment", Fields: map[string]Value{"id": float64(2), "body": "second"}},
	}}
	issue := &Record{
		Type:   "Issue",
		Fields: map[string]Value{"id": float64(10001), "key": "ABC-1", "summary": "Hello"},
		Lazy: map[string]func(c *Context) (Value, error){
			"comments": func(c *Context) (Value, error) { loads++; return comments, nil },
		},
		Bean: func(c *Context) (any, error) { return map[string]any{"id": "10001", "key": "ABC-1"}, nil },
	}
	c := &Context{Variables: map[string]Value{"issue": issue}}
	got := asJSON(t, evaluate(t, c, `{ key: issue.key, bodies: issue.comments.map(c => c.body), count: issue.comments.length, issue: issue }`))
	if got != `{"bodies":["first","second"],"count":2,"issue":{"id":"10001","key":"ABC-1"},"key":"ABC-1"}` {
		t.Fatal(got)
	}
	used := c.Used()
	if loads != 1 || used.ExpensiveOperations != 1 || used.Beans != 1 || used.Steps == 0 || used.PrimitiveValues != 4 {
		t.Fatalf("loads=%d used=%+v", loads, used)
	}

	limited := &Context{Variables: map[string]Value{"issue": issue}, Limits: Limits{Steps: 5, ExpensiveOperations: 10, Beans: 10, PrimitiveValues: 10}}
	if _, err := limited.Evaluate(`[1,2,3,4,5,6].map(x => x + 1)`); err == nil || !strings.Contains(err.Error(), "limit of 5 steps") {
		t.Fatalf("step limit err=%v", err)
	}
	if _, err := (&Context{Variables: map[string]Value{"issue": issue}}).Evaluate(`issue.missing`); err == nil ||
		!strings.Contains(err.Error(), "Unrecognized property of `issue`: \"missing\"") || !strings.Contains(err.Error(), "Available properties of type 'Issue'") {
		t.Fatalf("unknown property err=%v", err)
	}
	if _, err := (&Context{}).Evaluate(`issue.key`); err == nil || !strings.Contains(err.Error(), `Unrecognized identifier: "issue"`) {
		t.Fatalf("unknown identifier err=%v", err)
	}
	if _, err := (&Context{}).Evaluate(`null.key`); err == nil || !strings.Contains(err.Error(), `"null.key" - Cannot read property "key" of null.`) {
		t.Fatalf("null member err=%v", err)
	}
}

func TestAnalysesSyntaxTypesAndComplexity(t *testing.T) {
	syntax := Analyse("issue.key\n  + >", "syntax", nil)
	if syntax.Valid || len(syntax.Errors) != 1 || syntax.Errors[0].Line != 2 || syntax.Errors[0].Column != 5 || syntax.Errors[0].Type != "syntax" ||
		syntax.Errors[0].Message != primaryExpectation+" expected, > encountered." {
		t.Fatalf("syntax analysis = %+v", syntax)
	}
	if valid := Analyse(`issues.map(issue => issue.key)`, "syntax", nil); !valid.Valid || valid.Type != "" {
		t.Fatalf("valid syntax = %+v", valid)
	}
	for expression, want := range map[string]string{
		`issue.comments.map(c => c.author.displayName)`: "List<String>",
		`issues.filter(i => i.isEpic).length`:           "Number",
		`issue.summary.split(' ')`:                      "List<String>",
		`issue.created.plusDays(1)`:                     "Date",
		`{ a: 1 }`:                                      "Map",
		`[issue.id, 1]`:                                 "List<Number>",
		`ticket.key`:                                    "String",
	} {
		analysis := Analyse(expression, "type", map[string]string{"ticket": "Issue"})
		if !analysis.Valid || analysis.Type != want {
			t.Errorf("%s: %+v, want %s", expression, analysis, want)
		}
	}
	invalid := Analyse(`issue.nope`, "type", nil)
	if invalid.Valid || len(invalid.Errors) != 1 || invalid.Errors[0].Type != "type" || !strings.Contains(invalid.Errors[0].Message, `Unrecognized property of `+"`issue`"+`: "nope"`) {
		t.Fatalf("type error = %+v", invalid)
	}
	for expression, want := range map[string]string{
		`issue.key`:                   "0",
		`new Issue(10010).comments`:   "2",
		`issues.map(i => i.comments)`: "N",
		`[issue.properties, issues.map(i => i.comments.map(c => c.properties))]`: "1 + 2 * N",
	} {
		analysis := Analyse(expression, "complexity", nil)
		if analysis.Complexity == nil || analysis.Complexity.ExpensiveOperations != want {
			t.Errorf("%s complexity = %+v, want %s", expression, analysis.Complexity, want)
		}
	}
	if formula := Analyse(`issues.map(i => i.comments)`, "complexity", nil).Complexity; formula.Variables["N"] != "issues" {
		t.Fatalf("variables = %+v", formula.Variables)
	}
}
