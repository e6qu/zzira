package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const apiTaskIssueProperties = "jira-issue-properties-bulk"

// ErrIssuePropertyTaskConflict is a bulk property update that overlaps one
// already queued or running on the same issues.
var ErrIssuePropertyTaskConflict = errors.New("another bulk update on the same issues is already in progress")

// IssuePropertyBulkRequest is one of Jira's four bulk issue property operations.
type IssuePropertyBulkRequest struct {
	// Mode is "set" (the same properties on listed issues), "multi" (per-issue
	// properties), "set-filtered" (one property on filtered issues) or
	// "delete-filtered" (remove one property from filtered issues).
	Mode string `json:"mode"`
	// IssueIDs are the issues named by the request, as stored ids.
	IssueIDs   []string                   `json:"issueIds,omitempty"`
	Properties map[string]json.RawMessage `json:"properties,omitempty"`
	PerIssue   []IssuePropertyBulkItem    `json:"perIssue,omitempty"`
	Key        string                     `json:"key,omitempty"`
	Value      json.RawMessage            `json:"value,omitempty"`
	// Expression computes the value per work item instead of Value.
	Expression string `json:"expression,omitempty"`
	// Filter narrows the filtered modes; without IssueIDs every issue is eligible.
	CurrentValue json.RawMessage `json:"currentValue,omitempty"`
	HasProperty  *bool           `json:"hasProperty,omitempty"`
}

// IssuePropertyBulkItem is one issue's properties in a multi-issue update.
type IssuePropertyBulkItem struct {
	IssueID    string                     `json:"issueId"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// EnqueueIssuePropertiesTask queues a bulk property update, refusing one that
// overlaps an update already waiting or running on the same issues.
func (s *Store) EnqueueIssuePropertiesTask(ctx context.Context, workspaceID, actorID string, request IssuePropertyBulkRequest) (APITask, error) {
	issues := request.IssueIDs
	for _, item := range request.PerIssue {
		issues = append(issues, item.IssueID)
	}
	rows, err := s.Pool.Query(ctx, `SELECT payload FROM api_tasks WHERE workspace_id=$1 AND kind=$2 AND status IN ('ENQUEUED','RUNNING')`, workspaceID, apiTaskIssueProperties)
	if err != nil {
		return APITask{}, err
	}
	pending := map[string]bool{}
	pendingAll := false
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return APITask{}, err
		}
		var other IssuePropertyBulkRequest
		if json.Unmarshal(raw, &other) != nil {
			continue
		}
		otherIssues := other.IssueIDs
		for _, item := range other.PerIssue {
			otherIssues = append(otherIssues, item.IssueID)
		}
		if len(otherIssues) == 0 {
			pendingAll = true
		}
		for _, id := range otherIssues {
			pending[id] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return APITask{}, err
	}
	if pendingAll || (len(issues) == 0 && len(pending) > 0) {
		return APITask{}, ErrIssuePropertyTaskConflict
	}
	for _, id := range issues {
		if pending[id] {
			return APITask{}, ErrIssuePropertyTaskConflict
		}
	}
	task, err := queuedAPITask(workspaceID, actorID, "Bulk issue property update", apiTaskIssueProperties, request)
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// editableIssueIDs narrows issues to those the submitter can see and edit.
// Without candidates, every such issue on the site is considered.
func (s *Store) editableIssueIDs(ctx context.Context, workspaceID, userID string, candidates []string) ([]string, error) {
	query := `SELECT i.id, i.project_id FROM issues i WHERE i.workspace_id=$1 AND ` + VisibleIssuePredicate("i", "$2")
	args := []any{workspaceID, userID}
	if candidates != nil {
		query += ` AND i.id = ANY($3)`
		args = append(args, candidates)
	}
	rows, err := s.Pool.Query(ctx, query+` ORDER BY i.jira_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type candidate struct{ id, projectID string }
	visible := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.projectID); err != nil {
			return nil, err
		}
		visible = append(visible, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ids := []string{}
	for _, c := range visible {
		allowed, permErr := s.HasProjectPermission(ctx, workspaceID, userID, c.projectID, c.id, "EDIT_ISSUES")
		if permErr != nil {
			return nil, permErr
		}
		if allowed {
			ids = append(ids, c.id)
		}
	}
	return ids, nil
}

func (s *Store) executeIssuePropertiesTask(ctx context.Context, task APITask) error {
	var request IssuePropertyBulkRequest
	if err := json.Unmarshal(task.Payload, &request); err != nil {
		return fmt.Errorf("decode bulk issue property update: %w", err)
	}
	type change struct {
		issueID    string
		properties map[string]json.RawMessage
	}
	changes := []change{}
	switch request.Mode {
	case "set":
		ids, err := s.editableIssueIDs(ctx, task.WorkspaceID, task.SubmittedBy, request.IssueIDs)
		if err != nil {
			return err
		}
		for _, id := range ids {
			changes = append(changes, change{id, request.Properties})
		}
	case "multi":
		candidates := []string{}
		for _, item := range request.PerIssue {
			candidates = append(candidates, item.IssueID)
		}
		ids, err := s.editableIssueIDs(ctx, task.WorkspaceID, task.SubmittedBy, candidates)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, id := range ids {
			allowed[id] = true
		}
		for _, item := range request.PerIssue {
			if allowed[item.IssueID] {
				changes = append(changes, change{item.IssueID, item.Properties})
			}
		}
	case "set-filtered", "delete-filtered":
		ids, err := s.editableIssueIDs(ctx, task.WorkspaceID, task.SubmittedBy, request.IssueIDs)
		if err != nil {
			return err
		}
		for _, id := range ids {
			current, readErr := s.IssueProperty(ctx, id, request.Key)
			present := readErr == nil
			if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
				return readErr
			}
			if request.HasProperty != nil && *request.HasProperty != present {
				continue
			}
			if len(request.CurrentValue) > 0 && (!present || !jsonEqual(current, request.CurrentValue)) {
				continue
			}
			if request.Mode == "delete-filtered" {
				if !present {
					continue
				}
				changes = append(changes, change{id, map[string]json.RawMessage{request.Key: nil}})
				continue
			}
			value := request.Value
			if request.Expression != "" {
				if s.IssueExpressionEvaluator == nil {
					return fmt.Errorf("jira expressions are not available")
				}
				computed, evalErr := s.IssueExpressionEvaluator(ctx, task.WorkspaceID, task.SubmittedBy, id, request.Expression)
				// A work item the expression fails on, or whose value is too
				// large, keeps its property, as Jira does.
				if evalErr != nil || len(computed) == 0 || len(computed) > 32768 {
					continue
				}
				value = computed
			}
			changes = append(changes, change{id, map[string]json.RawMessage{request.Key: value}})
		}
	default:
		return fmt.Errorf("unknown bulk issue property mode %q", request.Mode)
	}
	for index, item := range changes {
		for key, value := range item.properties {
			if value == nil {
				if err := s.DeleteIssueProperty(ctx, task.SubmittedBy, item.issueID, key); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				continue
			}
			if _, err := s.SetIssueProperty(ctx, task.SubmittedBy, item.issueID, key, value); err != nil {
				return err
			}
		}
		if (index+1)%100 == 0 && index+1 < len(changes) {
			if err := s.UpdateAPITaskProgress(ctx, task, (index+1)*100/len(changes), fmt.Sprintf("Updated %d of %d issues.", index+1, len(changes))); err != nil {
				return err
			}
		}
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Updated properties on %d issues.", len(changes)), nil)
}

// jsonEqual compares two JSON documents by value rather than by spelling.
func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(left)
	rightCanonical, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftCanonical) == string(rightCanonical)
}
