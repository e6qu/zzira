package automation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

const eventBatch = 500

type eventRule struct {
	UUID, Event     string
	Value           eventTriggerValue
	AllowOtherRules bool
	fromIDs, toIDs  map[string]bool
	fields          map[string]bool
}

type loggedAction struct {
	Seq                  int64
	EntityType, EntityID string
	Op                   string
	Payload              []byte
	ActorID              string
	CausedBy             *string
	First                bool
}

// enqueueEvents turns new actions in the workspace's log into runs of the
// enabled rules whose work item event triggers they match. Locking the cursor
// row keeps replicas from reading the same events, and a run per rule and
// event keeps a retried batch from repeating one. Rules only see events from
// after they could run: history is never replayed.
func (r *Runner) enqueueEvents(ctx context.Context, workspaceID string) error {
	tx, err := r.Service.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var last int64
	if err := tx.QueryRow(ctx, `SELECT last_seq FROM automation_event_cursors WHERE workspace_id=$1 FOR UPDATE SKIP LOCKED`, workspaceID).Scan(&last); err != nil {
		// Another replica holds the cursor, or this is the first look: start
		// from the current head.
		if _, err := tx.Exec(ctx, `INSERT INTO automation_event_cursors(workspace_id,last_seq) SELECT id,seq FROM workspaces WHERE id=$1 ON CONFLICT DO NOTHING`, workspaceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	rules, err := r.eventRules(ctx, workspaceID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		if _, err := tx.Exec(ctx, `UPDATE automation_event_cursors SET last_seq=GREATEST(last_seq,(SELECT seq FROM workspaces WHERE id=$1)) WHERE workspace_id=$1`, workspaceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `
		SELECT a.seq, a.entity_type, a.entity_id, a.op, a.payload, a.actor_id, a.automation_rule_uuid::text,
		       NOT EXISTS (SELECT 1 FROM actions earlier WHERE earlier.workspace_id=a.workspace_id AND earlier.entity_type=a.entity_type AND earlier.entity_id=a.entity_id AND earlier.seq<a.seq)
		FROM actions a WHERE a.workspace_id=$1 AND a.seq>$2 AND a.entity_type IN ('issue','comment','issue_link') AND a.op='upsert'
		ORDER BY a.seq LIMIT $3`, workspaceID, last, eventBatch)
	if err != nil {
		return err
	}
	actions := []loggedAction{}
	for rows.Next() {
		var action loggedAction
		if err := rows.Scan(&action.Seq, &action.EntityType, &action.EntityID, &action.Op, &action.Payload, &action.ActorID, &action.CausedBy, &action.First); err != nil {
			rows.Close()
			return err
		}
		actions = append(actions, action)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	next := last
	if len(actions) < eventBatch {
		// Everything up to the head has been read, including actions that
		// are not work item events.
		if err := tx.QueryRow(ctx, `SELECT GREATEST($2::bigint, seq) FROM workspaces WHERE id=$1`, workspaceID, last).Scan(&next); err != nil {
			return err
		}
	}
	for _, action := range actions {
		next = max(next, action.Seq)
		event, issueID, diff := actionEvent(action)
		if event == "" {
			continue
		}
		for _, rule := range rules {
			if !rule.matches(event, diff, action) {
				continue
			}
			id, err := NewUUIDv7()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state,trigger_seq,issue_id,initiator_id)
				VALUES($1,$2,clock_timestamp(),'PENDING',$3,$4,$5)
				ON CONFLICT (rule_uuid,trigger_seq) WHERE trigger_seq IS NOT NULL DO NOTHING`, id, rule.UUID, action.Seq, issueID, action.ActorID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE automation_event_cursors SET last_seq=$2 WHERE workspace_id=$1`, workspaceID, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Runner) eventRules(ctx context.Context, workspaceID string) ([]eventRule, error) {
	rows, err := r.Service.Store.Pool.Query(ctx, `SELECT uuid::text,event_trigger,payload FROM automation_rules WHERE workspace_id=$1 AND state='ENABLED' AND event_trigger IS NOT NULL ORDER BY uuid`, workspaceID)
	if err != nil {
		return nil, err
	}
	rules := []eventRule{}
	for rows.Next() {
		var rule eventRule
		var payload []byte
		if err := rows.Scan(&rule.UUID, &rule.Event, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		var decoded struct {
			CanOtherRuleTrigger bool `json:"canOtherRuleTrigger"`
			Trigger             struct {
				Value json.RawMessage `json:"value"`
			} `json:"trigger"`
		}
		_ = json.Unmarshal(payload, &decoded)
		rule.AllowOtherRules = decoded.CanOtherRuleTrigger
		_ = json.Unmarshal(decodeComponentValue(decoded.Trigger.Value), &rule.Value)
		rules = append(rules, rule)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Statuses are named by the ids clients see or by stored ids.
	for index := range rules {
		rule := &rules[index]
		rule.fromIDs, rule.toIDs, rule.fields = map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, pair := range []struct {
			ids []string
			set map[string]bool
		}{{rule.Value.FromStatusIDs, rule.fromIDs}, {rule.Value.ToStatusIDs, rule.toIDs}} {
			for _, id := range pair.ids {
				pair.set[id] = true
				if status, err := r.Service.Store.StatusByID(ctx, id); err == nil {
					pair.set[status.ID] = true
				}
			}
		}
		for _, field := range rule.Value.Fields {
			rule.fields[strings.ToLower(strings.TrimSpace(field))] = true
		}
	}
	return rules, nil
}

// actionEvent reads the work item event an action records: created, updated
// (with its changes) or commented, and the work item it happened to.
func actionEvent(action loggedAction) (string, string, map[string]models.ChangeItem) {
	switch action.EntityType {
	case models.EntityIssue:
		var payload models.IssueUpdatePayload
		if json.Unmarshal(action.Payload, &payload) != nil || payload.SuppressEvents {
			return "", "", nil
		}
		if action.First {
			return "created", action.EntityID, nil
		}
		if len(payload.Diff) > 0 {
			return "updated", action.EntityID, payload.Diff
		}
	case models.EntityComment:
		var payload models.CommentUpsertPayload
		if action.First && json.Unmarshal(action.Payload, &payload) == nil && payload.Comment.IssueID != "" {
			return "commented", payload.Comment.IssueID, nil
		}
	case models.EntityIssueLink:
		// A link joins two work items but starts one run, because a run is
		// keyed by its rule and the action it came from. It starts for the
		// outward work item, the side that acts: an outward work item blocks
		// its inward one, and the link action puts the rule's work item there.
		var payload models.IssueLinkPayload
		if action.Op == models.OpUpsert && json.Unmarshal(action.Payload, &payload) == nil && payload.Link.OutwardID != "" {
			return "linked", payload.Link.OutwardID, nil
		}
	}
	return "", "", nil
}

// matches reports whether an event starts a rule. A rule never starts from
// its own actions, and from another rule's only when it allows that.
func (rule eventRule) matches(event string, diff map[string]models.ChangeItem, action loggedAction) bool {
	if action.CausedBy != nil && (*action.CausedBy == rule.UUID || !rule.AllowOtherRules) {
		return false
	}
	switch rule.Event {
	case "created", "commented", "linked":
		return event == rule.Event
	case "transitioned":
		change, ok := diff["status"]
		if event != "updated" || !ok {
			return false
		}
		return (len(rule.fromIDs) == 0 || rule.fromIDs[change.From]) && (len(rule.toIDs) == 0 || rule.toIDs[change.To])
	case "field_changed":
		if event != "updated" {
			return false
		}
		for field := range diff {
			if rule.fields[strings.ToLower(field)] {
				return true
			}
		}
	}
	return false
}
