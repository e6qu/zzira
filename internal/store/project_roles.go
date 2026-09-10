package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrProjectRoleValidation = errors.New("project role request is invalid")
	ErrProjectRoleConflict   = errors.New("project role is in use")
	ErrProjectRoleNotFound   = errors.New("project role does not exist")
)

type ProjectRoleActorInput struct {
	Users      []string
	GroupIDs   []string
	GroupNames []string
}

const projectRoleColumns = `id,workspace_id,name,description,is_admin,is_default`

func scanProjectRole(row pgx.Row) (*models.ProjectRole, error) {
	role := &models.ProjectRole{}
	if err := row.Scan(&role.ID, &role.WorkspaceID, &role.Name, &role.Description, &role.Admin, &role.Default); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectRoleNotFound
		}
		return nil, err
	}
	return role, nil
}

func (s *Store) ProjectRoles(ctx context.Context, workspaceID string) ([]*models.ProjectRole, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 ORDER BY lower(name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []*models.ProjectRole{}
	for rows.Next() {
		role, scanErr := scanProjectRole(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Store) GroupsByWorkspace(ctx context.Context, workspaceID string) ([]*models.Group, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id::text,g.directory_id::text,g.name,g.description,g.created_at,g.updated_at,count(gm.user_id)::int
		FROM groups g JOIN directories d ON d.id=g.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		LEFT JOIN group_members gm ON gm.group_id=g.id
		WHERE si.workspace_id=$1 AND d.active
		GROUP BY g.id ORDER BY lower(g.name),g.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []*models.Group{}
	for rows.Next() {
		group := &models.Group{}
		var createdAt, updatedAt time.Time
		if err = rows.Scan(&group.ID, &group.DirectoryID, &group.Name, &group.Description, &createdAt, &updatedAt, &group.MemberCount); err != nil {
			return nil, err
		}
		group.CreatedAt, group.UpdatedAt = formatAdminTime(createdAt), formatAdminTime(updatedAt)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) ProjectRole(ctx context.Context, workspaceID string, roleID int64) (*models.ProjectRole, error) {
	return scanProjectRole(s.Pool.QueryRow(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 AND id=$2`, workspaceID, roleID))
}

func validateProjectRoleName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrProjectRoleValidation)
	}
	return nil
}

func (s *Store) CreateProjectRole(ctx context.Context, workspaceID, actorID, name, description string) (*models.ProjectRole, error) {
	if err := validateProjectRoleName(name); err != nil {
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
	role, err := scanProjectRole(tx.QueryRow(ctx, `INSERT INTO project_roles(workspace_id,name,description) VALUES($1,$2,$3) RETURNING `+projectRoleColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a project role with this name already exists", ErrProjectRoleConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_role", strconv.FormatInt(role.ID, 10), models.OpUpsert, role); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Store) UpdateProjectRole(ctx context.Context, workspaceID, actorID string, roleID int64, name, description *string, full bool) (*models.ProjectRole, error) {
	if full && (name == nil || description == nil) {
		return nil, fmt.Errorf("%w: name and description are required", ErrProjectRoleValidation)
	}
	if name != nil {
		if err := validateProjectRoleName(*name); err != nil {
			return nil, err
		}
	}
	if name == nil && description == nil {
		return nil, fmt.Errorf("%w: provide name or description", ErrProjectRoleValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	role, err := scanProjectRole(tx.QueryRow(ctx, `UPDATE project_roles SET name=COALESCE($3,name),description=COALESCE($4,description),updated_at=now() WHERE workspace_id=$1 AND id=$2 RETURNING `+projectRoleColumns, workspaceID, roleID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a project role with this name already exists", ErrProjectRoleConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_role", strconv.FormatInt(role.ID, 10), models.OpUpsert, role); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Store) DeleteProjectRole(ctx context.Context, workspaceID, actorID string, roleID int64, swapID *int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	role, err := scanProjectRole(tx.QueryRow(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, roleID))
	if err != nil {
		return err
	}
	var inUse bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM role_bindings rb JOIN projects p ON p.id=rb.scope_id
		 WHERE rb.scope_type='project' AND p.workspace_id=$1 AND rb.role_key=$2
		UNION ALL
		SELECT 1 FROM project_role_default_actors WHERE workspace_id=$1 AND role_id=$3
		UNION ALL
		SELECT 1 FROM filter_share_permissions fp JOIN projects p ON p.id=fp.project_id
		 WHERE p.workspace_id=$1 AND fp.permission_type='projectRole' AND fp.project_role_id=$2
		UNION ALL
		SELECT 1 FROM permission_scheme_grants pg
		 WHERE pg.workspace_id=$1 AND pg.holder_type='projectRole' AND pg.holder_value=$2
	)`, workspaceID, strconv.FormatInt(roleID, 10), roleID).Scan(&inUse); err != nil {
		return err
	}
	if inUse && swapID == nil {
		return fmt.Errorf("%w: choose a replacement role", ErrProjectRoleConflict)
	}
	if swapID != nil {
		if *swapID == roleID {
			return fmt.Errorf("%w: replacement role must be different", ErrProjectRoleValidation)
		}
		replacement, replacementErr := scanProjectRole(tx.QueryRow(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, *swapID))
		if replacementErr != nil {
			return fmt.Errorf("%w: replacement role does not exist", ErrProjectRoleValidation)
		}
		oldKey, newKey := strconv.FormatInt(roleID, 10), strconv.FormatInt(*swapID, 10)
		if _, err = tx.Exec(ctx, `INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
			SELECT scope_type,scope_id,$3,principal_type,principal_id,source FROM role_bindings rb
			JOIN projects p ON p.id=rb.scope_id
			WHERE rb.scope_type='project' AND p.workspace_id=$1 AND rb.role_key=$2
			ON CONFLICT DO NOTHING`, workspaceID, oldKey, newKey); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM role_bindings rb USING projects p WHERE rb.scope_type='project' AND rb.scope_id=p.id AND p.workspace_id=$1 AND rb.role_key=$2`, workspaceID, oldKey); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO project_role_default_actors(workspace_id,role_id,principal_type,principal_id)
			SELECT workspace_id,$3,principal_type,principal_id FROM project_role_default_actors WHERE workspace_id=$1 AND role_id=$2
			ON CONFLICT DO NOTHING`, workspaceID, roleID, *swapID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM project_role_default_actors WHERE workspace_id=$1 AND role_id=$2`, workspaceID, roleID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM filter_share_permissions old USING projects p
			WHERE old.project_id=p.id AND p.workspace_id=$1 AND old.permission_type='projectRole' AND old.project_role_id=$2
			AND EXISTS (SELECT 1 FROM filter_share_permissions replacement_share
			 WHERE replacement_share.filter_id=old.filter_id AND replacement_share.permission_type='projectRole'
			 AND replacement_share.project_id=old.project_id AND replacement_share.project_role_id=$3 AND replacement_share.rights=old.rights)`, workspaceID, oldKey, newKey); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE filter_share_permissions fp SET project_role_id=$3 FROM projects p
			WHERE fp.project_id=p.id AND p.workspace_id=$1 AND fp.permission_type='projectRole' AND fp.project_role_id=$2`, workspaceID, oldKey, newKey); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM permission_scheme_grants old
			WHERE old.workspace_id=$1 AND old.holder_type='projectRole' AND old.holder_value=$2
			AND EXISTS(SELECT 1 FROM permission_scheme_grants replacement
			 WHERE replacement.workspace_id=old.workspace_id AND replacement.scheme_id=old.scheme_id
			 AND replacement.permission_key=old.permission_key AND replacement.holder_type='projectRole'
			 AND replacement.holder_value=$3)`, workspaceID, oldKey, newKey); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE permission_scheme_grants SET holder_parameter=$3,holder_value=$3
			WHERE workspace_id=$1 AND holder_type='projectRole' AND holder_value=$2`, workspaceID, oldKey, newKey); err != nil {
			return err
		}
		if (role.Admin && !replacement.Admin) || (role.Default && !replacement.Default) {
			if _, err = tx.Exec(ctx, `UPDATE project_roles SET is_admin=is_admin OR $3,is_default=is_default OR $4,updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, *swapID, role.Admin, role.Default); err != nil {
				return err
			}
		}
	}
	if command, deleteErr := tx.Exec(ctx, `DELETE FROM project_roles WHERE workspace_id=$1 AND id=$2`, workspaceID, roleID); deleteErr != nil {
		return deleteErr
	} else if command.RowsAffected() != 1 {
		return ErrProjectRoleNotFound
	}
	payload := map[string]any{"role": role}
	if swapID != nil {
		payload["swap"] = *swapID
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_role", strconv.FormatInt(role.ID, 10), models.OpDelete, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type rolePrincipal struct{ kind, id string }

func normalizedActorValues(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !seen[part] {
				seen[part] = true
				result = append(result, part)
			}
		}
	}
	return result
}

func validateSingleProjectRoleActorType(input ProjectRoleActorInput) error {
	users := len(normalizedActorValues(input.Users)) > 0
	groups := len(normalizedActorValues(input.GroupIDs)) > 0 || len(normalizedActorValues(input.GroupNames)) > 0
	if users == groups {
		return fmt.Errorf("%w: provide either users or groups", ErrProjectRoleValidation)
	}
	return nil
}

func resolveProjectRoleActors(ctx context.Context, tx pgx.Tx, workspaceID string, input ProjectRoleActorInput, requireActive bool) ([]rolePrincipal, error) {
	users := normalizedActorValues(input.Users)
	groupIDs := normalizedActorValues(input.GroupIDs)
	groupNames := normalizedActorValues(input.GroupNames)
	if len(groupIDs) > 0 && len(groupNames) > 0 {
		return nil, fmt.Errorf("%w: group and groupId cannot be used together", ErrProjectRoleValidation)
	}
	principals := make([]rolePrincipal, 0, len(users)+len(groupIDs)+len(groupNames))
	for _, id := range users {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.workspace_id=$1 AND u.id=$2 AND (NOT $3 OR u.active))`, workspaceID, id, requireActive).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: user %s does not exist or is inactive", ErrProjectRoleNotFound, id)
		}
		principals = append(principals, rolePrincipal{"user", id})
	}
	for _, id := range groupIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND g.id::text=$2 AND (NOT $3 OR d.active))`, workspaceID, id, requireActive).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: group %s does not exist", ErrProjectRoleNotFound, id)
		}
		principals = append(principals, rolePrincipal{"group", id})
	}
	for _, name := range groupNames {
		var id string
		err := tx.QueryRow(ctx, `SELECT g.id::text FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND lower(g.name)=lower($2) AND (NOT $3 OR d.active) ORDER BY g.id LIMIT 1`, workspaceID, name, requireActive).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: group %s does not exist", ErrProjectRoleNotFound, name)
		}
		if err != nil {
			return nil, err
		}
		principals = append(principals, rolePrincipal{"group", id})
	}
	return principals, nil
}

func scanProjectRoleActors(rows pgx.Rows) ([]models.ProjectRoleActor, error) {
	actors := []models.ProjectRoleActor{}
	for rows.Next() {
		var actor models.ProjectRoleActor
		if err := rows.Scan(&actor.ID, &actor.PrincipalType, &actor.PrincipalID, &actor.DisplayName, &actor.Active); err != nil {
			return nil, err
		}
		actors = append(actors, actor)
	}
	return actors, rows.Err()
}

func (s *Store) DefaultProjectRoleActors(ctx context.Context, workspaceID string, roleID int64) ([]models.ProjectRoleActor, error) {
	if _, err := s.ProjectRole(ctx, workspaceID, roleID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT d.id,d.principal_type,d.principal_id,
		COALESCE(u.display_name,g.name,d.principal_id),COALESCE(u.active,TRUE)
		FROM project_role_default_actors d
		LEFT JOIN users u ON d.principal_type='user' AND u.id=d.principal_id
		LEFT JOIN groups g ON d.principal_type='group' AND g.id::text=d.principal_id
		WHERE d.workspace_id=$1 AND d.role_id=$2 ORDER BY d.principal_type,lower(COALESCE(u.display_name,g.name,d.principal_id)),d.id`, workspaceID, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjectRoleActors(rows)
}

func (s *Store) mutateDefaultProjectRoleActors(ctx context.Context, workspaceID, actorID string, roleID int64, input ProjectRoleActorInput, add bool) ([]models.ProjectRoleActor, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if _, err = scanProjectRole(tx.QueryRow(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 AND id=$2 FOR SHARE`, workspaceID, roleID)); err != nil {
		return nil, err
	}
	if err = validateSingleProjectRoleActorType(input); err != nil {
		return nil, err
	}
	principals, err := resolveProjectRoleActors(ctx, tx, workspaceID, input, add)
	if err != nil {
		return nil, err
	}
	if len(principals) == 0 {
		return nil, fmt.Errorf("%w: provide at least one user or group", ErrProjectRoleValidation)
	}
	for _, principal := range principals {
		if add {
			_, err = tx.Exec(ctx, `INSERT INTO project_role_default_actors(workspace_id,role_id,principal_type,principal_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, workspaceID, roleID, principal.kind, principal.id)
		} else {
			command, deleteErr := tx.Exec(ctx, `DELETE FROM project_role_default_actors WHERE workspace_id=$1 AND role_id=$2 AND principal_type=$3 AND principal_id=$4`, workspaceID, roleID, principal.kind, principal.id)
			err = deleteErr
			if deleteErr == nil && command.RowsAffected() == 0 {
				err = ErrProjectRoleNotFound
			}
		}
		if err != nil {
			return nil, err
		}
	}
	op := models.OpUpsert
	if !add {
		op = models.OpDelete
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_role_default_actors", strconv.FormatInt(roleID, 10), op, input); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.DefaultProjectRoleActors(ctx, workspaceID, roleID)
}

func (s *Store) AddDefaultProjectRoleActors(ctx context.Context, workspaceID, actorID string, roleID int64, input ProjectRoleActorInput) ([]models.ProjectRoleActor, error) {
	return s.mutateDefaultProjectRoleActors(ctx, workspaceID, actorID, roleID, input, true)
}

func (s *Store) DeleteDefaultProjectRoleActors(ctx context.Context, workspaceID, actorID string, roleID int64, input ProjectRoleActorInput) ([]models.ProjectRoleActor, error) {
	return s.mutateDefaultProjectRoleActors(ctx, workspaceID, actorID, roleID, input, false)
}

func projectRoleAdmin(ctx context.Context, tx pgx.Tx, workspaceID, actorID, projectID string) error {
	allowed, _, err := hasProjectPermissionTx(ctx, tx, workspaceID, actorID, projectID, "", "ADMINISTER_PROJECTS")
	if err != nil {
		return err
	}
	if !allowed {
		return ErrProjectPermission
	}
	return nil
}

func (s *Store) CanAdministerProject(ctx context.Context, workspaceID, actorID, projectID string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = projectRoleAdmin(ctx, tx, workspaceID, actorID, projectID)
	if errors.Is(err, ErrProjectPermission) {
		return false, nil
	}
	return err == nil, err
}

func projectRoleAndProject(ctx context.Context, tx pgx.Tx, workspaceID, projectIDOrKey string, roleID int64) (string, error) {
	var projectID string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2)) FOR SHARE`, workspaceID, projectIDOrKey).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProjectRoleNotFound
		}
		return "", err
	}
	if _, err := scanProjectRole(tx.QueryRow(ctx, `SELECT `+projectRoleColumns+` FROM project_roles WHERE workspace_id=$1 AND id=$2 FOR SHARE`, workspaceID, roleID)); err != nil {
		return "", err
	}
	return projectID, nil
}

func (s *Store) ProjectRoleActors(ctx context.Context, workspaceID, projectIDOrKey string, roleID int64, excludeInactive bool) ([]models.ProjectRoleActor, error) {
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, ErrProjectRoleNotFound
	}
	if _, err = s.ProjectRole(ctx, workspaceID, roleID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT rb.id,rb.principal_type,rb.principal_id,
		COALESCE(u.display_name,g.name,rb.principal_id),COALESCE(u.active,TRUE)
		FROM role_bindings rb
		LEFT JOIN users u ON rb.principal_type='user' AND u.id=rb.principal_id
		LEFT JOIN groups g ON rb.principal_type='group' AND g.id::text=rb.principal_id
		WHERE rb.scope_type='project' AND rb.scope_id=$1 AND rb.role_key=$2
		AND (NOT $3 OR rb.principal_type<>'user' OR COALESCE(u.active,FALSE))
		ORDER BY rb.principal_type,lower(COALESCE(u.display_name,g.name,rb.principal_id)),rb.id`, project.ID, strconv.FormatInt(roleID, 10), excludeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjectRoleActors(rows)
}

func (s *Store) UserInProjectRole(ctx context.Context, workspaceID, projectIDOrKey, userID string, roleID int64) (bool, error) {
	project, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return false, ErrProjectRoleNotFound
	}
	var member bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM role_bindings rb
		WHERE rb.scope_type='project' AND rb.scope_id=$1 AND rb.role_key=$2
		AND ((rb.principal_type='user' AND rb.principal_id=$3) OR
			(rb.principal_type='group' AND EXISTS (
				SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id
				JOIN directories d ON d.id=g.directory_id
				WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$3 AND d.active))))`, project.ID, strconv.FormatInt(roleID, 10), userID).Scan(&member)
	return member, err
}

func (s *Store) mutateProjectRoleActors(ctx context.Context, workspaceID, actorID, projectIDOrKey string, roleID int64, input ProjectRoleActorInput, mode string) ([]models.ProjectRoleActor, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	projectID, err := projectRoleAndProject(ctx, tx, workspaceID, projectIDOrKey, roleID)
	if err != nil {
		return nil, err
	}
	if err = projectRoleAdmin(ctx, tx, workspaceID, actorID, projectID); err != nil {
		return nil, err
	}
	if mode == "delete" {
		if err = validateSingleProjectRoleActorType(input); err != nil {
			return nil, err
		}
	}
	principals, err := resolveProjectRoleActors(ctx, tx, workspaceID, input, mode != "delete")
	if err != nil {
		return nil, err
	}
	if mode != "set" && len(principals) == 0 {
		return nil, fmt.Errorf("%w: provide at least one user or group", ErrProjectRoleValidation)
	}
	roleKey := strconv.FormatInt(roleID, 10)
	if mode == "set" {
		if _, err = tx.Exec(ctx, `DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=$1 AND role_key=$2`, projectID, roleKey); err != nil {
			return nil, err
		}
	}
	for _, principal := range principals {
		switch mode {
		case "add", "set":
			_, err = tx.Exec(ctx, `INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source) VALUES('project',$1,$2,$3,$4,'manual') ON CONFLICT DO NOTHING`, projectID, roleKey, principal.kind, principal.id)
		case "delete":
			command, deleteErr := tx.Exec(ctx, `DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=$1 AND role_key=$2 AND principal_type=$3 AND principal_id=$4`, projectID, roleKey, principal.kind, principal.id)
			err = deleteErr
			if deleteErr == nil && command.RowsAffected() == 0 {
				err = ErrProjectRoleNotFound
			}
		default:
			return nil, ErrProjectRoleValidation
		}
		if err != nil {
			return nil, err
		}
	}
	payload := map[string]any{"projectId": projectID, "roleId": roleID, "actors": input, "mode": mode}
	op := models.OpUpsert
	if mode == "delete" {
		op = models.OpDelete
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_role_actors", projectID+":"+roleKey, op, payload); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ProjectRoleActors(ctx, workspaceID, projectID, roleID, false)
}

func (s *Store) AddProjectRoleActors(ctx context.Context, workspaceID, actorID, projectIDOrKey string, roleID int64, input ProjectRoleActorInput) ([]models.ProjectRoleActor, error) {
	return s.mutateProjectRoleActors(ctx, workspaceID, actorID, projectIDOrKey, roleID, input, "add")
}

func (s *Store) SetProjectRoleActors(ctx context.Context, workspaceID, actorID, projectIDOrKey string, roleID int64, input ProjectRoleActorInput) ([]models.ProjectRoleActor, error) {
	return s.mutateProjectRoleActors(ctx, workspaceID, actorID, projectIDOrKey, roleID, input, "set")
}

func (s *Store) DeleteProjectRoleActors(ctx context.Context, workspaceID, actorID, projectIDOrKey string, roleID int64, input ProjectRoleActorInput) ([]models.ProjectRoleActor, error) {
	return s.mutateProjectRoleActors(ctx, workspaceID, actorID, projectIDOrKey, roleID, input, "delete")
}
