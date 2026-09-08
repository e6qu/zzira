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
