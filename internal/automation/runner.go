package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
	// IssueID and InitiatorID are the work item and person of the event an
	// event run started from; both are empty for scheduled runs.
	IssueID, InitiatorID string
	// WebhookData is the body of the request that ran an incoming webhook
	// rule, which its actions read as {{webhookData}}.
	WebhookData json.RawMessage
	// TriggerData is what an event that happened to something other than a
	// work item carried: the version, the sprint, or the work item that has
	// gone. Its actions read it as {{version.name}} and the like.
	TriggerData json.RawMessage
	// WebResponse is the answer the rule's last web request received, which
	// later actions read as {{webResponse}}.
	WebResponse *webResponse
	RuleName    string
	ScopeARIs   []string
	// TriggerIssue is the work item the rule started from while branches run
	// for related work.
	TriggerIssue *models.Issue
}

type component struct {
	Component string          `json:"component"`
	Type      string          `json:"type"`
	Value     json.RawMessage `json:"value"`
	// Children are a branch's conditions and actions.
	Children []component `json:"children"`
}

// branchValue is which related work items a branch runs for, and for linked
// work, the link types to follow.
type branchValue struct {
	RelatedType string   `json:"relatedType"`
	LinkTypes   []string `json:"linkTypes"`
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
	if err := r.enqueueEvents(ctx, workspaceID); err != nil {
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
		 ON CONFLICT(rule_uuid,scheduled_for) WHERE trigger_seq IS NULL DO NOTHING
		)
		UPDATE automation_rules r SET
		 next_run_at=GREATEST(d.next_run_at+make_interval(mins=>d.interval_minutes),now()+make_interval(mins=>d.interval_minutes))
		FROM due d WHERE r.uuid=d.uuid`, workspaceID)
	if err != nil {
		return err
	}
	return r.enqueueDueCron(ctx, workspaceID)
}

// enqueueDueCron queues a run for each enabled cron rule whose time has come
// and moves the rule to its next time. Times missed while no worker ran are
// run once, as for fixed intervals.
func (r *Runner) enqueueDueCron(ctx context.Context, workspaceID string) error {
	tx, err := r.Service.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT uuid::text,next_run_at,cron_expression,schedule_timezone FROM automation_rules
		WHERE workspace_id=$1 AND state='ENABLED' AND cron_expression IS NOT NULL AND next_run_at<=now()
		FOR UPDATE SKIP LOCKED`, workspaceID)
	if err != nil {
		return err
	}
	type dueRule struct {
		uuid, expression, timezone string
		due                        time.Time
	}
	due := []dueRule{}
	for rows.Next() {
		var rule dueRule
		if err := rows.Scan(&rule.uuid, &rule.due, &rule.expression, &rule.timezone); err != nil {
			rows.Close()
			return err
		}
		due = append(due, rule)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now()
	for _, rule := range due {
		id, err := NewUUIDv7()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state) VALUES($1,$2,$3,'PENDING')
			ON CONFLICT (rule_uuid,scheduled_for) WHERE trigger_seq IS NULL DO NOTHING`, id, rule.uuid, rule.due); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE automation_rules SET next_run_at=$2 WHERE uuid=$1`, rule.uuid, nextCronRun(rule.expression, rule.timezone, now)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
		           ar.completed_at,ar.matched_count,ar.changed_count,ar.detail,ar.issue_id,ar.initiator_id,ar.webhook_data,ar.trigger_data
		)
		SELECT c.id::text,c.rule_uuid::text,c.scheduled_for,c.state,c.attempts,c.started_at,c.completed_at,
		       c.matched_count,c.changed_count,c.detail,rule.workspace_id,rule.actor_id,rule.payload,rule.jql,
		       COALESCE(c.issue_id,''),COALESCE(c.initiator_id,''),rule.name,rule.rule_scope_aris,c.webhook_data,c.trigger_data
		FROM claimed c JOIN automation_rules rule ON rule.uuid=c.rule_uuid`, workspaceID).
		Scan(&run.ID, &run.RuleUUID, &run.ScheduledFor, &run.State, &run.Attempts, &run.StartedAt,
			&run.CompletedAt, &run.MatchedCount, &run.ChangedCount, &run.Detail, &run.WorkspaceID,
			&run.ActorID, &run.Payload, &run.JQL, &run.IssueID, &run.InitiatorID, &run.RuleName, &run.ScopeARIs,
			&run.WebhookData, &run.TriggerData)
	return run, err
}

func (r *Runner) execute(ctx context.Context, run *claimedRun) (int, int, error) {
	if err := validateExecutionActor(run.Payload, run.ActorID); err != nil {
		return 0, 0, err
	}
	components, err := ruleComponents(run.Payload)
	if err != nil {
		return 0, 0, err
	}
	// The action log records this rule as the cause of every change it makes.
	ctx = store.WithAutomationRule(ctx, run.RuleUUID)
	var issues []*models.Issue
	total := 0
	switch {
	case EventTriggers[triggerType(run.Payload)] != "" && run.IssueID == "":
		// An event with no work item of its own -- a deletion, a version or a
		// sprint -- runs once, with what it carried.
		changed, err := r.runComponents(ctx, run, nil, components)
		if err != nil {
			return 0, 0, err
		}
		if changed {
			return 0, 1, nil
		}
		return 0, 0, nil
	case triggerType(run.Payload) == WebhookTriggerType:
		// An incoming webhook runs for the work the request named, or, when it
		// named none, once with no work item, as Jira's webhook trigger does.
		if run.IssueID == "" {
			changed, err := r.runComponents(ctx, run, nil, components)
			if err != nil {
				return 0, 0, err
			}
			if changed {
				return 0, 1, nil
			}
			return 0, 0, nil
		}
		issue, applies, err := r.eventIssue(ctx, run)
		if err != nil || !applies {
			return 0, 0, err
		}
		issues, total = []*models.Issue{issue}, 1
	case run.IssueID != "":
		issue, applies, err := r.eventIssue(ctx, run)
		if err != nil || !applies {
			return 0, 0, err
		}
		issues, total = []*models.Issue{issue}, 1
	default:
		query, err := jql.Parse(run.JQL)
		if err != nil {
			return 0, 0, fmt.Errorf("parse JQL: %w", err)
		}
		if err := r.Service.Store.ExpandAppJQL(ctx, run.WorkspaceID, query); err != nil {
			return 0, 0, fmt.Errorf("expand app JQL: %w", err)
		}
		resolver, err := r.Service.Store.JQLResolver(ctx, run.WorkspaceID)
		if err != nil {
			return 0, 0, fmt.Errorf("resolve JQL fields: %w", err)
		}
		compiled := jql.CompileAt(query, run.ActorID, resolver, 2)
		if compiled.Err != nil {
			return 0, 0, fmt.Errorf("compile JQL: %w", compiled.Err)
		}
		issues, total, err = r.Service.Store.Search(ctx, run.WorkspaceID, run.ActorID, compiled, 1000, 0)
		if err != nil {
			return 0, 0, fmt.Errorf("search issues: %w", err)
		}
		if total > 1000 {
			return total, 0, fmt.Errorf("JQL matched %d issues; scheduled rules are limited to 1000 per run", total)
		}
	}
	changedIssues := 0
	for _, issue := range issues {
		changed, err := r.runComponents(ctx, run, issue, components)
		if err != nil {
			return total, changedIssues, err
		}
		if changed {
			changedIssues++
		}
	}
	return total, changedIssues, nil
}

// errIssueDeleted reports that an action removed the work item the rule was
// running for. It stops the rule for that work item without failing the run.
var errIssueDeleted = errors.New("the work item was deleted")

// eventIssue loads the work item an event run started from, reporting whether
// the rule still applies: the rule actor can see it, it is in the rule's scope
// and it matches the trigger's JQL.
func (r *Runner) eventIssue(ctx context.Context, run *claimedRun) (*models.Issue, bool, error) {
	issue, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, run.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	visible, err := authz.CanSeeIssue(ctx, r.Service.Store, run.WorkspaceID, issue.ProjectID, run.ActorID, issue.ID, issue.SecurityLevelID)
	if err != nil || !visible || issue.ArchivedAt != "" {
		return nil, false, err
	}
	cloudID, err := r.Service.WorkspaceCloudID(ctx, run.WorkspaceID)
	if err != nil {
		return nil, false, err
	}
	if !ruleAppliesTo(&Rule{RuleScopeARIs: run.ScopeARIs}, cloudID, issue.ProjectID) {
		return nil, false, nil
	}
	if strings.TrimSpace(run.JQL) != "" {
		match, err := r.matchesJQL(ctx, run, issue, run.JQL)
		if err != nil || !match {
			return nil, false, err
		}
	}
	return issue, true, nil
}

// runComponents runs a rule's conditions and actions in order on a work item:
// a condition that does not hold stops the rule for it, and each action sees
// the work item as the previous one left it.
func (r *Runner) runComponents(ctx context.Context, run *claimedRun, issue *models.Issue, components []component) (bool, error) {
	if run.TriggerIssue == nil && issue != nil {
		run.TriggerIssue = issue
		defer func() { run.TriggerIssue = nil }()
	}
	// A rule an incoming webhook ran without work items has no work item to
	// name in a failure, and no related work to branch over.
	where := "the webhook's request"
	if issue != nil {
		where = issue.Key
	}
	changed := false
	for _, item := range components {
		if item.Component == "BRANCH" {
			if issue == nil {
				return changed, fmt.Errorf("%s on %s: a branch needs a work item, and the webhook named none", item.Type, where)
			}
			related, err := r.relatedIssues(ctx, run, issue, item)
			if err != nil {
				return changed, fmt.Errorf("%s on %s: %w", item.Type, issue.Key, err)
			}
			for _, relatedIssue := range related {
				didChange, err := r.runComponents(ctx, run, relatedIssue, item.Children)
				if err != nil {
					return changed, err
				}
				changed = changed || didChange
			}
			continue
		}
		if item.Component == "CONDITION" {
			holds, err := r.condition(ctx, run, issue, item)
			if err != nil {
				return changed, fmt.Errorf("%s on %s: %w", item.Type, where, err)
			}
			if !holds {
				return changed, nil
			}
			continue
		}
		didChange, err := r.apply(ctx, run, issue, item)
		// The work item the rule was running for no longer exists, so nothing
		// after this can run for it. The rule stops here rather than failing.
		if errors.Is(err, errIssueDeleted) {
			return true, nil
		}
		if err != nil {
			return changed, fmt.Errorf("%s on %s: %w", item.Type, where, err)
		}
		if didChange {
			changed = true
			if issue != nil {
				if fresh, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, issue.ID); err == nil {
					issue = fresh
				}
			}
		}
	}
	return changed, nil
}

// matchesJQL reports whether the rule actor's JQL matches a work item.
func (r *Runner) matchesJQL(ctx context.Context, run *claimedRun, issue *models.Issue, text string) (bool, error) {
	query, err := jql.Parse(fmt.Sprintf("(%s) AND key = %s", text, strconv.Quote(issue.Key)))
	if err != nil {
		return false, fmt.Errorf("parse JQL: %w", err)
	}
	if err := r.Service.Store.ExpandAppJQL(ctx, run.WorkspaceID, query); err != nil {
		return false, fmt.Errorf("expand app JQL: %w", err)
	}
	resolver, err := r.Service.Store.JQLResolver(ctx, run.WorkspaceID)
	if err != nil {
		return false, err
	}
	compiled := jql.CompileAt(query, run.ActorID, resolver, 2)
	if compiled.Err != nil {
		return false, fmt.Errorf("compile JQL: %w", compiled.Err)
	}
	_, total, err := r.Service.Store.Search(ctx, run.WorkspaceID, run.ActorID, compiled, 1, 0)
	return total > 0, err
}

// matchingWorkExists reports whether the rule actor can see any work item the
// query matches, where matchesJQL asks the same of one work item.
func (r *Runner) matchingWorkExists(ctx context.Context, run *claimedRun, text string) (bool, error) {
	query, err := jql.Parse(text)
	if err != nil {
		return false, fmt.Errorf("parse JQL: %w", err)
	}
	if err := r.Service.Store.ExpandAppJQL(ctx, run.WorkspaceID, query); err != nil {
		return false, fmt.Errorf("expand app JQL: %w", err)
	}
	resolver, err := r.Service.Store.JQLResolver(ctx, run.WorkspaceID)
	if err != nil {
		return false, err
	}
	compiled := jql.CompileAt(query, run.ActorID, resolver, 2)
	if compiled.Err != nil {
		return false, fmt.Errorf("compile JQL: %w", compiled.Err)
	}
	_, total, err := r.Service.Store.Search(ctx, run.WorkspaceID, run.ActorID, compiled, 1, 0)
	return total > 0, err
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

// Actions and conditions the runner executes.
var (
	runnableActions    = map[string]bool{"jira.issue.add-label": true, "jira.issue.remove-label": true, "jira.issue.assign": true, "jira.issue.transition": true, "jira.issue.comment": true, "jira.issue.edit": true, "jira.issue.link": true, "jira.issue.create-subtask": true, "jira.issue.email": true, "jira.issue.create": true, WebRequestActionType: true, "jira.issue.log-work": true, "jira.issue.delete": true, WikiPageActionType: true}
	runnableConditions = map[string]bool{"jira.issue.condition": true, "jira.jql.condition": true, "jira.issue.related.condition": true}
)

// relatedTypes are the related work items a branch can run for.
var relatedTypes = map[string]bool{"sub-tasks": true, "parent": true, "linked": true}

// maxBranchIssues bounds how many related work items one branch runs for.
const maxBranchIssues = 100

func ruleComponents(payload json.RawMessage) ([]component, error) {
	var rule struct {
		Components []component `json:"components"`
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return nil, err
	}
	actions, err := validateComponents(rule.Components, false)
	if err != nil {
		return nil, err
	}
	if actions == 0 {
		return nil, errors.New("rule has no actions")
	}
	return rule.Components, nil
}

// validateComponents checks that the runner can execute every component and
// counts the actions. Branches hold conditions and actions, not branches.
func validateComponents(items []component, inBranch bool) (int, error) {
	actions := 0
	for _, item := range items {
		switch item.Component {
		case "CONDITION":
			if !runnableConditions[item.Type] {
				return 0, fmt.Errorf("unsupported condition %q", item.Type)
			}
		case "", "ACTION":
			if !runnableActions[item.Type] {
				return 0, fmt.Errorf("unsupported action %q", item.Type)
			}
			actions++
		case "BRANCH":
			if inBranch {
				return 0, errors.New("branches cannot contain other branches")
			}
			if item.Type != "jira.issue.related" {
				return 0, fmt.Errorf("unsupported branch %q", item.Type)
			}
			var value branchValue
			if err := json.Unmarshal(decodeComponentValue(item.Value), &value); err != nil || !relatedTypes[value.RelatedType] {
				return 0, errors.New("a related work items branch needs relatedType sub-tasks, parent or linked")
			}
			nested, err := validateComponents(item.Children, true)
			if err != nil {
				return 0, err
			}
			if nested == 0 {
				return 0, errors.New("a branch needs at least one action")
			}
			actions += nested
		default:
			return 0, fmt.Errorf("component %q is not executable", item.Component)
		}
	}
	return actions, nil
}

// relatedIssues finds the work items a branch runs for that the rule actor can
// see: a work item's sub-tasks, its parent, or work linked to it by the
// branch's link types, named as the work item reads the link (such as blocks
// or is blocked by) or by the link type's name.
func (r *Runner) relatedIssues(ctx context.Context, run *claimedRun, issue *models.Issue, branch component) ([]*models.Issue, error) {
	var value branchValue
	_ = json.Unmarshal(decodeComponentValue(branch.Value), &value)
	candidates := []*models.Issue{}
	switch value.RelatedType {
	case "sub-tasks":
		children, err := r.Service.Store.ChildIssues(ctx, run.WorkspaceID, issue.ID)
		if err != nil {
			return nil, err
		}
		candidates = children
	case "parent":
		if issue.Parent == nil || issue.Parent.ID == "" {
			return nil, nil
		}
		parent, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, issue.Parent.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, parent)
	case "linked":
		links, err := r.Service.Store.LinksByIssue(ctx, issue.ID)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, linkType := range value.LinkTypes {
			wanted[strings.ToLower(strings.TrimSpace(linkType))] = true
		}
		seen := map[string]bool{}
		for _, link := range links {
			// A link's outward work item "blocks" its inward one.
			other, phrase := link.InwardID, link.Outward
			if link.InwardID == issue.ID {
				other, phrase = link.OutwardID, link.Inward
			}
			if other == issue.ID || seen[other] {
				continue
			}
			if len(wanted) > 0 && !wanted[strings.ToLower(phrase)] && !wanted[strings.ToLower(link.TypeName)] {
				continue
			}
			seen[other] = true
			linked, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, other)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, linked)
		}
	default:
		return nil, fmt.Errorf("unsupported related work items %q", value.RelatedType)
	}
	visible := []*models.Issue{}
	for _, candidate := range candidates {
		if candidate.ArchivedAt != "" {
			continue
		}
		ok, err := authz.CanSeeIssue(ctx, r.Service.Store, run.WorkspaceID, candidate.ProjectID, run.ActorID, candidate.ID, candidate.SecurityLevelID)
		if err != nil {
			return nil, err
		}
		if ok {
			visible = append(visible, candidate)
		}
		if len(visible) == maxBranchIssues {
			break
		}
	}
	return visible, nil
}

// createProject is where a create action raises its work: the project the
// action names, the one the triggering work item is in, or -- when the event
// brought no work item -- the single project the rule is scoped to. A rule
// that names none of those says so rather than failing on a work item that
// is not there.
func (r *Runner) createProject(ctx context.Context, run *claimedRun, issue *models.Issue, named string) (string, error) {
	if named != "" {
		return named, nil
	}
	if issue != nil {
		return issue.ProjectID, nil
	}
	cloudID, err := r.Service.WorkspaceCloudID(ctx, run.WorkspaceID)
	if err != nil {
		return "", err
	}
	scoped := ""
	for _, ari := range run.ScopeARIs {
		kind, id, ok := parseObjectARI(cloudID, ari)
		if !ok || kind != "project" {
			continue
		}
		if scoped != "" && scoped != id {
			return "", errors.New("this action needs a project: the trigger brought no work item and the rule covers several")
		}
		scoped = id
	}
	if scoped == "" {
		return "", errors.New("this action needs a project: the trigger brought no work item, so name one in the action or scope the rule to a project")
	}
	return scoped, nil
}

func (r *Runner) apply(ctx context.Context, run *claimedRun, issue *models.Issue, action component) (bool, error) {
	// Every action this rule catalog holds acts on a work item, except
	// raising one and sending a request. Jira fails such an action when the
	// trigger supplied none, rather than skipping it.
	if issue == nil && action.Type != "jira.issue.create" && action.Type != WebRequestActionType && !strings.HasPrefix(action.Type, "jira.issue.create:") {
		return false, errors.New("this action needs a work item, and the trigger supplied none")
	}
	valueRaw := decodeComponentValue(action.Value)
	render := func(text string) (string, error) { return r.renderSmartValues(ctx, run, issue, text) }
	switch action.Type {
	case "jira.issue.add-label":
		var value struct {
			Label string `json:"label"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Label) == "" {
			return false, errors.New("label action requires value.label")
		}
		label, err := render(value.Label)
		if err != nil {
			return false, err
		}
		if value.Label = strings.TrimSpace(label); value.Label == "" {
			return false, errors.New("label action rendered an empty label")
		}
		if slices.Contains(issue.Labels, value.Label) {
			return false, nil
		}
		labels := append(slices.Clone(issue.Labels), value.Label)
		_, changed, err := r.Service.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, Labels: &labels,
		})
		return changed != nil, err
	case "jira.issue.remove-label":
		var value struct {
			Label string `json:"label"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Label) == "" {
			return false, errors.New("remove label action requires value.label")
		}
		label, err := render(value.Label)
		if err != nil {
			return false, err
		}
		if label = strings.TrimSpace(label); label == "" {
			return false, errors.New("remove label action rendered an empty label")
		}
		index := slices.Index(issue.Labels, label)
		if index < 0 {
			return false, nil
		}
		labels := slices.Delete(slices.Clone(issue.Labels), index, index+1)
		_, changed, err := r.Service.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, Labels: &labels,
		})
		return changed != nil, err
	case "jira.issue.assign":
		var value struct {
			AccountID string `json:"accountId"`
			Method    string `json:"method"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || (value.AccountID == "" && value.Method == "") {
			return false, errors.New("assign action requires value.accountId or value.method")
		}
		// Jira picks the person when the rule names a method rather than one
		// account, from the people the project may assign work to.
		if value.Method != "" {
			if !assignmentMethods[value.Method] {
				return false, fmt.Errorf("assign action cannot pick by %q", value.Method)
			}
			picked, err := r.pickAssignee(ctx, run, issue, value.Method)
			if err != nil {
				return false, err
			}
			if issue.Assignee != nil && issue.Assignee.ID == picked {
				return false, nil
			}
			_, changed, err := r.Service.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
				ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, AssigneeID: &picked,
			})
			return changed != nil, err
		}
		accountID, err := render(value.AccountID)
		if err != nil {
			return false, err
		}
		switch accountID {
		case "ACTOR":
			accountID = run.ActorID
		case "UNASSIGNED":
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
		// Rules name the target by the status id clients see.
		if target, lookupErr := r.Service.Store.StatusByID(ctx, value.StatusID); lookupErr == nil {
			value.StatusID = target.ID
		}
		if issue.Status.ID == value.StatusID {
			return false, nil
		}
		workflow, err := r.Service.Store.WorkflowForProjectAndIssueType(ctx, issue.ProjectID, issue.IssueType.ID)
		if err != nil {
			return false, err
		}
		for _, transition := range workflow.Available(issue.Status.ID) {
			if transition.To == value.StatusID {
				_, changed, err := r.Service.Commands.TransitionIssue(ctx, run.ActorID, run.WorkspaceID, issue.ID, transition.ID)
				return changed != nil, err
			}
		}
		return false, fmt.Errorf("no workflow transition from %s to status %s", issue.Status.Name, value.StatusID)
	case "jira.issue.link":
		var value struct {
			LinkTypeID string `json:"linkTypeId"`
			IssueKey   string `json:"issueKey"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.LinkTypeID) == "" || strings.TrimSpace(value.IssueKey) == "" {
			return false, errors.New("link action requires value.linkTypeId and value.issueKey")
		}
		key, err := render(value.IssueKey)
		if err != nil {
			return false, err
		}
		if key = strings.TrimSpace(key); key == "" {
			return false, errors.New("link action rendered an empty work item key")
		}
		other, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, key)
		if err != nil {
			return false, fmt.Errorf("link action names %s, which is not a work item of this site", key)
		}
		if other.ID == issue.ID {
			return false, nil
		}
		// The link the rule names already holding means there is nothing to do.
		links, err := r.Service.Store.LinksByIssue(ctx, issue.ID)
		if err != nil {
			return false, err
		}
		for _, link := range links {
			if link.TypeID != value.LinkTypeID {
				continue
			}
			if (link.InwardID == issue.ID && link.OutwardID == other.ID) || (link.OutwardID == issue.ID && link.InwardID == other.ID) {
				return false, nil
			}
		}
		if _, _, err := r.Service.Commands.LinkIssue(ctx, run.ActorID, run.WorkspaceID, issue.ID, value.LinkTypeID, other.ID); err != nil {
			return false, err
		}
		return true, nil
	case "jira.issue.create":
		var value struct {
			IssueTypeID string `json:"issueTypeId"`
			Summary     string `json:"summary"`
			// ProjectID is where the work is raised when the trigger brought
			// no work item to take it from -- a deletion, a version, a
			// sprint, or a webhook that named none.
			ProjectID string `json:"projectId"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.IssueTypeID) == "" || strings.TrimSpace(value.Summary) == "" {
			return false, errors.New("create action requires value.issueTypeId and value.summary")
		}
		summary, err := render(value.Summary)
		if err != nil {
			return false, err
		}
		if summary = strings.TrimSpace(summary); summary == "" {
			return false, errors.New("create action rendered an empty summary")
		}
		projectID, err := r.createProject(ctx, run, issue, strings.TrimSpace(value.ProjectID))
		if err != nil {
			return false, err
		}
		// The work type comes from the project's own scheme, so a rule cannot
		// raise a type the project does not offer, and sub-tasks keep their own
		// action because they need a parent.
		issueTypes, err := r.Service.Store.ProjectIssueTypes(ctx, run.WorkspaceID, projectID, nil)
		if err != nil {
			return false, err
		}
		var wanted *models.IssueType
		for index, issueType := range issueTypes {
			if issueType.ID == value.IssueTypeID && !issueType.Subtask {
				wanted = &issueTypes[index]
				break
			}
		}
		if wanted == nil {
			return false, errors.New("the project offers no such work type")
		}
		project, err := r.Service.Store.ProjectByIDOrKey(ctx, run.WorkspaceID, projectID)
		if err != nil {
			return false, err
		}
		// Work of that type and summary already in the project means there is
		// nothing to do, so a scheduled rule does not raise one every interval.
		existing, err := r.matchingWorkExists(ctx, run, fmt.Sprintf("project = %s AND issuetype = %s AND summary = %s",
			jql.Quote(project.Key), jql.Quote(wanted.Name), jql.Quote(summary)))
		if err != nil {
			return false, err
		}
		if existing {
			return false, nil
		}
		created, _, err := r.Service.Commands.CreateIssue(ctx, commands.CreateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, ProjectIDOrKey: projectID,
			Summary: summary, IssueTypeID: wanted.ID,
		})
		return created != nil, err
	case "jira.issue.create-subtask":
		var value struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Summary) == "" {
			return false, errors.New("create sub-task action requires value.summary")
		}
		summary, err := render(value.Summary)
		if err != nil {
			return false, err
		}
		if summary = strings.TrimSpace(summary); summary == "" {
			return false, errors.New("create sub-task action rendered an empty summary")
		}
		// A sub-task of that name already hanging under the work item means
		// there is nothing to do, so a rule that runs again does not mint a
		// second one.
		children, err := r.Service.Store.ChildIssues(ctx, run.WorkspaceID, issue.ID)
		if err != nil {
			return false, err
		}
		for _, child := range children {
			if strings.EqualFold(child.Summary, summary) {
				return false, nil
			}
		}
		// The sub-task takes the project's own sub-task work type, so a rule
		// cannot raise one the project's scheme does not offer.
		issueTypes, err := r.Service.Store.ProjectIssueTypes(ctx, run.WorkspaceID, issue.ProjectID, nil)
		if err != nil {
			return false, err
		}
		subtaskTypeID := ""
		for _, issueType := range issueTypes {
			if issueType.Subtask {
				subtaskTypeID = issueType.ID
				break
			}
		}
		if subtaskTypeID == "" {
			return false, errors.New("the project offers no sub-task work type")
		}
		created, _, err := r.Service.Commands.CreateIssue(ctx, commands.CreateIssueInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, ProjectIDOrKey: issue.ProjectID,
			Summary: summary, IssueTypeID: subtaskTypeID, ParentIDOrKey: issue.ID,
		})
		return created != nil, err
	case "jira.issue.email":
		var value struct {
			Recipient string `json:"recipient"`
			Body      string `json:"body"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Body) == "" {
			return false, errors.New("email action requires value.body")
		}
		body, err := render(value.Body)
		if err != nil {
			return false, err
		}
		if body = strings.TrimSpace(body); body == "" {
			return false, errors.New("email action rendered an empty message")
		}
		// The recipients are resolved by id, because a work item carries a
		// user's identity and display name rather than an address.
		recipientIDs := []string{}
		switch value.Recipient {
		case "assignee":
			if issue.Assignee != nil && issue.Assignee.ID != "" {
				recipientIDs = append(recipientIDs, issue.Assignee.ID)
			}
		case "reporter":
			if issue.Reporter != nil && issue.Reporter.ID != "" {
				recipientIDs = append(recipientIDs, issue.Reporter.ID)
			}
		case "watchers":
			watchers, err := r.Service.Store.WatchersByIssue(ctx, issue.ID)
			if err != nil {
				return false, err
			}
			recipientIDs = watchers
		default:
			return false, fmt.Errorf("email action cannot send to %q", value.Recipient)
		}
		// Work nobody is assigned or watching leaves no one to write to, so the
		// rule changes nothing rather than queueing mail addressed to no one.
		subject := fmt.Sprintf("[%s] %s", issue.Key, issue.Summary)
		sent := false
		for _, recipientID := range recipientIDs {
			user, err := r.Service.Store.UserByID(ctx, recipientID)
			if err != nil || user == nil || strings.TrimSpace(user.Email) == "" || !user.Active {
				continue
			}
			if err := r.Service.Store.QueueEmail(ctx, run.WorkspaceID, user.Email, subject, body); err != nil {
				return sent, err
			}
			sent = true
		}
		return sent, nil
	case WikiPageActionType:
		var value struct {
			SpaceKey string `json:"spaceKey"`
			Title    string `json:"title"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.SpaceKey) == "" {
			return false, errors.New("create page action requires value.spaceKey")
		}
		return r.createWikiPage(ctx, run, issue, value.SpaceKey, value.Title, render)
	case "jira.issue.delete":
		// Jira deletes as the rule actor, so a rule may delete only what its
		// actor may delete.
		if _, err := r.Service.Commands.DeleteIssue(ctx, run.ActorID, run.WorkspaceID, issue.ID, "deleted by automation rule "+run.RuleName); err != nil {
			return false, err
		}
		return true, errIssueDeleted
	case "jira.issue.log-work":
		var value struct {
			Duration string `json:"duration"`
			Comment  string `json:"comment"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Duration) == "" {
			return false, errors.New("log work action requires value.duration")
		}
		spent, err := render(value.Duration)
		if err != nil {
			return false, err
		}
		// A duration is read the way the site reads every other one, so a
		// week and a day mean what its time tracking says they mean.
		configuration, err := r.Service.Store.JiraSiteConfiguration(ctx, run.WorkspaceID)
		if err != nil {
			return false, err
		}
		seconds, err := models.ParseJiraDuration(strings.TrimSpace(spent), configuration.TimeTracking)
		if err != nil {
			return false, fmt.Errorf("log work action: %w", err)
		}
		if seconds <= 0 {
			return false, errors.New("log work action needs a duration above zero")
		}
		var comment json.RawMessage
		if strings.TrimSpace(value.Comment) != "" {
			text, err := render(value.Comment)
			if err != nil {
				return false, err
			}
			comment = adf.ParagraphDoc(strings.TrimSpace(text))
		}
		worklog, _, err := r.Service.Commands.AddWorklog(ctx, run.ActorID, run.WorkspaceID, issue.ID, comment, int(seconds))
		return worklog != nil, err
	case WebRequestActionType:
		var value struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			// Body and Headers are what the receiver asked for: a rule that
			// writes them sends them instead of the site's own body.
			Body    string            `json:"body"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.URL) == "" {
			return false, errors.New("web request action requires value.url")
		}
		address, err := render(value.URL)
		if err != nil {
			return false, err
		}
		extras := webRequestExtras{Headers: map[string]string{}}
		if extras.Body, err = render(value.Body); err != nil {
			return false, err
		}
		for name, header := range value.Headers {
			rendered, headerErr := render(header)
			if headerErr != nil {
				return false, headerErr
			}
			extras.Headers[name] = rendered
		}
		return r.sendWebRequest(ctx, run, issue, value.Method, address, extras)
	case "jira.issue.comment":
		var value struct {
			Comment string `json:"comment"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil || strings.TrimSpace(value.Comment) == "" {
			return false, errors.New("comment action requires value.comment")
		}
		text, err := render(value.Comment)
		if err != nil {
			return false, err
		}
		_, commented, err := r.Service.Commands.AddComment(ctx, commands.AddCommentInput{
			ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID, PlainText: text,
		})
		return commented != nil, err
	case "jira.issue.edit":
		var value struct {
			Field string `json:"field"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(valueRaw, &value); err != nil {
			return false, errors.New("edit action requires value.field and value.value")
		}
		text, err := render(value.Value)
		if err != nil {
			return false, err
		}
		text = strings.TrimSpace(text)
		input := commands.UpdateIssueInput{ActorID: run.ActorID, WorkspaceID: run.WorkspaceID, IssueIDOrKey: issue.ID}
		switch value.Field {
		case "summary":
			if text == "" {
				return false, errors.New("edit action rendered an empty summary")
			}
			if text == issue.Summary {
				return false, nil
			}
			input.Summary = &text
		case "duedate":
			if text == issue.DueDate {
				return false, nil
			}
			input.DueDate = &text
		case "labels":
			// Jira's edit sets the labels the rule names, rather than adding
			// to what the work item carries, which is what add label does.
			wanted := []string{}
			for _, label := range strings.Split(text, ",") {
				if label = strings.TrimSpace(label); label != "" {
					wanted = append(wanted, label)
				}
			}
			current := append([]string{}, issue.Labels...)
			held := append([]string{}, wanted...)
			slices.Sort(current)
			slices.Sort(held)
			if slices.Equal(current, held) {
				return false, nil
			}
			input.Labels = &wanted
		case "description":
			// The work item already reads as the rule would leave it.
			if strings.TrimSpace(adf.PlainText(issue.Description)) == text {
				return false, nil
			}
			input.Description = adf.ParagraphDoc(text)
		case "priority":
			current := ""
			if issue.Priority != nil {
				current = issue.Priority.Name
			}
			// The work item already holds the priority the rule names, whether
			// the rule named it by name or by id.
			if strings.EqualFold(text, current) || (issue.Priority != nil && text == issue.Priority.ID) {
				return false, nil
			}
			input.PriorityID = &text
		default:
			return false, fmt.Errorf("edit action cannot change %q", value.Field)
		}
		_, changed, err := r.Service.Commands.UpdateIssue(ctx, input)
		return changed != nil, err
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
