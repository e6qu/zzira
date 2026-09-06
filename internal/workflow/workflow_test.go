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
