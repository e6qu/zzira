package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

type StatusUsage struct {
	Status      models.Status
	Issues      int
	Boards      int
	Workflows   int
	Automations int
}

func (u StatusUsage) Total() int { return u.Issues + u.Boards + u.Workflows + u.Automations }

func validateStatus(status models.Status) (models.Status, error) {
	status.Name = strings.TrimSpace(status.Name)
	status.Description = strings.TrimSpace(status.Description)
	if status.Name == "" || len(status.Name) > 255 {
		return status, fmt.Errorf("%w: status name is required (max 255 characters)", ErrAdminValidation)
	}
	if len(status.Description) > 1000 {
		return status, fmt.Errorf("%w: status description must be at most 1000 characters", ErrAdminValidation)
	}
	switch status.Category {
	case "new", "indeterminate", "done":
	default:
		return status, fmt.Errorf("%w: status category must be To do, In progress, or Done", ErrAdminValidation)
	}
	return status, nil
}

func statusNameAvailable(ctx context.Context, tx pgx.Tx, workspaceID, projectID, name, exceptID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('status:' || $1 || ':' || $2))`, workspaceID, projectID); err != nil {
		return err
	}
	return statusNameAvailableLocked(ctx, tx, workspaceID, projectID, name, exceptID)
}

func statusNameAvailableLocked(ctx context.Context, tx pgx.Tx, workspaceID, projectID, name, exceptID string) error {
	var duplicate bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM statuses
		 WHERE lower(name)=lower($3) AND id<>$4 AND (
		   ($2='' AND project_id IS NULL AND (workspace_id IS NULL OR workspace_id=$1)) OR
		   ($2<>'' AND workspace_id=$1 AND project_id=$2)))`,
		workspaceID, projectID, name, exceptID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return fmt.Errorf("%w: a visible status already uses that name", ErrAdminConflict)
	}
	return nil
}

func lockStatusScopes(ctx context.Context, tx pgx.Tx, workspaceID string, statuses []models.Status) error {
	scopes := make(map[string]bool)
	for _, status := range statuses {
		scopes[status.ProjectID] = true
	}
	ordered := make([]string, 0, len(scopes))
	for scope := range scopes {
		ordered = append(ordered, scope)
	}
	sort.Strings(ordered)
	for _, scope := range ordered {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('status:' || $1 || ':' || $2))`, workspaceID, scope); err != nil {
			return err
		}
	}
	return nil
}

func addStatusAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, statusID string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'status',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, statusID, encoded)
	return err
}

func (s *Store) CreateStatus(ctx context.Context, workspaceID, actorID string, status models.Status) (models.Status, error) {
	statuses, err := s.CreateStatuses(ctx, workspaceID, actorID, []models.Status{status})
	if err != nil {
		return status, err
	}
	return statuses[0], nil
}

// ErrAdminForbidden refuses an administrative change the caller may not make.
var ErrAdminForbidden = errors.New("admin forbidden")

// authorizeStatusScopes requires Administer Jira for global statuses and
// Administer Projects for statuses a project owns, as Jira does.
func authorizeStatusScopes(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, statuses []models.Status) error {
	checked := map[string]bool{}
	for _, status := range statuses {
		if checked[status.ProjectID] {
			continue
		}
		checked[status.ProjectID] = true
		var err error
		if status.ProjectID == "" {
			err = projectAdmin(ctx, tx, workspaceID, actorID)
		} else {
			err = projectAdministrator(ctx, tx, workspaceID, actorID, status.ProjectID)
		}
		if errors.Is(err, ErrProjectPermission) {
			return fmt.Errorf("%w: you do not have permission to manage these statuses", ErrAdminForbidden)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateStatuses(ctx context.Context, workspaceID, actorID string, statuses []models.Status) ([]models.Status, error) {
	if len(statuses) == 0 {
		return nil, fmt.Errorf("%w: at least one status is required", ErrAdminValidation)
	}
	validated := make([]models.Status, len(statuses))
	for index, status := range statuses {
		var err error
		validated[index], err = validateStatus(status)
		if err != nil {
			return nil, err
		}
		if validated[index].ID == "" {
			validated[index].ID = NewID("status")
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	projectIDs := make(map[string]bool)
	for _, status := range validated {
		if status.ProjectID != "" {
			projectIDs[status.ProjectID] = true
		}
	}
	for projectID := range projectIDs {
		var validProject bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND workspace_id=$2)`, projectID, workspaceID).Scan(&validProject); err != nil {
			return nil, err
		}
		if !validProject {
			return nil, fmt.Errorf("%w: project scope does not exist in this workspace", ErrAdminValidation)
		}
	}
	if err := authorizeStatusScopes(ctx, tx, workspaceID, actorID, validated); err != nil {
		return nil, err
	}
	if err := lockStatusScopes(ctx, tx, workspaceID, validated); err != nil {
		return nil, err
	}
	for index := range validated {
		status := &validated[index]
		if err := statusNameAvailableLocked(ctx, tx, workspaceID, status.ProjectID, status.Name, ""); err != nil {
			return nil, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO statuses(id,name,description,category,workspace_id,project_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING jira_id`, status.ID, status.Name, status.Description, status.Category, workspaceID, nilIfEmpty(status.ProjectID)).Scan(&status.JiraID); err != nil {
			if isUniqueViolation(err) {
				return nil, fmt.Errorf("%w: a status already uses that name or id", ErrAdminConflict)
			}
			return nil, err
		}
		if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.created", status.ID, map[string]any{"name": status.Name, "category": status.Category, "projectId": status.ProjectID}); err != nil {
			return nil, err
		}
		status.Protected = false
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return validated, nil
}

func (s *Store) UpdateStatus(ctx context.Context, workspaceID, actorID string, status models.Status) error {
	return s.UpdateStatuses(ctx, workspaceID, actorID, []models.Status{status})
}

func (s *Store) UpdateStatuses(ctx context.Context, workspaceID, actorID string, statuses []models.Status) error {
	if len(statuses) == 0 {
		return fmt.Errorf("%w: at least one status is required", ErrAdminValidation)
	}
	validated := make([]models.Status, len(statuses))
	seenIDs := make(map[string]bool, len(statuses))
	for index, status := range statuses {
		var err error
		validated[index], err = validateStatus(status)
		if err != nil {
			return err
		}
		if status.ID == "" {
			return fmt.Errorf("%w: status id is required", ErrAdminValidation)
		}
		if seenIDs[status.ID] {
			return fmt.Errorf("%w: status ids must be unique", ErrAdminValidation)
		}
		seenIDs[status.ID] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	orderedIDs := make([]string, 0, len(seenIDs))
	for id := range seenIDs {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Strings(orderedIDs)
	projectsByID := make(map[string]string, len(validated))
	for _, id := range orderedIDs {
		var owner, projectID sql.NullString
		if err := tx.QueryRow(ctx, `SELECT workspace_id,project_id FROM statuses WHERE id=$1`, id).Scan(&owner, &projectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAdminNotFound
			}
			return err
		}
		if !owner.Valid {
			return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
		}
		if owner.String != workspaceID {
			return ErrAdminNotFound
		}
		projectsByID[id] = projectID.String
	}
	for index := range validated {
		validated[index].ProjectID = projectsByID[validated[index].ID]
	}
	if err := authorizeStatusScopes(ctx, tx, workspaceID, actorID, validated); err != nil {
		return err
	}
	if err := lockStatusScopes(ctx, tx, workspaceID, validated); err != nil {
		return err
	}
	for _, id := range orderedIDs {
		var owner, projectID sql.NullString
		if err := tx.QueryRow(ctx, `SELECT workspace_id,project_id FROM statuses WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &projectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAdminNotFound
			}
			return err
		}
		if !owner.Valid {
			return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
		}
		if owner.String != workspaceID || projectID.String != projectsByID[id] {
			return ErrAdminNotFound
		}
	}
	targetNames := make(map[string]bool, len(validated))
	for _, status := range validated {
		key := status.ProjectID + "\x00" + strings.ToLower(status.Name)
		if targetNames[key] {
			return fmt.Errorf("%w: a visible status already uses that name", ErrAdminConflict)
		}
		targetNames[key] = true
		var duplicate bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM statuses
			 WHERE lower(name)=lower($3) AND NOT (id=ANY($4::text[])) AND (
			   ($2='' AND project_id IS NULL AND (workspace_id IS NULL OR workspace_id=$1)) OR
			   ($2<>'' AND workspace_id=$1 AND project_id=$2)))`,
			workspaceID, status.ProjectID, status.Name, orderedIDs).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return fmt.Errorf("%w: a visible status already uses that name", ErrAdminConflict)
		}
	}
	if len(validated) > 1 {
		for _, status := range validated {
			if _, err := tx.Exec(ctx, `UPDATE statuses SET name=$3 WHERE id=$1 AND workspace_id=$2`, status.ID, workspaceID, NewID("status_batch_tmp")); err != nil {
				return err
			}
		}
	}
	for _, status := range validated {
		tag, err := tx.Exec(ctx, `UPDATE statuses SET name=$3,description=$4,category=$5,updated_at=now() WHERE id=$1 AND workspace_id=$2`, status.ID, workspaceID, status.Name, status.Description, status.Category)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: a status already uses that name", ErrAdminConflict)
			}
			return err
		}
		if tag.RowsAffected() != 1 {
			return statusMutationNotFound(ctx, tx, status.ID)
		}
		if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.updated", status.ID, map[string]any{"name": status.Name, "category": status.Category, "projectId": status.ProjectID}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func statusMutationNotFound(ctx context.Context, tx pgx.Tx, statusID string) error {
	var owner sql.NullString
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM statuses WHERE id=$1`, statusID).Scan(&owner); err != nil {
		if err == pgx.ErrNoRows {
			return ErrAdminNotFound
		}
		return err
	}
	if !owner.Valid {
		return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
	}
	return ErrAdminNotFound
}

func statusUsage(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, workspaceID, statusID string) (StatusUsage, error) {
	var usage StatusUsage
	err := q.QueryRow(ctx, `
		SELECT st.id,st.name,st.description,st.category,COALESCE(st.project_id,''),st.workspace_id IS NULL,
		 (SELECT count(*) FROM issues i WHERE i.workspace_id=$1 AND i.status_id=st.id),
		 (SELECT count(*) FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1
		   AND EXISTS(SELECT 1 FROM jsonb_array_elements(b.board_columns) col WHERE col->'statusIds' ? st.id)),
		 (SELECT count(*) FROM workflows w WHERE (w.workspace_id=$1 OR (w.id='wf_default' AND w.workspace_id IS NULL)) AND (
		   EXISTS (SELECT 1 FROM jsonb_array_elements(w.def->'transitions') t WHERE t->>'to'=st.id OR (t->'from') ? st.id)
		   OR EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(w.draft_def,w.def)->'transitions') t WHERE t->>'to'=st.id OR (t->'from') ? st.id))),
		 (SELECT count(*) FROM automation_rules a WHERE a.workspace_id=$1 AND
		   jsonb_path_exists(a.payload, '$.**.statusId ? (@ == $status)', jsonb_build_object('status',to_jsonb(st.id))))
		FROM statuses st WHERE st.id=$2 AND (st.workspace_id IS NULL OR st.workspace_id=$1)`, workspaceID, statusID).
		Scan(&usage.Status.ID, &usage.Status.Name, &usage.Status.Description, &usage.Status.Category, &usage.Status.ProjectID, &usage.Status.Protected,
			&usage.Issues, &usage.Boards, &usage.Workflows, &usage.Automations)
	return usage, err
}

func (s *Store) StatusUsage(ctx context.Context, workspaceID, statusID string) (StatusUsage, error) {
	return statusUsage(ctx, s.Pool, workspaceID, statusID)
}

func (s *Store) StatusDirectory(ctx context.Context, workspaceID string) ([]StatusUsage, error) {
	statuses, err := s.StatusesForAdministration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]StatusUsage, 0, len(statuses))
	for _, status := range statuses {
		usage, err := s.StatusUsage(ctx, workspaceID, status.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, usage)
	}
	return out, nil
}

func (s *Store) DeleteStatus(ctx context.Context, workspaceID, actorID, statusID string) error {
	return s.DeleteStatuses(ctx, workspaceID, actorID, []string{statusID})
}

func (s *Store) DeleteStatuses(ctx context.Context, workspaceID, actorID string, statusIDs []string) error {
	if len(statusIDs) == 0 {
		return fmt.Errorf("%w: at least one status id is required", ErrAdminValidation)
	}
	seenIDs := make(map[string]bool, len(statusIDs))
	for _, id := range statusIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: status id is required", ErrAdminValidation)
		}
		if seenIDs[id] {
			return fmt.Errorf("%w: status ids must be unique", ErrAdminValidation)
		}
		seenIDs[id] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	orderedIDs := append([]string(nil), statusIDs...)
	sort.Strings(orderedIDs)
	scopes := make([]models.Status, 0, len(orderedIDs))
	for _, statusID := range orderedIDs {
		var owner, projectID sql.NullString
		if err := tx.QueryRow(ctx, `SELECT workspace_id,project_id FROM statuses WHERE id=$1 FOR UPDATE`, statusID).Scan(&owner, &projectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAdminNotFound
			}
			return err
		}
		if !owner.Valid {
			return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
		}
		if owner.String != workspaceID {
			return ErrAdminNotFound
		}
		scopes = append(scopes, models.Status{ID: statusID, ProjectID: projectID.String})
	}
	if err := authorizeStatusScopes(ctx, tx, workspaceID, actorID, scopes); err != nil {
		return err
	}
	usages := make(map[string]StatusUsage, len(statusIDs))
	for _, statusID := range statusIDs {
		usage, err := statusUsage(ctx, tx, workspaceID, statusID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAdminNotFound
			}
			return err
		}
		if usage.Total() != 0 {
			return fmt.Errorf("%w: status is still used by issues, boards, workflows, or automation", ErrAdminConflict)
		}
		usages[statusID] = usage
	}
	for _, statusID := range statusIDs {
		tag, err := tx.Exec(ctx, `DELETE FROM statuses WHERE id=$1 AND workspace_id=$2`, statusID, workspaceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAdminNotFound
		}
		if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.deleted", statusID, map[string]any{"name": usages[statusID].Status.Name}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// statusWorkflowContains is the SQL deciding whether a workflow definition,
// published or draft, reaches the status in $2.
const statusWorkflowContains = `(EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(%[1]s.def->'transitions','[]'::jsonb)) t WHERE t->>'to'=$2 OR (t->'from') ? $2)
	OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(%[1]s.draft_def->'transitions','[]'::jsonb)) t WHERE t->>'to'=$2 OR (t->'from') ? $2))`

// projectWorkflowIDs lists, for the project aliased p, every workflow its
// work can use: the workflow scheme's default and type mappings, published
// and draft, or the project's own workflow.
const projectWorkflowIDs = `(SELECT COALESCE(p.workflow_id,'wf_default') UNION
	SELECT ws.default_workflow_id FROM workflow_schemes ws WHERE ws.id=p.workflow_scheme_id UNION
	SELECT mapping.value FROM workflow_schemes ws, jsonb_each_text(ws.issue_type_mappings) mapping WHERE ws.id=p.workflow_scheme_id UNION
	SELECT ws.draft_def->>'defaultWorkflowId' FROM workflow_schemes ws WHERE ws.id=p.workflow_scheme_id AND ws.draft_def IS NOT NULL UNION
	SELECT mapping.value FROM workflow_schemes ws, jsonb_each_text(COALESCE(ws.draft_def->'issueTypeMappings','{}'::jsonb)) mapping WHERE ws.id=p.workflow_scheme_id)`

// StatusProjectUsages lists the projects using a status: through their work,
// their boards, or a workflow their workflow scheme or project assigns.
func (s *Store) StatusProjectUsages(ctx context.Context, workspaceID, statusID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT p.id FROM projects p WHERE p.workspace_id=$1 AND (
		 EXISTS(SELECT 1 FROM issues i WHERE i.project_id=p.id AND i.status_id=$2)
		 OR EXISTS(SELECT 1 FROM boards b WHERE b.project_id=p.id
		   AND EXISTS(SELECT 1 FROM jsonb_array_elements(b.board_columns) col WHERE col->'statusIds' ? $2))
		 OR EXISTS(SELECT 1 FROM workflows w WHERE w.id IN `+projectWorkflowIDs+` AND `+fmt.Sprintf(statusWorkflowContains, "w")+`)
		) ORDER BY p.id`, workspaceID, statusID)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

// StatusWorkflowUsages lists every workflow of the site whose published or
// draft definition reaches the status, whether or not a project uses it.
func (s *Store) StatusWorkflowUsages(ctx context.Context, workspaceID, statusID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT w.id FROM workflows w
		WHERE (w.workspace_id=$1 OR (w.id='wf_default' AND w.workspace_id IS NULL)) AND `+fmt.Sprintf(statusWorkflowContains, "w")+`
		ORDER BY w.id`, workspaceID, statusID)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

func collectIDs(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) StatusProjectIssueTypeUsages(ctx context.Context, workspaceID, projectID, statusID string) ([]string, error) {
	// A work type uses the status when work of that type is in it, or when the
	// workflow the project's scheme assigns the type reaches it.
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT it.id FROM issue_types it JOIN projects p ON p.id=$3 AND p.workspace_id=$1
		LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id AND ws.workspace_id=p.workspace_id
		WHERE EXISTS(SELECT 1 FROM issues i WHERE i.project_id=p.id AND i.issuetype_id=it.id AND i.status_id=$2)
		   OR EXISTS(SELECT 1 FROM workflows w WHERE w.id=COALESCE(ws.issue_type_mappings->>it.id,ws.default_workflow_id,p.workflow_id,'wf_default')
		      AND `+fmt.Sprintf(statusWorkflowContains, "w")+`
		      AND (it.workspace_id IS NULL OR it.workspace_id=p.workspace_id))
		ORDER BY it.id`, workspaceID, statusID, projectID)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}
