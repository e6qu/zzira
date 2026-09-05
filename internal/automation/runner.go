package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

type Runner struct {
	Service *Service
	Logf    func(string, ...any)
}

type claimedRun struct {
	Run
	WorkspaceID string
	ActorID     string
	Payload     json.RawMessage
	JQL         string
}

type component struct {
	Component string          `json:"component"`
	Type      string          `json:"type"`
	Value     json.RawMessage `json:"value"`
}

func (r *Runner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
				logger := r.Logf
				if logger == nil {
					logger = log.Printf
				}
				logger("automation runner: %v", err)
			}
		}
	}
}

// DrainOnce enqueues due rules and executes one claimed run. The SQL claim
// uses SKIP LOCKED, so multiple server replicas can safely call it.
func (r *Runner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Service == nil || r.Service.Store == nil || r.Service.Commands == nil {
		return errors.New("automation runner is not configured")
	}
	if err := r.enqueueDue(ctx, workspaceID); err != nil {
		return err
	}
	run, err := r.claim(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	matched, changed, executionErr := r.execute(ctx, run)
	return r.finish(ctx, run, matched, changed, executionErr)
}

func (r *Runner) enqueueDue(ctx context.Context, workspaceID string) error {
	_, err := r.Service.Store.Pool.Exec(ctx, `
		WITH due AS (
		 SELECT uuid,next_run_at,interval_minutes FROM automation_rules
		 WHERE workspace_id=$1 AND state='ENABLED' AND interval_minutes IS NOT NULL AND next_run_at<=now()
		 FOR UPDATE SKIP LOCKED
		), queued AS (
		 INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state)
		 SELECT gen_random_uuid(),uuid,next_run_at,'PENDING' FROM due
		 ON CONFLICT(rule_uuid,scheduled_for) DO NOTHING
		)
		UPDATE automation_rules r SET
		 next_run_at=GREATEST(d.next_run_at+make_interval(mins=>d.interval_minutes),now()+make_interval(mins=>d.interval_minutes))
		FROM due d WHERE r.uuid=d.uuid`, workspaceID)
	return err
}

func (r *Runner) claim(ctx context.Context, workspaceID string) (*claimedRun, error) {
	run := &claimedRun{}
	err := r.Service.Store.Pool.QueryRow(ctx, `
		WITH candidate AS (
		 SELECT ar.id FROM automation_runs ar JOIN automation_rules rule ON rule.uuid=ar.rule_uuid
		 WHERE rule.workspace_id=$1 AND rule.state='ENABLED'
		   AND ((ar.state IN ('PENDING','FAILED') AND ar.available_at<=now())
		        OR (ar.state='RUNNING' AND ar.claimed_at<now()-interval '2 minutes'))
		 ORDER BY ar.scheduled_for,ar.id FOR UPDATE OF ar SKIP LOCKED LIMIT 1
		), claimed AS (
		 UPDATE automation_runs ar SET state='RUNNING',attempts=attempts+1,claimed_at=now(),
		  started_at=COALESCE(started_at,now()),completed_at=NULL
		 FROM candidate WHERE ar.id=candidate.id
		 RETURNING ar.id,ar.rule_uuid,ar.scheduled_for,ar.state,ar.attempts,ar.started_at,
		           ar.completed_at,ar.matched_count,ar.changed_count,ar.detail
		)
		SELECT c.id::text,c.rule_uuid::text,c.scheduled_for,c.state,c.attempts,c.started_at,c.completed_at,
		       c.matched_count,c.changed_count,c.detail,rule.workspace_id,rule.actor_id,rule.payload,rule.jql
		FROM claimed c JOIN automation_rules rule ON rule.uuid=c.rule_uuid`, workspaceID).
		Scan(&run.ID, &run.RuleUUID, &run.ScheduledFor, &run.State, &run.Attempts, &run.StartedAt,
			&run.CompletedAt, &run.MatchedCount, &run.ChangedCount, &run.Detail, &run.WorkspaceID,
			&run.ActorID, &run.Payload, &run.JQL)
	return run, err
}

func (r *Runner) execute(ctx context.Context, run *claimedRun) (int, int, error) {
	if err := validateExecutionActor(run.Payload, run.ActorID); err != nil {
		return 0, 0, err
	}
	query, err := jql.Parse(run.JQL)
	if err != nil {
		return 0, 0, fmt.Errorf("parse JQL: %w", err)
	}
	compiled := jql.CompileAt(query, run.ActorID, jql.DefaultResolver(), 2)
	if compiled.Err != nil {
		return 0, 0, fmt.Errorf("compile JQL: %w", compiled.Err)
	}
	issues, total, err := r.Service.Store.Search(ctx, run.WorkspaceID, run.ActorID, compiled, 1000, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("search issues: %w", err)
	}
	if total > 1000 {
		return total, 0, fmt.Errorf("JQL matched %d issues; scheduled rules are limited to 1000 per run", total)
	}
	components, err := actionComponents(run.Payload)
	if err != nil {
		return total, 0, err
	}
	changedIssues := 0
	for _, issue := range issues {
		changed := false
		for _, action := range components {
			didChange, err := r.apply(ctx, run, issue, action)
			if err != nil {
				return total, changedIssues, fmt.Errorf("%s on %s: %w", action.Type, issue.Key, err)
			}
			changed = changed || didChange
		}
		if changed {
			changedIssues++
		}
	}
	return total, changedIssues, nil
}

func validateExecutionActor(payload json.RawMessage, actorID string) error {
	var rule struct {
		Actor struct {
			Actor string `json:"actor"`
			Type  string `json:"type"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return err
	}
	if rule.Actor.Type != "ACCOUNT_ID" || rule.Actor.Actor != actorID {
		return errors.New("scheduled execution requires an ACCOUNT_ID rule actor")
	}
	return nil
}

func actionComponents(payload json.RawMessage) ([]component, error) {
	var rule struct {
		Components []component `json:"components"`
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return nil, err
	}
	if len(rule.Components) == 0 {
		return nil, errors.New("rule has no actions")
	}
	for _, item := range rule.Components {
		if item.Component != "" && item.Component != "ACTION" {
			return nil, fmt.Errorf("component %q is not executable by the scheduled runner", item.Component)
		}
		if item.Type != "jira.issue.add-label" && item.Type != "jira.issue.assign" && item.Type != "jira.issue.transition" {
			return nil, fmt.Errorf("unsupported scheduled action %q", item.Type)
		}
	}
	return rule.Components, nil
}

func (r *Runner) apply(ctx context.Context, run *claimedRun, issue *models.Issue, action component) (bool, error) {
	valueRaw := action.Value
	var encoded string
	if json.Unmarshal(valueRaw, &encoded) == nil && strings.HasPrefix(strings.TrimSpace(encoded), "{") {
		valueRaw = json.RawMessage(encoded)
	}
	switch action.Type {
	case "jira.issue.add-label":
		var value struct {
			Label string `json:"label"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Label) == "" {
			return false, errors.New("label action requires value.label")
		}
		if slices.Contains(issue.Labels, value.Label) {
			return false, nil
		}
		labels := append(slices.Clone(issue.Labels), value.Label)
		_, changed, err := r.Service.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, Labels: &labels,
		})
		return changed != nil, err
	case "jira.issue.assign":
		var value struct {
			AccountID string `json:"accountId"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || value.AccountID == "" {
			return false, errors.New("assign action requires value.accountId")
		}
		accountID := value.AccountID
		if accountID == "ACTOR" {
			accountID = run.ActorID
		} else if accountID == "UNASSIGNED" {
			accountID = ""
		}
		_, changed, err := r.Service.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, AssigneeID: &accountID,
		})
		return changed != nil, err
	case "jira.issue.transition":
		var value struct {
			StatusID string `json:"statusId"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || value.StatusID == "" {
			return false, errors.New("transition action requires value.statusId")
		}
		if issue.Status.ID == value.StatusID {
			return false, nil
		}
		workflow, err := r.Service.Store.WorkflowForProject(ctx, issue.ProjectID)
		if err != nil {
			return false, err
		}
		for _, transition := range workflow.Transitions {
			if transition.To == value.StatusID && slices.Contains(transition.From, issue.Status.ID) {
				_, changed, err := r.Service.Commands.TransitionIssue(ctx, run.ActorID, run.WorkspaceID, issue.ID, transition.ID)
				return changed != nil, err
			}
		}
		return false, fmt.Errorf("no workflow transition from %s to status %s", issue.Status.Name, value.StatusID)
	default:
		return false, fmt.Errorf("unsupported scheduled action %q", action.Type)
	}
}

func (r *Runner) finish(ctx context.Context, run *claimedRun, matched, changed int, executionErr error) error {
	if executionErr == nil {
		state := "SUCCESS"
		if changed == 0 {
			state = "NO_ACTIONS"
		}
		tx, err := r.Service.Store.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `UPDATE automation_runs SET state=$2,completed_at=now(),matched_count=$3,changed_count=$4,detail='' WHERE id=$1`, run.ID, state, matched, changed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE automation_rules SET consecutive_failures=0 WHERE uuid=$1`, run.RuleUUID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	detail := executionErr.Error()
	tx, err := r.Service.Store.Pool.Begin(ctx)
	if err != nil {
		return errors.Join(executionErr, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE automation_runs SET state='FAILED',completed_at=now(),matched_count=$2,changed_count=$3,detail=$4,
		 available_at=now()+make_interval(secs=>LEAST(1800,30*(1 << LEAST(attempts,6)))) WHERE id=$1`, run.ID, matched, changed, detail); err != nil {
		return errors.Join(executionErr, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE automation_rules SET consecutive_failures=consecutive_failures+1,
		 state=CASE WHEN consecutive_failures+1>=10 THEN 'DISABLED' ELSE state END,
		 payload=CASE WHEN consecutive_failures+1>=10 THEN jsonb_set(payload,'{state}',to_jsonb('DISABLED'::text)) ELSE payload END,
		 updated_at=now() WHERE uuid=$1`, run.RuleUUID); err != nil {
		return errors.Join(executionErr, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Join(executionErr, err)
	}
	return executionErr
}
