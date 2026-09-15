package apps

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectModuleFamiliesKeepTheirConditions(t *testing.T) {
	adminOnly := []json.RawMessage{json.RawMessage(`{"condition":"user_is_admin"}`)}
	browse := []json.RawMessage{json.RawMessage(`{"condition":"has_project_permission","params":{"permission":"browse_projects"}}`)}
	mine := []json.RawMessage{json.RawMessage(`{"conditions":[{"condition":"is_issue_assigned_to_current_user"},{"condition":"is_issue_unassigned"}],"type":"or"}`)}
	cases := map[string]func() (moduleWire, error){
		"admin page": func() (moduleWire, error) {
			return translateConnectAdminPage(connectAdminPageWire{Key: "controls", URL: "/controls", Name: connectNameWire{Value: "Controls"}, Conditions: adminOnly})
		},
		"issue tab panel": func() (moduleWire, error) {
			return translateConnectIssueTabPanel(connectIssueTabPanelWire{Key: "activity", URL: "/activity", Name: connectNameWire{Value: "Activity"}, Conditions: mine})
		},
		"project page": func() (moduleWire, error) {
			return translateConnectProjectPage(connectProjectPageWire{Key: "insight", URL: "/insight", IconURL: "/insight.svg", Name: connectNameWire{Value: "Insight"}, Conditions: browse})
		},
		"project admin tab": func() (moduleWire, error) {
			return translateConnectProjectAdminPage(connectProjectAdminPageWire{Key: "settings", URL: "/settings", Location: "projectgroup2", Name: connectNameWire{Value: "Settings"}, Conditions: browse})
		},
		"web item": func() (moduleWire, error) {
			return translateConnectWebItem(connectWebItemWire{connectRemoteModuleWire{Key: "shortcut", URL: "/shortcut", Location: "system.top.navigation.bar", Name: connectNameWire{Value: "Shortcut"}, Conditions: adminOnly}})
		},
	}
	for name, translate := range cases {
		wire, err := translate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(wire.Conditions) == 0 {
			t.Fatalf("%s lost its conditions", name)
		}
	}
	context := connectIssueContextWire{Key: "risk", Name: connectNameWire{Value: "Risk"}}
	context.Icon.URL = "/risk.svg"
	context.Content.Type, context.Content.Label.Value = "label", "Risk"
	context.Target.Type, context.Target.URL = "web_panel", "/risk"
	context.Conditions = mine
	if wire, err := translateConnectIssueContext(context); err != nil || len(wire.Conditions) == 0 {
		t.Fatalf("issue context = %+v, %v", wire, err)
	}
	if _, err := translateConnectWebItem(connectWebItemWire{connectRemoteModuleWire{Key: "shortcut", URL: "/shortcut", Location: "system.top.navigation.bar", Name: connectNameWire{Value: "Shortcut"},
		Conditions: []json.RawMessage{json.RawMessage(`{"condition":"has_project_permission"}`)}}}); err == nil || !strings.Contains(err.Error(), "names a permission") {
		t.Fatalf("permission condition without a permission error = %v", err)
	}
	descriptor, err := ParseDescriptor([]byte(`{"key":"connect.conditions","name":"Conditions","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"generalPages":[{"key":"page","url":"/page","name":{"value":"Page"},"conditions":[{"condition":"user_is_admin","invert":true}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Modules) != 1 || !strings.Contains(string(descriptor.Modules[0].Conditions), "user_is_admin") {
		t.Fatalf("general page conditions = %+v", descriptor.Modules)
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.conditions","name":"Conditions","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"webPanels":[{"key":"panel","url":"/panel","location":"atl.jira.view.issue.right.context","name":{"value":"Panel"},"conditions":[{"condition":"can_attach_file_to_issue"}]}]}}`)); err == nil {
		t.Fatal("accepted a web panel with an unsupported condition")
	}
}

func TestConnectPermissionAndIssueConditions(t *testing.T) {
	conditions, err := connectConditions([]json.RawMessage{
		json.RawMessage(`{"condition":"has_issue_permission","params":{"permission":"edit_issues"}}`),
		json.RawMessage(`{"conditions":[{"condition":"is_issue_reported_by_current_user"},{"condition":"has_project_permission","params":{"permission":"ADMINISTER_PROJECTS"}}],"type":"OR"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	asked := []string{}
	allow := func(permissions ...string) func(string) bool {
		return func(permission string) bool {
			asked = append(asked, permission)
			for _, allowed := range permissions {
				if allowed == permission {
					return true
				}
			}
			return false
		}
	}
	reporter := ConnectConditionFacts{LoggedIn: true, IssuePermission: allow("EDIT_ISSUES"), ProjectPermission: allow(), Issue: &ConnectIssueFacts{ReportedByCurrentUser: true}}
	if !ConnectConditionsMet(conditions, reporter) {
		t.Fatal("the reporter who can edit the work item was refused")
	}
	if asked[0] != "EDIT_ISSUES" {
		t.Fatalf("permissions asked = %v", asked)
	}
	stranger := ConnectConditionFacts{LoggedIn: true, IssuePermission: allow("EDIT_ISSUES"), ProjectPermission: allow(), Issue: &ConnectIssueFacts{}}
	if ConnectConditionsMet(conditions, stranger) {
		t.Fatal("someone who neither reported the work item nor administers the project was admitted")
	}
	projectAdmin := ConnectConditionFacts{LoggedIn: true, IssuePermission: allow("EDIT_ISSUES"), ProjectPermission: allow("ADMINISTER_PROJECTS"), Issue: &ConnectIssueFacts{}}
	if !ConnectConditionsMet(conditions, projectAdmin) {
		t.Fatal("a project administrator was refused")
	}
	// Without a work item, conditions about one do not hold.
	if ConnectConditionsMet(conditions, ConnectConditionFacts{LoggedIn: true, SiteAdmin: true}) {
		t.Fatal("issue conditions held without a work item")
	}
}
