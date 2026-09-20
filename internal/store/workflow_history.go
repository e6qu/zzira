package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

// IssueWorkflowEvaluation builds the context a workflow's conditions and
// validators read for one work item: what the work item holds now, and the
// history -- statuses, approvals, transitions, and the statuses of the work
// above and below it -- that a rule may ask about. Every caller that decides
// which transitions are available reads the same context, so a transition
// cannot be offered in one surface and withheld in another.
func (s *Store) IssueWorkflowEvaluation(ctx context.Context, workspaceID, actorID string, issue *models.Issue) (workflow.EvaluationContext, error) {
	evaluation := workflow.ContextForIssue(actorID, issue)
	var err error
	if evaluation.StatusHistory, err = s.IssueStatusHistory(ctx, workspaceID, issue.ID); err != nil {
		return evaluation, err
	}
	if evaluation.Approvals, err = s.IssueApprovalDecisions(ctx, issue.ID); err != nil {
		return evaluation, err
	}
	if evaluation.Transitions, err = s.IssueTransitionHistory(ctx, workspaceID, issue.ID); err != nil {
		return evaluation, err
	}
	if evaluation.ParentStatus, evaluation.ChildStatuses, err = s.IssueHierarchyStatuses(ctx, workspaceID, issue.ID); err != nil {
		return evaluation, err
	}
	return evaluation, nil
}

// IssueStatusHistory returns statuses departed by real status changes, oldest
// first. It derives history from the immutable sync log used by changelog.
func (s *Store) IssueStatusHistory(ctx context.Context, workspaceID, issueID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT payload->'diff'->'status'->>'from'
		FROM actions
		WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2
		  AND payload->'diff'->'status'->>'from' IS NOT NULL
		  AND payload->'diff'->'status'->>'from' <> payload->'diff'->'status'->>'to'
		ORDER BY seq`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var statusID string
		if err := rows.Scan(&statusID); err != nil {
			return nil, err
		}
		statuses = append(statuses, statusID)
	}
	return statuses, rows.Err()
}

// IssueTransitionHistory returns real status changes with the actor that made
// each transition, oldest first.
func (s *Store) IssueTransitionHistory(ctx context.Context, workspaceID, issueID string) ([]workflow.TransitionHistory, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT payload->'diff'->'status'->>'from',payload->'diff'->'status'->>'to',actor_id
		FROM actions
		WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2
		  AND payload->'diff'->'status'->>'from' IS NOT NULL
		  AND payload->'diff'->'status'->>'from' <> payload->'diff'->'status'->>'to'
		ORDER BY seq`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var transitions []workflow.TransitionHistory
	for rows.Next() {
		var transition workflow.TransitionHistory
		if err := rows.Scan(&transition.FromStatusID, &transition.ToStatusID, &transition.ActorID); err != nil {
			return nil, err
		}
		transitions = append(transitions, transition)
	}
	return transitions, rows.Err()
}
