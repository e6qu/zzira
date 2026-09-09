package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrProjectCategoryConflict = errors.New("a project category with this name already exists")
	ErrProjectCategoryInvalid  = errors.New("project category does not exist in this workspace")
	ErrProjectFeatureInvalid   = errors.New("project feature does not exist")
	ErrProjectFeatureWrongType = errors.New("project features are available only for software projects")
)

var projectFeatureCatalog = []models.ProjectFeature{
	{Key: "jsw.classic.roadmap", Name: "Roadmap", Description: "Plan work across a project timeline.", State: "ENABLED", Prerequisites: []string{}},
	{Key: "jsw.classic.backlog", Name: "Backlog", Description: "Prioritize upcoming work before it enters a board.", State: "ENABLED", Prerequisites: []string{}},
	{Key: "jsw.classic.sprints", Name: "Sprints", Description: "Plan and deliver work in time-boxed iterations.", State: "ENABLED", Prerequisites: []string{"jsw.classic.backlog"}},
	{Key: "jsw.classic.reports", Name: "Reports", Description: "Inspect project flow, delivery, and trends.", State: "ENABLED", Prerequisites: []string{}},
	{Key: "jsw.classic.deployments", Name: "Deployments", Description: "Connect delivery work with deployment evidence.", State: "ENABLED", Prerequisites: []string{}},
	{Key: "jsw.classic.code", Name: "Code", Description: "Connect work with branches, commits, and pull requests.", State: "ENABLED", Prerequisites: []string{}},
}

func ProjectFeatureCatalog() []models.ProjectFeature {
	out := make([]models.ProjectFeature, len(projectFeatureCatalog))
	copy(out, projectFeatureCatalog)
	return out
}

func appendProjectGovernanceAction(ctx context.Context, tx pgx.Tx, workspaceID, actorID, entityType, entityID, op string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: entityType, EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
}

func scanProjectCategory(row pgx.Row) (*models.ProjectCategory, error) {
	c := &models.ProjectCategory{}
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Description)
	return c, err
}

func (s *Store) ProjectCategories(ctx context.Context, workspaceID string) ([]*models.ProjectCategory, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,workspace_id,name,description FROM project_categories WHERE workspace_id=$1 ORDER BY lower(name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.ProjectCategory{}
	for rows.Next() {
		c, err := scanProjectCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ProjectCategory(ctx context.Context, workspaceID, id string) (*models.ProjectCategory, error) {
	return scanProjectCategory(s.Pool.QueryRow(ctx, `SELECT id,workspace_id,name,description FROM project_categories WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
}

func (s *Store) CreateProjectCategory(ctx context.Context, workspaceID, actorID, name, description string) (*models.ProjectCategory, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	c, err := scanProjectCategory(tx.QueryRow(ctx, `INSERT INTO project_categories(workspace_id,name,description) VALUES($1,$2,$3) RETURNING id,workspace_id,name,description`, workspaceID, strings.TrimSpace(name), description))
	if isUniqueViolation(err) {
		return nil, ErrProjectCategoryConflict
	}
	if err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_category", c.ID, models.OpUpsert, c); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) UpdateProjectCategory(ctx context.Context, workspaceID, actorID, id string, name, description *string) (*models.ProjectCategory, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if name != nil {
		v := strings.TrimSpace(*name)
		name = &v
	}
	c, err := scanProjectCategory(tx.QueryRow(ctx, `UPDATE project_categories SET name=COALESCE($3,name),description=COALESCE($4,description),updated_at=now() WHERE workspace_id=$1 AND id=$2 RETURNING id,workspace_id,name,description`, workspaceID, id, name, description))
	if isUniqueViolation(err) {
		return nil, ErrProjectCategoryConflict
	}
	if err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_category", c.ID, models.OpUpsert, c); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) DeleteProjectCategory(ctx context.Context, workspaceID, actorID, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	c, err := scanProjectCategory(tx.QueryRow(ctx, `SELECT id,workspace_id,name,description FROM project_categories WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, id))
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE projects SET category_id=NULL WHERE workspace_id=$1 AND category_id=$2 RETURNING id,workspace_id,key,name,COALESCE(workflow_id,''),COALESCE(security_scheme_id,''),description,url,COALESCE(lead_account_id,''),assignee_type,project_type_key,COALESCE(category_id,''),sender_email`, workspaceID, id)
	if err != nil {
		return err
	}
	var projects []*models.Project
	for rows.Next() {
		p := &models.Project{}
		if err = rows.Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType, &p.ProjectTypeKey, &p.CategoryID, &p.SenderEmail); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range projects {
		if err = writeProjectAction(ctx, tx, actorID, p); err != nil {
			return err
		}
	}
	if command, err := tx.Exec(ctx, `DELETE FROM project_categories WHERE workspace_id=$1 AND id=$2`, workspaceID, id); err != nil {
		return err
	} else if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_category", c.ID, models.OpDelete, c); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ProjectProperties(ctx context.Context, workspaceID, projectIDOrKey string) ([]models.ProjectProperty, error) {
	p, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT project_id,property_key,value FROM project_properties WHERE project_id=$1 ORDER BY property_key`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ProjectProperty{}
	for rows.Next() {
		var property models.ProjectProperty
		if err = rows.Scan(&property.ProjectID, &property.Key, &property.Value); err != nil {
			return nil, err
		}
		out = append(out, property)
	}
	return out, rows.Err()
}

func (s *Store) ProjectProperty(ctx context.Context, workspaceID, projectIDOrKey, key string) (*models.ProjectProperty, error) {
	p, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, err
	}
	property := &models.ProjectProperty{}
	err = s.Pool.QueryRow(ctx, `SELECT project_id,property_key,value FROM project_properties WHERE project_id=$1 AND property_key=$2`, p.ID, key).Scan(&property.ProjectID, &property.Key, &property.Value)
	return property, err
}

func (s *Store) SetProjectProperty(ctx context.Context, workspaceID, actorID, projectIDOrKey, key string, value json.RawMessage) (*models.ProjectProperty, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, false, err
	}
	var projectID string
	if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR SHARE`, workspaceID, projectIDOrKey).Scan(&projectID); err != nil {
		return nil, false, err
	}
	var existed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_properties WHERE project_id=$1 AND property_key=$2)`, projectID, key).Scan(&existed); err != nil {
		return nil, false, err
	}
	property := &models.ProjectProperty{ProjectID: projectID, Key: key, Value: value}
	if _, err = tx.Exec(ctx, `INSERT INTO project_properties(project_id,property_key,value) VALUES($1,$2,$3) ON CONFLICT(project_id,property_key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, projectID, key, value); err != nil {
		return nil, false, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_property", projectID+":"+key, models.OpUpsert, property); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return property, !existed, nil
}

func (s *Store) DeleteProjectProperty(ctx context.Context, workspaceID, actorID, projectIDOrKey, key string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var property models.ProjectProperty
	if err = tx.QueryRow(ctx, `SELECT pp.project_id,pp.property_key,pp.value FROM project_properties pp JOIN projects p ON p.id=pp.project_id WHERE p.workspace_id=$1 AND (p.id=$2 OR upper(p.key)=upper($2)) AND pp.property_key=$3 FOR UPDATE`, workspaceID, projectIDOrKey, key).Scan(&property.ProjectID, &property.Key, &property.Value); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM project_properties WHERE project_id=$1 AND property_key=$2`, property.ProjectID, key); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_property", property.ProjectID+":"+key, models.OpDelete, &property); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ProjectFeatures(ctx context.Context, workspaceID, projectIDOrKey string) ([]models.ProjectFeature, error) {
	p, err := s.ProjectByIDOrKey(ctx, workspaceID, projectIDOrKey)
	if err != nil {
		return nil, err
	}
	if p.ProjectTypeKey != "software" {
		return nil, ErrProjectFeatureWrongType
	}
	states := map[string]string{}
	rows, err := s.Pool.Query(ctx, `SELECT feature_key,state FROM project_features WHERE project_id=$1`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, state string
		if err = rows.Scan(&key, &state); err != nil {
			return nil, err
		}
		states[key] = state
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := ProjectFeatureCatalog()
	for i := range out {
		if state := states[out[i].Key]; state != "" {
			out[i].State = state
		}
	}
	return out, nil
}

func (s *Store) SetProjectFeature(ctx context.Context, workspaceID, actorID, projectIDOrKey, key, state string) ([]models.ProjectFeature, error) {
	known := false
	for _, feature := range projectFeatureCatalog {
		if feature.Key == key {
			known = true
			break
		}
	}
	if !known {
		return nil, ErrProjectFeatureInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	var projectID, projectType string
	if err = tx.QueryRow(ctx, `SELECT id,project_type_key FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR SHARE`, workspaceID, projectIDOrKey).Scan(&projectID, &projectType); err != nil {
		return nil, err
	}
	if projectType != "software" {
		return nil, ErrProjectFeatureWrongType
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_features(project_id,feature_key,state) VALUES($1,$2,$3) ON CONFLICT(project_id,feature_key) DO UPDATE SET state=EXCLUDED.state,updated_at=now()`, projectID, key, state); err != nil {
		return nil, err
	}
	value := map[string]string{"projectId": projectID, "feature": key, "state": state}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_feature", projectID+":"+key, models.OpUpsert, value); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ProjectFeatures(ctx, workspaceID, projectID)
}

func (s *Store) SetProjectEmail(ctx context.Context, workspaceID, actorID, projectIDOrKey, email string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	p := &models.Project{}
	err = tx.QueryRow(ctx, `UPDATE projects SET sender_email=$3 WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) RETURNING id,workspace_id,key,name,COALESCE(workflow_id,''),COALESCE(security_scheme_id,''),description,url,COALESCE(lead_account_id,''),assignee_type,project_type_key,COALESCE(category_id,''),sender_email`, workspaceID, projectIDOrKey, email).Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType, &p.ProjectTypeKey, &p.CategoryID, &p.SenderEmail)
	if err != nil {
		return err
	}
	if err = writeProjectAction(ctx, tx, actorID, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
