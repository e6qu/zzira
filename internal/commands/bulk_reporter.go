package commands

import (
	"context"
	"slices"

	"github.com/e6qu/zzira/internal/models"
)

// BulkReporterField describes a Reporter edit only when every selected item
// permits it. Reporter-based permission grants require checking each item,
// rather than just its project's creation metadata.
func (s *Service) BulkReporterField(ctx context.Context, workspaceID, actorID string, issues []*models.Issue) (*models.CreateFieldMeta, error) {
	if len(issues) == 0 {
		return nil, nil
	}
	field := &models.CreateFieldMeta{ID: "reporter", Name: "Reporter", Type: "user"}
	type configuration struct {
		available bool
		required  bool
	}
	configurations := map[[2]string]configuration{}
	for _, issue := range issues {
		if issue == nil || issue.WorkspaceID != workspaceID {
			return nil, nil
		}
		for _, permission := range []string{"EDIT_ISSUES", "MODIFY_REPORTER"} {
			allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, permission)
			if err != nil {
				return nil, err
			}
			if !allowed {
				return nil, nil
			}
		}
		editable, err := s.IssueEditable(ctx, issue)
		if err != nil {
			return nil, err
		}
		if !editable {
			return nil, nil
		}
		key := [2]string{issue.ProjectID, issue.IssueType.ID}
		config, found := configurations[key]
		if !found {
			screen, err := s.Store.ResolveScreenFields(ctx, workspaceID, issue.ProjectID, issue.IssueType.ID, "edit")
			if err != nil {
				return nil, err
			}
			behaviour, err := s.Store.ResolveFieldBehaviour(ctx, workspaceID, issue.ProjectID, issue.IssueType.ID)
			if err != nil {
				return nil, err
			}
			config = configuration{available: !behaviour["reporter"].IsHidden && (screen == nil || slices.Contains(screen, "reporter")), required: behaviour["reporter"].IsRequired}
			configurations[key] = config
		}
		if !config.available {
			return nil, nil
		}
		field.Required = field.Required || config.required
	}
	members, err := s.Store.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		field.Options = append(field.Options, models.CreateFieldOption{ID: member.ID, Name: member.DisplayName})
	}
	return field, nil
}
