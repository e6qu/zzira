package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

type WorkflowUpdateDefinition struct {
	Workflow         workflow.Workflow
	ExpectedVersion  int
	StatusMigrations []WorkflowStatusMigration
}

type WorkflowStatusMigration struct {
	ProjectID   string
	IssueTypeID string
	OldStatusID string
	NewStatusID string
}

func addWorkflowAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action string, wf workflow.Workflow, values map[string]any) error {
	if values == nil {
		values = make(map[string]any)
	}
	values["name"], values["version"] = wf.Name, wf.Version
	detail, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'workflow',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, wf.ID, detail)
	return err
}

func (s *Store) workflowBatchDefinitions(ctx context.Context, workspaceID string, statuses []models.Status, workflows []workflow.Workflow) ([][]byte, []models.Status, error) {
	visible, err := s.StatusesForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	validatedStatuses := make([]models.Status, len(statuses))
	for index, status := range statuses {
		validated, err := validateStatus(status)
		if err != nil {
			return nil, nil, err
		}
		validatedStatuses[index] = validated
		visible = append(visible, validated)
	}
	definitions := make([][]byte, len(workflows))
	for index, wf := range workflows {
		definition, err := validateWorkflowAgainstStatuses(wf, visible)
		if err != nil {
			return nil, nil, err
		}
		definitions[index] = definition
	}
	return definitions, validatedStatuses, nil
}

func createWorkflowBatchStatuses(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, statuses []models.Status) error {
	for _, status := range statuses {
		if err := statusNameAvailable(ctx, tx, workspaceID, status.Name, ""); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO statuses(id,name,description,category,workspace_id) VALUES($1,$2,$3,$4,$5)`, status.ID, status.Name, status.Description, status.Category, workspaceID); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: a status already uses that name or id", ErrAdminConflict)
			}
			return err
		}
		if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.created", status.ID, map[string]any{"name": status.Name, "category": status.Category}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateWorkflowBatch(ctx context.Context, workspaceID, actorID string, statuses []models.Status, workflows []workflow.Workflow) ([]models.Status, []workflow.Workflow, error) {
	definitions, statuses, err := s.workflowBatchDefinitions(ctx, workspaceID, statuses, workflows)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('workflow:' || $1))`, workspaceID); err != nil {
		return nil, nil, err
	}
	if err := createWorkflowBatchStatuses(ctx, tx, workspaceID, actorID, statuses); err != nil {
		return nil, nil, err
	}
	for index := range workflows {
		wf := &workflows[index]
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE id=$1 OR (workspace_id=$2 AND lower(name)=lower($3)))`, wf.ID, workspaceID, wf.Name).Scan(&duplicate); err != nil {
			return nil, nil, err
		}
		if duplicate {
			return nil, nil, fmt.Errorf("%w: a workflow already uses that name or id", ErrAdminConflict)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflows(id,name,def,workspace_id) VALUES($1,$2,$3,$4)`, wf.ID, wf.Name, definitions[index], workspaceID); err != nil {
			if isUniqueViolation(err) {
				return nil, nil, fmt.Errorf("%w: a workflow already uses that id", ErrAdminConflict)
			}
			return nil, nil, err
		}
		wf.Version = 1
		if err := addWorkflowAudit(ctx, tx, workspaceID, actorID, "workflow.created", *wf, nil); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return statuses, workflows, nil
}

func workflowDefinitionStatuses(wf workflow.Workflow) []string {
	seen := make(map[string]bool)
	for _, transition := range wf.Transitions {
		seen[transition.To] = true
		for _, from := range transition.From {
			seen[from] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

func migrateWorkflowDefinitionIssues(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, update WorkflowUpdateDefinition) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT i.id,i.project_id,i.issuetype_id,i.status_id
		FROM issues i
		JOIN projects p ON p.id=i.project_id AND p.workspace_id=$1
		LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id AND ws.workspace_id=p.workspace_id
		WHERE COALESCE(ws.issue_type_mappings->>i.issuetype_id,ws.default_workflow_id,p.workflow_id,'wf_default')=$2
		ORDER BY i.id FOR UPDATE OF i`, workspaceID, update.Workflow.ID)
	if err != nil {
		return 0, err
	}
	type issueState struct{ id, projectID, issueTypeID, statusID string }
	var issues []issueState
	for rows.Next() {
		var issue issueState
		if err := rows.Scan(&issue.id, &issue.projectID, &issue.issueTypeID, &issue.statusID); err != nil {
			rows.Close()
			return 0, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	targetStatuses := make(map[string]bool)
	for _, id := range workflowDefinitionStatuses(update.Workflow) {
		targetStatuses[id] = true
	}
	defaultMappings := make(map[string]string)
	scopedMappings := make(map[string]string)
	for _, mapping := range update.StatusMigrations {
		if mapping.OldStatusID == "" || mapping.NewStatusID == "" || !targetStatuses[mapping.NewStatusID] {
			return 0, fmt.Errorf("%w: every status mapping needs known old and target workflow statuses", ErrAdminValidation)
		}
		if mapping.ProjectID == "" && mapping.IssueTypeID == "" {
			defaultMappings[mapping.OldStatusID] = mapping.NewStatusID
			continue
		}
		if mapping.ProjectID == "" || mapping.IssueTypeID == "" {
			return 0, fmt.Errorf("%w: scoped status mappings require project and issue type", ErrAdminValidation)
		}
		scopedMappings[mapping.ProjectID+"\x00"+mapping.IssueTypeID+"\x00"+mapping.OldStatusID] = mapping.NewStatusID
	}
	statusNames := make(map[string]string)
	migrated := 0
	for _, issue := range issues {
		if targetStatuses[issue.statusID] {
			continue
		}
		newStatusID := scopedMappings[issue.projectID+"\x00"+issue.issueTypeID+"\x00"+issue.statusID]
		if newStatusID == "" {
			newStatusID = defaultMappings[issue.statusID]
		}
		if newStatusID == "" {
			return 0, fmt.Errorf("%w: status mapping required for project %s, issue type %s, and status %s", ErrAdminConflict, issue.projectID, issue.issueTypeID, issue.statusID)
		}
		for _, statusID := range []string{issue.statusID, newStatusID} {
			if _, exists := statusNames[statusID]; exists {
				continue
			}
			var statusName string
			if err := tx.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2)`, statusID, workspaceID).Scan(&statusName); err != nil {
				return 0, fmt.Errorf("%w: status %s does not exist", ErrAdminValidation, statusID)
			}
			statusNames[statusID] = statusName
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE issues SET status_id=$2,updated_seq=$3,updated_at=now() WHERE id=$1`, issue.id, newStatusID, seq); err != nil {
			return 0, err
		}
		updated, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issue.id))
		if err != nil {
			return 0, err
		}
		payload, err := json.Marshal(map[string]any{
			"issue": updated,
			"diff":  map[string]models.ChangeItem{"status": {Field: "status", FieldType: "jira", From: issue.statusID, FromString: statusNames[issue.statusID], To: newStatusID, ToString: statusNames[newStatusID]}},
		})
		if err != nil {
			return 0, err
		}
		if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issue.id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return 0, err
		}
		migrated++
	}
	return migrated, nil
}

func (s *Store) UpdateWorkflowBatch(ctx context.Context, workspaceID, actorID string, statuses []models.Status, updates []WorkflowUpdateDefinition) ([]models.Status, []workflow.Workflow, error) {
	workflows := make([]workflow.Workflow, len(updates))
	for index := range updates {
		workflows[index] = updates[index].Workflow
	}
	definitions, statuses, err := s.workflowBatchDefinitions(ctx, workspaceID, statuses, workflows)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := createWorkflowBatchStatuses(ctx, tx, workspaceID, actorID, statuses); err != nil {
		return nil, nil, err
	}
	for index, update := range updates {
		if update.Workflow.ID == workflow.Default().ID {
			return nil, nil, fmt.Errorf("%w: the system workflow is read-only", ErrAdminValidation)
		}
		var currentVersion int
		if err := tx.QueryRow(ctx, `SELECT version FROM workflows WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, update.Workflow.ID, workspaceID).Scan(&currentVersion); err != nil {
			if err == pgx.ErrNoRows {
				return nil, nil, ErrAdminNotFound
			}
			return nil, nil, err
		}
		if currentVersion != update.ExpectedVersion {
			return nil, nil, fmt.Errorf("%w: workflow version is stale", ErrAdminConflict)
		}
		migrated, err := migrateWorkflowDefinitionIssues(ctx, tx, workspaceID, actorID, update)
		if err != nil {
			return nil, nil, err
		}
		updated := update.Workflow
		updated.Version = currentVersion + 1
		if _, err := tx.Exec(ctx, `UPDATE workflows SET def=$3,version=$4,published_at=now() WHERE id=$1 AND workspace_id=$2`, updated.ID, workspaceID, definitions[index], updated.Version); err != nil {
			return nil, nil, err
		}
		updates[index].Workflow = updated
		if err := addWorkflowAudit(ctx, tx, workspaceID, actorID, "workflow.updated", updated, map[string]any{"migratedIssues": migrated}); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	out := make([]workflow.Workflow, len(updates))
	for index := range updates {
		out[index] = updates[index].Workflow
	}
	return statuses, out, nil
}
