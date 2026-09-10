package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPermissionSchemeValidation = errors.New("permission scheme request is invalid")
	ErrPermissionSchemeNotFound   = errors.New("permission scheme does not exist")
	ErrPermissionSchemeConflict   = errors.New("permission scheme is in use")
)

type PermissionDefinition struct {
	Key         string
	Name        string
	Description string
	Type        string
}

type PermissionGrantInput struct {
	Permission      string
	HolderType      string
	HolderParameter string
	HolderValue     string
}

var projectPermissionDefinitions = []PermissionDefinition{
	{"ADMINISTER_PROJECTS", "Administer projects", "Configure a project and its people.", "PROJECT"},
	{"EDIT_WORKFLOW", "Edit workflows", "Edit workflows for a project.", "PROJECT"},
	{"EDIT_ISSUE_LAYOUT", "Edit issue layout", "Configure the layout of work items.", "PROJECT"},
	{"BROWSE_PROJECTS", "Browse projects", "View a project and its work items.", "PROJECT"},
	{"MANAGE_SPRINTS_PERMISSION", "Manage sprints", "Create, start, complete, and edit sprints.", "PROJECT"},
	{"SERVICEDESK_AGENT", "Service desk agent", "Work on service requests as an agent.", "PROJECT"},
	{"VIEW_DEV_TOOLS", "View development tools", "View linked development information.", "PROJECT"},
	{"VIEW_READONLY_WORKFLOW", "View read-only workflow", "View the project's workflow.", "PROJECT"},
	{"ASSIGNABLE_USER", "Assignable user", "Be assigned work items.", "PROJECT"},
	{"ASSIGN_ISSUES", "Assign issues", "Assign work items to users.", "PROJECT"},
	{"CLOSE_ISSUES", "Close issues", "Close work items.", "PROJECT"},
	{"CREATE_ISSUES", "Create issues", "Create work items.", "PROJECT"},
	{"DELETE_ISSUES", "Delete issues", "Delete work items.", "PROJECT"},
	{"EDIT_ISSUES", "Edit issues", "Edit work items.", "PROJECT"},
	{"LINK_ISSUES", "Link issues", "Link work items.", "PROJECT"},
	{"MODIFY_REPORTER", "Modify reporter", "Change a work item's reporter.", "PROJECT"},
	{"MOVE_ISSUES", "Move issues", "Move work items between projects.", "PROJECT"},
	{"RESOLVE_ISSUES", "Resolve issues", "Resolve and reopen work items.", "PROJECT"},
	{"SCHEDULE_ISSUES", "Schedule issues", "Set or change due dates.", "PROJECT"},
	{"SET_ISSUE_SECURITY", "Set issue security", "Set security levels on work items.", "PROJECT"},
	{"TRANSITION_ISSUES", "Transition issues", "Move work items through workflows.", "PROJECT"},
	{"MANAGE_WATCHERS", "Manage watchers", "Add and remove watchers.", "PROJECT"},
	{"VIEW_VOTERS_AND_WATCHERS", "View voters and watchers", "View voter and watcher lists.", "PROJECT"},
	{"ADD_COMMENTS", "Add comments", "Comment on work items.", "PROJECT"},
	{"DELETE_ALL_COMMENTS", "Delete all comments", "Delete any comment.", "PROJECT"},
	{"DELETE_OWN_COMMENTS", "Delete own comments", "Delete comments authored by the user.", "PROJECT"},
	{"EDIT_ALL_COMMENTS", "Edit all comments", "Edit any comment.", "PROJECT"},
	{"EDIT_OWN_COMMENTS", "Edit own comments", "Edit comments authored by the user.", "PROJECT"},
	{"CREATE_ATTACHMENTS", "Create attachments", "Attach files to work items.", "PROJECT"},
	{"DELETE_ALL_ATTACHMENTS", "Delete all attachments", "Delete any attachment.", "PROJECT"},
	{"DELETE_OWN_ATTACHMENTS", "Delete own attachments", "Delete attachments uploaded by the user.", "PROJECT"},
	{"DELETE_ALL_WORKLOGS", "Delete all worklogs", "Delete any worklog.", "PROJECT"},
	{"DELETE_OWN_WORKLOGS", "Delete own worklogs", "Delete worklogs authored by the user.", "PROJECT"},
	{"EDIT_ALL_WORKLOGS", "Edit all worklogs", "Edit any worklog.", "PROJECT"},
	{"EDIT_OWN_WORKLOGS", "Edit own worklogs", "Edit worklogs authored by the user.", "PROJECT"},
	{"WORK_ON_ISSUES", "Work on issues", "Log work on work items.", "PROJECT"},
}

var globalPermissionDefinitions = []PermissionDefinition{
	{"ADMINISTER", "Administer Jira", "Administer the Jira site.", "GLOBAL"},
	{"BULK_CHANGE", "Bulk change", "Modify multiple work items at once.", "GLOBAL"},
	{"CREATE_SHARED_OBJECTS", "Create shared objects", "Share filters and dashboards.", "GLOBAL"},
	{"MANAGE_GROUP_FILTER_SUBSCRIPTIONS", "Manage group filter subscriptions", "Subscribe groups to filters.", "GLOBAL"},
	{"SHARE_DASHBOARDS", "Share dashboards and filters", "Share dashboards and filters with other users.", "GLOBAL"},
	{"USER_PICKER", "Browse users and groups", "Select users and groups in Jira fields.", "GLOBAL"},
	{"BROWSE_USERS", "Browse users", "Find users in Jira.", "GLOBAL"},
	{"CREATE_TEAM_MANAGED_PROJECT", "Create team-managed projects", "Create team-managed projects.", "GLOBAL"},
	{"ADMINISTER_TEAM_MANAGED_PROJECTS", "Administer team-managed projects", "Administer every team-managed project.", "GLOBAL"},
}

func PermissionDefinitions() []PermissionDefinition {
	definitions := append([]PermissionDefinition{}, globalPermissionDefinitions...)
	definitions = append(definitions, projectPermissionDefinitions...)
	return definitions
}

func ProjectPermissionDefinitions() []PermissionDefinition {
	return append([]PermissionDefinition{}, projectPermissionDefinitions...)
}

func PermissionDefinitionByKey(key string) (PermissionDefinition, bool) {
	for _, definition := range PermissionDefinitions() {
		if definition.Key == key {
			return definition, true
		}
	}
	return PermissionDefinition{}, false
}

const permissionSchemeColumns = `ps.id,ps.workspace_id,ps.name,ps.description,ps.is_default,
	(SELECT count(*)::int FROM project_permission_schemes pps WHERE pps.workspace_id=ps.workspace_id AND pps.scheme_id=ps.id)`

func scanPermissionScheme(row pgx.Row) (*models.PermissionScheme, error) {
	scheme := &models.PermissionScheme{}
	if err := row.Scan(&scheme.ID, &scheme.WorkspaceID, &scheme.Name, &scheme.Description, &scheme.Default, &scheme.ProjectCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPermissionSchemeNotFound
		}
		return nil, err
	}
	return scheme, nil
}

func scanPermissionGrant(row pgx.Row) (models.PermissionGrant, error) {
	grant := models.PermissionGrant{}
	err := row.Scan(&grant.ID, &grant.Permission, &grant.HolderType, &grant.HolderParameter, &grant.HolderValue)
	return grant, err
}

func (s *Store) PermissionSchemes(ctx context.Context, workspaceID string, expand bool) ([]*models.PermissionScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+permissionSchemeColumns+` FROM permission_schemes ps WHERE ps.workspace_id=$1 ORDER BY ps.is_default DESC,lower(ps.name),ps.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemes := []*models.PermissionScheme{}
	for rows.Next() {
		scheme, scanErr := scanPermissionScheme(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		schemes = append(schemes, scheme)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if expand {
		for _, scheme := range schemes {
			scheme.Grants, err = s.PermissionSchemeGrants(ctx, workspaceID, scheme.ID)
			if err != nil {
				return nil, err
			}
		}
	}
	return schemes, nil
}

func (s *Store) PermissionScheme(ctx context.Context, workspaceID string, schemeID int64, expand bool) (*models.PermissionScheme, error) {
	scheme, err := scanPermissionScheme(s.Pool.QueryRow(ctx, `SELECT `+permissionSchemeColumns+` FROM permission_schemes ps WHERE ps.workspace_id=$1 AND ps.id=$2`, workspaceID, schemeID))
	if err != nil {
		return nil, err
	}
	if expand {
		scheme.Grants, err = s.PermissionSchemeGrants(ctx, workspaceID, schemeID)
	}
	return scheme, err
}

func (s *Store) PermissionSchemeGrants(ctx context.Context, workspaceID string, schemeID int64) ([]models.PermissionGrant, error) {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permission_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemeID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrPermissionSchemeNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,permission_key,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,'') FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2 ORDER BY permission_key,id`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []models.PermissionGrant{}
	for rows.Next() {
		grant, scanErr := scanPermissionGrant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *Store) PermissionSchemeGrant(ctx context.Context, workspaceID string, schemeID, grantID int64) (models.PermissionGrant, error) {
	grant, err := scanPermissionGrant(s.Pool.QueryRow(ctx, `SELECT id,permission_key,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,'') FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2 AND id=$3`, workspaceID, schemeID, grantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return grant, ErrPermissionSchemeNotFound
	}
	return grant, err
}

func validatePermissionSchemeName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrPermissionSchemeValidation)
	}
	return nil
}

var permissionKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,254}$`)

func validatePermissionGrant(ctx context.Context, tx pgx.Tx, workspaceID string, input PermissionGrantInput) (PermissionGrantInput, error) {
	input.Permission = strings.TrimSpace(input.Permission)
	input.HolderType = strings.TrimSpace(input.HolderType)
	input.HolderParameter = strings.TrimSpace(input.HolderParameter)
	input.HolderValue = strings.TrimSpace(input.HolderValue)
	if !permissionKeyPattern.MatchString(input.Permission) {
		return input, fmt.Errorf("%w: permission key is invalid", ErrPermissionSchemeValidation)
	}
	switch input.HolderType {
	case "anyone", "assignee", "projectLead", "reporter", "sd.customer.portal.only":
		if input.HolderParameter != "" || input.HolderValue != "" {
			return input, fmt.Errorf("%w: holder %s does not accept a value", ErrPermissionSchemeValidation, input.HolderType)
		}
	case "applicationRole":
		value := input.HolderValue
		if value == "" {
			value = input.HolderParameter
		}
		if value != "jira-software" && value != "jira-service-management" {
			return input, fmt.Errorf("%w: application role is invalid", ErrPermissionSchemeValidation)
		}
		input.HolderParameter, input.HolderValue = value, value
	case "group":
		var id, name string
		err := tx.QueryRow(ctx, `SELECT g.id::text,g.name FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND d.active AND (g.id::text=NULLIF($2,'') OR lower(g.name)=lower(NULLIF($3,''))) LIMIT 1`, workspaceID, input.HolderValue, input.HolderParameter).Scan(&id, &name)
		if err != nil {
			return input, fmt.Errorf("%w: group does not exist", ErrPermissionSchemeValidation)
		}
		input.HolderParameter, input.HolderValue = name, id
	case "projectRole":
		value := input.HolderValue
		if value == "" {
			value = input.HolderParameter
		}
		roleID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || roleID <= 0 {
			return input, fmt.Errorf("%w: project role is invalid", ErrPermissionSchemeValidation)
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_roles WHERE workspace_id=$1 AND id=$2)`, workspaceID, roleID).Scan(&exists); err != nil || !exists {
			return input, fmt.Errorf("%w: project role does not exist", ErrPermissionSchemeValidation)
		}
		input.HolderParameter, input.HolderValue = value, value
	case "user":
		value := input.HolderValue
		if value == "" {
			value = input.HolderParameter
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)`, workspaceID, value).Scan(&exists); err != nil || !exists {
			return input, fmt.Errorf("%w: user does not exist", ErrPermissionSchemeValidation)
		}
		input.HolderParameter, input.HolderValue = value, value
	case "groupCustomField", "userCustomField":
		value := input.HolderValue
		if value == "" {
			value = input.HolderParameter
		}
		if !strings.HasPrefix(value, "customfield_") {
			return input, fmt.Errorf("%w: custom field is invalid", ErrPermissionSchemeValidation)
		}
		input.HolderParameter, input.HolderValue = value, value
	default:
		return input, fmt.Errorf("%w: permission holder type is invalid", ErrPermissionSchemeValidation)
	}
	return input, nil
}

func insertPermissionGrant(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64, input PermissionGrantInput) (models.PermissionGrant, error) {
	input, err := validatePermissionGrant(ctx, tx, workspaceID, input)
	if err != nil {
		return models.PermissionGrant{}, err
	}
	grant, err := scanPermissionGrant(tx.QueryRow(ctx, `INSERT INTO permission_scheme_grants(workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,'')) RETURNING id,permission_key,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,'')`, workspaceID, schemeID, input.Permission, input.HolderType, input.HolderParameter, input.HolderValue))
	if isUniqueViolation(err) {
		return grant, fmt.Errorf("%w: permission grant already exists", ErrPermissionSchemeConflict)
	}
	return grant, err
}

func (s *Store) CreatePermissionScheme(ctx context.Context, workspaceID, actorID, name, description string, grants []PermissionGrantInput) (*models.PermissionScheme, error) {
	if err := validatePermissionSchemeName(name); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := scanPermissionScheme(tx.QueryRow(ctx, `INSERT INTO permission_schemes(workspace_id,name,description) VALUES($1,$2,$3) RETURNING id,workspace_id,name,description,is_default,0`, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a permission scheme with this name already exists", ErrPermissionSchemeConflict)
	}
	if err != nil {
		return nil, err
	}
	for _, input := range grants {
		grant, grantErr := insertPermissionGrant(ctx, tx, workspaceID, scheme.ID, input)
		if grantErr != nil {
			return nil, grantErr
		}
		scheme.Grants = append(scheme.Grants, grant)
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "permission_scheme", strconv.FormatInt(scheme.ID, 10), models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return scheme, nil
}

func (s *Store) UpdatePermissionScheme(ctx context.Context, workspaceID, actorID string, schemeID int64, name, description string, grants *[]PermissionGrantInput) (*models.PermissionScheme, error) {
	if err := validatePermissionSchemeName(name); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := scanPermissionScheme(tx.QueryRow(ctx, `UPDATE permission_schemes ps SET name=$3,description=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2 RETURNING ps.id,ps.workspace_id,ps.name,ps.description,ps.is_default,(SELECT count(*)::int FROM project_permission_schemes pps WHERE pps.workspace_id=ps.workspace_id AND pps.scheme_id=ps.id)`, workspaceID, schemeID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a permission scheme with this name already exists", ErrPermissionSchemeConflict)
	}
	if err != nil {
		return nil, err
	}
	if grants != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2`, workspaceID, schemeID); err != nil {
			return nil, err
		}
		for _, input := range *grants {
			grant, grantErr := insertPermissionGrant(ctx, tx, workspaceID, schemeID, input)
			if grantErr != nil {
				return nil, grantErr
			}
			scheme.Grants = append(scheme.Grants, grant)
		}
	} else {
		scheme.Grants, err = permissionSchemeGrantsTx(ctx, tx, workspaceID, schemeID)
		if err != nil {
			return nil, err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "permission_scheme", strconv.FormatInt(scheme.ID, 10), models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return scheme, nil
}

func permissionSchemeGrantsTx(ctx context.Context, tx pgx.Tx, workspaceID string, schemeID int64) ([]models.PermissionGrant, error) {
	rows, err := tx.Query(ctx, `SELECT id,permission_key,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,'') FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2 ORDER BY permission_key,id`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []models.PermissionGrant{}
	for rows.Next() {
		grant, scanErr := scanPermissionGrant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *Store) CreatePermissionGrant(ctx context.Context, workspaceID, actorID string, schemeID int64, input PermissionGrantInput) (models.PermissionGrant, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.PermissionGrant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return models.PermissionGrant{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permission_schemes WHERE workspace_id=$1 AND id=$2)`, workspaceID, schemeID).Scan(&exists); err != nil {
		return models.PermissionGrant{}, err
	}
	if !exists {
		return models.PermissionGrant{}, ErrPermissionSchemeNotFound
	}
	grant, err := insertPermissionGrant(ctx, tx, workspaceID, schemeID, input)
	if err != nil {
		return grant, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "permission_grant", strconv.FormatInt(grant.ID, 10), models.OpUpsert, grant); err != nil {
		return grant, err
	}
	return grant, tx.Commit(ctx)
}

func (s *Store) DeletePermissionGrant(ctx context.Context, workspaceID, actorID string, schemeID, grantID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	grant, err := scanPermissionGrant(tx.QueryRow(ctx, `DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2 AND id=$3 RETURNING id,permission_key,holder_type,COALESCE(holder_parameter,''),COALESCE(holder_value,'')`, workspaceID, schemeID, grantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPermissionSchemeNotFound
	}
	if err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "permission_grant", strconv.FormatInt(grant.ID, 10), models.OpDelete, grant); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeletePermissionScheme(ctx context.Context, workspaceID, actorID string, schemeID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := scanPermissionScheme(tx.QueryRow(ctx, `SELECT `+permissionSchemeColumns+` FROM permission_schemes ps WHERE ps.workspace_id=$1 AND ps.id=$2 FOR UPDATE`, workspaceID, schemeID))
	if err != nil {
		return err
	}
	if scheme.Default || scheme.ProjectCount > 0 {
		return fmt.Errorf("%w: assign every project to another scheme before deleting it", ErrPermissionSchemeConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM permission_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, schemeID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "permission_scheme", strconv.FormatInt(scheme.ID, 10), models.OpDelete, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AssignedPermissionScheme(ctx context.Context, workspaceID, projectIDOrKey string, expand bool) (*models.PermissionScheme, *models.Project, error) {
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, nil, ErrPermissionSchemeNotFound
	}
	scheme, err := scanPermissionScheme(s.Pool.QueryRow(ctx, `SELECT `+permissionSchemeColumns+` FROM permission_schemes ps JOIN project_permission_schemes pps ON pps.workspace_id=ps.workspace_id AND pps.scheme_id=ps.id WHERE pps.project_id=$1 AND ps.workspace_id=$2`, project.ID, workspaceID))
	if err != nil {
		return nil, nil, err
	}
	if expand {
		scheme.Grants, err = s.PermissionSchemeGrants(ctx, workspaceID, scheme.ID)
	}
	return scheme, project, err
}

func (s *Store) AssignPermissionScheme(ctx context.Context, workspaceID, actorID, projectIDOrKey string, schemeID int64) (*models.PermissionScheme, *models.Project, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, nil, err
	}
	project, err := scanProject(tx.QueryRow(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2)) FOR UPDATE`, workspaceID, projectIDOrKey))
	if err != nil {
		return nil, nil, ErrPermissionSchemeNotFound
	}
	scheme, err := scanPermissionScheme(tx.QueryRow(ctx, `SELECT `+permissionSchemeColumns+` FROM permission_schemes ps WHERE workspace_id=$1 AND id=$2 FOR SHARE`, workspaceID, schemeID))
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_permission_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,scheme_id=EXCLUDED.scheme_id,assigned_at=now()`, project.ID, workspaceID, schemeID); err != nil {
		return nil, nil, err
	}
	payload := map[string]any{"projectId": project.ID, "schemeId": schemeID}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_permission_scheme", project.ID, models.OpUpsert, payload); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return scheme, project, nil
}

func (s *Store) ProjectIDsForPermissionScheme(ctx context.Context, workspaceID string, schemeID int64) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id FROM projects p JOIN project_permission_schemes pps ON pps.project_id=p.id WHERE p.workspace_id=$1 AND pps.scheme_id=$2 AND p.lifecycle_state='ACTIVE' ORDER BY p.key`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func globalPermissionForUser(ctx context.Context, tx pgx.Tx, workspaceID, userID, permission string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	var admin, member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM role_bindings rb JOIN sites si ON
		 (rb.scope_type='site' AND rb.scope_id=si.id::text) OR
		 (rb.scope_type='organization' AND rb.scope_id=si.organization_id::text)
		WHERE si.workspace_id=$1 AND rb.role_key IN ('atlassian/org-admin','atlassian/site-admin')
		AND ((rb.principal_type='user' AND rb.principal_id=$2) OR
		 (rb.principal_type='group' AND EXISTS(SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id JOIN directories d ON d.id=g.directory_id WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$2 AND d.active)))
	)`, workspaceID, userID).Scan(&admin); err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)`, workspaceID, userID).Scan(&member); err != nil {
		return false, err
	}
	switch permission {
	case "BULK_CHANGE", "CREATE_SHARED_OBJECTS", "SHARE_DASHBOARDS", "USER_PICKER", "BROWSE_USERS", "CREATE_TEAM_MANAGED_PROJECT":
		return member, nil
	default:
		return false, nil
	}
}

func hasProjectPermissionTx(ctx context.Context, tx pgx.Tx, workspaceID, userID, projectIDOrKey, issueID, permission string) (bool, string, error) {
	if _, ok := PermissionDefinitionByKey(permission); !ok && !permissionKeyPattern.MatchString(permission) {
		return false, "", nil
	}
	if global, err := globalPermissionForUser(ctx, tx, workspaceID, userID, "ADMINISTER"); err != nil || global {
		return global, "", err
	}
	var projectID string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, workspaceID, projectIDOrKey).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", ErrPermissionSchemeNotFound
		}
		return false, "", err
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT jira_has_project_permission($1,$2,NULLIF($3,''),NULLIF($4,''),$5)`, workspaceID, projectID, userID, issueID, permission).Scan(&allowed)
	return allowed, projectID, err
}

func (s *Store) HasProjectPermission(ctx context.Context, workspaceID, userID, projectIDOrKey, issueID, permission string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	allowed, _, err := hasProjectPermissionTx(ctx, tx, workspaceID, userID, projectIDOrKey, issueID, permission)
	return allowed, err
}

func (s *Store) HasGlobalPermission(ctx context.Context, workspaceID, userID, permission string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return globalPermissionForUser(ctx, tx, workspaceID, userID, permission)
}

func (s *Store) ProjectsWithPermissions(ctx context.Context, workspaceID, userID string, permissions []string) ([]*models.Project, error) {
	projects, err := s.ProjectsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := []*models.Project{}
	for _, project := range projects {
		allowed := true
		for _, permission := range permissions {
			granted, permissionErr := s.HasProjectPermission(ctx, workspaceID, userID, project.ID, "", permission)
			if permissionErr != nil {
				return nil, permissionErr
			}
			if !granted {
				allowed = false
				break
			}
		}
		if allowed {
			result = append(result, project)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result, nil
}
