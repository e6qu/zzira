package store

import (
	"context"

	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// NewProjectSchemes are the avatar and schemes a create project request names
// by the ids clients see. Zero leaves the site default.
type NewProjectSchemes struct {
	AvatarID                 int64
	PermissionScheme         int64
	NotificationScheme       int64
	IssueSecurityScheme      int64
	WorkflowScheme           int64
	IssueTypeScheme          int64
	IssueTypeScreenScheme    int64
	FieldConfigurationScheme int64
}

// ProjectSchemeError names the create project field whose avatar or scheme
// does not exist.
type ProjectSchemeError struct {
	Field, Message string
}

func (e *ProjectSchemeError) Error() string { return e.Message }

// assignNewProjectSchemes gives a project being created the avatar and schemes
// its request names, recording each assignment as the separate assignment
// operations do. The project has no work items yet, so no migration is needed.
func (s *Store) assignNewProjectSchemes(ctx context.Context, tx pgx.Tx, p *models.Project, actorID string, schemes NewProjectSchemes) error {
	workspaceID := p.WorkspaceID
	text := func(id int64) string { return strconv.FormatInt(id, 10) }
	missing := func(field, message string) error { return &ProjectSchemeError{Field: field, Message: message} }
	if schemes.AvatarID != 0 {
		if _, system := SystemAvatarIcon("project", schemes.AvatarID); !system {
			return missing("avatarId", "The avatar does not exist.")
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET avatar_id=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, p.ID, schemes.AvatarID); err != nil {
			return err
		}
	}
	if schemes.PermissionScheme != 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permission_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemes.PermissionScheme).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return missing("permissionScheme", "The permission scheme does not exist.")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_permission_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,scheme_id=EXCLUDED.scheme_id,assigned_at=now()`, p.ID, workspaceID, schemes.PermissionScheme); err != nil {
			return err
		}
		if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_permission_scheme", p.ID, models.OpUpsert, map[string]any{"projectId": p.ID, "schemeId": schemes.PermissionScheme}); err != nil {
			return err
		}
	}
	if schemes.NotificationScheme != 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemes.NotificationScheme).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return missing("notificationScheme", "The notification scheme does not exist.")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_notification_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,scheme_id=EXCLUDED.scheme_id,assigned_at=now()`, p.ID, workspaceID, schemes.NotificationScheme); err != nil {
			return err
		}
		if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_notification_scheme", p.ID, models.OpUpsert, map[string]any{"projectId": p.ID, "notificationSchemeId": schemes.NotificationScheme}); err != nil {
			return err
		}
	}
	if schemes.IssueSecurityScheme != 0 {
		if _, err := issueSecuritySchemeTx(ctx, tx, workspaceID, text(schemes.IssueSecurityScheme), false); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return missing("issueSecurityScheme", "The issue security scheme does not exist.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET security_scheme_id=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, p.ID, text(schemes.IssueSecurityScheme)); err != nil {
			return err
		}
	}
	if schemes.WorkflowScheme != 0 {
		var schemeID, defaultWorkflowID string
		err := tx.QueryRow(ctx, `SELECT id,COALESCE(default_workflow_id,'') FROM workflow_schemes WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, schemes.WorkflowScheme).Scan(&schemeID, &defaultWorkflowID)
		if errors.Is(err, pgx.ErrNoRows) {
			return missing("workflowScheme", "The workflow scheme does not exist.")
		}
		if err != nil {
			return err
		}
		if defaultWorkflowID == "" {
			defaultWorkflowID = "wf_default"
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET workflow_scheme_id=$3,workflow_id=$4 WHERE workspace_id=$1 AND id=$2`, workspaceID, p.ID, schemeID, defaultWorkflowID); err != nil {
			return err
		}
		p.WorkflowID = defaultWorkflowID
		if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.assigned", schemeID, map[string]any{"projectId": p.ID, "projectName": p.Name, "migratedIssues": 0}); err != nil {
			return err
		}
	}
	if schemes.IssueTypeScheme != 0 {
		scheme, err := s.issueTypeScheme(ctx, workspaceID, text(schemes.IssueTypeScheme))
		if err != nil {
			return missing("issueTypeScheme", "The issue type scheme does not exist.")
		}
		if !scheme.IsDefault {
			if _, err := tx.Exec(ctx, `INSERT INTO project_issue_type_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3::bigint) ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, p.ID, workspaceID, scheme.ID); err != nil {
				return err
			}
		}
	}
	if schemes.IssueTypeScreenScheme != 0 {
		scheme, err := issueTypeScreenSchemeTx(ctx, tx, workspaceID, text(schemes.IssueTypeScreenScheme))
		if err != nil {
			return missing("issueTypeScreenScheme", "The issue type screen scheme does not exist.")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_issue_type_screen_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, p.ID, workspaceID, scheme.ID); err != nil {
			return err
		}
		if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_issue_type_screen_scheme", p.ID, models.OpUpsert, map[string]any{"projectId": p.ID, "issueTypeScreenSchemeId": scheme.ID}); err != nil {
			return err
		}
	}
	if schemes.FieldConfigurationScheme != 0 {
		scheme, err := fieldConfigurationSchemeTx(ctx, tx, workspaceID, text(schemes.FieldConfigurationScheme))
		if err != nil {
			return missing("fieldScheme", "The field scheme does not exist.")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_field_configuration_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT (project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, p.ID, workspaceID, scheme.ID); err != nil {
			return err
		}
		if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_field_configuration_scheme", p.ID, models.OpUpsert, map[string]any{"projectId": p.ID, "fieldConfigurationSchemeId": scheme.ID}); err != nil {
			return err
		}
	}
	return nil
}
