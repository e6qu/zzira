// Package automation implements Jira Cloud Automation rule management and
// durable scheduled rule execution.
package automation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/cron"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/store"
)

const maxRulePage = 100

type Service struct {
	Store    *store.Store
	Commands *commands.Service
}

type Rule struct {
	UUID            string
	WorkspaceID     string
	AuthorID        string
	ActorID         string
	Name            string
	Description     string
	Labels          []string
	State           string
	RuleScopeARIs   []string
	Payload         json.RawMessage
	Connections     json.RawMessage
	IntervalMinutes *int
	// CronExpression is the Quartz cron expression a scheduled rule follows
	// instead of a fixed interval.
	CronExpression string
	// EventTrigger is the work item event a rule starts from: created,
	// transitioned, field_changed or commented; empty for other triggers.
	EventTrigger        string
	ScheduleTimezone    string
	JQL                 string
	NextRunAt           *time.Time
	ConsecutiveFailures int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Run struct {
	ID           string
	RuleUUID     string
	ScheduledFor time.Time
	State        string
	Attempts     int
	StartedAt    *time.Time
	CompletedAt  *time.Time
	MatchedCount int
	ChangedCount int
	Detail       string
}

type SummaryFilter struct {
	States   []string
	Triggers []string
	ScopeARI string
	AuthorID string
	Limit    int
	Cursor   string
}

type SummaryPage struct {
	Rules      []*Rule
	Offset     int
	Limit      int
	Total      int
	NextCursor string
	PrevCursor string
}

type ruleWrite struct {
	Rule        map[string]json.RawMessage `json:"rule"`
	Connections json.RawMessage            `json:"connections"`
}

type nativeTriggerValue struct {
	IntervalMinutes int    `json:"intervalMinutes"`
	Timezone        string `json:"timezone"`
	JQL             string `json:"jql"`
	CronExpression  string `json:"cronExpression"`
	Schedule        *struct {
		IntervalMinutes int    `json:"intervalMinutes"`
		Timezone        string `json:"timezone"`
		Method          string `json:"method"`
		CronExpression  string `json:"cronExpression"`
	} `json:"schedule"`
}

func NewUUIDv7() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	millis := uint64(time.Now().UnixMilli())
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], millis)
	copy(value[:6], timestamp[2:])
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value[14] != '7' || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func (s *Service) WorkspaceCloudID(ctx context.Context, workspaceID string) (string, error) {
	var id string
	err := s.Store.Pool.QueryRow(ctx, `SELECT cloud_id::text FROM workspaces WHERE id=$1`, workspaceID).Scan(&id)
	return id, err
}

func (s *Service) Rule(ctx context.Context, workspaceID, uuid string) (*Rule, error) {
	return scanRule(s.Store.Pool.QueryRow(ctx, ruleSelect+` WHERE workspace_id=$1 AND uuid=$2`, workspaceID, uuid))
}

func (s *Service) Rules(ctx context.Context, workspaceID string, filter SummaryFilter) (*SummaryPage, error) {
	rows, err := s.Store.Pool.Query(ctx, ruleSelect+` WHERE workspace_id=$1 ORDER BY uuid`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := make([]*Rule, 0)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		if matches(rule, filter) {
			all = append(all, rule)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit == 0 {
		limit = maxRulePage
	}
	if limit < 0 || limit > maxRulePage {
		return nil, fmt.Errorf("limit must be between 1 and %d", maxRulePage)
	}
	offset, err := decodeCursor(filter.Cursor)
	if err != nil || offset > len(all) {
		return nil, fmt.Errorf("cursor is invalid or expired")
	}
	end := min(offset+limit, len(all))
	page := &SummaryPage{Rules: all[offset:end], Offset: offset, Limit: limit, Total: len(all)}
	if end < len(all) {
		page.NextCursor = encodeCursor(end)
	}
	if offset > 0 {
		page.PrevCursor = encodeCursor(max(0, offset-limit))
	}
	return page, nil
}

func matches(rule *Rule, filter SummaryFilter) bool {
	if filter.AuthorID != "" && rule.AuthorID != filter.AuthorID {
		return false
	}
	if filter.ScopeARI != "" && !contains(rule.RuleScopeARIs, filter.ScopeARI) {
		return false
	}
	if len(filter.States) > 0 && !contains(filter.States, rule.State) {
		return false
	}
	if len(filter.Triggers) > 0 && !contains(filter.Triggers, triggerType(rule.Payload)) {
		return false
	}
	return true
}

// cursorLifetime is how long an Automation page cursor stays usable.
const cursorLifetime = time.Hour

func encodeCursor(offset int) string {
	return encodeCursorAt(offset, time.Now())
}

// encodeCursorAt stamps a page offset with the time its cursor was issued.
func encodeCursorAt(offset int, issued time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset) + "." + strconv.FormatInt(issued.Unix(), 10)))
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	offsetText, issuedText, stamped := strings.Cut(string(raw), ".")
	offset, err := strconv.Atoi(offsetText)
	issued, issuedErr := strconv.ParseInt(issuedText, 10, 64)
	if !stamped || err != nil || issuedErr != nil || offset < 0 {
		return 0, errors.New("invalid cursor")
	}
	if time.Since(time.Unix(issued, 0)) > cursorLifetime {
		return 0, errors.New("the cursor has expired")
	}
	return offset, nil
}

func (s *Service) CreateRule(ctx context.Context, workspaceID, requesterID string, body json.RawMessage) (string, error) {
	prepared, err := s.prepareRule(ctx, workspaceID, requesterID, "", body)
	if err != nil {
		return "", err
	}
	_, err = s.Store.Pool.Exec(ctx, `
		INSERT INTO automation_rules
		(uuid,workspace_id,author_id,actor_id,name,description,labels,state,rule_scope_aris,payload,connections,interval_minutes,schedule_timezone,jql,next_run_at,event_trigger,cron_expression)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
		       CASE WHEN $8::text='ENABLED' AND $12::int IS NOT NULL THEN now()+make_interval(mins=>$12::int) WHEN $8::text='ENABLED' THEN $17::timestamptz END,
		       NULLIF($15,''),NULLIF($16,''))`,
		prepared.UUID, workspaceID, prepared.AuthorID, prepared.ActorID, prepared.Name, prepared.Description,
		prepared.Labels, prepared.State, prepared.RuleScopeARIs, prepared.Payload, prepared.Connections,
		prepared.IntervalMinutes, prepared.ScheduleTimezone, prepared.JQL, prepared.EventTrigger,
		prepared.CronExpression, nextCronRun(prepared.CronExpression, prepared.ScheduleTimezone, time.Now()))
	if err != nil {
		return "", fmt.Errorf("create rule: %w", err)
	}
	return prepared.UUID, nil
}

func (s *Service) UpdateRule(ctx context.Context, workspaceID, requesterID, uuid string, body json.RawMessage) error {
	if _, err := s.Rule(ctx, workspaceID, uuid); err != nil {
		return err
	}
	prepared, err := s.prepareRule(ctx, workspaceID, requesterID, uuid, body)
	if err != nil {
		return err
	}
	tag, err := s.Store.Pool.Exec(ctx, `
		UPDATE automation_rules SET author_id=$3,actor_id=$4,name=$5,description=$6,labels=$7,state=$8,
		 rule_scope_aris=$9,payload=$10,connections=$11,interval_minutes=$12,schedule_timezone=$13,jql=$14,
		 next_run_at=CASE WHEN $8::text='ENABLED' AND $12::int IS NOT NULL THEN COALESCE(CASE WHEN cron_expression IS NULL THEN next_run_at END,now()+make_interval(mins=>$12::int))
		                  WHEN $8::text='ENABLED' THEN $17::timestamptz END,
		 event_trigger=NULLIF($15,''),cron_expression=NULLIF($16,''),updated_at=now() WHERE workspace_id=$1 AND uuid=$2`, workspaceID, uuid,
		prepared.AuthorID, prepared.ActorID, prepared.Name, prepared.Description, prepared.Labels, prepared.State,
		prepared.RuleScopeARIs, prepared.Payload, prepared.Connections, prepared.IntervalMinutes, prepared.ScheduleTimezone, prepared.JQL, prepared.EventTrigger,
		prepared.CronExpression, nextCronRun(prepared.CronExpression, prepared.ScheduleTimezone, time.Now()))
	if err != nil {
		return fmt.Errorf("update rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Service) SetState(ctx context.Context, workspaceID, uuid, state string) error {
	if state != "ENABLED" && state != "DISABLED" {
		return fmt.Errorf("state must be ENABLED or DISABLED")
	}
	var next *time.Time
	if state == "ENABLED" {
		if rule, err := s.Rule(ctx, workspaceID, uuid); err == nil {
			next = nextCronRun(rule.CronExpression, rule.ScheduleTimezone, time.Now())
		}
	}
	tag, err := s.Store.Pool.Exec(ctx, `
		UPDATE automation_rules SET state=$3,payload=jsonb_set(payload,'{state}',to_jsonb($3::text)),
		 next_run_at=CASE WHEN $3='ENABLED' AND interval_minutes IS NOT NULL THEN COALESCE(next_run_at,now()+make_interval(mins=>interval_minutes))
		                  WHEN $3='ENABLED' AND cron_expression IS NOT NULL THEN $4::timestamptz END,
		 updated_at=now() WHERE workspace_id=$1 AND uuid=$2`, workspaceID, uuid, state, next)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Service) SetScope(ctx context.Context, workspaceID, uuid string, scope []string) error {
	raw, _ := json.Marshal(scope)
	tag, err := s.Store.Pool.Exec(ctx, `
		UPDATE automation_rules SET rule_scope_aris=$3,payload=jsonb_set(payload,'{ruleScopeARIs}',$4::jsonb),updated_at=now()
		WHERE workspace_id=$1 AND uuid=$2`, workspaceID, uuid, scope, raw)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Service) DeleteRule(ctx context.Context, workspaceID, uuid string) error {
	tag, err := s.Store.Pool.Exec(ctx, `DELETE FROM automation_rules WHERE workspace_id=$1 AND uuid=$2 AND state='DISABLED'`, workspaceID, uuid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var state string
		if err := s.Store.Pool.QueryRow(ctx, `SELECT state FROM automation_rules WHERE workspace_id=$1 AND uuid=$2`, workspaceID, uuid).Scan(&state); err != nil {
			return err
		}
		return fmt.Errorf("rule must be disabled before it can be deleted")
	}
	return nil
}

func (s *Service) Runs(ctx context.Context, workspaceID, uuid string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.Store.Pool.Query(ctx, `
		SELECT ar.id,ar.rule_uuid::text,ar.scheduled_for,ar.state,ar.attempts,ar.started_at,ar.completed_at,
		       ar.matched_count,ar.changed_count,ar.detail
		FROM automation_runs ar JOIN automation_rules r ON r.uuid=ar.rule_uuid
		WHERE r.workspace_id=$1 AND r.uuid=$2 ORDER BY ar.scheduled_for DESC LIMIT $3`, workspaceID, uuid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.RuleUUID, &run.ScheduledFor, &run.State, &run.Attempts, &run.StartedAt, &run.CompletedAt, &run.MatchedCount, &run.ChangedCount, &run.Detail); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Service) EnqueueNow(ctx context.Context, workspaceID, uuid string) error {
	id, err := NewUUIDv7()
	if err != nil {
		return err
	}
	tag, err := s.Store.Pool.Exec(ctx, `
		INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state)
		SELECT $3,uuid,clock_timestamp(),'PENDING' FROM automation_rules
		WHERE workspace_id=$1 AND uuid=$2 AND state='ENABLED'`, workspaceID, uuid, id)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("only enabled rules can run")
	}
	return err
}

func (s *Service) prepareRule(ctx context.Context, workspaceID, requesterID, forcedUUID string, body json.RawMessage) (*Rule, error) {
	var write ruleWrite
	if err := json.Unmarshal(body, &write); err != nil || write.Rule == nil {
		return nil, fmt.Errorf("body must contain a rule object")
	}
	name := strings.TrimSpace(jsonString(write.Rule["name"]))
	if name == "" || len(name) > 255 {
		return nil, fmt.Errorf("rule.name is required and must be at most 255 characters")
	}
	state := strings.ToUpper(jsonString(write.Rule["state"]))
	if state == "" {
		state = "ENABLED"
	}
	if !validState(state) {
		return nil, fmt.Errorf("rule.state must be ENABLED or DISABLED")
	}
	uuid := jsonString(write.Rule["uuid"])
	if forcedUUID != "" {
		uuid = forcedUUID
	} else if uuid == "" {
		var err error
		uuid, err = NewUUIDv7()
		if err != nil {
			return nil, err
		}
	}
	if !isUUIDv7(uuid) {
		return nil, fmt.Errorf("rule.uuid must be a UUID version 7")
	}
	actorID := requesterID
	actorValue := map[string]string{"actor": requesterID, "type": "ACCOUNT_ID"}
	var actor struct {
		Actor string `json:"actor"`
		Type  string `json:"type"`
	}
	if raw := write.Rule["actor"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &actor); err != nil || actor.Actor == "" || (actor.Type != "ACCOUNT_ID" && actor.Type != "EVENT_INITIATOR" && actor.Type != "SMART_VALUE") {
			return nil, fmt.Errorf("rule.actor must contain a supported actor type and value")
		}
		actorValue = map[string]string{"actor": actor.Actor, "type": actor.Type}
		if actor.Type == "ACCOUNT_ID" {
			actorID = actor.Actor
		}
	}
	if _, err := s.Store.MemberByID(ctx, workspaceID, actorID); err != nil {
		return nil, fmt.Errorf("rule actor is not an active workspace member")
	}
	authorID := jsonString(write.Rule["authorAccountId"])
	if authorID == "" {
		authorID = requesterID
	}
	if _, err := s.Store.MemberByID(ctx, workspaceID, authorID); err != nil {
		return nil, fmt.Errorf("rule author is not an active workspace member")
	}
	write.Rule["uuid"], _ = json.Marshal(uuid)
	write.Rule["name"], _ = json.Marshal(name)
	write.Rule["state"], _ = json.Marshal(state)
	write.Rule["authorAccountId"], _ = json.Marshal(authorID)
	write.Rule["actor"], _ = json.Marshal(actorValue)
	delete(write.Rule, "created")
	delete(write.Rule, "updated")
	if raw := write.Rule["trigger"]; len(raw) > 0 && string(raw) != "null" {
		withIDs, err := ensureComponentIDs(raw)
		if err != nil {
			return nil, fmt.Errorf("rule.trigger is invalid: %w", err)
		}
		write.Rule["trigger"] = withIDs
	}
	if write.Rule["components"] == nil {
		write.Rule["components"] = json.RawMessage(`[]`)
	} else {
		var components []json.RawMessage
		if err := json.Unmarshal(write.Rule["components"], &components); err != nil {
			return nil, fmt.Errorf("rule.components must be an array")
		}
		for index := range components {
			withIDs, itemErr := ensureComponentIDs(components[index])
			if itemErr != nil {
				return nil, fmt.Errorf("rule.components[%d] is invalid: %w", index, itemErr)
			}
			components[index] = withIDs
		}
		write.Rule["components"], _ = json.Marshal(components)
	}
	if write.Rule["labels"] == nil {
		write.Rule["labels"] = json.RawMessage(`[]`)
	}
	if write.Rule["ruleScopeARIs"] == nil {
		write.Rule["ruleScopeARIs"] = json.RawMessage(`[]`)
	}
	if len(write.Connections) == 0 || string(write.Connections) == "null" {
		write.Connections = json.RawMessage(`[]`)
	}
	var connections []json.RawMessage
	if err := json.Unmarshal(write.Connections, &connections); err != nil {
		return nil, fmt.Errorf("connections must be an array")
	}
	payload, err := json.Marshal(write.Rule)
	if err != nil {
		return nil, err
	}
	interval, cronExpression, timezone, query, err := nativeSchedule(payload)
	if err != nil {
		return nil, err
	}
	event, eventJQL, err := nativeEvent(payload)
	if err != nil {
		return nil, err
	}
	if event != "" {
		query = eventJQL
	}
	return &Rule{
		UUID: uuid, AuthorID: authorID, ActorID: actorID, Name: name,
		Description: jsonString(write.Rule["description"]), Labels: jsonStrings(write.Rule["labels"]),
		State: state, RuleScopeARIs: jsonStrings(write.Rule["ruleScopeARIs"]), Payload: payload,
		Connections: write.Connections, IntervalMinutes: interval, ScheduleTimezone: timezone, JQL: query,
		EventTrigger: event, CronExpression: cronExpression,
	}, nil
}

func ensureComponentIDs(raw json.RawMessage) (json.RawMessage, error) {
	var component map[string]any
	if err := json.Unmarshal(raw, &component); err != nil {
		return nil, err
	}
	if value, ok := component["id"].(string); !ok || value == "" {
		id, err := NewUUIDv7()
		if err != nil {
			return nil, err
		}
		component["id"] = id
	}
	for _, key := range []string{"children", "conditions"} {
		items, ok := component[key].([]any)
		if !ok {
			continue
		}
		for index := range items {
			itemRaw, err := json.Marshal(items[index])
			if err != nil {
				return nil, err
			}
			itemRaw, err = ensureComponentIDs(itemRaw)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(itemRaw, &items[index]); err != nil {
				return nil, err
			}
		}
		component[key] = items
	}
	return json.Marshal(component)
}

// nativeSchedule reads a scheduled trigger: a fixed interval in minutes or a
// Quartz cron expression, its timezone and its JQL.
func nativeSchedule(payload json.RawMessage) (*int, string, string, string, error) {
	var rule map[string]json.RawMessage
	if err := json.Unmarshal(payload, &rule); err != nil {
		return nil, "", "UTC", "", err
	}
	var trigger struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(rule["trigger"], &trigger); err != nil || trigger.Type == "" {
		return nil, "", "UTC", "", nil
	}
	if trigger.Type != "jira.issue.scheduled" && trigger.Type != "jira.jql.scheduled" {
		return nil, "", "UTC", "", nil
	}
	var value nativeTriggerValue
	if err := json.Unmarshal(decodeComponentValue(trigger.Value), &value); err != nil {
		return nil, "", "UTC", "", fmt.Errorf("scheduled trigger value must be an object")
	}
	expression := strings.TrimSpace(value.CronExpression)
	if value.Schedule != nil {
		if value.IntervalMinutes == 0 {
			value.IntervalMinutes = value.Schedule.IntervalMinutes
		}
		if value.Timezone == "" {
			value.Timezone = value.Schedule.Timezone
		}
		if expression == "" && (value.Schedule.Method == "" || value.Schedule.Method == "CRON_EXPRESSION") {
			expression = strings.TrimSpace(value.Schedule.CronExpression)
		}
	}
	if value.Timezone == "" {
		value.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return nil, "", "UTC", "", fmt.Errorf("scheduled trigger timezone is invalid")
	}
	value.JQL = strings.TrimSpace(value.JQL)
	if _, err := jql.Parse(value.JQL); err != nil {
		return nil, "", "UTC", "", fmt.Errorf("scheduled trigger JQL: %w", err)
	}
	if expression != "" {
		if _, err := cron.Parse(expression); err != nil {
			return nil, "", "UTC", "", fmt.Errorf("scheduled trigger cron expression: %w", err)
		}
		return nil, expression, value.Timezone, value.JQL, nil
	}
	if value.IntervalMinutes < 1 || value.IntervalMinutes > 43200 {
		return nil, "", "UTC", "", fmt.Errorf("scheduled trigger intervalMinutes must be between 1 and 43200")
	}
	return &value.IntervalMinutes, "", value.Timezone, value.JQL, nil
}

// nextCronRun is when a cron rule next runs after the given time, or nil when
// it is not a cron rule or its schedule never fires again.
func nextCronRun(expression, timezone string, after time.Time) *time.Time {
	if expression == "" {
		return nil
	}
	schedule, err := cron.Parse(expression)
	if err != nil {
		return nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	next, ok := schedule.Next(after, location)
	if !ok {
		return nil
	}
	next = next.UTC()
	return &next
}

// EventTriggers are Jira Automation's work item event triggers, by the event
// each starts from.
var EventTriggers = map[string]string{
	"jira.issue.event.trigger:created":      "created",
	"jira.issue.event.trigger:transitioned": "transitioned",
	"jira.issue.field.changed":              "field_changed",
	"jira.issue.event.trigger:commented":    "commented",
	"jira.issue.event.trigger:linked":       "linked",
}

// eventTriggerValue is what an event trigger narrows its events to: work
// matching JQL, transitions from or to statuses, and changes to fields.
type eventTriggerValue struct {
	JQL           string   `json:"jql"`
	FromStatusIDs []string `json:"fromStatusIds"`
	ToStatusIDs   []string `json:"toStatusIds"`
	Fields        []string `json:"fields"`
}

func decodeComponentValue(raw json.RawMessage) json.RawMessage {
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil && strings.HasPrefix(strings.TrimSpace(encoded), "{") {
		return json.RawMessage(encoded)
	}
	return raw
}

// nativeEvent reads a rule's work item event trigger and its JQL.
func nativeEvent(payload json.RawMessage) (string, string, error) {
	var rule struct {
		Trigger struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"trigger"`
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return "", "", err
	}
	event, ok := EventTriggers[rule.Trigger.Type]
	if !ok {
		return "", "", nil
	}
	value := eventTriggerValue{}
	if raw := decodeComponentValue(rule.Trigger.Value); len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", "", fmt.Errorf("%s trigger value must be an object", rule.Trigger.Type)
		}
	}
	value.JQL = strings.TrimSpace(value.JQL)
	if _, err := jql.Parse(value.JQL); err != nil {
		return "", "", fmt.Errorf("%s trigger JQL: %w", rule.Trigger.Type, err)
	}
	if event == "field_changed" && (len(value.Fields) == 0 || len(value.Fields) > 20) {
		return "", "", fmt.Errorf("field value changed trigger needs between 1 and 20 fields")
	}
	return event, value.JQL, nil
}

func triggerType(payload json.RawMessage) string {
	var rule struct {
		Trigger struct {
			Type string `json:"type"`
		} `json:"trigger"`
	}
	_ = json.Unmarshal(payload, &rule)
	return rule.Trigger.Type
}

func validState(state string) bool {
	return state == "ENABLED" || state == "DISABLED"
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func jsonStrings(raw json.RawMessage) []string {
	var value []string
	_ = json.Unmarshal(raw, &value)
	if value == nil {
		return []string{}
	}
	return value
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

const ruleSelect = `SELECT uuid::text,workspace_id,author_id,actor_id,name,description,labels,state,rule_scope_aris,
 payload,connections,interval_minutes,schedule_timezone,jql,next_run_at,consecutive_failures,created_at,updated_at,COALESCE(event_trigger,''),COALESCE(cron_expression,'') FROM automation_rules`

type rowScanner interface{ Scan(...any) error }

func scanRule(row rowScanner) (*Rule, error) {
	rule := &Rule{}
	err := row.Scan(&rule.UUID, &rule.WorkspaceID, &rule.AuthorID, &rule.ActorID, &rule.Name, &rule.Description,
		&rule.Labels, &rule.State, &rule.RuleScopeARIs, &rule.Payload, &rule.Connections, &rule.IntervalMinutes,
		&rule.ScheduleTimezone, &rule.JQL, &rule.NextRunAt, &rule.ConsecutiveFailures, &rule.CreatedAt, &rule.UpdatedAt, &rule.EventTrigger, &rule.CronExpression)
	return rule, err
}
