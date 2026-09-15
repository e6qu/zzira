package store

import "testing"

func TestServiceApprovalDecisions(t *testing.T) {
	for _, check := range []struct {
		name                             string
		approved, declined, total, value int
		kind, want                       string
	}{
		{"everyone without a condition", 2, 0, 3, 0, "", "pending"},
		{"everyone approved", 3, 0, 3, 0, "", "approved"},
		{"a number", 2, 0, 3, 2, "number", "approved"},
		{"more than there are approvers", 2, 0, 2, 5, "number", "approved"},
		{"a percentage rounds up", 1, 0, 3, 50, "percent", "pending"},
		{"any decline", 2, 1, 3, 1, "number", "declined"},
	} {
		if got := serviceApprovalDecision(check.approved, check.declined, check.total, check.kind, check.value); got != check.want {
			t.Fatalf("%s: decision = %s, want %s", check.name, got, check.want)
		}
	}
	groups := []serviceApprovalGroupTally{{Approved: 1, Total: 2}, {Approved: 0, Total: 1}}
	if got := serviceApprovalGroupDecision(0, groups, 1); got != "pending" {
		t.Fatalf("a group without an approval decided %s", got)
	}
	groups[1].Approved = 1
	if got := serviceApprovalGroupDecision(0, groups, 1); got != "approved" {
		t.Fatalf("one approval per group decided %s", got)
	}
	if got := serviceApprovalGroupDecision(0, groups, 2); got != "pending" {
		t.Fatalf("two approvals per group decided %s with one from the larger group", got)
	}
	groups[0].Approved = 2
	if got := serviceApprovalGroupDecision(0, groups, 2); got != "approved" {
		t.Fatalf("a group smaller than the number needs all its members, decided %s", got)
	}
	if got := serviceApprovalGroupDecision(1, groups, 1); got != "declined" {
		t.Fatalf("a decline decided %s", got)
	}
}
