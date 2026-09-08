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
