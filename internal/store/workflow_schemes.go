package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

type WorkflowSchemeImpact struct {
	IssueTypeID    string
	Status         models.Status
	IssueCount     int
	TargetWorkflow workflow.Workflow
}

type WorkflowStatusMapping struct {
	IssueTypeID string
	OldStatusID string
	NewStatusID string
}

type workflowSchemeDef struct {
	DefaultWorkflowID string            `json:"defaultWorkflowId"`
	IssueTypeMappings map[string]string `json:"issueTypeMappings"`
}

type workflowSchemeQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func addWorkflowSchemeAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, schemeID string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'workflow_scheme',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, schemeID, encoded)
	return err
}

func validateScheme(scheme workflow.Scheme) (workflow.Scheme, error) {
	scheme.Name = strings.TrimSpace(scheme.Name)
	scheme.Description = strings.TrimSpace(scheme.Description)
	if scheme.Name == "" || len(scheme.Name) > 255 {
		return scheme, fmt.Errorf("%w: scheme name is required (max 255 characters)", ErrAdminValidation)
	}
	if len(scheme.Description) > 1000 {
		return scheme, fmt.Errorf("%w: scheme description must be at most 1000 characters", ErrAdminValidation)
	}
	if scheme.DefaultWorkflowID == "" {
		scheme.DefaultWorkflowID = workflow.Default().ID
	}
	if scheme.IssueTypeMappings == nil {
		scheme.IssueTypeMappings = map[string]string{}
	}
	return scheme, nil
}

func validateSchemeWorkflows(ctx context.Context, q workflowSchemeQuerier, workspaceID string, scheme workflow.Scheme) error {
	workflowIDs := map[string]bool{scheme.DefaultWorkflowID: true}
	for issueTypeID, workflowID := range scheme.IssueTypeMappings {
		if strings.TrimSpace(issueTypeID) == "" || strings.TrimSpace(workflowID) == "" {
			return fmt.Errorf("%w: issue type and workflow mappings cannot be empty", ErrAdminValidation)
		}
		workflowIDs[workflowID] = true
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_types WHERE id=$1)`, issueTypeID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: issue type %q does not exist", ErrAdminValidation, issueTypeID)
		}
	}
	for workflowID := range workflowIDs {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE id=$1 AND project_id IS NULL AND (workspace_id=$2 OR (id='wf_default' AND workspace_id IS NULL)))`, workflowID, workspaceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: workflow %q does not exist in this workspace", ErrAdminValidation, workflowID)
		}
	}
	return nil
}

func (s *Store) CreateWorkflowScheme(ctx context.Context, workspaceID, actorID string, scheme workflow.Scheme) (workflow.Scheme, error) {
	scheme, err := validateScheme(scheme)
	if err != nil {
		return scheme, err
	}
	if scheme.ID == "" {
		scheme.ID = NewID("scheme")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return scheme, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validateSchemeWorkflows(ctx, tx, workspaceID, scheme); err != nil {
		return scheme, err
	}
	mappings, err := json.Marshal(scheme.IssueTypeMappings)
	if err != nil {
		return scheme, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflow_schemes(id,workspace_id,name,description,default_workflow_id,issue_type_mappings) VALUES($1,$2,$3,$4,$5,$6)`, scheme.ID, workspaceID, scheme.Name, scheme.Description, scheme.DefaultWorkflowID, mappings)
	if err != nil {
		if isUniqueViolation(err) {
			return scheme, fmt.Errorf("%w: a workflow scheme already uses that name or id", ErrAdminConflict)
		}
		return scheme, err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.created", scheme.ID, map[string]any{"name": scheme.Name}); err != nil {
		return scheme, err
	}
	scheme.Version = 1
	return scheme, tx.Commit(ctx)
}

func scanWorkflowScheme(row pgx.Row, useDraft bool) (workflow.Scheme, error) {
	var scheme workflow.Scheme
	var mappings []byte
	var draft []byte
	err := row.Scan(&scheme.ID, &scheme.Name, &scheme.Description, &scheme.DefaultWorkflowID, &mappings, &draft, &scheme.Version, &scheme.HasDraft)
	if err != nil {
		return scheme, err
	}
	if useDraft && scheme.HasDraft && len(draft) > 0 {
		var def workflowSchemeDef
		if err := json.Unmarshal(draft, &def); err != nil {
			return scheme, err
		}
		scheme.DefaultWorkflowID = def.DefaultWorkflowID
		scheme.IssueTypeMappings = def.IssueTypeMappings
	} else if err := json.Unmarshal(mappings, &scheme.IssueTypeMappings); err != nil {
		return scheme, err
	}
	return scheme, nil
}

func (s *Store) WorkflowSchemeByID(ctx context.Context, workspaceID, schemeID string, useDraft bool) (workflow.Scheme, error) {
	return scanWorkflowScheme(s.Pool.QueryRow(ctx, `SELECT id,name,description,default_workflow_id,issue_type_mappings,COALESCE(draft_def,'null'),version,draft_def IS NOT NULL FROM workflow_schemes WHERE id=$1 AND workspace_id=$2`, schemeID, workspaceID), useDraft)
}

func (s *Store) ListWorkflowSchemes(ctx context.Context, workspaceID string) ([]workflow.Scheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,description,default_workflow_id,issue_type_mappings,COALESCE(draft_def,'null'),version,draft_def IS NOT NULL FROM workflow_schemes WHERE workspace_id=$1 ORDER BY lower(name),id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schemes []workflow.Scheme
	for rows.Next() {
		scheme, err := scanWorkflowScheme(rows, false)
		if err != nil {
			return nil, err
		}
		schemes = append(schemes, scheme)
	}
	return schemes, rows.Err()
}

func (s *Store) SaveWorkflowSchemeDraft(ctx context.Context, workspaceID, actorID string, scheme workflow.Scheme) error {
	scheme, err := validateScheme(scheme)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validateSchemeWorkflows(ctx, tx, workspaceID, scheme); err != nil {
		return err
	}
	def, err := json.Marshal(workflowSchemeDef{DefaultWorkflowID: scheme.DefaultWorkflowID, IssueTypeMappings: scheme.IssueTypeMappings})
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE workflow_schemes SET name=$3,description=$4,draft_def=$5,updated_at=now() WHERE id=$1 AND workspace_id=$2`, scheme.ID, workspaceID, scheme.Name, scheme.Description, def)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: a workflow scheme already uses that name", ErrAdminConflict)
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAdminNotFound
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.draft.saved", scheme.ID, map[string]any{"name": scheme.Name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateWorkflowSchemeDraft(ctx context.Context, workspaceID, actorID, schemeID string) (workflow.Scheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return workflow.Scheme{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scheme, err := scanWorkflowScheme(tx.QueryRow(ctx, `SELECT id,name,description,default_workflow_id,issue_type_mappings,COALESCE(draft_def,'null'),version,draft_def IS NOT NULL FROM workflow_schemes WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, schemeID, workspaceID), false)
	if err != nil {
		return workflow.Scheme{}, ErrAdminNotFound
	}
	if scheme.HasDraft {
		return workflow.Scheme{}, fmt.Errorf("%w: the workflow scheme already has a draft", ErrAdminConflict)
	}
	def, err := json.Marshal(workflowSchemeDef{DefaultWorkflowID: scheme.DefaultWorkflowID, IssueTypeMappings: scheme.IssueTypeMappings})
	if err != nil {
		return workflow.Scheme{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_schemes SET draft_def=$3,updated_at=now() WHERE id=$1 AND workspace_id=$2`, schemeID, workspaceID, def); err != nil {
		return workflow.Scheme{}, err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.draft.created", schemeID, nil); err != nil {
		return workflow.Scheme{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workflow.Scheme{}, err
	}
	scheme.HasDraft = true
	return scheme, nil
}

func (s *Store) SavePublishedWorkflowScheme(ctx context.Context, workspaceID, actorID string, scheme workflow.Scheme) error {
	return s.savePublishedWorkflowScheme(ctx, workspaceID, actorID, scheme, nil, 0, "")
}

func (s *Store) UpdatePublishedWorkflowSchemeTask(ctx context.Context, workspaceID, actorID string, scheme workflow.Scheme, mappings []WorkflowStatusMapping, expectedVersion int) (APITask, error) {
	validated, err := validateScheme(scheme)
	if err != nil {
		return APITask{}, err
	}
	if err := s.ValidateWorkflowSchemeDefinition(ctx, workspaceID, validated); err != nil {
		return APITask{}, err
	}
	var currentVersion int
	if err := s.Pool.QueryRow(ctx, `SELECT version FROM workflow_schemes WHERE id=$1 AND workspace_id=$2`, validated.ID, workspaceID).Scan(&currentVersion); err != nil {
		return APITask{}, ErrAdminNotFound
	}
	if currentVersion != expectedVersion {
		return APITask{}, fmt.Errorf("%w: workflow scheme version is %d, expected %d", ErrAdminConflict, currentVersion, expectedVersion)
	}
	var nameConflict bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_schemes WHERE workspace_id=$1 AND id<>$2 AND lower(name)=lower($3))`, workspaceID, validated.ID, validated.Name).Scan(&nameConflict); err != nil {
		return APITask{}, err
	}
	if nameConflict {
		return APITask{}, fmt.Errorf("%w: a workflow scheme already uses that name", ErrAdminConflict)
	}
	projectRows, err := s.Pool.Query(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND workflow_scheme_id=$2 ORDER BY id`, workspaceID, validated.ID)
	if err != nil {
		return APITask{}, err
	}
	var projectIDs []string
	for projectRows.Next() {
		var projectID string
		if err := projectRows.Scan(&projectID); err != nil {
			projectRows.Close()
			return APITask{}, err
		}
		projectIDs = append(projectIDs, projectID)
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return APITask{}, err
	}
	projectRows.Close()
	for _, projectID := range projectIDs {
		impacts, err := s.WorkflowSchemeDefinitionImpact(ctx, workspaceID, projectID, validated)
		if err != nil {
			return APITask{}, err
		}
		if err := validateWorkflowSchemeImpactMappings(impacts, mappings); err != nil {
			return APITask{}, err
		}
	}
	task, err := queuedAPITask(workspaceID, actorID, "Update workflow scheme", apiTaskUpdateWorkflowScheme, updateWorkflowSchemeTaskPayload{Scheme: validated, StatusMappings: mappings, ExpectedVersion: expectedVersion})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

func (s *Store) savePublishedWorkflowScheme(ctx context.Context, workspaceID, actorID string, scheme workflow.Scheme, statusMappings []WorkflowStatusMapping, expectedVersion int, taskID string) error {
	scheme, err := validateScheme(scheme)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validateSchemeWorkflows(ctx, tx, workspaceID, scheme); err != nil {
		return err
	}
	var currentVersion int
	if err := tx.QueryRow(ctx, `SELECT version FROM workflow_schemes WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, scheme.ID, workspaceID).Scan(&currentVersion); err != nil {
		return ErrAdminNotFound
	}
	if expectedVersion > 0 && currentVersion != expectedVersion {
		return fmt.Errorf("%w: workflow scheme version is %d, expected %d", ErrAdminConflict, currentVersion, expectedVersion)
	}
	projectRows, err := tx.Query(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND workflow_scheme_id=$2 ORDER BY id`, workspaceID, scheme.ID)
	if err != nil {
		return err
	}
	var projectIDs []string
	for projectRows.Next() {
		var projectID string
		if err := projectRows.Scan(&projectID); err != nil {
			projectRows.Close()
			return err
		}
		projectIDs = append(projectIDs, projectID)
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return err
	}
	projectRows.Close()
	migrated := 0
	for _, projectID := range projectIDs {
		projectMigrated := 0
		if err := migrateWorkflowSchemeIssues(ctx, tx, workspaceID, actorID, projectID, scheme, statusMappings, &projectMigrated); err != nil {
			return err
		}
		migrated += projectMigrated
	}
	mappings, err := json.Marshal(scheme.IssueTypeMappings)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE workflow_schemes SET name=$3,description=$4,default_workflow_id=$5,issue_type_mappings=$6,version=version+1,updated_at=now() WHERE id=$1 AND workspace_id=$2`, scheme.ID, workspaceID, scheme.Name, scheme.Description, scheme.DefaultWorkflowID, mappings)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: a workflow scheme already uses that name", ErrAdminConflict)
		}
		return err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.updated", scheme.ID, map[string]any{"version": currentVersion + 1, "migratedIssues": migrated}); err != nil {
		return err
	}
	if err := completeAPITask(ctx, tx, workspaceID, taskID, "Workflow scheme updated.", map[string]any{"workflowSchemeId": scheme.ID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func schemeWorkflowID(scheme workflow.Scheme, issueTypeID string) string {
	if workflowID := scheme.IssueTypeMappings[issueTypeID]; workflowID != "" {
		return workflowID
	}
	return scheme.DefaultWorkflowID
}

func workflowByIDQuery(ctx context.Context, q workflowSchemeQuerier, workspaceID, workflowID string) (workflow.Workflow, error) {
	var def []byte
	var projectID string
	if err := q.QueryRow(ctx, `SELECT def,COALESCE(project_id,'') FROM workflows WHERE id=$1 AND (workspace_id=$2 OR (id='wf_default' AND workspace_id IS NULL))`, workflowID, workspaceID).Scan(&def, &projectID); err != nil {
		return workflow.Workflow{}, err
	}
	var result workflow.Workflow
	if err := json.Unmarshal(def, &result); err != nil {
		return result, err
	}
	result.ProjectID = projectID
	return result, nil
}

func workflowStatuses(wf workflow.Workflow) map[string]bool {
	statuses := make(map[string]bool)
	for _, transition := range wf.Transitions {
		statuses[transition.To] = true
		for _, from := range transition.From {
			statuses[from] = true
		}
	}
	return statuses
}

func schemeImpact(ctx context.Context, q workflowSchemeQuerier, workspaceID, projectID string, scheme workflow.Scheme, lock bool) ([]WorkflowSchemeImpact, error) {
	query := `SELECT issuetype_id,status_id,count(*)::int FROM issues WHERE workspace_id=$1 AND project_id=$2 GROUP BY issuetype_id,status_id ORDER BY issuetype_id,status_id`
	if lock {
		query = `SELECT issuetype_id,status_id,1 FROM issues WHERE workspace_id=$1 AND project_id=$2 ORDER BY issuetype_id,status_id FOR UPDATE`
	}
	rows, err := q.Query(ctx, query, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pair struct{ issueType, status string }
	counts := make(map[pair]int)
	for rows.Next() {
		var item pair
		var count int
		if err := rows.Scan(&item.issueType, &item.status, &count); err != nil {
			return nil, err
		}
		counts[item] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	workflows := make(map[string]workflow.Workflow)
	var impacts []WorkflowSchemeImpact
	for item, count := range counts {
		workflowID := schemeWorkflowID(scheme, item.issueType)
		wf, ok := workflows[workflowID]
		if !ok {
			wf, err = workflowByIDQuery(ctx, q, workspaceID, workflowID)
			if err != nil {
				return nil, err
			}
			workflows[workflowID] = wf
		}
		if workflowStatuses(wf)[item.status] {
			continue
		}
		status := models.Status{ID: item.status}
		_ = q.QueryRow(ctx, `SELECT name,description,category,COALESCE(project_id,''),workspace_id IS NULL FROM statuses WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2) AND (project_id IS NULL OR project_id=$3)`, item.status, workspaceID, projectID).Scan(&status.Name, &status.Description, &status.Category, &status.ProjectID, &status.Protected)
		impacts = append(impacts, WorkflowSchemeImpact{IssueTypeID: item.issueType, Status: status, IssueCount: count, TargetWorkflow: wf})
	}
	return impacts, nil
}

func validateWorkflowSchemeImpactMappings(impacts []WorkflowSchemeImpact, mappings []WorkflowStatusMapping) error {
	replacements := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		if mapping.IssueTypeID == "" || mapping.OldStatusID == "" || mapping.NewStatusID == "" {
			return fmt.Errorf("%w: every status mapping requires issueTypeId, oldStatusId, and newStatusId", ErrAdminValidation)
		}
		key := mapping.IssueTypeID + "\x00" + mapping.OldStatusID
		if existing := replacements[key]; existing != "" && existing != mapping.NewStatusID {
			return fmt.Errorf("%w: status mappings cannot provide conflicting replacements", ErrAdminValidation)
		}
		replacements[key] = mapping.NewStatusID
	}
	for _, impact := range impacts {
		newStatusID := replacements[impact.IssueTypeID+"\x00"+impact.Status.ID]
		if newStatusID == "" {
			return fmt.Errorf("%w: status mapping required for issue type %s and status %s", ErrAdminConflict, impact.IssueTypeID, impact.Status.ID)
		}
		if !workflowStatuses(impact.TargetWorkflow)[newStatusID] {
			return fmt.Errorf("%w: replacement status %s is not in target workflow %s", ErrAdminValidation, newStatusID, impact.TargetWorkflow.Name)
		}
	}
	return nil
}

func (s *Store) WorkflowSchemeImpact(ctx context.Context, workspaceID, projectID, schemeID string, useDraft bool) ([]WorkflowSchemeImpact, error) {
	scheme, err := s.WorkflowSchemeByID(ctx, workspaceID, schemeID, useDraft)
	if err != nil {
		return nil, err
	}
	return schemeImpact(ctx, s.Pool, workspaceID, projectID, scheme, false)
}

func (s *Store) WorkflowSchemeDefinitionImpact(ctx context.Context, workspaceID, projectID string, scheme workflow.Scheme) ([]WorkflowSchemeImpact, error) {
	scheme, err := validateScheme(scheme)
	if err != nil {
		return nil, err
	}
	if err := validateSchemeWorkflows(ctx, s.Pool, workspaceID, scheme); err != nil {
		return nil, err
	}
	return schemeImpact(ctx, s.Pool, workspaceID, projectID, scheme, false)
}

func (s *Store) ValidateWorkflowSchemeDefinition(ctx context.Context, workspaceID string, scheme workflow.Scheme) error {
	scheme, err := validateScheme(scheme)
	if err != nil {
		return err
	}
	return validateSchemeWorkflows(ctx, s.Pool, workspaceID, scheme)
}

func (s *Store) AssignWorkflowScheme(ctx context.Context, workspaceID, actorID, projectID, schemeID string) error {
	return s.SwitchWorkflowScheme(ctx, workspaceID, actorID, projectID, schemeID, nil)
}

func (s *Store) SwitchWorkflowScheme(ctx context.Context, workspaceID, actorID, projectID, schemeID string, mappings []WorkflowStatusMapping) error {
	return s.switchWorkflowScheme(ctx, workspaceID, actorID, projectID, schemeID, mappings, "")
}

func (s *Store) SwitchWorkflowSchemeTask(ctx context.Context, workspaceID, actorID, projectID, schemeID string, mappings []WorkflowStatusMapping) (APITask, error) {
	var projectExists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND workspace_id=$2)`, projectID, workspaceID).Scan(&projectExists); err != nil {
		return APITask{}, err
	}
	if !projectExists {
		return APITask{}, ErrAdminNotFound
	}
	impacts, err := s.WorkflowSchemeImpact(ctx, workspaceID, projectID, schemeID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return APITask{}, ErrAdminNotFound
	}
	if err != nil {
		return APITask{}, err
	}
	if err := validateWorkflowSchemeImpactMappings(impacts, mappings); err != nil {
		return APITask{}, err
	}
	task, err := queuedAPITask(workspaceID, actorID, "Switch project workflow scheme", apiTaskSwitchWorkflowScheme, switchWorkflowSchemeTaskPayload{ProjectID: projectID, SchemeID: schemeID, StatusMappings: mappings})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

func migrateWorkflowSchemeIssues(ctx context.Context, tx pgx.Tx, workspaceID, actorID, projectID string, scheme workflow.Scheme, mappings []WorkflowStatusMapping, migrated *int) error {
	rows, err := tx.Query(ctx, `SELECT id,issuetype_id,status_id FROM issues WHERE workspace_id=$1 AND project_id=$2 ORDER BY id FOR UPDATE`, workspaceID, projectID)
	if err != nil {
		return err
	}
	type issueState struct{ id, issueTypeID, statusID string }
	var issues []issueState
	for rows.Next() {
		var issue issueState
		if err := rows.Scan(&issue.id, &issue.issueTypeID, &issue.statusID); err != nil {
			rows.Close()
			return err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	replacements := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		if mapping.IssueTypeID == "" || mapping.OldStatusID == "" || mapping.NewStatusID == "" {
			return fmt.Errorf("%w: every status mapping requires issueTypeId, oldStatusId, and newStatusId", ErrAdminValidation)
		}
		replacements[mapping.IssueTypeID+"\x00"+mapping.OldStatusID] = mapping.NewStatusID
	}
	workflowCache := make(map[string]workflow.Workflow)
	statusNames := make(map[string]string)
	migratedCount := 0
	for _, issue := range issues {
		workflowID := schemeWorkflowID(scheme, issue.issueTypeID)
		wf, ok := workflowCache[workflowID]
		if !ok {
			wf, err = workflowByIDQuery(ctx, tx, workspaceID, workflowID)
			if err != nil {
				return err
			}
			workflowCache[workflowID] = wf
		}
		if workflowStatuses(wf)[issue.statusID] {
			continue
		}
		newStatusID := replacements[issue.issueTypeID+"\x00"+issue.statusID]
		if newStatusID == "" {
			return fmt.Errorf("%w: status mapping required for issue type %s and status %s", ErrAdminConflict, issue.issueTypeID, issue.statusID)
		}
		if !workflowStatuses(wf)[newStatusID] {
			return fmt.Errorf("%w: replacement status %s is not in target workflow %s", ErrAdminValidation, newStatusID, wf.Name)
		}
		for _, statusID := range []string{issue.statusID, newStatusID} {
			if _, ok := statusNames[statusID]; !ok {
				var statusName string
				if err := tx.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2) AND (project_id IS NULL OR project_id=$3)`, statusID, workspaceID, projectID).Scan(&statusName); err != nil {
					return fmt.Errorf("%w: status %s does not exist", ErrAdminValidation, statusID)
				}
				statusNames[statusID] = statusName
			}
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE issues SET status_id=$2,updated_seq=$3,updated_at=now() WHERE id=$1`, issue.id, newStatusID, seq); err != nil {
			return err
		}
		updated, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issue.id))
		if err != nil {
			return err
		}
		diff := map[string]models.ChangeItem{"status": diffItem("status", issue.statusID, statusNames[issue.statusID], newStatusID, statusNames[newStatusID])}
		payload, err := json.Marshal(models.IssueUpdatePayload{Diff: diff, Issue: *updated})
		if err != nil {
			return err
		}
		if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issue.id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return err
		}
		migratedCount++
	}
	*migrated = migratedCount
	return nil
}

func (s *Store) switchWorkflowScheme(ctx context.Context, workspaceID, actorID, projectID, schemeID string, mappings []WorkflowStatusMapping, taskID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var projectName string
	if err := tx.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, projectID, workspaceID).Scan(&projectName); err != nil {
		return ErrAdminNotFound
	}
	scheme, err := scanWorkflowScheme(tx.QueryRow(ctx, `SELECT id,name,description,default_workflow_id,issue_type_mappings,COALESCE(draft_def,'null'),version,draft_def IS NOT NULL FROM workflow_schemes WHERE id=$1 AND workspace_id=$2`, schemeID, workspaceID), false)
	if err != nil {
		return ErrAdminNotFound
	}
	migrated := 0
	if err := migrateWorkflowSchemeIssues(ctx, tx, workspaceID, actorID, projectID, scheme, mappings, &migrated); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET workflow_scheme_id=$3,workflow_id=$4 WHERE id=$1 AND workspace_id=$2`, projectID, workspaceID, schemeID, scheme.DefaultWorkflowID); err != nil {
		return err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.assigned", schemeID, map[string]any{"projectId": projectID, "projectName": projectName, "migratedIssues": migrated}); err != nil {
		return err
	}
	if err := completeAPITask(ctx, tx, workspaceID, taskID, "Workflow scheme migration completed.", map[string]any{"projectId": projectID, "workflowSchemeId": schemeID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PublishWorkflowSchemeDraft(ctx context.Context, workspaceID, actorID, schemeID string) error {
	return s.publishWorkflowSchemeDraft(ctx, workspaceID, actorID, schemeID, nil, "")
}

func (s *Store) PublishWorkflowSchemeDraftTask(ctx context.Context, workspaceID, actorID, schemeID string) (APITask, error) {
	return s.PublishWorkflowSchemeDraftTaskWithMappings(ctx, workspaceID, actorID, schemeID, nil)
}

func (s *Store) PublishWorkflowSchemeDraftTaskWithMappings(ctx context.Context, workspaceID, actorID, schemeID string, mappings []WorkflowStatusMapping) (APITask, error) {
	scheme, err := s.WorkflowSchemeByID(ctx, workspaceID, schemeID, true)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !scheme.HasDraft) {
		return APITask{}, ErrAdminConflict
	}
	if err != nil {
		return APITask{}, err
	}
	projects, err := s.ProjectsForWorkflowScheme(ctx, workspaceID, schemeID)
	if err != nil {
		return APITask{}, err
	}
	for _, project := range projects {
		impacts, err := schemeImpact(ctx, s.Pool, workspaceID, project.ID, scheme, false)
		if err != nil {
			return APITask{}, err
		}
		if err := validateWorkflowSchemeImpactMappings(impacts, mappings); err != nil {
			return APITask{}, err
		}
	}
	task, err := queuedAPITask(workspaceID, actorID, "Publish workflow scheme draft", apiTaskPublishWorkflowScheme, publishWorkflowSchemeTaskPayload{SchemeID: schemeID, StatusMappings: mappings})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

func (s *Store) publishWorkflowSchemeDraft(ctx context.Context, workspaceID, actorID, schemeID string, statusMappings []WorkflowStatusMapping, taskID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scheme, err := scanWorkflowScheme(tx.QueryRow(ctx, `SELECT id,name,description,default_workflow_id,issue_type_mappings,COALESCE(draft_def,'null'),version,draft_def IS NOT NULL FROM workflow_schemes WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, schemeID, workspaceID), true)
	if err != nil || !scheme.HasDraft {
		return ErrAdminConflict
	}
	projectRows, err := tx.Query(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND workflow_scheme_id=$2 ORDER BY id`, workspaceID, schemeID)
	if err != nil {
		return err
	}
	var projectIDs []string
	for projectRows.Next() {
		var id string
		if err := projectRows.Scan(&id); err != nil {
			projectRows.Close()
			return err
		}
		projectIDs = append(projectIDs, id)
	}
	projectRows.Close()
	migrated := 0
	for _, projectID := range projectIDs {
		projectMigrated := 0
		if err := migrateWorkflowSchemeIssues(ctx, tx, workspaceID, actorID, projectID, scheme, statusMappings, &projectMigrated); err != nil {
			return err
		}
		migrated += projectMigrated
	}
	mappings, err := json.Marshal(scheme.IssueTypeMappings)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_schemes SET default_workflow_id=$3,issue_type_mappings=$4,draft_def=NULL,version=version+1,updated_at=now() WHERE id=$1 AND workspace_id=$2`, schemeID, workspaceID, scheme.DefaultWorkflowID, mappings); err != nil {
		return err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.published", schemeID, map[string]any{"version": scheme.Version + 1, "migratedIssues": migrated}); err != nil {
		return err
	}
	if err := completeAPITask(ctx, tx, workspaceID, taskID, "Workflow scheme draft published.", map[string]any{"workflowSchemeId": schemeID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DiscardWorkflowSchemeDraft(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE workflow_schemes SET draft_def=NULL,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND draft_def IS NOT NULL`, schemeID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAdminConflict
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.draft.discarded", schemeID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ProjectsForWorkflowScheme(ctx context.Context, workspaceID, schemeID string) ([]*models.Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,key,name FROM projects WHERE workspace_id=$1 AND workflow_scheme_id=$2 ORDER BY key`, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*models.Project
	for rows.Next() {
		project := &models.Project{WorkspaceID: workspaceID}
		if err := rows.Scan(&project.ID, &project.Key, &project.Name); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) WorkflowSchemeForProject(ctx context.Context, workspaceID, projectID string) (workflow.Scheme, error) {
	var schemeID string
	if err := s.Pool.QueryRow(ctx, `SELECT workflow_scheme_id FROM projects WHERE id=$1 AND workspace_id=$2`, projectID, workspaceID).Scan(&schemeID); err != nil {
		return workflow.Scheme{}, err
	}
	return s.WorkflowSchemeByID(ctx, workspaceID, schemeID, false)
}

func (s *Store) DeleteWorkflowScheme(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM workflow_schemes WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, schemeID, workspaceID).Scan(&name); err != nil {
		return ErrAdminNotFound
	}
	var projects int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM projects WHERE workspace_id=$1 AND workflow_scheme_id=$2`, workspaceID, schemeID).Scan(&projects); err != nil {
		return err
	}
	if projects > 0 {
		return fmt.Errorf("%w: an active workflow scheme cannot be deleted", ErrAdminConflict)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workflow_schemes WHERE id=$1 AND workspace_id=$2`, schemeID, workspaceID); err != nil {
		return err
	}
	if err := addWorkflowSchemeAudit(ctx, tx, workspaceID, actorID, "workflow.scheme.deleted", schemeID, map[string]any{"name": name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WorkflowForProjectAndIssueType(ctx context.Context, projectID, issueTypeID string) (workflow.Workflow, error) {
	var workspaceID, workflowID string
	err := s.Pool.QueryRow(ctx, `
		SELECT p.workspace_id,COALESCE(ws.issue_type_mappings->>$2,ws.default_workflow_id,p.workflow_id,'wf_default')
		FROM projects p LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id
		WHERE p.id=$1`, projectID, issueTypeID).Scan(&workspaceID, &workflowID)
	if err != nil {
		return workflow.Workflow{}, err
	}
	wf, err := workflowByIDQuery(ctx, s.Pool, workspaceID, workflowID)
	if err != nil {
		return workflow.Workflow{}, err
	}
	if wf.ProjectID != "" && wf.ProjectID != projectID {
		return workflow.Workflow{}, pgx.ErrNoRows
	}
	return wf, nil
}
