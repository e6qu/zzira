package apps

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ConnectConditionFacts are what a Connect condition may ask about the person
// a module is shown to.
type ConnectConditionFacts struct {
	LoggedIn  bool
	SiteAdmin bool
	// ProjectPermission and IssuePermission answer whether the person holds
	// a permission in the project or on the work item the module is shown
	// with; nil when the module is shown without one.
	ProjectPermission func(permission string) bool
	IssuePermission   func(permission string) bool
	// Issue describes the work item the module is shown with, nil without one.
	Issue *ConnectIssueFacts
}

// ConnectIssueFacts are what conditions may ask about a work item.
type ConnectIssueFacts struct {
	AssignedToCurrentUser bool
	ReportedByCurrentUser bool
	Unassigned            bool
}

// connectCondition is one Connect condition, or a group of them joined by AND
// or OR.
type connectCondition struct {
	Condition  string             `json:"condition,omitempty"`
	Invert     bool               `json:"invert,omitempty"`
	Params     map[string]any     `json:"params,omitempty"`
	Conditions []connectCondition `json:"conditions,omitempty"`
	Type       string             `json:"type,omitempty"`
}

// supportedConnectConditions are the Connect conditions this site evaluates
// exactly; a module naming any other is refused when the app is installed.
var supportedConnectConditions = map[string]bool{
	"user_is_logged_in": true, "user_is_admin": true, "user_is_sysadmin": true,
	"has_project_permission": true, "has_issue_permission": true,
	"is_issue_assigned_to_current_user": true, "is_issue_reported_by_current_user": true, "is_issue_unassigned": true,
}

// connectConditions validates a module's conditions and returns them in the
// form kept with the module, or nil when there are none.
func connectConditions(raw []json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	parsed := make([]connectCondition, 0, len(raw))
	for _, item := range raw {
		var condition connectCondition
		if err := json.Unmarshal(item, &condition); err != nil {
			return nil, fmt.Errorf("conditions must be Connect condition objects")
		}
		if err := validateConnectCondition(condition, 0); err != nil {
			return nil, err
		}
		parsed = append(parsed, condition)
	}
	return json.Marshal(parsed)
}

func validateConnectCondition(condition connectCondition, depth int) error {
	if depth > 5 {
		return fmt.Errorf("conditions nest more than five deep")
	}
	if len(condition.Conditions) > 0 {
		if condition.Condition != "" {
			return fmt.Errorf("a group of conditions cannot also name a condition")
		}
		if kind := strings.ToLower(condition.Type); kind != "" && kind != "and" && kind != "or" {
			return fmt.Errorf("a group of conditions is joined by AND or OR")
		}
		for _, child := range condition.Conditions {
			if err := validateConnectCondition(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if !supportedConnectConditions[condition.Condition] {
		return fmt.Errorf("unsupported condition %q", condition.Condition)
	}
	if condition.Condition == "has_project_permission" || condition.Condition == "has_issue_permission" {
		if permission, _ := condition.Params["permission"].(string); strings.TrimSpace(permission) == "" {
			return fmt.Errorf("condition %q names a permission", condition.Condition)
		}
	}
	return nil
}

// ConnectConditionsMet reports whether a module's kept conditions hold for
// someone: every condition at the top must.
func ConnectConditionsMet(raw json.RawMessage, facts ConnectConditionFacts) bool {
	if len(raw) == 0 {
		return true
	}
	var conditions []connectCondition
	if json.Unmarshal(raw, &conditions) != nil {
		return false
	}
	for _, condition := range conditions {
		if !connectConditionMet(condition, facts) {
			return false
		}
	}
	return true
}

func connectConditionMet(condition connectCondition, facts ConnectConditionFacts) bool {
	met := false
	if len(condition.Conditions) > 0 {
		any := strings.EqualFold(condition.Type, "or")
		met = !any
		for _, child := range condition.Conditions {
			if any {
				met = met || connectConditionMet(child, facts)
			} else {
				met = met && connectConditionMet(child, facts)
			}
		}
	} else {
		switch condition.Condition {
		case "user_is_logged_in":
			met = facts.LoggedIn
		case "user_is_admin", "user_is_sysadmin":
			met = facts.SiteAdmin
		case "has_project_permission", "has_issue_permission":
			permission, _ := condition.Params["permission"].(string)
			check := facts.ProjectPermission
			if condition.Condition == "has_issue_permission" {
				check = facts.IssuePermission
			}
			met = check != nil && check(strings.ToUpper(strings.TrimSpace(permission)))
		case "is_issue_assigned_to_current_user":
			met = facts.Issue != nil && facts.Issue.AssignedToCurrentUser
		case "is_issue_reported_by_current_user":
			met = facts.Issue != nil && facts.Issue.ReportedByCurrentUser
		case "is_issue_unassigned":
			met = facts.Issue != nil && facts.Issue.Unassigned
		}
	}
	return met != condition.Invert
}
