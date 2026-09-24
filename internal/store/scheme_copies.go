package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

// Jira's scheme pages all offer the same action: copy this one, so a change
// can be tried on a copy before any project uses it. A copy carries the
// configuration and nothing else: it is assigned to no project, and it is
// never the site default.

// copyName is the name Jira gives a copy: "Copy of X", then "Copy 2 of X" and
// so on while that name is taken.
func copyName(original string, taken func(string) bool) string {
	name := "Copy of " + original
	for count := 2; taken(name); count++ {
		name = fmt.Sprintf("Copy %d of %s", count, original)
	}
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

// nameTaken reports whether a table already holds a name in this workspace.
func (s *Store) nameTaken(ctx context.Context, table, workspaceID string) func(string) bool {
	return func(name string) bool {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE workspace_id=$1 AND lower(name)=lower($2))`, table)
		if err := s.Pool.QueryRow(ctx, query, workspaceID, name).Scan(&exists); err != nil {
			return false
		}
		return exists
	}
}

// CopyPermissionScheme copies a scheme with its grants.
func (s *Store) CopyPermissionScheme(ctx context.Context, workspaceID, actorID string, schemeID int64) (*models.PermissionScheme, error) {
	original, err := s.PermissionScheme(ctx, workspaceID, schemeID, true)
	if err != nil {
		return nil, err
	}
	grants := make([]PermissionGrantInput, 0, len(original.Grants))
	for _, grant := range original.Grants {
		grants = append(grants, PermissionGrantInput{
			Permission: grant.Permission, HolderType: grant.HolderType,
			HolderParameter: grant.HolderParameter, HolderValue: grant.HolderValue,
		})
	}
	return s.CreatePermissionScheme(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "permission_schemes", workspaceID)), original.Description, grants)
}

// CopyWorkflowScheme copies a scheme with the workflow each work type uses.
// The copy takes the published mappings, never a draft's: a draft is a
// change someone is still making, and a copy is made to try a change against
// what the site runs today.
func (s *Store) CopyWorkflowScheme(ctx context.Context, workspaceID, actorID, schemeID string) (workflow.Scheme, error) {
	original, err := s.WorkflowSchemeByID(ctx, workspaceID, schemeID, false)
	if err != nil {
		return workflow.Scheme{}, err
	}
	mappings := make(map[string]string, len(original.IssueTypeMappings))
	for issueTypeID, workflowID := range original.IssueTypeMappings {
		mappings[issueTypeID] = workflowID
	}
	return s.CreateWorkflowScheme(ctx, workspaceID, actorID, workflow.Scheme{
		Name:              copyName(original.Name, s.nameTaken(ctx, "workflow_schemes", workspaceID)),
		Description:       original.Description,
		DefaultWorkflowID: original.DefaultWorkflowID,
		IssueTypeMappings: mappings,
	})
}

// CopyNotificationScheme copies a scheme with who it notifies of what.
func (s *Store) CopyNotificationScheme(ctx context.Context, workspaceID, actorID string, schemeID int64) (*models.NotificationScheme, error) {
	original, err := s.NotificationScheme(ctx, workspaceID, schemeID, true)
	if err != nil {
		return nil, err
	}
	entries := []NotificationEntryInput{}
	for _, event := range original.Events {
		for _, entry := range event.Notifications {
			entries = append(entries, NotificationEntryInput{
				EventID: event.EventID, NotificationType: entry.NotificationType, Parameter: entry.Parameter,
			})
		}
	}
	return s.CreateNotificationScheme(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "notification_schemes", workspaceID)), original.Description, entries)
}

// CopyIssueSecurityScheme copies a scheme with its levels and their members.
func (s *Store) CopyIssueSecurityScheme(ctx context.Context, workspaceID, actorID, schemeID string) (*models.SecurityScheme, error) {
	original, err := s.IssueSecurityScheme(ctx, workspaceID, schemeID, true)
	if err != nil {
		return nil, err
	}
	levels := make([]SecurityLevelInput, 0, len(original.Levels))
	for _, level := range original.Levels {
		members := make([]SecurityLevelMemberInput, 0, len(level.Grants))
		for _, member := range level.Grants {
			parameter := member.HolderValue
			if parameter == "" {
				parameter = member.HolderParameter
			}
			members = append(members, SecurityLevelMemberInput{Type: member.HolderType, Parameter: parameter})
		}
		levels = append(levels, SecurityLevelInput{
			Name: level.Name, Description: level.Description,
			Default: level.ID == original.DefaultLevelID, Members: members,
		})
	}
	return s.CreateIssueSecurityScheme(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "security_schemes", workspaceID)), original.Description, levels)
}

// CopyScreen copies a screen with its tabs and the fields on them, in order.
func (s *Store) CopyScreen(ctx context.Context, workspaceID, actorID, screenID string) (*models.Screen, error) {
	original, err := s.Screen(ctx, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	tabs, err := s.ScreenTabs(ctx, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	copied, err := s.CreateScreen(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "screens", workspaceID)), original.Description)
	if err != nil {
		return nil, err
	}
	// A new screen starts with one tab; the first of the original's tabs takes
	// its place and the rest are added.
	existing, err := s.ScreenTabs(ctx, workspaceID, copied.ID)
	if err != nil {
		return nil, err
	}
	for index, tab := range tabs {
		target := ""
		if index < len(existing) {
			target = existing[index].ID
			if existing[index].Name != tab.Name {
				if _, err := s.RenameScreenTab(ctx, workspaceID, actorID, copied.ID, target, tab.Name); err != nil {
					return nil, err
				}
			}
		} else {
			added, err := s.AddScreenTab(ctx, workspaceID, actorID, copied.ID, tab.Name)
			if err != nil {
				return nil, err
			}
			target = added.ID
		}
		fields, err := s.ScreenTabFields(ctx, workspaceID, screenID, tab.ID)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			if _, err := s.AddScreenTabField(ctx, workspaceID, actorID, copied.ID, target, field.ID); err != nil {
				return nil, err
			}
		}
	}
	return s.Screen(ctx, workspaceID, copied.ID)
}

// CopyScreenScheme copies a scheme with the screen it uses for each operation.
func (s *Store) CopyScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string) (*models.ScreenScheme, error) {
	schemes, err := s.ScreenSchemes(ctx, workspaceID, []string{schemeID})
	if err != nil {
		return nil, err
	}
	if len(schemes) == 0 {
		return nil, ErrScreenNotFound
	}
	original := schemes[0]
	screens := map[string]string{}
	for operation, screen := range original.Screens {
		screens[operation] = screen
	}
	return s.CreateScreenScheme(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "screen_schemes", workspaceID)), original.Description, screens)
}

// CopyIssueTypeScreenScheme copies a scheme with its work type mappings.
func (s *Store) CopyIssueTypeScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string) (*models.IssueTypeScreenScheme, error) {
	schemes, err := s.IssueTypeScreenSchemes(ctx, workspaceID, []string{schemeID})
	if err != nil {
		return nil, err
	}
	if len(schemes) == 0 {
		return nil, ErrScreenNotFound
	}
	original := schemes[0]
	mappings := make([]models.IssueTypeScreenSchemeItem, 0, len(original.Mappings))
	mappings = append(mappings, original.Mappings...)
	return s.CreateIssueTypeScreenScheme(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "issue_type_screen_schemes", workspaceID)), original.Description, mappings)
}

// CopyFieldConfiguration copies a configuration with every field's behavior.
func (s *Store) CopyFieldConfiguration(ctx context.Context, workspaceID, actorID, configurationID string) (*models.FieldConfiguration, error) {
	configurations, err := s.FieldConfigurations(ctx, workspaceID, []string{configurationID})
	if err != nil {
		return nil, err
	}
	if len(configurations) == 0 {
		return nil, ErrFieldConfigNotFound
	}
	original := configurations[0]
	copied, err := s.CreateFieldConfiguration(ctx, workspaceID, actorID,
		copyName(original.Name, s.nameTaken(ctx, "field_configurations", workspaceID)), original.Description)
	if err != nil {
		return nil, err
	}
	items, err := s.FieldConfigurationItems(ctx, workspaceID, configurationID)
	if err != nil {
		return nil, err
	}
	changes := make([]models.FieldConfigurationItem, 0, len(items))
	for _, item := range items {
		if !item.IsHidden && !item.IsRequired && strings.TrimSpace(item.Description) == "" {
			continue
		}
		changes = append(changes, item)
	}
	if len(changes) > 0 {
		if err := s.SetFieldConfigurationItems(ctx, workspaceID, actorID, copied.ID, changes); err != nil {
			return nil, err
		}
	}
	refreshed, err := s.FieldConfigurations(ctx, workspaceID, []string{copied.ID})
	if err != nil {
		return nil, err
	}
	if len(refreshed) == 0 {
		return copied, nil
	}
	return refreshed[0], nil
}
