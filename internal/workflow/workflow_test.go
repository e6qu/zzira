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

func TestPreviousStatusRulesUseOrderedHistory(t *testing.T) {
	context := EvaluationContext{StatusHistory: []string{"st_todo", "st_inprogress"}, CurrentStatus: "st_done"}
	condition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
		RuleKey: RulePreviousStatusCondition, Parameters: map[string]string{"previousStatusIds": "st_inprogress", "mostRecentStatusOnly": "true"},
	}}}}
	if !condition.ConditionsAllow(context) {
		t.Fatal("most recent previous status did not match")
	}
	condition.Conditions.Conditions[0].Parameters["not"] = "true"
	if condition.ConditionsAllow(context) {
		t.Fatal("negated previous status condition matched")
	}
	condition.Conditions.Conditions[0].Parameters = map[string]string{"previousStatusIds": "st_done", "mostRecentStatusOnly": "true", "includeCurrentStatus": "true"}
	if !condition.ConditionsAllow(context) {
		t.Fatal("current status was not included")
	}
	validator := Transition{Validators: []Rule{{RuleKey: RulePreviousStatusValidator, Parameters: map[string]string{"previousStatusIds": "st_todo"}}}}
	if err := validator.ValidateRules(context); err != nil {
		t.Fatal(err)
	}
	validator.Validators[0].Parameters["previousStatusIds"] = "st_missing"
	if err := validator.ValidateRules(context); err == nil {
		t.Fatal("missing previous status passed validation")
	}
}

func TestPreviousStatusConfigurationValidation(t *testing.T) {
	condition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{ID: "history", RuleKey: RulePreviousStatusCondition, Parameters: map[string]string{
		"previousStatusIds": "st_todo", "mostRecentStatusOnly": "false", "includeCurrentStatus": "false", "not": "false", "ignoreLoopTransitions": "true",
	}}}}}
	if err := ValidateTransitionRules(condition); err != nil {
		t.Fatal(err)
	}
	condition.Conditions.Conditions[0].Parameters["mostRecentStatusOnly"] = "sometimes"
	if err := ValidateTransitionRules(condition); err == nil {
		t.Fatal("invalid previous-status boolean was accepted")
	}
	validator := Transition{Validators: []Rule{{ID: "history-validator", RuleKey: RulePreviousStatusValidator, Parameters: map[string]string{"previousStatusIds": "st_todo,st_done"}}}}
	if err := ValidateTransitionRules(validator); err == nil {
		t.Fatal("multiple previous status ids were accepted")
	}
}

func TestSeparationOfDutiesUsesTransitionActors(t *testing.T) {
	transition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{ID: "separation", RuleKey: RuleSeparationOfDuties, Parameters: map[string]string{
		"fromStatusId": "st_todo", "toStatusId": "st_inprogress",
	}}}}}
	context := EvaluationContext{ActorID: "usr_reviewer", Transitions: []TransitionHistory{{FromStatusID: "st_todo", ToStatusID: "st_inprogress", ActorID: "usr_builder"}}}
	if !transition.ConditionsAllow(context) {
		t.Fatal("a different actor was blocked")
	}
	context.ActorID = "usr_builder"
	if transition.ConditionsAllow(context) {
		t.Fatal("the prior transition actor was allowed")
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Conditions.Conditions[0].Parameters["toStatusId"] = ""
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("incomplete separation-of-duties configuration was accepted")
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

func TestChangedFieldValidatorRequiresTransitionInput(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "changed", RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "fieldChanged", "fieldKey": "labels", "errorMessage": "Update labels during transition",
	}}}}
	if err := transition.ValidateRules(EvaluationContext{ChangedFields: map[string]bool{"summary": true}}); err == nil || err.Error() != "Update labels during transition" {
		t.Fatalf("validator error = %v", err)
	}
	if err := transition.ValidateRules(EvaluationContext{ChangedFields: map[string]bool{"labels": true}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["groupsExemptFromValidation"] = "group-1"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unimplemented group exemption was accepted")
	}
}

func TestRegularExpressionValidatorUsesEffectiveFieldValue(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "regexp", RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "fieldMatchesRegularExpression", "fieldKey": "summary", "regexp": `^REL-[0-9]+$`, "errorMessage": "Add a release reference",
	}}}}
	if err := transition.ValidateRules(EvaluationContext{FieldValues: map[string]json.RawMessage{"summary": json.RawMessage(`"Draft"`)}}); err == nil || err.Error() != "Add a release reference" {
		t.Fatalf("validator error = %v", err)
	}
	if err := transition.ValidateRules(EvaluationContext{FieldValues: map[string]json.RawMessage{"summary": json.RawMessage(`"REL-42"`)}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["regexp"] = "["
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("invalid regular expression was accepted")
	}
}

func TestSingleValueValidatorCountsEffectiveFieldValues(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "single", RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "fieldHasSingleValue", "fieldKey": "labels", "excludeSubtasks": "false",
	}}}}
	for name, raw := range map[string]json.RawMessage{
		"empty": json.RawMessage(`[]`), "many": json.RawMessage(`["one","two"]`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := transition.ValidateRules(EvaluationContext{FieldValues: map[string]json.RawMessage{"labels": raw}}); err == nil {
				t.Fatal("value count passed validation")
			}
		})
	}
	if err := transition.ValidateRules(EvaluationContext{FieldValues: map[string]json.RawMessage{"labels": json.RawMessage(`["one"]`)}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["excludeSubtasks"] = "sometimes"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("invalid excludeSubtasks value was accepted")
	}
}

func TestDateFieldValidatorComparesEffectiveDates(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "dates", RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "dateFieldComparison", "date1FieldKey": "customfield_start", "date2FieldKey": "customfield_due",
		"includeTime": "false", "conditionSelected": "<",
	}}}}
	context := EvaluationContext{FieldValues: map[string]json.RawMessage{
		"customfield_start": json.RawMessage(`"2026-09-06T23:00:00Z"`),
		"customfield_due":   json.RawMessage(`"2026-09-07T01:00:00Z"`),
	}}
	if err := transition.ValidateRules(context); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["includeTime"] = "true"
	transition.Validators[0].Parameters["conditionSelected"] = ">"
	if err := transition.ValidateRules(context); err == nil {
		t.Fatal("reversed time comparison passed")
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["includeTime"] = "sometimes"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("invalid includeTime value was accepted")
	}
}

func TestDateWindowValidatorLimitsDaysPastReference(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "window", RuleKey: RuleValidateFieldValue, Parameters: map[string]string{
		"ruleType": "windowDateComparison", "date1FieldKey": "customfield_target", "date2FieldKey": "customfield_baseline", "numberOfDays": "3",
	}}}}
	context := EvaluationContext{FieldValues: map[string]json.RawMessage{
		"customfield_target": json.RawMessage(`"2026-09-09"`), "customfield_baseline": json.RawMessage(`"2026-09-06"`),
	}}
	if err := transition.ValidateRules(context); err != nil {
		t.Fatal(err)
	}
	context.FieldValues["customfield_target"] = json.RawMessage(`"2026-09-10"`)
	if err := transition.ValidateRules(context); err == nil {
		t.Fatal("date beyond the configured window passed")
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["numberOfDays"] = "-1"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("negative date window was accepted")
	}
}

func TestParentAndChildBlockingRules(t *testing.T) {
	condition := Transition{Conditions: &ConditionGroup{Operation: "ALL", Conditions: []Rule{{
		ID: "children", RuleKey: RuleParentChildCondition,
		Parameters: map[string]string{"blocker": "CHILD", "statusIds": "st_todo,st_inprogress"},
	}}}}
	if condition.ConditionsAllow(EvaluationContext{ChildStatuses: []string{"st_done", "st_todo"}}) {
		t.Fatal("a child in a blocked status exposed the transition")
	}
	if !condition.ConditionsAllow(EvaluationContext{ChildStatuses: []string{"st_done"}}) {
		t.Fatal("completed children blocked the transition")
	}
	if err := ValidateTransitionRules(condition); err != nil {
		t.Fatal(err)
	}

	validator := Transition{Validators: []Rule{{
		ID: "parent", RuleKey: RuleParentChildValidator,
		Parameters: map[string]string{"blocker": "PARENT", "statusIds": "st_todo"},
	}}}
	if err := validator.ValidateRules(EvaluationContext{ParentStatus: "st_todo"}); err == nil {
		t.Fatal("a parent in the blocked status passed validation")
	}
	if err := validator.ValidateRules(EvaluationContext{ParentStatus: "st_inprogress"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransitionRules(validator); err != nil {
		t.Fatal(err)
	}
	validator.Validators[0].Parameters["blocker"] = "CHILD"
	if err := ValidateTransitionRules(validator); err == nil {
		t.Fatal("an invalid parent blocker configuration was accepted")
	}
}

func TestPermissionValidatorUsesGrantedJiraPermissions(t *testing.T) {
	transition := Transition{Validators: []Rule{{ID: "permission", RuleKey: RuleCheckPermissionValidator, Parameters: map[string]string{
		"permissionKey": "ADMINISTER_PROJECTS",
	}}}}
	if err := transition.ValidateRules(EvaluationContext{Permissions: map[string]bool{"EDIT_ISSUES": true}}); err == nil || err.Error() != "permission ADMINISTER_PROJECTS is required to perform this transition" {
		t.Fatalf("validator error = %v", err)
	}
	if err := transition.ValidateRules(EvaluationContext{Permissions: map[string]bool{"ADMINISTER_PROJECTS": true}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Validators[0].Parameters["permissionKey"] = "UNKNOWN_PERMISSION"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unknown Jira permission was accepted")
	}
}

func TestChangeAssigneePostFunctions(t *testing.T) {
	transition := Transition{Actions: []Rule{
		{RuleKey: RuleChangeAssignee, Parameters: map[string]string{"type": "to-selected-user", "accountId": "usr_first"}},
		{RuleKey: RuleUpdateField, Parameters: map[string]string{"field": "labels", "value": "released", "mode": "append"}},
		{RuleKey: RuleCopyFieldValue, Parameters: map[string]string{"sourceFieldKey": "summary", "targetFieldKey": "description", "issueSource": "SAME"}},
		{RuleKey: RuleChangeAssignee, Parameters: map[string]string{"type": "to-current-user"}},
	}}
	assigneeID, changed, err := transition.AssigneeEffect(EvaluationContext{ActorID: "usr_actor"})
	if err != nil || !changed || assigneeID != "usr_actor" {
		t.Fatalf("effect = %q, %t, %v", assigneeID, changed, err)
	}
	updates, err := transition.FieldUpdateEffects()
	if err != nil || len(updates) != 2 || updates[0].Field != "labels" || updates[0].Value != "released" || updates[1].SourceField != "summary" || updates[1].Field != "description" || updates[1].IssueSource != "SAME" {
		t.Fatalf("field effects = %+v, %v", updates, err)
	}
}

func TestCopyFieldPostFunctionValidation(t *testing.T) {
	transition := Transition{Actions: []Rule{{ID: "copy", RuleKey: RuleCopyFieldValue, Parameters: map[string]string{
		"sourceFieldKey": "summary", "targetFieldKey": "description", "issueSource": "SAME",
	}}}}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Actions[0].Parameters["issueSource"] = "PARENT"
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	effects, err := transition.FieldUpdateEffects()
	if err != nil || len(effects) != 1 || effects[0].IssueSource != "PARENT" {
		t.Fatalf("parent copy effect = %+v, %v", effects, err)
	}
	transition.Actions[0].Parameters["issueSource"] = "CHILD"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unsupported child issue source was accepted")
	}
	transition.Actions[0].Parameters["issueSource"] = "SAME"
	transition.Actions[0].Parameters["targetFieldKey"] = "status"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("read-only copy target was accepted")
	}
}

func TestTriggerWebhookPostFunctionValidationAndOrdering(t *testing.T) {
	transition := Transition{Actions: []Rule{
		{ID: "first", RuleKey: RuleTriggerWebhook, Parameters: map[string]string{"webhookId": "wh_first"}},
		{ID: "field", RuleKey: RuleUpdateField, Parameters: map[string]string{"field": "labels", "value": "sent", "mode": "append"}},
		{ID: "duplicate", RuleKey: RuleTriggerWebhook, Parameters: map[string]string{"webhookId": "wh_first"}},
		{ID: "second", RuleKey: RuleTriggerWebhook, Parameters: map[string]string{"webhookId": "wh_second"}},
	}}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	ids, err := transition.TriggerWebhookIDs()
	if err != nil || len(ids) != 2 || ids[0] != "wh_first" || ids[1] != "wh_second" {
		t.Fatalf("webhook effects = %v, %v", ids, err)
	}
	if _, _, err := transition.AssigneeEffect(EvaluationContext{}); err != nil {
		t.Fatal(err)
	}
	if effects, err := transition.FieldUpdateEffects(); err != nil || len(effects) != 1 {
		t.Fatalf("field effects = %+v, %v", effects, err)
	}
	transition.Actions[0].Parameters["webhookId"] = " "
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("empty webhook registration was accepted")
	}
}

func TestUpdateFieldPostFunctionValidation(t *testing.T) {
	transition := Transition{Actions: []Rule{{ID: "update", RuleKey: RuleUpdateField, Parameters: map[string]string{"field": "labels", "value": "released", "mode": "replace"}}}}
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatal(err)
	}
	transition.Actions[0].Parameters["field"] = "components"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unsupported update-field target was accepted")
	}
	transition.Actions[0].Parameters["field"] = "customfield_10001"
	if err := ValidateTransitionRules(transition); err != nil {
		t.Fatalf("custom field target was rejected: %v", err)
	}
	transition.Actions[0].Parameters["field"] = "labels"
	transition.Actions[0].Parameters["mode"] = "merge"
	if err := ValidateTransitionRules(transition); err == nil {
		t.Fatal("unsupported update-field mode was accepted")
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
