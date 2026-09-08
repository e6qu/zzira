package apps

import "testing"

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
    "webPanels":[{"key":"issue-risk","url":"/risk?issue={issue.key}","location":"atl.jira.view.issue.right.context","name":{"value":"Issue risk"}}],
    "contentBylineItems":[{"key":"review","url":"/review?content={content.id}","name":{"value":"Review"}}],
    "webItems":[{"key":"top-link","url":"/top","location":"system.top.navigation.bar","name":{"value":"Top link"}}],
    "jiraIssueFields":[{"key":"impact-score","name":{"value":"Impact score"},"description":{"value":"Operational impact"},"type":"number"}],
    "jiraIssueContents":[{"key":"runbook","name":{"value":"Runbook"},"tooltip":{"value":"Add runbook"},"icon":{"url":"/runbook.svg"},"target":{"type":"web_panel","url":"/runbook?issue={issue.key}"}}],
    "webhooks":[{"event":"jira:issue_updated","url":"/hooks/issues","filter":"project = OPS"}]
  }
}`)
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Format != "connect" || descriptor.Version != "connect-v1" || len(descriptor.Modules) != 5 || len(descriptor.Webhooks) != 1 || len(descriptor.IssueFields) != 1 {
		t.Fatalf("Connect descriptor = %+v", descriptor)
	}
	if descriptor.Key != "Connect.Operations" {
		t.Fatalf("Connect key = %q", descriptor.Key)
	}
	remoteModules := 0
	for _, module := range descriptor.Modules {
		if module.RemoteURL != "" {
			remoteModules++
		}
	}
	if remoteModules != 5 || descriptor.Webhooks[0].Key != "connect-webhook-1" {
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
	if _, err := ParseDescriptor([]byte(`{"key":"connect.bad","name":"Bad","baseUrl":"https://connect.example.test","authentication":{"type":"none"},"scopes":[],"modules":{}}`)); err == nil {
		t.Fatal("accepted a Connect descriptor without JWT authentication")
	}
	if _, err := ParseDescriptor([]byte(`{"key":"Connect_Default_JWT","name":"Default JWT","baseUrl":"https://connect.example.test","authentication":{},"scopes":[],"modules":{}}`)); err != nil {
		t.Fatalf("Connect descriptor with default JWT authentication: %v", err)
	}
}
