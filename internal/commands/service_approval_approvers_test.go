package commands

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestStatusApprovalApprovers(t *testing.T) {
	members := func(groupID string) ([]string, error) {
		return map[string][]string{"security": {"usr_ana", "usr_bea"}, "operations": {"usr_bea", "usr_cy"}}[groupID], nil
	}
	people, groups, err := statusApprovalApprovers(models.CustomFieldMultiUser, json.RawMessage(`["usr_ana","usr_dan","usr_ana"]`), members, map[string]bool{"usr_dan": true})
	if err != nil || !slices.Equal(people, []string{"usr_ana"}) || len(groups) != 0 {
		t.Fatalf("user picker approvers = %v %v %v", people, groups, err)
	}
	people, groups, err = statusApprovalApprovers(models.CustomFieldMultiGroup, json.RawMessage(`["security","operations"]`), members, map[string]bool{"usr_cy": true})
	if err != nil || !slices.Equal(people, []string{"usr_ana", "usr_bea"}) {
		t.Fatalf("group approvers = %v %v", people, err)
	}
	if !slices.Equal(groups["usr_bea"], []string{"security", "operations"}) || !slices.Equal(groups["usr_ana"], []string{"security"}) || groups["usr_cy"] != nil {
		t.Fatalf("approver groups = %v", groups)
	}
}
