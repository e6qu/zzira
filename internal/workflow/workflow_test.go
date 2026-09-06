package workflow

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
)

func TestAvailableFromTodo(t *testing.T) {
	ids := map[string]bool{}
	for _, tr := range Default().Available("st_todo") {
		ids[tr.ID] = true
	}
	if !ids["21"] || !ids["31"] || len(ids) != 2 {
		t.Fatalf("transitions from To Do must be {21,31}: %v", ids)
	}
}

func TestNestedTransitionConditions(t *testing.T) {
	transition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
		RuleKey: RuleRestrictIssueTransition, Parameters: map[string]string{"accountIds": "allow-reporter"},
	}}, ConditionGroups: []ConditionGroup{{Operation: "ANY", Conditions: []Rule{
		{RuleKey: RuleRestrictIssueTransition, Parameters: map[string]string{"accountIds": "allow-assignee"}},
		{RuleKey: RuleRestrictIssueTransition, Parameters: map[string]string{"accountIds": "usr_manager"}},
	}}}}}
	if !transition.ConditionsAllow(EvaluationContext{ActorID: "usr_manager", ReporterID: "usr_manager"}) {
		t.Fatal("reporter matching the nested manager condition must be allowed")
	}
	if transition.ConditionsAllow(EvaluationContext{ActorID: "usr_other", ReporterID: "usr_other"}) {
		t.Fatal("the outer reporter condition cannot bypass the nested ANY group")
	}
}

func TestRestrictFromAllUsersDistinguishesAPIRequests(t *testing.T) {
	uiOnly := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
		RuleKey: RuleRestrictFromAllUsers, Parameters: map[string]string{"restrictMode": "users"},
	}}}}
	if uiOnly.ConditionsAllow(EvaluationContext{ActorID: "usr_actor"}) {
		t.Fatal("users mode must hide the transition from browser users")
	}
	if !uiOnly.ConditionsAllow(EvaluationContext{ActorID: "usr_actor", IsAPI: true}) {
		t.Fatal("users mode must preserve API transitions")
	}
	all := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
		ID: "block", RuleKey: RuleRestrictFromAllUsers, Parameters: map[string]string{"restrictMode": "usersAndAPI"},
	}}}}
	if all.ConditionsAllow(EvaluationContext{ActorID: "usr_actor", IsAPI: true}) {
		t.Fatal("usersAndAPI mode must block API transitions")
	}
	if err := ValidateTransitionRules(all); err != nil {
		t.Fatal(err)
	}
	all.Conditions.Conditions[0].Parameters["restrictMode"] = "invalid"
	if err := ValidateTransitionRules(all); err == nil {
		t.Fatal("invalid restriction mode was accepted")
	}
}

func TestCheckFieldValueConditionComparators(t *testing.T) {
	issue := &models.Issue{Summary: "Ready", Labels: []string{"released", "verified"}, Fields: map[string]json.RawMessage{
		"customfield_points": json.RawMessage(`8`),
		"customfield_due":    json.RawMessage(`"2026-10-01"`),
		"customfield_option": json.RawMessage(`{"id":"10001","value":"Gold"}`),
	}}
	context := ContextForIssue("usr_actor", issue)
	tests := []struct {
		name       string
		field      string
		value      string
		comparator string
		kind       string
		want       bool
	}{
		{name: "text equal", field: "summary", value: `["Ready"]`, comparator: "=", kind: "STRING", want: true},
		{name: "array member", field: "labels", value: `["released"]`, comparator: "=", kind: "STRING", want: true},
		{name: "array differs", field: "labels", value: `["blocked"]`, comparator: "!=", kind: "STRING", want: true},
		{name: "number", field: "customfield_points", value: `["5"]`, comparator: ">=", kind: "NUMBER", want: true},
		{name: "date", field: "customfield_due", value: `["2026-12-01"]`, comparator: "<", kind: "DATE_WITHOUT_TIME", want: true},
		{name: "option", field: "customfield_option", value: `["10001"]`, comparator: "=", kind: "OPTIONID", want: true},
		{name: "option label", field: "customfield_option", value: `["Gold"]`, comparator: "=", kind: "STRING", want: true},
		{name: "missing", field: "customfield_missing", value: `["x"]`, comparator: "!=", kind: "STRING", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
				RuleKey: RuleCheckFieldValue, Parameters: map[string]string{"fieldId": test.field, "fieldValue": test.value, "comparator": test.comparator, "comparisonType": test.kind},
			}}}}
			if got := transition.ConditionsAllow(context); got != test.want {
				t.Fatalf("condition result = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCheckFieldValueConfigurationValidation(t *testing.T) {
	rule := Rule{ID: "check", RuleKey: RuleCheckFieldValue, Parameters: map[string]string{
		"fieldId": "summary", "fieldValue": `["Ready"]`, "comparator": "=", "comparisonType": "STRING",
	}}
	transition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{rule}}}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Conditions.Conditions[0].Parameters["fieldValue"] = "Ready"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("non-array fieldValue was accepted")
	}
	transition.Conditions.Conditions[0].Parameters["fieldValue"] = `["Ready"]`
	transition.Conditions.Conditions[0].Parameters["comparisonType"] = "UNKNOWN"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unknown comparison type was accepted")
	}
}

func TestRequiredFieldValidatorUsesConfiguredMessage(t *testing.T) {
	transition := Transition{Validators: []Rule{{RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "fieldRequired", "fieldsRequired": "assignee,customfield_10001", "errorMessage": "Complete ownership and review notes",
	}}}}
	context := EvaluationContext{FieldPresent: map[string]bool{"assignee": true}}
	if err := transition.ValidateRules(context); err == nil || err.Error() != "Complete ownership and review notes" {
		t.Fatalf("validator error = %v", err)
	}
	context.FieldPresent["customfield_10001"] = true
	if err := transition.ValidateRules(context); err != nil {
		t.Fatal(err)
	}
}

func TestChangeAssigneePostFunctions(t *testing.T) {
	transition := Transition{Actions: []Rule{
		{RuleKey: RuleChangeAssignee, Parameters: map[string]string{"type": "to-selected-user", "accountId": "usr_first"}},
		{RuleKey: RuleChangeAssignee, Parameters: map[string]string{"type": "to-current-user"}},
	}}
	assigneeID, changed, err := transition.AssigneeEffect(EvaluationContext{ActorID: "usr_actor"})
	if err != nil || !changed || assigneeID != "usr_actor" {
		t.Fatalf("effect = %q, %t, %v", assigneeID, changed, err)
	}
}

func TestContextForIssueRecognizesSystemAndCustomFields(t *testing.T) {
	issue := &models.Issue{Summary: "Ready", Description: adf.ParagraphDoc("Acceptance notes"), Assignee: &models.User{ID: "usr_owner"}, Fields: map[string]json.RawMessage{
		"customfield_empty": json.RawMessage(`[]`), "customfield_ready": json.RawMessage(`{"value":"yes"}`),
	}}
	context := ContextForIssue("usr_actor", issue)
	if !context.FieldPresent["summary"] || !context.FieldPresent["description"] || !context.FieldPresent["assignee"] || !context.FieldPresent["customfield_ready"] {
		t.Fatalf("missing field presence: %+v", context.FieldPresent)
	}
	if context.FieldPresent["customfield_empty"] || context.AssigneeID != "usr_owner" {
		t.Fatalf("unexpected context: %+v", context)
	}
}

func TestAvailableFromInProgress(t *testing.T) {
	ids := map[string]bool{}
	for _, tr := range Default().Available("st_inprogress") {
		ids[tr.ID] = true
	}
	if !ids["11"] || !ids["31"] {
		t.Fatalf("In Progress must allow To Do (11) and Done (31): %v", ids)
	}
}

func TestValidate(t *testing.T) {
	if _, ok := Default().Validate("31", "st_inprogress"); !ok {
		t.Fatal("31 must be valid from In Progress")
	}
	if _, ok := Default().Validate("31", "st_done"); ok {
		t.Fatal("31 must be invalid from Done")
	}
	if _, ok := Default().Validate("99", "st_todo"); ok {
		t.Fatal("unknown transition must be invalid")
	}
}

func TestCustomWorkflowOverridesDefault(t *testing.T) {
	wf := Workflow{ID: "x", Name: "X", Transitions: []Transition{
		{ID: "101", Name: "Confirm", From: []string{"st_todo"}, To: "st_inprogress"},
	}}
	ids := map[string]bool{}
	for _, tr := range wf.Available("st_todo") {
		ids[tr.ID] = true
	}
	if len(ids) != 1 || !ids["101"] {
		t.Fatalf("custom workflow transitions = %v", ids)
	}
	if _, ok := wf.Validate("21", "st_todo"); ok {
		t.Fatal("default transitions must not leak into custom workflow")
	}
}
