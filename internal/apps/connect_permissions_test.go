package apps

import (
	"strings"
	"testing"
)

func TestConnectPermissionModules(t *testing.T) {
	descriptor, err := ParseDescriptor([]byte(`{"key":"insights","name":"Insights","baseUrl":"https://insights.example.test","authentication":{"type":"jwt"},"scopes":["READ"],
		"modules":{
		  "jiraProjectPermissions":[{"key":"approve-release","name":{"value":"Approve releases"},"description":{"value":"Approve a release."},"category":"projects"},
		    {"key":"triage","name":{"value":"Triage"},"description":{"value":"Triage work."}}],
		  "jiraGlobalPermissions":[{"key":"view-insights","name":{"value":"View insights"},"description":{"value":"See insights."},"anonymousAllowed":false,"defaultGrants":["all"]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Permissions) != 3 {
		t.Fatalf("permissions = %+v", descriptor.Permissions)
	}
	byKey := map[string]int{}
	for index, permission := range descriptor.Permissions {
		byKey[permission.Key] = index
	}
	approve, triage, view := descriptor.Permissions[byKey["approve-release"]], descriptor.Permissions[byKey["triage"]], descriptor.Permissions[byKey["view-insights"]]
	if approve.Type != "PROJECT" || approve.Category != "projects" || triage.Category != "other" {
		t.Fatalf("project permissions = %+v %+v", approve, triage)
	}
	if view.Type != "GLOBAL" || view.AnonymousAllowed || len(view.DefaultGrants) != 1 || view.DefaultGrants[0] != "ALL" {
		t.Fatalf("global permission = %+v", view)
	}

	for name, modules := range map[string]string{
		"key":         `"jiraProjectPermissions":[{"key":"bad key","name":{"value":"A"},"description":{"value":"B"}}]`,
		"category":    `"jiraProjectPermissions":[{"key":"a","name":{"value":"A"},"description":{"value":"B"},"category":"security"}]`,
		"description": `"jiraProjectPermissions":[{"key":"a","name":{"value":"A"}}]`,
		"grant":       `"jiraGlobalPermissions":[{"key":"a","name":{"value":"A"},"description":{"value":"B"},"defaultGrants":["SOME"]}]`,
		"duplicate":   `"jiraProjectPermissions":[{"key":"a","name":{"value":"A"},"description":{"value":"B"}}],"jiraGlobalPermissions":[{"key":"a","name":{"value":"A"},"description":{"value":"B"}}]`,
	} {
		_, err := ParseDescriptor([]byte(`{"key":"insights","name":"Insights","baseUrl":"https://insights.example.test","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{` + modules + `}}`))
		if err == nil || !strings.Contains(err.Error(), "permission") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
