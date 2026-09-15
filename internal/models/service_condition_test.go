package models

import "testing"

func TestServiceRequestTypeFieldConditions(t *testing.T) {
	always := ServiceRequestTypeField{ID: "customfield_2"}
	conditional := ServiceRequestTypeField{ID: "customfield_3", ConditionFieldID: "customfield_1", ConditionOptionIDs: []string{"10", "12"}}
	for _, check := range []struct {
		chosen map[string][]string
		want   bool
	}{
		{map[string][]string{}, false},
		{map[string][]string{"customfield_1": {"11"}}, false},
		{map[string][]string{"customfield_1": {"11", "12"}}, true},
		{map[string][]string{"customfield_4": {"10"}}, false},
	} {
		if got := conditional.ShownFor(check.chosen); got != check.want {
			t.Fatalf("ShownFor(%v) = %v", check.chosen, got)
		}
		if !always.ShownFor(check.chosen) {
			t.Fatal("an unconditional field was hidden")
		}
	}
	if conditional.ConditionOptions() != "10 12" || !conditional.HasConditionOption("12") || conditional.HasConditionOption("11") {
		t.Fatalf("condition options = %q", conditional.ConditionOptions())
	}
}
