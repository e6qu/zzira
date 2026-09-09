package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

type IssueMove struct {
	ProjectID   string
	IssueTypeID string
	ParentID    string
	StatusID    string
	TaskID      string
}

// MoveIssue changes an issue's project/type/key mapping and records the task
// item in the same transaction. Project-bound version/component values and an
// incompatible security level are cleared when crossing a project boundary.
func (s *Store) MoveIssue(ctx context.Context, actorID, workspaceID, issueID string, move IssueMove) (*models.Issue, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2 FOR UPDATE OF i`, workspaceID, issueID))
	if err != nil {
		return nil, nil, err
	}
	var destinationKey, destinationName string
	if err := tx.QueryRow(ctx, `SELECT key,name FROM projects WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, move.ProjectID, workspaceID).Scan(&destinationKey, &destinationName); err != nil {
		return nil, nil, fmt.Errorf("destination project: %w", err)
	}
	var issueTypeName string
	var subtask bool
	if err := tx.QueryRow(ctx, `SELECT name,subtask FROM issue_types WHERE id=$1`, move.IssueTypeID).Scan(&issueTypeName, &subtask); err != nil {
		return nil, nil, fmt.Errorf("destination issue type: %w", err)
	}
	if move.StatusID == "" {
		return nil, nil, fmt.Errorf("destination status is required")
	}
	var statusName string
	if err := tx.QueryRow(ctx, `SELECT name FROM statuses WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2) AND (project_id IS NULL OR project_id=$3)`, move.StatusID, workspaceID, move.ProjectID).Scan(&statusName); err != nil {
		return nil, nil, fmt.Errorf("destination status: %w", err)
	}
	parentKey := ""
	if subtask {
		if move.ParentID == "" {
			return nil, nil, fmt.Errorf("destination parent is required for a sub-task")
		}
		var parentSubtask bool
		if err := tx.QueryRow(ctx, `
			SELECT parent.key,parent_type.subtask
			FROM issues parent JOIN issue_types parent_type ON parent_type.id=parent.issuetype_id
			WHERE parent.id=$1 AND parent.workspace_id=$2 AND parent.project_id=$3`, move.ParentID, workspaceID, move.ProjectID).Scan(&parentKey, &parentSubtask); err != nil {
			return nil, nil, fmt.Errorf("destination parent: %w", err)
		}
		if parentSubtask || move.ParentID == issueID {
			return nil, nil, fmt.Errorf("destination parent must be a standard issue")
		}
	} else if move.ParentID != "" {
		return nil, nil, fmt.Errorf("destination parent is only valid for a sub-task")
	}

	newKey := current.Key
	fields := current.Fields
	securityLevelID := current.SecurityLevelID
	if current.ProjectID != move.ProjectID {
		var issueNumber int64
		if err := tx.QueryRow(ctx, `UPDATE projects SET issue_seq=issue_seq+1 WHERE id=$1 RETURNING issue_seq`, move.ProjectID).Scan(&issueNumber); err != nil {
			return nil, nil, err
		}
		newKey = fmt.Sprintf("%s-%d", destinationKey, issueNumber)
		if _, err := tx.Exec(ctx, `INSERT INTO issue_key_aliases(workspace_id,issue_id,key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, workspaceID, issueID, current.Key); err != nil {
			return nil, nil, err
		}
		fields = cloneRawFields(current.Fields)
		delete(fields, "fixVersions")
		delete(fields, "versions")
		delete(fields, "components")
		if securityLevelID != "" {
			var valid bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM projects project JOIN security_schemes scheme ON scheme.id=project.security_scheme_id
				CROSS JOIN LATERAL jsonb_array_elements(scheme.levels) level
				WHERE project.id=$1 AND level->>'id'=$2
			)`, move.ProjectID, securityLevelID).Scan(&valid); err != nil {
				return nil, nil, err
			}
			if !valid {
				securityLevelID = ""
			}
		}
	}
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE issues SET project_id=$3,key=$4,issuetype_id=$5,parent_id=$6,status_id=$7,
			security_level_id=$8,fields=$9,updated_seq=$10,updated_at=now()
		WHERE workspace_id=$1 AND id=$2`, workspaceID, issueID, move.ProjectID, newKey, move.IssueTypeID,
		nilIfEmpty(move.ParentID), move.StatusID, nilIfEmpty(securityLevelID), fieldsJSON, seq); err != nil {
		return nil, nil, err
	}
	updated, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return nil, nil, err
	}
	diff := map[string]models.ChangeItem{}
	if current.ProjectID != move.ProjectID {
		diff["project"] = diffItem("project", current.ProjectID, current.Key, move.ProjectID, destinationName)
		diff["key"] = diffItem("key", current.Key, current.Key, newKey, newKey)
	}
	if current.IssueType.ID != move.IssueTypeID {
		diff["issuetype"] = diffItem("issuetype", current.IssueType.ID, current.IssueType.Name, move.IssueTypeID, issueTypeName)
	}
	if current.Status.ID != move.StatusID {
		diff["status"] = diffItem("status", current.Status.ID, current.Status.Name, move.StatusID, statusName)
	}
	oldParentID, oldParentKey := "", ""
	if current.Parent != nil {
		oldParentID, oldParentKey = current.Parent.ID, current.Parent.Key
	}
	if oldParentID != move.ParentID {
		diff["parent"] = diffItem("parent", oldParentID, oldParentKey, move.ParentID, parentKey)
	}
	if current.SecurityLevelID != securityLevelID {
		diff["security"] = diffItem("security", current.SecurityLevelID, current.SecurityLevelID, securityLevelID, securityLevelID)
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{Diff: diff, Issue: *updated})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if move.TaskID != "" {
		result, _ := json.Marshal(map[string]any{"jiraId": updated.JiraID, "key": updated.Key})
		if _, err := tx.Exec(ctx, `INSERT INTO bulk_issue_task_items(task_id,issue_id,result) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, move.TaskID, issueID, result); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return updated, action, nil
}

func cloneRawFields(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return map[string]json.RawMessage{}
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func (s *Store) BulkIssueTaskItemResult(ctx context.Context, taskID, issueID string) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT result FROM bulk_issue_task_items WHERE task_id=$1 AND issue_id=$2`, taskID, issueID).Scan(&result)
	return result, err
}
