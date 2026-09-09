package apps

import (
	"strings"
	"testing"
)

func TestParseDescriptorValidatesScopesAndModuleLocations(t *testing.T) {
	raw := []byte(`{"key":"release.notes","name":"Release notes","baseUrl":"https://apps.example.test/releases","version":"1.0.0","scopes":["write:app-storage","read:jira-work","read:app-storage"],"modules":[{"key":"release-page","type":"jira:globalPage","location":"jira.navigation","title":"Release notes","body":"Published release context."}]}`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Key != "release.notes" || len(descriptor.Modules) != 1 || len(descriptor.Scopes) != 3 || descriptor.Scopes[0] != "read:app-storage" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if _, err := ParseDescriptor([]byte(`{"key":"release.notes","name":"Release notes","baseUrl":"https://apps.example.test","version":"1","scopes":["read:app-storage"],"modules":[{"key":"page","type":"jira:globalPage","location":"jira.navigation","title":"Page","body":"Body"}]}`)); err == nil {
		t.Fatal("accepted a Jira module without read:jira-work")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"release.notes","name":"Release notes","baseUrl":"https://apps.example.test","version":"1","scopes":["read:jira-work"],"modules":[{"key":"page","type":"jira:globalPage","location":"confluence.navigation","title":"Page","body":"Body"}]}`)); err == nil {
		t.Fatal("accepted a mismatched module location")
	}
}

func TestParseDescriptorValidatesOutboundModules(t *testing.T) {
	raw := []byte(`{
  "key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test/base","version":"2",
  "scopes":["read:jira-work","manage:webhooks"],
  "modules":[],
  "lifecycle":{"installed":"/lifecycle/installed","disabled":"/lifecycle/disabled?reason=admin"},
  "webhooks":[{"key":"issue-events","url":"/hooks/issues","events":["jira:issue_created","jira:issue_updated"],"jql":"project = OPS"}],
  "scheduledTriggers":[{"key":"fast-sync","url":"/scheduled/fast","interval":"fiveMinute"},{"key":"daily-sync","url":"/scheduled/daily","interval":"day"}]
}`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Lifecycle["installed"] != "/lifecycle/installed" || len(descriptor.Webhooks) != 1 || len(descriptor.ScheduledTriggers) != 2 {
		t.Fatalf("outbound descriptor = %+v", descriptor)
	}

	cases := []string{
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":[],"modules":[],"lifecycle":{"installed":"https://other.example.test/callback"}}`,
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":[],"modules":[],"lifecycle":{"installed":"/\\evil.example/callback"}}`,
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":[],"modules":[],"webhooks":[{"key":"issues","url":"/hooks","events":["jira:issue_created"]}]}`,
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":["manage:webhooks"],"modules":[],"webhooks":[{"key":"issues","url":"/hooks","events":["jira:issue_created"],"jql":"project ="}]}`,
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":[],"modules":[],"scheduledTriggers":[{"key":"first","url":"/first","interval":"fiveMinute"},{"key":"second","url":"/second","interval":"fiveMinute"}]}`,
		`{"key":"operations.app","name":"Operations","baseUrl":"https://apps.example.test","version":"1","scopes":[],"modules":[],"scheduledTriggers":[{"key":"bad","url":"/bad","interval":"minute"}]}`,
	}
	for _, raw := range cases {
		if _, err := ParseDescriptor([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid outbound descriptor %s", raw)
		}
	}
}

func TestParseConnectDescriptorTranslatesSupportedModules(t *testing.T) {
	raw := []byte(`{
  "key":"Connect.Operations","name":"Connect operations","baseUrl":"https://connect.example.test/jira",
  "authentication":{"type":"jwt"},"scopes":["READ","write"],
  "lifecycle":{"installed":"/installed","uninstalled":"/uninstalled"},
  "modules":{
    "generalPages":[{"key":"operations","url":"/operations","name":{"value":"Operations"}}],
    "adminPages":[{"key":"site-controls","url":"/site-controls","name":{"value":"Site controls"},"weight":70,"params":{"source":"admin"}}],
    "webPanels":[{"key":"issue-risk","url":"/risk?issue={issue.key}","location":"atl.jira.view.issue.right.context","name":{"value":"Issue risk"}}],
    "contentBylineItems":[{"key":"review","url":"/review?content={content.id}","name":{"value":"Review"}}],
    "webItems":[{"key":"top-link","url":"/top","location":"system.top.navigation.bar","name":{"value":"Top link"}}],
    "jiraIssueFields":[{"key":"impact-score","name":{"value":"Impact score"},"description":{"value":"Operational impact"},"type":"number"}],
    "jiraIssueContents":[{"key":"runbook","name":{"value":"Runbook"},"tooltip":{"value":"Add runbook"},"icon":{"url":"/runbook.svg"},"target":{"type":"web_panel","url":"/runbook?issue={issue.key}"}}],
    "jiraProjectPages":[{"key":"project-release","url":"/project-release?project={project.key}","iconUrl":"/project-release.svg","weight":40,"name":{"value":"Release intelligence"}}],
    "jiraProjectAdminTabPanels":[{"key":"project-controls","url":"/project-controls?project={project.key}","location":"projectgroup3","weight":20,"params":{"source":"settings"},"name":{"value":"Project controls"}}],
    "jiraReports":[{"key":"delivery-risk","url":"/delivery-risk?project={project.key}","name":{"value":"Delivery risk"},"description":{"value":"Release and incident risk"},"reportCategory":"AGILE","thumbnailUrl":"/delivery-risk.svg"}],
    "jiraDashboardItems":[{"key":"release-health","url":"/release-health?item={dashboardItem.id}","name":{"value":"Release health"},"description":{"value":"Current release health"},"thumbnailUrl":"release-health.svg"}],
    "jiraIssueTabPanels":[{"key":"deployment-activity","url":"/deployment-activity?issue={issue.key}","name":{"value":"Deployments"},"weight":80,"params":{"source":"activity"}}],
    "jiraIssueContexts":[{"key":"delivery-context","name":{"value":"Delivery context"},"icon":{"url":"context.svg"},"content":{"type":"label","label":{"value":"3 linked deployments"}},"target":{"type":"web_panel","url":"/delivery-context?issue={issue.key}"}}],
    "jiraIssueGlances":[{"key":"legacy-glance","name":{"value":"Legacy glance"},"icon":{"url":"glance.svg"},"content":{"type":"label","label":{"value":"Legacy status"}},"target":{"type":"web_panel","url":"/legacy-glance"}}],
    "webhooks":[{"event":"jira:issue_updated","url":"/hooks/issues","filter":"project = OPS"}]
  }
}`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Format != "connect" || descriptor.Version != "connect-v1" || len(descriptor.Modules) != 13 || len(descriptor.Webhooks) != 1 || len(descriptor.IssueFields) != 1 {
		t.Fatalf("Connect descriptor = %+v", descriptor)
	}
	if descriptor.Key != "Connect.Operations" {
		t.Fatalf("Connect key = %q", descriptor.Key)
	}
	remoteModules := 0
	projectPage := false
	projectAdminPage := false
	report := false
	dashboardItem := false
	issueContext := false
	issueGlance := false
	issueTabPanel := false
	adminPage := false
	for _, module := range descriptor.Modules {
		if module.RemoteURL != "" {
			remoteModules++
		}
		if module.Key == "project-release" && module.Type == "jira:projectPage" && module.Location == "jira.project.page" && module.Position == 40 && strings.Contains(module.Body, "project-release.svg") {
			projectPage = true
		}
		if module.Key == "project-controls" && module.Type == "jira:projectAdminPage" && module.Location == "jira.project.settings" && module.Position == 3020 && strings.Contains(module.RemoteURL, "source=settings") {
			projectAdminPage = true
		}
		if module.Key == "delivery-risk" && module.Type == "jira:report" && module.Location == "jira.report" && strings.Contains(module.Body, `"category":"agile"`) {
			report = true
		}
		if module.Key == "release-health" && module.Type == "jira:dashboardGadget" && module.Location == "jira.dashboard" && strings.Contains(module.Body, "Current release health") {
			dashboardItem = true
		}
		if module.Key == "delivery-context" && module.Type == "jira:issueContext" && module.Location == "jira.issue.context" && strings.Contains(module.Body, "3 linked deployments") {
			issueContext = true
		}
		if module.Key == "legacy-glance" && module.Type == "jira:issueGlance" && module.Location == "jira.issue.context" && strings.Contains(module.Body, "Legacy status") {
			issueGlance = true
		}
		if module.Key == "deployment-activity" && module.Type == "jira:issueTabPanel" && module.Location == "jira.issue.activity" && module.Position == 80 && strings.Contains(module.RemoteURL, "issue={issue.key}") && strings.Contains(module.RemoteURL, "source=activity") {
			issueTabPanel = true
		}
		if module.Key == "site-controls" && module.Type == "jira:adminPage" && module.Location == "jira.admin" && module.Position == 70 && strings.Contains(module.RemoteURL, "source=admin") {
			adminPage = true
		}
	}
	if remoteModules != 13 || !projectPage || !projectAdminPage || !report || !dashboardItem || !issueContext || !issueGlance || !issueTabPanel || !adminPage || descriptor.Webhooks[0].Key != "connect-webhook-1" {
		t.Fatalf("translated modules = %+v, hooks = %+v", descriptor.Modules, descriptor.Webhooks)
	}
	for _, scope := range []string{"read:jira-work", "write:jira-work", "read:confluence-content", "write:confluence-content", "manage:webhooks"} {
		found := false
		for _, actual := range descriptor.Scopes {
			found = found || actual == scope
		}
		if !found {
			t.Errorf("translated scope %q missing from %v", scope, descriptor.Scopes)
		}
	}
	if descriptor.IssueFields[0].Key != "impact-score" || descriptor.IssueFields[0].Type != "number" {
		t.Fatalf("translated issue fields = %+v", descriptor.IssueFields)
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad","name":"Bad","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":[],"modules":{"jiraIssueFields":[{"key":"bad","name":{"value":"Bad"},"type":"user"}]}}`)); err == nil {
		t.Fatal("accepted an unsupported Connect issue-field type")
	}
	noScope, err := ParseDescriptor([]byte(`{"key":"connect.link","name":"Link","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":[],"modules":{"webItems":[{"key":"link","url":"/link","location":"system.header/right","name":{"value":"Link"}}]}}`))
	if err != nil || len(noScope.Modules) != 1 || noScope.Modules[0].Type != "confluence:webItem" {
		t.Fatalf("scope-free Connect web item = %+v, %v", noScope, err)
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-link","name":"Bad link","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":[],"modules":{"webItems":[{"key":"link","url":"/link","location":"admin_plugins_menu","name":{"value":"Link"}}]}}`)); err == nil {
		t.Fatal("accepted an unsupported Connect web-item location")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-content","name":"Bad content","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":[],"modules":{"jiraIssueContents":[{"key":"content","name":{"value":"Content"},"tooltip":{"value":"Add"},"icon":{"url":"/icon.svg"},"target":{"type":"web_panel","url":"/content"},"contentPresentConditions":[{"condition":"user_is_admin"}]}]}}`)); err == nil {
		t.Fatal("accepted unsupported issue-content presence conditions")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-project-page","name":"Bad project page","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraProjectPages":[{"key":"project","name":{"value":"Project"},"url":"/project","iconUrl":"/icon.svg","conditions":[{"condition":"user_is_admin"}]}]}}`)); err == nil {
		t.Fatal("accepted unsupported project-page conditions")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-project-admin","name":"Bad project admin","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraProjectAdminTabPanels":[{"key":"project","name":{"value":"Project"},"url":"/project","location":"projectgroup9"}]}}`)); err == nil {
		t.Fatal("accepted unsupported project-admin location")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-report","name":"Bad report","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraReports":[{"key":"report","name":{"value":"Report"},"description":{"value":"Report"},"url":"/report","reportCategory":"finance"}]}}`)); err == nil {
		t.Fatal("accepted unsupported report category")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-dashboard","name":"Bad dashboard","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraDashboardItems":[{"key":"item","name":{"value":"Item"},"description":{"value":"Item"},"url":"/item","thumbnailUrl":"/item.svg","configurable":true}]}}`)); err == nil {
		t.Fatal("accepted unsupported configurable dashboard item")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-context","name":"Bad context","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraIssueContexts":[{"key":"context","name":{"value":"Context"},"icon":{"url":"/context.svg"},"content":{"type":"label","label":{"value":"Context"}},"target":{"type":"web_panel","url":"/context"},"conditions":[{"condition":"user_is_logged_in"}]}]}}`)); err == nil {
		t.Fatal("accepted unsupported issue context conditions")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-tab","name":"Bad tab","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraIssueTabPanels":[{"key":"activity","name":{"value":"Activity"},"url":"https://outside.example.test/activity"}]}}`)); err == nil {
		t.Fatal("accepted an absolute issue-tab URL")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-tab","name":"Bad tab","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraIssueTabPanels":[{"key":"activity","name":{"value":"Activity"},"url":"/activity","conditions":[{"condition":"user_is_logged_in"}]}]}}`)); err == nil {
		t.Fatal("accepted unsupported issue-tab conditions")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad-admin","name":"Bad admin","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"adminPages":[{"key":"admin","name":{"value":"Admin"},"url":"/admin","fullPage":true}]}}`)); err == nil {
		t.Fatal("accepted unsupported full-page admin behavior")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad","name":"Bad","baseUrl":"https://connect.example.test","authentication":{"type":"none"},"scopes":[],"modules":{}}`)); err == nil {
		t.Fatal("accepted a Connect descriptor without JWT authentication")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"Connect_Default_JWT","name":"Default JWT","baseUrl":"https://connect.example.test","authentication":{},"scopes":[],"modules":{}}`)); err != nil {
		t.Fatalf("Connect descriptor with default JWT authentication: %v", err)
	}
}

func TestParseConnectDescriptorValidatesJQLFunctions(t *testing.T) {
	raw := []byte(`{
  "key":"connect.search","name":"Search functions","baseUrl":"https://connect.example.test/base",
  "authentication":{"type":"jwt"},"scopes":["READ"],
  "modules":{"jiraJqlFunctions":[{
    "key":"risk-issues","name":"riskIssues","url":"/jql/risk",
    "arguments":[{"name":"level","required":true},{"name":"team","required":false}],
    "types":["issue"],"operators":["in","not_in"]
  }]}
}`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.JQLFunctions) != 1 {
		t.Fatalf("JQL functions = %+v", descriptor.JQLFunctions)
	}
	function := descriptor.JQLFunctions[0]
	if function.Key != "risk-issues" || function.Name != "riskIssues" || function.Path != "/jql/risk" || len(function.Arguments) != 2 || len(function.Types) != 1 || len(function.Operators) != 2 {
		t.Fatalf("JQL function = %+v", function)
	}
	cases := []string{
		`{"key":"connect.search","name":"Search","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraJqlFunctions":[{"key":"risk","name":"currentUser","url":"/jql","arguments":[],"types":["user"],"operators":["="]}]}}`,
		`{"key":"connect.search","name":"Search","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraJqlFunctions":[{"key":"risk","name":"riskIssues","url":"https://outside.test/jql","arguments":[],"types":["issue"],"operators":["in"]}]}}`,
		`{"key":"connect.search","name":"Search","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraJqlFunctions":[{"key":"risk","name":"riskIssues","url":"/jql","arguments":[{"name":"same","required":false},{"name":"same","required":true}],"types":["issue"],"operators":["in"]}]}}`,
		`{"key":"connect.search","name":"Search","baseUrl":"https://connect.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraJqlFunctions":[{"key":"risk","name":"riskIssues","url":"/jql","arguments":[],"types":["issue"],"operators":["contains"]}]}}`,
	}
	for _, input := range cases {
		if _, err := ParseDescriptor([]byte(input)); err == nil {
			t.Fatalf("accepted invalid JQL function descriptor: %s", input)
		}
	}
}

func TestTranslateConnectIssueTabPanelDefaultsWeight(t *testing.T) {
	module, err := translateConnectIssueTabPanel(connectIssueTabPanelWire{
		Key: strings.Repeat("a", 100), URL: "/deployments?issue={issue.key}", Name: connectNameWire{Value: "Deployments"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if module.Type != "jira:issueTabPanel" || module.Location != "jira.issue.activity" || module.Position != 100 {
		t.Fatalf("translated issue tab = %+v", module)
	}
	if _, err := translateConnectIssueTabPanel(connectIssueTabPanelWire{Key: "bad_key", URL: "/deployments", Name: connectNameWire{Value: "Deployments"}}); err == nil {
		t.Fatal("accepted an issue-tab key outside the Connect key pattern")
	}
}
